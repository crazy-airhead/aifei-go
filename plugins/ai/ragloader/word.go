// word.go Word 文档加载器（对标 WordLoader 的 OOXML 分支）：docx = zip+XML
// 自解（word/document.xml 的 w:p 段落），SINGLE/PARAGRAPH 双模式（缺省按段）。
// .doc（OLE2 旧格式）不支持——00 §2.2-I 的降级声明（Java HWPF 分支不迁）。
package ragloader

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"os"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai/rag"
)

// WordLoadMode word 加载模式（Java LoadMode）。
type WordLoadMode uint8

const (
	// WordParagraph 每段一个文档（缺省——Java 同款）。
	WordParagraph WordLoadMode = iota
	// WordSingle 整文档一个。
	WordSingle
)

// WordLoader docx 加载器。
type WordLoader struct {
	open     func() (io.Reader, error)
	loadMode WordLoadMode
	meta     map[string]any
}

// NewWordLoader 从 io.Reader 构造。
func NewWordLoader(r io.Reader) *WordLoader {
	return &WordLoader{open: func() (io.Reader, error) { return r, nil }, meta: map[string]any{"type": "word"}}
}

// NewWordLoaderFromFile 从文件路径构造。
func NewWordLoaderFromFile(name string) *WordLoader {
	return &WordLoader{open: func() (io.Reader, error) { return os.Open(name) }, meta: map[string]any{"type": "word"}}
}

// LoadMode 加载模式。
func (l *WordLoader) LoadMode(m WordLoadMode) *WordLoader { l.loadMode = m; return l }

// AdditionalMetadata 附加元数据。
func (l *WordLoader) AdditionalMetadata(k string, v any) rag.Loader {
	l.meta[k] = v
	return l
}

// Load 解析 docx（zip 里 word/document.xml 的 w:p 段落文本）。
func (l *WordLoader) Load(ctx context.Context) ([]*rag.Document, error) {
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
	if !bytes.HasPrefix(data, []byte("PK")) {
		return nil, errUnsupportedWordFormat
	}

	paragraphs, err := docxParagraphs(data)
	if err != nil {
		return nil, err
	}

	if l.loadMode == WordSingle {
		return []*rag.Document{rag.NewDocument(strings.Join(paragraphs, "\n")).MetaMap(l.meta)}, nil
	}
	var docs []*rag.Document
	for _, p := range paragraphs {
		if strings.TrimSpace(p) == "" {
			continue // 空段跳过（Java getText 非空段落语义）
		}
		docs = append(docs, rag.NewDocument(p).MetaMap(l.meta))
	}
	return docs, nil
}

var errUnsupportedWordFormat = &loaderError{"ragloader: 非 docx（zip）格式——.doc 旧格式不支持"}

type loaderError struct{ msg string }

func (e *loaderError) Error() string { return e.msg }

// docxParagraphs 抽 word/document.xml 的段落文本（w:p → 内层 w:t 拼接；段落
// 内 w:br/w:tab 归一空格——XWPFParagraph.getText 的近似）。
func docxParagraphs(data []byte) ([]string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	var docFile *zip.File
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			docFile = f
			break
		}
	}
	if docFile == nil {
		return nil, &loaderError{"ragloader: docx 缺 word/document.xml"}
	}
	rc, err := docFile.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	decoder := xml.NewDecoder(rc)
	var paragraphs []string
	var current strings.Builder
	inText := false
	depthParagraph := false
	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p":
				if !depthParagraph {
					depthParagraph = true
					current.Reset()
				} else {
					// 嵌套段落（表格内）：闭合上一段
					paragraphs = append(paragraphs, current.String())
					current.Reset()
				}
			case "t":
				inText = true
			case "br", "tab":
				current.WriteString(" ")
			}
		case xml.CharData:
			if inText {
				current.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "p":
				if depthParagraph {
					paragraphs = append(paragraphs, current.String())
					current.Reset()
					depthParagraph = false
				}
			case "t":
				inText = false
			}
		}
	}
	return paragraphs, nil
}

var _ rag.Loader = (*WordLoader)(nil)
