// hierarchical.go 层级化协作协议（对标 team.protocol.HierarchicalProtocol）：
// 「运行看板」机制——成员产出结构化提取进看板，Supervisor 依据看板进度、成员
// 负载与错误记录全局调度；看板有错时物理阻断 FINISH。
package team

import (
	"encoding/json"
	"regexp"
	"strings"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/flow"
)

// 协议上下文键。
const (
	keyHierarchyState     = "hierarchy_state_obj"
	keyAgentUsage         = "agent_usage_map"
	keyActiveInstruction  = "active_instruction" // 主管当前轮派发指令，供专家透传
)

// overworkThreshold 同一专家被重复指派的过载告警阈值。
const overworkThreshold = 3

// maxMemoLen 看板报告区截断长度。
const maxMemoLen = 250

// statusFailed FAILED 语义（词边界，大小写不敏感）。
const statusFailed = "FAILED"

var statusFailedRe = regexp.MustCompile(`\b` + statusFailed + `\b`)

var (
	fenceTailRe = regexp.MustCompile("```\\w*\\s*\\n?$")
	fenceHeadRe = regexp.MustCompile("^\\s*```")
)

// HierarchicalState 协作状态机：汇报数据吸收、错误记录与状态持久化。
// errors 保序（Java ONode 插入序）供 firstErrorAgent 确定性回派。
type HierarchicalState struct {
	data map[string]any
}

// Data 内存看板直接访问。
func (s *HierarchicalState) Data() map[string]any { return s.data }

func (s *HierarchicalState) getOrNew(key string) map[string]any {
	if m, ok := s.data[key].(map[string]any); ok {
		return m
	}
	m := map[string]any{}
	s.data[key] = m
	return m
}

// reports 报告区。
func (s *HierarchicalState) reports() map[string]string {
	m := s.getOrNew("_reports")
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = stringValue(v)
	}
	return out
}

// HasReports 是否已有业务汇报（_reports 或 _results），与 meta 能力信息无关。
func (s *HierarchicalState) HasReports() bool {
	if m, ok := s.data["_reports"].(map[string]any); ok && len(m) > 0 {
		return true
	}
	if m, ok := s.data["_results"].(map[string]any); ok && len(m) > 0 {
		return true
	}
	return false
}

// metaErrs 错误表（map + 保序键，确定性迭代）。
type metaErrs struct {
	m    map[string]string
	keys []string
}

func (e *metaErrs) set(name, msg string) {
	if _, exists := e.m[name]; !exists {
		e.keys = append(e.keys, name)
	}
	e.m[name] = msg
}

func (e *metaErrs) remove(name string) {
	if _, exists := e.m[name]; !exists {
		return
	}
	delete(e.m, name)
	for i, k := range e.keys {
		if k == name {
			e.keys = append(e.keys[:i], e.keys[i+1:]...)
			break
		}
	}
}

func (s *HierarchicalState) errors() *metaErrs {
	meta := s.getOrNew("_meta")
	if e, ok := meta["errors"].(*metaErrs); ok {
		return e
	}
	e := &metaErrs{m: map[string]string{}}
	meta["errors"] = e
	return e
}

// HasErrors 看板是否有未解决错误。
func (s *HierarchicalState) HasErrors() bool { return len(s.errors().m) > 0 }

// MarkError 登记专家执行错误。
func (s *HierarchicalState) MarkError(agentName, err string) {
	s.errors().set(agentName, err)
}

// ClearError 清除专家错误。
func (s *HierarchicalState) ClearError(agentName string) {
	s.errors().remove(agentName)
}

// Absorb 吸收成员产出：JSON 结构化合并入 _results，裁剪正文入 _reports
//（sniffJson 起止精确裁剪，保留前后正文；纯 JSON 回复给兜底摘要）。
func (s *HierarchicalState) Absorb(agentName, content string, report map[string]any) {
	if content == "" {
		return
	}

	// 1. 提取结构化数据存入 _results（用于逻辑计算）
	if len(report) > 0 {
		s.getOrNew("_results")[agentName] = report
	}

	// 2. 按 JSON 起止裁剪，保留前后正文
	cleanContent := s.absorbCleanContent(content, report)
	if len(cleanContent) > maxMemoLen {
		cleanContent = cleanContent[:maxMemoLen] + "..."
	}
	s.getOrNew("_reports")[agentName] = cleanContent
}

func (s *HierarchicalState) absorbCleanContent(content string, report map[string]any) string {
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start == -1 || end <= start {
		return content
	}
	before := strings.TrimSpace(fenceTailRe.ReplaceAllString(content[:start], ""))
	after := strings.TrimSpace(fenceHeadRe.ReplaceAllString(content[end+1:], ""))
	result := strings.TrimSpace(before + joinSpace(before, after) + after)
	// 纯 JSON 回复时报告区给兜底摘要，避免看板空白
	if result == "" {
		if len(report) > 0 {
			if data, err := json.Marshal(report); err == nil {
				return string(data)
			}
		}
		return content
	}
	return result
}

func joinSpace(before, after string) string {
	if before == "" || after == "" {
		return ""
	}
	return " "
}

// String 看板 JSON。
func (s *HierarchicalState) String() string {
	data, err := json.Marshal(s.view())
	if err != nil {
		return "{}"
	}
	return string(data)
}

// view 增量构建渲染用看板（业务 keys + meta），避免全量序列化深拷贝。
func (s *HierarchicalState) view() map[string]any {
	view := map[string]any{}
	if m, ok := s.data["_reports"].(map[string]any); ok && len(m) > 0 {
		view["_reports"] = m
	}
	if m, ok := s.data["_results"].(map[string]any); ok && len(m) > 0 {
		view["_results"] = m
	}
	if e := s.errors(); len(e.m) > 0 {
		meta := map[string]any{}
		meta["errors"] = e.m
		view["_meta"] = meta
	}
	return view
}

// HierarchicalProtocol 层级化协作协议。
type HierarchicalProtocol struct {
	ProtocolBase
}

// NewHierarchicalProtocol 工厂。
func NewHierarchicalProtocol(config *Config) *HierarchicalProtocol {
	p := &HierarchicalProtocol{ProtocolBase: NewProtocolBase(config)}
	p.setProtocolName("HIERARCHICAL")
	return p
}

// Name 协议标识。
func (p *HierarchicalProtocol) Name() string { return "HIERARCHICAL" }

// getState 状态机 computeIfAbsent。
func (p *HierarchicalProtocol) getState(trace *Trace) *HierarchicalState {
	if v, ok := trace.ProtocolContext().Load(keyHierarchyState); ok {
		if s, ok := v.(*HierarchicalState); ok {
			return s
		}
	}
	s := &HierarchicalState{data: map[string]any{}}
	actual, loaded := trace.ProtocolContext().LoadOrStore(keyHierarchyState, s)
	if loaded {
		if s2, ok := actual.(*HierarchicalState); ok {
			return s2
		}
	}
	return s
}

// getUsage 负载统计 computeIfAbsent。
func (p *HierarchicalProtocol) getUsage(trace *Trace) map[string]int {
	if v, ok := trace.ProtocolContext().Load(keyAgentUsage); ok {
		if m, ok := v.(map[string]int); ok {
			return m
		}
	}
	m := map[string]int{}
	actual, loaded := trace.ProtocolContext().LoadOrStore(keyAgentUsage, m)
	if loaded {
		if m2, ok := actual.(map[string]int); ok {
			return m2
		}
	}
	return m
}

// BuildGraph 固定拓扑：Start → Supervisor ↔ Agents → End。Supervisor 无自环边；
// 错误阻断等场景应返回空，交由 commitRoute 继续解析/下一轮决策。
func (p *HierarchicalProtocol) BuildGraph(spec *flow.GraphSpec) {
	spec.AddStart(agent.IDStart).LinkAdd(IDSupervisor)

	spec.AddExclusiveNamed(NewSupervisorTask(p.config)).Then(func(ns *flow.NodeSpec) {
		p.linkAgents(ns)
	}).LinkAdd(agent.IDEnd)

	for _, a := range p.config.Agents() {
		spec.AddActivityNamed(a).LinkAdd(IDSupervisor)
	}

	spec.AddEnd(agent.IDEnd)
}

// InjectAgentInstruction 基类身份 profile + 协作历史，追加汇报规范与嵌套团队
// 完成标记提醒。
func (p *HierarchicalProtocol) InjectAgentInstruction(ctx flow.Context, ag agent.Agent, sb *strings.Builder) {
	p.ProtocolBase.InjectAgentInstruction(ctx, ag, sb)

	sb.WriteString("\n\n---") // 视觉分割线，帮助模型切分注意力
	sb.WriteString("\n## 协作与汇报规范\n")

	// 1. 强制汇报规范
	sb.WriteString("\n1. 汇报要求：\n- 请在回复结尾使用 JSON 块反馈核心数据（例：{\"result\": \"...\", \"status\": \"done\"}）。")

	// 2. 动态追加完成标记提醒（嵌套团队）
	if nested, ok := ag.(*Agent); ok {
		marker := nested.Config().FinishMarker()
		if marker != "" {
			sb.WriteString("\n2. **完成标记**：任务完成时，必须包含标记: `" + marker + "`\n")
		}
	}
}

// PrepareAgentPrompt 增强专家指令：Pre-Context（上一位同事汇报）、主管指令
// 透传与错误修正。基类已 Copy；此处只在副本上追加（copy-before-mutate）。
func (p *HierarchicalProtocol) PrepareAgentPrompt(trace *Trace, ag agent.Agent, originalPrompt *ai.Prompt) *ai.Prompt {
	finalPrompt := p.ProtocolBase.PrepareAgentPrompt(trace, ag, originalPrompt)

	state, _ := trace.ProtocolContext().Load(keyHierarchyState)
	var hstate *HierarchicalState
	if s, ok := state.(*HierarchicalState); ok {
		hstate = s
	}

	var sb strings.Builder

	// 专家不看全局看板，但能看到「上一个同事干了什么」，解决断层
	lastAgent := trace.LastAgentName()
	if hstate != nil && lastAgent != "" {
		if lastSummary, ok := hstate.reports()[lastAgent]; ok && lastSummary != "" {
			sb.WriteString("\n\n### 前置背景 (Pre-Context)\n")
			sb.WriteString("- 来自 " + lastAgent + ": **" + lastSummary + "**\n")
			sb.WriteString("> 请务必基于上述背景数据进行处理，不要修改原始关键信息。\n")
		}
	}

	// 注入 A：透传主管的决策分析逻辑
	if v, ok := trace.ProtocolContext().Load(keyActiveInstruction); ok {
		if activeInstruction, ok := v.(string); ok && activeInstruction != "" {
			sb.WriteString("\n\n### 来自主管的最新指令 (Supervisor Directive)\n")
			// 引用格式展示主管的分析过程，引导专家理解上下文
			sb.WriteString("> " + strings.ReplaceAll(strings.TrimSpace(activeInstruction), "\n", "\n> ") + "\n")
			sb.WriteString("---\n")
		}
	}

	// 注入 B：针对性的错误看板反馈
	if hstate != nil {
		if e := hstate.errors(); e != nil {
			if errorMsg, ok := e.m[ag.Name()]; ok {
				sb.WriteString("\n### 错误修正请求：\n")
				sb.WriteString("- 你之前的执行结果未通过验证：**" + errorMsg + "**\n")
				sb.WriteString("- 请根据主管的最新指令进行修复，并务必包含正确的完成标记。\n")
			}
		}
	}

	// 多模态提醒（基于副本消息列表判断）
	for _, m := range finalPrompt.Messages() {
		if m != nil && m.Role == ai.RoleUser && isMultiModalContent(m.Content) {
			sb.WriteString("\n- [重要]：输入包含多媒体附件，请务必结合视觉或文件内容进行处理。")
			break
		}
	}

	if sb.Len() > 0 {
		finalPrompt.AddMessage(ai.OfUser(sb.String()))
	}

	return finalPrompt
}

// OnAgentEnd 完成判定（嵌套团队特判）、看板吸收、主管指令清除与负载统计。
func (p *HierarchicalProtocol) OnAgentEnd(trace *Trace, ag agent.Agent) {
	state := p.getState(trace)

	lastContent := trace.LastAgentContent()

	if lastContent == "" {
		state.MarkError(ag.Name(), "Response is empty.")
	} else {
		// 完成判定：
		// - 嵌套团队：子团队 call() 返回的是已收口的 finalAnswer，通常已剥离
		//   [XXX_FINISH]；不应再强制要求正文含 finishMarker
		// - 普通专家：内容不含 FAILED 语义即视为完成
		isSuccess := false
		failReason := ""
		report := p.sniffJson(lastContent)
		textFailed := statusFailedRe.MatchString(lastContent)
		statusFailedHit := false
		if report != nil {
			if st, ok := report["status"].(string); ok {
				statusFailedHit = strings.EqualFold(st, statusFailed)
			}
		}

		if nested, ok := ag.(*Agent); ok {
			var nestedTrace *Trace
			if trace.Session() != nil {
				nestedTrace = nested.GetTrace(trace.Session())
			}
			nestedFinished := nestedTrace != nil &&
				(nestedTrace.Route() == agent.IDEnd ||
					(nestedTrace.HasFinalAnswer() && nestedTrace.FinalAnswer() != ""))
			marker := nested.Config().FinishMarker()
			// 子团队已结束，或交付文本仍带 finishMarker，或仅以非 FAILED 产出为准
			isSuccess = nestedFinished ||
				(marker != "" && strings.Contains(lastContent, marker)) ||
				(!textFailed && !statusFailedHit)
			if !isSuccess {
				failReason = "Nested team task not finished (missing completion signal)."
			}
		} else {
			isSuccess = !textFailed && !statusFailedHit
			if !isSuccess {
				failReason = "Task reported as failed (content or status contains FAILED)."
			}
		}

		if !isSuccess {
			state.MarkError(ag.Name(), failReason)
		} else {
			state.ClearError(ag.Name())
		}

		state.Absorb(ag.Name(), lastContent, report)
	}

	// 专家本轮执行完毕，清除主管指令，避免跨轮污染下一专家
	trace.ProtocolContext().Delete(keyActiveInstruction)

	// 统计负载
	usage := p.getUsage(trace)
	usage[ag.Name()]++
}

// ResolveSupervisorRoute 解析主管路由：
//   - finish + 看板有错：注入系统反馈；优先回派出错专家
//   - finish + 无错：返回空，交 commitRoute 走 ShouldSupervisorRoute SOP 守卫
//   - 普通派发：写入 active_instruction 后委托基类精确匹配
func (p *HierarchicalProtocol) ResolveSupervisorRoute(ctx flow.Context, trace *Trace, decision string) string {
	if strings.Contains(decision, p.config.FinishMarker()) {
		state := p.getState(trace)
		if state.HasErrors() {
			errorHint := "【系统干预】由于看板中仍有未解决的错误，任务不能结束。请指派相关专家修复。"

			// 1. 注入系统反馈（使用 ID_SYSTEM）
			trace.AddRecord(ai.RoleSystem, IDSystem, errorHint, 0)
			// 2. 将干预说明写入 active_instruction，辅助下一轮专家 Prompt
			trace.ProtocolContext().Store(keyActiveInstruction, errorHint)

			// 优先回派第一个仍登记在册的出错专家（commitRoute 在 resolve 非
			// 空时直接 routeTo；若返回空且 decision 不含 agent 名，
			// matchAgentRoute 失败会误入 END）
			if fixTarget := p.firstErrorAgent(state); fixTarget != "" {
				return fixTarget
			}
			return ""
		}

		// 正常 finish：不在此直接 END，交由 commitRoute + shouldSupervisorRoute
		// 统一收口（保留 SOP 完备性检查）
		return ""
	}

	if decision != "" {
		trace.ProtocolContext().Store(keyActiveInstruction, decision)
	}
	return p.ProtocolBase.ResolveSupervisorRoute(ctx, trace, decision)
}

// ShouldSupervisorRoute 终结审计：看板仍有未解决错误时拦截 FINISH。到达
// maxTurns 后放行，避免死循环；编排模式（graphAdjuster）由基类决定。
func (p *HierarchicalProtocol) ShouldSupervisorRoute(ctx flow.Context, trace *Trace, decision string) bool {
	if strings.Contains(decision, p.config.FinishMarker()) {
		state := p.getState(trace)
		if state.HasErrors() && trace.TurnCount() < trace.Options().MaxTurns {
			return false
		}
	}
	return p.ProtocolBase.ShouldSupervisorRoute(ctx, trace, decision)
}

// firstErrorAgent 看板 errors 中取第一个仍在团队中的专家名（保序）。
func (p *HierarchicalProtocol) firstErrorAgent(state *HierarchicalState) string {
	if state == nil || !state.HasErrors() {
		return ""
	}
	e := state.errors()
	for _, name := range e.keys {
		if p.config.HasAgent(name) {
			return name
		}
	}
	return ""
}

// PrepareSupervisorInstruction 实时构建运行看板：成员能力、负载与错误信息。
func (p *HierarchicalProtocol) PrepareSupervisorInstruction(_ flow.Context, trace *Trace, sb *strings.Builder) {
	stateV, _ := trace.ProtocolContext().Load(keyHierarchyState)
	var state *HierarchicalState
	if s, ok := stateV.(*HierarchicalState); ok {
		state = s
	}
	usage := p.getUsage(trace)

	sb.WriteString("\n## 运行看板 (Hierarchical Dashboard)\n")

	// 增量构建渲染用看板（业务 keys + 运行时 meta 注入）
	view := map[string]any{}
	if state != nil {
		if v := state.view(); len(v) > 0 {
			for k, val := range v {
				view[k] = val
			}
		}
	}
	meta := map[string]any{}
	if existing, ok := view["_meta"].(map[string]any); ok {
		meta = existing
	}

	if len(usage) > 0 {
		meta["agent_usage"] = usage
	}

	capabilities := map[string]any{}
	for _, name := range p.config.AgentNames() {
		ag := p.config.Agent(name)
		if ag != nil && ag.Profile() != nil {
			capabilities[name] = ag.Profile().InputModes()
		}
	}
	if len(capabilities) > 0 {
		meta["capabilities"] = capabilities
	}
	if len(meta) > 0 {
		view["_meta"] = meta
	}

	// 以业务汇报是否存在为准（capabilities 总会写入）
	hasReports := state != nil && state.HasReports()
	if !hasReports {
		sb.WriteString("> 暂无汇报，请下达初始指令。\n")
		// 首轮仍展示能力与负载元信息，便于主管选人
		if len(capabilities) > 0 || len(usage) > 0 {
			if data, err := json.Marshal(view); err == nil {
				sb.WriteString("```json\n" + string(data) + "\n```\n")
			}
		}
	} else {
		if data, err := json.Marshal(view); err == nil {
			sb.WriteString("```json\n" + string(data) + "\n```\n")
		}
	}

	// 动态 SOP（与 InjectSupervisorInstruction 静态准则分工：此处强调流程与交付）
	sb.WriteString("\n### 派发指令准则 (Standard Operating Procedure):\n")
	sb.WriteString("> 1. **显式数据传递**：指派专家时，必须复述看板 `_results` 中的核心参数。\n")
	sb.WriteString("\n### 管理与决策指引 (Management & Decision Hints):\n")
	sb.WriteString("> **决策指引**：对比看板进度。若 `_meta.errors` 存在或关键环节未产出，请继续调度，严禁提前结束。\n")
	sb.WriteString("> **流程完整性**：确保设计与执行角色（如 `implementer`）均已参与。严禁跳过关键执行环节直接结束。\n")
	sb.WriteString("> **终结交付**：若认为任务已达成，请输出 `" + p.config.FinishMarker() +
		"` 并在此回复中**汇总最终代码或方案**给用户。\n")
	sb.WriteString("> **注意**：输出标记后系统将立即停止，你将无法再次指派专家，请务必在这一步提供完整的交付内容。\n")
	sb.WriteString("> **负载建议**：参考 `agent_usage` 避免过度指派。若专家多次失败，请尝试更换专家或调整指令。\n")

	var overworked []string
	for _, name := range p.config.AgentNames() {
		if usage[name] >= overworkThreshold {
			overworked = append(overworked, name)
		}
	}
	if len(overworked) > 0 {
		sb.WriteString("\n> **![风险预警]**：专家 [" + strings.Join(overworked, ", ") +
			"] 已被重复指派多次且未达成目标。请停止无效循环，要求专家提供更详尽的错误说明，或尝试切换到其他专家。")
	}
}

// InjectSupervisorInstruction 协议静态准则 + 管理决策准则。
func (p *HierarchicalProtocol) InjectSupervisorInstruction(sb *strings.Builder) {
	p.ProtocolBase.InjectSupervisorInstruction(sb)

	sb.WriteString("\n### 管理决策准则：\n")
	sb.WriteString("1. **模态适配**：优先指派支持对应模式（图片/文件）的专家。\n")
	sb.WriteString("2. **错误恢复**：若 `_meta.errors` 存在，必须指派专家处理，直至错误清除。\n")
	sb.WriteString("3. **负载均衡**：参考 `agent_usage` 避免过度使用单一专家。")
}
