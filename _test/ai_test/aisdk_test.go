// aisdk_test.go P1-d ui/aisdk 测试：事件→parts 映射表驱动（确定性 ID 生成器）、
// httptest 完整 SSE 输出比对、WrapResponse 非流式展开。
package ai_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/ui/aisdk"
)

// scriptedDesc 回放脚本事件的 RequestDesc（映射层表驱动用，不发起真实请求）。
type scriptedDesc struct {
	events []*ai.ChatEvent
}

func (d *scriptedDesc) Session(ai.Session) ai.RequestDesc { return d }
func (d *scriptedDesc) Role(string) ai.RequestDesc        { return d }
func (d *scriptedDesc) Instruction(string) ai.RequestDesc { return d }
func (d *scriptedDesc) SystemPrompt(string) ai.RequestDesc {
	return d
}
func (d *scriptedDesc) Options(func(*ai.ChatOptions)) ai.RequestDesc { return d }
func (d *scriptedDesc) EventFilter(ai.EventFilter) ai.RequestDesc    { return d }
func (d *scriptedDesc) Call() (*ai.Response, error)                  { return nil, nil }
func (d *scriptedDesc) Stream(_ context.Context, sink ai.ChatSink) error {
	for _, ev := range d.events {
		if err := sink.OnEvent(ev); err != nil {
			return err
		}
	}
	return nil
}

// seqIDs 确定性 ID 生成器（每前缀独立计数：msg_1 / txt_1 / rsn_1 / call_1）。
func seqIDs() aisdk.IDGenerator {
	counters := map[string]int{}
	return func(prefix string) string {
		counters[prefix]++
		return fmt.Sprintf("%s%d", prefix, counters[prefix])
	}
}

// sseBodies 提取全部 SSE data 体（按帧序）。
func sseBodies(t *testing.T, raw string) []string {
	t.Helper()
	var out []string
	for _, frame := range strings.Split(strings.TrimSuffix(raw, "\n\n"), "\n\n") {
		if !strings.HasPrefix(frame, "data: ") {
			t.Fatalf("非 data 帧：%q", frame)
		}
		out = append(out, strings.TrimPrefix(frame, "data: "))
	}
	return out
}

func eqFrames(t *testing.T, name string, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("%s 帧序不符：\ngot:\n  %s\nwant:\n  %s", name,
			strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// TestAisdkEventMapping 事件→part 映射表驱动（首帧恒 start，末两帧恒 finish+[DONE]）。
func TestAisdkEventMapping(t *testing.T) {
	cases := []struct {
		name   string
		events []*ai.ChatEvent
		want   []string
	}{
		{
			name: "text 生命周期 + step 边界 + usage",
			events: []*ai.ChatEvent{
				{Type: ai.EventStepStart, Step: 0},
				{Type: ai.EventTextStart, Step: 0, Index: 0},
				{Type: ai.EventTextDelta, Step: 0, Index: 0, Text: "你"},
				{Type: ai.EventTextDelta, Step: 0, Index: 0, Text: "好"},
				{Type: ai.EventTextEnd, Step: 0, Index: 0},
				{Type: ai.EventStepEnd, Step: 0},
				{Type: ai.EventUsage, Usage: &ai.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}},
				{Type: ai.EventResponseEnd},
			},
			want: []string{
				`{"type":"start-step"}`,
				`{"type":"text-start","id":"txt_1"}`,
				`{"type":"text-delta","id":"txt_1","delta":"你"}`,
				`{"type":"text-delta","id":"txt_1","delta":"好"}`,
				`{"type":"text-end","id":"txt_1"}`,
				`{"type":"finish-step"}`,
				`{"type":"finish","finishReason":"stop","usage":{"promptTokens":10,"completionTokens":5,"totalTokens":15}}`,
			},
		},
		{
			name: "thinking 三连",
			events: []*ai.ChatEvent{
				{Type: ai.EventThinkingStart, Step: 0, Index: 0},
				{Type: ai.EventThinkingDelta, Step: 0, Index: 0, Text: "推理中"},
				{Type: ai.EventThinkingEnd, Step: 0, Index: 0},
			},
			want: []string{
				`{"type":"reasoning-start","id":"rsn_1"}`,
				`{"type":"reasoning-delta","id":"rsn_1","delta":"推理中"}`,
				`{"type":"reasoning-end","id":"rsn_1"}`,
				`{"type":"finish","finishReason":"stop"}`,
			},
		},
		{
			name: "工具调用流式（start/args-delta/end）",
			events: []*ai.ChatEvent{
				{Type: ai.EventToolCallStart, ToolCallID: "call_9", ToolCall: &ai.ToolCall{ID: "call_9", Name: "echo"}},
				{Type: ai.EventToolCallArgsDelta, ToolCallID: "call_9", Text: `{"w":`},
				{Type: ai.EventToolCallArgsDelta, ToolCallID: "call_9", Text: `"hi"}`},
				{Type: ai.EventToolCallEnd, ToolCallID: "call_9", ToolCall: &ai.ToolCall{
					ID: "call_9", Name: "echo", ArgumentsStr: `{"w":"hi"}`, Arguments: map[string]any{"w": "hi"},
				}},
			},
			want: []string{
				`{"type":"tool-input-start","toolCallId":"call_9","toolName":"echo"}`,
				`{"type":"tool-input-delta","toolCallId":"call_9","inputTextDelta":"{\"w\":"}`,
				`{"type":"tool-input-delta","toolCallId":"call_9","inputTextDelta":"\"hi\"}"}`,
				`{"type":"tool-input-available","toolCallId":"call_9","toolName":"echo","input":{"w":"hi"}}`,
				`{"type":"finish","finishReason":"stop"}`,
			},
		},
		{
			name: "工具调用整块（只有 END，补齐 start+delta）",
			events: []*ai.ChatEvent{
				{Type: ai.EventToolCallEnd, ToolCall: &ai.ToolCall{
					ID: "call_1", Name: "echo", ArgumentsStr: `{"w":"hi"}`, Arguments: map[string]any{"w": "hi"},
				}},
			},
			want: []string{
				`{"type":"tool-input-start","toolCallId":"call_1","toolName":"echo"}`,
				`{"type":"tool-input-delta","toolCallId":"call_1","inputTextDelta":"{\"w\":\"hi\"}"}`,
				`{"type":"tool-input-available","toolCallId":"call_1","toolName":"echo","input":{"w":"hi"}}`,
				`{"type":"finish","finishReason":"stop"}`,
			},
		},
		{
			name: "无供应商 id 的工具调用按事件身份分配稳定 id",
			events: []*ai.ChatEvent{
				{Type: ai.EventToolCallEnd, Index: 0, ToolCall: &ai.ToolCall{Name: "echo"}},
			},
			want: []string{
				`{"type":"tool-input-start","toolCallId":"call_1","toolName":"echo"}`,
				`{"type":"tool-input-available","toolCallId":"call_1","toolName":"echo"}`,
				`{"type":"finish","finishReason":"stop"}`,
			},
		},
		{
			name: "本地工具执行结果",
			events: []*ai.ChatEvent{
				{Type: ai.EventToolResult, ToolCallID: "call_9", Text: "hi!"},
			},
			want: []string{
				`{"type":"tool-output-available","toolCallId":"call_9","output":"hi!"}`,
				`{"type":"finish","finishReason":"stop"}`,
			},
		},
		{
			name: "服务端工具（无 TOOL_CALL 帧，幂等补齐 input 部件对）",
			events: []*ai.ChatEvent{
				{Type: ai.EventServerToolStart, ItemID: "srch1", SubType: "web_search"},
				{Type: ai.EventServerToolResult, ItemID: "srch1", SubType: "web_search", Text: "命中 3 条"},
			},
			want: []string{
				`{"type":"data-server-tool-start","data":{"eventType":"SERVER_TOOL_START","itemId":"srch1","subType":"web_search"}}`,
				`{"type":"tool-input-start","toolCallId":"srch1","toolName":"web_search"}`,
				`{"type":"tool-input-available","toolCallId":"srch1","toolName":"web_search"}`,
				`{"type":"tool-output-available","toolCallId":"srch1","output":"命中 3 条"}`,
				`{"type":"finish","finishReason":"stop"}`,
			},
		},
		{
			name: "abort 中止",
			events: []*ai.ChatEvent{
				{Type: ai.EventAbort, Text: "user cancel"},
			},
			want: []string{
				`{"type":"abort","reason":"user cancel"}`,
				`{"type":"finish","finishReason":"stop"}`,
			},
		},
		{
			name: "error 终态（error + finish(error) 恰一次）",
			events: []*ai.ChatEvent{
				{Type: ai.EventError, Err: errors.New("boom")},
			},
			want: []string{
				`{"type":"error","errorText":"boom"}`,
				`{"type":"finish","finishReason":"error"}`,
			},
		},
		{
			name: "搜索结果与引用",
			events: []*ai.ChatEvent{
				{Type: ai.EventSearchResult, SearchResult: &ai.SearchResult{ID: "s1", URL: "https://x/1", Title: "结果一"}},
				{Type: ai.EventSearchResult, SearchResult: &ai.SearchResult{URL: "https://x/2"}},
				{Type: ai.EventCitation, ItemID: "cit1", Citation: &ai.Citation{URL: "https://x/3", Title: "引用"}},
				{Type: ai.EventCitation, Citation: &ai.Citation{Title: "文档引用"}},
			},
			want: []string{
				`{"type":"source-url","sourceId":"s1","url":"https://x/1","title":"结果一"}`,
				`{"type":"source-url","sourceId":"https://x/2","url":"https://x/2","title":"https://x/2"}`,
				`{"type":"source-url","sourceId":"cit1","url":"https://x/3","title":"引用"}`,
				`{"type":"source-document","sourceId":"文档引用","mediaType":"","title":"文档引用"}`,
				`{"type":"finish","finishReason":"stop"}`,
			},
		},
		{
			name: "custom/status/raw 降级 data-*",
			events: []*ai.ChatEvent{
				{Type: ai.EventCustom, SubType: "foo"},
				{Type: ai.EventStatus},
				{Type: ai.EventRaw, RawType: "chat.completion.chunk", Raw: []byte(`{"k":1}`)},
			},
			want: []string{
				`{"type":"data-foo","data":{"eventType":"CUSTOM","subType":"foo"}}`,
				`{"type":"data-status","data":{"eventType":"STATUS"}}`,
				`{"type":"data-chat.completion.chunk","data":{"k":1}}`,
				`{"type":"finish","finishReason":"stop"}`,
			},
		},
		{
			name: "无 start 的 TEXT_END 不发帧",
			events: []*ai.ChatEvent{
				{Type: ai.EventTextEnd, Index: 0},
			},
			want: []string{
				`{"type":"finish","finishReason":"stop"}`,
			},
		},
		{
			name: "delta 自动开 part、收口时按 reasoning→text 闭合",
			events: []*ai.ChatEvent{
				{Type: ai.EventThinkingDelta, Index: 0, Text: "想"},
				{Type: ai.EventTextDelta, Index: 0, Text: "答"},
			},
			want: []string{
				`{"type":"reasoning-start","id":"rsn_1"}`,
				`{"type":"reasoning-delta","id":"rsn_1","delta":"想"}`,
				`{"type":"text-start","id":"txt_1"}`,
				`{"type":"text-delta","id":"txt_1","delta":"答"}`,
				`{"type":"reasoning-end","id":"rsn_1"}`,
				`{"type":"text-end","id":"txt_1"}`,
				`{"type":"finish","finishReason":"stop"}`,
			},
		},
		{
			name: "媒体完成 → file part",
			events: []*ai.ChatEvent{
				{Type: ai.EventMediaDone, Block: ai.ImageBlockOfURL("https://x/i.png")},
			},
			want: []string{
				`{"type":"file","url":"https://x/i.png","mediaType":"image/jpeg"}`,
				`{"type":"finish","finishReason":"stop"}`,
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := aisdk.NewWrapper(seqIDs())
			var buf strings.Builder
			if err := w.WriteStream(context.Background(), &buf, &scriptedDesc{events: c.events}, nil); err != nil {
				t.Fatalf("WriteStream: %v", err)
			}
			want := append([]string{`{"type":"start","messageId":"msg_1"}`}, c.want...)
			want = append(want, "[DONE]")
			eqFrames(t, c.name, sseBodies(t, buf.String()), want)
		})
	}
}

// TestAisdkMetadataPart meta 在 start 后紧随 message-metadata part。
func TestAisdkMetadataPart(t *testing.T) {
	w := aisdk.NewWrapper(seqIDs())
	var buf strings.Builder
	err := w.WriteStream(context.Background(), &buf, &scriptedDesc{}, map[string]any{"sessionId": "sess-1"})
	if err != nil {
		t.Fatalf("WriteStream: %v", err)
	}
	eqFrames(t, "metadata", sseBodies(t, buf.String()), []string{
		`{"type":"start","messageId":"msg_1"}`,
		`{"type":"message-metadata","messageMetadata":{"sessionId":"sess-1"}}`,
		`{"type":"finish","finishReason":"stop"}`,
		"[DONE]",
	})
}

// TestAisdkFullSSE httptest 端到端：假 OpenAI SSE（思考 + 正文 + usage）→ aisdk
// 端点 → 完整输出逐字节比对。
func TestAisdkFullSSE(t *testing.T) {
	s := newSSEResponder(t,
		`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"想一想"}}]}`,
		`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"你"}}]}`,
		`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"好"}}]}`,
		`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"id":"c1","model":"gpt-4o","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":4,"total_tokens":11}}`,
	)
	m := sseModel(t, s)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		wr := aisdk.NewWrapper(seqIDs())
		if err := wr.WriteStream(r.Context(), w, m.Prompt("hi"), map[string]any{"sessionId": "sess-9"}); err != nil {
			t.Errorf("WriteStream: %v", err)
		}
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	// 完整 SSE 输出比对（帧序 = 协议契约；step 边界统一收口未闭合 part，
	// usage 无 think → 无 reasoningTokens）
	want := strings.Join([]string{
		`data: {"type":"start","messageId":"msg_1"}`,
		``,
		`data: {"type":"message-metadata","messageMetadata":{"sessionId":"sess-9"}}`,
		``,
		`data: {"type":"start-step"}`,
		``,
		`data: {"type":"reasoning-start","id":"rsn_1"}`,
		``,
		`data: {"type":"reasoning-delta","id":"rsn_1","delta":"想一想"}`,
		``,
		`data: {"type":"text-start","id":"txt_1"}`,
		``,
		`data: {"type":"text-delta","id":"txt_1","delta":"你"}`,
		``,
		`data: {"type":"text-delta","id":"txt_1","delta":"好"}`,
		``,
		`data: {"type":"reasoning-end","id":"rsn_1"}`,
		``,
		`data: {"type":"text-end","id":"txt_1"}`,
		``,
		`data: {"type":"finish-step"}`,
		``,
		`data: {"type":"finish","finishReason":"stop","usage":{"promptTokens":7,"completionTokens":4,"totalTokens":11}}`,
		``,
		`data: [DONE]`,
		``,
		``,
	}, "\n")
	if string(raw) != want {
		t.Fatalf("完整 SSE 输出不符：\ngot:\n%s\nwant:\n%s", raw, want)
	}
}

// TestAisdkWrapResponse 非流式展开：聚合响应一次性转 parts（reasoning→text→finish）。
func TestAisdkWrapResponse(t *testing.T) {
	s := newSSEResponder(t,
		`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"思考"}}]}`,
		`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"答案"}}]}`,
		`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"id":"c1","model":"gpt-4o","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`,
	)
	m := sseModel(t, s)
	resp, err := ai.CallStream(context.Background(), m.Prompt("q"))
	if err != nil {
		t.Fatalf("CallStream: %v", err)
	}

	var buf strings.Builder
	if err := aisdk.NewWrapper(seqIDs()).WrapResponse(resp, &buf); err != nil {
		t.Fatalf("WrapResponse: %v", err)
	}
	eqFrames(t, "WrapResponse", sseBodies(t, buf.String()), []string{
		`{"type":"start","messageId":"msg_1"}`,
		`{"type":"reasoning-start","id":"rsn_1"}`,
		`{"type":"reasoning-delta","id":"rsn_1","delta":"思考"}`,
		`{"type":"reasoning-end","id":"rsn_1"}`,
		`{"type":"text-start","id":"txt_1"}`,
		`{"type":"text-delta","id":"txt_1","delta":"答案"}`,
		`{"type":"text-end","id":"txt_1"}`,
		`{"type":"finish","finishReason":"stop","usage":{"promptTokens":3,"completionTokens":2,"totalTokens":5}}`,
		"[DONE]",
	})
}

// TestAisdkWrapResponseToolCalls 非流式工具调用展开（start/delta/available 三连）。
func TestAisdkWrapResponseToolCalls(t *testing.T) {
	s := newSSEResponder(t,
		`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_2","type":"function","function":{"name":"echo","arguments":""}}]}}]}`,
		`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"w\":"}}]}}]}`,
		`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"你好\"}"}}]}}]}`,
		`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	m := sseModel(t, s)
	resp, err := ai.CallStream(context.Background(), m.Prompt("q")) // 不开 autoToolCall：停在 tool_calls 终态
	if err != nil {
		t.Fatalf("CallStream: %v", err)
	}
	if len(resp.ToolCalls()) == 0 {
		t.Fatalf("聚合响应应带 toolCalls")
	}

	var buf strings.Builder
	if err := aisdk.NewWrapper(seqIDs()).WrapResponse(resp, &buf); err != nil {
		t.Fatalf("WrapResponse: %v", err)
	}
	eqFrames(t, "WrapResponseToolCalls", sseBodies(t, buf.String()), []string{
		`{"type":"start","messageId":"msg_1"}`,
		`{"type":"tool-input-start","toolCallId":"call_2","toolName":"echo"}`,
		`{"type":"tool-input-delta","toolCallId":"call_2","inputTextDelta":"{\"w\":\"你好\"}"}`,
		`{"type":"tool-input-available","toolCallId":"call_2","toolName":"echo","input":{"w":"你好"}}`,
		`{"type":"finish","finishReason":"tool"}`,
		"[DONE]",
	})
}

// TestAisdkUpstreamErrorNoErrorEvent 上游失败但无 ERROR 事件（传输层错误）：
// 兜底 error part + finish("error")，错误原样返回。
func TestAisdkUpstreamErrorNoErrorEvent(t *testing.T) {
	boom := errors.New("connection reset")
	d := &errDesc{err: boom}
	w := aisdk.NewWrapper(seqIDs())
	var buf strings.Builder
	err := w.WriteStream(context.Background(), &buf, d, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("WriteStream 应原样返回上游错误，got %v", err)
	}
	eqFrames(t, "upstream error", sseBodies(t, buf.String()), []string{
		`{"type":"start","messageId":"msg_1"}`,
		`{"type":"error","errorText":"connection reset"}`,
		`{"type":"finish","finishReason":"error"}`,
		"[DONE]",
	})
}

// errDesc Stream 直接失败的 RequestDesc。
type errDesc struct{ err error }

func (d *errDesc) Session(ai.Session) ai.RequestDesc            { return d }
func (d *errDesc) Role(string) ai.RequestDesc                   { return d }
func (d *errDesc) Instruction(string) ai.RequestDesc            { return d }
func (d *errDesc) SystemPrompt(string) ai.RequestDesc           { return d }
func (d *errDesc) Options(func(*ai.ChatOptions)) ai.RequestDesc { return d }
func (d *errDesc) EventFilter(ai.EventFilter) ai.RequestDesc    { return d }
func (d *errDesc) Call() (*ai.Response, error)                  { return nil, d.err }
func (d *errDesc) Stream(context.Context, ai.ChatSink) error    { return d.err }
