package simple

import (
	"context"
	"errors"

	"github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/flow"
)

// Request Simple 请求构建器（对标 SimpleRequest：fluent 绑定会话与运行期选项）。
type Request struct {
	agent.RequestBase

	ag      *Agent
	prompt  *ai.Prompt
	options *Options
}

// Session 关联执行会话（持久化短期记忆与上下文快照）。
func (r *Request) Session(s agent.Session) *Request {
	r.RequestBase.Session(s)
	return r
}

// Options 运行时选项调整（温度、工具、拦截器等）。
func (r *Request) Options(fn func(o *Options)) *Request {
	if fn != nil {
		fn(r.options)
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

// Call 启动调用流程（非流式）。
func (r *Request) Call() (*Response, error) {
	session := r.orSession()
	message, err := r.ag.call(r.prompt, session, r.options)
	if err != nil {
		return nil, err
	}
	return &Response{session: session, trace: r.ag.getTrace(session.Context()), message: message}, nil
}

// Stream 启动流式调用：过程事件经 sink 转发（SimpleStart/SimpleDelta…），
// 结束后补发 SimpleEnd 终态（未取消时）。ctx 取消会中止模型流（sink 链
// 传递），最终结果为空 assistant——Java 线程中断同款语义。
func (r *Request) Stream(ctx context.Context, sink agent.EventSink) error {
	if sink == nil {
		return errors.New("simple: stream requires a sink")
	}
	session := r.orSession()
	r.options.SetStreamSink(sink)
	if ctx == nil {
		ctx = context.Background()
	}
	r.options.setGoCtx(ctx)

	message, err := r.ag.call(r.prompt, session, r.options)
	if err != nil {
		return err
	}
	trace := r.ag.getTrace(session.Context())
	if !trace.IsStreamCancelled() {
		trace.PushAgentEvent(&agent.Event{
			Kind: agent.KindSimpleEnd, RunID: trace.RunID(), AgentName: r.ag.cfg.Name,
			Session: session, Trace: trace, Message: message,
		})
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

// Response Simple 响应（对标 SimpleResponse）。
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
		return zero, errors.New("simple: nil response")
	}
	return ai.Bind[T](r.message)
}
