// ragloader_formats_test.go 重格式四件黑盒测试（markitdown 统一引擎）：
// 夹具为 conductor-oss/markitdown v0.0.1 testdata 的真实文件（Apache-2.0，
// 见 testdata/LICENSE）——docx 段落/整档双模式、pptx 真页标记切分、xlsx
// sheet 切分 + 行分批、pdf 段块近似页切分；旧格式拒绝与非法输入错误面。
package ai_plugin_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ragloader "github.com/crazy-airhead/aifei-go/plugins/ai/ragloader"
)

// fixture 读取 markitdown testdata 夹具。
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeFixture(t *testing.T, name string, data []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// minimalPDF 手写最小 PDF（pdf talent 测试共用——markitdown/pdfium 亦可解析）。
func minimalPDF(lines ...string) []byte {
	var objs []string
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
	var buf strings.Builder
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
	return []byte(buf.String())
}

// ---- docx ----

func TestRagloaderWord(t *testing.T) {
	data := fixture(t, "test.docx")

	// 缺省 PARAGRAPH：按 Markdown 空行分段（真实文档多段）
	docs, err := ragloader.NewWordLoaderNamed(strings.NewReader(string(data)), "test.docx").
		Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) < 5 {
		t.Fatalf("真实 docx 应多段（≥5），得 %d", len(docs))
	}
	for i, doc := range docs {
		if strings.TrimSpace(doc.Content) == "" {
			t.Fatalf("段 %d 空", i)
		}
		if doc.Metadata["type"] != "word" {
			t.Fatalf("元数据：%v", doc.Metadata)
		}
	}
	// 首段含标题（markitdown docx 输出：标题行为首段）
	if !strings.Contains(docs[0].Content, "AutoGen") {
		t.Fatalf("首段内容：%q", headStr(docs[0].Content, 80))
	}

	// SINGLE：整档单文档
	docs, err = ragloader.NewWordLoaderNamed(strings.NewReader(string(data)), "test.docx").
		LoadMode(ragloader.WordSingle).Load(context.Background())
	if err != nil || len(docs) != 1 {
		t.Fatalf("SINGLE：%d %v", len(docs), err)
	}
	if !strings.Contains(docs[0].Content, "AutoGen") || len(docs[0].Content) < 3000 {
		t.Fatalf("整档内容（%d 字）", len(docs[0].Content))
	}

	// .doc 旧格式拒绝（markitdown 无 OLE2 word 转换器——防纯文本兜底乱码）
	if _, err := ragloader.NewWordLoaderNamed(strings.NewReader("OLE2"), "a.doc").
		Load(context.Background()); err == nil || !strings.Contains(err.Error(), "旧格式不支持") {
		t.Fatalf(".doc 错误面：%v", err)
	}

	// 文件源
	path := writeFixture(t, "a.docx", data)
	if docs, err = ragloader.NewWordLoaderFromFile(path).Load(context.Background()); err != nil || len(docs) < 5 {
		t.Fatalf("文件源：%d %v", len(docs), err)
	}
}

// ---- pptx ----

func TestRagloaderPpt(t *testing.T) {
	data := fixture(t, "test.pptx")

	// PAGE：真页切分（markitdown 页标记——test.pptx 为 6 页）
	docs, err := ragloader.NewPptLoaderNamed(strings.NewReader(string(data)), "test.pptx").
		LoadMode(ragloader.PptPage).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 6 {
		t.Fatalf("真页应 6 文档，得 %d", len(docs))
	}
	for i, doc := range docs {
		if doc.Metadata["page"] != i+1 || doc.Metadata["total_pages"] != 6 || doc.Metadata["type"] != "ppt" {
			t.Fatalf("页 %d 元数据：%v", i+1, doc.Metadata)
		}
	}
	if !strings.Contains(docs[0].Content, "AutoGen") {
		t.Fatalf("首页内容：%q", headStr(docs[0].Content, 80))
	}

	// SINGLE：剥标记 + 分隔符连接（默认三换行）
	docs, err = ragloader.NewPptLoaderNamed(strings.NewReader(string(data)), "test.pptx").
		Load(context.Background())
	if err != nil || len(docs) != 1 {
		t.Fatalf("SINGLE：%d %v", len(docs), err)
	}
	if strings.Contains(docs[0].Content, "Slide number:") {
		t.Fatal("SINGLE 应剥除页标记")
	}
	if !strings.Contains(docs[0].Content, "\n\n\n") {
		t.Fatal("缺省页分隔符三换行")
	}

	// .ppt 旧格式拒绝
	if _, err := ragloader.NewPptLoaderNamed(strings.NewReader("old"), "a.ppt").
		Load(context.Background()); err == nil || !strings.Contains(err.Error(), "旧格式不支持") {
		t.Fatalf(".ppt 错误面：%v", err)
	}
}

// ---- xlsx ----

func TestRagloaderExcel(t *testing.T) {
	data := fixture(t, "test.xlsx")

	// 按 sheet 切分（夹具两 sheet：Sheet1 + UUID 名；各 ≤200 行一文档）
	docs, err := ragloader.NewExcelLoaderNamed(strings.NewReader(string(data)), "test.xlsx").
		Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("两 sheet 应 2 文档，得 %d", len(docs))
	}
	doc := docs[0]
	if doc.Metadata["sheet"] != "Sheet1" || doc.Metadata["type"] != "excel" {
		t.Fatalf("元数据：%v", doc.Metadata)
	}
	if !strings.Contains(doc.Content, "| Alpha |") {
		t.Fatalf("Markdown 表形态：%q", headStr(doc.Content, 120))
	}
	if docs[1].Metadata["sheet"] != "09060124-b5e7-4717-9d07-3c046eb" {
		t.Fatalf("第二 sheet 名：%v", docs[1].Metadata["sheet"])
	}

	// 行分批：maxRows=5 → 多批（Sheet1 约 24 行 → 5 批 + UUID sheet 2 批）
	docs, err = ragloader.NewExcelLoaderNamed(strings.NewReader(string(data)), "test.xlsx").
		DocumentMaxRows(5).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) < 5 {
		t.Fatalf("行分批应多批，得 %d", len(docs))
	}
	// 分批保留 sheet 元数据
	for _, d := range docs {
		if d.Metadata["sheet"] == nil {
			t.Fatalf("分批丢失 sheet 元数据：%v", d.Metadata)
		}
	}

	// 不分批：-1（每 sheet 一文档）
	docs, _ = ragloader.NewExcelLoaderNamed(strings.NewReader(string(data)), "test.xlsx").
		DocumentMaxRows(-1).Load(context.Background())
	if len(docs) != 2 {
		t.Fatalf("不分批每 sheet 一文档：%d", len(docs))
	}
}

// ---- pdf ----

func TestRagloaderPdf(t *testing.T) {
	data := fixture(t, "test.pdf")

	// 缺省 PAGE：Markdown 段块（近似页切分——块序 page 元数据 + summary）
	docs, err := ragloader.NewPdfLoaderNamed(strings.NewReader(string(data)), "test.pdf").
		Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) < 3 {
		t.Fatalf("真实 PDF 应多段块（≥3），得 %d", len(docs))
	}
	if docs[0].Metadata["page"] != 1 || docs[0].Metadata["type"] != "pdf" ||
		docs[0].Metadata["summary"] != "Page 1" {
		t.Fatalf("块元数据：%v", docs[0].Metadata)
	}
	total, _ := docs[0].Metadata["total_pages"].(int)
	if total != len(docs) {
		t.Fatalf("total_pages：%d vs %d", total, len(docs))
	}
	if !strings.Contains(docs[0].Content, "Introduction") {
		t.Fatalf("首块内容：%q", headStr(docs[0].Content, 80))
	}

	// SINGLE：整档（pages 元数据）
	docs, err = ragloader.NewPdfLoaderNamed(strings.NewReader(string(data)), "test.pdf").
		LoadMode(ragloader.PdfSingle).Load(context.Background())
	if err != nil || len(docs) != 1 {
		t.Fatalf("SINGLE：%d %v", len(docs), err)
	}
	if docs[0].Metadata["pages"] != 1 || len(docs[0].Content) < 2000 {
		t.Fatalf("整档（%d 字）：%v", len(docs[0].Content), docs[0].Metadata)
	}

	// 非法 PDF：markitdown/pdfium 报错
	if _, err := ragloader.NewPdfLoaderNamed(strings.NewReader("not a pdf"), "x.pdf").
		Load(context.Background()); err == nil {
		t.Fatal("非法 PDF 应报错")
	}
}

func headStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
