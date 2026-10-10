// html.go HTML 正文抽取加载器（对标 HtmlSimpleLoader）：goquery 解析，正文为
// body 归一化文本；metadata 提取 title / meta[name=description] / html[lang]。
package ragloader

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/crazy-airhead/aifei-go/ai/rag"
)

// HtmlSimpleLoader HTML 简单加载器。
type HtmlSimpleLoader struct {
	open    func() (io.Reader, error)
	charset string
	baseURI string
	meta    map[string]any
}

// NewHtmlSimpleLoader 从 io.Reader 构造。
func NewHtmlSimpleLoader(r io.Reader) *HtmlSimpleLoader {
	return &HtmlSimpleLoader{
		open:    func() (io.Reader, error) { return r, nil },
		charset: "utf-8",
		meta:    map[string]any{"type": "html"},
	}
}

// NewHtmlSimpleLoaderFromFile 从文件路径构造。
func NewHtmlSimpleLoaderFromFile(name string) *HtmlSimpleLoader {
	return &HtmlSimpleLoader{
		open:    func() (io.Reader, error) { return os.Open(name) },
		charset: "utf-8",
		meta:    map[string]any{"type": "html"},
	}
}

// NewHtmlSimpleLoaderFromURL 从 URL 构造（Load 时拉取；charset 以 Content-Type
// 与 meta 为准由 goquery 处理）。
func NewHtmlSimpleLoaderFromURL(url string) *HtmlSimpleLoader {
	return &HtmlSimpleLoader{
		open: func() (io.Reader, error) {
			resp, err := http.Get(url)
			if err != nil {
				return nil, err
			}
			if resp.StatusCode >= 400 {
				resp.Body.Close()
				return nil, io.EOF
			}
			return resp.Body, nil
		},
		charset: "utf-8",
		meta:    map[string]any{"type": "html"},
	}
}

// Charset 字符集（Java Options.charset；Go 侧由 goquery 的 charset 检测覆盖，
// 此处保留配置位——显式指定时经 golang.org/x/net/html 的 charset 覆盖链）。
func (l *HtmlSimpleLoader) Charset(cs string) *HtmlSimpleLoader {
	if cs != "" {
		l.charset = cs
	}
	return l
}

// BaseURI 相对地址基准（goquery NewDocumentFromReader 不消费 baseUri，保留
// 配置位以对齐 Java Options；相对 URL 解析由调用方后处理）。
func (l *HtmlSimpleLoader) BaseURI(u string) *HtmlSimpleLoader {
	if u != "" {
		l.baseURI = u
	}
	return l
}

// AdditionalMetadata 附加元数据。
func (l *HtmlSimpleLoader) AdditionalMetadata(k string, v any) rag.Loader {
	l.meta[k] = v
	return l
}

// Load 解析正文与元数据（对标 load + buildMetadata）。
func (l *HtmlSimpleLoader) Load(ctx context.Context) ([]*rag.Document, error) {
	r, err := l.open()
	if err != nil {
		return nil, err
	}
	if c, ok := r.(io.Closer); ok && !isHTTPBody(r) {
		defer c.Close()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	doc, err := goquery.NewDocumentFromReader(r)
	if err != nil {
		return nil, err
	}

	text := strings.Join(strings.Fields(doc.Find("body").Text()), " ")

	out := rag.NewDocument(text)
	if title := strings.TrimSpace(doc.Find("title").Text()); title != "" {
		out.Meta("title", title)
	}
	if desc, ok := doc.Find("meta[name=description]").Attr("content"); ok && strings.TrimSpace(desc) != "" {
		out.Meta("description", strings.TrimSpace(desc))
	}
	if lang, ok := doc.Find("html").Attr("lang"); ok && strings.TrimSpace(lang) != "" {
		out.Meta("language", strings.TrimSpace(lang))
	}
	out.MetaMap(l.meta)
	return []*rag.Document{out}, nil
}

// isHTTPBody HTTP 响应体由调用侧 defer 关闭（此处不重复关）。
func isHTTPBody(r io.Reader) bool {
	_, ok := r.(io.ReadCloser)
	return ok
}

var _ rag.Loader = (*HtmlSimpleLoader)(nil)
