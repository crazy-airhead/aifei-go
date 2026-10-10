// a2a.go A2A 点对点接力协议（对标 A2AProtocol + A2AHandoverTask）：Start 直连
// 首位专家；专家经 __transfer_to__ 工具点对点指定下家，handover 快车道仅做
// 校验/熔断/模态检查；5 次流转上限；Supervisor 只处理工具外溢的收尾。
package team

import (
	"context"
	"strings"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/flow"
)

// A2A 协议上下文键与常量。
const (
	keyA2AState = "a2a_state_obj"
	IDHandover  = "handover"
	// A2AMaxTransferRounds 最大流转次数（熔断）。
	A2AMaxTransferRounds = 5
)

// A2AState A2A 协作状态机（对标同名类）。
type A2AState struct {
	globalState    string
	transferCount  int
	lastTempTarget string
	lastPayload    string
}

// SetTransferRequest 发起移交请求（锁 payload；state 非空才覆盖 globalState）。
func (s *A2AState) SetTransferRequest(target, state, payload string) {
	s.lastTempTarget = target
	s.lastPayload = payload
	if state != "" {
		s.globalState = state
	}
}

// ConfirmTransfer 确认移交（count++、清 pending）。
func (s *A2AState) ConfirmTransfer() {
	s.transferCount++
	s.lastTempTarget = ""
}

// ClearPendingTransfer 清除待定移交（不耗额度）。
func (s *A2AState) ClearPendingTransfer() { s.lastTempTarget = "" }

// GlobalState 累积业务状态。
func (s *A2AState) GlobalState() string { return s.globalState }

// TransferCount 成功移交次数。
func (s *A2AState) TransferCount() int { return s.transferCount }

// PendingTarget 待定目标（空 = 无移交请求）。
func (s *A2AState) PendingTarget() string { return s.lastTempTarget }

// LastPayload 锁定的交接 memo。
func (s *A2AState) LastPayload() string { return s.lastPayload }

// A2AProtocol A2A 接力协议。
type A2AProtocol struct {
	ProtocolBase
}

// NewA2AProtocol 构造。
func NewA2AProtocol(config *Config) *A2AProtocol {
	p := &A2AProtocol{ProtocolBase: NewProtocolBase(config)}
	p.setProtocolName("A2A")
	return p
}

// Name 协议名。
func (p *A2AProtocol) Name() string { return "A2A" }

// getState 状态取或建。
func (p *A2AProtocol) getState(trace *Trace) *A2AState {
	if trace == nil {
		return &A2AState{}
	}
	v, _ := trace.ProtocolContext().LoadOrStore(keyA2AState, &A2AState{})
	s, _ := v.(*A2AState)
	return s
}

// State 协作状态访问器（集成方看板/测试消费）。
func (p *A2AProtocol) State(trace *Trace) *A2AState { return p.getState(trace) }

// BuildGraph 拓扑：Start → 首位专家 → handover(exclusive) → {成员|End|
// supervisor}；supervisor(exclusive) → {成员|End}。
func (p *A2AProtocol) BuildGraph(spec *flow.GraphSpec) {
	names := p.config.AgentNames()
	spec.AddStart(agent.IDStart).LinkAdd(names[0])

	for _, a := range p.config.Agents() {
		spec.AddActivityNamed(a).LinkAdd(IDHandover)
	}

	spec.AddExclusiveNamed(NewA2AHandoverTask(p.config, p)).Then(func(ns *flow.NodeSpec) {
		p.linkAgents(ns)
		ns.LinkAddConfig(agent.IDEnd, func(l *flow.LinkSpec) {
			l.Title("route = end")
			l.WhenCond(flow.ConditionFunc(func(ctx flow.Context) (bool, error) {
				trace, _ := ctx.Get(p.config.TraceKey()).(*Trace)
				return trace != nil && strings.EqualFold(trace.Route(), agent.IDEnd), nil
			}))
		})
		ns.LinkAdd(IDSupervisor)
	})

	spec.AddExclusiveNamed(NewSupervisorTask(p.config)).Then(func(ns *flow.NodeSpec) {
		p.linkAgents(ns)
	}).LinkAdd(agent.IDEnd)

	spec.AddEnd(agent.IDEnd)
}

// InjectAgentTools 注入 __transfer_to__（returnDirect + MetaAgent；trace 经
// 闭包捕获——swarm 同款模式）。
func (p *A2AProtocol) InjectAgentTools(ctx flow.Context, ag agent.Agent, add func(ai.FunctionTool)) {
	trace := Current(ctx)
	if trace == nil {
		return
	}
	state := p.getState(trace)
	targets := a2aTargetsJSON(p.config, ag)

	tool := ai.NewTool("__transfer_to__").
		Desc("将当前任务移交给指定专家继续处理（点对点接力）。").
		StringParam("target", "目标专家（必须是除你之外的成员）："+targets, true).
		StringParam("state", "持久化业务状态（JSON，可选，会累积）", false).
		StringParam("memo", "交接备忘：必写关键产出与下一步建议", false).
		ReturnDirect(true).
		MetaSet(agent.MetaAgent, ag.Name()).
		Handler(func(_ context.Context, args map[string]any) (any, error) {
			target, _ := args["target"].(string)
			st, _ := args["state"].(string)
			memo, _ := args["memo"].(string)
			state.SetTransferRequest(target, st, memo)
			return "已发起向 [" + target + "] 的接力请求。", nil
		}).
		Build()
	add(tool)
}


// InjectAgentInstruction 基类身份 + 接力断面（协作轨迹/上一步/待处理产出/
// 累积状态）。
func (p *A2AProtocol) InjectAgentInstruction(ctx flow.Context, ag agent.Agent, sb *strings.Builder) {
	p.ProtocolBase.InjectAgentInstruction(ctx, ag, sb)
	trace := Current(ctx)
	if trace == nil {
		return
	}

	sb.WriteString("\n\n## 专家协作指引\n")
	sb.WriteString("- 按专业能力与 Modes 精准选择移交对象\n")
	sb.WriteString("- 移交时 memo 必写关键产出与下一步建议\n\n")

	sb.WriteString("## 当前接力任务断面 (Active Session Context)\n")
	var routes []string
	for _, r := range trace.Records() {
		routes = append(routes, r.Source())
	}
	if len(routes) > 0 {
		sb.WriteString("协作轨迹: " + strings.Join(routes, " -> ") + "\n")
	}
	if last := trace.LastAgentName(); last != "" {
		sb.WriteString("上一步专家: " + last + "\n")
	}
	state := p.getState(trace)
	if state.LastPayload() != "" {
		sb.WriteString("待处理产出: " + state.LastPayload() + "\n")
	} else if content := trace.LastAgentContent(); content != "" {
		sb.WriteString("待处理产出: " + truncateRunes(content, 200) + "\n")
	}
	if state.GlobalState() != "" {
		sb.WriteString("累积业务状态: " + state.GlobalState() + "\n")
	}
}

// ResolveSupervisorRoute A2A 路由核心（handover 消费）：熔断 → 校验（存在性
// + 模态）→ 确认；失败回退上一位专家重试。
func (p *A2AProtocol) ResolveSupervisorRoute(_ flow.Context, trace *Trace, _ string) string {
	state := p.getState(trace)
	target := state.PendingTarget()
	if target == "" {
		return p.ProtocolBase.ResolveSupervisorRoute(flow.NewContext(), trace, "")
	}

	// 熔断
	if state.TransferCount() >= A2AMaxTransferRounds {
		trace.SetFinalAnswer("协作达到最大流转次数，任务中止。")
		return ""
	}

	// 目标校验
	canonical := p.config.NormalizeAgentName(target)
	if canonical == "" || !p.checkModality(trace, canonical) {
		state.ClearPendingTransfer()
		trace.AddRecord(ai.RoleSystem, "Supervisor",
			"【系统通知】移交失败：专家 ["+target+"] 不存在或能力不匹配（如无法处理图片）。请重新选择或尝试自行解决。", 0)
		// 回退上一位专家重试
		if last := trace.LastAgentName(); last != "" {
			return last
		}
		return ""
	}

	state.ConfirmTransfer()
	return canonical
}

// checkModality 模态检查：多模态输入只能交给有非文本输入能力的专家。
func (p *A2AProtocol) checkModality(trace *Trace, target string) bool {
	isMultiModal := false
	if prompt := trace.OriginalPrompt(); prompt != nil {
		for _, m := range prompt.Messages() {
			if messageHasMedia(m) {
				isMultiModal = true
				break
			}
		}
	}
	ag := p.config.Agent(target)
	if ag == nil {
		return false
	}
	profile := ag.Profile()
	if profile == nil || len(profile.InputModes()) == 0 {
		return !isMultiModal
	}
	for _, mode := range profile.InputModes() {
		if mode != "text" {
			return true
		}
	}
	return !isMultiModal
}

// PrepareSupervisorInstruction 追加接力看板。
func (p *A2AProtocol) PrepareSupervisorInstruction(_ flow.Context, trace *Trace, sb *strings.Builder) {
	state := p.getState(trace)
	sb.WriteString("\n### A2A 接力看板\n")
	sb.WriteString("- Global State: " + orDash(state.GlobalState()) + "\n")
	sb.WriteString("- Handovers: " + itoa(state.TransferCount()) + "/" + itoa(A2AMaxTransferRounds) + "\n")
}

// InjectSupervisorInstruction 追加 A2A 协作准则。
func (p *A2AProtocol) InjectSupervisorInstruction(sb *strings.Builder) {
	p.ProtocolBase.InjectSupervisorInstruction(sb)
	sb.WriteString("\n### A2A 协作准则\n")
	sb.WriteString("- 成员自主接力，你只处理收尾与异常\n")
	sb.WriteString("- 直接输出完成标记，禁止二次总结\n")
	sb.WriteString("- 流转超过 " + itoa(A2AMaxTransferRounds) + " 次时强制收尾\n")
}

// ---- handover 任务 ----

// A2AHandoverTask 接力快车道（非 LLM：校验 pending 目标 → 协议路由裁决）。
type A2AHandoverTask struct {
	config   *Config
	protocol *A2AProtocol
}

// NewA2AHandoverTask 构造。
func NewA2AHandoverTask(config *Config, protocol *A2AProtocol) *A2AHandoverTask {
	return &A2AHandoverTask{config: config, protocol: protocol}
}

// Name 节点名。
func (t *A2AHandoverTask) Name() string { return IDHandover }

// Title 同名。
func (t *A2AHandoverTask) Title() string { return IDHandover }

// Run 路由决策。
func (t *A2AHandoverTask) Run(ctx flow.Context, _ *flow.Node) error {
	trace, _ := ctx.Get(t.config.TraceKey()).(*Trace)
	if trace == nil {
		return nil
	}
	state := t.protocol.getState(trace)
	target := state.PendingTarget()
	if target == "" {
		// 专家没调工具/上轮失败已清理 → 交 supervisor 收尾
		trace.SetRoute(IDSupervisor)
		return nil
	}
	next := t.protocol.ResolveSupervisorRoute(ctx, trace, "")
	if next != "" {
		trace.SetRoute(next)
		return nil
	}
	trace.SetRoute(agent.IDEnd)
	return nil
}

// ---- 小件 ----

// a2aTargetsJSON 除自己外的成员 metadata JSON 数组（工具参数描述内嵌）。
func a2aTargetsJSON(config *Config, self agent.Agent) string {
	var sb strings.Builder
	sb.WriteString("[")
	first := true
	for _, name := range config.AgentNames() {
		if self != nil && strings.EqualFold(name, self.Name()) {
			continue
		}
		if !first {
			sb.WriteString(",")
		}
		first = false
		sb.WriteString(`{"name":"` + name + `"`)
		if ag := config.Agent(name); ag != nil && ag.Profile() != nil {
			sb.WriteString(`,"capabilities":"` + strings.Join(ag.Profile().Capabilities(), ",") + `"`)
		}
		sb.WriteString("}")
	}
	sb.WriteString("]")
	return sb.String()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

// messageHasMedia 消息是否携带非文本媒体块（Contents / AssistantMessage.Blocks）。
func messageHasMedia(m *ai.Message) bool {
	if m == nil {
		return false
	}
	switch c := m.Content.(type) {
	case *ai.Contents:
		return len(c.Blocks()) > 0
	case *ai.AssistantMessage:
		return len(c.Blocks) > 0
	}
	return false
}
