package agui

// Message 快照消息基形（MESSAGES_SNAPSHOT 载荷；Java Message 抽象基的扁平化
// 对应物——Go 单结构体 + Role 字段，子类差异以 ToolCalls/ToolCallID/Error 位
// 表达）。ID 缺省构造时生成 UUIDv4。
type Message struct {
	ID        string      `json:"id"`
	Role      Role        `json:"role"`
	Content   string      `json:"content"`
	Name      string      `json:"name,omitempty"`
	ToolCalls []*ToolCall `json:"toolCalls,omitempty"` // assistant
	ToolCallID string     `json:"toolCallId,omitempty"` // tool
	Error     string      `json:"error,omitempty"`      // tool
}

// NewMessage 建消息（id 缺省生成 UUIDv4）。
func NewMessage(role Role, content string) *Message {
	return &Message{ID: DefaultIDGenerator(), Role: role, Content: content}
}

// ToolCall 工具调用（快照形态）。
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // 恒 "function"
	Function *FunctionCall `json:"function"`
}

// FunctionCall 函数调用载荷（arguments 为序列化后的 JSON 字符串）。
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool 工具定义（快照/输入侧形态；parameters 为 JSON Schema 形状）。
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  *ToolParameters `json:"parameters,omitempty"`
}

// ToolParameters JSON Schema 形状。
type ToolParameters struct {
	Type       string                  `json:"type"`
	Properties map[string]*ToolProperty `json:"properties,omitempty"`
	Required   []string                `json:"required,omitempty"`
}

// ToolProperty 单属性。
type ToolProperty struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}
