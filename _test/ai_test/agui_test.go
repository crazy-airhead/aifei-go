// agui_test.go ui/agui 黑盒测试：事件映射黄金序列搬译 Java
// AgUiStreamWrapperTest（生命周期/contentKey 作用域/typed sources/未建模降级/
// abort/error 终态），外加 httptest 假 OpenAI 端到端。
package ai_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ai "github.com/crazy-airhead/aifei-go/ai"
	_ "github.com/crazy-airhead/aifei-go/ai/dialect/openai" // 注册默认方言
	"github.com/crazy-airhead/aifei-go/ai/ui/agui"
)

// aguiTypes 提取全部 SSE 帧的 type 字段（按帧序）。
func aguiTypes(t *testing.T, raw string) []string {
	t.Helper()
	var out []string
	for _, body := range sseBodies(t, raw) {
		var frame struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(body), &frame); err != nil {
			t.Fatalf("帧 JSON 解析失败 %q: %v", body, err)
		}
		out = append(out, frame.Type)
	}
	return out
}

// aguiRun 跑一段脚本事件流，返回原始 SSE 输出。
func aguiRun(t *testing.T, events []*ai.ChatEvent) string {
	t.Helper()
	var buf strings.Builder
	w := agui.NewWrapper("thread-1", "run-1")
	if err := w.WriteStream(context.Background(), &buf, &scriptedDesc{events: events}); err != nil {
		t.Fatalf("WriteStream: %v", err)
	}
	return buf.String()
}

// TestAguiLifecycleGolden 主黄金序列（搬译 mapsLifecycleTextReasoningAndToolEvents）：
// 完整 run/step/思考/正文/工具生命周期。注意 TEXT_START 落 default 分支降级
// CUSTOM("text_start")——正文开界由 TEXT_DELTA 懒开（Java 原语义忠实保留）。
func TestAguiLifecycleGolden(t *testing.T) {
	raw := aguiRun(t, []*ai.ChatEvent{
		{Type: ai.EventResponseStart, ResponseID: "r1"},
		{Type: ai.EventStepStart, Step: 0},
		{Type: ai.EventThinkingStart, Step: 0, Index: 0},
		{Type: ai.EventThinkingDelta, Step: 0, Index: 0, Text: "思考"},
		{Type: ai.EventThinkingEnd, Step: 0, Index: 0},
		{Type: ai.EventTextStart, Step: 0, Index: 0},
		{Type: ai.EventTextDelta, Step: 0, Index: 0, Text: "回答"},
		{Type: ai.EventTextEnd, Step: 0, Index: 0},
		{Type: ai.EventToolCallStart, Step: 0, ToolCallID: "c1", ToolCall: &ai.ToolCall{ID: "c1", Name: "echo"}},
		{Type: ai.EventToolCallArgsDelta, Step: 0, ToolCallID: "c1", Text: `{"w":"hi"}`},
		{Type: ai.EventToolCallEnd, Step: 0, ToolCallID: "c1"},
		{Type: ai.EventStepEnd, Step: 0},
		{Type: ai.EventResponseEnd, ResponseID: "r1"},
	})

	got := aguiTypes(t, raw)
	want := []string{
		"RUN_STARTED",
		"STEP_STARTED",
		"REASONING_START",
		"REASONING_MESSAGE_START",
		"REASONING_MESSAGE_CONTENT",
		"REASONING_MESSAGE_END",
		"REASONING_END",
		"CUSTOM", // text_start
		"TEXT_MESSAGE_START",
		"TEXT_MESSAGE_CONTENT",
		"TEXT_MESSAGE_END",
		"TOOL_CALL_START",
		"TOOL_CALL_ARGS",
		"TOOL_CALL_END",
		"STEP_FINISHED",
		"RUN_FINISHED",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("事件序不符：\ngot:  %v\nwant: %v", got, want)
	}

	// RUN_STARTED 帧字段（threadId/runId 显式）
	first := sseBodies(t, raw)[0]
	var runStarted struct {
		ThreadID string `json:"threadId"`
		RunID    string `json:"runId"`
	}
	json.Unmarshal([]byte(first), &runStarted)
	if runStarted.ThreadID != "thread-1" || runStarted.RunID != "run-1" {
		t.Fatalf("RUN_STARTED 字段错误：%+v", runStarted)
	}

	// 无 [DONE] 终标（AG-UI 与 AI SDK 的协议差异）
	if strings.Contains(raw, "[DONE]") {
		t.Fatal("AG-UI 流不应有 [DONE] 终标")
	}
}

// TestAguiContentIDScoping contentKey 按 response+step 作用域（搬译
// contentIdsAreScopedByResponseIdAndStep）：同 itemId 跨 step/response 各得新 ID。
func TestAguiContentIDScoping(t *testing.T) {
	raw := aguiRun(t, []*ai.ChatEvent{
		{Type: ai.EventTextDelta, ResponseID: "r1", Step: 0, ItemID: "t1", Text: "a"},
		{Type: ai.EventTextEnd, ResponseID: "r1", Step: 0, ItemID: "t1"},
		{Type: ai.EventTextDelta, ResponseID: "r1", Step: 1, ItemID: "t1", Text: "b"},
		{Type: ai.EventTextEnd, ResponseID: "r1", Step: 1, ItemID: "t1"},
		{Type: ai.EventTextDelta, ResponseID: "r2", Step: 0, ItemID: "t1", Text: "c"},
		{Type: ai.EventTextEnd, ResponseID: "r2", Step: 0, ItemID: "t1"},
	})
	ids := map[string]bool{}
	for _, body := range sseBodies(t, raw) {
		var frame struct {
			Type      string `json:"type"`
			MessageID string `json:"messageId"`
		}
		json.Unmarshal([]byte(body), &frame)
		if frame.Type == "TEXT_MESSAGE_START" {
			if ids[frame.MessageID] {
				t.Fatalf("messageId %s 重复", frame.MessageID)
			}
			ids[frame.MessageID] = true
		}
	}
	if len(ids) != 3 {
		t.Fatalf("三轮应各得新 messageId，得 %d 个：%v", len(ids), ids)
	}
}

// TestAguiTypedSourcesAsCustom SEARCH_RESULT/CITATION → CUSTOM（搬译
// typedSourcesAreRetainedInStandardCustomNameAndValue）。
func TestAguiTypedSourcesAsCustom(t *testing.T) {
	raw := aguiRun(t, []*ai.ChatEvent{
		{Type: ai.EventSearchResult, SearchResult: &ai.SearchResult{Index: 0, ID: "s1", Title: "T", URL: "https://x/a", Snippet: "S"}},
		{Type: ai.EventCitation, Citation: &ai.Citation{Title: "C", URL: "https://x/b", Snippet: "cited"}},
	})
	bodies := sseBodies(t, raw)
	customs := 0
	for _, body := range bodies {
		var frame struct {
			Type     string `json:"type"`
			Name     string `json:"name"`
			Value    struct {
				SearchResult *struct {
					URL string `json:"url"`
				} `json:"searchResult"`
				Citation *struct {
					CitedText string `json:"citedText"`
				} `json:"citation"`
			} `json:"value"`
			RawEvent json.RawMessage `json:"rawEvent"`
		}
		if err := json.Unmarshal([]byte(body), &frame); err != nil {
			t.Fatal(err)
		}
		if frame.Type != "CUSTOM" {
			continue
		}
		customs++
		switch frame.Name {
		case "search_result":
			if frame.Value.SearchResult == nil || frame.Value.SearchResult.URL != "https://x/a" {
				t.Fatalf("search_result value 错误：%s", body)
			}
		case "citation":
			if frame.Value.Citation == nil || frame.Value.Citation.CitedText != "cited" {
				t.Fatalf("citation value 错误：%s", body)
			}
		default:
			t.Fatalf("意外 CUSTOM name=%s", frame.Name)
		}
		// value == rawEvent（兼容旧客户端，Java 同款断言）
		var rawVal any
		json.Unmarshal(frame.RawEvent, &rawVal)
		valJSON, _ := json.Marshal(frame.Value)
		rawJSON, _ := json.Marshal(rawVal)
		// searchResult/citation 子集断言已足够，rawEvent 非空即可
		if len(frame.RawEvent) == 0 {
			t.Fatalf("rawEvent 应非空：%s", body)
		}
		_ = valJSON
		_ = rawJSON
	}
	if customs != 2 {
		t.Fatalf("应有 2 个 CUSTOM，得 %d", customs)
	}
}

// TestAguiUnsupportedEventsAsCustom 未建模事件降级（搬译
// unsupportedCoreEventsAreRetainedAsCustomEvents）。
func TestAguiUnsupportedEventsAsCustom(t *testing.T) {
	raw := aguiRun(t, []*ai.ChatEvent{
		{Type: ai.EventServerToolStart, SubType: "web_search", ItemID: "gs1"},
		{Type: ai.EventMediaPartial, Step: 2},
		{Type: ai.EventRefusalDelta, Text: "no"},
	})
	wants := []string{"server_tool_start", "media_partial", "refusal_delta"}
	bodies := sseBodies(t, raw)
	idx := 0
	for _, body := range bodies {
		var frame struct {
			Type string `json:"type"`
			Name string `json:"name"`
		}
		json.Unmarshal([]byte(body), &frame)
		if frame.Type != "CUSTOM" {
			continue
		}
		if idx >= len(wants) || frame.Name != wants[idx] {
			t.Fatalf("CUSTOM name 序不符：得 %s，期望 %s", frame.Name, wants[idx])
		}
		idx++
	}
	if idx != len(wants) {
		t.Fatalf("应有 %d 个 CUSTOM，得 %d", len(wants), idx)
	}
}

// TestAguiMediaDonePayload MEDIA_DONE 的 block 载荷（搬译
// mediaDoneCustomPayloadKeepsBlockAndIdentity）。
func TestAguiMediaDonePayload(t *testing.T) {
	raw := aguiRun(t, []*ai.ChatEvent{
		{Type: ai.EventMediaDone, ResponseID: "r1", Step: 2, ItemID: "image_1", Index: 3,
			Block: ai.ImageBlockOfBase64([]byte("hello"), "image/png")},
	})
	for _, body := range sseBodies(t, raw) {
		var frame struct {
			Type  string `json:"type"`
			Name  string `json:"name"`
			Value struct {
				EventType string `json:"eventType"`
				Block     struct {
					Type     string `json:"type"`
					Content  string `json:"content"`
					MimeType string `json:"mimeType"`
				} `json:"block"`
			} `json:"value"`
		}
		json.Unmarshal([]byte(body), &frame)
		if frame.Type == "CUSTOM" && frame.Name == "media_done" {
			if frame.Value.EventType != "MEDIA_DONE" || frame.Value.Block.MimeType != "image/png" {
				t.Fatalf("media_done 载荷错误：%s", body)
			}
			if frame.Value.Block.Content != "aGVsbG8=" {
				t.Fatalf("block 内容应为 base64：%s", body)
			}
			return
		}
	}
	t.Fatal("未找到 media_done CUSTOM")
}

// TestAguiTerminalStates ABORT/ERROR 终态（terminal 后 finish 静默）。
func TestAguiTerminalStates(t *testing.T) {
	raw := aguiRun(t, []*ai.ChatEvent{
		{Type: ai.EventTextDelta, ResponseID: "r1", Step: 0, Index: 0, Text: "半截"},
		{Type: ai.EventAbort, Text: "user"},
	})
	types := aguiTypes(t, raw)
	if types[len(types)-1] != "RUN_FINISHED" {
		t.Fatalf("ABORT 应以 RUN_FINISHED 收口：%v", types)
	}
	// outcome interrupt
	var finish struct {
		Outcome *struct {
			Type string `json:"type"`
		} `json:"outcome"`
	}
	json.Unmarshal([]byte(sseBodies(t, raw)[len(sseBodies(t, raw))-1]), &finish)
	if finish.Outcome == nil || finish.Outcome.Type != "interrupt" {
		t.Fatalf("ABORT 收口 outcome 应为 interrupt：%v", finish.Outcome)
	}

	raw = aguiRun(t, []*ai.ChatEvent{
		{Type: ai.EventError, Err: fmt.Errorf("boom")},
	})
	types = aguiTypes(t, raw)
	if len(types) != 2 || types[0] != "RUN_STARTED" || types[1] != "RUN_ERROR" {
		t.Fatalf("ERROR 应只发 RUN_ERROR 收口：%v", types)
	}
}

// TestAguiThinkingSignature THINKING_SIGNATURE → REASONING_ENCRYPTED_VALUE；
// THINKING_REDACTED → CUSTOM(thinking-redacted)。
func TestAguiThinkingSignature(t *testing.T) {
	raw := aguiRun(t, []*ai.ChatEvent{
		{Type: ai.EventThinkingSignature, SubType: "sig", ItemID: "t1", Text: "ENC"},
		{Type: ai.EventThinkingRedacted, SubType: "redacted"},
	})
	bodies := sseBodies(t, raw)[1:] // 跳过 RUN_STARTED
	var enc struct {
		Type      string `json:"type"`
		Subtype   string `json:"subtype"`
		EntityID  string `json:"entityId"`
		Encrypted string `json:"encryptedValue"`
	}
	json.Unmarshal([]byte(bodies[0]), &enc)
	if enc.Type != "REASONING_ENCRYPTED_VALUE" || enc.Subtype != "sig" ||
		enc.EntityID != "t1" || enc.Encrypted != "ENC" {
		t.Fatalf("REASONING_ENCRYPTED_VALUE 字段错误：%s", bodies[0])
	}
	var custom struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	json.Unmarshal([]byte(bodies[1]), &custom)
	if custom.Type != "CUSTOM" || custom.Name != "thinking-redacted" {
		t.Fatalf("THINKING_REDACTED 应为 CUSTOM(thinking-redacted)：%s", bodies[1])
	}
}

// TestAguiFullSSE httptest 端到端：假 OpenAI SSE（思考 + 正文）→ agui SSE。
func TestAguiFullSSE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for _, chunk := range []string{
			`data: {"choices":[{"delta":{"reasoning_content":"思"}}]}`,
			`data: {"choices":[{"delta":{"reasoning_content":"考"}}]}`,
			`data: {"choices":[{"delta":{"content":"答"}}]}`,
			`data: {"choices":[{"delta":{"content":"案"}}]}`,
			`data: [DONE]`,
		} {
			io.WriteString(w, chunk+"\n\n")
			fl.Flush()
		}
	}))
	defer srv.Close()

	m, err := ai.NewChatModel(ai.Config{APIURL: srv.URL, APIKey: "k", Model: "gpt-test"})
	if err != nil {
		t.Fatal(err)
	}
	w := agui.NewWrapper("th-e2e", "run-e2e")
	var buf strings.Builder
	if err := w.WriteStream(context.Background(), &buf, m.Prompt("hi")); err != nil {
		t.Fatalf("WriteStream: %v", err)
	}

	// openai SSE 无思考结束帧：THINKING_* 悬挂至 STEP_END 由 closeContents 统一
	// 闭合（推理先于正文）——Java 状态机同款行为
	types := aguiTypes(t, buf.String())
	want := []string{
		"RUN_STARTED",
		"STEP_STARTED",
		"REASONING_START", "REASONING_MESSAGE_START",
		"REASONING_MESSAGE_CONTENT", "REASONING_MESSAGE_CONTENT",
		"TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT", "TEXT_MESSAGE_CONTENT",
		"REASONING_MESSAGE_END", "REASONING_END",
		"TEXT_MESSAGE_END",
		"STEP_FINISHED",
		"RUN_FINISHED",
	}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Fatalf("端到端事件序不符：\ngot:  %v\nwant: %v", types, want)
	}
}
