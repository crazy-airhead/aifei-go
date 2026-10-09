package ai

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// ProtocolState 协议回放状态（对标 Java MessageProtocolState）：方言把厂商往返
// 状态（Gemini thoughtSignature、Anthropic thinking 签名等）挂在终态消息的
// ProtocolStates 袋上，随会话原样回传。SemanticHash 在消息物化边界绑定语义摘要——
// 消息语义被改写后拒绝回放陈旧协议快照（P1-b 方言矩阵引入）。
type ProtocolState struct {
	Version      int            `json:"version"`
	SemanticHash string         `json:"semanticHash,omitempty"`
	Data         map[string]any `json:"data,omitempty"`
}

// protocolSemanticPayload 参与语义摘要的字段（不含 metadata 与协议状态本身）。
type protocolSemanticPayload struct {
	Text          string         `json:"text"`
	Thinking      string         `json:"thinking,omitempty"`
	ToolCalls     []ToolCall     `json:"toolCalls,omitempty"`
	Blocks        []ContentBlock `json:"blocks,omitempty"`
	SearchResults []SearchResult `json:"searchResults,omitempty"`
	Citations     []Citation     `json:"citations,omitempty"`
}

// SemanticHash 消息语义摘要（sha256 of 规范 JSON）。仅自洽用途——跨实现比对无意义。
func SemanticHash(m *AssistantMessage) string {
	if m == nil {
		return ""
	}
	data, err := json.Marshal(protocolSemanticPayload{
		Text:          m.Text,
		Thinking:      m.Thinking,
		ToolCalls:     m.ToolCalls,
		Blocks:        m.Blocks,
		SearchResults: m.SearchResults,
		Citations:     m.Citations,
	})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// BindProtocolStateHashes 为消息上尚未绑定摘要的协议状态补绑当前语义摘要
// （对标 Java bindProtocolStateSemanticHashes：快照边界一次性完成）。值形态宽容：
// *ProtocolState 原地补绑；map（JSON 反序列化形态）经规范化后回写。
func BindProtocolStateHashes(m *AssistantMessage) {
	if m == nil || len(m.ProtocolStates) == 0 {
		return
	}
	for k, v := range m.ProtocolStates {
		state := normalizeProtocolState(v)
		if state == nil {
			continue
		}
		if state.SemanticHash == "" {
			state.SemanticHash = SemanticHash(m)
		}
		m.ProtocolStates[k] = state
	}
}

// normalizeProtocolState 状态值宽容形态归一（*ProtocolState | map[string]any）。
func normalizeProtocolState(v any) *ProtocolState {
	switch s := v.(type) {
	case *ProtocolState:
		return s
	case ProtocolState:
		return &s
	case map[string]any:
		data, err := json.Marshal(s)
		if err != nil {
			return nil
		}
		st := &ProtocolState{}
		if json.Unmarshal(data, st) != nil {
			return nil
		}
		return st
	}
	return nil
}
