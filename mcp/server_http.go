// server_http.go streamable HTTP 传输服务端（对标 WebRxStreamableServerTransport
// Provider）+ 旧式 SSE 服务端（WebRxSseServerTransportProvider）+ aifei 挂载中间件。
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/crazy-airhead/aifei-go/log"
)

// ---- SSE 出站流 ----

// sseSink 单条 HTTP 响应承载的 SSE 出站流（POST 回写流或 GET 监听流）。send
// 与 close 互斥——close 等待在途写完成后置 done 并收号，杜绝 handler 返回后
// 的迟到写入。
type sseSink struct {
	w      http.ResponseWriter
	fl     http.Flusher
	mu     sync.Mutex
	done   bool
	closed chan struct{}
	once   sync.Once
}

func newSSESink(w http.ResponseWriter) *sseSink {
	fl, _ := w.(http.Flusher)
	return &sseSink{w: w, fl: fl, closed: make(chan struct{})}
}

func (s *sseSink) send(msg Message) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return ErrSessionClosed
	}
	if _, err := fmt.Fprintf(s.w, "event: message\ndata: %s\n\n", b); err != nil {
		return err
	}
	if s.fl != nil {
		s.fl.Flush()
	}
	return nil
}

func (s *sseSink) close() {
	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		return
	}
	s.done = true
	s.mu.Unlock()
	s.once.Do(func() { close(s.closed) })
}

// Done 流收束信号（GET 监听 handler 据此退出）。
func (s *sseSink) Done() <-chan struct{} { return s.closed }

// writeEvent 写自定义事件名的 SSE 帧（旧式 SSE 的 endpoint 事件）。
func (s *sseSink) writeEvent(event, data string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return ErrSessionClosed
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, data); err != nil {
		return err
	}
	if s.fl != nil {
		s.fl.Flush()
	}
	return nil
}

// ---- streamable HTTP ----

// Handler streamable HTTP 端点（挂到任意 http.Server / mux；单端点承载
// POST/GET/DELETE 全语义）。
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.serveStreamable)
}

// Middleware aifei 挂载缝（server.WithHTTPHandler 裸通道）：前缀命中走 MCP
// 端点，其余穿透下一 handler。
func (s *Server) Middleware(prefix string) func(http.Handler) http.Handler {
	prefix = strings.TrimSuffix(prefix, "/")
	h := s.Handler()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if p := r.URL.Path; p == prefix || strings.HasPrefix(p, prefix+"/") {
				h.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Server) serveStreamable(w http.ResponseWriter, r *http.Request) {
	if s.isClosing() {
		writeHTTPError(w, http.StatusServiceUnavailable, "Server is shutting down")
		return
	}
	if err := s.securityValidator(r); err != nil {
		writeHTTPError(w, http.StatusForbidden, err.Error())
		return
	}
	switch r.Method {
	case http.MethodPost:
		s.serveStreamablePOST(w, r)
	case http.MethodGet:
		s.serveStreamableGET(w, r)
	case http.MethodDelete:
		s.serveStreamableDELETE(w, r)
	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		writeHTTPError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (s *Server) isClosing() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.closing
}

// serveStreamablePOST 入站消息：initialize → 建会话 + JSON 响应 + 会话头；
// 其余请求 → SSE 回写流（响应后收流）；通知/响应 → 202。
func (s *Server) serveStreamablePOST(w http.ResponseWriter, r *http.Request) {
	if !(acceptsMediaType(r, "application/json") && acceptsMediaType(r, "text/event-stream")) {
		writeHTTPError(w, http.StatusBadRequest, "Invalid Accept header: application/json and text/event-stream are required")
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	msg, err := UnmarshalMessage(body)
	if err != nil {
		writeHTTPError(w, http.StatusBadRequest, "Invalid message format")
		return
	}

	if req, ok := msg.(*JSONRPCRequest); ok && req.Method == MethodInitialize {
		sess := s.newSession(ClientCapabilities{}, Implementation{})
		if sess == nil {
			writeHTTPError(w, http.StatusServiceUnavailable, "Server is shutting down")
			return
		}
		cc := &CallCtx{ctx: r.Context(), sess: sess, progressToken: progressTokenOf(req)}
		result, rpcErr := sess.safeCall(s.handlers[MethodInitialize], cc, req.Params)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", sess.id)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(buildResponseFrame(req.ID, result, rpcErr))
		return
	}

	id := r.Header.Get("Mcp-Session-Id")
	if id == "" {
		writeHTTPError(w, http.StatusBadRequest, "Session ID missing")
		return
	}
	sess := s.sessionByID(id)
	if sess == nil {
		writeHTTPError(w, http.StatusNotFound, "Session not found: "+id)
		return
	}

	switch m := msg.(type) {
	case *JSONRPCRequest:
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		sink := newSSESink(w)
		sess.dispatchRequest(r.Context(), sink, m)
		sink.close() // 响应后收流（Java responseStream 完成语义）
	case *JSONRPCNotification:
		sess.dispatchNotification(m)
		w.WriteHeader(http.StatusAccepted)
	case *JSONRPCResponse:
		sess.acceptResponse(m)
		w.WriteHeader(http.StatusAccepted)
	default:
		writeHTTPError(w, http.StatusBadRequest, "Invalid message format")
	}
}

// serveStreamableGET 监听流：Accept 校验 → 会话校验 → Last-Event-ID（无回放
// 存储，空回放即收流，Java Flux.empty 同款）→ 常驻 SSE 直至断开；流断开即
// 会话失效（客户端据此重连重握手）。
func (s *Server) serveStreamableGET(w http.ResponseWriter, r *http.Request) {
	if !acceptsMediaType(r, "text/event-stream") {
		writeHTTPError(w, http.StatusBadRequest, "Invalid Accept header: text/event-stream is required")
		return
	}
	id := r.Header.Get("Mcp-Session-Id")
	if id == "" {
		writeHTTPError(w, http.StatusBadRequest, "Session ID missing")
		return
	}
	sess := s.sessionByID(id)
	if sess == nil {
		writeHTTPError(w, http.StatusNotFound, "Session not found: "+id)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	sink := newSSESink(w)
	if last := r.Header.Get("Last-Event-ID"); last != "" {
		log.Warn("mcp: Last-Event-ID replay not supported; no events will be resent (session %s)", id)
		sink.close()
		return
	}
	sess.setListening(sink)

	// 保活（默认关）：按间隔向监听流发 ping 反向请求
	if s.keepAlive > 0 {
		stop := make(chan struct{})
		go func() {
			t := time.NewTicker(s.keepAlive)
			defer t.Stop()
			for {
				select {
				case <-stop:
					return
				case <-t.C:
					ctx, cancel := context.WithTimeout(context.Background(), sess.requestTimeout)
					if err := sess.sendRequest(ctx, sink, MethodPing, nil, nil); err != nil {
						log.Debug("mcp: keepalive ping failed on session %s: %v", id, err)
					}
					cancel()
				}
			}
		}()
		defer close(stop)
	}

	select {
	case <-r.Context().Done():
	case <-sink.Done():
	}
	sess.clearListening(sink)
	sess.close()
	s.removeSession(sess)
}

// serveStreamableDELETE 会话注销。
func (s *Server) serveStreamableDELETE(w http.ResponseWriter, r *http.Request) {
	id := r.Header.Get("Mcp-Session-Id")
	if id == "" {
		writeHTTPError(w, http.StatusBadRequest, "Session ID missing")
		return
	}
	sess := s.sessionByID(id)
	if sess == nil {
		writeHTTPError(w, http.StatusNotFound, "Session not found: "+id)
		return
	}
	sess.close()
	s.removeSession(sess)
	w.WriteHeader(http.StatusOK)
}

// buildResponseFrame 组响应帧。
func buildResponseFrame(id any, result any, rpcErr *JSONRPCError) *JSONRPCResponse {
	if rpcErr != nil {
		return &JSONRPCResponse{JSONRPC: JSONRPCVersion, ID: id, Error: rpcErr}
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return &JSONRPCResponse{JSONRPC: JSONRPCVersion, ID: id,
			Error: &JSONRPCError{Code: CodeInternalError, Message: err.Error()}}
	}
	return &JSONRPCResponse{JSONRPC: JSONRPCVersion, ID: id, Result: raw}
}

// writeHTTPError 传输层错误回执（JSON 信封）。
func writeHTTPError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": msg}})
}

// acceptsMediaType Accept 头包含指定 MIME（含 */*、type/* 通配；Java MediaType
// isCompatible 的宽松近似）。
func acceptsMediaType(r *http.Request, mime string) bool {
	for _, part := range strings.Split(r.Header.Get("Accept"), ",") {
		m := strings.TrimSpace(part)
		if i := strings.IndexByte(m, ';'); i >= 0 {
			m = strings.TrimSpace(m[:i])
		}
		switch m {
		case mime, "*/*":
			return true
		case "application/*":
			if mime == "application/json" {
				return true
			}
		case "text/*":
			if mime == "text/event-stream" {
				return true
			}
		}
	}
	return false
}

// ---- 旧式 SSE（2024-11-05）服务端 ----

// SSEHandler 旧式 SSE 双端点 handler：GET ssePath 建会话并推 endpoint 事件
// （报文端点绝对 URL 带 sessionId 查询参数），全部服务端帧走该流；POST
// messagePath?sessionId=... 收客户端消息（响应回 GET 流）。GET 断开即注销会话。
func (s *Server) SSEHandler(ssePath, messagePath string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(ssePath, func(w http.ResponseWriter, r *http.Request) {
		s.serveOldSSE(w, r, messagePath)
	})
	mux.HandleFunc(messagePath, func(w http.ResponseWriter, r *http.Request) {
		s.serveOldSSEMessage(w, r)
	})
	return mux
}

func (s *Server) serveOldSSE(w http.ResponseWriter, r *http.Request, messagePath string) {
	if s.isClosing() {
		writeHTTPError(w, http.StatusServiceUnavailable, "Server is shutting down")
		return
	}
	if err := s.securityValidator(r); err != nil {
		writeHTTPError(w, http.StatusForbidden, err.Error())
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeHTTPError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if !acceptsMediaType(r, "text/event-stream") {
		writeHTTPError(w, http.StatusBadRequest, "Invalid Accept header: text/event-stream is required")
		return
	}
	sess := s.newSession(ClientCapabilities{}, Implementation{})
	if sess == nil {
		writeHTTPError(w, http.StatusServiceUnavailable, "Server is shutting down")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	sink := newSSESink(w)
	if sink.fl != nil {
		sink.fl.Flush()
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	endpoint := fmt.Sprintf("%s://%s%s?sessionId=%s", scheme, r.Host, messagePath, sess.id)
	if err := sink.writeEvent("endpoint", endpoint); err != nil {
		sess.close()
		s.removeSession(sess)
		return
	}
	sess.setListening(sink)

	select {
	case <-r.Context().Done():
	case <-sink.Done():
	}
	sess.clearListening(sink)
	sess.close()
	s.removeSession(sess)
}

func (s *Server) serveOldSSEMessage(w http.ResponseWriter, r *http.Request) {
	if s.isClosing() {
		writeHTTPError(w, http.StatusServiceUnavailable, "Server is shutting down")
		return
	}
	if err := s.securityValidator(r); err != nil {
		writeHTTPError(w, http.StatusForbidden, err.Error())
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeHTTPError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	sess := s.sessionByID(r.URL.Query().Get("sessionId"))
	if sess == nil {
		writeHTTPError(w, http.StatusNotFound, "Session not found")
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	msg, err := UnmarshalMessage(body)
	if err != nil {
		writeHTTPError(w, http.StatusBadRequest, "Invalid message format")
		return
	}
	sink := sess.listeningSink()
	switch m := msg.(type) {
	case *JSONRPCRequest:
		// POST 即回 202，请求生命周期须脱离 r.Context()（响应回 GET 流）
		go sess.dispatchRequest(context.Background(), sink, m)
	case *JSONRPCNotification:
		sess.dispatchNotification(m)
	case *JSONRPCResponse:
		sess.acceptResponse(m)
	}
	w.WriteHeader(http.StatusAccepted)
}

// listeningSink 当前监听流（无则 nil；旧式 SSE 响应路径用）。
func (s *serverSession) listeningSink() serverSink {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listening
}
