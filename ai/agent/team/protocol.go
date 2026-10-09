// protocol.go 团队协作协议 SPI（对标 team.TeamProtocol/TeamProtocolBase）：
// 4 阶段生命周期——静态构图 / 成员运行期 / supervisor 治理 / 收尾。
package team

import (
	"encoding/json"
	"strings"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/flow"
)

// Protocol 团队协作协议：定义多智能体协作的拓扑结构、指令干预与路由治理。
// 成员运行期五方法即 agent.TeamProtocol 窄面（经 protocolNarrow 适配后写入
// KeyProtocol，供成员智能体的 AgentBase.Run/react Call 消费）。
type Protocol interface {
	// Name 协议唯一标识（如 SEQUENTIAL / HIERARCHICAL / SWARM）。
	Name() string

	// ---- [阶段 1：静态构建] ----

	// BuildGraph 构建协作拓扑图。
	BuildGraph(spec *flow.GraphSpec)

	// ---- [阶段 2：成员 Agent 运行期] ----

	// InjectAgentTools 注入协议专属工具（如转交、抄送等控制工具）。
	InjectAgentTools(ctx flow.Context, ag agent.Agent, add func(ai.FunctionTool))
	// InjectAgentInstruction 注入行为约束指令（角色规范）。
	InjectAgentInstruction(ctx flow.Context, ag agent.Agent, sb *strings.Builder)
	// PrepareAgentPrompt 动态生成提示词。入参通常是共享的 workingMemory——
	// 若需追加协议消息必须先 Copy 再改，禁止原地修改（copy-before-mutate 铁律，
	// 跨 agent 串写即源于违反此约束）。
	PrepareAgentPrompt(trace *Trace, ag agent.Agent, originalPrompt *ai.Prompt) *ai.Prompt
	// ResolveAgentOutput 解析并适配 Agent 输出（记录轨迹前的内容转换）。
	ResolveAgentOutput(trace *Trace, ag agent.Agent, content string) string
	// OnAgentEnd Agent 节点执行结束回调。
	OnAgentEnd(trace *Trace, ag agent.Agent)

	// ---- [阶段 3：主管 Supervisor 治理] ----

	// InjectSupervisorTools 注入 Supervisor 协议专属工具。
	InjectSupervisorTools(ctx flow.Context, add func(ai.FunctionTool))
	// InjectSupervisorInstruction 注入 Supervisor 静态系统指令。
	InjectSupervisorInstruction(sb *strings.Builder)
	// PrepareSupervisorInstruction 注入动态决策指令（实时进度、环境感知）。
	PrepareSupervisorInstruction(ctx flow.Context, trace *Trace, sb *strings.Builder)
	// PrepareSupervisorContext 注入决策上下文（待办、黑板状态等）。
	PrepareSupervisorContext(ctx flow.Context, trace *Trace, sb *strings.Builder)
	// ShouldSupervisorExecute 决策准入（false = 跳过 LLM 决策，用于固定流）。
	ShouldSupervisorExecute(ctx flow.Context, trace *Trace) bool
	// ResolveSupervisorRoute 解析路由目标（"" = 系统默认逻辑解析）。
	ResolveSupervisorRoute(ctx flow.Context, trace *Trace, decision string) string
	// ShouldSupervisorRoute 路由守卫（false = 拦截该决策，强制继续协作）。
	ShouldSupervisorRoute(ctx flow.Context, trace *Trace, decision string) bool
	// OnSupervisorRouting 路由目标确定后的最终回调。
	OnSupervisorRouting(ctx flow.Context, trace *Trace, nextAgent string)

	// ---- [阶段 4：收尾] ----

	// OnTeamFinished 协作任务结束清理（对标 onTeamFinished）。
	OnTeamFinished(ctx flow.Context, trace *Trace)
}

// protocolNarrow 宽面协议 → agent.TeamProtocol 窄面适配（*Trace 下探；非
// team.Trace 时走 base 缺省——copy 副本/原样透传）。
type protocolNarrow struct{ p Protocol }

func (n protocolNarrow) InjectAgentTools(ctx flow.Context, ag agent.Agent, add func(ai.FunctionTool)) {
	n.p.InjectAgentTools(ctx, ag, add)
}

func (n protocolNarrow) InjectAgentInstruction(ctx flow.Context, ag agent.Agent, sb *strings.Builder) {
	n.p.InjectAgentInstruction(ctx, ag, sb)
}

func (n protocolNarrow) PrepareAgentPrompt(trace agent.TeamTrace, ag agent.Agent, originalPrompt *ai.Prompt) *ai.Prompt {
	if t, ok := trace.(*Trace); ok {
		return n.p.PrepareAgentPrompt(t, ag, originalPrompt)
	}
	if originalPrompt == nil {
		return ai.NewPrompt()
	}
	return originalPrompt.Copy()
}

func (n protocolNarrow) ResolveAgentOutput(trace agent.TeamTrace, ag agent.Agent, content string) string {
	if t, ok := trace.(*Trace); ok {
		return n.p.ResolveAgentOutput(t, ag, content)
	}
	return content
}

func (n protocolNarrow) OnAgentEnd(trace agent.TeamTrace, ag agent.Agent) {
	if t, ok := trace.(*Trace); ok {
		n.p.OnAgentEnd(t, ag)
	}
}

// ProtocolBase 协议基类：拓扑链接工具、JSON 嗅探与 SOP 完备性检查。嵌入者
// 按需覆写（构造时回填 config）。
type ProtocolBase struct {
	config       *Config
	protocolName string // 子类构造时经 setProtocolName 回填
}

// NewProtocolBase 绑定团队配置。
func NewProtocolBase(config *Config) ProtocolBase { return ProtocolBase{config: config} }

// linkAgents 自动构建节点到全部成员的动态链接（when 按 route 大小写不敏感比对）。
func (p *ProtocolBase) linkAgents(ns *flow.NodeSpec) {
	for _, agentName := range p.config.AgentNames() {
		name := agentName
		ns.LinkAddConfig(name, func(l *flow.LinkSpec) {
			l.Title("route = " + name)
			l.WhenCond(flow.ConditionFunc(func(ctx flow.Context) (bool, error) {
				trace, _ := ctx.Get(p.config.TraceKey()).(*Trace)
				if trace == nil {
					return false, nil
				}
				return strings.EqualFold(trace.Route(), name), nil
			}))
		})
	}
}

// sniffJson 从文本中提取首个 JSON 对象（首 '{' 到末 '}' 的启发式；畸形返回 nil）。
func (p *ProtocolBase) sniffJson(content string) map[string]any {
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start == -1 || end <= start {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(content[start:end+1]), &out); err != nil {
		return nil
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// getCandidateAgents 候选成员列表（含无 profile 成员——仍可被调度，不应抹掉）。
func (p *ProtocolBase) getCandidateAgents() []string { return p.config.AgentNames() }

// isLogicFinished SOP 完备性检查：至少一名专家产出记录且已发生过决策轮次。
// 不再强制「注册末位 N 个 Agent 必须参与」——对非线性调度过硬，协议可覆写。
func (p *ProtocolBase) isLogicFinished(trace *Trace) bool {
	if trace == nil || trace.RecordCount() == 0 {
		return false
	}
	if trace.TurnCount() < 1 {
		return false
	}
	for _, r := range trace.Records() {
		if r.IsAgent() {
			return true
		}
	}
	return false
}

// isLastNAgentsParticipated 检查末尾 N 个成员是否已参与协作。
func (p *ProtocolBase) isLastNAgentsParticipated(trace *Trace, n int) bool {
	names := p.config.AgentNames()
	size := len(names)
	if size <= 1 {
		return true
	}
	lo := size - n
	if lo < 0 {
		lo = 0
	}
	requiredTail := names[lo:]
	participated := map[string]bool{}
	for _, r := range trace.Records() {
		participated[strings.ToLower(r.Source())] = true
	}
	for _, name := range requiredTail {
		if !participated[strings.ToLower(name)] {
			return false
		}
	}
	return true
}

// ---- 阶段 2 默认实现 ----

// InjectAgentTools 默认无协议工具。
func (p *ProtocolBase) InjectAgentTools(flow.Context, agent.Agent, func(ai.FunctionTool)) {}

// InjectAgentInstruction 注入成员身份与协作进度上下文（对标 base 实现，
// 中文文案）。
func (p *ProtocolBase) InjectAgentInstruction(ctx flow.Context, ag agent.Agent, sb *strings.Builder) {
	trace := Current(ctx)
	if trace == nil {
		return
	}

	profileDesc := ""
	if ag.Profile() != nil {
		profileDesc = ag.Profile().FormatString("zh")
	}
	windowSize := 5
	if trace.Options() != nil {
		if ws := trace.Options().RecordWindowSize; ws > 0 {
			windowSize = ws
		} else if ws == 0 {
			windowSize = 1
		}
	}

	sb.WriteString("## 任务上下文 (SYSTEM CONTEXT)\n")
	sb.WriteString("### 你的身份\n")
	sb.WriteString(profileDesc)
	sb.WriteString("\n\n")
	sb.WriteString("### 协作进度 (最近 " + itoa(windowSize) + " 条)\n")
	sb.WriteString(trace.FormattedHistoryWindow(windowSize, false))
	sb.WriteString("\n\n---\n请根据上述进度完成你的任务。")
}

// PrepareAgentPrompt 默认返回副本（copy-before-mutate 铁律的基座实现）。
func (p *ProtocolBase) PrepareAgentPrompt(_ *Trace, _ agent.Agent, originalPrompt *ai.Prompt) *ai.Prompt {
	if originalPrompt == nil {
		return ai.NewPrompt()
	}
	return originalPrompt.Copy()
}

// ResolveAgentOutput 默认原样透传。
func (p *ProtocolBase) ResolveAgentOutput(_ *Trace, _ agent.Agent, content string) string {
	return content
}

// OnAgentEnd 默认无操作。
func (p *ProtocolBase) OnAgentEnd(*Trace, agent.Agent) {}

// ---- 阶段 3 默认实现 ----

// InjectSupervisorTools 默认无协议工具。
func (p *ProtocolBase) InjectSupervisorTools(flow.Context, func(ai.FunctionTool)) {}

// InjectSupervisorInstruction 注入协作协议静态准则（候选成员 + 完成要求）。
func (p *ProtocolBase) InjectSupervisorInstruction(sb *strings.Builder) {
	candidates := strings.Join(p.getCandidateAgents(), ", ")
	sb.WriteString("\n## 协作协议：" + p.ProtocolName() + "\n")
	sb.WriteString("- 备选成员: [" + candidates + "]\n")
	sb.WriteString("- 决策要求：任务完成请输出 " + p.config.FinishMarker())
}

// ProtocolName 协议名（基类无法读接口 Name，子类构造时经 setProtocolName 回填，
// 或直接覆写本方法）。
func (p *ProtocolBase) ProtocolName() string { return p.protocolName }

func (p *ProtocolBase) setProtocolName(name string) { p.protocolName = name }

// PrepareSupervisorInstruction 默认无动态指令。
func (p *ProtocolBase) PrepareSupervisorInstruction(flow.Context, *Trace, *strings.Builder) {}

// PrepareSupervisorContext 默认无决策上下文。
func (p *ProtocolBase) PrepareSupervisorContext(flow.Context, *Trace, *strings.Builder) {}

// ShouldSupervisorExecute 默认始终执行 LLM 决策。
func (p *ProtocolBase) ShouldSupervisorExecute(flow.Context, *Trace) bool { return true }

// ResolveSupervisorRoute 默认成员名精确命中（大小写不敏感）才接管。
func (p *ProtocolBase) ResolveSupervisorRoute(_ flow.Context, _ *Trace, decision string) string {
	if ag := p.config.Agent(decision); ag != nil && decision != "" {
		if canonical, ok := p.config.lowerIndex[strings.ToLower(decision)]; ok {
			return canonical
		}
	}
	return "" // 触发 SupervisorTask 的模糊匹配兜底
}

// ShouldSupervisorRoute 路由守卫：拦截过早的 FINISH（SOP 完备性检查）。
// 编排模式（graphAdjuster 有值）不物理拦截，由用户自行控制。
func (p *ProtocolBase) ShouldSupervisorRoute(_ flow.Context, trace *Trace, decision string) bool {
	if strings.Contains(decision, p.config.FinishMarker()) {
		if p.config.GraphAdjuster() != nil {
			return true
		}
		if !p.isLogicFinished(trace) {
			return false // 强制 Supervisor 继续指派
		}
	}
	return true
}

// OnSupervisorRouting 默认无操作。
func (p *ProtocolBase) OnSupervisorRouting(flow.Context, *Trace, string) {}

// ---- 阶段 4 默认实现 ----

// OnTeamFinished 默认无操作。
func (p *ProtocolBase) OnTeamFinished(flow.Context, *Trace) {}

// messageTextOf 消息正文文本抽取（string / AssistantMessage / Contents /
// ToolMessage；其余形态空串）。
func messageTextOf(m *ai.Message) string {
	if m == nil {
		return ""
	}
	switch c := m.Content.(type) {
	case string:
		return c
	case *ai.AssistantMessage:
		return c.Text
	case *ai.Contents:
		return c.Text()
	case *ai.ToolMessage:
		return c.ResultText()
	}
	return ""
}

// itoa strconv 简写（包内）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
