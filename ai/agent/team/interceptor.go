// interceptor.go 团队协作拦截器（对标 team.TeamInterceptor：Java 一接口实现
// Agent/Flow/Chat 三接口；Go 以单一接口内嵌 ai.Interceptor + flow.Interceptor，
// 同一拦截器对象在 chat 层与 flow 层均按序生效）。
package team

import (
	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/flow"
)

// Interceptor 团队协作拦截器：团队（OnTeamStart/End）、决策（supervisor 准入/
// 模型前后/决策后置）、成员（agent 准入/结束）三维切面 + flow 节点回调。
type Interceptor interface {
	ai.Interceptor   // chat 层（IsEnabled + OnPrepare）
	flow.Interceptor // flow 层（InterceptFlow/OnNodeStart/OnNodeEnd）

	Name() string

	// ---- [维度 1：团队级] ----
	OnTeamStart(trace *Trace)
	OnTeamEnd(trace *Trace)

	// ---- [维度 2：决策级（Supervisor）] ----
	// ShouldSupervisorContinue 决策准入（false = 熔断并中止协作）。
	ShouldSupervisorContinue(trace *Trace) bool
	// OnModelStart 模型请求前置（LLM 调用前）。
	OnModelStart(trace *Trace, req ai.RequestDesc)
	// OnModelEnd 模型响应后置（LLM 返回后、解析前）。
	OnModelEnd(trace *Trace, resp *ai.Response)
	// OnSupervisorDecision 决策结果输出（指令解析后）。
	OnSupervisorDecision(trace *Trace, decision string)

	// ---- [维度 3：成员级（Agent）] ----
	// ShouldAgentContinue 成员执行准入（false = 跳过并回滚至决策层）。
	ShouldAgentContinue(trace *Trace, ag agent.Agent) bool
	// OnAgentEnd 成员执行结束。
	OnAgentEnd(trace *Trace, ag agent.Agent)
}

// InterceptorBase 默认实现（恒启用、全直通），嵌入者按需覆写。
type InterceptorBase struct {
	ai.InterceptorBase

	disabled bool
}

// SetEnabled 启/停拦截器。
func (b *InterceptorBase) SetEnabled(on bool) { b.disabled = !on }

// IsEnabled 是否启用（零值启用）。
func (b *InterceptorBase) IsEnabled() bool { return !b.disabled }

func (InterceptorBase) OnTeamStart(*Trace)                                  {}
func (InterceptorBase) OnTeamEnd(*Trace)                                    {}
func (InterceptorBase) ShouldSupervisorContinue(*Trace) bool                { return true }
func (InterceptorBase) OnModelStart(*Trace, ai.RequestDesc)                 {}
func (InterceptorBase) OnModelEnd(*Trace, *ai.Response)                     {}
func (InterceptorBase) OnSupervisorDecision(*Trace, string)                 {}
func (InterceptorBase) ShouldAgentContinue(*Trace, agent.Agent) bool        { return true }
func (InterceptorBase) OnAgentEnd(*Trace, agent.Agent)                      {}

// agentInterceptorAdapter 窄面适配：AgentBase.Run 经 agent.TeamTrace.Interceptors()
// 取到的成员级拦截器视图（*Trace 下探；非 team.Trace 时放行/直通）。
type agentInterceptorAdapter struct{ ic Interceptor }

func (a agentInterceptorAdapter) Name() string    { return a.ic.Name() }
func (a agentInterceptorAdapter) IsEnabled() bool { return a.ic.IsEnabled() }

func (a agentInterceptorAdapter) ShouldAgentContinue(trace agent.TeamTrace, ag agent.Agent) bool {
	if t, ok := trace.(*Trace); ok {
		return a.ic.ShouldAgentContinue(t, ag)
	}
	return true
}

func (a agentInterceptorAdapter) OnAgentEnd(trace agent.TeamTrace, ag agent.Agent) {
	if t, ok := trace.(*Trace); ok {
		a.ic.OnAgentEnd(t, ag)
	}
}

// interceptorsAsAgent 窄面视图（供 Trace.Interceptors 缝）。
func interceptorsAsAgent(ics []Interceptor) []agent.TeamInterceptor {
	out := make([]agent.TeamInterceptor, 0, len(ics))
	for _, ic := range ics {
		if ic != nil {
			out = append(out, agentInterceptorAdapter{ic: ic})
		}
	}
	return out
}

// interceptorsAsChat chat 层视图（ai.Interceptor 面；逐元素转回——
// []Interceptor 与 []ai.Interceptor 之间 variadic spread 不合法）。
func interceptorsAsChat(ics []Interceptor) []ai.Interceptor {
	out := make([]ai.Interceptor, 0, len(ics))
	for _, ic := range ics {
		if ic != nil {
			out = append(out, ic)
		}
	}
	return out
}

// interceptorsAsFlow flow 层视图。
func interceptorsAsFlow(ics []Interceptor) []flow.Interceptor {
	out := make([]flow.Interceptor, 0, len(ics))
	for _, ic := range ics {
		if ic != nil {
			out = append(out, ic)
		}
	}
	return out
}
