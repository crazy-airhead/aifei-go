package react

import (
	"context"
	"strings"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
)

// Options ReAct 运行选项（对标 ReActOptions：ChatOptions 基座承载工具/
// 拦截器/Raw 参数，之上叠加 ReAct 运行参数；streamSink/goCtx 为 transient，
// 不随 Copy 传播——Java 同款）。
type Options struct {
	ai.ChatOptions

	// Interceptors ReAct 拦截器（覆写 ChatOptions 同名字段：这里要求全生命
	// 周期钩子面；react.Interceptor 内嵌 ai.Interceptor，故随请求下探 chat 层
	// 仍按序生效）。buildRequest 时显式并入 ChatOptions.Interceptors。
	Interceptors []Interceptor

	// Attrs ReAct 层键值属性。
	Attrs map[string]any

	// MaxTurns 初始最大推理回合数（防死循环；Java 缺省 8）。
	MaxTurns int
	// maxTurnsNew 被 AddMaxTurns 扩容后的值（>0 时生效上限取此值）。
	maxTurnsNew int

	// AutoRethink 自动反思：达 80% 回合提前注入自我反思提示并扩容回合。
	AutoRethink bool

	// MaxRetries Reason 请求最大重试次数（总尝试数）。
	MaxRetries int
	// RetryDelayMs 重试基础延迟（毫秒）。
	RetryDelayMs int64
	// SessionWindowSize 会话回溯窗口大小（0 关闭；Java 缺省 8）。
	SessionWindowSize int
	// OutputSchema 输出格式约束（JSON Schema 字符串）。
	OutputSchema string

	// FeedbackMode 反馈模式（追加 __ask_for_feedback 工具，缺失关键信息时挂起）。
	FeedbackMode bool
	// PlanningMode 规划模式（本卡仅保留选项位与轨迹计划状态）。
	PlanningMode bool

	feedbackDescriptionProvider       func(*Trace) string
	feedbackReasonDescriptionProvider func(*Trace) string
	planningInstructionProvider       func(*Trace) string

	talentInstruction string

	streamSink agent.EventSink // transient
	goCtx      context.Context // transient：流式取消源
}

// NewOptions 缺省选项（Java ReActOptions 构造缺省：8 回合 / 3 次重试 /
// 1000ms 延迟 / 窗口 8）。
func NewOptions() *Options {
	return &Options{
		MaxTurns:          8,
		MaxRetries:        3,
		RetryDelayMs:      1000,
		SessionWindowSize: 8,
	}
}

// AttrPut 写 attr。
func (o *Options) AttrPut(key string, value any) *Options {
	if o.Attrs == nil {
		o.Attrs = map[string]any{}
	}
	o.Attrs[key] = value
	return o
}

// AttrGet 读 attr。
func (o *Options) AttrGet(key string) any {
	if o.Attrs == nil {
		return nil
	}
	return o.Attrs[key]
}

// Copy 浅拷贝（slice 共享底层、sink 与 ctx 不拷贝；maxTurnsNew 与
// talentInstruction 不拷贝——Java copy() 同款）。
func (o *Options) Copy() *Options {
	if o == nil {
		return NewOptions()
	}
	out := NewOptions()
	out.ChatOptions = o.ChatOptions
	out.Interceptors = append([]Interceptor(nil), o.Interceptors...)
	if o.Attrs != nil {
		out.Attrs = map[string]any{}
		for k, v := range o.Attrs {
			out.Attrs[k] = v
		}
	}
	out.MaxTurns = o.MaxTurns
	out.AutoRethink = o.AutoRethink
	out.MaxRetries = o.MaxRetries
	out.RetryDelayMs = o.RetryDelayMs
	out.SessionWindowSize = o.SessionWindowSize
	out.OutputSchema = o.OutputSchema
	out.FeedbackMode = o.FeedbackMode
	out.feedbackDescriptionProvider = o.feedbackDescriptionProvider
	out.feedbackReasonDescriptionProvider = o.feedbackReasonDescriptionProvider
	out.PlanningMode = o.PlanningMode
	out.planningInstructionProvider = o.planningInstructionProvider
	return out
}

// SetStreamSink 设置流出口（transient）。
func (o *Options) SetStreamSink(sink agent.EventSink) *Options {
	o.streamSink = sink
	return o
}

// StreamSink 当前流出口（可 nil）。
func (o *Options) StreamSink() agent.EventSink { return o.streamSink }

// setGoCtx 绑定取消源（Request.Stream 注入）。
func (o *Options) setGoCtx(c context.Context) { o.goCtx = c }

// goContext 取消源（未绑定时 nil）。
func (o *Options) goContext() context.Context { return o.goCtx }

// cancelled 取消源是否已取消。
func (o *Options) cancelled() bool { return o.goCtx != nil && o.goCtx.Err() != nil }

// ---- 配置注入 ----

// SetRetryConfig 重试参数（max>=1、delayMs>=500——Java 钳制同款）。
func (o *Options) SetRetryConfig(maxRetries int, retryDelayMs int64) *Options {
	if maxRetries < 1 {
		maxRetries = 1
	}
	if retryDelayMs < 500 {
		retryDelayMs = 500
	}
	o.MaxRetries, o.RetryDelayMs = maxRetries, retryDelayMs
	return o
}

// SetSessionWindowSize 会话回溯深度（<=0 关闭）。
func (o *Options) SetSessionWindowSize(n int) *Options {
	if n < 0 {
		n = 0
	}
	o.SessionWindowSize = n
	return o
}

// SetMaxTurns 设置初始回合上限。
func (o *Options) SetMaxTurns(n int) *Options {
	o.MaxTurns = n
	return o
}

// AddMaxTurns 扩容回合上限（autoRethink 续航；Java maxTurnsNew 同款记账）。
func (o *Options) AddMaxTurns(v int) {
	if o.maxTurnsNew > 0 {
		o.maxTurnsNew += v
	} else {
		o.maxTurnsNew = o.MaxTurns + v
	}
}

// SetAutoRethink 自动反思开关。
func (o *Options) SetAutoRethink(b bool) *Options { o.AutoRethink = b; return o }

// SetOutputSchema 输出 schema。
func (o *Options) SetOutputSchema(s string) *Options { o.OutputSchema = s; return o }

// SetPlanningMode 规划模式开关。
func (o *Options) SetPlanningMode(b bool) *Options { o.PlanningMode = b; return o }

// SetPlanningInstructionProvider 规划指令提供者。
func (o *Options) SetPlanningInstructionProvider(fn func(*Trace) string) *Options {
	o.planningInstructionProvider = fn
	return o
}

// SetFeedbackMode 反馈模式开关。
func (o *Options) SetFeedbackMode(b bool) *Options { o.FeedbackMode = b; return o }

// SetFeedbackDescriptionProvider 反馈说明提供者。
func (o *Options) SetFeedbackDescriptionProvider(fn func(*Trace) string) *Options {
	o.feedbackDescriptionProvider = fn
	return o
}

// SetFeedbackReasonDescriptionProvider 反馈原因说明提供者。
func (o *Options) SetFeedbackReasonDescriptionProvider(fn func(*Trace) string) *Options {
	o.feedbackReasonDescriptionProvider = fn
	return o
}

func (o *Options) setTalentInstruction(s string) { o.talentInstruction = s }

// ---- 参数读取 ----

// InitialMaxTurns 初始回合上限。
func (o *Options) InitialMaxTurns() int { return o.MaxTurns }

// EffectiveMaxTurns 当前生效回合上限（被扩容后取新值）。
func (o *Options) EffectiveMaxTurns() int {
	if o.maxTurnsNew > 0 {
		return o.maxTurnsNew
	}
	return o.MaxTurns
}

// TalentInstruction 才能指令（activeTalents 渲染）。
func (o *Options) TalentInstruction() string { return o.talentInstruction }

// PlanningInstruction 规划指令（无提供者时空串）。
func (o *Options) PlanningInstruction(trace *Trace) string {
	if o.planningInstructionProvider == nil {
		return ""
	}
	return o.planningInstructionProvider(trace)
}

// FeedbackDescription 反馈说明。
func (o *Options) FeedbackDescription(trace *Trace) string {
	if o.feedbackDescriptionProvider == nil {
		return ""
	}
	return o.feedbackDescriptionProvider(trace)
}

// FeedbackReasonDescription 反馈原因说明。
func (o *Options) FeedbackReasonDescription(trace *Trace) string {
	if o.feedbackReasonDescriptionProvider == nil {
		return ""
	}
	return o.feedbackReasonDescriptionProvider(trace)
}

// Interceptor ReAct 拦截器（对标 ReActInterceptor：ai.Interceptor（chat 层
// OnPrepare，随请求进入 ChatOptions.Interceptors）+ 全生命周期钩子。Java 的
// deprecated 钩子（onThought/onAction/onObservation——4.0.4 已由 onReasonEnd/
// onToolCallStart/onToolCallEnd 取代且无本卡内消费方）不迁移）。
type Interceptor interface {
	ai.Interceptor

	// OnAgentStart 智能体开始执行前。
	OnAgentStart(trace *Trace)
	// OnReasonStart Reason 开始前（systemPrompt 组装中段；上下文压缩位）。
	OnReasonStart(trace *Trace, systemPromptBuf *strings.Builder)
	// OnReasonRetry Reason 请求失败即将重试时；返回 true 表示已重建上下文。
	OnReasonRetry(trace *Trace, err error, attempt int, systemPrompt string) bool
	// OnReasonEnd 接收 LLM 返回的原始推理消息。
	OnReasonEnd(trace *Trace, resp *ai.Response, message *ai.AssistantMessage, durationMs int64)
	// OnPlan 计划节点消息（规划模式后续卡启用；接口先行固化）。
	OnPlan(trace *Trace, message *ai.AssistantMessage)
	// OnActionStart Action 开始（本轮全部 exchanger 就绪后）。
	OnActionStart(trace *Trace, toolCalls []*ToolExchanger)
	// OnToolCallStart 单个工具调用前（权限控制/参数预检位）。
	OnToolCallStart(trace *Trace, exchanger *ToolExchanger)
	// OnToolCallEnd 工具执行完成后（100% 强闭环；挂起/中断时 observation 为 nil）。
	OnToolCallEnd(trace *Trace, exchanger *ToolExchanger, observation *ai.Message, err error, durationMs int64)
	// OnActionEnd Action 结束（不挂起时才触发）。
	OnActionEnd(trace *Trace, toolCalls []*ToolExchanger)
	// OnAgentEnd 任务结束（成功或异常中止）。
	OnAgentEnd(trace *Trace)
}

// InterceptorBase 默认实现（恒启用、直通；嵌入者按需覆写——对标
// AbsReActInterceptor 的 enabled 开关以零值=启用表达）。
type InterceptorBase struct {
	ai.InterceptorBase

	disabled bool
}

// SetEnabled 启/停拦截器。
func (b *InterceptorBase) SetEnabled(on bool) { b.disabled = !on }

// IsEnabled 是否启用（零值启用）。
func (b *InterceptorBase) IsEnabled() bool { return !b.disabled }

func (InterceptorBase) OnAgentStart(*Trace)                                             {}
func (InterceptorBase) OnReasonStart(*Trace, *strings.Builder)                          {}
func (InterceptorBase) OnReasonRetry(*Trace, error, int, string) bool                   { return false }
func (InterceptorBase) OnReasonEnd(*Trace, *ai.Response, *ai.AssistantMessage, int64)   {}
func (InterceptorBase) OnPlan(*Trace, *ai.AssistantMessage)                             {}
func (InterceptorBase) OnActionStart(*Trace, []*ToolExchanger)                          {}
func (InterceptorBase) OnToolCallStart(*Trace, *ToolExchanger)                          {}
func (InterceptorBase) OnToolCallEnd(*Trace, *ToolExchanger, *ai.Message, error, int64) {}
func (InterceptorBase) OnActionEnd(*Trace, []*ToolExchanger)                            {}
func (InterceptorBase) OnAgentEnd(*Trace)                                               {}
