// word.go Word 文档加载器（docx 经 markitdown 统一引擎转 Markdown；保留
// SINGLE/PARAGRAPH 双模式——PARAGRAPH 按 Markdown 空行分段）。.doc（OLE2 旧
// 格式）markitdown 无对应转换器且会落入纯文本兜底产出乱码——显式不支持
//（00 §2.2-I 降级声明维持）。
package ragloader

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai/rag"
)

// WordLoadMode word 加载模式。
type WordLoadMode uint8

const (
	// WordParagraph 每个 Markdown 段落一个文档（缺省）。
	WordParagraph WordLoadMode = iota
	// WordSingle 整文档一个。
	WordSingle
)

// WordLoader docx 加载器（markitdown 引擎）。
type WordLoader struct {
	open     func() (io.Reader, error)
	filename string
	loadMode WordLoadMode
	meta     map[string]any
}

// NewWordLoader 从 io.Reader 构造（缺省按 .docx 路由）。
func NewWordLoader(r io.Reader) *WordLoader {
	return NewWordLoaderNamed(r, "document.docx")
}

// NewWordLoaderNamed 从 io.Reader + 文件名构造（扩展名驱动 markitdown 路由）。
func NewWordLoaderNamed(r io.Reader, filename string) *WordLoader {
	return &WordLoader{open: func() (io.Reader, error) { return r, nil },
		filename: filename, meta: map[string]any{"type": "word"}}
}

// NewWordLoaderFromFile 从文件路径构造。
func NewWordLoaderFromFile(name string) *WordLoader {
	return &WordLoader{open: func() (io.Reader, error) { return os.Open(name) },
		filename: name, meta: map[string]any{"type": "word"}}
}

// LoadMode 加载模式。
func (l *WordLoader) LoadMode(m WordLoadMode) *WordLoader { l.loadMode = m; return l }

// AdditionalMetadata 附加元数据。
func (l *WordLoader) AdditionalMetadata(k string, v any) rag.Loader {
	l.meta[k] = v
	return l
}

// Load markitdown 转换 + 模式切分。
func (l *WordLoader) Load(ctx context.Context) ([]*rag.Document, error) {
	data, err := readAll(l.open)
	if err != nil {
		return nil, err
	}
	if ext := strings.ToLower(filepath.Ext(l.filename)); ext == ".doc" {
		return nil, &loaderError{"ragloader: .doc 旧格式不支持（markitdown 仅覆盖 OOXML docx）"}
	}
	markdown, _, err := convertToMarkdown(ctx, data, l.filename)
	if err != nil {
		return nil, err
	}
	if l.loadMode == WordSingle {
		return []*rag.Document{rag.NewDocument(markdown).MetaMap(l.meta)}, nil
	}
	return segmentsToDocs(splitMarkdownParagraphs(markdown), l.meta, 0, false), nil
}

var _ rag.Loader = (*WordLoader)(nil)
