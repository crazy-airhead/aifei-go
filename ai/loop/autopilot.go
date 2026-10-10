// autopilot.go 多阶段管道（对标 AutopilotExecutor + PipelineConfig/Tracking +
// DefaultStageAdapters）：EXPANSION→PLANNING→EXECUTION→QA→VALIDATION→
// COMPLETED/FAILED；每阶段起一个 loop 会话阻塞跑完。
package loop

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// PipelineStage 管道阶段。
type PipelineStage string

const (
	StageExpansion  PipelineStage = "EXPANSION"
	StagePlanning   PipelineStage = "PLANNING"
	StageExecution  PipelineStage = "EXECUTION"
	StageQA         PipelineStage = "QA"
	StageValidation PipelineStage = "VALIDATION"
	StageCompleted  PipelineStage = "COMPLETED"
	StageFailed     PipelineStage = "FAILED"
)

// IsTerminal 终态。
func (s PipelineStage) IsTerminal() bool { return s == StageCompleted || s == StageFailed }

// ToTeamPhase Team Phase 双向映射。
func (s PipelineStage) ToTeamPhase() TeamPhase {
	switch s {
	case StageExpansion, StagePlanning:
		return PhasePlan
	case StageExecution:
		return PhaseExec
	case StageQA, StageValidation:
		return PhaseVerify
	case StageCompleted:
		return PhaseCompleted
	default:
		return PhaseCompleted
	}
}

// StageFromTeamPhase 反向映射。
func StageFromTeamPhase(p TeamPhase) PipelineStage {
	switch p {
	case PhasePlan, PhasePrd:
		return StagePlanning
	case PhaseExec:
		return StageExecution
	case PhaseVerify:
		return StageQA
	case PhaseFix:
		return StageExecution
	case PhaseCompleted:
		return StageCompleted
	default: // CANCELLED
		return StageFailed
	}
}

// PipelineRequest 管道请求。
type PipelineRequest struct {
	SessionID            string
	Description          string
	MaxIterations        int
	VerificationRequired bool
	Context              map[string]any
}

// NewPipelineRequest 起步请求。
func NewPipelineRequest(sessionID, description string) *PipelineRequest {
	return &PipelineRequest{SessionID: sessionID, Description: description,
		MaxIterations: 50, VerificationRequired: true, Context: map[string]any{}}
}

// PipelineResult 管道结果。
type PipelineResult struct {
	SessionID string
	Success   bool
	Message   string
	Tracking  *PipelineTracking
}

// Summary 摘要。
func (r *PipelineResult) Summary() string {
	if r.Tracking == nil {
		return r.Message
	}
	return r.Message + "\n" + r.Tracking.Summary()
}

// PipelineConfig 五阶段开关 + 阶段策略映射。
type PipelineConfig struct {
	ExpansionEnabled, PlanningEnabled, ExecutionEnabled, QAEnabled, ValidationEnabled bool
	strategies map[PipelineStage]Strategy
}

// NewPipelineConfig 缺省全开。
func NewPipelineConfig() *PipelineConfig {
	return &PipelineConfig{true, true, true, true, true, map[PipelineStage]Strategy{}}
}

// SetStageEnabled 阶段开关。
func (c *PipelineConfig) SetStageEnabled(s PipelineStage, on bool) *PipelineConfig {
	switch s {
	case StageExpansion:
		c.ExpansionEnabled = on
	case StagePlanning:
		c.PlanningEnabled = on
	case StageExecution:
		c.ExecutionEnabled = on
	case StageQA:
		c.QAEnabled = on
	case StageValidation:
		c.ValidationEnabled = on
	}
	return c
}

// SetStageStrategy 阶段策略覆盖（缺省：EXECUTION→Ralph、QA→UltraQA、
// 其余→TeamPipeline）。
func (c *PipelineConfig) SetStageStrategy(s PipelineStage, st Strategy) *PipelineConfig {
	c.strategies[s] = st
	return c
}

// EnabledStages 启用阶段（固定序）。
func (c *PipelineConfig) EnabledStages() []PipelineStage {
	var out []PipelineStage
	for _, s := range []PipelineStage{StageExpansion, StagePlanning, StageExecution, StageQA, StageValidation} {
		if enabled := map[PipelineStage]bool{
			StageExpansion: c.ExpansionEnabled, StagePlanning: c.PlanningEnabled,
			StageExecution: c.ExecutionEnabled, StageQA: c.QAEnabled,
			StageValidation: c.ValidationEnabled,
		}[s]; enabled {
			out = append(out, s)
		}
	}
	return out
}

// StrategyForStage 阶段策略。
func (c *PipelineConfig) StrategyForStage(s PipelineStage) Strategy {
	if st, ok := c.strategies[s]; ok {
		return st
	}
	switch s {
	case StageExecution:
		return NewRalphStrategy()
	case StageQA:
		return NewUltraQAStrategy()
	default:
		return NewTeamStrategy()
	}
}

// StageRecord 阶段记录。
type StageRecord struct {
	Stage              PipelineStage
	EnteredAt          time.Time
	CompletedAt        time.Time
	Success            bool
	Summary            string
	Skipped            bool
}

// PipelineTracking 管道跟踪。
type PipelineTracking struct {
	SessionID   string
	CurrentStage PipelineStage
	History     []*StageRecord
	StartedAt   time.Time
	CompletedAt time.Time
	Failed      bool
}

// NewPipelineTracking 构造（当前阶段 EXPANSION）。
func NewPipelineTracking(sessionID string) *PipelineTracking {
	return &PipelineTracking{SessionID: sessionID, CurrentStage: StageExpansion, StartedAt: time.Now()}
}

// EnterStage 进入阶段。
func (t *PipelineTracking) EnterStage(s PipelineStage) {
	t.History = append(t.History, &StageRecord{Stage: s, EnteredAt: time.Now()})
	t.CurrentStage = s
}

// CompleteStage 收尾该阶段首个未完成记录。
func (t *PipelineTracking) CompleteStage(s PipelineStage, success bool, summary string) {
	for _, r := range t.History {
		if r.Stage == s && r.CompletedAt.IsZero() {
			r.CompletedAt = time.Now()
			r.Success = success
			r.Summary = summary
			return
		}
	}
}

// SkipStage 跳过。
func (t *PipelineTracking) SkipStage(s PipelineStage) {
	for _, r := range t.History {
		if r.Stage == s && r.CompletedAt.IsZero() {
			r.CompletedAt = time.Now()
			r.Skipped = true
			r.Summary = "skipped"
			return
		}
	}
}

// Complete 管道收口。
func (t *PipelineTracking) Complete(failed bool) {
	t.CompletedAt = time.Now()
	t.Failed = failed
}

// IsCompleted 完成。
func (t *PipelineTracking) IsCompleted() bool { return !t.CompletedAt.IsZero() }

// Summary 摘要。
func (t *PipelineTracking) Summary() string {
	var sb strings.Builder
	status := "RUNNING"
	if t.IsCompleted() {
		if t.Failed {
			status = "FAILED"
		} else {
			status = "COMPLETED"
		}
	}
	sb.WriteString(fmt.Sprintf("Status: %s | Current: %s\nStage History: ", status, t.CurrentStage))
	for i, r := range t.History {
		if i > 0 {
			sb.WriteString(" → ")
		}
		switch {
		case r.Skipped:
			sb.WriteString(string(r.Stage) + " (SKIPPED)")
		case r.CompletedAt.IsZero():
			sb.WriteString(string(r.Stage) + " (RUNNING)")
		case r.Success:
			sb.WriteString(string(r.Stage) + " ✓")
		default:
			sb.WriteString(string(r.Stage) + " ✗")
		}
	}
	return sb.String()
}

// StageAdapter 阶段适配器 SPI。
type StageAdapter interface {
	SupportedStage() PipelineStage
	Execute(stage PipelineStage, context map[string]any, engine Engine) (*StageResult, error)
}

// StageResult 阶段结果。
type StageResult struct {
	Success bool
	Message string
	Data    map[string]any
}

// OKStage 成功。
func OKStage(msg string) *StageResult { return &StageResult{Success: true, Message: msg} }

// FailStage 失败。
func FailStage(msg string) *StageResult { return &StageResult{Success: false, Message: msg} }

// DefaultStageAdapters 内置适配器。
type DefaultStageAdapters struct{}

// NewDefaultStageAdapters 构造。
func NewDefaultStageAdapters() *DefaultStageAdapters { return &DefaultStageAdapters{} }

// Build 内置四件。
func (d *DefaultStageAdapters) Build() []StageAdapter {
	return []StageAdapter{
		expansionAdapter{},
		validationAdapter{},
	}
}

type expansionAdapter struct{}

func (expansionAdapter) SupportedStage() PipelineStage { return StageExpansion }
func (expansionAdapter) Execute(_ PipelineStage, context map[string]any, _ Engine) (*StageResult, error) {
	desc, _ := context["description"].(string)
	if desc == "" {
		return FailStage("EXPANSION requires 'description' in context"), nil
	}
	context["expandedDescription"] = "[EXPANDED] " + desc
	context["requirementsAnalyzed"] = true
	return OKStage("expanded"), nil
}

type validationAdapter struct{}

func (validationAdapter) SupportedStage() PipelineStage { return StageValidation }
func (validationAdapter) Execute(_ PipelineStage, context map[string]any, _ Engine) (*StageResult, error) {
	passed, _ := context["validationChecksPassed"].(bool)
	if !passed {
		return FailStage("validation checks failed"), nil
	}
	context["finalValidationPassed"] = true
	context["validatedAt"] = time.Now().Format(time.RFC3339)
	return OKStage("validated"), nil
}

// AutopilotExecutor 管道编排器。
type AutopilotExecutor struct {
	mu        sync.Mutex
	engine    Engine
	config    *PipelineConfig
	tracking  map[string]*PipelineTracking
	adapters  map[PipelineStage]StageAdapter
	sessions  map[string]map[PipelineStage]Session
}

// NewAutopilotExecutor 构造（默认适配器：expansion + validation）。
func NewAutopilotExecutor(engine Engine, config *PipelineConfig) *AutopilotExecutor {
	if config == nil {
		config = NewPipelineConfig()
	}
	a := &AutopilotExecutor{
		engine: engine, config: config,
		tracking: map[string]*PipelineTracking{},
		adapters: map[PipelineStage]StageAdapter{},
		sessions: map[string]map[PipelineStage]Session{},
	}
	for _, ad := range NewDefaultStageAdapters().Build() {
		a.adapters[ad.SupportedStage()] = ad
	}
	return a
}

// AddAdapter 注册适配器。
func (a *AutopilotExecutor) AddAdapter(ad StageAdapter) *AutopilotExecutor {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.adapters[ad.SupportedStage()] = ad
	return a
}

// StartPipeline 启动管道（阻塞跑完全部阶段）。
func (a *AutopilotExecutor) StartPipeline(req *PipelineRequest) *PipelineResult {
	tracking := NewPipelineTracking(req.SessionID)
	a.mu.Lock()
	a.tracking[req.SessionID] = tracking
	a.mu.Unlock()

	for _, stage := range a.config.EnabledStages() {
		tracking.EnterStage(stage)

		if ad, ok := a.adapters[stage]; ok {
			result, err := ad.Execute(stage, req.Context, a.engine)
			if err != nil || result == nil || !result.Success {
				msg := "adapter error"
				if result != nil {
					msg = result.Message
				}
				if err != nil {
					msg = err.Error()
				}
				tracking.CompleteStage(stage, false, msg)
				tracking.Complete(true)
				return &PipelineResult{SessionID: req.SessionID, Success: false,
					Message: fmt.Sprintf("Stage %s failed: %s", stage, msg), Tracking: tracking}
			}
			tracking.CompleteStage(stage, true, result.Message)
			continue
		}

		ok := a.executeStageWithEngine(stage, req, tracking)
		if !ok {
			tracking.Complete(true)
			return &PipelineResult{SessionID: req.SessionID, Success: false,
				Message: fmt.Sprintf("Stage %s failed", stage), Tracking: tracking}
		}
	}
	tracking.Complete(false)
	return &PipelineResult{SessionID: req.SessionID, Success: true,
		Message: "Pipeline completed successfully", Tracking: tracking}
}

// executeStageWithEngine 阶段起 loop 会话阻塞执行。
func (a *AutopilotExecutor) executeStageWithEngine(stage PipelineStage, req *PipelineRequest, tracking *PipelineTracking) bool {
	strategy := a.config.StrategyForStage(stage)
	session, err := a.engine.Start(&Config{
		TaskDescription:         fmt.Sprintf("Pipeline stage: %s - %s", stage, req.Description),
		Strategy:                strategy,
		MaxIterations:           req.MaxIterations,
		VerificationRequired:    req.VerificationRequired,
		StatePersistenceEnabled: true,
		Parameters:              req.Context,
	})
	if err != nil {
		tracking.CompleteStage(stage, false, err.Error())
		return false
	}
	a.mu.Lock()
	if a.sessions[req.SessionID] == nil {
		a.sessions[req.SessionID] = map[PipelineStage]Session{}
	}
	a.sessions[req.SessionID][stage] = session
	a.mu.Unlock()

	result := session.WaitForCompletion()
	success := result != nil && result.Success
	summary := ""
	if result != nil {
		summary = result.Message
	}
	tracking.CompleteStage(stage, success, summary)
	return success
}

// CancelPipeline 取消（停全部阶段会话）。
func (a *AutopilotExecutor) CancelPipeline(sessionID string) {
	a.mu.Lock()
	tracking := a.tracking[sessionID]
	stageSessions := a.sessions[sessionID]
	delete(a.tracking, sessionID)
	delete(a.sessions, sessionID)
	a.mu.Unlock()

	for _, s := range stageSessions {
		s.Stop()
	}
	if tracking != nil {
		tracking.Complete(true)
	}
}

// Status 状态（NOT_STARTED/FAILED/COMPLETED/RUNNING:stage）。
func (a *AutopilotExecutor) Status(sessionID string) string {
	a.mu.Lock()
	tracking := a.tracking[sessionID]
	a.mu.Unlock()
	if tracking == nil {
		return "NOT_STARTED"
	}
	if tracking.IsCompleted() {
		if tracking.Failed {
			return "FAILED"
		}
		return "COMPLETED"
	}
	return "RUNNING:" + string(tracking.CurrentStage)
}

// ActivePipelines 活跃管道。
func (a *AutopilotExecutor) ActivePipelines() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for id, t := range a.tracking {
		if !t.IsCompleted() {
			out = append(out, id)
		}
	}
	return out
}

// Tracking 快照。
func (a *AutopilotExecutor) Tracking(sessionID string) *PipelineTracking {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.tracking[sessionID]
}
