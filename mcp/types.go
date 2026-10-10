// types.go 域类型（对标 McpSchema 域类型区）：方法/通知常量、能力协商、
// 工具/资源/提示三族、内容多态、采样/征询/补全、日志与进度通知。
// Java builder + 不可变类 → Go 导出 struct（值语义）+ 指针字段表可选。
package mcp

import (
	"encoding/json"
	"fmt"
)

// 方法与通知常量（Java METHOD_*）。
const (
	MethodInitialize = "initialize"

	MethodPing = "ping"

	MethodToolsList = "tools/list"
	MethodToolsCall = "tools/call"

	MethodResourcesList          = "resources/list"
	MethodResourcesRead          = "resources/read"
	MethodResourcesTemplatesList = "resources/templates/list"
	MethodResourcesSubscribe     = "resources/subscribe"
	MethodResourcesUnsubscribe   = "resources/unsubscribe"

	MethodPromptsList = "prompts/list"
	MethodPromptsGet  = "prompts/get"

	MethodCompletionComplete = "completion/complete"

	MethodLoggingSetLevel = "logging/setLevel"

	MethodRootsList = "roots/list"

	MethodSamplingCreateMessage = "sampling/createMessage"
	MethodElicitationCreate     = "elicitation/create"
)

// 通知方法常量（Java METHOD_NOTIFICATION_*）。
const (
	MethodNotificationInitialized          = "notifications/initialized"
	MethodNotificationProgress             = "notifications/progress"
	MethodNotificationCancelled            = "notifications/cancelled"
	MethodNotificationToolsListChanged     = "notifications/tools/list_changed"
	MethodNotificationResourcesListChanged = "notifications/resources/list_changed"
	MethodNotificationResourcesUpdated     = "notifications/resources/updated"
	MethodNotificationPromptsListChanged   = "notifications/prompts/list_changed"
	MethodNotificationMessage              = "notifications/message"
	MethodNotificationRootsListChanged     = "notifications/roots/list_changed"
)

// Role 消息角色（Java Role 枚举）。
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Annotations 内容注记（Java Annotations）：受众与优先级（0..1）。
type Annotations struct {
	Audience []Role   `json:"audience,omitempty"`
	Priority *float64 `json:"priority,omitempty"`
}

// Implementation 客户端/服务端实现标识。
type Implementation struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

// ---- 能力协商 ----

// RootCapabilities 客户端 roots 能力。
type RootCapabilities struct {
	ListChanged *bool `json:"listChanged,omitempty"`
}

// SamplingCapabilities 客户端采样能力（标记位）。
type SamplingCapabilities struct{}

// ElicitationCapabilities 客户端征询能力（标记位）。
type ElicitationCapabilities struct{}

// ClientCapabilities 客户端能力声明。
type ClientCapabilities struct {
	Experimental map[string]any           `json:"experimental,omitempty"`
	Roots        *RootCapabilities        `json:"roots,omitempty"`
	Sampling     *SamplingCapabilities    `json:"sampling,omitempty"`
	Elicitation  *ElicitationCapabilities `json:"elicitation,omitempty"`
}

// ToolCapabilities 服务端工具能力。
type ToolCapabilities struct {
	ListChanged *bool `json:"listChanged,omitempty"`
}

// ResourceCapabilities 服务端资源能力。
type ResourceCapabilities struct {
	Subscribe   *bool `json:"subscribe,omitempty"`
	ListChanged *bool `json:"listChanged,omitempty"`
}

// PromptCapabilities 服务端提示能力。
type PromptCapabilities struct {
	ListChanged *bool `json:"listChanged,omitempty"`
}

// LoggingCapabilities 服务端日志能力（标记位）。
type LoggingCapabilities struct{}

// CompletionCapabilities 服务端补全能力（标记位）。
type CompletionCapabilities struct{}

// ServerCapabilities 服务端能力声明。
type ServerCapabilities struct {
	Completions  *CompletionCapabilities `json:"completions,omitempty"`
	Experimental map[string]any          `json:"experimental,omitempty"`
	Logging      *LoggingCapabilities    `json:"logging,omitempty"`
	Prompts      *PromptCapabilities     `json:"prompts,omitempty"`
	Resources    *ResourceCapabilities   `json:"resources,omitempty"`
	Tools        *ToolCapabilities       `json:"tools,omitempty"`
}

// InitializeRequest 初始化请求（protocolVersion 为客户端支持的最新版本）。
type InitializeRequest struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ClientCapabilities `json:"capabilities"`
	ClientInfo      Implementation     `json:"clientInfo"`
	Meta            map[string]any     `json:"_meta,omitempty"`
}

// InitializeResult 初始化响应（protocolVersion 为协商定版版本）。
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      Implementation     `json:"serverInfo"`
	Instructions    string             `json:"instructions,omitempty"`
	Meta            map[string]any     `json:"_meta,omitempty"`
}

// EmptyResult 空结果（ping 等）。
type EmptyResult struct{}

// ---- 分页 ----

// PaginatedRequest 带游标的分页请求。
type PaginatedRequest struct {
	Cursor string         `json:"cursor,omitempty"`
	Meta   map[string]any `json:"_meta,omitempty"`
}

// ---- JSON Schema 与工具 ----

// JsonSchema 工具入参 schema（Java JsonSchema 字段子集）。
type JsonSchema struct {
	Type                 string         `json:"type,omitempty"`
	Properties           map[string]any `json:"properties,omitempty"`
	Required             []string       `json:"required,omitempty"`
	AdditionalProperties *bool          `json:"additionalProperties,omitempty"`
	Defs                 map[string]any `json:"$defs,omitempty"`
	Definitions          map[string]any `json:"definitions,omitempty"`
}

// ToolAnnotations 工具行为提示位（全部为 hint，不构成行为保证）。
type ToolAnnotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
	ReturnDirect    *bool  `json:"returnDirect,omitempty"`
}

// Tool 服务端暴露的工具。
type Tool struct {
	Name         string           `json:"name"`
	Title        string           `json:"title,omitempty"`
	Description  string           `json:"description,omitempty"`
	InputSchema  JsonSchema       `json:"inputSchema"`
	OutputSchema map[string]any   `json:"outputSchema,omitempty"`
	Annotations  *ToolAnnotations `json:"annotations,omitempty"`
	Meta         map[string]any   `json:"_meta,omitempty"`
}

// ListToolsResult 工具列表（分页）。
type ListToolsResult struct {
	Tools      []Tool         `json:"tools"`
	NextCursor string         `json:"nextCursor,omitempty"`
	Meta       map[string]any `json:"_meta,omitempty"`
}

// CallToolRequest 工具调用请求。
type CallToolRequest struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
	Meta      map[string]any `json:"_meta,omitempty"`
}

// CallToolResult 工具调用结果。
type CallToolResult struct {
	Content           []Content      `json:"content,omitempty"`
	IsError           bool           `json:"isError,omitempty"`
	StructuredContent any            `json:"structuredContent,omitempty"`
	Meta              map[string]any `json:"_meta,omitempty"`
}

// UnmarshalJSON Content 列表经 UnmarshalContent 判别解入。
func (r *CallToolResult) UnmarshalJSON(b []byte) error {
	type alias struct {
		Content           []json.RawMessage `json:"content,omitempty"`
		IsError           bool              `json:"isError,omitempty"`
		StructuredContent json.RawMessage   `json:"structuredContent,omitempty"`
		Meta              map[string]any    `json:"_meta,omitempty"`
	}
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	list, err := unmarshalContentList(a.Content)
	if err != nil {
		return err
	}
	r.Content = list
	r.IsError = a.IsError
	r.Meta = a.Meta
	if a.StructuredContent != nil {
		var v any
		if err := json.Unmarshal(a.StructuredContent, &v); err != nil {
			return err
		}
		r.StructuredContent = v
	}
	return nil
}

func unmarshalContentList(raw []json.RawMessage) ([]Content, error) {
	if raw == nil {
		return nil, nil
	}
	out := make([]Content, 0, len(raw))
	for _, e := range raw {
		c, err := UnmarshalContent(e)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// ---- 内容多态（判别 type 字段）----

// Content 内容块（Java @JsonTypeInfo(property="type") 多态）。具体类型带
// Type 字段（构造/反序列化填充），UnmarshalContent 为判别入口。
type Content interface {
	isContent()
	ContentType() string
}

// TextContent 文本内容。
type TextContent struct {
	Type        string         `json:"type"`
	Annotations *Annotations   `json:"annotations,omitempty"`
	Text        string         `json:"text"`
	Meta        map[string]any `json:"_meta,omitempty"`
}

func (c *TextContent) isContent()          {}
func (c *TextContent) ContentType() string { return "text" }

// NewTextContent 构造文本内容（Type 由构造器填充）。
func NewTextContent(text string) *TextContent {
	return &TextContent{Type: "text", Text: text}
}

// ImageContent 图片内容（base64）。
type ImageContent struct {
	Type        string         `json:"type"`
	Annotations *Annotations   `json:"annotations,omitempty"`
	Data        string         `json:"data"`
	MimeType    string         `json:"mimeType"`
	Meta        map[string]any `json:"_meta,omitempty"`
}

func (c *ImageContent) isContent()          {}
func (c *ImageContent) ContentType() string { return "image" }

// NewImageContent 构造图片内容。
func NewImageContent(data, mimeType string) *ImageContent {
	return &ImageContent{Type: "image", Data: data, MimeType: mimeType}
}

// AudioContent 音频内容（base64）。
type AudioContent struct {
	Type        string         `json:"type"`
	Annotations *Annotations   `json:"annotations,omitempty"`
	Data        string         `json:"data"`
	MimeType    string         `json:"mimeType"`
	Meta        map[string]any `json:"_meta,omitempty"`
}

func (c *AudioContent) isContent()          {}
func (c *AudioContent) ContentType() string { return "audio" }

// NewAudioContent 构造音频内容。
func NewAudioContent(data, mimeType string) *AudioContent {
	return &AudioContent{Type: "audio", Data: data, MimeType: mimeType}
}

// EmbeddedResource 内嵌资源内容。
type EmbeddedResource struct {
	Type        string           `json:"type"`
	Annotations *Annotations     `json:"annotations,omitempty"`
	Resource    ResourceContents `json:"resource"`
	Meta        map[string]any   `json:"_meta,omitempty"`
}

func (c *EmbeddedResource) isContent()          {}
func (c *EmbeddedResource) ContentType() string { return "resource" }

// UnmarshalJSON Resource 经 UnmarshalResourceContents 判别解入。
func (c *EmbeddedResource) UnmarshalJSON(b []byte) error {
	type alias struct {
		Type        string          `json:"type"`
		Annotations *Annotations    `json:"annotations,omitempty"`
		Resource    json.RawMessage `json:"resource"`
		Meta        map[string]any  `json:"_meta,omitempty"`
	}
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	rc, err := UnmarshalResourceContents(a.Resource)
	if err != nil {
		return err
	}
	c.Type, c.Annotations, c.Resource, c.Meta = a.Type, a.Annotations, rc, a.Meta
	return nil
}

// NewEmbeddedResource 构造内嵌资源内容。
func NewEmbeddedResource(rc ResourceContents) *EmbeddedResource {
	return &EmbeddedResource{Type: "resource", Resource: rc}
}

// ResourceLink 资源链接内容（客户端可按需再读）。
type ResourceLink struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	URI         string         `json:"uri"`
	Description string         `json:"description,omitempty"`
	MimeType    string         `json:"mimeType,omitempty"`
	Size        *int64         `json:"size,omitempty"`
	Annotations *Annotations   `json:"annotations,omitempty"`
	Meta        map[string]any `json:"_meta,omitempty"`
}

func (c *ResourceLink) isContent()          {}
func (c *ResourceLink) ContentType() string { return "resource_link" }

// NewResourceLink 构造资源链接内容。
func NewResourceLink(name, uri string) *ResourceLink {
	return &ResourceLink{Type: "resource_link", Name: name, URI: uri}
}

// UnmarshalContent 内容判别反序列化（Java @JsonSubTypes name ↔ type 字段）。
func UnmarshalContent(b []byte) (Content, error) {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return nil, err
	}
	var c Content
	switch probe.Type {
	case "text":
		c = &TextContent{Type: "text"}
	case "image":
		c = &ImageContent{Type: "image"}
	case "audio":
		c = &AudioContent{Type: "audio"}
	case "resource":
		c = &EmbeddedResource{Type: "resource"}
	case "resource_link":
		c = &ResourceLink{Type: "resource_link"}
	default:
		return nil, fmt.Errorf("mcp: unknown content type: %q", probe.Type)
	}
	if err := json.Unmarshal(b, c); err != nil {
		return nil, err
	}
	return c, nil
}

// ---- 资源族 ----

// Resource 资源描述。
type Resource struct {
	URI         string         `json:"uri"`
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description,omitempty"`
	MimeType    string         `json:"mimeType,omitempty"`
	Size        *int64         `json:"size,omitempty"`
	Annotations *Annotations   `json:"annotations,omitempty"`
	Meta        map[string]any `json:"_meta,omitempty"`
}

// ResourceTemplate 资源模板（RFC 6570 URI 模板）。
type ResourceTemplate struct {
	URITemplate string         `json:"uriTemplate"`
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description,omitempty"`
	MimeType    string         `json:"mimeType,omitempty"`
	Annotations *Annotations   `json:"annotations,omitempty"`
	Meta        map[string]any `json:"_meta,omitempty"`
}

// ListResourcesResult 资源列表（分页）。
type ListResourcesResult struct {
	Resources  []Resource     `json:"resources"`
	NextCursor string         `json:"nextCursor,omitempty"`
	Meta       map[string]any `json:"_meta,omitempty"`
}

// ListResourceTemplatesResult 资源模板列表（分页）。
type ListResourceTemplatesResult struct {
	ResourceTemplates []ResourceTemplate `json:"resourceTemplates"`
	NextCursor        string             `json:"nextCursor,omitempty"`
	Meta              map[string]any     `json:"_meta,omitempty"`
}

// ReadResourceRequest 资源读取请求。
type ReadResourceRequest struct {
	URI  string         `json:"uri"`
	Meta map[string]any `json:"_meta,omitempty"`
}

// ResourceContents 资源内容多态（Java Id.DEDUCTION：有 text 即文本、有 blob
// 即二进制，无显式 type 字段）。
type ResourceContents interface {
	isResourceContents()
	ResourceURI() string
	ResourceMime() string
}

// TextResourceContents 文本资源内容。
type TextResourceContents struct {
	URI      string         `json:"uri"`
	MimeType string         `json:"mimeType,omitempty"`
	Text     string         `json:"text"`
	Meta     map[string]any `json:"_meta,omitempty"`
}

func (*TextResourceContents) isResourceContents() {}
func (c *TextResourceContents) ResourceURI() string {
	return c.URI
}
func (c *TextResourceContents) ResourceMime() string { return c.MimeType }

// BlobResourceContents 二进制资源内容（base64）。
type BlobResourceContents struct {
	URI      string         `json:"uri"`
	MimeType string         `json:"mimeType,omitempty"`
	Blob     string         `json:"blob"`
	Meta     map[string]any `json:"_meta,omitempty"`
}

func (*BlobResourceContents) isResourceContents() {}
func (c *BlobResourceContents) ResourceURI() string {
	return c.URI
}
func (c *BlobResourceContents) ResourceMime() string { return c.MimeType }

// UnmarshalResourceContents 资源内容判别反序列化（DEDUCTION：text/blob 字段
// 存在性判别）。
func UnmarshalResourceContents(b []byte) (ResourceContents, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(b, &probe); err != nil {
		return nil, err
	}
	_, hasText := probe["text"]
	_, hasBlob := probe["blob"]
	switch {
	case hasText:
		rc := &TextResourceContents{}
		return rc, json.Unmarshal(b, rc)
	case hasBlob:
		rc := &BlobResourceContents{}
		return rc, json.Unmarshal(b, rc)
	default:
		return nil, fmt.Errorf("mcp: cannot deserialize ResourceContents: %s", string(b))
	}
}

// ReadResourceResult 资源读取结果。
type ReadResourceResult struct {
	Contents []ResourceContents `json:"contents"`
	Meta     map[string]any     `json:"_meta,omitempty"`
}

// UnmarshalJSON Contents 经 UnmarshalResourceContents 判别解入。
func (r *ReadResourceResult) UnmarshalJSON(b []byte) error {
	type alias struct {
		Contents []json.RawMessage `json:"contents"`
		Meta     map[string]any    `json:"_meta,omitempty"`
	}
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	out := make([]ResourceContents, 0, len(a.Contents))
	for _, e := range a.Contents {
		rc, err := UnmarshalResourceContents(e)
		if err != nil {
			return err
		}
		out = append(out, rc)
	}
	r.Contents, r.Meta = out, a.Meta
	return nil
}

// SubscribeRequest 资源订阅请求。
type SubscribeRequest struct {
	URI  string         `json:"uri"`
	Meta map[string]any `json:"_meta,omitempty"`
}

// UnsubscribeRequest 资源退订请求。
type UnsubscribeRequest struct {
	URI  string         `json:"uri"`
	Meta map[string]any `json:"_meta,omitempty"`
}

// ---- 提示族 ----

// PromptArgument 提示参数声明。
type PromptArgument struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Required    *bool  `json:"required,omitempty"`
}

// Prompt 提示模板描述。
type Prompt struct {
	Name        string           `json:"name"`
	Title       string           `json:"title,omitempty"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
	Meta        map[string]any   `json:"_meta,omitempty"`
}

// ListPromptsResult 提示列表（分页）。
type ListPromptsResult struct {
	Prompts    []Prompt       `json:"prompts"`
	NextCursor string         `json:"nextCursor,omitempty"`
	Meta       map[string]any `json:"_meta,omitempty"`
}

// GetPromptRequest 提示获取请求。
type GetPromptRequest struct {
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments,omitempty"`
	Meta      map[string]any    `json:"_meta,omitempty"`
}

// PromptMessage 提示消息。
type PromptMessage struct {
	Role    Role    `json:"role"`
	Content Content `json:"content"`
}

// UnmarshalJSON Content 经 UnmarshalContent 判别解入。
func (m *PromptMessage) UnmarshalJSON(b []byte) error {
	type alias struct {
		Role    Role            `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	c, err := UnmarshalContent(a.Content)
	if err != nil {
		return err
	}
	m.Role, m.Content = a.Role, c
	return nil
}

// GetPromptResult 提示获取结果。
type GetPromptResult struct {
	Description string          `json:"description,omitempty"`
	Messages    []PromptMessage `json:"messages"`
	Meta        map[string]any  `json:"_meta,omitempty"`
}

// ---- 日志与进度 ----

// LoggingLevel 日志级别（syslog 级 + default；Java 枚举带 int 值供比较）。
type LoggingLevel string

const (
	LogLevelDefault   LoggingLevel = "default"
	LogLevelDebug     LoggingLevel = "debug"
	LogLevelInfo      LoggingLevel = "info"
	LogLevelNotice    LoggingLevel = "notice"
	LogLevelWarning   LoggingLevel = "warning"
	LogLevelError     LoggingLevel = "error"
	LogLevelCritical  LoggingLevel = "critical"
	LogLevelAlert     LoggingLevel = "alert"
	LogLevelEmergency LoggingLevel = "emergency"
)

// Severity 数值序（default 最低，Java level int 对应物）。
func (l LoggingLevel) Severity() int {
	switch l {
	case LogLevelDebug:
		return 0
	case LogLevelInfo:
		return 1
	case LogLevelNotice:
		return 2
	case LogLevelWarning:
		return 3
	case LogLevelError:
		return 4
	case LogLevelCritical:
		return 5
	case LogLevelAlert:
		return 6
	case LogLevelEmergency:
		return 7
	default: // LogLevelDefault 及未知
		return -1
	}
}

// SetLevelRequest 日志级别设置请求。
type SetLevelRequest struct {
	Level LoggingLevel   `json:"level"`
	Meta  map[string]any `json:"_meta,omitempty"`
}

// LoggingMessageNotification 日志消息通知。
type LoggingMessageNotification struct {
	Level  LoggingLevel   `json:"level"`
	Logger string         `json:"logger,omitempty"`
	Data   string         `json:"data"`
	Meta   map[string]any `json:"_meta,omitempty"`
}

// ProgressNotification 进度通知（progressToken 回引用）。
type ProgressNotification struct {
	ProgressToken any            `json:"progressToken"`
	Progress      float64        `json:"progress"`
	Total         *float64       `json:"total,omitempty"`
	Message       string         `json:"message,omitempty"`
	Meta          map[string]any `json:"_meta,omitempty"`
}

// ResourcesUpdatedNotification 资源更新通知。
type ResourcesUpdatedNotification struct {
	URI  string         `json:"uri"`
	Meta map[string]any `json:"_meta,omitempty"`
}

// ---- Roots（客户端反向能力）----

// Root 客户端根目录。
type Root struct {
	URI  string `json:"uri"`
	Name string `json:"name,omitempty"`
}

// ListRootsResult 根目录列表。
type ListRootsResult struct {
	Roots      []Root         `json:"roots"`
	NextCursor string         `json:"nextCursor,omitempty"`
	Meta       map[string]any `json:"_meta,omitempty"`
}

// ---- 采样（客户端反向能力）----

// ModelHint 模型提示。
type ModelHint struct {
	Name string `json:"name,omitempty"`
}

// ModelPreferences 模型偏好。
type ModelPreferences struct {
	Hints                []ModelHint `json:"hints,omitempty"`
	CostPriority         *float64    `json:"costPriority,omitempty"`
	SpeedPriority        *float64    `json:"speedPriority,omitempty"`
	IntelligencePriority *float64    `json:"intelligencePriority,omitempty"`
}

// SamplingMessage 采样消息。
type SamplingMessage struct {
	Role    Role    `json:"role"`
	Content Content `json:"content"`
}

// UnmarshalJSON Content 经 UnmarshalContent 判别解入。
func (m *SamplingMessage) UnmarshalJSON(b []byte) error {
	type alias struct {
		Role    Role            `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	c, err := UnmarshalContent(a.Content)
	if err != nil {
		return err
	}
	m.Role, m.Content = a.Role, c
	return nil
}

// ContextInclusionStrategy 采样上下文包含策略。
type ContextInclusionStrategy string

const (
	ContextNone       ContextInclusionStrategy = "none"
	ContextThisServer ContextInclusionStrategy = "thisServer"
	ContextAllServers ContextInclusionStrategy = "allServers"
)

// CreateMessageRequest 采样请求（服务端 → 客户端）。
type CreateMessageRequest struct {
	Messages         []SamplingMessage        `json:"messages"`
	ModelPreferences *ModelPreferences        `json:"modelPreferences,omitempty"`
	SystemPrompt     string                   `json:"systemPrompt,omitempty"`
	IncludeContext   ContextInclusionStrategy `json:"includeContext,omitempty"`
	Temperature      *float64                 `json:"temperature,omitempty"`
	MaxTokens        int                      `json:"maxTokens"`
	StopSequences    []string                 `json:"stopSequences,omitempty"`
	Metadata         map[string]any           `json:"metadata,omitempty"`
	Meta             map[string]any           `json:"_meta,omitempty"`
}

// StopReason 采样停止原因。
type StopReason string

const (
	StopEndTurn   StopReason = "endTurn"
	StopSequence  StopReason = "stopSequence"
	StopMaxTokens StopReason = "maxTokens"
	StopUnknown   StopReason = "unknown"
)

// CreateMessageResult 采样结果。
type CreateMessageResult struct {
	Role       Role           `json:"role"`
	Content    Content        `json:"content"`
	Model      string         `json:"model,omitempty"`
	StopReason StopReason     `json:"stopReason,omitempty"`
	Meta       map[string]any `json:"_meta,omitempty"`
}

// UnmarshalJSON Content 经 UnmarshalContent 判别解入。
func (r *CreateMessageResult) UnmarshalJSON(b []byte) error {
	type alias struct {
		Role       Role            `json:"role"`
		Content    json.RawMessage `json:"content"`
		Model      string          `json:"model,omitempty"`
		StopReason StopReason      `json:"stopReason,omitempty"`
		Meta       map[string]any  `json:"_meta,omitempty"`
	}
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	c, err := UnmarshalContent(a.Content)
	if err != nil {
		return err
	}
	r.Role, r.Content, r.Model, r.StopReason, r.Meta = a.Role, c, a.Model, a.StopReason, a.Meta
	return nil
}

// ---- 征询（客户端反向能力；2025-11-25 的 Form/Url 为扩展位）----

// ElicitAction 征询动作。
type ElicitAction string

const (
	ElicitAccept  ElicitAction = "accept"
	ElicitDecline ElicitAction = "decline"
	ElicitCancel  ElicitAction = "cancel"
)

// ElicitRequest 征询请求（服务端 → 客户端）。
type ElicitRequest struct {
	Message         string         `json:"message"`
	RequestedSchema map[string]any `json:"requestedSchema"`
	Meta            map[string]any `json:"_meta,omitempty"`
}

// ElicitResult 征询结果。
type ElicitResult struct {
	Action  ElicitAction   `json:"action"`
	Content map[string]any `json:"content,omitempty"`
	Meta    map[string]any `json:"_meta,omitempty"`
}

// ---- 补全 ----

// PromptReference 提示引用。
type PromptReference struct {
	Type string `json:"type"` // 恒 "ref/prompt"
	Name string `json:"name"`
}

// ResourceReference 资源引用。
type ResourceReference struct {
	Type string `json:"type"` // 恒 "ref/resource"
	URI  string `json:"uri"`
}

// CompleteArgument 补全参数。
type CompleteArgument struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// CompleteContext 补全上下文。
type CompleteContext struct {
	Arguments map[string]string `json:"arguments,omitempty"`
}

// CompleteRequest 补全请求。
type CompleteRequest struct {
	Ref      json.RawMessage  `json:"ref"` // PromptReference | ResourceReference（按 type 判别）
	Argument CompleteArgument `json:"argument"`
	Context  *CompleteContext `json:"context,omitempty"`
	Meta     map[string]any   `json:"_meta,omitempty"`
}

// UnmarshalCompleteRef 补全引用判别（ref/prompt | ref/resource）。
func UnmarshalCompleteRef(b []byte) (any, error) {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return nil, err
	}
	switch probe.Type {
	case "ref/prompt":
		r := &PromptReference{}
		return r, json.Unmarshal(b, r)
	case "ref/resource":
		r := &ResourceReference{}
		return r, json.Unmarshal(b, r)
	default:
		return nil, fmt.Errorf("mcp: unknown complete ref type: %q", probe.Type)
	}
}

// CompleteCompletion 补全建议。
type CompleteCompletion struct {
	Values  []string `json:"values"`
	Total   *int     `json:"total,omitempty"`
	HasMore bool     `json:"hasMore,omitempty"`
}

// CompleteResult 补全结果。
type CompleteResult struct {
	Completion CompleteCompletion `json:"completion"`
	Meta       map[string]any     `json:"_meta,omitempty"`
}
