// pdf.go PDF 文档加载器（对标 PdfLoader，ledongthuc/pdf 纯 Go 文本层抽取）：
// SINGLE（页分隔符拼接 + pages 元数据）/PAGE（每页一文档，page/total_pages
// 元数据 + summary）双模式（缺省 PAGE——Java 同款）。扫描件 OCR 明确不支持
//（05 §7 降级声明）。
package ragloader

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	pdfapi "github.com/ledongthuc/pdf"

	"github.com/crazy-airhead/aifei-go/ai/rag"
)

// PdfLoadMode pdf 加载模式。
type PdfLoadMode uint8

const (
	// PdfPage 每页一个文档（缺省——Java 同款）。
	PdfPage PdfLoadMode = iota
	// PdfSingle 整文档一个。
	PdfSingle
)

// DefaultPdfPageDelimiter SINGLE 模式页分隔符（Java pageDelimiter 缺省 "\n\n"）。
const DefaultPdfPageDelimiter = "\n\n"

// PdfLoader pdf 加载器。
type PdfLoader struct {
	open          func() (io.Reader, error)
	loadMode      PdfLoadMode
	pageDelimiter string
	meta          map[string]any
}

// NewPdfLoader 从 io.Reader 构造。
func NewPdfLoader(r io.Reader) *PdfLoader {
	return &PdfLoader{open: func() (io.Reader, error) { return r, nil },
		pageDelimiter: DefaultPdfPageDelimiter, meta: map[string]any{"type": "pdf"}}
}

// NewPdfLoaderFromFile 从文件路径构造。
func NewPdfLoaderFromFile(name string) *PdfLoader {
	return &PdfLoader{open: func() (io.Reader, error) { return os.Open(name) },
		pageDelimiter: DefaultPdfPageDelimiter, meta: map[string]any{"type": "pdf"}}
}

// LoadMode 加载模式。
func (l *PdfLoader) LoadMode(m PdfLoadMode) *PdfLoader { l.loadMode = m; return l }

// PageDelimiter 页分隔符（SINGLE 模式）。
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

// Load 抽取文本层。
func (l *PdfLoader) Load(ctx context.Context) ([]*rag.Document, error) {
	r, err := l.open()
	if err != nil {
		return nil, err
	}
	if c, ok := r.(io.Closer); ok {
		defer c.Close()
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	reader, err := pdfapi.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("ragloader: pdf open: %w", err)
	}
	total := reader.NumPage()

	if l.loadMode == PdfSingle {
		var pages []string
		for i := 1; i <= total; i++ {
			text, err := pdfPageText(reader, i)
			if err != nil {
				return nil, err
			}
			pages = append(pages, text)
		}
		return []*rag.Document{
			rag.NewDocument(strings.Join(pages, l.pageDelimiter)).
				Meta("pages", total).MetaMap(l.meta),
		}, nil
	}

	var docs []*rag.Document
	for i := 1; i <= total; i++ {
		text, err := pdfPageText(reader, i)
		if err != nil {
			return nil, err
		}
		docs = append(docs, rag.NewDocument(strings.TrimSpace(text)).
			Meta("page", i).Meta("total_pages", total).
			Meta("summary", fmt.Sprintf("Page %d", i)).
			MetaMap(l.meta))
	}
	return docs, nil
}

// pdfPageText 单页文本（Content().Text 同句合并——IsSameSentence 的字体/字号/
// 基线判定；行间换行。PDFTextStripper 的近似面）。
func pdfPageText(reader *pdfapi.Reader, pageNum int) (string, error) {
	page := reader.Page(pageNum)
	if page.V.IsNull() {
		return "", fmt.Errorf("ragloader: pdf page %d missing", pageNum)
	}
	texts := page.Content().Text
	var sb strings.Builder
	lastY := -1.0
	for _, t := range texts {
		if t.S == "" {
			continue
		}
		if lastY >= 0 && abs64(t.Y-lastY) >= 5 {
			sb.WriteString("\n")
		}
		sb.WriteString(t.S)
		lastY = t.Y
	}
	return sb.String(), nil
}

func abs64(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

var _ rag.Loader = (*PdfLoader)(nil)
