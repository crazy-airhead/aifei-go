// integration.go loop 与 agent/flow/harness 的集成桥（对标 solon-ai-loop
// integration 包三件）：宿主侧把各自的执行面注入 Bridge，集成门面提供开箱
// 的 Ralph/UltraQA 启动参数与状态查询。
package loop

import (
	"context"
	"fmt"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/flow"
)

// ==================== Agent 桥（SolonAgentIntegration 对应物） ====================

// AgentBridge 智能体执行桥。
type AgentBridge struct {
	Agent agent.Agent
}

// ExecutePrompt 执行提示（错误转 "Agent error: ..." 字符串——Java 同款不抛）。
func (b *AgentBridge) ExecutePrompt(promptText string) string {
	if b.Agent == nil {
		return "Agent error: agent not injected. Call SetAgent() first."
	}
	msg, err := b.Agent.Call(ai.PromptOf(promptText), agent.NewMemorySession("loop-agent"))
	if err != nil {
		return "Agent error: " + err.Error()
	}
	if msg == nil {
		return ""
	}
	return msg.Text
}

// ExecutionStatus 执行状态快照（Java ExecutionStatus）。
type ExecutionStatus struct {
	SessionID      string
	State          string
	IterationCount int
	SuccessRate    float64
}

// AgentIntegration 智能体集成门面。
type AgentIntegration struct {
	engine Engine
	bridge *AgentBridge
}

// NewAgentIntegration 构造。
func NewAgentIntegration(engine Engine) *AgentIntegration {
	return &AgentIntegration{engine: engine, bridge: &AgentBridge{}}
}

// Bridge 执行桥。
func (i *AgentIntegration) Bridge() *AgentBridge { return i.bridge }

// StartAgentExecution 通用启动。
func (i *AgentIntegration) StartAgentExecution(cfg *Config) (Session, error) {
	return i.engine.Start(cfg)
}

// StartRalphLoop Ralph 循环（architect 模式、50 轮、验证开）。
func (i *AgentIntegration) StartRalphLoop(taskDesc string, validator Validator) (Session, error) {
	strategy := NewRalphStrategy().SetCriticMode(CriticArchitect)
	return i.engine.Start(&Config{
		TaskDescription: taskDesc, Strategy: strategy, Validator: validator,
		MaxIterations: 50, VerificationRequired: true, StatePersistenceEnabled: true,
	})
}

// StartUltraQALoop 质量门禁循环（10 门禁轮、TESTS 目标）。
func (i *AgentIntegration) StartUltraQALoop(taskDesc string, validator Validator) (Session, error) {
	strategy := NewUltraQAStrategy()
	strategy.MaxTestAttempts = 10
	return i.engine.Start(&Config{
		TaskDescription: taskDesc, Strategy: strategy, Validator: validator,
		MaxIterations: 100, VerificationRequired: true, StatePersistenceEnabled: true,
	})
}

// StartAgentDrivenRalphLoop 智能体驱动的 Ralph（故事实现器 = 桥执行）。
func (i *AgentIntegration) StartAgentDrivenRalphLoop(taskDesc string, a agent.Agent) (Session, error) {
	i.bridge.Agent = a
	strategy := NewRalphStrategy().SetCriticMode(CriticArchitect)
	strategy.SetImplementor(func(storyDesc string, ctx *Context) (any, error) {
		return i.bridge.ExecutePrompt("Implement the following story: " + storyDesc), nil
	})
	return i.engine.Start(&Config{
		TaskDescription: taskDesc, Strategy: strategy,
		MaxIterations: 50, VerificationRequired: true, StatePersistenceEnabled: true,
		Parameters: map[string]any{"sessionId": NewUUID()},
	})
}

// Pause/Resume/Stop 控制。
func (i *AgentIntegration) Pause(sessionID string)   { i.engine.Pause(sessionID) }
func (i *AgentIntegration) Resume(sessionID string)  { i.engine.Resume(sessionID) }
func (i *AgentIntegration) Stop(sessionID string)    { i.engine.Stop(sessionID) }

// GetAgentExecutionStatus 状态查询（无会话 NOT_FOUND）。
func (i *AgentIntegration) GetAgentExecutionStatus(sessionID string) ExecutionStatus {
	s, ok := i.engine.GetSession(sessionID)
	if !ok || s == nil {
		return ExecutionStatus{SessionID: sessionID, State: "NOT_FOUND"}
	}
	return ExecutionStatus{
		SessionID: sessionID, State: string(s.State()),
		IterationCount: s.IterationCount(), SuccessRate: s.SuccessRate(),
	}
}

// ==================== Flow 桥（SolonFlowIntegration 对应物） ====================

// FlowBridge flow 上下文桥（vars 提取/结果回写）。
type FlowBridge struct {
	Context flow.Context
}

// ExtractFlowVariables 变量提取（nil → 空 map）。
func (b *FlowBridge) ExtractFlowVariables() map[string]any {
	if b.Context == nil {
		return map[string]any{}
	}
	return b.Context.Vars()
}

// WriteBackResult 结果回写（nil no-op）。
func (b *FlowBridge) WriteBackResult(key string, value any) {
	if b.Context != nil {
		b.Context.Put(key, value)
	}
}

// FlowExecutionStatus 流执行状态（含 flowId）。
type FlowExecutionStatus struct {
	SessionID      string
	State          string
	IterationCount int
	SuccessRate    float64
	FlowID         string
}

// FlowIntegration flow 集成门面。
type FlowIntegration struct {
	engine Engine
	bridge *FlowBridge
}

// NewFlowIntegration 构造。
func NewFlowIntegration(engine Engine) *FlowIntegration {
	return &FlowIntegration{engine: engine, bridge: &FlowBridge{}}
}

// Bridge 流桥。
func (i *FlowIntegration) Bridge() *FlowBridge { return i.bridge }

// StartFlowRalphLoop flow 关联的 Ralph 循环。
func (i *FlowIntegration) StartFlowRalphLoop(flowID, taskDesc string, validator Validator) (Session, error) {
	strategy := NewRalphStrategy().SetCriticMode(CriticArchitect)
	return i.engine.Start(&Config{
		TaskDescription: taskDesc, Strategy: strategy, Validator: validator,
		MaxIterations: 50, VerificationRequired: true, StatePersistenceEnabled: true,
		Parameters: map[string]any{"flowId": flowID},
	})
}

// StartFlowDrivenPipeline flow 变量驱动的管道（变量提取 + flowId 附加、验证关）。
func (i *FlowIntegration) StartFlowDrivenPipeline(flowID string, strategy Strategy) (Session, error) {
	vars := i.bridge.ExtractFlowVariables()
	vars["flowId"] = flowID
	return i.engine.Start(&Config{
		TaskDescription: "Flow-driven pipeline: " + flowID, Strategy: strategy,
		MaxIterations: strategy.MaxIterations(), VerificationRequired: false,
		StatePersistenceEnabled: true, Parameters: vars,
	})
}

// CreateFlowBasedConfig flow 配置便捷。
func (i *FlowIntegration) CreateFlowBasedConfig(flowID string, strategy Strategy, validator Validator) *Config {
	return &Config{
		TaskDescription: "Flow-based execution: " + flowID, Strategy: strategy,
		Validator: validator, MaxIterations: strategy.MaxIterations(),
		VerificationRequired: true, StatePersistenceEnabled: true,
		Parameters: map[string]any{"flowId": flowID},
	}
}

// Pause/Resume/Stop 控制。
func (i *FlowIntegration) Pause(sessionID string)  { i.engine.Pause(sessionID) }
func (i *FlowIntegration) Resume(sessionID string) { i.engine.Resume(sessionID) }
func (i *FlowIntegration) Stop(sessionID string)   { i.engine.Stop(sessionID) }

// GetFlowExecutionStatus 状态查询（flowId 从上下文参数提取）。
func (i *FlowIntegration) GetFlowExecutionStatus(sessionID string) FlowExecutionStatus {
	s, ok := i.engine.GetSession(sessionID)
	if !ok || s == nil {
		return FlowExecutionStatus{SessionID: sessionID, State: "NOT_FOUND"}
	}
	flowID, _ := s.Context().GetParameter("flowId").(string)
	return FlowExecutionStatus{
		SessionID: sessionID, State: string(s.State()),
		IterationCount: s.IterationCount(), SuccessRate: s.SuccessRate(), FlowID: flowID,
	}
}

// WriteBackResult 结果回写透传。
func (i *FlowIntegration) WriteBackResult(sessionID, key string, value any) error {
	s, ok := i.engine.GetSession(sessionID)
	if !ok || s == nil {
		return fmt.Errorf("loop: session %s not found", sessionID)
	}
	if result := s.Result(); result != nil {
		i.bridge.WriteBackResult(key, result.Result)
	}
	return nil
}

// ==================== Harness 桥（SolonHarnessIntegration 对应物） ====================

// HarnessCaller harness 执行面（避免 loop → harness 的模块依赖：宿主注入闭包；
// Java 直接持 HarnessEngine，Go 以最小接口解耦）。
type HarnessCaller interface {
	// CallPrompt 以文本提示执行主代理，返回正文。
	CallPrompt(ctx context.Context, prompt string) (string, error)
}

// HarnessBridge harness 执行桥。
type HarnessBridge struct {
	Caller HarnessCaller
}

// ExecuteWithHarness 执行（错误转 "Harness execution error: ..."——Java 同款）。
func (b *HarnessBridge) ExecuteWithHarness(prompt string) string {
	if b.Caller == nil {
		return "Harness execution error: harness not injected"
	}
	out, err := b.Caller.CallPrompt(context.Background(), prompt)
	if err != nil {
		return "Harness execution error: " + err.Error()
	}
	return out
}

// ToolExecutionStatus 工具执行状态。
type ToolExecutionStatus struct {
	SessionID        string
	State            string
	IterationCount   int
	SuccessRate      float64
	HarnessAvailable bool
}

// HarnessIntegration harness 集成门面。
type HarnessIntegration struct {
	engine Engine
	bridge *HarnessBridge
}

// NewHarnessIntegration 构造。
func NewHarnessIntegration(engine Engine) *HarnessIntegration {
	return &HarnessIntegration{engine: engine, bridge: &HarnessBridge{}}
}

// Bridge harness 桥。
func (i *HarnessIntegration) Bridge() *HarnessBridge { return i.bridge }

// StartToolUltraQALoop 工具驱动质量门禁（10 轮）。
func (i *HarnessIntegration) StartToolUltraQALoop(taskDesc string, validator Validator) (Session, error) {
	strategy := NewUltraQAStrategy()
	strategy.MaxTestAttempts = 10
	return i.engine.Start(&Config{
		TaskDescription: taskDesc, Strategy: strategy, Validator: validator,
		MaxIterations: 100, VerificationRequired: true, StatePersistenceEnabled: true,
	})
}

// StartHarnessDrivenUltraQA harness 驱动（5 轮、验证关——Java 同款）。
func (i *HarnessIntegration) StartHarnessDrivenUltraQA(taskDesc string, caller HarnessCaller) (Session, error) {
	i.bridge.Caller = caller
	strategy := NewUltraQAStrategy()
	strategy.MaxTestAttempts = 5
	return i.engine.Start(&Config{
		TaskDescription: taskDesc, Strategy: strategy,
		MaxIterations: 20, VerificationRequired: false, StatePersistenceEnabled: true,
		Parameters: map[string]any{"harnessMode": true, "sessionId": NewUUID()},
	})
}

// Pause/Resume/Stop 控制。
func (i *HarnessIntegration) Pause(sessionID string)  { i.engine.Pause(sessionID) }
func (i *HarnessIntegration) Resume(sessionID string) { i.engine.Resume(sessionID) }
func (i *HarnessIntegration) Stop(sessionID string)   { i.engine.Stop(sessionID) }

// GetToolExecutionStatus 状态查询。
func (i *HarnessIntegration) GetToolExecutionStatus(sessionID string) ToolExecutionStatus {
	s, ok := i.engine.GetSession(sessionID)
	if !ok || s == nil {
		return ToolExecutionStatus{SessionID: sessionID, State: "NOT_FOUND"}
	}
	return ToolExecutionStatus{
		SessionID: sessionID, State: string(s.State()),
		IterationCount: s.IterationCount(), SuccessRate: s.SuccessRate(),
		HarnessAvailable: i.bridge.Caller != nil,
	}
}
