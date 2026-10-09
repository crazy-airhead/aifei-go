// Package aisdk 把 ChatEvent 流适配为 Vercel AI SDK UI Message Stream Protocol v1
// 的 SSE parts（对标 solon-ai-ui-aisdk；协议规范见
// https://ai-sdk.dev/docs/ai-sdk-ui/stream-protocol）。
//
// 每帧形如 `data: {"type":"...", ...}\n\n`，流末以 `data: [DONE]\n\n` 收尾，
// 可直接作为 text/event-stream 端点响应体，兼容 @ai-sdk/vue 与 @ai-sdk/react
// 的 useChat。
package aisdk

// Part SSE part 载荷（每个具体 part 一个 struct，字段名/序与 Java writeFields
// 一致；Type 由构造函数注入，encoding/json 按 tag 直出协议帧 data 体）。
type Part interface {
	partType() string // 封闭标记：只有本包 part 实现得动
}

// StartPart 起始 part（携带 messageId）。
type StartPart struct {
	Type      string `json:"type"`
	MessageID string `json:"messageId"`
}

func NewStartPart(messageID string) StartPart { return StartPart{Type: "start", MessageID: messageID} }

// StartStepPart 步骤开始（多轮工具调用的步骤边界）。
type StartStepPart struct {
	Type string `json:"type"`
}

func NewStartStepPart() StartStepPart { return StartStepPart{Type: "start-step"} }

// FinishStepPart 步骤结束。
type FinishStepPart struct {
	Type string `json:"type"`
}

func NewFinishStepPart() FinishStepPart { return FinishStepPart{Type: "finish-step"} }

// TextStartPart 正文 part 开。
type TextStartPart struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

func NewTextStartPart(id string) TextStartPart { return TextStartPart{Type: "text-start", ID: id} }

// TextDeltaPart 正文增量。
type TextDeltaPart struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Delta string `json:"delta"`
}

func NewTextDeltaPart(id, delta string) TextDeltaPart {
	return TextDeltaPart{Type: "text-delta", ID: id, Delta: delta}
}

// TextEndPart 正文 part 闭。
type TextEndPart struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

func NewTextEndPart(id string) TextEndPart { return TextEndPart{Type: "text-end", ID: id} }

// ReasoningStartPart 推理 part 开。
type ReasoningStartPart struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

func NewReasoningStartPart(id string) ReasoningStartPart {
	return ReasoningStartPart{Type: "reasoning-start", ID: id}
}

// ReasoningDeltaPart 推理增量。
type ReasoningDeltaPart struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Delta string `json:"delta"`
}

func NewReasoningDeltaPart(id, delta string) ReasoningDeltaPart {
	return ReasoningDeltaPart{Type: "reasoning-delta", ID: id, Delta: delta}
}

// ReasoningEndPart 推理 part 闭。
type ReasoningEndPart struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

func NewReasoningEndPart(id string) ReasoningEndPart {
	return ReasoningEndPart{Type: "reasoning-end", ID: id}
}

// ToolInputStartPart 工具输入开（参数流式传输开始）。
type ToolInputStartPart struct {
	Type       string `json:"type"`
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
}

func NewToolInputStartPart(toolCallID, toolName string) ToolInputStartPart {
	return ToolInputStartPart{Type: "tool-input-start", ToolCallID: toolCallID, ToolName: toolName}
}

// ToolInputDeltaPart 工具参数增量。
type ToolInputDeltaPart struct {
	Type           string `json:"type"`
	ToolCallID     string `json:"toolCallId"`
	InputTextDelta string `json:"inputTextDelta"`
}

func NewToolInputDeltaPart(toolCallID, delta string) ToolInputDeltaPart {
	return ToolInputDeltaPart{Type: "tool-input-delta", ToolCallID: toolCallID, InputTextDelta: delta}
}

// ToolInputAvailablePart 工具输入就绪（参数完整可执行）。Input 为解析后的参数
// 对象；nil 时省略 input 字段（Java 同款）。
type ToolInputAvailablePart struct {
	Type       string         `json:"type"`
	ToolCallID string         `json:"toolCallId"`
	ToolName   string         `json:"toolName"`
	Input      map[string]any `json:"input,omitempty"`
}

func NewToolInputAvailablePart(toolCallID, toolName string, input map[string]any) ToolInputAvailablePart {
	return ToolInputAvailablePart{
		Type: "tool-input-available", ToolCallID: toolCallID, ToolName: toolName, Input: input,
	}
}

// ToolOutputAvailablePart 工具输出就绪。Output 原样嵌入协议帧（字符串/对象均可）。
type ToolOutputAvailablePart struct {
	Type       string `json:"type"`
	ToolCallID string `json:"toolCallId"`
	Output     any    `json:"output,omitempty"`
}

func NewToolOutputAvailablePart(toolCallID string, output any) ToolOutputAvailablePart {
	return ToolOutputAvailablePart{Type: "tool-output-available", ToolCallID: toolCallID, Output: output}
}

// FinishUsage finish part 内嵌的用量摘要（只取协议关心的四项；think>0 才带
// reasoningTokens，Java 同款）。
type FinishUsage struct {
	PromptTokens     int64 `json:"promptTokens"`
	CompletionTokens int64 `json:"completionTokens"`
	TotalTokens      int64 `json:"totalTokens"`
	ReasoningTokens  int64 `json:"reasoningTokens,omitempty"`
}

// FinishPart 流终态（finishReason + 用量摘要）。
type FinishPart struct {
	Type         string       `json:"type"`
	FinishReason string       `json:"finishReason"`
	Usage        *FinishUsage `json:"usage,omitempty"`
}

func NewFinishPart(finishReason string, usage *FinishUsage) FinishPart {
	return FinishPart{Type: "finish", FinishReason: finishReason, Usage: usage}
}

// ErrorPart 错误终态（与 finish("error") 成对出现）。
type ErrorPart struct {
	Type      string `json:"type"`
	ErrorText string `json:"errorText"`
}

func NewErrorPart(errorText string) ErrorPart { return ErrorPart{Type: "error", ErrorText: errorText} }

// DataPart 未建模事件的通用 data-* part（Type 形如 "data-custom"；Data 省略时
// 只剩 type）。
type DataPart struct {
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
}

func NewDataPart(dataType string, data any) DataPart {
	return DataPart{Type: "data-" + dataType, Data: data}
}

// FilePart 媒体文件 part（url + mediaType）。
type FilePart struct {
	Type      string `json:"type"`
	URL       string `json:"url"`
	MediaType string `json:"mediaType"`
}

func NewFilePart(url, mediaType string) FilePart {
	return FilePart{Type: "file", URL: url, MediaType: mediaType}
}

// MetadataPart 消息元数据（协议 type 为 "message-metadata"）。
type MetadataPart struct {
	Type            string         `json:"type"`
	MessageMetadata map[string]any `json:"messageMetadata,omitempty"`
}

func NewMetadataPart(meta map[string]any) MetadataPart {
	return MetadataPart{Type: "message-metadata", MessageMetadata: meta}
}

// AbortPart 上游中止（reason 为空时省略）。
type AbortPart struct {
	Type   string `json:"type"`
	Reason string `json:"reason,omitempty"`
}

func NewAbortPart(reason string) AbortPart { return AbortPart{Type: "abort", Reason: reason} }

// SourceUrlPart 网络来源引用。
type SourceUrlPart struct {
	Type     string `json:"type"`
	SourceID string `json:"sourceId"`
	URL      string `json:"url"`
	Title    string `json:"title"`
}

func NewSourceUrlPart(sourceID, url, title string) SourceUrlPart {
	return SourceUrlPart{Type: "source-url", SourceID: sourceID, URL: url, Title: title}
}

// SourceDocumentPart 文档来源引用（title 可省略）。
type SourceDocumentPart struct {
	Type      string `json:"type"`
	SourceID  string `json:"sourceId"`
	MediaType string `json:"mediaType"`
	Title     string `json:"title,omitempty"`
}

func NewSourceDocumentPart(sourceID, mediaType, title string) SourceDocumentPart {
	return SourceDocumentPart{Type: "source-document", SourceID: sourceID, MediaType: mediaType, Title: title}
}

// partType 封闭标记实现（不承担运行时职责，只为把 Part 集合锁在本包）。

func (p StartPart) partType() string               { return p.Type }
func (p StartStepPart) partType() string           { return p.Type }
func (p FinishStepPart) partType() string          { return p.Type }
func (p TextStartPart) partType() string           { return p.Type }
func (p TextDeltaPart) partType() string           { return p.Type }
func (p TextEndPart) partType() string             { return p.Type }
func (p ReasoningStartPart) partType() string      { return p.Type }
func (p ReasoningDeltaPart) partType() string      { return p.Type }
func (p ReasoningEndPart) partType() string        { return p.Type }
func (p ToolInputStartPart) partType() string      { return p.Type }
func (p ToolInputDeltaPart) partType() string      { return p.Type }
func (p ToolInputAvailablePart) partType() string  { return p.Type }
func (p ToolOutputAvailablePart) partType() string { return p.Type }
func (p FinishPart) partType() string              { return p.Type }
func (p ErrorPart) partType() string               { return p.Type }
func (p DataPart) partType() string                { return p.Type }
func (p FilePart) partType() string                { return p.Type }
func (p MetadataPart) partType() string            { return p.Type }
func (p AbortPart) partType() string               { return p.Type }
func (p SourceUrlPart) partType() string           { return p.Type }
func (p SourceDocumentPart) partType() string      { return p.Type }
