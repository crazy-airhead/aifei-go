// engine.go 引擎与会话（对标 LoopEngine/SimpleLoopEngine/LoopSession）。
package loop

import (
	"context"
	"sync"
	"time"

	"github.com/crazy-airhead/aifei-go/log"
)

// Engine 引擎接口（对标 LoopEngine）。
type Engine interface {
	Start(cfg *Config) (Session, error)
	Pause(sessionID string)
	Resume(sessionID string)
	Stop(sessionID string)
	GetState(sessionID string) (State, bool)
	GetSession(sessionID string) (Session, bool)
	IsRunning(sessionID string) bool
	ActiveSessions() []Session
}

// Session 会话接口（对标 LoopSession）。
type Session interface {
	ID() string
	State() State
	Context() *Context
	ShouldContinue() bool
	ExecuteIteration() *IterationResult
	Result() *LoopResult
	IterationHistory() []IterationResult
	IterationCount() int
	SuccessRate() float64
	StartTime() time.Time
	WaitForCompletion() *LoopResult
	OnStateChange(fn func(State))
	OnIterationComplete(fn func(*IterationResult))
	Pause()
	Resume()
	Stop()
}

// SimpleEngine 默认引擎。
type SimpleEngine struct {
	mu       sync.Mutex
	sessions map[string]*simpleSession
	state    StateManager
	guard    *MutualExclusionGuard
	disk     *DiskStateManager
}

// NewEngine 构造（cfg 可空 = 缺省）。
func NewEngine(cfg *EngineConfig) *SimpleEngine {
	if cfg == nil {
		cfg = NewEngineConfig()
	}
	e := &SimpleEngine{sessions: map[string]*simpleSession{}}
	if cfg.StateManager != nil {
		e.state = cfg.StateManager
	} else {
		e.state = NewInMemoryStateManager()
	}
	e.disk = NewDiskStateManager("")
	e.guard = NewMutualExclusionGuard(e.disk)
	return e
}

// SetProjectRootDirectory 切根目录（重建磁盘面）。
func (e *SimpleEngine) SetProjectRootDirectory(dir string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.disk = NewDiskStateManager(dir)
	e.guard = NewMutualExclusionGuard(e.disk)
}

// Guard 互斥守卫。
func (e *SimpleEngine) Guard() *MutualExclusionGuard { return e.guard }

// Disk 磁盘状态面。
func (e *SimpleEngine) Disk() *DiskStateManager { return e.disk }

// Start 启动会话（同步置 PLANNING + running 后起后台 goroutine）。
func (e *SimpleEngine) Start(cfg *Config) (Session, error) {
	if cfg == nil || cfg.Strategy == nil {
		return nil, &engineError{"loop: strategy is required"}
	}
	id := NewUUID()
	params := map[string]any{"taskDescription": cfg.TaskDescription, "sessionId": id}
	for k, v := range cfg.Parameters {
		params[k] = v
	}
	ctx := &Context{
		SessionID: id, TaskDescription: cfg.TaskDescription,
		CurrentState: StateIdle, MaxIterations: cfg.MaxIterations,
		StartTime: time.Now(), Parameters: params, ContextData: map[string]any{},
	}
	s := &simpleSession{
		id: id, ctx: ctx, strategy: cfg.Strategy, validator: cfg.Validator,
		running: true, startTime: time.Now(),
	}
	s.pauseCond = sync.NewCond(&s.mu0)
	if s.validator == nil {
		s.validator = AcceptAllValidator{}
	}

	e.mu.Lock()
	e.sessions[id] = s
	e.mu.Unlock()

	s.setState(StatePlanning)
	if cfg.StatePersistenceEnabled {
		if err := e.state.SaveState(&StateData{
			SessionID: id, State: StatePlanning, StartTime: ctx.StartTime,
			LastUpdateTime: time.Now(), ContextData: map[string]any{},
		}); err != nil {
			log.Warn("loop: save state: %v", err)
		}
	}

	go s.execute()
	return s, nil
}

// Pause 暂停。
func (e *SimpleEngine) Pause(sessionID string) {
	e.mu.Lock()
	s := e.sessions[sessionID]
	e.mu.Unlock()
	if s != nil {
		s.Pause()
	}
}

// Resume 恢复。
func (e *SimpleEngine) Resume(sessionID string) {
	e.mu.Lock()
	s := e.sessions[sessionID]
	e.mu.Unlock()
	if s != nil {
		s.Resume()
	}
}

// Stop 停止（终态 FAILED——Java 同款：人工停止记为失败）。
func (e *SimpleEngine) Stop(sessionID string) {
	e.mu.Lock()
	s := e.sessions[sessionID]
	e.mu.Unlock()
	if s != nil {
		s.Stop()
	}
}

// GetState 状态查询。
func (e *SimpleEngine) GetState(sessionID string) (State, bool) {
	e.mu.Lock()
	s := e.sessions[sessionID]
	e.mu.Unlock()
	if s == nil {
		return "", false
	}
	return s.State(), true
}

// GetSession 会话查询。
func (e *SimpleEngine) GetSession(sessionID string) (Session, bool) {
	e.mu.Lock()
	s := e.sessions[sessionID]
	e.mu.Unlock()
	return s, s != nil
}

// IsRunning 运行中。
func (e *SimpleEngine) IsRunning(sessionID string) bool {
	e.mu.Lock()
	s := e.sessions[sessionID]
	e.mu.Unlock()
	return s != nil && s.IsRunning()
}

// ActiveSessions 活跃会话。
func (e *SimpleEngine) ActiveSessions() []Session {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Session
	for _, s := range e.sessions {
		if s.State().IsActive() {
			out = append(out, s)
		}
	}
	return out
}

// ---- 会话实现 ----

type simpleSession struct {
	id        string
	ctx       *Context
	strategy  Strategy
	validator Validator

	mu0       sync.Mutex
	state     State
	running   bool
	paused    bool
	pauseCond *sync.Cond

	history    []IterationResult
	stateFns   []func(State)
	iterFns    []func(*IterationResult)

	result     *LoopResult
	startTime  time.Time
	endTime    time.Time
}

// ID 标识。
func (s *simpleSession) ID() string { return s.id }

// State 当前状态。
func (s *simpleSession) State() State {
	s.mu0.Lock()
	defer s.mu0.Unlock()
	return s.state
}

// Context 上下文。
func (s *simpleSession) Context() *Context { return s.ctx }

// IsRunning 运行中。
func (s *simpleSession) IsRunning() bool {
	s.mu0.Lock()
	defer s.mu0.Unlock()
	return s.running && s.state.IsActive()
}

// IterationHistory 历史快照。
func (s *simpleSession) IterationHistory() []IterationResult {
	s.mu0.Lock()
	defer s.mu0.Unlock()
	return append([]IterationResult(nil), s.history...)
}

// IterationCount 迭代数。
func (s *simpleSession) IterationCount() int {
	s.mu0.Lock()
	defer s.mu0.Unlock()
	return len(s.history)
}

// SuccessRate 成功率。
func (s *simpleSession) SuccessRate() float64 {
	s.mu0.Lock()
	defer s.mu0.Unlock()
	if len(s.history) == 0 {
		return 0
	}
	success := 0
	for _, it := range s.history {
		if it.Success {
			success++
		}
	}
	return float64(success) / float64(len(s.history))
}

// StartTime 开始时间。
func (s *simpleSession) StartTime() time.Time { return s.startTime }

// Result 终态结果。
func (s *simpleSession) Result() *LoopResult {
	s.mu0.Lock()
	defer s.mu0.Unlock()
	return s.result
}

// OnStateChange 状态监听。
func (s *simpleSession) OnStateChange(fn func(State)) {
	s.mu0.Lock()
	s.stateFns = append(s.stateFns, fn)
	s.mu0.Unlock()
}

// OnIterationComplete 迭代监听。
func (s *simpleSession) OnIterationComplete(fn func(*IterationResult)) {
	s.mu0.Lock()
	s.iterFns = append(s.iterFns, fn)
	s.mu0.Unlock()
}

// ShouldContinue 策略续跑判定。
func (s *simpleSession) ShouldContinue() bool { return s.strategy.ShouldContinue(s.ctx) }

// ExecuteIteration 单轮（测试/手动步进用）。
func (s *simpleSession) ExecuteIteration() *IterationResult {
	s.ctx.IterationCount++
	result := s.strategy.ExecuteIteration(s.ctx)
	s.mu0.Lock()
	s.history = append(s.history, *result)
	fns := append([]func(*IterationResult){}, s.iterFns...)
	s.mu0.Unlock()
	for _, fn := range fns {
		fn(result)
	}
	return result
}

// Pause 暂停（执行 goroutine 阻塞在条件变量——Java 退出线程的缺陷已修）。
func (s *simpleSession) Pause() {
	s.mu0.Lock()
	if s.state.CanPause() {
		s.setStateLocked(StatePaused)
		s.paused = true
	}
	s.mu0.Unlock()
}

// Resume 恢复（唤醒阻塞的执行 goroutine 真续跑）。
func (s *simpleSession) Resume() {
	s.mu0.Lock()
	if s.state == StatePaused {
		s.setStateLocked(StateExecuting)
		s.paused = false
		s.pauseCond.Broadcast()
	}
	s.mu0.Unlock()
}

// Stop 停止（终态 FAILED；Java 同款语义——人工停止记失败）。
func (s *simpleSession) Stop() {
	s.mu0.Lock()
	if !s.state.IsTerminal() {
		s.setStateLocked(StateFailed)
	}
	s.running = false
	s.paused = false
	s.pauseCond.Broadcast()
	s.mu0.Unlock()
}

// WaitForCompletion 阻塞等终态。
func (s *simpleSession) WaitForCompletion() *LoopResult {
	for {
		s.mu0.Lock()
		terminal := s.state.IsTerminal() && s.result != nil
		s.mu0.Unlock()
		if terminal {
			return s.Result()
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// setState 状态转移 + 通知。
func (s *simpleSession) setState(to State) {
	s.mu0.Lock()
	defer s.mu0.Unlock()
	s.setStateLocked(to)
}

func (s *simpleSession) setStateLocked(to State) {
	if s.state == to {
		return
	}
	s.state = to
	// ctx.CurrentState 只由执行 goroutine 同步（单写者——Pause/Stop 等外部
	// 调用不改共享 ctx，消除跨 goroutine 数据竞争）
	fns := append([]func(State){}, s.stateFns...)
	for _, fn := range fns {
		fn(to)
	}
}

// execute 主循环（后台 goroutine）：迭代 → VERIFYING → 验证裁决。
func (s *simpleSession) execute() {
	s.setState(StatePlanning)
	for {
		s.mu0.Lock()
		running := s.running
		paused := s.paused
		s.ctx.CurrentState = s.state
		s.mu0.Unlock()
		if !running || !s.strategy.ShouldContinue(s.ctx) {
			break
		}
		// 暂停阻塞（不退出循环——Java 缺陷修正）
		for paused && s.running {
			s.pauseCond.L.Lock()
			if !s.paused || !s.running {
				s.pauseCond.L.Unlock()
				break
			}
			s.pauseCond.Wait()
			s.pauseCond.L.Unlock()
			s.mu0.Lock()
			paused = s.paused
			s.mu0.Unlock()
		}
		if !s.running {
			break
		}

		result := s.ExecuteIteration()

		s.mu0.Lock()
		aborted := !s.running || s.state.IsTerminal()
		s.mu0.Unlock()
		if aborted {
			break
		}
		if !result.Success {
			s.finish(StateFailed, result.Message)
			return
		}

		s.setState(StateVerifying)
		v := s.validator.Validate(result.Result, SimpleCriteria("iteration-validation", s.ctx.TaskDescription))
		s.updateState(v)
		if s.ctx.CurrentState.IsTerminal() {
			return
		}
	}
	// 循环从未执行（shouldContinue 首轮 false）
	s.mu0.Lock()
	terminal := s.state.IsTerminal()
	s.mu0.Unlock()
	if !terminal {
		s.finish(StateCompleted, "no iterations required")
	}
}

// updateState 验证裁决：passed&&continue→EXECUTING；passed&&!continue→COMPLETED；
// needsFix→FIXING（修复轮继续）；否则 FAILED。
func (s *simpleSession) updateState(v *ValidationResult) {
	if v == nil {
		v = PassedResult("null validation")
	}
	switch {
	case v.Passed && s.strategy.ShouldContinue(s.ctx):
		s.setState(StateExecuting)
	case v.Passed:
		s.finish(StateCompleted, v.Message)
	case v.NeedsFix:
		s.setState(StateFixing) // FIXING 属 isActive——下一轮即修复轮
	default:
		s.finish(StateFailed, v.Message)
	}
}

// finish 终态收口。
func (s *simpleSession) finish(state State, message string) {
	s.mu0.Lock()
	s.endTime = time.Now()
	success := 0
	for _, it := range s.history {
		if it.Success {
			success++
		}
	}
	rate := 0.0
	if len(s.history) > 0 {
		rate = float64(success) / float64(len(s.history))
	}
	s.result = &LoopResult{
		SessionID: s.id, FinalState: state, Success: state == StateCompleted,
		Message: message, TotalDuration: s.endTime.Sub(s.startTime),
		TotalIterations: len(s.history), SuccessfulIterations: success,
		SuccessRate: rate, StartTime: s.startTime, EndTime: s.endTime,
		IterationHistory: append([]IterationResult(nil), s.history...),
	}
	s.running = false
	s.setStateLocked(state)
	s.mu0.Unlock()
}

type engineError struct{ msg string }

func (e *engineError) Error() string { return e.msg }

var _ = context.Background
