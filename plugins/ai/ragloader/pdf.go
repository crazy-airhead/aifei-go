// pdf.go PDF 文档加载器（pdf 经 markitdown 统一引擎——pdfium WebAssembly
// 纯 Go 运行，wazero 承载，零外部二进制；输出结构化 Markdown：字号驱动标题
// 层级/加粗/脚注过滤）。PAGE 模式说明：统一引擎不暴露页边界（页间与页内均
// 以空行分隔），PAGE 退化为按 Markdown 段落切块——page 元数据为块序（近似
// 页序，pdf_parse 工具与分块检索的粒度不受影响）。扫描件 OCR 不支持维持
//（05 §7 降级声明）。
package ragloader

import (
	"context"
	"io"
	"os"

	"github.com/crazy-airhead/aifei-go/ai/rag"
)

// PdfLoadMode pdf 加载模式。
type PdfLoadMode uint8

const (
	// PdfPage 每段一个文档（缺省——按 Markdown 空行分段的近似页切分）。
	PdfPage PdfLoadMode = iota
	// PdfSingle 整文档一个。
	PdfSingle
)

// DefaultPdfPageDelimiter SINGLE 模式段分隔符。
const DefaultPdfPageDelimiter = "\n\n"

// PdfLoader pdf 加载器（markitdown 引擎）。
type PdfLoader struct {
	open          func() (io.Reader, error)
	filename      string
	loadMode      PdfLoadMode
	pageDelimiter string
	meta          map[string]any
}

// NewPdfLoader 从 io.Reader 构造（缺省按 .pdf 路由）。
func NewPdfLoader(r io.Reader) *PdfLoader {
	return NewPdfLoaderNamed(r, "document.pdf")
}

// NewPdfLoaderNamed 从 io.Reader + 文件名构造。
func NewPdfLoaderNamed(r io.Reader, filename string) *PdfLoader {
	return &PdfLoader{open: func() (io.Reader, error) { return r, nil },
		filename: filename, pageDelimiter: DefaultPdfPageDelimiter,
		meta: map[string]any{"type": "pdf"}}
}

// NewPdfLoaderFromFile 从文件路径构造。
func NewPdfLoaderFromFile(name string) *PdfLoader {
	return &PdfLoader{open: func() (io.Reader, error) { return os.Open(name) },
		filename: name, pageDelimiter: DefaultPdfPageDelimiter,
		meta: map[string]any{"type": "pdf"}}
}

// LoadMode 加载模式。
func (l *PdfLoader) LoadMode(m PdfLoadMode) *PdfLoader { l.loadMode = m; return l }

// PageDelimiter 段分隔符（SINGLE 模式）。
func (l *PdfLoader) PageDelimiter(d string) *PdfLoader {
	if d != "" {
		l.pageDelimiter = d
	}
	return l
}

// AdditionalMetadata 附加元数据。
func (l *PdfLoader) AdditionalMetadata(k string, v any) rag.Loader {
	l.meta[k] = v
	return l
}

// Load markitdown 转换 + 模式切分。
func (l *PdfLoader) Load(ctx context.Context) ([]*rag.Document, error) {
	data, err := readAll(l.open)
	if err != nil {
		return nil, err
	}
	markdown, _, err := convertToMarkdown(ctx, data, l.filename)
	if err != nil {
		return nil, err
	}
	// 引擎对无文本 PDF 返回占位文案——保持为正文透传（分块检索语义不变）
	if l.loadMode == PdfSingle {
		return []*rag.Document{
			rag.NewDocument(markdown).Meta("pages", 1).MetaMap(l.meta),
		}, nil
	}
	docs := segmentsToDocs(splitMarkdownParagraphs(markdown), l.meta, 0, true)
	// 段级 summary 语义保留（块序近似页序）
	for _, doc := range docs {
		if p, _ := doc.Metadata["page"].(int); p > 0 {
			doc.Meta("summary", "Page "+itoaLocal(p))
		}
	}
	return docs, nil
}

func itoaLocal(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

var _ rag.Loader = (*PdfLoader)(nil)
