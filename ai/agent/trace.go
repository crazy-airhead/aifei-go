package agent

import (
	"sync"

	"github.com/crazy-airhead/aifei-go/ai"
)

// Trace 智能体执行轨迹（对标 AgentTrace 的只读面）。
type Trace interface {
	RunID() string
	AgentName() string
	BeginTimeMs() int64
	OriginalPrompt() *ai.Prompt
	WorkingMemory() *ai.Prompt
	Metrics() *Metrics
}

// Metrics 用量与耗时指标（对标 trace.Metrics，逐字段移植）。
// 并发安全；addMetrics 不汇总耗时（父级耗时按自身活跃执行片段统计）。
type Metrics struct {
	mu sync.Mutex

	totalDuration int64

	promptTokens     int64
	thinkTokens      int64
	completionTokens int64
	totalTokens      int64

	cacheCreationInputTokens   int64
	cacheReadInputTokens       int64
	cacheCreation5mInputTokens int64
	cacheCreation1hInputTokens int64

	webSearchRequests int64
	webFetchRequests  int64
}

// Reset 清零全部指标。
func (m *Metrics) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resetLocked()
}

func (m *Metrics) resetLocked() {
	m.totalDuration = 0
	m.promptTokens = 0
	m.thinkTokens = 0
	m.completionTokens = 0
	m.totalTokens = 0
	m.cacheCreationInputTokens = 0
	m.cacheReadInputTokens = 0
	m.cacheCreation5mInputTokens = 0
	m.cacheCreation1hInputTokens = 0
	m.webSearchRequests = 0
	m.webFetchRequests = 0
}

// AddTotalDuration 累加耗时（毫秒）。
func (m *Metrics) AddTotalDuration(ms int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.totalDuration += ms
}

// SetTotalDuration 覆写耗时（毫秒）。
func (m *Metrics) SetTotalDuration(ms int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.totalDuration = ms
}

// AddUsage 累加一次模型调用的用量。
func (m *Metrics) AddUsage(u *ai.Usage) {
	if u == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.promptTokens += u.PromptTokens
	m.thinkTokens += u.ThinkTokens
	m.completionTokens += u.CompletionTokens
	m.totalTokens += u.TotalTokens
	m.cacheCreationInputTokens += u.CacheCreationInputTokens
	m.cacheReadInputTokens += u.CacheReadInputTokens
	m.cacheCreation5mInputTokens += u.CacheCreation5mInputTokens
	m.cacheCreation1hInputTokens += u.CacheCreation1hInputTokens
	m.webSearchRequests += u.WebSearchRequests
	m.webFetchRequests += u.WebFetchRequests
}

// AddMetrics 汇总其它智能体的用量指标（不汇总耗时）。
func (m *Metrics) AddMetrics(src *Metrics) {
	if src == nil {
		return
	}
	s := src.Snapshot()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.promptTokens += s.promptTokens
	m.thinkTokens += s.thinkTokens
	m.completionTokens += s.completionTokens
	m.totalTokens += s.totalTokens
	m.cacheCreationInputTokens += s.cacheCreationInputTokens
	m.cacheReadInputTokens += s.cacheReadInputTokens
	m.cacheCreation5mInputTokens += s.cacheCreation5mInputTokens
	m.cacheCreation1hInputTokens += s.cacheCreation1hInputTokens
	m.webSearchRequests += s.webSearchRequests
	m.webFetchRequests += s.webFetchRequests
}

// Snapshot 当前指标的一致性快照。
func (m *Metrics) Snapshot() *Metrics {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := &Metrics{}
	out.totalDuration = m.totalDuration
	out.promptTokens = m.promptTokens
	out.thinkTokens = m.thinkTokens
	out.completionTokens = m.completionTokens
	out.totalTokens = m.totalTokens
	out.cacheCreationInputTokens = m.cacheCreationInputTokens
	out.cacheReadInputTokens = m.cacheReadInputTokens
	out.cacheCreation5mInputTokens = m.cacheCreation5mInputTokens
	out.cacheCreation1hInputTokens = m.cacheCreation1hInputTokens
	out.webSearchRequests = m.webSearchRequests
	out.webFetchRequests = m.webFetchRequests
	return out
}

// DeltaSince 相对基线快照的增量。
func (m *Metrics) DeltaSince(baseline *Metrics) *Metrics {
	if baseline == nil {
		return m.Snapshot()
	}
	cur, base := m.Snapshot(), baseline.Snapshot()
	d := &Metrics{}
	d.totalDuration = cur.totalDuration - base.totalDuration
	d.promptTokens = cur.promptTokens - base.promptTokens
	d.thinkTokens = cur.thinkTokens - base.thinkTokens
	d.completionTokens = cur.completionTokens - base.completionTokens
	d.totalTokens = cur.totalTokens - base.totalTokens
	d.cacheCreationInputTokens = cur.cacheCreationInputTokens - base.cacheCreationInputTokens
	d.cacheReadInputTokens = cur.cacheReadInputTokens - base.cacheReadInputTokens
	d.cacheCreation5mInputTokens = cur.cacheCreation5mInputTokens - base.cacheCreation5mInputTokens
	d.cacheCreation1hInputTokens = cur.cacheCreation1hInputTokens - base.cacheCreation1hInputTokens
	d.webSearchRequests = cur.webSearchRequests - base.webSearchRequests
	d.webFetchRequests = cur.webFetchRequests - base.webFetchRequests
	return d
}

// TotalDuration 耗时（毫秒）。
func (m *Metrics) TotalDuration() int64 { m.mu.Lock(); defer m.mu.Unlock(); return m.totalDuration }

// PromptTokens / ThinkTokens / CompletionTokens / TotalTokens 各类令牌数。
func (m *Metrics) PromptTokens() int64 { m.mu.Lock(); defer m.mu.Unlock(); return m.promptTokens }
func (m *Metrics) ThinkTokens() int64  { m.mu.Lock(); defer m.mu.Unlock(); return m.thinkTokens }
func (m *Metrics) CompletionTokens() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.completionTokens
}
func (m *Metrics) TotalTokens() int64 { m.mu.Lock(); defer m.mu.Unlock(); return m.totalTokens }

// Cache* 缓存令牌数。
func (m *Metrics) CacheCreationInputTokens() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cacheCreationInputTokens
}
func (m *Metrics) CacheReadInputTokens() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cacheReadInputTokens
}
func (m *Metrics) CacheCreation5mInputTokens() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cacheCreation5mInputTokens
}
func (m *Metrics) CacheCreation1hInputTokens() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cacheCreation1hInputTokens
}

// WebSearchRequests / WebFetchRequests 服务端工具调用数。
func (m *Metrics) WebSearchRequests() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.webSearchRequests
}
func (m *Metrics) WebFetchRequests() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.webFetchRequests
}

// CacheRate 缓存命中率（0-100 百分比，保留 2 位小数）：缓存读取输入令牌占
// 提示语输入令牌的比例；promptTokens<=0 时为 0。
func (m *Metrics) CacheRate() float64 {
	m.mu.Lock()
	prompt, cached := m.promptTokens, m.cacheReadInputTokens
	m.mu.Unlock()
	if prompt <= 0 {
		return 0
	}
	rate := float64(cached) * 100.0 / float64(prompt)
	if rate < 0 {
		rate = 0
	}
	if rate > 100 {
		rate = 100
	}
	return float64(int64(rate*100+0.5)) / 100.0
}
