package simple

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/flow"
)

// Trace Simple 运行轨迹（对标 SimpleTrace：状态机上下文——短期记忆/原始
// 提示词/指标；config/options/session/protocol 为 transient 绑定）。
type Trace struct {
	cfg      *Config
	options  *Options
	session  agent.Session
	protocol agent.TeamProtocol

	agentName      string
	runId          string
	originalPrompt *ai.Prompt
	workingMemory  *ai.Prompt
	metrics        *agent.Metrics
	beginTimeMs    int64
}

// NewTrace 空轨迹（零值 workingMemory/metrics 就绪）。
func NewTrace() *Trace {
	return &Trace{workingMemory: ai.NewPrompt(), metrics: &agent.Metrics{}}
}

// prepare 绑定 transient 引用（每次 call 进入时刷新）。
func (t *Trace) prepare(cfg *Config, options *Options, session agent.Session, protocol agent.TeamProtocol, agentName string) {
	t.cfg = cfg
	t.agentName = agentName
	t.options = options
	t.session = session
	t.protocol = protocol
}

// reset 新任务开始：清指标、记原始提示词与起始时间、换 runId。
func (t *Trace) reset(originalPrompt *ai.Prompt) {
	t.metrics.Reset()
	t.originalPrompt = originalPrompt
	t.beginTimeMs = time.Now().UnixMilli()
	t.runId = newRunID()
}

func newRunID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return time.Now().Format("20060102150405")
	}
	return hex.EncodeToString(b)
}

// ---- AgentTrace 只读面 ----

// RunID 运行 id（惰性生成——空提示词恢复路径未 reset 时仍有值）。
func (t *Trace) RunID() string {
	if t.runId == "" {
		t.runId = newRunID()
	}
	return t.runId
}

// AgentName 智能体名。
func (t *Trace) AgentName() string { return t.agentName }

// BeginTimeMs 任务开始时间。
func (t *Trace) BeginTimeMs() int64 { return t.beginTimeMs }

// OriginalPrompt 原始提示词。
func (t *Trace) OriginalPrompt() *ai.Prompt { return t.originalPrompt }

// WorkingMemory 工作记忆（每次模型调用的消息按轮归档于此）。
func (t *Trace) WorkingMemory() *ai.Prompt { return t.workingMemory }

// Metrics 用量指标。
func (t *Trace) Metrics() *agent.Metrics { return t.metrics }

// ---- transient 访问 ----

// Config 本次绑定的智能体配置。
func (t *Trace) Config() *Config { return t.cfg }

// Options 本次运行选项。
func (t *Trace) Options() *Options { return t.options }

// Session 本次会话。
func (t *Trace) Session() agent.Session { return t.session }

// Protocol 本次团队协议（可 nil）。
func (t *Trace) Protocol() agent.TeamProtocol { return t.protocol }

// Context 会话上下文（session 缺省时 nil）。
func (t *Trace) Context() flow.Context {
	if t.session != nil {
		return t.session.Context()
	}
	return nil
}

// ---- 流式 ----

// HasStreamSink 是否有流出口。
func (t *Trace) HasStreamSink() bool {
	return t.options != nil && t.options.StreamSink() != nil
}

// IsStreamCancelled 流式订阅是否已取消（无出口视为 true——Java 同款；
// 有出口时以取消源 ctx 为准）。
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
