// 生成方言 SPI（对标 GenerateDialect/DialectManager）。选择规则与
// chat/embedding/rerank 对齐：cfg.Standard 精确匹配注册名 → 优先级序第一个
// Matched(cfg) → IsDefault 补位（openai）。
package generate

import (
	"sort"
	"sync"
)

// Dialect 生成方言 SPI。
type Dialect interface {
	// Name 方言注册名（"openai" / "ollama" / "dashscope"）
	Name() string
	// IsDefault 是否补位方言（openai 为 true：无匹配时兜底）
	IsDefault() bool
	// Matched 按配置判断是否匹配（standard/provider/URL 特征）
	Matched(cfg *Config) bool

	// BuildRequestJSON 构建请求体。promptStr 与 promptMap 互斥、str 优先
	//（对标 Java prompt(...) 三个重载的语义）；options 平铺根级且最后写入
	//（可覆盖 model 同名键——GenerateOptions.model 助手即依赖此语义）。
	BuildRequestJSON(cfg *Config, opts map[string]any, promptStr string, promptMap map[string]any) ([]byte, error)
	// ParseResponseJSON 解析响应体：错误节点转 error；data 为生成内容列表。
	ParseResponseJSON(cfg *Config, body []byte) (*Response, error)
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

// RegisterDialect 生成方言自注册（priority 小者优先，同优先级按注册序）。
func RegisterDialect(d Dialect, priority int) {
	dialectMu.Lock()
	defer dialectMu.Unlock()
	dialectSeq++
	dialectReg = append(dialectReg, dialectEntry{d: d, priority: priority, seq: dialectSeq})
}

// SelectGenerateDialect 方言选择（对标 GenerateDialectManager.select：Java 注册
// 序 dashscope → ollama → openai，同 rank 稳定排序后效果一致）。
func SelectGenerateDialect(cfg *Config) Dialect {
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
	RegisterDialect(&dashscopeDialect{}, 10)
	RegisterDialect(&ollamaDialect{}, 20)
	RegisterDialect(&openaiDialect{}, 100)
}
