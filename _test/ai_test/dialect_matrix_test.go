// dialect_matrix_test.go P1-b 方言矩阵回放测试：anthropic / gemini / ollama /
// dashscope 四方言各 ≥3 组回放（纯文本流 / 工具流 / 非流式或归一场景）、
// 请求 JSON 快照比对与 SelectDialect 优先级表驱动验收。
package ai_test

import (
	"bytes"
	"testing"

	"github.com/crazy-airhead/aifei-go/ai"
	anthropic "github.com/crazy-airhead/aifei-go/ai/dialect/anthropic"
	dashscope "github.com/crazy-airhead/aifei-go/ai/dialect/dashscope"
	gemini "github.com/crazy-airhead/aifei-go/ai/dialect/gemini"
	ollama "github.com/crazy-airhead/aifei-go/ai/dialect/ollama"
)

// matrixQueryOrderTool 四方言共用的工具定义。
func matrixQueryOrderTool() ai.FunctionTool {
	return ai.NewTool("query-order").
		Desc("查询订单").
		StringParam("orderId", "订单号", true).
		Build()
}

// matrixStream 方言参数化的流式回放：逐帧喂给 ParseResponseJSON。
func matrixStream(d ai.Dialect, cfg *ai.Config, frames ...string) (*ai.Accumulator, []*ai.ChatEvent) {
	acc := ai.NewAccumulator()
	acc.SetStream(true)
	var events []*ai.ChatEvent
	ctx := ai.NewStreamContext(cfg, acc, true, 0, func(ev *ai.ChatEvent) { events = append(events, ev) })
	for _, f := range frames {
		d.ParseResponseJSON(ctx, f)
	}
	return acc, events
}

// matrixCall 方言参数化的非流式回放。
func matrixCall(d ai.Dialect, cfg *ai.Config, body string) (*ai.Response, []*ai.ChatEvent, *ai.Accumulator) {
	acc := ai.NewAccumulator()
	var events []*ai.ChatEvent
	ctx := ai.NewStreamContext(cfg, acc, false, 0, func(ev *ai.ChatEvent) { events = append(events, ev) })
	d.ParseResponseJSON(ctx, body)
	return acc.BuildResponse("", events), events, acc
}

func matrixOf(t *testing.T, events []*ai.ChatEvent, typ ai.EventType) []*ai.ChatEvent {
	t.Helper()
	var out []*ai.ChatEvent
	for _, ev := range events {
		if ev.Type == typ {
			out = append(out, ev)
		}
	}
	return out
}

func matrixTexts(t *testing.T, events []*ai.ChatEvent, typ ai.EventType) []string {
	t.Helper()
	var out []string
	for _, ev := range matrixOf(t, events, typ) {
		out = append(out, ev.Text)
	}
	return out
}

func matrixNode(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("期望 JSON object，got %T (%v)", v, v)
	}
	return m
}

func matrixArray(t *testing.T, v any) []any {
	t.Helper()
	arr, ok := v.([]any)
	if !ok {
		t.Fatalf("期望 JSON array，got %T (%v)", v, v)
	}
	return arr
}

// matrixStrEq 字符串切片相等断言。
func matrixStrEq(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("切片长度不符：got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 项不符：got %q want %q（整体 got %v want %v）", i, got[i], want[i], got, want)
		}
	}
}

// ---- SelectDialect 优先级表驱动（P1-b 验收）----

func TestSelectDialectMatrix(t *testing.T) {
	cases := []struct {
		name string
		cfg  *ai.Config
		want string
	}{
		// ① Standard 精确匹配（优先级最高，不看 provider/URL）
		{"Standard anthropic", &ai.Config{Standard: "anthropic"}, "anthropic"},
		{"Standard ollama", &ai.Config{Standard: "ollama"}, "ollama"},
		{"Standard gemini", &ai.Config{Standard: "gemini"}, "gemini"},
		{"Standard dashscope", &ai.Config{Standard: "dashscope"}, "dashscope"},
		{"Standard 压过 provider", &ai.Config{Standard: "openai-completions", Provider: "anthropic"}, "openai-completions"},
		// ② Provider 命中（优先级序：dashscope10 < ollama20 < gemini30 < anthropic40 < openai100）
		{"provider claude", &ai.Config{Provider: "claude"}, "anthropic"},
		{"provider google", &ai.Config{Provider: "google"}, "gemini"},
		{"provider ollama", &ai.Config{Provider: "ollama"}, "ollama"},
		{"provider dashscope", &ai.Config{Provider: "dashscope"}, "dashscope"},
		// ②' 空 standard 时按端点特征匹配
		{"/messages 端点", &ai.Config{APIURL: "https://api.anthropic.com/v1/messages"}, "anthropic"},
		{"/api/chat 端点", &ai.Config{APIURL: "http://localhost:11434/api/chat"}, "ollama"},
		{":generateContent 端点", &ai.Config{APIURL: "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-pro:generateContent"}, "gemini"},
		{"百炼端点前缀", &ai.Config{APIURL: dashscope.URL_PREFIX + "aigc/text-generation/generation"}, "dashscope"},
		// ③ openai 补位
		{"空配置补位", &ai.Config{}, "openai-completions"},
		{"未知 Standard 补位", &ai.Config{Standard: "not-registered", APIURL: "https://api.openai.com"}, "openai-completions"},
	}
	for _, c := range cases {
		d := ai.SelectDialect(c.cfg)
		if d == nil || d.Name() != c.want {
			t.Fatalf("%s：SelectDialect = %v，want %s", c.name, d, c.want)
		}
	}
}

// ---- anthropic ----

func TestAnthropicStreamText(t *testing.T) {
	acc, events := matrixStream(&anthropic.Dialect{}, &ai.Config{Model: "claude-sonnet-4-5"},
		`data: {"type":"message_start","message":{"id":"msg_01","model":"claude-sonnet-4-5","usage":{"input_tokens":25,"output_tokens":1}}}`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"你"}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"好"}}`,
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":12}}`,
		`data: {"type":"message_stop"}`,
	)
	resp := acc.BuildResponse("", events)
	if resp.Text() != "你好" {
		t.Fatalf("text = %q", resp.Text())
	}
	if resp.FinishReason() != "stop" { // end_turn 归一
		t.Fatalf("finishReason = %q", resp.FinishReason())
	}
	if resp.Model() != "claude-sonnet-4-5" {
		t.Fatalf("model = %q", resp.Model())
	}
	// usage 合并：message_start input 25 + message_delta output 12（覆盖不叠加）
	if resp.Usage() == nil || resp.Usage().PromptTokens != 25 || resp.Usage().CompletionTokens != 12 ||
		resp.Usage().TotalTokens != 37 {
		t.Fatalf("usage = %+v", resp.Usage())
	}
	matrixStrEq(t, matrixTexts(t, events, ai.EventTextDelta), "你", "好")
	if acc.IsFinished() != true {
		t.Fatalf("message_stop 后应完成")
	}
}

// TestAnthropicStreamThinkingToolCall thinking 签名 + input_json_delta 聚合 +
// 双 tool_use 块按 blockIdx 归位（Go Accumulator 以 Index 为首键：START/ARGS_DELTA
// 必须统一 "idx:<blockIdx>"，否则一次调用拆成两个槽）。
func TestAnthropicStreamThinkingToolCall(t *testing.T) {
	acc, events := matrixStream(&anthropic.Dialect{}, &ai.Config{Model: "claude-sonnet-4-5"},
		`data: {"type":"message_start","message":{"id":"msg_02","model":"claude-sonnet-4-5","usage":{"input_tokens":100,"output_tokens":2}}}`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"让我想想"}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"……细想"}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-abc"}}`,
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_01","name":"query-order","input":{}}}`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"orderId\":"}}`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"A1\"}"}}`,
		`data: {"type":"content_block_stop","index":1}`,
		`data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_02","name":"query-order","input":{}}}`,
		`data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"orderId\":\"A2\"}"}}`,
		`data: {"type":"content_block_stop","index":2}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":50,"output_tokens_details":{"thinking_tokens":30}}}`,
		`data: {"type":"message_stop"}`,
	)
	resp := acc.BuildResponse("", events)

	if resp.Thinking() != "让我想想……细想" {
		t.Fatalf("thinking = %q", resp.Thinking())
	}
	calls := resp.ToolCalls()
	if len(calls) != 2 {
		t.Fatalf("toolCalls = %+v，应为 2 个（idx 键统一后不得拆裂）", calls)
	}
	if calls[0].ID != "toolu_01" || calls[0].Name != "query-order" ||
		calls[0].Arguments["orderId"] != "A1" || calls[0].ArgumentsStr != `{"orderId":"A1"}` {
		t.Fatalf("calls[0] = %+v", calls[0])
	}
	if calls[1].ID != "toolu_02" || calls[1].Arguments["orderId"] != "A2" {
		t.Fatalf("calls[1] = %+v", calls[1])
	}
	if resp.FinishReason() != "tool" {
		t.Fatalf("finishReason = %q", resp.FinishReason())
	}
	if u := resp.Usage(); u == nil || u.ThinkTokens != 30 || u.CompletionTokens != 50 {
		t.Fatalf("usage = %+v", u)
	}
	if acc.ThinkingSignature() != "sig-abc" {
		t.Fatalf("thinkingSignature = %q", acc.ThinkingSignature())
	}
	matrixStrEq(t, matrixTexts(t, events, ai.EventThinkingSignature), "sig-abc")
	if starts := matrixOf(t, events, ai.EventToolCallStart); len(starts) != 2 {
		t.Fatalf("TOOL_CALL_START = %d 个，应为 2", len(starts))
	}
	if deltas := matrixOf(t, events, ai.EventToolCallArgsDelta); len(deltas) != 3 {
		t.Fatalf("TOOL_CALL_ARGS_DELTA = %d 个，应为 3", len(deltas))
	}

	// 终态协议状态（thinking 签名进 anthropic.messages 命名空间）
	state, ok := resp.Message().ProtocolStates["anthropic.messages"].(*ai.ProtocolState)
	if !ok {
		t.Fatalf("协议状态缺失：%v", resp.Message().ProtocolStates)
	}
	if state.Version != 1 || state.Data["thinkingSignature"] != "sig-abc" {
		t.Fatalf("协议状态 = %+v", state)
	}
}

// TestAnthropicToolCallRoundTrip 签名跨轮回放：流式聚合 → BuildAssistantToolCallMessage
// （携带协议状态）→ 下一轮 BuildRequestJSON 回传 thinking 块 + tool_use；连续 tool
// 消息合并为单条 user tool_result 节点；消息语义被改动后状态降级（不回传 thinking）。
func TestAnthropicToolCallRoundTrip(t *testing.T) {
	d := &anthropic.Dialect{}
	acc, _ := matrixStream(d, &ai.Config{Model: "claude-sonnet-4-5"},
		`data: {"type":"message_start","message":{"id":"msg_02","model":"claude-sonnet-4-5","usage":{"input_tokens":10,"output_tokens":1}}}`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"让我想想"}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-abc"}}`,
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_01","name":"query-order","input":{}}}`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"orderId\":\"A1\"}"}}`,
		`data: {"type":"content_block_stop","index":1}`,
		`data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_02","name":"query-order","input":{}}}`,
		`data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"orderId\":\"A2\"}"}}`,
		`data: {"type":"content_block_stop","index":2}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
		`data: {"type":"message_stop"}`,
	)
	asst := d.BuildAssistantToolCallMessage(acc, acc.ToolCallBuilders())
	if asst == nil {
		t.Fatalf("BuildAssistantToolCallMessage 返回 nil")
	}
	history := []*ai.Message{
		ai.OfSystem("你是助手"),
		ai.OfUser("查一下 A1"),
		asst,
		ai.OfTool(ai.ToolResultOf("订单 A1"), "query-order", "toolu_01", false),
		ai.OfTool(ai.ToolResultOf("库存 5 件"), "query-order", "toolu_02", false),
	}

	cfg := &ai.Config{Model: "claude-sonnet-4-5", APIURL: "https://api.anthropic.com"}
	body, url, err := d.BuildRequestJSON(cfg, &ai.ChatOptions{}, history, false)
	if err != nil {
		t.Fatalf("BuildRequestJSON: %v", err)
	}
	if url != "https://api.anthropic.com/v1/messages" {
		t.Fatalf("url = %q", url)
	}
	root := mustJSONMap(t, body)
	if root["system"] != "你是助手" {
		t.Fatalf("system = %v", root["system"])
	}
	messages := matrixArray(t, root["messages"])
	if len(messages) != 3 { // user / assistant / 合并后的 user tool_result
		t.Fatalf("messages = %v，应为 3 条（连续 tool 合并）", messages)
	}
	assistant := matrixNode(t, messages[1])
	if assistant["role"] != "assistant" {
		t.Fatalf("assistant role = %v", assistant["role"])
	}
	content := matrixArray(t, assistant["content"])
	if len(content) != 3 {
		t.Fatalf("assistant content = %v，应为 thinking + 2 个 tool_use", content)
	}
	thinking := matrixNode(t, content[0])
	if thinking["type"] != "thinking" || thinking["signature"] != "sig-abc" ||
		thinking["thinking"] != "让我想想" {
		t.Fatalf("thinking 块 = %v", thinking)
	}
	for i, wantID := range []string{"toolu_01", "toolu_02"} {
		call := matrixNode(t, content[i+1])
		if call["type"] != "tool_use" || call["id"] != wantID || call["name"] != "query-order" {
			t.Fatalf("tool_use 块 = %v", call)
		}
		args := matrixNode(t, call["input"])
		if wantID == "toolu_01" && args["orderId"] != "A1" {
			t.Fatalf("toolu_01 input = %v", args)
		}
	}
	toolResultNode := matrixNode(t, messages[2])
	if toolResultNode["role"] != "user" {
		t.Fatalf("tool_result 节点 role = %v", toolResultNode["role"])
	}
	results := matrixArray(t, toolResultNode["content"])
	if len(results) != 2 {
		t.Fatalf("tool_result 块 = %v，应为 2 块", results)
	}
	if r0 := matrixNode(t, results[0]); r0["tool_use_id"] != "toolu_01" || r0["content"] != "订单 A1" {
		t.Fatalf("tool_result[0] = %v", r0)
	}

	// 语义篡改后协议状态拒绝回放（签名不再回传）
	tampered, _ := history[2].Content.(*ai.AssistantMessage)
	tampered.Thinking = "被改动的思考"
	body2, _, err := d.BuildRequestJSON(cfg, &ai.ChatOptions{}, history, false)
	if err != nil {
		t.Fatalf("BuildRequestJSON: %v", err)
	}
	root2 := mustJSONMap(t, body2)
	content2 := matrixArray(t, matrixNode(t, matrixArray(t, root2["messages"])[1])["content"])
	if len(content2) != 2 {
		t.Fatalf("篡改后 assistant content = %v，thinking 应被降级剔除", content2)
	}
	if matrixNode(t, content2[0])["type"] != "tool_use" {
		t.Fatalf("篡改后首块 = %v", content2[0])
	}
}

// TestAnthropicStreamCacheUsage Prompt Caching 计量：input_tokens 不含缓存需并入、
// 5m/1h TTL 明细、message_delta 累计快照按字段存在性覆盖（显式值胜出，不相加）。
func TestAnthropicStreamCacheUsage(t *testing.T) {
	acc, events := matrixStream(&anthropic.Dialect{}, &ai.Config{Model: "claude-sonnet-4-5"},
		`data: {"type":"message_start","message":{"id":"msg_03","model":"claude-sonnet-4-5","usage":{"input_tokens":10,"cache_creation":{"ephemeral_5m_input_tokens":100,"ephemeral_1h_input_tokens":50},"cache_read_input_tokens":400,"output_tokens":1,"service_tier":"standard"}}}`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"好"}}`,
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":20,"input_tokens":5}}`,
		`data: {"type":"message_stop"}`,
	)
	u := acc.BuildResponse("", events).Usage()
	if u == nil {
		t.Fatalf("usage 缺失")
	}
	// prompt = input(修正 5) + cache_creation(150) + cache_read(400)
	if u.PromptTokens != 555 {
		t.Fatalf("promptTokens = %d，want 555", u.PromptTokens)
	}
	if u.CompletionTokens != 20 { // 1 → 20 覆盖而非 21 相加
		t.Fatalf("completionTokens = %d，want 20", u.CompletionTokens)
	}
	if u.TotalTokens != 575 {
		t.Fatalf("totalTokens = %d，want 575", u.TotalTokens)
	}
	if u.CacheCreationInputTokens != 150 || u.CacheCreation5mInputTokens != 100 || u.CacheCreation1hInputTokens != 50 {
		t.Fatalf("cacheCreation = %d/5m:%d/1h:%d", u.CacheCreationInputTokens, u.CacheCreation5mInputTokens, u.CacheCreation1hInputTokens)
	}
	if u.CacheReadInputTokens != 400 {
		t.Fatalf("cacheRead = %d", u.CacheReadInputTokens)
	}
	if u.ServiceTier != "standard" { // 缺失字段沿用 message_start
		t.Fatalf("serviceTier = %q", u.ServiceTier)
	}
}

func TestAnthropicNonStream(t *testing.T) {
	resp, events, acc := matrixCall(&anthropic.Dialect{}, &ai.Config{Model: "claude-sonnet-4-5"}, `{
		"id": "msg_04",
		"model": "claude-sonnet-4-5",
		"stop_reason": "tool_use",
		"content": [
			{"type": "thinking", "thinking": "想想", "signature": "sig-ns"},
			{"type": "text", "text": "查询中"},
			{"type": "tool_use", "id": "toolu_03", "name": "query-order", "input": {"orderId": "A9"}}
		],
		"usage": {"input_tokens": 12, "output_tokens": 9}
	}`)
	if resp.Text() != "查询中" || resp.Thinking() != "想想" {
		t.Fatalf("text/thinking = %q/%q", resp.Text(), resp.Thinking())
	}
	calls := resp.ToolCalls()
	if len(calls) != 1 || calls[0].ID != "toolu_03" || calls[0].Arguments["orderId"] != "A9" {
		t.Fatalf("toolCalls = %+v", calls)
	}
	if resp.FinishReason() != "tool" {
		t.Fatalf("finishReason = %q", resp.FinishReason())
	}
	if u := resp.Usage(); u == nil || u.PromptTokens != 12 || u.CompletionTokens != 9 || u.TotalTokens != 21 {
		t.Fatalf("usage = %+v", u)
	}
	// 非流式保留完整工具调用生命周期 + 签名事件
	if n := len(matrixOf(t, events, ai.EventToolCallStart)); n != 1 {
		t.Fatalf("TOOL_CALL_START = %d", n)
	}
	if n := len(matrixOf(t, events, ai.EventToolCallArgsDelta)); n != 1 {
		t.Fatalf("TOOL_CALL_ARGS_DELTA = %d", n)
	}
	if n := len(matrixOf(t, events, ai.EventToolCallEnd)); n != 1 {
		t.Fatalf("TOOL_CALL_END = %d", n)
	}
	matrixStrEq(t, matrixTexts(t, events, ai.EventThinkingSignature), "sig-ns")
	if state, ok := acc.Message().ProtocolStates["anthropic.messages"].(*ai.ProtocolState); !ok ||
		state.Data["thinkingSignature"] != "sig-ns" {
		t.Fatalf("协议状态缺失")
	}
}

func TestAnthropicBuildRequestJSON(t *testing.T) {
	d := &anthropic.Dialect{}
	cfg := &ai.Config{Model: "claude-sonnet-4-5", APIURL: "https://api.anthropic.com"}
	opts := &ai.ChatOptions{}
	opts.Temperature(0.7).
		MaxTokens(4096).
		Set("thinking", map[string]any{"type": "enabled", "budget_tokens": int64(2000)}).
		Set("frequency_penalty", 0.5). // Messages API 不接受：剔除
		Set("seed", 7).                // 同上
		Set("stop", "END").            // → stop_sequences
		Set("user", "u-1")             // → metadata.user_id
	opts.Tools = []ai.FunctionTool{matrixQueryOrderTool()}
	msgs := []*ai.Message{ai.OfSystem("你是助手"), ai.OfUser("查一下 A1")}

	body, url, err := d.BuildRequestJSON(cfg, opts, msgs, false)
	if err != nil {
		t.Fatalf("BuildRequestJSON: %v", err)
	}
	if url != "https://api.anthropic.com/v1/messages" {
		t.Fatalf("url = %q", url)
	}
	root := mustJSONMap(t, body)
	if root["model"] != "claude-sonnet-4-5" || root["max_tokens"] != float64(4096) {
		t.Fatalf("model/max_tokens = %v/%v", root["model"], root["max_tokens"])
	}
	if root["system"] != "你是助手" {
		t.Fatalf("system = %v", root["system"])
	}
	messages := matrixArray(t, root["messages"])
	if len(messages) != 1 {
		t.Fatalf("messages = %v", messages)
	}
	if m0 := matrixNode(t, messages[0]); m0["role"] != "user" || m0["content"] != "查一下 A1" {
		t.Fatalf("messages[0] = %v", m0)
	}
	if root["temperature"] != 0.7 {
		t.Fatalf("temperature = %v", root["temperature"])
	}
	if seqs := matrixArray(t, root["stop_sequences"]); len(seqs) != 1 || seqs[0] != "END" {
		t.Fatalf("stop_sequences = %v", root["stop_sequences"])
	}
	if meta := matrixNode(t, root["metadata"]); meta["user_id"] != "u-1" {
		t.Fatalf("metadata = %v", meta)
	}
	thinking := matrixNode(t, root["thinking"])
	if thinking["type"] != "enabled" || thinking["budget_tokens"] != float64(2000) {
		t.Fatalf("thinking = %v", thinking)
	}
	for _, banned := range []string{"frequency_penalty", "seed", "stream"} {
		if _, has := root[banned]; has {
			t.Fatalf("黑名单选项 %q 泄漏进请求体", banned)
		}
	}
	tools := matrixArray(t, root["tools"])
	if len(tools) != 1 {
		t.Fatalf("tools = %v", tools)
	}
	tool := matrixNode(t, tools[0])
	if tool["name"] != "query-order" || tool["description"] != "查询订单" {
		t.Fatalf("tool = %v", tool)
	}
	schema := matrixNode(t, tool["input_schema"])
	if schema["type"] != "object" {
		t.Fatalf("input_schema = %v", schema)
	}
	if _, has := matrixNode(t, schema["properties"])["orderId"]; !has {
		t.Fatalf("input_schema.properties = %v", schema["properties"])
	}

	// 字节稳定性（快照可比对的前提）
	body2, _, err := d.BuildRequestJSON(cfg, opts, msgs, false)
	if err != nil || !bytes.Equal(body, body2) {
		t.Fatalf("重复构建请求体不稳定：%v %s vs %s", err, body, body2)
	}

	// max_completion_tokens 别名并入 max_tokens；URL 版本段补全
	aliasOpts := &ai.ChatOptions{}
	aliasOpts.Set("max_completion_tokens", 77)
	aliasBody, aliasURL, err := d.BuildRequestJSON(
		&ai.Config{Model: "claude-sonnet-4-5", APIURL: "https://gw.example/v1"}, aliasOpts,
		[]*ai.Message{ai.OfUser("hi")}, true)
	if err != nil {
		t.Fatalf("BuildRequestJSON: %v", err)
	}
	aliasRoot := mustJSONMap(t, aliasBody)
	if aliasRoot["max_tokens"] != float64(77) {
		t.Fatalf("max_completion_tokens 别名 = %v", aliasRoot["max_tokens"])
	}
	if aliasRoot["stream"] != true {
		t.Fatalf("stream = %v", aliasRoot["stream"])
	}
	if aliasURL != "https://gw.example/v1/messages" {
		t.Fatalf("url = %q", aliasURL)
	}
}

// ---- gemini ----

func TestGeminiStreamText(t *testing.T) {
	acc, events := matrixStream(&gemini.Dialect{}, &ai.Config{Model: "gemini-2.5-pro"},
		`data: {"candidates":[{"content":{"parts":[{"text":"你"}],"role":"model"},"index":0}]}`,
		`data: {"candidates":[{"content":{"parts":[{"text":"好"}],"role":"model"},"index":0}]}`,
		`data: {"candidates":[{"content":{"parts":[{"text":"！"}],"role":"model"},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3,"totalTokenCount":13},"modelVersion":"gemini-2.5-pro"}`,
		`data: [DONE]`,
	)
	resp := acc.BuildResponse("", events)
	if resp.Text() != "你好！" {
		t.Fatalf("text = %q", resp.Text())
	}
	if resp.FinishReason() != "stop" { // STOP 归一
		t.Fatalf("finishReason = %q", resp.FinishReason())
	}
	if resp.Model() != "gemini-2.5-pro" { // modelVersion（官方原生字段）
		t.Fatalf("model = %q", resp.Model())
	}
	if u := resp.Usage(); u == nil || u.PromptTokens != 10 || u.CompletionTokens != 3 || u.TotalTokens != 13 {
		t.Fatalf("usage = %+v", u)
	}
	matrixStrEq(t, matrixTexts(t, events, ai.EventTextDelta), "你", "好", "！")
}

// TestGeminiStreamThoughtToolCall thought 标记边界 + functionCall 累计快照归并：
// 唯一 START + 单个 ARGS_DELTA + thoughtSignature 协议状态。
func TestGeminiStreamThoughtToolCall(t *testing.T) {
	acc, events := matrixStream(&gemini.Dialect{}, &ai.Config{Model: "gemini-2.5-pro"},
		`data: {"candidates":[{"content":{"parts":[{"thought":true,"text":"思考中"}],"role":"model"},"index":0}]}`,
		`data: {"candidates":[{"content":{"parts":[{"thought":true,"text":"……"},{"functionCall":{"name":"query-order","args":{"orderId":"A1"}},"thoughtSignature":"sig-g1"}],"role":"model"},"finishReason":"STOP","index":0}]}`,
		`data: [DONE]`,
	)
	resp := acc.BuildResponse("", events)
	if resp.Thinking() != "思考中……" {
		t.Fatalf("thinking = %q", resp.Thinking())
	}
	calls := resp.ToolCalls()
	if len(calls) != 1 || calls[0].Name != "query-order" || calls[0].Arguments["orderId"] != "A1" {
		t.Fatalf("toolCalls = %+v", calls)
	}
	// Gemini 线上协议对 functionCall 响应同样报 STOP（Java 侧不做 tool 抬升），
	// 自动工具循环按调用存在性分流，不依赖 finishReason
	if resp.FinishReason() != "stop" {
		t.Fatalf("finishReason = %q", resp.FinishReason())
	}
	if n := len(matrixOf(t, events, ai.EventToolCallStart)); n != 1 {
		t.Fatalf("TOOL_CALL_START = %d 个，应为 1（快照归并）", n)
	}
	matrixStrEq(t, matrixTexts(t, events, ai.EventToolCallArgsDelta), `{"orderId":"A1"}`)
	matrixStrEq(t, matrixTexts(t, events, ai.EventThinkingSignature), "sig-g1")

	// 签名进 google.generate-content 命名空间终态协议袋
	state, ok := resp.Message().ProtocolStates["google.generate-content"].(*ai.ProtocolState)
	if !ok {
		t.Fatalf("协议状态缺失：%v", resp.Message().ProtocolStates)
	}
	signatures, _ := state.Data["thoughtSignatures"].(map[string]any)
	if len(signatures) != 1 {
		t.Fatalf("thoughtSignatures = %v", signatures)
	}
	for _, v := range signatures {
		if v != "sig-g1" {
			t.Fatalf("thoughtSignatures = %v", signatures)
		}
	}
}

// TestGeminiParallelSameNameFunctionCalls 同 chunk 并行同名调用 name#n 去冲突：
// 两个 getWeather/query-order 同时调用不得合并成一个。
func TestGeminiParallelSameNameFunctionCalls(t *testing.T) {
	acc, events := matrixStream(&gemini.Dialect{}, &ai.Config{Model: "gemini-2.5-pro"},
		`data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"query-order","args":{"orderId":"A1"}}},{"functionCall":{"name":"query-order","args":{"orderId":"A2"}}}],"role":"model"},"finishReason":"STOP","index":0}]}`,
		`data: [DONE]`,
	)
	calls := acc.BuildResponse("", events).ToolCalls()
	if len(calls) != 2 {
		t.Fatalf("toolCalls = %+v，应为 2 个（同名并行不合并）", calls)
	}
	if calls[0].Name != "query-order" || calls[1].Name != "query-order" {
		t.Fatalf("names = %q/%q", calls[0].Name, calls[1].Name)
	}
	if calls[0].Arguments["orderId"] != "A1" || calls[1].Arguments["orderId"] != "A2" {
		t.Fatalf("args = %v/%v", calls[0].Arguments, calls[1].Arguments)
	}
	if n := len(matrixOf(t, events, ai.EventToolCallStart)); n != 2 {
		t.Fatalf("TOOL_CALL_START = %d 个，应为 2", n)
	}
}

// TestGeminiSignatureRoundTrip thoughtSignature 跨轮回放：流式聚合 →
// BuildAssistantToolCallMessage（摘要须在物化边界绑定，否则回放校验必然拒绝）→
// 下一轮 functionCall part 携带 thoughtSignature；消息语义被改动后签名降级。
func TestGeminiSignatureRoundTrip(t *testing.T) {
	d := &gemini.Dialect{}
	acc, _ := matrixStream(d, &ai.Config{Model: "gemini-2.5-pro"},
		`data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"query-order","args":{"orderId":"A1"}},"thoughtSignature":"sig-g9"}],"role":"model"},"finishReason":"STOP","index":0}]}`,
		`data: [DONE]`,
	)
	asst := d.BuildAssistantToolCallMessage(acc, acc.ToolCallBuilders())
	if asst == nil {
		t.Fatalf("BuildAssistantToolCallMessage 返回 nil")
	}
	history := []*ai.Message{
		ai.OfSystem("你是助手"),
		ai.OfUser("查一下 A1"),
		asst,
		ai.OfTool(ai.ToolResultOf("订单 A1"), "query-order", "", false),
	}
	cfg := &ai.Config{Model: "gemini-2.5-pro", APIURL: "https://generativelanguage.googleapis.com/v1beta"}
	body, _, err := d.BuildRequestJSON(cfg, &ai.ChatOptions{}, history, false)
	if err != nil {
		t.Fatalf("BuildRequestJSON: %v", err)
	}
	contents := matrixArray(t, mustJSONMap(t, body)["contents"])
	if len(contents) != 3 { // user / model(functionCall) / user(functionResponse)
		t.Fatalf("contents = %v，应为 3 条", contents)
	}
	modelNode := matrixNode(t, contents[1])
	if modelNode["role"] != "model" {
		t.Fatalf("model 节点 role = %v", modelNode["role"])
	}
	modelParts := matrixArray(t, modelNode["parts"])
	if len(modelParts) != 1 {
		t.Fatalf("model parts = %v", modelParts)
	}
	part := matrixNode(t, modelParts[0])
	if part["thoughtSignature"] != "sig-g9" {
		t.Fatalf("functionCall part = %v，thoughtSignature 应回传", part)
	}
	fn := matrixNode(t, part["functionCall"])
	if fn["name"] != "query-order" || matrixNode(t, fn["args"])["orderId"] != "A1" {
		t.Fatalf("functionCall = %v", fn)
	}

	// 语义篡改后签名不再回传（摘要校验拒绝陈旧快照）
	tampered, _ := history[2].Content.(*ai.AssistantMessage)
	tampered.Thinking = "被改动的思考"
	body2, _, err := d.BuildRequestJSON(cfg, &ai.ChatOptions{}, history, false)
	if err != nil {
		t.Fatalf("BuildRequestJSON: %v", err)
	}
	contents2 := matrixArray(t, mustJSONMap(t, body2)["contents"])
	part2 := matrixNode(t, matrixArray(t, matrixNode(t, contents2[1])["parts"])[0])
	if _, has := part2["thoughtSignature"]; has {
		t.Fatalf("篡改后 thoughtSignature = %v，应被降级剔除", part2["thoughtSignature"])
	}
}

func TestGeminiNonStream(t *testing.T) {
	resp, events, _ := matrixCall(&gemini.Dialect{}, &ai.Config{Model: "gemini-2.5-pro"}, `{
		"candidates": [{
			"content": {"parts": [
				{"thought": true, "text": "**计划**\n先查库"},
				{"text": "正文回答"}
			], "role": "model"},
			"finishReason": "STOP",
			"index": 0,
			"groundingMetadata": {"groundingChunks": [{"web": {"uri": "https://example.com/doc", "title": "文档"}}]}
		}],
		"usageMetadata": {"promptTokenCount": 8, "candidatesTokenCount": 2, "totalTokenCount": 10},
		"modelVersion": "gemini-2.5-pro"
	}`)
	if resp.Text() != "正文回答" {
		t.Fatalf("text = %q", resp.Text())
	}
	if resp.Thinking() != "先查库" { // 标题型加粗包裹被清理
		t.Fatalf("thinking = %q", resp.Thinking())
	}
	if u := resp.Usage(); u == nil || u.PromptTokens != 8 || u.CompletionTokens != 2 || u.TotalTokens != 10 {
		t.Fatalf("usage = %+v", u)
	}
	citations := matrixOf(t, events, ai.EventCitation)
	if len(citations) != 1 || citations[0].Citation == nil ||
		citations[0].Citation.URL != "https://example.com/doc" || citations[0].SubType != "google_search" {
		t.Fatalf("citations = %+v", citations)
	}
}

func TestGeminiBuildRequestJSON(t *testing.T) {
	d := &gemini.Dialect{}
	cfg := &ai.Config{Model: "gemini-2.5-pro", APIURL: "https://generativelanguage.googleapis.com/v1beta"}
	opts := &ai.ChatOptions{}
	opts.Temperature(0.7).MaxTokens(512)
	msgs := []*ai.Message{ai.OfSystem("你是助手"), ai.OfUser("查一下 A1")}

	body, url, err := d.BuildRequestJSON(cfg, opts, msgs, false)
	if err != nil {
		t.Fatalf("BuildRequestJSON: %v", err)
	}
	if url != "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-pro:generateContent" {
		t.Fatalf("url = %q", url)
	}
	root := mustJSONMap(t, body)
	sysInst := matrixNode(t, root["systemInstruction"])
	sysParts := matrixArray(t, sysInst["parts"])
	if len(sysParts) != 1 || matrixNode(t, sysParts[0])["text"] != "你是助手" {
		t.Fatalf("systemInstruction = %v", root["systemInstruction"])
	}
	contents := matrixArray(t, root["contents"])
	if len(contents) != 1 {
		t.Fatalf("contents = %v", contents)
	}
	c0 := matrixNode(t, contents[0])
	if c0["role"] != "user" {
		t.Fatalf("contents[0].role = %v", c0["role"])
	}
	c0Parts := matrixArray(t, c0["parts"])
	if len(c0Parts) != 1 || matrixNode(t, c0Parts[0])["text"] != "查一下 A1" {
		t.Fatalf("contents[0].parts = %v", c0["parts"])
	}
	gc := matrixNode(t, root["generationConfig"])
	if gc["temperature"] != 0.7 || gc["maxOutputTokens"] != float64(512) {
		t.Fatalf("generationConfig = %v", gc)
	}

	body2, _, err := d.BuildRequestJSON(cfg, opts, msgs, false)
	if err != nil || !bytes.Equal(body, body2) {
		t.Fatalf("重复构建请求体不稳定：%v", err)
	}

	// 流式端点动词 + alt=sse
	_, streamURL, err := d.BuildRequestJSON(cfg, opts, msgs, true)
	if err != nil {
		t.Fatalf("BuildRequestJSON: %v", err)
	}
	if streamURL != "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-pro:streamGenerateContent?alt=sse" {
		t.Fatalf("streamURL = %q", streamURL)
	}
}

// ---- ollama ----

func TestOllamaStreamTextUsage(t *testing.T) {
	acc, events := matrixStream(&ollama.Dialect{}, &ai.Config{Model: "llama3.1"},
		`{"model":"llama3.1","message":{"role":"assistant","content":"你"},"done":false}`,
		`{"model":"llama3.1","message":{"role":"assistant","content":"好"},"done":false}`,
		`{"model":"llama3.1","message":{"role":"assistant","content":""},"done_reason":"stop","done":true,"prompt_eval_count":26,"eval_count":42}`,
	)
	resp := acc.BuildResponse("", events)
	if resp.Text() != "你好" {
		t.Fatalf("text = %q", resp.Text())
	}
	if resp.Model() != "llama3.1" {
		t.Fatalf("model = %q", resp.Model())
	}
	if resp.FinishReason() != "stop" {
		t.Fatalf("finishReason = %q", resp.FinishReason())
	}
	// done 流尾帧才计 usage
	if u := resp.Usage(); u == nil || u.PromptTokens != 26 || u.CompletionTokens != 42 || u.TotalTokens != 68 {
		t.Fatalf("usage = %+v", u)
	}
	matrixStrEq(t, matrixTexts(t, events, ai.EventTextDelta), "你", "好")
}

// TestOllamaStreamToolCallSnapshot Ollama 部分实现按累计快照重发完整 tool_calls
// （arguments 为 object 形态）：START 去重 + 参数只交付一次。
func TestOllamaStreamToolCallSnapshot(t *testing.T) {
	acc, events := matrixStream(&ollama.Dialect{}, &ai.Config{Model: "llama3.1"},
		`{"model":"llama3.1","message":{"role":"assistant","content":""},"done":false}`,
		`{"model":"llama3.1","message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"query-order","arguments":{"orderId":"A1"}}}]},"done":false}`,
		`{"model":"llama3.1","message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"query-order","arguments":{"orderId":"A1"}}}]},"done_reason":"stop","done":true,"prompt_eval_count":10,"eval_count":5}`,
	)
	resp := acc.BuildResponse("", events)
	calls := resp.ToolCalls()
	if len(calls) != 1 || calls[0].Name != "query-order" || calls[0].Arguments["orderId"] != "A1" {
		t.Fatalf("toolCalls = %+v", calls)
	}
	if n := len(matrixOf(t, events, ai.EventToolCallStart)); n != 1 {
		t.Fatalf("TOOL_CALL_START = %d 个，应为 1", n)
	}
	if n := len(matrixOf(t, events, ai.EventToolCallArgsDelta)); n != 1 {
		t.Fatalf("TOOL_CALL_ARGS_DELTA = %d 个，快照前缀只应交付一次", n)
	}
	if u := resp.Usage(); u == nil || u.PromptTokens != 10 || u.CompletionTokens != 5 {
		t.Fatalf("usage = %+v", u)
	}
}

func TestOllamaCallText(t *testing.T) {
	resp, events, _ := matrixCall(&ollama.Dialect{}, &ai.Config{Model: "llama3.1"},
		`{"model":"llama3.1","message":{"role":"assistant","content":"答案"},"done_reason":"stop","done":true,"prompt_eval_count":8,"eval_count":3}`)
	if resp.Text() != "答案" {
		t.Fatalf("text = %q", resp.Text())
	}
	if resp.FinishReason() != "stop" {
		t.Fatalf("finishReason = %q", resp.FinishReason())
	}
	if u := resp.Usage(); u == nil || u.TotalTokens != 11 {
		t.Fatalf("usage = %+v", u)
	}
	if n := len(matrixOf(t, events, ai.EventTextDelta)); n != 0 { // 非流式走终态载体
		t.Fatalf("非流式不应发 TEXT_DELTA，got %d", n)
	}
}

func TestOllamaBuildRequestJSON(t *testing.T) {
	d := &ollama.Dialect{}
	cfg := &ai.Config{Model: "llama3.1", APIURL: "http://localhost:11434"}
	opts := &ai.ChatOptions{}
	opts.Set("thinking", true) // 统一开关 → Ollama 原生 think
	opts.SetCacheControl(ai.CacheControl{Ttl: "10m", PromptCacheKey: "u-1"})
	opts.Tools = []ai.FunctionTool{matrixQueryOrderTool()}
	msgs := []*ai.Message{ai.OfUser("你好")}

	body, url, err := d.BuildRequestJSON(cfg, opts, msgs, false)
	if err != nil {
		t.Fatalf("BuildRequestJSON: %v", err)
	}
	if url != "http://localhost:11434/api/chat" {
		t.Fatalf("url = %q", url)
	}
	root := mustJSONMap(t, body)
	if root["model"] != "llama3.1" {
		t.Fatalf("model = %v", root["model"])
	}
	messages := matrixArray(t, root["messages"])
	if len(messages) != 1 {
		t.Fatalf("messages = %v", messages)
	}
	if m0 := matrixNode(t, messages[0]); m0["role"] != "user" || m0["content"] != "你好" {
		t.Fatalf("messages[0] = %v", m0)
	}
	if root["stream"] != false {
		t.Fatalf("stream = %v", root["stream"])
	}
	if root["think"] != true {
		t.Fatalf("think = %v（thinking 应映射为 think）", root["think"])
	}
	if root["keep_alive"] != "10m" || root["prompt_cache_key"] != "u-1" {
		t.Fatalf("keep_alive/prompt_cache_key = %v/%v", root["keep_alive"], root["prompt_cache_key"])
	}
	tools := matrixArray(t, root["tools"])
	if len(tools) != 1 {
		t.Fatalf("tools = %v", tools)
	}
	fn := matrixNode(t, matrixNode(t, tools[0])["function"])
	if fn["name"] != "query-order" {
		t.Fatalf("tool function = %v", fn)
	}

	body2, _, err := d.BuildRequestJSON(cfg, opts, msgs, false)
	if err != nil || !bytes.Equal(body, body2) {
		t.Fatalf("重复构建请求体不稳定：%v", err)
	}
}

// ---- dashscope ----

// TestDashscopeStreamSnapshotFallback incremental_output 未生效（中转端点忽略参数）
// 时每帧携带累计快照：兜底归一裁掉已交付前缀，正文仍完整聚合。
func TestDashscopeStreamSnapshotFallback(t *testing.T) {
	acc, events := matrixStream(&dashscope.Dialect{}, &ai.Config{Model: "qwen-plus"},
		`{"output":{"choices":[{"message":{"content":"长江是中","role":"assistant"},"finish_reason":null}]},"request_id":"req-1"}`,
		`{"output":{"choices":[{"message":{"content":"长江是中国最","role":"assistant"},"finish_reason":null}]},"request_id":"req-1"}`,
		`{"output":{"choices":[{"message":{"content":"长江是中国最长的河流","role":"assistant"},"finish_reason":"stop"}]},"request_id":"req-1","usage":{"input_tokens":10,"output_tokens":8,"total_tokens":18}}`,
	)
	resp := acc.BuildResponse("", events)
	if resp.Text() != "长江是中国最长的河流" {
		t.Fatalf("text = %q", resp.Text())
	}
	// 首帧 12 字节 ≥ 阈值后进入快照模式：后续帧只透出新增后缀
	matrixStrEq(t, matrixTexts(t, events, ai.EventTextDelta), "长江是中", "国最", "长的河流")
	if resp.FinishReason() != "stop" {
		t.Fatalf("finishReason = %q", resp.FinishReason())
	}
	if u := resp.Usage(); u == nil || u.PromptTokens != 10 || u.CompletionTokens != 8 || u.TotalTokens != 18 {
		t.Fatalf("usage = %+v", u)
	}
	if resp.Model() != "qwen-plus" { // 百炼帧不带 model，回退配置值
		t.Fatalf("model = %q", resp.Model())
	}
}

// TestDashscopeStreamIncrementalPassthrough 服务端已给增量时不得误裁（非前缀关系
// 原样通过，避免双重处理）。
func TestDashscopeStreamIncrementalPassthrough(t *testing.T) {
	acc, events := matrixStream(&dashscope.Dialect{}, &ai.Config{Model: "qwen-plus"},
		`{"output":{"choices":[{"message":{"content":"长江是中","role":"assistant"}}]},"request_id":"req-2"}`,
		`{"output":{"choices":[{"message":{"content":"国最长的河流","role":"assistant"},"finish_reason":"stop"}]},"request_id":"req-2"}`,
	)
	resp := acc.BuildResponse("", events)
	if resp.Text() != "长江是中国最长的河流" {
		t.Fatalf("text = %q", resp.Text())
	}
	matrixStrEq(t, matrixTexts(t, events, ai.EventTextDelta), "长江是中", "国最长的河流")
}

func TestDashscopeStreamToolCall(t *testing.T) {
	acc, events := matrixStream(&dashscope.Dialect{}, &ai.Config{Model: "qwen-plus"},
		`{"output":{"choices":[{"message":{"content":"","role":"assistant","tool_calls":[{"type":"function","id":"call_1","function":{"name":"query-order","arguments":{"orderId":"A1"}}}]}}]},"request_id":"req-3"}`,
		`{"output":{"choices":[{"message":{"content":"","role":"assistant"},"finish_reason":"tool_calls"}]},"request_id":"req-3"}`,
	)
	resp := acc.BuildResponse("", events)
	calls := resp.ToolCalls()
	if len(calls) != 1 || calls[0].ID != "call_1" || calls[0].Name != "query-order" ||
		calls[0].Arguments["orderId"] != "A1" {
		t.Fatalf("toolCalls = %+v", calls)
	}
	if resp.FinishReason() != "tool" {
		t.Fatalf("finishReason = %q", resp.FinishReason())
	}
	if n := len(matrixOf(t, events, ai.EventToolCallStart)); n != 1 {
		t.Fatalf("TOOL_CALL_START = %d", n)
	}
}

func TestDashscopeCallText(t *testing.T) {
	resp, _, _ := matrixCall(&dashscope.Dialect{}, &ai.Config{Model: "qwen-plus"},
		`{"output":{"choices":[{"message":{"content":"你好，我是通义","role":"assistant"},"finish_reason":"stop"}]},"request_id":"req-4","usage":{"input_tokens":6,"output_tokens":5,"total_tokens":11,"think_tokens":2}}`)
	if resp.Text() != "你好，我是通义" {
		t.Fatalf("text = %q", resp.Text())
	}
	if u := resp.Usage(); u == nil || u.PromptTokens != 6 || u.CompletionTokens != 5 ||
		u.TotalTokens != 11 || u.ThinkTokens != 2 {
		t.Fatalf("usage = %+v", u)
	}
}

func TestDashscopeBuildRequestJSON(t *testing.T) {
	d := &dashscope.Dialect{}
	endpoint := dashscope.URL_PREFIX + "aigc/text-generation/generation"
	cfg := &ai.Config{Model: "qwen-plus", APIURL: endpoint}
	opts := &ai.ChatOptions{}
	opts.Temperature(0.7)
	opts.Set("thinking", true)
	msgs := []*ai.Message{ai.OfSystem("你是助手"), ai.OfUser("查一下 A1")}

	body, url, err := d.BuildRequestJSON(cfg, opts, msgs, false)
	if err != nil {
		t.Fatalf("BuildRequestJSON: %v", err)
	}
	if url != endpoint { // 百炼要求完整端点，原样透传
		t.Fatalf("url = %q", url)
	}
	root := mustJSONMap(t, body)
	if root["model"] != "qwen-plus" || root["stream"] != false {
		t.Fatalf("model/stream = %v/%v", root["model"], root["stream"])
	}
	input := matrixNode(t, root["input"])
	messages := matrixArray(t, input["messages"])
	if len(messages) != 2 {
		t.Fatalf("input.messages = %v", messages)
	}
	if m0 := matrixNode(t, messages[0]); m0["role"] != "system" || m0["content"] != "你是助手" {
		t.Fatalf("input.messages[0] = %v", m0)
	}
	if m1 := matrixNode(t, messages[1]); m1["role"] != "user" || m1["content"] != "查一下 A1" {
		t.Fatalf("input.messages[1] = %v", m1)
	}
	parameters := matrixNode(t, root["parameters"])
	if parameters["enable_thinking"] != true || parameters["temperature"] != 0.7 ||
		parameters["result_format"] != "message" {
		t.Fatalf("parameters = %v", parameters)
	}
	if _, has := parameters["incremental_output"]; has { // 非流式不注入
		t.Fatalf("非流式不应有 incremental_output：%v", parameters)
	}

	// 流式：主修在请求侧显式开启增量
	streamBody, _, err := d.BuildRequestJSON(cfg, opts, msgs, true)
	if err != nil {
		t.Fatalf("BuildRequestJSON: %v", err)
	}
	streamRoot := mustJSONMap(t, streamBody)
	if p := matrixNode(t, streamRoot["parameters"]); p["incremental_output"] != true {
		t.Fatalf("流式 parameters = %v", p)
	}

	body2, _, err := d.BuildRequestJSON(cfg, opts, msgs, false)
	if err != nil || !bytes.Equal(body, body2) {
		t.Fatalf("重复构建请求体不稳定：%v", err)
	}
}
