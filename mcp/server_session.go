// server_session.go 服务端会话（对标 McpStreamableServerSession + DefaultMcp
// StreamableServerSessionFactory）：反向请求 pending 表 + 双出口路由（请求绑定
// 流/监听流）+ 三传输共用的分发核心。stdio/HTTP 承载在 server_http.go 与本文件
// 的 RunStdio。
package mcp

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/crazy-airhead/aifei-go/log"
)

// serverSink 出站帧出口：POST 回写流（sseSink）、监听流（sseSink）或 stdout
// （stdioSink）。send 串行化由各实现自持锁保证；close 幂等。
type serverSink interface {
	send(msg Message) error
	close()
}

// serverSession 服务端会话：Mcp-Session-Id 对应的实体。反向请求（采样/征询/
// 保活 ping）在请求处理期内走请求绑定 sink，之外走监听流（GET SSE / stdio）；
// 无可用出口时按 Java MissingMcpTransportSession 语义报
// "Stream unavailable for session <id>"。
type serverSession struct {
	id     string
	server *Server

	mu        sync.Mutex
	caps      ClientCapabilities
	info      Implementation
	listening serverSink
	minLevel  LoggingLevel
	closed    bool

	done    chan struct{}
	pending sync.Map // int64 → chan *JSONRPCResponse（容量 1）
	nextID  atomic.Int64

	requestTimeout time.Duration
}

// capabilities 客户端能力（加锁读，initialize 会写入）。
func (s *serverSession) capabilities() ClientCapabilities {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.caps
}

// implementation 客户端实现标识（加锁读）。
func (s *serverSession) implementation() Implementation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.info
}

// levelAllowed 日志级别过滤（setLevel 之后仅推送不低于该级别的通知）。
func (s *serverSession) levelAllowed(l LoggingLevel) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return l.Severity() >= s.minLevel.Severity()
}

// setListening 登记监听流（GET SSE 建立时；stdio 会话常驻一个 stdioSink）。
func (s *serverSession) setListening(sink serverSink) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.listening = sink
}

// clearListening 摘除监听流（仅当仍是当前流——旧流的断开不得误清新流）。
func (s *serverSession) clearListening(sink serverSink) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listening == sink {
		s.listening = nil
	}
}

// sendRequest 反向请求：帧走 sink（nil 则监听流）；响应经 acceptResponse 唤醒
// pending；超时由调用方 ctx 驱动。无可用出口 = Java "Stream unavailable"。
func (s *serverSession) sendRequest(ctx context.Context, sink serverSink, method string, params any, result any) error {
	if sink == nil {
		s.mu.Lock()
		sink = s.listening
		s.mu.Unlock()
	}
	if sink == nil {
		return fmt.Errorf("mcp: stream unavailable for session %s", s.id)
	}
	id := s.nextID.Add(1)
	ch := make(chan *JSONRPCResponse, 1)
	s.pending.Store(id, ch)
	defer s.pending.Delete(id)
	if err := sink.send(&JSONRPCRequest{
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
	case <-s.done:
		return ErrSessionClosed
	}
}

// sendNotification 通知：帧走 sink（nil 则监听流）；无可用出口报错（请求期
// 通知携带 progressToken，丢失即协议破坏——与广播路径 notifyListening 的
// 尽力语义不同）。
func (s *serverSession) sendNotification(sink serverSink, method string, params any) error {
	if sink == nil {
		s.mu.Lock()
		sink = s.listening
		s.mu.Unlock()
	}
	if sink == nil {
		return fmt.Errorf("mcp: stream unavailable for session %s", s.id)
	}
	return sink.send(&JSONRPCNotification{
		JSONRPC: JSONRPCVersion,
		Method:  method,
		Params:  marshalParams(params),
	})
}

// notifyListening 广播路径：走监听流，无流静默告警（Java notifyClients 的
// sendNotificationAsync 异常落日志语义）。
func (s *serverSession) notifyListening(method string, params any) {
	s.mu.Lock()
	sink, closed := s.listening, s.closed
	s.mu.Unlock()
	if closed || sink == nil {
		return
	}
	if err := sink.send(&JSONRPCNotification{
		JSONRPC: JSONRPCVersion,
		Method:  method,
		Params:  marshalParams(params),
	}); err != nil {
		log.Warn("mcp: send notification %s to session %s: %v", method, s.id, err)
	}
}

// acceptResponse 客户端响应帧入 pending（POST 收到 Response / stdio 收到
// Response 时调用）。
func (s *serverSession) acceptResponse(resp *JSONRPCResponse) {
	if resp.ID == nil {
		log.Error("mcp: discarded response without id on session %s", s.id)
		return
	}
	v, ok := s.pending.LoadAndDelete(resp.ID)
	if !ok {
		log.Warn("mcp: unexpected response for unknown id %v on session %s", resp.ID, s.id)
		return
	}
	v.(chan *JSONRPCResponse) <- resp // 容量 1 且先删后投，不阻塞
}

// close 终止会话：pending 以 ErrSessionClosed 解除，监听流关闭（幂等）。
func (s *serverSession) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	listening := s.listening
	s.listening = nil
	s.mu.Unlock()
	close(s.done)
	s.pending.Range(func(key, _ any) bool {
		s.pending.Delete(key)
		return true
	})
	if listening != nil {
		listening.close()
	}
}

// ---- 分发核心（stdio / streamable POST / 旧式 SSE 共用）----

// dispatchRequest 请求分发：查表 → CallCtx → 响应回写 sink；panic →
// INTERNAL_ERROR（Java 异常聚合同类兜底）。同步执行——HTTP POST 由处理器
// 等待完成后收流；stdio 由调用方放 goroutine。
func (s *serverSession) dispatchRequest(ctx context.Context, sink serverSink, req *JSONRPCRequest) {
	cc := &CallCtx{ctx: ctx, sess: s, sink: sink, progressToken: progressTokenOf(req)}
	handler, ok := s.server.handlers[req.Method]
	var result any
	var rpcErr *JSONRPCError
	if !ok {
		rpcErr = &JSONRPCError{Code: CodeMethodNotFound, Message: "Method not found: " + req.Method}
	} else {
		result, rpcErr = s.safeCall(handler, cc, req.Params)
	}
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
	if err := sink.send(&resp); err != nil {
		log.Warn("mcp: issue sending response to the client: %v", err)
	}
}

func (s *serverSession) safeCall(handler serverHandlerFunc, cc *CallCtx, params json.RawMessage) (result any, rpcErr *JSONRPCError) {
	defer func() {
		if r := recover(); r != nil {
			rpcErr = &JSONRPCError{Code: CodeInternalError, Message: fmt.Sprintf("%v", r)}
		}
	}()
	return handler(cc, params)
}

// dispatchNotification 通知分发（Java asyncNotificationHandlers 的服务端子集：
// initialized/cancelled/roots 变更均记日志；未注册方法告警）。
func (s *serverSession) dispatchNotification(n *JSONRPCNotification) {
	switch n.Method {
	case MethodNotificationInitialized:
		log.Info("mcp: received initialized notification from client")
	case MethodNotificationCancelled:
		log.Debug("mcp: received cancellation notification (not supported): %s", string(n.Params))
	case MethodNotificationRootsListChanged:
		log.Info("mcp: received roots/list_changed notification from client")
	default:
		log.Warn("mcp: no handler registered for notification method: %s", n.Method)
	}
}

// progressToken 帧级 _meta 中的进度令牌。
func progressTokenOf(req *JSONRPCRequest) any {
	if req.Meta == nil {
		return nil
	}
	return req.Meta["progressToken"]
}

// ---- stdio 承载 ----

// stdioSink stdout 行帧出口（JSON 序列化不含裸换行，单行即单帧）。
type stdioSink struct {
	w    io.Writer
	mu   sync.Mutex
	done bool
}

func (s *stdioSink) send(msg Message) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return ErrSessionClosed
	}
	_, err = s.w.Write(append(b, '\n'))
	return err
}

func (s *stdioSink) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.done = true
}

// Stdin/Stdout 传输缝（测试注入用；默认 os.Stdin/os.Stdout）。
var (
	Stdin  io.Reader = os.Stdin
	Stdout io.Writer = os.Stdout
)

// RunStdio 以 stdio 传输运行服务端（对标 McpServerEndpoint stdio=true：会话在
// 启动时预建，initialize 作为普通请求抵达）。stdout 归协议通道独占——缺省日志
// 输出在此切至 stderr（日志写 stdout 会污染行帧）。ctx 结束或 stdin EOF 即返
// 回；返回前取消在途分发并注销会话。
func (s *Server) RunStdio(ctx context.Context) error {
	log.SetOutput(os.Stderr)
	sess := s.newSession(ClientCapabilities{}, Implementation{})
	if sess == nil {
		return fmt.Errorf("mcp: server is closing")
	}
	sink := &stdioSink{w: Stdout}
	sess.setListening(sink)
	dispatchRoot, rootCancel := context.WithCancel(ctx)
	defer func() {
		rootCancel()
		sess.close()
		s.removeSession(sess)
	}()

	lines := make(chan []byte)
	readErr := make(chan error, 1)
	go func() {
		defer close(lines)
		r := bufio.NewReader(Stdin)
		for {
			line, err := r.ReadBytes('\n')
			if len(line) > 0 {
				lines <- line
			}
			if err != nil {
				readErr <- err
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-readErr:
			if err != io.EOF {
				return err
			}
			return nil
		case line, ok := <-lines:
			if !ok {
				continue
			}
			dispatchStdioLine(dispatchRoot, sink, sess, line)
		}
	}
}

// dispatchStdioLine 单行帧分发：解析失败按 -32700 回执（id null）；请求异步
// 分发（响应回写 stdout），通知/响应同步处理。
func dispatchStdioLine(dispatchRoot context.Context, sink *stdioSink, sess *serverSession, line []byte) {
	msg, err := UnmarshalMessage(trimSpace(line))
	if err != nil {
		_ = sink.send(&JSONRPCResponse{JSONRPC: JSONRPCVersion,
			Error: &JSONRPCError{Code: CodeParseError, Message: err.Error()}})
		return
	}
	switch m := msg.(type) {
	case *JSONRPCRequest:
		go func() {
			ctx, cancel := context.WithCancel(dispatchRoot)
			defer cancel()
			sess.dispatchRequest(ctx, sink, m)
		}()
	case *JSONRPCNotification:
		sess.dispatchNotification(m)
	case *JSONRPCResponse:
		sess.acceptResponse(m)
	}
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == ' ' || b[len(b)-1] == '\t') {
		b = b[:len(b)-1]
	}
	return b
}

// newSessionID 会话 ID（Java UUID.randomUUID 对应物，crypto/rand v4 形态）。
func newSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand 失败即系统级故障
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
