// proxy_socks.go SOCKS5 无鉴权代理（对标 SocksProxyServer.java）。cmd
// 字段忽略（Java 同款——BIND/UDP 请求也按 CONNECT 处理）；父代理路由
// 走 dialViaParent 的 HTTP CONNECT 隧道（Java Socket(Proxy.Type.HTTP)
// 内建行为的手工等价物）。直接分支不带拨号超时（Java new Socket(host,
// port) 同款无限阻塞；父代理分支 30s）。
package sandbox

import (
	"bufio"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"

	"github.com/crazy-airhead/aifei-go/log"
)

// errProxyAlreadyStarted 重复 Start。
var errProxyAlreadyStarted = errors.New("sandbox: proxy server already started")

// proxyBase 代理服务器公共生命周期：127.0.0.1 监听、每连接一 goroutine、
// Stop 关监听并断开在飞连接（Java executor.shutdownNow 只中断线程不关
// socket，依赖进程退出——Go 显式清场）。
type proxyBase struct {
	mu    sync.Mutex
	ln    net.Listener
	conns map[net.Conn]struct{}
	done  chan struct{}
}

func (b *proxyBase) start(port int, handle func(net.Conn)) (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return 0, err
	}
	b.mu.Lock()
	if b.ln != nil {
		b.mu.Unlock()
		ln.Close()
		return 0, errProxyAlreadyStarted
	}
	b.ln = ln
	b.conns = map[net.Conn]struct{}{}
	done := make(chan struct{})
	b.done = done
	b.mu.Unlock()

	go b.acceptLoop(ln, done, handle)
	return ln.Addr().(*net.TCPAddr).Port, nil
}

func (b *proxyBase) acceptLoop(ln net.Listener, done chan struct{}, handle func(net.Conn)) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-done: // Stop 关监听
				return
			default:
				log.Error("sandbox: proxy accept error: %v", err)
				continue
			}
		}
		b.mu.Lock()
		if b.conns == nil { // 已 Stop
			b.mu.Unlock()
			conn.Close()
			return
		}
		b.conns[conn] = struct{}{}
		b.mu.Unlock()

		go func() {
			defer func() {
				b.mu.Lock()
				delete(b.conns, conn)
				b.mu.Unlock()
			}()
			handle(conn)
		}()
	}
}

func (b *proxyBase) stop() {
	b.mu.Lock()
	ln := b.ln
	b.ln = nil
	conns := b.conns
	b.conns = nil
	done := b.done
	b.done = nil
	b.mu.Unlock()

	// 先关 done 再关监听：Accept 因关监听解阻塞时 done 必已关闭，
	// acceptLoop 直接退出（否则错误分支与 done 关闭之间自旋刷错误日志）。
	if done != nil {
		close(done)
	}
	if ln != nil {
		_ = ln.Close()
	}
	for c := range conns {
		_ = c.Close()
	}
}

// SocksProxyServer SOCKS5 无鉴权代理。
type SocksProxyServer struct {
	proxyBase
	config func() *RuntimeConfig
	ask    AskCallback
	parent *ParentProxyResolver
}

// NewSocksProxyServer 构造（配置固定引用）。
func NewSocksProxyServer(cfg *RuntimeConfig, ask AskCallback, parent *ParentProxyResolver) *SocksProxyServer {
	return NewSocksProxyServerFunc(func() *RuntimeConfig { return cfg }, ask, parent)
}

// NewSocksProxyServerFunc 构造（配置 provider 每请求读取——支持热更新）。
func NewSocksProxyServerFunc(getCfg func() *RuntimeConfig, ask AskCallback, parent *ParentProxyResolver) *SocksProxyServer {
	return &SocksProxyServer{config: getCfg, ask: ask, parent: parent}
}

// Start 监听 127.0.0.1:port（0 = 临时端口）并返回实际端口。
func (s *SocksProxyServer) Start(port int) (int, error) {
	return s.proxyBase.start(port, s.handleClient)
}

// Stop 关停（关监听 + 断在飞连接）。
func (s *SocksProxyServer) Stop() { s.proxyBase.stop() }

func (s *SocksProxyServer) handleClient(client net.Conn) {
	defer client.Close()
	in := bufio.NewReader(client)

	// SOCKS5 问候：版本 5，跳过方法列表，无需鉴权
	version, err := in.ReadByte()
	if err != nil || version != 5 {
		return
	}
	numMethods, err := in.ReadByte()
	if err != nil {
		return
	}
	for i := 0; i < int(numMethods); i++ {
		if _, err := in.ReadByte(); err != nil {
			return
		}
	}
	if _, err := client.Write([]byte{0x05, 0x00}); err != nil {
		return
	}

	// SOCKS5 请求：ver/cmd/rsv/atyp——cmd 不校验（Java 同款，一律按
	// CONNECT 处理）
	ver, _ := in.ReadByte()
	_ = ver
	cmd, _ := in.ReadByte()
	_ = cmd
	_, _ = in.ReadByte()
	atyp, err := in.ReadByte()
	if err != nil {
		return
	}

	var hostname string
	switch atyp {
	case 0x01: // IPv4
		b := make([]byte, 4)
		if _, err := io.ReadFull(in, b); err != nil {
			return
		}
		hostname = net.IP(b).String()
	case 0x03: // 域名
		l, err := in.ReadByte()
		if err != nil {
			return
		}
		b := make([]byte, l)
		if _, err := io.ReadFull(in, b); err != nil {
			return
		}
		hostname = string(b)
	case 0x04: // IPv6（Go 文本为压缩形，Java 为全展形——仅日志/匹配用，等价）
		b := make([]byte, 16)
		if _, err := io.ReadFull(in, b); err != nil {
			return
		}
		hostname = net.IP(b).String()
	default:
		s.sendReply(client, 0x08, 0) // address type not supported
		return
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(in, pb); err != nil {
		return
	}
	port := int(pb[0])<<8 | int(pb[1])

	if !IsValidHost(hostname) {
		log.Error("sandbox: denying SOCKS connection to malformed host %s", hostname)
		s.sendReply(client, 0x04, port) // host unreachable
		return
	}

	if !FilterNetworkRequest(port, hostname, s.config(), s.ask) {
		s.sendReply(client, 0x02, port) // connection not allowed
		return
	}

	var upstream net.Conn
	if s.parent != nil && s.parent.HTTPProxyURL() != "" && !s.parent.ShouldBypassProxy(hostname, port) {
		conn, err := dialViaParent(s.parent.HTTPProxyURL(),
			net.JoinHostPort(hostname, strconv.Itoa(port)))
		if err != nil {
			log.Error("sandbox: SOCKS parent proxy connect failed: %v", err)
			s.sendReply(client, 0x04, port)
			return
		}
		upstream = conn
	} else {
		conn, err := net.Dial("tcp", net.JoinHostPort(hostname, strconv.Itoa(port)))
		if err != nil {
			s.sendReply(client, 0x04, port)
			log.Error("sandbox: SOCKS connect failed: %v", err)
			return
		}
		upstream = conn
	}
	s.sendReply(client, 0x00, port) // success

	// 双向泵，双双退出后关两端（Java pipe finally 关闭同款）
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		pump(in, upstream)
	}()
	go func() {
		defer wg.Done()
		pump(upstream, client)
	}()
	wg.Wait()
	upstream.Close()
}

// sendReply SOCKS5 应答（对标 sendReply）：恒 atyp=0x01 + 0.0.0.0 绑定
// 地址 + 端口（Java 的 atyp/host 形参同样只进日志不进报文）。
func (s *SocksProxyServer) sendReply(out io.Writer, status byte, port int) {
	out.Write([]byte{0x05, status, 0x00, 0x01, 0, 0, 0, 0, byte(port >> 8), byte(port)})
}
