// disk.go 磁盘状态持久化（对标 DiskStateManager + AtomicWrite）：.loops/
// 目录布局（state/{ralph,team,ultraqa,sessions}/、prd/、progress/）、_meta
// 信封 + data 双层 JSON、原子写（tmp+rename）、互斥守卫。
package loop

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// StateData 可持久化状态 DTO（对标 LoopStateData）。
type StateData struct {
	SessionID            string         `json:"sessionId"`
	State                State          `json:"state"`
	IterationCount       int            `json:"iterationCount"`
	SuccessfulIterations int            `json:"successfulIterations"`
	StartTime            time.Time      `json:"startTime"`
	LastUpdateTime       time.Time      `json:"lastUpdateTime"`
	ContextData          map[string]any `json:"contextData"`
	Metadata             map[string]any `json:"metadata"`
}

// StateManager 状态持久化 SPI。
type StateManager interface {
	SaveState(data *StateData) error
	LoadState(sessionID string) (*StateData, error)
	ClearState(sessionID string) error
	HasState(sessionID string) (bool, error)
	AllSessionIDs() ([]string, error)
}

// InMemoryStateManager 内存实现。
type InMemoryStateManager struct {
	mu      sync.Mutex
	states  map[string]*StateData
	project map[string]string
}

// NewInMemoryStateManager 构造。
func NewInMemoryStateManager() *InMemoryStateManager {
	return &InMemoryStateManager{states: map[string]*StateData{}, project: map[string]string{}}
}

// SaveState 保存。
func (m *InMemoryStateManager) SaveState(data *StateData) error {
	if data == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[data.SessionID] = data
	return nil
}

// LoadState 读取。
func (m *InMemoryStateManager) LoadState(sessionID string) (*StateData, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.states[sessionID], nil
}

// ClearState 清除。
func (m *InMemoryStateManager) ClearState(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.states, sessionID)
	return nil
}

// HasState 存在性。
func (m *InMemoryStateManager) HasState(sessionID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.states[sessionID]
	return ok, nil
}

// AllSessionIDs 全部会话。
func (m *InMemoryStateManager) AllSessionIDs() ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for id := range m.states {
		out = append(out, id)
	}
	return out, nil
}

// RegisterSession 会话归属注册（validateSession 消费）。
func (m *InMemoryStateManager) RegisterSession(sessionID, mode, projectPath string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.project[sessionID] = mode + ":" + projectPath
}

// ValidateSession 归属校验。
func (m *InMemoryStateManager) ValidateSession(sessionID, mode, projectPath string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.project[sessionID] == mode+":"+projectPath
}

// ---- 磁盘实现 ----

// 三模式目录（对标 OMC mode-state-io）。
var diskModes = []string{"ralph", "team", "ultraqa"}

// DiskStateManager 磁盘 JSON 状态管理。
type DiskStateManager struct {
	root string // {root}/.loops
}

// NewDiskStateManager 构造（root 空 = 当前目录）。
func NewDiskStateManager(root string) *DiskStateManager {
	if root == "" {
		root = "."
	}
	d := &DiskStateManager{root: filepath.Join(root, ".loops")}
	d.ensureDirs()
	return d
}

// Root 根目录。
func (d *DiskStateManager) Root() string { return d.root }

func (d *DiskStateManager) ensureDirs() {
	for _, sub := range []string{"state/ralph", "state/team", "state/ultraqa", "state/sessions", "prd", "progress"} {
		os.MkdirAll(filepath.Join(d.root, sub), 0o700)
	}
}

func (d *DiskStateManager) statePath(mode, sessionID string) string {
	return filepath.Join(d.root, "state", mode, sessionID+".json")
}

// modeOf 会话模式推断（contextData["mode"] 缺省 ralph）。
func modeOf(mode string, data *StateData) string {
	if mode != "" {
		return mode
	}
	if data != nil {
		if m, _ := data.ContextData["mode"].(string); m != "" {
			return m
		}
	}
	return "ralph"
}

// SaveStateInMode 写状态（_meta 信封 + data；成功后更新会话摘要）。
func (d *DiskStateManager) SaveStateInMode(mode string, data *StateData) error {
	if data == nil {
		return nil
	}
	mode = modeOf(mode, data)
	now := time.Now().Format(time.RFC3339)
	envelope := map[string]any{
		"_meta": map[string]any{"written_at": now, "mode": mode, "sessionId": data.SessionID},
		"data":  data,
	}
	b, err := marshalJSON(envelope)
	if err != nil {
		return err
	}
	if err := atomicWrite(d.statePath(mode, data.SessionID), b); err != nil {
		return err
	}
	return d.updateSessionSummary(data.SessionID, mode)
}

// ReadState 读状态（剥 _meta 只回 data；旧版兼容：无 data 键时根对象即 data）。
func (d *DiskStateManager) ReadState(mode, sessionID string) (*StateData, error) {
	b, err := os.ReadFile(d.statePath(mode, sessionID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var envelope struct {
		Meta map[string]any `json:"_meta"`
		Data *StateData     `json:"data"`
	}
	if err := unmarshalJSON(b, &envelope); err != nil {
		return nil, fmt.Errorf("loop: state parse: %w", err)
	}
	if envelope.Data != nil {
		return envelope.Data, nil
	}
	// 旧格式：根对象即 data（state 值非法时静默置 IDLE——Java 同款容忍）
	var legacy StateData
	if err := unmarshalJSON(b, &legacy); err == nil && legacy.SessionID != "" {
		if !validState(legacy.State) {
			legacy.State = StateIdle
		}
		return &legacy, nil
	}
	return nil, nil
}

func validState(s State) bool {
	switch s {
	case StateIdle, StatePlanning, StateExecuting, StateVerifying, StateFixing,
		StatePaused, StateCompleted, StateFailed:
		return true
	}
	return false
}

// LoadState 依次探 ralph→team→ultraqa。
func (d *DiskStateManager) LoadState(sessionID string) (*StateData, error) {
	for _, mode := range diskModes {
		data, err := d.ReadState(mode, sessionID)
		if err != nil {
			return nil, err
		}
		if data != nil {
			return data, nil
		}
	}
	return nil, nil
}

// HasState 三模式探测。
func (d *DiskStateManager) HasState(sessionID string) (bool, error) {
	for _, mode := range diskModes {
		if _, err := os.Stat(d.statePath(mode, sessionID)); err == nil {
			return true, nil
		}
	}
	return false, nil
}

// ClearState 清状态（三模式 + 摘要 + prd/progress）。
func (d *DiskStateManager) ClearState(mode, sessionID string) error {
	for _, m := range diskModes {
		if mode == "" || m == mode {
			os.Remove(d.statePath(m, sessionID))
		}
	}
	os.Remove(filepath.Join(d.root, "state", "sessions", sessionID+".json"))
	os.Remove(filepath.Join(d.root, "prd", sessionID+".json"))
	os.Remove(filepath.Join(d.root, "progress", sessionID+".txt"))
	return nil
}

// AllSessionIDs 会话摘要目录。
func (d *DiskStateManager) AllSessionIDs() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(d.root, "state", "sessions"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			out = append(out, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	return out, nil
}

// SaveState 实现 StateManager（mode 由 data 推断）。
func (d *DiskStateManager) SaveState(data *StateData) error { return d.SaveStateInMode("", data) }

// updateSessionSummary 会话摘要 {sessionId, mode, lastUpdated}。
func (d *DiskStateManager) updateSessionSummary(sessionID, mode string) error {
	summary := map[string]any{"sessionId": sessionID, "mode": mode, "lastUpdated": time.Now().Format(time.RFC3339)}
	b, err := marshalJSON(summary)
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(d.root, "state", "sessions", sessionID+".json"), b)
}

// WritePrd / ReadPrd PRD 文档（json）。
func (d *DiskStateManager) WritePrd(sessionID string, b []byte) error {
	return atomicWrite(filepath.Join(d.root, "prd", sessionID+".json"), b)
}

// ReadPrd 读 PRD。
func (d *DiskStateManager) ReadPrd(sessionID string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(d.root, "prd", sessionID+".json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	return b, err
}

// WriteProgress / ReadProgress / AppendProgress 进度记忆（txt）。
func (d *DiskStateManager) WriteProgress(sessionID string, b []byte) error {
	return atomicWrite(filepath.Join(d.root, "progress", sessionID+".txt"), b)
}

// ReadProgress 读进度。
func (d *DiskStateManager) ReadProgress(sessionID string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(d.root, "progress", sessionID+".txt"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	return b, err
}

// AppendProgress 追加进度。
func (d *DiskStateManager) AppendProgress(sessionID string, b []byte) error {
	old, _ := d.ReadProgress(sessionID)
	return d.WriteProgress(sessionID, append(old, b...))
}

// CleanupGhostLegacy 清孤儿（磁盘有而内存归属无的会话文件全删——对标同名）。
func (d *DiskStateManager) CleanupGhostLegacy(projectPath string) int {
	removed := 0
	for _, mode := range diskModes {
		entries, _ := os.ReadDir(filepath.Join(d.root, "state", mode))
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".json") {
				os.Remove(filepath.Join(d.root, "state", mode, e.Name()))
				removed++
			}
		}
	}
	return removed
}

// atomicWrite 原子写（tmp+rename+0600；对标 AtomicWrite）。
func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp."+NewUUID())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// NewUUID UUIDv4 形态。
func NewUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return formatUUID(b)
}

func formatUUID(b []byte) string {
	const hexDigits = "0123456789abcdef"
	out := make([]byte, 0, 36)
	for i, v := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out = append(out, '-')
		}
		out = append(out, hexDigits[v>>4], hexDigits[v&0x0f])
	}
	return string(out)
}

// HasStateForMode 指定模式的磁盘状态存在性（守卫双检消费）。
func (d *DiskStateManager) HasStateForMode(mode, sessionID string) (bool, error) {
	if _, err := os.Stat(d.statePath(mode, sessionID)); err == nil {
		return true, nil
	}
	return false, nil
}
