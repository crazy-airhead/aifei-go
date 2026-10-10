// aguiadapt.go agent.Event → agui.AgentEventAdapter 适配器（Go 版的
// 「反射解耦」对应物：ui 层不 import agent，agent 侧自带适配）。
package agent

import (
	"context"
	"io"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/ui/agui"
)

// AGUIEvent agent 事件的 AG-UI 适配视图（组合而非类型重定义——Event 的同名字段
// 会与方法冲突）。
type AGUIEvent struct {
	*Event
}

// KindName 类别名（Go 统一枚举名——Java 简单名的 Event 后缀在 Go 收敛）。
func (e *AGUIEvent) KindName() string { return e.Kind.String() }

// RunID 运行标识（遮蔽 Event 字段为方法）。
func (e *AGUIEvent) RunID() string { return e.Event.RunID }

// AgentName 代理名。
func (e *AGUIEvent) AgentName() string { return e.Event.AgentName }

// Text 文本。
func (e *AGUIEvent) Text() string { return e.Event.Text }

// Thinking 思考位（SupervisorDelta 载荷）。
func (e *AGUIEvent) Thinking() bool {
	return e.Event.Meta != nil && e.Event.Meta["thinking"] == true
}

// ChatEvent 内嵌聊天事件。
func (e *AGUIEvent) ChatEvent() *ai.ChatEvent { return e.Event.ChatEvent }

// CallID 工具调用 id。
func (e *AGUIEvent) CallID() string {
	s, _ := e.Event.Meta["callId"].(string)
	return s
}

// ToolName 工具名。
func (e *AGUIEvent) ToolName() string {
	s, _ := e.Event.Meta["toolName"].(string)
	return s
}

// Response 终态响应。
func (e *AGUIEvent) Response() any { return e.Event.Message }

// Abnormal 异常终态位。
func (e *AGUIEvent) Abnormal() bool {
	return e.Event.Meta != nil && e.Event.Meta["abnormal"] == true
}

// AGUIOf 构造适配视图。
func AGUIOf(ev *Event) *AGUIEvent {
	if ev == nil {
		return nil
	}
	return &AGUIEvent{Event: ev}
}

// AGUIWriteStream agent 事件流 → AG-UI SSE 的便捷入口（react/team 的流式
// sink 直接消费）。
func AGUIWriteStream(ctx context.Context, out io.Writer, threadID, runID string, next func(emit func(agui.AgentEventAdapter)) error) error {
	return agui.WriteAgentStream(ctx, out, threadID, runID, next)
}

var _ agui.AgentEventAdapter = (*AGUIEvent)(nil)
