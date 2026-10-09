package agent

// RequestBase 请求构建器基座（对标 AgentRequest：持有会话，由各智能体的
// Request 内嵌并补齐自身选项面）。
type RequestBase struct {
	session Session
}

// Session 设置会话并返回自身（嵌入者以窄化返回类型转发）。
func (r *RequestBase) Session(s Session) *RequestBase {
	r.session = s
	return r
}

// SessionOf 当前会话（可 nil）。
func (r *RequestBase) SessionOf() Session { return r.session }
