// server.go 服务端门面（对标 McpServerEndpointProvider + McpAsyncServer 的
// 注册/分发面）：工具/资源/提示三注册表 + 能力协商 + 变更通知广播 + CallCtx
// 反向采样/征询。传输承载（streamable HTTP / 旧式 SSE / stdio）在
// server_http.go 与 server_session.go。
package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/crazy-airhead/aifei-go/log"
)

// ToolFunc 工具处理器（对标 @ToolMapping 方法）：err 非空时按 Java
// McpResultResponder 语义转为 isError CallToolResult（协议层成功）。
type ToolFunc func(ctx *CallCtx, args map[string]any) (*CallToolResult, error)

// ResourceFunc 资源处理器（对标 @ResourceMapping）：返回值经结果转换——
// *ReadResourceResult / ResourceContents / []ResourceContents / string（文本）/
// []byte（base64 blob）。
type ResourceFunc func(ctx *CallCtx) (any, error)

// PromptFunc 提示处理器（对标 @PromptMapping）：返回值经结果转换——
// *GetPromptResult / PromptMessage / []PromptMessage / string（user 文本）。
type PromptFunc func(ctx *CallCtx, args map[string]string) (any, error)

// ServerOption 服务端选项。
type ServerOption func(*Server)

// WithInstructions 服务端使用指引（initialize 结果 instructions）。
func WithInstructions(instructions string) ServerOption {
	return func(s *Server) { s.instructions = instructions }
}

// WithCapabilities 覆盖能力声明（缺省 = 工具 listChanged + 资源
// subscribe/listChanged + 提示 listChanged + 日志，Java StatefulMcpServerHost
// 硬编码四能力同宽）。
func WithCapabilities(cap ServerCapabilities) ServerOption {
	return func(s *Server) { s.capabilitiesOverride = &cap }
}

// WithStrictToolName 工具名严格校验（默认开；不合法注册即 panic，Java
// IllegalArgumentException 对应物；false 时仅告警放行）。
func WithStrictToolName(strict bool) ServerOption {
	return func(s *Server) { s.strictToolName = strict }
}

// WithSecurityValidator HTTP 请求安全钩子（默认放行；Java
// ServerTransportSecurityValidator.NOOP 对应物）——拒绝时整请求 403。
func WithSecurityValidator(fn func(*http.Request) error) ServerOption {
	return func(s *Server) {
		if fn != nil {
			s.securityValidator = fn
		}
	}
}

// WithServerRequestTimeout 服务端→客户端反向请求（采样/征询/ping 保活）
// 超时（默认 20s）。
func WithServerRequestTimeout(d time.Duration) ServerOption {
	return func(s *Server) { s.requestTimeout = d }
}

// WithKeepAlive GET 流保活间隔（Java heartbeatInterval；默认关——按间隔向
// 各会话发 ping 反向请求）。
func WithKeepAlive(d time.Duration) ServerOption {
	return func(s *Server) { s.keepAlive = d }
}

// WithServerProtocolVersions 服务端支持的协议版本表（升序，末位为最新；默认
// 全量表）。名称带 Server 前缀以避开客户端同名选项。
func WithServerProtocolVersions(versions []string) ServerOption {
	return func(s *Server) { s.protocolVersions = versions }
}

// Server MCP 服务端门面。
type Server struct {
	serverInfo        Implementation
	instructions      string
	capabilities      func() ServerCapabilities
	requestTimeout    time.Duration
	keepAlive         time.Duration
	protocolVersions  []string
	strictToolName    bool
	securityValidator func(*http.Request) error

	mu                   sync.RWMutex
	tools                map[string]*ToolSpec
	resources            map[string]*ResourceSpec
	resourceTemplates    map[string]*ResourceSpec
	prompts              map[string]*PromptSpec
	subscriptions        map[string]map[string]*serverSession // uri → 会话集
	sessions             map[string]*serverSession
	closing              bool
	capabilitiesOverride *ServerCapabilities

	handlers map[string]serverHandlerFunc
}

type serverHandlerFunc func(cc *CallCtx, params json.RawMessage) (any, *JSONRPCError)

// NewServer 建服务端。info.Name 为空时默认 "aifei-go mcp server"。
func NewServer(info Implementation, opts ...ServerOption) *Server {
	if info.Name == "" {
		info.Name = "aifei-go mcp server"
	}
	if info.Version == "" {
		info.Version = "1.0.0"
	}
	s := &Server{
		serverInfo:        info,
		requestTimeout:    20 * time.Second,
		protocolVersions:  SupportedVersions,
		strictToolName:    true,
		securityValidator: func(*http.Request) error { return nil },
		tools:             map[string]*ToolSpec{},
		resources:         map[string]*ResourceSpec{},
		resourceTemplates: map[string]*ResourceSpec{},
		prompts:           map[string]*PromptSpec{},
		subscriptions:     map[string]map[string]*serverSession{},
		sessions:          map[string]*serverSession{},
	}
	s.capabilities = s.defaultCapabilities
	for _, o := range opts {
		o(s)
	}
	s.handlers = s.buildHandlers()
	return s
}

// defaultCapabilities 缺省能力（Java StatefulMcpServerHost：tools(true) +
// resources(true,true) + prompts(true) + logging()）。
func (s *Server) defaultCapabilities() ServerCapabilities {
	if s.capabilitiesOverride != nil {
		return *s.capabilitiesOverride
	}
	t, r, p := true, true, true
	return ServerCapabilities{
		Tools:     &ToolCapabilities{ListChanged: &t},
		Resources: &ResourceCapabilities{Subscribe: &r, ListChanged: &r},
		Prompts:   &PromptCapabilities{ListChanged: &p},
		Logging:   &LoggingCapabilities{},
	}
}

// ---- 注册：工具 ----

// ToolSpec 工具注册项（链式补充元数据后仍可在注册后继续持有引用）。
type ToolSpec struct {
	Tool    Tool
	Handler ToolFunc
}

// Title 补充标题。
func (t *ToolSpec) Title(v string) *ToolSpec { t.Tool.Title = v; return t }

// Desc 补充描述。
func (t *ToolSpec) Desc(v string) *ToolSpec { t.Tool.Description = v; return t }

// Annotations 补充行为注记。
func (t *ToolSpec) Annotations(a ToolAnnotations) *ToolSpec {
	t.Tool.Annotations = &a
	return t
}

// OutputSchema 补充输出 schema（结构化输出）。
func (t *ToolSpec) OutputSchema(m map[string]any) *ToolSpec { t.Tool.OutputSchema = m; return t }

// Meta 补充 _meta。
func (t *ToolSpec) Meta(m map[string]any) *ToolSpec { t.Tool.Meta = m; return t }

// Tool 注册工具。inputSchema 为 JSON Schema 文本（空串 = {"type":"object"}）。
// 同名重复注册 = 替换（Java "Replace existing Tool" 告警语义）；注册后按
// listChanged 能力自动广播变更通知。
func (s *Server) Tool(name, inputSchema string, fn ToolFunc) *ToolSpec {
	if !validToolName(name) {
		if s.strictToolName {
			panic(fmt.Sprintf("mcp: invalid tool name: %q", name))
		}
		log.Warn("mcp: invalid tool name: %q (strict off, accepted)", name)
	}
	schema := JsonSchema{Type: "object"}
	if inputSchema != "" {
		if err := json.Unmarshal([]byte(inputSchema), &schema); err != nil {
			panic(fmt.Sprintf("mcp: tool %q input schema: %v", name, err))
		}
	}
	spec := &ToolSpec{
		Tool:    Tool{Name: name, InputSchema: schema},
		Handler: fn,
	}
	s.mu.Lock()
	replaced := false
	if _, ok := s.tools[name]; ok {
		replaced = true
	}
	s.tools[name] = spec
	s.mu.Unlock()
	if replaced {
		log.Warn("mcp: replace existing tool with name '%s'", name)
	}
	s.afterRegistryChange(func(cap ServerCapabilities) bool {
		return cap.Tools != nil && cap.Tools.ListChanged != nil && *cap.Tools.ListChanged
	}, s.NotifyToolsListChanged)
	return spec
}

// HasTool 是否已注册。
func (s *Server) HasTool(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.tools[name]
	return ok
}

// RemoveTool 移除工具（未找到返回 false；命中时按能力广播变更通知）。
func (s *Server) RemoveTool(name string) bool {
	s.mu.Lock()
	_, ok := s.tools[name]
	delete(s.tools, name)
	s.mu.Unlock()
	if !ok {
		log.Warn("mcp: ignore as a tool with name '%s' not found", name)
		return false
	}
	s.afterRegistryChange(func(cap ServerCapabilities) bool {
		return cap.Tools != nil && cap.Tools.ListChanged != nil && *cap.Tools.ListChanged
	}, s.NotifyToolsListChanged)
	return true
}

// ToolNames 已注册工具名（有序）。
func (s *Server) ToolNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortedKeys(s.tools)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---- 注册：资源 ----

// ResourceSpec 资源注册项（含模板形态）。
type ResourceSpec struct {
	Resource   Resource
	Template   ResourceTemplate
	IsTemplate bool
	Handler    ResourceFunc
}

// Title 补充标题。
func (r *ResourceSpec) Title(v string) *ResourceSpec {
	r.Resource.Title = v
	r.Template.Title = v
	return r
}

// Desc 补充描述。
func (r *ResourceSpec) Desc(v string) *ResourceSpec {
	r.Resource.Description = v
	r.Template.Description = v
	return r
}

// MimeType 补充 MIME（读取结果缺省 text/plain）。
func (r *ResourceSpec) MimeType(v string) *ResourceSpec {
	r.Resource.MimeType = v
	r.Template.MimeType = v
	return r
}

// mimeType 读取结果 MIME（静态/模板形态取一，缺省 text/plain）。
func (r *ResourceSpec) mimeType() string {
	if r.IsTemplate {
		if r.Template.MimeType != "" {
			return r.Template.MimeType
		}
	} else if r.Resource.MimeType != "" {
		return r.Resource.MimeType
	}
	return "text/plain"
}

// Resource 注册资源。uri 含 '{' 即按 RFC 6570 模板注册（Java
// StatefulResourceRegistry 的 uri.indexOf('{') 分流同款）；读取结果经
// 资源结果转换。
func (s *Server) Resource(uri, name string, fn ResourceFunc) *ResourceSpec {
	if strings.Contains(uri, "{") {
		return s.ResourceTemplate(uri, name, fn)
	}
	spec := &ResourceSpec{
		Resource: Resource{URI: uri, Name: name},
		Handler:  fn,
	}
	s.mu.Lock()
	s.resources[uri] = spec
	s.mu.Unlock()
	s.notifyResourcesListChangedIfEnabled()
	return spec
}

// ResourceTemplate 注册资源模板（显式形态，等价 uri 含 '{'）。
func (s *Server) ResourceTemplate(tpl, name string, fn ResourceFunc) *ResourceSpec {
	spec := &ResourceSpec{
		Template:   ResourceTemplate{URITemplate: tpl, Name: name},
		IsTemplate: true,
		Handler:    fn,
	}
	s.mu.Lock()
	s.resourceTemplates[tpl] = spec
	s.mu.Unlock()
	s.notifyResourcesListChangedIfEnabled()
	return spec
}

// HasResource 是否已注册（静态或模板均查）。
func (s *Server) HasResource(uri string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.resources[uri]; ok {
		return true
	}
	_, ok := s.resourceTemplates[uri]
	return ok
}

// RemoveResource 移除资源（静态/模板按 uri 分流）。
func (s *Server) RemoveResource(uri string) bool {
	s.mu.Lock()
	_, ok1 := s.resources[uri]
	delete(s.resources, uri)
	_, ok2 := s.resourceTemplates[uri]
	delete(s.resourceTemplates, uri)
	s.mu.Unlock()
	if !(ok1 || ok2) {
		return false
	}
	s.notifyResourcesListChangedIfEnabled()
	return true
}

// ResourceURIs 已注册资源 URI（静态 + 模板，有序）。
func (s *Server) ResourceURIs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := append(sortedKeys(s.resources), sortedKeys(s.resourceTemplates)...)
	sort.Strings(out)
	return out
}

func (s *Server) notifyResourcesListChangedIfEnabled() {
	s.afterRegistryChange(func(cap ServerCapabilities) bool {
		return cap.Resources != nil && cap.Resources.ListChanged != nil && *cap.Resources.ListChanged
	}, s.NotifyResourcesListChanged)
}

// ---- 注册：提示 ----

// PromptSpec 提示注册项。
type PromptSpec struct {
	Prompt  Prompt
	Handler PromptFunc
}

// Title 补充标题。
func (p *PromptSpec) Title(v string) *PromptSpec { p.Prompt.Title = v; return p }

// Desc 补充描述（同时作为 GetPromptResult 缺省描述）。
func (p *PromptSpec) Desc(v string) *PromptSpec { p.Prompt.Description = v; return p }

// Prompt 注册提示。
func (s *Server) Prompt(name string, arguments []PromptArgument, fn PromptFunc) *PromptSpec {
	spec := &PromptSpec{
		Prompt:  Prompt{Name: name, Arguments: arguments},
		Handler: fn,
	}
	s.mu.Lock()
	s.prompts[name] = spec
	s.mu.Unlock()
	s.afterRegistryChange(func(cap ServerCapabilities) bool {
		return cap.Prompts != nil && cap.Prompts.ListChanged != nil && *cap.Prompts.ListChanged
	}, s.NotifyPromptsListChanged)
	return spec
}

// HasPrompt 是否已注册。
func (s *Server) HasPrompt(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.prompts[name]
	return ok
}

// RemovePrompt 移除提示。
func (s *Server) RemovePrompt(name string) bool {
	s.mu.Lock()
	_, ok := s.prompts[name]
	delete(s.prompts, name)
	s.mu.Unlock()
	if !ok {
		return false
	}
	s.afterRegistryChange(func(cap ServerCapabilities) bool {
		return cap.Prompts != nil && cap.Prompts.ListChanged != nil && *cap.Prompts.ListChanged
	}, s.NotifyPromptsListChanged)
	return true
}

// PromptNames 已注册提示名（有序）。
func (s *Server) PromptNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortedKeys(s.prompts)
}

// afterRegistryChange 能力开关命中时广播（Java addTool/removeTool →
// if(listChanged) notifyToolsListChanged）。
func (s *Server) afterRegistryChange(enabled func(ServerCapabilities) bool, notify func()) {
	if enabled(s.defaultCapabilities()) {
		notify()
	}
}

// ---- 变更通知广播（Java notifyClients）----

// NotifyToolsListChanged 向全部会话广播工具列表变更。
func (s *Server) NotifyToolsListChanged() { s.notifyAll(MethodNotificationToolsListChanged, nil) }

// NotifyResourcesListChanged 向全部会话广播资源列表变更。
func (s *Server) NotifyResourcesListChanged() {
	s.notifyAll(MethodNotificationResourcesListChanged, nil)
}

// NotifyPromptsListChanged 向全部会话广播提示列表变更。
func (s *Server) NotifyPromptsListChanged() { s.notifyAll(MethodNotificationPromptsListChanged, nil) }

// NotifyResourceUpdated 向订阅了 uri 的会话推送资源更新通知（Java
// resourceSubscriptions 路由）。
func (s *Server) NotifyResourceUpdated(uri string) {
	params := ResourcesUpdatedNotification{URI: uri}
	s.mu.RLock()
	subs := s.subscriptions[uri]
	var targets []*serverSession
	for _, sess := range subs {
		targets = append(targets, sess)
	}
	s.mu.RUnlock()
	for _, sess := range targets {
		sess.notifyListening(MethodNotificationResourcesUpdated, params)
	}
}

// NotifyLogging 向全部会话推送日志消息通知（按各会话 setLevel 的级别过滤）。
func (s *Server) NotifyLogging(n LoggingMessageNotification) {
	s.mu.RLock()
	var targets []*serverSession
	for _, sess := range s.sessions {
		if sess.levelAllowed(n.Level) {
			targets = append(targets, sess)
		}
	}
	s.mu.RUnlock()
	for _, sess := range targets {
		sess.notifyListening(MethodNotificationMessage, n)
	}
}

func (s *Server) notifyAll(method string, params any) {
	s.mu.RLock()
	var targets []*serverSession
	for _, sess := range s.sessions {
		targets = append(targets, sess)
	}
	s.mu.RUnlock()
	for _, sess := range targets {
		sess.notifyListening(method, params)
	}
}

// ---- 会话管理 ----

// SessionCount 活跃会话数（测试/监控用）。
func (s *Server) SessionCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sessions)
}

// Close 关闭服务端：全部会话终止（pending 反向请求以 ErrSessionClosed 解除），
// 后续 HTTP 请求 503（Java isClosing）。幂等。
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return nil
	}
	s.closing = true
	var targets []*serverSession
	for _, sess := range s.sessions {
		targets = append(targets, sess)
	}
	s.sessions = map[string]*serverSession{}
	s.subscriptions = map[string]map[string]*serverSession{}
	s.mu.Unlock()
	for _, sess := range targets {
		sess.close()
	}
	return nil
}

// newSession 建会话并登记（initialize 时调用）。
func (s *Server) newSession(caps ClientCapabilities, info Implementation) *serverSession {
	sess := &serverSession{
		id:             newSessionID(),
		server:         s,
		caps:           caps,
		info:           info,
		requestTimeout: s.requestTimeout,
		minLevel:       LogLevelDefault,
		done:           make(chan struct{}),
	}
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return nil
	}
	s.sessions[sess.id] = sess
	s.mu.Unlock()
	return sess
}

func (s *Server) sessionByID(id string) *serverSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[id]
}

func (s *Server) removeSession(sess *serverSession) {
	s.mu.Lock()
	delete(s.sessions, sess.id)
	for uri, subs := range s.subscriptions {
		delete(subs, sess.id)
		if len(subs) == 0 {
			delete(s.subscriptions, uri)
		}
	}
	s.mu.Unlock()
}

// negotiate 服务端版本协商（Java asyncInitializeRequestHandler）：请求版本在
// 支持表内即回显，否则回落最新。
func (s *Server) negotiate(requested string) string {
	for _, v := range s.protocolVersions {
		if v == requested {
			return requested
		}
	}
	log.Warn("mcp: client requested unsupported protocol version: %s, suggesting %s instead",
		requested, Latest(s.protocolVersions))
	return Latest(s.protocolVersions)
}

// ---- CallCtx（对标 McpServerContext + Exchange）----

// CallCtx 工具/资源/提示调用上下文：携带会话 ID、HTTP 请求 ctx、请求 URI
// 与模板变量（资源命中时）、progressToken 与反向采样/征询入口。
type CallCtx struct {
	ctx           context.Context
	sess          *serverSession
	sink          serverSink
	requestURI    string
	vars          map[string]string
	progressToken any
}

// Context 原始请求 ctx（透传给处理器）。
func (c *CallCtx) Context() context.Context { return c.ctx }

// SessionID 会话 ID。
func (c *CallCtx) SessionID() string { return c.sess.id }

// ClientCapabilities 客户端能力（initialize 声明）。
func (c *CallCtx) ClientCapabilities() ClientCapabilities { return c.sess.capabilities() }

// ClientInfo 客户端实现标识。
func (c *CallCtx) ClientInfo() Implementation { return c.sess.implementation() }

// RequestURI 资源读取请求 URI（resources/read 时有效）。
func (c *CallCtx) RequestURI() string { return c.requestURI }

// RequestVars 资源模板变量（Java getUriTemplateVariables；静态资源为空表）。
func (c *CallCtx) RequestVars() map[string]string {
	if c.vars == nil {
		return map[string]string{}
	}
	return c.vars
}

// ProgressToken 本次请求 _meta 携带的进度令牌（无则 nil）。
func (c *CallCtx) ProgressToken() any { return c.progressToken }

// NotifyProgress 发送进度通知（无 progressToken 时静默跳过；帧走本请求回写流）。
func (c *CallCtx) NotifyProgress(progress float64, total float64, message string) error {
	if c.progressToken == nil {
		return nil
	}
	n := ProgressNotification{ProgressToken: c.progressToken, Progress: progress, Message: message}
	if total > 0 {
		t := total
		n.Total = &t
	}
	return c.sess.sendNotification(c.sink, MethodNotificationProgress, n)
}

// Sample 反向采样（Java exchange.createMessage）：客户端须声明 sampling 能力。
func (c *CallCtx) Sample(req *CreateMessageRequest) (*CreateMessageResult, error) {
	if c.sess.capabilities().Sampling == nil {
		return nil, fmt.Errorf("mcp: client must be configured with sampling capabilities")
	}
	var out CreateMessageResult
	if err := c.sess.sendRequest(c.ctx, c.sink, MethodSamplingCreateMessage, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Elicit 反向征询（Java exchange.elicitation）：客户端须声明 elicitation 能力。
func (c *CallCtx) Elicit(req *ElicitRequest) (*ElicitResult, error) {
	if c.sess.capabilities().Elicitation == nil {
		return nil, fmt.Errorf("mcp: client must be configured with elicitation capabilities")
	}
	var out ElicitResult
	if err := c.sess.sendRequest(c.ctx, c.sink, MethodElicitationCreate, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- 请求分发表（Java requestHandlers 表）----

func (s *Server) buildHandlers() map[string]serverHandlerFunc {
	return map[string]serverHandlerFunc{
		MethodInitialize: s.initializeHandler,

		MethodPing: func(*CallCtx, json.RawMessage) (any, *JSONRPCError) {
			return EmptyResult{}, nil
		},

		MethodToolsList: s.toolsListHandler,
		MethodToolsCall: s.toolsCallHandler,

		MethodResourcesList:          s.resourcesListHandler,
		MethodResourcesTemplatesList: s.resourceTemplatesListHandler,
		MethodResourcesRead:          s.resourcesReadHandler,
		MethodResourcesSubscribe:     s.resourcesSubscribeHandler,
		MethodResourcesUnsubscribe:   s.resourcesUnsubscribeHandler,

		MethodPromptsList: s.promptsListHandler,
		MethodPromptsGet:  s.promptsGetHandler,

		MethodLoggingSetLevel: s.loggingSetLevelHandler,
	}
}

func (s *Server) initializeHandler(cc *CallCtx, params json.RawMessage) (any, *JSONRPCError) {
	var req InitializeRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: err.Error()}
	}
	cc.sess.mu.Lock()
	cc.sess.caps = req.Capabilities
	cc.sess.info = req.ClientInfo
	cc.sess.mu.Unlock()
	log.Info("mcp: client initialize request - protocol: %s, info: %s %s",
		req.ProtocolVersion, req.ClientInfo.Name, req.ClientInfo.Version)
	return InitializeResult{
		ProtocolVersion: s.negotiate(req.ProtocolVersion),
		Capabilities:    s.defaultCapabilities(),
		ServerInfo:      s.serverInfo,
		Instructions:    s.instructions,
	}, nil
}

func (s *Server) toolsListHandler(*CallCtx, json.RawMessage) (any, *JSONRPCError) {
	s.mu.RLock()
	names := sortedKeys(s.tools)
	tools := make([]Tool, 0, len(names))
	for _, n := range names {
		tools = append(tools, s.tools[n].Tool)
	}
	s.mu.RUnlock()
	return ListToolsResult{Tools: tools}, nil
}

func (s *Server) toolsCallHandler(cc *CallCtx, params json.RawMessage) (any, *JSONRPCError) {
	var req CallToolRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: err.Error()}
	}
	s.mu.RLock()
	spec, ok := s.tools[req.Name]
	s.mu.RUnlock()
	if !ok {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: "Tool not found: " + req.Name,
		}
	}
	result, err := spec.Handler(cc, req.Arguments)
	return toCallToolResult(result, err, spec.Tool.Meta), nil
}

// toCallToolResult 工具结果转换（Java McpResultResponder.doToolResultResponse）：
// err → isError 文本；nil → 空文本；其余原样。
func toCallToolResult(result *CallToolResult, err error, meta map[string]any) *CallToolResult {
	if err != nil {
		msg := err.Error()
		return &CallToolResult{Content: []Content{NewTextContent(msg)}, IsError: true, Meta: meta}
	}
	if result == nil {
		return &CallToolResult{Content: []Content{NewTextContent("")}, Meta: meta}
	}
	return result
}

func (s *Server) resourcesListHandler(*CallCtx, json.RawMessage) (any, *JSONRPCError) {
	s.mu.RLock()
	names := sortedKeys(s.resources)
	list := make([]Resource, 0, len(names))
	for _, n := range names {
		list = append(list, s.resources[n].Resource)
	}
	s.mu.RUnlock()
	return ListResourcesResult{Resources: list}, nil
}

func (s *Server) resourceTemplatesListHandler(*CallCtx, json.RawMessage) (any, *JSONRPCError) {
	s.mu.RLock()
	names := sortedKeys(s.resourceTemplates)
	list := make([]ResourceTemplate, 0, len(names))
	for _, n := range names {
		list = append(list, s.resourceTemplates[n].Template)
	}
	s.mu.RUnlock()
	return ListResourceTemplatesResult{ResourceTemplates: list}, nil
}

func (s *Server) resourcesReadHandler(cc *CallCtx, params json.RawMessage) (any, *JSONRPCError) {
	var req ReadResourceRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: err.Error()}
	}
	spec := s.findResource(req.URI)
	if spec == nil {
		return nil, &JSONRPCError{
			Code:    CodeResourceNotFound,
			Message: "Resource not found",
			Data:    map[string]string{"uri": req.URI},
		}
	}
	cc.requestURI = req.URI
	if spec.IsTemplate {
		if vars, ok := matchURITemplate(spec.Template.URITemplate, req.URI); ok {
			cc.vars = vars
		}
	}
	result, err := spec.Handler(cc)
	if err != nil {
		return nil, &JSONRPCError{Code: CodeInternalError, Message: err.Error()}
	}
	return toReadResourceResult(result, req.URI, spec.mimeType()), nil
}

// findResource 静态资源（全等）优先，再模板匹配（Java findResourceSpecification
// → findResourceTemplateSpecification；静态侧 Java 亦走模板匹配，全等即其特例）。
func (s *Server) findResource(uri string) *ResourceSpec {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, k := range sortedKeys(s.resources) {
		if k == uri {
			return s.resources[k]
		}
	}
	for _, k := range sortedKeys(s.resourceTemplates) {
		if _, ok := matchURITemplate(k, uri); ok {
			return s.resourceTemplates[k]
		}
	}
	return nil
}

// toReadResourceResult 资源结果转换（Java McpResultResponder.
// doResourceResultResponse：ReadResourceResult / ResourceContents / 文本 / blob）。
func toReadResourceResult(result any, uri, mime string) *ReadResourceResult {
	switch v := result.(type) {
	case *ReadResourceResult:
		return v
	case ReadResourceResult:
		return &v
	case []ResourceContents:
		return &ReadResourceResult{Contents: v}
	case ResourceContents:
		return &ReadResourceResult{Contents: []ResourceContents{v}}
	case string:
		return &ReadResourceResult{Contents: []ResourceContents{
			&TextResourceContents{URI: uri, MimeType: mime, Text: v},
		}}
	case []byte:
		return &ReadResourceResult{Contents: []ResourceContents{
			&BlobResourceContents{URI: uri, MimeType: mime, Blob: base64.StdEncoding.EncodeToString(v)},
		}}
	default:
		return &ReadResourceResult{Contents: []ResourceContents{
			&TextResourceContents{URI: uri, MimeType: mime, Text: fmt.Sprintf("%v", result)},
		}}
	}
}

func (s *Server) resourcesSubscribeHandler(cc *CallCtx, params json.RawMessage) (any, *JSONRPCError) {
	if sc := s.defaultCapabilities(); sc.Resources == nil || sc.Resources.Subscribe == nil || !*sc.Resources.Subscribe {
		return nil, &JSONRPCError{Code: CodeInvalidRequest, Message: "Resources subscription is not enabled"}
	}
	var req SubscribeRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: err.Error()}
	}
	s.mu.Lock()
	subs := s.subscriptions[req.URI]
	if subs == nil {
		subs = map[string]*serverSession{}
		s.subscriptions[req.URI] = subs
	}
	subs[cc.sess.id] = cc.sess
	s.mu.Unlock()
	return EmptyResult{}, nil
}

func (s *Server) resourcesUnsubscribeHandler(cc *CallCtx, params json.RawMessage) (any, *JSONRPCError) {
	var req UnsubscribeRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: err.Error()}
	}
	s.mu.Lock()
	if subs := s.subscriptions[req.URI]; subs != nil {
		delete(subs, cc.sess.id)
		if len(subs) == 0 {
			delete(s.subscriptions, req.URI)
		}
	}
	s.mu.Unlock()
	return EmptyResult{}, nil
}

func (s *Server) promptsListHandler(*CallCtx, json.RawMessage) (any, *JSONRPCError) {
	s.mu.RLock()
	names := sortedKeys(s.prompts)
	list := make([]Prompt, 0, len(names))
	for _, n := range names {
		list = append(list, s.prompts[n].Prompt)
	}
	s.mu.RUnlock()
	return ListPromptsResult{Prompts: list}, nil
}

func (s *Server) promptsGetHandler(cc *CallCtx, params json.RawMessage) (any, *JSONRPCError) {
	var req GetPromptRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: err.Error()}
	}
	s.mu.RLock()
	spec, ok := s.prompts[req.Name]
	s.mu.RUnlock()
	if !ok {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: "Invalid prompt name",
			Data:    "Prompt not found: " + req.Name,
		}
	}
	result, err := spec.Handler(cc, req.Arguments)
	if err != nil {
		return nil, &JSONRPCError{Code: CodeInternalError, Message: err.Error()}
	}
	return toGetPromptResult(result, spec.Prompt.Description), nil
}

// toGetPromptResult 提示结果转换（Java McpResultResponder.doPromptResultResponse
// 的 Go 子集：GetPromptResult / PromptMessage / 列表 / 字符串）。
func toGetPromptResult(result any, description string) *GetPromptResult {
	switch v := result.(type) {
	case *GetPromptResult:
		return v
	case GetPromptResult:
		return &v
	case PromptMessage:
		return &GetPromptResult{Description: description, Messages: []PromptMessage{v}}
	case []PromptMessage:
		return &GetPromptResult{Description: description, Messages: v}
	case string:
		return &GetPromptResult{Description: description, Messages: []PromptMessage{
			{Role: RoleUser, Content: NewTextContent(v)},
		}}
	default:
		return &GetPromptResult{Description: description, Messages: []PromptMessage{
			{Role: RoleUser, Content: NewTextContent(fmt.Sprintf("%v", result))},
		}}
	}
}

func (s *Server) loggingSetLevelHandler(cc *CallCtx, params json.RawMessage) (any, *JSONRPCError) {
	var req SetLevelRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: err.Error()}
	}
	cc.sess.mu.Lock()
	cc.sess.minLevel = req.Level
	cc.sess.mu.Unlock()
	return EmptyResult{}, nil
}

// ---- RegisterTools 反射批量注册（@ToolMapping 扫描对应物）----

// RegisterTools 反射扫描 v（结构体指针）的导出方法注册为工具。方法签名二选一：
//
//	func(ctx *mcp.CallCtx, args map[string]any) (any, error)
//	func(ctx *mcp.CallCtx, args *T) (any, error)   // 参数结构体：args JSON 反序列化注入
//
// 工具名 = 方法名；结果 *CallToolResult 原样，其余值 JSON 文本化（isError 仅
// err 非空时）。schema 与描述用 Tool()/ToolSpec 链式补充（Go 无注解）。
func RegisterTools(s *Server, v any) error {
	rv := reflect.ValueOf(v)
	rt := rv.Type()
	if rt.Kind() != reflect.Pointer || rt.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("mcp: RegisterTools expects a pointer to a struct, got %T", v)
	}
	for i := 0; i < rt.NumMethod(); i++ {
		m := rt.Method(i)
		if m.PkgPath != "" { // 未导出方法
			continue
		}
		if !toolMethodShape(m.Type) {
			continue
		}
		s.Tool(m.Name, "", adaptToolMethod(rv.Method(i), m.Type.In(2)))
	}
	return nil
}

// toolMethodShape 判定 *T 方法形态：receiver + (*CallCtx, args) 两实参 + 单
// 返回值 + error（reflect.Type.Method 的 Type 含 receiver 作首参）。
func toolMethodShape(t reflect.Type) bool {
	if t.NumIn() != 3 || t.NumOut() != 2 {
		return false
	}
	if t.In(1) != reflect.TypeOf((*CallCtx)(nil)) {
		return false
	}
	argsType := t.In(2)
	if argsType != reflect.TypeOf(map[string]any(nil)) &&
		!(argsType.Kind() == reflect.Pointer && argsType.Elem().Kind() == reflect.Struct) {
		return false
	}
	return t.Out(1) == reflect.TypeOf((*error)(nil)).Elem()
}

func adaptToolMethod(mv reflect.Value, argsType reflect.Type) ToolFunc {
	mapArgs := argsType == reflect.TypeOf(map[string]any(nil))
	return func(cc *CallCtx, args map[string]any) (*CallToolResult, error) {
		var in []reflect.Value
		if mapArgs {
			in = append(in, reflect.ValueOf(cc), reflect.ValueOf(args))
		} else {
			ptr := reflect.New(argsType.Elem())
			if len(args) > 0 {
				if b, err := json.Marshal(args); err == nil {
					_ = json.Unmarshal(b, ptr.Interface())
				}
			}
			in = append(in, reflect.ValueOf(cc), ptr)
		}
		out := mv.Call(in)
		if err, _ := out[1].Interface().(error); err != nil {
			return nil, err
		}
		val := out[0].Interface()
		if result, ok := val.(*CallToolResult); ok {
			return result, nil
		}
		if val == nil {
			return &CallToolResult{}, nil
		}
		if str, ok := val.(string); ok {
			return &CallToolResult{Content: []Content{NewTextContent(str)}}, nil
		}
		b, err := json.Marshal(val)
		if err != nil {
			return nil, err
		}
		return &CallToolResult{Content: []Content{NewTextContent(string(b))}}, nil
	}
}
