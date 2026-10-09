package ai

import (
	"encoding/json"
	"fmt"
	"time"
)

// Role 聊天角色（对标 ChatRole）。
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message 聊天消息。Content 按角色可为：
//   - string（纯文本快捷形态）
//   - ContentBlock / []ContentBlock（多模态）
//   - *Contents（文本+块有序混合）
//   - *ToolMessage（tool 角色）
//   - *AssistantMessage（assistant 角色的聚合形态，会话持久化用）
type Message struct {
	Role      Role
	Content   any
	CreatedAt *int64 // 毫秒，可空
	Metadata  map[string]any
}

func (m *Message) AddMetadata(k string, v any) *Message {
	if m.Metadata == nil {
		m.Metadata = map[string]any{}
	}
	m.Metadata[k] = v
	return m
}

// ---- 工厂（对标 ChatMessage.ofXxx 静态族）----

func OfUser(content any) *Message {
	return &Message{Role: RoleUser, Content: normalizeUserContent(content)}
}

func OfSystem(content string) *Message {
	return &Message{Role: RoleSystem, Content: content}
}

// OfSystemRole 角色化系统消息：# Role 段 + 指令段。
func OfSystemRole(role, instruction string) *Message {
	return &Message{Role: RoleSystem, Content: "# Role: " + role + "\n\n" + instruction}
}

func OfAssistant(content string) *Message {
	return &Message{Role: RoleAssistant, Content: content}
}

func OfAssistantBlocks(blocks []ContentBlock) *Message {
	return &Message{Role: RoleAssistant, Content: blocks}
}

func OfTool(result *ToolResult, name, callID string, returnDirect bool) *Message {
	return &Message{Role: RoleTool, Content: &ToolMessage{
		Result: result, Name: name, CallID: callID, ReturnDirect: returnDirect,
	}}
}

// OfUserAugment 用户消息增强（RAG）："%s\n\n Now: %s\n\n References: %s"。
func OfUserAugment(query string, docs any) *Message {
	content := fmt.Sprintf("%s\n\n Now: %s\n\n References: %s",
		query, time.Now().Format(time.RFC3339), docs)
	return OfUser(content)
}

func normalizeUserContent(content any) any {
	switch c := content.(type) {
	case nil:
		return ""
	case string, ContentBlock, []ContentBlock, *Contents:
		return c
	case *ToolMessage, *AssistantMessage:
		return c
	default:
		return c // 交给序列化层报错
	}
}

// Clone 深拷贝（协议 copy-before-mutate 规则：元数据/块/metas 均独立）。
func (m *Message) Clone() *Message {
	c := &Message{Role: m.Role, Content: cloneContent(m.Content)}
	if m.CreatedAt != nil {
		v := *m.CreatedAt
		c.CreatedAt = &v
	}
	c.Metadata = copyMap(m.Metadata)
	return c
}

func cloneContent(content any) any {
	switch c := content.(type) {
	case string:
		return c
	case ContentBlock:
		return cloneBlock(c)
	case []ContentBlock:
		n := make([]ContentBlock, len(c))
		for i, b := range c {
			n[i] = cloneBlock(b)
		}
		return n
	case *Contents:
		return c.Clone()
	case *ToolMessage:
		n := *c
		if c.Result != nil {
			r := *c.Result
			r.Blocks = cloneBlocks(c.Result.Blocks)
			r.Meta = copyMap(c.Result.Meta)
			n.Result = &r
		}
		return &n
	case *AssistantMessage:
		return c.Clone()
	}
	return content
}

func cloneBlocks(blocks []ContentBlock) []ContentBlock {
	if blocks == nil {
		return nil
	}
	n := make([]ContentBlock, len(blocks))
	for i, b := range blocks {
		n[i] = cloneBlock(b)
	}
	return n
}

// ---- 序列化（会话持久化）----

// SessionInlineBase64MaxChars 内联 base64 媒体的最大字符数（超过则截断并标记 external）。
const SessionInlineBase64MaxChars = 64 * 1024

// MarshalJSON 序列化为会话持久化 JSON（含大块 base64 降级）。
func (m *Message) MarshalJSON() ([]byte, error) {
	node := map[string]any{"role": string(m.Role)}
	if m.CreatedAt != nil {
		node["createdAt"] = *m.CreatedAt
	}
	if len(m.Metadata) > 0 {
		node["metadata"] = m.Metadata
	}

	switch c := m.Content.(type) {
	case nil:
		// content 省略
	case string:
		node["content"] = c
	case *ToolMessage:
		node["content"] = c.compacted()
	case *AssistantMessage:
		node["content"] = c.compacted()
	case ContentBlock:
		node["content"] = []ContentBlock{compactBlock(c)}
	case []ContentBlock:
		node["content"] = compactBlocks(c)
	case *Contents:
		node["content"] = compactBlocks(c.Blocks())
	default:
		return nil, fmt.Errorf("ai: unsupported message content type %T", c)
	}
	return json.Marshal(node)
}

// UnmarshalJSON 反序列化：string 直存；数组按 "type" 判别为块；tool 角色恢复 *ToolMessage；
// assistant 角色的对象形态恢复 *AssistantMessage。
func (m *Message) UnmarshalJSON(data []byte) error {
	var raw struct {
		Role     string          `json:"role"`
		Content  json.RawMessage `json:"content"`
		CreateAt *int64          `json:"createdAt"`
		Metadata map[string]any  `json:"metadata"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.Role = Role(raw.Role)
	m.CreatedAt = raw.CreateAt
	m.Metadata = raw.Metadata
	if len(raw.Content) == 0 || string(raw.Content) == "null" {
		return nil
	}
	switch raw.Content[0] {
	case '"':
		var s string
		if err := json.Unmarshal(raw.Content, &s); err != nil {
			return err
		}
		m.Content = s
		return nil
	case '[':
		blocks, err := unmarshalBlocks(raw.Content)
		if err != nil {
			return err
		}
		m.Content = blocks
		return nil
	case '{':
		switch m.Role {
		case RoleTool:
			var tm ToolMessage
			if err := json.Unmarshal(raw.Content, &tm); err != nil {
				return err
			}
			m.Content = &tm
			return nil
		case RoleAssistant:
			var am AssistantMessage
			if err := json.Unmarshal(raw.Content, &am); err != nil {
				return err
			}
			m.Content = &am
			return nil
		}
	}
	return fmt.Errorf("ai: cannot unmarshal message content: %s", raw.Content)
}

// compactBlocks 大块 base64 降级（对标 compactLargeMediaInNode）：
// data 清空并标记 metas.storage=external / data_truncated / original_data_length。
// 只处理类型化块，不触碰协议状态与工具参数里的同名 data。
func compactBlocks(blocks []ContentBlock) []ContentBlock {
	out := make([]ContentBlock, len(blocks))
	for i, b := range blocks {
		out[i] = compactBlock(b)
	}
	return out
}

func compactBlock(b ContentBlock) ContentBlock {
	base64Len := func(data []byte) int { return (len(data) + 2) / 3 * 4 }
	switch v := b.(type) {
	case ImageBlock:
		if len(v.Data) > 0 && base64Len(v.Data) > SessionInlineBase64MaxChars {
			if v.Meta == nil {
				v.Meta = map[string]any{}
			}
			v.Meta["storage"] = "external"
			v.Meta["data_truncated"] = true
			v.Meta["original_data_length"] = base64Len(v.Data)
			v.Data = nil
		}
		return v
	case AudioBlock:
		if len(v.Data) > 0 && base64Len(v.Data) > SessionInlineBase64MaxChars {
			if v.Meta == nil {
				v.Meta = map[string]any{}
			}
			v.Meta["storage"] = "external"
			v.Meta["data_truncated"] = true
			v.Meta["original_data_length"] = base64Len(v.Data)
			v.Data = nil
		}
		return v
	case VideoBlock:
		if len(v.Data) > 0 && base64Len(v.Data) > SessionInlineBase64MaxChars {
			if v.Meta == nil {
				v.Meta = map[string]any{}
			}
			v.Meta["storage"] = "external"
			v.Meta["data_truncated"] = true
			v.Meta["original_data_length"] = base64Len(v.Data)
			v.Data = nil
		}
		return v
	case BlobBlock:
		if len(v.Data) > 0 && base64Len(v.Data) > SessionInlineBase64MaxChars {
			if v.Meta == nil {
				v.Meta = map[string]any{}
			}
			v.Meta["storage"] = "external"
			v.Meta["data_truncated"] = true
			v.Meta["original_data_length"] = base64Len(v.Data)
			v.Data = nil
		}
		return v
	}
	return b
}
