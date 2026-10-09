// sequential.go 顺序协作协议（对标 team.protocol.SequentialProtocol +
// SequentialRoutingTask）：按成员定义的物理顺序线性调度，零 LLM 决策。
// 具备质量门禁与模态安全检查（多模态输入自动跳过不兼容成员）。
package team

import (
	"encoding/json"
	"strings"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/flow"
)

// IDRouting Sequential 路由节点 id。
const IDRouting = "routing"

// keySequenceState 协议上下文键。
const keySequenceState = "sequence_state_obj"

// summaryMaxLength 阶段摘要截断长度。
const summaryMaxLength = 50

// StageStatus 阶段状态。
type StageStatus string

const (
	StagePending   StageStatus = "PENDING"
	StageCompleted StageStatus = "COMPLETED"
	StageRetrying  StageStatus = "RETRYING"
	StageFailed    StageStatus = "FAILED"
	StageSkipped   StageStatus = "SKIPPED"
)

// StageInfo 阶段信息。
type StageInfo struct {
	Status  StageStatus
	Retries int
	Summary string
}

// SequenceState 流水线状态机（断点续传：按 records 恢复各阶段状态与索引）。
type SequenceState struct {
	pipeline     []string
	currentIndex int
	stages       map[string]*StageInfo
}

// GetPipeline 流水线（成员注册序）。
func (s *SequenceState) GetPipeline() []string { return append([]string(nil), s.pipeline...) }

// GetCurrentIndex 当前阶段索引。
func (s *SequenceState) GetCurrentIndex() int { return s.currentIndex }

// GetStages 阶段表快照。
func (s *SequenceState) GetStages() map[string]*StageInfo {
	out := make(map[string]*StageInfo, len(s.stages))
	for k, v := range s.stages {
		c := *v
		out[k] = &c
	}
	return out
}

// init 首次访问时以配置成员序初始化；按 records 恢复断点（找第一个未终态阶段，
// 避免 SKIPPED/乱序参与时用完成数错位）。
func (s *SequenceState) init(trace *Trace) {
	if len(s.pipeline) > 0 {
		return
	}
	if trace == nil || trace.Config() == nil {
		return
	}

	s.pipeline = trace.Config().AgentNames()
	s.stages = make(map[string]*StageInfo, len(s.pipeline))
	inPipeline := map[string]bool{}
	for _, name := range s.pipeline {
		s.stages[name] = &StageInfo{Status: StagePending}
		inPipeline[name] = true
	}

	recovered := make(map[string]StageStatus, len(s.pipeline))
	for _, r := range trace.Records() {
		if r == nil || !r.IsAgent() || !inPipeline[r.Source()] {
			continue
		}
		content := r.Content()
		switch {
		case strings.Contains(content, "Incompatible modality") || strings.Contains(content, "SKIPPED"):
			recovered[r.Source()] = StageSkipped
		case strings.Contains(content, "Quality check failed") || strings.Contains(content, "FAILED"):
			recovered[r.Source()] = StageFailed
		default:
			recovered[r.Source()] = StageCompleted
		}
	}
	for name, status := range recovered {
		if info := s.stages[name]; info != nil {
			info.Status = status
		}
	}

	s.currentIndex = 0
	for s.currentIndex < len(s.pipeline) {
		info := s.stages[s.pipeline[s.currentIndex]]
		if info == nil {
			break
		}
		if info.Status == StageCompleted || info.Status == StageFailed || info.Status == StageSkipped {
			s.currentIndex++
		} else {
			break
		}
	}
}

// getNextAgent 下一位执行者（流水线耗尽 → end）。
func (s *SequenceState) getNextAgent() string {
	if s.currentIndex < len(s.pipeline) {
		return s.pipeline[s.currentIndex]
	}
	return agent.IDEnd
}

// markCurrent 标记当前阶段。
func (s *SequenceState) markCurrent(status StageStatus, summary string) {
	if s.currentIndex < len(s.pipeline) {
		s.markAgent(s.pipeline[s.currentIndex], status, summary)
	}
}

// markAgent 按 agent 名标记阶段（onAgentEnd 时与 currentIndex 对齐校验）。
func (s *SequenceState) markAgent(agentName string, status StageStatus, summary string) {
	info := s.stages[agentName]
	if info == nil {
		return
	}
	info.Status = status
	if summary != "" {
		if len(summary) > summaryMaxLength {
			summary = summary[:summaryMaxLength] + "..."
		}
		info.Summary = summary
	}
}

// next 推进一步。
func (s *SequenceState) next() { s.currentIndex++ }

// advancePast 将 currentIndex 推进到指定 agent 的下一位置（不在 pipeline 则
// next()）。
func (s *SequenceState) advancePast(agentName string) {
	for i, name := range s.pipeline {
		if name == agentName {
			s.currentIndex = i + 1
			return
		}
	}
	s.next()
}

// rewindTo 将 currentIndex 回退到指定 agent（阶段重试）。
func (s *SequenceState) rewindTo(agentName string) {
	for i, name := range s.pipeline {
		if name == agentName {
			s.currentIndex = i
			return
		}
	}
}

// String 进度看板（JSON）。
func (s *SequenceState) String() string {
	root := map[string]any{}
	progress := s.currentIndex + 1
	if progress > len(s.pipeline) {
		progress = len(s.pipeline)
	}
	root["progress"] = itoa(progress) + "/" + itoa(len(s.pipeline))
	stagesNode := make([]map[string]any, 0, len(s.pipeline))
	for _, name := range s.pipeline {
		v := s.stages[name]
		stagesNode = append(stagesNode, map[string]any{
			"agent": name, "status": string(v.Status), "retries": v.Retries,
		})
	}
	root["stages"] = stagesNode
	data, err := json.Marshal(root)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// SequentialProtocol 顺序协作协议。
type SequentialProtocol struct {
	ProtocolBase

	maxRetriesPerStage int
}

// NewSequentialProtocol 工厂。
func NewSequentialProtocol(config *Config) *SequentialProtocol {
	p := &SequentialProtocol{
		ProtocolBase:       NewProtocolBase(config),
		maxRetriesPerStage: 1,
	}
	p.setProtocolName("SEQUENTIAL")
	return p
}

// SetMaxRetriesPerStage 阶段质量重试上限。
func (p *SequentialProtocol) SetMaxRetriesPerStage(n int) { p.maxRetriesPerStage = n }

// Name 协议标识。
func (p *SequentialProtocol) Name() string { return "SEQUENTIAL" }

// getSequenceState 状态看板（computeIfAbsent，断点续传）。
func (p *SequentialProtocol) getSequenceState(trace *Trace) *SequenceState {
	if v, ok := trace.ProtocolContext().Load(keySequenceState); ok {
		if s, ok := v.(*SequenceState); ok {
			return s
		}
	}
	s := &SequenceState{}
	s.init(trace)
	actual, loaded := trace.ProtocolContext().LoadOrStore(keySequenceState, s)
	if loaded {
		if s2, ok := actual.(*SequenceState); ok {
			return s2
		}
	}
	return s
}

// BuildGraph 静态拓扑：Start → routing ← Agents；routing（exclusive）→ 成员/End。
func (p *SequentialProtocol) BuildGraph(spec *flow.GraphSpec) {
	spec.AddStart(agent.IDStart).LinkAdd(IDRouting)

	for _, a := range p.config.Agents() {
		spec.AddActivityNamed(a).LinkAdd(IDRouting)
	}

	spec.AddExclusiveNamed(NewSequentialRoutingTask(p.config, p)).Then(func(ns *flow.NodeSpec) {
		p.linkAgents(ns)
		ns.LinkAdd(agent.IDEnd)
	})

	spec.AddEnd(agent.IDEnd)
}

// PrepareSupervisorInstruction 流水线进度看板（Sequential 不走 LLM 决策，仅
// 保留协议面完整性）。
func (p *SequentialProtocol) PrepareSupervisorInstruction(_ flow.Context, trace *Trace, sb *strings.Builder) {
	state := p.getSequenceState(trace)
	sb.WriteString("\n### 流水线进度 (Pipeline Progress)\n")
	sb.WriteString("```json\n" + state.String() + "\n```\n")
}

// OnAgentEnd 质量门禁：成功推进流水线；失败在重试额度内回退重试，否则 FAILED
// 跳过；HITL 挂起冻结当前阶段（不消耗重试额度）。
func (p *SequentialProtocol) OnAgentEnd(trace *Trace, ag agent.Agent) {
	state := p.getSequenceState(trace)
	content := trace.LastAgentContent()
	isPending := trace.Session() != nil && trace.Session().IsPending()
	info := state.stages[ag.Name()]

	// HITL / 人工挂起：冻结当前阶段，不消耗质量重试额度，也不推进流水线
	if isPending {
		state.markAgent(ag.Name(), StageRetrying, "Pending human intervention")
		state.rewindTo(ag.Name())
		trace.SetRoute(ag.Name())
		return
	}

	if assessQuality(content) {
		state.markAgent(ag.Name(), StageCompleted, content)
		state.advancePast(ag.Name())
		trace.SetRoute(IDRouting)
	} else {
		if info != nil && info.Retries < p.maxRetriesPerStage {
			info.Retries++
			state.markAgent(ag.Name(), StageRetrying, "Quality check failed")
			// 回退 currentIndex 到该 agent，确保重试时 getNextAgent 正确
			state.rewindTo(ag.Name())
			trace.SetRoute(ag.Name())
		} else {
			state.markAgent(ag.Name(), StageFailed, "Quality check failed")
			state.advancePast(ag.Name())
			trace.SetRoute(IDRouting)
		}
	}
}

// DetectMultiModalPresence 上下文是否含多模态输入：优先看 originalPrompt 的
// user 消息，再回退扫描最近专家产出中的 markdown 图片标记。
func (p *SequentialProtocol) DetectMultiModalPresence(trace *Trace) bool {
	if trace == nil {
		return false
	}

	if original := trace.OriginalPrompt(); original != nil {
		for _, m := range original.Messages() {
			if m == nil || m.Role != ai.RoleUser {
				continue
			}
			if isMultiModalContent(m.Content) {
				return true
			}
		}
	}

	content := trace.LastAgentContent()
	if content == "" {
		return false
	}
	return strings.Contains(content, "![image]") || strings.Contains(content, "](data:image/")
}

// SupportsImage 专家是否支持图像模态（profile / inputModes 空安全）。
func (p *SequentialProtocol) SupportsImage(ag agent.Agent) bool {
	if ag == nil || ag.Profile() == nil {
		return false
	}
	modes := ag.Profile().InputModes()
	for _, m := range modes {
		if strings.EqualFold(m, "image") {
			return true
		}
	}
	return false
}

// isLogicFinished 以 pipeline 推进为准：至少走过最后一阶段（含 SKIPPED/FAILED）
// 才允许逻辑完结。
func (p *SequentialProtocol) isLogicFinished(trace *Trace) bool {
	state := p.getSequenceState(trace)
	return len(state.pipeline) > 0 && state.currentIndex >= len(state.pipeline)
}

// assessQuality 基础质量检查：内容长度超过 2 个字符视为有效。
func assessQuality(content string) bool {
	if content == "" {
		return false
	}
	return len([]rune(strings.TrimSpace(content))) > 2
}

// SequentialRoutingTask 顺序协作协议的物理流水线控制器。
type SequentialRoutingTask struct {
	config   *Config
	protocol *SequentialProtocol
}

// NewSequentialRoutingTask 构建路由任务。
func NewSequentialRoutingTask(config *Config, protocol *SequentialProtocol) *SequentialRoutingTask {
	return &SequentialRoutingTask{config: config, protocol: protocol}
}

// Name 任务标识（routing）。
func (t *SequentialRoutingTask) Name() string { return IDRouting }

// Title 同名。
func (t *SequentialRoutingTask) Title() string { return IDRouting }

// Run 核心逻辑：取下一位应执行的 Agent（含模态安全检查的自动跳过）。
func (t *SequentialRoutingTask) Run(ctx flow.Context, _ *flow.Node) error {
	trace, _ := ctx.Get(t.config.TraceKey()).(*Trace)
	if trace == nil {
		return nil
	}

	// 获取并同步状态
	state := t.protocol.getSequenceState(trace)

	// 核心逻辑：获取下一个应该执行的 Agent
	next := state.getNextAgent()

	// 模态检查：多模态输入时跳过不支持 image 的专家
	for next != agent.IDEnd {
		nextAgent := t.config.Agent(next)
		if nextAgent == nil {
			state.markCurrent(StageSkipped, "Agent missing")
			state.next()
			next = state.getNextAgent()
			continue
		}

		if t.protocol.DetectMultiModalPresence(trace) && !t.protocol.SupportsImage(nextAgent) {
			state.markCurrent(StageSkipped, "Incompatible modality")
			state.next()
			next = state.getNextAgent()
			continue
		}
		break
	}

	// 物理重定向路由
	trace.SetRoute(next)
	return nil
}

// isMultiModalContent user 消息内容是否含多模态块（对标 UserMessage.isMultiModal）。
func isMultiModalContent(content any) bool {
	switch c := content.(type) {
	case []ai.ContentBlock:
		return len(c) > 0
	case *ai.Contents:
		return c.IsMultiModal()
	case ai.ContentBlock:
		return true
	}
	return false
}
