package ai

import (
	"encoding/json"
	"fmt"
)

// ContentBlock 是多模态内容块的统一抽象（对标 Java ContentBlock 家族，5 个具体实现：
// Text/Image/Audio/Video/Blob，外加 ResourceLink 资源引用）。
// 序列化按 "type" 字段判别分发（各类型自带 MarshalJSON 注入判别字段）。
type ContentBlock interface {
	Kind() string // "text" | "image" | "audio" | "video" | "blob" | "resource"
	MimeType() string
	Content() any // string / []byte / url
	Metas() map[string]any
}

// 块序列化别名：剥离 MarshalJSON 方法集，避免内嵌包装结构体的方法提升造成无限递归。
type imageBlockAlias ImageBlock
type audioBlockAlias AudioBlock
type videoBlockAlias VideoBlock
type blobBlockAlias BlobBlock
type resourceLinkAlias ResourceLink

// TextBlock 文本块。
type TextBlock struct {
	Text string `json:"text"`
}

func TextBlockOf(s string) TextBlock { return TextBlock{Text: s} }

func (b TextBlock) Kind() string          { return "text" }
func (b TextBlock) MimeType() string      { return "text/plain" }
func (b TextBlock) Content() any          { return b.Text }
func (b TextBlock) Metas() map[string]any { return nil }

func (b TextBlock) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"type": "text", "text": b.Text})
}

// ImageBlock 图像块：URL 或 base64 数据二选一（对标 AbsMedia）。
type ImageBlock struct {
	URL  string         `json:"url,omitempty"`
	Data []byte         `json:"data,omitempty"` // JSON 中为 base64（无 data: 前缀）
	Mime string         `json:"mimeType,omitempty"`
	Meta map[string]any `json:"metas,omitempty"`
}

func ImageBlockOfURL(u string, mime ...string) ImageBlock {
	return ImageBlock{URL: u, Mime: firstOr(mime, "image/jpeg")}
}

func ImageBlockOfBase64(b []byte, mime ...string) ImageBlock {
	return ImageBlock{Data: b, Mime: firstOr(mime, "image/jpeg")}
}

func (b ImageBlock) Kind() string     { return "image" }
func (b ImageBlock) MimeType() string { return b.Mime }
func (b ImageBlock) Content() any {
	if b.URL != "" {
		return b.URL
	}
	return b.Data
}
func (b ImageBlock) Metas() map[string]any { return b.Meta }

func (b ImageBlock) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		imageBlockAlias
	}{Type: "image", imageBlockAlias: imageBlockAlias(b)})
}

// AudioBlock 音频块。
type AudioBlock struct {
	URL  string         `json:"url,omitempty"`
	Data []byte         `json:"data,omitempty"`
	Mime string         `json:"mimeType,omitempty"`
	Meta map[string]any `json:"metas,omitempty"`
}

func AudioBlockOfURL(u string, mime ...string) AudioBlock {
	return AudioBlock{URL: u, Mime: firstOr(mime, "audio/mpeg")}
}

func AudioBlockOfBase64(b []byte, mime ...string) AudioBlock {
	return AudioBlock{Data: b, Mime: firstOr(mime, "audio/mpeg")}
}

func (b AudioBlock) Kind() string     { return "audio" }
func (b AudioBlock) MimeType() string { return b.Mime }
func (b AudioBlock) Content() any {
	if b.URL != "" {
		return b.URL
	}
	return b.Data
}
func (b AudioBlock) Metas() map[string]any { return b.Meta }

func (b AudioBlock) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		audioBlockAlias
	}{Type: "audio", audioBlockAlias: audioBlockAlias(b)})
}

// VideoBlock 视频块。
type VideoBlock struct {
	URL  string         `json:"url,omitempty"`
	Data []byte         `json:"data,omitempty"`
	Mime string         `json:"mimeType,omitempty"`
	Meta map[string]any `json:"metas,omitempty"`
}

func VideoBlockOfURL(u string, mime ...string) VideoBlock {
	return VideoBlock{URL: u, Mime: firstOr(mime, "video/mpeg")}
}

func VideoBlockOfBase64(b []byte, mime ...string) VideoBlock {
	return VideoBlock{Data: b, Mime: firstOr(mime, "video/mpeg")}
}

func (b VideoBlock) Kind() string     { return "video" }
func (b VideoBlock) MimeType() string { return b.Mime }
func (b VideoBlock) Content() any {
	if b.URL != "" {
		return b.URL
	}
	return b.Data
}
func (b VideoBlock) Metas() map[string]any { return b.Meta }

func (b VideoBlock) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		videoBlockAlias
	}{Type: "video", videoBlockAlias: videoBlockAlias(b)})
}

// BlobBlock 通用二进制块（未定 mime 的文件载荷，3.9.2+）。
type BlobBlock struct {
	URL  string         `json:"url,omitempty"`
	Data []byte         `json:"data,omitempty"`
	Mime string         `json:"mimeType,omitempty"`
	Meta map[string]any `json:"metas,omitempty"`
}

func BlobBlockOfURL(u string, mime ...string) BlobBlock {
	return BlobBlock{URL: u, Mime: firstOr(mime, "application/octet-stream")}
}

func BlobBlockOfBase64(b []byte, mime ...string) BlobBlock {
	return BlobBlock{Data: b, Mime: firstOr(mime, "application/octet-stream")}
}

func (b BlobBlock) Kind() string     { return "blob" }
func (b BlobBlock) MimeType() string { return b.Mime }
func (b BlobBlock) Content() any {
	if b.URL != "" {
		return b.URL
	}
	return b.Data
}
func (b BlobBlock) Metas() map[string]any { return b.Meta }

func (b BlobBlock) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		blobBlockAlias
	}{Type: "blob", blobBlockAlias: blobBlockAlias(b)})
}

// ResourceLink 资源链接块：引用外部命名资源，不内联载荷（对标 ResourceBlock 语义位）。
type ResourceLink struct {
	Name string         `json:"name"`
	URI  string         `json:"uri"`
	Mime string         `json:"mimeType,omitempty"`
	Meta map[string]any `json:"metas,omitempty"`
}

func (r *ResourceLink) Kind() string          { return "resource" }
func (r *ResourceLink) MimeType() string      { return r.Mime }
func (r *ResourceLink) Content() any          { return r.URI }
func (r *ResourceLink) Metas() map[string]any { return r.Meta }

func (r *ResourceLink) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		resourceLinkAlias
	}{Type: "resource", resourceLinkAlias: resourceLinkAlias(*r)})
}

// Contents 多模态容器：文本段与块的有序混合（对标 Java Contents）。
type Contents struct {
	items []any // string | ContentBlock
}

func NewContents(text ...string) *Contents {
	c := &Contents{}
	for _, s := range text {
		c.AddText(s)
	}
	return c
}

func (c *Contents) AddText(s string) *Contents {
	c.items = append(c.items, s)
	return c
}

func (c *Contents) AddBlock(b ContentBlock) *Contents {
	c.items = append(c.items, b)
	return c
}

func (c *Contents) AddBlocks(blocks []ContentBlock) *Contents {
	for _, b := range blocks {
		c.AddBlock(b)
	}
	return c
}

// IsMultiModal 是否含非文本块（有线形态判别：>1 块或单块非文本 → 数组形态）。
func (c *Contents) IsMultiModal() bool {
	for _, it := range c.items {
		if _, ok := it.(TextBlock); !ok {
			return true
		}
	}
	return false
}

// Texts 依序取出全部文本段。
func (c *Contents) Texts() []string {
	var out []string
	for _, it := range c.items {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// Text 拼接全部文本段。
func (c *Contents) Text() string {
	s := ""
	for _, t := range c.Texts() {
		s += t
	}
	return s
}

// Blocks 依序取出全部块（文本段转为 TextBlock）。
func (c *Contents) Blocks() []ContentBlock {
	out := make([]ContentBlock, 0, len(c.items))
	for _, it := range c.items {
		switch v := it.(type) {
		case string:
			out = append(out, TextBlock{Text: v})
		case ContentBlock:
			out = append(out, v)
		}
	}
	return out
}

func (c *Contents) Clone() *Contents {
	n := &Contents{items: make([]any, len(c.items))}
	for i, it := range c.items {
		switch v := it.(type) {
		case string:
			n.items[i] = v
		case ContentBlock:
			n.items[i] = cloneBlock(v)
		}
	}
	return n
}

// cloneBlock 深拷贝块（值复制 + metas map 复制）。
func cloneBlock(b ContentBlock) ContentBlock {
	switch v := b.(type) {
	case TextBlock:
		return v
	case ImageBlock:
		v.Meta = copyMap(v.Meta)
		v.Data = append([]byte(nil), v.Data...)
		return v
	case AudioBlock:
		v.Meta = copyMap(v.Meta)
		v.Data = append([]byte(nil), v.Data...)
		return v
	case VideoBlock:
		v.Meta = copyMap(v.Meta)
		v.Data = append([]byte(nil), v.Data...)
		return v
	case BlobBlock:
		v.Meta = copyMap(v.Meta)
		v.Data = append([]byte(nil), v.Data...)
		return v
	case *ResourceLink:
		n := *v
		n.Meta = copyMap(v.Meta)
		return &n
	}
	return b
}

func copyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	n := make(map[string]any, len(m))
	for k, v := range m {
		n[k] = v
	}
	return n
}

func firstOr(ss []string, def string) string {
	if len(ss) > 0 && ss[0] != "" {
		return ss[0]
	}
	return def
}

// ---- 反序列化判别：按 "type" 分发 ----

func unmarshalBlocks(data []byte) ([]ContentBlock, error) {
	var arr []json.RawMessage
	if err := json.Unmarshal(data, &arr); err != nil {
		return nil, err
	}
	blocks := make([]ContentBlock, 0, len(arr))
	for _, raw := range arr {
		b, err := unmarshalBlock(raw)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, b)
	}
	return blocks, nil
}

func unmarshalBlock(raw []byte) (ContentBlock, error) {
	var env struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	switch env.Type {
	case "text":
		var b TextBlock
		return b, json.Unmarshal(raw, &b)
	case "image":
		var b ImageBlock
		return b, json.Unmarshal(raw, &b)
	case "audio":
		var b AudioBlock
		return b, json.Unmarshal(raw, &b)
	case "video":
		var b VideoBlock
		return b, json.Unmarshal(raw, &b)
	case "blob":
		var b BlobBlock
		return b, json.Unmarshal(raw, &b)
	case "resource":
		var b ResourceLink
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
		return &b, nil
	}
	return nil, fmt.Errorf("ai: unknown content block type %q", env.Type)
}
