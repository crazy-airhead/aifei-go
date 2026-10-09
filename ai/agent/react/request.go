package react

import (
	"context"
	"errors"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/flow"
)

// Request ReAct 请求构建器（对标 ReActRequest）：fluent 绑定会话与运行期选项。
// 线程不安全——每个请求由 Agent.Prompt 独立创建。
type Request struct {
	agent.RequestBase

	ag        *Agent
	prompt    *ai.Prompt
	options   *Options
	adjustors []func(*Options)
}

// Session 关联执行会话（持久化短期记忆与上下文快照）。
func (r *Request) Session(s agent.Session) *Request {
	r.RequestBase.Session(s)
	return r
}

// Options 追加运行时选项调整（可多次调用，按序生效）。
func (r *Request) Options(fn func(o *Options)) *Request {
	if fn != nil {
		r.adjustors = append(r.adjustors, fn)
	}
	return r
}

// orSession 会话缺省时降级临时内存会话（id "tmp"——Java of() 同款）。
func (r *Request) orSession() agent.Session {
	if s := r.SessionOf(); s != nil {
		return s
	}
	return agent.NewMemorySession("tmp")
}

// init 惰性初始化选项：续跑时从上次轨迹绑定的 options 拷贝（避免在同一实例上
// 叠加 interceptor/streamSink），否则取默认选项副本；最后应用请求级调整。
func (r *Request) init(session agent.Session) *Options {
	if r.options != nil {
		return r.options
	}
	options := r.ag.cfg.DefaultOptions.Copy()
	if trace, ok := session.Context().Get(r.ag.cfg.TraceKey()).(*Trace); ok && trace != nil && trace.Options() != nil {
		options = trace.Options().Copy()
	}
	for _, fn := range r.adjustors {
		fn(options)
	}
	r.options = options
	return options
}

// Call 执行同步调用：阻塞直至推理完成或超时。
func (r *Request) Call() (*Response, error) {
	session := r.orSession()
	options := r.init(session)

	message, err := r.ag.call(r.prompt, session, options)
	if err != nil {
		return nil, err
	}
	return &Response{session: session, trace: r.ag.getTrace(session.Context()), message: message}, nil
}

// Stream 启动流式调用：过程事件经 sink 转发（ReasonStart/ReasonDelta/ToolCall*/
// Action*…），结束后补发 RunEnd 终态（未取消时）。ctx 取消会中止模型流并映射
// 为空 assistant（Java sink.interrupt 同款语义）。
func (r *Request) Stream(ctx context.Context, sink agent.EventSink) error {
	if sink == nil {
		return errors.New("react: stream requires a sink")
	}
	session := r.orSession()
	options := r.init(session)
	options.SetStreamSink(sink)
	if ctx == nil {
		ctx = context.Background()
	}
	options.setGoCtx(ctx)

	message, err := r.ag.call(r.prompt, session, options)
	if err != nil {
		return err
	}
	trace := r.ag.getTrace(session.Context())
	if !trace.IsStreamCancelled() {
		resp := &Response{session: session, trace: trace, message: message}
		ev := trace.event(agent.KindRunEnd)
		ev.Message = message
		ev.Text = message.Text
		ev.Meta["abnormal"] = trace.IsAbnormal()
		ev.Meta["response"] = resp
		trace.PushAgentEvent(ev)
	}
	return nil
}

// StreamChan channel 形态的流式调用（转发全部事件；Stream 返回即关闭）。
func (r *Request) StreamChan(ctx context.Context) (<-chan *agent.Event, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ch := make(chan *agent.Event, 16)
	err := r.Stream(ctx, agent.EventSinkFunc(func(ev *agent.Event) error {
		select {
		case ch <- ev:
		case <-ctx.Done(): // 消费方停止：丢弃后续（PushAgentEvent 吞错）
		}
		return nil
	}))
	close(ch)
	return ch, err
}

// Response ReAct 响应（对标 ReActResponse）。
type Response struct {
	session agent.Session
	trace   *Trace
	message *ai.AssistantMessage
}

// Trace 运行轨迹。
func (r *Response) Trace() *Trace { return r.trace }

// Session 会话。
func (r *Response) Session() agent.Session { return r.session }

// Context 会话上下文。
func (r *Response) Context() flow.Context { return r.session.Context() }

// Metrics 用量指标。
func (r *Response) Metrics() *agent.Metrics { return r.trace.Metrics() }

// Message 终态消息。
func (r *Response) Message() *ai.AssistantMessage { return r.message }

// Content 文本正文。
func (r *Response) Content() string {
	if r.message == nil {
		return ""
	}
	return r.message.Text
}

// Bind 结构化输出反序列化（包级泛型 helper，双轨惯例）。
func Bind[T any](r *Response) (T, error) {
	var zero T
	if r == nil || r.message == nil {
		return zero, errors.New("react: nil response")
	}
	return ai.Bind[T](r.message)
}
