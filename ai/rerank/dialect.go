// 重排方言 SPI（对标 RerankingDialect/DialectManager，选择规则与 chat/embedding
// 对齐）：cfg.Standard 精确匹配注册名 → 优先级序第一个 Matched → IsDefault 补位。
package rerank

import (
	"sort"
	"sync"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/rag"
)

// Dialect 重排方言 SPI。
type Dialect interface {
	// Name 方言注册名（"openai" / "dashscope"）
	Name() string
	// IsDefault 是否补位方言（openai 为 true：无匹配时兜底）
	IsDefault() bool
	// Matched 按配置判断是否匹配（standard/provider/URL 特征）
	Matched(cfg *ai.Config) bool

	// BuildRequestJSON 构建请求体（URL 恒为 cfg.APIURL——Java 侧不做端点拼接）。
	BuildRequestJSON(cfg *ai.Config, opts map[string]any, query string, documents []*rag.Document) ([]byte, error)
	// ParseResponseJSON 解析响应体：错误节点转 error；results 按 index 升序。
	ParseResponseJSON(body []byte) (*Response, error)
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

// RegisterDialect 重排方言自注册（priority 小者优先，同优先级按注册序）。
func RegisterDialect(d Dialect, priority int) {
	dialectMu.Lock()
	defer dialectMu.Unlock()
	dialectSeq++
	dialectReg = append(dialectReg, dialectEntry{d: d, priority: priority, seq: dialectSeq})
}

// SelectRerankDialect 方言选择（对标 RerankingDialectManager.select）。
func SelectRerankDialect(cfg *ai.Config) Dialect {
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
	// 对标 RerankingDialectManager 静态注册两方言（Java 注册序 dashscope →
	// openai，同 rank 稳定排序后效果一致）
	RegisterDialect(&dashscopeDialect{}, 10)
	RegisterDialect(&openaiDialect{}, 100)
}
