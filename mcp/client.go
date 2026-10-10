// client.go 客户端门面（对标 McpAsyncClient + LifecycleInitializer + solon
// McpClientProvider 的心跳重连）：懒初始化握手（版本协商 + initialized 通知）、
// 全量协议操作（分页自动翻页）、变更通知消费者、ping 心跳与指数退避重连。
// 每次初始化经传输工厂新建传输 + 会话（stdio = 重新拉起进程；HTTP 传输在
// P3-c 落地）。
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/crazy-airhead/aifei-go/log"
)

// ErrClientClosed 客户端已 Close（Java IllegalStateException("The current status has been closed.")）。
var ErrClientClosed = fmt.Errorf("mcp: client closed")

// 心跳退避上限（Java McpClientProvider：10 分钟）。
const heartbeatMaxBackoff = 10 * time.Minute

// TransportFactory 每次初始化新建传输（Java LifecycleInitializer.sessionSupplier
// 的传输侧 + McpClientProvider.buildClient 的传输重建合流）。
type TransportFactory func(ctx context.Context) (Transport, error)

// ClientOption 客户端选项。
type ClientOption func(*Client)

// WithClientInfo 客户端实现标识（默认 "aifei-go mcp client"/"1.0.0"）。
func WithClientInfo(info Implementation) ClientOption {
	return func(c *Client) { c.clientInfo = info }
}

// WithClientCapabilities 声明客户端能力（roots/sampling/elicitation；声明
// sampling/elicitation 须同时提供对应处理器）。
func WithClientCapabilities(cap ClientCapabilities, sampling SamplingHandler, elicitation ElicitationHandler) ClientOption {
	return func(c *Client) {
		c.clientCapabilities = cap
		c.samplingHandler = sampling
		c.elicitationHandler = elicitation
	}
}

// WithRoots 声明 roots 能力与初始根目录（roots/list 反向请求处理器由此挂上）。
func WithRoots(listChanged bool, roots ...Root) ClientOption {
	return func(c *Client) {
		c.clientCapabilities.Roots = &RootCapabilities{ListChanged: &listChanged}
		for _, r := range roots {
			c.roots[r.URI] = r
		}
	}
}

// WithRequestTimeout 单请求超时（默认 20s，Java McpClient.AsyncSpec 同款）。
func WithRequestTimeout(d time.Duration) ClientOption {
	return func(c *Client) { c.requestTimeout = d }
}

// WithInitializationTimeout 初始化握手超时（默认 20s）。
func WithInitializationTimeout(d time.Duration) ClientOption {
	return func(c *Client) { c.initTimeout = d }
}

// WithProtocolVersions 客户端支持的协议版本表（升序，末位发起握手；默认全量表）。
func WithProtocolVersions(versions []string) ClientOption {
	return func(c *Client) { c.protocolVersions = versions }
}

// WithHeartbeat 心跳：按 interval ping；失败 → Reset + 延迟翻倍（上限 10min）；
// 成功 → 延迟复位。每次心跳都会先懒初始化——即心跳驱动重连（Java 心跳只探测、
// 重连依赖下次用户调用，Go 侧有意加强）。
func WithHeartbeat(interval time.Duration) ClientOption {
	return func(c *Client) { c.heartbeatInterval = interval }
}

// WithToolsChangeConsumer 工具列表变更消费者（收到通知后自动重拉全量再回调）。
func WithToolsChangeConsumer(fn func([]Tool)) ClientOption {
	return func(c *Client) { c.AddToolsChangeConsumer(fn) }
}

// WithResourcesChangeConsumer 资源列表变更消费者。
func WithResourcesChangeConsumer(fn func([]Resource)) ClientOption {
	return func(c *Client) { c.AddResourcesChangeConsumer(fn) }
}

// WithResourcesUpdateConsumer 资源更新消费者（收到通知后自动重读该资源）。
func WithResourcesUpdateConsumer(fn func([]ResourceContents)) ClientOption {
	return func(c *Client) { c.AddResourcesUpdateConsumer(fn) }
}

// WithPromptsChangeConsumer 提示列表变更消费者。
func WithPromptsChangeConsumer(fn func([]Prompt)) ClientOption {
	return func(c *Client) { c.AddPromptsChangeConsumer(fn) }
}

// WithLoggingConsumer 日志消息通知消费者。
func WithLoggingConsumer(fn func(LoggingMessageNotification)) ClientOption {
	return func(c *Client) { c.AddLoggingConsumer(fn) }
}

// WithProgressConsumer 进度通知消费者。
func WithProgressConsumer(fn func(ProgressNotification)) ClientOption {
	return func(c *Client) { c.AddProgressConsumer(fn) }
}

// SamplingHandler 服务端反向采样处理器。
type SamplingHandler func(ctx context.Context, req *CreateMessageRequest) (*CreateMessageResult, *JSONRPCError)

// ElicitationHandler 服务端反向征询处理器。
type ElicitationHandler func(ctx context.Context, req *ElicitRequest) (*ElicitResult, *JSONRPCError)

// Client MCP 客户端门面。
type Client struct {
	transportFactory TransportFactory

	mu         sync.Mutex
	session    *Session
	initResult *InitializeResult
	closed     bool
	heartbeat  *heartbeatState

	clientInfo         Implementation
	clientCapabilities ClientCapabilities
	samplingHandler    SamplingHandler
	elicitationHandler ElicitationHandler
	protocolVersions   []string
	requestTimeout     time.Duration
	initTimeout        time.Duration
	heartbeatInterval  time.Duration

	roots map[string]Root

	consumerMu               sync.Mutex
	toolsChangeConsumers     []func([]Tool)
	resourcesChangeConsumers []func([]Resource)
	resourcesUpdateConsumers []func([]ResourceContents)
	promptsChangeConsumers   []func([]Prompt)
	loggingConsumers         []func(LoggingMessageNotification)
	progressConsumers        []func(ProgressNotification)
}

type heartbeatState struct {
	stop chan struct{}
	once sync.Once
}

// NewClient 建客户端。factory 每次初始化被调用（首连与每次 Reset 重连）。
func NewClient(factory TransportFactory, opts ...ClientOption) *Client {
	c := &Client{
		transportFactory: factory,
		clientInfo:       Implementation{Name: "aifei-go mcp client", Version: "1.0.0"},
		protocolVersions: SupportedVersions,
		requestTimeout:   20 * time.Second,
		initTimeout:      20 * time.Second,
		roots:            map[string]Root{},
	}
	for _, o := range opts {
		o(c)
	}
	if c.clientCapabilities.Sampling != nil && c.samplingHandler == nil {
		panic("mcp: sampling handler must not be nil when client capabilities include sampling")
	}
	if c.clientCapabilities.Elicitation != nil && c.elicitationHandler == nil {
		panic("mcp: elicitation handler must not be nil when client capabilities include elicitation")
	}
	c.startHeartbeatIfNeeded()
	return c
}

// DialStdio stdio 通道便捷建连（懒连接：进程在首次 Initialize/操作时拉起；
// 重连时重新拉起进程）。命令不可执行等错误在首次操作时上抛。
func DialStdio(name string, args ...string) (*Client, error) {
	t := NewStdioTransport(name, args...)
	return NewClient(t.TransportFactory()), nil
}

// ---- 状态 ----

// Initialize 显式初始化（幂等；已初始化直接返回缓存结果）。也可省略——任何
// 操作都会懒初始化（Java withInitialization 语义）。
func (c *Client) Initialize(ctx context.Context) (*InitializeResult, error) {
	_, init, err := c.ensureInitialized(ctx)
	if err != nil {
		return nil, err
	}
	return init, nil
}

// IsInitialized 是否已完成初始化握手。
func (c *Client) IsInitialized() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.initResult != nil && c.session != nil
}

// ServerCapabilities 服务端能力（未初始化为零值）。
func (c *Client) ServerCapabilities() ServerCapabilities {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.initResult == nil {
		return ServerCapabilities{}
	}
	return c.initResult.Capabilities
}

// ServerInfo 服务端实现标识。
func (c *Client) ServerInfo() Implementation {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.initResult == nil {
		return Implementation{}
	}
	return c.initResult.ServerInfo
}

// ServerInstructions 服务端使用指引。
func (c *Client) ServerInstructions() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.initResult == nil {
		return ""
	}
	return c.initResult.Instructions
}

// NegotiatedVersion 协商定版版本（未初始化为空串）。
func (c *Client) NegotiatedVersion() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.initResult == nil {
		return ""
	}
	return c.initResult.ProtocolVersion
}

// Reset 重置连接：关当前会话，下次操作（或心跳）重新初始化（Java
// McpClientProvider.reset）。
func (c *Client) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.teardownLocked()
}

// Close 关闭客户端（停心跳 + 关会话；幂等）。
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	c.teardownLocked()
	if c.heartbeat != nil {
		c.heartbeat.once.Do(func() { close(c.heartbeat.stop) })
	}
	return nil
}

func (c *Client) teardownLocked() {
	if c.session != nil {
		_ = c.session.Close()
	}
	c.session = nil
	c.initResult = nil
}

// ensureInitialized 懒初始化（Java withInitialization：首个调用者执行握手，
// 并发调用者等锁加入——对应 "Joining previous initialization"）。传输死亡
// （会话 Done）时下次操作自愈重建。
func (c *Client) ensureInitialized(ctx context.Context) (*Session, *InitializeResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, nil, ErrClientClosed
	}
	if c.session != nil && c.initResult != nil {
		select {
		case <-c.session.Done(): // 传输已终止：自愈重建
			c.teardownLocked()
		default:
			return c.session, c.initResult, nil
		}
	}

	t, err := c.transportFactory(ctx)
	if err != nil {
		return nil, nil, err
	}
	s := NewSession(t, c.requestHandlers(), c.notificationHandlers())

	ictx, cancel := context.WithTimeout(ctx, c.initTimeout)
	defer cancel()
	var result InitializeResult
	req := InitializeRequest{
		ProtocolVersion: Latest(c.protocolVersions),
		Capabilities:    c.clientCapabilities,
		ClientInfo:      c.clientInfo,
	}
	if err := s.SendRequest(ictx, MethodInitialize, req, &result); err != nil {
		_ = s.Close()
		return nil, nil, err
	}
	if _, err := Negotiate(c.protocolVersions, result.ProtocolVersion); err != nil {
		_ = s.Close()
		return nil, nil, err
	}
	if err := s.SendNotification(MethodNotificationInitialized, nil); err != nil {
		_ = s.Close()
		return nil, nil, err
	}
	c.session, c.initResult = s, &result
	log.Info("mcp: initialized — server %s %s, protocol %s", result.ServerInfo.Name, result.ServerInfo.Version, result.ProtocolVersion)
	return c.session, c.initResult, nil
}

// withTimeout 请求级超时：调用方 ctx 无 deadline 时套 requestTimeout。
func (c *Client) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, c.requestTimeout)
}

// ---- 基础 ----

// Ping 发送 ping。
func (c *Client) Ping(ctx context.Context) error {
	s, _, err := c.ensureInitialized(ctx)
	if err != nil {
		return err
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	return s.SendRequest(ctx, MethodPing, nil, nil)
}

// ---- 工具 ----

// ListTools 单页工具列表。
func (c *Client) ListTools(ctx context.Context, cursor string) (*ListToolsResult, error) {
	s, init, err := c.ensureInitialized(ctx)
	if err != nil {
		return nil, err
	}
	if init.Capabilities.Tools == nil {
		return nil, fmt.Errorf("mcp: server does not provide tools capability")
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	var out ListToolsResult
	if err := s.SendRequest(ctx, MethodToolsList, PaginatedRequest{Cursor: cursor}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListAllTools 全量工具列表（跟随 nextCursor 翻页；Java expand/reduce 对应物）。
func (c *Client) ListAllTools(ctx context.Context) ([]Tool, error) {
	var all []Tool
	cursor := ""
	for {
		page, err := c.ListTools(ctx, cursor)
		if err != nil {
			return nil, err
		}
		all = append(all, page.Tools...)
		if page.NextCursor == "" {
			return all, nil
		}
		cursor = page.NextCursor
	}
}

// CallTool 调用工具。
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*CallToolResult, error) {
	s, init, err := c.ensureInitialized(ctx)
	if err != nil {
		return nil, err
	}
	if init.Capabilities.Tools == nil {
		return nil, fmt.Errorf("mcp: server does not provide tools capability")
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	var out CallToolResult
	req := CallToolRequest{Name: name, Arguments: args}
	if err := s.SendRequest(ctx, MethodToolsCall, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- 资源 ----

// ListResources 单页资源列表。
func (c *Client) ListResources(ctx context.Context, cursor string) (*ListResourcesResult, error) {
	s, init, err := c.ensureInitialized(ctx)
	if err != nil {
		return nil, err
	}
	if init.Capabilities.Resources == nil {
		return nil, fmt.Errorf("mcp: server does not provide the resources capability")
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	var out ListResourcesResult
	if err := s.SendRequest(ctx, MethodResourcesList, PaginatedRequest{Cursor: cursor}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListAllResources 全量资源列表（自动翻页）。
func (c *Client) ListAllResources(ctx context.Context) ([]Resource, error) {
	var all []Resource
	cursor := ""
	for {
		page, err := c.ListResources(ctx, cursor)
		if err != nil {
			return nil, err
		}
		all = append(all, page.Resources...)
		if page.NextCursor == "" {
			return all, nil
		}
		cursor = page.NextCursor
	}
}

// ListResourceTemplates 单页资源模板列表。
func (c *Client) ListResourceTemplates(ctx context.Context, cursor string) (*ListResourceTemplatesResult, error) {
	s, init, err := c.ensureInitialized(ctx)
	if err != nil {
		return nil, err
	}
	if init.Capabilities.Resources == nil {
		return nil, fmt.Errorf("mcp: server does not provide the resources capability")
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	var out ListResourceTemplatesResult
	if err := s.SendRequest(ctx, MethodResourcesTemplatesList, PaginatedRequest{Cursor: cursor}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ReadResource 读取资源。
func (c *Client) ReadResource(ctx context.Context, uri string) (*ReadResourceResult, error) {
	s, init, err := c.ensureInitialized(ctx)
	if err != nil {
		return nil, err
	}
	if init.Capabilities.Resources == nil {
		return nil, fmt.Errorf("mcp: server does not provide the resources capability")
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	var out ReadResourceResult
	if err := s.SendRequest(ctx, MethodResourcesRead, ReadResourceRequest{URI: uri}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SubscribeResource 订阅资源变更。
func (c *Client) SubscribeResource(ctx context.Context, uri string) error {
	s, init, err := c.ensureInitialized(ctx)
	if err != nil {
		return err
	}
	if init.Capabilities.Resources == nil {
		return fmt.Errorf("mcp: server does not provide the resources capability")
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	return s.SendRequest(ctx, MethodResourcesSubscribe, SubscribeRequest{URI: uri}, nil)
}

// UnsubscribeResource 退订资源变更。
func (c *Client) UnsubscribeResource(ctx context.Context, uri string) error {
	s, _, err := c.ensureInitialized(ctx)
	if err != nil {
		return err
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	return s.SendRequest(ctx, MethodResourcesUnsubscribe, UnsubscribeRequest{URI: uri}, nil)
}

// ---- 提示 ----

// ListPrompts 单页提示列表。
func (c *Client) ListPrompts(ctx context.Context, cursor string) (*ListPromptsResult, error) {
	s, _, err := c.ensureInitialized(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	var out ListPromptsResult
	if err := s.SendRequest(ctx, MethodPromptsList, PaginatedRequest{Cursor: cursor}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListAllPrompts 全量提示列表（自动翻页）。
func (c *Client) ListAllPrompts(ctx context.Context) ([]Prompt, error) {
	var all []Prompt
	cursor := ""
	for {
		page, err := c.ListPrompts(ctx, cursor)
		if err != nil {
			return nil, err
		}
		all = append(all, page.Prompts...)
		if page.NextCursor == "" {
			return all, nil
		}
		cursor = page.NextCursor
	}
}

// GetPrompt 获取提示内容。
func (c *Client) GetPrompt(ctx context.Context, name string, args map[string]string) (*GetPromptResult, error) {
	s, _, err := c.ensureInitialized(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	var out GetPromptResult
	if err := s.SendRequest(ctx, MethodPromptsGet, GetPromptRequest{Name: name, Arguments: args}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- 日志与补全 ----

// SetLoggingLevel 设置服务端日志级别。
func (c *Client) SetLoggingLevel(ctx context.Context, level LoggingLevel) error {
	s, init, err := c.ensureInitialized(ctx)
	if err != nil {
		return err
	}
	if init.Capabilities.Logging == nil {
		return fmt.Errorf("mcp: server's logging capabilities are not enabled")
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	return s.SendRequest(ctx, MethodLoggingSetLevel, SetLevelRequest{Level: level}, nil)
}

// CompleteCompletion 补全请求。
func (c *Client) CompleteCompletion(ctx context.Context, req *CompleteRequest) (*CompleteResult, error) {
	s, _, err := c.ensureInitialized(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	var out CompleteResult
	if err := s.SendRequest(ctx, MethodCompletionComplete, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- Roots ----

// AddRoot 动态添加根目录（listChanged 能力开启且已初始化时自动发通知）。
func (c *Client) AddRoot(ctx context.Context, root Root) error {
	if c.clientCapabilities.Roots == nil {
		return fmt.Errorf("mcp: client must be configured with roots capabilities")
	}
	c.mu.Lock()
	if _, exists := c.roots[root.URI]; exists {
		c.mu.Unlock()
		return fmt.Errorf("mcp: root with uri %q already exists", root.URI)
	}
	c.roots[root.URI] = root
	initialized := c.initResult != nil
	c.mu.Unlock()

	if c.rootsListChanged() != nil && initialized {
		return c.RootsListChangedNotification(ctx)
	}
	return nil
}

// RemoveRoot 动态移除根目录。
func (c *Client) RemoveRoot(ctx context.Context, uri string) error {
	if c.clientCapabilities.Roots == nil {
		return fmt.Errorf("mcp: client must be configured with roots capabilities")
	}
	c.mu.Lock()
	_, exists := c.roots[uri]
	delete(c.roots, uri)
	initialized := c.initResult != nil
	c.mu.Unlock()
	if !exists {
		return fmt.Errorf("mcp: root with uri %q not found", uri)
	}
	if c.rootsListChanged() != nil && initialized {
		return c.RootsListChangedNotification(ctx)
	}
	return nil
}

// RootsListChangedNotification 手动发送 roots/list_changed 通知。
func (c *Client) RootsListChangedNotification(ctx context.Context) error {
	s, _, err := c.ensureInitialized(ctx)
	if err != nil {
		return err
	}
	return s.SendNotification(MethodNotificationRootsListChanged, nil)
}

func (c *Client) rootsListChanged() *bool {
	if c.clientCapabilities.Roots == nil {
		return nil
	}
	return c.clientCapabilities.Roots.ListChanged
}

// ---- 心跳 ----

func (c *Client) startHeartbeatIfNeeded() {
	if c.heartbeatInterval <= 0 {
		return
	}
	c.heartbeat = &heartbeatState{stop: make(chan struct{})}
	go c.heartbeatLoop()
}

func (c *Client) heartbeatLoop() {
	delay := c.heartbeatInterval
	for {
		select {
		case <-c.heartbeat.stop:
			return
		case <-time.After(delay):
		}
		ctx, cancel := context.WithTimeout(context.Background(), c.requestTimeout)
		err := c.Ping(ctx)
		cancel()
		if err != nil {
			log.Warn("mcp: heartbeat failed, resetting client: %v", err)
			c.Reset()
			delay = min(delay*2, heartbeatMaxBackoff)
		} else {
			delay = c.heartbeatInterval
		}
	}
}

// ---- 入站处理器表 ----

// requestHandlers 反向请求处理器（每次初始化的会话共用一份；闭包动态解析
// 当前会话/状态）。ping 恒回空对象（规范：非 null 空结果）。
func (c *Client) requestHandlers() map[string]RequestHandlerFunc {
	h := map[string]RequestHandlerFunc{
		MethodPing: func(_ context.Context, _ json.RawMessage) (any, *JSONRPCError) {
			return map[string]any{}, nil
		},
	}
	if c.clientCapabilities.Roots != nil {
		h[MethodRootsList] = func(_ context.Context, _ json.RawMessage) (any, *JSONRPCError) {
			c.mu.Lock()
			roots := make([]Root, 0, len(c.roots))
			for _, r := range c.roots {
				roots = append(roots, r)
			}
			c.mu.Unlock()
			return ListRootsResult{Roots: roots}, nil
		}
	}
	if c.clientCapabilities.Sampling != nil {
		h[MethodSamplingCreateMessage] = func(ctx context.Context, params json.RawMessage) (any, *JSONRPCError) {
			var req CreateMessageRequest
			if err := json.Unmarshal(params, &req); err != nil {
				return nil, &JSONRPCError{Code: CodeInvalidParams, Message: err.Error()}
			}
			return c.samplingHandler(ctx, &req)
		}
	}
	if c.clientCapabilities.Elicitation != nil {
		h[MethodElicitationCreate] = func(ctx context.Context, params json.RawMessage) (any, *JSONRPCError) {
			var req ElicitRequest
			if err := json.Unmarshal(params, &req); err != nil {
				return nil, &JSONRPCError{Code: CodeInvalidParams, Message: err.Error()}
			}
			return c.elicitationHandler(ctx, &req)
		}
	}
	return h
}

// notificationHandlers 通知处理器（变更类通知自动重拉再回调消费者——Java
// asyncToolsChangeNotificationHandler 等）。
func (c *Client) notificationHandlers() map[string]NotificationHandlerFunc {
	return map[string]NotificationHandlerFunc{
		MethodNotificationToolsListChanged: func(_ json.RawMessage) error {
			tools, err := c.ListAllTools(context.Background())
			if err != nil {
				log.Error("mcp: error handling tools list change notification: %v", err)
				return nil
			}
			c.eachToolsChangeConsumer(func(fn func([]Tool)) { fn(tools) })
			return nil
		},
		MethodNotificationResourcesListChanged: func(_ json.RawMessage) error {
			resources, err := c.ListAllResources(context.Background())
			if err != nil {
				log.Error("mcp: error handling resources list change notification: %v", err)
				return nil
			}
			c.consumerMu.Lock()
			consumers := append([]func([]Resource){}, c.resourcesChangeConsumers...)
			c.consumerMu.Unlock()
			for _, fn := range consumers {
				fn(resources)
			}
			return nil
		},
		MethodNotificationResourcesUpdated: func(params json.RawMessage) error {
			var n ResourcesUpdatedNotification
			if err := json.Unmarshal(params, &n); err != nil {
				log.Error("mcp: bad resources/updated notification: %v", err)
				return nil
			}
			result, err := c.ReadResource(context.Background(), n.URI)
			if err != nil {
				log.Error("mcp: error handling resource update notification: %v", err)
				return nil
			}
			c.consumerMu.Lock()
			consumers := append([]func([]ResourceContents){}, c.resourcesUpdateConsumers...)
			c.consumerMu.Unlock()
			for _, fn := range consumers {
				fn(result.Contents)
			}
			return nil
		},
		MethodNotificationPromptsListChanged: func(_ json.RawMessage) error {
			prompts, err := c.ListAllPrompts(context.Background())
			if err != nil {
				log.Error("mcp: error handling prompts list change notification: %v", err)
				return nil
			}
			c.consumerMu.Lock()
			consumers := append([]func([]Prompt){}, c.promptsChangeConsumers...)
			c.consumerMu.Unlock()
			for _, fn := range consumers {
				fn(prompts)
			}
			return nil
		},
		MethodNotificationMessage: func(params json.RawMessage) error {
			var n LoggingMessageNotification
			if err := json.Unmarshal(params, &n); err != nil {
				return nil
			}
			c.consumerMu.Lock()
			consumers := append([]func(LoggingMessageNotification){}, c.loggingConsumers...)
			c.consumerMu.Unlock()
			for _, fn := range consumers {
				fn(n)
			}
			return nil
		},
		MethodNotificationProgress: func(params json.RawMessage) error {
			var n ProgressNotification
			if err := json.Unmarshal(params, &n); err != nil {
				return nil
			}
			c.consumerMu.Lock()
			consumers := append([]func(ProgressNotification){}, c.progressConsumers...)
			c.consumerMu.Unlock()
			for _, fn := range consumers {
				fn(n)
			}
			return nil
		},
	}
}

func (c *Client) eachToolsChangeConsumer(apply func(fn func([]Tool))) {
	c.consumerMu.Lock()
	consumers := append([]func([]Tool){}, c.toolsChangeConsumers...)
	c.consumerMu.Unlock()
	for _, fn := range consumers {
		apply(fn)
	}
}

// ---- 消费者注册（可多次追加；options 即薄包装）----

// AddToolsChangeConsumer 追加工具变更消费者。
func (c *Client) AddToolsChangeConsumer(fn func([]Tool)) {
	c.consumerMu.Lock()
	defer c.consumerMu.Unlock()
	c.toolsChangeConsumers = append(c.toolsChangeConsumers, fn)
}

// AddResourcesChangeConsumer 追加资源列表变更消费者。
func (c *Client) AddResourcesChangeConsumer(fn func([]Resource)) {
	c.consumerMu.Lock()
	defer c.consumerMu.Unlock()
	c.resourcesChangeConsumers = append(c.resourcesChangeConsumers, fn)
}

// AddResourcesUpdateConsumer 追加资源更新消费者。
func (c *Client) AddResourcesUpdateConsumer(fn func([]ResourceContents)) {
	c.consumerMu.Lock()
	defer c.consumerMu.Unlock()
	c.resourcesUpdateConsumers = append(c.resourcesUpdateConsumers, fn)
}

// AddPromptsChangeConsumer 追加提示变更消费者。
func (c *Client) AddPromptsChangeConsumer(fn func([]Prompt)) {
	c.consumerMu.Lock()
	defer c.consumerMu.Unlock()
	c.promptsChangeConsumers = append(c.promptsChangeConsumers, fn)
}

// AddLoggingConsumer 追加日志通知消费者。
func (c *Client) AddLoggingConsumer(fn func(LoggingMessageNotification)) {
	c.consumerMu.Lock()
	defer c.consumerMu.Unlock()
	c.loggingConsumers = append(c.loggingConsumers, fn)
}

// AddProgressConsumer 追加进度通知消费者。
func (c *Client) AddProgressConsumer(fn func(ProgressNotification)) {
	c.consumerMu.Lock()
	defer c.consumerMu.Unlock()
	c.progressConsumers = append(c.progressConsumers, fn)
}
