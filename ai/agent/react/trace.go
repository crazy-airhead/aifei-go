package react

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/flow"
)

func newRunID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return time.Now().Format("20060102150405")
	}
	return hex.EncodeToString(b)
}

// Trace ReAct 运行轨迹（对标 ReActTrace：状态机上下文——路由/FinalAnswer/
// 回合计数/工作记忆/计划/协议工具；config/options/session/protocol 为
// transient 绑定）。实现 agent.Trace（事件载体的 Trace 面）。
type Trace struct {
	cfg      *Config
	options  *Options
	session  agent.Session
	protocol agent.TeamProtocol

	// protocolToolMap 协议注入的专用工具映射表。
	protocolToolMap map[string]ai.FunctionTool

	agentName string
	runId     string
	// currentTurnId 当前回合 id（构造事件时快照进 Meta，每轮推理前重置）。
	currentTurnId string

	metrics        *agent.Metrics
	beginTimeMs    int64
	originalPrompt *ai.Prompt
	workingMemory  *ai.Prompt

	turnCounter       int
	toolCounter       int
	emptyRetryCounter int

	// route 逻辑路由（reason/action/end；构造时缺省 reason）。
	route string
	// finalAnswer 最终回答 + 是否已写（Java null vs "" 之辨）。
	finalAnswer    string
	hasFinalAnswer bool
	// abnormal 是否异常结束（只要不是由 ai 输出最终答案的都算异常）。
	abnormal          bool
	lastReasonMessage *ai.AssistantMessage
	// finalMediaBlocks returnDirect 工具产出的终态 media（终态收口合并）。
	finalMediaBlocks []ai.ContentBlock

	plans     []string
	planIndex int

	extras map[string]any
}

// NewTrace 空轨迹（路由缺省 reason；工作记忆/指标就绪）。
func NewTrace() *Trace {
	return &Trace{
		route:           IDReason,
		workingMemory:   ai.NewPrompt(),
		metrics:         &agent.Metrics{},
		protocolToolMap: map[string]ai.FunctionTool{},
		extras:          map[string]any{},
	}
}

// ---- 生命周期（对标 prepare/reset/activeTalents，包内使用） ----

// prepare 绑定 transient 引用；finalAnswer/异常态清零、空响应计数按本次执行
// 隔离（续跑不走 reset，沿用历史值会让首个偶发空响应跨过重试上限而误熔断）、
// 挂起态复位。
func (t *Trace) prepare(cfg *Config, options *Options, session agent.Session, protocol agent.TeamProtocol, agentName string) {
	t.cfg = cfg
	t.agentName = agentName
	t.options = options
	t.session = session
	t.protocol = protocol
	t.finalAnswer, t.hasFinalAnswer = "", false
	t.abnormal = false
	t.emptyRetryCounter = 0
	session.Pending(false, "")
}

// activeTalents 激活才能（工具上下文并入原始提示词 attrs；工具部署进
// options.Tools、指令聚合进 talentInstruction，由系统提示词收口注入）。
func (t *Trace) activeTalents() {
	if t.originalPrompt != nil && len(t.options.ToolContext) > 0 {
		for k, v := range t.options.ToolContext {
			t.originalPrompt.Attrs()[k] = v
		}
	}
	var sb strings.Builder
	ai.ActiveTalents(&t.options.ChatOptions, t.originalPrompt, &sb)
	if sb.Len() > 0 {
		t.options.setTalentInstruction(sb.String())
	}
}

// reset 新任务开始：计数器/路由/终态/计划/工作记忆/指标全量重置。
func (t *Trace) reset(originalPrompt *ai.Prompt) {
	t.turnCounter = 0
	t.toolCounter = 0
	t.emptyRetryCounter = 0

	t.route = IDReason
	t.finalAnswer, t.hasFinalAnswer = "", false
	t.lastReasonMessage = nil
	t.finalMediaBlocks = nil

	t.plans = nil
	t.planIndex = 0
	t.workingMemory.Clear()
	for k := range t.extras {
		delete(t.extras, k)
	}

	t.metrics.Reset()

	t.originalPrompt = originalPrompt
	t.beginTimeMs = time.Now().UnixMilli()
	t.runId = ""
	t.currentTurnId = ""
}

// ---- agent.Trace 只读面 ----

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

// WorkingMemory 工作记忆。
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

// ---- 协议工具 ----

// AddProtocolTool 注册协议内置工具。
func (t *Trace) AddProtocolTool(tool ai.FunctionTool) {
	if tool != nil {
		t.protocolToolMap[tool.Name()] = tool
	}
}

// ProtocolTool 按名取协议工具。
func (t *Trace) ProtocolTool(name string) ai.FunctionTool { return t.protocolToolMap[name] }

// ProtocolTools 全部协议工具。
func (t *Trace) ProtocolTools() []ai.FunctionTool {
	out := make([]ai.FunctionTool, 0, len(t.protocolToolMap))
	for _, v := range t.protocolToolMap {
		out = append(out, v)
	}
	return out
}

// ---- 回合与路由 ----

// CurrentTurnId 当前回合 id（不存在时懒生成）。
func (t *Trace) CurrentTurnId() string {
	if t.currentTurnId == "" {
		t.currentTurnId = newRunID()
	}
	return t.currentTurnId
}

// NewCurrentTurnId 开启新回合并返回新回合 id。
func (t *Trace) NewCurrentTurnId() string {
	t.currentTurnId = newRunID()
	return t.currentTurnId
}

// TurnCount 已消耗回合数。
func (t *Trace) TurnCount() int { return t.turnCounter }

// NextTurn 递增回合数。
func (t *Trace) NextTurn() int {
	t.turnCounter++
	return t.turnCounter
}

// Route 当前逻辑路由。
func (t *Trace) Route() string { return t.route }

// SetRoute 更新路由。
func (t *Trace) SetRoute(route string) { t.route = route }

// IsAbnormal 是否异常结束（只要不是由 ai 输出最终答案的都算异常）。
func (t *Trace) IsAbnormal() bool { return t.abnormal }

// FinalAnswer 最终回答（未写入时空串）。
func (t *Trace) FinalAnswer() string { return t.finalAnswer }

// HasFinalAnswer 是否已写入最终回答（Java null 之辨：挂起分支判空用）。
func (t *Trace) HasFinalAnswer() bool { return t.hasFinalAnswer }

// SetFinalAnswer 写入最终回答并标记异常结束（Java 单参重载同款——
// returnDirect / 熔断兜底文案走此路径，异常标记触发流式补位）。
func (t *Trace) SetFinalAnswer(answer string) {
	t.finalAnswer, t.hasFinalAnswer = answer, true
	t.abnormal = true
}

// SetFinalAnswerDetail 写入最终回答并显式指定异常标记。
func (t *Trace) SetFinalAnswerDetail(answer string, abnormal bool) {
	t.finalAnswer, t.hasFinalAnswer = answer, true
	t.abnormal = abnormal
}

// LastReasonMessage 模型最近一次原始推理消息。
func (t *Trace) LastReasonMessage() *ai.AssistantMessage { return t.lastReasonMessage }

// SetLastReasonMessage 记录最近一次推理消息。
func (t *Trace) SetLastReasonMessage(m *ai.AssistantMessage) { t.lastReasonMessage = m }

// FinalMediaBlocks returnDirect 工具产出的终态 media。
func (t *Trace) FinalMediaBlocks() []ai.ContentBlock { return t.finalMediaBlocks }

// SetFinalMediaBlocks 记录终态 media。
func (t *Trace) SetFinalMediaBlocks(blocks []ai.ContentBlock) { t.finalMediaBlocks = blocks }

// FormattedHistory 人性化历史记录格式（User/Assistant/Action/Observation 行）。
func (t *Trace) FormattedHistory() string {
	var sb strings.Builder
	for _, msg := range t.workingMemory.Messages() {
		switch c := msg.Content.(type) {
		case string:
			if msg.Role == ai.RoleUser {
				sb.WriteString("[User] " + c + "\n")
			}
		case *ai.AssistantMessage:
			if c.Text != "" {
				sb.WriteString("[Assistant] " + c.Text + "\n")
			}
			for _, call := range c.ToolCalls {
				sb.WriteString("[Action] " + call.Name + ": " + call.ArgumentsStr + "\n")
			}
		case *ai.ToolMessage:
			sb.WriteString("[Observation] " + c.ResultText() + "\n")
		}
	}
	return sb.String()
}

// IncrementToolCallCount 增加工具调用计数。
func (t *Trace) IncrementToolCallCount() { t.toolCounter++ }

// ToolCallCount 已触发的工具调用总数。
func (t *Trace) ToolCallCount() int { return t.toolCounter }

// ---- 计划 ----

// HasPlans 是否存在计划。
func (t *Trace) HasPlans() bool { return len(t.plans) > 0 }

// Plans 当前执行计划（只读拷贝）。
func (t *Trace) Plans() []string { return append([]string(nil), t.plans...) }

// SetPlans 设置或重置计划（仅规划模式生效；空行过滤、修剪）。
func (t *Trace) SetPlans(newPlans []string) {
	t.plans = nil
	if t.options != nil && t.options.PlanningMode {
		for _, p := range newPlans {
			p = strings.TrimSpace(p)
			if p != "" {
				t.plans = append(t.plans, p)
			}
		}
	}
}

// AddPlan 追加单条计划步骤。
func (t *Trace) AddPlan(step string) {
	step = strings.TrimSpace(step)
	if step != "" {
		t.plans = append(t.plans, step)
	}
}

// PlanIndex 当前计划游标。
func (t *Trace) PlanIndex() int { return t.planIndex }

// SetPlanIndex 设置计划游标。
func (t *Trace) SetPlanIndex(i int) { t.planIndex = i }

// FormattedPlans 格式化的计划文本（注入提示词用）。
func (t *Trace) FormattedPlans() string {
	if len(t.plans) == 0 {
		return ""
	}
	var sb strings.Builder
	for i, p := range t.plans {
		sb.WriteString(strconv.Itoa(i+1) + ". " + p + "\n")
	}
	return sb.String()
}

// PlanProgress 当前执行进度描述（模型参考）。
func (t *Trace) PlanProgress() string {
	if len(t.plans) == 0 {
		return ""
	}
	return "Total Plans: " + strconv.Itoa(len(t.plans)) + ", Current Turn: " + strconv.Itoa(t.TurnCount())
}

// ---- extras ----

// Extras 附加数据袋。
func (t *Trace) Extras() map[string]any { return t.extras }

// Extra 读附加数据。
func (t *Trace) Extra(key string) any { return t.extras[key] }

// SetExtra 写附加数据。
func (t *Trace) SetExtra(key string, val any) { t.extras[key] = val }

// ---- 流式 ----

// HasStreamSink 是否有流出口。
func (t *Trace) HasStreamSink() bool {
	return t.options != nil && t.options.StreamSink() != nil
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

// event 统一事件载体（快照当前回合 id 进 Meta——AbsReActEvent 同款）。
func (t *Trace) event(kind agent.EventKind) *agent.Event {
	return &agent.Event{
		Kind: kind, RunID: t.RunID(), AgentName: t.agentName,
		Session: t.session, Trace: t,
		Meta: map[string]any{"turnId": t.CurrentTurnId()},
	}
}
