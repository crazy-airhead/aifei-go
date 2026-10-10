package agui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	ai "github.com/crazy-airhead/aifei-go/ai"
)

// Wrapper ChatEvent 流 → AG-UI 事件的适配器（对标 AgUiStreamWrapper）。
// threadId/runId 每流在 WriteStream 入口解析一次（空回退随机）；ID 生成策略
// 可注入。映射规则逐分支对标 Java map(ChatEvent)（含 TEXT_START 落 default 的
// 原语义：正文开界由 TEXT_DELTA 懒开，TEXT_START 本身降级 CUSTOM）。
type Wrapper struct {
	ids     IDGenerator
	threadID string
	runID    string
}

// NewWrapper 构造。threadID/runID 为空时各回退随机 UUID（对标 of() 缺省）；
// WriteStream 的显式入参优先。
func NewWrapper(threadID, runID string, ids ...IDGenerator) *Wrapper {
	g := DefaultIDGenerator
	if len(ids) > 0 && ids[0] != nil {
		g = ids[0]
	}
	if threadID == "" {
		threadID = g()
	}
	if runID == "" {
		runID = g()
	}
	return &Wrapper{ids: g, threadID: threadID, runID: runID}
}

// ThreadID 流线程标识。
func (w *Wrapper) ThreadID() string { return w.threadID }

// RunID 流运行标识。
func (w *Wrapper) RunID() string { return w.runID }

// WriteStream 流式写出：订阅 req 的 ChatEvent 流，逐事件映射为 AG-UI 事件并
// 以 `data: {...}\n\n` 帧写出（AG-UI 无 [DONE] 终标，以 RUN_FINISHED/RUN_ERROR
// 收口）。返回值约定与 aisdk.WriteStream 一致：上游失败已按 RUN_ERROR 收口后
// 原样返回错误；下游写失败直接返回（流已中止）。
func (w *Wrapper) WriteStream(ctx context.Context, out io.Writer, req ai.RequestDesc) error {
	st := newAGState(w, out)
	st.emit(NewRunStartedEvent(w.threadID, w.runID))

	err := req.Stream(ctx, ai.ChatSinkFunc(st.onEvent))
	if st.writeErr != nil {
		return st.writeErr
	}
	st.closeOut(err)
	return err
}

// 包级便捷入口（缺省 Wrapper）。
func WriteStream(ctx context.Context, out io.Writer, req ai.RequestDesc, threadID, runID string) error {
	return NewWrapper(threadID, runID).WriteStream(ctx, out, req)
}

// ==================== 流式状态机（对标私有 State） ====================

type agState struct {
	w   *Wrapper
	out io.Writer

	textIDs      map[string]string // contentKey → messageId
	reasoningIDs map[string]string
	toolIDs      map[string]string // contentKey → 生成 ID（兜底）

	openTextKeys   []string // 开启序（LinkedHashSet 对应物）
	openReasonKeys []string
	openTools    map[string]struct{}

	result     any    // RESPONSE_END 的响应快照
	agentError string // agent 流的异常终态（chat 流不产生）
	terminal   bool   // 已发过 ABORT/ERROR 终态

	writeErr error
}

func newAGState(w *Wrapper, out io.Writer) *agState {
	return &agState{
		w:            w,
		out:          out,
		textIDs:      map[string]string{},
		reasoningIDs: map[string]string{},
		toolIDs:      map[string]string{},
		openTools:    map[string]struct{}{},
	}
}

// onEvent 事件映射主体（逐 case 对标 AgUiStreamWrapper.map）。
func (st *agState) onEvent(ev *ai.ChatEvent) error {
	switch ev.Type {
	case ai.EventResponseStart:
		// RUN_STARTED 已由骨架发出，跳过

	case ai.EventResponseEnd:
		if r := ev.Response(); r != nil {
			st.result = r
		}

	case ai.EventStepStart:
		st.emitRaw(NewStepStartedEvent("step-"+strconv.Itoa(ev.Step)), ev)

	case ai.EventStepEnd:
		st.closeContents()
		st.emitRaw(NewStepFinishedEvent("step-"+strconv.Itoa(ev.Step)), ev)

	case ai.EventTextDelta:
		if ev.Text != "" {
			id := st.openText(ev)
			st.emitRaw(NewTextMessageContentEvent(id, ev.Text), ev)
		}

	case ai.EventTextEnd:
		st.closeText(ev)

	case ai.EventThinkingStart:
		st.openReasoning(ev)

	case ai.EventThinkingDelta:
		if ev.Text != "" {
			id := st.openReasoning(ev)
			st.emitRaw(NewReasoningMessageContentEvent(id, ev.Text), ev)
		}

	case ai.EventThinkingEnd:
		st.closeReasoning(ev)

	case ai.EventThinkingSignature:
		entityID := ev.ItemID
		if entityID == "" {
			entityID = ev.ToolCallID
		}
		st.emitRaw(NewReasoningEncryptedValueEvent(ev.SubType, entityID, ev.Text), ev)

	case ai.EventThinkingRedacted:
		payload := chatEventPayload(ev)
		st.emit(NewCustomEvent("thinking-redacted", payload))

	case ai.EventToolCallStart:
		id := st.toolID(ev)
		name := ""
		if ev.ToolCall != nil {
			name = ev.ToolCall.Name
		}
		st.openTools[id] = struct{}{}
		st.emitRaw(NewToolCallStartEvent(id, name, ev.ItemID), ev)

	case ai.EventToolCallArgsDelta:
		st.emitRaw(NewToolCallArgsEvent(st.toolID(ev), ev.Text), ev)

	case ai.EventToolCallEnd:
		id := st.toolID(ev)
		delete(st.openTools, id)
		st.emitRaw(NewToolCallEndEvent(id), ev)

	case ai.EventToolResult:
		st.emitRaw(NewToolCallResultEvent(st.toolID(ev), ev.Text, ev.ItemID), ev)

	case ai.EventAbort:
		st.closeContents()
		st.emitRaw(NewRunFinishedEvent(st.w.threadID, st.w.runID, st.result,
			&RunOutcome{Type: "interrupt"}), ev)
		st.terminal = true

	case ai.EventError:
		st.closeContents()
		errText := "Stream error"
		if ev.Err != nil {
			errText = ev.Err.Error()
		}
		st.emitRaw(NewRunErrorEvent(errText), ev)
		st.terminal = true

	case ai.EventRaw:
		st.emit(newAGUIRawEvent(ev.Raw))

	case ai.EventCustom:
		name := ev.SubType
		if name == "" {
			name = "custom"
		}
		payload := chatEventPayload(ev)
		st.emit(NewCustomEvent(name, payload))

	default:
		// STATUS/HEARTBEAT/USAGE/SERVER_TOOL_*/SEARCH_RESULT/CITATION/MEDIA_*/
		// REFUSAL_DELTA/CONTENT_FILTER/TEXT_START 及未来类型
		st.emit(NewCustomEvent(strings.ToLower(ev.Type.String()), chatEventPayload(ev)))
	}
	return st.writeErr
}

// closeOut 流收口（正常完成与上游错误共用；对标 finishSuccess/finishError）。
func (st *agState) closeOut(upstreamErr error) {
	if st.terminal {
		return
	}
	if st.agentError != "" {
		st.closeContents()
		st.emitRaw(NewRunErrorEvent(st.agentError), nil)
		return
	}
	if upstreamErr != nil {
		st.closeContents()
		st.emitRaw(NewRunErrorEvent(upstreamErr.Error()), nil)
		return
	}
	st.closeContents()
	st.emitRaw(NewRunFinishedEvent(st.w.threadID, st.w.runID, st.result, nil), nil)
}

// openText 懒开正文（首帧发 TEXT_MESSAGE_START；同 key 复用 messageId）。
func (st *agState) openText(ev *ai.ChatEvent) string {
	key := contentKey(ev)
	id, ok := st.textIDs[key]
	if !ok {
		id = st.w.ids()
		st.textIDs[key] = id
	}
	if !containsKey(st.openTextKeys, key) {
		st.openTextKeys = append(st.openTextKeys, key)
		st.emitRaw(NewTextMessageStartEvent(id), nil)
	}
	return id
}

func (st *agState) textIDOf(ev *ai.ChatEvent) string {
	return st.textIDs[contentKey(ev)]
}

// closeText 正文闭（幂等；ids 保留——同 key 再来 delta 复用 id 并重发 start）。
func (st *agState) closeText(ev *ai.ChatEvent) {
	key := contentKey(ev)
	if id, ok := st.textIDs[key]; ok && containsKey(st.openTextKeys, key) {
		st.openTextKeys = removeKey(st.openTextKeys, key)
		st.emitRaw(NewTextMessageEndEvent(id), ev)
	}
}

// openReasoning 懒开推理（首帧连发 REASONING_START + REASONING_MESSAGE_START）。
func (st *agState) openReasoning(ev *ai.ChatEvent) string {
	key := contentKey(ev)
	id, ok := st.reasoningIDs[key]
	if !ok {
		id = st.w.ids()
		st.reasoningIDs[key] = id
	}
	if !containsKey(st.openReasonKeys, key) {
		st.openReasonKeys = append(st.openReasonKeys, key)
		st.emitRaw(NewReasoningStartEvent(id), nil)
		st.emitRaw(NewReasoningMessageStartEvent(id), nil)
	}
	return id
}

// closeReasoning 推理闭（先 REASONING_MESSAGE_END 后 REASONING_END）。
func (st *agState) closeReasoning(ev *ai.ChatEvent) {
	key := contentKey(ev)
	if id, ok := st.reasoningIDs[key]; ok && containsKey(st.openReasonKeys, key) {
		st.openReasonKeys = removeKey(st.openReasonKeys, key)
		st.emitRaw(NewReasoningMessageEndEvent(id), ev)
		st.emitRaw(NewReasoningEndEvent(id), ev)
	}
}

// closeContents 收口未闭合内容（推理先于正文、按开启序；合成事件不带 rawEvent）。
func (st *agState) closeContents() {
	for _, key := range append([]string(nil), st.openReasonKeys...) {
		if id, ok := st.reasoningIDs[key]; ok {
			st.emitRaw(NewReasoningMessageEndEvent(id), nil)
			st.emitRaw(NewReasoningEndEvent(id), nil)
		}
	}
	st.openReasonKeys = nil
	for _, key := range append([]string(nil), st.openTextKeys...) {
		if id, ok := st.textIDs[key]; ok {
			st.emitRaw(NewTextMessageEndEvent(id), nil)
		}
	}
	st.openTextKeys = nil
}

// toolID 工具调用标识（供应商 id 优先；否则按 contentKey 分配稳定 id）。
func (st *agState) toolID(ev *ai.ChatEvent) string {
	if id := ev.ToolCallID; id != "" {
		return id
	}
	if ev.ToolCall != nil && ev.ToolCall.ID != "" {
		return ev.ToolCall.ID
	}
	key := contentKey(ev)
	if v, ok := st.toolIDs[key]; ok {
		return v
	}
	v := st.w.ids()
	st.toolIDs[key] = v
	return v
}

// emitRaw 写一帧事件（rawEvent 回填供应商原始帧；nil 表示合成事件）。
func (st *agState) emitRaw(e Event, source *ai.ChatEvent) {
	if source != nil && len(source.Raw) > 0 {
		setRawEvent(e, json.RawMessage(source.Raw))
	}
	st.emit(e)
}

func (st *agState) emit(e Event) {
	if st.writeErr != nil {
		return
	}
	st.writeErr = st.writeFrame(e)
}

func (st *agState) writeFrame(e Event) error {
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(st.out, "data: %s\n\n", data); err != nil {
		return err
	}
	if f, ok := st.out.(interface{ Flush() }); ok {
		f.Flush()
	}
	return nil
}

// setRawEvent 事件基的 rawEvent 回填（Base 内嵌字段逐一设置——事件为具体类型，
// 无统一 setter 面）。
func setRawEvent(e Event, raw any) {
	type baseHolder interface{ base() *Base }
	switch v := e.(type) {
	case *RunStartedEvent:
		v.RawEvent = raw
	case *RunFinishedEvent:
		v.RawEvent = raw
	case *RunErrorEvent:
		v.RawEvent = raw
	case *StepStartedEvent:
		v.RawEvent = raw
	case *StepFinishedEvent:
		v.RawEvent = raw
	case *TextMessageStartEvent:
		v.RawEvent = raw
	case *TextMessageContentEvent:
		v.RawEvent = raw
	case *TextMessageEndEvent:
		v.RawEvent = raw
	case *ToolCallStartEvent:
		v.RawEvent = raw
	case *ToolCallArgsEvent:
		v.RawEvent = raw
	case *ToolCallEndEvent:
		v.RawEvent = raw
	case *ToolCallResultEvent:
		v.RawEvent = raw
	case *ReasoningStartEvent:
		v.RawEvent = raw
	case *ReasoningEndEvent:
		v.RawEvent = raw
	case *ReasoningMessageStartEvent:
		v.RawEvent = raw
	case *ReasoningMessageContentEvent:
		v.RawEvent = raw
	case *ReasoningMessageEndEvent:
		v.RawEvent = raw
	case *ReasoningEncryptedValueEvent:
		v.RawEvent = raw
	case *CustomEvent:
		v.RawEvent = raw
	}
}

// aguiRawEvent RAW 事件（rawEvent 为载荷本体）。
type aguiRawEvent struct {
	Base
}

func newAGUIRawEvent(raw []byte) *aguiRawEvent {
	return &aguiRawEvent{Base{Type: EventRaw, Timestamp: nowMillis(), RawEvent: json.RawMessage(raw)}}
}

// chatEventPayload CUSTOM 的 value/rawEvent 形态（对标 chatEventPayload 的有序
// 键集：eventType 恒有、step 恒有、raw 恒有，其余非空才放）。
func chatEventPayload(ev *ai.ChatEvent) map[string]any {
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
	data["step"] = ev.Step
	if ev.ItemID != "" {
		data["itemId"] = ev.ItemID
	}
	if ev.ToolCallID != "" {
		data["toolCallId"] = ev.ToolCallID
	}
	if ev.Index >= 0 {
		data["index"] = ev.Index
	}
	if ev.Text != "" {
		data["text"] = ev.Text
	}
	if ev.SearchResult != nil {
		data["searchResult"] = map[string]any{
			"index": ev.SearchResult.Index, "id": ev.SearchResult.ID,
			"title": ev.SearchResult.Title, "url": ev.SearchResult.URL,
			"snippet": ev.SearchResult.Snippet,
		}
	}
	if ev.Citation != nil {
		data["citation"] = map[string]any{
			"title": ev.Citation.Title, "url": ev.Citation.URL,
			"citedText": ev.Citation.Snippet,
		}
	}
	if len(ev.Attrs) > 0 {
		data["attrs"] = ev.Attrs
	}
	if ev.Block != nil {
		data["block"] = map[string]any{
			"type": ev.Block.Kind(), "content": ev.Block.Content(), "mimeType": ev.Block.MimeType(),
		}
	}
	if len(ev.Raw) > 0 {
		data["raw"] = json.RawMessage(ev.Raw)
	} else {
		data["raw"] = nil
	}
	return data
}

// contentKey 内容键（response/step/item 定位一条消息的开闭边界）。
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
func (e *aguiRawEvent) eventType() EventType { return e.Type }
