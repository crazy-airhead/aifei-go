// options.go 团队协作运行时选项（对标 team.TeamOptions + ModelOptionsAmend 的
// 合并面：supervisor 模型选项直接内嵌 ai.ChatOptions）。
package team

import (
	"context"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
)

// Options 团队运行时选项（熔断阈值、容错策略与拦截器链）。
type Options struct {
	ai.ChatOptions // supervisor 模型选项：Tools/ToolContext/Raw/HTTPCustomize/CacheControl

	// Interceptors 团队拦截器（覆写 ChatOptions 同名字段：此处要求团队全生命
	// 周期钩子面；team.Interceptor 内嵌 ai.Interceptor 与 flow.Interceptor，
	// 随请求/求值下探两层仍按序生效）。构建 supervisor 请求时逐个转回。
	Interceptors []Interceptor

	// Attrs 团队层键值属性。
	Attrs map[string]any

	// MaxTurns 最大协作回合数（Supervisor 指派次数上限；Java 缺省 8）。
	MaxTurns int
	// MaxRetries 调度模型最大重试次数（总尝试数）。
	MaxRetries int
	// RetryDelayMs 重试规避延迟（毫秒）。
	RetryDelayMs int64
	// SessionWindowSize 会话回溯窗口大小（0 关闭）。
	SessionWindowSize int
	// RecordWindowSize 记录回溯窗口大小。
	RecordWindowSize int

	// FeedbackMode 反馈模式（supervisor 允许主动寻求外部帮助/反馈）。
	FeedbackMode bool

	feedbackDescriptionProvider       func(*Trace) string
	feedbackReasonDescriptionProvider func(*Trace) string

	// talentInstruction 才能级指令（activeTalents 激活后写入；才能包体系随
	// 后续卡迁移，此处保留提示词追加强化的语义位）。
	talentInstruction string

	streamSink agent.EventSink  // transient：流式出口
	goCtx      context.Context // transient：流式取消源
}

// NewOptions 缺省选项（Java TeamOptions 构造缺省：8 回合 / 3 次重试 /
// 1000ms 延迟 / 窗口 8/8）。
func NewOptions() *Options {
	return &Options{
		MaxTurns:          8,
		MaxRetries:        3,
		RetryDelayMs:      1000,
		SessionWindowSize: 8,
		RecordWindowSize:  8,
	}
}

// Copy 拷贝（streamSink/goCtx 为运行时注入字段，不参与拷贝——Java 同款）。
func (o *Options) Copy() *Options {
	tmp := NewOptions()
	tmp.Attrs = map[string]any{}
	for k, v := range o.Attrs {
		tmp.Attrs[k] = v
	}
	// 模型选项（内嵌 ChatOptions 面）
	tmp.Tools = append([]ai.FunctionTool(nil), o.Tools...)
	tmp.Interceptors = append([]Interceptor(nil), o.Interceptors...)
	tmp.ToolContext = map[string]any{}
	for k, v := range o.ToolContext {
		tmp.ToolContext[k] = v
	}
	tmp.Raw = map[string]any{}
	for k, v := range o.Raw {
		tmp.Raw[k] = v
	}
	tmp.HTTPCustomize = o.HTTPCustomize
	tmp.CacheControl = o.CacheControl

	tmp.MaxTurns = o.MaxTurns
	tmp.MaxRetries = o.MaxRetries
	tmp.RetryDelayMs = o.RetryDelayMs
	tmp.SessionWindowSize = o.SessionWindowSize
	tmp.RecordWindowSize = o.RecordWindowSize

	tmp.FeedbackMode = o.FeedbackMode
	tmp.feedbackDescriptionProvider = o.feedbackDescriptionProvider
	tmp.feedbackReasonDescriptionProvider = o.feedbackReasonDescriptionProvider
	return tmp
}

// AttrPut 写 attr。
func (o *Options) AttrPut(key string, value any) *Options {
	if o.Attrs == nil {
		o.Attrs = map[string]any{}
	}
	o.Attrs[key] = value
	return o
}

// Attr 读 attr。
func (o *Options) Attr(key string) any { return o.Attrs[key] }

// ---- 流式（transient） ----

// SetStreamSink 绑定流出口。
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

// ---- 配置注入（钳制语义与 Java setter 一致） ----

// SetRetryConfig 重试参数（max>=1、delayMs>=1000——Java 钳制同款）。
func (o *Options) SetRetryConfig(maxRetries int, retryDelayMs int64) *Options {
	if maxRetries < 1 {
		maxRetries = 1
	}
	if retryDelayMs < 1000 {
		retryDelayMs = 1000
	}
	o.MaxRetries, o.RetryDelayMs = maxRetries, retryDelayMs
	return o
}

// SetMaxTurns 协作回合上限（>=1）。
func (o *Options) SetMaxTurns(maxTurns int) *Options {
	if maxTurns < 1 {
		maxTurns = 1
	}
	o.MaxTurns = maxTurns
	return o
}

// SetSessionWindowSize 会话回溯深度（>=0）。
func (o *Options) SetSessionWindowSize(n int) *Options {
	if n < 0 {
		n = 0
	}
	o.SessionWindowSize = n
	return o
}

// SetRecordWindowSize 记录回溯窗口。
func (o *Options) SetRecordWindowSize(n int) *Options {
	o.RecordWindowSize = n
	return o
}

// SetFeedbackMode 反馈模式开关。
func (o *Options) SetFeedbackMode(on bool) *Options {
	o.FeedbackMode = on
	return o
}

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

// FeedbackDescription 反馈说明（未配置时空串）。
func (o *Options) FeedbackDescription(trace *Trace) string {
	if o.feedbackDescriptionProvider == nil {
		return ""
	}
	return o.feedbackDescriptionProvider(trace)
}

// FeedbackReasonDescription 反馈原因说明（未配置时空串）。
func (o *Options) FeedbackReasonDescription(trace *Trace) string {
	if o.feedbackReasonDescriptionProvider == nil {
		return ""
	}
	return o.feedbackReasonDescriptionProvider(trace)
}
