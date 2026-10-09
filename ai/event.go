package ai

import "encoding/json"

// EventGroup 事件组（对标 ChatEventGroup，9 组闭集）。
type EventGroup uint8

const (
	GroupLifecycle EventGroup = iota
	GroupStep
	GroupText
	GroupThinking
	GroupToolCall
	GroupServerTool
	GroupMedia
	GroupSafety
	GroupMeta
)

func (g EventGroup) String() string {
	switch g {
	case GroupLifecycle:
		return "LIFECYCLE"
	case GroupStep:
		return "STEP"
	case GroupText:
		return "TEXT"
	case GroupThinking:
		return "THINKING"
	case GroupToolCall:
		return "TOOL_CALL"
	case GroupServerTool:
		return "SERVER_TOOL"
	case GroupMedia:
		return "MEDIA"
	case GroupSafety:
		return "SAFETY"
	case GroupMeta:
		return "META"
	}
	return "UNKNOWN"
}

// EventPhase 事件相位（START/DELTA/END/NONE）。
type EventPhase uint8

const (
	PhaseNone EventPhase = iota
	PhaseStart
	PhaseDelta
	PhaseEnd
)

func (p EventPhase) String() string {
	switch p {
	case PhaseStart:
		return "START"
	case PhaseDelta:
		return "DELTA"
	case PhaseEnd:
		return "END"
	}
	return "NONE"
}

// EventType 事件类型（对标 ChatEventType，32 型一次定义齐；组/相位静态绑定）。
type EventType uint16

const (
	EventResponseStart EventType = iota + 1 // LIFECYCLE/START
	EventStatus                             // LIFECYCLE/NONE
	EventHeartbeat                          // LIFECYCLE/NONE
	EventResponseEnd                        // LIFECYCLE/END
	EventAbort                              // LIFECYCLE/END

	EventStepStart // STEP/START
	EventStepEnd   // STEP/END

	EventTextStart // TEXT/START
	EventTextDelta // TEXT/DELTA
	EventTextEnd   // TEXT/END

	EventThinkingStart     // THINKING/START
	EventThinkingDelta     // THINKING/DELTA
	EventThinkingEnd       // THINKING/END
	EventThinkingSignature // THINKING/NONE（幂等赋值，绝不追加）
	EventThinkingRedacted  // THINKING/NONE

	EventToolCallStart     // TOOL_CALL/START
	EventToolCallArgsDelta // TOOL_CALL/DELTA
	EventToolCallEnd       // TOOL_CALL/END
	EventToolResult        // TOOL_CALL/NONE

	EventServerToolStart     // SERVER_TOOL/START
	EventServerToolArgsDelta // SERVER_TOOL/DELTA
	EventServerToolResult    // SERVER_TOOL/END

	EventSearchResult // MEDIA/NONE
	EventCitation     // MEDIA/NONE
	EventMediaPartial // MEDIA/DELTA
	EventMediaDone    // MEDIA/END

	EventRefusalDelta  // SAFETY/DELTA
	EventContentFilter // SAFETY/NONE

	EventUsage  // META/NONE
	EventError  // META/NONE
	EventRaw    // META/NONE
	EventCustom // META/NONE
)

var eventTypeMeta = map[EventType]struct {
	name  string
	group EventGroup
	phase EventPhase
}{
	EventResponseStart:     {"RESPONSE_START", GroupLifecycle, PhaseStart},
	EventStatus:            {"STATUS", GroupLifecycle, PhaseNone},
	EventHeartbeat:         {"HEARTBEAT", GroupLifecycle, PhaseNone},
	EventResponseEnd:       {"RESPONSE_END", GroupLifecycle, PhaseEnd},
	EventAbort:             {"ABORT", GroupLifecycle, PhaseEnd},
	EventStepStart:         {"STEP_START", GroupStep, PhaseStart},
	EventStepEnd:           {"STEP_END", GroupStep, PhaseEnd},
	EventTextStart:         {"TEXT_START", GroupText, PhaseStart},
	EventTextDelta:         {"TEXT_DELTA", GroupText, PhaseDelta},
	EventTextEnd:           {"TEXT_END", GroupText, PhaseEnd},
	EventThinkingStart:     {"THINKING_START", GroupThinking, PhaseStart},
	EventThinkingDelta:     {"THINKING_DELTA", GroupThinking, PhaseDelta},
	EventThinkingEnd:       {"THINKING_END", GroupThinking, PhaseEnd},
	EventThinkingSignature: {"THINKING_SIGNATURE", GroupThinking, PhaseNone},
	EventThinkingRedacted:  {"THINKING_REDACTED", GroupThinking, PhaseNone},
	EventToolCallStart:     {"TOOL_CALL_START", GroupToolCall, PhaseStart},
	EventToolCallArgsDelta: {"TOOL_CALL_ARGS_DELTA", GroupToolCall, PhaseDelta},
	EventToolCallEnd:       {"TOOL_CALL_END", GroupToolCall, PhaseEnd},
	EventToolResult:        {"TOOL_RESULT", GroupToolCall, PhaseNone},
	EventServerToolStart:   {"SERVER_TOOL_START", GroupServerTool, PhaseStart},
	EventServerToolArgsDelta: {
		name:  "SERVER_TOOL_ARGS_DELTA",
		group: GroupServerTool, phase: PhaseDelta,
	},
	EventServerToolResult: {"SERVER_TOOL_RESULT", GroupServerTool, PhaseEnd},
	EventSearchResult:     {"SEARCH_RESULT", GroupMedia, PhaseNone},
	EventCitation:         {"CITATION", GroupMedia, PhaseNone},
	EventMediaPartial:     {"MEDIA_PARTIAL", GroupMedia, PhaseDelta},
	EventMediaDone:        {"MEDIA_DONE", GroupMedia, PhaseEnd},
	EventRefusalDelta:     {"REFUSAL_DELTA", GroupSafety, PhaseDelta},
	EventContentFilter:    {"CONTENT_FILTER", GroupSafety, PhaseNone},
	EventUsage:            {"USAGE", GroupMeta, PhaseNone},
	EventError:            {"ERROR", GroupMeta, PhaseNone},
	EventRaw:              {"RAW", GroupMeta, PhaseNone},
	EventCustom:           {"CUSTOM", GroupMeta, PhaseNone},
}

func (t EventType) String() string {
	if m, ok := eventTypeMeta[t]; ok {
		return m.name
	}
	return "UNKNOWN"
}

func (t EventType) Group() EventGroup {
	if m, ok := eventTypeMeta[t]; ok {
		return m.group
	}
	return GroupMeta
}

func (t EventType) Phase() EventPhase {
	if m, ok := eventTypeMeta[t]; ok {
		return m.phase
	}
	return PhaseNone
}

// IsDelta 是否为增量相位事件。
func (t EventType) IsDelta() bool { return t.Phase() == PhaseDelta }

// IsTerminal 是否为终态事件（RESPONSE_END | ABORT | ERROR）。
func (t EventType) IsTerminal() bool {
	return t == EventResponseEnd || t == EventAbort || t == EventError
}

// ChatEvent 单结构体承载全部 32 型事件（对标 Java 宽接口 + 访问器）；
// 哪些载荷字段有意义取决于 Type（载荷约定见各事件注释）。
type ChatEvent struct {
	Type    EventType
	RawType string // 厂商原始帧名（"chat.completion.chunk" 等）
	SubType string // 服务端工具类别（web_search/code_interpreter/mcp_call/...）

	ResponseID         string // 框架响应 id（一次 stream 内恒定）
	ProviderResponseID string // resp_xxx / msg_xxx / chatcmpl-xxx
	Step               int    // 0 基，autoToolCall 每轮模型调用递增
	ItemID             string // reasoning/content-block item id
	ToolCallID         string
	Index              int // -1 = 未知

	Text         string       // TEXT/THINKING/REFUSAL delta 载荷；SIGNATURE 时为签名值
	ToolCall     *ToolCall    // TOOL_CALL_START 头部 / END 完整体
	Block        ContentBlock // MEDIA_DONE 载荷
	Citation     *Citation
	SearchResult *SearchResult
	Usage        *Usage
	Err          error           // ERROR 载荷
	Raw          json.RawMessage // 仅 EventRaw（缺省 HEARTBEAT/RAW 被过滤）
	Attrs        map[string]any

	resp *Response         // 仅 RESPONSE_END（ERROR 携带可挽救部分）
	msg  *AssistantMessage // 同上
}

// Response 终态事件携带的聚合响应。
func (e *ChatEvent) Response() *Response { return e.resp }

// Message 终态事件携带的聚合助手消息。
func (e *ChatEvent) Message() *AssistantMessage { return e.msg }

// SetResponse 事件管线在 RESPONSE_END 上回填聚合（框架内部使用）。
func (e *ChatEvent) SetResponse(r *Response, m *AssistantMessage) {
	e.resp, e.msg = r, m
}

func (e *ChatEvent) Is(types ...EventType) bool {
	for _, t := range types {
		if e.Type == t {
			return true
		}
	}
	return false
}

func (e *ChatEvent) IsGroup(groups ...EventGroup) bool {
	for _, g := range groups {
		if e.Type.Group() == g {
			return true
		}
	}
	return false
}

func (e *ChatEvent) IsDelta() bool { return e.Type.IsDelta() }

func (e *ChatEvent) IsTerminal() bool { return e.Type.IsTerminal() }

// EventFilter 事件过滤器（默认过滤 HEARTBEAT 与 RAW；LIFECYCLE/STEP 组恒放行）。
type EventFilter interface {
	Apply(ev *ChatEvent) bool
}

type EventFilterFunc func(ev *ChatEvent) bool

func (f EventFilterFunc) Apply(ev *ChatEvent) bool { return f(ev) }

// DefaultEventFilter 对标 Java ChatEventFilter.DEFAULT。
func DefaultEventFilter(ev *ChatEvent) bool {
	if ev.Type == EventHeartbeat || ev.Type == EventRaw {
		return false
	}
	if ev.IsGroup(GroupLifecycle, GroupStep) {
		return true
	}
	return true
}
