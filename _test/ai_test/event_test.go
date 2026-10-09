package ai_test

import (
	"errors"
	"testing"

	"github.com/crazy-airhead/aifei-go/ai"
)

func TestEventMetaTable(t *testing.T) {
	spot := []struct {
		typ     ai.EventType
		name    string
		group   ai.EventGroup
		isDelta bool
	}{
		{ai.EventResponseStart, "RESPONSE_START", ai.GroupLifecycle, false},
		{ai.EventStatus, "STATUS", ai.GroupLifecycle, false},
		{ai.EventHeartbeat, "HEARTBEAT", ai.GroupLifecycle, false},
		{ai.EventResponseEnd, "RESPONSE_END", ai.GroupLifecycle, false},
		{ai.EventAbort, "ABORT", ai.GroupLifecycle, false},
		{ai.EventStepStart, "STEP_START", ai.GroupStep, false},
		{ai.EventTextDelta, "TEXT_DELTA", ai.GroupText, true},
		{ai.EventThinkingSignature, "THINKING_SIGNATURE", ai.GroupThinking, false},
		{ai.EventToolCallArgsDelta, "TOOL_CALL_ARGS_DELTA", ai.GroupToolCall, true},
		{ai.EventToolResult, "TOOL_RESULT", ai.GroupToolCall, false},
		{ai.EventServerToolStart, "SERVER_TOOL_START", ai.GroupServerTool, false},
		{ai.EventMediaPartial, "MEDIA_PARTIAL", ai.GroupMedia, true},
		{ai.EventRefusalDelta, "REFUSAL_DELTA", ai.GroupSafety, true},
		{ai.EventUsage, "USAGE", ai.GroupMeta, false},
		{ai.EventCustom, "CUSTOM", ai.GroupMeta, false},
	}
	for _, s := range spot {
		if s.typ.String() != s.name || s.typ.Group() != s.group || s.typ.IsDelta() != s.isDelta {
			t.Fatalf("%d: %q/%v delta=%v want %q/%v delta=%v",
				s.typ, s.typ.String(), s.typ.Group(), s.typ.IsDelta(), s.name, s.group, s.isDelta)
		}
	}
	// 32 型齐备：EventCustom 是最后一个
	if ai.EventCustom != 32 {
		t.Fatalf("event count = %d want 32", ai.EventCustom)
	}
	// 终态 = RESPONSE_END | ABORT | ERROR
	for _, term := range []ai.EventType{ai.EventResponseEnd, ai.EventAbort, ai.EventError} {
		if !term.IsTerminal() {
			t.Fatalf("%s should be terminal", term)
		}
	}
	if ai.EventTextStart.IsTerminal() || ai.EventStepEnd.IsTerminal() {
		t.Fatal("non-terminal events flagged terminal")
	}
}

func TestAccumulatorTextAndThinking(t *testing.T) {
	acc := ai.NewAccumulator()
	for _, ev := range []*ai.ChatEvent{
		{Type: ai.EventThinkingStart},
		{Type: ai.EventThinkingDelta, Text: "先想"},
		{Type: ai.EventThinkingDelta, Text: "一想"},
		{Type: ai.EventThinkingEnd},
		{Type: ai.EventTextStart},
		{Type: ai.EventTextDelta, Text: "答案"},
		{Type: ai.EventTextDelta, Text: "是 42"},
		{Type: ai.EventTextEnd},
	} {
		acc.Merge(ev)
	}
	m := acc.Message()
	if m.Text != "答案是 42" {
		t.Fatalf("text = %q", m.Text)
	}
	if m.Thinking != "先想一想" {
		t.Fatalf("thinking = %q", m.Thinking)
	}
	if !m.HasThinking() || m.ThinkingOnly() {
		t.Fatalf("hasThinking=%v thinkingOnly=%v", m.HasThinking(), m.ThinkingOnly())
	}
}

func TestAccumulatorToolCallDeltaSplicing(t *testing.T) {
	acc := ai.NewAccumulator()
	for _, ev := range []*ai.ChatEvent{
		{Type: ai.EventToolCallStart, ToolCall: &ai.ToolCall{Index: "idx:0", ID: "call_1", Name: "queryOrder"}},
		{Type: ai.EventToolCallArgsDelta, ToolCall: &ai.ToolCall{Index: "idx:0"}, Text: `{"order`},
		{Type: ai.EventToolCallArgsDelta, ToolCall: &ai.ToolCall{Index: "idx:0"}, Text: `Id":"A1"}`},
		{Type: ai.EventToolCallEnd, ToolCall: &ai.ToolCall{Index: "idx:0", ID: "call_1", Name: "queryOrder"}},
	} {
		acc.Merge(ev)
	}
	calls := acc.Message().ToolCalls
	if len(calls) != 1 {
		t.Fatalf("calls = %#v", calls)
	}
	c := calls[0]
	if c.ID != "call_1" || c.Name != "queryOrder" {
		t.Fatalf("call = %+v", c)
	}
	if c.ArgumentsStr != `{"orderId":"A1"}` {
		t.Fatalf("args = %q", c.ArgumentsStr)
	}
	if c.Arguments["orderId"] != "A1" {
		t.Fatalf("arguments = %#v", c.Arguments)
	}
}

func TestAccumulatorMultipleToolCalls(t *testing.T) {
	acc := ai.NewAccumulator()
	events := []*ai.ChatEvent{
		{Type: ai.EventToolCallStart, ToolCall: &ai.ToolCall{Index: "idx:0", ID: "c1", Name: "f1"}},
		{Type: ai.EventToolCallStart, ToolCall: &ai.ToolCall{Index: "idx:1", ID: "c2", Name: "f2"}},
		{Type: ai.EventToolCallArgsDelta, ToolCall: &ai.ToolCall{Index: "idx:0"}, Text: `{"a":1}`},
		{Type: ai.EventToolCallArgsDelta, ToolCall: &ai.ToolCall{Index: "idx:1"}, Text: `{"b":2}`},
		{Type: ai.EventToolCallEnd, ToolCall: &ai.ToolCall{Index: "idx:0"}},
		{Type: ai.EventToolCallEnd, ToolCall: &ai.ToolCall{Index: "idx:1"}},
	}
	for _, ev := range events {
		acc.Merge(ev)
	}
	calls := acc.Message().ToolCalls
	if len(calls) != 2 {
		t.Fatalf("calls = %#v", calls)
	}
	if calls[0].Name != "f1" || calls[1].Name != "f2" {
		t.Fatalf("order broken: %#v", calls)
	}
	if calls[0].Arguments["a"] != float64(1) || calls[1].Arguments["b"] != float64(2) {
		t.Fatalf("args = %#v", calls)
	}
}

func TestAccumulatorTextMediaInterleave(t *testing.T) {
	acc := ai.NewAccumulator()
	for _, ev := range []*ai.ChatEvent{
		{Type: ai.EventTextDelta, Text: "前文"},
		{Type: ai.EventMediaDone, Block: ai.ImageBlockOfURL("https://x/1.png")},
		{Type: ai.EventTextDelta, ItemID: "b2", Text: "后文"},
	} {
		acc.Merge(ev)
	}
	m := acc.Message()
	if m.Text != "前文后文" {
		t.Fatalf("text = %q", m.Text)
	}
	if len(m.Blocks) != 1 {
		t.Fatalf("blocks = %#v", m.Blocks)
	}
}

func TestAccumulatorUsageFinishReasonSignature(t *testing.T) {
	acc := ai.NewAccumulator()
	acc.Merge(&ai.ChatEvent{Type: ai.EventTextDelta, Text: "好"})
	acc.Merge(&ai.ChatEvent{
		Type:  ai.EventThinkingSignature,
		Text:  "sig-v1",
		Attrs: map[string]any{"finish_reason": "tool_calls"},
	})
	acc.Merge(&ai.ChatEvent{Type: ai.EventUsage, Usage: &ai.Usage{
		PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, CacheReadInputTokens: 4,
	}})

	m := acc.Message()
	if m.ProtocolStates["thinking_signature"] != "sig-v1" {
		t.Fatalf("protocolStates = %#v", m.ProtocolStates)
	}
	if m.Thinking != "" {
		t.Fatalf("signature must not append thinking: %q", m.Thinking)
	}
	if acc.Usage().CacheRate() != 0.4 {
		t.Fatalf("cacheRate = %v", acc.Usage().CacheRate())
	}

	resp := acc.BuildResponse("gpt-x", nil)
	if resp.FinishReason() != "tool" {
		t.Fatalf("finishReason = %q", resp.FinishReason())
	}
	if resp.Model() != "gpt-x" || resp.Content() != "好" {
		t.Fatalf("resp = %+v", resp)
	}
	if !resp.IsTerminal() || resp.IsEmpty() || !resp.HasContent() {
		t.Fatal("resp flags wrong")
	}
}

func TestAccumulatorEdgeCases(t *testing.T) {
	// 无事件流：空消息、默认 stop、无错误
	acc := ai.NewAccumulator()
	resp := acc.BuildResponse("m", nil)
	if !resp.IsEmpty() || resp.Err() != nil || resp.FinishReason() != "stop" {
		t.Fatalf("empty accumulator resp = %+v", resp)
	}

	// 仅 ERROR：错误记录，其余为空
	acc2 := ai.NewAccumulator()
	wantErr := errors.New("boom")
	acc2.Merge(&ai.ChatEvent{Type: ai.EventError, Err: wantErr})
	resp2 := acc2.BuildResponse("m", nil)
	if resp2.Err() != wantErr {
		t.Fatalf("err = %v", resp2.Err())
	}
	if resp2.FinishReason() != "" {
		t.Fatalf("error-only finishReason = %q", resp2.FinishReason())
	}

	// 截断的 args JSON：保留字符串形态，不因解析失败丢调用
	acc3 := ai.NewAccumulator()
	acc3.Merge(&ai.ChatEvent{Type: ai.EventToolCallStart, ToolCall: &ai.ToolCall{Index: "idx:0", Name: "f"}})
	acc3.Merge(&ai.ChatEvent{Type: ai.EventToolCallArgsDelta, ToolCall: &ai.ToolCall{Index: "idx:0"}, Text: `{"a":`})
	calls := acc3.Message().ToolCalls
	if len(calls) != 1 || calls[0].ArgumentsStr != `{"a":` || calls[0].Arguments != nil {
		t.Fatalf("truncated args = %#v", calls)
	}
}

func TestAccumulatorFinishReasonNormalization(t *testing.T) {
	cases := map[string]string{
		"tool_calls":     "tool",
		"function_call":  "tool",
		"stop":           "stop",
		"end_turn":       "stop",
		"length":         "length",
		"content_filter": "content_filter",
		"":               "stop",
	}
	for in, want := range cases {
		acc := ai.NewAccumulator()
		acc.Merge(&ai.ChatEvent{Type: ai.EventTextDelta, Text: "x",
			Attrs: map[string]any{"finish_reason": in}})
		if got := acc.BuildResponse("m", nil).FinishReason(); got != want {
			t.Fatalf("normalize(%q) = %q want %q", in, got, want)
		}
	}
}

func TestEventIsHelpers(t *testing.T) {
	ev := &ai.ChatEvent{Type: ai.EventTextDelta}
	if !ev.Is(ai.EventTextDelta) || ev.Is(ai.EventTextEnd) {
		t.Fatal("Is()")
	}
	if !ev.IsGroup(ai.GroupText) || ev.IsGroup(ai.GroupMeta) {
		t.Fatal("IsGroup()")
	}
	if !ev.IsDelta() || ev.IsTerminal() {
		t.Fatal("delta/terminal helpers")
	}
}
