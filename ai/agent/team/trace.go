// trace.go 团队协作轨迹（对标 team.TeamTrace）：任务在团队内部流转的全生命
// 周期状态，协作「黑匣子」与状态总线。满足 agent.TeamTrace 缝（成员智能体的
// AgentBase.Run 经该面读协议/拦截器/流出口并记录产出）。
package team

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/flow"
)

// Record 协作足迹（对标 TeamTrace.TeamRecord）。
type Record struct {
	role       ai.Role
	source     string
	content    string
	durationMs int64
}

// NewRecord 构造足迹。
func NewRecord(role ai.Role, source, content string, durationMs int64) *Record {
	return &Record{role: role, source: source, content: content, durationMs: durationMs}
}

// Role 足迹角色。
func (r *Record) Role() ai.Role { return r.role }

// IsAgent 是否智能体产出（assistant 角色；系统/调度记录为 false）。
func (r *Record) IsAgent() bool { return r.role == ai.RoleAssistant }

// Source 产出方（agent 名 / system / supervisor）。
func (r *Record) Source() string { return r.source }

// Content 产出内容。
func (r *Record) Content() string { return r.content }

// Duration 执行耗时（毫秒）。
func (r *Record) Duration() int64 { return r.durationMs }

// Trace 团队轨迹。字段访问无锁（单协程驱动），records/protocolContext/extras
// 为并发安全容器（流式推送与拦截器可能在其它协程读取）。
type Trace struct {
	mu sync.RWMutex

	config  *Config   // transient
	options *Options  // transient
	session agent.Session // transient

	runId           string
	agentName       string
	originalPrompt  *ai.Prompt
	workingMemory   *ai.Prompt
	records         []*Record
	route           string
	finalAnswer     string
	hasFinalAnswer  bool
	lastDecision    string
	lastAgentName   string
	turnCounter     atomic.Int64
	metrics         agent.Metrics
	beginTimeMs     int64
	protocolContext sync.Map
	extras          sync.Map
}

// NewTrace 空轨迹。
func NewTrace() *Trace {
	return &Trace{
		workingMemory: ai.NewPrompt(),
	}
}

// Current 从流上下文解析当前团队轨迹（KEY_CURRENT_TEAM_TRACE_KEY → traceKey；
// 无则 nil——Java TeamTrace.getCurrent 同款）。
func Current(ctx flow.Context) *Trace {
	if ctx == nil {
		return nil
	}
	traceKey, _ := ctx.Get(agent.KeyCurrentTeamTraceKey).(string)
	if traceKey == "" {
		return nil
	}
	trace, _ := ctx.Get(traceKey).(*Trace)
	return trace
}

// ---- 生命周期 ----

// NewTracePrepared 构造带运行时环境的轨迹（集成方直调协议方法/测试用——
// 内部走 prepare 全语义：绑定 config/options/session 与原始输入）。
func NewTracePrepared(config *Config, session agent.Session, originalPrompt *ai.Prompt) *Trace {
	t := NewTrace()
	t.prepare(config, config.DefaultOptions(), session, config.Name())
	if ctx := session.Context(); ctx != nil {
		ctx.Put(agent.KeyCurrentTeamTraceKey, config.TraceKey())
		ctx.Put(config.TraceKey(), t)
	}
	t.setOriginalPrompt(originalPrompt)
	return t
}

// prepare 运行时环境准备（绑定 transient 引用；每次执行清挂起标记与终态）。
func (t *Trace) prepare(config *Config, options *Options, session agent.Session, agentName string) {
	t.config = config
	t.options = options
	t.session = session
	t.agentName = agentName
	t.mu.Lock()
	t.finalAnswer = ""
	t.hasFinalAnswer = false
	t.mu.Unlock()

	// 每次执行重置中断状态
	session.Pending(false, "")
}

// activeTalents 激活才能（工具上下文并入原始提示词 attrs；Go 版才能包随
// 后续卡迁移，此处保留 attrs 合并语义）。
func (t *Trace) activeTalents() {
	if t.originalPrompt != nil && len(t.options.ToolContext) > 0 {
		attrs := t.originalPrompt.Attrs()
		for k, v := range t.options.ToolContext {
			attrs[k] = v
		}
	}
}

// reset 新任务开始：路由/终态/流水账/工作记忆/协议上下文/指标全量重置。
func (t *Trace) reset(originalPrompt *ai.Prompt) {
	t.turnCounter.Store(0)
	t.mu.Lock()
	t.route = ""
	t.finalAnswer = ""
	t.hasFinalAnswer = false
	t.lastDecision = ""
	t.lastAgentName = ""
	t.records = nil
	t.mu.Unlock()
	t.workingMemory.Clear()
	t.protocolContext.Range(func(k, _ any) bool { t.protocolContext.Delete(k); return true })
	t.metrics.Reset()
	t.setOriginalPrompt(originalPrompt)
	t.beginTimeMs = time.Now().UnixMilli()
	t.runId = newRunID()
}

// ---- 状态访问 ----

// IsInitial 是否初始状态（无任何足迹）。
func (t *Trace) IsInitial() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.records) == 0
}

// LastAgentContent 最近一位专家（非 Supervisor/system）的产出。
func (t *Trace) LastAgentContent() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for i := len(t.records) - 1; i >= 0; i-- {
		if t.records[i].IsAgent() {
			return t.records[i].Content()
		}
	}
	return ""
}

// LastAgentDuration 最近一位专家的执行耗时（毫秒）。
func (t *Trace) LastAgentDuration() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for i := len(t.records) - 1; i >= 0; i-- {
		if t.records[i].IsAgent() {
			return t.records[i].Duration()
		}
	}
	return 0
}

// RunID 运行 id（惰性生成——空提示词恢复路径未 reset 时仍有值）。
func (t *Trace) RunID() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.runId == "" {
		t.runId = newRunID()
	}
	return t.runId
}

// AgentName 当前团队名。
func (t *Trace) AgentName() string { return t.agentName }

// Metrics 用量与耗时指标。
func (t *Trace) Metrics() *agent.Metrics { return &t.metrics }

// BeginTimeMs 任务开始时间。
func (t *Trace) BeginTimeMs() int64 { return t.beginTimeMs }

// Config 关联团队配置。
func (t *Trace) Config() *Config { return t.config }

// Options 运行时选项。
func (t *Trace) Options() *Options { return t.options }

// Session 当前活跃会话。
func (t *Trace) Session() agent.Session { return t.session }

// Context 会话上下文（session 缺省时 nil）。
func (t *Trace) Context() flow.Context {
	if t.session != nil {
		return t.session.Context()
	}
	return nil
}

// Protocol 协议（窄面缝：agent.TeamTrace.Protocol；宽面经 Config.ProtocolWide）。
func (t *Trace) Protocol() agent.TeamProtocol {
	if t.config == nil {
		return nil
	}
	return t.config.protocolNarrow()
}

// OriginalPrompt 当前任务提示词。
func (t *Trace) OriginalPrompt() *ai.Prompt {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.originalPrompt
}

func (t *Trace) setOriginalPrompt(p *ai.Prompt) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.originalPrompt = p
}

// WorkingMemory 工作记忆。
func (t *Trace) WorkingMemory() *ai.Prompt { return t.workingMemory }

// Route 路由决策（下一个 agent 或 end）。
func (t *Trace) Route() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.route
}

// SetRoute 设置路由。
func (t *Trace) SetRoute(route string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.route = route
}

// LastDecision 调度器原始决策文本（异常复盘用）。
func (t *Trace) LastDecision() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lastDecision
}

// SetLastDecision 记录决策文本。
func (t *Trace) SetLastDecision(decision string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastDecision = decision
}

// LastAgentName 最后运行的专家名。
func (t *Trace) LastAgentName() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lastAgentName
}

// SetLastAgentName 记录最后运行的专家名。
func (t *Trace) SetLastAgentName(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastAgentName = name
}

// TurnCount 迭代轮次计数。
func (t *Trace) TurnCount() int { return int(t.turnCounter.Load()) }

// ResetTurnCount 清零轮次。
func (t *Trace) ResetTurnCount() { t.turnCounter.Store(0) }

// NextTurn 递增并返回轮次。
func (t *Trace) NextTurn() int { return int(t.turnCounter.Add(1)) }

// ---- 协议私有上下文 ----

// ProtocolContext 协议私有存储空间（键值并发安全）。
func (t *Trace) ProtocolContext() *sync.Map { return &t.protocolContext }

// ResetProtocolContext 清空协议上下文。
func (t *Trace) ResetProtocolContext() {
	t.protocolContext.Range(func(k, _ any) bool { t.protocolContext.Delete(k); return true })
}

// ProtocolDashboardSnapshot 协议状态看板快照（JSON；供 agent 感知全局进度）。
func (t *Trace) ProtocolDashboardSnapshot() string {
	m := map[string]any{}
	t.protocolContext.Range(func(k, v any) bool { m[k.(string)] = v; return true })
	data, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// ---- 足迹 ----

// RecordCount 足迹数。
func (t *Trace) RecordCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.records)
}

// AddRecord 记录执行足迹。
func (t *Trace) AddRecord(role ai.Role, agentName, content string, durationMs int64) {
	t.mu.Lock()
	t.records = append(t.records, NewRecord(role, agentName, content, durationMs))
	t.mu.Unlock()
}

// Records 全量足迹（快照拷贝）。
func (t *Trace) Records() []*Record {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return append([]*Record(nil), t.records...)
}

// FormattedHistory 全量格式化历史（Markdown）。
func (t *Trace) FormattedHistory() string { return t.FormattedHistoryWindow(0, true) }

// FormattedHistoryOf 最近 windowSize 条（含系统记录）。
func (t *Trace) FormattedHistoryOf(windowSize int) string {
	return t.FormattedHistoryWindow(windowSize, true)
}

// FormattedHistoryWindow 渲染协作历史（windowSize 0=不限；includeSystem
// 是否包含调度器决策记录）。
func (t *Trace) FormattedHistoryWindow(windowSize int, includeSystem bool) string {
	records := t.Records()
	if len(records) == 0 {
		return "No progress yet."
	}
	if !includeSystem {
		kept := records[:0]
		for _, r := range records {
			if r.IsAgent() {
				kept = append(kept, r)
			}
		}
		records = kept
	}
	if windowSize > 0 && len(records) > windowSize {
		records = records[len(records)-windowSize:]
	}
	parts := make([]string, 0, len(records))
	for _, r := range records {
		title := "System Instruction"
		if r.IsAgent() {
			title = "Expert Output"
		}
		parts = append(parts, "### "+title+" from ["+r.Source()+"]:\n"+r.Content())
	}
	return strings.Join(parts, "\n\n")
}

// ---- 终态 ----

// FinalAnswer 最终交付答案。
func (t *Trace) FinalAnswer() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.finalAnswer
}

// SetFinalAnswer 设置最终答案。
func (t *Trace) SetFinalAnswer(answer string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.finalAnswer = answer
	t.hasFinalAnswer = true
}

// HasFinalAnswer 是否已显式设置过最终答案。
func (t *Trace) HasFinalAnswer() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.hasFinalAnswer
}

// ---- extras ----

// Extras 附加数据袋。
func (t *Trace) Extras() *sync.Map { return &t.extras }

// Extra 读附加数据。
func (t *Trace) Extra(key string) any { v, _ := t.extras.Load(key); return v }

// SetExtra 写附加数据。
func (t *Trace) SetExtra(key string, val any) { t.extras.Store(key, val) }

// ---- 流式 ----

// HasStreamSink 是否有流出口。
func (t *Trace) HasStreamSink() bool {
	return t.options != nil && t.options.StreamSink() != nil
}

// StreamSink 当前流出口（可 nil；agent.TeamTrace 缝）。
func (t *Trace) StreamSink() agent.EventSink {
	if t.options == nil {
		return nil
	}
	return t.options.StreamSink()
}

// IsStreamCancelled 流式订阅是否已取消（无出口视为 true——Java 同款）。
func (t *Trace) IsStreamCancelled() bool {
	if !t.HasStreamSink() {
		return true
	}
	return t.options.cancelled()
}

// PushAgentEvent 推送事件（判空/已取消/异常吞掉——事件通道不影响主流程）。
func (t *Trace) PushAgentEvent(ev *agent.Event) {
	if ev == nil || t.IsStreamCancelled() {
		return
	}
	if err := t.options.StreamSink().OnEvent(ev); err != nil {
		_ = err // 忽略投递异常
	}
}

// Interceptors 成员级拦截器窄面（agent.TeamTrace 缝）。
func (t *Trace) Interceptors() []agent.TeamInterceptor {
	if t.options == nil {
		return nil
	}
	return interceptorsAsAgent(t.options.Interceptors)
}

// event 统一事件载体。
func (t *Trace) event(kind agent.EventKind) *agent.Event {
	return &agent.Event{
		Kind: kind, RunID: t.RunID(), AgentName: t.agentName,
		Session: t.session, Trace: t, Meta: map[string]any{},
	}
}

func newRunID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "run"
	}
	return hex.EncodeToString(b)
}
