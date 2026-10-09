package ai

import "strings"

// Interceptor 准备期拦截（对标 ChatInterceptor 的 onPrepare）：系统提示组装
// 中段介入，可追加指令、改写 options、标注 prompt。
type Interceptor interface {
	IsEnabled() bool
	OnPrepare(session Session, opts *ChatOptions, p *Prompt, system *strings.Builder)
}

// InterceptorBase 默认实现（恒启用、直通），嵌入者按需覆写。
type InterceptorBase struct{}

func (InterceptorBase) IsEnabled() bool                                            { return true }
func (InterceptorBase) OnPrepare(Session, *ChatOptions, *Prompt, *strings.Builder) {}

// CallInterceptor 调用期拦截（可选能力，类型断言启用）：包装整轮模型调用
// （鉴权、审计、重试等）。未实现者直通。
type CallInterceptor interface {
	Interceptor
	InterceptCall(req *CallRequest, chain *CallChain) (*Response, error)
}

// ToolInterceptor 工具级拦截（可选能力）：包装工具执行。
type ToolInterceptor interface {
	Interceptor
	InterceptTool(req *ToolRequest, chain *ToolChain) *ToolResult
}

// CallRequest 一次模型调用（拦截器可见面；对标 ChatRequest 的只读面）。
type CallRequest struct {
	cfg      *Config
	options  *ChatOptions
	session  Session
	messages []*Message // finalPrompt：system（若有）+ 会话历史
	isStream bool
}

func (r *CallRequest) Config() *Config       { return r.cfg }
func (r *CallRequest) Options() *ChatOptions { return r.options }
func (r *CallRequest) Session() Session      { return r.session }
func (r *CallRequest) Messages() []*Message  { return r.messages }
func (r *CallRequest) IsStream() bool        { return r.isStream }

// ToolRequest 工具执行请求（对标 Java ToolRequest）。
type ToolRequest struct {
	call        *CallRequest
	toolContext map[string]any
	args        map[string]any
}

func (r *ToolRequest) Call() *CallRequest          { return r.call }
func (r *ToolRequest) ToolContext() map[string]any { return r.toolContext }
func (r *ToolRequest) Args() map[string]any        { return r.args }

// CallChain 调用拦截链。
type CallChain struct {
	interceptors []Interceptor
	pos          int
	terminal     func(*CallRequest) (*Response, error)
}

func newCallChain(list []Interceptor, terminal func(*CallRequest) (*Response, error)) *CallChain {
	return &CallChain{interceptors: append([]Interceptor(nil), list...), terminal: terminal}
}

// DoIntercept 依次经过启用的 CallInterceptor，终点为真实调用。
func (c *CallChain) DoIntercept(req *CallRequest) (*Response, error) {
	for c.pos < len(c.interceptors) {
		i := c.interceptors[c.pos]
		c.pos++
		if !i.IsEnabled() {
			continue
		}
		if ci, ok := i.(CallInterceptor); ok {
			return ci.InterceptCall(req, c)
		}
	}
	return c.terminal(req)
}

// ToolChain 工具拦截链。
type ToolChain struct {
	interceptors []Interceptor
	pos          int
	terminal     func(*ToolRequest) *ToolResult
}

func newToolChain(list []Interceptor, terminal func(*ToolRequest) *ToolResult) *ToolChain {
	return &ToolChain{interceptors: append([]Interceptor(nil), list...), terminal: terminal}
}

// DoIntercept 依次经过启用的 ToolInterceptor，终点为工具执行。
func (c *ToolChain) DoIntercept(req *ToolRequest) *ToolResult {
	for c.pos < len(c.interceptors) {
		i := c.interceptors[c.pos]
		c.pos++
		if !i.IsEnabled() {
			continue
		}
		if ti, ok := i.(ToolInterceptor); ok {
			return ti.InterceptTool(req, c)
		}
	}
	return c.terminal(req)
}
