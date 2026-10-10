// contractnet.go 合同网招投标协议（对标 ContractNetProtocol +
// ContractNetBiddingTask）：Supervisor 经 __call_for_bids__ 招标 → bidding 节点
// 算法自动投标（capability 关键词加权）→ 看板定标（只能指派已投标专家——
// resolve + onSupervisorRouting 双层守卫）→ 中标者按标书执行。招标轮上限 2。
package team

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/flow"
)

// ContractNet 常量。
const (
	keyContractState = "contract_state_obj"
	IDBidding        = "bidding"
	// MaxBiddingRounds 招标轮上限。
	MaxBiddingRounds = 2
)

// Bid 单份标书。
type Bid struct {
	Score   int    `json:"score"`
	Plan    string `json:"plan"`
	AutoBid bool   `json:"auto_bid"`
}

// ContractState 合同网状态机。
type ContractState struct {
	bids         map[string]*Bid
	bidOrder     []string
	history      []map[string]*Bid
	historyOrder [][]string
	awardedAgent string
	requirement  string
	rounds       int
}

// newContractState 构造。
func newContractState() *ContractState {
	return &ContractState{bids: map[string]*Bid{}}
}

// AddBid 收标（大小写不敏感查重）。
func (s *ContractState) AddBid(name string, bid *Bid) {
	key := s.findBidKey(name)
	if key == "" {
		key = name
		s.bidOrder = append(s.bidOrder, key)
	}
	s.bids[key] = bid
}

func (s *ContractState) findBidKey(name string) string {
	for _, k := range s.bidOrder {
		if strings.EqualFold(k, name) {
			return k
		}
	}
	return ""
}

// HasBids 有标。
func (s *ContractState) HasBids() bool { return len(s.bids) > 0 }

// HasAgentBid 指定专家是否已投标。
func (s *ContractState) HasAgentBid(name string) bool { return s.findBidKey(name) != "" }

// GetBid 指定专家标书。
func (s *ContractState) GetBid(name string) *Bid {
	if key := s.findBidKey(name); key != "" {
		return s.bids[key]
	}
	return nil
}

// SetAwardedAgent 定标。
func (s *ContractState) SetAwardedAgent(name string) { s.awardedAgent = name }

// AwardedAgent 中标者。
func (s *ContractState) AwardedAgent() string { return s.awardedAgent }

// Rounds 当前招标轮。
func (s *ContractState) Rounds() int { return s.rounds }

// archiveCurrentRounds 归档当前轮。
func (s *ContractState) archiveCurrentRounds() {
	if len(s.bids) == 0 {
		return
	}
	s.history = append(s.history, s.bids)
	s.historyOrder = append(s.historyOrder, s.bidOrder)
}

// startNewBidding 开新一轮（归档 → 清标 → 覆写 requirement → rounds++）。
func (s *ContractState) startNewBidding(requirement string) {
	s.archiveCurrentRounds()
	s.bids = map[string]*Bid{}
	s.bidOrder = nil
	s.awardedAgent = ""
	if requirement != "" {
		s.requirement = requirement
	}
	s.rounds++
}

// tryStartNewBidding 轮次守卫（超限 false）。
func (s *ContractState) tryStartNewBidding(requirement string) bool {
	if s.rounds >= MaxBiddingRounds {
		return false
	}
	s.startNewBidding(requirement)
	return true
}

// TryStartNewBidding 轮次守卫的导出面（测试/诊断）。
func (s *ContractState) TryStartNewBidding(requirement string) bool {
	return s.tryStartNewBidding(requirement)
}

// HistoryCount 归档轮数。
func (s *ContractState) HistoryCount() int { return len(s.history) }

// String 看板序列化（对标 ContractState.toString）。
func (s *ContractState) String() string {
	phase := "WAITING_FOR_BIDS"
	if s.awardedAgent != "" {
		phase = "AWARDED"
	} else if s.HasBids() {
		phase = "EVALUATING"
	}
	out := map[string]any{
		"phase":         phase,
		"current_round": s.rounds,
		"history_count": len(s.history),
		"winner":        orDash(s.awardedAgent),
	}
	if s.requirement != "" {
		out["requirement"] = s.requirement
	}
	board := map[string]any{}
	for _, k := range s.bidOrder {
		b := s.bids[k]
		source := "Expert_Proposal"
		if b.AutoBid {
			source = "Capability_Match"
		}
		board[k] = map[string]any{"score": b.Score, "plan": b.Plan, "source": source}
	}
	out["bids_board"] = board
	var hist []map[string]any
	for i, round := range s.history {
		scores := map[string]int{}
		for k, b := range round {
			scores[k] = b.Score
		}
		hist = append(hist, map[string]any{"round": i + 1, "scores": scores})
	}
	if hist != nil {
		out["history_summary"] = hist
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// ContractNetProtocol 合同网协议。
type ContractNetProtocol struct {
	ProtocolBase
}

// NewContractNetProtocol 构造。
func NewContractNetProtocol(config *Config) *ContractNetProtocol {
	p := &ContractNetProtocol{ProtocolBase: NewProtocolBase(config)}
	p.setProtocolName("CONTRACT_NET")
	return p
}

// Name 协议名。
func (p *ContractNetProtocol) Name() string { return "CONTRACT_NET" }

// State 协作状态访问器（集成方看板/测试消费）。
func (p *ContractNetProtocol) State(trace *Trace) *ContractState { return p.getState(trace) }

// getState 状态取或建。
func (p *ContractNetProtocol) getState(trace *Trace) *ContractState {
	if trace == nil {
		return newContractState()
	}
	v, _ := trace.ProtocolContext().LoadOrStore(keyContractState, newContractState())
	s, _ := v.(*ContractState)
	return s
}

// BuildGraph 拓扑：Start → supervisor(exclusive)（bidding 精确边在 linkAgents
// 之前）→ {bidding|成员|End}；bidding(activity) → supervisor；成员 → supervisor。
func (p *ContractNetProtocol) BuildGraph(spec *flow.GraphSpec) {
	spec.AddStart(agent.IDStart).LinkAdd(IDSupervisor)

	spec.AddExclusiveNamed(NewSupervisorTask(p.config)).Then(func(ns *flow.NodeSpec) {
		ns.LinkAddConfig(IDBidding, func(l *flow.LinkSpec) {
			l.Title("route = bidding")
			l.WhenCond(flow.ConditionFunc(func(ctx flow.Context) (bool, error) {
				trace, _ := ctx.Get(p.config.TraceKey()).(*Trace)
				return trace != nil && trace.Route() == IDBidding, nil
			}))
		})
		p.linkAgents(ns)
	}).LinkAdd(agent.IDEnd)

	spec.AddActivityNamed(NewContractNetBiddingTask(p.config, p)).LinkAdd(IDSupervisor)

	for _, a := range p.config.Agents() {
		spec.AddActivityNamed(a).LinkAdd(IDSupervisor)
	}

	spec.AddEnd(agent.IDEnd)
}

// InjectSupervisorTools 注入 __call_for_bids__（非 returnDirect——招标后模型
// 继续决策）。
func (p *ContractNetProtocol) InjectSupervisorTools(ctx flow.Context, add func(ai.FunctionTool)) {
	trace := Current(ctx)
	if trace == nil {
		return
	}
	state := p.getState(trace)

	add(ai.NewTool("__call_for_bids__").
		Desc("发起招标：向全部专家征集标书（每轮招标后进入自动投标与看板定标）。").
		StringParam("requirement", "任务需求描述", true).
		Handler(func(_ context.Context, args map[string]any) (any, error) {
			requirement := strings.TrimSpace(stringValue(args["requirement"]))
			if requirement == "" {
				return nil, nil
			}
			if !state.tryStartNewBidding(requirement) {
				return "招标次数已耗尽。请根据现有标书定标，或直接回复用户无法完成。", nil
			}
			trace.SetRoute(IDBidding)
			return "已进入招标流程。", nil
		}).
		Build())
}

// InjectAgentTools 注入 __submit_proposal__（仅招标轮；returnDirect + MetaAgent）。
func (p *ContractNetProtocol) InjectAgentTools(ctx flow.Context, ag agent.Agent, add func(ai.FunctionTool)) {
	trace := Current(ctx)
	if trace == nil || trace.Route() != IDBidding {
		return
	}
	state := p.getState(trace)
	self := ag.Name()

	add(ai.NewTool("__submit_proposal__").
		Desc("提交标书参与竞标（score 1-100，plan 为执行计划）").
		IntParam("score", "自评胜任分（1-100）", true).
		StringParam("plan", "执行计划", true).
		ReturnDirect(true).
		MetaSet(agent.MetaAgent, self).
		Handler(func(_ context.Context, args map[string]any) (any, error) {
			plan := strings.TrimSpace(stringValue(args["plan"]))
			if plan == "" {
				return "投标失败：plan 不能为空。", nil
			}
			state.AddBid(self, &Bid{Score: normalizeScore(args["score"]), Plan: plan})
			return "标书已收录。", nil
		}).
		Build())
}

// InjectAgentInstruction 基类 + CNP 规范。
func (p *ContractNetProtocol) InjectAgentInstruction(ctx flow.Context, ag agent.Agent, sb *strings.Builder) {
	p.ProtocolBase.InjectAgentInstruction(ctx, ag, sb)
	sb.WriteString("\n## CNP 协作规范\n")
	sb.WriteString("- 招标阶段收到任务需求时，请调用 __submit_proposal__ 提交标书\n")
}

// InjectSupervisorInstruction 基类 + CNP 协作规范。
func (p *ContractNetProtocol) InjectSupervisorInstruction(sb *strings.Builder) {
	p.ProtocolBase.InjectSupervisorInstruction(sb)
	sb.WriteString("\n### CNP 协作规范\n")
	sb.WriteString("1. 需要专家执行时，先调用 __call_for_bids__ 获取标书\n")
	sb.WriteString("2. 只能指派 bids_board 中已投标的专家，按 score/plan 择优录取\n")
	sb.WriteString("3. 定标前检查专家能力模态（多模态任务交给有对应能力的专家）\n")
	sb.WriteString("4. 禁止旁路 bids_board 之外专家\n")
}

// PrepareAgentPrompt 中标执行指令注入（copy-before-mutate）。
func (p *ContractNetProtocol) PrepareAgentPrompt(trace *Trace, ag agent.Agent, originalPrompt *ai.Prompt) *ai.Prompt {
	finalPrompt := p.ProtocolBase.PrepareAgentPrompt(trace, ag, originalPrompt)
	state := p.getState(trace)
	if state.AwardedAgent() != "" && strings.EqualFold(state.AwardedAgent(), ag.Name()) {
		if bid := state.GetBid(ag.Name()); bid != nil {
			return finalPrompt.AddMessage(ai.OfUser(
				"\n【中标执行指令】\n> 你已在竞标中胜出。请严格按你提交的计划执行：\n" + bid.Plan))
		}
	}
	return finalPrompt
}

// PrepareSupervisorInstruction 看板 + 警告区 + 成员资质表。
func (p *ContractNetProtocol) PrepareSupervisorInstruction(_ flow.Context, trace *Trace, sb *strings.Builder) {
	state := p.getState(trace)

	// 警告区
	if state.Rounds() > 0 && !state.HasBids() {
		if state.Rounds() >= MaxBiddingRounds {
			sb.WriteString("\n[最终警告] 招标次数已耗尽且无人投标——任务可能超出了当前团队的能力范围。\n")
		} else {
			sb.WriteString(fmt.Sprintf("\n警告：前一轮招标无人响应。剩余尝试次数: %d\n", MaxBiddingRounds-state.Rounds()))
		}
	} else if state.Rounds() >= MaxBiddingRounds && state.HasBids() {
		sb.WriteString("\n[提示] 招标次数已用尽，请基于当前看板完成定标\n")
	}

	sb.WriteString("\n### 合同网竞标看板\n")
	sb.WriteString("```json\n" + state.String() + "\n```\n")
	sb.WriteString(fmt.Sprintf("- 剩余招标次数: %d\n", max0(MaxBiddingRounds-state.Rounds())))
	if !state.HasBids() {
		sb.WriteString("\n> **强制规范**：必须先调用 __call_for_bids__ 获取专家标书，才能指派专家。\n")
		sb.WriteString("> **操作建议**：请调用 __call_for_bids__ 发起招标。\n")
	}
	sb.WriteString("\n#### 成员资质表:\n")
	for _, a := range p.config.Agents() {
		if a.Profile() != nil {
			sb.WriteString("- " + a.Profile().FormatString("zh") + "\n")
		} else {
			sb.WriteString("- " + a.Name() + "\n")
		}
	}
}

// ResolveSupervisorRoute 四段守卫（bidding 保持 → 显式招标决策 → 成员命中须
// 有标 → 空）。
func (p *ContractNetProtocol) ResolveSupervisorRoute(_ flow.Context, trace *Trace, decision string) string {
	state := p.getState(trace)

	// 1. 招标路由保持
	if trace.Route() == IDBidding {
		return IDBidding
	}

	// 2. 显式招标决策
	if isExplicitBiddingDecision(decision) {
		if state.tryStartNewBidding("") {
			return IDBidding
		}
		return ""
	}

	// 3. 成员命中
	if name := p.findAgentInDecision(decision); name != "" {
		canonical := p.config.NormalizeAgentName(name)
		if !state.HasAgentBid(canonical) {
			trace.AddRecord(ai.RoleSystem, IDBidding,
				"【系统指引】指派无效。必须先调用 __call_for_bids__ 获取标书，且只能指派已出现在 bids_board 中的专家。", 0)
			if state.tryStartNewBidding("") {
				return IDBidding
			}
			return ""
		}
		state.SetAwardedAgent(canonical)
		return canonical
	}
	return ""
}

// ShouldSupervisorRoute 基类 SOP + 定标未执行拦截。
func (p *ContractNetProtocol) ShouldSupervisorRoute(ctx flow.Context, trace *Trace, decision string) bool {
	if strings.Contains(decision, p.config.FinishMarker()) && p.config.GraphAdjuster() == nil {
		if !p.isLogicFinished(trace) {
			return false
		}
	}
	return true
}

// OnSupervisorRouting 最终路由守卫（堵 matchAgentRoute 旁路：无标专家不可
// 被直接路由——开新标或终止）。
func (p *ContractNetProtocol) OnSupervisorRouting(ctx flow.Context, trace *Trace, nextAgent string) {
	if nextAgent == "" || nextAgent == IDBidding || nextAgent == agent.IDEnd ||
		nextAgent == agent.IDStart || nextAgent == IDSupervisor || nextAgent == "system" {
		return
	}
	canonical := p.config.NormalizeAgentName(nextAgent)
	state := p.getState(trace)
	if canonical == "" {
		trace.SetRoute(IDSupervisor)
		return
	}
	if !state.HasAgentBid(canonical) {
		if state.tryStartNewBidding("") {
			trace.SetRoute(IDBidding)
		} else {
			trace.SetFinalAnswer("无法定标：目标专家无有效标书且招标次数已耗尽。")
			trace.AddRecord(ai.RoleSystem, IDBidding, "无法定标：目标专家无有效标书且招标次数已耗尽。", 0)
			trace.SetRoute(agent.IDEnd)
		}
		return
	}
	state.SetAwardedAgent(canonical)
	if canonical != nextAgent {
		trace.SetRoute(canonical)
	}
}

// IsLogicFinished 定标未执行不放行（中标者必须已有产出记录）——导出面。
func (p *ContractNetProtocol) IsLogicFinished(trace *Trace) bool {
	return p.isLogicFinished(trace)
}

// isLogicFinished 定标未执行不放行（中标者必须已有产出记录）。
func (p *ContractNetProtocol) isLogicFinished(trace *Trace) bool {
	if trace == nil || trace.RecordCount() == 0 {
		return false
	}
	state := p.getState(trace)
	winner := state.AwardedAgent()
	if winner == "" {
		return p.ProtocolBase.isLogicFinished(trace)
	}
	for _, r := range trace.Records() {
		if r.IsAgent() && strings.EqualFold(r.Source(), winner) {
			return true
		}
	}
	return false
}

// ---- bidding 任务 ----

// ContractNetBiddingTask 自动投标节点（非 LLM：capability 关键词加权打分）。
type ContractNetBiddingTask struct {
	config   *Config
	protocol *ContractNetProtocol
}

// NewContractNetBiddingTask 构造。
func NewContractNetBiddingTask(config *Config, protocol *ContractNetProtocol) *ContractNetBiddingTask {
	return &ContractNetBiddingTask{config: config, protocol: protocol}
}

// Name 节点名。
func (t *ContractNetBiddingTask) Name() string { return IDBidding }

// Title 同名。
func (t *ContractNetBiddingTask) Title() string { return IDBidding }

// Run 自动投标：跳过已投（手动优先）→ constructBid → 归还定标权。
func (t *ContractNetBiddingTask) Run(ctx flow.Context, _ *flow.Node) error {
	trace, _ := ctx.Get(t.config.TraceKey()).(*Trace)
	if trace == nil {
		return nil
	}
	state := t.protocol.getState(trace)
	if state.Rounds() == 0 {
		state.startNewBidding("")
	}

	manual, auto := 0, 0
	for _, a := range t.config.Agents() {
		if strings.EqualFold(a.Name(), IDSupervisor) {
			continue
		}
		if state.HasAgentBid(a.Name()) {
			manual++
			continue
		}
		bid := constructBid(a, trace.OriginalPrompt(), state.requirement)
		state.AddBid(a.Name(), bid)
		auto++
	}
	trace.AddRecord(ai.RoleSystem, IDBidding,
		fmt.Sprintf("Bidding finished. %d bids collected (%d manual, %d auto).", manual+auto, manual, auto), 0)
	trace.SetRoute(IDSupervisor)
	return nil
}

// constructBid 打分算法（role 词边界 +10、capabilities 命中保底 60/加成 15、
// 上限 95；plan 记录命中来源）。
func constructBid(ag agent.Agent, originalPrompt *ai.Prompt, requirement string) *Bid {
	taskDesc := strings.ToLower(strings.TrimSpace(requirement))
	if taskDesc == "" && originalPrompt != nil {
		if msgs := originalPrompt.Messages(); len(msgs) > 0 {
			taskDesc = strings.ToLower(messageTextOf(msgs[len(msgs)-1]))
		}
	}

	score := 30
	var roleHits, capHits []string

	if ag.Profile() != nil {
		for _, token := range splitRoleTokens(ag.Role()) {
			if len([]rune(token)) < 2 {
				continue
			}
			if matchesKeyword(taskDesc, token) {
				score += 10
				roleHits = append(roleHits, token)
				break
			}
		}
		if caps := ag.Profile().Capabilities(); len(caps) > 0 {
			if score < 60 {
				score = 60
			}
			for _, cap := range caps {
				cap = strings.TrimSpace(cap)
				if cap == "" || strings.EqualFold(cap, "assistant") {
					continue
				}
				if matchesKeyword(taskDesc, cap) {
					score += 15
					capHits = append(capHits, cap)
				}
			}
		}
	}

	plan := fmt.Sprintf("Auto-matched by capabilities: %v", capHits)
	if len(roleHits) > 0 || len(capHits) > 0 {
		plan = fmt.Sprintf("Auto-matched keywords: %v; capabilities: %v", append(roleHits, capHits...), capHits)
	}
	if score > 95 {
		score = 95
	}
	return &Bid{Score: score, Plan: plan, AutoBid: true}
}

// splitRoleTokens 角色描述分词（[\s,，、/|;:]+）。
func splitRoleTokens(role string) []string {
	if role == "" {
		return nil
	}
	f := func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', ',', '，', '、', '/', '|', ';', ':':
			return true
		}
		return false
	}
	return strings.FieldsFunc(role, f)
}

var wordCharRe = regexp.MustCompile(`^[\p{L}\p{N}_]+$`)

// matchesKeyword 关键词匹配（词元形态走词边界，否则 contains）。
func matchesKeyword(text, keyword string) bool {
	if text == "" || keyword == "" {
		return false
	}
	if wordCharRe.MatchString(keyword) {
		return containsWordBounded(text, keyword)
	}
	return strings.Contains(text, keyword)
}

// containsWordBounded 词边界包含（RE2 无 lookaround 的手工实现——supervisor.go
// 同款语义）。
func containsWordBounded(text, word string) bool {
	lt := strings.ToLower(text)
	lw := strings.ToLower(word)
	for i := 0; i+len(lw) <= len(lt); i++ {
		if lt[i:i+len(lw)] != lw {
			continue
		}
		before := true
		if i > 0 {
			before = !isWordByte(lt[i-1])
		}
		after := true
		if i+len(lw) < len(lt) {
			after = !isWordByte(lt[i+len(lw)])
		}
		if before && after {
			return true
		}
	}
	return false
}

// isExplicitBiddingDecision 显式招标话术识别。
func isExplicitBiddingDecision(decision string) bool {
	upper := strings.ToUpper(decision)
	return strings.Contains(upper, "BIDDING") || strings.Contains(decision, "招标") ||
		strings.Contains(upper, "CALL_FOR_BIDS") || strings.Contains(upper, "__CALL_FOR_BIDS__")
}

// findAgentInDecision 决策文本中的成员命中（整段恰为成员名 or 词边界最后出现）。
func (p *ContractNetProtocol) findAgentInDecision(decision string) string {
	clean := strings.Trim(strings.TrimSpace(decision), "*`")
	for _, name := range p.config.AgentNames() {
		if strings.EqualFold(strings.TrimSpace(clean), name) {
			return name
		}
	}
	last, lastIdx := "", -1
	for _, name := range p.config.AgentNames() {
		if idx := indexWordBounded(strings.ToLower(clean), strings.ToLower(name)); idx >= lastIdx && idx >= 0 {
			lastIdx = idx
			last = name
		}
	}
	return last
}

func indexWordBounded(text, word string) int {
	lt := strings.ToLower(text)
	lw := strings.ToLower(word)
	for i := 0; i+len(lw) <= len(lt); i++ {
		if lt[i:i+len(lw)] != lw {
			continue
		}
		before := true
		if i > 0 {
			before = !isWordByte(lt[i-1])
		}
		after := true
		if i+len(lw) < len(lt) {
			after = !isWordByte(lt[i+len(lw)])
		}
		if before && after {
			return i
		}
	}
	return -1
}

// NormalizeBidScore 分数归一的导出面（数字/字符串解析、clamp 1..100、缺省 50）。
func NormalizeBidScore(v any) int { return normalizeScore(v) }

// MatchesKeyword 关键词匹配的导出面（词元形态词边界、否则 contains）。
func MatchesKeyword(text, keyword string) bool { return matchesKeyword(text, keyword) }

// normalizeScore 分数归一（数字/字符串解析、clamp 1..100、缺省 50）。
func normalizeScore(v any) int {
	switch n := v.(type) {
	case float64:
		return clampScore(int(n))
	case int:
		return clampScore(n)
	case string:
		var s int
		if _, err := fmt.Sscanf(n, "%d", &s); err == nil {
			return clampScore(s)
		}
	}
	return 50
}

func clampScore(n int) int {
	if n < 1 {
		return 1
	}
	if n > 100 {
		return 100
	}
	return n
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

// sortedBidKeys 调试辅助（保序键）。
func (s *ContractState) sortedBidKeys() []string {
	keys := append([]string(nil), s.bidOrder...)
	sort.Strings(keys)
	return keys
}
