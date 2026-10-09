// Package simple 提供简单智能体（对标 solon-ai-agent 的 simple 子包）：
// 单次直接响应，具备指令增强、历史窗口管理、自动重试（仅传输错误）、
// JSON 结构化输出约束与流式转发。
package simple

import (
	"context"

	"github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
)

// Options Simple 运行时选项（对标 SimpleOptions：ChatOptions 基座 + attrs +
// transient 流出口；Copy 不拷贝 sink/ctx）。
type Options struct {
	ai.ChatOptions

	// Attrs Simple 层键值属性。
	Attrs map[string]any

	// SimpleInterceptors Simple 层拦截器（同时也是 ai.Interceptor，随请求
	// 进入 ChatOptions.Interceptors 参与 OnPrepare）。
	SimpleInterceptors []Interceptor

	streamSink agent.EventSink // transient
	goCtx      context.Context // transient：流式取消源
}

// Interceptor Simple 拦截器（对标 SimpleInterceptor：ai.Interceptor +
// OnSimpleStart/OnSimpleEnd）。
type Interceptor interface {
	ai.Interceptor
	OnSimpleStart(trace *Trace)
	OnSimpleEnd(trace *Trace)
}

// InterceptorBase 默认实现（恒启用、直通）。
type InterceptorBase struct {
	ai.InterceptorBase
}

func (InterceptorBase) OnSimpleStart(*Trace) {}
func (InterceptorBase) OnSimpleEnd(*Trace)   {}

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

// Copy 浅拷贝（slice/map 共享底层、sink 与 ctx 不拷贝——Java copy() 同款）。
func (o *Options) Copy() *Options {
	if o == nil {
		return &Options{}
	}
	out := &Options{}
	out.ChatOptions = o.ChatOptions
	out.Attrs = o.Attrs
	out.SimpleInterceptors = o.SimpleInterceptors
	return out
}

// SetStreamSink 设置流出口（transient，不随 Copy 传播）。
func (o *Options) SetStreamSink(sink agent.EventSink) *Options {
	o.streamSink = sink
	return o
}

// StreamSink 当前流出口（可 nil）。
func (o *Options) StreamSink() agent.EventSink { return o.streamSink }

// setGoCtx 绑定取消源（SimpleRequest.Stream 注入）。
func (o *Options) setGoCtx(c context.Context) { o.goCtx = c }

// goContext 取消源（未绑定时 nil）。
func (o *Options) goContext() context.Context { return o.goCtx }

// cancelled 取消源是否已取消（nil 视为未取消）。
func (o *Options) cancelled() bool { return o.goCtx != nil && o.goCtx.Err() != nil }
