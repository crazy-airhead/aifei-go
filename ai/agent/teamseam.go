package agent

import (
	"strings"

	"github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/flow"
)

// TeamTrace 团队协作轨迹（结构化缝，对标 team.TeamTrace 的 AgentBase 可见面；
// P2-c 的 team 包类型将满足本接口。AgentBase 默认 run 只依赖此面，P2-a 即可
// 用桩驱动全部路径）。
type TeamTrace interface {
	Trace // RunID/AgentName/BeginTimeMs/OriginalPrompt/WorkingMemory/Metrics

	SetLastAgentName(name string)
	LastAgentName() string

	Protocol() TeamProtocol
	Interceptors() []TeamInterceptor

	HasStreamSink() bool
	StreamSink() EventSink
	// PushAgentEvent 推送实时事件（无 sink 或已取消时静默丢弃，错误吞掉——
	// Java pushAgentEvent 同款，事件通道永不影响主流程）。
	PushAgentEvent(ev *Event)

	// AddRecord 记录一条智能体产出（role/agentName/content/耗时毫秒）。
	AddRecord(role ai.Role, agentName, content string, durationMs int64)
}

// TeamProtocol 协议后处理缝（对标 team 包各 *Protocol 的 AgentBase 可见面）。
type TeamProtocol interface {
	// PrepareAgentPrompt 组装智能体生效提示词（nil 表示无——由智能体自定路径）。
	PrepareAgentPrompt(trace TeamTrace, ag Agent, workingMemory *ai.Prompt) *ai.Prompt
	// InjectAgentInstruction 向系统提示构建器注入协作指令。
	InjectAgentInstruction(ctx flow.Context, ag Agent, sb *strings.Builder)
	// InjectAgentTools 注入协议工具。
	InjectAgentTools(ctx flow.Context, ag Agent, add func(ai.FunctionTool))
	// ResolveAgentOutput 解析智能体产出（协议后处理；空串表示未产出）。
	ResolveAgentOutput(trace TeamTrace, ag Agent, content string) string
	// OnAgentEnd 智能体执行后置回调。
	OnAgentEnd(trace TeamTrace, ag Agent)
}

// TeamInterceptor 团队拦截器（对标 team.TeamInterceptor 的协作面）。
type TeamInterceptor interface {
	Name() string
	IsEnabled() bool
	// ShouldAgentContinue false = 跳过该智能体（记录 [Skipped] 后直接返回）。
	ShouldAgentContinue(trace TeamTrace, ag Agent) bool
	OnAgentEnd(trace TeamTrace, ag Agent)
}

// CurrentTeamTrace 从流上下文解析当前团队轨迹（无则 nil）。
func CurrentTeamTrace(ctx flow.Context) TeamTrace {
	if ctx == nil {
		return nil
	}
	traceKey, _ := ctx.Get(KeyCurrentTeamTraceKey).(string)
	if traceKey == "" {
		return nil
	}
	trace, _ := ctx.Get(traceKey).(TeamTrace)
	return trace
}

// ProtocolOf 从流上下文解析协议（KeyProtocol；无则 nil）。
func ProtocolOf(ctx flow.Context) TeamProtocol {
	if ctx == nil {
		return nil
	}
	p, _ := ctx.Get(KeyProtocol).(TeamProtocol)
	return p
}
