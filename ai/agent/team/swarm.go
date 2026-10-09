// swarm.go 蜂群协作协议（对标 team.protocol.SwarmProtocol）：去中心化入口 +
// 任务涌现池 + 信息素负载感知调度。Start 直连首位专家（不经 Supervisor），
// 成员经 __emerge_tasks__ 工具或 sub_tasks JSON 涌现子任务进池，Supervisor
// 按信息素看板调度；池未清空前 FINISH 被物理阻断。
package team

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/flow"
)

// keySwarmState 协议上下文键。
const keySwarmState = "swarm_state_obj"

// toolEmerge 任务涌现工具名。
const toolEmerge = "__emerge_tasks__"

// 信息素步长与任务池上限。
const (
	pheromoneDecay     = 1 // 挥发步长
	pheromoneReinforce = 5 // 增强步长
	maxTaskPoolSize    = 32
)

// SwarmTask 涌现任务实体。
type SwarmTask struct {
	Task  string
	Agent string // 建议专家名（空 = 未指定）
}

// SwarmState 蜂群状态实体：信息素表 + 任务池（Java synchronized → mutex）。
type SwarmState struct {
	mu         sync.Mutex
	pheromones map[string]int
	taskPool   []*SwarmTask
}

// NewSwarmState 空状态。
func NewSwarmState() *SwarmState {
	return &SwarmState{pheromones: map[string]int{}}
}

// GetPheromones 信息素表快照。
func (s *SwarmState) GetPheromones() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int, len(s.pheromones))
	for k, v := range s.pheromones {
		out[k] = v
	}
	return out
}

// GetTaskPool 任务池快照。
func (s *SwarmState) GetTaskPool() []*SwarmTask {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*SwarmTask(nil), s.taskPool...)
}

// Emerge 将涌现任务写入任务池。tasks 支持 , ; ， ； 换行分隔；agents 与 tasks
// 按序对齐；validAgents 非空时做规范化与非法名降级（降级为未标注，不污染调度）。
func (s *SwarmState) Emerge(tasks, agents string, validAgents []string) {
	if tasks == "" {
		return
	}

	taskArr := strings.FieldsFunc(tasks, swarmSplit)
	var agentArr []string
	if agents != "" {
		agentArr = strings.FieldsFunc(agents, swarmSplit)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for i, task := range taskArr {
		if len(s.taskPool) >= maxTaskPoolSize {
			break // 防长会话无限膨胀
		}

		task = strings.TrimSpace(task)
		if task == "" {
			continue
		}

		agentName := ""
		if i < len(agentArr) {
			agentName = strings.TrimSpace(agentArr[i])
		}
		if agentName != "" {
			if canonical := swarmCanonicalAgentName(agentName, validAgents); canonical != "" {
				agentName = canonical
			} else {
				agentName = "" // 非法专家名降级为未标注
			}
		}

		if s.containsTask(task, agentName) {
			continue
		}
		s.taskPool = append(s.taskPool, &SwarmTask{Task: task, Agent: agentName})
	}
}

// swarmSplit FieldsFunc 分隔规则：, ; ， ； 换行。
func swarmSplit(r rune) bool {
	return r == ',' || r == ';' || r == '，' || r == '；' || r == '\n'
}

// DecayAndReinforce 信息素挥发（全员 -1，下限 0）与当前专家增强（+5）。
func (s *SwarmState) DecayAndReinforce(currentAgent string) {
	if currentAgent == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.pheromones {
		if v > pheromoneDecay {
			s.pheromones[k] = v - pheromoneDecay
		} else {
			s.pheromones[k] = 0
		}
	}
	s.pheromones[currentAgent] += pheromoneReinforce
}

// Consume 仅消费「指派给该 Agent 的第一条任务」，不做任务文本匹配，避免误删。
func (s *SwarmState) Consume(nextAgent string) {
	if nextAgent == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, t := range s.taskPool {
		if strings.EqualFold(nextAgent, t.Agent) {
			s.taskPool = append(s.taskPool[:i], s.taskPool[i+1:]...)
			return
		}
	}
}

// PickNextAgent 从任务池挑选下一位执行者：优先低信息素、且尽量避开刚执行完
// 的 Agent（冷却加权）。池中任务均未标注专家时按信息素兜底选择。
func (s *SwarmState) PickNextAgent(agentNames []string, lastAgent string) string {
	if len(agentNames) == 0 {
		return ""
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.taskPool) == 0 {
		return ""
	}

	// 1) 优先：任务池中已标注合法专家的任务
	bestTagged, bestTaggedScore := "", int(^uint(0)>>1) // MaxInt
	for _, t := range s.taskPool {
		canonical := swarmCanonicalAgentName(t.Agent, agentNames)
		if canonical == "" {
			continue
		}
		if score := s.scoreAgent(canonical, lastAgent); score < bestTaggedScore {
			bestTaggedScore = score
			bestTagged = canonical
		}
	}
	if bestTagged != "" {
		return bestTagged
	}

	// 2) 兜底：池中有未标注专家的任务时，按信息素选负载最低者
	hasUntagged := false
	for _, t := range s.taskPool {
		if t.Agent == "" {
			hasUntagged = true
			break
		}
	}
	if !hasUntagged {
		return ""
	}

	best, bestScore := "", int(^uint(0)>>1)
	for _, name := range agentNames {
		if score := s.scoreAgent(name, lastAgent); score < bestScore {
			bestScore = score
			best = name
		}
	}
	return best
}

func (s *SwarmState) scoreAgent(agentName, lastAgent string) int {
	p := s.pheromones[agentName]
	// 刚执行完的 Agent 额外加权，形成冷却
	if lastAgent != "" && strings.EqualFold(agentName, lastAgent) {
		p += pheromoneReinforce
	}
	return p
}

func (s *SwarmState) containsTask(task, agent string) bool {
	for _, t := range s.taskPool {
		if t.Task == task && strings.EqualFold(t.Agent, agent) {
			return true
		}
	}
	return false
}

// swarmCanonicalAgentName 在候选集中 EqualFold 命中规范名（空名/未命中空串）。
func swarmCanonicalAgentName(name string, agentNames []string) string {
	if name == "" {
		return ""
	}
	if agentNames == nil {
		return name
	}
	for _, candidate := range agentNames {
		if strings.EqualFold(candidate, name) {
			return candidate
		}
	}
	return ""
}

// MarshalJSON 状态看板（信息素 + 任务池；键名对齐提示词中的 task_pool 表述）。
func (s *SwarmState) MarshalJSON() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pool := make([]map[string]any, 0, len(s.taskPool))
	for _, t := range s.taskPool {
		pool = append(pool, map[string]any{"task": t.Task, "agent": t.Agent})
	}
	return json.Marshal(map[string]any{
		"pheromones": s.pheromones,
		"task_pool":  pool,
	})
}

// String 状态看板 JSON。
func (s *SwarmState) String() string {
	data, err := s.MarshalJSON()
	if err != nil {
		return "{}"
	}
	return string(data)
}

// SwarmProtocol 蜂群协作协议。
type SwarmProtocol struct {
	ProtocolBase
}

// NewSwarmProtocol 工厂。
func NewSwarmProtocol(config *Config) *SwarmProtocol {
	p := &SwarmProtocol{ProtocolBase: NewProtocolBase(config)}
	p.setProtocolName("SWARM")
	return p
}

// Name 协议标识。
func (p *SwarmProtocol) Name() string { return "SWARM" }

// getSwarmState 状态机 computeIfAbsent。
func (p *SwarmProtocol) getSwarmState(trace *Trace) *SwarmState {
	if v, ok := trace.ProtocolContext().Load(keySwarmState); ok {
		if s, ok := v.(*SwarmState); ok {
			return s
		}
	}
	s := NewSwarmState()
	actual, loaded := trace.ProtocolContext().LoadOrStore(keySwarmState, s)
	if loaded {
		if s2, ok := actual.(*SwarmState); ok {
			return s2
		}
	}
	return s
}

// BuildGraph 去中心化拓扑：Start 直连首位专家（不经 Supervisor 决策冷启动），
// 各专家回连 Supervisor，Supervisor exclusive 路由全员或 End。无成员即失败。
func (p *SwarmProtocol) BuildGraph(spec *flow.GraphSpec) {
	names := p.config.AgentNames()
	if len(names) == 0 {
		panic("SwarmProtocol requires at least one agent")
	}

	spec.AddStart(agent.IDStart).LinkAdd(names[0])

	for _, a := range p.config.Agents() {
		spec.AddActivityNamed(a).LinkAdd(IDSupervisor)
	}

	spec.AddExclusiveNamed(NewSupervisorTask(p.config)).Then(func(ns *flow.NodeSpec) {
		p.linkAgents(ns)
	}).LinkAdd(agent.IDEnd)

	spec.AddEnd(agent.IDEnd)
}

// InjectAgentTools 注入 __emerge_tasks__ 涌现工具（returnDirect + META_AGENT）。
func (p *SwarmProtocol) InjectAgentTools(ctx flow.Context, ag agent.Agent, add func(ai.FunctionTool)) {
	trace := Current(ctx)
	if trace == nil {
		return
	}
	state := p.getSwarmState(trace)

	validAgents := p.config.AgentNames()

	tool := ai.NewTool(toolEmerge).
		ReturnDirect(true).
		Title("涌现任务").
		Desc("当你发现需要拆解子任务或建议其他专家介入时调用。").
		StringParam("tasks", "待办任务描述，多个用分号分隔", true).
		StringParam("agents", "建议执行的专家名，多个用分号分隔", false).
		MetaSet(agent.MetaAgent, ag.Name()).
		Handler(func(_ context.Context, args map[string]any) (any, error) {
			state.Emerge(stringValue(args["tasks"]), stringValue(args["agents"]), validAgents)
			return "系统：子任务已加入任务池。", nil
		}).
		Build()

	add(tool)
}

// InjectAgentInstruction 基类身份/协作历史 + 蜂群规范。
func (p *SwarmProtocol) InjectAgentInstruction(ctx flow.Context, ag agent.Agent, sb *strings.Builder) {
	p.ProtocolBase.InjectAgentInstruction(ctx, ag, sb)

	sb.WriteString("\n## 蜂群协作规范\n")
	sb.WriteString("- **任务涌现**：若当前任务需他人配合或拆解，请调用 `" + toolEmerge + "` 将子任务放入池中。\n")
	sb.WriteString("- **专注性**：仅在发现新任务维度时调用工具，不要重复同步已有任务。\n")
}

// PrepareAgentPrompt 基类副本上追加任务池快照（降低重复涌现）。
func (p *SwarmProtocol) PrepareAgentPrompt(trace *Trace, ag agent.Agent, originalPrompt *ai.Prompt) *ai.Prompt {
	finalPrompt := p.ProtocolBase.PrepareAgentPrompt(trace, ag, originalPrompt)

	v, _ := trace.ProtocolContext().Load(keySwarmState)
	state, _ := v.(*SwarmState)
	if state == nil || len(state.GetTaskPool()) == 0 {
		return finalPrompt
	}

	snapshot := "【蜂群任务池快照】\n```json\n" + state.String() + "\n```"
	return finalPrompt.AddMessage(ai.OfUser(snapshot))
}

// ResolveAgentOutput 兼容文本 JSON 涌现：解析 sub_tasks:[{task,agent},...] 写入
// 任务池（工具路径仍是主路径；此逻辑保证 JSON 输出可被识别）。内容原样透传。
func (p *SwarmProtocol) ResolveAgentOutput(trace *Trace, ag agent.Agent, content string) string {
	if content == "" {
		return content
	}

	jsonNode := p.sniffJson(content)
	if jsonNode == nil {
		return content
	}
	raw, ok := jsonNode["sub_tasks"]
	if !ok {
		return content
	}
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		return content
	}

	state := p.getSwarmState(trace)
	validAgents := p.config.AgentNames()
	var tasks, agents []string
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		task := strings.TrimSpace(stringValue(m["task"]))
		if task == "" {
			continue
		}
		tasks = append(tasks, task)
		agents = append(agents, strings.TrimSpace(stringValue(m["agent"])))
	}

	if len(tasks) > 0 {
		state.Emerge(strings.Join(tasks, ";"), strings.Join(agents, ";"), validAgents)
	}
	return content
}

// ResolveSupervisorRoute 解析主管路由：finish + 任务池非空 + 未达 maxTurns 时
// 硬回派池中下一 Agent（避免 commitRoute 在 shouldSupervisorRoute=false 后仍
// 因无法 matchAgent 而误入 END）；正常 finish 返回空，交统一收口。
func (p *SwarmProtocol) ResolveSupervisorRoute(ctx flow.Context, trace *Trace, decision string) string {
	if strings.Contains(decision, p.config.FinishMarker()) {
		state := p.getSwarmState(trace)
		maxTurns := swarmMaxTurnsOf(trace)

		if len(state.GetTaskPool()) > 0 && trace.TurnCount() < maxTurns {
			next := state.PickNextAgent(p.config.AgentNames(), trace.LastAgentName())
			if next != "" {
				return next
			}
			// 池中任务均无法解析到合法 Agent：返回空，由 shouldSupervisorRoute 拦截
			return ""
		}

		// 正常 finish：不在此直接 END
		return ""
	}

	return p.ProtocolBase.ResolveSupervisorRoute(ctx, trace, decision)
}

// ShouldSupervisorRoute 终结守卫：任务池非空且未达 maxTurns 时拦截 FINISH；
// 保留基类 SOP 守卫。
func (p *SwarmProtocol) ShouldSupervisorRoute(ctx flow.Context, trace *Trace, decision string) bool {
	if strings.Contains(decision, p.config.FinishMarker()) {
		state := p.getSwarmState(trace)
		if len(state.GetTaskPool()) > 0 && trace.TurnCount() < swarmMaxTurnsOf(trace) {
			return false
		}
	}
	return p.ProtocolBase.ShouldSupervisorRoute(ctx, trace, decision)
}

// OnAgentEnd 信息素挥发与当前专家增强。
func (p *SwarmProtocol) OnAgentEnd(trace *Trace, ag agent.Agent) {
	if ag != nil {
		p.getSwarmState(trace).DecayAndReinforce(ag.Name())
	}
	p.ProtocolBase.OnAgentEnd(trace, ag)
}

// OnSupervisorRouting 路由落定后消费任务池中该专家的首条任务（系统节点除外）。
func (p *SwarmProtocol) OnSupervisorRouting(ctx flow.Context, trace *Trace, nextAgent string) {
	if nextAgent != "" &&
		nextAgent != agent.IDEnd &&
		nextAgent != agent.IDStart &&
		nextAgent != IDSupervisor &&
		nextAgent != IDSystem {
		p.getSwarmState(trace).Consume(nextAgent)
	}
	p.ProtocolBase.OnSupervisorRouting(ctx, trace, nextAgent)
}

// InjectSupervisorInstruction 协议静态准则 + SWARM 决策准则。
func (p *SwarmProtocol) InjectSupervisorInstruction(sb *strings.Builder) {
	p.ProtocolBase.InjectSupervisorInstruction(sb)

	sb.WriteString("\n### SWARM 决策准则：\n")
	sb.WriteString("1. **任务终止**：若任务完成，输出 `" + p.config.FinishMarker() + "`。\n")
	sb.WriteString("2. **决策优先**：若未完成，仅输出下一位 Agent 名字。不要解释。\n")
	sb.WriteString("3. **任务池优先**：看板 task_pool 非空时，优先指派其中建议的专家。")
}

// PrepareSupervisorInstruction 蜂群状态看板（信息素 + 任务池）与调度指引。
func (p *SwarmProtocol) PrepareSupervisorInstruction(_ flow.Context, trace *Trace, sb *strings.Builder) {
	state := p.getSwarmState(trace)

	sb.WriteString("\n### 蜂群状态看板 (Swarm Intelligence)\n")
	sb.WriteString("```json\n" + state.String() + "\n```\n")

	recommended := state.PickNextAgent(p.config.AgentNames(), trace.LastAgentName())

	sb.WriteString("> **调度指引**：\n")
	sb.WriteString("> 1. **负载均衡**：优先指派 pheromones 值较低的成员。\n")
	sb.WriteString("> 2. **能力匹配**：确保 task_pool 中的任务描述与 Agent 的 Capabilities 一致。\n")
	sb.WriteString("> 3. **冷却避障**：避免连续指派刚执行完的 Agent。\n")
	if recommended != "" && len(state.GetTaskPool()) > 0 {
		sb.WriteString("> 4. **推荐下一执行者**：`" + recommended + "`\n")
	}
}

// swarmMaxTurnsOf 配置缺省时的回退（Java 同款 5）。
func swarmMaxTurnsOf(trace *Trace) int {
	if trace.Options() != nil {
		return trace.Options().MaxTurns
	}
	return 5
}
