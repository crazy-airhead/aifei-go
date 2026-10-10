// interactions_test.go gemini Interactions 方言黑盒测试：黄金样本搬译 Java
// GeminiInteractionsDialectUrlTest / RequestBuilderTest / EventTest /
// TerminalStatusTest / ThoughtSignatureTest。
package ai_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ai "github.com/crazy-airhead/aifei-go/ai"
	gemini "github.com/crazy-airhead/aifei-go/ai/dialect/gemini"
)

func newInteractionsDialect() *gemini.InteractionsDialect {
	return &gemini.InteractionsDialect{}
}

// ---- URL/matched 矩阵（搬译 GeminiInteractionsDialectUrlTest）----

func TestInteractionsURLMatrix(t *testing.T) {
	d := newInteractionsDialect()
	cases := []struct{ in, want string }{
		{"https://example.test/v1beta/interactions", "https://example.test/v1beta/interactions"},
		{"https://example.test/v1beta/interactions/", "https://example.test/v1beta/interactions"},
		{"https://example.test", "https://example.test/v1/interactions"},
		{"https://example.test/", "https://example.test/v1/interactions"},
		{"https://example.test/v1", "https://example.test/v1/interactions"},
		{"https://example.test/v1/", "https://example.test/v1/interactions"},
		{"https://example.test/v1beta", "https://example.test/v1beta/interactions"},
		{"https://example.test/v1alpha/", "https://example.test/v1alpha/interactions"},
		{"https://example.test?foo=1#section", "https://example.test/v1/interactions?foo=1"},
	}
	for _, c := range cases {
		if got := gemini.InteractionsURL(c.in); got != c.want {
			t.Fatalf("URL 归一 %q → %q，期望 %q", c.in, got, c.want)
		}
	}

	// matched：standard 命中（含弃用别名）
	for _, std := range []string{"google-interactions", "gemini-interactions", "Google-Interactions"} {
		if !d.Matched(&ai.Config{Standard: std}) {
			t.Fatalf("standard=%s 应命中", std)
		}
	}
	// URL 后缀命中（?alt=sse 可带）
	if !d.Matched(&ai.Config{APIURL: "https://example.test/v1beta/interactions?alt=sse"}) {
		t.Fatal("interactions 后缀（带 query）应命中")
	}
	// generateContent URL 不命中
	if d.Matched(&ai.Config{APIURL: "https://example.test/v1/models/gemini-3:generateContent"}) {
		t.Fatal("generateContent URL 不应命中")
	}
	// standard 非空且不匹配 → false
	if d.Matched(&ai.Config{Standard: "openai", APIURL: "https://example.test/v1/interactions"}) {
		t.Fatal("standard 显式指定时不按 URL 命中")
	}
}

// ---- 请求体黄金（搬译 RequestBuilderTest）----

func interactionsBuild(t *testing.T, msgs []*ai.Message, isStream bool, optFn func(*ai.ChatOptions)) map[string]any {
	t.Helper()
	d := newInteractionsDialect()
	cfg := &ai.Config{APIURL: "https://example.test/v1/interactions", Model: "gemini-3-pro"}
	opts := &ai.ChatOptions{}
	if optFn != nil {
		optFn(opts)
	}
	body, url, err := d.BuildRequestJSON(cfg, opts, msgs, isStream)
	if err != nil {
		t.Fatal(err)
	}
	if !isStream && url != cfg.APIURL {
		t.Fatalf("非流式 URL 应原样，得 %s", url)
	}
	if isStream && url != cfg.APIURL+"?alt=sse" {
		t.Fatalf("流式 URL 应追加 alt=sse（仅缺失时），得 %s", url)
	}
	var req map[string]any
	json.Unmarshal(body, &req)
	return req
}

func TestInteractionsRequestShape(t *testing.T) {
	msgs := []*ai.Message{
		ai.OfSystem("one"),
		ai.OfSystem("two"),
		ai.OfUser("hi"),
	}
	req := interactionsBuild(t, msgs, true, func(o *ai.ChatOptions) {
		o.OptionSet("generation_config", map[string]any{"max_completion_tokens": "256", "thinking_level": "minimal"})
		o.OptionSet("reasoning_effort", "minimal")
		o.OptionSet("max_completion_tokens", 128)
		o.OptionSet("tool_choice", "required")
	})

	if req["model"] != "gemini-3-pro" {
		t.Fatalf("model 在请求体：%v", req["model"])
	}
	if req["system_instruction"] != "one\n\ntwo" {
		t.Fatalf("system_instruction 应为 \\n\\n 连接的纯字符串：%v", req["system_instruction"])
	}
	input := req["input"].([]any)
	if len(input) != 1 {
		t.Fatalf("input 应只有 1 个 user_input step：%v", input)
	}
	if req["stream"] != true || req["store"] != false {
		t.Fatalf("stream/store 错误：%v %v", req["stream"], req["store"])
	}

	gc := req["generation_config"].(map[string]any)
	if gc["max_output_tokens"] != float64(256) {
		t.Fatalf("max_output_tokens 应 int 归一（含字符串 \"256\" 透传）：(%T) %v", gc["max_output_tokens"], gc["max_output_tokens"])
	}
	if gc["thinking_level"] != "minimal" {
		t.Fatalf("thinking_level：%v", gc["thinking_level"])
	}
	if gc["thinking_summaries"] != "auto" {
		t.Fatalf("有 effort 时 thinking_summaries=auto：%v", gc["thinking_summaries"])
	}
	if gc["tool_choice"] != "any" {
		t.Fatalf("tool_choice required→any：%v", gc["tool_choice"])
	}
	if _, has := req["generationConfig"]; has {
		t.Fatal("不应有 camelCase generationConfig 键")
	}
}

func TestInteractionsRequestNamedToolChoice(t *testing.T) {
	req := interactionsBuild(t, []*ai.Message{ai.OfUser("q")}, false, func(o *ai.ChatOptions) {
		o.OptionSet("tool_choice", map[string]any{"function": map[string]any{"name": "weather"}})
	})
	gc := req["generation_config"].(map[string]any)
	tc := gc["tool_choice"].(map[string]any)
	at := tc["allowed_tools"].(map[string]any)
	if at["mode"] != "any" {
		t.Fatalf("allowed_tools.mode：%v", at["mode"])
	}
	if tools := at["tools"].([]any); len(tools) != 1 || tools[0] != "weather" {
		t.Fatalf("allowed_tools.tools：%v", at["tools"])
	}
	if _, has := req["tool_choice"]; has {
		t.Fatal("根级不应有 tool_choice")
	}
}

func TestInteractionsRequestStructuredOutput(t *testing.T) {
	req := interactionsBuild(t, []*ai.Message{ai.OfUser("q")}, false, func(o *ai.ChatOptions) {
		o.OutputSchema = `{"type":"object"}`
	})
	rf := req["response_format"].([]any)
	item := rf[0].(map[string]any)
	if item["type"] != "text" || item["mime_type"] != "application/json" {
		t.Fatalf("response_format[0]：%v", item)
	}
	if _, has := req["generation_config"]; has {
		t.Fatal("无白名单选项时不应输出 generation_config")
	}
}

func TestInteractionsRequestToolsAndSteps(t *testing.T) {
	// 工具扁平数组
	req := interactionsBuild(t, []*ai.Message{ai.OfUser("q")}, false, func(o *ai.ChatOptions) {
		o.Tools = []ai.FunctionTool{ai.NewTool("weather").Desc("查天气").Build()}
	})
	tools := req["tools"].([]any)
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" || tool["name"] != "weather" || tool["description"] != "查天气" {
		t.Fatalf("tools 扁平数组形态：%v", tool)
	}
	if _, ok := tool["parameters"]; !ok {
		t.Fatal("parameters 应存在")
	}

	// assistant 工具调用回放：thought step（签名）+ function_call steps
	am := &ai.AssistantMessage{
		Text: "答",
		ToolCalls: []ai.ToolCall{{ID: "call-1", Name: "getWeather", ArgumentsStr: `{"city":"hz"}`, Arguments: map[string]any{"city": "hz"}}},
	}
	// 无签名状态时不发 thought step
	req = interactionsBuild(t, []*ai.Message{ai.OfUser("q"), {Role: ai.RoleAssistant, Content: am},
		{Role: ai.RoleTool, Content: &ai.ToolMessage{Name: "getWeather", CallID: "call-1", Result: &ai.ToolResult{Text: "sunny"}}}}, false, nil)
	input := req["input"].([]any)
	if len(input) != 4 {
		t.Fatalf("input 步数错误（有文本的 toolCalls 先写 model_output step）：%v", input)
	}
	fc := input[2].(map[string]any)
	if fc["type"] != "function_call" || fc["name"] != "getWeather" || fc["id"] != "call-1" {
		t.Fatalf("function_call step：%v", fc)
	}
	if args := fc["arguments"].(map[string]any); args["city"] != "hz" {
		t.Fatalf("arguments object：%v", args)
	}
	fr := input[3].(map[string]any)
	if fr["type"] != "function_result" || fr["call_id"] != "call-1" {
		t.Fatalf("function_result step：%v", fr)
	}
	if arr := fr["result"].([]any); len(arr) != 1 || arr[0].(map[string]any)["text"] != "sunny" {
		t.Fatalf("result 数组形态：%v", fr["result"])
	}
}

// ---- 流式事件回放（搬译 EventTest）----

func interactionsStream(t *testing.T, frames ...string) (*ai.Accumulator, []*ai.ChatEvent) {
	t.Helper()
	return matrixStream(newInteractionsDialect(), &ai.Config{Model: "gemini-3-pro"}, frames...)
}

func eventTypes(events []*ai.ChatEvent) []string {
	var out []string
	for _, ev := range events {
		out = append(out, ev.Type.String())
	}
	return out
}

func TestInteractionsStreamLifecycle(t *testing.T) {
	_, events := interactionsStream(t,
		`{"event_type":"interaction.created","interaction":{"id":"int_1","model":"gemini-3-pro"}}`,
		`{"event_type":"step.start","index":0,"step":{"type":"model_output","content":[{"type":"text","text":"hello "}]}}`,
		`{"event_type":"step.delta","index":0,"delta":{"type":"text","text":"world"}}`,
		`{"event_type":"step.stop","index":0,"step":{"type":"model_output"}}`,
		`{"event_type":"interaction.completed","id":"v1_x","interaction":{"status":"completed","usage":{"total_input_tokens":7,"total_output_tokens":20,"total_thought_tokens":22,"total_tokens":49}}}`,
	)

	got := strings.Join(eventTypes(events), ",")
	want := "STATUS,TEXT_DELTA,TEXT_DELTA,STATUS"
	if got != want {
		t.Fatalf("事件序不符：\ngot:  %s\nwant: %s", got, want)
	}
	if events[0].ItemID != "int_1" {
		t.Fatalf("STATUS itemId 应为交互 id：%v", events[0].ItemID)
	}
	// usage 映射（interaction.completed）
	for _, ev := range events {
		if ev.Type == ai.EventStatus && ev.Usage != nil {
			u := ev.Usage
			if u.PromptTokens != 7 || u.CompletionTokens != 20 || u.ThinkTokens != 22 || u.TotalTokens != 49 {
				t.Fatalf("usage 映射错误：%+v", u)
			}
			return
		}
	}
	t.Fatal("interaction.completed 的 STATUS 应带 usage")
}

func TestInteractionsStreamServerToolThreeState(t *testing.T) {
	_, events := interactionsStream(t,
		`{"event_type":"step.start","index":0,"step":{"type":"google_search_call","id":"gs_1"}}`,
		`{"event_type":"step.delta","index":0,"step":{"type":"google_search_call","id":"gs_1"},"delta":{"type":"arguments_delta","arguments":"{\"q\":"}}`,
		`{"event_type":"step.stop","index":0,"step":{"type":"google_search_call","id":"gs_1"}}`,
	)
	got := strings.Join(eventTypes(events), ",")
	want := "SERVER_TOOL_START,SERVER_TOOL_ARGS_DELTA,SERVER_TOOL_RESULT"
	if got != want {
		t.Fatalf("服务端工具三态：\ngot:  %s\nwant: %s", got, want)
	}
	if events[0].SubType != "google_search_call" || events[0].ItemID != "gs_1" {
		t.Fatalf("SERVER_TOOL_START 字段：%+v", events[0])
	}
}

func TestInteractionsStreamSearchResultsProjection(t *testing.T) {
	acc, events := interactionsStream(t,
		`{"event_type":"step.start","index":0,"step":{"type":"google_search_result","id":"gr_1","result":[`+
			`{"id":"r1","title":"T1","url":"https://x/1","snippet":"S1"},`+
			`{"web":{"title":"T2","uri":"https://x/2","summary":"S2"}},`+
			`{"search_suggestions":["a"]}]}}`,
		// stop 重放同 step：不得重复投影
		`{"event_type":"step.stop","index":0,"step":{"type":"google_search_result","id":"gr_1","result":[`+
			`{"id":"r1","title":"T1","url":"https://x/1","snippet":"S1"}]}}`,
	)
	var results []*ai.SearchResult
	for _, ev := range events {
		if ev.Type == ai.EventSearchResult {
			results = append(results, ev.SearchResult)
		}
	}
	if len(results) != 2 {
		t.Fatalf("应恰 2 个 SEARCH_RESULT（suggestions 不算、stop 重放去重），得 %d", len(results))
	}
	if results[0].URL != "https://x/1" || results[0].Snippet != "S1" {
		t.Fatalf("results[0]：%+v", results[0])
	}
	if results[1].URL != "https://x/2" || results[1].Snippet != "S2" {
		t.Fatalf("web 包装形态 results[1]：%+v", results[1])
	}
	if len(acc.Message().SearchResults) != 2 {
		t.Fatalf("终态 SearchResults 聚合：%d", len(acc.Message().SearchResults))
	}
}

func TestInteractionsStreamFunctionCall(t *testing.T) {
	acc, events := interactionsStream(t,
		`{"event_type":"step.start","index":0,"step":{"type":"function_call","name":"getWeather","id":"call-a"}}`,
		`{"event_type":"step.delta","index":0,"step":{"type":"function_call"},"delta":{"type":"arguments_delta","arguments":"{\"city\":"}}`,
		`{"event_type":"step.delta","index":0,"step":{"type":"function_call"},"delta":{"type":"arguments_delta","arguments":"\"hz\"}"}}`,
		`{"event_type":"step.stop","index":0,"step":{"type":"function_call","id":"call-a","name":"getWeather"}}`,
	)
	got := strings.Join(eventTypes(events), ",")
	want := "TOOL_CALL_START,TOOL_CALL_ARGS_DELTA,TOOL_CALL_ARGS_DELTA"
	if got != want {
		t.Fatalf("函数调用事件序：\ngot:  %s\nwant: %s", got, want)
	}
	// step.stop 后无 TOOL_CALL_END（END 由 core 收尾）
	for _, ev := range events {
		if ev.Type == ai.EventToolCallEnd {
			t.Fatal("方言不发 TOOL_CALL_END（core 唯一收尾）")
		}
	}
	calls := acc.Message().ToolCalls
	if len(calls) != 1 || calls[0].Name != "getWeather" || calls[0].ID != "call-a" {
		t.Fatalf("终态 ToolCalls：%+v", calls)
	}
	if calls[0].ArgumentsStr != `{"city":"hz"}` {
		t.Fatalf("参数拼接：%q", calls[0].ArgumentsStr)
	}
}

func TestInteractionsStreamThoughtAndSignature(t *testing.T) {
	acc, events := interactionsStream(t,
		`{"event_type":"step.start","index":0,"step":{"type":"thought"}}`,
		`{"event_type":"step.delta","index":0,"delta":{"type":"thought_summary","content":[{"type":"text","text":"思考"}]}}`,
		`{"event_type":"step.delta","index":0,"delta":{"type":"thought_signature","signature":"sig_int"}}`,
		`{"event_type":"step.start","index":1,"step":{"type":"function_call","name":"getWeather","id":"call-a"}}`,
		`{"event_type":"step.stop","index":1,"step":{"type":"function_call","id":"call-a"}}`,
	)
	got := strings.Join(eventTypes(events), ",")
	if !strings.Contains(got, "THINKING_DELTA") || !strings.Contains(got, "THINKING_SIGNATURE") {
		t.Fatalf("思考事件缺失：%s", got)
	}
	if acc.ThinkingSignature() != "sig_int" {
		t.Fatalf("thinkingSignature 未积累：%q", acc.ThinkingSignature())
	}
	// step.stop 签名绑定：终态协议状态
	if acc.TerminalProtocolState("google.interactions") == nil {
		t.Fatal("签名应绑定 google.interactions 协议状态")
	}

	// BuildAssistantToolCallMessage：thought step + function_call step
	msg := newInteractionsDialect().BuildAssistantToolCallMessage(acc, acc.ToolCallBuilders())
	if msg == nil {
		t.Fatal("应产出回放消息")
	}
	am, _ := msg.Content.(*ai.AssistantMessage)
	if am == nil || len(am.ToolCalls) != 1 || am.ToolCalls[0].ID != "call-a" {
		t.Fatalf("回放消息：%+v", am)
	}
	if am.ProtocolStates["google.interactions"] == nil {
		t.Fatal("回放消息应带签名协议状态")
	}

	// 重建请求：input 含独立 thought step（签名）+ function_call step
	req := interactionsBuild(t, []*ai.Message{ai.OfUser("q"), msg}, false, nil)
	input := req["input"].([]any)
	if len(input) != 3 { // user_input + thought + function_call
		t.Fatalf("重建 input 步数：%v", input)
	}
	thought := input[1].(map[string]any)
	if thought["type"] != "thought" || thought["signature"] != "sig_int" {
		t.Fatalf("thought step：%v", thought)
	}
	fc := input[2].(map[string]any)
	if _, has := fc["thought_signature"]; has {
		t.Fatal("function_call step 不应有 thought_signature 字段")
	}
}

func TestInteractionsStreamUnknownEvents(t *testing.T) {
	_, events := interactionsStream(t,
		`{"event_type":"interaction.some_future","foo":1}`,
		`{"event_type":"step.start","index":0,"step":{"type":"text"}}`,
	)
	for _, ev := range events {
		if ev.Type == ai.EventTextDelta || ev.Type == ai.EventThinkingDelta {
			t.Fatalf("未建模 step.type=text 不应产内容事件：%v", ev.Type)
		}
	}
	if len(events) == 0 || events[0].Type != ai.EventRaw || events[0].RawType != "interaction.some_future" {
		t.Fatalf("未知 event_type 应 RAW：%v", events)
	}
}

// ---- 终态矩阵（搬译 TerminalStatusTest）----

// interactionsCall 非流式回放。
func interactionsCall(t *testing.T, body string) *ai.Accumulator {
	t.Helper()
	acc := ai.NewAccumulator()
	ctx := ai.NewStreamContext(&ai.Config{Model: "gemini-3-pro"}, acc, false, 0, func(ev *ai.ChatEvent) {})
	newInteractionsDialect().ParseResponseJSON(ctx, body)
	return acc
}

func TestInteractionsTerminalStatus(t *testing.T) {
	cases := []struct {
		frames []string
		errMsg string
		finished bool
		reason  string
	}{
		{[]string{`{"event_type":"error","error":{"message":"boom"}}`}, "boom", true, ""},
		{[]string{`{"status":"failed","error":{"message":"model overloaded"}}`}, "model overloaded", true, ""},
		{[]string{`{"status":"failed"}`}, "Gemini interaction failed", true, ""},
		{[]string{`{"status":"cancelled"}`}, "", true, ""},
		{[]string{`{"status":"incomplete"}`}, "", true, ""},
		{[]string{`{"status":"budget_exceeded"}`}, "", true, ""},
		{[]string{`{"status":"in_progress"}`}, "", false, ""},
		{[]string{`{"status":"queued"}`}, "", false, ""},
	}
	for i, c := range cases {
		var acc *ai.Accumulator
		if strings.HasPrefix(c.frames[0], `{"event_type"`) {
			acc, _ = interactionsStream(t, c.frames...)
		} else {
			acc = interactionsCall(t, c.frames[0])
		}
		if acc.IsFinished() != c.finished {
			t.Fatalf("case %d finished=%v 期望 %v", i, acc.IsFinished(), c.finished)
		}
		if c.errMsg != "" {
			if acc.Err() == nil || !strings.Contains(acc.Err().Error(), c.errMsg) {
				t.Fatalf("case %d 错误信息：%v", i, acc.Err())
			}
		} else if acc.Err() != nil {
			t.Fatalf("case %d 不应报错：%v", i, acc.Err())
		}
	}
	// budget_exceeded → length
	acc := interactionsCall(t, `{"status":"budget_exceeded"}`)
	if r := accFinishReason(acc); r != "length" {
		t.Fatalf("budget_exceeded finishReason=length，得 %q", r)
	}
}

func accFinishReason(acc *ai.Accumulator) string {
	return acc.BuildResponse("", nil).FinishReason()
}

// ---- 非流式（搬译 ThoughtSignatureTest 非流式部分 + 多语义帧）----

func TestInteractionsNonStream(t *testing.T) {
	d := newInteractionsDialect()
	cfg := &ai.Config{Model: "gemini-3-pro"}
	acc := ai.NewAccumulator()
	var events []*ai.ChatEvent
	ctx := ai.NewStreamContext(cfg, acc, false, 0, func(ev *ai.ChatEvent) { events = append(events, ev) })
	d.ParseResponseJSON(ctx, `{"id":"v1_1","model":"gemini-3-pro","status":"completed",
		"steps":[
			{"type":"thought","summary":[{"type":"text","text":"思考"}],"signature":"sig-1"},
			{"type":"google_search_call","id":"gs_1"},
			{"type":"model_output","content":[{"type":"text","text":"杭州今天晴"}]}],
		"usage":{"total_input_tokens":7,"total_output_tokens":20,"total_tokens":27}}`)

	msg := acc.Message()
	if msg.Text != "杭州今天晴" || msg.Thinking != "思考" {
		t.Fatalf("终态消息：%+v", msg)
	}
	if acc.ThinkingSignature() != "sig-1" {
		t.Fatalf("签名：%q", acc.ThinkingSignature())
	}
	// 多语义帧：THINKING_SIGNATURE 事件 + SERVER_TOOL_START，无 TEXT_DELTA
	hasSig, hasServer := false, false
	for _, ev := range events {
		switch ev.Type {
		case ai.EventTextDelta:
			t.Fatal("非流式不产 TEXT_DELTA")
		case ai.EventThinkingSignature:
			hasSig = ev.Text == "sig-1"
		case ai.EventServerToolStart:
			hasServer = ev.SubType == "google_search_call"
		}
	}
	if !hasSig || !hasServer {
		t.Fatalf("THINKING_SIGNATURE/SERVER_TOOL_START 缺失：%v", eventTypes(events))
	}
	if u := acc.Usage(); u == nil || u.TotalTokens != 27 {
		t.Fatalf("usage 仅 finished 时解析：%v", acc.Usage())
	}

	// steps 数组解析（ParseAssistantMessage）→ 协议状态 JSON 往返
	msgs := d.ParseAssistantMessage(acc, map[string]any{"steps": []any{
		map[string]any{"type": "thought", "signature": "sig_step"},
		map[string]any{"type": "function_call", "id": "call-1", "name": "f", "arguments": map[string]any{"a": 1}},
	}})
	if len(msgs) != 1 || len(msgs[0].ToolCalls) != 1 {
		t.Fatalf("steps 解析：%+v", msgs)
	}
	if msgs[0].ProtocolStates["google.interactions"] == nil {
		t.Fatal("steps 解析应带签名协议状态")
	}
}

// TestInteractionsEndToEnd httptest 假服务端到端：/interactions 端点 + alt=sse
// SSE 流 → 事件管线聚合。
func TestInteractionsEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/interactions") {
			t.Errorf("路径应为 /interactions：%s", r.URL.Path)
		}
		if r.URL.Query().Get("alt") != "sse" {
			t.Errorf("流式应带 alt=sse：%v", r.URL.RawQuery)
		}
		if r.Header.Get("x-goog-api-key") != "k1" {
			t.Errorf("x-goog-api-key 鉴权缺失")
		}
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if req["model"] != "gemini-3-pro" {
			t.Errorf("model 应在请求体：%v", req["model"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for _, chunk := range []string{
			`{"event_type":"interaction.created","interaction":{"id":"i1","model":"gemini-3-pro"}}`,
			`{"event_type":"step.start","index":0,"step":{"type":"model_output","content":[{"type":"text","text":"你好"}]}}`,
			`{"event_type":"interaction.completed","id":"i1","interaction":{"status":"completed"}}`,
		} {
			io.WriteString(w, "data: "+chunk+"\n\n")
			fl.Flush()
		}
	}))
	defer srv.Close()

	m, err := ai.NewChatModel(ai.Config{APIURL: srv.URL + "/v1/interactions", APIKey: "k1", Model: "gemini-3-pro"})
	if err != nil {
		t.Fatal(err)
	}
	if name := m.Dialect().Name(); name != "google-interactions" {
		t.Fatalf("URL 后缀应选中 interactions 方言，得 %s", name)
	}
	resp, err := ai.CallStream(context.Background(), m.Prompt("hi"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message().Text != "你好" {
		t.Fatalf("聚合文本：%q", resp.Message().Text)
	}
	if resp.FinishReason() != "stop" {
		t.Fatalf("finishReason：%q", resp.FinishReason())
	}
}
