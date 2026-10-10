package agui

// ==================== 正文消息（TEXT_MESSAGE_*） ====================

// TextMessageStartEvent 正文消息开（lazy open 由 TEXT_DELTA 触发）。
type TextMessageStartEvent struct {
	Base
	MessageID string `json:"messageId"`
	Role      string `json:"role"`
}

func NewTextMessageStartEvent(messageID string) *TextMessageStartEvent {
	return &TextMessageStartEvent{Base{Type: EventTextMessageStart, Timestamp: nowMillis()},
		messageID, string(RoleAssistant)}
}

// TextMessageContentEvent 正文增量。
type TextMessageContentEvent struct {
	Base
	MessageID string `json:"messageId"`
	Delta     string `json:"delta"`
}

func NewTextMessageContentEvent(messageID, delta string) *TextMessageContentEvent {
	return &TextMessageContentEvent{Base{Type: EventTextMessageContent, Timestamp: nowMillis()},
		messageID, delta}
}

// TextMessageEndEvent 正文消息闭。
type TextMessageEndEvent struct {
	Base
	MessageID string `json:"messageId"`
}

func NewTextMessageEndEvent(messageID string) *TextMessageEndEvent {
	return &TextMessageEndEvent{Base{Type: EventTextMessageEnd, Timestamp: nowMillis()}, messageID}
}

// TextMessageChunkEvent 便捷事件（Start+Content+End 合一；wrapper 不发）。
type TextMessageChunkEvent struct {
	Base
	MessageID string `json:"messageId"`
	Role      string `json:"role"`
	Delta     string `json:"delta"`
}

func NewTextMessageChunkEvent(messageID, role, delta string) *TextMessageChunkEvent {
	return &TextMessageChunkEvent{Base{Type: EventTextMessageChunk, Timestamp: nowMillis()},
		messageID, role, delta}
}

// ==================== 工具调用（TOOL_CALL_*） ====================

// ToolCallStartEvent 工具调用开。
type ToolCallStartEvent struct {
	Base
	ToolCallID      string `json:"toolCallId"`
	ToolCallName    string `json:"toolCallName"`
	ParentMessageID string `json:"parentMessageId,omitempty"`
}

func NewToolCallStartEvent(toolCallID, toolCallName, parentMessageID string) *ToolCallStartEvent {
	return &ToolCallStartEvent{Base{Type: EventToolCallStart, Timestamp: nowMillis()},
		toolCallID, toolCallName, parentMessageID}
}

// ToolCallArgsEvent 工具参数增量（delta 为 JSON 片段）。
type ToolCallArgsEvent struct {
	Base
	ToolCallID string `json:"toolCallId"`
	Delta      string `json:"delta"`
}

func NewToolCallArgsEvent(toolCallID, delta string) *ToolCallArgsEvent {
	return &ToolCallArgsEvent{Base{Type: EventToolCallArgs, Timestamp: nowMillis()}, toolCallID, delta}
}

// ToolCallEndEvent 工具调用闭。
type ToolCallEndEvent struct {
	Base
	ToolCallID string `json:"toolCallId"`
}

func NewToolCallEndEvent(toolCallID string) *ToolCallEndEvent {
	return &ToolCallEndEvent{Base{Type: EventToolCallEnd, Timestamp: nowMillis()}, toolCallID}
}

// ToolCallChunkEvent 便捷事件（wrapper 不发）。
type ToolCallChunkEvent struct {
	Base
	ToolCallID      string `json:"toolCallId"`
	ToolCallName    string `json:"toolCallName"`
	ParentMessageID string `json:"parentMessageId,omitempty"`
	Delta           string `json:"delta"`
}

func NewToolCallChunkEvent(toolCallID, toolCallName, parentMessageID, delta string) *ToolCallChunkEvent {
	return &ToolCallChunkEvent{Base{Type: EventToolCallChunk, Timestamp: nowMillis()},
		toolCallID, toolCallName, parentMessageID, delta}
}

// ToolCallResultEvent 工具结果。
type ToolCallResultEvent struct {
	Base
	ToolCallID string `json:"toolCallId"`
	Content    string `json:"content"`
	MessageID  string `json:"messageId,omitempty"`
	Role       Role   `json:"role"`
}

func NewToolCallResultEvent(toolCallID, content, messageID string) *ToolCallResultEvent {
	return &ToolCallResultEvent{Base{Type: EventToolCallResult, Timestamp: nowMillis()},
		toolCallID, content, messageID, RoleTool}
}

// ==================== 推理（REASONING_*，取代 THINKING_*） ====================

// ReasoningStartEvent 推理块开。
type ReasoningStartEvent struct {
	Base
	MessageID string `json:"messageId"`
}

func NewReasoningStartEvent(messageID string) *ReasoningStartEvent {
	return &ReasoningStartEvent{Base{Type: EventReasoningStart, Timestamp: nowMillis()}, messageID}
}

// ReasoningEndEvent 推理块闭。
type ReasoningEndEvent struct {
	Base
	MessageID string `json:"messageId"`
}

func NewReasoningEndEvent(messageID string) *ReasoningEndEvent {
	return &ReasoningEndEvent{Base{Type: EventReasoningEnd, Timestamp: nowMillis()}, messageID}
}

// ReasoningMessageStartEvent 推理消息开（role 恒 "reasoning"）。
type ReasoningMessageStartEvent struct {
	Base
	MessageID string `json:"messageId"`
	Role      string `json:"role"`
}

func NewReasoningMessageStartEvent(messageID string) *ReasoningMessageStartEvent {
	return &ReasoningMessageStartEvent{Base{Type: EventReasoningMessageStart, Timestamp: nowMillis()},
		messageID, "reasoning"}
}

// ReasoningMessageContentEvent 推理消息增量。
type ReasoningMessageContentEvent struct {
	Base
	MessageID string `json:"messageId"`
	Delta     string `json:"delta"`
}

func NewReasoningMessageContentEvent(messageID, delta string) *ReasoningMessageContentEvent {
	return &ReasoningMessageContentEvent{Base{Type: EventReasoningMessageContent, Timestamp: nowMillis()},
		messageID, delta}
}

// ReasoningMessageEndEvent 推理消息闭。
type ReasoningMessageEndEvent struct {
	Base
	MessageID string `json:"messageId"`
}

func NewReasoningMessageEndEvent(messageID string) *ReasoningMessageEndEvent {
	return &ReasoningMessageEndEvent{Base{Type: EventReasoningMessageEnd, Timestamp: nowMillis()}, messageID}
}

// ReasoningMessageChunkEvent 便捷事件（wrapper 不发）。
type ReasoningMessageChunkEvent struct {
	Base
	MessageID string `json:"messageId"`
	Delta     string `json:"delta"`
}

func NewReasoningMessageChunkEvent(messageID, delta string) *ReasoningMessageChunkEvent {
	return &ReasoningMessageChunkEvent{Base{Type: EventReasoningMessageChunk, Timestamp: nowMillis()},
		messageID, delta}
}

// ReasoningEncryptedValueEvent 加密推理值（Anthropic 思考签名等）。
type ReasoningEncryptedValueEvent struct {
	Base
	Subtype        string `json:"subtype,omitempty"`
	EntityID       string `json:"entityId,omitempty"`
	EncryptedValue string `json:"encryptedValue,omitempty"`
}

func NewReasoningEncryptedValueEvent(subtype, entityID, encryptedValue string) *ReasoningEncryptedValueEvent {
	return &ReasoningEncryptedValueEvent{Base{Type: EventReasoningEncryptedValueEv, Timestamp: nowMillis()},
		subtype, entityID, encryptedValue}
}

// ==================== THINKING_*（Deprecated，协议兼容保留） ====================

// ThinkingStartEvent Deprecated：由 REASONING_START 取代。
type ThinkingStartEvent struct {
	Base
	MessageID string `json:"messageId"`
}

func NewThinkingStartEvent(messageID string) *ThinkingStartEvent {
	return &ThinkingStartEvent{Base{Type: EventThinkingStart, Timestamp: nowMillis()}, messageID}
}

// ThinkingEndEvent Deprecated：由 REASONING_END 取代。
type ThinkingEndEvent struct {
	Base
	MessageID string `json:"messageId"`
}

func NewThinkingEndEvent(messageID string) *ThinkingEndEvent {
	return &ThinkingEndEvent{Base{Type: EventThinkingEnd, Timestamp: nowMillis()}, messageID}
}

// ThinkingTextMessageStartEvent Deprecated。
type ThinkingTextMessageStartEvent struct {
	Base
	MessageID string `json:"messageId"`
}

func NewThinkingTextMessageStartEvent(messageID string) *ThinkingTextMessageStartEvent {
	return &ThinkingTextMessageStartEvent{Base{Type: EventThinkingTextMessageStart, Timestamp: nowMillis()}, messageID}
}

// ThinkingTextMessageContentEvent Deprecated。
type ThinkingTextMessageContentEvent struct {
	Base
	MessageID string `json:"messageId"`
	Delta     string `json:"delta"`
}

func NewThinkingTextMessageContentEvent(messageID, delta string) *ThinkingTextMessageContentEvent {
	return &ThinkingTextMessageContentEvent{Base{Type: EventThinkingTextMessageContent, Timestamp: nowMillis()},
		messageID, delta}
}

// ThinkingTextMessageEndEvent Deprecated。
type ThinkingTextMessageEndEvent struct {
	Base
	MessageID string `json:"messageId"`
}

func NewThinkingTextMessageEndEvent(messageID string) *ThinkingTextMessageEndEvent {
	return &ThinkingTextMessageEndEvent{Base{Type: EventThinkingTextMessageEnd, Timestamp: nowMillis()}, messageID}
}

func (e *TextMessageStartEvent) eventType() EventType          { return e.Type }
func (e *TextMessageContentEvent) eventType() EventType        { return e.Type }
func (e *TextMessageEndEvent) eventType() EventType            { return e.Type }
func (e *TextMessageChunkEvent) eventType() EventType          { return e.Type }
func (e *ToolCallStartEvent) eventType() EventType             { return e.Type }
func (e *ToolCallArgsEvent) eventType() EventType              { return e.Type }
func (e *ToolCallEndEvent) eventType() EventType               { return e.Type }
func (e *ToolCallChunkEvent) eventType() EventType             { return e.Type }
func (e *ToolCallResultEvent) eventType() EventType            { return e.Type }
func (e *ReasoningStartEvent) eventType() EventType            { return e.Type }
func (e *ReasoningEndEvent) eventType() EventType              { return e.Type }
func (e *ReasoningMessageStartEvent) eventType() EventType     { return e.Type }
func (e *ReasoningMessageContentEvent) eventType() EventType   { return e.Type }
func (e *ReasoningMessageEndEvent) eventType() EventType       { return e.Type }
func (e *ReasoningMessageChunkEvent) eventType() EventType     { return e.Type }
func (e *ReasoningEncryptedValueEvent) eventType() EventType   { return e.Type }
func (e *ThinkingStartEvent) eventType() EventType             { return e.Type }
func (e *ThinkingEndEvent) eventType() EventType               { return e.Type }
func (e *ThinkingTextMessageStartEvent) eventType() EventType  { return e.Type }
func (e *ThinkingTextMessageContentEvent) eventType() EventType { return e.Type }
func (e *ThinkingTextMessageEndEvent) eventType() EventType    { return e.Type }
