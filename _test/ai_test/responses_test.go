// responses_test.go P1-e openai-responses 方言测试：SelectDialect 匹配、请求构建
// （instructions/扁平工具/reasoning 归一/多模态输入/合成与回放 input items/prompt cache/
// text.format）、非流式解析（文本+推理+usage/工具调用+跨轮回放/状态机/媒体引用/空回退）、
// 流式解析（生命周期/分片隔离/推理签名/快照归一/函数参数恢复/服务端工具/音频/终态门）、
// 全栈两轮 autoToolCall。
package ai_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"testing"

	"github.com/crazy-airhead/aifei-go/ai"
	openai "github.com/crazy-airhead/aifei-go/ai/dialect/openai"
)

// respDialect 被测方言（import 即注册）。
func respDialect() *openai.ResponsesDialect { return &openai.ResponsesDialect{} }

// respCfg 基准配置（gpt-5 属推理能力模型，统一 reasoning 映射生效）。
func respCfg() *ai.Config { return &ai.Config{Model: "gpt-5", APIURL: "https://api.openai.com/v1"} }

// respStreamOpts 携带 options 的流式回放（快照模式等依赖 ctx.Options 的场景）。
func respStreamOpts(opts *ai.ChatOptions, frames ...string) (*ai.Accumulator, []*ai.ChatEvent) {
	acc := ai.NewAccumulator()
	acc.SetStream(true)
	var events []*ai.ChatEvent
	ctx := ai.NewStreamContext(respCfg(), acc, true, 0, func(ev *ai.ChatEvent) { events = append(events, ev) })
	ctx.SetOptions(opts)
	for _, f := range frames {
		respDialect().ParseResponseJSON(ctx, f)
	}
	return acc, events
}

// ---- SelectDialect ----

func TestSelectDialectResponses(t *testing.T) {
	cases := []struct {
		name string
		cfg  *ai.Config
		want string
	}{
		{"Standard 精确", &ai.Config{Standard: "openai-responses", APIURL: "https://gw.example/v1/chat/completions"}, "openai-responses"},
		{"URL 后缀（无 standard）", &ai.Config{APIURL: "https://api.openai.com/v1/responses"}, "openai-responses"},
		{"URL 后缀去尾斜杠与查询串", &ai.Config{APIURL: "https://gw.example/v1/responses/?x=1"}, "openai-responses"},
		{"chat 端点不误配", &ai.Config{APIURL: "https://api.openai.com/v1/chat/completions"}, "openai-completions"},
		{"standard 占用时 URL 不参与", &ai.Config{Standard: "anthropic", APIURL: "https://x/v1/responses"}, "anthropic"},
	}
	for _, c := range cases {
		d := ai.SelectDialect(c.cfg)
		if d == nil || d.Name() != c.want {
			t.Fatalf("%s：SelectDialect = %v，want %s", c.name, d, c.want)
		}
	}
}

// ---- 请求构建 ----

func TestResponsesBuildRequestBasics(t *testing.T) {
	d := respDialect()
	opts := &ai.ChatOptions{}
	opts.Temperature(0.5).
		Set("stop", []any{"END"}).        // Chat Completions 专属：剔除
		Set("frequency_penalty", 0.1).    // 同上
		Set("max_completion_tokens", 77). // → max_output_tokens
		Set("instructions", "补充指令").      // 与 system 合并（system 在前）
		Set("stream_options", map[string]any{"include_usage": true, "include_obfuscation": true})
	msgs := []*ai.Message{ai.OfSystem("你是助手"), ai.OfSystem("第二条"), ai.OfUser("查 A1")}

	body, url, err := d.BuildRequestJSON(respCfg(), opts, msgs, true)
	if err != nil {
		t.Fatalf("BuildRequestJSON: %v", err)
	}
	if url != "https://api.openai.com/v1/responses" {
		t.Fatalf("url = %q", url)
	}
	root := mustJSONMap(t, body)
	if root["model"] != "gpt-5" || root["stream"] != true {
		t.Fatalf("model/stream = %v/%v", root["model"], root["stream"])
	}
	if root["instructions"] != "你是助手\n\n第二条\n\n补充指令" {
		t.Fatalf("instructions = %q", root["instructions"])
	}
	if root["max_output_tokens"] != float64(77) {
		t.Fatalf("max_output_tokens = %v", root["max_output_tokens"])
	}
	if root["temperature"] != 0.5 {
		t.Fatalf("temperature = %v", root["temperature"])
	}
	for _, banned := range []string{"stop", "frequency_penalty", "max_completion_tokens", "response_format"} {
		if _, has := root[banned]; has {
			t.Fatalf("黑名单选项 %q 泄漏进请求体", banned)
		}
	}
	so := matrixNode(t, root["stream_options"])
	if len(so) != 1 || so["include_obfuscation"] != true {
		t.Fatalf("stream_options = %v（流式仅 include_obfuscation 可转发）", so)
	}
	input := matrixArray(t, root["input"])
	if len(input) != 1 {
		t.Fatalf("input = %v（system 提取到 instructions，不应入 input）", input)
	}
	if u := matrixNode(t, input[0]); u["role"] != "user" || u["content"] != "查 A1" {
		t.Fatalf("input[0] = %v", u)
	}

	// 字节稳定性（快照可比对的前提）
	body2, _, err := d.BuildRequestJSON(respCfg(), opts, msgs, true)
	if err != nil || !bytes.Equal(body, body2) {
		t.Fatalf("重复构建请求体不稳定：%v", err)
	}

	// 非流式不带 stream_options；显式 max_output_tokens 优先于别名
	opts2 := &ai.ChatOptions{}
	opts2.Set("max_output_tokens", 9).Set("max_tokens", 100)
	nb, _, err := d.BuildRequestJSON(respCfg(), opts2, msgs, false)
	if err != nil {
		t.Fatalf("BuildRequestJSON: %v", err)
	}
	nroot := mustJSONMap(t, nb)
	if nroot["stream"] != false {
		t.Fatalf("stream = %v", nroot["stream"])
	}
	if _, has := nroot["stream_options"]; has {
		t.Fatalf("非流式不应有 stream_options")
	}
	if nroot["max_output_tokens"] != float64(9) {
		t.Fatalf("max_output_tokens = %v（显式优先）", nroot["max_output_tokens"])
	}
}

func TestResponsesBuildRequestTools(t *testing.T) {
	d := respDialect()
	tool := ai.NewTool("query-order").Desc("查订单").
		StringParam("orderId", "订单号", true).Build()
	strictTool := ai.NewTool("strict-tool").Desc("严格").
		StringParam("q", "查询", true).Strict(true).Build()
	badSchema := ai.NewTool("bad-schema").Desc("坏 schema").
		InputSchemaJSON("{not-json").Build()
	outputTool := ai.NewTool("out-tool").Desc("带输出 schema").
		StringParam("q", "查询", true).
		OutputSchemaJSON(`{"type":"object","properties":{"summary":{"type":"string"}}}`).
		OutputSchemaJSON("{broken").Build() // 末次设置生效：非法 → 丢弃

	opts := &ai.ChatOptions{}
	opts.Tools = []ai.FunctionTool{tool, strictTool, badSchema, outputTool}
	body, _, err := d.BuildRequestJSON(respCfg(), opts, []*ai.Message{ai.OfUser("hi")}, false)
	if err != nil {
		t.Fatalf("BuildRequestJSON: %v", err)
	}
	tools := matrixArray(t, mustJSONMap(t, body)["tools"])
	if len(tools) != 4 {
		t.Fatalf("tools = %v", tools)
	}

	t0 := matrixNode(t, tools[0])
	if t0["type"] != "function" || t0["name"] != "query-order" || t0["description"] != "查订单" {
		t.Fatalf("tool[0] = %v（应为扁平形态，无 function 嵌套）", t0)
	}
	if t0["strict"] != false {
		t.Fatalf("strict 缺省 = %v，应为 false（openai-java 定义为必填）", t0["strict"])
	}
	params := matrixNode(t, t0["parameters"])
	if params["type"] != "object" {
		t.Fatalf("parameters = %v", params)
	}
	if _, has := matrixNode(t, params["properties"])["orderId"]; !has {
		t.Fatalf("parameters.properties = %v", params["properties"])
	}

	if matrixNode(t, tools[1])["strict"] != true {
		t.Fatalf("显式 strict 未生效: %v", tools[1])
	}

	// 非法 input schema 回退空参数骨架（不污染请求）
	bad := matrixNode(t, tools[2])
	badParams := matrixNode(t, bad["parameters"])
	if badParams["type"] != "object" || len(matrixNode(t, badParams["properties"])) != 0 {
		t.Fatalf("非法 schema 回退 = %v", badParams)
	}

	// 非法 output schema 直接丢弃
	if _, has := matrixNode(t, tools[3])["output_schema"]; has {
		t.Fatalf("非法 output_schema 应丢弃: %v", tools[3])
	}

	// 合法 output schema 保留
	okOut := ai.NewTool("ok-out").StringParam("q", "查询", true).
		OutputSchemaJSON(`{"type":"object"}`).Build()
	opts2 := &ai.ChatOptions{Tools: []ai.FunctionTool{okOut}}
	body2, _, _ := d.BuildRequestJSON(respCfg(), opts2, []*ai.Message{ai.OfUser("hi")}, false)
	if _, has := matrixNode(t, matrixArray(t, mustJSONMap(t, body2)["tools"])[0])["output_schema"]; !has {
		t.Fatalf("合法 output_schema 应保留")
	}
}

func TestResponsesBuildRequestReasoning(t *testing.T) {
	d := respDialect()
	build := func(model string, fn func(o *ai.ChatOptions)) map[string]any {
		opts := &ai.ChatOptions{}
		fn(opts)
		body, _, err := d.BuildRequestJSON(&ai.Config{Model: model, APIURL: "https://api.openai.com/v1"}, opts,
			[]*ai.Message{ai.OfUser("hi")}, false)
		if err != nil {
			t.Fatalf("BuildRequestJSON: %v", err)
		}
		return mustJSONMap(t, body)
	}

	// 显式 reasoning：min→low、summary 数组取首个已知值、generate_summary 别名、未知 effort 透传
	root := build("gpt-5", func(o *ai.ChatOptions) {
		o.Set("reasoning", map[string]any{"effort": "min", "summary": []any{"concise", "auto"}})
	})
	r := matrixNode(t, root["reasoning"])
	if r["effort"] != "low" || r["summary"] != "concise" {
		t.Fatalf("reasoning = %v", r)
	}
	root = build("gpt-5", func(o *ai.ChatOptions) {
		o.Set("reasoning", map[string]any{"effort": "ultra", "generate_summary": "detailed"})
	})
	r = matrixNode(t, root["reasoning"])
	// 未知 effort 透传；generate_summary 别名值归一为官方枚举、键名保留
	if r["effort"] != "ultra" || r["generate_summary"] != "detailed" {
		t.Fatalf("reasoning（未知透传+别名） = %v", r)
	}
	// 字符串形态 = effort 简写
	root = build("gpt-5", func(o *ai.ChatOptions) { o.Set("reasoning", "high") })
	if matrixNode(t, root["reasoning"])["effort"] != "high" {
		t.Fatalf("reasoning 字符串 = %v", root["reasoning"])
	}

	// 统一开关（gpt-5 命中能力门限）：thinking(false)→effort none；thinking(true)→summary auto
	root = build("gpt-5", func(o *ai.ChatOptions) { o.Thinking(false) })
	if matrixNode(t, root["reasoning"])["effort"] != "none" {
		t.Fatalf("thinking(false) = %v", root["reasoning"])
	}
	root = build("gpt-5", func(o *ai.ChatOptions) { o.Thinking(true) })
	if matrixNode(t, root["reasoning"])["summary"] != "auto" {
		t.Fatalf("thinking(true) = %v", root["reasoning"])
	}
	// reasoning_effort 严格归一 + 与 thinking(true) 并存
	root = build("gpt-5", func(o *ai.ChatOptions) { o.Set("reasoning_effort", "high") })
	r = matrixNode(t, root["reasoning"])
	if r["effort"] != "high" {
		t.Fatalf("reasoning_effort = %v", r)
	}
	if _, has := r["summary"]; has {
		t.Fatalf("未开 thinking 不应补 summary: %v", r)
	}
	root = build("gpt-5", func(o *ai.ChatOptions) { o.Thinking(true).Set("reasoning_effort", "low") })
	r = matrixNode(t, root["reasoning"])
	if r["effort"] != "low" || r["summary"] != "auto" {
		t.Fatalf("effort+summary = %v", r)
	}
	// 非推理模型：不自动映射
	root = build("gpt-4o", func(o *ai.ChatOptions) { o.Thinking(true) })
	if _, has := root["reasoning"]; has {
		t.Fatalf("gpt-4o 不应有 reasoning: %v", root["reasoning"])
	}

	// store=false 补 include=reasoning.encrypted_content（幂等不重复）
	root = build("gpt-5", func(o *ai.ChatOptions) { o.Set("store", false) })
	include := matrixArray(t, root["include"])
	if len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include = %v", include)
	}
	root = build("gpt-5", func(o *ai.ChatOptions) {})
	if _, has := root["include"]; has {
		t.Fatalf("默认 store=true 不应补 include")
	}
}

func TestResponsesBuildRequestToolChoiceAndCache(t *testing.T) {
	d := respDialect()
	opts := &ai.ChatOptions{}
	opts.Set("tool_choice", map[string]any{"type": "function", "function": map[string]any{"name": "query-order"}}).
		SetCacheControl(ai.CacheControl{PromptCacheKey: "u-9", Ttl: "10m"}). // Ttl 是 Anthropic 语义：不映射
		Set("prompt_cache_breakpoint", true).
		Set("prompt_cache_options", map[string]any{"mode": "explicit", "ttl": "30m", "custom": "x"}).
		Set("prompt_cache_retention", "24h")
	body, _, err := d.BuildRequestJSON(respCfg(), opts, []*ai.Message{ai.OfUser("查 A1"), ai.OfUser("查 A2")}, false)
	if err != nil {
		t.Fatalf("BuildRequestJSON: %v", err)
	}
	root := mustJSONMap(t, body)

	// tool_choice 扁平化
	tc := matrixNode(t, root["tool_choice"])
	if tc["type"] != "function" || tc["name"] != "query-order" {
		t.Fatalf("tool_choice = %v（应扁平 {type,name}）", tc)
	}
	// 字符串透传
	opts2 := &ai.ChatOptions{}
	opts2.Set("tool_choice", "auto")
	root2, _, _ := func() (map[string]any, string, error) {
		b, u, e := d.BuildRequestJSON(respCfg(), opts2, []*ai.Message{ai.OfUser("hi")}, false)
		return mustJSONMap(t, b), u, e
	}()
	if root2["tool_choice"] != "auto" {
		t.Fatalf("tool_choice 字符串 = %v", root2["tool_choice"])
	}

	if root["prompt_cache_key"] != "u-9" {
		t.Fatalf("prompt_cache_key = %v", root["prompt_cache_key"])
	}
	if _, has := root["prompt_cache_retention"]; !has || root["prompt_cache_retention"] != "24h" {
		t.Fatalf("prompt_cache_retention = %v", root["prompt_cache_retention"])
	}
	pco := matrixNode(t, root["prompt_cache_options"])
	if len(pco) != 2 || pco["mode"] != "explicit" || pco["ttl"] != "30m" {
		t.Fatalf("prompt_cache_options = %v（白名单外字段应忽略）", pco)
	}

	// 单断点挂到最后一个可缓存内容项
	input := matrixArray(t, root["input"])
	last := matrixNode(t, matrixArray(t, matrixNode(t, input[1])["content"])[0])
	if bp := matrixNode(t, last["prompt_cache_breakpoint"]); bp["mode"] != "explicit" {
		t.Fatalf("断点 = %v", last)
	}
	first := matrixNode(t, matrixArray(t, matrixNode(t, input[0])["content"])[0])
	if _, has := first["prompt_cache_breakpoint"]; has {
		t.Fatalf("单断点只挂末项: %v", input[0])
	}

	// 多断点（Solon 扩展）：末 N 项各挂
	opts3 := &ai.ChatOptions{}
	opts3.Set("prompt_cache_breakpoints", []any{true, true})
	body3, _, _ := d.BuildRequestJSON(respCfg(), opts3, []*ai.Message{ai.OfUser("a"), ai.OfUser("b"), ai.OfUser("c")}, false)
	input3 := matrixArray(t, mustJSONMap(t, body3)["input"])
	flagged := 0
	for _, raw := range input3 {
		for _, part := range matrixArray(t, matrixNode(t, raw)["content"]) {
			if _, has := matrixNode(t, part)["prompt_cache_breakpoint"]; has {
				flagged++
			}
		}
	}
	if flagged != 2 {
		t.Fatalf("多断点挂载数 = %d，应为 2（末两项）", flagged)
	}
}

func TestResponsesBuildUserMultimodal(t *testing.T) {
	d := respDialect()
	img := ai.ImageBlockOfURL("https://i.example/a.png")
	audio := ai.AudioBlockOfBase64([]byte("wav-bytes"), "audio/wav")
	msgs := []*ai.Message{ai.OfUser([]ai.ContentBlock{ai.TextBlockOf("看图"), img, audio})}

	// 未启用 input_audio：报错（仅兼容网关支持）
	if _, _, err := d.BuildRequestJSON(respCfg(), &ai.ChatOptions{}, msgs, false); err == nil {
		t.Fatalf("未启用 responses_input_audio_enabled 应报错")
	}

	opts := &ai.ChatOptions{}
	opts.Set(openai.OptionResponsesInputAudioEnabled, true)
	body, _, err := d.BuildRequestJSON(respCfg(), opts, msgs, false)
	if err != nil {
		t.Fatalf("BuildRequestJSON: %v", err)
	}
	content := matrixArray(t, matrixNode(t, matrixArray(t, mustJSONMap(t, body)["input"])[0])["content"])
	if len(content) != 3 {
		t.Fatalf("content = %v", content)
	}
	if c0 := matrixNode(t, content[0]); c0["type"] != "input_text" || c0["text"] != "看图" {
		t.Fatalf("content[0] = %v", c0)
	}
	if c1 := matrixNode(t, content[1]); c1["type"] != "input_image" || c1["image_url"] != "https://i.example/a.png" {
		t.Fatalf("content[1] = %v", c1)
	}
	c2 := matrixNode(t, content[2])
	ia := matrixNode(t, c2["input_audio"])
	if c2["type"] != "input_audio" || ia["data"] != base64.StdEncoding.EncodeToString([]byte("wav-bytes")) ||
		ia["format"] != "wav" {
		t.Fatalf("content[2] = %v（嵌套形态 {data,format}）", c2)
	}
}

func TestResponsesBuildAssistantSynthesized(t *testing.T) {
	d := respDialect()
	build := func(am *ai.AssistantMessage) []any {
		msgs := []*ai.Message{{Role: ai.RoleAssistant, Content: am}}
		body, _, err := d.BuildRequestJSON(respCfg(), &ai.ChatOptions{}, msgs, false)
		if err != nil {
			t.Fatalf("BuildRequestJSON: %v", err)
		}
		return matrixArray(t, mustJSONMap(t, body)["input"])
	}

	// 纯思考（无协议状态）：退化为 reasoning_text（DeepSeek 扩展），不补空 assistant 项
	input := build(&ai.AssistantMessage{Thinking: "想想"})
	if len(input) != 1 {
		t.Fatalf("ThinkingOnly input = %v", input)
	}
	rs := matrixNode(t, input[0])
	if rs["type"] != "reasoning" {
		t.Fatalf("reasoning 项 = %v", rs)
	}
	if txt := matrixNode(t, matrixArray(t, rs["content"])[0]); txt["type"] != "reasoning_text" || txt["text"] != "想想" {
		t.Fatalf("reasoning content = %v", rs["content"])
	}

	// 文本 + 工具调用：assistant 文本项 + function_call 项（截断参数净化为 {}）
	input = build(&ai.AssistantMessage{Text: "已查", ToolCalls: []ai.ToolCall{
		{ID: "call_1", Name: "query-order", ArgumentsStr: `{"orderId":"A1"}`},
		{ID: "call_2", Name: "query-order", ArgumentsStr: `{"orderId":"A1"`}, // 截断
	}})
	if len(input) != 3 {
		t.Fatalf("input = %v（assistant + 2 个 function_call）", input)
	}
	if a := matrixNode(t, input[0]); a["role"] != "assistant" || a["content"] != "已查" {
		t.Fatalf("input[0] = %v", a)
	}
	fc1 := matrixNode(t, input[1])
	if fc1["type"] != "function_call" || fc1["call_id"] != "call_1" || fc1["arguments"] != `{"orderId":"A1"}` {
		t.Fatalf("function_call[0] = %v", fc1)
	}
	if fc2 := matrixNode(t, input[2]); fc2["arguments"] != "{}" {
		t.Fatalf("截断参数应净化为 {}: %v", fc2)
	}

	// image_generation 产出：按 id 回传 image_generation_call，data 不再入 content
	imgGen := ai.ImageBlock{URL: "image-generation://ig_1",
		Meta: map[string]any{"source_type": "image_generation_call", "image_generation_id": "ig_1"}}
	input = build(&ai.AssistantMessage{Text: "图好了", Blocks: []ai.ContentBlock{imgGen}})
	if len(input) != 2 {
		t.Fatalf("input = %v", input)
	}
	ig := matrixNode(t, input[0])
	if ig["type"] != "image_generation_call" || ig["id"] != "ig_1" {
		t.Fatalf("image_generation_call = %v", ig)
	}
	msgContent := matrixArray(t, matrixNode(t, input[1])["content"])
	if c0 := matrixNode(t, msgContent[0]); c0["type"] != "input_text" || c0["text"] != "图好了" {
		t.Fatalf("assistant content = %v（image_generation 块不重复写 data）", msgContent)
	}
}

func TestResponsesBuildToolOutputItem(t *testing.T) {
	d := respDialect()
	msgs := []*ai.Message{ai.OfTool(ai.ToolResultOf("结果文本"), "query-order", "call_1", false)}
	body, _, err := d.BuildRequestJSON(respCfg(), &ai.ChatOptions{}, msgs, false)
	if err != nil {
		t.Fatalf("BuildRequestJSON: %v", err)
	}
	item := matrixNode(t, matrixArray(t, mustJSONMap(t, body)["input"])[0])
	if item["type"] != "function_call_output" || item["call_id"] != "call_1" || item["output"] != "结果文本" {
		t.Fatalf("function_call_output = %v", item)
	}

	// 块形态结果：output 为 input_* 内容数组
	rich := &ai.ToolResult{Text: "旁白", Blocks: []ai.ContentBlock{
		ai.TextBlockOf("块文本"),
		ai.ImageBlockOfURL("https://i.example/r.png"),
	}}
	msgs = []*ai.Message{{Role: ai.RoleTool, Content: &ai.ToolMessage{Result: rich, Name: "query-order", CallID: "call_2"}}}
	body, _, err = d.BuildRequestJSON(respCfg(), &ai.ChatOptions{}, msgs, false)
	if err != nil {
		t.Fatalf("BuildRequestJSON: %v", err)
	}
	out := matrixArray(t, matrixNode(t, matrixArray(t, mustJSONMap(t, body)["input"])[0])["output"])
	if len(out) != 2 {
		t.Fatalf("output = %v", out)
	}
	if o0 := matrixNode(t, out[0]); o0["type"] != "input_text" || o0["text"] != "块文本" {
		t.Fatalf("output[0] = %v", o0)
	}
	if o1 := matrixNode(t, out[1]); o1["type"] != "input_image" || o1["image_url"] != "https://i.example/r.png" {
		t.Fatalf("output[1] = %v", o1)
	}
}

// TestResponsesBuildAssistantReplay 协议状态回放：流式聚合 → BuildAssistantToolCallMessage
// （原始 output items 按序回放）→ 下一轮请求；GLM 默认关回放、选项覆盖、语义篡改降级。
func TestResponsesBuildAssistantReplay(t *testing.T) {
	d := respDialect()
	acc, _ := matrixStream(d, respCfg(),
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[]}}`,
		`{"type":"response.reasoning_text.delta","item_id":"rs_1","output_index":0,"content_index":0,"delta":"想想"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"enc-1"}}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"query-order","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":1,"delta":"{\"orderId\":\"A1\"}"}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"query-order","arguments":"{\"orderId\":\"A1\"}"}}`,
		`{"type":"response.completed","response":{"id":"resp_1","model":"gpt-5","output":[
			{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"enc-1"},
			{"type":"function_call","id":"fc_1","call_id":"call_1","name":"query-order","arguments":"{\"orderId\":\"A1\"}"}],
			"usage":{"input_tokens":9,"output_tokens":9,"total_tokens":18}}}`,
		`[DONE]`,
	)
	asst := d.BuildAssistantToolCallMessage(acc, acc.ToolCallBuilders())
	if asst == nil {
		t.Fatalf("BuildAssistantToolCallMessage 返回 nil")
	}
	history := []*ai.Message{
		ai.OfUser("查 A1"),
		asst,
		ai.OfTool(ai.ToolResultOf("A1 已发货"), "query-order", "call_1", false),
	}

	body, _, err := d.BuildRequestJSON(respCfg(), &ai.ChatOptions{}, history, false)
	if err != nil {
		t.Fatalf("BuildRequestJSON: %v", err)
	}
	input := matrixArray(t, mustJSONMap(t, body)["input"])
	if len(input) != 4 { // user / reasoning / function_call / function_call_output
		t.Fatalf("input = %v", input)
	}
	rs := matrixNode(t, input[1])
	if rs["type"] != "reasoning" || rs["id"] != "rs_1" || rs["encrypted_content"] != "enc-1" {
		t.Fatalf("回放 reasoning 项 = %v", rs)
	}
	fc := matrixNode(t, input[2])
	if fc["type"] != "function_call" || fc["call_id"] != "call_1" || fc["arguments"] != `{"orderId":"A1"}` {
		t.Fatalf("回放 function_call = %v", fc)
	}
	fo := matrixNode(t, input[3])
	if fo["type"] != "function_call_output" || fo["call_id"] != "call_1" || fo["output"] != "A1 已发货" {
		t.Fatalf("function_call_output = %v", fo)
	}

	// GLM 默认关 reasoning 回放（兼容层会拒绝 reasoning item）
	glmCfg := &ai.Config{Model: "glm-4.6", APIURL: "https://api.openai.com/v1"}
	body, _, err = d.BuildRequestJSON(glmCfg, &ai.ChatOptions{}, history, false)
	if err != nil {
		t.Fatalf("BuildRequestJSON: %v", err)
	}
	if got := matrixArray(t, mustJSONMap(t, body)["input"]); len(got) != 3 {
		t.Fatalf("GLM input = %v（reasoning 应剔除）", got)
	}
	// 选项显式开启后恢复回放
	glmOpts := &ai.ChatOptions{}
	glmOpts.Set(openai.OptionResponsesReasoningReplayEnabled, true)
	body, _, err = d.BuildRequestJSON(glmCfg, glmOpts, history, false)
	if err != nil {
		t.Fatalf("BuildRequestJSON: %v", err)
	}
	if got := matrixArray(t, mustJSONMap(t, body)["input"]); len(got) != 4 {
		t.Fatalf("GLM+选项 input = %v（应回放）", got)
	}

	// 语义篡改：状态摘要失配 → 原始 items 拒绝回放，降级为合成路径：
	// reasoning 退化为 DeepSeek reasoning_text 扩展（无 id/encrypted_content 可供官方回放）
	tampered, _ := asst.Content.(*ai.AssistantMessage)
	tampered.Text = "被改动的文本"
	body, _, err = d.BuildRequestJSON(respCfg(), &ai.ChatOptions{}, history, false)
	if err != nil {
		t.Fatalf("BuildRequestJSON: %v", err)
	}
	input = matrixArray(t, mustJSONMap(t, body)["input"])
	if len(input) != 5 { // user / reasoning(合成) / assistant(被改文本) / function_call / function_call_output
		t.Fatalf("篡改后 input = %v", input)
	}
	rs2 := matrixNode(t, input[1])
	if rs2["type"] != "reasoning" {
		t.Fatalf("篡改后首项 = %v（应为合成 reasoning）", rs2)
	}
	if _, has := rs2["id"]; has {
		t.Fatalf("篡改后原始 rs_1 不得回放: %v", rs2)
	}
	if _, has := rs2["encrypted_content"]; has {
		t.Fatalf("篡改后 encrypted_content 不得回放: %v", rs2)
	}
	if txt := matrixNode(t, matrixArray(t, rs2["content"])[0]); txt["text"] != "想想" {
		t.Fatalf("合成 reasoning content = %v", rs2["content"])
	}
	if a := matrixNode(t, input[2]); a["role"] != "assistant" || a["content"] != "被改动的文本" {
		t.Fatalf("降级 assistant 项 = %v", a)
	}
}

func TestResponsesPrepareOutputFormatOptions(t *testing.T) {
	d := respDialect()
	opts := &ai.ChatOptions{}
	opts.OutputSchema = `{"type":"object","properties":{"city":{"type":"string"}}}`
	d.PrepareOutputFormatOptions(opts)
	format := matrixNode(t, matrixNode(t, opts.OptionGet("text"))["format"])
	if format["type"] != "json_schema" || format["name"] != "output_schema" || format["strict"] != true {
		t.Fatalf("format = %v", format)
	}
	schema := matrixNode(t, format["schema"])
	if schema["additionalProperties"] != false {
		t.Fatalf("strict 补全 additionalProperties = %v", schema)
	}
	if req := matrixArray(t, schema["required"]); len(req) != 1 || req[0] != "city" {
		t.Fatalf("strict 补全 required = %v", req)
	}

	// 非法 schema：退回 json_object（保留「输出为合法 JSON」协议保证）
	bad := &ai.ChatOptions{}
	bad.OutputSchema = "not-json"
	d.PrepareOutputFormatOptions(bad)
	bf := matrixNode(t, matrixNode(t, bad.OptionGet("text"))["format"])
	if bf["type"] != "json_object" {
		t.Fatalf("非法 schema format = %v", bf)
	}
}

// ---- 非流式解析 ----

func TestResponsesNonStreamTextReasoning(t *testing.T) {
	resp, events, acc := matrixCall(respDialect(), respCfg(), `{
		"id": "resp_ns1", "model": "gpt-5", "status": "completed",
		"output": [
			{"type": "reasoning", "id": "rs_9",
			 "content": [{"type": "reasoning_text", "text": "先想"}], "summary": []},
			{"type": "message", "id": "msg_9", "role": "assistant",
			 "content": [{"type": "output_text", "text": "答案",
				"annotations": [{"type": "url_citation", "url": "https://e.example", "title": "文档", "cited_text": "原文"}]}]}
		],
		"usage": {"input_tokens": 10, "output_tokens": 5, "total_tokens": 15,
			"input_tokens_details": {"cached_tokens": 4, "cache_write_tokens": 2},
			"output_tokens_details": {"reasoning_tokens": 3}}
	}`)
	if resp.Text() != "答案" || resp.Thinking() != "先想" {
		t.Fatalf("text/thinking = %q/%q", resp.Text(), resp.Thinking())
	}
	if resp.Model() != "gpt-5" || resp.FinishReason() != "stop" {
		t.Fatalf("model/finishReason = %q/%q", resp.Model(), resp.FinishReason())
	}
	u := resp.Usage()
	if u == nil || u.PromptTokens != 10 || u.CompletionTokens != 5 || u.TotalTokens != 15 ||
		u.CacheReadInputTokens != 4 || u.CacheCreationInputTokens != 2 || u.ThinkTokens != 3 {
		t.Fatalf("usage = %+v", u)
	}
	// 兼容网关顶层缓存字段仅在官方明细缺失时生效
	_, _, acc2 := matrixCall(respDialect(), respCfg(),
		`{"id":"r","status":"completed","output":[],"usage":{"input_tokens":8,"output_tokens":2,"prompt_cache_hit_tokens":6,"input_cache_write_tokens":1}}`)
	if u2 := acc2.Usage(); u2 == nil || u2.CacheReadInputTokens != 6 || u2.CacheCreationInputTokens != 1 {
		t.Fatalf("兼容 usage = %+v", u2)
	}
	// usage 全 0 节点也保留；缺失 usage → nil
	_, _, acc3 := matrixCall(respDialect(), respCfg(),
		`{"id":"r","status":"completed","output":[],"usage":{"total_tokens":0}}`)
	if acc3.Usage() == nil {
		t.Fatalf("零值 usage 节点应保留")
	}

	// 引用事件（与流式 annotation.added 对称）
	citations := matrixOf(t, events, ai.EventCitation)
	if len(citations) != 1 || citations[0].Citation == nil ||
		citations[0].Citation.URL != "https://e.example" || citations[0].Citation.Title != "文档" ||
		citations[0].Citation.Snippet != "原文" {
		t.Fatalf("citations = %+v", citations)
	}
	if citations[0].SubType != "url_citation" || citations[0].Text != "https://e.example" {
		t.Fatalf("citation meta = %+v", citations[0])
	}

	// 协议状态：reasoning id 供多轮回放
	state, ok := acc.Message().ProtocolStates["openai.responses"].(*ai.ProtocolState)
	if !ok {
		t.Fatalf("协议状态缺失：%v", acc.Message().ProtocolStates)
	}
	items, _ := state.Data["reasoning_items"].([]any)
	if len(items) != 1 || matrixNode(t, items[0])["id"] != "rs_9" {
		t.Fatalf("reasoning_items = %v", state.Data["reasoning_items"])
	}
	_ = resp
}

// TestResponsesNonStreamToolCallRoundTrip function_call 终态 + 工具事件生命周期 +
// finishReason tool_calls 回写（BuildResponse 归一为 tool）+ 第二轮原始 items 回放。
func TestResponsesNonStreamToolCallRoundTrip(t *testing.T) {
	d := respDialect()
	body := `{
		"id": "resp_ns2", "model": "gpt-5", "status": "completed",
		"output": [
			{"type": "reasoning", "id": "rs_t", "summary": [{"type": "summary_text", "text": "想想"}]},
			{"type": "function_call", "id": "fc_t", "call_id": "call_t", "name": "query-order",
			 "arguments": "{\"orderId\":\"A9\"}"},
			{"type": "function_call", "id": "fc_b", "call_id": "call_b", "name": "query-order",
			 "arguments": "{\"orderId\":"}
		],
		"usage": {"input_tokens": 7, "output_tokens": 3, "total_tokens": 10}
	}`
	resp, events, _ := matrixCall(d, respCfg(), body)
	calls := resp.ToolCalls()
	if len(calls) != 2 {
		t.Fatalf("toolCalls = %+v", calls)
	}
	if calls[0].ID != "call_t" || calls[0].Name != "query-order" ||
		calls[0].Arguments["orderId"] != "A9" || calls[0].ArgumentsStr != `{"orderId":"A9"}` {
		t.Fatalf("calls[0] = %+v", calls[0])
	}
	if calls[1].ArgumentsStr != "{}" { // 截断参数出口净化
		t.Fatalf("calls[1] = %+v（应净化为 {}）", calls[1])
	}
	if resp.Thinking() != "想想" { // reasoning 无 content 时回退 summary
		t.Fatalf("thinking = %q", resp.Thinking())
	}
	if resp.FinishReason() != "tool" { // tool_calls 归一
		t.Fatalf("finishReason = %q", resp.FinishReason())
	}
	// 非流式保留完整工具生命周期
	if n := len(matrixOf(t, events, ai.EventToolCallStart)); n != 2 {
		t.Fatalf("TOOL_CALL_START = %d", n)
	}
	argDeltas := matrixOf(t, events, ai.EventToolCallArgsDelta)
	if len(argDeltas) != 2 { // 净化为 "{}" 的调用也保留参数轨迹
		t.Fatalf("TOOL_CALL_ARGS_DELTA = %d", len(argDeltas))
	}
	if argDeltas[1].Text != "{}" {
		t.Fatalf("ARGS_DELTA[1] = %q（应为净化后 {}）", argDeltas[1].Text)
	}
	if n := len(matrixOf(t, events, ai.EventToolCallEnd)); n != 2 {
		t.Fatalf("TOOL_CALL_END = %d", n)
	}

	// 第二轮回放：原始 output items（reasoning + function_call×2）+ 工具输出
	history := []*ai.Message{
		ai.OfUser("查 A9"),
		d.BuildAssistantToolCallMessage(resp2Acc(t, d, body), nil),
		ai.OfTool(ai.ToolResultOf("A9 已发货"), "query-order", "call_t", false),
		ai.OfTool(ai.ToolResultOf("B 缺货"), "query-order", "call_b", false),
	}
	rb, _, err := d.BuildRequestJSON(respCfg(), &ai.ChatOptions{}, history, false)
	if err != nil {
		t.Fatalf("BuildRequestJSON: %v", err)
	}
	rinput := matrixArray(t, mustJSONMap(t, rb)["input"])
	if len(rinput) != 6 { // user / reasoning / function_call×2 / function_call_output×2（不合并）
		t.Fatalf("回放 input = %v", rinput)
	}
	if rs := matrixNode(t, rinput[1]); rs["type"] != "reasoning" || rs["id"] != "rs_t" {
		t.Fatalf("回放 reasoning = %v", rs)
	}
	fc1 := matrixNode(t, rinput[2])
	if fc1["type"] != "function_call" || fc1["call_id"] != "call_t" {
		t.Fatalf("回放 function_call[0] = %v", fc1)
	}
	if fc2 := matrixNode(t, rinput[3]); fc2["call_id"] != "call_b" || fc2["arguments"] != "{}" {
		t.Fatalf("回放 function_call[1] = %v（截断参数净化）", fc2)
	}
	if o1 := matrixNode(t, rinput[4]); o1["call_id"] != "call_t" || o1["output"] != "A9 已发货" {
		t.Fatalf("回放 output[0] = %v", o1)
	}
	if o2 := matrixNode(t, rinput[5]); o2["call_id"] != "call_b" || o2["output"] != "B 缺货" {
		t.Fatalf("回放 output[1] = %v", o2)
	}
}

// resp2Acc 二次解析同一 body 取聚合（round-trip 辅助）。
func resp2Acc(t *testing.T, d *openai.ResponsesDialect, body string) *ai.Accumulator {
	t.Helper()
	acc := ai.NewAccumulator()
	ctx := ai.NewStreamContext(respCfg(), acc, false, 0, func(*ai.ChatEvent) {})
	d.ParseResponseJSON(ctx, body)
	return acc
}

// TestResponsesNonStreamStatuses failed / cancelled / incomplete / 顶层 error 四种终态。
func TestResponsesNonStreamStatuses(t *testing.T) {
	d := respDialect()

	// failed：ERROR + 空终态
	resp, events, _ := matrixCall(d, respCfg(),
		`{"id":"resp_f","status":"failed","error":{"message":"boom","code":"500"}}`)
	if resp.Err() == nil {
		t.Fatalf("failed 应有错误")
	}
	if errs := matrixOf(t, events, ai.EventError); len(errs) != 1 {
		t.Fatalf("ERROR = %d 个", len(errs))
	}
	if resp.Message() == nil {
		t.Fatalf("failed 也应有空终态")
	}

	// cancelled：finishReason + STATUS 终态事件
	_, events, acc := matrixCall(d, respCfg(),
		`{"id":"resp_c","model":"gpt-5","status":"cancelled","output":[
			{"type":"message","content":[{"type":"output_text","text":"半截"}]}]}`)
	if acc.BuildResponse("", events).FinishReason() != "cancelled" {
		t.Fatalf("cancelled finishReason = %q", acc.BuildResponse("", events).FinishReason())
	}
	statuses := matrixOf(t, events, ai.EventStatus)
	if len(statuses) != 1 || statuses[0].RawType != "response.cancelled" ||
		statuses[0].Attrs["finish_reason"] != "cancelled" {
		t.Fatalf("cancelled STATUS = %+v", statuses)
	}

	// incomplete：max_output_tokens 归一为 length（subType 保留原始值）
	_, events, acc = matrixCall(d, respCfg(),
		`{"id":"resp_i","model":"gpt-5","status":"incomplete",
		 "incomplete_details":{"reason":"max_output_tokens"},"output":[
			{"type":"message","content":[{"type":"output_text","text":"截断"}]}]}`)
	if fr := acc.BuildResponse("", events).FinishReason(); fr != "length" {
		t.Fatalf("incomplete finishReason = %q", fr)
	}
	statuses = matrixOf(t, events, ai.EventStatus)
	if len(statuses) != 1 || statuses[0].SubType != "max_output_tokens" ||
		statuses[0].Text != "length" || statuses[0].Attrs["finish_reason"] != "length" {
		t.Fatalf("incomplete STATUS = %+v", statuses)
	}

	// 顶层 error 对象
	resp, events, _ = matrixCall(d, respCfg(), `{"error":{"message":"限额","type":"quota"}}`)
	if resp.Err() == nil || len(matrixOf(t, events, ai.EventError)) != 1 {
		t.Fatalf("顶层 error 未生效：%v", resp.Err())
	}
}

func TestResponsesNonStreamMediaCitation(t *testing.T) {
	audioB64 := base64.StdEncoding.EncodeToString([]byte("wavdata"))
	resp, events, _ := matrixCall(respDialect(), respCfg(), `{
		"id": "resp_m", "model": "gpt-5", "status": "completed",
		"output": [
			{"type": "message", "id": "msg_m", "content": [
				{"type": "refusal", "refusal": "不能说更多"},
				{"type": "output_audio", "data": "`+audioB64+`", "transcript": "语音转写"}
			]},
			{"type": "image_generation_call", "id": "ig_1", "status": "completed",
			 "result": "https://img.example/x.png", "revised_prompt": "a cat"},
			{"type": "web_search_call", "id": "ws_1", "status": "completed"}
		]
	}`)
	// 拒答进正文 + 专用事件（REFUSAL_DELTA + 终态 CONTENT_FILTER 各一次）；音频转写进正文
	if resp.Text() != "不能说更多语音转写" {
		t.Fatalf("text = %q", resp.Text())
	}
	if n := len(matrixOf(t, events, ai.EventRefusalDelta)); n != 1 {
		t.Fatalf("REFUSAL_DELTA = %d", n)
	}
	if cf := matrixOf(t, events, ai.EventContentFilter); len(cf) != 1 || cf[0].RawType != "response.refusal.done" {
		t.Fatalf("CONTENT_FILTER = %+v", cf)
	}
	// 媒体块：音频（原始字节 + transcript）与图像生成（URL + meta）
	blocks := resp.Message().Blocks
	if len(blocks) != 2 {
		t.Fatalf("blocks = %+v", blocks)
	}
	audio, ok := blocks[0].(ai.AudioBlock)
	if !ok || string(audio.Data) != "wavdata" || audio.Meta["transcript"] != "语音转写" {
		t.Fatalf("audio = %+v", blocks[0])
	}
	image, ok := blocks[1].(ai.ImageBlock)
	if !ok || image.URL != "https://img.example/x.png" ||
		image.Meta["image_generation_id"] != "ig_1" || image.Meta["revised_prompt"] != "a cat" {
		t.Fatalf("image = %+v", blocks[1])
	}
	// 文本与媒体统一走事件聚合（终态 blocks 不丢文本）
	if td := matrixOf(t, events, ai.EventTextDelta); len(td) != 1 || td[0].Text != "不能说更多语音转写" {
		t.Fatalf("TEXT_DELTA = %+v", td)
	}
	if n := len(matrixOf(t, events, ai.EventMediaDone)); n != 2 {
		t.Fatalf("MEDIA_DONE = %d", n)
	}
	// 服务端工具项 → SERVER_TOOL_RESULT（与流式对称）
	st := matrixOf(t, events, ai.EventServerToolResult)
	if len(st) != 1 || st[0].SubType != "web_search_call" || st[0].ItemID != "ws_1" {
		t.Fatalf("SERVER_TOOL_RESULT = %+v", st)
	}
}

func TestResponsesNonStreamEmptyFallback(t *testing.T) {
	// 空 output 数组：仅回退顶层 output_text（reasoning_text 不在此分支）
	resp, _, _ := matrixCall(respDialect(), respCfg(),
		`{"id":"r","model":"deepseek-r1","status":"completed","output":[],
		  "output_text":"答案","reasoning_text":"想"}`)
	if resp.Text() != "答案" || resp.Thinking() != "" {
		t.Fatalf("空 output 回退 = %q/%q", resp.Text(), resp.Thinking())
	}
	// 无 output 数组（DeepSeek 顶层字段形态）：双便捷字段
	resp, _, _ = matrixCall(respDialect(), respCfg(),
		`{"id":"r","model":"deepseek-r1","status":"completed","output_text":"答案","reasoning_text":"想"}`)
	if resp.Text() != "答案" || resp.Thinking() != "想" {
		t.Fatalf("便捷字段回退 = %q/%q", resp.Text(), resp.Thinking())
	}
}

// TestResponsesParseEntryEdge 入口边界：[DONE] 哨兵 / "error xxx" 纯文本 / 损坏帧跳过。
func TestResponsesParseEntryEdge(t *testing.T) {
	d := respDialect()
	// 非流式 [DONE]：空终态 + finished
	resp, _, _ := matrixCall(d, respCfg(), "[DONE]")
	if resp.Message() == nil {
		t.Fatalf("[DONE] 应有空终态")
	}

	// "error xxx" 纯文本（部分中转直出）
	_, events, acc := matrixCall(d, respCfg(), "error quota exceeded")
	if acc.Err() == nil || len(matrixOf(t, events, ai.EventError)) != 1 {
		t.Fatalf("纯文本 error 未生效: %v", acc.Err())
	}

	// 损坏 JSON 帧：跳过不阻断（不算完成也不报错）
	_, _, acc = matrixCall(d, respCfg(), "{oops")
	if acc.Err() != nil || acc.IsFinished() {
		t.Fatalf("损坏帧应跳过: err=%v finished=%v", acc.Err(), acc.IsFinished())
	}
}

// ---- 流式解析 ----

// TestResponsesStreamTextLifecycle 官方文本流全生命周期：delta 先记录后发射，
// done/completed 幂等补齐（已交付不再重复），completed 收口 usage 与回放状态。
func TestResponsesStreamTextLifecycle(t *testing.T) {
	acc, events := matrixStream(respDialect(), respCfg(),
		`{"type":"response.created","response":{"id":"resp_1","model":"gpt-5"}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1","status":"in_progress","content":[]}}`,
		`{"type":"response.content_part.added","item_id":"msg_1","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}`,
		`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"你"}`,
		`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"好"}`,
		`{"type":"response.output_text.done","item_id":"msg_1","output_index":0,"content_index":0,"text":"你好"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_1","status":"completed","content":[{"type":"output_text","text":"你好"}]}}`,
		`{"type":"response.completed","response":{"id":"resp_1","model":"gpt-5",
			"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12},
			"output":[{"type":"message","id":"msg_1","status":"completed","content":[{"type":"output_text","text":"你好"}]}]}}`,
		`[DONE]`,
	)
	resp := acc.BuildResponse("", events)
	if resp.Text() != "你好" {
		t.Fatalf("text = %q", resp.Text())
	}
	// done/completed 不得重复交付已发文本
	matrixStrEq(t, matrixTexts(t, events, ai.EventTextDelta), "你", "好")
	if resp.Model() != "gpt-5" || resp.FinishReason() != "stop" {
		t.Fatalf("model/finishReason = %q/%q", resp.Model(), resp.FinishReason())
	}
	if u := resp.Usage(); u == nil || u.PromptTokens != 10 || u.CompletionTokens != 2 || u.TotalTokens != 12 {
		t.Fatalf("usage = %+v", u)
	}
	if !acc.IsFinished() {
		t.Fatalf("completed 后应 finished")
	}
	// created 帧 → STATUS（订阅方可感知服务端推进）
	if st := matrixOf(t, events, ai.EventStatus); len(st) != 1 || st[0].RawType != "response.created" {
		t.Fatalf("STATUS = %+v", st)
	}
	// completed 提升回放状态：终态协议袋携带原始 output items
	state, ok := resp.Message().ProtocolStates["openai.responses"].(*ai.ProtocolState)
	if !ok {
		t.Fatalf("协议状态缺失：%v", resp.Message().ProtocolStates)
	}
	wrappers, _ := state.Data["responses_output_items"].([]any)
	if len(wrappers) != 1 {
		t.Fatalf("output_items = %v", state.Data)
	}
	if item := matrixNode(t, wrappers[0])["item"]; matrixNode(t, item)["id"] != "msg_1" {
		t.Fatalf("output item = %v", item)
	}
}

// TestResponsesStreamContentIndexIsolation 同一 message 的多个 content 分片各自
// 独立交付槽，completed 补齐不串槽、不重复。
func TestResponsesStreamContentIndexIsolation(t *testing.T) {
	acc, events := matrixStream(respDialect(), respCfg(),
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_2","content":[]}}`,
		`{"type":"response.content_part.added","item_id":"msg_2","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}`,
		`{"type":"response.output_text.delta","item_id":"msg_2","output_index":0,"content_index":0,"delta":"前"}`,
		`{"type":"response.content_part.added","item_id":"msg_2","output_index":0,"content_index":1,"part":{"type":"output_text","text":""}}`,
		`{"type":"response.output_text.delta","item_id":"msg_2","output_index":0,"content_index":1,"delta":"后"}`,
		`{"type":"response.completed","response":{"id":"resp_2","model":"gpt-5",
			"output":[{"type":"message","id":"msg_2","content":[
				{"type":"output_text","text":"前"},{"type":"output_text","text":"后"}]}]}}`,
		`[DONE]`,
	)
	resp := acc.BuildResponse("", events)
	if resp.Text() != "前后" {
		t.Fatalf("text = %q", resp.Text())
	}
	matrixStrEq(t, matrixTexts(t, events, ai.EventTextDelta), "前", "后")
}

// TestResponsesStreamReasoningSignature 推理流 + 加密签名：output_item.done 与
// completed 重放共享去重键，签名只发一次；回放状态含 reasoning item。
func TestResponsesStreamReasoningSignature(t *testing.T) {
	acc, events := matrixStream(respDialect(), respCfg(),
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[]}}`,
		`{"type":"response.reasoning_text.delta","item_id":"rs_1","output_index":0,"content_index":0,"delta":"想想"}`,
		`{"type":"response.reasoning_text.done","item_id":"rs_1","output_index":0,"content_index":0,"text":"想想"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"enc-1"}}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"message","id":"msg_1","content":[]}}`,
		`{"type":"response.output_text.delta","item_id":"msg_1","output_index":1,"content_index":0,"delta":"答"}`,
		`{"type":"response.completed","response":{"id":"resp_r","model":"gpt-5","output":[
			{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"enc-1"},
			{"type":"message","id":"msg_1","content":[{"type":"output_text","text":"答"}]}]}}`,
		`[DONE]`,
	)
	resp := acc.BuildResponse("", events)
	if resp.Thinking() != "想想" || resp.Text() != "答" {
		t.Fatalf("thinking/text = %q/%q", resp.Thinking(), resp.Text())
	}
	matrixStrEq(t, matrixTexts(t, events, ai.EventThinkingDelta), "想想")
	sigs := matrixOf(t, events, ai.EventThinkingSignature)
	if len(sigs) != 1 || sigs[0].Text != "enc-1" || sigs[0].ItemID != "rs_1" {
		t.Fatalf("THINKING_SIGNATURE = %+v（done 与 completed 应去重为一次）", sigs)
	}
	state, ok := resp.Message().ProtocolStates["openai.responses"].(*ai.ProtocolState)
	if !ok {
		t.Fatalf("协议状态缺失")
	}
	items, _ := state.Data["reasoning_items"].([]any)
	if len(items) != 1 {
		t.Fatalf("reasoning_items = %v", state.Data["reasoning_items"])
	}
	ri := matrixNode(t, items[0])
	if ri["id"] != "rs_1" || ri["encrypted_content"] != "enc-1" {
		t.Fatalf("reasoning item = %v", ri)
	}
}

// TestResponsesStreamReasoningSnapshotMode 兼容网关累计快照声明
// （responses_reasoning_delta_mode=snapshot）下的后缀归一。
func TestResponsesStreamReasoningSnapshotMode(t *testing.T) {
	opts := &ai.ChatOptions{}
	opts.Set(openai.OptionResponsesReasoningDeltaMode, "snapshot")
	acc, events := respStreamOpts(opts,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_2","summary":[]}}`,
		`{"type":"response.reasoning_text.delta","item_id":"rs_2","output_index":0,"content_index":0,"delta":"长江是中国最"}`,
		`{"type":"response.reasoning_text.delta","item_id":"rs_2","output_index":0,"content_index":0,"delta":"长江是中国最长的河流"}`,
		`{"type":"response.reasoning_text.done","item_id":"rs_2","output_index":0,"content_index":0,"text":"长江是中国最长的河流"}`,
	)
	resp := acc.BuildResponse("", events)
	if resp.Thinking() != "长江是中国最长的河流" {
		t.Fatalf("thinking = %q", resp.Thinking())
	}
	matrixStrEq(t, matrixTexts(t, events, ai.EventThinkingDelta), "长江是中国最", "长的河流")
}

// TestResponsesStreamFunctionCall 函数参数流：START 去重、逐段 ARGS_DELTA、
// done/output_item.done/completed 三处兜底共用恢复路径且不重复落地。
func TestResponsesStreamFunctionCall(t *testing.T) {
	acc, events := matrixStream(respDialect(), respCfg(),
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"query-order","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":"{\"order"}`,
		`{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":"Id\":\"A1\"}"}`,
		`{"type":"response.function_call_arguments.done","item_id":"fc_1","output_index":0,"arguments":"{\"orderId\":\"A1\"}"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"query-order","arguments":"{\"orderId\":\"A1\"}"}}`,
		`{"type":"response.completed","response":{"id":"resp_t","model":"gpt-5","output":[
			{"type":"function_call","id":"fc_1","call_id":"call_1","name":"query-order","arguments":"{\"orderId\":\"A1\"}"}],
			"usage":{"input_tokens":5,"output_tokens":5}}}`,
		`[DONE]`,
	)
	resp := acc.BuildResponse("", events)
	calls := resp.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("toolCalls = %+v（三处兜底不得重复落地）", calls)
	}
	if calls[0].ID != "call_1" || calls[0].Name != "query-order" ||
		calls[0].Arguments["orderId"] != "A1" || calls[0].ArgumentsStr != `{"orderId":"A1"}` {
		t.Fatalf("call = %+v", calls[0])
	}
	if starts := matrixOf(t, events, ai.EventToolCallStart); len(starts) != 1 {
		t.Fatalf("TOOL_CALL_START = %d（按 callId 去重）", len(starts))
	}
	matrixStrEq(t, matrixTexts(t, events, ai.EventToolCallArgsDelta), `{"order`, `Id":"A1"}`)
	// 方言不发 TOOL_CALL_END（核心统一收口）
	if n := len(matrixOf(t, events, ai.EventToolCallEnd)); n != 0 {
		t.Fatalf("TOOL_CALL_END = %d（应由核心发出）", n)
	}
}

// TestResponsesStreamTerminalGateAndMisc 终态门（incomplete 后忽略内容帧）、
// keepalive/RAW/error 帧语义。
func TestResponsesStreamTerminalGateAndMisc(t *testing.T) {
	// incomplete 后：迟到内容帧被门拦截；STATUS 为方言最后一个语义事件
	acc, events := matrixStream(respDialect(), respCfg(),
		`{"type":"response.incomplete","response":{"id":"resp_i","model":"gpt-5",
			"incomplete_details":{"reason":"max_output_tokens"},"output":[],
			"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		`{"type":"response.output_text.delta","item_id":"x","output_index":0,"content_index":0,"delta":"迟到的帧"}`,
	)
	resp := acc.BuildResponse("", events)
	if resp.FinishReason() != "length" {
		t.Fatalf("finishReason = %q", resp.FinishReason())
	}
	if n := len(matrixOf(t, events, ai.EventTextDelta)); n != 0 {
		t.Fatalf("终态门未拦截内容帧: %d", n)
	}
	st := matrixOf(t, events, ai.EventStatus)
	if len(st) != 1 || st[0].SubType != "length" || st[0].Text != "length" ||
		st[0].Attrs["finish_reason"] != "length" {
		t.Fatalf("incomplete STATUS = %+v（流式 SubType 用归一后的 finishReason）", st)
	}

	// keepalive → HEARTBEAT；未建模事件 → RAW
	_, events = matrixStream(respDialect(), respCfg(),
		`{"type":"keepalive"}`,
		`{"type":"response.custom_event","x":1}`,
	)
	if n := len(matrixOf(t, events, ai.EventHeartbeat)); n != 1 {
		t.Fatalf("HEARTBEAT = %d", n)
	}
	raws := matrixOf(t, events, ai.EventRaw)
	if len(raws) != 1 || raws[0].RawType != "response.custom_event" {
		t.Fatalf("RAW = %+v", raws)
	}

	// 顶层 error 帧：清理状态 + ERROR + finished
	acc, events = matrixStream(respDialect(), respCfg(),
		`{"error":{"message":"boom","type":"server_error"}}`,
	)
	if acc.Err() == nil || !acc.IsFinished() {
		t.Fatalf("error 帧未生效: %v", acc.Err())
	}
	if errs := matrixOf(t, events, ai.EventError); len(errs) != 1 {
		t.Fatalf("ERROR = %d", len(errs))
	}

	// response.failed 事件
	acc, events = matrixStream(respDialect(), respCfg(),
		`{"type":"response.failed","response":{"id":"resp_f2","model":"gpt-5","error":{"message":"生成失败"}}}`,
	)
	if acc.Err() == nil || len(matrixOf(t, events, ai.EventError)) != 1 {
		t.Fatalf("response.failed 未生效: %v", acc.Err())
	}
}

// TestResponsesStreamServerTools 服务端工具三段事件 + 终态去重（显式 completed 与
// output_item.done 回落共享键）+ 渐进图像 + 引用幂等。
func TestResponsesStreamServerTools(t *testing.T) {
	imgB64 := base64.StdEncoding.EncodeToString([]byte("pngbytes"))
	acc, events := matrixStream(respDialect(), respCfg(),
		`{"type":"response.web_search_call.in_progress","item_id":"ws_1","output_index":0}`,
		`{"type":"response.web_search_call.completed","item_id":"ws_1","output_index":0}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"web_search_call","id":"ws_1","status":"completed"}}`,
		`{"type":"response.image_generation_call.partial_image","item_id":"ig_1","output_index":1,"partial_image_index":0,"partial_image_b64":"`+imgB64+`"}`,
		`{"type":"response.image_generation_call.partial_image","item_id":"ig_1","output_index":1,"partial_image_index":0,"partial_image_b64":"`+imgB64+`"}`,
		`{"type":"response.output_text.annotation.added","item_id":"msg_1","output_index":2,"content_index":0,"annotation_index":0,"annotation":{"type":"url_citation","url":"https://e.example"}}`,
		`{"type":"response.output_text.annotation.added","item_id":"msg_1","output_index":2,"content_index":0,"annotation_index":0,"annotation":{"type":"url_citation","url":"https://e.example"}}`,
	)
	_ = acc
	if starts := matrixOf(t, events, ai.EventServerToolStart); len(starts) != 1 || starts[0].SubType != "web_search_call" {
		t.Fatalf("SERVER_TOOL_START = %+v", starts)
	}
	results := matrixOf(t, events, ai.EventServerToolResult)
	if len(results) != 1 { // completed 与 output_item.done 回落共享去重键
		t.Fatalf("SERVER_TOOL_RESULT = %d（应为 1，终态去重）", len(results))
	}
	// 渐进图像：媒体语义 + 原始字节 + 索引幂等
	partials := matrixOf(t, events, ai.EventMediaPartial)
	if len(partials) != 1 {
		t.Fatalf("MEDIA_PARTIAL = %d（同索引应去重）", len(partials))
	}
	img, ok := partials[0].Block.(ai.ImageBlock)
	if !ok || string(img.Data) != "pngbytes" {
		t.Fatalf("partial image = %+v（线上 base64 应解码为原始字节）", partials[0].Block)
	}
	// 引用幂等（SSE 重放不重复）
	citations := matrixOf(t, events, ai.EventCitation)
	if len(citations) != 1 || citations[0].Citation == nil || citations[0].Citation.URL != "https://e.example" {
		t.Fatalf("CITATION = %+v", citations)
	}
}

// TestResponsesStreamMediaAudioRefusal 音频旁路（数据 base64 累积 + 转写投影正文）
// 与拒答流（文本降级 + 专用事件并存）。
func TestResponsesStreamMediaAudioRefusal(t *testing.T) {
	wavB64 := base64.StdEncoding.EncodeToString([]byte("wavdata"))
	acc, events := matrixStream(respDialect(), respCfg(),
		`{"type":"response.output_audio.delta","item_id":"msg_a","output_index":0,"delta":"`+wavB64+`"}`,
		`{"type":"response.output_audio.done","item_id":"msg_a","output_index":0}`,
		`{"type":"response.output_audio_transcript.delta","item_id":"msg_a","output_index":0,"delta":"你"}`,
		`{"type":"response.output_audio_transcript.done","item_id":"msg_a","output_index":0,"transcript":"你好"}`,
		`{"type":"response.refusal.delta","item_id":"msg_r","output_index":1,"content_index":0,"delta":"不能"}`,
		`{"type":"response.refusal.done","item_id":"msg_r","output_index":1,"content_index":0,"refusal":"不能继续"}`,
	)
	resp := acc.BuildResponse("", events)
	// 音频：数据块终帧一次交付（Data 原始字节）；转写 done 另发一次（媒体语义并存）
	dones := matrixOf(t, events, ai.EventMediaDone)
	if len(dones) != 2 {
		t.Fatalf("MEDIA_DONE = %d（数据块 + 转写应各一次）", len(dones))
	}
	audio, ok := dones[0].Block.(ai.AudioBlock)
	if !ok || string(audio.Data) != "wavdata" {
		t.Fatalf("audio done = %+v", dones[0].Block)
	}
	if dones[0].SubType != "audio" || dones[1].SubType != "audio_transcript" {
		t.Fatalf("MEDIA_DONE subTypes = %q/%q", dones[0].SubType, dones[1].SubType)
	}
	// 转写投影正文（delta + done 缺失后缀）
	texts := matrixTexts(t, events, ai.EventTextDelta)
	want := []string{"你", "好", "不能", "继续"}
	if len(texts) != len(want) {
		t.Fatalf("TEXT_DELTA = %v, want %v", texts, want)
	}
	for i := range want {
		if texts[i] != want[i] {
			t.Fatalf("TEXT_DELTA[%d] = %q want %q（整体 %v）", i, texts[i], want[i], texts)
		}
	}
	if resp.Text() != "你好不能继续" {
		t.Fatalf("text = %q", resp.Text())
	}
	// 拒答：REFUSAL_DELTA 增量 + 终态 CONTENT_FILTER 一次
	if rd := matrixOf(t, events, ai.EventRefusalDelta); len(rd) != 1 || rd[0].Text != "不能" {
		t.Fatalf("REFUSAL_DELTA = %+v", rd)
	}
	if cf := matrixOf(t, events, ai.EventContentFilter); len(cf) != 1 || cf[0].Text != "不能继续" {
		t.Fatalf("CONTENT_FILTER = %+v", cf)
	}
}

// ---- 全栈 ----

// TestResponsesFullStackAutoToolCall 流式全栈两轮：工具调用轮（Responses 事件流）→
// 自动执行 → 第二轮请求回放 function_call + function_call_output → 终答。
func TestResponsesFullStackAutoToolCall(t *testing.T) {
	toolRound := []string{
		`{"type":"response.created","response":{"id":"resp_1","model":"gpt-5"}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"query-order","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":"{\"orderId\":\"A1\"}"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"query-order","arguments":"{\"orderId\":\"A1\"}"}}`,
		`{"type":"response.completed","response":{"id":"resp_1","model":"gpt-5","usage":{"input_tokens":9,"output_tokens":9,"total_tokens":18},"output":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"query-order","arguments":"{\"orderId\":\"A1\"}"}]}}`,
		`[DONE]`,
	}
	finalRound := []string{
		`{"type":"response.created","response":{"id":"resp_2","model":"gpt-5"}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1","content":[]}}`,
		`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"A1 已发货"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_1","content":[{"type":"output_text","text":"A1 已发货"}]}}`,
		`{"type":"response.completed","response":{"id":"resp_2","model":"gpt-5","usage":{"input_tokens":5,"output_tokens":4,"total_tokens":9},"output":[{"type":"message","id":"msg_1","content":[{"type":"output_text","text":"A1 已发货"}]}]}}`,
		`[DONE]`,
	}
	s := newSSEScripted(t, 0, [][]string{toolRound, finalRound})
	m, err := ai.NewChatModel(ai.Config{
		Standard: "openai-responses", APIURL: s.srv.URL, Model: "gpt-5", APIKey: "sk-test",
	})
	if err != nil {
		t.Fatalf("NewChatModel: %v", err)
	}

	rec := &sinkRecorder{}
	err = m.Prompt("查一下订单 A1").
		Options(func(o *ai.ChatOptions) {
			o.AutoToolCall = autoToolOn()
			o.Tools = []ai.FunctionTool{streamTool(t, "query-order", "A1 已发货")}
		}).
		Stream(context.Background(), rec)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	// 两轮请求
	if s.count() != 2 {
		t.Fatalf("请求数 = %d（应两轮）", s.count())
	}
	second := s.request(t, 1)
	input := matrixArray(t, second["input"])
	if len(input) != 3 { // user / function_call / function_call_output
		t.Fatalf("第二轮 input = %v", input)
	}
	fc := matrixNode(t, input[1])
	if fc["type"] != "function_call" || fc["call_id"] != "call_1" ||
		fc["arguments"] != `{"orderId":"A1"}` {
		t.Fatalf("回放 function_call = %v", fc)
	}
	fo := matrixNode(t, input[2])
	if fo["type"] != "function_call_output" || fo["call_id"] != "call_1" || fo["output"] != "A1 已发货" {
		t.Fatalf("function_call_output = %v", fo)
	}
	if second["stream"] != true || second["model"] != "gpt-5" {
		t.Fatalf("第二轮请求头 = %v/%v", second["stream"], second["model"])
	}

	// 工具确实执行过、终答进入末轮响应
	results := rec.ofType(ai.EventToolResult)
	if len(results) != 1 || results[0].ToolCallID != "call_1" || results[0].Text != "A1 已发货" {
		t.Fatalf("TOOL_RESULT = %+v", results)
	}
	end := rec.ofType(ai.EventResponseEnd)
	if len(end) != 1 {
		t.Fatalf("RESPONSE_END = %d", len(end))
	}
	if end[0].Response().Text() != "A1 已发货" {
		t.Fatalf("终答 = %q", end[0].Response().Text())
	}
}
