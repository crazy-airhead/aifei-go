package ai

import "encoding/json"

// Usage token 用量（对标 AiUsage 全字段）。
type Usage struct {
	PromptTokens     int64 `json:"promptTokens,omitempty"`
	ThinkTokens      int64 `json:"thinkTokens,omitempty"`
	CompletionTokens int64 `json:"completionTokens,omitempty"`
	TotalTokens      int64 `json:"totalTokens,omitempty"`

	CacheCreationInputTokens   int64 `json:"cacheCreationInputTokens,omitempty"`
	CacheReadInputTokens       int64 `json:"cacheReadInputTokens,omitempty"`
	CacheCreation5mInputTokens int64 `json:"cacheCreation5mInputTokens,omitempty"`
	CacheCreation1hInputTokens int64 `json:"cacheCreation1hInputTokens,omitempty"`

	WebSearchRequests int64 `json:"webSearchRequests,omitempty"`
	WebFetchRequests  int64 `json:"webFetchRequests,omitempty"`

	ServiceTier  string          `json:"serviceTier,omitempty"`
	InferenceGeo string          `json:"inferenceGeo,omitempty"`
	Source       json.RawMessage `json:"source,omitempty"` // 原始 usage 帧
}

// CacheRate 缓存命中率（cacheRead / prompt，prompt 为 0 时返回 0）。
func (u *Usage) CacheRate() float64 {
	if u == nil || u.PromptTokens == 0 {
		return 0
	}
	return float64(u.CacheReadInputTokens) / float64(u.PromptTokens)
}

// Add 跨 step 累计（autoToolCall 多轮的总用量）。
func (u *Usage) Add(other *Usage) *Usage {
	if other == nil {
		return u
	}
	u.PromptTokens += other.PromptTokens
	u.ThinkTokens += other.ThinkTokens
	u.CompletionTokens += other.CompletionTokens
	u.TotalTokens += other.TotalTokens
	u.CacheCreationInputTokens += other.CacheCreationInputTokens
	u.CacheReadInputTokens += other.CacheReadInputTokens
	u.CacheCreation5mInputTokens += other.CacheCreation5mInputTokens
	u.CacheCreation1hInputTokens += other.CacheCreation1hInputTokens
	u.WebSearchRequests += other.WebSearchRequests
	u.WebFetchRequests += other.WebFetchRequests
	return u
}
