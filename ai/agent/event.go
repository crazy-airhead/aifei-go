package agent

import (
	"github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/flow"
)

// EventKind 智能体事件类别（对标 Java agent 包各 *Event 类型；03-agent-design
// 事件模型统一枚举，P2-b/P2-c/P2-d 各卡逐步启用）。
type EventKind uint8

const (
	// Simple 家族（SimpleAgent）
	KindSimpleStart EventKind = iota + 1
	KindSimpleDelta
	KindSimpleEnd

	// Run/Reason（ReAct，P2-b）
	KindRunStart
	KindRunEnd
	KindReasonStart
	KindReasonDelta
	KindReasonEnd

	// 工具/动作（ReAct，P2-b）
	KindToolCallStart
	KindToolCallEnd
	KindActionStart
	KindActionEnd

	// 节点（AgentBase 默认 run）
	KindNodeStart
	KindNodeEnd

	// Team（P2-c）
	KindTeamStart
	KindTeamEnd
	KindPlan

	// 观测
	KindContextSize

	// HITL（P2-d）
	KindHITLPending
	KindHITLDecided

	// Supervisor（P2-c）
	KindSupervisorDelta
)

// String 事件类别名。
func (k EventKind) String() string {
	switch k {
	case KindSimpleStart:
		return "SimpleStart"
	case KindSimpleDelta:
		return "SimpleDelta"
	case KindSimpleEnd:
		return "SimpleEnd"
	case KindRunStart:
		return "RunStart"
	case KindRunEnd:
		return "RunEnd"
	case KindReasonStart:
		return "ReasonStart"
	case KindReasonDelta:
		return "ReasonDelta"
	case KindReasonEnd:
		return "ReasonEnd"
	case KindToolCallStart:
		return "ToolCallStart"
	case KindToolCallEnd:
		return "ToolCallEnd"
	case KindActionStart:
		return "ActionStart"
	case KindActionEnd:
		return "ActionEnd"
	case KindNodeStart:
		return "NodeStart"
	case KindNodeEnd:
		return "NodeEnd"
	case KindTeamStart:
		return "TeamStart"
	case KindTeamEnd:
		return "TeamEnd"
	case KindPlan:
		return "Plan"
	case KindContextSize:
		return "ContextSize"
	case KindHITLPending:
		return "HITLPending"
	case KindHITLDecided:
		return "HITLDecided"
	case KindSupervisorDelta:
		return "SupervisorDelta"
	}
	return "Unknown"
}

// Event 智能体事件（统一载体：对标 Java AbsAgentEvent 及其子类的并集面）。
// 各类别按需读取对应字段：SimpleDelta 带 ChatEvent（流式转发）、SimpleEnd 带
// Message（终态聚合）、Node 事件带 Meta{nodeId,nodeTitle}。
type Event struct {
	Kind      EventKind
	RunID     string
	AgentName string
	Text      string
	Session   Session
	Trace     Trace
	ChatEvent *ai.ChatEvent        // KindSimpleDelta 载荷
	Message   *ai.AssistantMessage // 终态聚合消息
	Meta      map[string]any
}

// NodeStartEventOf 节点开始事件（Meta 携带 nodeId/nodeTitle）。
func NodeStartEventOf(node *flow.Node, trace TeamTrace) *Event {
	ev := &Event{Kind: KindNodeStart, Meta: map[string]any{}}
	if node != nil {
		ev.Meta["nodeId"] = node.ID()
		ev.Meta["nodeTitle"] = node.Title()
	}
	if trace != nil {
		ev.RunID, ev.AgentName, ev.Trace = trace.RunID(), trace.LastAgentName(), trace
	}
	return ev
}

// NodeEndEventOf 节点结束事件（携带终态消息）。
func NodeEndEventOf(node *flow.Node, trace TeamTrace, msg *ai.AssistantMessage) *Event {
	ev := NodeStartEventOf(node, trace)
	ev.Kind = KindNodeEnd
	ev.Message = msg
	if msg != nil {
		ev.Text = msg.Text
	}
	return ev
}

// EventSink 智能体事件出口。
type EventSink interface {
	OnEvent(*Event) error
}

// EventSinkFunc 函数适配。
type EventSinkFunc func(*Event) error

func (f EventSinkFunc) OnEvent(ev *Event) error { return f(ev) }
