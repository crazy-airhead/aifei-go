// openaistyle.go OpenAI 家族共享的帧消息解析与出站节点构建（对标 Java
// AbstractChatDialect 中被 openai/ollama/dashscope 复用的部分）。
package dialectkit

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai"
)

var (
	ThinkOpenTag    = "<think>"
	ThinkCloseTag   = "</think>"
	EmptyToolParams = map[string]any{"type": "object", "properties": map[string]any{}}
)

// ParseAssistantMessage OpenAI 风格 message/delta 节点 → 聚合助手消息（一帧一条）。
// 思考与正文同属这一轮模型输出；<think> 标签状态机处理无推理字段的流式思考。
// ollama（thinking→reasoning 预映射后）与 dashscope（快照归一后）共用。
func ParseAssistantMessage(acc *ai.Accumulator, node map[string]any) []*ai.AssistantMessage {
	if node == nil {
		return nil
	}

	contentBlocks := ParseAssistantContentBlocks(node)
	content := ProjectText(contentBlocks)
	var blocksForMsg []ai.ContentBlock
	if len(contentBlocks) > 1 || (len(contentBlocks) == 1 && contentBlocks[0].Kind() != "text") {
		blocksForMsg = contentBlocks
	}

	toolCalls := ParseToolCalls(acc, node["tool_calls"])
	searchResults := ParseSearchResults(node["search_results"])

	if len(toolCalls) > 0 {
		acc.SetInThinking(false)
	}

	var reasoningContent string
	if _, ok := node["reasoning_content"]; ok {
		reasoningContent = GetStr(node, "reasoning_content")
		acc.SetReasoningFieldName("reasoning_content")
	} else if _, ok := node["reasoning"]; ok {
		reasoningContent = GetStr(node, "reasoning")
		acc.SetReasoningFieldName("reasoning")
	}

	var pendingThinking, splitThinking string
	if reasoningContent != "" {
		acc.SetHasReasoningField(true)
		if acc.IsStream() {
			if content == "" {
				// 只有思考：内容通道承载思考增量，等待后续帧补正文
				content = reasoningContent
				acc.SetInThinking(true)
			} else {
				splitThinking = reasoningContent
				acc.SetInThinking(false)
			}
		} else {
			// 非流式双字段：思考单独存放
			pendingThinking = reasoningContent
		}
	} else if content != "" {
		if acc.HasReasoningField() {
			acc.SetInThinking(false)
		} else if acc.IsStream() {
			if strings.HasPrefix(content, ThinkOpenTag) {
				acc.SetInThinking(true)
				content = content[len(ThinkOpenTag):]
			} else if acc.InThinking() {
				if thinkEnd := strings.Index(content, ThinkCloseTag); thinkEnd >= 0 {
					acc.SetInThinking(false)
					splitThinking = content[:thinkEnd]
					content = content[thinkEnd+len(ThinkCloseTag):]
				}
			}
		}
	}

	if content != "" || pendingThinking != "" || splitThinking != "" ||
		len(toolCalls) > 0 || len(blocksForMsg) > 0 || len(searchResults) > 0 {
		textOut, thinkingOut := "", ""
		if acc.InThinking() {
			thinkingOut = content
		} else {
			textOut = content
			if splitThinking != "" {
				thinkingOut = splitThinking
			} else {
				thinkingOut = pendingThinking
			}
		}
		msg := ai.Snapshot(textOut, thinkingOut, toolCalls)
		msg.Blocks = blocksForMsg
		msg.SearchResults = searchResults
		return []*ai.AssistantMessage{msg}
	}
	return nil
}

// ParseAssistantContentBlocks content 字段（string | array）→ 内容块。
func ParseAssistantContentBlocks(node map[string]any) []ai.ContentBlock {
	var blocks []ai.ContentBlock
	switch c := node["content"].(type) {
	case string:
		if c != "" {
			blocks = append(blocks, ai.TextBlock{Text: c})
		}
	case []any:
		for _, item := range c {
			m, _ := item.(map[string]any)
			if m == nil {
				continue
			}
			switch GetStr(m, "type") {
			case "text":
				if text := GetStr(m, "text"); text != "" {
					blocks = append(blocks, ai.TextBlock{Text: text})
				}
			case "image_url":
				if u := GetMapStr(m, "image_url", "url"); u != "" {
					blocks = append(blocks, ImageBlockFromDataString(u))
				}
			case "audio_url":
				if u := GetMapStr(m, "audio_url", "url"); u != "" {
					blocks = append(blocks, AudioBlockFromDataString(u))
				}
			}
		}
	}
	return blocks
}

func ImageBlockFromDataString(u string) ai.ImageBlock {
	if strings.HasPrefix(u, "data:") {
		if b, mime := DecodeDataURL(u); mime != "" {
			return ai.ImageBlock{Data: b, Mime: mime}
		}
	}
	return ai.ImageBlock{URL: u}
}

func AudioBlockFromDataString(u string) ai.AudioBlock {
	if strings.HasPrefix(u, "data:") {
		if b, mime := DecodeDataURL(u); mime != "" {
			return ai.AudioBlock{Data: b, Mime: mime}
		}
	}
	return ai.AudioBlock{URL: u}
}

// ProjectText 块列表的文本投影。
func ProjectText(blocks []ai.ContentBlock) string {
	var sb strings.Builder
	for _, b := range blocks {
		if tb, ok := b.(ai.TextBlock); ok {
			sb.WriteString(tb.Text)
		}
	}
	return sb.String()
}

func ParseToolCalls(acc *ai.Accumulator, node any) []ai.ToolCall {
	arr, ok := node.([]any)
	if !ok {
		return nil
	}
	var out []ai.ToolCall
	for _, item := range arr {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		out = append(out, ParseToolCall(acc, m))
	}
	return out
}

// ParseToolCall 流式分片的 index 是聚合主键（id 仅首分片携带）；
// 无 index 的端点退回 id，再退回 lastToolCallId。
func ParseToolCall(acc *ai.Accumulator, n map[string]any) ai.ToolCall {
	callID := GetStr(n, "id")

	index := ""
	if idx := GetInt(n, "index"); idx >= 0 {
		index = "idx:" + strconv.FormatInt(idx, 10)
	}
	if callID != "" {
		acc.SetLastToolCallID(callID)
	}
	if index == "" {
		if callID != "" {
			index = callID
		} else {
			index = acc.LastToolCallID()
		}
	}

	name, argsStr := "", ""
	var argsMap map[string]any
	if fn, ok := n["function"].(map[string]any); ok {
		name = GetStr(fn, "name")
		switch a := fn["arguments"].(type) {
		case string:
			// 流式分片的 arguments 可能是不完整 JSON 片段，解析失败保留字符串形态
			argsStr = a
			var m map[string]any
			if json.Unmarshal([]byte(a), &m) == nil {
				argsMap = m
			}
		case map[string]any:
			argsMap = a
			if data, err := json.Marshal(a); err == nil {
				argsStr = string(data)
			}
		}
	}
	return ai.ToolCall{Index: index, ID: callID, Name: name, ArgumentsStr: argsStr, Arguments: argsMap}
}

// ParseSearchResults OpenAI 兼容/DashScope 风格搜索结果投影。
func ParseSearchResults(node any) []ai.SearchResult {
	arr, ok := node.([]any)
	if !ok || len(arr) == 0 {
		return nil
	}
	var out []ai.SearchResult
	for _, item := range arr {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		r := ai.SearchResult{
			Index: int(GetInt(m, "index")),
			ID:    GetStr(m, "id"),
			Title: GetStr(m, "title"),
			URL:   GetStr(m, "url"),
		}
		if s := GetStr(m, "snippet"); s != "" {
			r.Snippet = s
		} else {
			r.Snippet = GetStr(m, "summary")
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ParseUsage OpenAI 风格 usage 节点 → ai.Usage（total 缺省补和；reasoning_tokens
// 优先 DeepSeek think_tokens 兜底；cached_tokens 优先 prompt_cache_hit_tokens 兜底）。
func ParseUsage(u map[string]any) *ai.Usage {
	promptTokens := GetInt(u, "prompt_tokens")
	completionTokens := GetInt(u, "completion_tokens")
	totalTokens := promptTokens + completionTokens
	if _, has := u["total_tokens"]; has {
		totalTokens = GetInt(u, "total_tokens")
	}

	var thinkTokens int64
	if details, ok := u["completion_tokens_details"].(map[string]any); ok {
		if _, has := details["reasoning_tokens"]; has {
			thinkTokens = GetInt(details, "reasoning_tokens")
		}
	}
	if thinkTokens == 0 {
		if _, has := u["think_tokens"]; has {
			thinkTokens = GetInt(u, "think_tokens")
		}
	}

	var cacheRead, cacheCreation int64
	details, hasDetails := u["prompt_tokens_details"].(map[string]any)
	if hasDetails {
		if _, has := details["cached_tokens"]; has {
			cacheRead = GetInt(details, "cached_tokens")
		}
		if _, has := details["cache_write_tokens"]; has {
			cacheCreation = GetInt(details, "cache_write_tokens")
		}
	}
	if cacheRead == 0 {
		if !hasDetails {
			if _, has := u["prompt_cache_hit_tokens"]; has {
				cacheRead = GetInt(u, "prompt_cache_hit_tokens")
			}
		}
	}

	return &ai.Usage{
		PromptTokens:               promptTokens,
		ThinkTokens:                thinkTokens,
		CompletionTokens:           completionTokens,
		TotalTokens:                totalTokens,
		CacheCreationInputTokens:   cacheCreation,
		CacheReadInputTokens:       cacheRead,
		CacheCreation5mInputTokens: cacheCreation5m(details),
		CacheCreation1hInputTokens: cacheCreation1h(details),
	}
}

func cacheCreation5m(details map[string]any) int64 {
	if details == nil {
		return 0
	}
	if _, has := details["cache_creation_5m_tokens"]; !has {
		return 0
	}
	return GetInt(details, "cache_creation_5m_tokens")
}

func cacheCreation1h(details map[string]any) int64 {
	if details == nil {
		return 0
	}
	if _, has := details["cache_creation_1h_tokens"]; !has {
		return 0
	}
	return GetInt(details, "cache_creation_1h_tokens")
}

// BuildToolsNode OpenAI 形态 tools 节点（ollama 放根、dashscope 放 parameters 内）。
func BuildToolsNode(tools []ai.FunctionTool) []any {
	out := make([]any, 0, len(tools))
	for _, t := range tools {
		schema := map[string]any{}
		if s := t.InputSchema(); s != "" {
			var cand map[string]any
			if json.Unmarshal([]byte(s), &cand) == nil && cand != nil {
				schema = cand
			}
		}
		if schema == nil {
			schema = EmptyToolParams
		}
		fn := map[string]any{
			"name":        t.Name(),
			"description": ai.DescriptionAndMeta(t),
			"parameters":  schema,
		}
		if strict := t.Strict(); strict != nil {
			fn["strict"] = *strict // Chat Completions 的 strict 在 function 内层
		}
		out = append(out, map[string]any{"type": "function", "function": fn})
	}
	return out
}

// BuildOutboundToolCalls 类型化工具调用 → OpenAI 兼容 tool_calls（arguments 净化）。
func BuildOutboundToolCalls(calls []ai.ToolCall) []any {
	if len(calls) == 0 {
		return nil
	}
	out := make([]any, 0, len(calls))
	for _, c := range calls {
		item := map[string]any{"type": "function"}
		if c.ID != "" {
			item["id"] = c.ID
		}
		item["function"] = map[string]any{
			"name":      c.Name,
			"arguments": SanitizeCall(&c),
		}
		out = append(out, item)
	}
	return out
}

// ExtractJSONObject 字符串中嵌套的 JSON object 提取（对标 snack4 readLast +
// hasNestedJsonBlock 的组合语义）：整串可解析则原样；否则截取首个 '{' 到末个 '}'。
// 常见于 Ollama 工具参数以 "text {...}" 形态混发。
func ExtractJSONObject(s string) (map[string]any, bool) {
	if s == "" {
		return nil, false
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err == nil && m != nil {
		return m, true
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return nil, false
	}
	if err := json.Unmarshal([]byte(s[start:end+1]), &m); err == nil && m != nil {
		return m, true
	}
	return nil, false
}
