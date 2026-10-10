// session.go 会话层（对标 McpClientSession）：pending-reply 表 + 消息泵 +
// 服务端请求/通知分发。Mono pendingSink → chan *JSONRPCResponse；超时 = 调用方
// ctx（Java requestTimeout 的 ctx 等价物，契约 §4）。
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/crazy-airhead/aifei-go/log"
)

// ErrSessionClosed 会话已关闭（Close 或传输终止时所有 pending 请求以此终止；
// Java RuntimeException("MCP session with server terminated") 对应物）。
var ErrSessionClosed = fmt.Errorf("mcp: session closed")

// TransportError 传输层错误（发送失败；Java McpTransportException 对应物）。
// 桥层（ai/mcp executeWithRetry）以此为据重连——与超时、会话终止共同构成
// IsTransportError 的三类判定。
type TransportError struct {
	Err error
}

func (e *TransportError) Error() string { return "mcp: transport error: " + e.Err.Error() }
func (e *TransportError) Unwrap() error { return e.Err }

// IsTransportError 传输层错误判定（Java McpClientProvider.isTransportError：
// McpTransportException | TimeoutException | 连接类错误）：发送失败、会话终止
// 或请求超时 → 需要重连重试。
func IsTransportError(err error) bool {
	if err == nil {
		return false
	}
	var te *TransportError
	return errors.As(err, &te) ||
		errors.Is(err, ErrSessionClosed) ||
		errors.Is(err, context.DeadlineExceeded)
}

// Transport 帧传输缝（stdio/HTTP 传输在 P3-b/c 实现本接口）：Send 发出帧、
// Receive 返回入站帧通道（消息泵消费，通道关闭即传输终止）。
type Transport interface {
	Send(ctx context.Context, msg Message) error
	Receive() <-chan Message
	Close() error
}

// RequestHandlerFunc 入站请求处理器（Java RequestHandler<Mono<T>> 的同步形态）。
type RequestHandlerFunc func(ctx context.Context, params json.RawMessage) (any, *JSONRPCError)

// NotificationHandlerFunc 入站通知处理器。
type NotificationHandlerFunc func(params json.RawMessage) error

// Session 双向 JSON-RPC 会话：客户端与服务端共用（服务端只是 handler 表更满）。
type Session struct {
	transport Transport
	pending   sync.Map // int64 → chan *JSONRPCResponse（容量 1）
	nextID    atomic.Int64

	requestHandlers      map[string]RequestHandlerFunc
	notificationHandlers map[string]NotificationHandlerFunc

	closed    chan struct{}
	closeOnce sync.Once
	closeErr  error
}

// NewSession 建会话并启动消息泵（Java 构造器 transport.connect(this::handle)）。
// handler 表可为 nil（客户端通常只挂 roots/list 等反向请求处理器）。
func NewSession(t Transport, requestHandlers map[string]RequestHandlerFunc, notificationHandlers map[string]NotificationHandlerFunc) *Session {
	if t == nil {
		panic("mcp: transport must not be nil")
	}
	s := &Session{
		transport:            t,
		requestHandlers:      requestHandlers,
		notificationHandlers: notificationHandlers,
		closed:               make(chan struct{}),
	}
	go s.pump()
	return s
}

// SendRequest 发送请求并等待响应：int64 递增 ID → pending 表 → 超时/关闭由
// ctx 与会话关闭驱动；result 非 nil 时把响应 result 解入（Java Void 跳过对应
// result==nil）。响应 error 非空时返回 *JSONRPCError（errors.As 可取 Code）。
func (s *Session) SendRequest(ctx context.Context, method string, params any, result any) error {
	id := s.nextID.Add(1)

	ch := make(chan *JSONRPCResponse, 1)
	s.pending.Store(id, ch)
	defer s.pending.Delete(id)

	if err := s.transport.Send(ctx, &JSONRPCRequest{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Method:  method,
		Params:  marshalParams(params),
	}); err != nil {
		return &TransportError{Err: fmt.Errorf("send request %s: %w", method, err)}
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return resp.Error
		}
		if result != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, result)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("mcp: request %s: %w", method, ctx.Err())
	case <-s.closed:
		return ErrSessionClosed
	}
}

// SendNotification 发送通知（无响应、无 id）。
func (s *Session) SendNotification(method string, params any) error {
	if err := s.transport.Send(context.Background(), &JSONRPCNotification{
		JSONRPC: JSONRPCVersion,
		Method:  method,
		Params:  marshalParams(params),
	}); err != nil {
		return &TransportError{Err: fmt.Errorf("send notification %s: %w", method, err)}
	}
	return nil
}

// Close 关闭会话：唤醒全部 pending（ErrSessionClosed）并关闭传输（幂等）。
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		close(s.closed)
		s.dismissPending()
		s.closeErr = s.transport.Close()
	})
	return s.closeErr
}

// Done 会话终止信号（Close 或传输 Receive 关闭）。客户端门面据此在下次
// 操作时自愈重建会话。
func (s *Session) Done() <-chan struct{} { return s.closed }

func (s *Session) dismissPending() {
	s.pending.Range(func(key, _ any) bool {
		s.pending.Delete(key)
		log.Warn("mcp: abruptly terminating exchange for request %v", key)
		return true
	})
}

// pump 消息泵（Java handle()）：Response 唤醒 pending；Request 分发 handler 并
// 回写响应；Notification 分发 handler。请求/通知异步分发（Java subscribe 的
// 非阻塞语义）；响应同步（保序）。传输关闭即会话终止。
func (s *Session) pump() {
	for msg := range s.transport.Receive() {
		switch m := msg.(type) {
		case *JSONRPCResponse:
			s.dispatchResponse(m)
		case *JSONRPCRequest:
			go s.dispatchRequest(m)
		case *JSONRPCNotification:
			go s.dispatchNotification(m)
		default:
			log.Warn("mcp: received unknown message type: %v", msg)
		}
	}
	// 传输终止：残留 pending 以 ErrSessionClosed 终止
	s.closeOnce.Do(func() {
		close(s.closed)
	})
}

func (s *Session) dispatchResponse(resp *JSONRPCResponse) {
	if resp.ID == nil {
		log.Error("mcp: discarded MCP request response without session id. " +
			"This is an indication of a bug in the request sender code that can lead to memory leaks " +
			"as pending requests will never be completed.")
		return
	}
	v, ok := s.pending.LoadAndDelete(resp.ID)
	if !ok {
		log.Warn("mcp: unexpected response for unknown id %v", resp.ID)
		return
	}
	v.(chan *JSONRPCResponse) <- resp // 容量 1 且先删后投，不阻塞
}

func (s *Session) dispatchRequest(req *JSONRPCRequest) {
	result, rpcErr := s.handleRequest(req)
	var resp JSONRPCResponse
	if rpcErr != nil {
		resp = JSONRPCResponse{JSONRPC: JSONRPCVersion, ID: req.ID, Error: rpcErr}
	} else {
		raw, err := json.Marshal(result)
		if err != nil {
			resp = JSONRPCResponse{JSONRPC: JSONRPCVersion, ID: req.ID,
				Error: &JSONRPCError{Code: CodeInternalError, Message: err.Error()}}
		} else {
			resp = JSONRPCResponse{JSONRPC: JSONRPCVersion, ID: req.ID, Result: raw}
		}
	}
	if err := s.transport.Send(context.Background(), &resp); err != nil {
		log.Warn("mcp: issue sending response to the client: %v", err)
	}
}

// handleRequest handler 查表分发；panic → INTERNAL_ERROR（Java 异常 →
// aggregateExceptionMessages 同类兜底）。
func (s *Session) handleRequest(req *JSONRPCRequest) (result any, rpcErr *JSONRPCError) {
	defer func() {
		if r := recover(); r != nil {
			rpcErr = &JSONRPCError{Code: CodeInternalError, Message: fmt.Sprintf("%v", r)}
		}
	}()
	handler, ok := s.requestHandlers[req.Method]
	if !ok {
		return nil, methodNotFound(req.Method)
	}
	return handler(context.Background(), req.Params)
}

// methodNotFound 缺省错误（Java getMethodNotFoundError：roots/list 特判文案）。
func methodNotFound(method string) *JSONRPCError {
	if method == MethodRootsList {
		return &JSONRPCError{
			Code:    CodeMethodNotFound,
			Message: "Roots not supported",
			Data:    map[string]any{"reason": "Client does not have roots capability"},
		}
	}
	return &JSONRPCError{Code: CodeMethodNotFound, Message: "Method not found: " + method}
}

func (s *Session) dispatchNotification(n *JSONRPCNotification) {
	handler, ok := s.notificationHandlers[n.Method]
	if !ok {
		log.Warn("mcp: no handler registered for notification method: %s", n.Method)
		return
	}
	if err := handler(n.Params); err != nil {
		log.Error("mcp: error handling notification %s: %v", n.Method, err)
	}
}

// marshalParams 参数序列化（nil 省略；json.RawMessage 原样透传）。
func marshalParams(params any) json.RawMessage {
	if params == nil {
		return nil
	}
	if raw, ok := params.(json.RawMessage); ok {
		return raw
	}
	b, err := json.Marshal(params)
	if err != nil {
		// 序列化失败按 Java IllegalArgumentException 语义上抛给调用方无法在此
		// 表达——编码为内部错误帧由调用方看到；此处保守返回原文 nil
		log.Error("mcp: marshal params: %v", err)
		return nil
	}
	return b
}
