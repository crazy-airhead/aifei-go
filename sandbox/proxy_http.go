// proxy_http.go HTTP CONNECT 正向代理 + 明文 HTTP 转发（对标
// HttpProxyServer.java）。127.0.0.1 监听、每连接一 goroutine；域名过滤
// 走 FilterNetworkRequest（fail-closed），父代理路由走 dialViaParent。
//
// 与 Java 的偏差（均有上游缺陷背景，注释就地展开）：
//   - 配置经 provider 每请求读取（Manager.UpdateConfig 活切换的既有
//     语义承诺）；Java 构造时固定引用。
//   - Java handleClient 的 finally 无条件 close(client)——pipe() 纯异步
//     返回后连接立即被关，刚建立的 CONNECT 隧道随即双泵 SocketException
//     （上游移植缺陷，代理实际不可用）；Go 的 tunnel 阻塞等双泵退出，
//     外层 defer 再关。
//   - 请求体读取：Java 的 BufferedReader 预读会吞掉 socket 流里已到的
//     body 字节（其注释自认 unreliable）；Go 统一经同一 bufio.Reader，
//     无此缺陷。
//   - 直连分支 HttpURLConnection → net/http（首值头转发、按 Content-
//     Length 精确或 chunked 裸流转发体、状态行重建）。
//   - Stop 显式断开在飞连接（Java shutdownNow 只中断线程不关 socket，
//     依赖进程退出收尾）。
package sandbox

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/crazy-airhead/aifei-go/log"
)

// hopByHopHeaders RFC 7230 §6.1 逐跳头（不得转发）。
var hopByHopHeaders = map[string]struct{}{
	"connection":          {},
	"keep-alive":          {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
	"proxy-connection":    {},
	"te":                  {},
	"trailer":             {},
	"transfer-encoding":   {},
	"upgrade":             {},
}

const response403Blocked = "HTTP/1.1 403 Forbidden\r\n" +
	"Content-Type: text/plain\r\n" +
	"X-Proxy-Error: blocked-by-sandbox-runtime\r\n" +
	"\r\n" +
	"Connection blocked by network policy"

const response502BadGateway = "HTTP/1.1 502 Bad Gateway\r\n\r\n"

// proxyHTTPClient 直连分支的 HTTP 客户端（Java HttpURLConnection 的
// connectTimeout/readTimeout 30s → 拨号/握手/响应头超时 30s；体流式
// 不限总时长）。
var proxyHTTPClient = &http.Client{
	Transport: &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	},
}

// FilterRequestCallback 每请求过滤回调（转发前裁决）：返回 false 或
// panic 一律拒绝（fail-closed）。headers 键为小写。对标
// FilterRequestCallback（Java 根包与 net 包两份同形接口合一）。
type FilterRequestCallback func(method, url string, headers map[string]string) bool

// HTTPProxyServer HTTP CONNECT 正向代理。
type HTTPProxyServer struct {
	proxyBase
	config func() *RuntimeConfig
	ask    AskCallback
	parent *ParentProxyResolver
	filter FilterRequestCallback
}

// NewHTTPProxyServer 构造（配置固定引用）。
func NewHTTPProxyServer(cfg *RuntimeConfig, ask AskCallback, parent *ParentProxyResolver, filter FilterRequestCallback) *HTTPProxyServer {
	return NewHTTPProxyServerFunc(func() *RuntimeConfig { return cfg }, ask, parent, filter)
}

// NewHTTPProxyServerFunc 构造（配置 provider 每请求读取——支持热更新）。
func NewHTTPProxyServerFunc(getCfg func() *RuntimeConfig, ask AskCallback, parent *ParentProxyResolver, filter FilterRequestCallback) *HTTPProxyServer {
	return &HTTPProxyServer{config: getCfg, ask: ask, parent: parent, filter: filter}
}

// Start 监听 127.0.0.1:port（0 = 临时端口）并返回实际端口。
func (s *HTTPProxyServer) Start(port int) (int, error) {
	return s.proxyBase.start(port, s.handleClient)
}

// Stop 关停（关监听 + 断在飞连接）。
func (s *HTTPProxyServer) Stop() { s.proxyBase.stop() }

func (s *HTTPProxyServer) handleClient(client net.Conn) {
	defer client.Close()

	br := bufio.NewReader(client)
	requestLine, err := readLine(br)
	if err != nil && requestLine == "" {
		return
	}
	if requestLine == "" {
		return
	}

	headers := newHeaderMap()
	for {
		line, err := readLine(br)
		if err != nil {
			return
		}
		if line == "" {
			break
		}
		if ci := strings.Index(line, ":"); ci > 0 {
			headers.set(strings.ToLower(strings.TrimSpace(line[:ci])),
				strings.TrimSpace(line[ci+1:]))
		}
	}

	if strings.HasPrefix(requestLine, "CONNECT") {
		s.handleConnect(client, br, requestLine)
	} else {
		s.handleHTTP(client, br, requestLine, headers)
	}
}

// ---- CONNECT（HTTPS 隧道） ----

func (s *HTTPProxyServer) handleConnect(client net.Conn, br *bufio.Reader, requestLine string) {
	parts := strings.Split(requestLine, " ")
	if len(parts) < 2 {
		client.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
		return
	}
	target := parts[1]
	var hostname string
	var port int

	if strings.HasPrefix(target, "[") {
		bracketEnd := strings.Index(target, "]")
		if bracketEnd < 0 {
			client.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
			return
		}
		hostname = target[1:bracketEnd]
		portStr := target[bracketEnd+1:]
		if strings.HasPrefix(portStr, ":") {
			p, err := strconv.Atoi(portStr[1:])
			if err != nil {
				client.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
				return
			}
			port = p
		} else {
			port = 443
		}
	} else {
		hp := strings.Split(target, ":")
		hostname = hp[0]
		if len(hp) > 1 {
			// 端口非数字：Java Integer.parseInt 抛 RuntimeException 逃逸
			// 出 IOException catch，executor 吞掉——连接无响应关闭。Go
			// 静默 return，可观测行为一致。
			p, err := strconv.Atoi(hp[1])
			if err != nil {
				return
			}
			port = p
		} else {
			port = 443
		}
	}

	if !IsValidHost(hostname) {
		log.Error("sandbox: denying CONNECT to malformed host %s:%d", hostname, port)
		client.Write([]byte("HTTP/1.1 403 Forbidden\r\n" +
			"Content-Type: text/plain\r\n" +
			"X-Proxy-Error: blocked-by-sandbox-runtime\r\n" +
			"\r\n" +
			"Connection blocked - invalid host"))
		return
	}

	if !FilterNetworkRequest(port, hostname, s.config(), s.ask) {
		client.Write([]byte(response403Blocked))
		return
	}

	// filterRequest 回调：CONNECT 合成 connect:// URL 供检查（Java 同款）
	if s.filter != nil {
		if !s.callFilter("CONNECT", "connect://"+net.JoinHostPort(hostname, strconv.Itoa(port)), nil) {
			log.Debug("sandbox: filterRequest denied CONNECT %s:%d", hostname, port)
			client.Write([]byte(response403Blocked))
			return
		}
	}

	var upstream net.Conn
	if s.parent != nil && s.parent.HTTPProxyURL() != "" && !s.parent.ShouldBypassProxy(hostname, port) {
		conn, err := dialViaParent(s.parent.HTTPProxyURL(), net.JoinHostPort(hostname, strconv.Itoa(port)))
		if err != nil {
			log.Error("sandbox: parent proxy CONNECT failed: %v", err)
			client.Write([]byte(response502BadGateway))
			return
		}
		upstream = conn
	} else {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(hostname, strconv.Itoa(port)), 30*time.Second)
		if err != nil {
			client.Write([]byte(response502BadGateway))
			log.Error("sandbox: CONNECT tunnel failed: %v", err)
			return
		}
		upstream = conn
	}

	if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		upstream.Close()
		return
	}
	tunnel(client, br, upstream)
}

// ---- 明文 HTTP ----

func (s *HTTPProxyServer) handleHTTP(client net.Conn, br *bufio.Reader, requestLine string, headers *headerMap) {
	parts := strings.Split(requestLine, " ")
	if len(parts) < 2 {
		client.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
		return
	}
	method, urlStr := parts[0], parts[1]

	if !s.forwardHTTP(client, br, method, urlStr, headers) {
		return
	}
}

// forwardHTTP 明文 HTTP 转发主路径（过滤 → 头处理 → 父代理/直连分流）。
// 返回 false 表示上游已写响应或连接已由外层关闭。
func (s *HTTPProxyServer) forwardHTTP(client net.Conn, br *bufio.Reader, method, urlStr string, headers *headerMap) bool {
	// Java new URL(...) 解析失败（缺协议/空主机等）→ catch → 502
	u, err := url.Parse(urlStr)
	if err != nil || u.Hostname() == "" || u.Scheme == "" {
		client.Write([]byte(response502BadGateway))
		log.Error("sandbox: HTTP proxy error: invalid request URL %q", urlStr)
		return false
	}
	hostname := u.Hostname()
	port := defaultPort(u)

	if !FilterNetworkRequest(port, hostname, s.config(), s.ask) {
		client.Write([]byte(response403Blocked))
		return false
	}

	if s.filter != nil {
		if !s.callFilter(method, urlStr, headers) {
			log.Debug("sandbox: filterRequest denied %s %s", method, urlStr)
			client.Write([]byte(response403Blocked))
			return false
		}
	}

	// 剥逐跳头 + Host 头指向目标（显式端口才带上；v6 补方括号）
	fwdHeaders := stripHopByHop(headers)
	hostHeader := hostname
	if strings.Contains(hostname, ":") {
		hostHeader = "[" + hostname + "]"
	}
	if p := u.Port(); p != "" {
		hostHeader += ":" + p
	}
	fwdHeaders.set("host", hostHeader)

	if s.parent != nil && s.parent.HTTPProxyURL() != "" && !s.parent.ShouldBypassProxy(hostname, port) {
		return s.forwardViaParent(client, br, method, u, fwdHeaders, headers)
	}
	return s.forwardDirect(client, br, method, u, fwdHeaders, headers)
}

// forwardViaParent 经父代理转发（正向代理语义：请求行用绝对 URI）。
// 注意上游即如此：https 父代理也走裸 TCP（仅端口回填 443，不做 TLS）。
func (s *HTTPProxyServer) forwardViaParent(client net.Conn, br *bufio.Reader, method string,
	u *url.URL, fwdHeaders, headers *headerMap) bool {

	proxyURL, err := url.Parse(s.parent.HTTPProxyURL())
	if err != nil || proxyURL.Hostname() == "" {
		client.Write([]byte(response502BadGateway))
		return false
	}
	proxyPort := 80
	if p := proxyURL.Port(); p != "" {
		if n, perr := strconv.Atoi(p); perr == nil && n > 0 {
			proxyPort = n
		}
	} else if strings.EqualFold(proxyURL.Scheme, "https") {
		proxyPort = 443
	}

	proxyConn, err := net.Dial("tcp", net.JoinHostPort(proxyURL.Hostname(), strconv.Itoa(proxyPort)))
	if err != nil {
		client.Write([]byte(response502BadGateway))
		log.Error("sandbox: parent proxy dial failed: %v", err)
		return false
	}
	defer proxyConn.Close()

	// 请求行：绝对 URI（正向代理协议）；路径空 → "/"
	reqURI := u.RequestURI()
	if reqURI == "" {
		reqURI = "/"
	}
	var req bytes.Buffer
	req.WriteString(method + " " + u.Scheme + "://" + u.Host + reqURI + " HTTP/1.1\r\n")
	for _, kv := range fwdHeaders.entries() {
		req.WriteString(kv.key + ": " + kv.value + "\r\n")
	}
	req.WriteString("\r\n")
	if _, err := proxyConn.Write(req.Bytes()); err != nil {
		client.Write([]byte(response502BadGateway))
		return false
	}

	// 体转发（Content-Length 精确 / chunked 读到关闭——Java 同款）
	forwardRequestBody(br, proxyConn, headers)

	// 响应：状态行 + 头（剥逐跳）+ 体读到连接关闭
	pbr := bufio.NewReader(proxyConn)
	statusLine, err := readLine(pbr)
	if err != nil {
		client.Write([]byte(response502BadGateway))
		return false
	}
	respHeaders := newHeaderMap()
	for {
		line, rerr := readLine(pbr)
		if rerr != nil || line == "" {
			break
		}
		if ci := strings.Index(line, ":"); ci > 0 {
			respHeaders.set(strings.ToLower(strings.TrimSpace(line[:ci])),
				strings.TrimSpace(line[ci+1:]))
		}
	}

	var out bytes.Buffer
	out.WriteString(statusLine + "\r\n")
	for _, kv := range stripHopByHop(respHeaders).entries() {
		out.WriteString(kv.key + ": " + kv.value + "\r\n")
	}
	out.WriteString("\r\n")
	client.Write(out.Bytes())
	pump(pbr, client)
	return true
}

// forwardDirect 直连目标（Java HttpURLConnection 分支）。
func (s *HTTPProxyServer) forwardDirect(client net.Conn, br *bufio.Reader, method string,
	u *url.URL, fwdHeaders, headers *headerMap) bool {

	contentLength := 0
	if cls := headers.get("content-length"); cls != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(cls)); err == nil {
			contentLength = n
		}
	}
	transferEncoding := strings.ToLower(headers.get("transfer-encoding"))
	chunked := strings.Contains(transferEncoding, "chunked")

	var body io.Reader
	if contentLength > 0 {
		body = io.LimitReader(br, int64(contentLength))
	} else if chunked {
		// chunked 请求体按原始字节（含分帧）搬运——上游怪癖保留：
		// Java 读原始 chunked 流当普通体写，Go 等价地裸流转发。
		body = br
	}

	req, err := http.NewRequest(method, u.String(), body)
	if err != nil {
		client.Write([]byte(response502BadGateway))
		log.Error("sandbox: HTTP proxy error: %v", err)
		return false
	}
	if contentLength > 0 {
		req.ContentLength = int64(contentLength)
	}
	// Host 由 net/http 按 URL 自设（Java 同款跳过 host 头）
	for _, kv := range fwdHeaders.entries() {
		if kv.key == "host" {
			continue
		}
		req.Header.Set(kv.key, kv.value)
	}

	resp, err := proxyHTTPClient.Do(req)
	if err != nil {
		client.Write([]byte(response502BadGateway))
		log.Error("sandbox: HTTP proxy error: %v", err)
		return false
	}
	defer resp.Body.Close()

	// 响应头：每名取首值（Java "take first value for simplicity"），
	// Content-Length 在 Go 的 Header 表之外——已知则补写。
	respHeaders := newHeaderMap()
	for k, vs := range resp.Header {
		if len(vs) > 0 {
			respHeaders.set(strings.ToLower(k), vs[0])
		}
	}
	if resp.ContentLength >= 0 && respHeaders.get("content-length") == "" {
		respHeaders.set("content-length", strconv.FormatInt(resp.ContentLength, 10))
	}

	var out bytes.Buffer
	reason := resp.Status
	if i := strings.Index(reason, " "); i >= 0 {
		reason = reason[i+1:]
	}
	fmt.Fprintf(&out, "HTTP/1.1 %d %s\r\n", resp.StatusCode, reason)
	for _, kv := range stripHopByHop(respHeaders).entries() {
		out.WriteString(kv.key + ": " + kv.value + "\r\n")
	}
	out.WriteString("\r\n")
	client.Write(out.Bytes())
	pump(resp.Body, client)
	return true
}

// forwardRequestBody 请求体转发（对标 forwardRequestBody）：Content-
// Length 精确字节；chunked 读到连接关闭；GET/HEAD 等无体跳过。
func forwardRequestBody(clientIn *bufio.Reader, upstreamOut io.Writer, headers *headerMap) {
	contentLength := 0
	if cls := headers.get("content-length"); cls != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(cls)); err == nil {
			contentLength = n
		}
	}

	if contentLength > 0 {
		buf := make([]byte, 8192)
		remaining := contentLength
		for remaining > 0 {
			toRead := len(buf)
			if remaining < toRead {
				toRead = remaining
			}
			read, err := clientIn.Read(buf[:toRead])
			if read > 0 {
				if _, werr := upstreamOut.Write(buf[:read]); werr != nil {
					return
				}
				remaining -= read
			}
			if err != nil {
				return
			}
		}
	} else if strings.Contains(strings.ToLower(headers.get("transfer-encoding")), "chunked") {
		pump(clientIn, upstreamOut)
	}
}

// ---- 共用件 ----

// headerKV 有序键值对。
type headerKV struct{ key, value string }

// headerMap 有序头表（对标 Java LinkedHashMap：键小写、重复键原位覆值
// 保序）。
type headerMap struct {
	keys []string
	m    map[string]string
}

func newHeaderMap() *headerMap {
	return &headerMap{m: map[string]string{}}
}

func (h *headerMap) set(key, value string) {
	if _, ok := h.m[key]; !ok {
		h.keys = append(h.keys, key)
	}
	h.m[key] = value
}

func (h *headerMap) get(key string) string { return h.m[key] }

func (h *headerMap) entries() []headerKV {
	out := make([]headerKV, 0, len(h.keys))
	for _, k := range h.keys {
		out = append(out, headerKV{k, h.m[k]})
	}
	return out
}

// snapshot 拷贝为普通 map（filterRequest 回调入参——实现方只读）。
func (h *headerMap) snapshot() map[string]string {
	out := make(map[string]string, len(h.keys))
	for k, v := range h.m {
		out[k] = v
	}
	return out
}

// stripHopByHop 剥逐跳头（RFC 7230 §6.1 固定集 ∪ Connection 头点名集）。
// 对标 stripHopByHop（parent-proxy.ts）。
func stripHopByHop(headers *headerMap) *headerMap {
	extra := map[string]struct{}{}
	if conn := headers.get("connection"); conn != "" {
		for _, tok := range strings.Split(conn, ",") {
			if t := strings.ToLower(strings.TrimSpace(tok)); t != "" {
				extra[t] = struct{}{}
			}
		}
	}
	out := newHeaderMap()
	for _, k := range headers.keys {
		if _, hop := hopByHopHeaders[k]; hop {
			continue
		}
		if _, x := extra[k]; x {
			continue
		}
		out.set(k, headers.m[k])
	}
	return out
}

// callFilter 回调护栏：panic → 拒绝（Java catch Exception 的对应物）。
func (s *HTTPProxyServer) callFilter(method, reqURL string, headers *headerMap) (allowed bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("sandbox: filterRequest threw for %s %s: %v", method, reqURL, r)
			allowed = false
		}
	}()
	var snap map[string]string
	if headers != nil {
		snap = headers.snapshot()
	}
	return s.filter(method, reqURL, snap)
}

// defaultPort URL 显式端口优先，否则按 scheme 缺省（http 80 / https 443 /
// 其余 -1——Java URL.getDefaultPort）。
func defaultPort(u *url.URL) int {
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			return n
		}
	}
	switch u.Scheme {
	case "http":
		return 80
	case "https":
		return 443
	}
	return -1
}

// readLine 读一行并剥 CRLF/LF；EOF 带部分内容时返回已读部分 + err。
func readLine(br *bufio.Reader) (string, error) {
	line, err := br.ReadString('\n')
	return strings.TrimRight(line, "\r\n"), err
}

// pump 单向搬运泵（读到 EOF/错误或写失败即止）。
func pump(in io.Reader, out io.Writer) {
	buf := make([]byte, 8192)
	for {
		n, err := in.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// tunnel CONNECT 隧道双向泵：阻塞至两个方向都结束（偏离 Java：pipe()
// 纯异步返回、handleClient finally 随即 close(client) 掐断隧道——上游
// 移植缺陷；Go 等双泵退出，外层 defer 再关 client）。client 侧经
// bufio.Reader 续读（CONNECT 头后预读的隧道字节不丢）。
func tunnel(client net.Conn, cbr *bufio.Reader, upstream net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		pump(cbr, client)
	}()
	go func() {
		defer wg.Done()
		pump(upstream, client)
	}()
	wg.Wait()
	upstream.Close()
}

// bufferedConn 把 bufio.Reader 预读的字节并回连接（Java 的 proxyReader
// 预读字节在 pipe 时被丢弃——父代理早发数据会丢，上游缺陷；Go 经同一
// reader 续读）。
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// dialViaParent 经父 HTTP 代理建 CONNECT 隧道（HttpProxyServer 的显式
// 报文与 SocksProxyServer 的 Socket(Proxy.Type.HTTP) 内建行为在 Go 的
// 统一等价物）。父代理 URL 未写端口 → 直接失败（Java getPort()==-1 构造
// 抛异常的对应物——对照 handleHTTP 分支有 80/443 缺省回填）。
func dialViaParent(parentURL, targetHostPort string) (net.Conn, error) {
	u, err := url.Parse(parentURL)
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("invalid parent proxy URL: %s", redactUserinfo(parentURL))
	}
	if u.Port() == "" {
		return nil, fmt.Errorf("parent proxy URL has no port: %s", redactUserinfo(parentURL))
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(u.Hostname(), u.Port()), 30*time.Second)
	if err != nil {
		return nil, err
	}
	req := "CONNECT " + targetHostPort + " HTTP/1.1\r\nHost: " + targetHostPort + "\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	status, err := readLine(br)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if !strings.Contains(status, "200") {
		conn.Close()
		return nil, errors.New("parent proxy denied CONNECT: " + status)
	}
	for {
		line, err := readLine(br)
		if err != nil {
			conn.Close()
			return nil, err
		}
		if line == "" {
			break
		}
	}
	return &bufferedConn{Conn: conn, r: br}, nil
}
