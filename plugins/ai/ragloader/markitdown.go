// markitdown.go 统一重格式处理核心（经 github.com/conductor-oss/markitdown
// 引擎：docx/xlsx/pptx/pdf/xls/html/csv/epub/rss/ipynb → Markdown）。四个类型化
// 加载器（Word/Ppt/Excel/Pdf）保留各自的模式语义与元数据，解析统一委托本核心：
//   - pptx 输出带 `<!-- Slide number: N -->` 页标记 → 真页切分
//   - xlsx 输出带 `## SheetName` 标题 → 按 sheet 切分
//   - docx/pdf 输出标准 Markdown（空行分段）
// 引擎单例共享（markitdown 实例无请求态；pdfium 池全局 sync.Once 初始化，
// wazero 纯 Go 运行 pdfium.wasm——零外部二进制）。
package ragloader

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"

	md "github.com/conductor-oss/markitdown"

	"github.com/crazy-airhead/aifei-go/ai/rag"
)

var (
	mdEngineOnce sync.Once
	mdEngine     *md.MarkItDown
)

// readAll 统一读取（构造期零 IO；错误面收口在 Load——与既有加载器一致；
// 入参为打开函数——加载器的 open 字段形态）。
func readAll(open func() (io.Reader, error)) ([]byte, error) {
	r, err := open()
	if err != nil {
		return nil, err
	}
	if c, ok := r.(io.Closer); ok {
		defer c.Close()
	}
	return io.ReadAll(r)
}

// engine 惰性单例（首次调用触发 pdfium 池懒初始化）。
func engine() *md.MarkItDown {
	mdEngineOnce.Do(func() {
		mdEngine = md.New()
	})
	return mdEngine
}

// convertToMarkdown 统一转换：filename 提供扩展名路由（docx/xlsx/pptx/pdf/
// xls/html/csv/…），返回完整 Markdown 与小写扩展名。
func convertToMarkdown(ctx context.Context, data []byte, filename string) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	ext := strings.ToLower(filepath.Ext(filename))
	result, err := engine().ConvertReader(bytes.NewReader(data), md.StreamInfo{
		Filename: filename,
		Extension: ext,
	})
	if err != nil {
		return "", ext, fmt.Errorf("ragloader: markitdown convert %s: %w", filename, err)
	}
	return result.Markdown, ext, nil
}

// mdSegment 切分段（正文 + 序号定位）。
type mdSegment struct {
	Body  string
	Label string // slide N / sheet 名 / 空串（段落）
}

// splitMarkdownSlides 按页标记切分（`<!-- Slide number: N -->`——pptx 输出）。
func splitMarkdownSlides(markdown string) []mdSegment {
	var segments []mdSegment
	remaining := markdown
	for {
		idx := strings.Index(remaining, "<!-- Slide number:")
		if idx < 0 {
			break
		}
		// 标记行结束
		lineEnd := strings.Index(remaining[idx:], "-->")
		if lineEnd < 0 {
			break
		}
		label := strings.TrimSpace(remaining[idx : idx+lineEnd+3])
		rest := remaining[idx+lineEnd+3:]
		// 到下一标记为止
		next := strings.Index(rest, "<!-- Slide number:")
		var body string
		if next >= 0 {
			body = rest[:next]
			remaining = rest[next:]
		} else {
			body = rest
			remaining = ""
		}
		segments = append(segments, mdSegment{Body: strings.TrimSpace(body), Label: label})
		if next < 0 {
			break
		}
	}
	// 无标记形态（非 pptx 或空）——整段兜底
	if len(segments) == 0 {
		if s := strings.TrimSpace(markdown); s != "" {
			return []mdSegment{{Body: s}}
		}
		return nil
	}
	return segments
}

// splitMarkdownSheets 按二级标题切分（`## Name` 行——xlsx 输出；标题行并入段）。
func splitMarkdownSheets(markdown string) []mdSegment {
	var segments []mdSegment
	var current strings.Builder
	currentLabel := ""
	flush := func() {
		if s := strings.TrimSpace(current.String()); s != "" {
			segments = append(segments, mdSegment{Body: s, Label: currentLabel})
		}
		current.Reset()
	}
	for _, line := range strings.Split(markdown, "\n") {
		if strings.HasPrefix(line, "## ") {
			flush()
			currentLabel = strings.TrimSpace(strings.TrimPrefix(line, "## "))
		}
		current.WriteString(line)
		current.WriteString("\n")
	}
	flush()
	return segments
}

// splitMarkdownParagraphs 按空行分段（docx/pdf 输出；空段跳过）。
func splitMarkdownParagraphs(markdown string) []mdSegment {
	var segments []mdSegment
	for _, para := range strings.Split(markdown, "\n\n") {
		if s := strings.TrimSpace(para); s != "" {
			segments = append(segments, mdSegment{Body: s})
		}
	}
	return segments
}

// batchMarkdownLines 按非空行数分批（documentMaxRows 语义的 Markdown 对应物：
// 表格行/正文行统一按行计）。
func batchMarkdownLines(body string, maxLines int) []string {
	if maxLines <= 0 {
		return []string{body}
	}
	lines := strings.Split(body, "\n")
	var out []string
	var batch []string
	count := 0
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			count++
		}
		batch = append(batch, line)
		if count >= maxLines {
			out = append(out, strings.Join(batch, "\n"))
			batch, count = nil, 0
		}
	}
	if len(batch) > 0 {
		out = append(out, strings.Join(batch, "\n"))
	}
	return out
}

// segmentsToDocs 段 → 文档（附加元数据 + 可选行分批）。
func segmentsToDocs(segments []mdSegment, meta map[string]any, maxLines int, pageMeta bool) []*rag.Document {
	var docs []*rag.Document
	page := 0
	for _, seg := range segments {
		for _, chunk := range batchMarkdownLines(seg.Body, maxLines) {
			if strings.TrimSpace(chunk) == "" {
				continue
			}
			page++
			doc := rag.NewDocument(chunk).MetaMap(meta)
			if seg.Label != "" {
				doc.Meta("sheet", seg.Label)
			}
			if pageMeta {
				doc.Meta("page", page).Meta("total_pages", -1) // 近似页序（见各加载器说明）
			}
			docs = append(docs, doc)
		}
	}
	// 回填 total_pages
	if pageMeta {
		for _, doc := range docs {
			if v, _ := doc.Metadata["total_pages"].(int); v == -1 {
				doc.Metadata["total_pages"] = page
			}
		}
	}
	return docs
}

// loaderError 加载器错误（统一错误面）。
type loaderError struct{ msg string }

func (e *loaderError) Error() string { return e.msg }
