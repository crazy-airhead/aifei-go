// Package embedding 嵌入模型（对标 solon-ai EmbeddingModel）：与 chat 同构的
// Config + 方言 + httpx，但契约从简——无事件流、无会话，纯「文本数组 → 向量数组」。
// 响应侧由方言按 index 排序后返回（对标 Java EmbeddingResponse 构造期排序），
// usage 不进 Go 契约。
package embedding

import (
	"sort"
	"sync"

	ai "github.com/crazy-airhead/aifei-go/ai"
)

// Dialect 嵌入方言 SPI（对标 EmbeddingDialect）。
type Dialect interface {
	// Name 方言注册名（"openai" / "ollama" / "dashscope"）
	Name() string
	// IsDefault 是否补位方言（openai 为 true：无匹配时兜底）
	IsDefault() bool
	// Matched 按配置判断是否匹配（standard/provider/URL 特征）
	Matched(cfg *ai.Config) bool

	// BuildRequestJSON 构建请求体（URL 恒为 cfg.APIURL——Java 侧不做端点拼接，
	// ollama 由使用方直接给 …/api/embed，dashscope 给完整百炼端点）。
	BuildRequestJSON(cfg *ai.Config, opts map[string]any, input []string) ([]byte, error)
	// ParseResponseJSON 解析响应体：错误节点转 error；data 按 index 升序排序后
	// 逐条返回向量（与 input 下标对齐；无 index 的方言按返回序）。
	ParseResponseJSON(body []byte) ([][]float32, error)
}

type dialectEntry struct {
	d        Dialect
	priority int
	seq      int // 同优先级按注册序
}

var (
	dialectMu  sync.RWMutex
	dialectReg []dialectEntry
	dialectSeq int
)

// RegisterDialect 嵌入方言自注册（priority 小者优先，同优先级按注册序）。
func RegisterDialect(d Dialect, priority int) {
	dialectMu.Lock()
	defer dialectMu.Unlock()
	dialectSeq++
	dialectReg = append(dialectReg, dialectEntry{d: d, priority: priority, seq: dialectSeq})
}

// SelectEmbeddingDialect 方言选择（对标 EmbeddingDialectManager.select，选择
// 规则与 chat 的 ai.SelectDialect 对齐）：① cfg.Standard 精确匹配注册名 →
// ② 优先级序第一个 Matched(cfg) → ③ IsDefault 补位。
func SelectEmbeddingDialect(cfg *ai.Config) Dialect {
	dialectMu.RLock()
	entries := append([]dialectEntry(nil), dialectReg...)
	dialectMu.RUnlock()
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].priority != entries[j].priority {
			return entries[i].priority < entries[j].priority
		}
		return entries[i].seq < entries[j].seq
	})

	if cfg != nil && cfg.Standard != "" {
		for _, e := range entries {
			if e.d.Name() == cfg.Standard {
				return e.d
			}
		}
	}
	for _, e := range entries {
		if e.d.Matched(cfg) {
			return e.d
		}
	}
	for _, e := range entries {
		if e.d.IsDefault() {
			return e.d
		}
	}
	return nil
}

func init() {
	// 对标 EmbeddingDialectManager 静态注册三方言（Java 注册序 dashscope →
	// ollama → openai，同 rank 稳定排序后效果一致）
	RegisterDialect(&dashscopeDialect{}, 10)
	RegisterDialect(&ollamaDialect{}, 20)
	RegisterDialect(&openaiDialect{}, 100)
}

// indexedVector 带 index 的向量（排序用）。
type indexedVector struct {
	index int
	vec   []float32
}

// sortVectors 按 index 升序排列并剥壳（无 index 方言不经此函数）。
func sortVectors(items []indexedVector) [][]float32 {
	sort.SliceStable(items, func(i, j int) bool { return items[i].index < items[j].index })
	out := make([][]float32, len(items))
	for i, it := range items {
		out[i] = it.vec
	}
	return out
}
