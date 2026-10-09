package ai

import (
	"encoding/json"
	"fmt"
)

// AssistantMessage 助手消息聚合体：文本 + 思考 + 工具调用 + 多模态块 + 协议状态袋（D12）。
// ProtocolStates 必须随消息进出会话与工具循环原样回传（Anthropic 思考签名、
// OpenAI snapshot 态等厂商往返状态依赖它保证多轮工具调用正确性）。
type AssistantMessage struct {
	Text           string         `json:"text,omitempty"`
	Thinking       string         `json:"thinking,omitempty"`
	ToolCalls      []ToolCall     `json:"toolCalls,omitempty"`
	Blocks         []ContentBlock `json:"blocks,omitempty"`
	SearchResults  []SearchResult `json:"searchResults,omitempty"`
	Citations      []Citation     `json:"citations,omitempty"`
	ProtocolStates map[string]any `json:"protocolStates,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

// Snapshot 由聚合结果构建助手消息（对标 AssistantMessage.snapshot）。
func Snapshot(text string, thinking string, calls []ToolCall) *AssistantMessage {
	return &AssistantMessage{Text: text, Thinking: thinking, ToolCalls: calls}
}

func (m *AssistantMessage) HasThinking() bool {
	return m.Thinking != ""
}

// ThinkingOnly 是否为纯思考消息（目标端无法回放思考时整条跳过，方言侧使用）。
func (m *AssistantMessage) ThinkingOnly() bool {
	return m.Thinking != "" && m.Text == "" && len(m.ToolCalls) == 0
}

func (m *AssistantMessage) Clone() *AssistantMessage {
	n := *m
	n.ToolCalls = append([]ToolCall(nil), m.ToolCalls...)
	n.Blocks = cloneBlocks(m.Blocks)
	n.SearchResults = append([]SearchResult(nil), m.SearchResults...)
	n.Citations = append([]Citation(nil), m.Citations...)
	n.ProtocolStates = copyMap(m.ProtocolStates)
	n.Metadata = copyMap(m.Metadata)
	return &n
}

// compacted 返回块降级后的浅拷贝（会话持久化路径）。
func (m *AssistantMessage) compacted() *AssistantMessage {
	if len(m.Blocks) == 0 {
		return m
	}
	n := *m
	n.Blocks = compactBlocks(m.Blocks)
	return &n
}

func (m *AssistantMessage) UnmarshalJSON(data []byte) error {
	var raw struct {
		Text           string          `json:"text"`
		Thinking       string          `json:"thinking"`
		ToolCalls      []ToolCall      `json:"toolCalls"`
		Blocks         json.RawMessage `json:"blocks"`
		SearchResults  []SearchResult  `json:"searchResults"`
		Citations      []Citation      `json:"citations"`
		ProtocolStates map[string]any  `json:"protocolStates"`
		Metadata       map[string]any  `json:"metadata"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.Text, m.Thinking = raw.Text, raw.Thinking
	m.ToolCalls, m.SearchResults, m.Citations = raw.ToolCalls, raw.SearchResults, raw.Citations
	m.ProtocolStates, m.Metadata = raw.ProtocolStates, raw.Metadata
	if len(raw.Blocks) > 0 && string(raw.Blocks) != "null" {
		blocks, err := unmarshalBlocks(raw.Blocks)
		if err != nil {
			return err
		}
		m.Blocks = blocks
	}
	return nil
}

// Bind 结构化输出：助手文本按 JSON 解析为 T（包级泛型，nami.As[T] 先例）。
func Bind[T any](m *AssistantMessage) (T, error) {
	var t T
	if err := json.Unmarshal([]byte(m.Text), &t); err != nil {
		return t, fmt.Errorf("ai.Bind: %w", err)
	}
	return t, nil
}

// SearchResult 网络搜索/深度研究结果（P1 随方言补齐字段）。
type SearchResult struct {
	URL     string         `json:"url,omitempty"`
	Title   string         `json:"title,omitempty"`
	Snippet string         `json:"snippet,omitempty"`
	Meta    map[string]any `json:"metas,omitempty"`
}

// Citation 引用标注（P1 随方言补齐字段）。
type Citation struct {
	Index   int            `json:"index,omitempty"`
	Title   string         `json:"title,omitempty"`
	URL     string         `json:"url,omitempty"`
	Snippet string         `json:"snippet,omitempty"`
	Meta    map[string]any `json:"metas,omitempty"`
}
