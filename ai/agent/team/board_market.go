// board_market.go 黑板协议（对标 BlackboardProtocol：成员主动工具回写共享
// KV + 自由文本 todo 队列；FINISH 硬门禁 = todo 清空且 status != FAILED）、
// 市场协议（对标 MarketBasedProtocol：执行后按质量/耗时 EMA 更新身价，Price/
// Score/ROI 看板调度）、None 协议（透明容器：不构图，编排交外部 graphAdjuster）。
package team

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/flow"
)

// ==================== Blackboard ====================

// Blackboard 常量。
const (
	keyBlackboardState = "blackboard_state_obj"
	// BlackboardStatusFailed / BlackboardStatusCompleted 看板状态语义。
	BlackboardStatusFailed    = "FAILED"
	BlackboardStatusCompleted = "COMPLETED"
)

// BoardState 黑板状态机（共享 KV + 保序 todo 队列）。
type BoardState struct {
	data        map[string]any
	todos       []string
	lastUpdater string
}

// NewBoardState 构造（导出面——集成方黑板看板消费）。
func NewBoardState() *BoardState { return &BoardState{data: map[string]any{}} }

// Data 共享数据快照。
func (s *BoardState) Data() map[string]any { return s.data }

// Todos 待办快照。
func (s *BoardState) Todos() []string { return append([]string(nil), s.todos...) }

// LastUpdater 最后回写者。
func (s *BoardState) LastUpdater() string { return s.lastUpdater }

// Merge 结构化 state 合并（导出）（completed 声明优先 → todo → status/result → 其余
// 平铺；result 值联动 completeMatchingTodos）。
func (s *BoardState) Merge(agentName string, rawInput map[string]any) {
	s.lastUpdater = agentName
	// 1. 完成声明
	for _, key := range []string{"completed_todo", "done_todo", "complete_todo"} {
		if v, ok := rawInput[key]; ok && v != nil {
			s.CompleteTodos(stringifyArg(v))
		}
	}
	// 2. 逐键
	for k, v := range rawInput {
		lk := strings.ToLower(k)
		switch lk {
		case "todo":
			s.addTodoNodes(v)
		case "completed_todo", "done_todo", "complete_todo":
			// 已处理
		case "status":
			s.data["status"] = v
			if fmt.Sprint(v) == BlackboardStatusCompleted {
				if rv, ok := rawInput["result"]; ok {
					s.CompleteMatchingTodos(stringifyArg(rv))
				}
			}
		case "result":
			s.data[k] = v
			s.CompleteMatchingTodos(stringifyArg(v))
		default:
			s.data[k] = v
		}
	}
}

// AddDirect 直接回写（result/todo）。
func (s *BoardState) AddDirect(agentName, result, todo string) {
	s.lastUpdater = agentName
	if result != "" {
		s.data["output_"+agentName] = result
		s.CompleteMatchingTodos(result)
	}
	if todo != "" {
		s.ParseAndAddTodos(todo)
	}
}

// completeTodos 完成声明处理（精确或包含匹配移除）。
func (s *BoardState) CompleteTodos(raw string) {
	for _, item := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '，' || r == '；' || r == '\n' || r == '\r'
	}) {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		s.completeTodoExactOrContains(item)
	}
}

func (s *BoardState) completeTodoExactOrContains(item string) {
	lower := strings.ToLower(item)
	for i := 0; i < len(s.todos); i++ {
		todo := strings.ToLower(s.todos[i])
		if s.todos[i] == item || todo == lower || strings.Contains(todo, lower) || strings.Contains(lower, todo) {
			s.todos = append(s.todos[:i], s.todos[i+1:]...)
			i--
		}
	}
}

// completeMatchingTodos 文本命中清理（原文包含或完成语式）。
func (s *BoardState) CompleteMatchingTodos(text string) {
	if text == "" {
		return
	}
	lower := strings.ToLower(text)
	hasDoneWord := strings.Contains(lower, "完成") || strings.Contains(lower, "completed") || strings.Contains(lower, "done")
	for i := 0; i < len(s.todos); i++ {
		todo := s.todos[i]
		lt := strings.ToLower(todo)
		if todo == "" || strings.Contains(lower, lt) || (hasDoneWord && strings.Contains(lower, lt)) {
			s.todos = append(s.todos[:i], s.todos[i+1:]...)
			i--
		}
	}
}

// addTodoNodes todo 节点追加（数组/单值逐条）。
func (s *BoardState) addTodoNodes(v any) {
	if arr, ok := v.([]any); ok {
		for _, item := range arr {
			s.ParseAndAddTodos(stringifyArg(item))
		}
		return
	}
	s.ParseAndAddTodos(stringifyArg(v))
}

var todoPrefixRe = regexp.MustCompile(`^(?:\d+[.)、]\s*|[-*•]\s+)`)

// parseAndAddTodos 解析入队（分隔归一 → 去序号前缀 → 去重保序）。
func (s *BoardState) ParseAndAddTodos(raw string) {
	for _, item := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '，' || r == '；' || r == '\n' || r == '\r'
	}) {
		item = todoPrefixRe.ReplaceAllString(strings.TrimSpace(item), "")
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		dup := false
		for _, t := range s.todos {
			if t == item {
				dup = true
				break
			}
		}
		if !dup {
			s.todos = append(s.todos, item)
		}
	}
}

// Status 看板状态。
func (s *BoardState) Status() string {
	v, _ := s.data["status"].(string)
	return v
}

// String 看板序列化。
func (s *BoardState) String() string {
	out := map[string]any{}
	for k, v := range s.data {
		out[k] = v
	}
	if len(s.todos) > 0 {
		out["todo"] = s.todos
	}
	if s.lastUpdater != "" {
		out["_meta"] = map[string]any{"last_updater": s.lastUpdater}
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func stringifyArg(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// BlackboardProtocol 黑板协议（复用 Hierarchical 拓扑与指令骨架）。
type BlackboardProtocol struct {
	HierarchicalProtocol
}

// NewBlackboardProtocol 构造。
func NewBlackboardProtocol(config *Config) *BlackboardProtocol {
	inner := NewHierarchicalProtocol(config)
	p := &BlackboardProtocol{HierarchicalProtocol: *inner}
	p.setProtocolName("BLACKBOARD")
	return p
}

// Name 协议名。
func (p *BlackboardProtocol) Name() string { return "BLACKBOARD" }

// State 协作状态访问器（集成方看板/测试消费）。
func (p *BlackboardProtocol) State(trace *Trace) *BoardState { return p.getState(trace) }

// getState 状态取或建。
func (p *BlackboardProtocol) getState(trace *Trace) *BoardState {
	if trace == nil {
		return NewBoardState()
	}
	v, _ := trace.ProtocolContext().LoadOrStore(keyBlackboardState, NewBoardState())
	s, _ := v.(*BoardState)
	return s
}

// InjectAgentTools 注入 __sync_to_blackboard__（四参数全可选——避免 required
// 逼模型编造；returnDirect + MetaAgent）。
func (p *BlackboardProtocol) InjectAgentTools(ctx flow.Context, ag agent.Agent, add func(ai.FunctionTool)) {
	trace := Current(ctx)
	if trace == nil {
		return
	}
	state := p.getState(trace)
	self := ag.Name()

	add(ai.NewTool("__sync_to_blackboard__").
		Desc("将阶段结论、待办与完成声明同步到协作黑板（全参数可选，按需填写）").
		StringParam("result", "阶段结论（完成某条 TODO 时请写出该条原文）", false).
		StringParam("todo", "后续待办（分号/逗号分隔）", false).
		StringParam("completed_todo", "已完成的待办原文（分号/逗号分隔）", false).
		StringParam("state", "详细状态 JSON（可含 todo/completed_todo/status）", false).
		ReturnDirect(true).
		MetaSet(agent.MetaAgent, self).
		Handler(func(_ context.Context, args map[string]any) (any, error) {
			if raw := strings.TrimSpace(stringValue(args["completed_todo"])); raw != "" {
				state.CompleteTodos(raw)
			}
			state.AddDirect(self, strings.TrimSpace(stringValue(args["result"])), strings.TrimSpace(stringValue(args["todo"])))
			if raw := strings.TrimSpace(stringValue(args["state"])); strings.HasPrefix(raw, "{") {
				var m map[string]any
				if err := json.Unmarshal([]byte(raw), &m); err == nil && len(m) > 0 {
					state.Merge(self, m)
				}
			}
			return "系统：黑板已更新。后续专家将基于此状态继续。", nil
		}).
		Build())
}

// InjectAgentInstruction Hierarchical 汇报规范 + 黑板协作规范。
func (p *BlackboardProtocol) InjectAgentInstruction(ctx flow.Context, ag agent.Agent, sb *strings.Builder) {
	p.HierarchicalProtocol.InjectAgentInstruction(ctx, ag, sb)
	sb.WriteString("\n## 黑板协作规范\n")
	sb.WriteString("- 产出阶段结论时主动调用 __sync_to_blackboard__ 同步\n")
	sb.WriteString("- 数据导向：查黑板快照避免重复请求与冗余回合\n")
	sb.WriteString("- 闭环：完成待办时用 completed_todo 传原文，或让 result 完整写出该条内容\n")
}

// PrepareAgentPrompt Hierarchical 前置背景 + 黑板快照注入（copy-before-mutate）。
func (p *BlackboardProtocol) PrepareAgentPrompt(trace *Trace, ag agent.Agent, originalPrompt *ai.Prompt) *ai.Prompt {
	finalPrompt := p.HierarchicalProtocol.PrepareAgentPrompt(trace, ag, originalPrompt)
	state := p.getState(trace)
	return finalPrompt.AddMessage(ai.OfUser(
		"【协作黑板快照】\n```json\n" + state.String() + "\n```"))
}

// ResolveSupervisorRoute finish 时两级干预：todos 非空不得路由（"" 让
// Supervisor 重选）；FAILED 回派最后回写者；其余走 Hierarchical。
func (p *BlackboardProtocol) ResolveSupervisorRoute(ctx flow.Context, trace *Trace, decision string) string {
	state := p.getState(trace)
	if strings.Contains(decision, p.config.FinishMarker()) {
		if len(state.Todos()) > 0 {
			return "" // 不得把 todo 自由文本当 Agent 名路由
		}
		if state.Status() == BlackboardStatusFailed {
			if last := state.LastUpdater(); last != "" && p.config.HasAgent(last) {
				return p.config.NormalizeAgentName(last) // 回派修复
			}
			return ""
		}
	}
	return p.HierarchicalProtocol.ResolveSupervisorRoute(ctx, trace, decision)
}

// ShouldSupervisorRoute 终结审计：todo 未清或 FAILED 物理拦截（maxTurns 内）。
func (p *BlackboardProtocol) ShouldSupervisorRoute(ctx flow.Context, trace *Trace, decision string) bool {
	state := p.getState(trace)
	if strings.Contains(decision, p.config.FinishMarker()) &&
		(len(state.Todos()) > 0 || state.Status() == BlackboardStatusFailed) &&
		trace.TurnCount() < trace.Options().MaxTurns {
		return false
	}
	return p.HierarchicalProtocol.ShouldSupervisorRoute(ctx, trace, decision)
}

// PrepareSupervisorInstruction 黑板看板在前 + Hierarchical 运行看板。
func (p *BlackboardProtocol) PrepareSupervisorInstruction(ctx flow.Context, trace *Trace, sb *strings.Builder) {
	state := p.getState(trace)
	sb.WriteString("\n### 全局黑板 (Blackboard Dashboard)\n")
	if state.String() == "{}" {
		sb.WriteString("- Empty\n")
	} else {
		sb.WriteString("```json\n" + state.String() + "\n```\n")
	}
	p.HierarchicalProtocol.PrepareSupervisorInstruction(ctx, trace, sb)
}

// InjectSupervisorInstruction Hierarchical + 黑板管理原则。
func (p *BlackboardProtocol) InjectSupervisorInstruction(sb *strings.Builder) {
	p.HierarchicalProtocol.InjectSupervisorInstruction(sb)
	sb.WriteString("\n### 黑板管理原则\n")
	sb.WriteString("- Todo Driven：优先处理黑板 todo 列表中的待办\n")
	sb.WriteString("- 全部完成且信息补全时，整理结论并结束任务\n")
}

// ==================== MarketBased ====================

// MarketBased 常量。
const (
	keyMarketState = "market_state_obj"
	marketMinPrice = 0.1
)

// MarketAgentProfile 市场档案（EMA 质量/效率 + 动态身价）。
type MarketAgentProfile struct {
	Quality        float64 `json:"quality"`
	Efficiency     float64 `json:"efficiency"`
	CompletedTasks int     `json:"completedTasks"`
	BasePrice      float64 `json:"basePrice"`
	CurrentPrice   float64 `json:"currentPrice"`
}

// ROI 性价比 = (Quality×Efficiency)/max(MIN_PRICE, CurrentPrice)。
func (m *MarketAgentProfile) ROI() float64 {
	price := math.Max(marketMinPrice, m.CurrentPrice)
	return (m.Quality * m.Efficiency) / price
}

// MarketState 市场状态（保序成员档案表）。
type MarketState struct {
	marketplace map[string]*MarketAgentProfile
	order       []string
}

// NewMarketState 构造（导出面）。
func NewMarketState() *MarketState {
	return &MarketState{marketplace: map[string]*MarketAgentProfile{}}
}

// profileOf 取或建。
func (s *MarketState) profileOf(name string) *MarketAgentProfile {
	if p, ok := s.marketplace[name]; ok {
		return p
	}
	p := &MarketAgentProfile{Quality: 0.8, Efficiency: 0.7, BasePrice: 1.0, CurrentPrice: 1.0}
	s.marketplace[name] = p
	s.order = append(s.order, name)
	return p
}

// RecordTransaction 成交结算（EMA 更新 + 动态定价；导出面）。
func (s *MarketState) RecordTransaction(name string, q, e float64, ag agent.Agent) {
	p := s.profileOf(name)
	if p.CompletedTasks == 0 && ag != nil {
		p.BasePrice = resolveBasePrice(ag)
		p.CurrentPrice = p.BasePrice
	}
	p.CompletedTasks++
	p.Quality = p.Quality*0.6 + q*0.4
	p.Efficiency = p.Efficiency*0.6 + e*0.4
	// CurrentPrice = Base × (0.5+Quality) × (1+ln1p(deals)×0.15) × (多模态 1.5)
	modalityBoost := 1.0
	if ag != nil && ag.Profile() != nil {
		for _, mode := range ag.Profile().InputModes() {
			if mode == "image" {
				modalityBoost = 1.5
				break
			}
		}
	}
	p.CurrentPrice = p.BasePrice * (0.5 + p.Quality) * (1 + math.Log1p(float64(p.CompletedTasks))*0.15) * modalityBoost
}

// seedMarketplace 幂等种子（全员入表——首轮即可见 Price/ROI）。
func (s *MarketState) seedMarketplace(agents []agent.Agent) {
	for _, a := range agents {
		p := s.profileOf(a.Name())
		if p.CompletedTasks == 0 {
			p.BasePrice = resolveBasePrice(a)
			p.CurrentPrice = p.BasePrice
		}
	}
}

func resolveBasePrice(ag agent.Agent) float64 {
	if ag == nil || ag.Profile() == nil {
		return 1.0
	}
	v := ag.Profile().Metadata()["base_price"]
	switch n := v.(type) {
	case float64:
		if n >= marketMinPrice {
			return n
		}
	case int:
		if float64(n) >= marketMinPrice {
			return float64(n)
		}
	case string:
		var f float64
		if _, err := fmt.Sscanf(n, "%g", &f); err == nil && f >= marketMinPrice {
			return f
		}
	}
	return 1.0
}

// String 市场看板。
func (s *MarketState) String() string {
	var sb strings.Builder
	sb.WriteString("{")
	for i, name := range s.order {
		if i > 0 {
			sb.WriteString(", ")
		}
		p := s.marketplace[name]
		sb.WriteString(fmt.Sprintf(`"%s": {score: %.2f, price: %.2f, roi: %.2f, deals: %d}`,
			name, p.Quality, p.CurrentPrice, p.ROI(), p.CompletedTasks))
	}
	sb.WriteString("}")
	return sb.String()
}

// MarketBasedProtocol 市场协议（Hierarchical + 经济学信号）。
type MarketBasedProtocol struct {
	HierarchicalProtocol
}

// NewMarketBasedProtocol 构造。
func NewMarketBasedProtocol(config *Config) *MarketBasedProtocol {
	inner := NewHierarchicalProtocol(config)
	p := &MarketBasedProtocol{HierarchicalProtocol: *inner}
	p.setProtocolName("MARKET_BASED")
	return p
}

// Name 协议名。
func (p *MarketBasedProtocol) Name() string { return "MARKET_BASED" }

// State 协作状态访问器（集成方看板/测试消费）。
func (p *MarketBasedProtocol) State(trace *Trace) *MarketState { return p.getState(trace) }

// Profile 成员档案访问（缺则建）。
func (s *MarketState) Profile(name string) *MarketAgentProfile { return s.profileOf(name) }

// getState 状态取或建。
func (p *MarketBasedProtocol) getState(trace *Trace) *MarketState {
	if trace == nil {
		return NewMarketState()
	}
	v, _ := trace.ProtocolContext().LoadOrStore(keyMarketState, NewMarketState())
	s, _ := v.(*MarketState)
	return s
}

// PrepareSupervisorInstruction 市场看板 + 策略指引 + ROI King / Budget Pick。
func (p *MarketBasedProtocol) PrepareSupervisorInstruction(_ flow.Context, trace *Trace, sb *strings.Builder) {
	state := p.getState(trace)
	state.seedMarketplace(p.config.Agents())

	sb.WriteString("\n### 专家人才市场 (Expert Marketplace)\n")
	sb.WriteString("```json\n" + state.String() + "\n```\n")
	sb.WriteString("策略指引：\n")
	sb.WriteString("- 简单任务 → 选低价（Price 低）\n")
	sb.WriteString("- 攻坚任务 → 选高信誉与性价比（Score·ROI 高）\n")
	sb.WriteString("- 预算受限 → 选 Budget Pick\n")

	// ROI King / Budget Pick
	roiKing, budgetPick := "", ""
	var roiBest, priceLow float64 = -1, math.MaxFloat64
	for _, name := range state.order {
		prof := state.marketplace[name]
		if prof.ROI() > roiBest {
			roiBest = prof.ROI()
			roiKing = name
		}
		if prof.CurrentPrice < priceLow {
			priceLow = prof.CurrentPrice
			budgetPick = name
		}
	}
	if roiKing != "" {
		sb.WriteString(fmt.Sprintf("- **ROI King**: %s\n", roiKing))
	}
	if budgetPick != "" {
		sb.WriteString(fmt.Sprintf("- **Budget Pick**: %s\n", budgetPick))
	}
}

// InjectSupervisorInstruction Hierarchical + 调度准则。
func (p *MarketBasedProtocol) InjectSupervisorInstruction(sb *strings.Builder) {
	p.HierarchicalProtocol.InjectSupervisorInstruction(sb)
	sb.WriteString("\n### 调度准则：简单任务看价格(Price)，攻坚任务看信誉(Score)与性价比(ROI)。\n")
}

// OnAgentEnd 结算（质量/耗时 → EMA/身价）→ Hierarchical 看板吸收。
func (p *MarketBasedProtocol) OnAgentEnd(trace *Trace, ag agent.Agent) {
	state := p.getState(trace)
	content := trace.LastAgentContent()
	duration := trace.LastAgentDuration()

	var q, e float64
	if content == "" || strings.Contains(content, "Error:") || strings.Contains(content, "Exception:") {
		q, e = 0.1, 0.1
	} else {
		q = marketAssessQuality(content)
		e = math.Max(0.1, 1.0-float64(duration)/60000.0)
	}
	state.RecordTransaction(ag.Name(), q, e, ag)

	p.HierarchicalProtocol.OnAgentEnd(trace, ag)
}

// marketAssessQuality 产出质量启发式（市场结算用——Sequential 的同名 bool
// 判定不共用）。
func marketAssessQuality(content string) float64 {
	if content == "" {
		return 0.1
	}
	q := 0.5
	if strings.Contains(content, "```") {
		q += 0.2
	}
	if strings.Contains(content, "[Done]") || strings.Contains(content, "SUCCESS") {
		q += 0.2
	}
	if strings.Contains(content, "I'm sorry") || strings.Contains(content, "cannot fulfill") {
		q -= 0.2
	}
	return math.Max(0.1, math.Min(1.0, q))
}

// ==================== None ====================

// NoneProtocol 透明容器：TeamAgent 仅作为 Agent 挂载池，构图交外部
//（graphAdjuster / 手动调用）。
type NoneProtocol struct {
	ProtocolBase
}

// NewNoneProtocol 构造。
func NewNoneProtocol(config *Config) *NoneProtocol {
	p := &NoneProtocol{ProtocolBase: NewProtocolBase(config)}
	p.setProtocolName("NONE")
	return p
}

// Name 协议名。
func (p *NoneProtocol) Name() string { return "NONE" }

// BuildGraph 显式留空。
func (p *NoneProtocol) BuildGraph(*flow.GraphSpec) {}
