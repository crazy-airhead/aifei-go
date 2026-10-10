// proxy_test.go P5-s3 代理服务器黑盒测试：ParentProxyResolver 解析链
// 与 NO_PROXY 匹配矩阵（回环恒直连/CIDR/后缀/裸 IP/端口剥离/畸形 CIDR
// 整条忽略）、HTTP CONNECT 隧道双向回环（上游 finally-close 缺陷的回
// 归钉）、网络过滤 403/畸形主机、明文 HTTP 转发（头透传/逐跳剥离/体转
// 发）、filterRequest 回调（拒/panic 容错）、父代理路由（CONNECT 隧道
// 早发数据不丢、绝对 URI 转发、NO_PROXY 回环段绕行直连）、配置活切换、
// SOCKS5 三 atyp 回环与拒绝码、Manager Start/StopProxyServers 接线与
// 父代理装配（显式配置/环境变量/都无）。
package sandbox_test

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/crazy-airhead/aifei-go/sandbox"
)

// ---- 测试工具 ----

// unsetProxyEnv 摘除全部代理环境变量（恢复挂 Cleanup）——resolver 的
// 环境探测链必须可控（开发者 shell 常设 HTTP_PROXY）。
func unsetProxyEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "NO_PROXY", "no_proxy"} {
		old, had := os.LookupEnv(k)
		os.Unsetenv(k)
		t.Cleanup(func() {
			if had {
				os.Setenv(k, old)
			} else {
				os.Unsetenv(k)
			}
		})
	}
}

func readTestLine(br *bufio.Reader) (string, error) {
	line, err := br.ReadString('\n')
	return strings.TrimRight(line, "\r\n"), err
}

// startEcho 本地 TCP 回显服务。
func startEcho(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				io.Copy(c, c)
			}()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}

// startMiniParent 充当父代理：CONNECT → 回 200 并先推 "welcome"（钉住
// 早发数据不丢）再转回显；其余请求（绝对 URI）→ 回固定响应。请求行经
// got 通道回放供断言。
func startMiniParent(t *testing.T) (int, chan string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 8)
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				line, err := readTestLine(br)
				if err != nil {
					return
				}
				for {
					h, err := readTestLine(br)
					if err != nil || h == "" {
						break
					}
				}
				got <- line
				if strings.HasPrefix(line, "CONNECT") {
					// 200 + 头 + 早发数据同一批写出——父代理应答的预读
					// 字节必须并回隧道（bufferedConn）
					c.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\nwelcome"))
					io.Copy(c, c)
				} else {
					c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nhello"))
				}
			}(c)
		}
	}()
	return l.Addr().(*net.TCPAddr).Port, got
}

// allowLocalConfig 放行 127.0.0.1 的过滤配置。
func allowLocalConfig() *sandbox.RuntimeConfig {
	return &sandbox.RuntimeConfig{
		Network: &sandbox.NetworkConfig{AllowedDomains: []string{"127.0.0.1"}},
	}
}

// httpConnect 手工 CONNECT 握手；返回连接 + 已越过响应头的 reader。
func httpConnect(t *testing.T, proxyPort int, hostport string) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(proxyPort))
	if err != nil {
		t.Fatal(err)
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", hostport, hostport)
	br := bufio.NewReader(conn)
	status, err := readTestLine(br)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "200") {
		t.Fatalf("CONNECT status = %q", status)
	}
	for {
		h, err := readTestLine(br)
		if err != nil || h == "" {
			break
		}
	}
	return conn, br
}

// readHTTPResponse 读完整响应（状态行/头/定长体）。
func readHTTPResponse(t *testing.T, br *bufio.Reader) (string, map[string]string, string) {
	t.Helper()
	status, err := readTestLine(br)
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	headers := map[string]string{}
	for {
		h, err := readTestLine(br)
		if err != nil {
			t.Fatalf("read header: %v", err)
		}
		if h == "" {
			break
		}
		if ci := strings.Index(h, ":"); ci > 0 {
			headers[strings.ToLower(strings.TrimSpace(h[:ci]))] = strings.TrimSpace(h[ci+1:])
		}
	}
	body := ""
	if cl := headers["content-length"]; cl != "" {
		n, _ := strconv.Atoi(cl)
		buf := make([]byte, n)
		if _, err := io.ReadFull(br, buf); err != nil {
			t.Fatalf("read body: %v", err)
		}
		body = string(buf)
	} else {
		// 无定长（403 文案等）：对端写完即关连接，读到 EOF
		rest, _ := io.ReadAll(br)
		body = string(rest)
	}
	return status, headers, body
}

// ---- ParentProxyResolver ----

func TestParentProxyResolution(t *testing.T) {
	unsetProxyEnv(t)

	// 显式配置全链
	r := sandbox.NewParentProxyResolver(&sandbox.ParentProxyConfig{
		HTTP: "proxy.corp:8080", HTTPS: "https://secure.proxy:8443", NoProxy: "a.com",
	})
	if r.HTTPProxyURL() != "http://proxy.corp:8080" {
		t.Errorf("http url = %q", r.HTTPProxyURL())
	}
	if r.HTTPSProxyURL() != "https://secure.proxy:8443" {
		t.Errorf("https url = %q", r.HTTPSProxyURL())
	}

	// schemeless 补 http://（curl 同款）；https 缺省回落 http 值
	r = sandbox.NewParentProxyResolver(&sandbox.ParentProxyConfig{HTTP: "p:3128"})
	if r.HTTPProxyURL() != "http://p:3128" || r.HTTPSProxyURL() != "http://p:3128" {
		t.Errorf("schemeless/fallback = %q %q", r.HTTPProxyURL(), r.HTTPSProxyURL())
	}

	// 非法/不支持 scheme → 空串
	for _, raw := range []string{"socks5://x:1080", "http://", "://nop", "http://[bad"} {
		if got := sandbox.NewParentProxyResolver(&sandbox.ParentProxyConfig{HTTP: raw}).HTTPProxyURL(); got != "" {
			t.Errorf("invalid %q resolved to %q", raw, got)
		}
	}

	// 环境变量链：显式配置优先；缺省 HTTP_PROXY → https 回落 http
	t.Setenv("HTTP_PROXY", "http://envh:1")
	r = sandbox.NewParentProxyResolver(&sandbox.ParentProxyConfig{HTTPS: "https://s:2"})
	if r.HTTPProxyURL() != "http://envh:1" {
		t.Errorf("env http = %q", r.HTTPProxyURL())
	}
	if r.HTTPSProxyURL() != "https://s:2" {
		t.Errorf("explicit https = %q", r.HTTPSProxyURL())
	}
	r = sandbox.NewParentProxyResolver(nil)
	if r.HTTPProxyURL() != "http://envh:1" || r.HTTPSProxyURL() != "http://envh:1" {
		t.Errorf("env fallback = %q %q", r.HTTPProxyURL(), r.HTTPSProxyURL())
	}
}

func TestParentProxySelectURL(t *testing.T) {
	unsetProxyEnv(t)
	r := sandbox.NewParentProxyResolver(&sandbox.ParentProxyConfig{HTTP: "http://h:1", HTTPS: "https://s:2"})
	if r.SelectParentProxyURL(true) != "https://s:2" || r.SelectParentProxyURL(false) != "http://h:1" {
		t.Errorf("select both = %q %q", r.SelectParentProxyURL(true), r.SelectParentProxyURL(false))
	}
	// 仅 https 配置：HTTPS 目标走之；明文 HTTP 目标不回落（直连）
	r = sandbox.NewParentProxyResolver(&sandbox.ParentProxyConfig{HTTPS: "https://s:2"})
	if r.SelectParentProxyURL(true) != "https://s:2" || r.SelectParentProxyURL(false) != "" {
		t.Errorf("select https-only = %q %q", r.SelectParentProxyURL(true), r.SelectParentProxyURL(false))
	}
}

func TestParentProxyBypass(t *testing.T) {
	unsetProxyEnv(t)
	r := sandbox.NewParentProxyResolver(&sandbox.ParentProxyConfig{
		HTTP:    "http://proxy:8080",
		NoProxy: "10.0.0.0/8, .example.com, svc:9090, [2001:db8::1]:443, 192.168.1.1, bad.cidr/xx, *.wild.com",
	})

	cases := []struct {
		host string
		want bool
	}{
		// 回环/localhost 恒直连（与 NO_PROXY 无关）
		{"localhost", true}, {"LOCALHOST", true},
		{"127.0.0.1", true}, {"127.5.4.3", true},
		{"::1", true}, {"::ffff:127.3.2.1", true},
		// 无规则命中
		{"example.org", false}, {"proxy", false},
		// CIDR
		{"10.1.2.3", true}, {"10.255.0.1", true}, {"11.0.0.1", false},
		// 点前缀后缀：子域与裸域都匹配
		{"sub.example.com", true}, {"example.com", true}, {"other.com", false},
		// 尾点规范化
		{"example.com.", true},
		// 端口剥离后缀
		{"svc", true}, {"svc.other", false},
		// 括号 IPv6 裸 IP → /128
		{"2001:db8::1", true}, {"2001:db8::2", false},
		// 裸 IPv4 → /32
		{"192.168.1.1", true}, {"192.168.1.2", false},
		// 畸形 CIDR 整条忽略（不当后缀用）
		{"bad.cidr", false},
		// *.wild.com → .wild.com 语义
		{"a.wild.com", true}, {"wild.com", true}, {"xwild.com", false},
	}
	for _, c := range cases {
		if got := r.ShouldBypassProxy(c.host, 443); got != c.want {
			t.Errorf("ShouldBypassProxy(%q) = %v, want %v", c.host, got, c.want)
		}
	}

	// NO_PROXY=* 全直连；无 NO_PROXY 时仅回环直连
	all := sandbox.NewParentProxyResolver(&sandbox.ParentProxyConfig{NoProxy: "*"})
	if !all.ShouldBypassProxy("anything.example", 80) {
		t.Error("NO_PROXY=* must bypass everything")
	}
	none := sandbox.NewParentProxyResolver(&sandbox.ParentProxyConfig{HTTP: "http://p:1"})
	if none.ShouldBypassProxy("example.org", 80) || !none.ShouldBypassProxy("127.0.0.1", 80) {
		t.Error("no rules: only loopback bypasses")
	}
}

// ---- HTTP 代理 ----

func TestHTTPProxyConnectTunnel(t *testing.T) {
	unsetProxyEnv(t)
	echoPort := startEcho(t)
	srv := sandbox.NewHTTPProxyServer(allowLocalConfig(), nil, nil, nil)
	port, err := srv.Start(0)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	conn, br := httpConnect(t, port, "127.0.0.1:"+strconv.Itoa(echoPort))
	defer conn.Close()
	// 双向回环（Java 版 finally-close 缺陷下此处即刻断流——回归钉）
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(br, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("echo = %q err=%v", buf, err)
	}
}

func TestHTTPProxyConnectBlocked(t *testing.T) {
	unsetProxyEnv(t)
	// AllowedDomains 空非 nil = 全拒
	srv := sandbox.NewHTTPProxyServer(&sandbox.RuntimeConfig{
		Network: &sandbox.NetworkConfig{AllowedDomains: []string{}},
	}, nil, nil, nil)
	port, err := srv.Start(0)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(conn, "CONNECT 127.0.0.1:1 HTTP/1.1\r\nHost: 127.0.0.1:1\r\n\r\n")
	status, headers, body := readHTTPResponse(t, bufio.NewReader(conn))
	if !strings.Contains(status, "403") || headers["x-proxy-error"] != "blocked-by-sandbox-runtime" {
		t.Fatalf("blocked CONNECT = %q %v", status, headers)
	}
	if !strings.Contains(body, "blocked by network policy") {
		t.Errorf("blocked body = %q", body)
	}
}

func TestHTTPProxyConnectInvalidHost(t *testing.T) {
	unsetProxyEnv(t)
	srv := sandbox.NewHTTPProxyServer(allowLocalConfig(), nil, nil, nil)
	port, err := srv.Start(0)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(conn, "CONNECT bad~host:443 HTTP/1.1\r\nHost: bad~host:443\r\n\r\n")
	status, _, body := readHTTPResponse(t, bufio.NewReader(conn))
	if !strings.Contains(status, "403") || !strings.Contains(body, "invalid host") {
		t.Fatalf("invalid host = %q %q", status, body)
	}
}

func TestHTTPProxyPlainForward(t *testing.T) {
	unsetProxyEnv(t)
	var gotReq chan *http.Request = make(chan *http.Request, 2)
	var gotBody chan string = make(chan string, 2)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotReq <- r
		gotBody <- string(b)
		w.Header().Set("X-Resp", "from-upstream")
		w.WriteHeader(200)
		io.WriteString(w, "upstream-body")
	}))
	defer up.Close()

	srv := sandbox.NewHTTPProxyServer(allowLocalConfig(), nil, nil, nil)
	port, err := srv.Start(0)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	// GET：绝对 URI 请求行；Connection 点名的 X-Drop-Me 逐跳剥离
	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(conn, "GET %s/path?q=1 HTTP/1.1\r\nHost: x\r\nX-Test: v\r\nX-Drop-Me: y\r\nConnection: X-Drop-Me\r\n\r\n", up.URL)
	status, headers, body := readHTTPResponse(t, bufio.NewReader(conn))
	conn.Close()

	if !strings.Contains(status, "200") || body != "upstream-body" || headers["x-resp"] != "from-upstream" {
		t.Fatalf("GET forward = %q %v %q", status, headers, body)
	}
	req := <-gotReq
	if req.Method != "GET" || req.URL.Path != "/path" || req.URL.RawQuery != "q=1" {
		t.Errorf("upstream request = %s %s?%s", req.Method, req.URL.Path, req.URL.RawQuery)
	}
	if req.Header.Get("X-Test") != "v" {
		t.Errorf("X-Test not forwarded: %v", req.Header)
	}
	if req.Header.Get("X-Drop-Me") != "" {
		t.Errorf("connection-named header must be stripped: %v", req.Header)
	}
	if host, _, _ := net.SplitHostPort(up.Listener.Addr().String()); !strings.HasPrefix(req.Host, host+":") {
		t.Errorf("Host header = %q, want target host:port", req.Host)
	}
	if b := <-gotBody; b != "" {
		t.Errorf("GET body = %q", b)
	}

	// POST：Content-Length 精确体转发
	conn, err = net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(conn, "POST %s/submit HTTP/1.1\r\nHost: x\r\nContent-Length: 10\r\n\r\nhello post", up.URL)
	status, _, body = readHTTPResponse(t, bufio.NewReader(conn))
	conn.Close()
	if !strings.Contains(status, "200") || body != "upstream-body" {
		t.Fatalf("POST forward = %q %q", status, body)
	}
	if b := <-gotBody; b != "hello post" {
		t.Errorf("POST body = %q", b)
	}
}

func TestHTTPProxyFilterRequest(t *testing.T) {
	unsetProxyEnv(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer up.Close()

	var seen []string
	srv := sandbox.NewHTTPProxyServer(allowLocalConfig(), nil, nil, func(method, url string, headers map[string]string) bool {
		seen = append(seen, method+" "+url)
		if method == "POST" {
			return false
		}
		if strings.Contains(url, "/boom") {
			panic("filter exploded")
		}
		return true
	})
	port, err := srv.Start(0)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	do := func(request string) (string, map[string]string, string) {
		conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		fmt.Fprint(conn, request)
		return readHTTPResponse(t, bufio.NewReader(conn))
	}

	// GET 放行
	status, _, body := do(fmt.Sprintf("GET %s/a HTTP/1.1\r\nHost: x\r\n\r\n", up.URL))
	if !strings.Contains(status, "200") || body != "ok" {
		t.Fatalf("allowed GET = %q %q", status, body)
	}
	// POST 拒
	status, _, body = do(fmt.Sprintf("POST %s/b HTTP/1.1\r\nHost: x\r\n\r\n", up.URL))
	if !strings.Contains(status, "403") {
		t.Fatalf("denied POST = %q", status)
	}
	// panic → fail-closed 拒
	status, _, _ = do(fmt.Sprintf("GET %s/boom HTTP/1.1\r\nHost: x\r\n\r\n", up.URL))
	if !strings.Contains(status, "403") {
		t.Fatalf("panicking filter must deny = %q", status)
	}

	// CONNECT 也过 filter（合成 connect:// URL）
	echoPort := startEcho(t)
	conn, br := httpConnect(t, port, "127.0.0.1:"+strconv.Itoa(echoPort))
	conn.Close()
	_ = br
	foundConnect := false
	for _, s := range seen {
		if strings.HasPrefix(s, "CONNECT connect://127.0.0.1:") {
			foundConnect = true
		}
	}
	if !foundConnect {
		t.Errorf("filter never saw CONNECT: %v", seen)
	}
}

func TestHTTPProxyParentRoute(t *testing.T) {
	unsetProxyEnv(t)
	parentPort, got := startMiniParent(t)
	parent := sandbox.NewParentProxyResolver(&sandbox.ParentProxyConfig{
		HTTP: fmt.Sprintf("http://127.0.0.1:%d", parentPort),
	})
	cfg := &sandbox.RuntimeConfig{
		Network: &sandbox.NetworkConfig{AllowedDomains: []string{"example.org", "127.0.0.1"}},
	}
	srv := sandbox.NewHTTPProxyServer(cfg, nil, parent, nil)
	port, err := srv.Start(0)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	// CONNECT example.org → 经父代理（mini parent 回 200 + 早发 welcome + 回显）
	conn, br := httpConnect(t, port, "example.org:443")
	defer conn.Close()
	welcome := make([]byte, 7)
	if _, err := io.ReadFull(br, welcome); err != nil || string(welcome) != "welcome" {
		t.Fatalf("early data from parent = %q err=%v", welcome, err)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(br, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("echo via parent = %q err=%v", buf, err)
	}
	select {
	case line := <-got:
		if !strings.HasPrefix(line, "CONNECT example.org:443 ") {
			t.Errorf("parent saw %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("parent never received CONNECT")
	}

	// 明文 HTTP 经父代理：请求行须为绝对 URI
	hconn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	hconn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprint(hconn, "GET http://example.org:81/path?q=1 HTTP/1.1\r\nHost: example.org:81\r\n\r\n")
	status, _, body := readHTTPResponse(t, bufio.NewReader(hconn))
	hconn.Close()
	if !strings.Contains(status, "200") || body != "hello" {
		t.Fatalf("via-parent GET = %q %q", status, body)
	}
	select {
	case line := <-got:
		if line != "GET http://example.org:81/path?q=1 HTTP/1.1" {
			t.Errorf("parent request line = %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("parent never received GET")
	}

	// NO_PROXY 回环段：127.0.0.1 目标绕行直连（父代理不可见）
	echoPort := startEcho(t)
	conn2, br2 := httpConnect(t, port, "127.0.0.1:"+strconv.Itoa(echoPort))
	if _, err := conn2.Write([]byte("direct")); err != nil {
		t.Fatal(err)
	}
	buf = make([]byte, 6)
	if _, err := io.ReadFull(br2, buf); err != nil || string(buf) != "direct" {
		t.Fatalf("direct echo = %q err=%v", buf, err)
	}
	conn2.Close()
	select {
	case line := <-got:
		t.Fatalf("loopback target must bypass parent, parent saw %q", line)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestHTTPProxyLiveConfig(t *testing.T) {
	unsetProxyEnv(t)
	echoPort := startEcho(t)
	deny := &sandbox.RuntimeConfig{Network: &sandbox.NetworkConfig{AllowedDomains: []string{}}}
	allow := allowLocalConfig()
	current := deny
	srv := sandbox.NewHTTPProxyServerFunc(func() *sandbox.RuntimeConfig { return current }, nil, nil, nil)
	port, err := srv.Start(0)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	// 初始全拒
	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(conn, "CONNECT 127.0.0.1:%d HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n", echoPort)
	status, _, _ := readHTTPResponse(t, bufio.NewReader(conn))
	conn.Close()
	if !strings.Contains(status, "403") {
		t.Fatalf("initial config must deny: %q", status)
	}

	// 换配置即生效（不重启）
	current = allow
	tun, br := httpConnect(t, port, "127.0.0.1:"+strconv.Itoa(echoPort))
	defer tun.Close()
	if _, err := tun.Write([]byte("now")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 3)
	if _, err := io.ReadFull(br, buf); err != nil || string(buf) != "now" {
		t.Fatalf("echo after live update = %q err=%v", buf, err)
	}
}

// ---- SOCKS5 代理 ----

// socksRequest 完整握手 + 请求；返回连接/reader/应答状态码。
func socksRequest(t *testing.T, proxyPort int, atyp byte, host string, v6 net.IP, v4 [4]byte, port int) (net.Conn, *bufio.Reader, byte) {
	t.Helper()
	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(proxyPort))
	if err != nil {
		t.Fatal(err)
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	conn.Write([]byte{0x05, 0x01, 0x00})
	ver := make([]byte, 2)
	if _, err := io.ReadFull(conn, ver); err != nil || ver[0] != 5 || ver[1] != 0 {
		t.Fatalf("greeting reply = %v err=%v", ver, err)
	}

	var req []byte
	req = append(req, 0x05, 0x01, 0x00, atyp)
	switch atyp {
	case 0x01:
		req = append(req, v4[0], v4[1], v4[2], v4[3])
	case 0x03:
		req = append(req, byte(len(host)))
		req = append(req, host...)
	case 0x04:
		req = append(req, v6.To16()...)
	}
	req = append(req, byte(port>>8), byte(port))
	conn.Write(req)

	reply := make([]byte, 10)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("reply: %v", err)
	}
	return conn, bufio.NewReader(conn), reply[1]
}

func TestSocksProxyRoundtrip(t *testing.T) {
	unsetProxyEnv(t)
	echoPort := startEcho(t)
	srv := sandbox.NewSocksProxyServer(allowLocalConfig(), nil, nil)
	port, err := srv.Start(0)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	// 域名 atyp（IP 字符串形态）
	conn, br, status := socksRequest(t, port, 0x03, "127.0.0.1", nil, [4]byte{}, echoPort)
	if status != 0x00 {
		t.Fatalf("socks reply status = %#x", status)
	}
	if _, err := conn.Write([]byte("socks-ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 10)
	if _, err := io.ReadFull(br, buf); err != nil || string(buf) != "socks-ping" {
		t.Fatalf("echo = %q err=%v", buf, err)
	}
	conn.Close()

	// IPv4 atyp
	conn, br, status = socksRequest(t, port, 0x01, "", nil, [4]byte{127, 0, 0, 1}, echoPort)
	if status != 0x00 {
		t.Fatalf("ipv4 reply status = %#x", status)
	}
	if _, err := conn.Write([]byte("v4")); err != nil {
		t.Fatal(err)
	}
	buf = make([]byte, 2)
	if _, err := io.ReadFull(br, buf); err != nil {
		t.Fatal(err)
	}
	conn.Close()

	// 不支持的 atyp → 0x08
	_, _, status = socksRequest(t, port, 0x05, "", nil, [4]byte{}, 80)
	if status != 0x08 {
		t.Fatalf("unsupported atyp status = %#x", status)
	}
}

func TestSocksProxyBlocked(t *testing.T) {
	unsetProxyEnv(t)
	srv := sandbox.NewSocksProxyServer(&sandbox.RuntimeConfig{
		Network: &sandbox.NetworkConfig{AllowedDomains: []string{}},
	}, nil, nil)
	port, err := srv.Start(0)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	_, _, status := socksRequest(t, port, 0x03, "127.0.0.1", nil, [4]byte{}, 80)
	if status != 0x02 { // connection not allowed
		t.Fatalf("blocked reply status = %#x", status)
	}
}

func TestSocksProxyParentRoute(t *testing.T) {
	unsetProxyEnv(t)
	parentPort, got := startMiniParent(t)
	parent := sandbox.NewParentProxyResolver(&sandbox.ParentProxyConfig{
		HTTP: fmt.Sprintf("http://127.0.0.1:%d", parentPort),
	})
	cfg := &sandbox.RuntimeConfig{
		Network: &sandbox.NetworkConfig{AllowedDomains: []string{"example.org"}},
	}
	srv := sandbox.NewSocksProxyServer(cfg, nil, parent)
	port, err := srv.Start(0)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	// 经父代理 CONNECT 隧道：welcome 早发数据 + 回显
	conn, br, status := socksRequest(t, port, 0x03, "example.org", nil, [4]byte{}, 443)
	if status != 0x00 {
		t.Fatalf("reply status = %#x", status)
	}
	welcome := make([]byte, 7)
	if _, err := io.ReadFull(br, welcome); err != nil || string(welcome) != "welcome" {
		t.Fatalf("early data = %q err=%v", welcome, err)
	}
	if _, err := conn.Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2)
	if _, err := io.ReadFull(br, buf); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	select {
	case line := <-got:
		if !strings.HasPrefix(line, "CONNECT example.org:443 ") {
			t.Errorf("parent saw %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("parent never received CONNECT")
	}
}

// ---- Manager 接线 ----

func TestManagerStartStopProxyServers(t *testing.T) {
	unsetProxyEnv(t)
	echoPort := startEcho(t)
	m, err := sandbox.New(sandbox.RuntimeConfig{
		Network: &sandbox.NetworkConfig{AllowedDomains: []string{"127.0.0.1"}},
	})
	if err != nil {
		t.Skipf("sandbox deps unavailable: %v", err)
	}
	defer m.Reset()

	httpPort, socksPort, err := m.StartProxyServers()
	if err != nil {
		t.Fatal(err)
	}
	if httpPort <= 0 || socksPort <= 0 {
		t.Fatalf("ports = %d %d", httpPort, socksPort)
	}
	if p := m.ProxyPort(); p == nil || *p != httpPort {
		t.Fatalf("ProxyPort() = %v", p)
	}
	if p := m.SocksProxyPort(); p == nil || *p != socksPort {
		t.Fatalf("SocksProxyPort() = %v", p)
	}

	// 幂等
	h2, s2, err := m.StartProxyServers()
	if err != nil || h2 != httpPort || s2 != socksPort {
		t.Fatalf("idempotent start = %d %d %v", h2, s2, err)
	}

	// 双代理各自可用
	tun, _ := httpConnect(t, httpPort, "127.0.0.1:"+strconv.Itoa(echoPort))
	tun.Write([]byte("m"))
	buf := make([]byte, 1)
	io.ReadFull(tun, buf)
	tun.Close()
	if string(buf) != "m" {
		t.Fatalf("http echo = %q", buf)
	}

	conn, br, status := socksRequest(t, socksPort, 0x03, "127.0.0.1", nil, [4]byte{}, echoPort)
	if status != 0x00 {
		t.Fatalf("socks status = %#x", status)
	}
	conn.Write([]byte("s"))
	io.ReadFull(br, buf)
	conn.Close()
	if string(buf) != "s" {
		t.Fatalf("socks echo = %q", buf)
	}

	// Stop 后端口清空、监听关闭
	m.StopProxyServers()
	if m.ProxyPort() != nil || m.SocksProxyPort() != nil {
		t.Fatal("ports must be cleared after stop")
	}
	if c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(httpPort)); err == nil {
		c.Close()
		t.Fatal("http proxy must be closed after stop")
	}
}

func TestManagerParentProxyWiring(t *testing.T) {
	unsetProxyEnv(t)
	// 显式配置
	m, err := sandbox.New(sandbox.RuntimeConfig{
		Network: &sandbox.NetworkConfig{ParentProxy: &sandbox.ParentProxyConfig{HTTP: "http://p:8080"}},
	})
	if err != nil {
		t.Skipf("sandbox deps unavailable: %v", err)
	}
	if r := m.ParentProxyResolver(); r == nil || r.HTTPProxyURL() != "http://p:8080" {
		t.Fatalf("explicit wiring = %v", m.ParentProxyResolver())
	}
	m.Reset()

	// 环境变量
	t.Setenv("HTTP_PROXY", "http://envp:3128")
	m2, err := sandbox.New(sandbox.RuntimeConfig{})
	if err != nil {
		t.Skipf("sandbox deps unavailable: %v", err)
	}
	defer m2.Reset()
	if r := m2.ParentProxyResolver(); r == nil || r.HTTPProxyURL() != "http://envp:3128" {
		t.Fatalf("env wiring = %v", m2.ParentProxyResolver())
	}

	// 都无 → nil（t.Setenv 到用例结束才恢复——此处须显式清掉再构造）
	os.Unsetenv("HTTP_PROXY")
	m3, err := sandbox.New(sandbox.RuntimeConfig{})
	if err != nil {
		t.Skipf("sandbox deps unavailable: %v", err)
	}
	defer m3.Reset()
	if m3.ParentProxyResolver() != nil {
		t.Fatal("no config/env must yield nil resolver")
	}
}
