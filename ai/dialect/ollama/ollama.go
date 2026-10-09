// Package ollama 是 Ollama /api/chat 方言：本地无鉴权、媒体走侧车数组、
// 流尾以 done 帧收束（非 [DONE] 哨兵）、usage 只在 done 帧携带。
package ollama

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/internal/dialectkit"
)

// 方言本地控制项（只影响入站解析，不出站）。
const (
	// OptionToolArgumentsMode 工具参数帧模式："snapshot"（服务端每帧发累计快照，
	// 方言裁剪已交付前缀）|"delta"（默认，每帧为纯增量）。
	OptionToolArgumentsMode = "ollama_tool_arguments_mode"
	// OptionMediaMode 媒体帧模式："delta"（每帧媒体都发 MEDIA_DONE）|缺省（去重）。
	OptionMediaMode = "ollama_media_mode"
)

var schemaInstructionT = "\n\n## [IMPORTANT: OUTPUT FORMAT]\n" +
	"Format your response as a JSON object strictly following this schema:\n" +
	"<output_schema>\n%s\n</output_schema>\n" +
	"Output only the raw JSON, beginning with '{' and ending with '}'."

// Dialect Ollama /api/chat 方言。
type Dialect struct{}

func init() { ai.RegisterDialect(&Dialect{}, 20) }

func (d *Dialect) Name() string    { return "ollama" }
func (d *Dialect) IsDefault() bool { return false }

func (d *Dialect) Matched(cfg *ai.Config) bool {
	if cfg == nil {
		return false
	}
	standard := cfg.StandardOrProvider()
	if strings.EqualFold(standard, "ollama") {
		return true
	}
	return standard == "" && strings.HasSuffix(cfg.APIURL, "/api/chat")
}

// ---- 请求侧 ----

// BuildRequestJSON {model, messages, stream, keep_alive, prompt_cache_key,
// options 根级透传（thinking→think；本地控制项剔除）, tools(OpenAI 形态)}。
// 与 OpenAI 家族的差异：options 在根级（不嵌 options 对象）、无 /v1 前缀插入、
// 工具参数 arguments 以 JSON object 出站。
func (d *Dialect) BuildRequestJSON(cfg *ai.Config, opts *ai.ChatOptions, msgs []*ai.Message, isStream bool) ([]byte, string, error) {
	if opts == nil {
		opts = &ai.ChatOptions{}
	}

	root := map[string]any{}
	if cfg != nil && cfg.Model != "" {
		root["model"] = cfg.Model
	}

	messages := []any{}
	for _, m := range msgs {
		if d.isSkippableThinkingOnly(cfg, m) {
			continue
		}
		if node := d.buildMessageNode(m); node != nil {
			messages = append(messages, node)
		}
	}
	root["messages"] = messages

	root["stream"] = isStream

	// 模型驻留时间（keep_alive 决定 KV Cache 保留时长，默认 5 分钟可复用缓存）
	if keepAlive := opts.OptionGet("keep_alive"); keepAlive != nil {
		root["keep_alive"] = keepAlive
	} else if opts.CacheControl.Ttl != "" {
		root["keep_alive"] = opts.CacheControl.Ttl
	}
	if opts.CacheControl.PromptCacheKey != "" {
		root["prompt_cache_key"] = opts.CacheControl.PromptCacheKey
	}

	for k, v := range opts.Raw {
		switch k {
		case "thinking":
			// 统一 thinking(Boolean) → Ollama 原生 think；显式 think 优先且不被覆盖
			if b, ok := v.(bool); ok {
				if _, has := opts.Raw["think"]; !has {
					root["think"] = b
				}
			}
			continue
		case OptionToolArgumentsMode, OptionMediaMode:
			continue // 方言本地控制项不出站
		}
		root[k] = v
	}

	if len(opts.Tools) > 0 {
		root["tools"] = dialectkit.BuildToolsNode(opts.Tools)
	}

	body, err := json.Marshal(root)
	if err != nil {
		return nil, "", err
	}
	return body, buildAPIURL(cfg.APIURL), nil
}

// buildAPIURL 去 fragment；已带 /api/chat 原样；否则按尾斜杠补 api/chat。
// 与 OpenAI 补全的差异：不插入 /v1 前缀。
func buildAPIURL(apiURL string) string {
	if i := strings.Index(apiURL, "#"); i > 0 {
		apiURL = apiURL[:i]
	}
	if strings.HasSuffix(apiURL, "/api/chat") {
		return apiURL
	}
	if strings.HasSuffix(apiURL, "/") {
		return apiURL + "api/chat"
	}
	return apiURL + "/api/chat"
}

func (d *Dialect) PrepareOutputSchemaInstruction(schema string, sb *strings.Builder) {
	sb.WriteString(fmt.Sprintf(schemaInstructionT, schema))
}

func (d *Dialect) PrepareOutputFormatOptions(opts *ai.ChatOptions) {
	opts.OptionSet("response_format", map[string]any{"type": "json_object"})
}

// isSkippableThinkingOnly 纯思考消息仅当目标能把 thinking 重建为协议字段时保留
// （基类逻辑：OpenRouter/DeepSeek 目标回放，其余跳过）。
func (d *Dialect) isSkippableThinkingOnly(cfg *ai.Config, m *ai.Message) bool {
	am := dialectkit.AssistantOf(m)
	return am != nil && am.ThinkingOnly() && dialectkit.ResolveChatCompletionsThinkingField(cfg) == ""
}

func (d *Dialect) buildMessageNode(m *ai.Message) map[string]any {
	switch m.Role {
	case ai.RoleSystem:
		return map[string]any{"role": "system", "content": dialectkit.ContentText(m.Content)}
	case ai.RoleUser:
		node := map[string]any{"role": "user", "content": dialectkit.ContentText(m.Content)}
		appendMediaArrays(node, dialectkit.MessageBlocks(m.Content))
		return node
	case ai.RoleAssistant:
		return d.buildAssistantMessageNode(m)
	case ai.RoleTool:
		return buildToolMessageNode(m)
	}
	return nil
}

// buildAssistantMessageNode assistant 回传对齐 Ollama：content 字符串 +
// images/audios/videos 侧车（非 OpenAI content 数组）+ thinking + tool_calls
// （arguments 为 JSON object）。
func (d *Dialect) buildAssistantMessageNode(m *ai.Message) map[string]any {
	node := map[string]any{"role": "assistant"}

	am := dialectkit.AssistantOf(m)
	if am == nil {
		node["content"] = dialectkit.ContentText(m.Content)
		return node
	}

	if am.Text != "" {
		node["content"] = am.Text
	} else {
		node["content"] = ""
	}
	if len(am.Blocks) > 0 {
		appendMediaArrays(node, am.Blocks)
	}
	// Ollama 原生历史思考字段固定为 thinking，不采用源消息字段名
	if am.Thinking != "" {
		node["thinking"] = am.Thinking
	}
	if calls := buildOutboundToolCalls(am.ToolCalls); len(calls) > 0 {
		node["tool_calls"] = calls
	}
	return node
}

func buildToolMessageNode(m *ai.Message) map[string]any {
	node := map[string]any{"role": "tool"}
	t, _ := m.Content.(*ai.ToolMessage)
	if t == nil {
		node["content"] = dialectkit.ContentText(m.Content)
		return node
	}
	if t.Name != "" {
		node["name"] = t.Name
	}
	if t.CallID != "" {
		node["tool_call_id"] = t.CallID
	}
	var blocks []ai.ContentBlock
	if t.Result != nil {
		blocks = t.Result.Blocks
	}
	if len(blocks) == 0 {
		node["content"] = t.ResultText()
		return node
	}
	// 工具结果多模态：文本项入 content 数组，媒体走侧车（Ollama 形态）
	var contentArr []any
	media := map[string][]any{}
	for _, b := range blocks {
		if tb, ok := b.(ai.TextBlock); ok {
			if tb.Text != "" {
				contentArr = append(contentArr, map[string]any{"type": "text", "text": tb.Text})
			}
			continue
		}
		appendMediaItem(media, b)
	}
	if len(contentArr) > 0 {
		node["content"] = contentArr
	} else {
		node["content"] = t.ResultText()
	}
	for k, v := range media {
		if len(v) > 0 {
			node[k] = v
		}
	}
	return node
}

// buildOutboundToolCalls OpenAI 形态 tool_calls，但 arguments 为 JSON object
// （Ollama 原生不接受字符串参数）。
func buildOutboundToolCalls(calls []ai.ToolCall) []any {
	if len(calls) == 0 {
		return nil
	}
	out := make([]any, 0, len(calls))
	for _, c := range calls {
		item := map[string]any{"type": "function"}
		if c.ID != "" {
			item["id"] = c.ID
		}
		args := map[string]any{}
		_ = json.Unmarshal([]byte(dialectkit.SanitizeCall(&c)), &args)
		item["function"] = map[string]any{"name": c.Name, "arguments": args}
		out = append(out, item)
	}
	return out
}

// appendMediaArrays blocks → images/audios/videos 侧车数组（session 截断后的
// 空媒体跳过，避免写出空侧车）。
func appendMediaArrays(node map[string]any, blocks []ai.ContentBlock) {
	media := map[string][]any{}
	for _, b := range blocks {
		appendMediaItem(media, b)
	}
	for k, v := range media {
		if len(v) > 0 {
			node[k] = v
		}
	}
}

func appendMediaItem(media map[string][]any, b ai.ContentBlock) {
	url, data, metas, kind := mediaOf(b)
	if kind == "" || !dialectkit.IsMediaPlayable(url, data, metas) {
		return
	}
	value := blockDataString(url, data)
	if value == "" {
		return
	}
	switch kind {
	case "image":
		media["images"] = append(media["images"], value)
	case "audio":
		media["audios"] = append(media["audios"], value)
	case "video":
		media["videos"] = append(media["videos"], value)
	}
}

func mediaOf(b ai.ContentBlock) (url string, data []byte, metas map[string]any, kind string) {
	switch v := b.(type) {
	case ai.ImageBlock:
		return v.URL, v.Data, v.Meta, "image"
	case ai.AudioBlock:
		return v.URL, v.Data, v.Meta, "audio"
	case ai.VideoBlock:
		return v.URL, v.Data, v.Meta, "video"
	}
	return "", nil, nil, ""
}

// blockDataString URL 优先（可为 data: URL）；否则裸 base64（Ollama 侧车无前缀）。
func blockDataString(url string, data []byte) string {
	if url != "" {
		return url
	}
	if len(data) > 0 {
		return base64.StdEncoding.EncodeToString(data)
	}
	return ""
}

// BuildAssistantToolCallMessage 流式聚合的工具调用 → assistant 消息
// （content/thinking 取 acc 聚合；arguments 净化）。
func (d *Dialect) BuildAssistantToolCallMessage(acc *ai.Accumulator, builders map[string]*ai.ToolCallBuilder) *ai.Message {
	var calls []ai.ToolCall
	for _, key := range acc.ToolCallOrder() {
		b := builders[key]
		if b == nil {
			continue
		}
		calls = append(calls, ai.ToolCall{
			Index:        b.Index,
			ID:           b.ID,
			Name:         b.Name,
			ArgumentsStr: dialectkit.SanitizeArguments(b.Args(), b.Name),
		})
	}
	if len(calls) == 0 {
		return nil
	}
	msg := &ai.AssistantMessage{ToolCalls: calls}
	if m := acc.Message(); m != nil {
		msg.Text, msg.Thinking = m.Text, m.Thinking
	}
	return &ai.Message{Role: ai.RoleAssistant, Content: msg}
}

// BuildAssistantMessageByToolMessages returnDirect：工具结果合成最终 assistant 消息
// （基类默认实现，与 openai 方言一致）。
func (d *Dialect) BuildAssistantMessageByToolMessages(msg *ai.AssistantMessage, toolMsgs []*ai.Message) *ai.AssistantMessage {
	var buf strings.Builder
	var mergedBlocks []ai.ContentBlock
	for _, tm := range toolMsgs {
		t, _ := tm.Content.(*ai.ToolMessage)
		if t == nil {
			continue
		}
		if buf.Len() > 0 {
			buf.WriteByte('\n')
		}
		buf.WriteString(t.ResultText())
		if t.Result != nil {
			for _, b := range t.Result.Blocks {
				if _, isText := b.(ai.TextBlock); !isText {
					mergedBlocks = append(mergedBlocks, b)
				}
			}
		}
	}
	out := &ai.AssistantMessage{Text: buf.String(), Blocks: mergedBlocks}
	out.AddMetadata("reason", "tool")
	out.AddMetadata("source", msg.Text)
	return out
}

// ---- 响应侧 ----

// ParseResponseJSON 解析一帧（call 为整体 body；stream 为单个 SSE data）。
// Ollama 帧承载 message 节点（content/thinking/tool_calls）+ done 流尾。
func (d *Dialect) ParseResponseJSON(ctx *ai.StreamContext, body string) {
	acc := ctx.Acc()

	var root map[string]any
	if err := json.Unmarshal([]byte(body), &root); err != nil || root == nil {
		return
	}

	d.parseFrameNode(ctx, acc, root)

	if acc.Err() != nil {
		dialectkit.EmitError(ctx, acc.Err(), root)
	}
}

func (d *Dialect) parseFrameNode(ctx *ai.StreamContext, acc *ai.Accumulator, root map[string]any) {
	if errNode, has := root["error"]; has {
		acc.SetError(errors.New(errorMessage(errNode)))
		return
	}

	acc.SetModel(dialectkit.GetStr(root, "model"))
	acc.SetFinished(dialectkit.GetBool(root, "done"))
	doneReason := dialectkit.GetStr(root, "done_reason")

	msgNode, _ := root["message"].(map[string]any)
	d.preprocessToolCallIndexes(msgNode)
	snapshotSet := collectStructuredSnapshotIndexes(msgNode)

	frameOccurrences := map[string]int{}
	for _, msg := range d.ParseAssistantMessage(acc, msgNode) {
		if ctx.IsStream() {
			// Ollama 部分实现返回 arguments 累计快照；仅在快照形态下裁剪前缀
			eventMessage := d.normalizeToolCallArgumentDeltas(ctx, msg, snapshotSet)
			eventCarrier := withoutMediaBlocks(eventMessage)
			dialectkit.PublishMessageEvents(ctx, eventCarrier)
			if eventMessage != msg {
				// 参数事件只携带增量；终态工具载体仍保留服务端当前完整值
				acc.MergeTerminalMessage(withoutMediaBlocks(msg))
			}
			d.emitMediaDoneEvents(ctx, msg.Blocks, frameOccurrences)
		} else {
			acc.SetTerminalMessage(msg)
			d.emitMediaDoneEvents(ctx, msg.Blocks, frameOccurrences)
		}
	}

	if doneReason != "" {
		acc.SetFinishReason(doneReason)
	}

	if acc.IsFinished() {
		_, hasPrompt := root["prompt_eval_count"]
		_, hasEval := root["eval_count"]
		if hasPrompt || hasEval {
			promptTokens := dialectkit.GetInt(root, "prompt_eval_count")
			completionTokens := dialectkit.GetInt(root, "eval_count")
			u := &ai.Usage{
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				TotalTokens:      promptTokens + completionTokens,
			}
			if raw, err := json.Marshal(root); err == nil {
				u.Source = raw
			}
			acc.SetUsage(u)
		}

		if !ctx.IsStream() && !acc.TerminalPresent() {
			acc.SetTerminalMessage(&ai.AssistantMessage{})
		}
	}
}

func errorMessage(node any) string {
	if m, ok := node.(map[string]any); ok {
		return dialectkit.ExtractErrorMessage(m)
	}
	if s := fmt.Sprint(node); s != "" {
		return s
	}
	return "Unknown error"
}

// ParseAssistantMessage message 节点 → 聚合消息：thinking 字段预映射到通用
// reasoning 管线（无 reasoning/reasoning_content 时），媒体侧车并入消息块。
func (d *Dialect) ParseAssistantMessage(acc *ai.Accumulator, node map[string]any) []*ai.AssistantMessage {
	if node != nil {
		if _, hasR := node["reasoning"]; !hasR {
			if _, hasRC := node["reasoning_content"]; !hasRC {
				if thinking := dialectkit.GetStr(node, "thinking"); thinking != "" {
					node["reasoning"] = thinking
				}
			}
		}
	}

	messageList := dialectkit.ParseAssistantMessage(acc, node)
	mediaBlocks := parseMediaSidecars(node)
	if len(mediaBlocks) == 0 {
		return messageList
	}

	var result []*ai.AssistantMessage
	mediaMerged := false
	for _, msg := range messageList {
		// 非流式完整响应可在同一消息保留文本、思考、工具与媒体；
		// 流式下让媒体避开 thinking/tool_calls 事件载体。
		if !mediaMerged && (!acc.IsStream() || (msg.Thinking == "" && len(msg.ToolCalls) == 0)) {
			var blocks []ai.ContentBlock
			if msg.Text != "" {
				blocks = append(blocks, ai.TextBlock{Text: msg.Text})
			}
			for _, b := range msg.Blocks {
				if _, isText := b.(ai.TextBlock); !isText {
					blocks = append(blocks, b)
				}
			}
			blocks = append(blocks, mediaBlocks...)
			merged := ai.Snapshot(msg.Text, msg.Thinking, msg.ToolCalls)
			merged.Blocks = blocks
			merged.SearchResults = msg.SearchResults
			merged.Citations = msg.Citations
			merged.Metadata = msg.Metadata
			result = append(result, merged)
			mediaMerged = true
		} else {
			result = append(result, msg)
		}
	}

	// 若只有 thinking/tool 消息，补一条带媒体的空文本消息
	if !mediaMerged {
		result = append(result, &ai.AssistantMessage{Blocks: mediaBlocks})
	}
	return result
}

// parseMediaSidecars 解析 Ollama message 侧车媒体数组（值可为裸 base64、
// data: URL 或 http(s) URL）。
func parseMediaSidecars(node map[string]any) []ai.ContentBlock {
	var blocks []ai.ContentBlock
	for _, kv := range []struct {
		key  string
		kind string
	}{{"images", "image"}, {"audios", "audio"}, {"videos", "video"}} {
		arr, _ := node[kv.key].([]any)
		for _, item := range arr {
			value, ok := item.(string)
			if !ok || value == "" {
				continue
			}
			if b := mediaBlockFromValue(kv.kind, value); b != nil {
				blocks = append(blocks, b)
			}
		}
	}
	return blocks
}

func mediaBlockFromValue(kind, value string) ai.ContentBlock {
	newBlock := func(url string, data []byte) ai.ContentBlock {
		switch kind {
		case "image":
			return ai.ImageBlock{URL: url, Data: data}
		case "audio":
			return ai.AudioBlock{URL: url, Data: data}
		default:
			return ai.VideoBlock{URL: url, Data: data}
		}
	}
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "data:") {
		return newBlock(value, nil)
	}
	if raw, err := base64.StdEncoding.DecodeString(value); err == nil {
		return newBlock("", raw)
	}
	return nil
}

// ---- 工具调用身份与参数快照归一 ----

// preprocessToolCallIndexes Ollama 调用身份（顶层 index > function.index > 稳定 id
// > 帧内数组位置）规整为通用解析可读的数字 index 注入节点。函数名不是调用身份，
// 否则同名并行调用会错误合并。
func (d *Dialect) preprocessToolCallIndexes(msgNode map[string]any) {
	if msgNode == nil {
		return
	}
	items, _ := msgNode["tool_calls"].([]any)
	for position, item := range items {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		idx := readToolCallIndex(m["index"])
		if idx < 0 {
			if fn, ok := m["function"].(map[string]any); ok {
				idx = readToolCallIndex(fn["index"])
			}
		}
		if idx < 0 {
			if id := dialectkit.GetStr(m, "id"); id != "" {
				continue // 通用解析退回 id 聚合
			}
			idx = position
		}
		m["index"] = float64(idx)
	}
}

// collectStructuredSnapshotIndexes 记录 arguments 以 object 形态（结构化快照）
// 到达的调用（按规整后的身份键），供参数增量归一判定。
func collectStructuredSnapshotIndexes(msgNode map[string]any) map[string]bool {
	items, _ := msgNode["tool_calls"].([]any)
	if len(items) == 0 {
		return nil
	}
	out := map[string]bool{}
	for position, item := range items {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		fn, _ := m["function"].(map[string]any)
		if fn == nil {
			continue
		}
		if _, isObject := fn["arguments"].(map[string]any); !isObject {
			continue
		}
		idx := readToolCallIndex(m["index"])
		if idx < 0 {
			if fnIdx := readToolCallIndex(fn["index"]); fnIdx >= 0 {
				idx = fnIdx
			} else if id := dialectkit.GetStr(m, "id"); id != "" {
				out[id] = true
				continue
			} else {
				idx = position
			}
		}
		out["idx:"+fmt.Sprint(idx)] = true
	}
	return out
}

// readToolCallIndex index 值宽容读取（数字或 "idx:n"/"n" 字符串）→ n；无/坏值 -1。
func readToolCallIndex(v any) int {
	switch val := v.(type) {
	case float64:
		return int(val)
	case string:
		s := strings.TrimPrefix(val, "idx:")
		var n int
		if _, err := fmt.Sscanf(s, "%d", &n); err == nil && n >= 0 {
			return n
		}
	}
	return -1
}

// toolArgumentsState 参数增量归一状态：sticky 快照模式——首帧不裁，检测到
// （显式 snapshot 选项或结构化 object 参数）后按累计前缀裁剪。
type toolArgumentsState struct {
	accumulated  strings.Builder
	snapshotMode bool
}

func (s *toolArgumentsState) normalize(current string, structuredSnapshot bool) string {
	if current == "" {
		return current
	}
	delta := current
	if l := s.accumulated.Len(); l > 0 {
		if (s.snapshotMode || structuredSnapshot) && strings.HasPrefix(current, s.accumulated.String()) {
			s.snapshotMode = true
			delta = current[l:]
		}
	}
	s.accumulated.WriteString(delta)
	return delta
}

// normalizeToolCallArgumentDeltas 事件载体只携带参数增量；快照形态裁掉已交付前缀。
func (d *Dialect) normalizeToolCallArgumentDeltas(ctx *ai.StreamContext, msg *ai.AssistantMessage, snapshotSet map[string]bool) *ai.AssistantMessage {
	if len(msg.ToolCalls) == 0 {
		return msg
	}

	states, _ := ctx.AttrIfAbsent("__ollamaToolArgumentStates", func() any {
		return map[string]*toolArgumentsState{}
	}).(map[string]*toolArgumentsState)
	snapshotMode := false
	if opts := ctx.Options(); opts != nil {
		snapshotMode = strings.EqualFold(fmt.Sprint(opts.OptionGet(OptionToolArgumentsMode)), "snapshot")
	}

	deltaCalls := make([]ai.ToolCall, 0, len(msg.ToolCalls))
	changed := false
	for _, call := range msg.ToolCalls {
		stateKey := fmt.Sprintf("%d:%s", ctx.Step(), call.Index)
		state, ok := states[stateKey]
		if !ok {
			state = &toolArgumentsState{snapshotMode: snapshotMode}
			states[stateKey] = state
		}
		structured := snapshotSet != nil && snapshotSet[call.Index] ||
			(call.Index == "" && snapshotSet[call.ID])
		delta := state.normalize(call.ArgumentsStr, structured)
		if delta != call.ArgumentsStr {
			changed = true
		}
		c := call
		c.ArgumentsStr = delta
		deltaCalls = append(deltaCalls, c)
	}
	if !changed {
		return msg
	}
	out := ai.Snapshot(msg.Text, msg.Thinking, deltaCalls)
	out.Blocks = msg.Blocks
	out.SearchResults = msg.SearchResults
	out.Citations = msg.Citations
	out.Metadata = msg.Metadata
	return out
}

// withoutMediaBlocks 媒体块不进通用事件载体（另有 MEDIA_DONE 通道）。
func withoutMediaBlocks(msg *ai.AssistantMessage) *ai.AssistantMessage {
	if msg == nil || len(msg.Blocks) == 0 {
		return msg
	}
	out := ai.Snapshot(msg.Text, msg.Thinking, msg.ToolCalls)
	out.SearchResults = msg.SearchResults
	out.Citations = msg.Citations
	out.Metadata = msg.Metadata
	return out
}

// emitMediaDoneEvents 媒体块 → MEDIA_DONE。缺省按 类别+mime+内容+出现序 去重
// （同帧重复与跨帧重复都计数）；ollama_media_mode=delta 关走去重。
func (d *Dialect) emitMediaDoneEvents(ctx *ai.StreamContext, blocks []ai.ContentBlock, frameOccurrences map[string]int) {
	if len(blocks) == 0 {
		return
	}
	deltaMode := false
	if opts := ctx.Options(); opts != nil {
		deltaMode = strings.EqualFold(fmt.Sprint(opts.OptionGet(OptionMediaMode)), "delta")
	}
	var emitted map[string]struct{}
	if !deltaMode {
		emitted, _ = ctx.AttrIfAbsent("__ollamaEmittedMedia", func() any {
			return map[string]struct{}{}
		}).(map[string]struct{})
	}
	index := 0
	for _, b := range blocks {
		if b == nil {
			index++
			continue
		}
		if _, isText := b.(ai.TextBlock); isText {
			index++
			continue
		}
		baseKey := fmt.Sprintf("%T:%s:%v", b, b.MimeType(), b.Content())
		rank := frameOccurrences[baseKey]
		frameOccurrences[baseKey] = rank + 1
		if emitted == nil {
			d.emitMediaDone(ctx, b, index)
		} else {
			key := fmt.Sprintf("%s:%d", baseKey, rank)
			if _, ok := emitted[key]; !ok {
				emitted[key] = struct{}{}
				d.emitMediaDone(ctx, b, index)
			}
		}
		index++
	}
}

func (d *Dialect) emitMediaDone(ctx *ai.StreamContext, b ai.ContentBlock, index int) {
	ev := ctx.Event(ai.EventMediaDone)
	ev.Index = index
	ev.Block = b
	ctx.Emit(ev)
}
