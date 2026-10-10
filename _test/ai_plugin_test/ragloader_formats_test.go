// ragloader_formats_test.go 重格式四件黑盒测试：docx/pptx（测试内自造 zip+XML
// 夹具）、xlsx（excelize 生成夹具）、pdf（手写最小 PDF——文本层抽取）。
package ai_plugin_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	ragloader "github.com/crazy-airhead/aifei-go/plugins/ai/ragloader"
)

// zipOf 测试夹具：map{路径→内容} → zip 字节。
func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, content)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func docxOf(paragraphs ...string) string {
	var body strings.Builder
	for _, p := range paragraphs {
		body.WriteString(`<w:p><w:r><w:t>` + p + `</w:t></w:r></w:p>`)
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>` + body.String() + `</w:body></w:document>`
}

// ---- docx ----

func TestRagloaderWord(t *testing.T) {
	data := zipOf(t, map[string]string{
		"[Content_Types].xml":      `<Types/>`,
		"word/document.xml":        docxOf("第一段文字。", "第二段：含 &lt;特殊&gt; 与 &amp; 符号。", "Third paragraph."),
		"word/_rels/document.xml.rels": `<Relationships/>`,
	})
	l := ragloader.NewWordLoader(bytes.NewReader(data))

	// 缺省 PARAGRAPH 模式
	docs, err := l.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 3 {
		t.Fatalf("按段切分应 3 文档，得 %d", len(docs))
	}
	if docs[0].Content != "第一段文字。" {
		t.Fatalf("段落 0：%q", docs[0].Content)
	}
	if docs[1].Content != "第二段：含 <特殊> 与 & 符号。" {
		t.Fatalf("XML 转义应还原：%q", docs[1].Content)
	}
	if docs[0].Metadata["type"] != "word" {
		t.Fatalf("元数据：%v", docs[0].Metadata)
	}

	// SINGLE 模式
	docs, err = ragloader.NewWordLoader(bytes.NewReader(data)).
		LoadMode(ragloader.WordSingle).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || strings.Count(docs[0].Content, "\n") != 2 {
		t.Fatalf("整文档单文档（\\n 连接）：%q", docs[0].Content)
	}

	// 空 docx（无段落）
	empty := zipOf(t, map[string]string{"word/document.xml": docxOf()})
	if docs, err := ragloader.NewWordLoader(bytes.NewReader(empty)).Load(context.Background()); err != nil || len(docs) != 0 {
		t.Fatalf("空文档：%v %d", err, len(docs))
	}

	// 非 zip（.doc 旧格式）报不支持
	if _, err := ragloader.NewWordLoader(strings.NewReader("OLE2 binary...")).Load(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "旧格式不支持") {
		t.Fatalf("旧格式错误面：%v", err)
	}

	// 文件源
	dir := t.TempDir()
	path := filepath.Join(dir, "a.docx")
	osWrite(t, path, data)
	if docs, err := ragloader.NewWordLoaderFromFile(path).Load(context.Background()); err != nil || len(docs) != 3 {
		t.Fatalf("文件源：%v %d", err, len(docs))
	}
}

// ---- pptx ----

func TestRagloaderPpt(t *testing.T) {
	slide := func(texts ...string) string {
		var body strings.Builder
		for _, text := range texts {
			body.WriteString(`<a:p><a:r><a:t>` + text + `</a:t></a:r></a:p>`)
		}
		return `<?xml version="1.0"?><p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:cSld><p:spTree>` + body.String() + `</p:spTree></p:cSld></p:sld>`
	}
	data := zipOf(t, map[string]string{
		"ppt/slides/slide1.xml": slide("封面标题", "副标题"),
		"ppt/slides/slide2.xml": slide("第二页内容", "要点一", "要点二"),
		"ppt/slides/slide10.xml": slide("第十页（排序验证）"),
	})

	// 缺省 SINGLE
	docs, err := ragloader.NewPptLoader(bytes.NewReader(data)).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("整文档应 1 文档：%d", len(docs))
	}
	// 数字序（slide2 在 slide10 前）
	idx2 := strings.Index(docs[0].Content, "第二页内容")
	idx10 := strings.Index(docs[0].Content, "第十页")
	if idx2 < 0 || idx10 < 0 || idx2 > idx10 {
		t.Fatalf("页序（数字序）：%q", docs[0].Content)
	}
	if !strings.Contains(docs[0].Content, "\n\n\n") {
		t.Fatal("缺省页分隔符三换行")
	}

	// PAGE 模式：每页一文档 + page/total_pages 元数据
	docs, err = ragloader.NewPptLoader(bytes.NewReader(data)).
		LoadMode(ragloader.PptPage).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 3 {
		t.Fatalf("每页一文档应 3：%d", len(docs))
	}
	if docs[0].Metadata["page"] != 1 || docs[2].Metadata["page"] != 3 || docs[2].Metadata["total_pages"] != 3 {
		t.Fatalf("页元数据：%v", docs[2].Metadata)
	}
	if !strings.Contains(docs[0].Content, "封面标题") {
		t.Fatalf("页内容：%q", docs[0].Content)
	}

	// 非 zip 报不支持
	if _, err := ragloader.NewPptLoader(strings.NewReader("old ppt")).Load(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "旧格式不支持") {
		t.Fatalf("旧格式错误面：%v", err)
	}
}

// ---- xlsx ----

func TestRagloaderExcel(t *testing.T) {
	// excelize 造夹具
	f := excelize.NewFile()
	sheet1 := f.GetSheetName(0)
	f.SetSheetRow(sheet1, "A1", &[]any{"名称", "数量", "备注"})
	f.SetSheetRow(sheet1, "A2", &[]any{"苹果", 3, "新鲜"})
	f.SetSheetRow(sheet1, "A3", &[]any{"香蕉", 5, ""})
	f.SetSheetRow(sheet1, "A4", &[]any{"橙子", 7, "酸甜"})
	f.NewSheet("Sheet2")
	f.SetSheetRow("Sheet2", "A1", &[]any{"k", "v"})
	f.SetSheetRow("Sheet2", "A2", &[]any{"a", 1})
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()

	l := ragloader.NewExcelLoader(bytes.NewReader(data))
	docs, err := l.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 { // sheet1 3 行（≤200 一批）+ sheet2 1 行
		t.Fatalf("批文档数：%d", len(docs))
	}
	// sheet1 首批：3 行 map JSON
	var rows []map[string]any
	if err := json.Unmarshal([]byte(docs[0].Content), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0]["名称"] != "苹果" || rows[2]["备注"] != "酸甜" {
		t.Fatalf("行映射：%v", rows)
	}
	if docs[0].Metadata["sheet"] != sheet1 || docs[0].Metadata["type"] != "excel" {
		t.Fatalf("元数据：%v", docs[0].Metadata)
	}
	// 数值经 excelize GetRows 为字符串形态
	if rows[0]["数量"] != "3" && rows[0]["数量"] != 3.0 {
		t.Fatalf("数值单元格：%v", rows[0]["数量"])
	}

	// 分批：maxRows=2 → sheet1 2 批（2+1）
	docs, err = ragloader.NewExcelLoader(bytes.NewReader(data)).
		DocumentMaxRows(2).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 3 { // 2+1+1
		t.Fatalf("分批文档数：%d", len(docs))
	}

	// 不分批：-1
	docs, _ = ragloader.NewExcelLoader(bytes.NewReader(data)).
		DocumentMaxRows(-1).Load(context.Background())
	if len(docs) != 2 {
		t.Fatalf("不分批每 sheet 一文档：%d", len(docs))
	}

	// 空行终止：首行标题后空行即停
	f2 := excelize.NewFile()
	s := f2.GetSheetName(0)
	f2.SetSheetRow(s, "A1", &[]any{"h1", "h2"})
	f2.SetSheetRow(s, "A2", &[]any{"v1", "v2"})
	f2.SetSheetRow(s, "A4", &[]any{"v3", "v4"}) // A3 空行
	var buf2 bytes.Buffer
	f2.Write(&buf2)
	if docs, _ := ragloader.NewExcelLoader(bytes.NewReader(buf2.Bytes())).Load(context.Background()); len(docs) != 1 {
		t.Fatalf("空行终止：%d", len(docs))
	}
}

// ---- pdf（手写最小 PDF 夹具）----

func minimalPDF(lines ...string) []byte {
	var objs []string
	// 1=catalog 2=pages 3=page 4=content 5=font
	content := "BT /F1 12 Tf 72 720 Td 14 TL\n"
	for _, line := range lines {
		content += fmt.Sprintf("(%s) Tj T*\n", line)
	}
	content += "ET"
	objs = append(objs,
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	)
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for i, obj := range objs {
		offsets = append(offsets, buf.Len())
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets[1:] {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return buf.Bytes()
}

func TestRagloaderPdf(t *testing.T) {
	data := minimalPDF("Hello PDF World", "Second line of text")

	// 缺省 PAGE 模式
	docs, err := ragloader.NewPdfLoader(bytes.NewReader(data)).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("单页 PDF 应 1 文档：%d", len(docs))
	}
	if !strings.Contains(docs[0].Content, "Hello PDF World") || !strings.Contains(docs[0].Content, "Second line") {
		t.Fatalf("文本抽取：%q", docs[0].Content)
	}
	if docs[0].Metadata["page"] != 1 || docs[0].Metadata["total_pages"] != 1 ||
		docs[0].Metadata["summary"] != "Page 1" || docs[0].Metadata["type"] != "pdf" {
		t.Fatalf("页元数据：%v", docs[0].Metadata)
	}

	// SINGLE 模式 + pages 元数据
	docs, err = ragloader.NewPdfLoader(bytes.NewReader(data)).
		LoadMode(ragloader.PdfSingle).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].Metadata["pages"] != 1 {
		t.Fatalf("整文档：%v", docs[0].Metadata)
	}

	// 非法 PDF 报错
	if _, err := ragloader.NewPdfLoader(strings.NewReader("not a pdf")).Load(context.Background()); err == nil {
		t.Fatal("非法 PDF 应报错")
	}
}

func osWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
