// Package lsp LSP 客户端能力包（对标 solon-ai-talent-lsp 的最小面：jsonrpc
// over stdio（Content-Length 帧层自实现——复用 mcp 帧层思路但独立小实现）、
// initialize/initialized/shutdown 生命周期、didOpen/didChange 文件同步、
// publishDiagnostics 回调、单 lsp 工具操作族；诊断主路径 reportFileDiagnostics
// 供 Terminal 的 fileDiagnosticsHook 接线）。落 plugins（exec + jsonrpc）。
package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	ai "github.com/crazy-airhead/aifei-go/ai"
)

// ServerParams 服务器参数（command + 扩展名路由）。
type ServerParams struct {
	Name       string
	Command    []string
	Extensions []string
	Env        map[string]string
	Enabled    bool
}

// DefaultServers 内置 14 种服务器的命令面（jdtls/typescript/go/python/rust/
// c-cpp/csharp/ruby/php/bash/lua/dart/swift/kotlin——安装存在性由启动探针判定）。
func DefaultServers() []ServerParams {
	mk := func(name string, cmd []string, exts ...string) ServerParams {
		return ServerParams{Name: name, Command: cmd, Extensions: exts, Enabled: true}
	}
	return []ServerParams{
		mk("typescript", []string{"typescript-language-server", "--stdio"}, ".ts", ".tsx", ".js", ".jsx"),
		mk("go", []string{"gopls"}, ".go"),
		mk("python", []string{"pyright-langserver", "--stdio"}, ".py"),
		mk("rust", []string{"rust-analyzer"}, ".rs"),
		mk("c-cpp", []string{"clangd", "--background-index"}, ".c", ".h", ".cpp", ".hpp", ".cc"),
		mk("ruby", []string{"solargraph", "stdio"}, ".rb"),
		mk("php", []string{"intelephense", "--stdio"}, ".php"),
		mk("bash", []string{"bash-language-server", "start"}, ".sh", ".bash"),
		mk("lua", []string{"lua-language-server"}, ".lua"),
		mk("dart", []string{"dart", "language-server", "--protocol=lsp"}, ".dart"),
		mk("swift", []string{"sourcekit-lsp"}, ".swift"),
		mk("kotlin", []string{"kotlin-language-server"}, ".kt", ".kts"),
		mk("yaml", []string{"yaml-language-server", "--stdio"}, ".yaml", ".yml"),
		mk("java", []string{"jdtls"}, ".java"),
	}
}

// Manager LSP 管理器（按扩展名路由 + 客户端缓存 + broken 名单）。
type Manager struct {
	mu       sync.Mutex
	workspace string
	servers  map[string]*ServerParams
	clients  map[string]*Client
	broken   map[string]bool
	onDiagnostics func(uri string, diags []Diagnostic)
}

// Diagnostic 诊断项。
type Diagnostic struct {
	Line      int    `json:"line"` // 0-based
	Character int    `json:"character"`
	Severity  int    `json:"severity"` // 1=Error 2=Warning
	Message   string `json:"message"`
}

// NewManager 构造。
func NewManager(workspace string) *Manager {
	return &Manager{
		workspace: workspace, servers: map[string]*ServerParams{},
		clients: map[string]*Client{}, broken: map[string]bool{},
	}
}

// RegisterServer 注册。
func (m *Manager) RegisterServer(p ServerParams) {
	cp := p
	if len(cp.Extensions) == 0 {
		// DefaultServers 显式给扩展名；无扩展名的按名匹配（.name）
		cp.Extensions = []string{"." + p.Name}
	}
	m.mu.Lock()
	m.servers[p.Name] = &cp
	m.mu.Unlock()
}

// SetDiagnosticsCallback 诊断回调。
func (m *Manager) SetDiagnosticsCallback(fn func(uri string, diags []Diagnostic)) {
	m.mu.Lock()
	m.onDiagnostics = fn
	m.mu.Unlock()
}

// HasServers 有可用服务器。
func (m *Manager) HasServers() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.servers) > 0
}

// clientForFile 扩展名路由。
func (m *Manager) clientForFile(path string) (*Client, error) {
	ext := strings.ToLower(filepath.Ext(path))
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.servers {
		if !s.Enabled {
			continue
		}
		for _, e := range s.Extensions {
			if strings.EqualFold(ext, e) {
				if m.broken[s.Name] {
					return nil, fmt.Errorf("lsp: server %s marked broken", s.Name)
				}
				if c, ok := m.clients[s.Name]; ok {
					return c, nil
				}
				c, err := StartClient(context.Background(), m.workspace, s, m.dispatchDiagnostics)
				if err != nil {
					m.broken[s.Name] = true
					return nil, fmt.Errorf("lsp: start %s failed: %w", s.Name, err)
				}
				m.clients[s.Name] = c
				return c, nil
			}
		}
	}
	return nil, fmt.Errorf("lsp: no server for %s", ext)
}

func (m *Manager) dispatchDiagnostics(uri string, diags []Diagnostic) {
	m.mu.Lock()
	fn := m.onDiagnostics
	m.mu.Unlock()
	if fn != nil {
		fn(uri, diags)
	}
}

// FileURI 文件 → file:// URI。
func FileURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return "file://" + abs
}

// ---- jsonrpc over stdio 客户端 ----

// Client 单服务器客户端。
type Client struct {
	params   *ServerParams
	cmd      *exec.Cmd
	stdin    *json.Encoder
	mu       sync.Mutex
	pending  map[string]chan json.RawMessage
	nextID   int
	docVersions map[string]int
}

// StartClient 启动 + initialize + initialized。
func StartClient(ctx context.Context, workspace string, params *ServerParams, onDiag func(string, []Diagnostic)) (*Client, error) {
	cmd := exec.Command(params.Command[0], params.Command[1:]...)
	if len(params.Command) == 1 {
		cmd = exec.Command(params.Command[0])
	}
	cmd.Env = append(os.Environ(), envOf(params.Env)...)
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &Client{
		params: params, cmd: cmd, pending: map[string]chan json.RawMessage{},
		docVersions: map[string]int{},
	}
	c.stdin = json.NewEncoder(frameWriter{w: stdinPipe})

	// 通知/响应读泵
	go c.readPump(stdoutPipe, onDiag)

	// initialize
	initResult, err := c.request(ctx, "initialize", map[string]any{
		"processId": os.Getpid(),
		"rootUri":   FileURI(workspace),
		"capabilities": map[string]any{},
	})
	if err != nil {
		cmd.Process.Kill()
		return nil, err
	}
	_ = initResult
	c.notify("initialized", map[string]any{})
	return c, nil
}

type frameWriter struct{ w interface{ Write([]byte) (int, error) } }

func (f frameWriter) Write(p []byte) (int, error) {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(p))
	if _, err := f.w.Write([]byte(header)); err != nil {
		return 0, err
	}
	return f.w.Write(p)
}

func (c *Client) readPump(stdout interface{ Read([]byte) (int, error) }, onDiag func(string, []Diagnostic)) {
	reader := bufio.NewReader(stdout)
	for {
		// 头部
		length := 0
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			if strings.HasPrefix(strings.ToLower(line), "content-length:") {
				fmt.Sscanf(line, "Content-Length: %d", &length)
			}
		}
		if length <= 0 || length > 16<<20 {
			continue
		}
		body := make([]byte, length)
		if _, err := readFull(reader, body); err != nil {
			return
		}
		var msg struct {
			ID     *json.Number     `json:"id"`
			Method string           `json:"method"`
			Params json.RawMessage  `json:"params"`
			Result json.RawMessage  `json:"result"`
		}
		if err := json.Unmarshal(body, &msg); err != nil {
			continue
		}
		if msg.ID != nil && msg.Method == "" {
			// 响应
			c.mu.Lock()
			ch, ok := c.pending[msg.ID.String()]
			c.mu.Unlock()
			if ok {
				ch <- msg.Result
			}
			continue
		}
		// 通知
		switch msg.Method {
		case "textDocument/publishDiagnostics":
			var params struct {
				URI         string       `json:"uri"`
				Diagnostics []Diagnostic `json:"diagnostics"`
			}
			if err := json.Unmarshal(msg.Params, &params); err == nil && onDiag != nil {
				onDiag(params.URI, params.Diagnostics)
			}
		}
	}
}

func readFull(reader *bufio.Reader, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := reader.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func envOf(env map[string]string) []string {
	var out []string
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

func (c *Client) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	idStr := fmt.Sprint(id)
	ch := make(chan json.RawMessage, 1)
	c.pending[idStr] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, idStr)
		c.mu.Unlock()
	}()

	msg := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	if err := c.stdin.Encode(msg); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-ch:
		return result, nil
	case <-time.After(15 * time.Second):
		return nil, fmt.Errorf("lsp: %s timeout", method)
	}
}

func (c *Client) notify(method string, params any) {
	msg := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	_ = c.stdin.Encode(msg)
}

// SyncFile 文件同步（首 didOpen、变化 didChange；force 强制版本变更）。
func (c *Client) SyncFile(path, content string, force bool) {
	uri := FileURI(path)
	c.mu.Lock()
	_, opened := c.docVersions[uri]
	c.docVersions[uri]++
	version := c.docVersions[uri]
	c.mu.Unlock()

	if !opened {
		c.notify("textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{"uri": uri, "languageId": langIDOf(path), "version": 1, "text": content},
		})
		return
	}
	c.notify("textDocument/didChange", map[string]any{
		"textDocument": map[string]any{"uri": uri, "version": version},
		"contentChanges": []map[string]any{{"text": content}},
	})
}

func langIDOf(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	return strings.TrimPrefix(ext, ".")
}

// ---- Talent ----

// Lsp 能力包。
type Lsp struct {
	ai.TalentBase
	manager    *Manager
	readFile   func(path string) (string, error)
}

// New 构造（readFile 缺省 os.ReadFile——测试可注入内存盘）。
func New(manager *Manager, readFile func(path string) (string, error)) *Lsp {
	if readFile == nil {
		readFile = func(p string) (string, error) {
			b, err := os.ReadFile(p)
			return string(b), err
		}
	}
	return &Lsp{manager: manager, readFile: readFile}
}

// Name 名称。
func (t *Lsp) Name() string { return "Lsp" }

// Description 描述。
func (t *Lsp) Description() string { return "LSP 语言服务客户端（定义跳转/引用/hover/诊断）" }

// IsSupported 有服务器才启用。
func (t *Lsp) IsSupported(*ai.Prompt) bool { return t.manager.HasServers() }

// Tools 单 lsp 工具（operation 分发）。
func (t *Lsp) Tools(*ai.Prompt) []ai.FunctionTool {
	return []ai.FunctionTool{ai.NewTool("lsp").
		Desc("LSP 操作：goToDefinition/findReferences/hover/goToImplementation/documentSymbol/workspaceSymbol").
		StringParam("operation", "操作名", true).
		StringParam("filePath", "文件路径（相对工作区）", true).
		IntParam("line", "行号（1-based，默认 1）", false).
		IntParam("character", "列号（1-based，默认 1）", false).
		Handler(t.handle).
		Build()}
}

func (t *Lsp) handle(ctx context.Context, args map[string]any) (any, error) {
	operation, _ := args["operation"].(string)
	filePath, _ := args["filePath"].(string)
	line := intOf(args["line"])
	if line < 1 {
		line = 1
	}
	character := intOf(args["character"])
	if character < 1 {
		character = 1
	}

	client, err := t.manager.clientForFile(filePath)
	if err != nil {
		return "LSP 服务不可用: " + err.Error(), nil
	}
	// 先同步文件
	content, err := t.readFile(filePath)
	if err == nil {
		client.SyncFile(filePath, content, false)
	}

	params := map[string]any{
		"textDocument": map[string]any{"uri": FileURI(filePath)},
		"position":     map[string]any{"line": line - 1, "character": character - 1},
	}
	var method string
	switch operation {
	case "goToDefinition":
		method = "textDocument/definition"
	case "findReferences":
		method = "textDocument/references"
		params["context"] = map[string]any{"includeDeclaration": true}
	case "hover":
		method = "textDocument/hover"
	case "goToImplementation":
		method = "textDocument/implementation"
	case "documentSymbol":
		method = "textDocument/documentSymbol"
		delete(params, "position")
	case "workspaceSymbol":
		method = "workspace/symbol"
		params = map[string]any{"query": ""}
	default:
		return "不支持的 operation: " + operation, nil
	}
	result, err := client.request(ctx, method, params)
	if err != nil {
		return "LSP 调用失败: " + err.Error(), nil
	}
	if len(result) == 0 || string(result) == "null" {
		return fmt.Sprintf("No results found for %s", operation), nil
	}
	return string(result), nil
}

func intOf(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}
