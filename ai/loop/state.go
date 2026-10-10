// Package loop 持久循环引擎（对标 solon-ai-loop，契约 docs/arch/ai/
// 06-talents-and-extended.md §5）：IDLE→PLANNING→EXECUTING→VERIFYING→
// FIXING→COMPLETED/FAILED(+PAUSED) 状态机 + Strategy SPI（Ralph/TeamPipeline/
// UltraQA 三内置）+ 验证器 + 磁盘状态持久化（原子写 + 三模式互斥）+ Autopilot
// 多阶段管道。对 flow 的依赖收敛为 flow.Context 单点（Java 同）。
//
// 两处 Java 已知缺陷的 Go 修正（文档注明）：① LoopContext.iterationCount 不
// 递增导致 maxIterations 保险丝只对 0 生效——Go 每轮真实递增；② pause 使执行
// 线程退出、resume 不续跑——Go 以条件变量阻塞执行 goroutine，resume 真续跑。
package loop

import "sync"

// State 循环状态（8 值）。
type State string

const (
	StateIdle      State = "IDLE"
	StatePlanning  State = "PLANNING"
	StateExecuting State = "EXECUTING"
	StateVerifying State = "VERIFYING"
	StateFixing    State = "FIXING"
	StatePaused    State = "PAUSED"
	StateCompleted State = "COMPLETED"
	StateFailed    State = "FAILED"
)

// IsActive 活跃（非终态非暂停）。
func (s State) IsActive() bool {
	switch s {
	case StatePlanning, StateExecuting, StateVerifying, StateFixing:
		return true
	}
	return false
}

// IsTerminal 终态。
func (s State) IsTerminal() bool { return s == StateCompleted || s == StateFailed }

// CanPause 可暂停。
func (s State) CanPause() bool { return s.IsActive() }

// CanResume 可恢复。
func (s State) CanResume() bool { return s == StatePaused }

// transitions 状态机转移表（对标 LoopStateManager）。
var transitions = map[State][]State{
	StateIdle:      {StatePlanning},
	StatePlanning:  {StateExecuting, StatePaused, StateFailed},
	StateExecuting: {StateVerifying, StatePaused, StateFailed},
	StateVerifying: {StateCompleted, StateFixing, StatePaused, StateFailed},
	StateFixing:    {StateExecuting, StatePaused, StateFailed},
	StatePaused:    {StatePlanning, StateExecuting, StateVerifying, StateFixing, StateFailed},
	StateCompleted: {},
	StateFailed:    {},
}

// IsValidTransition 转移合法性。
func IsValidTransition(from, to State) bool {
	for _, s := range transitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// AllowedTransitions 合法目标集。
func AllowedTransitions(from State) []State {
	return append([]State(nil), transitions[from]...)
}

// ---- Config ----

// Config 一次循环启动配置（对标 LoopConfig）。
type Config struct {
	TaskDescription          string
	Strategy                 Strategy
	Validator                Validator // nil = 自动 passed（Java null validator 同款）
	MaxIterations            int       // 0 保险丝（首轮即停）
	VerificationRequired     bool
	StatePersistenceEnabled  bool
	Parameters               map[string]any
}

// EngineConfig 引擎级配置（对标 LoopEngineConfig）。
type EngineConfig struct {
	StateManager        StateManager
	MonitoringEnabled   bool
	DebuggingEnabled    bool
	CleanupIntervalSecs int
	StateExpirationMs   int64
}

// NewEngineConfig 缺省（cleanup 300s、过期 1h）。
func NewEngineConfig() *EngineConfig {
	return &EngineConfig{CleanupIntervalSecs: 300, StateExpirationMs: 3600000}
}

// mutex 全局表共享锁（引擎实例间隔离由 id 命名空间保证——Go 以 guard 实例为界）。
var _ = sync.Mutex{}
