// interactions 请求构建（对标 GeminiInteractionsRequestBuilder）：
// {model?, system_instruction: "纯字符串", input: [step...],
// generation_config?: {...}, stream: bool, store: bool, tools?: [...],
// response_format?: [...], ...根级白名单透传}。
package gemini

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/internal/dialectkit"
)

// generationConfigWhitelist generation_config 透传白名单键（snake_case）。
var generationConfigWhitelist = map[string]struct{}{
	"image_config": {}, "max_output_tokens": {}, "seed": {}, "speech_config": {},
	"stop_sequences": {}, "transcription_config": {}, "video_config": {},
	"thinking_level": {}, "thinking_summaries": {},
}

// rootOptionWhitelist 根级透传 option 白名单。
var rootOptionWhitelist = map[string]struct{}{
	"background": {}, "previous_interaction_id": {}, "service_tier": {},
	"webhook_config": {}, "environment": {}, "safety_settings": {}, "labels": {},
}

func buildInteractionsRequest(cfg *ai.Config, opts *ai.ChatOptions, msgs []*ai.Message, isStream bool) ([]byte, error) {
	if opts == nil {
		opts = &ai.ChatOptions{}
	}
	root := map[string]any{}

	// model 在请求体（不在 URL 路径——与 generateContent 的关键差异）
	if cfg != nil && cfg.Model != "" {
		root["model"] = cfg.Model
	}

	// system_instruction：纯字符串（多 SystemMessage 以 \n\n 连接）
	if sys := interactionsSystemInstruction(msgs); sys != "" {
		root["system_instruction"] = sys
	}

	// input：step 数组（跳过 SystemMessage 与纯思考历史）
	input := []any{}
	for _, m := range msgs {
		if m.Role == ai.RoleSystem {
			continue
		}
		if isSkippableThinkingOnly(m) {
			continue
		}
		input = append(input, interactionsStepsOf(m)...)
	}
	root["input"] = input

	// generation_config（snake_case 白名单 + thinking 统一 + tool_choice 内嵌）
	if gc := buildInteractionsGenerationConfig(opts); len(gc) > 0 {
		root["generation_config"] = gc
	}

	root["stream"] = isStream
	root["store"] = optionBoolOr(opts, "store", false)

	// tools：扁平数组 [{type:"function", name, description, parameters}]
	if len(opts.Tools) > 0 {
		tools := make([]any, 0, len(opts.Tools))
		for _, tool := range opts.Tools {
			tools = append(tools, map[string]any{
				"type":        "function",
				"name":        tool.Name(),
				"description": tool.Description(),
				"parameters":  schemaObjectOf(tool.InputSchema()),
			})
		}
		root["tools"] = tools
	}

	// response_format：outputSchema 非空时 [{type:"text", mime_type:application/json, schema}]
	if schema := opts.OutputSchema; schema != "" {
		root["response_format"] = []any{map[string]any{
			"type":      "text",
			"mime_type": "application/json",
			"schema":    schemaObjectOf(schema),
		}}
	}

	// 根级白名单透传（仅 root 未含该键时）
	for k, v := range opts.Raw {
		if _, blocked := rootOptionWhitelist[k]; !blocked {
			continue
		}
		if _, exists := root[k]; !exists {
			root[k] = v
		}
	}

	return json.Marshal(root)
}

// interactionsSystemInstruction SystemMessage 内容以 \n\n 连接（纯字符串）。
func interactionsSystemInstruction(msgs []*ai.Message) string {
	var parts []string
	for _, m := range msgs {
		if m.Role != ai.RoleSystem {
			continue
		}
		if s := dialectkit.ContentText(m.Content); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n")
}

// interactionsStepsOf 消息 → input steps。
func interactionsStepsOf(m *ai.Message) []any {
	switch content := m.Content.(type) {
	case *ai.ToolMessage:
		step := map[string]any{
			"type":   "function_result",
			"result": []any{map[string]any{"type": "text", "text": content.ResultText()}},
		}
		if content.CallID != "" {
			step["call_id"] = content.CallID
		}
		return []any{step}
	case *ai.AssistantMessage:
		if len(content.ToolCalls) > 0 {
			var steps []any
			// 可选先写 model_output（若有文本/媒体）
			if content.Text != "" || len(content.Blocks) > 0 {
				if blocks := interactionsContentBlocks(m); len(blocks) > 0 {
					steps = append(steps, map[string]any{"type": "model_output", "content": blocks})
				}
			}
			// 思考签名独立 thought step（跨轮回放）
			if sig := resolveSignature(content, interactionsProtocolID, &content.ToolCalls[0], 0); sig != "" {
				steps = append(steps, map[string]any{"type": "thought", "signature": sig})
			}
			for i := range content.ToolCalls {
				call := &content.ToolCalls[i]
				_, args := stepArguments(callArgumentsOf(call))
				step := map[string]any{"type": "function_call", "name": call.Name, "arguments": args}
				if call.ID != "" {
					step["id"] = call.ID // 仅非空才写，不伪造
				}
				steps = append(steps, step)
			}
			return steps
		}
		blocks := interactionsContentBlocks(m)
		if len(blocks) == 0 {
			return nil
		}
		return []any{map[string]any{"type": "model_output", "content": blocks}}
	default:
		// 兜底：user_input + 文本
		if s := dialectkit.ContentText(m.Content); s != "" {
			return []any{map[string]any{"type": "user_input", "content": []any{map[string]any{"type": "text", "text": s}}}}
		}
		return nil
	}
}

// interactionsContentBlocks 消息多模态块 → content 数组（文本逐块 + 媒体；
// 无块时回退纯文本）。媒体项：{type: image|audio|video, mime_type,
// data(base64)|uri}，mime 缺省 image/jpeg / audio/mpeg / video/mp4。
func interactionsContentBlocks(m *ai.Message) []any {
	var out []any
	for _, block := range dialectkit.MessageBlocks(m.Content) {
		if tb, ok := block.(ai.TextBlock); ok {
			if tb.Text != "" {
				out = append(out, map[string]any{"type": "text", "text": tb.Text})
			}
			continue
		}
		if item := interactionsMediaItem(block); item != nil {
			out = append(out, item)
		}
	}
	if len(out) == 0 {
		if content := dialectkit.ContentText(m.Content); content != "" {
			out = append(out, map[string]any{"type": "text", "text": content})
		} else if am := dialectkit.AssistantOf(m); am != nil && am.Text != "" {
			out = append(out, map[string]any{"type": "text", "text": am.Text})
		}
	}
	return out
}

// interactionsMediaItem 媒体块映射（data/uri 皆空跳过；与 chat 的 mediaPart
// 形态不同——interactions 顶层 type: image|audio|video + mime_type/data/uri）。
func interactionsMediaItem(block ai.ContentBlock) map[string]any {
	if block == nil {
		return nil
	}
	var mediaType, defMime, data, uri, mime string
	switch b := block.(type) {
	case *ai.ImageBlock:
		mediaType, defMime, data, uri, mime = "image", "image/jpeg", base64Of(b.Data), b.URL, b.Mime
	case *ai.AudioBlock:
		mediaType, defMime, data, uri, mime = "audio", "audio/mpeg", base64Of(b.Data), b.URL, b.Mime
	case *ai.VideoBlock:
		mediaType, defMime, data, uri, mime = "video", "video/mp4", base64Of(b.Data), b.URL, b.Mime
	default:
		return nil
	}
	if data == "" && uri == "" {
		return nil
	}
	if mime == "" {
		mime = defMime
	}
	item := map[string]any{"type": mediaType, "mime_type": mime}
	if data != "" {
		item["data"] = data
	}
	if uri != "" {
		item["uri"] = uri
	}
	return item
}

func base64Of(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return base64.StdEncoding.EncodeToString(b)
}

// buildInteractionsGenerationConfig generation_config 组装：
//  1. option generation_config/generationConfig（优先前者）白名单键透传
//     （thinking_summaries 布尔归一 "auto"/"none"）；
//  2. 源内 thinkingConfig（includeThoughts/thinkingLevel/thinkingBudget 映射）；
//  3. max_completion_tokens|max_tokens → max_output_tokens；
//  4. 统一 thinking 开关 + reasoning_effort；
//  5. tool_choice 内嵌映射（"required"→"any"；{function:{name}}→allowed_tools）。
func buildInteractionsGenerationConfig(opts *ai.ChatOptions) map[string]any {
	gc := map[string]any{}

	if native, ok := opts.OptionGet("generation_config").(map[string]any); ok {
		mergeInteractionsGenerationConfig(gc, native)
	} else if native, ok := opts.OptionGet("generationConfig").(map[string]any); ok {
		mergeInteractionsGenerationConfig(gc, native)
	}

	// thinkingConfig 源内映射
	if tc, ok := opts.OptionGet("thinkingConfig").(map[string]any); ok {
		applyThinkingConfig(gc, tc)
	} else if tc, ok := opts.OptionGet("generation_config").(map[string]any); ok {
		if inner, ok := tc["thinkingConfig"].(map[string]any); ok {
			applyThinkingConfig(gc, inner)
		}
	} else if native, ok := opts.OptionGet("generationConfig").(map[string]any); ok {
		if inner, ok := native["thinkingConfig"].(map[string]any); ok {
			applyThinkingConfig(gc, inner)
		}
	}

	// 统一 thinking 开关 + reasoning_effort（显式 thinkingConfig 优先不被覆盖）
	thinkingSwitch, hasThinking := opts.Raw["thinking"]
	effort := opts.Raw["reasoning_effort"]
	if hasThinking {
		if b, ok := thinkingSwitch.(bool); ok && !b {
			gc["thinking_summaries"] = "none"
		} else if ok {
			if _, has := gc["thinking_level"]; !has {
				gc["thinking_level"] = "medium"
			}
		}
	}
	if effort != nil {
		if s := effortLevel(effort); s != "" {
			gc["thinking_level"] = s
		}
	}
	if (effort != nil || (hasThinking && thinkingSwitch == true)) {
		if _, has := gc["thinking_summaries"]; !has {
			gc["thinking_summaries"] = "auto"
		}
	}

	// tool_choice 内嵌
	if tc := opts.OptionGet("tool_choice"); tc != nil {
		switch v := tc.(type) {
		case string:
			if v == "required" {
				gc["tool_choice"] = "any"
			} else {
				gc["tool_choice"] = v
			}
		case map[string]any:
			if fn, ok := v["function"].(map[string]any); ok {
				if name := dialectkit.GetStr(fn, "name"); name != "" {
					gc["tool_choice"] = map[string]any{
						"allowed_tools": map[string]any{"mode": "any", "tools": []any{name}},
					}
				}
			}
		}
	}
	return gc
}

// mergeInteractionsGenerationConfig 白名单键拷贝（布尔 thinking_summaries 归一；
// 源内 max_completion_tokens/max_tokens → max_output_tokens int 归一）。
func mergeInteractionsGenerationConfig(dst, src map[string]any) {
	for k, v := range src {
		switch k {
		case "max_completion_tokens", "max_tokens":
			dst["max_output_tokens"] = asInt(v)
			continue
		}
		if _, ok := generationConfigWhitelist[k]; !ok {
			continue
		}
		if k == "thinking_summaries" {
			if b, ok := v.(bool); ok {
				if b {
					v = "auto"
				} else {
					v = "none"
				}
			}
		}
		dst[k] = v
	}
}

// applyThinkingConfig thinkingConfig 源内映射：includeThoughts → summaries、
// thinkingLevel → level、thinkingBudget → 按预算映射 level。
func applyThinkingConfig(gc, tc map[string]any) {
	if v, ok := tc["includeThoughts"]; ok {
		if b, ok := v.(bool); ok {
			if b {
				gc["thinking_summaries"] = "auto"
			} else {
				gc["thinking_summaries"] = "none"
			}
		}
	}
	if s, ok := tc["thinkingLevel"].(string); ok && s != "" {
		gc["thinking_level"] = strings.ToLower(s)
	}
	if v, ok := tc["thinkingBudget"]; ok {
		if n, ok := asInt(v).(int); ok {
			switch {
			case n > 8192:
				gc["thinking_level"] = "high"
			case n > 2048:
				gc["thinking_level"] = "medium"
			default:
				gc["thinking_level"] = "low"
			}
		}
	}
}

// effortLevel reasoning_effort 匹配（含 minimal/min → minimal；low/medium/
// high|max）。
func effortLevel(v any) string {
	s := strings.ToLower(fmt.Sprint(v))
	switch {
	case strings.Contains(s, "minimal"), strings.Contains(s, "min"):
		return "minimal"
	case strings.Contains(s, "low"):
		return "low"
	case strings.Contains(s, "medium"):
		return "medium"
	case strings.Contains(s, "high"), strings.Contains(s, "max"):
		return "high"
	}
	return ""
}

// optionBoolOr option 布尔值（缺省/类型不符回退 def）。
func optionBoolOr(opts *ai.ChatOptions, key string, def bool) bool {
	if v := opts.OptionGet(key); v != nil {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}

// asInt 数字归一（字符串 "256" → int；float → int）。
func asInt(v any) any {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		if n == float64(int(n)) {
			return int(n)
		}
		return n
	case string:
		if i, err := strconv.Atoi(n); err == nil {
			return i
		}
	}
	return v
}

// schemaObjectOf schema 文本 → object（解析失败写原字符串）。
func schemaObjectOf(schema string) any {
	var m map[string]any
	if err := json.Unmarshal([]byte(schema), &m); err != nil || m == nil {
		return schema
	}
	return m
}

// callArgumentsOf 工具调用参数（ArgumentsStr 优先，空则序列化 Arguments）。
func callArgumentsOf(call *ai.ToolCall) any {
	if call.Arguments != nil {
		return call.Arguments
	}
	if call.ArgumentsStr != "" {
		return call.ArgumentsStr
	}
	return map[string]any{}
}
