package ai

import "encoding/json"

// Response 聚合响应：一次 call/stream 的不可变快照（对标 ChatResponse）。
type Response struct {
	model        string
	finishReason string
	usage        *Usage
	message      *AssistantMessage
	events       []*ChatEvent
	err          error
	raw          json.RawMessage
}

func (r *Response) Model() string { return r.model }

func (r *Response) Message() *AssistantMessage {
	if r.message == nil {
		return &AssistantMessage{}
	}
	return r.message
}

// Content 文本便捷形态（= Message().Text）。
func (r *Response) Content() string { return r.Message().Text }

func (r *Response) Text() string           { return r.Message().Text }
func (r *Response) Thinking() string       { return r.Message().Thinking }
func (r *Response) ToolCalls() []ToolCall  { return r.Message().ToolCalls }
func (r *Response) Blocks() []ContentBlock { return r.Message().Blocks }

// FinishReason 归一化终因："tool" | "stop" | 透传（"length"/"content_filter"）。
func (r *Response) FinishReason() string { return r.finishReason }

func (r *Response) Usage() *Usage        { return r.usage }
func (r *Response) Events() []*ChatEvent { return r.events }
func (r *Response) Err() error           { return r.err }
func (r *Response) Raw() json.RawMessage { return r.raw }

// IsTerminal 是否到达终态（有终因或错误）。
func (r *Response) IsTerminal() bool { return r.finishReason != "" || r.err != nil }

// IsEmpty 无任何载荷（text/thinking/toolCalls/blocks 全空）。
func (r *Response) IsEmpty() bool {
	m := r.Message()
	return m.Text == "" && m.Thinking == "" && len(m.ToolCalls) == 0 && len(m.Blocks) == 0
}

// HasContent 是否有可见内容（文本或多模态块）。
func (r *Response) HasContent() bool {
	m := r.Message()
	return m.Text != "" || len(m.Blocks) > 0
}
