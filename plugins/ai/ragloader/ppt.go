// ppt.go PPT 文档加载器（pptx 经 markitdown 统一引擎转 Markdown；页边界由
// 引擎输出的 `<!-- Slide number: N -->` 标记提供——真页切分）。.ppt（OLE2 旧
// 格式）显式不支持（00 §2.2-I 降级声明维持）。
package ragloader

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai/rag"
)

// PptLoadMode ppt 加载模式。
type PptLoadMode uint8

const (
	// PptSingle 整文档一个（缺省）：页标记剥除、页以分隔符连接。
	PptSingle PptLoadMode = iota
	// PptPage 每页一个（真页——引擎页标记切分）。
	PptPage
)

// DefaultPptPageDelimiter SINGLE 模式页分隔符缺省（三换行——历史缺省保持）。
const DefaultPptPageDelimiter = "\n\n\n"

// PptLoader pptx 加载器（markitdown 引擎）。
type PptLoader struct {
	open          func() (io.Reader, error)
	filename      string
	loadMode      PptLoadMode
	pageDelimiter string
	meta          map[string]any
}

// NewPptLoader 从 io.Reader 构造（缺省按 .pptx 路由）。
func NewPptLoader(r io.Reader) *PptLoader {
	return NewPptLoaderNamed(r, "presentation.pptx")
}

// NewPptLoaderNamed 从 io.Reader + 文件名构造。
func NewPptLoaderNamed(r io.Reader, filename string) *PptLoader {
	return &PptLoader{open: func() (io.Reader, error) { return r, nil },
		filename: filename, pageDelimiter: DefaultPptPageDelimiter,
		meta: map[string]any{"type": "ppt"}}
}

// NewPptLoaderFromFile 从文件路径构造。
func NewPptLoaderFromFile(name string) *PptLoader {
	return &PptLoader{open: func() (io.Reader, error) { return os.Open(name) },
		filename: name, pageDelimiter: DefaultPptPageDelimiter,
		meta: map[string]any{"type": "ppt"}}
}

// LoadMode 加载模式。
func (l *PptLoader) LoadMode(m PptLoadMode) *PptLoader { l.loadMode = m; return l }

// PageDelimiter 页分隔符（SINGLE 模式连接用）。
func (l *PptLoader) PageDelimiter(d string) *PptLoader {
	if d != "" {
		l.pageDelimiter = d
	}
	return l
}

// AdditionalMetadata 附加元数据。
func (l *PptLoader) AdditionalMetadata(k string, v any) rag.Loader {
	l.meta[k] = v
	return l
}

// Load markitdown 转换 + 页切分。
func (l *PptLoader) Load(ctx context.Context) ([]*rag.Document, error) {
	data, err := readAll(l.open)
	if err != nil {
		return nil, err
	}
	if ext := strings.ToLower(filepath.Ext(l.filename)); ext == ".ppt" {
		return nil, &loaderError{"ragloader: .ppt 旧格式不支持（markitdown 仅覆盖 OOXML pptx）"}
	}
	markdown, _, err := convertToMarkdown(ctx, data, l.filename)
	if err != nil {
		return nil, err
	}
	slides := splitMarkdownSlides(markdown)
	if l.loadMode == PptPage {
		return segmentsToDocs(slides, l.meta, 0, true), nil
	}
	// SINGLE：剥除页标记，页以分隔符连接
	bodies := make([]string, len(slides))
	for i, seg := range slides {
		bodies[i] = seg.Body
	}
	return []*rag.Document{
		rag.NewDocument(strings.Join(bodies, l.pageDelimiter)).MetaMap(l.meta),
	}, nil
}

var _ rag.Loader = (*PptLoader)(nil)
