// ppt.go PPT 文档加载器（对标 PptLoader 的降级决策面）：pptx = zip+XML 自解
//（ppt/slides/slideN.xml 的 a:p 文本，页序号即文档序），SINGLE/PAGE 双模式
//（缺省 SINGLE——Java 同款）。.ppt（OLE2 旧格式）不支持——00 §2.2-I 降级
// 声明（Java Tika AutoDetectParser 分支不迁）；文本框优先。
package ragloader

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai/rag"
)

// PptLoadMode ppt 加载模式。
type PptLoadMode uint8

const (
	// PptSingle 整文档一个（缺省——Java 同款）。
	PptSingle PptLoadMode = iota
	// PptPage 每页一个。
	PptPage
)

// PptLoader pptx 加载器。
type PptLoader struct {
	open          func() (io.Reader, error)
	loadMode      PptLoadMode
	pageDelimiter string
	meta          map[string]any
}

// NewPptLoader 从 io.Reader 构造。
func NewPptLoader(r io.Reader) *PptLoader {
	return &PptLoader{open: func() (io.Reader, error) { return r, nil },
		pageDelimiter: "\n\n\n", meta: map[string]any{"type": "ppt"}}
}

// NewPptLoaderFromFile 从文件路径构造。
func NewPptLoaderFromFile(name string) *PptLoader {
	return &PptLoader{open: func() (io.Reader, error) { return os.Open(name) },
		pageDelimiter: "\n\n\n", meta: map[string]any{"type": "ppt"}}
}

// LoadMode 加载模式。
func (l *PptLoader) LoadMode(m PptLoadMode) *PptLoader { l.loadMode = m; return l }

// PageDelimiter 页分隔符（SINGLE 模式拼接用，缺省三换行——Java 同款）。
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

// Load 解析 pptx。
func (l *PptLoader) Load(ctx context.Context) ([]*rag.Document, error) {
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
		return nil, &loaderError{"ragloader: 非 pptx（zip）格式——.ppt 旧格式不支持"}
	}

	pages, err := pptxPages(data)
	if err != nil {
		return nil, err
	}

	if l.loadMode == PptSingle {
		return []*rag.Document{rag.NewDocument(strings.Join(pages, l.pageDelimiter)).MetaMap(l.meta)}, nil
	}
	var docs []*rag.Document
	for i, page := range pages {
		if strings.TrimSpace(page) == "" {
			continue
		}
		docs = append(docs, rag.NewDocument(page).
			Meta("page", i+1).Meta("total_pages", len(pages)).MetaMap(l.meta))
	}
	return docs, nil
}

var slideNamePattern = regexp.MustCompile(`^ppt/slides/slide(\d+)\.xml$`)

// pptxPages 抽幻灯片文本（页序 = 文件名数字序；a:p → 内层 a:t 拼接）。
func pptxPages(data []byte) ([]string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	type slide struct {
		num  int
		text string
	}
	var slides []slide
	for _, f := range zr.File {
		m := slideNamePattern.FindStringSubmatch(f.Name)
		if m == nil {
			continue
		}
		num, _ := strconv.Atoi(m[1])
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		text, err := slideText(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		slides = append(slides, slide{num: num, text: strings.TrimSpace(text)})
	}
	sort.Slice(slides, func(i, j int) bool { return slides[i].num < slides[j].num })
	out := make([]string, len(slides))
	for i, s := range slides {
		out[i] = s.text
	}
	return out, nil
}

// slideText 单页文本（a:t 拼接，段落边界换行）。
func slideText(rc io.Reader) (string, error) {
	decoder := xml.NewDecoder(rc)
	var sb strings.Builder
	inText := false
	paragraphDepth := 0
	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p":
				paragraphDepth++
			case "t":
				inText = true
			}
		case xml.CharData:
			if inText {
				sb.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "p":
				if paragraphDepth > 0 {
					paragraphDepth--
					if paragraphDepth == 0 {
						sb.WriteString("\n")
					}
				}
			case "t":
				inText = false
			}
		}
	}
	return sb.String(), nil
}

var _ rag.Loader = (*PptLoader)(nil)
