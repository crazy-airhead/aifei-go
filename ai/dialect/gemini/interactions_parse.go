// interactions 响应解析（对标 GeminiInteractionsResponseParser）：
// 流式按 event_type 帧协议（interaction.created/step.start/delta/stop/
// status_update/completed/error），非流式为整帧 steps[] 数组。跨帧状态挂
// StreamContext attrs（并发流隔离）。
package gemini

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/internal/dialectkit"
)

// ctx attr 键（跨帧状态；挂 ctx 不挂方言实例——并发流不共享）。
const (
	attrStepAccumulators = "__geminiInteractionsStepAcc"
	attrSignatureBound   = "__geminiInteractionsSignatureBound"
)

// InteractionStepType step 类型（17 值 apiValue）。
type InteractionStepType string

const (
	stepUserInput         InteractionStepType = "user_input"
	stepModelOutput       InteractionStepType = "model_output"
	stepThought           InteractionStepType = "thought"
	stepFunctionCall      InteractionStepType = "function_call"
	stepFunctionResult    InteractionStepType = "function_result"
	stepGoogleSearchCall  InteractionStepType = "google_search_call"
	stepGoogleSearchResul InteractionStepType = "google_search_result"
	stepCodeExecCall      InteractionStepType = "code_execution_call"
	stepCodeExecResult    InteractionStepType = "code_execution_result"
	stepURLContextCall    InteractionStepType = "url_context_call"
	stepURLContextResult  InteractionStepType = "url_context_result"
	stepMCPCall           InteractionStepType = "mcp_server_tool_call"
	stepMCPResult         InteractionStepType = "mcp_server_tool_result"
	stepFileSearchCall    InteractionStepType = "file_search_call"
	stepFileSearchResult  InteractionStepType = "file_search_result"
	stepMapsCall          InteractionStepType = "google_maps_call"
	stepMapsResult        InteractionStepType = "google_maps_result"
)

// interactionStepTypeOf 未知值返回空串。
func interactionStepTypeOf(v string) InteractionStepType {
	switch InteractionStepType(v) {
	case stepUserInput, stepModelOutput, stepThought, stepFunctionCall, stepFunctionResult,
		stepGoogleSearchCall, stepGoogleSearchResul, stepCodeExecCall, stepCodeExecResult,
		stepURLContextCall, stepURLContextResult, stepMCPCall, stepMCPResult,
		stepFileSearchCall, stepFileSearchResult, stepMapsCall, stepMapsResult:
		return InteractionStepType(v)
	}
	return ""
}

// isServerToolStep 服务端工具判定（非五类基础 step 即服务端工具，走三态事件）。
func isServerToolStep(t InteractionStepType) bool {
	switch t {
	case stepUserInput, stepModelOutput, stepThought, stepFunctionCall, stepFunctionResult, "":
		return false
	}
	return true
}

// stepAccumulator 单 step 跨帧累积器。
type stepAccumulator struct {
	stepType   InteractionStepType
	functionName string
	callID     string
	arguments  strings.Builder
	emittedLen int
	searchKeys map[string]bool
	startSent  bool
}

type stepAccTable map[int]*stepAccumulator

// parseInteractionsResponse 响应唯一入口。
func parseInteractionsResponse(ctx *ai.StreamContext, body string) {
	acc := ctx.Acc()
	if body == "" {
		return
	}
	if ctx.IsStream() {
		for _, frame := range sseFrames(body) {
			if frame == "[DONE]" {
				continue // interactions 协议无 [DONE]；容错跳过
			}
			root := parseFrameObject(frame)
			if root == nil {
				continue
			}
			if applyInteractionsError(ctx, acc, root) {
				return
			}
			applyInteractionsStreamEvent(ctx, acc, root)
		}
		return
	}
	root := parseFrameObject(body)
	if root == nil {
		return
	}
	if applyInteractionsError(ctx, acc, root) {
		return
	}
	applyInteractionsNonStream(ctx, acc, root)
}

// applyInteractionsError 顶层 error 帧：error + finished + finishReason + ERROR
// 事件（error 是终态）。
func applyInteractionsError(ctx *ai.StreamContext, acc *ai.Accumulator, root map[string]any) bool {
	errNode, has := root["error"]
	if !has || errNode == nil {
		return false
	}
	msg := dialectkit.ExtractErrorMessage(errNode)
	acc.SetError(strErr(msg))
	acc.SetFinished(true)
	acc.SetFinishReason("error")
	dialectkit.EmitError(ctx, acc.Err(), root)
	return true
}

func strErr(s string) error { return &interactionsError{s} }

type interactionsError struct{ msg string }

func (e *interactionsError) Error() string { return e.msg }

// applyInteractionsStreamEvent 流式帧分派。
func applyInteractionsStreamEvent(ctx *ai.StreamContext, acc *ai.Accumulator, root map[string]any) {
	switch dialectkit.GetStr(root, "event_type") {
	case "interaction.created":
		table := stepTableOf(ctx)
		for k := range table {
			delete(table, k)
		}
		ctx.AttrPut(attrSignatureBound, false)
		if interaction, ok := root["interaction"].(map[string]any); ok {
			if m := dialectkit.GetStr(interaction, "model"); m != "" {
				acc.SetModel(m)
			}
			ctx.SetProviderResponseID(responseIDOf(root))
		}
		ev := ctx.Event(ai.EventStatus)
		ev.RawType = "interaction.created"
		ev.ItemID = responseIDOf(root)
		ctx.Emit(ev)

	case "step.start":
		applyStepStart(ctx, acc, root)

	case "step.delta":
		applyStepDelta(ctx, acc, root)

	case "step.stop":
		applyStepStop(ctx, acc, root)

	case "interaction.status_update":
		node, _ := root["interaction"].(map[string]any)
		if node == nil {
			node = root
		}
		ctx.SetProviderResponseID(responseIDOf(root))
		applyInteractionStatus(ctx, acc, node)
		ev := ctx.Event(ai.EventStatus)
		ev.RawType = "interaction.status_update"
		ctx.Emit(ev)

	case "interaction.completed":
		ctx.SetProviderResponseID(responseIDOf(root))
		node, _ := root["interaction"].(map[string]any)
		if node == nil {
			node = root
		}
		terminal := applyInteractionStatus(ctx, acc, node)
		ev := ctx.Event(ai.EventStatus)
		ev.RawType = "interaction.completed"
		if u := interactionsUsageOf(node["usage"]); u != nil {
			ev.Usage = u
		}
		ctx.Emit(ev)
		_ = terminal

	default:
		// 未知 event_type → RAW（rawType=事件名，raw=整帧）
		ev := ctx.Event(ai.EventRaw)
		ev.RawType = dialectkit.GetStr(root, "event_type")
		ev.Raw = mustJSON(root)
		ctx.Emit(ev)
	}
}

// responseIDOf data.id 优先，否则 interaction.id。
func responseIDOf(root map[string]any) string {
	if id := dialectkit.GetStr(root, "id"); id != "" {
		return id
	}
	if interaction, ok := root["interaction"].(map[string]any); ok {
		return dialectkit.GetStr(interaction, "id")
	}
	return ""
}

// applyInteractionStatus 终态 status 处理：completed 等六值为终态（finished +
// finishReason 映射；failed 取 error.message）。
func applyInteractionStatus(ctx *ai.StreamContext, acc *ai.Accumulator, node map[string]any) bool {
	status := dialectkit.GetStr(node, "status")
	reason, terminal := interactionsFinishReason(status)
	if !terminal {
		return false
	}
	acc.SetFinished(true)
	if reason != "" {
		acc.SetFinishReason(reason)
	}
	if status == "failed" {
		msg := "Gemini interaction failed"
		if e, ok := node["error"].(map[string]any); ok {
			if s := dialectkit.GetStr(e, "message"); s != "" {
				msg = s
			}
		}
		acc.SetError(strErr(msg))
		acc.SetFinishReason("error")
	}
	return true
}

// interactionsFinishReason status → finishReason（六终态）。
func interactionsFinishReason(status string) (string, bool) {
	switch status {
	case "completed":
		return "stop", true
	case "requires_action":
		return "tool_calls", true
	case "failed":
		return "error", true
	case "cancelled":
		return "cancelled", true
	case "incomplete":
		return "incomplete", true
	case "budget_exceeded":
		return "length", true
	}
	return "", false
}

// interactionsUsageOf usage 映射：prompt=total_input+total_tool_use、think=
// total_thought、completion=total_output、total=total_tokens、cacheRead=
// total_cached、webSearch=Σ search 类 grounding 计数。
func interactionsUsageOf(v any) *ai.Usage {
	node, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	u := &ai.Usage{
		PromptTokens:     getInt(node, "total_input_tokens") + getInt(node, "total_tool_use_tokens"),
		ThinkTokens:      getInt(node, "total_thought_tokens"),
		CompletionTokens: getInt(node, "total_output_tokens"),
		TotalTokens:      getInt(node, "total_tokens"),
		CacheReadInputTokens: getInt(node, "total_cached_tokens"),
	}
	if counts, ok := node["grounding_tool_count"].([]any); ok {
		for _, c := range counts {
			if m, ok := c.(map[string]any); ok {
				if strings.Contains(strings.ToLower(dialectkit.GetStr(m, "type")), "search") {
					u.WebSearchRequests += getInt(m, "count")
				}
			}
		}
	}
	if raw, err := json.Marshal(node); err == nil {
		u.Source = raw
	}
	return u
}

func getInt(m map[string]any, k string) int64 {
	switch v := m[k].(type) {
	case float64:
		return int64(v)
	case int:
		return int64(v)
	case int64:
		return v
	case string:
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	}
	return 0
}

// ---- step 生命周期 ----

func stepTableOf(ctx *ai.StreamContext) stepAccTable {
	return ctx.AttrIfAbsent(attrStepAccumulators, func() any { return stepAccTable{} }).(stepAccTable)
}

func signatureBoundOf(ctx *ai.StreamContext) *bool {
	return ctx.AttrIfAbsent(attrSignatureBound, func() any { b := false; return &b }).(*bool)
}

// applyStepStart step.start：登记累积器 + 首块内容 + 服务端工具三态之 START。
func applyStepStart(ctx *ai.StreamContext, acc *ai.Accumulator, root map[string]any) {
	step, _ := root["step"].(map[string]any)
	if step == nil {
		return
	}
	idx := int(getInt(root, "index"))
	table := stepTableOf(ctx)

	typeStr := dialectkit.GetStr(step, "type")
	st := interactionStepTypeOf(typeStr)
	if st == "" {
		// 未知 step type：登记累积器但发 RAW，不产生内容/工具事件
		table[idx] = &stepAccumulator{stepType: "", searchKeys: map[string]bool{}}
		ev := ctx.Event(ai.EventRaw)
		ev.RawType = "step.start"
		ev.SubType = typeStr
		ev.Raw = mustJSON(root)
		ctx.Emit(ev)
		return
	}

	sa := &stepAccumulator{stepType: st, searchKeys: map[string]bool{}}
	sa.functionName = dialectkit.GetStr(step, "name")
	sa.callID = firstNonEmptyStr(dialectkit.GetStr(step, "id"), dialectkit.GetStr(step, "call_id"))
	table[idx] = sa

	switch st {
	case stepThought:
		acc.SetInThinking(true)
		if s := summaryText(step["summary"]); s != "" {
			emitThinkingDelta(ctx, s)
		}
	case stepModelOutput:
		for _, item := range parseInteractionContentItems(step["content"]) {
			if item.text != "" {
				emitTextDelta(ctx, item.text)
			}
			if item.block != nil {
				emitMediaDone(ctx, "step:"+strconv.Itoa(idx), item.block)
			}
		}
	case stepFunctionCall:
		mergeStepIdentity(sa, step)
		if v, ok := step["arguments"]; ok {
			sa.arguments.WriteString(argumentsStringOf(v))
		}
		emitFunctionStartIfReady(ctx, sa, idx, false)
		emitPendingFunctionArguments(ctx, sa, idx)
	case stepGoogleSearchResul:
		emitGoogleSearchResults(ctx, sa, step)
	}

	emitStepServerToolEvent(ctx, ai.EventServerToolStart, "step.start", st, sa, idx, root)
}

// applyStepDelta step.delta：thought_signature 先于累积器检查（acc 级跨帧状态）。
func applyStepDelta(ctx *ai.StreamContext, acc *ai.Accumulator, root map[string]any) {
	delta, _ := root["delta"].(map[string]any)
	if delta == nil {
		return
	}
	if dt := dialectkit.GetStr(delta, "type"); dt == "thought_signature" {
		acc.SetThinkingSignature(dialectkit.GetStr(delta, "signature"))
		ev := ctx.Event(ai.EventThinkingSignature)
		ev.RawType = "step.delta"
		ev.Text = acc.ThinkingSignature()
		ctx.Emit(ev)
		return
	}

	idx := int(getInt(root, "index"))
	table := stepTableOf(ctx)
	sa := table[idx]
	if sa == nil {
		return
	}
	step, _ := root["step"].(map[string]any)

	if sa.stepType == stepGoogleSearchResul {
		if step != nil {
			emitGoogleSearchResults(ctx, sa, step)
		}
		emitGoogleSearchResults(ctx, sa, delta)
	}
	if sa.stepType == stepFunctionCall && step != nil {
		mergeStepIdentity(sa, step)
		emitFunctionStartIfReady(ctx, sa, idx, false)
	}

	switch dialectkit.GetStr(delta, "type") {
	case "text":
		if sa.stepType == stepModelOutput {
			acc.SetInThinking(false)
			if s := dialectkit.GetStr(delta, "text"); s != "" {
				emitTextDelta(ctx, s)
			}
		}
	case "thought_summary":
		text := contentItemsText(coalesce(delta["content"], delta["summary"]))
		if text != "" {
			acc.SetInThinking(true)
			emitThinkingDelta(ctx, text)
		}
	case "image", "audio", "video":
		if item := parseInteractionContentItem(delta); item != nil && item.block != nil {
			emitMediaDone(ctx, "step:"+strconv.Itoa(idx), item.block)
		}
	case "document":
		ev := ctx.Event(ai.EventRaw)
		ev.RawType = "step.delta"
		ev.SubType = "document"
		ev.Raw = mustJSON(root)
		ctx.Emit(ev)
	case "arguments_delta":
		if sa.stepType == stepFunctionCall {
			sa.arguments.WriteString(dialectkit.GetStr(delta, "arguments"))
			emitFunctionStartIfReady(ctx, sa, idx, false)
			emitPendingFunctionArguments(ctx, sa, idx)
		}
	}

	emitStepServerToolEvent(ctx, ai.EventServerToolArgsDelta, "step.delta", sa.stepType, sa, idx, root)
}

// applyStepStop step.stop：服务端工具 RESULT 先行；function_call 冲刷与签名
// 绑定；永不发 TOOL_CALL_END（END 由 core 唯一收尾）。
func applyStepStop(ctx *ai.StreamContext, acc *ai.Accumulator, root map[string]any) {
	idx := int(getInt(root, "index"))
	table := stepTableOf(ctx)
	sa := table[idx]
	step, _ := root["step"].(map[string]any)

	if sa != nil {
		emitStepServerToolEvent(ctx, ai.EventServerToolResult, "step.stop", sa.stepType, sa, idx, root)
		delete(table, idx)

		switch sa.stepType {
		case stepGoogleSearchResul:
			if step != nil {
				emitGoogleSearchResults(ctx, sa, step)
			}
		case stepFunctionCall:
			if step != nil {
				mergeStepIdentity(sa, step)
			}
			emitFunctionStartIfReady(ctx, sa, idx, true)
			emitPendingFunctionArguments(ctx, sa, idx)
			// 签名绑定：thinkingSignature 非空且未绑定 → 协议状态挂终态
			if sig := acc.ThinkingSignature(); sig != "" && !*signatureBoundOf(ctx) {
				*signatureBoundOf(ctx) = true
				call := &ai.ToolCall{Index: "idx:" + strconv.Itoa(idx), ID: sa.callID, Name: sa.functionName}
				if state := createSignatureState(call, 0, sig); state != nil {
					acc.PutTerminalProtocolState(interactionsProtocolID, state)
				}
			}
		}
	} else if step != nil {
		// 无累积器（未见过 start 的孤立 stop）：仅服务端工具三态收尾
		st := interactionStepTypeOf(dialectkit.GetStr(step, "type"))
		if isServerToolStep(st) {
			sa = &stepAccumulator{stepType: st, searchKeys: map[string]bool{}}
			sa.callID = firstNonEmptyStr(dialectkit.GetStr(step, "id"), dialectkit.GetStr(step, "call_id"))
			emitStepServerToolEvent(ctx, ai.EventServerToolResult, "step.stop", st, sa, idx, root)
		}
	}
	acc.SetInThinking(false)
}

// emitStepServerToolEvent 仅服务端工具 step 发三态事件（start/delta/stop 各一）。
func emitStepServerToolEvent(ctx *ai.StreamContext, t ai.EventType, rawType string, st InteractionStepType, sa *stepAccumulator, idx int, root map[string]any) {
	if !isServerToolStep(st) {
		return
	}
	ev := ctx.Event(t)
	ev.RawType = rawType
	ev.SubType = string(st)
	if sa != nil {
		ev.ItemID = sa.callID
	}
	ev.Index = idx
	ev.Raw = mustJSON(root)
	ctx.Emit(ev)
}

// mergeStepIdentity 合并 step 身份（name 与 id/call_id）。
func mergeStepIdentity(sa *stepAccumulator, step map[string]any) {
	if name := dialectkit.GetStr(step, "name"); name != "" && sa.functionName == "" {
		sa.functionName = name
	}
	if id := firstNonEmptyStr(dialectkit.GetStr(step, "id"), dialectkit.GetStr(step, "call_id")); id != "" && sa.callID == "" {
		sa.callID = id
	}
}

// argumentsStringOf arguments 值 → 字符串（object 序列化；其余原样）。
func argumentsStringOf(v any) string {
	switch args := v.(type) {
	case map[string]any:
		return string(mustJSON(args))
	case string:
		return args
	default:
		return ""
	}
}

// emitFunctionStartIfReady START 就绪判定（name 非空且 id 非空；stop 阶段允许
// name_index 回退——id 为空时用 name）。
func emitFunctionStartIfReady(ctx *ai.StreamContext, sa *stepAccumulator, idx int, allowNameFallback bool) {
	if sa.functionName == "" || sa.startSent {
		return
	}
	if sa.callID == "" {
		if !allowNameFallback {
			return
		}
	}
	sa.startSent = true
	ev := ctx.Event(ai.EventToolCallStart)
	ev.ToolCall = &ai.ToolCall{
		Index: "idx:" + strconv.Itoa(idx),
		ID:    sa.callID,
		Name:  sa.functionName,
	}
	ctx.Emit(ev)
}

// emitPendingFunctionArguments 只发尚未发过的参数增量（游标 emittedLen）。
// ARGS_DELTA 携带同 Index 的 ToolCall 载体（聚合槽键对齐，chat 方言同款）。
func emitPendingFunctionArguments(ctx *ai.StreamContext, sa *stepAccumulator, idx int) {
	if !sa.startSent {
		return
	}
	full := sa.arguments.String()
	if len(full) > sa.emittedLen {
		chunk := full[sa.emittedLen:]
		sa.emittedLen = len(full)
		if chunk != "" {
			ev := ctx.Event(ai.EventToolCallArgsDelta)
			ev.ToolCall = &ai.ToolCall{Index: "idx:" + strconv.Itoa(idx), ID: sa.callID, Name: sa.functionName}
			ev.ToolCallID = sa.callID
			ev.Text = chunk
			ev.Index = idx
			ctx.Emit(ev)
		}
	}
}

// ---- 内容项 ----

type interactionContentItem struct {
	text  string
	block ai.ContentBlock
}

// parseInteractionContentItems 单 object 或数组皆可。
func parseInteractionContentItems(v any) []interactionContentItem {
	if arr, ok := v.([]any); ok {
		var out []interactionContentItem
		for _, item := range arr {
			if m, ok := item.(map[string]any); ok {
				if ci := parseInteractionContentItem(m); ci != nil {
					out = append(out, *ci)
				}
			}
		}
		return out
	}
	if m, ok := v.(map[string]any); ok {
		if ci := parseInteractionContentItem(m); ci != nil {
			return []interactionContentItem{*ci}
		}
	}
	return nil
}

// parseInteractionContentItem 宽容内容项：text 键或 type=text → 文本；
// image/audio/video（data/uri）；inline_data 包装；file_data/file_uri 包装。
func parseInteractionContentItem(m map[string]any) *interactionContentItem {
	typeStr := dialectkit.GetStr(m, "type")
	if _, hasText := m["text"]; hasText || typeStr == "text" {
		if s := dialectkit.GetStr(m, "text"); s != "" {
			return &interactionContentItem{text: s}
		}
		return nil
	}
	switch typeStr {
	case "image", "audio", "video":
		defMime := map[string]string{"image": "image/jpeg", "audio": "audio/mpeg", "video": "video/mp4"}[typeStr]
		mime := firstNonEmptyStr(getAnyStr(m, "mime_type", "mimeType"), defMime)
		if block := createMediaByMime(mime, getAnyStr(m, "uri"), dialectkit.GetStr(m, "data")); block != nil {
			return &interactionContentItem{block: block}
		}
		return nil
	case "inline_data":
		if inner, ok := m["inline_data"].(map[string]any); ok {
			return parseInteractionContentItem(inner)
		}
		if inner, ok := m["inlineData"].(map[string]any); ok {
			return parseInteractionContentItem(inner)
		}
		return nil
	case "file_data":
		if inner, ok := coalesceMap(m["file_data"], m["fileData"]); ok {
			return parseInteractionContentItem(inner)
		}
		return nil
	}
	// 无 type 但有包装键
	if inner, ok := coalesceMap(m["inline_data"], m["inlineData"]); ok {
		return parseInteractionContentItem(inner)
	}
	if uri := getAnyStr(m, "file_uri", "fileUri", "file_data", "fileData"); uri != "" {
		return &interactionContentItem{block: createMediaByMime(getAnyStr(m, "mime_type", "mimeType"), uri, "")}
	}
	return nil
}

// summaryText summary 数组的文本拼接。
func summaryText(v any) string { return contentItemsText(v) }

// contentItemsText content/summary 数组的文本拼接。
func contentItemsText(v any) string {
	var sb strings.Builder
	for _, item := range parseInteractionContentItems(v) {
		sb.WriteString(item.text)
	}
	return sb.String()
}

// emitGoogleSearchResults 逐项网页结果投影（start/stop 重放同 step 去重）。
func emitGoogleSearchResults(ctx *ai.StreamContext, sa *stepAccumulator, node map[string]any) {
	items := searchResultArrayOf(node)
	for pos, v := range items {
		item, ok := v.(map[string]any)
		if !ok {
			continue
		}
		source := item
		if web, ok := item["web"].(map[string]any); ok {
			source = web
		}
		url := firstNonEmptyStr(dialectkit.GetStr(source, "url"), dialectkit.GetStr(source, "uri"))
		if url == "" {
			continue // 仅 search_suggestions 等生命周期载荷不伪装成结果
		}
		idx := getInt(item, "index")
		if idx == 0 {
			if n := getInt(source, "index"); n != 0 {
				idx = n
			} else {
				idx = int64(pos)
			}
		}
		id := firstNonEmptyStr(dialectkit.GetStr(item, "id"), dialectkit.GetStr(source, "id"))
		key := "index:" + strconv.FormatInt(idx, 10) + ":id:" + id + "|url:" + url
		if sa.searchKeys[key] {
			continue
		}
		sa.searchKeys[key] = true

		snippet := firstNonEmptyStr(dialectkit.GetStr(source, "snippet"),
			dialectkit.GetStr(source, "summary"), dialectkit.GetStr(source, "description"))
		ev := ctx.Event(ai.EventSearchResult)
		ev.SearchResult = &ai.SearchResult{
			Index: int(idx), ID: id,
			Title:   firstNonEmptyStr(dialectkit.GetStr(source, "title"), dialectkit.GetStr(item, "title")),
			URL:     url,
			Snippet: snippet,
		}
		ev.SubType = "google_search_result"
		ev.Text = url
		ev.Raw = mustJSON(item)
		ctx.Emit(ev)
	}
}

// searchResultArrayOf result|results|search_results 找数组（object 再入其
// results|search_results|items）。
func searchResultArrayOf(node map[string]any) []any {
	for _, key := range []string{"result", "results", "search_results"} {
		if v, ok := node[key]; ok {
			switch arr := v.(type) {
			case []any:
				return arr
			case map[string]any:
				for _, inner := range []string{"results", "search_results", "items"} {
					if a, ok := arr[inner].([]any); ok {
						return a
					}
				}
			}
		}
	}
	return nil
}

// ---- 非流式 ----

// applyInteractionsNonStream 非流式整帧：id/model/status → steps[] 遍历 →
// 唯一终态 AssistantMessage + 签名状态 + usage（仅 finished 时）。
func applyInteractionsNonStream(ctx *ai.StreamContext, acc *ai.Accumulator, root map[string]any) {
	if id := dialectkit.GetStr(root, "id"); id != "" {
		ctx.SetProviderResponseID(id)
	}
	if m := dialectkit.GetStr(root, "model"); m != "" {
		acc.SetModel(m)
	}
	if applyInteractionStatus(ctx, acc, root) {
		if err := acc.Err(); err != nil {
			dialectkit.EmitError(ctx, err, root)
			return
		}
	}

	var text, thinking strings.Builder
	var toolCalls []ai.ToolCall
	var blocks []ai.ContentBlock
	var firstSig string

	if arr, ok := root["steps"].([]any); ok {
		for _, v := range arr {
			step, ok := v.(map[string]any)
			if !ok {
				continue
			}
			st := interactionStepTypeOf(dialectkit.GetStr(step, "type"))
			switch st {
			case stepThought:
				sig := dialectkit.GetStr(step, "signature")
				if sig != "" && firstSig == "" {
					firstSig = sig
				}
				if sig != "" {
					acc.SetThinkingSignature(sig)
					ev := ctx.Event(ai.EventThinkingSignature)
					ev.RawType = "thought"
					ev.Text = sig
					ctx.Emit(ev)
				}
				if s := summaryText(step["summary"]); s != "" {
					thinking.WriteString(s)
				}
			case stepModelOutput:
				for _, item := range parseInteractionContentItems(step["content"]) {
					if item.text != "" {
						text.WriteString(item.text)
					}
					if item.block != nil {
						blocks = append(blocks, item.block)
						emitMediaDone(ctx, "", item.block)
					}
				}
			case stepFunctionCall:
				name := dialectkit.GetStr(step, "name")
				id := dialectkit.GetStr(step, "id")
				if id == "" {
					id = name
				}
				argsStrB, argsMap := stepArguments(step["arguments"])
				call := ai.ToolCall{Index: id, ID: id, Name: name, ArgumentsStr: string(argsStrB), Arguments: argsMap}
				toolCalls = append(toolCalls, call)
				// 旧网关兼容：首个调用带 thought_signature 字段
				if len(toolCalls) == 1 && firstSig == "" {
					if sig := dialectkit.GetStr(step, "thought_signature"); sig != "" {
						firstSig = sig
						acc.SetThinkingSignature(sig)
						ev := ctx.Event(ai.EventThinkingSignature)
						ev.RawType = "thought"
						ev.Text = sig
						ctx.Emit(ev)
					}
				}
			default:
				// 服务端工具 CALL → START 快照；RESULT → RESULT；SEARCH 额外投影
				if st == "" {
					ev := ctx.Event(ai.EventRaw)
					ev.RawType = "step"
					ev.SubType = dialectkit.GetStr(step, "type")
					ev.Raw = mustJSON(step)
					ctx.Emit(ev)
					continue
				}
				callID := firstNonEmptyStr(dialectkit.GetStr(step, "id"), dialectkit.GetStr(step, "call_id"))
				switch {
				case strings.HasSuffix(string(st), "_call"):
					ev := ctx.Event(ai.EventServerToolStart)
					ev.RawType = "step"
					ev.SubType = string(st)
					ev.ItemID = callID
					ctx.Emit(ev)
				case strings.HasSuffix(string(st), "_result"):
					ev := ctx.Event(ai.EventServerToolResult)
					ev.RawType = "step"
					ev.SubType = string(st)
					ev.ItemID = callID
					ctx.Emit(ev)
				}
				if st == stepGoogleSearchResul {
					emitGoogleSearchResults(ctx, &stepAccumulator{searchKeys: map[string]bool{}}, step)
				}
			}
		}
	}

	if text.Len() > 0 || len(toolCalls) > 0 || thinking.Len() > 0 || len(blocks) > 0 {
		msg := &ai.AssistantMessage{
			Text: text.String(), Thinking: thinking.String(),
			ToolCalls: toolCalls, Blocks: blocks,
		}
		if len(toolCalls) > 0 && firstSig != "" {
			if state := createSignatureState(&toolCalls[0], 0, firstSig); state != nil {
				msg.ProtocolStates = map[string]any{interactionsProtocolID: state}
			}
		}
		acc.SetTerminalMessage(msg)
	}
	acc.SetInThinking(false)
	if acc.IsFinished() {
		if u := interactionsUsageOf(root["usage"]); u != nil {
			acc.SetUsage(u)
		}
	}
}

// ---- 事件发射小件 ----

func emitTextDelta(ctx *ai.StreamContext, s string) {
	ev := ctx.Event(ai.EventTextDelta)
	ev.Text = s
	ctx.Emit(ev)
}

func emitThinkingDelta(ctx *ai.StreamContext, s string) {
	ev := ctx.Event(ai.EventThinkingDelta)
	ev.Text = s
	ctx.Emit(ev)
}

func emitMediaDone(ctx *ai.StreamContext, itemID string, block ai.ContentBlock) {
	ev := ctx.Event(ai.EventMediaDone)
	ev.ItemID = itemID
	ev.Block = block
	ctx.Emit(ev)
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func coalesce(vals ...any) any {
	for _, v := range vals {
		if v != nil {
			return v
		}
	}
	return nil
}

func coalesceMap(vals ...any) (map[string]any, bool) {
	for _, v := range vals {
		if m, ok := v.(map[string]any); ok {
			return m, true
		}
	}
	return nil, false
}

func getAnyStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s := dialectkit.GetStr(m, k); s != "" {
			return s
		}
	}
	return ""
}
