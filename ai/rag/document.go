// Package rag 实现 RAG（检索增强生成）SPI 与零依赖内置实现，对标 solon-ai-core
// 的 rag 域（契约 docs/arch/ai/05-rag-design.md）：Document / Loader / Splitter /
// Repository（Storer、Lifecycle 能力接口拆分）/ Query；TextLoader 与
// InMemoryRepository 内置；过滤表达式以 enjoy 求值（SnEL 的 Go 对应物）。
// 带三方依赖的后端（pdf/word 解析、ES/向量库）在 plugins/ai/ragloader 与
// plugins/ai/ragrepo（P5 按需逐个）。
package rag

// Document 检索文档（对标 Java Document）：内容 + 元数据 + 嵌入矢量。Score 为
// 搜索命中得分——瞬态值，由仓储打分产生、不随文档持久化。
type Document struct {
	ID        string
	Content   string
	Metadata  map[string]any
	Embedding []float32

	score float64
}

// NewDocument 建文档（Metadata 预建空表）。
func NewDocument(content string) *Document {
	return &Document{Content: content, Metadata: map[string]any{}}
}

// Meta 链式追加元数据（value 为 nil 时忽略——Java metadata(key, null) 同款）。
func (d *Document) Meta(k string, v any) *Document {
	if d.Metadata == nil {
		d.Metadata = map[string]any{}
	}
	if v != nil {
		d.Metadata[k] = v
	}
	return d
}

// MetaMap 批量追加元数据。
func (d *Document) MetaMap(m map[string]any) *Document {
	for k, v := range m {
		d.Meta(k, v)
	}
	return d
}

// Title 标题（元数据 title 快捷访问，缺省空串）。
func (d *Document) Title() string { s, _ := d.Metadata["title"].(string); return s }

// URL 资源地址（元数据 url 快捷访问）。
func (d *Document) URL() string { s, _ := d.Metadata["url"].(string); return s }

// Summary 摘要（元数据 summary 快捷访问）。
func (d *Document) Summary() string { s, _ := d.Metadata["summary"].(string); return s }

// Score 搜索命中得分（未打分为 0）。
func (d *Document) Score() float64 { return d.score }

// SetScore 设置得分（链式；仓储命中打分用，Java score(v) 对应物）。
func (d *Document) SetScore(s float64) *Document { d.score = s; return d }
