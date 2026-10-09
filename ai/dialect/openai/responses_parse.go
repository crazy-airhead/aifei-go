package openai

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai"
)

// 方言私有状态键（StreamContext attrs；单请求生命周期）。
const (
	streamStateKey            = "OpenaiResponsesStreamState"
	responsesTerminalKey      = "ResponsesTerminalStatus"
	responsesSignatureKeysKey = "ResponsesReasoningSignatureKeys"
	responsesStartedCallsKey  = "OpenaiResponsesStartedToolCalls"
	defaultResponsesStateKey  = "__default__"
)

// responsesReplayableItemTypes 可回放的 output item 类型（逐帧判定热路径，静态集合）。
var responsesReplayableItemTypes = map[string]struct{}{
	"message": {}, "file_search_call": {}, "computer_call": {}, "computer_call_output": {},
	"web_search_call": {}, "function_call": {}, "function_call_output": {}, "tool_search_call": {},
	"tool_search_output": {}, "additional_tools": {}, "reasoning": {}, "compaction": {},
	"image_generation_call": {}, "code_interpreter_call": {}, "local_shell_call": {},
	"local_shell_call_output": {}, "shell_call": {}, "shell_call_output": {}, "apply_patch_call": {},
	"apply_patch_call_output": {}, "mcp_list_tools": {}, "mcp_approval_request": {},
	"mcp_approval_response": {}, "mcp_call": {}, "custom_tool_call_output": {}, "custom_tool_call": {},
	"program": {}, "program_output": {},
}

// responsesServerToolPrefixes 服务端工具事件前缀（由模型服务方执行，不经本地工具链）。
var responsesServerToolPrefixes = []string{
	"response.web_search_call", "response.file_search_call", "response.code_interpreter_call",
	"response.computer_call", "response.mcp_call", "response.mcp_call_arguments",
	"response.mcp_list_tools", "response.image_generation_call", "response.shell_call",
	"response.local_shell_call", "response.custom_tool_call_input",
}

// ---- 快照增量归一（Java openai SnapshotDeltaNormalizer 镜像）----

const responsesMinMatchLength = 8

// responsesNormalizer 官方 delta（追加）与兼容网关累计快照（后缀归一）双模式判定：
// 累积达最短匹配长度且新帧以已累积文本为前缀、且更长 → 判定快照流（粘性）。
type responsesNormalizer struct {
	accumulated  strings.Builder
	snapshotMode bool
}

// normalize 归一化一帧：返回本帧真正的新增文本（快照重复帧返回空串），新增部分记入累积。
func (n *responsesNormalizer) normalize(value string) string {
	if value == "" {
		return value
	}
	delta := value
	if n.accumulated.Len() > 0 {
		if n.snapshotMode {
			if n.startsWithAccumulated(value) {
				delta = value[n.accumulated.Len():] // 完全相等 → ""
			}
		} else if n.accumulated.Len() >= responsesMinMatchLength &&
			len(value) > n.accumulated.Len() && n.startsWithAccumulated(value) {
			n.snapshotMode = true
			delta = value[n.accumulated.Len():]
		}
	}
	n.accumulated.WriteString(delta)
	return delta
}

// append 不做判定地直接记入累积（官方独有事件：不存在快照实现，无需承担误判风险，
// 但仍要参与后续通道的累积基准）。
func (n *responsesNormalizer) append(value string) {
	if value != "" {
		n.accumulated.WriteString(value)
	}
}

func (n *responsesNormalizer) startsWithAccumulated(value string) bool {
	if value == "" {
		return false
	}
	return strings.HasPrefix(value, n.accumulated.String())
}

// ---- 流式按请求隔离状态 ----

// responsesItemState 单个 output item 的字段级交付状态。
// Java 以 StreamState 扁平字段 + ItemSnapshot 保存/恢复实现 item 间切换；
// Go 直接持指针——活跃即 itemStates 中同一对象，切换零拷贝、语义等价。
type responsesItemState struct {
	itemId, itemType, phase         string
	textContent, reasoningContent   *responsesNormalizer
	textContents                    map[string]*responsesNormalizer
	reasoningContents               map[string]*responsesNormalizer
	deliveredTextContents           map[string]*strings.Builder
	deliveredReasoningContents      map[string]*strings.Builder
	emittedFunctionCalls            map[string]struct{}
	emittedMediaItems               map[string]struct{}
	emittedPartialImages            map[string]struct{}
	emittedRefusalParts             map[string]struct{}
	functionCallId, functionName    string
	functionArguments               *strings.Builder
	functionArgumentsEmitted        bool
	reasoningId, reasoningEncrypted string
}

func newResponsesItemState() *responsesItemState {
	return &responsesItemState{
		textContents:               map[string]*responsesNormalizer{},
		reasoningContents:          map[string]*responsesNormalizer{},
		deliveredTextContents:      map[string]*strings.Builder{},
		deliveredReasoningContents: map[string]*strings.Builder{},
		emittedFunctionCalls:       map[string]struct{}{},
		emittedMediaItems:          map[string]struct{}{},
		emittedPartialImages:       map[string]struct{}{},
		emittedRefusalParts:        map[string]struct{}{},
	}
}

// responsesStreamState 一次请求的流式状态：item 状态表 + 跨 item 全局幂等集
// （key 已含完整 item 身份，刻意不随 item 切换保存/恢复）+ 音频旁路缓冲。
type responsesStreamState struct {
	active         *responsesItemState
	activeStateKey string
	itemStates     map[string]*responsesItemState

	emittedAnnotations    map[string]struct{}
	emittedServerToolItem map[string]struct{}
	outputItemAliases     map[int]string

	audioData       strings.Builder
	audioTranscript strings.Builder
	audioDelivered  bool
}

func newResponsesStreamState() *responsesStreamState {
	return &responsesStreamState{
		itemStates:            map[string]*responsesItemState{},
		emittedAnnotations:    map[string]struct{}{},
		emittedServerToolItem: map[string]struct{}{},
		outputItemAliases:     map[int]string{},
	}
}

func responsesStreamStateOf(ctx *ai.StreamContext) *responsesStreamState {
	st, _ := ctx.AttrIfAbsent(streamStateKey, func() any { return newResponsesStreamState() }).(*responsesStreamState)
	return st
}

// current 活跃 item 状态（无则按默认键懒建——与 Java activateItemState 的
// DEFAULT 兜底等价）。
func (st *responsesStreamState) current() *responsesItemState {
	if st.active == nil {
		st.active = newResponsesItemState()
		st.activeStateKey = defaultResponsesStateKey
		st.itemStates[st.activeStateKey] = st.active
	}
	return st.active
}

// stateByKey 按 item 键取状态（活跃键命中或已保存快照）。
func (st *responsesStreamState) stateByKey(key string) *responsesItemState {
	if st.active != nil && key == st.activeStateKey {
		return st.active
	}
	return st.itemStates[key]
}

// activate 定位/迁移 item 状态键（id 后到时把 index 键迁移到 id 键，
// 并登记 outputIndex → idKey 别名）。
func (st *responsesStreamState) activate(itemId string, outputIndex int) {
	idKey, indexKey := "", ""
	if itemId != "" {
		idKey = "id:" + itemId
	}
	if outputIndex >= 0 {
		indexKey = "index:" + itoa(outputIndex)
	}
	if idKey != "" && outputIndex >= 0 {
		st.outputItemAliases[outputIndex] = idKey
		if _, hasID := st.itemStates[idKey]; !hasID {
			if _, hasIdx := st.itemStates[indexKey]; hasIdx {
				st.itemStates[idKey] = st.itemStates[indexKey]
				delete(st.itemStates, indexKey)
			}
		}
		if st.activeStateKey == indexKey && st.active != nil {
			// 当前活跃项首次拿到 id：活跃增量随指针整体迁到 id 键（Java 先落盘再迁移）
			st.itemStates[idKey] = st.active
			delete(st.itemStates, indexKey)
			st.activeStateKey = ""
		}
	}

	key := idKey
	if key == "" && outputIndex >= 0 {
		if alias, ok := st.outputItemAliases[outputIndex]; ok {
			key = alias
		} else {
			key = indexKey
		}
	}
	if key == "" {
		if st.activeStateKey == "" {
			key = defaultResponsesStateKey
		} else {
			key = st.activeStateKey
		}
	}
	if key == st.activeStateKey && st.active != nil {
		if itemId != "" {
			st.active.itemId = itemId
		}
		return
	}
	s := st.itemStates[key]
	if s == nil {
		s = newResponsesItemState()
		if itemId != "" {
			s.itemId = itemId
		}
		st.itemStates[key] = s
	}
	st.active = s
	st.activeStateKey = key
}

// finishActive output_item.done 收尾：保留 itemStates 快照、清空活跃引用。
func (st *responsesStreamState) finishActive() {
	st.active = nil
	st.activeStateKey = ""
}

// ---- item/part 键与交付记录 ----

func (st *responsesStreamState) currentItemId() string {
	if st.active == nil {
		return ""
	}
	return st.active.itemId
}

// itemKeyForEvent 事件定位 item 的键：item_id → output_index → 当前项 → 默认。
func (st *responsesStreamState) itemKeyForEvent(node map[string]any) string {
	itemId := getStr(node, "item_id")
	if itemId == "" {
		itemId = st.currentItemId()
	}
	if itemId != "" {
		return "id:" + itemId
	}
	if idx := optionalIndex(node, "output_index"); idx >= 0 {
		return "index:" + itoa(idx)
	}
	return defaultResponsesStateKey
}

// eventItemKey 事件定位内容分片的键：item 键 + 分片名 + 分片序号。
func (st *responsesStreamState) eventItemKey(node map[string]any, partName string) string {
	return st.itemKeyForEvent(node) + ":" + partName + ":" + itoa(optionalIndex(node, partName))
}

// outputItemKeyFrom 输出项定位 item 的键（item.id → event.item_id → output_index）。
func (st *responsesStreamState) outputItemKey(item, node map[string]any) string {
	itemId := getStr(item, "id")
	if itemId == "" {
		itemId = getStr(node, "item_id")
	}
	if itemId != "" {
		return "id:" + itemId
	}
	if idx := optionalIndex(node, "output_index"); idx >= 0 {
		return "index:" + itoa(idx)
	}
	return defaultResponsesStateKey
}

// textNormalizer 正文归一器（content_index 命名空间；匿名 content:-1 兼容
// 「索引后到」的网关帧——summary_index 不得接管匿名 content 状态）。
func (st *responsesStreamState) textNormalizer(node map[string]any) *responsesNormalizer {
	s := st.current()
	contentIndex := optionalIndex(node, "content_index")
	key := "content:" + itoa(contentIndex)
	n := s.textContents[key]
	if n == nil && contentIndex >= 0 {
		if initial, has := s.textContents["content:-1"]; has && len(s.textContents) == 1 {
			n = initial
			delete(s.textContents, "content:-1")
			s.textContents[key] = n
		}
	}
	if n == nil {
		n = &responsesNormalizer{}
		s.textContents[key] = n
	}
	s.textContent = n
	return n
}

// reasoningNormalizer 思考归一器（summary_index 与 content_index 是不同官方
// 命名空间，禁止把匿名 content 状态迁移到 summary）。
func (st *responsesStreamState) reasoningNormalizer(node map[string]any) *responsesNormalizer {
	s := st.current()
	summaryIndex := optionalIndex(node, "summary_index")
	contentIndex := optionalIndex(node, "content_index")
	key := "content:" + itoa(contentIndex)
	if summaryIndex >= 0 {
		key = "summary:" + itoa(summaryIndex)
	}
	n := s.reasoningContents[key]
	if n == nil && contentIndex >= 0 {
		if initial, has := s.reasoningContents["content:-1"]; has && len(s.reasoningContents) == 1 {
			n = initial
			delete(s.reasoningContents, "content:-1")
			s.reasoningContents[key] = n
		}
	}
	if n == nil {
		n = &responsesNormalizer{}
		s.reasoningContents[key] = n
	}
	s.reasoningContent = n
	return n
}

// recordDeliveredText 记录真正交付给聚合的官方正文增量。
func (st *responsesStreamState) recordDeliveredText(node map[string]any, value string) {
	if value == "" {
		return
	}
	st.activate(getStr(node, "item_id"), optionalIndex(node, "output_index"))
	s := st.current()
	key := st.eventItemKey(node, "content_index")
	buf := s.deliveredTextContents[key]
	if buf == nil {
		buf = &strings.Builder{}
		s.deliveredTextContents[key] = buf
	}
	buf.WriteString(value)
}

// recordDeliveredReasoning 记录真正交付给聚合的 reasoning 增量。
func (st *responsesStreamState) recordDeliveredReasoning(node map[string]any, partName, value string) {
	if value == "" {
		return
	}
	st.activate(getStr(node, "item_id"), optionalIndex(node, "output_index"))
	s := st.current()
	key := st.eventItemKey(node, partName)
	buf := s.deliveredReasoningContents[key]
	if buf == nil {
		buf = &strings.Builder{}
		s.deliveredReasoningContents[key] = buf
	}
	buf.WriteString(value)
}

func (st *responsesStreamState) deliveredText(itemKey, partKey string) string {
	if s := st.stateByKey(itemKey); s != nil {
		if buf := s.deliveredTextContents[partKey]; buf != nil {
			return buf.String()
		}
	}
	return ""
}

func (st *responsesStreamState) deliveredReasoning(itemKey, partKey string) string {
	if s := st.stateByKey(itemKey); s != nil {
		if buf := s.deliveredReasoningContents[partKey]; buf != nil {
			return buf.String()
		}
	}
	return ""
}

// missingResponsesSuffix 最终快照与已交付 delta 合并，只返回尚未交付的后缀；
// done/final 与已交付不一致时不能盲目重复追加（保留 raw 供诊断）。
func missingResponsesSuffix(finalValue, deliveredValue string) string {
	if finalValue == "" {
		return ""
	}
	if deliveredValue == "" {
		return finalValue
	}
	if finalValue == deliveredValue {
		return ""
	}
	if strings.HasPrefix(finalValue, deliveredValue) {
		return finalValue[len(deliveredValue):]
	}
	return ""
}

// appendStreamFinalText 从流式终态帧补齐未交付文本/思考（正文发 TEXT_DELTA，
// 思考经 emitThinkingDelta 语义事件）。
func (st *responsesStreamState) appendStreamFinalText(ctx *ai.StreamContext, node map[string]any,
	value string, reasoning bool, partName string) bool {
	if value == "" {
		return false
	}
	acc := ctx.Acc()
	st.activate(getStr(node, "item_id"), optionalIndex(node, "output_index"))
	itemKey := st.itemKeyForEvent(node)
	partKey := st.eventItemKey(node, partName)
	var delivered string
	if reasoning {
		delivered = st.deliveredReasoning(itemKey, partKey)
	} else {
		delivered = st.deliveredText(itemKey, partKey)
	}
	missing := missingResponsesSuffix(value, delivered)
	if missing == "" {
		return false
	}
	if reasoning {
		st.recordDeliveredReasoning(node, partName, missing)
		st.mergeReasoningMetadata(acc)
		st.emitThinkingDelta(ctx, node, missing)
	} else {
		ev := ctx.Event(ai.EventTextDelta)
		ev.RawType = getStr(node, "type")
		ev.ItemID = getStr(node, "item_id")
		ev.Index = optionalIndex(node, "output_index")
		ev.Text = missing
		ev.Raw = responsesRawJSON(node)
		withResponsesAttrs(ev, node)
		ctx.Emit(ev)
		st.recordDeliveredText(node, missing)
	}
	return true
}

// mergeReasoningMetadata 当前 reasoning 标识合并到聚合工作台。
func (st *responsesStreamState) mergeReasoningMetadata(acc *ai.Accumulator) {
	if acc == nil || st.active == nil {
		return
	}
	if st.active.reasoningId != "" {
		acc.AggregationMeta()[aggReasoningID] = st.active.reasoningId
	}
	if st.active.reasoningEncrypted != "" {
		acc.AggregationMeta()[aggReasoningEnc] = st.active.reasoningEncrypted
	}
}

// emitThinkingDelta 发射 reasoning 增量；item_id 缺失时回退到当前 reasoning/output item。
func (st *responsesStreamState) emitThinkingDelta(ctx *ai.StreamContext, node map[string]any, text string) {
	itemId := getStr(node, "item_id")
	if itemId == "" {
		if s := st.active; s != nil {
			if s.reasoningId != "" {
				itemId = s.reasoningId
			} else {
				itemId = s.itemId
			}
		}
	}
	ev := ctx.Event(ai.EventThinkingDelta)
	ev.RawType = getStr(node, "type")
	ev.ItemID = itemId
	ev.Index = optionalIndex(node, "output_index")
	ev.Text = text
	ev.Raw = responsesRawJSON(node)
	withResponsesAttrs(ev, node)
	ctx.Emit(ev)
}

// emitReasoningSignature 按 reasoning 项协议身份发射加密签名；
// 跨 output_item.done / completed 重放统一去重。
func (st *responsesStreamState) emitReasoningSignature(ctx *ai.StreamContext, itemKey, rawType,
	itemId string, outputIndex int, encryptedContent string, raw, attrsNode map[string]any) bool {
	if encryptedContent == "" {
		return false
	}
	identity := itemKey
	if identity == "" {
		if itemId != "" {
			identity = "id:" + itemId
		} else if outputIndex >= 0 {
			identity = "index:" + itoa(outputIndex)
		} else {
			identity = defaultResponsesStateKey
		}
	}
	signatureKey := identity + ":encrypted_content:" + encryptedContent
	keys, _ := ctx.AttrIfAbsent(responsesSignatureKeysKey, func() any {
		return map[string]struct{}{}
	}).(map[string]struct{})
	if _, dup := keys[signatureKey]; dup {
		return false
	}
	keys[signatureKey] = struct{}{}

	ev := ctx.Event(ai.EventThinkingSignature)
	ev.RawType = rawType
	ev.ItemID = itemId
	ev.Index = outputIndex
	ev.Text = encryptedContent
	ev.Raw = responsesRawJSON(raw)
	withResponsesAttrs(ev, attrsNode)
	ctx.Emit(ev)
	return true
}

// ---- 事件属性与工具函数 ----

func itoa(n int) string { return strconv.Itoa(n) }

// optionalIndex 节点整数字段（缺省/空 → -1）。
func optionalIndex(node map[string]any, name string) int {
	if node == nil {
		return -1
	}
	if v, ok := node[name]; ok && v != nil {
		return int(getInt(node, name))
	}
	return -1
}

// firstNonEmptyStr 依序取首个非空字符串字段。
func firstNonEmptyStr(node map[string]any, names ...string) string {
	for _, name := range names {
		if v := getStr(node, name); v != "" {
			return v
		}
	}
	return ""
}

// responsesRawJSON 节点 → 事件 raw 载荷。
func responsesRawJSON(node map[string]any) json.RawMessage {
	if node == nil {
		return nil
	}
	data, err := json.Marshal(node)
	if err != nil {
		return nil
	}
	return data
}

// withResponsesAttrs 帧公共属性（序号/各级索引/状态/logprobs）挂事件。
func withResponsesAttrs(ev *ai.ChatEvent, node map[string]any) {
	if node == nil {
		return
	}
	if ev.Attrs == nil {
		ev.Attrs = map[string]any{}
	}
	setIndex := func(key string) {
		if v, ok := node[key]; ok && v != nil {
			ev.Attrs[key] = int(getInt(node, key))
		}
	}
	if v, ok := node["sequence_number"]; ok && v != nil {
		ev.Attrs["sequence_number"] = getInt(node, "sequence_number")
	}
	setIndex("output_index")
	setIndex("content_index")
	setIndex("summary_index")
	setIndex("command_index")
	setIndex("annotation_index")
	setIndex("partial_image_index")
	if v, ok := node["status"]; ok && v != nil {
		ev.Attrs["status"] = getStr(node, "status")
	}
	if v, ok := node["logprobs"]; ok {
		ev.Attrs["logprobs"] = v
	}
}

// emitResponsesError 错误事件（可选携带原始帧）。
func emitResponsesError(ctx *ai.StreamContext, rawType string, raw map[string]any) {
	ev := ctx.Event(ai.EventError)
	ev.RawType = rawType
	ev.Err = ctx.Acc().Err()
	ev.Raw = responsesRawJSON(raw)
	withResponsesAttrs(ev, raw)
	ctx.Emit(ev)
}

// isResponsesReasoningSnapshotMode 兼容网关累计快照声明（responses_reasoning_delta_mode=snapshot）。
func isResponsesReasoningSnapshotMode(ctx *ai.StreamContext) bool {
	if ctx == nil || ctx.Options() == nil {
		return false
	}
	mode := ctx.Options().OptionGet(OptionResponsesReasoningDeltaMode)
	if mode == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(fmt.Sprint(mode)), "snapshot")
}

// ---- 解析入口 ----

// ParseResponseJSON 解析一帧（call 为整体 body；stream 为单个 SSE data 帧；
// [DONE] 哨兵先把聚合中的 Responses 状态提交终态协议袋）。
func (d *ResponsesDialect) ParseResponseJSON(ctx *ai.StreamContext, body string) {
	acc := ctx.Acc()
	if body == "" {
		return
	}
	if body == "[DONE]" {
		if acc.IsStream() {
			attachResponsesReplayState(acc)
			ctx.AttrRemove(streamStateKey)
		} else if !acc.IsFinished() {
			acc.SetTerminalMessage(ai.Snapshot("", "", nil))
		}
		if !acc.IsFinished() {
			acc.SetFinished(true)
		}
		return
	}
	// 部分中转直接输出 "error xxx" 纯文本（非 JSON）
	if strings.HasPrefix(body, "error ") {
		acc.SetError(errors.New(body))
		emitResponsesError(ctx, "error", nil)
		return
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(body), &root); err != nil || root == nil {
		return // 单帧损坏不阻断流
	}
	if acc.IsStream() {
		parseResponsesStreamNode(ctx, acc, root)
	} else {
		parseResponsesNonStream(ctx, acc, root)
	}
}

// ---- 流式帧解析 ----

func isResponsesOutputAudioEvent(eventType string) bool {
	switch eventType {
	case "response.output_audio.delta", "response.output_audio.done",
		"response.output_audio_transcript.delta", "response.output_audio_transcript.done",
		"response.audio.delta", "response.audio.done",
		"response.audio.transcript.delta", "response.audio.transcript.done":
		return true
	}
	return false
}

func isResponsesServerToolEvent(eventType string) bool {
	for _, prefix := range responsesServerToolPrefixes {
		if strings.HasPrefix(eventType, prefix) {
			return true
		}
	}
	return false
}

func isResponsesServerToolItem(itemType string) bool {
	switch itemType {
	case "web_search_call", "file_search_call", "code_interpreter_call", "computer_call",
		"computer_call_output", "function_call_output", "mcp_call", "mcp_list_tools",
		"mcp_approval_request", "mcp_approval_response", "shell_call", "shell_call_output",
		"local_shell_call", "local_shell_call_output", "apply_patch_call",
		"apply_patch_call_output", "custom_tool_call", "custom_tool_call_output",
		"tool_search_call", "tool_search_output", "program", "program_output",
		"compaction", "additional_tools":
		return true
	}
	return false
}

// parseResponsesStreamNode 单个 SSE 事件帧 → 语义事件。
func parseResponsesStreamNode(ctx *ai.StreamContext, acc *ai.Accumulator, oResp map[string]any) {
	// 顶层 error 帧：流已终止（error 为对象时规范提取 "[type] message"）
	if errNode, has := oResp["error"]; has && errNode != nil {
		ctx.AttrRemove(streamStateKey)
		acc.SetError(errors.New(extractErrorMessage(errNode)))
		acc.SetFinished(true)
		emitResponsesError(ctx, "error", oResp)
		return
	}

	eventType := getStr(oResp, "type")
	// incomplete/cancelled 已是供应商终态：忽略其后成功或内容帧（保留 ERROR 覆盖能力）
	if v := ctx.AttrIfAbsent(responsesTerminalKey, nil); v != nil &&
		eventType != "error" && eventType != "response.failed" {
		return
	}

	if st := responsesStreamStateOf(ctx); st != nil {
		itemId := getStr(oResp, "item_id")
		outputIndex := optionalIndex(oResp, "output_index")
		if itemId != "" || outputIndex >= 0 {
			st.activate(itemId, outputIndex)
		}
	}

	switch {
	case eventType == "keepalive":
		// SDK 保活：不当成模型内容也不当 RAW
		ev := ctx.Event(ai.EventHeartbeat)
		ev.RawType = eventType
		ev.Raw = responsesRawJSON(oResp)
		ctx.Emit(ev)

	case eventType == "error":
		ctx.AttrRemove(streamStateKey)
		// error 事件可能把 message/type/code 平链在帧顶层（无 error 子对象）
		oError := oResp
		if errNode, has := oResp["error"]; has && errNode != nil {
			oError = map[string]any{"error": errNode}
		}
		acc.SetError(errors.New(extractErrorMessage(oError["error"])))
		acc.SetFinished(true)
		emitResponsesError(ctx, eventType, oResp)

	case eventType == "response.created" || eventType == "response.in_progress" || eventType == "response.queued":
		response, _ := oResp["response"].(map[string]any)
		if response != nil {
			acc.SetModel(getStr(response, "model"))
			ctx.SetProviderResponseID(getStr(response, "id"))
		}
		ev := ctx.Event(ai.EventStatus)
		ev.RawType = eventType
		if response != nil {
			ev.ItemID = getStr(response, "id")
		}
		ev.Raw = responsesRawJSON(oResp)
		withResponsesAttrs(ev, oResp)
		ctx.Emit(ev)

	case isResponsesOutputAudioEvent(eventType):
		st := responsesStreamStateOf(ctx)
		transcript := strings.Contains(eventType, "audio_transcript") || strings.Contains(eventType, "audio.transcript")
		done := strings.HasSuffix(eventType, ".done")
		value := firstNonEmptyStr(oResp, "delta", "transcript")
		var evType ai.EventType = ai.EventMediaPartial
		if done {
			evType = ai.EventMediaDone
		}
		ev := ctx.Event(evType)
		ev.RawType = eventType
		if transcript {
			ev.SubType = "audio_transcript"
		} else {
			ev.SubType = "audio"
		}
		ev.Raw = responsesRawJSON(oResp)
		withResponsesAttrs(ev, oResp)
		if transcript {
			missing := value
			if done {
				missing = missingResponsesSuffix(value, st.audioTranscript.String())
			}
			if missing != "" {
				st.audioTranscript.WriteString(missing)
				// 与非流式 output_audio 对齐：transcript 既保留媒体语义，也投影为最终正文
				textEv := ctx.Event(ai.EventTextDelta)
				textEv.RawType = eventType
				textEv.SubType = "audio_transcript"
				textEv.ItemID = getStr(oResp, "item_id")
				textEv.Index = optionalIndex(oResp, "output_index")
				textEv.Text = missing
				textEv.Raw = responsesRawJSON(oResp)
				withResponsesAttrs(textEv, oResp)
				ctx.Emit(textEv)
				ev.Text = missing
				mergeResponsesAudioTranscriptMetadata(acc, st.audioTranscript.String())
			}
		} else if !done && value != "" {
			st.audioData.WriteString(value)
			ev.Block = audioBlockOfBase64WithMeta(value)
		} else if done && st.audioData.Len() > 0 && !st.audioDelivered {
			audio := audioBlockOfBase64WithMeta(st.audioData.String())
			if st.audioTranscript.Len() > 0 {
				audio.Meta["transcript"] = st.audioTranscript.String()
			}
			st.audioDelivered = true
			ev.Block = audio
		}
		ctx.Emit(ev)

	case eventType == "response.output_text.annotation.added":
		st := responsesStreamStateOf(ctx)
		annotationIndex := optionalIndex(oResp, "annotation_index")
		key := st.itemKeyForEvent(oResp) + ":content_index:" + itoa(optionalIndex(oResp, "content_index")) +
			":annotation_index:" + itoa(annotationIndex)
		if _, dup := st.emittedAnnotations[key]; !dup {
			st.emittedAnnotations[key] = struct{}{}
			annotation, _ := oResp["annotation"].(map[string]any)
			ev := ctx.Event(ai.EventCitation)
			ev.RawType = eventType
			ev.SubType = getStr(annotation, "type")
			ev.ItemID = getStr(oResp, "item_id")
			ev.Index = annotationIndex
			ev.Text = extractResponsesAnnotationText(annotation)
			ev.Citation = parseResponsesCitation(annotation)
			ev.Raw = responsesRawJSON(oResp)
			withResponsesAttrs(ev, oResp)
			ctx.Emit(ev)
		}

	case eventType != "" && isResponsesServerToolEvent(eventType):
		st := responsesStreamStateOf(ctx)
		if eventType == "response.image_generation_call.partial_image" {
			partialIndex := optionalIndex(oResp, "partial_image_index")
			var partialKey string
			if partialIndex >= 0 {
				partialKey = st.itemKeyForEvent(oResp) + ":partial_image:" + itoa(partialIndex)
			} else {
				b64 := getStr(oResp, "partial_image_b64")
				partialKey = st.itemKeyForEvent(oResp) + ":partial_image_hash:" +
					strconv.FormatUint(uint64(hashString(b64)), 10) + ":" + strconv.Itoa(len(b64))
			}
			if _, dup := st.current().emittedPartialImages[partialKey]; !dup {
				st.current().emittedPartialImages[partialKey] = struct{}{}
				emitResponsesServerToolEvent(ctx, eventType, oResp)
			}
		} else {
			emitResponsesServerToolEvent(ctx, eventType, oResp)
		}

	case eventType == "response.refusal.done":
		// 携带最终文本：既交付正文投影，又发一次终态安全事件
		st := responsesStreamStateOf(ctx)
		value := firstNonEmptyStr(oResp, "refusal", "text")
		appended := st.appendStreamFinalText(ctx, oResp, value, false, "content_index")
		refusalKey := st.eventItemKey(oResp, "content_index")
		if _, dup := st.current().emittedRefusalParts[refusalKey]; !dup {
			st.current().emittedRefusalParts[refusalKey] = struct{}{}
			ev := ctx.Event(ai.EventContentFilter)
			ev.RawType = eventType
			ev.SubType = "refusal"
			ev.ItemID = getStr(oResp, "item_id")
			ev.Index = optionalIndex(oResp, "output_index")
			ev.Text = value
			ev.Raw = responsesRawJSON(oResp)
			withResponsesAttrs(ev, oResp)
			ctx.Emit(ev)
		}
		_ = appended

	case eventType == "response.output_item.added":
		item, _ := oResp["item"].(map[string]any)
		if item != nil {
			st := responsesStreamStateOf(ctx)
			st.activate(getStr(item, "id"), optionalIndex(oResp, "output_index"))
			s := st.current()
			s.itemId = getStr(item, "id")
			s.itemType = getStr(item, "type")
			s.phase = getStr(item, "phase")
			if s.phase != "" {
				acc.AggregationMeta()[aggPhase] = s.phase
			}
			switch s.itemType {
			case "message":
				s.textContent = &responsesNormalizer{}
				s.textContents["content:-1"] = s.textContent
			case "reasoning":
				s.reasoningContent = &responsesNormalizer{}
				s.reasoningContents["content:-1"] = s.reasoningContent
				if id := getStr(item, "id"); id != "" {
					s.reasoningId = id
				}
				if encrypted := getStr(item, "encrypted_content"); encrypted != "" {
					s.reasoningEncrypted = encrypted
				}
			case "function_call":
				s.functionCallId = getStr(item, "call_id")
				s.functionName = getStr(item, "name")
				s.functionArguments = &strings.Builder{}
				s.functionArgumentsEmitted = false
				emitResponsesFunctionCallStart(ctx, s.functionCallId, s.functionName)
			}
		}

	case eventType == "response.output_item.done":
		item, _ := oResp["item"].(map[string]any)
		st := responsesStreamStateOf(ctx)
		if item != nil {
			captureResponsesReplayMetadata(acc, []any{item}, optionalIndex(oResp, "output_index"))
			switch getStr(item, "type") {
			case "image_generation_call":
				if block := parseResponsesImageGenerationCall(item); block != nil {
					imageKey := st.outputItemKey(item, oResp)
					if _, dup := st.current().emittedMediaItems[imageKey]; !dup {
						st.current().emittedMediaItems[imageKey] = struct{}{}
						ev := ctx.Event(ai.EventMediaDone)
						ev.RawType = eventType
						ev.ItemID = getStr(item, "id")
						ev.Index = optionalIndex(oResp, "output_index")
						ev.Block = block
						ev.Raw = responsesRawJSON(oResp)
						withResponsesAttrs(ev, oResp)
						ctx.Emit(ev)
					}
				}
			case "message":
				appendResponsesFinalOutput(ctx, acc, []any{item}, st)
			}
			st.activate(getStr(item, "id"), optionalIndex(oResp, "output_index"))
			switch getStr(item, "type") {
			case "reasoning":
				if id := getStr(item, "id"); id != "" {
					st.current().reasoningId = id
				}
				if encrypted := getStr(item, "encrypted_content"); encrypted != "" {
					st.current().reasoningEncrypted = encrypted
				}
				st.mergeReasoningMetadata(acc)
				if encrypted := getStr(item, "encrypted_content"); encrypted != "" {
					st.emitReasoningSignature(ctx, st.outputItemKey(item, oResp), eventType,
						st.current().reasoningId, optionalIndex(oResp, "output_index"), encrypted, oResp, oResp)
				}
			case "function_call":
				callId := firstNonEmptyStr(item, "call_id", "id")
				name := getStr(item, "name")
				if callId != "" && name != "" {
					st.current().functionCallId = callId
					st.current().functionName = name
					flushResponsesFunctionCall(ctx, st, getStr(item, "arguments"))
				}
			}
			if phase := getStr(item, "phase"); phase != "" {
				st.current().phase = phase
				acc.AggregationMeta()[aggPhase] = phase
			}
			if isResponsesServerToolItem(getStr(item, "type")) {
				emitResponsesItemDoneFallback(ctx, st, item, oResp)
			}
		}
		st.finishActive()

	case eventType == "response.content_part.added":
		part, _ := oResp["part"].(map[string]any)
		if part != nil {
			st := responsesStreamStateOf(ctx)
			switch getStr(part, "type") {
			case "output_text":
				s := st.current()
				s.textContent = &responsesNormalizer{}
				s.textContents["content:"+itoa(optionalIndex(oResp, "content_index"))] = s.textContent
			case "reasoning_text":
				s := st.current()
				s.reasoningContent = &responsesNormalizer{}
				s.reasoningContents["content:"+itoa(optionalIndex(oResp, "content_index"))] = s.reasoningContent
			}
		}

	case eventType == "response.reasoning_summary_part.added":
		// 官方 o 系列思维链摘要 part（reasoning_summary_text）
		st := responsesStreamStateOf(ctx)
		if st.current().reasoningContent == nil {
			st.current().reasoningContent = &responsesNormalizer{}
		}

	case eventType == "response.reasoning_summary_text.delta":
		// 官方独有事件（不存在累计快照实现），不做快照归一，但参与累积基准
		delta := getStr(oResp, "delta")
		if delta != "" {
			st := responsesStreamStateOf(ctx)
			st.activate(getStr(oResp, "item_id"), optionalIndex(oResp, "output_index"))
			st.reasoningNormalizer(oResp).append(delta)
			st.recordDeliveredReasoning(oResp, "summary_index", delta)
			st.mergeReasoningMetadata(acc)
			st.emitThinkingDelta(ctx, oResp, delta)
		}

	case eventType == "response.reasoning_summary_text.done":
		st := responsesStreamStateOf(ctx)
		st.appendStreamFinalText(ctx, oResp, getStr(oResp, "text"), true, "summary_index")

	case eventType == "response.reasoning_summary_part.done":
		st := responsesStreamStateOf(ctx)
		part, _ := oResp["part"].(map[string]any)
		var value string
		if part != nil {
			value = firstNonEmptyStr(part, "text", "summary_text")
		}
		st.appendStreamFinalText(ctx, oResp, value, true, "summary_index")
		if _, has := oResp["status"]; has {
			ev := ctx.Event(ai.EventStatus)
			ev.RawType = eventType
			ev.SubType = "reasoning_summary"
			ev.ItemID = getStr(oResp, "item_id")
			ev.Index = optionalIndex(oResp, "output_index")
			ev.Text = getStr(oResp, "status")
			ev.Raw = responsesRawJSON(oResp)
			withResponsesAttrs(ev, oResp)
			ctx.Emit(ev)
		}

	case eventType == "response.refusal.delta":
		// 官方拒答流式增量：按普通文本输出避免内容丢失；另发专用事件（文本降级同时保留）
		delta := getStr(oResp, "delta")
		if delta != "" {
			st := responsesStreamStateOf(ctx)
			s := st.current()
			if s.textContent == nil {
				s.textContent = &responsesNormalizer{}
				s.textContents["content:-1"] = s.textContent
			}
			st.recordDeliveredText(oResp, delta)
			textEv := ctx.Event(ai.EventTextDelta)
			textEv.RawType = eventType
			textEv.ItemID = getStr(oResp, "item_id")
			textEv.Index = optionalIndex(oResp, "output_index")
			textEv.Text = delta
			textEv.Raw = responsesRawJSON(oResp)
			withResponsesAttrs(textEv, oResp)
			ctx.Emit(textEv)

			refusalEv := ctx.Event(ai.EventRefusalDelta)
			refusalEv.RawType = eventType
			refusalEv.ItemID = getStr(oResp, "item_id")
			refusalEv.Index = optionalIndex(oResp, "output_index")
			refusalEv.Text = delta
			refusalEv.Raw = responsesRawJSON(oResp)
			withResponsesAttrs(refusalEv, oResp)
			ctx.Emit(refusalEv)
		}

	case eventType == "response.reasoning_text.delta":
		delta := getStr(oResp, "delta")
		if delta != "" {
			st := responsesStreamStateOf(ctx)
			st.activate(getStr(oResp, "item_id"), optionalIndex(oResp, "output_index"))
			s := st.current()
			if s.itemType == "" {
				s.itemType = "reasoning"
			}
			normalizer := st.reasoningNormalizer(oResp)
			if isResponsesReasoningSnapshotMode(ctx) {
				// 仅在调用方明确声明兼容网关发送累计快照时才做后缀归一
				delta = normalizer.normalize(delta)
			} else {
				// OpenAI 官方 Responses 的 delta 永远是新增内容
				normalizer.append(delta)
			}
			if delta != "" {
				st.recordDeliveredReasoning(oResp, "content_index", delta)
				st.mergeReasoningMetadata(acc)
				st.emitThinkingDelta(ctx, oResp, delta)
			}
		}

	case eventType == "response.reasoning_text.done":
		st := responsesStreamStateOf(ctx)
		st.appendStreamFinalText(ctx, oResp, getStr(oResp, "text"), true, "content_index")

	case eventType == "response.output_text.done":
		st := responsesStreamStateOf(ctx)
		st.appendStreamFinalText(ctx, oResp, getStr(oResp, "text"), false, "content_index")

	case eventType == "response.content_part.done":
		st := responsesStreamStateOf(ctx)
		part, _ := oResp["part"].(map[string]any)
		var partType, value string
		if part != nil {
			partType = getStr(part, "type")
			value = firstNonEmptyStr(part, "text", "refusal")
		}
		refusal := partType == "refusal"
		reasoning := partType == "reasoning_text"
		st.appendStreamFinalText(ctx, oResp, value, reasoning, "content_index")
		if refusal && value != "" {
			refusalKey := st.eventItemKey(oResp, "content_index")
			if _, dup := st.current().emittedRefusalParts[refusalKey]; !dup {
				st.current().emittedRefusalParts[refusalKey] = struct{}{}
				ev := ctx.Event(ai.EventContentFilter)
				ev.RawType = eventType
				ev.SubType = "refusal"
				ev.ItemID = getStr(oResp, "item_id")
				ev.Index = optionalIndex(oResp, "output_index")
				ev.Text = value
				ev.Raw = responsesRawJSON(oResp)
				withResponsesAttrs(ev, oResp)
				ctx.Emit(ev)
			}
		}

	case eventType == "response.completed":
		response, _ := oResp["response"].(map[string]any)
		st := responsesStreamStateOf(ctx)
		if st.activeStateKey == "" {
			st.current() // stateForFinalOutput：确保默认活跃态
		}
		if response != nil {
			ctx.SetProviderResponseID(getStr(response, "id"))
			acc.SetModel(getStr(response, "model"))
			if u := parseResponsesUsage(response["usage"]); u != nil {
				acc.SetUsage(u)
			}
			output := getArray(response, "output")
			captureResponsesReplayMetadata(acc, output, 0)
			appendResponsesFinalOutput(ctx, acc, output, st)
			attachResponsesReplayState(acc)
		}
		// 保留字段级交付状态到 [DONE]，用于终态重放/重复帧幂等
		acc.SetFinished(true)

	case eventType == "response.output_text.delta":
		delta := getStr(oResp, "delta")
		if delta != "" {
			st := responsesStreamStateOf(ctx)
			st.recordDeliveredText(oResp, delta)
			ev := ctx.Event(ai.EventTextDelta)
			ev.RawType = eventType
			ev.ItemID = getStr(oResp, "item_id")
			ev.Index = optionalIndex(oResp, "output_index")
			ev.Text = delta
			ev.Raw = responsesRawJSON(oResp)
			withResponsesAttrs(ev, oResp)
			ctx.Emit(ev)
		}

	case eventType == "response.content_part.delta":
		delta, _ := oResp["delta"].(map[string]any)
		if delta != nil {
			text := getStr(delta, "text")
			if text != "" {
				st := responsesStreamStateOf(ctx)
				if getStr(delta, "type") == "reasoning_text" {
					st.recordDeliveredReasoning(oResp, "content_index", text)
					st.mergeReasoningMetadata(acc)
					st.emitThinkingDelta(ctx, oResp, text)
				} else {
					st.recordDeliveredText(oResp, text)
					ev := ctx.Event(ai.EventTextDelta)
					ev.RawType = eventType
					ev.ItemID = getStr(oResp, "item_id")
					ev.Index = optionalIndex(oResp, "output_index")
					ev.Text = text
					ev.Raw = responsesRawJSON(oResp)
					withResponsesAttrs(ev, oResp)
					ctx.Emit(ev)
				}
			}
		}

	case eventType == "response.function_call_arguments.delta":
		delta := getStr(oResp, "delta")
		if delta != "" {
			st := responsesStreamStateOf(ctx)
			st.activate(getStr(oResp, "item_id"), optionalIndex(oResp, "output_index"))
			s := st.current()
			if s.functionArguments == nil {
				s.functionArguments = &strings.Builder{}
			}
			s.functionArguments.WriteString(delta)
			emitResponsesFunctionCallStart(ctx, s.functionCallId, s.functionName)
			if s.functionCallId != "" {
				ev := ctx.Event(ai.EventToolCallArgsDelta)
				ev.ToolCall = &ai.ToolCall{ID: s.functionCallId, Name: s.functionName}
				ev.ToolCallID = s.functionCallId
				ev.Text = delta
				ctx.Emit(ev)
				s.functionArgumentsEmitted = true
			}
		}

	case eventType == "response.function_call_arguments.done":
		// done 事件自身含 item_id/name/arguments，可在缺少 output_item.added 或重连后独立恢复
		st := responsesStreamStateOf(ctx)
		st.activate(getStr(oResp, "item_id"), optionalIndex(oResp, "output_index"))
		s := st.current()
		if s.functionCallId != "" && s.functionName != "" {
			flushResponsesFunctionCall(ctx, st, getStr(oResp, "arguments"))
		} else {
			// 官方 done 事件只有 output item id，没有 call_id；先缓存，
			// 等待 output_item.done/completed 补全真实 call_id
			s.functionName = getStr(oResp, "name")
			s.functionArguments = &strings.Builder{}
			s.functionArguments.WriteString(getStr(oResp, "arguments"))
		}

	case eventType == "response.incomplete" || eventType == "response.cancelled":
		// 供应商终态：先补齐 output/tool/media/usage，STATUS 作为方言最后一个语义事件
		incomplete := eventType == "response.incomplete"
		response, _ := oResp["response"].(map[string]any)
		st := responsesStreamStateOf(ctx)
		if st.activeStateKey == "" {
			st.current()
		}
		reason := ""
		if !incomplete {
			reason = "cancelled"
		}
		if response != nil {
			ctx.SetProviderResponseID(getStr(response, "id"))
			acc.SetModel(getStr(response, "model"))
			if u := parseResponsesUsage(response["usage"]); u != nil {
				acc.SetUsage(u)
			}
			output := getArray(response, "output")
			captureResponsesReplayMetadata(acc, output, 0)
			appendResponsesFinalOutput(ctx, acc, output, st)
			attachResponsesReplayState(acc)
			if incomplete {
				if details, ok := response["incomplete_details"].(map[string]any); ok {
					reason = getStr(details, "reason")
				}
			}
		}
		if incomplete {
			if reason == "" || reason == "max_output_tokens" {
				reason = "length"
			}
		} else {
			reason = "cancelled"
		}
		acc.SetFinishReason(reason)
		acc.SetFinished(true)
		ev := ctx.Event(ai.EventStatus)
		ev.RawType = eventType
		ev.SubType = reason
		ev.Text = reason
		ev.Raw = responsesRawJSON(oResp)
		withResponsesAttrs(ev, oResp)
		if ev.Attrs == nil {
			ev.Attrs = map[string]any{}
		}
		ev.Attrs["finish_reason"] = reason
		ctx.Emit(ev)
		ctx.AttrPut(responsesTerminalKey, eventType)

	case eventType == "response.failed":
		ctx.AttrRemove(streamStateKey)
		response, _ := oResp["response"].(map[string]any)
		if response != nil {
			ctx.SetProviderResponseID(getStr(response, "id"))
			acc.SetModel(getStr(response, "model"))
			if u := parseResponsesUsage(response["usage"]); u != nil {
				acc.SetUsage(u)
			}
			// 与非流式 error 提取一致：格式化为 "[type/code] message"
			errNode := response["error"]
			if errNode == nil {
				errNode = any(response)
			}
			acc.SetError(errors.New(extractErrorMessage(errNode)))
		} else {
			acc.SetError(errors.New("Response failed"))
		}
		emitResponsesError(ctx, eventType, oResp)
		acc.SetFinished(true)

	default:
		// 未建模事件以 RAW 透出（默认不投递，需显式开启）
		ev := ctx.Event(ai.EventRaw)
		ev.RawType = eventType
		ev.Raw = responsesRawJSON(oResp)
		withResponsesAttrs(ev, oResp)
		ctx.Emit(ev)
	}
}

// hashString 字符串散列（部分镜像帧无 partial_image_index 时的内容签名去重）。
func hashString(s string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

// decodeWireBase64 线上 base64 → 原始字节（Go 约定 Data 持原始字节，出站再编码；
// Java AudioBlock.data 直接持 base64 串，两版在块边界完成转换）。解码失败按原文
// 字节保留，与 Java 不因坏帧丢块的行为一致。
func decodeWireBase64(b64 string) []byte {
	if decoded, err := base64.StdEncoding.DecodeString(b64); err == nil {
		return decoded
	}
	return []byte(b64)
}

// audioBlockOfBase64WithMeta 音频帧块（Meta 预建——转录后到时经共享 map 回填）。
func audioBlockOfBase64WithMeta(b64 string) ai.AudioBlock {
	block := ai.AudioBlockOfBase64(decodeWireBase64(b64))
	block.Meta = map[string]any{}
	return block
}

// mergeResponsesAudioTranscriptMetadata 已交付音频块补转录元数据。
func mergeResponsesAudioTranscriptMetadata(acc *ai.Accumulator, transcript string) {
	if acc == nil || transcript == "" {
		return
	}
	for _, block := range acc.MediaBlocks() {
		if audio, ok := block.(ai.AudioBlock); ok && audio.Meta != nil {
			audio.Meta["transcript"] = transcript
		}
	}
}

// extractResponsesEventText 服务端工具事件文本投影。
func extractResponsesEventText(node map[string]any) string {
	if node == nil {
		return ""
	}
	if value := firstNonEmptyStr(node, "delta", "arguments", "input", "code", "command", "text", "transcript"); value != "" {
		return value
	}
	if delta, ok := node["delta"].(map[string]any); ok {
		stdout := getStr(delta, "stdout")
		stderr := getStr(delta, "stderr")
		if stdout != "" && stderr != "" {
			return stdout + stderr
		}
		if stdout != "" {
			return stdout
		}
		return stderr
	}
	if output, ok := node["output"]; ok && output != nil {
		if s, isStr := output.(string); isStr {
			if s != "" {
				return s
			}
			return ""
		}
		return string(responsesRawJSON(map[string]any{"output": output}))
	}
	return ""
}

// emitResponsesServerToolEvent 服务端工具事件（联网搜索/代码执行/MCP/文件检索/图像生成）：
// 供应商能力差异走 subType；终态（completed/failed/done）跨显式事件与 output_item.done
// 回落统一去重。
func emitResponsesServerToolEvent(ctx *ai.StreamContext, eventType string, oResp map[string]any) bool {
	subType := strings.TrimPrefix(eventType, "response.")
	phase := ""
	if dot := strings.Index(subType, "."); dot >= 0 {
		phase = subType[dot+1:]
		subType = subType[:dot]
	}

	if phase == "completed" || phase == "failed" || phase == "done" {
		st := responsesStreamStateOf(ctx)
		key := responsesServerToolTerminalKey(st, oResp, subType)
		if _, dup := st.emittedServerToolItem[key]; dup {
			return false
		}
		st.emittedServerToolItem[key] = struct{}{}
	}

	var evType ai.EventType
	switch {
	case phase == "partial_image":
		evType = ai.EventMediaPartial // 图像渐进帧：媒体而非工具语义
	case strings.HasSuffix(phase, "delta"):
		evType = ai.EventServerToolArgsDelta
	case phase == "completed" || phase == "done" || phase == "failed" || strings.HasSuffix(phase, ".done"):
		evType = ai.EventServerToolResult
	default:
		evType = ai.EventServerToolStart // in_progress / searching / generating / interpreting 等
	}

	ev := ctx.Event(evType)
	ev.RawType = eventType
	ev.SubType = subType
	ev.ItemID = getStr(oResp, "item_id")
	ev.Index = optionalIndex(oResp, "output_index")
	ev.Text = extractResponsesEventText(oResp)
	ev.Raw = responsesRawJSON(oResp)
	withResponsesAttrs(ev, oResp)
	if delta, ok := oResp["delta"].(map[string]any); ok {
		if ev.Attrs == nil {
			ev.Attrs = map[string]any{}
		}
		if _, has := delta["stdout"]; has {
			ev.Attrs["stdout"] = getStr(delta, "stdout")
		}
		if _, has := delta["stderr"]; has {
			ev.Attrs["stderr"] = getStr(delta, "stderr")
		}
	}
	if output, ok := oResp["output"]; ok && output != nil {
		if ev.Attrs == nil {
			ev.Attrs = map[string]any{}
		}
		ev.Attrs["output"] = output
	}
	if ev.Attrs == nil {
		ev.Attrs = map[string]any{}
	}
	for _, field := range []string{"background", "output_format", "quality", "size"} {
		if _, has := oResp[field]; has {
			ev.Attrs[field] = getStr(oResp, field)
		}
	}
	if b64 := getStr(oResp, "partial_image_b64"); b64 != "" {
		ev.Block = ai.ImageBlockOfBase64(decodeWireBase64(b64))
	}
	ctx.Emit(ev)
	return true
}

// responsesServerToolTerminalKey 终态去重键（output_index 优先，回退 item 键）。
func responsesServerToolTerminalKey(st *responsesStreamState, node map[string]any, subType string) string {
	identity := ""
	if outputIndex := optionalIndex(node, "output_index"); outputIndex >= 0 {
		identity = "index:" + itoa(outputIndex)
	} else {
		identity = st.itemKeyForEvent(node)
	}
	return identity + ":" + subType
}

// emitResponsesItemDoneFallback output_item.done 携带服务端工具项时的终态回落
// （与显式 *.completed/*.done 事件共享去重键）。
func emitResponsesItemDoneFallback(ctx *ai.StreamContext, st *responsesStreamState, item, node map[string]any) {
	key := responsesServerToolTerminalKey(st, node, getStr(item, "type"))
	if _, dup := st.emittedServerToolItem[key]; dup {
		return
	}
	st.emittedServerToolItem[key] = struct{}{}
	ev := ctx.Event(ai.EventServerToolResult)
	ev.RawType = "response.output_item.done"
	ev.SubType = getStr(item, "type")
	ev.ItemID = getStr(item, "id")
	ev.Index = optionalIndex(node, "output_index")
	ev.Text = extractResponsesEventText(item)
	ev.Raw = responsesRawJSON(node)
	withResponsesAttrs(ev, node)
	ctx.Emit(ev)
}

// parseResponsesCitation 标注 → 引用（type 走 Meta；Go Citation 以 Snippet 承载 citedText）。
func parseResponsesCitation(annotation map[string]any) *ai.Citation {
	if annotation == nil {
		return nil
	}
	c := &ai.Citation{
		Title:   firstNonEmptyStr(annotation, "title", "filename"),
		URL:     getStr(annotation, "url"),
		Snippet: firstNonEmptyStr(annotation, "cited_text", "text"),
	}
	if t := getStr(annotation, "type"); t != "" {
		if c.Meta == nil {
			c.Meta = map[string]any{}
		}
		c.Meta["type"] = t
	}
	return c
}

// extractResponsesAnnotationText 标注文本投影（url → file_id → filename → 原始 JSON）。
func extractResponsesAnnotationText(annotation map[string]any) string {
	if annotation == nil {
		return ""
	}
	if v := getStr(annotation, "url"); v != "" {
		return v
	}
	if v := getStr(annotation, "file_id"); v != "" {
		return v
	}
	if v := getStr(annotation, "filename"); v != "" {
		return v
	}
	return string(responsesRawJSON(annotation))
}

// emitResponsesAnnotations 非流式 content[].annotations[] → CITATION 事件。
func emitResponsesAnnotations(ctx *ai.StreamContext, contentItem map[string]any) {
	emitResponsesAnnotationsFinal(ctx, nil, nil, -1, contentItem)
}

func emitResponsesAnnotationsFinal(ctx *ai.StreamContext, st *responsesStreamState, itemKey *string,
	contentIndex int, contentItem map[string]any) {
	annotations := getArray(contentItem, "annotations")
	for annotationIndex, raw := range annotations {
		annotation, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		key := defaultResponsesStateKey
		if itemKey != nil && *itemKey != "" {
			key = *itemKey
		}
		key = key + ":content_index:" + itoa(contentIndex) + ":annotation_index:" + itoa(annotationIndex)
		if st != nil {
			if _, dup := st.emittedAnnotations[key]; dup {
				continue
			}
			st.emittedAnnotations[key] = struct{}{}
		}
		ev := ctx.Event(ai.EventCitation)
		ev.RawType = "response.output_text.annotation"
		ev.SubType = getStr(annotation, "type")
		ev.Index = annotationIndex
		ev.Text = extractResponsesAnnotationText(annotation)
		ev.Citation = parseResponsesCitation(annotation)
		ev.Raw = responsesRawJSON(annotation)
		if ev.Attrs == nil {
			ev.Attrs = map[string]any{}
		}
		ev.Attrs["content_index"] = contentIndex
		ev.Attrs["annotation_index"] = annotationIndex
		if itemKey != nil && strings.HasPrefix(*itemKey, "id:") {
			ev.ItemID = (*itemKey)[3:]
		}
		ctx.Emit(ev)
	}
}

// ---- 回放元数据捕获 ----

// captureResponsesReplayMetadata output 数组 → 聚合工作台（有序 output items /
// message items / reasoning items；幂等合并）。
func captureResponsesReplayMetadata(acc *ai.Accumulator, output []any, firstOutputIndex int) {
	if acc == nil || output == nil {
		return
	}
	meta := acc.AggregationMeta()
	orderedItems := responsesMetaList(meta, aggOutputItems)
	messages := responsesMetaList(meta, aggMessageItems)
	reasoningItems := responsesMetaList(meta, aggReasoningItems)
	for position, raw := range output {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		typeName := getStr(item, "type")
		idx := position
		if firstOutputIndex >= 0 {
			idx = firstOutputIndex + position
		}
		if _, replayable := responsesReplayableItemTypes[typeName]; replayable {
			wrapper := map[string]any{"output_index": idx, "item": item}
			orderedItems = putOrderedResponsesReplayItem(orderedItems, wrapper)
		}
		if typeName == "message" {
			value := map[string]any{"text": extractResponsesMessageTextArr(getArray(item, "content"))}
			if id := getStr(item, "id"); id != "" {
				value["id"] = id
			}
			phase := getStr(item, "phase")
			if phase != "" {
				value["phase"] = phase
			}
			messages = putResponsesReplayItem(messages, value)
			if len(messages) == 1 && phase != "" {
				meta[aggPhase] = phase
			}
		} else if typeName == "reasoning" {
			value := map[string]any{}
			if id := getStr(item, "id"); id != "" {
				value["id"] = id
			}
			if encrypted := getStr(item, "encrypted_content"); encrypted != "" {
				value["encrypted_content"] = encrypted
			}
			if len(value) > 0 {
				reasoningItems = putResponsesReplayItem(reasoningItems, value)
			}
		}
	}
	if len(orderedItems) == 0 {
		delete(meta, aggOutputItems)
	} else {
		meta[aggOutputItems] = orderedItems
	}
	if len(messages) == 0 {
		delete(meta, aggMessageItems)
	} else {
		meta[aggMessageItems] = messages
	}
	if len(reasoningItems) == 0 {
		delete(meta, aggReasoningItems)
	} else {
		meta[aggReasoningItems] = reasoningItems
	}
}

// responsesMetaList 工作台列表键（懒建；调用方结束前统一回写）。
func responsesMetaList(meta map[string]any, key string) []any {
	if v, ok := meta[key].([]any); ok {
		return v
	}
	return nil
}

// putOrderedResponsesReplayItem 有序 output items 幂等合并：同 id（或同
// output_index+type 的无 id 项）putAll 覆盖，否则追加。
func putOrderedResponsesReplayItem(items []any, wrapper map[string]any) []any {
	value, _ := wrapper["item"].(map[string]any)
	id := value["id"]
	for _, oldRaw := range items {
		oldWrapper, ok := oldRaw.(map[string]any)
		if !ok {
			continue
		}
		old, _ := oldWrapper["item"].(map[string]any)
		sameID := id != nil && old != nil && fmtString(id) == fmtString(old["id"])
		sameIndex := id == nil && responsesNumEq(wrapper["output_index"], oldWrapper["output_index"]) &&
			value["type"] == old["type"]
		if sameID || sameIndex {
			for k, v := range wrapper {
				oldWrapper[k] = v
			}
			return items
		}
	}
	return append(items, wrapper)
}

// putResponsesReplayItem message/reasoning items 幂等合并：同 id 覆盖；
// 无 id 时按 phase/text/encrypted_content 三元组匹配。
func putResponsesReplayItem(items []any, value map[string]any) []any {
	if id, ok := value["id"]; ok && id != nil {
		for _, oldRaw := range items {
			if old, ok := oldRaw.(map[string]any); ok && fmtString(id) == getStr(old, "id") {
				for k, v := range value {
					old[k] = v
				}
				return items
			}
		}
	} else {
		for _, oldRaw := range items {
			if old, ok := oldRaw.(map[string]any); ok &&
				getStr(old, "phase") == getStr(value, "phase") &&
				getStr(old, "text") == getStr(value, "text") &&
				getStr(old, "encrypted_content") == getStr(value, "encrypted_content") {
				for k, v := range value {
					old[k] = v
				}
				return items
			}
		}
	}
	return append(items, value)
}

func fmtString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if v == nil {
		return ""
	}
	return string(responsesRawJSON(map[string]any{"v": v})) // 罕见形态兜底
}

// responsesNumEq 数值宽容相等（int/int64/float64 互通；JSON 数字跨形态安全）。
func responsesNumEq(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	af, aok := responsesToFloat(a)
	bf, bok := responsesToFloat(b)
	if aok && bok {
		return af == bf
	}
	return a == b
}

func responsesToFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// extractResponsesMessageTextArr message content 数组的文本投影（output_text/text + refusal）。
func extractResponsesMessageTextArr(content []any) string {
	var sb strings.Builder
	for _, raw := range content {
		part, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch getStr(part, "type") {
		case "output_text", "text":
			sb.WriteString(getStr(part, "text"))
		case "refusal":
			sb.WriteString(firstNonEmptyStr(part, "refusal", "text"))
		}
	}
	return sb.String()
}

// ---- 流式终帧补齐 ----

// appendResponsesFinalOutput 从 completed/incomplete 携带的最终 output 补齐没有
// 收到分片的内容（幂等：已交付的 item 不再重复追加）。
func appendResponsesFinalOutput(ctx *ai.StreamContext, acc *ai.Accumulator, output []any, st *responsesStreamState) bool {
	if output == nil {
		return false
	}
	added := false
	for outputPosition, raw := range output {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		st.activate(getStr(item, "id"), outputPosition)
		itemKey := st.activeStateKey
		if itemKey == "" {
			itemKey = defaultResponsesStateKey
		}
		itemType := getStr(item, "type")
		switch itemType {
		case "message":
			var textBuf strings.Builder
			var media []ai.ContentBlock
			for partIndex, partRaw := range getArray(item, "content") {
				part, ok := partRaw.(map[string]any)
				if !ok {
					continue
				}
				partType := getStr(part, "type")
				partKey := itemKey + ":content_index:" + itoa(partIndex)
				switch {
				case partType == "output_text" || partType == "text":
					value := getStr(part, "text")
					missing := missingResponsesSuffix(value, st.deliveredText(itemKey, partKey))
					if missing != "" {
						textBuf.WriteString(missing)
						st.recordDeliveredText(finalEventNode(item, outputPosition, partIndex, -1), missing)
					}
					emitResponsesAnnotationsFinal(ctx, st, &itemKey, partIndex, part)
				case partType == "refusal":
					value := firstNonEmptyStr(part, "refusal", "text")
					delivered := st.deliveredText(itemKey, partKey)
					missing := missingResponsesSuffix(value, delivered)
					if missing != "" {
						textBuf.WriteString(missing)
						st.recordDeliveredText(finalEventNode(item, outputPosition, partIndex, -1), missing)
						if delivered == "" {
							ev := ctx.Event(ai.EventRefusalDelta)
							ev.RawType = "response.refusal"
							ev.ItemID = getStr(item, "id")
							ev.Index = outputPosition
							ev.Text = value
							ev.Raw = responsesRawJSON(part)
							withResponsesAttrs(ev, finalEventNode(item, outputPosition, partIndex, -1))
							ctx.Emit(ev)
						}
					}
					refusalKey := itemKey + ":content_index:" + itoa(partIndex)
					if value != "" {
						if _, dup := st.current().emittedRefusalParts[refusalKey]; !dup {
							st.current().emittedRefusalParts[refusalKey] = struct{}{}
							ev := ctx.Event(ai.EventContentFilter)
							ev.RawType = "response.refusal.done"
							ev.SubType = "refusal"
							ev.ItemID = getStr(item, "id")
							ev.Index = outputPosition
							ev.Text = value
							ev.Raw = responsesRawJSON(part)
							if ev.Attrs == nil {
								ev.Attrs = map[string]any{}
							}
							ev.Attrs["content_index"] = partIndex
							ev.Attrs["output_index"] = outputPosition
							ctx.Emit(ev)
						}
					}
				case partType == "output_image" || partType == "image" || hasKey(part, "image_url"):
					mediaKey := itemKey + ":content_index:" + itoa(partIndex) + ":media"
					if _, dup := st.current().emittedMediaItems[mediaKey]; !dup {
						if block := parseResponsesMessageImage(part); block != nil {
							media = append(media, block)
							st.current().emittedMediaItems[mediaKey] = struct{}{}
						}
					}
				case partType == "output_audio":
					transcript := getStr(part, "transcript")
					if missing := missingResponsesSuffix(transcript, st.audioTranscript.String()); missing != "" {
						textBuf.WriteString(missing)
						st.audioTranscript.WriteString(missing)
					}
					mediaKey := itemKey + ":content_index:" + itoa(partIndex) + ":media"
					if _, dup := st.current().emittedMediaItems[mediaKey]; !dup {
						if block := parseResponsesMessageAudio(part); block != nil {
							if audio, isAudio := block.(ai.AudioBlock); !isAudio || !mergeResponsesEquivalentAudio(acc, audio) {
								media = append(media, block)
							}
							st.current().emittedMediaItems[mediaKey] = struct{}{}
						}
					}
					mergeResponsesAudioTranscriptMetadata(acc, transcript)
				}
			}
			text := textBuf.String()
			if text != "" || len(media) > 0 {
				if text != "" {
					ev := ctx.Event(ai.EventTextDelta)
					ev.RawType = "response.output_text.final"
					ev.ItemID = getStr(item, "id")
					ev.Index = outputPosition
					ev.Text = text
					ev.Raw = responsesRawJSON(item)
					ctx.Emit(ev)
				}
				if phase := getStr(item, "phase"); phase != "" {
					acc.AggregationMeta()[aggPhase] = phase
				}
				acc.MergeTerminalMessage(&ai.AssistantMessage{Text: text, Blocks: media})
				for _, block := range media {
					ev := ctx.Event(ai.EventMediaDone)
					ev.ItemID = getStr(item, "id")
					ev.Index = outputPosition
					ev.Block = block
					ev.Raw = responsesRawJSON(item)
					ctx.Emit(ev)
				}
				added = true
			}
			if len(media) > 0 {
				added = true
			}
		case "reasoning":
			for partIndex, partRaw := range getArray(item, "content") {
				part, ok := partRaw.(map[string]any)
				if !ok {
					continue
				}
				value := getStr(part, "text")
				partKey := itemKey + ":content_index:" + itoa(partIndex)
				if missing := missingResponsesSuffix(value, st.deliveredReasoning(itemKey, partKey)); missing != "" {
					finalEvent := finalEventNode(item, outputPosition, partIndex, -1)
					finalEvent["type"] = "response.reasoning_text.final"
					st.recordDeliveredReasoning(finalEvent, "content_index", missing)
					st.emitThinkingDelta(ctx, finalEvent, missing)
					added = true
				}
			}
			for summaryIndex, partRaw := range getArray(item, "summary") {
				part, ok := partRaw.(map[string]any)
				if !ok {
					continue
				}
				value := getStr(part, "text")
				partKey := itemKey + ":summary_index:" + itoa(summaryIndex)
				if missing := missingResponsesSuffix(value, st.deliveredReasoning(itemKey, partKey)); missing != "" {
					finalEvent := finalEventNode(item, outputPosition, -1, summaryIndex)
					finalEvent["type"] = "response.reasoning_summary_text.final"
					st.recordDeliveredReasoning(finalEvent, "summary_index", missing)
					st.emitThinkingDelta(ctx, finalEvent, missing)
					added = true
				}
			}
			if id := getStr(item, "id"); id != "" {
				st.current().reasoningId = id
			}
			if encrypted := getStr(item, "encrypted_content"); encrypted != "" {
				st.current().reasoningEncrypted = encrypted
			}
			st.mergeReasoningMetadata(acc)
			if phase := getStr(item, "phase"); phase != "" {
				acc.AggregationMeta()[aggPhase] = phase
			}
			if st.emitReasoningSignature(ctx, itemKey, "response.output_item.final",
				getStr(item, "id"), outputPosition, getStr(item, "encrypted_content"), item, nil) {
				added = true
			}
		case "function_call":
			callId := firstNonEmptyStr(item, "call_id", "id")
			if callId != "" && !st.functionEmitted(itemKey, callId) {
				st.activate(getStr(item, "id"), -1)
				s := st.current()
				s.functionCallId = callId
				s.functionName = getStr(item, "name")
				s.functionArguments = &strings.Builder{}
				s.functionArgumentsEmitted = false
				if flushResponsesFunctionCall(ctx, st, getStr(item, "arguments")) {
					added = true
				}
			}
		case "image_generation_call":
			if !st.mediaEmitted(itemKey) {
				if image := parseResponsesImageGenerationCall(item); image != nil {
					st.current().emittedMediaItems[itemKey] = struct{}{}
					ev := ctx.Event(ai.EventMediaDone)
					ev.ItemID = getStr(item, "id")
					ev.Index = outputPosition
					ev.Block = image
					ev.Raw = responsesRawJSON(item)
					ctx.Emit(ev)
					added = true
				}
			}
		}
	}
	return added
}

// finalEventNode 合成的终帧节点（补齐路径记录交付/发事件用）。
func finalEventNode(item map[string]any, outputIndex, contentIndex, summaryIndex int) map[string]any {
	node := map[string]any{
		"item_id":      getStr(item, "id"),
		"output_index": outputIndex,
	}
	if contentIndex >= 0 {
		node["content_index"] = contentIndex
	}
	if summaryIndex >= 0 {
		node["summary_index"] = summaryIndex
	}
	return node
}

func hasKey(node map[string]any, key string) bool {
	_, ok := node[key]
	return ok
}

func (st *responsesStreamState) functionEmitted(itemKey, callId string) bool {
	if s := st.stateByKey(itemKey); s != nil {
		_, ok := s.emittedFunctionCalls[callId]
		return ok
	}
	return false
}

func (st *responsesStreamState) mediaEmitted(itemKey string) bool {
	if s := st.stateByKey(itemKey); s != nil {
		_, ok := s.emittedMediaItems[itemKey]
		return ok
	}
	return false
}

// flushResponsesFunctionCall 流式 function_call 落地：净化参数 → 终态载体合并 →
// START（去重）→ 未发过的全量 ARGS_DELTA → 重置状态（arguments.done 与
// output_item.done 兜底共用）。TOOL_CALL_END 由核心统一发出。
func flushResponsesFunctionCall(ctx *ai.StreamContext, st *responsesStreamState, arguments string) bool {
	acc := ctx.Acc()
	s := st.current()
	if arguments == "" && s.functionArguments != nil {
		arguments = s.functionArguments.String()
	}
	// 流式解析出口净化：截断损坏的 arguments 禁止入历史（会毒化会话）
	arguments = SanitizeArguments(arguments, s.functionName)
	reset := func() {
		s.functionCallId, s.functionName, s.functionArguments = "", "", nil
		s.functionArgumentsEmitted = false
	}
	if s.functionCallId != "" {
		if _, dup := s.emittedFunctionCalls[s.functionCallId]; dup {
			reset()
			return false
		}
		s.emittedFunctionCalls[s.functionCallId] = struct{}{}
	}
	argMap := map[string]any{}
	if arguments != "" {
		var m map[string]any
		if json.Unmarshal([]byte(arguments), &m) == nil && m != nil {
			argMap = m
		}
	}
	call := ai.ToolCall{
		ID:           s.functionCallId,
		Name:         s.functionName,
		ArgumentsStr: arguments,
		Arguments:    argMap,
	}
	acc.MergeTerminalMessage(&ai.AssistantMessage{ToolCalls: []ai.ToolCall{call}})
	emitResponsesFunctionCallStart(ctx, s.functionCallId, s.functionName)
	if arguments != "" && !s.functionArgumentsEmitted {
		ev := ctx.Event(ai.EventToolCallArgsDelta)
		ev.ToolCall = &call
		ev.ToolCallID = s.functionCallId
		ev.Text = arguments
		ctx.Emit(ev)
	}
	reset()
	return true
}

// emitResponsesFunctionCallStart START 事件（请求内按 callId 去重）。
func emitResponsesFunctionCallStart(ctx *ai.StreamContext, callId, name string) {
	if callId == "" {
		return
	}
	started, _ := ctx.AttrIfAbsent(responsesStartedCallsKey, func() any {
		return map[string]struct{}{}
	}).(map[string]struct{})
	if _, dup := started[callId]; dup {
		return
	}
	started[callId] = struct{}{}
	ev := ctx.Event(ai.EventToolCallStart)
	ev.ToolCall = &ai.ToolCall{ID: callId, Name: name}
	ev.ToolCallID = callId
	ctx.Emit(ev)
}

// mergeResponsesEquivalentAudio 同 data 音频块等价合并（补元数据，不重复入列）。
func mergeResponsesEquivalentAudio(acc *ai.Accumulator, candidate ai.AudioBlock) bool {
	if acc == nil {
		return false
	}
	for _, block := range acc.MediaBlocks() {
		if existing, ok := block.(ai.AudioBlock); ok && string(existing.Data) == string(candidate.Data) {
			if existing.Meta == nil {
				existing.Meta = map[string]any{}
			}
			if candidate.Meta == nil {
				candidate.Meta = map[string]any{}
			}
			for k, v := range candidate.Meta {
				existing.Meta[k] = v
			}
			return true
		}
	}
	return false
}

// parseResponsesImageGenerationCall image_generation_call 输出项 → ImageBlock
// （result base64/URL；仅 id 时以 image-generation://id 占位便于多轮回传）。
func parseResponsesImageGenerationCall(item map[string]any) ai.ContentBlock {
	if item == nil {
		return nil
	}
	result := getStr(item, "result")
	id := getStr(item, "id")
	revisedPrompt := getStr(item, "revised_prompt")
	status := getStr(item, "status")

	var block *ai.ImageBlock
	if result != "" {
		if strings.HasPrefix(result, "data:") && strings.Contains(result, ";base64,") {
			comma := strings.Index(result, ",")
			mime := result[5:strings.Index(result, ";")]
			b64 := result[comma+1:]
			blk := ai.ImageBlockOfBase64(decodeWireBase64(b64), mime)
			block = &blk
		} else if strings.HasPrefix(result, "http://") || strings.HasPrefix(result, "https://") {
			blk := ai.ImageBlockOfURL(result)
			block = &blk
		} else {
			blk := ai.ImageBlockOfBase64(decodeWireBase64(result))
			block = &blk
		}
	} else if id != "" {
		blk := ai.ImageBlockOfURL("image-generation://" + id)
		block = &blk
	}
	if block == nil {
		return nil
	}
	block.Meta = map[string]any{"source_type": "image_generation_call"}
	if id != "" {
		block.Meta["id"] = id
		block.Meta["image_generation_id"] = id
	}
	if status != "" {
		block.Meta["status"] = status
	}
	if revisedPrompt != "" {
		block.Meta["revised_prompt"] = revisedPrompt
	}
	return *block
}

// parseResponsesMessageImage message content 的图片项（image_url 字符串/对象、
// url/data/b64_json/result 多形态）。
func parseResponsesMessageImage(contentItem map[string]any) ai.ContentBlock {
	if contentItem == nil {
		return nil
	}
	url := ""
	data := ""
	if imageUrl, ok := contentItem["image_url"]; ok && imageUrl != nil {
		switch u := imageUrl.(type) {
		case string:
			url = u
		case map[string]any:
			url = getStr(u, "url")
			data = firstNonEmptyStr(u, "data", "b64_json")
		}
	}
	if url == "" {
		url = getStr(contentItem, "url")
	}
	if data == "" {
		data = firstNonEmptyStr(contentItem, "data", "result")
	}
	if data != "" {
		if strings.HasPrefix(data, "data:") && strings.Contains(data, ";base64,") {
			comma := strings.Index(data, ",")
			mime := data[5:strings.Index(data, ";")]
			return ai.ImageBlockOfBase64(decodeWireBase64(data[comma+1:]), mime)
		}
		return ai.ImageBlockOfBase64(decodeWireBase64(data))
	}
	if url != "" {
		return ai.ImageBlockOfURL(url)
	}
	return nil
}

// parseResponsesMessageAudio message content 的音频项（data base64 + transcript）。
func parseResponsesMessageAudio(contentItem map[string]any) ai.ContentBlock {
	if contentItem == nil {
		return nil
	}
	data := getStr(contentItem, "data")
	if data == "" {
		return nil
	}
	var block ai.AudioBlock
	if strings.HasPrefix(data, "data:") && strings.Contains(data, ";base64,") {
		semicolon := strings.Index(data, ";")
		comma := strings.Index(data, ",")
		block = ai.AudioBlockOfBase64(decodeWireBase64(data[comma+1:]), data[5:semicolon])
	} else {
		block = ai.AudioBlockOfBase64(decodeWireBase64(data))
	}
	block.Meta = map[string]any{"source_type": "output_audio"}
	if transcript := getStr(contentItem, "transcript"); transcript != "" {
		block.Meta["transcript"] = transcript
	}
	return block
}

// parseResponsesUsage usage 节点 → ai.Usage（Responses 形态：input_tokens/
// output_tokens；官方 input_tokens_details.{cached_tokens,cache_write_tokens}、
// output_tokens_details.reasoning_tokens；兼容网关的 Chat Completions 顶层缓存
// 字段仅在官方明细缺失时生效。usage 节点存在且合法时全 0 也保留）。
func parseResponsesUsage(node any) *ai.Usage {
	u, ok := node.(map[string]any)
	if !ok || u == nil {
		return nil
	}
	inputTokens := getInt(u, "input_tokens")
	outputTokens := getInt(u, "output_tokens")
	totalTokens := inputTokens + outputTokens
	if _, has := u["total_tokens"]; has {
		totalTokens = getInt(u, "total_tokens")
	}

	cacheRead := int64(0)
	cacheWrite := int64(0)
	details, _ := u["input_tokens_details"].(map[string]any)
	if _, has := details["cached_tokens"]; has {
		cacheRead = getInt(details, "cached_tokens")
	}
	if _, has := details["cache_write_tokens"]; has {
		cacheWrite = getInt(details, "cache_write_tokens")
	}

	thinkTokens := int64(0)
	if outputDetails, ok := u["output_tokens_details"].(map[string]any); ok {
		if _, has := outputDetails["reasoning_tokens"]; has {
			thinkTokens = getInt(outputDetails, "reasoning_tokens")
		}
	} else if _, has := u["reasoning_tokens"]; has {
		thinkTokens = getInt(u, "reasoning_tokens")
	}

	// 兼容部分 Responses 网关沿用 Chat Completions 的顶层缓存命中字段（仅官方明细缺失时）
	if _, has := details["cached_tokens"]; !has {
		if _, has := u["prompt_cache_hit_tokens"]; has {
			cacheRead = getInt(u, "prompt_cache_hit_tokens")
		} else if _, has := u["input_cached_tokens"]; has {
			cacheRead = getInt(u, "input_cached_tokens")
		}
	}
	if _, has := details["cache_write_tokens"]; !has {
		if _, has := u["input_cache_write_tokens"]; has {
			cacheWrite = getInt(u, "input_cache_write_tokens")
		}
	}

	usage := &ai.Usage{
		PromptTokens:             inputTokens,
		CompletionTokens:         outputTokens,
		TotalTokens:              totalTokens,
		ThinkTokens:              thinkTokens,
		CacheReadInputTokens:     cacheRead,
		CacheCreationInputTokens: cacheWrite,
	}
	if src, err := json.Marshal(u); err == nil {
		usage.Source = src
	}
	return usage
}

// ---- 非流式 ----

// parseResponsesNonStream 整体 body → 终态消息 + 语义事件（错误/中止/拒答/引用/
// 服务端工具结果并非流式独有）。思考与正文合并为单条消息。
func parseResponsesNonStream(ctx *ai.StreamContext, acc *ai.Accumulator, oResp map[string]any) {
	// 顶层错误形态
	if errNode, has := oResp["error"]; has && errNode != nil {
		acc.SetError(errors.New(extractErrorMessage(errNode)))
		emitResponsesError(ctx, "error", oResp)
		acc.SetFinished(true)
		return
	}

	status := getStr(oResp, "status")
	terminalStatusType := ""
	terminalStatusReason := ""
	lastFinishReason := ""
	if status == "failed" {
		errNode := oResp["error"]
		if errNode == nil {
			errNode = any(oResp)
		}
		acc.SetError(errors.New(extractErrorMessage(errNode)))
		emitResponsesError(ctx, "response.failed", oResp)
		acc.SetFinished(true)
		if !acc.TerminalPresent() {
			acc.SetTerminalMessage(ai.Snapshot("", "", nil))
		}
		return
	}
	if status == "cancelled" {
		lastFinishReason = "cancelled"
		acc.SetFinishReason("cancelled")
		terminalStatusType = "response.cancelled"
		terminalStatusReason = "cancelled"
	} else if status == "incomplete" {
		// 输出被截断（如 max_output_tokens）：保留原始 reason 并回填归一化 finishReason
		details, _ := oResp["incomplete_details"].(map[string]any)
		terminalStatusReason = getStr(details, "reason")
		reason := terminalStatusReason
		if reason == "" || reason == "max_output_tokens" {
			reason = "length"
		}
		lastFinishReason = reason
		acc.SetFinishReason(reason)
		terminalStatusType = "response.incomplete"
	}

	ctx.SetProviderResponseID(getStr(oResp, "id"))
	acc.SetModel(getStr(oResp, "model"))

	outputArray := getArray(oResp, "output")
	captureResponsesReplayMetadata(acc, outputArray, 0)
	if outputArray != nil {
		var textContent strings.Builder
		var reasoningContent strings.Builder
		reasoningItemId := ""
		reasoningEncrypted := ""
		var mediaBlocks []ai.ContentBlock
		var allToolCalls []ai.ToolCall
		hasRefusal := false
		assistantPhase := ""

		for outputPosition, raw := range outputArray {
			outputItem, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			itemType := getStr(outputItem, "type")
			switch itemType {
			case "reasoning":
				// 官方 reasoning item（多轮回放需 id/encrypted_content）；
				// DeepSeek Responses 为 reasoning_text 私有扩展
				if id := getStr(outputItem, "id"); id != "" {
					reasoningItemId = id
				}
				encrypted := getStr(outputItem, "encrypted_content")
				if encrypted != "" {
					reasoningEncrypted = encrypted
					st := responsesStreamStateOf(ctx)
					itemKey := "index:" + itoa(outputPosition)
					if id := getStr(outputItem, "id"); id != "" {
						itemKey = "id:" + id
					}
					st.emitReasoningSignature(ctx, itemKey, "response.output_item",
						getStr(outputItem, "id"), outputPosition, encrypted, outputItem, nil)
				}
				var itemReasoning strings.Builder
				for _, contentRaw := range getArray(outputItem, "content") {
					contentItem, ok := contentRaw.(map[string]any)
					if !ok {
						continue
					}
					switch getStr(contentItem, "type") {
					case "reasoning_text", "text":
						itemReasoning.WriteString(getStr(contentItem, "text"))
					}
				}
				// 每个 reasoning item 独立判断 summary 回退，不被前一个 item 的正文污染
				if itemReasoning.Len() == 0 {
					for _, summaryRaw := range getArray(outputItem, "summary") {
						summaryItem, ok := summaryRaw.(map[string]any)
						if !ok {
							continue
						}
						itemReasoning.WriteString(getStr(summaryItem, "text"))
					}
				}
				reasoningContent.WriteString(itemReasoning.String())
			case "message":
				if phase := getStr(outputItem, "phase"); phase != "" {
					assistantPhase = phase
				}
				for _, contentRaw := range getArray(outputItem, "content") {
					contentItem, ok := contentRaw.(map[string]any)
					if !ok {
						continue
					}
					contentType := getStr(contentItem, "type")
					switch {
					case contentType == "output_text" || contentType == "text":
						textContent.WriteString(getStr(contentItem, "text"))
						// 引用与流式 annotation.added 对称
						emitResponsesAnnotations(ctx, contentItem)
					case contentType == "refusal":
						refusal := firstNonEmptyStr(contentItem, "refusal", "text")
						if refusal != "" {
							textContent.WriteString(refusal)
							// 专用事件 + 文本降级并存（与流式一致）
							ev := ctx.Event(ai.EventRefusalDelta)
							ev.RawType = "response.refusal"
							ev.Text = refusal
							ev.Raw = responsesRawJSON(contentItem)
							ctx.Emit(ev)
							hasRefusal = true
						}
					case contentType == "output_image" || contentType == "image" || hasKey(contentItem, "image_url"):
						if block := parseResponsesMessageImage(contentItem); block != nil {
							mediaBlocks = append(mediaBlocks, block)
						}
					case contentType == "output_audio":
						if block := parseResponsesMessageAudio(contentItem); block != nil {
							mediaBlocks = append(mediaBlocks, block)
						}
						if transcript := getStr(contentItem, "transcript"); transcript != "" {
							textContent.WriteString(transcript)
						}
					}
				}
			case "image_generation_call":
				if block := parseResponsesImageGenerationCall(outputItem); block != nil {
					mediaBlocks = append(mediaBlocks, block)
				}
			case "function_call":
				callId := getStr(outputItem, "call_id")
				functionName := getStr(outputItem, "name")
				// 非流式解析出口净化：截断损坏的 arguments 禁止入历史
				arguments := SanitizeArguments(getStr(outputItem, "arguments"), functionName)
				argMap := map[string]any{}
				if arguments != "" {
					var m map[string]any
					if json.Unmarshal([]byte(arguments), &m) == nil && m != nil {
						argMap = m
					}
				}
				allToolCalls = append(allToolCalls, ai.ToolCall{
					ID: callId, Name: functionName, ArgumentsStr: arguments, Arguments: argMap,
				})
			default:
				if isResponsesServerToolItem(itemType) {
					// 与流式 SERVER_TOOL_RESULT 对称
					ev := ctx.Event(ai.EventServerToolResult)
					ev.RawType = "response.output_item"
					ev.SubType = itemType
					ev.ItemID = getStr(outputItem, "id")
					ev.Raw = responsesRawJSON(outputItem)
					ctx.Emit(ev)
				}
			}
		}

		if hasRefusal {
			// 与流式 refusal.done 对称：拒答终态只发一次
			ev := ctx.Event(ai.EventContentFilter)
			ev.RawType = "response.refusal.done"
			ev.Raw = responsesRawJSON(oResp)
			ctx.Emit(ev)
		}

		var blocksForMsg []ai.ContentBlock
		if len(mediaBlocks) > 0 {
			if textContent.Len() > 0 {
				blocksForMsg = append(blocksForMsg, ai.TextBlockOf(textContent.String()))
			}
			blocksForMsg = append(blocksForMsg, mediaBlocks...)
			if textContent.Len() > 0 {
				// 非流式正文也走统一事件聚合，否则后续 MEDIA_DONE 会使终态 blocks 只剩媒体
				ev := ctx.Event(ai.EventTextDelta)
				ev.RawType = "response.output_item"
				ev.Text = textContent.String()
				ev.Raw = responsesRawJSON(oResp)
				ctx.Emit(ev)
			}
			for _, block := range mediaBlocks {
				ev := ctx.Event(ai.EventMediaDone)
				ev.RawType = "response.output_item"
				ev.Block = block
				ev.Raw = responsesRawJSON(oResp)
				ctx.Emit(ev)
			}
		}

		// 思考与正文合并为单条消息（AssistantMessage 已分离 text/thinking）
		thinkingOut := reasoningContent.String()
		textOut := textContent.String()
		finishReason := "stop"
		if lastFinishReason != "" {
			finishReason = lastFinishReason
		}
		if len(allToolCalls) > 0 {
			finishReason = "tool_calls"
		}
		lastFinishReason = finishReason
		// 工具调用的终态原因必须回写：它是框架判定「要不要进下一轮工具调用」的唯一来源
		acc.SetFinishReason(finishReason)

		if textOut == "" && thinkingOut == "" && len(allToolCalls) == 0 && blocksForMsg == nil {
			// 回退到 output_text 便捷字段
			textOut = getStr(oResp, "output_text")
		}

		for i := range allToolCalls {
			call := &allToolCalls[i]
			emitResponsesFunctionCallStart(ctx, call.ID, call.Name)
			if call.ArgumentsStr != "" {
				ev := ctx.Event(ai.EventToolCallArgsDelta)
				ev.ToolCall = call
				ev.ToolCallID = call.ID
				ev.Text = call.ArgumentsStr
				ctx.Emit(ev)
			}
			endEv := ctx.Event(ai.EventToolCallEnd)
			endEv.ToolCall = call
			endEv.ToolCallID = call.ID
			ctx.Emit(endEv)
		}

		if textOut != "" || thinkingOut != "" || reasoningItemId != "" || reasoningEncrypted != "" ||
			len(allToolCalls) > 0 || blocksForMsg != nil {
			msg := &ai.AssistantMessage{Text: textOut, Thinking: thinkingOut, Blocks: blocksForMsg}
			if len(allToolCalls) > 0 {
				msg.ToolCalls = allToolCalls
			}
			// 官方多轮回放 reasoning 项需要 id/encrypted_content：只进协议状态
			meta := acc.AggregationMeta()
			if reasoningItemId != "" {
				meta[aggReasoningID] = reasoningItemId
			}
			if reasoningEncrypted != "" {
				meta[aggReasoningEnc] = reasoningEncrypted
			}
			if assistantPhase != "" {
				meta[aggPhase] = assistantPhase
			}
			if state := responsesStateFromAggregation(meta); state != nil {
				msg.ProtocolStates = map[string]any{responsesProtocolID: state}
				removeResponsesAggregationKeys(meta)
			}
			acc.SetTerminalMessage(msg)
		}
	} else {
		// 无 output 数组：尝试便捷字段 output_text / reasoning_text（DeepSeek 顶层字段）
		reasoningText := getStr(oResp, "reasoning_text")
		outputText := getStr(oResp, "output_text")
		if reasoningText != "" || outputText != "" {
			acc.SetTerminalMessage(ai.Snapshot(outputText, reasoningText, nil))
		}
	}

	if u := parseResponsesUsage(oResp["usage"]); u != nil {
		acc.SetUsage(u)
	}

	if !acc.TerminalPresent() {
		acc.SetTerminalMessage(ai.Snapshot("", "", nil))
	}

	if terminalStatusType != "" {
		ev := ctx.Event(ai.EventStatus)
		ev.RawType = terminalStatusType
		ev.SubType = terminalStatusReason
		ev.Text = lastFinishReason
		ev.Raw = responsesRawJSON(oResp)
		if ev.Attrs == nil {
			ev.Attrs = map[string]any{}
		}
		ev.Attrs["finish_reason"] = lastFinishReason
		ctx.Emit(ev)
	}

	acc.SetFinished(true)
}
