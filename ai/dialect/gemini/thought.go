package gemini

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/internal/dialectkit"
)

// 协议状态标识（thoughtSignature 回放命名空间）。
const (
	generateContentProtocolID = "google.generate-content"
	interactionsProtocolID    = "google.interactions"
	protocolStateVersion      = 1
)

// cleanThoughtHeadingMaxLength 清理思考内容时，仅处理不超过此长度的整行 "**...**"
// 包裹（标题型），避免误删正常加粗正文。
const cleanThoughtHeadingMaxLength = 30

// createSignatureState 为终态聚合创建签名协议状态（语义摘要由消息物化边界补绑）。
func createSignatureState(call *ai.ToolCall, index int, signature string) *ai.ProtocolState {
	if call == nil || signature == "" {
		return nil
	}
	return &ai.ProtocolState{
		Version: protocolStateVersion,
		Data: map[string]any{"thoughtSignatures": map[string]any{
			callKey(call, index): signature,
		}},
	}
}

// resolveSignature 按语义摘要校验后回放签名：版本不符、摘要缺失或消息语义已变
// 都拒绝（防止回放陈旧快照）。Go 端无 legacy ToolCall.thoughtSignature 字段——
// 该形态只存在于 Java 旧版本会话数据。
func resolveSignature(m *ai.AssistantMessage, protocol string, call *ai.ToolCall, index int) string {
	if m == nil || call == nil || m.ProtocolStates == nil {
		return ""
	}
	v, ok := m.ProtocolStates[protocol]
	if !ok {
		return ""
	}
	state := protocolStateOf(v)
	if state == nil || state.Version != protocolStateVersion || state.SemanticHash == "" ||
		state.SemanticHash != ai.SemanticHash(m) {
		return ""
	}
	signatures, _ := state.Data["thoughtSignatures"].(map[string]any)
	if signatures == nil {
		return ""
	}
	if sig, ok := signatures[callKey(call, index)].(string); ok {
		return sig
	}
	return ""
}

// protocolStateOf 状态值宽容归一（*ai.ProtocolState | 反序列化 map 形态）。
func protocolStateOf(v any) *ai.ProtocolState {
	switch s := v.(type) {
	case *ai.ProtocolState:
		return s
	case ai.ProtocolState:
		return &s
	case map[string]any:
		data, err := json.Marshal(s)
		if err != nil {
			return nil
		}
		st := &ai.ProtocolState{}
		if json.Unmarshal(data, st) != nil {
			return nil
		}
		return st
	}
	return nil
}

// callKey 调用在签名表中的稳定身份（id > index > 数组位置）。
func callKey(call *ai.ToolCall, index int) string {
	if call.ID != "" {
		return "id:" + call.ID
	}
	if call.Index != "" {
		return "index:" + call.Index
	}
	return fmt.Sprintf("position:%d", index)
}

// ---- 思考内容解析（candidates[].content → 聚合消息）----

// parseThoughtContent parts → 聚合消息：functionCall（累计快照语义 + name#n 并行
// 去冲突 + 续帧恢复）、thought 思考分片（in_thinking 边界）、文本与媒体。
func parseThoughtContent(acc *ai.Accumulator, oContent map[string]any) []*ai.AssistantMessage {
	var messageList []*ai.AssistantMessage
	if oContent == nil {
		return messageList
	}

	parts := dialectkit.GetArray(oContent, "parts")
	if parts == nil {
		if p, has := oContent["part"]; has {
			parts = []any{p}
		}
	}
	if parts == nil {
		return messageList
	}

	hasThoughtPart := false
	hasNormalPart := false
	hasMediaPart := false

	var toolCalls []ai.ToolCall
	var mediaBlocks []ai.ContentBlock
	var orderedBlocks []ai.ContentBlock
	var thoughtTextAll strings.Builder
	var normalTextAll strings.Builder
	// 同一 chunk 内同名函数并行调用去冲突：首个用 name，后续用 name#n 作为流式聚合 index
	nameCount := map[string]int{}
	firstToolCallSignature := ""

	for _, item := range parts {
		part, _ := item.(map[string]any)
		if part == nil {
			continue
		}
		isThought := dialectkit.GetBool(part, "thought")

		functionCall := dialectkit.GetNode(part, "functionCall")
		if functionCall == nil {
			functionCall = dialectkit.GetNode(part, "function_call")
		}

		if functionCall != nil {
			functionName := dialectkit.GetStr(functionCall, "name")
			// 续帧兼容：OpenAI 兼容网关流式转 Gemini 时会把 functionCall 分帧发送
			//（帧1 带 name/空 args，帧2 带 args/空 name）。name 为空且已有调用上下文时，
			// 从 lastToolCallId 恢复函数名，避免生成 name 为空的无效 ToolCall。
			if functionName == "" && acc.LastToolCallID() != "" {
				functionName = acc.LastToolCallID()
			}
			// Gemini 3+：functionCall 携带唯一调用 id；服务端不返回 id 时不伪造——
			// 完全按 Gemini 2.5 的 name 关联方式回传，避免网关关联失败。
			callID := ""
			if id := dialectkit.GetStr(functionCall, "id"); id != "" {
				callID = id
			} else {
				callID = dialectkit.GetStr(functionCall, "call_id")
			}
			argsNode := functionCall["args"]
			if argsNode == nil {
				argsNode = functionCall["arguments"]
			}
			if s, isStr := argsNode.(string); isStr {
				var parsed map[string]any
				if json.Unmarshal([]byte(s), &parsed) == nil && parsed != nil {
					argsNode = parsed
				}
			}
			// 解析出口净化：仅 object 形态的 args 采纳；字符串（可能内含截断 JSON）等
			// 一律归一为空对象，避免非法 JSON 进入会话历史后毒化后续请求
			argsJSON := ""
			var argsMap map[string]any
			if m, isObj := argsNode.(map[string]any); isObj {
				argsMap = m
				if len(m) > 0 || !acc.IsStream() {
					if data, err := json.Marshal(m); err == nil {
						argsJSON = string(data)
					}
				}
			}
			if argsJSON == "" && !acc.IsStream() {
				argsJSON = "{}"
			}

			callIndex := ""
			if functionName != "" {
				// 跨 chunk 同名（同一 functionCall 续传）仍用 name 聚合；
				// 同 chunk 并行同名（两个 getWeather 同时调用）用 name#n 区分
				nameCount[functionName]++
				seen := nameCount[functionName]
				if seen == 1 {
					callIndex = functionName
				} else {
					callIndex = fmt.Sprintf("%s#%d", functionName, seen-1)
				}
			} else {
				callIndex = acc.LastToolCallID()
			}
			if functionName != "" {
				acc.SetLastToolCallID(functionName)
			}
			toolCalls = append(toolCalls, ai.ToolCall{
				Index: callIndex, ID: callID, Name: functionName,
				ArgumentsStr: argsJSON, Arguments: argsMap,
			})

			// 仅第一个 functionCall part 携带 thoughtSignature（并行调用时后续 part 没有）
			if len(toolCalls) == 1 {
				signature := dialectkit.GetStr(part, "thoughtSignature")
				if signature == "" {
					signature = dialectkit.GetStr(part, "thought_signature")
				}
				if signature != "" {
					firstToolCallSignature = signature
					acc.SetThinkingSignature(signature)
				}
			}
		} else if isThought {
			hasThoughtPart = true
			thoughtTextAll.WriteString(dialectkit.GetStr(part, "text"))
		} else if _, hasText := part["text"]; hasText {
			hasNormalPart = true
			partText := dialectkit.GetStr(part, "text")
			if partText != "" {
				normalTextAll.WriteString(partText)
				orderedBlocks = append(orderedBlocks, ai.TextBlock{Text: partText})
			}
		} else if media := parseMediaPart(part); media != nil {
			mediaBlocks = append(mediaBlocks, media)
			orderedBlocks = append(orderedBlocks, media)
			hasMediaPart = true
		}
	}

	if len(toolCalls) > 0 {
		if acc.InThinking() && acc.IsStream() {
			messageList = append(messageList, &ai.AssistantMessage{})
		}
		acc.SetInThinking(false)

		msg := ai.Snapshot(normalTextAll.String(), thoughtTextAll.String(), toolCalls)
		if len(orderedBlocks) > 0 {
			msg.Blocks = append([]ai.ContentBlock(nil), orderedBlocks...)
		}
		if state := createSignatureState(&toolCalls[0], 0, firstToolCallSignature); state != nil {
			msg.ProtocolStates = map[string]any{generateContentProtocolID: state}
			// 摘要在消息构造边界绑定（对标 Java snapshot 内建绑定）：emitStream
			// 的 resolveSignature 只认可与消息语义一致的已绑定状态
			ai.BindProtocolStateHashes(msg)
		}
		messageList = append(messageList, msg)
		return messageList
	}

	if acc.IsStream() {
		switch {
		case hasThoughtPart && !hasNormalPart && !hasMediaPart:
			// 思考独占帧：进边界后逐 part 投影思考增量
			if !acc.InThinking() {
				messageList = append(messageList, &ai.AssistantMessage{})
				acc.SetInThinking(true)
			}
			for _, item := range parts {
				part, _ := item.(map[string]any)
				if part == nil || !dialectkit.GetBool(part, "thought") {
					continue
				}
				if text := dialectkit.GetStr(part, "text"); text != "" {
					messageList = append(messageList, &ai.AssistantMessage{Thinking: text})
				}
			}
		case !hasThoughtPart && (hasNormalPart || hasMediaPart):
			// 正文独占帧：出边界；有媒体时合并为单条消息（文本先 delta 再整段会重复）
			if acc.InThinking() {
				messageList = append(messageList, &ai.AssistantMessage{})
				acc.SetInThinking(false)
			}
			var normalContent strings.Builder
			for _, item := range parts {
				part, _ := item.(map[string]any)
				if part == nil || dialectkit.GetBool(part, "thought") {
					continue
				}
				if _, hasText := part["text"]; !hasText {
					continue
				}
				if text := dialectkit.GetStr(part, "text"); text != "" {
					normalContent.WriteString(text)
					if len(mediaBlocks) == 0 {
						messageList = append(messageList, &ai.AssistantMessage{Text: text})
					}
				}
			}
			if len(mediaBlocks) > 0 {
				messageList = append(messageList, mediaMessage(normalContent.String(), mediaBlocks))
			}
		case hasThoughtPart && (hasNormalPart || hasMediaPart):
			// 混合帧：先思考增量，空消息出边界，再正文/媒体
			if !acc.InThinking() {
				messageList = append(messageList, &ai.AssistantMessage{})
			}
			for _, item := range parts {
				part, _ := item.(map[string]any)
				if part == nil || !dialectkit.GetBool(part, "thought") {
					continue
				}
				if text := dialectkit.GetStr(part, "text"); text != "" {
					messageList = append(messageList, &ai.AssistantMessage{Thinking: text})
				}
			}
			messageList = append(messageList, &ai.AssistantMessage{})
			acc.SetInThinking(false)

			var normalContent strings.Builder
			for _, item := range parts {
				part, _ := item.(map[string]any)
				if part == nil || dialectkit.GetBool(part, "thought") {
					continue
				}
				if _, hasText := part["text"]; !hasText {
					continue
				}
				if text := dialectkit.GetStr(part, "text"); text != "" {
					normalContent.WriteString(text)
					if len(mediaBlocks) == 0 {
						messageList = append(messageList, &ai.AssistantMessage{Text: text})
					}
				}
			}
			if len(mediaBlocks) > 0 {
				messageList = append(messageList, mediaMessage(normalContent.String(), mediaBlocks))
			}
		}
	} else {
		// 非流式：思考与正文各自拼接为单条消息（思考去标题型加粗包裹）
		var thoughtContent strings.Builder
		var normalContent strings.Builder
		for _, item := range parts {
			part, _ := item.(map[string]any)
			if part == nil {
				continue
			}
			if _, hasText := part["text"]; !hasText {
				continue
			}
			text := dialectkit.GetStr(part, "text")
			if text == "" {
				continue
			}
			if dialectkit.GetBool(part, "thought") {
				thoughtContent.WriteString(text)
			} else {
				normalContent.WriteString(text)
			}
		}

		var blocksForMsg []ai.ContentBlock
		if len(mediaBlocks) > 0 {
			if normalContent.Len() > 0 {
				blocksForMsg = append(blocksForMsg, ai.TextBlock{Text: normalContent.String()})
			}
			blocksForMsg = append(blocksForMsg, mediaBlocks...)
		}

		thinking := cleanThoughtContent(thoughtContent.String())
		switch {
		case thoughtContent.Len() > 0 && normalContent.Len() > 0:
			messageList = append(messageList, &ai.AssistantMessage{
				Text: normalContent.String(), Thinking: thinking, Blocks: blocksForMsg,
			})
		case thoughtContent.Len() > 0:
			messageList = append(messageList, &ai.AssistantMessage{Thinking: thinking, Blocks: blocksForMsg})
		case normalContent.Len() > 0 || blocksForMsg != nil:
			messageList = append(messageList, &ai.AssistantMessage{Text: normalContent.String(), Blocks: blocksForMsg})
		}
	}

	return messageList
}

// mediaMessage 文本 + 媒体合并为单条消息。
func mediaMessage(text string, mediaBlocks []ai.ContentBlock) *ai.AssistantMessage {
	var blocks []ai.ContentBlock
	if text != "" {
		blocks = append(blocks, ai.TextBlock{Text: text})
	}
	blocks = append(blocks, mediaBlocks...)
	return &ai.AssistantMessage{Text: text, Blocks: blocks}
}

// parseMediaPart Gemini part 媒体（inline_data/file_data，camel/snake 双形态）。
func parseMediaPart(part map[string]any) ai.ContentBlock {
	if part == nil {
		return nil
	}
	inline := dialectkit.GetNode(part, "inline_data")
	if inline == nil {
		inline = dialectkit.GetNode(part, "inlineData")
	}
	if inline != nil {
		mime := dialectkit.GetStr(inline, "mime_type")
		if mime == "" {
			mime = dialectkit.GetStr(inline, "mimeType")
		}
		data := dialectkit.GetStr(inline, "data")
		return createMediaByMime(mime, "", data)
	}

	fileData := dialectkit.GetNode(part, "file_data")
	if fileData == nil {
		fileData = dialectkit.GetNode(part, "fileData")
	}
	if fileData != nil {
		mime := dialectkit.GetStr(fileData, "mime_type")
		if mime == "" {
			mime = dialectkit.GetStr(fileData, "mimeType")
		}
		uri := dialectkit.GetStr(fileData, "file_uri")
		if uri == "" {
			uri = dialectkit.GetStr(fileData, "fileUri")
		}
		return createMediaByMime(mime, uri, "")
	}
	return nil
}

// createMediaByMime 按 mime 前缀分流媒体类型；data 为 base64 字符串。
func createMediaByMime(mime, url, data string) ai.ContentBlock {
	if data == "" && url == "" {
		return nil
	}
	mediaType := "image"
	if mime != "" {
		lower := strings.ToLower(mime)
		switch {
		case strings.HasPrefix(lower, "audio/"):
			mediaType = "audio"
		case strings.HasPrefix(lower, "video/"):
			mediaType = "video"
		case strings.HasPrefix(lower, "image/"):
			mediaType = "image"
		}
	}

	var rawData []byte
	if data != "" {
		if decoded, err := base64.StdEncoding.DecodeString(data); err == nil {
			rawData = decoded
		}
	}
	switch mediaType {
	case "audio":
		return ai.AudioBlock{URL: url, Data: rawData, Mime: mime}
	case "video":
		return ai.VideoBlock{URL: url, Data: rawData, Mime: mime}
	default:
		return ai.ImageBlock{URL: url, Data: rawData, Mime: mime}
	}
}

// cleanThoughtContent 清理思考内容中的冗余 Markdown 格式：短小的标题型
// "**...**" 包裹行与空包裹符行跳过；思考正文里合法的整行加粗短语不受影响。
func cleanThoughtContent(content string) string {
	if content == "" {
		return content
	}
	var cleaned []string
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) <= cleanThoughtHeadingMaxLength &&
			strings.HasPrefix(trimmed, "**") && strings.HasSuffix(trimmed, "**") {
			continue
		}
		cleaned = append(cleaned, line)
	}
	return strings.Join(cleaned, "\n")
}

// ---- 流式 functionCall 聚合（args 累计快照 → 唯一 START + 单个 ARGS_DELTA）----

// functionCallStreamState 单个函数调用的流式归并状态（候选完成时统一冲刷）。
type functionCallStreamState struct {
	stableKey         string
	candidateIndex    int
	functionName      string
	callID            string
	argumentsSnapshot string
	signature         string
	signatureEmitted  bool
}

func (s *functionCallStreamState) matches(call *ai.ToolCall) bool {
	if s.callID != "" && call.ID != "" {
		return s.callID == call.ID
	}
	if s.functionName != "" && call.Name != "" {
		return s.functionName == call.Name
	}
	return true
}

func (s *functionCallStreamState) merge(call *ai.ToolCall) {
	if call.Name != "" {
		s.functionName = call.Name
	}
	if call.ID != "" {
		s.callID = call.ID
	}
	if call.ArgumentsStr != "" {
		s.argumentsSnapshot = call.ArgumentsStr
	} else if s.argumentsSnapshot == "" {
		s.argumentsSnapshot = "{}"
	}
}

// functionCallStateBag 保持插入序（Java LinkedHashMap 语义）：冲刷时 START 事件
// 按 stableKey 序号有序发出。
type functionCallStateBag struct {
	order []string
	m     map[string]*functionCallStreamState
}

func (b *functionCallStateBag) get(key string) *functionCallStreamState {
	if b.m == nil {
		b.m = map[string]*functionCallStreamState{}
	}
	return b.m[key]
}

func (b *functionCallStateBag) put(key string, state *functionCallStreamState) {
	if b.m == nil {
		b.m = map[string]*functionCallStreamState{}
	}
	if _, exists := b.m[key]; !exists {
		b.order = append(b.order, key)
	}
	b.m[key] = state
}

func (b *functionCallStateBag) remove(key string) {
	delete(b.m, key)
	for i, k := range b.order {
		if k == key {
			b.order = append(b.order[:i], b.order[i+1:]...)
			break
		}
	}
}

const attrFunctionCallStates = "__geminiFunctionCallStates"
const attrEmittedMediaPositions = "__geminiEmittedMediaPositions"
const attrMediaSequence = "__geminiMediaSequence"

func functionCallStates(ctx *ai.StreamContext) *functionCallStateBag {
	bag, _ := ctx.AttrIfAbsent(attrFunctionCallStates, func() any {
		return &functionCallStateBag{}
	}).(*functionCallStateBag)
	return bag
}

// emitStream 候选内容 → 事件（不写终态载体；聚合由 Accumulator.Merge 完成）。
// functionCall.args 是累计快照：调用头与最终参数按候选与调用位置稳定归并，
// 候选完成时发出唯一 START 与一个真实 ARGS_DELTA，避免多个完整 JSON 快照串接。
func emitStream(ctx *ai.StreamContext, oContent map[string]any, candidateIndex int, completed bool) {
	acc := ctx.Acc()
	messages := parseThoughtContent(acc, oContent)
	toolOrdinal := 0

	for _, message := range messages {
		if message.Thinking != "" {
			ev := ctx.Event(ai.EventThinkingDelta)
			ev.Text = message.Thinking
			ctx.Emit(ev)
		}
		if message.Text != "" {
			ev := ctx.Event(ai.EventTextDelta)
			ev.Text = message.Text
			ctx.Emit(ev)
		}
		if len(message.Blocks) > 0 {
			for _, block := range message.Blocks {
				if _, isText := block.(ai.TextBlock); isText {
					continue
				}
				mediaIndex := nextMediaIndex(ctx)
				ev := ctx.Event(ai.EventMediaDone)
				ev.ItemID = fmt.Sprintf("candidate:%d:media:%d", candidateIndex, mediaIndex)
				ev.Index = mediaIndex
				ev.Block = block
				ctx.Emit(ev)
			}
		}
		if len(message.ToolCalls) > 0 {
			messageToolIndex := 0
			for i := range message.ToolCalls {
				call := &message.ToolCalls[i]
				signature := resolveSignature(message, generateContentProtocolID, call, messageToolIndex)
				messageToolIndex++
				stableKey := resolveFunctionCallKey(ctx, candidateIndex, toolOrdinal, call)
				toolOrdinal++

				state := functionCallStates(ctx).get(stableKey)
				if state == nil {
					state = &functionCallStreamState{stableKey: stableKey, candidateIndex: candidateIndex}
					functionCallStates(ctx).put(stableKey, state)
				}
				state.merge(call)

				if signature != "" {
					state.signature = signature
					if !state.signatureEmitted {
						state.signatureEmitted = true
						ev := ctx.Event(ai.EventThinkingSignature)
						ev.ItemID = stableKey
						ev.Text = signature
						ctx.Emit(ev)
					}
				}
			}
		}
	}

	if completed {
		flushFunctionCalls(ctx, candidateIndex)
	}
}

// emitMediaEvents 非流式解析结果中的媒体经 MEDIA_DONE 统一归并（按位置去重）。
func emitMediaEvents(ctx *ai.StreamContext, messages []*ai.AssistantMessage, candidateIndex int) {
	mediaOrdinal := 0
	emitted, _ := ctx.AttrIfAbsent(attrEmittedMediaPositions, func() any {
		return map[string]struct{}{}
	}).(map[string]struct{})

	for _, message := range messages {
		if len(message.Blocks) == 0 {
			continue
		}
		for _, block := range message.Blocks {
			if _, isText := block.(ai.TextBlock); isText {
				continue
			}
			positionKey := fmt.Sprintf("candidate:%d:media:%d", candidateIndex, mediaOrdinal)
			if _, ok := emitted[positionKey]; !ok {
				emitted[positionKey] = struct{}{}
				ev := ctx.Event(ai.EventMediaDone)
				ev.ItemID = positionKey
				ev.Index = mediaOrdinal
				ev.Block = block
				ctx.Emit(ev)
			}
			mediaOrdinal++
		}
	}
}

// completeStream 供应商只用 [DONE] 收尾时提交所有尚未发出的 functionCall 快照。
func completeStream(ctx *ai.StreamContext) {
	bag := functionCallStates(ctx)
	seen := map[int]struct{}{}
	var candidateIndexes []int
	for _, key := range bag.order {
		state := bag.m[key]
		if state == nil {
			continue
		}
		if _, ok := seen[state.candidateIndex]; !ok {
			seen[state.candidateIndex] = struct{}{}
			candidateIndexes = append(candidateIndexes, state.candidateIndex)
		}
	}
	for _, candidateIndex := range candidateIndexes {
		flushFunctionCalls(ctx, candidateIndex)
	}
}

func nextMediaIndex(ctx *ai.StreamContext) int {
	value, _ := ctx.AttrIfAbsent(attrMediaSequence, func() any { return 0 }).(int)
	ctx.AttrPut(attrMediaSequence, value+1)
	return value
}

// resolveFunctionCallKey 调用的流内稳定键：基础位序键冲突（同位不同调用）时按
// 身份后缀去冲突。
func resolveFunctionCallKey(ctx *ai.StreamContext, candidateIndex, ordinal int, call *ai.ToolCall) string {
	bag := functionCallStates(ctx)
	base := fmt.Sprintf("candidate:%d:function:%d", candidateIndex, ordinal)
	if existing := bag.get(base); existing == nil || existing.matches(call) {
		return base
	}
	identity := "next"
	if call.ID != "" {
		identity = call.ID
	} else if call.Name != "" {
		identity = call.Name
	}
	candidate := base + ":" + identity
	suffix := 1
	for {
		existing := bag.get(candidate)
		if existing == nil || existing.matches(call) {
			return candidate
		}
		candidate = fmt.Sprintf("%s:%s:%d", base, identity, suffix)
		suffix++
	}
}

// flushFunctionCalls 冲刷候选的全部调用状态：唯一 START + 最终参数 ARGS_DELTA +
// 签名协议状态（进终态协议袋）。
func flushFunctionCalls(ctx *ai.StreamContext, candidateIndex int) {
	acc := ctx.Acc()
	bag := functionCallStates(ctx)
	for _, key := range append([]string(nil), bag.order...) {
		state := bag.m[key]
		if state == nil || state.candidateIndex != candidateIndex {
			continue
		}

		call := ai.ToolCall{Index: state.stableKey, ID: state.callID, Name: state.functionName}
		ev := ctx.Event(ai.EventToolCallStart)
		ev.ItemID = state.stableKey
		ev.ToolCall = &call
		ev.ToolCallID = state.callID
		ctx.Emit(ev)

		finalArgs := state.argumentsSnapshot
		if finalArgs == "" {
			finalArgs = "{}"
		}
		argsCall := call
		argsCall.ArgumentsStr = finalArgs
		ev = ctx.Event(ai.EventToolCallArgsDelta)
		ev.ItemID = state.stableKey
		ev.ToolCall = &argsCall
		ev.ToolCallID = state.callID
		ev.Text = finalArgs
		ctx.Emit(ev)

		if state.signature != "" {
			acc.PutTerminalProtocolState(generateContentProtocolID,
				createSignatureState(&call, 0, state.signature))
		}
		bag.remove(key)
	}
}

// ---- 响应帧解析 ----

// ParseResponseJSON 解析一帧：error 节点、candidates[0]（finishReason/content/
// usageMetadata）、promptFeedback 拦截；旁路补发联网来源 CITATION 与代码执行
// SERVER_TOOL_* 事件。
func (d *Dialect) ParseResponseJSON(ctx *ai.StreamContext, body string) {
	acc := ctx.Acc()

	if body == "[DONE]" {
		// 供应商只用 [DONE] 收尾时提交尚未发出的 functionCall 快照
		//（OpenAI 兼容网关转 Gemini 场景），避免工具调用静默丢失
		completeStream(ctx)
		acc.SetFinished(true)
		return
	}

	if ctx.IsStream() {
		for _, frame := range sseFrames(body) {
			if frame == "[DONE]" {
				completeStream(ctx)
				acc.SetFinished(true)
				continue
			}
			root := parseFrameObject(frame)
			if root == nil {
				continue
			}
			d.applyResponseNode(ctx, acc, root)
			emitGroundingCitations(ctx, root)
			emitCodeExecution(ctx, root)
			if acc.Err() != nil {
				dialectkit.EmitError(ctx, acc.Err(), root)
				return
			}
		}
		return
	}

	root := parseFrameObject(body)
	if root == nil {
		return
	}
	d.applyNonStreamNode(ctx, acc, root)
	emitGroundingCitations(ctx, root)
	emitCodeExecution(ctx, root)
	if acc.Err() != nil {
		dialectkit.EmitError(ctx, acc.Err(), root)
	}
}

// sseFrames 兜底：个别网关把多帧 JSON 直接按行发送（无 data: 前缀）。
func sseFrames(body string) []string {
	var root map[string]any
	if json.Unmarshal([]byte(body), &root) == nil && root != nil {
		return []string{body}
	}
	var frames []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.TrimPrefix(line, "data:")
		line = strings.TrimSpace(line)
		if line != "" {
			frames = append(frames, line)
		}
	}
	return frames
}

// parseFrameObject 单帧 JSON 容错解析：损坏帧返回 nil，由调用方跳过。
func parseFrameObject(data string) map[string]any {
	var root map[string]any
	if json.Unmarshal([]byte(data), &root) != nil || root == nil {
		return nil
	}
	return root
}

// applyResponseNode 流式帧应用：错误 → model → candidates[0]（finishReason 状态 +
// 内容事件）→ promptFeedback 拦截 → usageMetadata（完成帧才计）。
func (d *Dialect) applyResponseNode(ctx *ai.StreamContext, acc *ai.Accumulator, root map[string]any) bool {
	if applyErrorIfPresent(acc, root) {
		return true
	}
	applyModelName(acc, root)

	hasContent := false
	candidates := dialectkit.GetArray(root, "candidates")
	if len(candidates) > 0 {
		choice, _ := candidates[0].(map[string]any)
		if choice != nil {
			finishReason := dialectkit.GetStr(choice, "finishReason")
			if finishReason == "" {
				finishReason = dialectkit.GetStr(choice, "finish_reason")
			}
			if finishReason != "" {
				acc.SetFinished(true)
				acc.SetFinishReason(finishReason)
			}
			content := dialectkit.GetNode(choice, "content")
			emitStream(ctx, content, 0, finishReason != "")
			hasContent = true
			applyAbnormalFinishReason(acc, choice, finishReason)
		}
	}

	// prompt 被安全策略拦截时无 candidates 返回，需显式报错避免静默结束
	if !hasContent && applyPromptFeedbackBlock(acc, root) {
		return true
	}

	if usage := dialectkit.GetNode(root, "usageMetadata"); usage != nil && acc.IsFinished() {
		acc.SetUsage(buildUsage(usage))
	}
	return hasContent
}

// applyNonStreamNode 非流式整帧应用：内容直接进终态载体；媒体补 MEDIA_DONE；
// usage 不受完成状态约束。
func (d *Dialect) applyNonStreamNode(ctx *ai.StreamContext, acc *ai.Accumulator, root map[string]any) {
	if applyErrorIfPresent(acc, root) {
		return
	}
	applyModelName(acc, root)

	candidates := dialectkit.GetArray(root, "candidates")
	if len(candidates) > 0 {
		choice, _ := candidates[0].(map[string]any)
		if choice != nil {
			finishReason := dialectkit.GetStr(choice, "finishReason")
			if finishReason == "" {
				finishReason = dialectkit.GetStr(choice, "finish_reason")
			}
			content := dialectkit.GetNode(choice, "content")
			messageList := parseThoughtContent(acc, content)
			emitMediaEvents(ctx, messageList, 0)
			for _, msg := range messageList {
				acc.SetTerminalMessage(msg)
			}
			if finishReason != "" {
				acc.SetFinished(true)
				acc.SetFinishReason(finishReason)
				applyAbnormalFinishReason(acc, choice, finishReason)
			}
		}
	}

	if acc.IsFinished() && !acc.TerminalPresent() {
		acc.SetTerminalMessage(&ai.AssistantMessage{})
	}

	// prompt 被安全策略拦截时无 candidates 返回，需显式报错避免静默返回空响应
	if !acc.TerminalPresent() && applyPromptFeedbackBlock(acc, root) {
		return
	}

	if usage := dialectkit.GetNode(root, "usageMetadata"); usage != nil {
		acc.SetUsage(buildUsage(usage))
	}
}

// applyErrorIfPresent error.message 优先，缺失时回退 error 字符串形态。
func applyErrorIfPresent(acc *ai.Accumulator, root map[string]any) bool {
	errNode, has := root["error"]
	if !has {
		return false
	}
	if m, ok := errNode.(map[string]any); ok {
		if msg := dialectkit.GetStr(m, "message"); msg != "" {
			acc.SetError(errors.New(msg))
			return true
		}
	}
	if s := fmt.Sprint(errNode); s != "" {
		acc.SetError(errors.New(s))
		return true
	}
	acc.SetError(errors.New("Unknown error"))
	return true
}

// applyModelName 模型名：model（兼容网关）优先，其次 modelVersion（官方原生）。
func applyModelName(acc *ai.Accumulator, root map[string]any) {
	if v, ok := root["model"]; ok && v != nil {
		acc.SetModel(dialectkit.GetStr(root, "model"))
	} else if v, ok := root["modelVersion"]; ok && v != nil {
		acc.SetModel(dialectkit.GetStr(root, "modelVersion"))
	}
}

// applyPromptFeedbackBlock promptFeedback 安全拦截（camel/snake 双形态）。
func applyPromptFeedbackBlock(acc *ai.Accumulator, root map[string]any) bool {
	feedback := dialectkit.GetNode(root, "promptFeedback")
	if feedback == nil {
		feedback = dialectkit.GetNode(root, "prompt_feedback")
	}
	if feedback == nil {
		return false
	}
	blockReason := dialectkit.GetStr(feedback, "blockReason")
	if blockReason == "" {
		blockReason = dialectkit.GetStr(feedback, "block_reason")
	}
	if blockReason == "" {
		return false
	}
	acc.SetError(errors.New("prompt blocked: " + blockReason))
	return true
}

// buildUsage usageMetadata → ai.Usage。与官方 SDK 语义对齐：toolUsePromptTokenCount
// 是独立项（不含在 promptTokenCount 内），需计入 prompt；responseTokenCount 是
// candidatesTokenCount 的新版替代字段。
func buildUsage(oUsage map[string]any) *ai.Usage {
	toolUseTokens := dialectkit.GetInt(oUsage, "toolUsePromptTokenCount")
	promptTokens := dialectkit.GetInt(oUsage, "promptTokenCount") + toolUseTokens
	completionTokens := dialectkit.GetInt(oUsage, "candidatesTokenCount")
	if completionTokens == 0 {
		completionTokens = dialectkit.GetInt(oUsage, "responseTokenCount")
	}
	u := &ai.Usage{
		PromptTokens:         promptTokens,
		ThinkTokens:          dialectkit.GetInt(oUsage, "thoughtsTokenCount"),
		CompletionTokens:     completionTokens,
		TotalTokens:          dialectkit.GetInt(oUsage, "totalTokenCount"),
		CacheReadInputTokens: dialectkit.GetInt(oUsage, "cachedContentTokenCount"),
	}
	if raw, err := json.Marshal(oUsage); err == nil {
		u.Source = raw
	}
	return u
}

// applyAbnormalFinishReason STOP/MAX_TOKENS 之外的终止原因可诊断（对标官方 SDK
// checkFinishReason）。
func applyAbnormalFinishReason(acc *ai.Accumulator, candidate map[string]any, finishReason string) {
	if finishReason == "" || strings.EqualFold(finishReason, "STOP") ||
		strings.EqualFold(finishReason, "MAX_TOKENS") ||
		strings.EqualFold(finishReason, "FINISH_REASON_UNSPECIFIED") {
		return
	}
	message := dialectkit.GetStr(candidate, "finishMessage")
	if message == "" {
		message = dialectkit.GetStr(candidate, "finish_message")
	}
	if message == "" {
		message = "Gemini generation stopped: " + finishReason
	} else {
		message = "Gemini generation stopped (" + finishReason + "): " + message
	}
	acc.SetError(errors.New(message))
}

// emitGroundingCitations 联网搜索来源（candidates[0].groundingMetadata.
// groundingChunks[].web）→ CITATION 事件（按出现位置去重）。
func emitGroundingCitations(ctx *ai.StreamContext, root map[string]any) {
	candidates := dialectkit.GetArray(root, "candidates")
	if len(candidates) == 0 {
		return
	}
	candidate, _ := candidates[0].(map[string]any)
	if candidate == nil {
		return
	}
	grounding := dialectkit.GetNode(candidate, "groundingMetadata")
	if grounding == nil {
		return
	}
	chunks := dialectkit.GetArray(grounding, "groundingChunks")
	if len(chunks) == 0 {
		return
	}

	candidateIndex := 0
	emitted, _ := ctx.AttrIfAbsent("__geminiGroundingCitations", func() any {
		return map[string]struct{}{}
	}).(map[string]struct{})

	for chunkIndex, item := range chunks {
		chunk, _ := item.(map[string]any)
		if chunk == nil {
			continue
		}
		web := dialectkit.GetNode(chunk, "web")
		if web == nil {
			continue
		}
		uri := dialectkit.GetStr(web, "uri")
		if uri == "" {
			continue
		}
		occurrenceKey := fmt.Sprintf("%d:%d:%s", candidateIndex, chunkIndex, uri)
		if _, ok := emitted[occurrenceKey]; ok {
			continue
		}
		emitted[occurrenceKey] = struct{}{}

		citation := ai.Citation{
			Title: dialectkit.GetStr(web, "title"),
			URL:   uri,
			Meta:  map[string]any{"type": "google_search"},
		}
		ev := ctx.Event(ai.EventCitation)
		ev.RawType = "groundingMetadata"
		ev.SubType = "google_search"
		ev.Index = candidateIndex
		ev.Text = uri
		ev.Citation = &citation
		if raw, err := json.Marshal(chunk); err == nil {
			ev.Raw = raw
		}
		if ev.Attrs == nil {
			ev.Attrs = map[string]any{}
		}
		ev.Attrs["chunk_index"] = chunkIndex
		ctx.Emit(ev)
	}
}

// emitCodeExecution 代码执行服务端工具事件（executableCode/codeExecutionResult →
// SERVER_TOOL_START/RESULT，保证成对）。只旁路发事件，不介入思考处理器的文本拼接。
func emitCodeExecution(ctx *ai.StreamContext, root map[string]any) {
	candidates := dialectkit.GetArray(root, "candidates")
	if len(candidates) == 0 {
		return
	}
	candidate, _ := candidates[0].(map[string]any)
	if candidate == nil {
		return
	}
	content := dialectkit.GetNode(candidate, "content")
	if content == nil {
		return
	}
	parts := dialectkit.GetArray(content, "parts")
	if len(parts) == 0 {
		return
	}

	candidateIndex := 0
	for _, item := range parts {
		part, _ := item.(map[string]any)
		if part == nil {
			continue
		}
		// 官方 REST 用 camelCase，部分兼容网关转发时写成 snake_case，两种都接
		executableCode := dialectkit.GetNode(part, "executableCode")
		if executableCode == nil {
			executableCode = dialectkit.GetNode(part, "executable_code")
		}
		if executableCode != nil {
			ev := ctx.Event(ai.EventServerToolStart)
			ev.RawType = "executableCode"
			ev.SubType = "code_execution"
			ev.Index = candidateIndex
			ev.Text = dialectkit.GetStr(executableCode, "code")
			if raw, err := json.Marshal(part); err == nil {
				ev.Raw = raw
			}
			ctx.Emit(ev)
			continue
		}

		executionResult := dialectkit.GetNode(part, "codeExecutionResult")
		if executionResult == nil {
			executionResult = dialectkit.GetNode(part, "code_execution_result")
		}
		if executionResult != nil {
			ev := ctx.Event(ai.EventServerToolResult)
			ev.RawType = "codeExecutionResult"
			ev.SubType = "code_execution"
			ev.Index = candidateIndex
			ev.Text = dialectkit.GetStr(executionResult, "output")
			if raw, err := json.Marshal(part); err == nil {
				ev.Raw = raw
			}
			ctx.Emit(ev)
		}
	}
}
