// react_stream_test.go P2-b ReAct 流式（Java ReActStreamTest 场景搬译）：
// 基础流 ReasonDelta+RunEnd、含工具调用的完整生命周期
// （Reason*→ToolCall*→…→RunEnd 事件序列）、模型失败 → RunEnd 携兜底文案。
package ai_agent_test

import (
	"context"
	"strings"
	"testing"

	"github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/ai/agent/react"
)

// indexOfKind 首个匹配类别事件下标（-1 未找到）。
func indexOfKind(evs []*agent.Event, kind agent.EventKind) int {
	for i, ev := range evs {
		if ev.Kind == kind {
			return i
		}
	}
	return -1
}

// TestReActStreamSimple 基础流式输出：思考片段（ReasonDelta）与终态汇总
// （RunEnd）齐备（对标 testSimpleStream）。
func TestReActStreamSimple(t *testing.T) {
	srv := newScripted(t, scriptedResp{body: chatSSE("我是 ReAct 智能体"), sse: true})
	a, err := react.NewBuilder(agentModel(t, srv)).Name("stream1").Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	rec := &eventRecorder{}
	sess := agent.NewMemorySession("stream_001")
	if err := a.Prompt("你好，请问你是谁？").Session(sess).Stream(context.Background(), rec); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	all := rec.all()
	deltas := rec.of(agent.KindReasonDelta)
	ends := rec.of(agent.KindRunEnd)
	if len(deltas) == 0 {
		t.Fatalf("流中应包含思考片段，事件 = %v", kindsOf(all))
	}
	if len(ends) != 1 {
		t.Fatalf("RunEnd = %d 个", len(ends))
	}

	// 文本增量拼接 = 终答；RunEnd 为末事件且带终态文本
	var text strings.Builder
	for _, ev := range deltas {
		if ev.ChatEvent != nil && ev.ChatEvent.Type == ai.EventTextDelta {
			text.WriteString(ev.ChatEvent.Text)
		}
	}
	if got := text.String(); got != "我是 ReAct 智能体" {
		t.Fatalf("增量拼接 = %q", got)
	}
	if all[len(all)-1].Kind != agent.KindRunEnd {
		t.Fatalf("末事件 = %v", all[len(all)-1].Kind)
	}
	if ends[0].Text != "我是 ReAct 智能体" {
		t.Fatalf("RunEnd.Text = %q", ends[0].Text)
	}
	if ends[0].Meta["abnormal"] != false {
		t.Fatalf("RunEnd.abnormal = %v", ends[0].Meta["abnormal"])
	}
}

// TestReActStreamAction 含工具调用的完整流生命周期：
// RunStart → ReasonStart → ReasonEnd → ActionStart → ToolCallStart →
// ToolCallEnd → ActionEnd → ReasonStart → ReasonDelta(终答增量) → ReasonEnd →
// RunEnd（对标 testActionStream）。
func TestReActStreamAction(t *testing.T) {
	srv := newScripted(t,
		scriptedResp{body: toolCallSSE("call_1", "get_weather", `{"city":"北京"}`), sse: true},
		scriptedResp{body: chatSSE("北京今天晴"), sse: true})

	a, err := react.NewBuilder(agentModel(t, srv)).
		Name("stream2").
		ToolAdd(weatherTool(new(int32), new([]string))).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	rec := &eventRecorder{}
	sess := agent.NewMemorySession("stream_002")
	if err := a.Prompt("帮我查询北京天气").Session(sess).Stream(context.Background(), rec); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	all := rec.all()
	kinds := kindsOf(all)

	// 事件序：首 RunStart、末 RunEnd；ToolCall 段先于终答 ReasonDelta 段
	if kinds[0] != agent.KindRunStart {
		t.Fatalf("首事件 = %v", kinds[0])
	}
	if kinds[len(kinds)-1] != agent.KindRunEnd {
		t.Fatalf("末事件 = %v", kinds[len(kinds)-1])
	}
	iToolStart := indexOfKind(all, agent.KindToolCallStart)
	iToolEnd := indexOfKind(all, agent.KindToolCallEnd)
	iDelta := indexOfKind(all, agent.KindReasonDelta)
	iEnd := indexOfKind(all, agent.KindRunEnd)
	if iToolStart < 0 || iToolEnd < 0 || iDelta < 0 {
		t.Fatalf("缺关键事件: %v", kinds)
	}
	if !(iToolStart < iToolEnd && iToolEnd < iDelta && iDelta < iEnd) {
		t.Fatalf("事件序错乱: toolStart=%d toolEnd=%d delta=%d end=%d, %v",
			iToolStart, iToolEnd, iDelta, iEnd, kinds)
	}

	// ToolCallStart 携带调用详情；ToolCallEnd 携带观测文本
	start := rec.of(agent.KindToolCallStart)[0]
	if start.Meta["toolName"] != "get_weather" || start.Meta["callId"] != "call_1" {
		t.Fatalf("ToolCallStart.Meta = %v", start.Meta)
	}
	tce := rec.of(agent.KindToolCallEnd)[0]
	if !strings.Contains(tce.Text, "晴") {
		t.Fatalf("ToolCallEnd.Text = %q", tce.Text)
	}
	if rec.of(agent.KindActionStart) == nil || rec.of(agent.KindActionEnd) == nil {
		t.Fatalf("缺 Action 段事件: %v", kinds)
	}

	// RunEnd 终态汇总
	runEnd := rec.of(agent.KindRunEnd)[0]
	if runEnd.Text != "北京今天晴" {
		t.Fatalf("RunEnd.Text = %q", runEnd.Text)
	}
	if runEnd.Message == nil || runEnd.Message.Text != "北京今天晴" {
		t.Fatalf("RunEnd.Message = %+v", runEnd.Message)
	}
}

// TestReActStreamError 模型响应失败：流以兜底文案收口，RunEnd 标记异常
// （对标 testStreamError）。
func TestReActStreamError(t *testing.T) {
	srv := newScripted(t, scriptedResp{body: `{"error":{"message":"provider down"}}`, sse: true})
	a, err := react.NewBuilder(agentModel(t, srv)).
		Name("stream3").
		Retry(1). // 单次尝试即耗尽，走兜底
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	rec := &eventRecorder{}
	if err := a.Prompt("hello").Stream(context.Background(), rec); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	ends := rec.of(agent.KindRunEnd)
	if len(ends) != 1 {
		t.Fatalf("RunEnd = %d 个，事件 = %v", len(ends), kindsOf(rec.all()))
	}
	if !strings.Contains(ends[0].Text, "暂时无法使用模型服务") {
		t.Fatalf("RunEnd.Text = %q", ends[0].Text)
	}
	if ends[0].Meta["abnormal"] != true {
		t.Fatalf("RunEnd.abnormal = %v", ends[0].Meta["abnormal"])
	}
	// 异常结束触发流式补位（ReasonDelta 兜底帧）
	if len(rec.of(agent.KindReasonDelta)) == 0 {
		t.Fatalf("异常收口应补位 ReasonDelta")
	}
}

// kindsOf 事件类别序列（失败信息用）。
func kindsOf(evs []*agent.Event) []agent.EventKind {
	out := make([]agent.EventKind, 0, len(evs))
	for _, ev := range evs {
		out = append(out, ev.Kind)
	}
	return out
}
