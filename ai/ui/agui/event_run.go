// Package agui 把 ChatEvent 流适配为 AG-UI 协议事件（对标 solon-ai-ui-agui；
// 协议规范见 https://docs.ag-ui.com/concepts/events）。事件族 31 型（含 5 个
// @Deprecated THINKING_*，由 REASONING_* 取代）；事件对象经 JSON 序列化为
// `data: {...}\n\n` SSE 帧（无 event: 名行、无 [DONE] 终标——AG-UI 以
// RUN_FINISHED/RUN_ERROR 收口，对照 AI SDK 协议的差异）。
//
// 与 aisdk 的关系：共享「ChatEvent → 前端协议」的定位，但事件模型独立（本包
// 事件为完整对象族而非薄 part）；Java 侧两模块亦各自成体系。
package agui

import "time"

// EventType 事件类型（字符串枚举；Java code == 常量名，无数字码）。
type EventType string

const (
	EventTextMessageStart   EventType = "TEXT_MESSAGE_START"
	EventTextMessageContent EventType = "TEXT_MESSAGE_CONTENT"
	EventTextMessageEnd     EventType = "TEXT_MESSAGE_END"
	EventTextMessageChunk   EventType = "TEXT_MESSAGE_CHUNK" // 便捷事件（wrapper 不发）

	EventToolCallStart  EventType = "TOOL_CALL_START"
	EventToolCallArgs   EventType = "TOOL_CALL_ARGS"
	EventToolCallEnd    EventType = "TOOL_CALL_END"
	EventToolCallChunk  EventType = "TOOL_CALL_CHUNK" // 便捷事件（wrapper 不发）
	EventToolCallResult EventType = "TOOL_CALL_RESULT"

	EventReasoningStart            EventType = "REASONING_START"
	EventReasoningEnd              EventType = "REASONING_END"
	EventReasoningMessageStart     EventType = "REASONING_MESSAGE_START"
	EventReasoningMessageContent   EventType = "REASONING_MESSAGE_CONTENT"
	EventReasoningMessageEnd       EventType = "REASONING_MESSAGE_END"
	EventReasoningMessageChunk     EventType = "REASONING_MESSAGE_CHUNK" // 便捷事件（wrapper 不发）
	EventReasoningEncryptedValueEv EventType = "REASONING_ENCRYPTED_VALUE"

	// Deprecated: THINKING_* 族由 REASONING_* 取代（Java @Deprecated 保留）。
	EventThinkingStart              EventType = "THINKING_START"
	EventThinkingEnd                EventType = "THINKING_END"
	EventThinkingTextMessageStart   EventType = "THINKING_TEXT_MESSAGE_START"
	EventThinkingTextMessageContent EventType = "THINKING_TEXT_MESSAGE_CONTENT"
	EventThinkingTextMessageEnd     EventType = "THINKING_TEXT_MESSAGE_END"

	EventStateSnapshot    EventType = "STATE_SNAPSHOT"
	EventStateDelta       EventType = "STATE_DELTA"
	EventMessagesSnapshot EventType = "MESSAGES_SNAPSHOT"

	EventRaw    EventType = "RAW"
	EventCustom EventType = "CUSTOM"

	EventRunStarted  EventType = "RUN_STARTED"
	EventRunFinished EventType = "RUN_FINISHED"
	EventRunError    EventType = "RUN_ERROR"

	EventStepStarted  EventType = "STEP_STARTED"
	EventStepFinished EventType = "STEP_FINISHED"
)

// Role 消息角色（Java Role 枚举；协议形态为小写 code）。
type Role string

const (
	RoleAssistant Role = "assistant"
	RoleDeveloper Role = "developer"
	RoleSystem    Role = "system"
	RoleTool      Role = "tool"
	RoleUser      Role = "user"
)

// Event 事件封闭接口（只有本包事件实现得动）。
type Event interface {
	eventType() EventType
}

// Base 事件基字段（嵌入各具体事件；timestamp 构造即取当前毫秒，rawEvent 为
// 供应商原始帧——wrapper 映射事件携带，合成收口事件不带）。
type Base struct {
	Type      EventType `json:"type"`
	Timestamp int64     `json:"timestamp"`
	RawEvent  any       `json:"rawEvent,omitempty"`
}

func nowMillis() int64 { return time.Now().UnixMilli() }

// ==================== Run / Step ====================

// RunStartedEvent 流开始。
type RunStartedEvent struct {
	Base
	ThreadID string `json:"threadId"`
	RunID    string `json:"runId"`
}

func NewRunStartedEvent(threadID, runID string) *RunStartedEvent {
	return &RunStartedEvent{Base{Type: EventRunStarted, Timestamp: nowMillis()}, threadID, runID}
}

// RunOutcome RunFinished 的可选终态（正常完成省略；interrupt 携带 Interrupts）。
type RunOutcome struct {
	Type      string      `json:"type"` // "success" | "interrupt"
	Interrupts []Interrupt `json:"interrupts,omitempty"`
}

// RunFinishedEvent 流收口。Result 为 RESPONSE_END 的响应快照（向后兼容位）。
type RunFinishedEvent struct {
	Base
	ThreadID string      `json:"threadId"`
	RunID    string      `json:"runId"`
	Result   any         `json:"result,omitempty"`
	Outcome  *RunOutcome `json:"outcome,omitempty"`
}

func NewRunFinishedEvent(threadID, runID string, result any, outcome *RunOutcome) *RunFinishedEvent {
	return &RunFinishedEvent{Base{Type: EventRunFinished, Timestamp: nowMillis()}, threadID, runID, result, outcome}
}

// RunErrorEvent 流失败收口。
type RunErrorEvent struct {
	Base
	Error string `json:"error"`
}

func NewRunErrorEvent(errText string) *RunErrorEvent {
	return &RunErrorEvent{Base{Type: EventRunError, Timestamp: nowMillis()}, errText}
}

// StepStartedEvent 步骤开始（stepName = "step-N"）。
type StepStartedEvent struct {
	Base
	StepName string `json:"stepName"`
}

func NewStepStartedEvent(stepName string) *StepStartedEvent {
	return &StepStartedEvent{Base{Type: EventStepStarted, Timestamp: nowMillis()}, stepName}
}

// StepFinishedEvent 步骤结束。
type StepFinishedEvent struct {
	Base
	StepName string `json:"stepName"`
}

func NewStepFinishedEvent(stepName string) *StepFinishedEvent {
	return &StepFinishedEvent{Base{Type: EventStepFinished, Timestamp: nowMillis()}, stepName}
}

func (e *RunStartedEvent) eventType() EventType  { return e.Type }
func (e *RunFinishedEvent) eventType() EventType { return e.Type }
func (e *RunErrorEvent) eventType() EventType    { return e.Type }
func (e *StepStartedEvent) eventType() EventType  { return e.Type }
func (e *StepFinishedEvent) eventType() EventType { return e.Type }
