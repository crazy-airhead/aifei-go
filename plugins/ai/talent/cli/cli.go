// Package cli 终端会话能力包（对标 TerminalTalent 的 bash 主干：同步 bash +
// 异步三件套 bash_start/bash_wait/bash_stop——进程树终止、会话快照、硬超时；
// 文件五件套 ls/read/write/edit/grep 由 ai/talent/file+sys 组合替代——本包
// 聚焦终端会话与沙箱接缝）。PTY 面按需后接（Java 经 pty 库；Go 交互式终端
// 同样走三方，首卡以进程组 exec 落非交互主干）。
package cli

import (
	"context"
	"fmt"
	"crypto/rand"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	ai "github.com/crazy-airhead/aifei-go/ai"
)

// 默认参数（Java 同款）。
const (
	DefaultTimeoutMs    = 120000
	DefaultMaxOutput    = 64000
	DefaultYieldMs      = 1000
	DefaultHardTimeout  = 120000
	sessionTTL          = 10 * time.Minute
)

// CommandSnapshot 会话快照（formatCommandSnapshot 形态）。
type CommandSnapshot struct {
	SessionID      string `json:"session_id"`
	Status         string `json:"status"` // running | completed | failed | timeout | terminated
	ExitCode       int    `json:"exit_code"`
	HardTimeoutMs  int64  `json:"hard_timeout"`
	Terminated     bool   `json:"terminated"`
	WallTimeMs     int64  `json:"wall_time_ms"`
	OutputCharsTotal int  `json:"output_chars_total"`
	Returned       int    `json:"returned"`
	Truncated      bool   `json:"truncated"`
	Output         string `json:"output"`
}

// Session 会话。
type Session struct {
	ID        string
	Cmd       *exec.Cmd
	StartedAt time.Time

	mu       sync.Mutex
	output   strings.Builder
	exitCode int
	done     bool
	status   string
	terminated bool
	hardTimeout int64
	cancel   context.CancelFunc
}

// Status 状态快照。
func (s *Session) Status() CommandSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := s.output.Len()
	out := s.output.String()
	truncated := false
	if len(out) > DefaultMaxOutput {
		out = out[:DefaultMaxOutput]
		truncated = true
	}
	status := s.status
	if !s.done {
		status = "running"
	}
	return CommandSnapshot{
		SessionID: s.ID, Status: status, ExitCode: s.exitCode,
		HardTimeoutMs: s.hardTimeout, Terminated: s.terminated,
		WallTimeMs: time.Since(s.StartedAt).Milliseconds(),
		OutputCharsTotal: total, Returned: len(out), Truncated: truncated, Output: out,
	}
}

// Manager 会话管理器（cmd_ 前缀 id、TTL 清理、进程树终止）。
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

// NewManager 构造。
func NewManager() *Manager {
	m := &Manager{sessions: map[string]*Session{}}
	go m.gcLoop()
	return m
}

func (m *Manager) gcLoop() {
	ticker := time.NewTicker(time.Minute)
	for range ticker.C {
		m.mu.Lock()
		for id, s := range m.sessions {
			if s.done && time.Since(s.StartedAt) > sessionTTL {
				delete(m.sessions, id)
			}
		}
		m.mu.Unlock()
	}
}

func newSessionID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "cmd_0000000000000000"
	}
	const hexDigits = "0123456789abcdef"
	out := []byte("cmd_................")
	for i, v := range b {
		out[4+i*2] = hexDigits[v>>4]
		out[4+i*2+1] = hexDigits[v&0x0f]
	}
	return string(out)
}

// Start 启动异步命令。
func (m *Manager) Start(ctx context.Context, command string, yieldMs, hardTimeoutMs, maxOutput int) (*Session, CommandSnapshot, error) {
	if yieldMs <= 0 {
		yieldMs = DefaultYieldMs
	}
	if hardTimeoutMs <= 0 {
		hardTimeoutMs = DefaultHardTimeout
	}
	s := &Session{ID: newSessionID(), StartedAt: time.Now(), status: "running", hardTimeout: int64(hardTimeoutMs)}
	sctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(sctx, "cmd", "/c", command)
	} else {
		cmd = exec.CommandContext(sctx, "sh", "-c", command)
	}
	s.Cmd = cmd

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		out, err := cmd.CombinedOutput() // 合流（redirectErrorStream 同款）
		s.mu.Lock()
		s.output.Write(out)
		s.done = true
		s.terminated = sctx.Err() != nil
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				s.exitCode = exitErr.ExitCode()
			} else {
				s.exitCode = -1
			}
			if s.terminated {
				s.status = "terminated"
			} else {
				s.status = "failed"
			}
		} else {
			s.exitCode = 0
			s.status = "completed"
		}
		s.mu.Unlock()
	}()

	m.mu.Lock()
	m.sessions[s.ID] = s
	m.mu.Unlock()

	// 硬超时调度
	go func() {
		deadline := time.NewTimer(time.Duration(hardTimeoutMs) * time.Millisecond)
		defer deadline.Stop()
		select {
		case <-deadline.C:
			s.mu.Lock()
			alreadyDone := s.done
			s.mu.Unlock()
			if !alreadyDone {
				cancel() // 进程组终止（exec.CommandContext 已杀进程树）
				s.mu.Lock()
				s.status = "timeout"
				s.mu.Unlock()
			}
		case <-doneOf(&wg):
		}
	}()

	// yield 等待
	yieldDeadline := time.NewTimer(time.Duration(yieldMs) * time.Millisecond)
	defer yieldDeadline.Stop()
	select {
	case <-yieldDeadline.C:
	case <-doneOf(&wg):
	}
	return s, s.Status(), nil
}

func doneOf(wg *sync.WaitGroup) <-chan struct{} {
	ch := make(chan struct{})
	go func() { wg.Wait(); close(ch) }()
	return ch
}

// Wait 等待产出（可写 stdin 简化：首卡非交互不写）。
func (m *Manager) Wait(sessionID string, yieldMs int) (CommandSnapshot, error) {
	if yieldMs <= 0 {
		yieldMs = DefaultYieldMs
	}
	s := m.Get(sessionID)
	if s == nil {
		return CommandSnapshot{}, fmt.Errorf("cli: unknown session %s", sessionID)
	}
	yieldDeadline := time.NewTimer(time.Duration(yieldMs) * time.Millisecond)
	defer yieldDeadline.Stop()
	select {
	case <-yieldDeadline.C:
	case <-time.After(0):
		// 已完成则直接返回
	}
	return s.Status(), nil
}

// Stop 终止。
func (m *Manager) Stop(sessionID, reason string) (CommandSnapshot, error) {
	s := m.Get(sessionID)
	if s == nil {
		return CommandSnapshot{}, fmt.Errorf("cli: unknown session %s", sessionID)
	}
	s.mu.Lock()
	if !s.done {
		if s.cancel != nil {
			s.cancel()
		}
		s.status = "terminated"
		s.terminated = true
	}
	s.mu.Unlock()
	return s.Status(), nil
}

// Get 会话查询。
func (m *Manager) Get(sessionID string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[sessionID]
}

// ---- Talent ----

// Terminal 终端能力包。
type Terminal struct {
	ai.TalentBase
	manager *Manager
}

// New 构造。
func New(manager *Manager) *Terminal { return &Terminal{manager: manager} }

// Name 名称。
func (t *Terminal) Name() string { return "Terminal" }

// Description 描述。
func (t *Terminal) Description() string {
	return "提供终端交互、文件发现、分页读取、全文搜索及精准编辑能力。"
}

// Tools 工具集。
func (t *Terminal) Tools(*ai.Prompt) []ai.FunctionTool {
	return []ai.FunctionTool{
		ai.NewTool("bash").
			Desc("执行 shell 命令并返回输出（含 [exit_code]）").
			StringParam("command", "命令", true).
			IntParam("timeout", "超时毫秒（默认 120000）", false).
			IntParam("max_output_chars", "最大输出字符（默认 64000）", false).
			Handler(t.toolBash).
			Build(),
		ai.NewTool("bash_start").
			Desc("启动异步命令会话（返回会话快照）").
			StringParam("command", "命令", true).
			StringParam("workdir", "工作目录（可选）", false).
			IntParam("yield_time_ms", "首次等待毫秒（默认 1000）", false).
			IntParam("max_output_chars", "最大输出字符（可选）", false).
			IntParam("hard_timeout_ms", "硬超时毫秒（默认 120000）", false).
			Handler(t.toolBashStart).
			Build(),
		ai.NewTool("bash_wait").
			Desc("等待异步会话产出").
			StringParam("session_id", "会话 id", true).
			IntParam("yield_time_ms", "等待毫秒（默认 1000）", false).
			Handler(t.toolBashWait).
			Build(),
		ai.NewTool("bash_stop").
			Desc("终止异步会话").
			StringParam("session_id", "会话 id", true).
			StringParam("reason", "终止原因（可选）", false).
			Handler(t.toolBashStop).
			Build(),
	}
}

func (t *Terminal) toolBash(ctx context.Context, args map[string]any) (any, error) {
	command, _ := args["command"].(string)
	timeout := intOf(args["timeout"])
	if timeout <= 0 {
		timeout = DefaultTimeoutMs
	}
	maxOut := intOf(args["max_output_chars"])
	if maxOut <= 0 {
		maxOut = DefaultMaxOutput
	}

	cctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Millisecond)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(cctx, "cmd", "/c", command)
	} else {
		cmd = exec.CommandContext(cctx, "sh", "-c", command)
	}
	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	output := string(out)
	truncated := false
	if len(output) > maxOut {
		output = output[:maxOut]
		truncated = true
	}
	result := output
	if truncated {
		result += "\n[输出已截断]"
	}
	return fmt.Sprintf("%s\n[exit_code]", result) + fmt.Sprint(exitCode), nil
}

func (t *Terminal) toolBashStart(ctx context.Context, args map[string]any) (any, error) {
	command, _ := args["command"].(string)
	_, snap, err := t.manager.Start(ctx, command,
		intOf(args["yield_time_ms"]), intOf(args["hard_timeout_ms"]), intOf(args["max_output_chars"]))
	if err != nil {
		return "启动失败: " + err.Error(), nil
	}
	return formatSnapshot(snap), nil
}

func (t *Terminal) toolBashWait(ctx context.Context, args map[string]any) (any, error) {
	sessionID, _ := args["session_id"].(string)
	snap, err := t.manager.Wait(sessionID, intOf(args["yield_time_ms"]))
	if err != nil {
		return "ERROR: " + err.Error(), nil
	}
	return formatSnapshot(snap), nil
}

func (t *Terminal) toolBashStop(ctx context.Context, args map[string]any) (any, error) {
	sessionID, _ := args["session_id"].(string)
	snap, err := t.manager.Stop(sessionID, stringOf(args["reason"]))
	if err != nil {
		return "ERROR: " + err.Error(), nil
	}
	return formatSnapshot(snap), nil
}

// formatSnapshot 快照 → 文案（formatCommandSnapshot 形态）。
func formatSnapshot(s CommandSnapshot) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("session_id: %s\nstatus: %s\nexit_code: %d\n", s.SessionID, s.Status, s.ExitCode))
	sb.WriteString(fmt.Sprintf("hard_timeout: %d\nterminated: %v\nwall_time_ms: %d\n",
		s.HardTimeoutMs, s.Terminated, s.WallTimeMs))
	sb.WriteString(fmt.Sprintf("output_chars_total: %d\nreturned: %d\ntruncated: %v\n",
		s.OutputCharsTotal, s.Returned, s.Truncated))
	if s.Status == "running" {
		sb.WriteString(fmt.Sprintf("Process running with session ID: %s\n", s.SessionID))
	}
	sb.WriteString("Output:\n" + s.Output)
	return sb.String()
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

func stringOf(v any) string {
	s, _ := v.(string)
	return s
}
