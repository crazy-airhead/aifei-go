package ai

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Accumulator 事件流聚合器（对标 ChatAccumulator）：事件 → AssistantMessage/Usage/
// ToolCall 完全体。Merge 幂等吸收，是方言事件管线的唯一聚合入口。
type Accumulator struct {
	// 有序内容条目：文本块（按 item/index/default 键追踪）与媒体块交错保序
	entries []*accEntry
	// 文本聚合键的当前槽（加速连续 delta；换键时重定位）
	curTextKey string

	thinking strings.Builder
	// thinkingSignature 幂等赋值（绝不追加）
	thinkingSignature string

	toolCalls map[string]*ToolCallBuilder
	toolOrder []string

	searchResults []SearchResult
	citations     []Citation

	usage        *Usage
	err          error
	finishReason string
	stream       bool

	// 终态载体（对标 terminalXxx：事件聚合优先、载体兜底；toolCalls 以载体为先——
	// Java buildTerminalMessage 同一解析序）。非流式整条消息 replace，流式非空字段覆盖。
	terminalPresent        bool
	terminalText           string
	terminalThinking       string
	terminalToolCalls      []ToolCall
	terminalBlocks         []ContentBlock
	terminalSearchResults  []SearchResult
	terminalCitations      []Citation
	terminalProtocolStates map[string]any
	terminalMetadata       map[string]any

	// 方言可变工作台（对标 ChatAccumulator 的公共字段）
	model              string
	finished           bool
	lastToolCallID     string
	inThinking         bool // <think> 标签 / 纯 reasoning 帧通道开启中
	hasReasoningField  bool
	reasoningFieldName string // "reasoning_content" | "reasoning"

	// aggregationMeta 方言解析工作台（对标 Java aggregationMetadata）：
	// 请求内累积、终帧提升为协议状态；与 Metadata 分离，不进入最终消息。
	aggregationMeta map[string]any
}

type accEntry struct {
	isText bool
	key    string
	text   strings.Builder
	block  ContentBlock
}

// ToolCallBuilder 流式工具调用构建器（args 增量拼接；身份晚到时迁移键）。
type ToolCallBuilder struct {
	Index string
	ID    string
	Name  string
	args  strings.Builder
}

// Args 已累计的参数 JSON 片段。
func (b *ToolCallBuilder) Args() string { return b.args.String() }

func NewAccumulator() *Accumulator {
	return &Accumulator{toolCalls: map[string]*ToolCallBuilder{}}
}

// textKey 事件定位文本槽的键（text:item:<id> / text:index:<n> / text:default）。
func textKey(ev *ChatEvent) string {
	switch {
	case ev.ItemID != "":
		return "text:item:" + ev.ItemID
	case ev.Index >= 0:
		return "text:index:" + strconv.Itoa(ev.Index)
	default:
		return "text:default"
	}
}

// toolKey 工具调用聚合键：index（"idx:<n>"）优先于 id。
func toolKey(tc *ToolCall) string {
	if tc == nil {
		return ""
	}
	if tc.Index != "" {
		return tc.Index
	}
	if tc.ID != "" {
		return tc.ID
	}
	return ""
}

// Merge 吸收一个事件（幂等；签名覆写不追加）。
func (a *Accumulator) Merge(ev *ChatEvent) {
	if ev == nil {
		return
	}
	switch ev.Type {
	case EventTextDelta:
		key := textKey(ev)
		slot := a.textSlot(key)
		slot.text.WriteString(ev.Text)
	case EventMediaDone:
		if ev.Block != nil {
			a.entries = append(a.entries, &accEntry{block: ev.Block})
			a.curTextKey = ""
		}
	case EventThinkingDelta:
		a.thinking.WriteString(ev.Text)
	case EventThinkingSignature:
		a.thinkingSignature = ev.Text
	case EventToolCallStart, EventToolCallArgsDelta, EventToolCallEnd:
		a.mergeToolCall(ev)
		// END 携带完整体时作为终态协议载体保存（直接事件形态方言的兜底路径）
		if ev.Type == EventToolCallEnd && ev.ToolCall != nil && ev.ToolCall.Name != "" {
			a.mergeTerminalToolCall(*ev.ToolCall)
		}
	case EventSearchResult:
		if ev.SearchResult != nil {
			a.searchResults = append(a.searchResults, *ev.SearchResult)
		}
	case EventCitation:
		if ev.Citation != nil {
			a.citations = append(a.citations, *ev.Citation)
		}
	case EventUsage:
		if ev.Usage != nil {
			a.usage = ev.Usage
		}
	case EventError:
		if ev.Err != nil {
			a.err = ev.Err
		}
	}
	if fr, ok := ev.Attrs["finish_reason"].(string); ok && fr != "" {
		a.finishReason = fr
	}
}

func (a *Accumulator) textSlot(key string) *accEntry {
	if a.curTextKey == key {
		for i := len(a.entries) - 1; i >= 0; i-- {
			if e := a.entries[i]; e.isText && e.key == key {
				return e
			}
		}
	}
	for _, e := range a.entries {
		if e.isText && e.key == key {
			a.curTextKey = key
			return e
		}
	}
	e := &accEntry{isText: true, key: key}
	a.entries = append(a.entries, e)
	a.curTextKey = key
	return e
}

func (a *Accumulator) mergeToolCall(ev *ChatEvent) {
	tc := ev.ToolCall
	key := toolKey(tc)
	if key == "" && ev.ToolCallID != "" {
		key = ev.ToolCallID
	}
	if key == "" {
		key = "idx:-1"
	}
	b, ok := a.toolCalls[key]
	if !ok {
		// 身份晚到迁移：先按 id 建槽、随后 index 到达 → 迁移到 idx 键
		if tc != nil && tc.Index != "" {
			if old, exist := a.toolCalls[tc.ID]; exist && tc.ID != "" {
				b, ok = old, true
				delete(a.toolCalls, tc.ID)
				a.toolCalls[key] = b
				for i, k := range a.toolOrder {
					if k == tc.ID {
						a.toolOrder[i] = key
					}
				}
			}
		}
		if !ok {
			b = &ToolCallBuilder{}
			a.toolCalls[key] = b
			a.toolOrder = append(a.toolOrder, key)
		}
	}
	if tc != nil {
		if tc.Index != "" {
			b.Index = tc.Index
		}
		if tc.ID != "" {
			b.ID = tc.ID
		}
		if tc.Name != "" {
			b.Name = tc.Name
		}
	}
	switch ev.Type {
	case EventToolCallArgsDelta:
		b.args.WriteString(ev.Text)
	case EventToolCallEnd:
		// END 携带完整体时合并终态（此前仅有增量片段）
		if tc != nil && tc.ArgumentsStr != "" && b.args.Len() == 0 {
			b.args.WriteString(tc.ArgumentsStr)
		}
	}
}

// buildToolCalls 依首次出现序产出完整 ToolCall 列表（args 解析失败保留字符串形态）。
func (a *Accumulator) buildToolCalls() []ToolCall {
	if len(a.toolOrder) == 0 {
		return nil
	}
	out := make([]ToolCall, 0, len(a.toolOrder))
	for _, key := range a.toolOrder {
		b := a.toolCalls[key]
		tc := ToolCall{
			Index:        b.Index,
			ID:           b.ID,
			Name:         b.Name,
			ArgumentsStr: b.args.String(),
		}
		if tc.ArgumentsStr != "" {
			var m map[string]any
			if err := json.Unmarshal([]byte(tc.ArgumentsStr), &m); err == nil {
				tc.Arguments = m
			}
		}
		out = append(out, tc)
	}
	return out
}

// Message 当前聚合快照（每次调用新建，可安全持有）。
// 解析序对标 Java buildTerminalMessage：文本/思考/媒体事件聚合优先、终态载体
// 兜底；工具调用以终态载体为先（END 完整体或方言恢复），否则回退事件聚合。
func (a *Accumulator) Message() *AssistantMessage {
	var sb strings.Builder
	for _, e := range a.entries {
		if e.isText {
			sb.WriteString(e.text.String())
		}
	}
	text := sb.String()
	thinking := a.thinking.String()
	if text == "" && a.terminalText != "" {
		text = a.terminalText
	}
	if thinking == "" && a.terminalThinking != "" {
		thinking = a.terminalThinking
	}

	toolCalls := a.terminalToolCalls
	if len(toolCalls) == 0 {
		toolCalls = a.buildToolCalls()
	} else {
		// END 载体按调用逐一送达：补上尚未 END 的进行中调用（流中快照完整性）
		inTerminal := map[string]struct{}{}
		for i := range toolCalls {
			if k := toolKey(&toolCalls[i]); k != "" {
				inTerminal[k] = struct{}{}
			}
		}
		for _, c := range a.buildToolCalls() {
			if k := toolKey(&c); k != "" {
				if _, ok := inTerminal[k]; !ok {
					toolCalls = append(append([]ToolCall(nil), toolCalls...), c)
				}
			}
		}
	}

	m := &AssistantMessage{
		Text:          text,
		Thinking:      thinking,
		ToolCalls:     toolCalls,
		SearchResults: append([]SearchResult(nil), a.searchResults...),
		Citations:     append([]Citation(nil), a.citations...),
		Metadata:      a.terminalMetadata,
	}
	for _, e := range a.entries {
		if !e.isText {
			m.Blocks = append(m.Blocks, e.block)
		}
	}
	if len(m.Blocks) == 0 {
		m.Blocks = a.terminalBlocks
	}
	if len(m.SearchResults) == 0 {
		m.SearchResults = a.terminalSearchResults
	}
	if len(m.Citations) == 0 {
		m.Citations = a.terminalCitations
	}
	if len(a.terminalProtocolStates) > 0 {
		m.ProtocolStates = copyMap(a.terminalProtocolStates)
		BindProtocolStateHashes(m)
	}
	if a.thinkingSignature != "" {
		if m.ProtocolStates == nil {
			m.ProtocolStates = map[string]any{}
		}
		m.ProtocolStates["thinking_signature"] = a.thinkingSignature
	}
	return m
}

// BuildResponse 构建终态快照。finishReason 归一（tool/stop/透传）；
// 空缺时仅在无错误路径补 "stop"（错误路径保持空，终态由 Err 表达）。
func (a *Accumulator) BuildResponse(model string, events []*ChatEvent) *Response {
	if model == "" {
		model = a.model
	}
	fr := a.finishReason
	if fr != "" {
		fr = normalizeFinishReason(fr)
	} else if a.err == nil {
		fr = "stop"
	}
	return &Response{
		model:        model,
		finishReason: fr,
		usage:        a.usage,
		message:      a.Message(),
		events:       events,
		err:          a.err,
	}
}

func (a *Accumulator) Err() error    { return a.err }
func (a *Accumulator) Usage() *Usage { return a.usage }

// ---- 方言工作台 ----

// SetStream / IsStream 流式标记（构建 Accumulator 时随上下文写入；
// ParseAssistantMessage 等无 ctx 入参的路径借它分流）。
func (a *Accumulator) SetStream(b bool) { a.stream = b }
func (a *Accumulator) IsStream() bool   { return a.stream }

func (a *Accumulator) SetModel(m string)      { a.model = m }
func (a *Accumulator) Model() string          { return a.model }
func (a *Accumulator) SetFinished(b bool)     { a.finished = b }
func (a *Accumulator) IsFinished() bool       { return a.finished }
func (a *Accumulator) SetError(err error)     { a.err = err }
func (a *Accumulator) SetUsage(u *Usage)      { a.usage = u }
func (a *Accumulator) LastToolCallID() string { return a.lastToolCallID }

// SetLastToolCallID 记录最近一次工具调用 id（id 仅首分片携带，后续分片兜底用）。
// ThinkingSignature / SetThinkingSignature 思考签名幂等态（Gemini thoughtSignature
// 等方言在解析边界直接读写；事件通道 THINKING_SIGNATURE 走 Merge 同一字段）。
func (a *Accumulator) ThinkingSignature() string       { return a.thinkingSignature }
func (a *Accumulator) SetThinkingSignature(sig string) { a.thinkingSignature = sig }

func (a *Accumulator) SetLastToolCallID(id string) {
	if id != "" {
		a.lastToolCallID = id
	}
}

// SetFinishReason 方言直写结束原因（非空才写；流式事件 Attrs 亦可携带）。
func (a *Accumulator) SetFinishReason(fr string) {
	if fr != "" {
		a.finishReason = fr
	}
}

// ToolCallBuilders 流式工具调用构建器表（autoToolCall 出站重建与参数恢复用）。
func (a *Accumulator) ToolCallBuilders() map[string]*ToolCallBuilder { return a.toolCalls }

// ToolCallOrder 构建器首次出现序（Go map 无序，出站重建需确定序）。
func (a *Accumulator) ToolCallOrder() []string { return a.toolOrder }

// InThinking / SetInThinking <think> 通道解析状态。
func (a *Accumulator) InThinking() bool     { return a.inThinking }
func (a *Accumulator) SetInThinking(b bool) { a.inThinking = b }

// HasReasoningField / SetHasReasoningField 推理字段出现标记（reasoning 协议族）。
func (a *Accumulator) HasReasoningField() bool     { return a.hasReasoningField }
func (a *Accumulator) SetHasReasoningField(b bool) { a.hasReasoningField = b }

// ReasoningFieldName / SetReasoningFieldName 本流使用的推理字段名（出站回放参考）。
func (a *Accumulator) ReasoningFieldName() string        { return a.reasoningFieldName }
func (a *Accumulator) SetReasoningFieldName(name string) { a.reasoningFieldName = name }

// ---- 终态载体 ----

// TerminalPresent 是否已有终态载体（空结果判定用）。
func (a *Accumulator) TerminalPresent() bool { return a.terminalPresent }

// SetTerminalMessage 非流式：完整消息整体替换终态载体。
func (a *Accumulator) SetTerminalMessage(m *AssistantMessage) {
	a.mergeTerminalMessage(m, true)
}

// MergeTerminalMessage 流式：仅覆盖非空字段。
func (a *Accumulator) MergeTerminalMessage(m *AssistantMessage) {
	a.mergeTerminalMessage(m, false)
}

// parseToolCallArgs 终态 toolCalls 的 Arguments 兜底解析（Java 由方言在构造
// ToolCall 时填充；Go 部分方言只给 ArgumentsStr，快照统一补齐，调用方/工具
// 执行拿到的终态调用都有解析后参数）。拷贝后解析，不动入参切片。
func parseToolCallArgs(calls []ToolCall) []ToolCall {
	needParse := false
	for i := range calls {
		if calls[i].Arguments == nil && calls[i].ArgumentsStr != "" {
			needParse = true
			break
		}
	}
	if !needParse {
		return calls
	}
	out := append([]ToolCall(nil), calls...)
	for i := range out {
		if out[i].Arguments == nil && out[i].ArgumentsStr != "" {
			var m map[string]any
			if json.Unmarshal([]byte(out[i].ArgumentsStr), &m) == nil {
				out[i].Arguments = m
			}
		}
	}
	return out
}

func (a *Accumulator) mergeTerminalMessage(m *AssistantMessage, replace bool) {
	if m == nil {
		return
	}
	a.terminalPresent = true
	if replace || m.Text != "" {
		a.terminalText = m.Text
	}
	if replace || m.Thinking != "" {
		a.terminalThinking = m.Thinking
	}
	if replace || len(m.ToolCalls) > 0 {
		a.terminalToolCalls = parseToolCallArgs(m.ToolCalls)
	}
	if replace || len(m.Blocks) > 0 {
		a.terminalBlocks = m.Blocks
	}
	if replace || len(m.SearchResults) > 0 {
		a.terminalSearchResults = m.SearchResults
	}
	if replace || len(m.Citations) > 0 {
		a.terminalCitations = m.Citations
	}
	if replace || len(m.ProtocolStates) > 0 {
		a.terminalProtocolStates = m.ProtocolStates
	}
	if replace || len(m.Metadata) > 0 {
		a.terminalMetadata = m.Metadata
	}
}

// PutTerminalProtocolState 方言终态协议状态写入（对标 Java putTerminalProtocolState：
// anthropic thinking 签名、gemini thoughtSignature 等）：直接进终态载体袋，
// Message() 原样透出。终帧一次性写入，不与通用 thinking_signature 键合并。
func (a *Accumulator) PutTerminalProtocolState(id string, data any) {
	a.terminalPresent = true
	if a.terminalProtocolStates == nil {
		a.terminalProtocolStates = map[string]any{}
	}
	a.terminalProtocolStates[id] = data
}

// TerminalProtocolState 终态协议状态读取（方言聚合出口复用回放状态用）。
func (a *Accumulator) TerminalProtocolState(id string) any {
	return a.terminalProtocolStates[id]
}

// AggregationMeta 方言解析工作台（对标 Java aggregationMetadata）：键值随请求
// 累积、终帧提升为协议状态后清键；不进入最终消息（与 Metadata 分离）。
func (a *Accumulator) AggregationMeta() map[string]any {
	if a.aggregationMeta == nil {
		a.aggregationMeta = map[string]any{}
	}
	return a.aggregationMeta
}

// MediaBlocks 已聚合的媒体块（方言音频转录等元数据回填用；条目内部引用，
// 修改 Metas() 返回的 map 对聚合内同值块可见）。
func (a *Accumulator) MediaBlocks() []ContentBlock {
	var out []ContentBlock
	for _, e := range a.entries {
		if !e.isText {
			out = append(out, e.block)
		}
	}
	if len(out) == 0 {
		return a.terminalBlocks
	}
	return out
}

// mergeTerminalToolCall END 事件携带的完整 ToolCall → 终态载体（同键字段级合并：
// 非空字段覆盖；参数空缺回退事件聚合 builder 的累计——END 载体可能只带头部字段）。
func (a *Accumulator) mergeTerminalToolCall(call ToolCall) {
	a.terminalPresent = true
	if call.ArgumentsStr == "" {
		if b := a.toolCalls[toolKey(&call)]; b != nil {
			call.ArgumentsStr = b.Args()
		}
	}
	if call.ArgumentsStr != "" && call.Arguments == nil {
		var m map[string]any
		if json.Unmarshal([]byte(call.ArgumentsStr), &m) == nil {
			call.Arguments = m
		}
	}
	merged := append([]ToolCall(nil), a.terminalToolCalls...)
	for i := range merged {
		if key := toolKey(&merged[i]); key != "" && key == toolKey(&call) {
			if call.ID != "" {
				merged[i].ID = call.ID
			}
			if call.Name != "" {
				merged[i].Name = call.Name
			}
			if call.Index != "" {
				merged[i].Index = call.Index
			}
			if call.ArgumentsStr != "" {
				merged[i].ArgumentsStr = call.ArgumentsStr
				merged[i].Arguments = call.Arguments
			}
			a.terminalToolCalls = merged
			return
		}
	}
	a.terminalToolCalls = append(merged, call)
}

// normalizeFinishReason 归一化：含 tool/function → "tool"；含 stop/end → "stop"；
// 其余透传（"length"/"content_filter"）；空缺默认 "stop"。
func normalizeFinishReason(s string) string {
	if s == "" {
		return "stop"
	}
	low := strings.ToLower(s)
	if strings.Contains(low, "tool") || strings.Contains(low, "function") {
		return "tool"
	}
	if strings.Contains(low, "stop") || strings.Contains(low, "end") {
		return "stop"
	}
	return s
}
