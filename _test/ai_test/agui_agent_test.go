// agui_agent_test.go agent 事件流 → AG-UI 映射黑盒测试（搬译 Java
// AgUiStreamWrapperTest 的 agent 分支：supervisor-delta 恒 CUSTOM 且不产正文、
// ChatEvent 委托复用状态机、工具三件、终态只记、其余 CUSTOM；适配器在
// ai/agent（agui 模块零反向依赖验证）。
package ai_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/ai/ui/agui"
)

func runAgentStream(t *testing.T, events []*agent.Event) string {
	t.Helper()
	var buf strings.Builder
	err := agui.WriteAgentStream(context.Background(), &buf, "th-1", "run-1", func(emit func(agui.AgentEventAdapter)) error {
		for _, ev := range events {
			emit(agent.AGUIOf(ev))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func aguiNames(t *testing.T, raw string) []string {
	t.Helper()
	var out []string
	for _, body := range sseBodies(t, raw) {
		var frame struct {
			Type string `json:"type"`
			Name string `json:"name"`
		}
		json.Unmarshal([]byte(body), &frame)
		if frame.Type == "CUSTOM" {
			out = append(out, "CUSTOM:"+frame.Name)
		} else {
			out = append(out, frame.Type)
		}
	}
	return out
}

// TestAguiAgentSupervisorDelta supervisor-delta 恒 CUSTOM 且不产正文/推理事件
//（搬译 supervisorDeltaIsCustomAndNeverProducesContentEvents）。
func TestAguiAgentSupervisorDelta(t *testing.T) {
	raw := runAgentStream(t, []*agent.Event{
		{Kind: agent.KindSupervisorDelta, RunID: "run_1", AgentName: "supervisor",
			Text: "internal", Meta: map[string]any{"thinking": true},
			ChatEvent: &ai.ChatEvent{Type: ai.EventTextDelta, Text: "正文不应出现"}},
	})
	got := aguiNames(t, raw)
	if len(got) != 3 || got[0] != "RUN_STARTED" || got[1] != "CUSTOM:supervisor-delta" || got[2] != "RUN_FINISHED" {
		t.Fatalf("事件序：%v", got)
	}
	// payload 含 agentEventType/runId/thinking/text/chatEvent
	body := sseBodies(t, raw)[1]
	var frame struct {
		Value map[string]any `json:"value"`
	}
	json.Unmarshal([]byte(body), &frame)
	if frame.Value["agentEventType"] != "SupervisorDelta" || frame.Value["runId"] != "run_1" ||
		frame.Value["thinking"] != true || frame.Value["text"] != "internal" {
		t.Fatalf("payload：%v", frame.Value)
	}
	if _, has := frame.Value["chatEvent"]; !has {
		t.Fatal("应携带 chatEvent 载荷")
	}
}

// TestAguiAgentChatEventDelegation 携带 ChatEvent 的 Delta 复用状态机。
func TestAguiAgentChatEventDelegation(t *testing.T) {
	raw := runAgentStream(t, []*agent.Event{
		{Kind: agent.KindSimpleDelta, ChatEvent: &ai.ChatEvent{Type: ai.EventTextDelta, Text: "你好"}},
		{Kind: agent.KindSimpleDelta, ChatEvent: &ai.ChatEvent{Type: ai.EventTextDelta, Text: "世界"}},
	})
	got := aguiNames(t, raw)
	want := []string{"RUN_STARTED", "TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT", "TEXT_MESSAGE_CONTENT", "TEXT_MESSAGE_END", "RUN_FINISHED"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("委托映射：\ngot  %v\nwant %v", got, want)
	}
}

// TestAguiAgentToolLifecycle 工具三件（Start → 条件 End + 恒 Result）。
func TestAguiAgentToolLifecycle(t *testing.T) {
	raw := runAgentStream(t, []*agent.Event{
		{Kind: agent.KindToolCallStart, Meta: map[string]any{"callId": "c1", "toolName": "search"}},
		{Kind: agent.KindToolCallEnd, Meta: map[string]any{"callId": "c1"}, Text: "结果文本"},
	})
	got := aguiNames(t, raw)
	want := []string{"RUN_STARTED", "TOOL_CALL_START", "TOOL_CALL_END", "TOOL_CALL_RESULT", "RUN_FINISHED"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("工具三件：\ngot  %v\nwant %v", got, want)
	}
	// Result 字段
	bodies := sseBodies(t, raw)
	var result struct {
		ToolCallID string `json:"toolCallId"`
		Content    string `json:"content"`
		Role       string `json:"role"`
	}
	json.Unmarshal([]byte(bodies[3]), &result)
	if result.ToolCallID != "c1" || result.Content != "结果文本" || result.Role != "tool" {
		t.Fatalf("Result：%+v", result)
	}

	// 未 Start 的孤立 End：不产 TOOL_CALL_END，仍产 Result
	raw2 := runAgentStream(t, []*agent.Event{
		{Kind: agent.KindToolCallEnd, Meta: map[string]any{"callId": "orphan"}, Text: "孤立"},
	})
	got2 := aguiNames(t, raw2)
	if got2[1] != "TOOL_CALL_RESULT" {
		t.Fatalf("孤立 End：%v", got2)
	}
}

// TestAguiAgentEndStates 终态只记（RunEnd 正常带 result；异常 → RUN_ERROR）。
func TestAguiAgentEndStates(t *testing.T) {
	msg := &ai.AssistantMessage{Text: "最终答案"}
	raw := runAgentStream(t, []*agent.Event{
		{Kind: agent.KindRunEnd, Message: msg},
	})
	got := aguiNames(t, raw)
	if strings.Join(got, ",") != "RUN_STARTED,RUN_FINISHED" {
		t.Fatalf("正常终态：%v", got)
	}
	var finish struct {
		Result struct {
			Text string `json:"text"`
		} `json:"result"`
	}
	json.Unmarshal([]byte(sseBodies(t, raw)[1]), &finish)
	if finish.Result.Text != "最终答案" {
		t.Fatalf("result 载荷：%v", finish.Result)
	}

	// 异常终态：agentError → RUN_ERROR
	raw2 := runAgentStream(t, []*agent.Event{
		{Kind: agent.KindTeamEnd, Meta: map[string]any{"abnormal": true}, Text: "agent failed"},
	})
	got2 := aguiNames(t, raw2)
	if strings.Join(got2, ",") != "RUN_STARTED,RUN_ERROR" {
		t.Fatalf("异常终态：%v", got2)
	}
	var errFrame struct {
		Error string `json:"error"`
	}
	json.Unmarshal([]byte(sseBodies(t, raw2)[1]), &errFrame)
	if errFrame.Error != "agent failed" {
		t.Fatalf("错误文案：%q", errFrame.Error)
	}

	// 异常无文本 → 缺省文案
	raw3 := runAgentStream(t, []*agent.Event{
		{Kind: agent.KindRunEnd, Meta: map[string]any{"abnormal": true}},
	})
	json.Unmarshal([]byte(sseBodies(t, raw3)[1]), &errFrame)
	if errFrame.Error != "Agent run failed" {
		t.Fatalf("缺省文案：%q", errFrame.Error)
	}
}

// TestAguiAgentOtherEventsCustom 其余一切 → CUSTOM（类别名）。
func TestAguiAgentOtherEventsCustom(t *testing.T) {
	raw := runAgentStream(t, []*agent.Event{
		{Kind: agent.KindPlan, RunID: "r", AgentName: "a", Text: "计划内容"},
		{Kind: agent.KindNodeStart, AgentName: "node-agent"},
		{Kind: agent.KindHITLPending, Text: "待审批"},
	})
	got := aguiNames(t, raw)
	if got[1] != "CUSTOM:Plan" || got[2] != "CUSTOM:NodeStart" || got[3] != "CUSTOM:HITLPending" {
		t.Fatalf("其余事件：%v", got)
	}
	var plan struct {
		Value map[string]any `json:"value"`
	}
	json.Unmarshal([]byte(sseBodies(t, raw)[1]), &plan)
	if plan.Value["agentEventType"] != "Plan" || plan.Value["text"] != "计划内容" ||
		plan.Value["runId"] != "r" || plan.Value["agentName"] != "a" {
		t.Fatalf("Plan payload：%v", plan.Value)
	}
}

// TestAguiAgentUpstreamError 上游错误 → RUN_ERROR 收口。
func TestAguiAgentUpstreamError(t *testing.T) {
	var buf strings.Builder
	err := agui.WriteAgentStream(context.Background(), &buf, "th", "run", func(emit func(agui.AgentEventAdapter)) error {
		emit(agent.AGUIOf(&agent.Event{Kind: agent.KindPlan}))
		return context.DeadlineExceeded
	})
	if err == nil {
		t.Fatal("应透传上游错误")
	}
	got := aguiNames(t, buf.String())
	if strings.Join(got, ",") != "RUN_STARTED,CUSTOM:Plan,RUN_ERROR" {
		t.Fatalf("上游错误收口：%v", got)
	}
}
