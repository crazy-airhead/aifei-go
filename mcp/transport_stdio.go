// transport_stdio.go stdio 传输（对标 StdioClientTransport）：拉起子进程，
// stdin/stdout 按行帧 JSON-RPC（帧内不得含裸换行），stderr 逐行透传日志。
// 重连语义：进程死亡即传输死亡（Receive 关闭）；客户端门面的传输工厂重建
// 整个传输（Java McpClientProvider.reset 后 buildClient 同款）。
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/crazy-airhead/aifei-go/log"
)

// stdioMaxLine 行帧上限（消息可能携带 base64 大载荷）。
const stdioMaxLine = 16 << 20 // 16MB

// StdioTransport 子进程 stdio 传输。Connect 拉起进程后方可作 Transport 使用。
type StdioTransport struct {
	name string
	args []string
	env  []string // 追加环境变量（"K=V"）

	stderrLine func(line string) // stderr 透传钩子（默认 log.Info）

	cmd      *exec.Cmd
	stdin    io.WriteCloser
	recv     chan Message
	writeMu  sync.Mutex
	isClosed atomic.Bool
}

// NewStdioTransport 构造 stdio 传输（未启动）。
func NewStdioTransport(name string, args ...string) *StdioTransport {
	return &StdioTransport{
		name:       name,
		args:       args,
		stderrLine: func(line string) { log.Info("mcp: [stdio-stderr] %s", line) },
	}
}

// StdioEnv 追加子进程环境变量（默认仅继承 os.Environ）。
func (t *StdioTransport) StdioEnv(env map[string]string) *StdioTransport {
	for k, v := range env {
		t.env = append(t.env, k+"="+v)
	}
	return t
}

// StdioStderr 设置 stderr 逐行处理钩子。
func (t *StdioTransport) StdioStderr(fn func(line string)) *StdioTransport {
	if fn != nil {
		t.stderrLine = fn
	}
	return t
}

// Connect 拉起子进程并启动读泵（Java connect：spawn + inbound/outbound/error 三线）。
func (t *StdioTransport) Connect(_ context.Context) error {
	t.cmd = exec.Command(t.name, t.args...)
	t.cmd.Env = append(t.cmd.Environ(), t.env...)

	stdout, err := t.cmd.StdoutPipe()
	if err != nil {
		return &TransportError{Err: err}
	}
	stderr, err := t.cmd.StderrPipe()
	if err != nil {
		return &TransportError{Err: err}
	}
	stdin, err := t.cmd.StdinPipe()
	if err != nil {
		return &TransportError{Err: err}
	}
	if err := t.cmd.Start(); err != nil {
		return &TransportError{Err: err}
	}

	t.stdin = stdin
	t.recv = make(chan Message, 64)
	go t.readStdout(stdout)
	go t.readStderr(stderr)
	log.Debug("mcp: stdio server started: %s", strings.Join(t.cmd.Args, " "))
	return nil
}

// readStdout stdout 读泵：逐行解 JSON-RPC → 入站通道；EOF/错误即传输终止。
func (t *StdioTransport) readStdout(r io.Reader) {
	defer close(t.recv)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), stdioMaxLine)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		msg, err := UnmarshalMessage([]byte(line))
		if err != nil {
			if t.isClosed.Load() {
				return
			}
			log.Error("mcp: stdio inbound parse error for line: %s: %v", line, err)
			return
		}
		t.recv <- msg
	}
	if err := sc.Err(); err != nil && !t.isClosed.Load() {
		log.Error("mcp: stdio read error: %v", err)
	}
}

// readStderr stderr 泵：逐行透传（Java errorSink + stdErrorHandler）。
func (t *StdioTransport) readStderr(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), stdioMaxLine)
	for sc.Scan() {
		if t.stderrLine != nil {
			t.stderrLine(sc.Text())
		}
	}
}

// Send 实现 Transport：写一行 JSON（规范：帧内不得含裸换行；json.Marshal 已
// 转义字符串内换行，此处再兜底替换）。
func (t *StdioTransport) Send(_ context.Context, msg Message) error {
	if t.isClosed.Load() || t.stdin == nil {
		return &TransportError{Err: ErrSessionClosed}
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return &TransportError{Err: err}
	}
	s := strings.ReplaceAll(string(b), "\r\n", "\\n")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\n")

	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if _, err := io.WriteString(t.stdin, s+"\n"); err != nil {
		return &TransportError{Err: err}
	}
	return nil
}

// Receive 实现 Transport：入站帧通道（关闭即传输终止）。
func (t *StdioTransport) Receive() <-chan Message { return t.recv }

// Close 实现 Transport：关 stdin（进程见 EOF）→ 限时等待退出 → 超时 Kill。
func (t *StdioTransport) Close() error {
	if t.isClosed.Swap(true) {
		return nil
	}
	if t.stdin != nil {
		_ = t.stdin.Close()
	}
	if t.cmd == nil || t.cmd.Process == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		_, _ = t.cmd.Process.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = t.cmd.Process.Kill()
		<-done
	}
	log.Debug("mcp: stdio server stopped")
	return nil
}

// StdioTransportFactory 重建工厂（客户端门面重连时重新拉起进程）。
func (t *StdioTransport) TransportFactory() func(context.Context) (Transport, error) {
	name, args, env := t.name, t.args, t.env
	return func(ctx context.Context) (Transport, error) {
		nt := NewStdioTransport(name, args...)
		nt.env = env
		if err := nt.Connect(ctx); err != nil {
			return nil, err
		}
		return nt, nil
	}
}
