package ai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/crazy-airhead/aifei-go/ai"
	openai "github.com/crazy-airhead/aifei-go/ai/dialect/openai"
	"github.com/crazy-airhead/aifei-go/ai/httpx"
)

// import 即注册（数据库驱动惯例）：openai 方言经本文件命名导入进入注册表。

// —— 方言注册与选择 ——

// fakeP1Dialect 高优先级假方言：只匹配 provider=fake-p1，验证优先级选择序。
type fakeP1Dialect struct{ ai.Dialect }

func (f *fakeP1Dialect) Name() string    { return "fake-p1" }
func (f *fakeP1Dialect) IsDefault() bool { return false }
func (f *fakeP1Dialect) Matched(cfg *ai.Config) bool {
	return cfg != nil && cfg.Provider == "fake-p1"
}

func init() { ai.RegisterDialect(&fakeP1Dialect{}, 1) }

func TestSelectDialect(t *testing.T) {
	if d := ai.SelectDialect(&ai.Config{}); d == nil || d.Name() != "openai-completions" {
		t.Fatalf("空配置应补位默认方言 openai，got %v", d)
	}
	if d := ai.SelectDialect(&ai.Config{Standard: "openai-completions"}); d == nil || d.Name() != "openai-completions" {
		t.Fatalf("Standard 精确匹配失败，got %v", d)
	}
	if d := ai.SelectDialect(&ai.Config{Standard: "not-registered"}); d == nil || d.Name() != "openai-completions" {
		t.Fatalf("未知 Standard 应回落补位方言，got %v", d)
	}
	// 优先级序：fake-p1(1) 先于 openai(100)，Matched 命中即选中
	if d := ai.SelectDialect(&ai.Config{Provider: "fake-p1"}); d == nil || d.Name() != "fake-p1" {
		t.Fatalf("provider 匹配应选中 fake-p1，got %v", d)
	}
	names := ai.Dialects()
	found := map[string]bool{}
	for _, n := range names {
		found[n] = true
	}
	if !found["openai-completions"] || !found["fake-p1"] {
		t.Fatalf("Dialects() = %v，应含两个已注册方言", names)
	}
}

// —— 接口地址补全 ——

func TestBuildAPIURL(t *testing.T) {
	cases := []struct{ apiURL, want string }{
		{"", ""},
		{"https://api.openai.com", "https://api.openai.com/v1/chat/completions"},
		{"https://api.openai.com/", "https://api.openai.com/v1/chat/completions"},
		{"https://api.openai.com/v1", "https://api.openai.com/v1/chat/completions"},
		{"https://api.deepseek.com/v4", "https://api.deepseek.com/v4/chat/completions"},
		{"https://res.example/openai/deployments/gpt-4o", "https://res.example/openai/deployments/gpt-4o/chat/completions"},
		{"https://api.openai.com/v1/chat/completions", "https://api.openai.com/v1/chat/completions"},
		{"https://res.example/v1?api-version=2024-02-01", "https://res.example/v1/chat/completions?api-version=2024-02-01"},
		{"https://x.example/base#fragment", "https://x.example/base/v1/chat/completions"},
	}
	for _, c := range cases {
		if got := openai.BuildAPIURL(c.apiURL, "chat/completions"); got != c.want {
			t.Fatalf("BuildAPIURL(%q) = %q, want %q", c.apiURL, got, c.want)
		}
	}
}

// —— 请求侧：BuildRequestJSON ——

func mustJSONMap(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("请求体非法 JSON: %v\n%s", err, body)
	}
	return m
}

func TestOpenAIBuildRequestJSON(t *testing.T) {
	d := &openai.Dialect{}
	cfg := &ai.Config{Model: "gpt-4o", APIURL: "https://api.openai.com", APIKey: "sk-test"}
	opts := &ai.ChatOptions{}
	opts.Temperature(0.7).
		MaxTokens(100).
		Set("openai_instruction_role", "system").
		SetCacheControl(ai.CacheControl{PromptCacheKey: "user-42"})
	opts.Tools = []ai.FunctionTool{ai.NewTool("query-order").
		Desc("查询订单").
		StringParam("orderId", "订单号", true).
		Build()}
	msgs := []*ai.Message{ai.OfSystem("你是助手"), ai.OfUser("查一下 A1")}

	body, url, err := d.BuildRequestJSON(cfg, opts, msgs, false)
	if err != nil {
		t.Fatalf("BuildRequestJSON: %v", err)
	}
	if url != "https://api.openai.com/v1/chat/completions" {
		t.Fatalf("url = %q", url)
	}
	root := mustJSONMap(t, body)

	if root["model"] != "gpt-4o" {
		t.Fatalf("model = %v", root["model"])
	}
	if root["stream"] != false {
		t.Fatalf("stream = %v", root["stream"])
	}
	if _, has := root["stream_options"]; has {
		t.Fatal("非流式不应带 stream_options")
	}
	if root["temperature"] != 0.7 {
		t.Fatalf("temperature = %v", root["temperature"])
	}
	if root["max_tokens"] != float64(100) {
		t.Fatalf("max_tokens = %v", root["max_tokens"])
	}
	if root["prompt_cache_key"] != "user-42" {
		t.Fatalf("prompt_cache_key = %v", root["prompt_cache_key"])
	}
	if _, has := root["openai_instruction_role"]; has {
		t.Fatal("openai_instruction_role 是方言本地选项，不应出站")
	}

	msgsArr, _ := root["messages"].([]any)
	if len(msgsArr) != 2 {
		t.Fatalf("messages = %v", root["messages"])
	}
	first, _ := msgsArr[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "你是助手" {
		t.Fatalf("messages[0] = %v", first)
	}
	second, _ := msgsArr[1].(map[string]any)
	if second["role"] != "user" || second["content"] != "查一下 A1" {
		t.Fatalf("messages[1] = %v", second)
	}

	tools, _ := root["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %v", root["tools"])
	}
	tool, _ := tools[0].(map[string]any)
	if tool["type"] != "function" {
		t.Fatalf("tool.type = %v", tool["type"])
	}
	fn, _ := tool["function"].(map[string]any)
	if fn["name"] != "query-order" || fn["description"] != "查询订单" {
		t.Fatalf("function = %v", fn)
	}
	params, _ := fn["parameters"].(map[string]any)
	if req, _ := params["required"].([]any); len(req) != 1 || req[0] != "orderId" {
		t.Fatalf("parameters.required = %v", params["required"])
	}

	// 字节稳定（map 序列化键排序）：两次构建一致
	body2, _, err := d.BuildRequestJSON(cfg, opts, msgs, false)
	if err != nil || string(body) != string(body2) {
		t.Fatalf("重复构建请求体不稳定:\n%s\n%s", body, body2)
	}
}

func TestOpenAIBuildRequestJSONStream(t *testing.T) {
	d := &openai.Dialect{}
	cfg := &ai.Config{Model: "gpt-4o", APIURL: "https://api.openai.com"}
	opts := &ai.ChatOptions{}
	body, _, err := d.BuildRequestJSON(cfg, opts, nil, true)
	if err != nil {
		t.Fatalf("BuildRequestJSON: %v", err)
	}
	root := mustJSONMap(t, body)
	if root["stream"] != true {
		t.Fatalf("stream = %v", root["stream"])
	}
	if arr, _ := root["messages"].([]any); arr == nil || len(arr) != 0 {
		t.Fatalf("空会话 messages 应序列化为 []，got %v", root["messages"])
	}
	so, _ := root["stream_options"].(map[string]any)
	if so["include_usage"] != true {
		t.Fatalf("stream_options = %v，应自动补 include_usage", root["stream_options"])
	}

	// Raw 已带 stream_options：仅补缺省键，已有键保留
	opts2 := &ai.ChatOptions{}
	opts2.OptionSet("stream_options", map[string]any{"foo": 1})
	body2, _, _ := d.BuildRequestJSON(cfg, opts2, nil, true)
	root2 := mustJSONMap(t, body2)
	so2, _ := root2["stream_options"].(map[string]any)
	if so2["include_usage"] != true || so2["foo"] != float64(1) {
		t.Fatalf("stream_options 合并 = %v", root2["stream_options"])
	}
}

func TestOpenAIBuildRequestJSONToolMeta(t *testing.T) {
	d := &openai.Dialect{}
	cfg := &ai.Config{Model: "gpt-4o"}
	opts := &ai.ChatOptions{}
	opts.Tools = []ai.FunctionTool{ai.NewTool("check").
		Desc("检查").
		MetaSet("vip", true).
		MetaSet("tier", "gold").
		Strict(true).
		Build()}
	body, _, err := d.BuildRequestJSON(cfg, opts, nil, false)
	if err != nil {
		t.Fatalf("BuildRequestJSON: %v", err)
	}
	root := mustJSONMap(t, body)
	tools, _ := root["tools"].([]any)
	tool, _ := tools[0].(map[string]any)
	fn, _ := tool["function"].(map[string]any)
	if fn["description"] != "[tier:gold] [vip] 检查" {
		t.Fatalf("description = %q（meta 前缀按键排序）", fn["description"])
	}
	if fn["strict"] != true {
		t.Fatalf("strict = %v（应在 function 内层）", fn["strict"])
	}
}

// —— 请求侧：消息节点形态 ——

func buildMessages(t *testing.T, cfg *ai.Config, opts *ai.ChatOptions, msgs []*ai.Message) []map[string]any {
	t.Helper()
	d := &openai.Dialect{}
	body, _, err := d.BuildRequestJSON(cfg, opts, msgs, false)
	if err != nil {
		t.Fatalf("BuildRequestJSON: %v", err)
	}
	root := mustJSONMap(t, body)
	arr, _ := root["messages"].([]any)
	out := make([]map[string]any, 0, len(arr))
	for _, item := range arr {
		m, _ := item.(map[string]any)
		out = append(out, m)
	}
	return out
}

func buildMessage(t *testing.T, cfg *ai.Config, opts *ai.ChatOptions, m *ai.Message) map[string]any {
	t.Helper()
	got := buildMessages(t, cfg, opts, []*ai.Message{m})
	if len(got) != 1 {
		t.Fatalf("messages = %v，应为 1 条", got)
	}
	return got[0]
}

func TestOpenAIBuildMessagesRoles(t *testing.T) {
	msgs := []*ai.Message{ai.OfSystem("系统指令")}

	for _, c := range []struct {
		name, model, optRole string
		want                 string
	}{
		{"默认 auto 普通模型", "gpt-4o", "", "system"},
		{"o 系推理模型 auto", "o3-mini", "", "developer"},
		{"gpt-5 auto", "gpt-5-turbo", "", "developer"},
		{"无连字符 gpt5", "gpt5-mini", "", "developer"},
		{"o1-preview 保持 system", "o1-preview", "", "system"},
		{"显式 system", "o3-mini", "system", "system"},
		{"显式 developer", "gpt-4o", "developer", "developer"},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg := &ai.Config{Model: c.model}
			opts := &ai.ChatOptions{}
			if c.optRole != "" {
				opts.OptionSet(openai.OptionInstructionRole, c.optRole)
			}
			got := buildMessages(t, cfg, opts, msgs)
			if len(got) != 1 || got[0]["role"] != c.want {
				t.Fatalf("role = %v, want %q", got, c.want)
			}
		})
	}

	t.Run("非法角色值 panic", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("非法 openai_instruction_role 应 panic")
			}
		}()
		opts := &ai.ChatOptions{}
		opts.OptionSet(openai.OptionInstructionRole, "bogus")
		buildMessages(t, &ai.Config{Model: "gpt-4o"}, opts, msgs)
	})
}

func TestOpenAIBuildMessagesThinkingReplay(t *testing.T) {
	thinkingOnly := &ai.Message{Role: ai.RoleAssistant, Content: &ai.AssistantMessage{Thinking: "想想"}}

	t.Run("普通目标跳过纯思考消息", func(t *testing.T) {
		got := buildMessages(t, &ai.Config{Model: "gpt-4o"}, &ai.ChatOptions{}, []*ai.Message{thinkingOnly})
		if len(got) != 0 {
			t.Fatalf("messages = %v，应为空", got)
		}
	})
	t.Run("DeepSeek 模型回放 reasoning_content", func(t *testing.T) {
		got := buildMessage(t, &ai.Config{Model: "deepseek-chat"}, &ai.ChatOptions{}, thinkingOnly)
		if got["reasoning_content"] != "想想" || got["content"] != "" {
			t.Fatalf("deepseek 节点 = %v", got)
		}
	})
	t.Run("OpenRouter 端点回放 reasoning", func(t *testing.T) {
		got := buildMessage(t, &ai.Config{Model: "x", APIURL: "https://openrouter.ai/api"}, &ai.ChatOptions{}, thinkingOnly)
		if got["reasoning"] != "想想" {
			t.Fatalf("openrouter 节点 = %v", got)
		}
	})
}

func TestOpenAIBuildMessagesAssistantVariants(t *testing.T) {
	cfg := &ai.Config{Model: "gpt-4o"}

	t.Run("工具调用消息 content 为 null", func(t *testing.T) {
		am := &ai.AssistantMessage{ToolCalls: []ai.ToolCall{{
			ID: "call_1", Name: "query-order",
			ArgumentsStr: `{"orderId":`,
		}}}
		got := buildMessage(t, cfg, &ai.ChatOptions{}, &ai.Message{Role: ai.RoleAssistant, Content: am})
		if _, has := got["content"]; !has {
			t.Fatal("含 tool_calls 时 content 字段须存在")
		}
		if got["content"] != nil {
			t.Fatalf("content = %v, want null", got["content"])
		}
		calls, _ := got["tool_calls"].([]any)
		call, _ := calls[0].(map[string]any)
		fn, _ := call["function"].(map[string]any)
		if fn["name"] != "query-order" {
			t.Fatalf("function = %v", fn)
		}
		if fn["arguments"] != "{}" {
			t.Fatalf("截断 arguments 应净化为 {}，got %v", fn["arguments"])
		}
	})

	t.Run("块中 <think> 前导段剥除", func(t *testing.T) {
		am := &ai.AssistantMessage{Blocks: []ai.ContentBlock{ai.TextBlock{Text: "<think>reason</think>answer"}}}
		got := buildMessage(t, cfg, &ai.ChatOptions{}, &ai.Message{Role: ai.RoleAssistant, Content: am})
		arr, _ := got["content"].([]any)
		item, _ := arr[0].(map[string]any)
		if item["type"] != "text" || item["text"] != "answer" {
			t.Fatalf("content[0] = %v", item)
		}
	})
}

func TestOpenAIBuildMessagesUserAndTool(t *testing.T) {
	cfg := &ai.Config{Model: "gpt-4o"}

	t.Run("用户多模态", func(t *testing.T) {
		m := &ai.Message{Role: ai.RoleUser, Content: []ai.ContentBlock{
			ai.TextBlock{Text: "看图"},
			ai.ImageBlock{URL: "https://x/i.png", Mime: "image/png"},
		}}
		got := buildMessage(t, cfg, &ai.ChatOptions{}, m)
		arr, _ := got["content"].([]any)
		if len(arr) != 2 {
			t.Fatalf("content = %v", got["content"])
		}
		first, _ := arr[0].(map[string]any)
		second, _ := arr[1].(map[string]any)
		if first["type"] != "text" {
			t.Fatalf("content[0] = %v", first)
		}
		if second["type"] != "image_url" {
			t.Fatalf("content[1] = %v", second)
		}
		iu, _ := second["image_url"].(map[string]any)
		if iu["url"] != "https://x/i.png" {
			t.Fatalf("image_url = %v", iu)
		}
	})

	t.Run("工具结果消息", func(t *testing.T) {
		m := ai.OfTool(ai.ToolResultOf("A1 已发货"), "query-order", "call_1", false)
		got := buildMessage(t, cfg, &ai.ChatOptions{}, m)
		if got["role"] != "tool" || got["name"] != "query-order" ||
			got["tool_call_id"] != "call_1" || got["content"] != "A1 已发货" {
			t.Fatalf("tool 节点 = %v", got)
		}
	})
}

// —— 响应侧：call 分支（整体 body 解析 → 终态载体） ——

func parseCall(cfg *ai.Config, body string) (*ai.Response, []*ai.ChatEvent, *ai.Accumulator) {
	acc := ai.NewAccumulator()
	var events []*ai.ChatEvent
	ctx := ai.NewStreamContext(cfg, acc, false, 0, func(ev *ai.ChatEvent) { events = append(events, ev) })
	(&openai.Dialect{}).ParseResponseJSON(ctx, body)
	return acc.BuildResponse("", events), events, acc
}

func TestOpenAIParseCallText(t *testing.T) {
	resp, events, acc := parseCall(&ai.Config{Model: "gpt-4o"}, `{
		"id": "chatcmpl-1",
		"object": "chat.completion",
		"model": "gpt-4o-2024-08-06",
		"choices": [{"index": 0, "message": {"role": "assistant", "content": "你好"}, "finish_reason": "stop"}],
		"usage": {
			"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15,
			"completion_tokens_details": {"reasoning_tokens": 7},
			"prompt_tokens_details": {"cached_tokens": 3}
		}
	}`)
	if resp.Text() != "你好" {
		t.Fatalf("text = %q", resp.Text())
	}
	if resp.FinishReason() != "stop" {
		t.Fatalf("finishReason = %q", resp.FinishReason())
	}
	if resp.Model() != "gpt-4o-2024-08-06" {
		t.Fatalf("model 应回退到响应体声明值，got %q", resp.Model())
	}
	u := resp.Usage()
	if u == nil || u.PromptTokens != 10 || u.CompletionTokens != 5 || u.TotalTokens != 15 ||
		u.ThinkTokens != 7 || u.CacheReadInputTokens != 3 {
		t.Fatalf("usage = %+v", u)
	}
	if len(events) != 0 {
		t.Fatalf("call 纯文本不应产生增量事件，got %v", events)
	}
	if !acc.IsFinished() {
		t.Fatal("call 应置 finished")
	}
}

func TestOpenAIParseCallToolCalls(t *testing.T) {
	resp, _, _ := parseCall(&ai.Config{Model: "gpt-4o"}, `{
		"id": "c2",
		"model": "gpt-4o",
		"choices": [{
			"index": 0,
			"message": {
				"role": "assistant",
				"content": null,
				"tool_calls": [{"id": "call_1", "type": "function", "index": 0,
					"function": {"name": "query-order", "arguments": "{\"orderId\":\"A1\"}"}}]
			},
			"finish_reason": "tool_calls"
		}],
		"usage": {"prompt_tokens": 1, "completion_tokens": 2, "total_tokens": 3}
	}`)
	if resp.FinishReason() != "tool" {
		t.Fatalf("finishReason = %q（tool_calls 应归一为 tool）", resp.FinishReason())
	}
	calls := resp.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("toolCalls = %+v", calls)
	}
	c := calls[0]
	if c.ID != "call_1" || c.Index != "idx:0" || c.Name != "query-order" {
		t.Fatalf("toolCall = %+v", c)
	}
	if c.ArgumentsStr != `{"orderId":"A1"}` || c.Arguments["orderId"] != "A1" {
		t.Fatalf("arguments = %q / %v", c.ArgumentsStr, c.Arguments)
	}
}

func TestOpenAIParseCallReasoning(t *testing.T) {
	resp, _, _ := parseCall(&ai.Config{Model: "deepseek-chat"}, `{
		"choices": [{"index": 0,
			"message": {"role": "assistant", "content": "答案", "reasoning_content": "思考过程"},
			"finish_reason": "stop"}]
	}`)
	if resp.Text() != "答案" || resp.Thinking() != "思考过程" {
		t.Fatalf("text/thinking = %q / %q", resp.Text(), resp.Thinking())
	}
}

func TestOpenAIParseCallError(t *testing.T) {
	resp, events, _ := parseCall(&ai.Config{Model: "gpt-4o"}, `{
		"error": {"message": "Rate limited", "type": "rate_limit_error", "code": 429}
	}`)
	if resp.Err() == nil {
		t.Fatal("error 节点应置错误")
	}
	if got := resp.Err().Error(); got != "[rate_limit_error] Rate limited" {
		t.Fatalf("err = %q", got)
	}
	if resp.FinishReason() != "" {
		t.Fatalf("错误路径 finishReason = %q，应保持空", resp.FinishReason())
	}
	if len(events) != 1 || events[0].Type != ai.EventError || events[0].Err == nil {
		t.Fatalf("events = %+v，应有一条 ERROR", events)
	}
}

func TestOpenAIParseCallErrorPlainText(t *testing.T) {
	resp, _, _ := parseCall(&ai.Config{}, "error upstream timeout")
	if resp.Err() == nil || resp.Err().Error() != "error upstream timeout" {
		t.Fatalf("err = %v", resp.Err())
	}
}

func TestOpenAIParseCallRefusal(t *testing.T) {
	resp, events, _ := parseCall(&ai.Config{}, `{
		"object": "chat.completion",
		"choices": [{"index": 0,
			"message": {"role": "assistant", "content": "", "refusal": "不能协助该请求"},
			"finish_reason": "stop"}]
	}`)
	if len(events) != 1 || events[0].Type != ai.EventRefusalDelta || events[0].Text != "不能协助该请求" {
		t.Fatalf("events = %+v，应有一条 REFUSAL_DELTA", events)
	}
	if resp.Text() != "" {
		t.Fatalf("text = %q", resp.Text())
	}
}

func TestOpenAIParseCallEmpty(t *testing.T) {
	resp, _, _ := parseCall(&ai.Config{}, `{
		"id": "c3", "model": "m",
		"choices": [],
		"usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}
	}`)
	if !resp.IsEmpty() {
		t.Fatalf("空结果 message = %+v", resp.Message())
	}
	if resp.FinishReason() != "stop" {
		t.Fatalf("finishReason = %q", resp.FinishReason())
	}
	if resp.Usage() == nil || resp.Usage().TotalTokens != 2 {
		t.Fatalf("usage = %+v", resp.Usage())
	}
}

func TestOpenAIParseCallLegacyFunctionCall(t *testing.T) {
	resp, _, _ := parseCall(&ai.Config{}, `{
		"choices": [{"index": 0,
			"message": {"role": "assistant", "function_call": {"name": "foo", "arguments": "{\"a\":1}"}},
			"finish_reason": "function_call"}]
	}`)
	calls := resp.ToolCalls()
	if len(calls) != 1 || calls[0].Name != "foo" || calls[0].ID != "legacy_function_call" {
		t.Fatalf("toolCalls = %+v", calls)
	}
	if calls[0].Arguments["a"] != float64(1) {
		t.Fatalf("arguments = %v", calls[0].Arguments)
	}
	if resp.FinishReason() != "tool" {
		t.Fatalf("finishReason = %q", resp.FinishReason())
	}
}

func TestOpenAIParseCallMultimodalAndSearch(t *testing.T) {
	resp, _, _ := parseCall(&ai.Config{}, `{
		"choices": [{"index": 0,
			"message": {"role": "assistant",
				"content": [
					{"type": "text", "text": "看图"},
					{"type": "image_url", "image_url": {"url": "data:image/png;base64,aGk="}}
				],
				"search_results": [{"id": "r1", "title": "T", "url": "https://x", "snippet": "S"}]
			},
			"finish_reason": "stop"}]
	}`)
	m := resp.Message()
	if m.Text != "看图" {
		t.Fatalf("text = %q（content 数组文本投影）", m.Text)
	}
	if len(m.Blocks) != 2 {
		t.Fatalf("blocks = %+v", m.Blocks)
	}
	img, _ := m.Blocks[1].(ai.ImageBlock)
	if string(img.Data) != "hi" || img.Mime != "image/png" {
		t.Fatalf("imageBlock = %+v（data URL 应解码）", m.Blocks[1])
	}
	if len(m.SearchResults) != 1 || m.SearchResults[0].ID != "r1" || m.SearchResults[0].Snippet != "S" {
		t.Fatalf("searchResults = %+v", m.SearchResults)
	}
}

func TestOpenAIParseCallMalformedFrame(t *testing.T) {
	resp, _, _ := parseCall(&ai.Config{}, "not-json{{{")
	if resp.Err() != nil {
		t.Fatalf("坏帧应跳过，got err %v", resp.Err())
	}
	if !resp.IsEmpty() {
		t.Fatalf("message = %+v", resp.Message())
	}
}

// —— httptest 假 OpenAI 回放（httpx → 方言整链，不真连厂商） ——

func TestOpenAICallViaFakeServer(t *testing.T) {
	const callBody = `{"id":"chatcmpl-x","object":"chat.completion","model":"gpt-4o",` +
		`"choices":[{"index":0,"message":{"role":"assistant","content":"早上好"},` +
		`"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("请求路径 = %q，应已补全 /v1/chat/completions", r.URL.Path)
		}
		if au := r.Header.Get("Authorization"); au != "Bearer sk-test" {
			t.Errorf("Authorization = %q", au)
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("请求体非法: %v", err)
		}
		if req["model"] != "gpt-4o" || req["stream"] != false {
			t.Errorf("请求体 = %v", req)
		}
		_, _ = w.Write([]byte(callBody))
	}))
	defer srv.Close()

	cfg := &ai.Config{Model: "gpt-4o", APIURL: srv.URL, APIKey: "sk-test"}
	d := &openai.Dialect{}
	opts := &ai.ChatOptions{}
	msgs := []*ai.Message{ai.OfUser("问候")}

	body, url, err := d.BuildRequestJSON(cfg, opts, msgs, false)
	if err != nil {
		t.Fatalf("BuildRequestJSON: %v", err)
	}
	client := httpx.New()
	respBody, status, err := client.PostJSON(context.Background(), url, body,
		map[string]string{"Authorization": "Bearer " + cfg.APIKey})
	if err != nil || status != 200 {
		t.Fatalf("PostJSON: status=%d err=%v", status, err)
	}

	acc := ai.NewAccumulator()
	var events []*ai.ChatEvent
	ctx := ai.NewStreamContext(cfg, acc, false, 0, func(ev *ai.ChatEvent) { events = append(events, ev) })
	d.ParseResponseJSON(ctx, string(respBody))
	resp := acc.BuildResponse(cfg.Model, events)

	if resp.Text() != "早上好" || resp.FinishReason() != "stop" {
		t.Fatalf("resp text=%q finish=%q", resp.Text(), resp.FinishReason())
	}
	if u := resp.Usage(); u == nil || u.TotalTokens != 6 {
		t.Fatalf("usage = %+v", u)
	}
}

func TestOpenAIStreamViaFakeServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if acc := r.Header.Get("Accept"); acc != "text/event-stream" {
			t.Errorf("Accept = %q", acc)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, frame := range []string{
			`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"query-order","arguments":""}}]}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"orderId\":\"A9\"}"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			"[DONE]",
		} {
			_, _ = w.Write([]byte("data: " + frame + "\n\n"))
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer srv.Close()

	cfg := &ai.Config{Model: "gpt-4o", APIURL: srv.URL}
	d := &openai.Dialect{}
	body, url, err := d.BuildRequestJSON(cfg, &ai.ChatOptions{}, nil, true)
	if err != nil {
		t.Fatalf("BuildRequestJSON: %v", err)
	}

	acc := ai.NewAccumulator()
	acc.SetStream(true)
	var events []*ai.ChatEvent
	ctx := ai.NewStreamContext(cfg, acc, true, 0, func(ev *ai.ChatEvent) { events = append(events, ev) })
	client := httpx.New()
	err = client.PostJSONStream(context.Background(), url, body, nil, func(_, data string) error {
		d.ParseResponseJSON(ctx, data)
		return nil
	})
	if err != nil {
		t.Fatalf("PostJSONStream: %v", err)
	}
	resp := acc.BuildResponse(cfg.Model, events)

	if len(events) != 2 || events[0].Type != ai.EventToolCallStart || events[1].Type != ai.EventToolCallArgsDelta {
		var got []ai.EventType
		for _, ev := range events {
			got = append(got, ev.Type)
		}
		t.Fatalf("事件序列 = %v", got)
	}
	calls := resp.ToolCalls()
	if len(calls) != 1 || calls[0].ArgumentsStr != `{"orderId":"A9"}` {
		t.Fatalf("toolCalls = %+v", calls)
	}
	if resp.FinishReason() != "tool" || !acc.IsFinished() {
		t.Fatalf("finish=%q finished=%v", resp.FinishReason(), acc.IsFinished())
	}
}

// —— 响应侧：stream 分支（帧事件序列 + 终态参数恢复） ——

func parseStream(cfg *ai.Config, frames ...string) (*ai.Accumulator, []*ai.ChatEvent) {
	acc := ai.NewAccumulator()
	acc.SetStream(true)
	var events []*ai.ChatEvent
	ctx := ai.NewStreamContext(cfg, acc, true, 0, func(ev *ai.ChatEvent) { events = append(events, ev) })
	d := &openai.Dialect{}
	for _, f := range frames {
		d.ParseResponseJSON(ctx, f)
	}
	return acc, events
}

func TestOpenAIParseStreamToolCallAndUsage(t *testing.T) {
	_, events := parseStream(&ai.Config{Model: "gpt-4o"},
		`{"id":"c9","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
		`{"choices":[{"index":0,"delta":{"reasoning_content":"思"}}]}`,
		`{"choices":[{"index":0,"delta":{"reasoning_content":"考","content":"答"}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"query-order","arguments":""}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"ord"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"erId\":\"A1\"}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":9,"completion_tokens":8,"total_tokens":17}}`,
		"[DONE]",
	)

	wantSeq := []ai.EventType{
		ai.EventThinkingDelta, // 思
		ai.EventThinkingDelta, // 考
		ai.EventTextDelta,     // 答
		ai.EventToolCallStart, // idx:0（仅一次）
		ai.EventToolCallArgsDelta,
		ai.EventToolCallArgsDelta,
	}
	if len(events) != len(wantSeq) {
		var got []ai.EventType
		for _, ev := range events {
			got = append(got, ev.Type)
		}
		t.Fatalf("事件序列 = %v, want %v", got, wantSeq)
	}
	for i := range wantSeq {
		if events[i].Type != wantSeq[i] {
			t.Fatalf("事件[%d] = %v, want %v", i, events[i].Type, wantSeq[i])
		}
	}
	if events[3].Index != 0 || events[3].ToolCallID != "call_1" {
		t.Fatalf("TOOL_CALL_START = %+v", events[3])
	}
	if events[4].Text != `{"ord` || events[5].Text != `erId":"A1"}` {
		t.Fatalf("args 增量 = %q / %q", events[4].Text, events[5].Text)
	}
}

func TestOpenAIParseStreamTerminal(t *testing.T) {
	acc, _ := parseStream(&ai.Config{Model: "gpt-4o"},
		`{"id":"c9","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
		`{"choices":[{"index":0,"delta":{"reasoning_content":"思"}}]}`,
		`{"choices":[{"index":0,"delta":{"reasoning_content":"考","content":"答"}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"query-order","arguments":""}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"orderId\":\"A1\"}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":9,"completion_tokens":8,"total_tokens":17}}`,
		"[DONE]",
	)
	m := acc.Message()
	if m.Text != "答" || m.Thinking != "思考" {
		t.Fatalf("text/thinking = %q / %q", m.Text, m.Thinking)
	}
	if len(m.ToolCalls) != 1 {
		t.Fatalf("toolCalls = %+v", m.ToolCalls)
	}
	tc := m.ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "query-order" || tc.ArgumentsStr != `{"orderId":"A1"}` {
		t.Fatalf("终态 toolCall = %+v（分片参数应完整恢复）", tc)
	}
	if tc.Arguments["orderId"] != "A1" {
		t.Fatalf("arguments map = %v", tc.Arguments)
	}
	if !acc.IsFinished() {
		t.Fatal("[DONE] 应置 finished")
	}
	if u := acc.Usage(); u == nil || u.TotalTokens != 17 {
		t.Fatalf("usage = %+v", u)
	}
}

func TestOpenAIParseStreamThinkTag(t *testing.T) {
	// 无推理字段端点：<think> 标签状态机
	acc, events := parseStream(&ai.Config{Model: "qwen"},
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":"<think>前"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"段</think>正"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"文","finish_reason":"stop"}},{"index":1,"delta":{}}]}`,
	)
	wantSeq := []ai.EventType{
		ai.EventThinkingDelta, // 前
		ai.EventThinkingDelta, // 段
		ai.EventTextDelta,     // 正
		ai.EventTextDelta,     // 文
	}
	var gotSeq []ai.EventType
	for _, ev := range events {
		gotSeq = append(gotSeq, ev.Type)
	}
	if len(gotSeq) != len(wantSeq) {
		t.Fatalf("事件序列 = %v, want %v", gotSeq, wantSeq)
	}
	for i := range wantSeq {
		if gotSeq[i] != wantSeq[i] {
			t.Fatalf("事件[%d] = %v, want %v", i, gotSeq[i], wantSeq[i])
		}
	}
	m := acc.Message()
	if m.Text != "正文" || m.Thinking != "前段" {
		t.Fatalf("text/thinking = %q / %q", m.Text, m.Thinking)
	}
}

// —— 出站净化与工具消息重建 ——

func TestSanitizeArguments(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"b":1,"a":"x"}`, `{"a":"x","b":1}`},
		{`{"a":`, "{}"},
		{`[1,2]`, "{}"},
		{``, "{}"},
		{`"str"`, "{}"},
	}
	for _, c := range cases {
		if got := openai.SanitizeArguments(c.in, "f"); got != c.want {
			t.Fatalf("SanitizeArguments(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBuildAssistantToolCallMessage(t *testing.T) {
	d := &openai.Dialect{}
	acc := ai.NewAccumulator()
	ctx := ai.NewStreamContext(nil, acc, true, 0, nil)

	start := ctx.Event(ai.EventToolCallStart)
	start.ToolCall = &ai.ToolCall{Index: "idx:0", ID: "call_1", Name: "query-order"}
	ctx.Emit(start)
	d1 := ctx.Event(ai.EventToolCallArgsDelta)
	d1.ToolCall = &ai.ToolCall{Index: "idx:0"}
	d1.Text = `{"orderId`
	ctx.Emit(d1)
	d2 := ctx.Event(ai.EventToolCallArgsDelta)
	d2.ToolCall = &ai.ToolCall{Index: "idx:0"}
	d2.Text = `":"A1"}`
	ctx.Emit(d2)

	msg := d.BuildAssistantToolCallMessage(acc, acc.ToolCallBuilders())
	if msg == nil || msg.Role != ai.RoleAssistant {
		t.Fatalf("msg = %+v", msg)
	}
	am, _ := msg.Content.(*ai.AssistantMessage)
	if len(am.ToolCalls) != 1 {
		t.Fatalf("toolCalls = %+v", am.ToolCalls)
	}
	tc := am.ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "query-order" || tc.ArgumentsStr != `{"orderId":"A1"}` {
		t.Fatalf("toolCall = %+v", tc)
	}

	// 截断参数 → 出站净化为 {}
	acc2 := ai.NewAccumulator()
	ctx2 := ai.NewStreamContext(nil, acc2, true, 0, nil)
	s2 := ctx2.Event(ai.EventToolCallStart)
	s2.ToolCall = &ai.ToolCall{Index: "idx:0", Name: "f"}
	ctx2.Emit(s2)
	a2 := ctx2.Event(ai.EventToolCallArgsDelta)
	a2.ToolCall = &ai.ToolCall{Index: "idx:0"}
	a2.Text = `{"a":`
	ctx2.Emit(a2)
	msg2 := d.BuildAssistantToolCallMessage(acc2, acc2.ToolCallBuilders())
	am2, _ := msg2.Content.(*ai.AssistantMessage)
	if am2.ToolCalls[0].ArgumentsStr != "{}" {
		t.Fatalf("截断参数应净化，got %q", am2.ToolCalls[0].ArgumentsStr)
	}

	// 无调用 → nil
	if d.BuildAssistantToolCallMessage(ai.NewAccumulator(), nil) != nil {
		t.Fatal("空聚合应返回 nil")
	}
}

func TestBuildAssistantMessageByToolMessages(t *testing.T) {
	d := &openai.Dialect{}
	msg := &ai.AssistantMessage{Text: "调用工具"}
	toolMsgs := []*ai.Message{
		ai.OfTool(ai.ToolResultOf("A1 已发货"), "query-order", "call_1", false),
		ai.OfTool(&ai.ToolResult{Text: "图已生成", Blocks: []ai.ContentBlock{ai.ImageBlock{URL: "https://x/1.png"}}}, "gen", "call_2", false),
	}
	out := d.BuildAssistantMessageByToolMessages(msg, toolMsgs)
	if out.Text != "A1 已发货\n图已生成" {
		t.Fatalf("text = %q", out.Text)
	}
	if len(out.Blocks) != 1 {
		t.Fatalf("blocks = %+v（非文本块应合并）", out.Blocks)
	}
	if out.Metadata["reason"] != "tool" || out.Metadata["source"] != "调用工具" {
		t.Fatalf("metadata = %v", out.Metadata)
	}
}

// —— 输出 schema 指令（Java 逐字对标形态） ——

func TestPrepareOutputSchemaInstruction(t *testing.T) {
	d := &openai.Dialect{}
	var sb strings.Builder
	sb.WriteString("前文")
	d.PrepareOutputSchemaInstruction(`{"type":"object"}`, &sb)
	s := sb.String()
	if !strings.HasPrefix(s, "前文\n\n## [IMPORTANT: OUTPUT FORMAT]\n") {
		t.Fatalf("instruction 前缀形态 = %q", s)
	}
	if !strings.Contains(s, "<output_schema>\n{\"type\":\"object\"}\n</output_schema>") {
		t.Fatalf("instruction 应嵌入 schema，got %q", s)
	}
	if !strings.HasSuffix(s, "Output only the raw JSON, beginning with '{' and ending with '}'.") {
		t.Fatalf("instruction 结尾形态 = %q", s)
	}

	opts := &ai.ChatOptions{}
	d.PrepareOutputFormatOptions(opts)
	if got := opts.RawJSON(); got != `{"response_format":{"type":"json_object"}}` {
		t.Fatalf("RawJSON = %s", got)
	}
}
