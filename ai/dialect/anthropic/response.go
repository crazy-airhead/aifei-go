// response.go Anthropic Messages 响应解析（流式 SSE 事件流 + 非流式整帧）：
// thinking 签名 / redacted_thinking 逐块留存、input_json_delta 参数聚合、
// Prompt Caching 计量（usage 累计快照按字段存在性覆盖合并）与 stop_reason 语义分流。
//
// 与 Java 4.1 AnthropicResponseParser 的差异（Go 侧裁剪）：服务端工具原始块留存与
// 跨轮回放（captureServerToolBlock / CONTENT_BLOCKS_RAW / container）、text citations
// 不迁——SERVER_TOOL_* 事件照发，仅无原样回传载体。
package anthropic

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/internal/dialectkit"
)

// ---- 流式按请求隔离状态（对标 Java StreamToolState 族）----

// streamToolState 单个 content_block 的工具调用流式状态（按协议 index 跟踪——
// 一次响应可包含多个并列/交错的 tool_use 块，start/delta/stop 均以 index 归属）。
type streamToolState struct {
	toolUseId string
	toolName  string
	toolInput strings.Builder
	// initialInput content_block_start 已给出的参数初值（eager input streaming / 网关），
	// 此后可能不再有 delta；空串表示无初值（空对象不算，否则会与 delta 拼出脏值）。
	initialInput  string
	hasInputDelta bool
	serverTool    bool
}

const (
	attrToolStates      = "__anthropicToolStates"         // map[int]*streamToolState
	attrSignatures      = "__anthropicThinkingSignatures" // map[int]*strings.Builder
	attrRedactedBlocks  = "__anthropicRedactedBlocks"     // *[]string（append 需原地）
	attrTerminalEmitted = "__anthropicTerminalEmitted"    // bool
)

func toolStatesOf(ctx *ai.StreamContext) map[int]*streamToolState {
	m, _ := ctx.AttrIfAbsent(attrToolStates, func() any {
		return map[int]*streamToolState{}
	}).(map[int]*streamToolState)
	return m
}

func streamSignatures(ctx *ai.StreamContext) map[int]*strings.Builder {
	m, _ := ctx.AttrIfAbsent(attrSignatures, func() any {
		return map[int]*strings.Builder{}
	}).(map[int]*strings.Builder)
	return m
}

func redactedBlocksOf(ctx *ai.StreamContext) *[]string {
	p, _ := ctx.AttrIfAbsent(attrRedactedBlocks, func() any {
		v := []string{}
		return &v
	}).(*[]string)
	return p
}

// appendStreamThinkingSignature 累计 thinking 块的 signature 分片：官方协议每块只发
// 一个完整 signature_delta；兼容网关拆多帧时按 index 拼回，避免不同块分片串扰。
func appendStreamThinkingSignature(ctx *ai.StreamContext, index int, fragment string) string {
	m := streamSignatures(ctx)
	b := m[index]
	if b == nil {
		b = &strings.Builder{}
		m[index] = b
	}
	b.WriteString(fragment)
	return b.String()
}

// initialToolInput content_block_start 已给出的参数初值：仅非空 object 有意义，
// 空对象归一为无初值（""），否则会与随后的 delta 拼成 {"a":1}} 类脏值。
func initialToolInput(contentBlock map[string]any) string {
	input, ok := contentBlock["input"].(map[string]any)
	if !ok || len(input) == 0 {
		return ""
	}
	data, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	return string(data)
}

// resolveFinalToolInput 块完成时的最终参数：无初值取累计 delta；有初值时仅当
// delta 累计是完整单 object 才采纳（截断的累计串宁可回退初值）。
func resolveFinalToolInput(state *streamToolState) string {
	if state == nil {
		return ""
	}
	if state.initialInput == "" {
		return state.toolInput.String()
	}
	if state.hasInputDelta && isSingleJSONObject(state.toolInput.String()) {
		return state.toolInput.String()
	}
	return state.initialInput
}

// isSingleJSONObject 整串恰好是一个 JSON object（trim 后 {} 包裹且整体可解析）。
func isSingleJSONObject(s string) bool {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "{") || !strings.HasSuffix(trimmed, "}") {
		return false
	}
	var v any
	return json.Unmarshal([]byte(trimmed), &v) == nil
}

// ---- 入口 ----

// ParseResponseJSON 按流式/非流式分流。
func (d *Dialect) ParseResponseJSON(ctx *ai.StreamContext, body string) {
	if ctx.IsStream() {
		parseStreamResponse(ctx, body)
		return
	}
	parseNonStreamResponse(ctx, body)
}

// ---- 流式 ----

// parseStreamResponse 正文/思考/工具参数直接走事件通道（聚合由 Accumulator 完成）；
// 终态只保存事件无法重建的协议载荷（thinking 签名、redacted_thinking 分块）。
func parseStreamResponse(ctx *ai.StreamContext, body string) {
	acc := ctx.Acc()
	if body == "" {
		return
	}

	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		jsonData := line
		if strings.HasPrefix(line, "data:") {
			jsonData = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
		if jsonData == "" {
			continue
		}
		if jsonData == "[DONE]" {
			// 工具状态由终态收口统一清理，不能在此先移除（缺 content_block_stop 的
			// 截断流参数已在事件通道交付）
			emitTerminalFrameOnce(ctx, "[DONE]")
			return
		}

		var root map[string]any
		if json.Unmarshal([]byte(jsonData), &root) != nil || root == nil {
			continue
		}

		if errNode, has := root["error"]; has {
			acc.SetError(parseAnthropicError(errNode))
			dialectkit.EmitError(ctx, acc.Err(), root)
			return
		}

		switch eventType := dialectkit.GetStr(root, "type"); eventType {
		case "error":
			ctx.AttrRemove(attrToolStates)
			acc.SetError(parseAnthropicError(root["error"]))
			dialectkit.EmitError(ctx, acc.Err(), root)
			return
		case "message_start":
			if message := dialectkit.GetNode(root, "message"); message != nil {
				acc.SetModel(dialectkit.GetStr(message, "model"))
				// 供应商响应标识（msg_xxx）：记录一次，后续事件自动预填
				ctx.SetProviderResponseID(dialectkit.GetStr(message, "id"))
				if usage := parseUsage(dialectkit.GetNode(message, "usage")); usage != nil {
					acc.SetUsage(usage)
				}
			}
		case "content_block_start":
			handleContentBlockStart(ctx, root, eventType)
		case "content_block_delta":
			handleContentBlockDelta(ctx, root, eventType)
		case "content_block_stop":
			handleContentBlockStop(ctx, root, eventType)
		case "message_delta":
			handleMessageDelta(ctx, root, eventType)
		case "message_stop":
			// 消息结束：收口终态；完成状态只在这里设置（message_delta 携带的
			// stop_reason 是消息元数据，不能证明所有 content block 已到齐）
			emitTerminalFrameOnce(ctx, eventType)
		case "ping":
			ev := ctx.Event(ai.EventHeartbeat)
			ev.RawType = eventType
			setRaw(ev, root)
			ctx.Emit(ev)
		case "":
			// 无 type 的帧：跳过
		default:
			// 未建模事件以 RAW 透出（GA 前向兼容，不静默丢帧）
			ev := ctx.Event(ai.EventRaw)
			ev.RawType = eventType
			setRaw(ev, root)
			ctx.Emit(ev)
		}
	}
}

// handleContentBlockStart 按 content_block.type 分流。
func handleContentBlockStart(ctx *ai.StreamContext, root map[string]any, eventType string) {
	acc := ctx.Acc()
	contentBlock := dialectkit.GetNode(root, "content_block")
	if contentBlock == nil {
		return
	}
	blockIdx := int(dialectkit.GetInt(root, "index"))
	blockType := dialectkit.GetStr(contentBlock, "type")

	switch {
	case blockType == "thinking":
		acc.SetReasoningFieldName("thinking")
		acc.SetInThinking(true)
		if thinking := dialectkit.GetStr(contentBlock, "thinking"); thinking != "" {
			emitStreamDelta(ctx, ai.EventThinkingDelta, eventType, blockIdx, thinking, root)
		}
		if signature := dialectkit.GetStr(contentBlock, "signature"); signature != "" {
			b := &strings.Builder{}
			b.WriteString(signature)
			streamSignatures(ctx)[blockIdx] = b
			acc.SetThinkingSignature(signature)
			ev := ctx.Event(ai.EventThinkingSignature)
			ev.RawType = eventType
			ev.Index = blockIdx
			ev.Text = signature
			setRaw(ev, root)
			ctx.Emit(ev)
		}
	case blockType == "text":
		// 思考→正文边界：in_thinking 关闭（THINKING_END 由终态收口按需补齐）
		acc.SetInThinking(false)
		if text := dialectkit.GetStr(contentBlock, "text"); text != "" {
			emitStreamDelta(ctx, ai.EventTextDelta, eventType, blockIdx, text, root)
		}
		// text 块内嵌 citations：P1-b 裁剪不迁
	case blockType == "tool_use":
		// 本地工具调用直接进事件通道，不借 AssistantMessage 充当流式分片载体
		acc.SetInThinking(false)
		state := &streamToolState{
			toolUseId:    dialectkit.GetStr(contentBlock, "id"),
			toolName:     dialectkit.GetStr(contentBlock, "name"),
			initialInput: initialToolInput(contentBlock),
		}
		toolStatesOf(ctx)[blockIdx] = state
		emitToolCallEvent(ctx, ai.EventToolCallStart, eventType, blockIdx, state, "", nil, root)
	case blockType == "redacted_thinking":
		// 安全过滤的推理内容块：opaque 数据逐块原样保留（拼接会损坏 base64）
		if data := dialectkit.GetStr(contentBlock, "data"); data != "" {
			rb := redactedBlocksOf(ctx)
			*rb = append(*rb, data)
			ev := ctx.Event(ai.EventThinkingRedacted)
			ev.RawType = eventType
			ev.Index = blockIdx
			ev.Text = data
			setRaw(ev, root)
			ctx.Emit(ev)
		}
	case blockType == "server_tool_use" || blockType == "mcp_tool_use":
		// 服务端执行的工具调用：参数同样经 input_json_delta 分片下发，必须登记
		// 状态，否则参数分片静默丢失（mcp_tool_use 的结果块由 _tool_result 后缀
		// 规则捎带命中）
		serverState := &streamToolState{
			serverTool:   true,
			toolUseId:    dialectkit.GetStr(contentBlock, "id"),
			toolName:     dialectkit.GetStr(contentBlock, "name"),
			initialInput: initialToolInput(contentBlock),
		}
		toolStatesOf(ctx)[blockIdx] = serverState

		ev := ctx.Event(ai.EventServerToolStart)
		ev.RawType = eventType
		ev.SubType = serverState.toolName
		ev.ItemID = serverState.toolUseId
		ev.Index = blockIdx
		appendServerToolCaller(ev, contentBlock)
		setRaw(ev, root)
		ctx.Emit(ev)
	case strings.HasSuffix(blockType, "_tool_result"):
		// web_search_tool_result / web_fetch_tool_result 等：文本投影进事件，
		// 不再拼进正文（订阅方无法与模型自述区分）
		ev := ctx.Event(ai.EventServerToolResult)
		ev.RawType = eventType
		ev.SubType = blockType
		ev.ItemID = dialectkit.GetStr(contentBlock, "tool_use_id")
		ev.Index = blockIdx
		ev.Text = extractToolResultText(contentBlock)
		setRaw(ev, root)
		ctx.Emit(ev)
	case blockType == "container_upload":
		// 代码执行产出的文件（仅 file_id）
		fileId := dialectkit.GetStr(contentBlock, "file_id")
		ev := ctx.Event(ai.EventServerToolResult)
		ev.RawType = eventType
		ev.SubType = blockType
		ev.ItemID = fileId
		ev.Index = blockIdx
		ev.Text = fileId
		setRaw(ev, root)
		ctx.Emit(ev)
	case blockType != "":
		// 未建模内容块以 RAW 透出（块级/事件级/增量级三层兜底对称）
		ev := ctx.Event(ai.EventRaw)
		ev.RawType = eventType
		ev.SubType = blockType
		ev.Index = blockIdx
		setRaw(ev, root)
		ctx.Emit(ev)
	}
}

// handleContentBlockDelta 按 delta.type 分流。
func handleContentBlockDelta(ctx *ai.StreamContext, root map[string]any, eventType string) {
	delta := dialectkit.GetNode(root, "delta")
	if delta == nil {
		return
	}
	blockIdx := int(dialectkit.GetInt(root, "index"))
	acc := ctx.Acc()

	switch deltaType := dialectkit.GetStr(delta, "type"); deltaType {
	case "thinking_delta":
		if thinking := dialectkit.GetStr(delta, "thinking"); thinking != "" {
			emitStreamDelta(ctx, ai.EventThinkingDelta, eventType, blockIdx, thinking, root)
		}
	case "signature_delta":
		if fragment := dialectkit.GetStr(delta, "signature"); fragment != "" {
			signature := appendStreamThinkingSignature(ctx, blockIdx, fragment)
			// 官方流只发一帧完整签名；网关拆帧时保存当前完整累计值，
			// 末帧事件与终态载体始终一致
			acc.SetThinkingSignature(signature)
			ev := ctx.Event(ai.EventThinkingSignature)
			ev.RawType = eventType
			ev.Index = blockIdx
			ev.Text = signature
			setRaw(ev, root)
			ctx.Emit(ev)
		}
	case "text_delta":
		if text := dialectkit.GetStr(delta, "text"); text != "" {
			emitStreamDelta(ctx, ai.EventTextDelta, eventType, blockIdx, text, root)
		}
	case "citations_delta", "citation_delta":
		// 引用：P1-b 裁剪不迁
	case "input_json_delta":
		partialJSON := dialectkit.GetStr(delta, "partial_json")
		if partialJSON == "" {
			return
		}
		state := toolStatesOf(ctx)[blockIdx]
		if state == nil || state.initialInput != "" {
			return
		}
		state.hasInputDelta = true
		state.toolInput.WriteString(partialJSON)
		if state.serverTool {
			ev := ctx.Event(ai.EventServerToolArgsDelta)
			ev.RawType = eventType
			ev.ToolCallID = state.toolUseId
			ev.ItemID = state.toolUseId
			ev.Index = blockIdx
			ev.Text = partialJSON
			setRaw(ev, root)
			ctx.Emit(ev)
			return
		}
		emitToolCallEvent(ctx, ai.EventToolCallArgsDelta, eventType, blockIdx, state, partialJSON, nil, root)
	case "":
		// 无 type 的 delta：跳过
	default:
		// 未建模增量以 RAW 透出
		ev := ctx.Event(ai.EventRaw)
		ev.RawType = eventType
		ev.SubType = deltaType
		ev.Index = blockIdx
		setRaw(ev, root)
		ctx.Emit(ev)
	}
}

// handleContentBlockStop 按 index 释放工具块状态；eager input（start 已给完整参数、
// 无 delta）在此补发一次 ARGS_DELTA。TOOL_CALL_END 不由此发——核心在完整参数聚合
// 后统一补（避免双发）。
func handleContentBlockStop(ctx *ai.StreamContext, root map[string]any, eventType string) {
	blockIdx := int(dialectkit.GetInt(root, "index"))
	states := toolStatesOf(ctx)
	state := states[blockIdx]
	delete(states, blockIdx)
	if state == nil {
		return
	}
	finalInput := resolveFinalToolInput(state)
	if state.initialInput == "" || finalInput == "" {
		return
	}
	if state.serverTool {
		ev := ctx.Event(ai.EventServerToolArgsDelta)
		ev.RawType = eventType
		ev.ToolCallID = state.toolUseId
		ev.ItemID = state.toolUseId
		ev.Index = blockIdx
		ev.Text = finalInput
		setRaw(ev, root)
		ctx.Emit(ev)
		return
	}
	emitToolCallEvent(ctx, ai.EventToolCallArgsDelta, eventType, blockIdx, state, finalInput, nil, root)
}

// handleMessageDelta usage 是累计快照（按字段存在性覆盖，含显式 0；不能相加）；
// stop_reason 是消息元数据，完成标记仍等 message_stop。
func handleMessageDelta(ctx *ai.StreamContext, root map[string]any, eventType string) {
	acc := ctx.Acc()

	if usageNode := dialectkit.GetNode(root, "usage"); usageNode != nil {
		var prevSource json.RawMessage
		if prev := acc.Usage(); prev != nil {
			prevSource = prev.Source
		}
		merged := mergeUsageSource(prevSource, usageNode)
		if usage := parseUsage(merged); usage != nil {
			acc.SetUsage(usage)
		}
	}

	delta := dialectkit.GetNode(root, "delta")
	if delta == nil {
		return
	}
	if finishReason := dialectkit.GetStr(delta, "stop_reason"); finishReason != "" {
		acc.SetFinishReason(finishReason)
		emitStopReasonEvent(ctx, eventType, finishReason, delta, root)
	}
}

// emitStopReasonEvent 停止原因语义分流（供应商原始值保留在 finishReason，此处只补
// 事件）：refusal → CONTENT_FILTER（带 stop_details）；stop_sequence → STATUS（带
// 实际命中的序列）；pause_turn/max_tokens/model_context_window_exceeded → STATUS
// （截断类不抬为 ERROR——已生成的正文与计费不能连带弃掉）。
func emitStopReasonEvent(ctx *ai.StreamContext, rawType, stopReason string, stopNode, raw map[string]any) {
	switch stopReason {
	case "refusal":
		category, explanation := "", ""
		if stopDetails := dialectkit.GetNode(stopNode, "stop_details"); stopDetails != nil {
			category = dialectkit.GetStr(stopDetails, "category")
			explanation = dialectkit.GetStr(stopDetails, "explanation")
		}
		ev := ctx.Event(ai.EventContentFilter)
		ev.RawType = rawType
		if category == "" {
			category = stopReason
		}
		ev.SubType = category
		ev.Text = explanation
		setRaw(ev, raw)
		ctx.Emit(ev)
	case "stop_sequence":
		ev := ctx.Event(ai.EventStatus)
		ev.RawType = rawType
		ev.SubType = stopReason
		if stopNode != nil {
			ev.Text = dialectkit.GetStr(stopNode, "stop_sequence")
		}
		setRaw(ev, raw)
		ctx.Emit(ev)
	case "pause_turn", "max_tokens", "model_context_window_exceeded":
		ev := ctx.Event(ai.EventStatus)
		ev.RawType = rawType
		ev.SubType = stopReason
		setRaw(ev, raw)
		ctx.Emit(ev)
	}
}

// emitTerminalFrameOnce 流式终态收口（幂等）：message_stop 与兼容网关 [DONE] 双发
// 场景只执行一次；完成状态始终置位。
func emitTerminalFrameOnce(ctx *ai.StreamContext, rawType string) {
	acc := ctx.Acc()
	first := false
	ctx.AttrIfAbsent(attrTerminalEmitted, func() any {
		first = true
		return true
	})
	if first {
		emitTerminalFrame(ctx, rawType)
	}
	acc.SetFinished(true)
}

// emitTerminalFrame 终态只保存事件无法重建的协议字段（签名 + redacted 分块）。
// 本地工具参数已在事件通道交付，无跨轮回放载体的截断兑底也就无需额外动作。
func emitTerminalFrame(ctx *ai.StreamContext, rawType string) {
	acc := ctx.Acc()
	if wasThinking := acc.InThinking(); wasThinking {
		acc.SetInThinking(false)
		ev := ctx.Event(ai.EventThinkingEnd)
		ev.RawType = rawType
		ctx.Emit(ev)
	}
	ctx.AttrRemove(attrToolStates)

	protocolData := map[string]any{}
	if signature := acc.ThinkingSignature(); signature != "" {
		protocolData[stateSignature] = signature
	}
	if rb := redactedBlocksOf(ctx); len(*rb) > 0 {
		protocolData[stateRedactedBlk] = append([]string(nil), *rb...)
	}
	if state := createState(protocolData); state != nil {
		acc.PutTerminalProtocolState(protocolID, state)
	}
}

// ---- 非流式 ----

// parseNonStreamResponse content[] 块 → 事件 + 终态消息；与流式路径对称（签名、
// redacted、服务端工具走事件通道，不降级拼正文）。stop 事件在内容块之后发，
// 保持与流式同序。
func parseNonStreamResponse(ctx *ai.StreamContext, body string) {
	acc := ctx.Acc()

	if body == "[DONE]" {
		if !acc.IsFinished() {
			acc.SetTerminalMessage(&ai.AssistantMessage{})
			acc.SetFinished(true)
		}
		return
	}

	var root map[string]any
	if json.Unmarshal([]byte(body), &root) != nil || root == nil {
		return
	}

	if errNode, has := root["error"]; has {
		acc.SetError(parseAnthropicError(errNode))
		dialectkit.EmitError(ctx, acc.Err(), root)
		return
	}

	// 模型与响应标识先于内容事件（事件自动携带 message id）
	acc.SetModel(dialectkit.GetStr(root, "model"))
	ctx.SetProviderResponseID(dialectkit.GetStr(root, "id"))
	stopReason := dialectkit.GetStr(root, "stop_reason")

	contentArray := dialectkit.GetArray(root, "content")
	if contentArray == nil {
		// 无 content 也要建立空终态，保持完成响应的消息契约
		acc.SetTerminalMessage(&ai.AssistantMessage{})
		acc.SetFinishReason("stop")
		acc.SetFinished(true)
		return
	}

	var thinkingContent, normalContent strings.Builder
	thinkingSignature := ""
	var mediaBlocks []ai.ContentBlock
	var allToolCalls []ai.ToolCall
	var redactedList []string

	for blockIndex, item := range contentArray {
		contentItem, _ := item.(map[string]any)
		if contentItem == nil {
			continue
		}
		contentType := dialectkit.GetStr(contentItem, "type")
		switch contentType {
		case "thinking":
			if thinking := dialectkit.GetStr(contentItem, "thinking"); thinking != "" {
				thinkingContent.WriteString(thinking)
				emitStreamDelta(ctx, ai.EventThinkingDelta, contentType, blockIndex, thinking, contentItem)
			}
			if signature := dialectkit.GetStr(contentItem, "signature"); signature != "" {
				thinkingSignature = signature
				acc.SetThinkingSignature(signature)
				ev := ctx.Event(ai.EventThinkingSignature)
				ev.RawType = contentType
				ev.Index = blockIndex
				ev.Text = signature
				setRaw(ev, contentItem)
				ctx.Emit(ev)
			}
		case "text":
			if text := dialectkit.GetStr(contentItem, "text"); text != "" {
				normalContent.WriteString(text)
				emitStreamDelta(ctx, ai.EventTextDelta, contentType, blockIndex, text, contentItem)
			}
			// 非流式引用内嵌在 text 块 citations 数组：P1-b 裁剪不迁
		case "image":
			if imageBlock := parseClaudeImageBlock(contentItem); imageBlock != nil {
				mediaBlocks = append(mediaBlocks, imageBlock)
				ev := ctx.Event(ai.EventMediaDone)
				ev.RawType = contentType
				ev.Index = blockIndex
				ev.Block = imageBlock
				setRaw(ev, contentItem)
				ctx.Emit(ev)
			}
		case "tool_use":
			toolName := dialectkit.GetStr(contentItem, "name")
			toolId := dialectkit.GetStr(contentItem, "id")
			arguments, _ := contentItem["input"].(map[string]any)
			// 网关/兼容实现可能缺省 input，兜底空对象
			inputJson := "{}"
			if arguments != nil {
				if data, err := json.Marshal(arguments); err == nil {
					inputJson = string(data)
				}
			}
			toolCall := ai.ToolCall{
				Index:        fmt.Sprintf("idx:%d", blockIndex),
				ID:           toolId,
				Name:         toolName,
				ArgumentsStr: inputJson,
				Arguments:    arguments,
			}
			allToolCalls = append(allToolCalls, toolCall)

			// 非流式没有核心流收口补位，须在完整响应内保留完整工具调用生命周期
			state := &streamToolState{toolUseId: toolId, toolName: toolName}
			emitToolCallEvent(ctx, ai.EventToolCallStart, contentType, blockIndex, state, "", nil, contentItem)
			if inputJson != "" && len(arguments) > 0 {
				emitToolCallEvent(ctx, ai.EventToolCallArgsDelta, contentType, blockIndex, state, inputJson, nil, contentItem)
			}
			emitToolCallEvent(ctx, ai.EventToolCallEnd, contentType, blockIndex, state, "", &toolCall, contentItem)
		case "redacted_thinking":
			if data := dialectkit.GetStr(contentItem, "data"); data != "" {
				redactedList = append(redactedList, data)
				ev := ctx.Event(ai.EventThinkingRedacted)
				ev.RawType = contentType
				ev.Index = blockIndex
				ev.Text = data
				setRaw(ev, contentItem)
				ctx.Emit(ev)
			}
		case "server_tool_use", "mcp_tool_use":
			ev := ctx.Event(ai.EventServerToolStart)
			ev.RawType = contentType
			ev.SubType = dialectkit.GetStr(contentItem, "name")
			ev.ItemID = dialectkit.GetStr(contentItem, "id")
			ev.Index = blockIndex
			appendServerToolCaller(ev, contentItem)
			setRaw(ev, contentItem)
			ctx.Emit(ev)

			// 非流式 server-only 工具同样交付完整参数，且绝不进入本地 tool_calls
			if serverInput := initialToolInput(contentItem); serverInput != "" {
				sEv := ctx.Event(ai.EventServerToolArgsDelta)
				sEv.RawType = contentType
				sEv.ToolCallID = dialectkit.GetStr(contentItem, "id")
				sEv.ItemID = sEv.ToolCallID
				sEv.Index = blockIndex
				sEv.Text = serverInput
				setRaw(sEv, contentItem)
				ctx.Emit(sEv)
			}
		case "container_upload":
			fileId := dialectkit.GetStr(contentItem, "file_id")
			ev := ctx.Event(ai.EventServerToolResult)
			ev.RawType = contentType
			ev.SubType = contentType
			ev.ItemID = fileId
			ev.Index = blockIndex
			ev.Text = fileId
			setRaw(ev, contentItem)
			ctx.Emit(ev)
		default:
			if contentType != "" && strings.HasSuffix(contentType, "_tool_result") {
				ev := ctx.Event(ai.EventServerToolResult)
				ev.RawType = contentType
				ev.SubType = contentType
				ev.ItemID = dialectkit.GetStr(contentItem, "tool_use_id")
				ev.Index = blockIndex
				ev.Text = extractToolResultText(contentItem)
				setRaw(ev, contentItem)
				ctx.Emit(ev)
				continue
			}
			if contentType != "" {
				ev := ctx.Event(ai.EventRaw)
				ev.RawType = contentType
				ev.SubType = contentType
				ev.Index = blockIndex
				setRaw(ev, contentItem)
				ctx.Emit(ev)
			}
		}
	}

	textStr, thinkingStr := normalContent.String(), thinkingContent.String()

	protocolData := map[string]any{}
	if thinkingSignature != "" {
		protocolData[stateSignature] = thinkingSignature
	}
	if len(redactedList) > 0 {
		protocolData[stateRedactedBlk] = redactedList
	}

	var blocksForMsg []ai.ContentBlock
	if len(mediaBlocks) > 0 {
		if textStr != "" {
			blocksForMsg = append(blocksForMsg, ai.TextBlock{Text: textStr})
		}
		blocksForMsg = append(blocksForMsg, mediaBlocks...)
	}

	// finishReason：优先真实 stop_reason；tool 场景兜底 tool_use
	choiceFinishReason := stopReason
	if choiceFinishReason == "" {
		if len(allToolCalls) > 0 {
			choiceFinishReason = "tool_use"
		} else {
			choiceFinishReason = "stop"
		}
	}

	msg := &ai.AssistantMessage{
		Text:      textStr,
		Thinking:  thinkingStr,
		ToolCalls: allToolCalls,
		Blocks:    blocksForMsg,
	}
	acc.SetTerminalMessage(msg)
	if state := createState(protocolData); state != nil {
		acc.PutTerminalProtocolState(protocolID, state)
	}
	acc.SetFinishReason(choiceFinishReason)

	// 与流式 message_delta 对称：停止事件在内容块之后（非流式的 stop_sequence 与
	// stop_details 位于 message 本体顶层）
	if stopReason != "" {
		emitStopReasonEvent(ctx, "message", stopReason, root, root)
	}

	if usage := parseUsage(dialectkit.GetNode(root, "usage")); usage != nil {
		acc.SetUsage(usage)
	}
	acc.SetFinished(true)
}

// parseClaudeImageBlock image 块：source.base64 / source.url；无 source 时兼容顶层
// data/url。base64 解码进 ImageBlock.Data（出站由 claudeImageBlock 重新编码）。
func parseClaudeImageBlock(contentItem map[string]any) ai.ContentBlock {
	if contentItem == nil {
		return nil
	}
	source := dialectkit.GetNode(contentItem, "source")
	if source == nil {
		data := dialectkit.GetStr(contentItem, "data")
		if data != "" {
			return imageBlockOfBase64(data, "")
		}
		if url := dialectkit.GetStr(contentItem, "url"); url != "" {
			return ai.ImageBlock{URL: url}
		}
		return nil
	}

	mediaType := dialectkit.GetStr(source, "media_type")
	if mediaType == "" {
		mediaType = dialectkit.GetStr(source, "mediaType")
	}
	sourceType := dialectkit.GetStr(source, "type")

	if sourceType == "base64" {
		return imageBlockOfBase64(dialectkit.GetStr(source, "data"), mediaType)
	}
	if sourceType == "url" {
		if url := dialectkit.GetStr(source, "url"); url != "" {
			return ai.ImageBlock{URL: url, Mime: mediaType}
		}
		return nil
	}
	// 无显式 type 时按字段存在性兜底
	if _, has := source["data"]; has {
		return imageBlockOfBase64(dialectkit.GetStr(source, "data"), mediaType)
	}
	if _, has := source["url"]; has {
		if url := dialectkit.GetStr(source, "url"); url != "" {
			return ai.ImageBlock{URL: url, Mime: mediaType}
		}
	}
	return nil
}

func imageBlockOfBase64(data, mime string) ai.ContentBlock {
	if data == "" {
		return nil
	}
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil
	}
	return ai.ImageBlock{Data: decoded, Mime: mime}
}

// ParseAssistantMessage message/content 节点 → 聚合消息：Claude content 块数组形态
// 走本方言解析（thinking 签名 + redacted 分块进协议状态）；其余走基类默认。
func (d *Dialect) ParseAssistantMessage(acc *ai.Accumulator, node map[string]any) []*ai.AssistantMessage {
	content, ok := node["content"].([]any)
	if !ok || node == nil {
		return dialectkit.ParseAssistantMessage(acc, node)
	}

	var thinkingContent, normalContent strings.Builder
	thinkingSignature := ""
	var mediaBlocks []ai.ContentBlock
	var toolCalls []ai.ToolCall
	var redactedList []string

	for _, item := range content {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		switch dialectkit.GetStr(m, "type") {
		case "thinking":
			thinkingContent.WriteString(dialectkit.GetStr(m, "thinking"))
			if signature := dialectkit.GetStr(m, "signature"); signature != "" {
				thinkingSignature = signature
			}
		case "text":
			normalContent.WriteString(dialectkit.GetStr(m, "text"))
		case "image":
			if b := parseClaudeImageBlock(m); b != nil {
				mediaBlocks = append(mediaBlocks, b)
			}
		case "tool_use":
			toolId := dialectkit.GetStr(m, "id")
			input, _ := m["input"].(map[string]any)
			inputJson := "{}"
			if len(input) > 0 {
				if data, err := json.Marshal(input); err == nil {
					inputJson = string(data)
				}
			}
			toolCalls = append(toolCalls, ai.ToolCall{
				Index: toolId, ID: toolId, Name: dialectkit.GetStr(m, "name"),
				ArgumentsStr: inputJson, Arguments: input,
			})
		case "redacted_thinking":
			if data := dialectkit.GetStr(m, "data"); data != "" {
				redactedList = append(redactedList, data)
			}
		}
	}

	msg := ai.Snapshot(normalContent.String(), thinkingContent.String(), toolCalls)
	if len(mediaBlocks) > 0 {
		if msg.Text != "" {
			msg.Blocks = append(msg.Blocks, ai.TextBlock{Text: msg.Text})
		}
		msg.Blocks = append(msg.Blocks, mediaBlocks...)
	}
	protocolData := map[string]any{}
	if thinkingSignature != "" {
		protocolData[stateSignature] = thinkingSignature
	}
	if len(redactedList) > 0 {
		protocolData[stateRedactedBlk] = redactedList
	}
	if state := createState(protocolData); state != nil {
		msg.ProtocolStates = map[string]any{protocolID: state}
		// 摘要在消息构造边界绑定（对标 Java snapshot 内建绑定），回放校验才有效
		ai.BindProtocolStateHashes(msg)
	}
	return []*ai.AssistantMessage{msg}
}

// ---- usage（Prompt Caching 计量）----

// parseUsage usage 节点 → ai.Usage。Anthropic 的 input_tokens 不含缓存部分，需并入
// cache 两项归一为“全部输入 token”（与 OpenAI prompt_tokens 对齐，否则 cacheRate
// 恒 100%）；包含协议字段即构建（显式 0 也有语义——累计快照修正），不能按值判断。
func parseUsage(usageNode map[string]any) *ai.Usage {
	if usageNode == nil {
		return nil
	}
	u := usageNode
	inputTokens, _ := numberOf(u["input_tokens"])
	outputTokens, _ := numberOf(u["output_tokens"])

	// 缓存写入 TTL 明细（1h 单价是 5m 两倍，只有汇总无法还原真实缓存成本）
	var cacheCreation5m, cacheCreation1h int64
	if cc := dialectkit.GetNode(u, "cache_creation"); cc != nil {
		cacheCreation5m, _ = numberOf(cc["ephemeral_5m_input_tokens"])
		cacheCreation1h, _ = numberOf(cc["ephemeral_1h_input_tokens"])
	}
	// 只给明细不给汇总的形态：由明细求和补出
	cacheCreationInputTokens := cacheCreation5m + cacheCreation1h
	if raw, has := u["cache_creation_input_tokens"]; has {
		if n, ok := numberOf(raw); ok {
			cacheCreationInputTokens = n
		}
	}
	cacheReadInputTokens, _ := numberOf(u["cache_read_input_tokens"])

	// 思考 token（thinking_tokens；兼容网关可能用 OpenAI 风格 reasoning_tokens）
	var thinkTokens int64
	if details := dialectkit.GetNode(u, "output_tokens_details"); details != nil {
		thinkTokens, _ = numberOf(details["thinking_tokens"])
		if thinkTokens == 0 {
			thinkTokens, _ = numberOf(details["reasoning_tokens"])
		}
	}

	// 服务端内置工具调用次数（按次计费，不能并入 token）
	var webSearchRequests, webFetchRequests int64
	if stu := dialectkit.GetNode(u, "server_tool_use"); stu != nil {
		webSearchRequests, _ = numberOf(stu["web_search_requests"])
		webFetchRequests, _ = numberOf(stu["web_fetch_requests"])
	}
	serviceTier := dialectkit.GetStr(u, "service_tier")
	inferenceGeo := dialectkit.GetStr(u, "inference_geo")

	totalInputTokens := inputTokens + cacheCreationInputTokens + cacheReadInputTokens

	hasUsageFields := false
	for _, key := range []string{
		"input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens",
		"cache_creation", "output_tokens_details", "server_tool_use", "service_tier", "inference_geo",
	} {
		if _, has := u[key]; has {
			hasUsageFields = true
			break
		}
	}
	if !hasUsageFields {
		return nil
	}

	usage := &ai.Usage{
		PromptTokens:               totalInputTokens,
		ThinkTokens:                thinkTokens,
		CompletionTokens:           outputTokens,
		TotalTokens:                totalInputTokens + outputTokens,
		CacheCreationInputTokens:   cacheCreationInputTokens,
		CacheReadInputTokens:       cacheReadInputTokens,
		CacheCreation5mInputTokens: cacheCreation5m,
		CacheCreation1hInputTokens: cacheCreation1h,
		WebSearchRequests:          webSearchRequests,
		WebFetchRequests:           webFetchRequests,
		ServiceTier:                serviceTier,
		InferenceGeo:               inferenceGeo,
	}
	if raw, err := json.Marshal(u); err == nil {
		usage.Source = raw
	}
	return usage
}

// mergeUsageSource 两个 usage 累计快照合并：当前帧明确携带的字段覆盖旧值（含显式
// 0），缺失字段沿用旧值——message_delta.usage 是累计快照不是增量，不能相加。
func mergeUsageSource(prev json.RawMessage, curr map[string]any) map[string]any {
	if len(prev) == 0 {
		return curr
	}
	var prevMap map[string]any
	if json.Unmarshal(prev, &prevMap) != nil || prevMap == nil {
		return curr
	}
	for k, v := range curr {
		prevMap[k] = v
	}
	return prevMap
}

// ---- 错误与小工具 ----

// parseAnthropicError error 节点 → "[type] message"；无 message 回退整块 JSON。
func parseAnthropicError(node any) error {
	if m, ok := node.(map[string]any); ok {
		typ := dialectkit.GetStr(m, "type")
		msg := dialectkit.GetStr(m, "message")
		if msg == "" {
			if raw, err := json.Marshal(m); err == nil {
				msg = string(raw)
			}
		}
		if typ != "" {
			return fmt.Errorf("[%s] %s", typ, msg)
		}
		if msg != "" {
			return errors.New(msg)
		}
		return errors.New("Anthropic API error")
	}
	return errors.New(dialectkit.ExtractErrorMessage(node))
}

// setRaw 事件附带原始帧。
func setRaw(ev *ai.ChatEvent, node map[string]any) {
	if node == nil {
		return
	}
	if data, err := json.Marshal(node); err == nil {
		ev.Raw = data
	}
}

// emitStreamDelta THINKING/TEXT 增量事件。
func emitStreamDelta(ctx *ai.StreamContext, t ai.EventType, rawType string, index int, text string, raw map[string]any) {
	ev := ctx.Event(t)
	ev.RawType = rawType
	ev.Index = index
	ev.Text = text
	setRaw(ev, raw)
	ctx.Emit(ev)
}

// emitToolCallEvent 本地工具调用事件。聚合主键统一 "idx:<blockIdx>"（Go Accumulator
// 以 Index 为首键——START/ARGS_DELTA/END 三处必须一致，否则同一调用拆成两个槽）。
func emitToolCallEvent(ctx *ai.StreamContext, t ai.EventType, rawType string, blockIdx int,
	state *streamToolState, text string, full *ai.ToolCall, raw map[string]any) {
	call := ai.ToolCall{
		Index: fmt.Sprintf("idx:%d", blockIdx),
		ID:    state.toolUseId,
		Name:  state.toolName,
	}
	if full != nil {
		call = *full
	}
	ev := ctx.Event(t)
	ev.RawType = rawType
	ev.ToolCall = &call
	ev.ToolCallID = state.toolUseId
	ev.ItemID = state.toolUseId
	if idx := dialectkit.ToolCallIndex(&call); idx >= 0 {
		ev.Index = idx
	} else {
		ev.Index = blockIdx
	}
	ev.Text = text
	setRaw(ev, raw)
	ctx.Emit(ev)
}

// appendServerToolCaller server_tool_use.caller 区分模型直调与代码执行内嵌调用
// （对象取 type/tool_id，网关简化形态为纯字符串）。
func appendServerToolCaller(ev *ai.ChatEvent, contentBlock map[string]any) {
	callerRaw, has := contentBlock["caller"]
	if !has || callerRaw == nil {
		return
	}
	ensureAttrs := func() {
		if ev.Attrs == nil {
			ev.Attrs = map[string]any{}
		}
	}
	switch caller := callerRaw.(type) {
	case string:
		if caller != "" {
			ensureAttrs()
			ev.Attrs["caller"] = caller
		}
	case map[string]any:
		if t := dialectkit.GetStr(caller, "type"); t != "" {
			ensureAttrs()
			ev.Attrs["caller"] = t
		}
		if toolId := dialectkit.GetStr(caller, "tool_id"); toolId != "" {
			ensureAttrs()
			ev.Attrs["callerToolId"] = toolId
		}
	}
}

// extractToolResultText 服务端工具结果块内的可读文本：content 并非 [{text}] 一种
// 形态——web_search 是 title/url 列表、web_fetch 是单对象、code_execution 是
// stdout/stderr，按各工具实际结构提取。
func extractToolResultText(contentBlock map[string]any) string {
	resultContent, has := contentBlock["content"]
	if !has || resultContent == nil {
		return ""
	}
	switch v := resultContent.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, item := range v {
			if s := extractResultEntryText(item); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		return extractResultEntryText(v)
	}
	return ""
}

// extractResultEntryText 单条目文本投影（text / error_code / stdout+stderr /
// title+url / tool_references / file_id）。
func extractResultEntryText(node any) string {
	m, isMap := node.(map[string]any)
	if !isMap {
		if s, isStr := node.(string); isStr {
			return s
		}
		return ""
	}
	if text := dialectkit.GetStr(m, "text"); text != "" {
		return text
	}
	if errorCode := dialectkit.GetStr(m, "error_code"); errorCode != "" {
		if errorMessage := dialectkit.GetStr(m, "error_message"); errorMessage != "" {
			return "[" + errorCode + "] " + errorMessage
		}
		return "[" + errorCode + "]"
	}
	_, hasStdout := m["stdout"]
	_, hasStderr := m["stderr"]
	if hasStdout || hasStderr {
		var parts []string
		if s := dialectkit.GetStr(m, "stdout"); s != "" {
			parts = append(parts, s)
		}
		if s := dialectkit.GetStr(m, "stderr"); s != "" {
			parts = append(parts, s)
		}
		return strings.Join(parts, "\n")
	}
	url := dialectkit.GetStr(m, "url")
	title := dialectkit.GetStr(m, "title")
	if title == "" {
		if doc, ok := m["content"].(map[string]any); ok {
			title = dialectkit.GetStr(doc, "title")
		}
	}
	if title != "" && url != "" {
		return title + " - " + url
	}
	if title != "" {
		return title
	}
	if url != "" {
		return url
	}
	if refs, ok := m["tool_references"].([]any); ok {
		var names []string
		for _, r := range refs {
			rm, _ := r.(map[string]any)
			if name := dialectkit.GetStr(rm, "tool_name"); name != "" {
				names = append(names, name)
			}
		}
		if len(names) > 0 {
			return strings.Join(names, ", ")
		}
	}
	return dialectkit.GetStr(m, "file_id")
}
