package aisdk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	ai "github.com/crazy-airhead/aifei-go/ai"
)

// Wrapper ChatEvent 流 → AI SDK SSE parts 的适配器（对标 AiSdkStreamWrapper）。
// ID 生成策略可注入（测试用确定性生成器）。
type Wrapper struct {
	ids IDGenerator
}

// NewWrapper 构造（ids 缺省 DefaultIDGenerator）。
func NewWrapper(ids ...IDGenerator) *Wrapper {
	g := DefaultIDGenerator
	if len(ids) > 0 && ids[0] != nil {
		g = ids[0]
	}
	return &Wrapper{ids: g}
}

// WriteStream 流式写出：订阅 req 的 ChatEvent 流，逐事件映射为 part 帧写入 w
// （对标 toAiSdkStream(Flux<ChatEvent>, metadata)）。meta 非 nil 时在 start 后
// 追加 message-metadata part。
//
// 返回值约定：
//   - 正常完成：nil（finish part + [DONE] 已写出）
//   - 上游失败：协议帧已按 error + finish("error") + [DONE] 收口后原样返回错误
//     （调用方不必再写响应体，但可记日志）
//   - 下游写出失败：直接返回写错误（流已中止，帧无法送达）
//
// w 实现 http.Flusher 时逐帧刷新（SSE 实时性）。
func (w *Wrapper) WriteStream(ctx context.Context, out io.Writer, req ai.RequestDesc, meta map[string]any) error {
	st := newStreamState(w, out)
	st.emit(NewStartPart(idsOfMessage(w.ids)))
	if len(meta) > 0 {
		st.emit(NewMetadataPart(meta))
	}

	err := req.Stream(ctx, ai.ChatSinkFunc(st.onEvent))
	if st.writeErr != nil {
		return st.writeErr // 下游已断：上游已被 sink 错误中止（对标 sink.onDispose(upstream)）
	}
	st.closeOut(err)
	return err
}

// WrapResponse 非流式一次性写出：把一次 call 的聚合响应直接展开为 parts
// （对标 toAiSdkStream(ChatResponse)）。展开序与 Java 一致：start → 推理三连 →
// 工具调用 → 来源引用 → 正文三连 → 非文本块 file part → finish → [DONE]。
func (w *Wrapper) WrapResponse(resp *ai.Response, out io.Writer) error {
	st := newStreamState(w, out)
	st.emit(NewStartPart(idsOfMessage(w.ids)))

	if msg := resp.Message(); msg != nil {
		if msg.Thinking != "" {
			rid := idsOfReasoning(w.ids)
			st.emit(NewReasoningStartPart(rid))
			st.emit(NewReasoningDeltaPart(rid, msg.Thinking))
			st.emit(NewReasoningEndPart(rid))
		}
		for _, tc := range msg.ToolCalls {
			tcID := tc.ID
			if tcID == "" {
				tcID = idsOfToolCall(w.ids)
			}
			st.emit(NewToolInputStartPart(tcID, tc.Name))
			if tc.ArgumentsStr != "" {
				st.emit(NewToolInputDeltaPart(tcID, tc.ArgumentsStr))
			}
			st.emit(NewToolInputAvailablePart(tcID, tc.Name, tc.Arguments))
		}
		for i := range msg.SearchResults {
			st.emitSearchResult(&msg.SearchResults[i])
		}
		for i := range msg.Citations {
			st.emitCitation(&msg.Citations[i], "")
		}
		if msg.Text != "" {
			tid := idsOfText(w.ids)
			st.emit(NewTextStartPart(tid))
			st.emit(NewTextDeltaPart(tid, msg.Text))
			st.emit(NewTextEndPart(tid))
		}
		for _, block := range resp.Blocks() {
			if block == nil || block.Kind() == "text" {
				continue
			}
			if s := contentString(block.Content()); s != "" {
				st.emit(NewFilePart(s, block.MimeType()))
			}
		}
	}

	reason := resp.FinishReason()
	if reason == "" {
		reason = "stop"
	}
	st.emit(NewFinishPart(reason, finishUsageOf(resp.Usage())))
	st.writeDone()
	return st.writeErr
}

// 包级便捷入口（缺省 Wrapper）。

func WriteStream(ctx context.Context, w io.Writer, req ai.RequestDesc, meta map[string]any) error {
	return NewWrapper().WriteStream(ctx, w, req, meta)
}

func WrapResponse(resp *ai.Response, w io.Writer) error {
	return NewWrapper().WrapResponse(resp, w)
}

// ==================== 流式状态机（对标 EventState + onEvent） ====================

type streamState struct {
	w   *Wrapper
	out io.Writer

	textIDs        map[string]string // 内容键 → part id
	reasoningIDs   map[string]string
	openTextKeys   []string // 开启序（LinkedHashSet 对应物）
	openReasonKeys []string

	toolInputStarted map[string]bool   // 已发过 tool-input-start 的工具调用 id
	toolIDs          map[string]string // 无供应商 id 时按事件身份分配的稳定 id
	serverToolNames  map[string]string // 服务端工具名（itemId/subType → name）

	finishReason string
	usage        *ai.Usage

	errorSeen bool
	errorText string

	writeErr error // 下游写出失败（中止上游、跳过后续收口写出）
}

func newStreamState(w *Wrapper, out io.Writer) *streamState {
	return &streamState{
		w:                w,
		out:              out,
		textIDs:          map[string]string{},
		reasoningIDs:     map[string]string{},
		toolInputStarted: map[string]bool{},
		toolIDs:          map[string]string{},
		serverToolNames:  map[string]string{},
	}
}

// onEvent 事件 → part 映射主体（逐 case 对标 AiSdkStreamWrapper.onEvent）。
func (st *streamState) onEvent(ev *ai.ChatEvent) error {
	switch ev.Type {
	case ai.EventStepStart:
		st.emit(NewStartStepPart())

	case ai.EventStepEnd:
		// 步骤内未闭合的内容 part 先收口，避免跨步骤悬挂
		st.closeOpenParts()
		st.emit(NewFinishStepPart())

	case ai.EventTextStart:
		st.openText(ev)

	case ai.EventTextDelta:
		if ev.Text != "" {
			st.emit(NewTextDeltaPart(st.openText(ev), ev.Text))
		}

	case ai.EventTextEnd:
		st.closeText(ev)

	case ai.EventThinkingStart:
		st.openReasoning(ev)

	case ai.EventThinkingDelta:
		if ev.Text != "" {
			st.emit(NewReasoningDeltaPart(st.openReasoning(ev), ev.Text))
		}

	case ai.EventThinkingEnd:
		st.closeReasoning(ev)

	case ai.EventThinkingSignature:
		st.emit(NewDataPart("thinking-signature", eventPayload(ev)))

	case ai.EventThinkingRedacted:
		st.emit(NewDataPart("thinking-redacted", eventPayload(ev)))

	case ai.EventToolCallStart:
		st.emitToolInputStart(ev)

	case ai.EventToolCallArgsDelta:
		st.emitToolInputDelta(ev)

	case ai.EventToolCallEnd:
		st.emitToolCall(ev)

	case ai.EventToolResult:
		// 本地工具执行结果（流内可见，旧帧流投影下不可达）
		st.emit(NewToolOutputAvailablePart(ev.ToolCallID, ev.Text))

	case ai.EventServerToolStart:
		st.rememberServerToolName(ev)
		st.emit(NewDataPart("server-tool-start", eventPayload(ev)))

	case ai.EventServerToolArgsDelta:
		st.emit(NewDataPart("server-tool-args", eventPayload(ev)))

	case ai.EventServerToolResult:
		// 服务端工具无 TOOL_CALL_* 帧：幂等补齐 input 部件对再发结果，
		// 标准客户端不会丢弃无 input 的 output
		tcID := ev.ItemID
		if tcID == "" {
			tcID = ev.SubType
		}
		tcName := st.serverToolNames[tcID]
		if tcName == "" {
			tcName = ev.SubType
		}
		if !st.toolInputStarted[tcID] {
			st.toolInputStarted[tcID] = true
			st.emit(NewToolInputStartPart(tcID, tcName))
			st.emit(NewToolInputAvailablePart(tcID, tcName, nil))
		}
		st.emit(NewToolOutputAvailablePart(tcID, ev.Text))

	case ai.EventSearchResult:
		st.emitSearchResult(ev.SearchResult)

	case ai.EventCitation:
		st.emitCitationEvent(ev)

	case ai.EventMediaPartial:
		st.emit(NewDataPart("media-partial", eventPayload(ev)))

	case ai.EventMediaDone:
		// 媒体完成的语义负载就是当前块本身，不依赖终态 response
		if ev.Block != nil {
			if s := contentString(ev.Block.Content()); s != "" {
				st.emit(NewFilePart(s, ev.Block.MimeType()))
			}
		}

	case ai.EventUsage:
		if ev.Usage != nil {
			st.usage = ev.Usage
		}

	case ai.EventAbort:
		st.emit(NewAbortPart(ev.Text))

	case ai.EventError:
		// 核心失败路径：先发终态 ERROR 事件再返回错误；此处只记录信息，
		// error/finish part 由 closeOut 统一发射（避免同次失败发两遍）
		st.errorSeen = true
		st.errorText = "Stream error"
		if ev.Err != nil {
			st.errorText = ev.Err.Error()
		}
		if ev.Usage != nil {
			st.usage = ev.Usage
		}

	case ai.EventResponseEnd:
		if ev.Usage != nil {
			st.usage = ev.Usage
		}
		if r := ev.Response(); r != nil && r.FinishReason() != "" {
			st.finishReason = r.FinishReason()
		}

	case ai.EventRefusalDelta:
		st.emit(NewDataPart("refusal", eventPayload(ev)))

	case ai.EventContentFilter:
		st.emit(NewDataPart("content-filter", eventPayload(ev)))

	case ai.EventCustom:
		name := ev.SubType
		if name == "" {
			name = "custom"
		}
		st.emit(NewDataPart(name, eventPayload(ev)))

	case ai.EventRaw:
		// 缺省事件过滤器会挡掉 RAW；调用方显式放行才可达
		name := ev.RawType
		if name == "" {
			name = "raw"
		}
		st.emit(NewDataPart(name, json.RawMessage(ev.Raw)))

	case ai.EventStatus, ai.EventHeartbeat:
		st.emit(NewDataPart(strings.ToLower(ev.Type.String()), eventPayload(ev)))
	}
	return st.writeErr
}

// closeOut 流收口（正常完成与上游错误共用）：先闭合未完 part，再按
// error/finish 终态收尾；上游错误文案作 errorText 兜底。
func (st *streamState) closeOut(upstreamErr error) {
	st.closeOpenParts()

	errorText := st.errorText
	if st.errorSeen {
		st.emit(NewErrorPart(errorText))
		st.emit(NewFinishPart("error", finishUsageOf(st.usage)))
	} else if upstreamErr != nil {
		// 上游失败但未发过 ERROR 事件（如传输层错误直返）
		st.emit(NewErrorPart(upstreamErr.Error()))
		st.emit(NewFinishPart("error", finishUsageOf(st.usage)))
	} else {
		reason := st.finishReason
		if reason == "" {
			reason = "stop"
		}
		st.emit(NewFinishPart(reason, finishUsageOf(st.usage)))
	}
	st.writeDone()
}

// emit 写一帧 part（下游失败记录 writeErr，后续帧跳过——错误经 onEvent 返回
// 上游后流被中止）。
func (st *streamState) emit(p Part) {
	if st.writeErr != nil {
		return
	}
	st.writeErr = st.writeFrame(p)
}

func (st *streamState) writeFrame(p Part) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return st.writeRawData(data)
}

func (st *streamState) writeDone() {
	if st.writeErr != nil {
		return
	}
	_ = st.writeRawData([]byte("[DONE]"))
}

func (st *streamState) writeRawData(data []byte) error {
	if _, err := fmt.Fprintf(st.out, "data: %s\n\n", data); err != nil {
		return err
	}
	if f, ok := st.out.(interface{ Flush() }); ok {
		f.Flush()
	}
	return nil
}

// closeOpenParts 闭合所有未闭合的 reasoning/text part（reasoning 先于 text，
// 按开启序）。
func (st *streamState) closeOpenParts() {
	for _, key := range append([]string(nil), st.openReasonKeys...) {
		if id, ok := st.reasoningIDs[key]; ok {
			st.emit(NewReasoningEndPart(id))
		}
	}
	st.openReasonKeys = nil
	for _, key := range append([]string(nil), st.openTextKeys...) {
		if id, ok := st.textIDs[key]; ok {
			st.emit(NewTextEndPart(id))
		}
	}
	st.openTextKeys = nil
}

func (st *streamState) openText(ev *ai.ChatEvent) string {
	key := contentKey(ev)
	id, ok := st.textIDs[key]
	if !ok {
		id = idsOfText(st.w.ids)
		st.textIDs[key] = id
	}
	if !containsKey(st.openTextKeys, key) {
		st.openTextKeys = append(st.openTextKeys, key)
		st.emit(NewTextStartPart(id))
	}
	return id
}

func (st *streamState) closeText(ev *ai.ChatEvent) {
	key := contentKey(ev)
	if id, ok := st.textIDs[key]; ok && containsKey(st.openTextKeys, key) {
		st.openTextKeys = removeKey(st.openTextKeys, key)
		st.emit(NewTextEndPart(id))
	}
}

func (st *streamState) openReasoning(ev *ai.ChatEvent) string {
	key := contentKey(ev)
	id, ok := st.reasoningIDs[key]
	if !ok {
		id = idsOfReasoning(st.w.ids)
		st.reasoningIDs[key] = id
	}
	if !containsKey(st.openReasonKeys, key) {
		st.openReasonKeys = append(st.openReasonKeys, key)
		st.emit(NewReasoningStartPart(id))
	}
	return id
}

func (st *streamState) closeReasoning(ev *ai.ChatEvent) {
	key := contentKey(ev)
	if id, ok := st.reasoningIDs[key]; ok && containsKey(st.openReasonKeys, key) {
		st.openReasonKeys = removeKey(st.openReasonKeys, key)
		st.emit(NewReasoningEndPart(id))
	}
}

// contentKey 内容键：response/step/item 定位一条 part 的开闭边界（对标
// contentKey；namespace 留给 P2 agent 轮次隔离）。
func contentKey(ev *ai.ChatEvent) string {
	respID := ev.ResponseID
	if respID == "" {
		respID = "default"
	}
	key := "response:" + respID + ":step:" + strconv.Itoa(ev.Step) + ":"
	switch {
	case ev.ItemID != "":
		return key + "item:" + ev.ItemID
	case ev.Index >= 0:
		return key + "index:" + strconv.Itoa(ev.Index)
	default:
		return key + "default"
	}
}

// emitToolInputStart 工具输入开始（同一工具调用只发一次）。
func (st *streamState) emitToolInputStart(ev *ai.ChatEvent) string {
	tcID := st.resolveToolCallID(ev)
	if !st.toolInputStarted[tcID] {
		st.toolInputStarted[tcID] = true
		name := ""
		if ev.ToolCall != nil {
			name = ev.ToolCall.Name
		}
		st.emit(NewToolInputStartPart(tcID, name))
	}
	return tcID
}

// emitToolInputDelta 工具参数增量（未开过 start 的先幂等补开）。
func (st *streamState) emitToolInputDelta(ev *ai.ChatEvent) {
	tcID := st.emitToolInputStart(ev)
	if ev.Text != "" {
		st.emit(NewToolInputDeltaPart(tcID, ev.Text))
	}
}

// emitToolCall 工具调用完整体：参数未走增量通道时（整块方言）补齐 start+delta，
// 再发 available。
func (st *streamState) emitToolCall(ev *ai.ChatEvent) {
	tc := ev.ToolCall
	if tc == nil {
		return
	}
	tcID := st.resolveToolCallID(ev)
	if !st.toolInputStarted[tcID] {
		st.toolInputStarted[tcID] = true
		st.emit(NewToolInputStartPart(tcID, tc.Name))
		if tc.ArgumentsStr != "" {
			st.emit(NewToolInputDeltaPart(tcID, tc.ArgumentsStr))
		}
	}
	st.emit(NewToolInputAvailablePart(tcID, tc.Name, tc.Arguments))
}

// resolveToolCallID 供应商 id 优先；否则按事件身份分配稳定 id。
func (st *streamState) resolveToolCallID(ev *ai.ChatEvent) string {
	id := ev.ToolCallID
	if id == "" && ev.ToolCall != nil {
		id = ev.ToolCall.ID
	}
	if id != "" {
		return id
	}
	key := contentKey(ev)
	if v, ok := st.toolIDs[key]; ok {
		return v
	}
	v := idsOfToolCall(st.w.ids)
	st.toolIDs[key] = v
	return v
}

func (st *streamState) rememberServerToolName(ev *ai.ChatEvent) {
	id := ev.ItemID
	if id == "" {
		id = ev.SubType
	}
	if id != "" && ev.SubType != "" {
		if _, ok := st.serverToolNames[id]; !ok {
			st.serverToolNames[id] = ev.SubType
		}
	}
}

func (st *streamState) emitSearchResult(r *ai.SearchResult) {
	if r == nil || r.URL == "" {
		return
	}
	sourceID := r.ID
	if sourceID == "" {
		sourceID = r.URL
	}
	title := r.Title
	if title == "" {
		title = r.URL
	}
	st.emit(NewSourceUrlPart(sourceID, r.URL, title))
}

// emitCitationEvent 事件路径的引用（优先类型化负载；旧形态把 text 当 URL）。
func (st *streamState) emitCitationEvent(ev *ai.ChatEvent) {
	if ev.Citation != nil {
		st.emitCitation(ev.Citation, ev.ItemID)
		return
	}
	if ev.Text != "" {
		sourceID := ev.ItemID
		if sourceID == "" {
			sourceID = ev.Text
		}
		st.emit(NewSourceUrlPart(sourceID, ev.Text, ev.Text))
	} else if ev.ItemID != "" {
		st.emit(NewSourceDocumentPart(ev.ItemID, ev.SubType, ev.ItemID))
	}
}

// emitCitation 类型化引用：有 URL 走 source-url，否则 source-document。
// Go Citation 无 type 字段，mediaType 由调用方补（事件路径用 SubType）。
func (st *streamState) emitCitation(c *ai.Citation, preferredID string) {
	if c == nil {
		return
	}
	sourceID := firstNonEmpty(preferredID, c.URL, c.Title, c.Snippet)
	if sourceID == "" {
		return
	}
	if c.URL != "" {
		title := c.Title
		if title == "" {
			title = c.URL
		}
		st.emit(NewSourceUrlPart(sourceID, c.URL, title))
	} else {
		title := c.Title
		if title == "" {
			title = sourceID
		}
		st.emit(NewSourceDocumentPart(sourceID, "", title))
	}
}

// eventPayload 未建模事件的通用降级负载（对标 eventPayload：有值才带键）。
func eventPayload(ev *ai.ChatEvent) map[string]any {
	data := map[string]any{"eventType": ev.Type.String()}
	if ev.RawType != "" {
		data["rawType"] = ev.RawType
	}
	if ev.SubType != "" {
		data["subType"] = ev.SubType
	}
	if ev.ResponseID != "" {
		data["responseId"] = ev.ResponseID
	}
	if ev.ProviderResponseID != "" {
		data["providerResponseId"] = ev.ProviderResponseID
	}
	if ev.ItemID != "" {
		data["itemId"] = ev.ItemID
	}
	if ev.ToolCallID != "" {
		data["toolCallId"] = ev.ToolCallID
	}
	if ev.Text != "" {
		data["text"] = ev.Text
	}
	if len(ev.Attrs) > 0 {
		data["attrs"] = ev.Attrs
	}
	if len(ev.Raw) > 0 {
		data["raw"] = json.RawMessage(ev.Raw)
	}
	return data
}

// finishUsageOf AiUsage → finish part 用量摘要。
func finishUsageOf(u *ai.Usage) *FinishUsage {
	if u == nil {
		return nil
	}
	return &FinishUsage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
		ReasoningTokens:  u.ThinkTokens,
	}
}

// contentString 块内容的字符串形态（string / []byte / 其余 fmt 兜底）。
func contentString(v any) string {
	switch s := v.(type) {
	case nil:
		return ""
	case string:
		return s
	case []byte:
		return string(s)
	default:
		return fmt.Sprint(v)
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func containsKey(list []string, key string) bool {
	for _, k := range list {
		if k == key {
			return true
		}
	}
	return false
}

func removeKey(list []string, key string) []string {
	out := list[:0]
	for _, k := range list {
		if k != key {
			out = append(out, k)
		}
	}
	return out
}
