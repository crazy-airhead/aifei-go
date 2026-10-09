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
func (a *Accumulator) Message() *AssistantMessage {
	m := &AssistantMessage{
		Thinking:      a.thinking.String(),
		ToolCalls:     a.buildToolCalls(),
		SearchResults: append([]SearchResult(nil), a.searchResults...),
		Citations:     append([]Citation(nil), a.citations...),
	}
	var sb strings.Builder
	for _, e := range a.entries {
		if e.isText {
			sb.WriteString(e.text.String())
		} else {
			m.Blocks = append(m.Blocks, e.block)
		}
	}
	m.Text = sb.String()
	if a.thinkingSignature != "" {
		m.ProtocolStates = map[string]any{"thinking_signature": a.thinkingSignature}
	}
	return m
}

// BuildResponse 构建终态快照。finishReason 归一（tool/stop/透传）；
// 空缺时仅在无错误路径补 "stop"（错误路径保持空，终态由 Err 表达）。
func (a *Accumulator) BuildResponse(model string, events []*ChatEvent) *Response {
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
