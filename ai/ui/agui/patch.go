package agui

import "crypto/rand"

// JsonPatchOperation RFC 6902 JSON Patch 操作（STATE_DELTA 的 delta 数组元素）。
type JsonPatchOperation struct {
	Op    string `json:"op"`              // add | remove | replace | move | copy | test
	Path  string `json:"path"`            // JSON Pointer
	Value any    `json:"value,omitempty"` // 按 op 使用
	From  string `json:"from,omitempty"`  // move/copy 源指针
}

// PatchAdd add 操作。
func PatchAdd(path string, value any) JsonPatchOperation {
	return JsonPatchOperation{Op: "add", Path: path, Value: value}
}

// PatchReplace replace 操作。
func PatchReplace(path string, value any) JsonPatchOperation {
	return JsonPatchOperation{Op: "replace", Path: path, Value: value}
}

// PatchRemove remove 操作。
func PatchRemove(path string) JsonPatchOperation {
	return JsonPatchOperation{Op: "remove", Path: path}
}

// PatchMove move 操作。
func PatchMove(from, path string) JsonPatchOperation {
	return JsonPatchOperation{Op: "move", From: from, Path: path}
}

// StateDeltaEvent 状态增量（JSON Patch）。
type StateDeltaEvent struct {
	Base
	Delta []JsonPatchOperation `json:"delta"`
}

func NewStateDeltaEvent(delta []JsonPatchOperation) *StateDeltaEvent {
	return &StateDeltaEvent{Base{Type: EventStateDelta, Timestamp: nowMillis()}, delta}
}

// ==================== Interrupt / RunOutcome 辅助 ====================

// Interrupt 中断详情（RunOutcome type=interrupt 时携带；字段全可选）。
type Interrupt struct {
	ID             string         `json:"id,omitempty"`
	Reason         string         `json:"reason,omitempty"` // tool_call | input_required | confirmation
	Message        string         `json:"message,omitempty"`
	ToolCallID     string         `json:"toolCallId,omitempty"`
	ResponseSchema any            `json:"responseSchema,omitempty"`
	ExpiresAt      string         `json:"expiresAt,omitempty"` // ISO-8601
	Metadata       map[string]any `json:"metadata,omitempty"`
}

// Context 协议辅助值对象（description + value；wrapper 未使用，完整协议面保留）。
type Context struct {
	Description string `json:"description"`
	Value       string `json:"value"`
}

// ==================== ID 生成 ====================

// IDGenerator 事件标识生成策略（对标 Java UUID.randomUUID()；可注入便于测试）。
type IDGenerator func() string

// DefaultIDGenerator UUIDv4 形态（随机 16 字节，8-4-4-4-12 十六进制）。
func DefaultIDGenerator() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant
	const hexDigits = "0123456789abcdef"
	out := make([]byte, 0, 36)
	for i, v := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out = append(out, '-')
		}
		out = append(out, hexDigits[v>>4], hexDigits[v&0x0f])
	}
	return string(out)
}
