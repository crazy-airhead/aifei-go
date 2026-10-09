// Package openai 是 OpenAI Chat Completions 方言（补位默认方言）。
// P0-d 交付 call 分支（整体 body 解析 → 终态载体）；流式累计快照归一
// （SnapshotState）在 P1-a 落地。
package openai

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/internal/dialectkit"
)

// OptionInstructionRole 本地方言指令角色策略：auto（默认）/ system / developer。
// 只控制 Chat Completions 出站角色，不透传给服务端。
const OptionInstructionRole = "openai_instruction_role"

const snapshotStateKey = "OpenaiStreamSnapshotState"

var (
	schemaInstructionT = "\n\n## [IMPORTANT: OUTPUT FORMAT]\n" +
		"Format your response as a JSON object strictly following this schema:\n" +
		"<output_schema>\n%s\n</output_schema>\n" +
		"Output only the raw JSON, beginning with '{' and ending with '}'."

	// 跨方言共享辅助（dialectkit，P1-b 抽取）的本包别名，保持既有调用点不变
	getStr              = dialectkit.GetStr
	getInt              = dialectkit.GetInt
	getArray            = dialectkit.GetArray
	getMapStr           = dialectkit.GetMapStr
	firstMetaStr        = dialectkit.FirstMetaStr
	decodeDataURL       = dialectkit.DecodeDataURL
	extractErrorMessage = dialectkit.ExtractErrorMessage
	contentText         = dialectkit.ContentText
	messageBlocks       = dialectkit.MessageBlocks
	assistantOf         = dialectkit.AssistantOf

	// OpenAI 家族帧消息解析与出站节点（dialectkit/openaistyle.go）
	parseToolCalls         = dialectkit.ParseToolCalls
	parseUsage             = dialectkit.ParseUsage
	buildToolsNode         = dialectkit.BuildToolsNode
	buildOutboundToolCalls = dialectkit.BuildOutboundToolCalls
)

// Dialect OpenAI chat/completions 方言。
type Dialect struct{}

func init() { ai.RegisterDialect(&Dialect{}, 100) }

func (d *Dialect) Name() string            { return "openai-completions" }
func (d *Dialect) IsDefault() bool         { return true }
func (d *Dialect) Matched(*ai.Config) bool { return false } // 补位方言不主动匹配

// ---- 请求侧 ----

// BuildRequestJSON 组装顺序对标 Java AbstractChatDialect.buildRequestJson +
// OpenaiChatDialect.buildRequestJson：model → messages（思考专用消息按目标可回放性
// 跳过）→ stream → prompt_cache_key → options 原样透传（本地方言选项除外）→
// stream_options.include_usage → tools。
func (d *Dialect) BuildRequestJSON(cfg *ai.Config, opts *ai.ChatOptions, msgs []*ai.Message, isStream bool) ([]byte, string, error) {
	if opts == nil {
		opts = &ai.ChatOptions{}
	}
	instructionRole := resolveInstructionRole(cfg, opts)

	root := map[string]any{}
	if cfg != nil && cfg.Model != "" {
		root["model"] = cfg.Model
	}

	messages := []any{}
	for _, m := range msgs {
		if isSkippableThinkingOnly(cfg, m) {
			continue
		}
		if node := d.buildMessageNode(cfg, m, instructionRole); node != nil {
			messages = append(messages, node)
		}
	}
	root["messages"] = messages

	root["stream"] = isStream
	if opts.CacheControl.PromptCacheKey != "" {
		root["prompt_cache_key"] = opts.CacheControl.PromptCacheKey
	}

	// options 原样透传；openai_instruction_role 是方言本地选项，不出站
	//（reasoning_effort/thinking 的模型族字段映射在 P1-b 方言矩阵展开）
	for k, v := range opts.Raw {
		if k == OptionInstructionRole {
			continue
		}
		root[k] = v
	}

	if isStream {
		if so, ok := root["stream_options"].(map[string]any); ok {
			if _, has := so["include_usage"]; !has {
				so["include_usage"] = true
			}
		} else {
			root["stream_options"] = map[string]any{"include_usage": true}
		}
	}

	if len(opts.Tools) > 0 {
		root["tools"] = buildToolsNode(opts.Tools)
	}

	body, err := json.Marshal(root)
	if err != nil {
		return nil, "", err
	}
	return body, BuildAPIURL(cfg.APIURL, "chat/completions"), nil
}

// PrepareOutputSchemaInstruction 输出 schema 指令（Java 逐字对标）。
func (d *Dialect) PrepareOutputSchemaInstruction(schema string, sb *strings.Builder) {
	sb.WriteString(fmt.Sprintf(schemaInstructionT, schema))
}

// PrepareOutputFormatOptions 输出格式选项：response_format=json_object。
func (d *Dialect) PrepareOutputFormatOptions(opts *ai.ChatOptions) {
	opts.OptionSet("response_format", map[string]any{"type": "json_object"})
}

// BuildAssistantToolCallMessage 流式聚合的工具调用 → assistant 消息（autoToolCall
// 历史回放与终态载体用）。与 Java 的差异：直接构造聚合消息，不经线协议 JSON 往返。
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
			ArgumentsStr: SanitizeArguments(b.Args(), b.Name),
		})
	}
	if len(calls) == 0 {
		return nil
	}
	return &ai.Message{Role: ai.RoleAssistant, Content: &ai.AssistantMessage{
		ToolCalls: calls,
	}}
}

// BuildAssistantMessageByToolMessages returnDirect：工具结果合成最终 assistant 消息。
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
		// 工具结果多模态块（截图等）合并；TextBlock 已拼入 content，跳过避免重复
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

// ---- 消息节点（对标 buildXxxMessageNodeDo）----

func (d *Dialect) buildMessageNode(cfg *ai.Config, m *ai.Message, instructionRole string) map[string]any {
	switch m.Role {
	case ai.RoleSystem:
		role := "system"
		if instructionRole == "developer" {
			role = "developer"
		}
		return map[string]any{"role": role, "content": contentText(m.Content)}
	case ai.RoleUser:
		return buildUserMessageNode(m)
	case ai.RoleAssistant:
		return buildAssistantMessageNode(cfg, m)
	case ai.RoleTool:
		return buildToolMessageNode(m)
	}
	return nil
}

func buildUserMessageNode(m *ai.Message) map[string]any {
	node := map[string]any{"role": "user"}
	blocks := messageBlocks(m.Content)
	if blocks == nil {
		node["content"] = contentText(m.Content)
		return node
	}
	var arr []any
	for _, b := range blocks {
		if item := openaiContentItem(b); item != nil {
			arr = append(arr, item)
		}
	}
	if len(arr) == 0 {
		// 媒体全不可播（会话截断后）：退回文本投影，避免空 content 数组
		node["content"] = contentText(m.Content)
		return node
	}
	node["content"] = arr
	return node
}

func buildAssistantMessageNode(cfg *ai.Config, m *ai.Message) map[string]any {
	am := assistantOf(m)
	node := map[string]any{"role": "assistant"}

	if len(am.Blocks) == 0 {
		switch {
		case am.Text != "":
			node["content"] = am.Text
		case len(am.ToolCalls) > 0:
			// OpenAI 规范：含 tool_calls 时 content 字段须存在（可为 null）
			node["content"] = nil
		default:
			// 纯思考轮无正文无调用：写空串占位，避免 {"role":"assistant"} 空消息毒化会话
			node["content"] = ""
		}
	} else {
		var arr []any
		var audioSidecarID string
		for _, b := range am.Blocks {
			if tb, ok := b.(ai.TextBlock); ok {
				if text := ai.StripThinkTags(tb.Text); text != "" {
					arr = append(arr, map[string]any{"type": "text", "text": text})
				}
				continue
			}
			if ab, ok := b.(ai.AudioBlock); ok {
				// 多轮音频优先侧车 audio.id（消息级字段，不在 content 数组内）
				if audioSidecarID == "" {
					audioSidecarID = firstMetaStr(ab.Metas(), "audio_id", "id")
				}
				if audioSidecarID != "" {
					continue
				}
			}
			if item := openaiContentItem(b); item != nil {
				arr = append(arr, item)
			}
		}
		if audioSidecarID != "" {
			node["audio"] = map[string]any{"id": audioSidecarID}
		}
		if len(arr) == 0 && am.Text != "" {
			node["content"] = am.Text
		} else {
			node["content"] = arr
		}
	}

	if am.Thinking != "" {
		if field := resolveAssistantThinkingField(cfg); field != "" {
			node[field] = am.Thinking
		}
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
		node["content"] = contentText(m.Content)
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
	} else {
		var arr []any
		for _, b := range blocks {
			if item := openaiContentItem(b); item != nil {
				arr = append(arr, item)
			}
		}
		if len(arr) == 0 {
			node["content"] = t.ResultText()
		} else {
			node["content"] = arr
		}
	}
	return node
}

// openaiContentItem 内容块 → OpenAI 兼容 content item；空/截断媒体返回 nil。
func openaiContentItem(b ai.ContentBlock) map[string]any {
	if b == nil {
		return nil
	}
	switch blk := b.(type) {
	case ai.TextBlock:
		if blk.Text != "" {
			return map[string]any{"type": "text", "text": blk.Text}
		}
		return nil
	case ai.ImageBlock:
		if data := mediaDataString(blk.URL, blk.Data, "image", blk.Mime); data != "" {
			return map[string]any{"type": "image_url", "image_url": map[string]any{"url": data}}
		}
	case ai.AudioBlock:
		// 多轮音频优先侧车 audio.id；无 id 降级 audio_url
		if id := firstMetaStr(blk.Metas(), "audio_id", "id"); id != "" {
			return map[string]any{"type": "audio", "audio": map[string]any{"id": id}}
		}
		if data := mediaDataString(blk.URL, blk.Data, "audio", blk.Mime); data != "" && !strings.HasPrefix(data, "audio://") {
			return map[string]any{"type": "audio_url", "audio_url": map[string]any{"url": data}}
		}
	case ai.VideoBlock:
		if data := mediaDataString(blk.URL, blk.Data, "video", blk.Mime); data != "" {
			return map[string]any{"type": "video_url", "video_url": map[string]any{"url": data}}
		}
	}
	return nil
}

// mediaDataString URL 优先；否则 data:base64。可播性（含侧车 id）由调用方判断。
var (
	mediaDataString = dialectkit.MediaDataString
	isMediaPlayable = dialectkit.IsMediaPlayable
)

// resolveInstructionRole 指令角色策略：显式 system/developer；auto 按模型族
// （o1 及后续推理族用 developer；o1-preview/o1-mini 早期模型保持 system）。
func resolveInstructionRole(cfg *ai.Config, opts *ai.ChatOptions) string {
	configured := opts.OptionGet(OptionInstructionRole)
	policy := "auto"
	if configured != nil {
		policy = strings.ToLower(strings.TrimSpace(fmt.Sprint(configured)))
	}
	switch policy {
	case "system", "developer":
		return policy
	case "auto":
	default:
		panic(OptionInstructionRole + " must be one of: auto, system, developer")
	}
	if cfg == nil {
		return "system"
	}
	if prefersDeveloperRole(cfg.Model) {
		return "developer"
	}
	return "system"
}

func prefersDeveloperRole(model string) bool {
	if model == "" {
		return false
	}
	modelName := strings.ToLower(strings.TrimSpace(model))
	// o1-preview / o1-mini 早期模型：官方 SDK 仍用 system
	if IsModelFamily(modelName, "o1-preview") || IsModelFamily(modelName, "o1-mini") {
		return false
	}
	return isReasoningCapableModel(modelName)
}

func isReasoningCapableModel(model string) bool {
	return IsModelFamily(model, "gpt-5") || IsModelFamily(model, "gpt-6") ||
		IsModelFamily(model, "o1") || IsModelFamily(model, "o3") || IsModelFamily(model, "o4")
}

// resolveAssistantThinkingField 历史思考回放字段（按目标端点定，未知目标不回放）：
// OpenRouter → reasoning；DeepSeek 系 → reasoning_content；其余 null。
func resolveAssistantThinkingField(cfg *ai.Config) string {
	if cfg == nil {
		return ""
	}
	provider := strings.ToLower(cfg.Provider)
	apiURL := strings.ToLower(cfg.APIURL)
	if strings.Contains(provider, "openrouter") || strings.Contains(apiURL, "openrouter") {
		return "reasoning"
	}
	model := strings.ToLower(cfg.Model)
	if strings.Contains(model, "deepseek") || strings.Contains(provider, "deepseek") ||
		strings.Contains(apiURL, "deepseek") {
		return "reasoning_content"
	}
	return ""
}

// isSkippableThinkingOnly 纯思考消息仅当目标能把 thinking 重建为协议字段时保留。
func isSkippableThinkingOnly(cfg *ai.Config, m *ai.Message) bool {
	am := assistantOf(m)
	return am != nil && am.ThinkingOnly() && resolveAssistantThinkingField(cfg) == ""
}

// ---- 响应侧 ----

// ParseResponseJSON 解析一帧（call 为整体 body；stream 为单个 SSE data）。
// 单帧损坏（网关截断 JSON）跳过不阻断流；「整个响应体都不是模型流」由上层守卫兜底。
func (d *Dialect) ParseResponseJSON(ctx *ai.StreamContext, body string) {
	acc := ctx.Acc()

	if body == "[DONE]" { // 哨兵帧
		ctx.AttrRemove(snapshotStateKey)
		if !acc.IsFinished() {
			acc.SetFinished(true)
		}
		return
	}

	// 部分中转直接输出 "error xxx" 纯文本（非 JSON）
	if strings.HasPrefix(body, "error ") {
		acc.SetError(errors.New(body))
		emitError(ctx, acc, nil)
		return
	}

	var root map[string]any
	if err := json.Unmarshal([]byte(body), &root); err != nil || root == nil {
		return
	}

	d.parseFrameNode(ctx, acc, root)
	emitRefusalEvents(ctx, root)
	if acc.Err() != nil {
		emitError(ctx, acc, root)
	}
}

func (d *Dialect) parseFrameNode(ctx *ai.StreamContext, acc *ai.Accumulator, root map[string]any) {
	// 顶层错误形态（object=error 或官方 {error:{message,type,code}}）
	if getStr(root, "object") == "error" {
		if _, has := root["error"]; !has {
			acc.SetError(errors.New(extractErrorMessage(root["message"])))
			return
		}
	}
	if errNode, has := root["error"]; has {
		acc.SetError(errors.New(extractErrorMessage(errNode)))
		return
	}

	acc.SetModel(getStr(root, "model"))
	ctx.SetProviderResponseID(getStr(root, "id")) // chatcmpl-xxx，各 chunk 一致

	if choices, ok := root["choices"].([]any); ok {
		for _, c := range choices {
			choice, _ := c.(map[string]any)
			if choice == nil {
				continue
			}
			finishReason := getStr(choice, "finish_reason")

			var node map[string]any
			if ctx.IsStream() { // object=chat.completion.chunk
				node, _ = choice["delta"].(map[string]any)
			} else { // object=chat.completion
				node, _ = choice["message"].(map[string]any)
			}
			node = normalizeLegacyFunctionCall(acc, node)

			for _, msg := range d.ParseAssistantMessage(acc, node) {
				if ctx.IsStream() {
					publishAssistantMessageEvents(ctx, msg)
					// publish 只带增量：用 builder 完整累计恢复终态 ToolCall，
					// 避免部分分片覆盖最终参数
					restoreTerminalToolCallArguments(acc, msg)
				} else {
					acc.SetTerminalMessage(msg)
				}
			}

			if finishReason != "" {
				acc.SetFinished(true)
				acc.SetFinishReason(finishReason)
			}

			break // 单结果模型：只消费首个 choice，避免多候选拼接
		}
	}

	if !ctx.IsStream() {
		acc.SetFinished(true)
		if !acc.TerminalPresent() {
			// 非流式空结果仍需明确终态
			acc.SetTerminalMessage(ai.Snapshot("", "", nil))
		}
	}

	if u, ok := root["usage"].(map[string]any); ok {
		acc.SetUsage(parseUsage(u))
	}
}

// ParseAssistantMessage 厂商 message/delta 节点 → 聚合助手消息（dialectkit
// OpenAI 家族共享实现：<think> 标签状态机处理无推理字段的流式思考）。
func (d *Dialect) ParseAssistantMessage(acc *ai.Accumulator, node map[string]any) []*ai.AssistantMessage {
	return dialectkit.ParseAssistantMessage(acc, node)
}

// publishAssistantMessageEvents 帧消息 → 事件通道（dialectkit 共享管线）。
var (
	publishAssistantMessageEvents = dialectkit.PublishMessageEvents
	emitToolCallEvent             = dialectkit.EmitToolCallEvent
	toolCallEventIndex            = dialectkit.ToolCallIndex
	toolCallEventKey              = dialectkit.ToolCallEventKey
	parseAssistantContentBlocks   = dialectkit.ParseAssistantContentBlocks
	parseSearchResults            = dialectkit.ParseSearchResults
	projectText                   = dialectkit.ProjectText
)

// restoreTerminalToolCallArguments 用 builder 完整累计参数恢复终态 ToolCall。
func restoreTerminalToolCallArguments(acc *ai.Accumulator, msg *ai.AssistantMessage) {
	if msg == nil || len(msg.ToolCalls) == 0 {
		return
	}
	builders := acc.ToolCallBuilders()
	var terminal []ai.ToolCall
	for _, call := range msg.ToolCalls {
		if call.Index == "" {
			continue
		}
		b := builders[call.Index]
		if b == nil || b.Args() == "" {
			continue
		}
		terminal = append(terminal, ai.ToolCall{
			Index:        b.Index,
			ID:           b.ID,
			Name:         b.Name,
			ArgumentsStr: b.Args(),
			Arguments:    call.Arguments,
		})
	}
	if len(terminal) > 0 {
		acc.MergeTerminalMessage(&ai.AssistantMessage{ToolCalls: terminal})
	}
}

// normalizeLegacyFunctionCall 旧版 function_call → tool_calls 形态（兼容网关）。
func normalizeLegacyFunctionCall(acc *ai.Accumulator, message map[string]any) map[string]any {
	if message == nil {
		return nil
	}
	if _, has := message["tool_calls"]; has {
		return message
	}
	fn, ok := message["function_call"].(map[string]any)
	if !ok {
		return message
	}
	id := getStr(fn, "id")
	if id == "" {
		id = acc.LastToolCallID()
	}
	if id == "" {
		id = "legacy_function_call"
	}
	message["tool_calls"] = []any{map[string]any{
		"id": id, "index": 0, "type": "function", "function": fn,
	}}
	return message
}

// parseUsage usage 节点 → ai.Usage（dialectkit OpenAI 家族共享实现）。

func emitError(ctx *ai.StreamContext, acc *ai.Accumulator, raw map[string]any) {
	ev := ctx.Event(ai.EventError)
	ev.RawType = "error"
	ev.Err = acc.Err()
	if raw != nil {
		if data, err := json.Marshal(raw); err == nil {
			ev.Raw = data
		}
	}
	ctx.Emit(ev)
}

func emitRefusalEvents(ctx *ai.StreamContext, root map[string]any) {
	choices, ok := root["choices"].([]any)
	if !ok {
		return
	}
	rawType := getStr(root, "object")
	if rawType == "" {
		rawType = "chat.completion.chunk"
	}
	for _, c := range choices {
		choice, _ := c.(map[string]any)
		if choice == nil {
			continue
		}
		message, _ := choice["delta"].(map[string]any)
		if message == nil {
			message, _ = choice["message"].(map[string]any)
		}
		if message == nil {
			continue
		}
		if refusal := getStr(message, "refusal"); refusal != "" {
			ev := ctx.Event(ai.EventRefusalDelta)
			ev.RawType = rawType
			ev.Text = refusal
			ctx.Emit(ev)
		}
	}
}

// ---- 响应节点小工具：parseAssistantContentBlocks / parseToolCalls / parseSearchResults
// ---- / projectText 等已抽取 dialectkit（顶部 var 别名）----
