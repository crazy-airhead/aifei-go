// agent_stream.go AG-UI 的 agent 事件流映射（对标 AgUiStreamWrapper.
// toAgUiAgentStream——Java 以反射解耦 agent 模块，Go 以最小能力接口表达：
// ai/agent.Event 经 agent 包内的 AGUIEvent 适配器满足 AgentEventAdapter）。
//
// 分支表（命中即返回，与 Java 同序）：
//  1. SupervisorDelta → 恒 CUSTOM("supervisor-delta")（即使携带 ChatEvent 也
//     不产出正文/推理事件——内部思考不污染前端流）；
//  2. 携带 ChatEvent 的 Delta 事件 → 复用 ChatEvent 映射状态机（同管线续播）；
//  3. ToolCallStart → TOOL_CALL_START（openTools 登记）；
//  4. ToolCallEnd → 条件 TOOL_CALL_END + 恒 TOOL_CALL_RESULT；
//  5. RunEnd/SimpleEnd/TeamEnd → 无输出，只记 result/agentError 终态；
//  6. 其余一切 → CUSTOM(name=事件类别名)。
package agui

import (
	"context"
	"fmt"
	"io"
	"strconv"

	ai "github.com/crazy-airhead/aifei-go/ai"
)

// AgentEventAdapter agent 事件的最小能力面（ui 层与 agent 模块解耦）。
type AgentEventAdapter interface {
	// KindName 事件类别名（Custom 事件的 name；如 "SupervisorDelta"/"ToolCallStart"）。
	KindName() string
	RunID() string
	AgentName() string
	Text() string
	// Thinking 是否思考增量（SupervisorDelta 分支消费）。
	Thinking() bool
	// ChatEvent 内嵌聊天事件（Delta 类；nil = 无）。
	ChatEvent() *ai.ChatEvent
	// CallID/ToolName 工具调用身份（ToolCallStart/End 分支）。
	CallID() string
	ToolName() string
	// Response/Abnormal 终态（RunEnd/SimpleEnd/TeamEnd 分支）。
	Response() any
	Abnormal() bool
}

// WriteAgentStream agent 事件流 → AG-UI SSE（对标 toAgUiAgentStream：RunStarted
// 首帧、逐事件分派、finishSuccess/finishError 收口）。next 内以 emit 推送事件。
func (w *Wrapper) WriteAgentStream(ctx context.Context, out io.Writer, threadID, runID string, next func(emit func(AgentEventAdapter)) error) error {
	if threadID == "" {
		threadID = w.threadID
	}
	if runID == "" {
		runID = w.runID
	}
	st := newAGState(&Wrapper{ids: w.ids, threadID: threadID, runID: runID}, out)
	st.emitRaw(NewRunStartedEvent(threadID, runID), nil)

	err := next(func(ev AgentEventAdapter) {
		if st.writeErr != nil {
			return
		}
		st.onAgentEvent(ev)
	})
	if st.writeErr != nil {
		return st.writeErr
	}
	st.closeAgentOut(err)
	return err
}

// 包级便捷入口。
func WriteAgentStream(ctx context.Context, out io.Writer, threadID, runID string, next func(emit func(AgentEventAdapter)) error) error {
	return NewWrapper(threadID, runID).WriteAgentStream(ctx, out, threadID, runID, next)
}

// onAgentEvent 分支映射主体。
func (st *agState) onAgentEvent(ev AgentEventAdapter) {
	name := ev.KindName()

	// 1. SupervisorDelta 优先（即使含 ChatEvent）
	if name == "SupervisorDelta" {
		payload := map[string]any{
			"agentEventType": name,
			"runId":          ev.RunID(),
			"agentName":      ev.AgentName(),
			"thinking":       ev.Thinking(),
		}
		if s := ev.Text(); s != "" {
			payload["text"] = s
		}
		if ce := ev.ChatEvent(); ce != nil {
			payload["chatEvent"] = chatEventPayload(ce)
		}
		st.emit(NewCustomEvent("supervisor-delta", payload))
		return
	}

	// 2. 携带 ChatEvent 的 Delta → 复用 ChatEvent 状态机
	if ce := ev.ChatEvent(); ce != nil {
		st.onEvent(ce)
		return
	}

	// 3/4. 工具调用生命周期
	switch name {
	case "ToolCallStart":
		id := ev.CallID()
		st.openTools[id] = struct{}{}
		st.emitRaw(NewToolCallStartEvent(id, ev.ToolName(), ""), nil)
		return
	case "ToolCallEnd":
		id := ev.CallID()
		if _, open := st.openTools[id]; open {
			delete(st.openTools, id)
			st.emitRaw(NewToolCallEndEvent(id), nil)
		}
		st.emitRaw(NewToolCallResultEvent(id, ev.Text(), ""), nil)
		return
	}

	// 5. 终态：只记结果/异常
	switch name {
	case "RunEnd", "SimpleEnd", "TeamEnd":
		if r := ev.Response(); r != nil {
			st.result = r
		}
		if ev.Abnormal() {
			st.agentError = ev.Text()
			if st.agentError == "" {
				st.agentError = "Agent run failed"
			}
		}
		return
	}

	// 6. 其余 → CUSTOM
	payload := map[string]any{
		"agentEventType": name,
		"runId":          ev.RunID(),
		"agentName":      ev.AgentName(),
	}
	if s := ev.Text(); s != "" {
		payload["text"] = s
	}
	st.emit(NewCustomEvent(name, payload))
}

// closeAgentOut 收口（对标 finishSuccess/finishError：terminal 静默、
// agentError → RUN_ERROR、否则 RUN_FINISHED(result)）。
func (st *agState) closeAgentOut(upstreamErr error) {
	if st.terminal {
		return
	}
	if st.agentError != "" {
		st.closeContents()
		st.emitRaw(NewRunErrorEvent(st.agentError), nil)
		return
	}
	if upstreamErr != nil {
		st.closeContents()
		st.emitRaw(NewRunErrorEvent(upstreamErr.Error()), nil)
		return
	}
	st.closeContents()
	st.emitRaw(NewRunFinishedEvent(st.w.threadID, st.w.runID, st.result, nil), nil)
}

// agentEventText 工具结果的文本抽取（测试辅助）。
func agentEventText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

var _ = strconv.Itoa
