package ai

import "encoding/json"

// ToolCall 模型发起的工具调用（对标 Java ToolCall）。
type ToolCall struct {
	Index        string         `json:"index,omitempty"` // 流式聚合键（"idx:<n>" 形式，index 优先于 id）
	ID           string         `json:"id,omitempty"`
	Name         string         `json:"name,omitempty"`
	ArgumentsStr string         `json:"arguments,omitempty"` // 原始参数 JSON 字符串（流式期间可能截断）
	Arguments    map[string]any `json:"argumentsMap,omitempty"`
}

// ToolResult 工具执行结果（= Contents + success/error）。
type ToolResult struct {
	Text    string         `json:"text,omitempty"`
	Blocks  []ContentBlock `json:"blocks,omitempty"`
	IsError bool           `json:"isError,omitempty"`
	Meta    map[string]any `json:"metas,omitempty"`
}

func ToolResultOf(s string) *ToolResult  { return &ToolResult{Text: s} }
func ToolResultErr(s string) *ToolResult { return &ToolResult{Text: s, IsError: true} }

// ContentText 文本形态的结果（Text 或首文本块）。
func (r *ToolResult) ContentText() string {
	if r.Text != "" {
		return r.Text
	}
	for _, b := range r.Blocks {
		if tb, ok := b.(TextBlock); ok {
			return tb.Text
		}
	}
	return ""
}

func (r *ToolResult) Clone() *ToolResult {
	n := *r
	n.Blocks = cloneBlocks(r.Blocks)
	n.Meta = copyMap(r.Meta)
	return &n
}

func (r *ToolResult) UnmarshalJSON(data []byte) error {
	var raw struct {
		Text    string          `json:"text"`
		Blocks  json.RawMessage `json:"blocks"`
		IsError bool            `json:"isError"`
		Meta    map[string]any  `json:"metas"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	r.Text, r.IsError, r.Meta = raw.Text, raw.IsError, raw.Meta
	if len(raw.Blocks) > 0 && string(raw.Blocks) != "null" {
		blocks, err := unmarshalBlocks(raw.Blocks)
		if err != nil {
			return err
		}
		r.Blocks = blocks
	}
	return nil
}

// ToolMessage 工具消息（对标 Java ToolMessage：name/toolCallId/returnDirect 为一等字段，
// 非 metadata；线格式由方言展开为 role=tool + name + tool_call_id + content）。
type ToolMessage struct {
	Result       *ToolResult `json:"result"`
	Name         string      `json:"name,omitempty"`
	CallID       string      `json:"toolCallId,omitempty"`
	ReturnDirect bool        `json:"returnDirect,omitempty"`
}

func (t *ToolMessage) ResultText() string {
	if t.Result == nil {
		return ""
	}
	return t.Result.ContentText()
}

// compacted 返回结果块降级后的浅拷贝（会话持久化路径）。
func (t *ToolMessage) compacted() *ToolMessage {
	if t.Result == nil || len(t.Result.Blocks) == 0 {
		return t
	}
	n := *t
	r := *t.Result
	r.Blocks = compactBlocks(t.Result.Blocks)
	n.Result = &r
	return &n
}
