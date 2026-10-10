// protocols5_test.go 五新协议黑盒测试：None（透明容器零节点）、A2A（工具状态/
// 路由裁决/熔断/模态/回退/handover）、ContractNet（招标轮次/自动打分/守卫/
// 定标执行门禁/看板）、Blackboard（todo 生命周期/merge/门禁/快照注入）、
// Market（结算 EMA/定价/看板标注/质量启发式）。单元级直调协议方法（Go 侧
// 无需反射），端到端经 scripted 假模型覆盖 A2A 接力与 ContractNet 招标流。
package ai_agent_test

import (
	"encoding/json"
	"strings"
	"testing"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/ai/agent/team"
	"github.com/crazy-airhead/aifei-go/flow"
)

// newTeamTrace 直调协议方法用的最小轨迹（session + options + 原始输入）。
func newTeamTrace(t *testing.T, agents ...agent.Agent) (*team.Config, *team.Trace) {
	t.Helper()
	cfg := team.NewConfig(agentModel(t, newScripted(t, scriptedResp{body: chatJSON("supervisor 决策")})))
	cfg.SetProtocol(func(c *team.Config) team.Protocol { return team.NewNoneProtocol(c) })
	for _, a := range agents {
		cfg.AddAgent(a)
	}
	tr := team.NewTracePrepared(cfg, agent.NewMemorySession("test"), ai.PromptOf("原始任务"))
	return cfg, tr
}

// simpleAgent 固定输出测试成员（Profile 构造一次缓存——元数据可变）。
type simpleAgent struct {
	name    string
	role    string
	caps    []string
	profile *agent.Profile
}

func (a *simpleAgent) Name() string  { return a.name }
func (a *simpleAgent) Title() string { return a.name }
func (a *simpleAgent) Run(flow.Context, *flow.Node) error { return nil }
func (a *simpleAgent) Role() string { return a.role }
func (a *simpleAgent) RoleFor(flow.Context) string { return a.role }
func (a *simpleAgent) Profile() *agent.Profile {
	if a.profile == nil {
		p := agent.NewProfile()
		p.ConstraintAdd(a.role)
		for _, c := range a.caps {
			p.CapabilityAdd(c)
		}
		a.profile = p
	}
	return a.profile
}
func (a *simpleAgent) Metadata(flow.Context) map[string]any { return nil }
func (a *simpleAgent) Call(p *ai.Prompt, s agent.Session) (*ai.AssistantMessage, error) {
	return &ai.AssistantMessage{Text: "产出 of " + a.name}, nil
}

// ---- None ----

func TestTeamNoneProtocol(t *testing.T) {
	cfg, _ := newTeamTrace(t, &simpleAgent{name: "a", role: "r"})
	p := team.NewNoneProtocol(cfg)
	spec := flow.NewGraphSpec("none")
	p.BuildGraph(spec)
	if n := spec.GetNode(agent.IDStart); n != nil {
		t.Fatal("NONE 不构图")
	}
	if p.Name() != "NONE" {
		t.Fatalf("协议名：%s", p.Name())
	}
}

// ---- A2A ----

func TestTeamA2AStateAndRouting(t *testing.T) {
	cfg, trace := newTeamTrace(t,
		&simpleAgent{name: "designer", role: "前端设计 专家"},
		&simpleAgent{name: "developer", role: "后端开发 专家"})
	p := team.NewA2AProtocol(cfg)
	state := p.State(trace)

	// 移交请求：payload 锁定、state 累积
	state.SetTransferRequest("developer", `{"phase":"coding"}`, "memo: DATA_777")
	if state.PendingTarget() != "developer" || state.LastPayload() != "memo: DATA_777" {
		t.Fatalf("移交请求：%+v", state)
	}

	// 成功移交：确认后 count+1、pending 清空
	next := p.ResolveSupervisorRoute(flow.NewContext(), trace, "")
	if next != "developer" {
		t.Fatalf("合法目标应放行：%q", next)
	}
	if state.TransferCount() != 1 || state.PendingTarget() != "" {
		t.Fatalf("确认移交：%+v", state)
	}

	// 幻觉目标：回退上一位专家 + 系统通知
	trace.SetLastAgentName("designer")
	state.SetTransferRequest("ghost", "", "")
	next = p.ResolveSupervisorRoute(flow.NewContext(), trace, "")
	if next != "designer" {
		t.Fatalf("幻觉目标应回退上一位：%q", next)
	}
	found := false
	for _, r := range trace.Records() {
		if r.Source() == "Supervisor" && strings.Contains(r.Content(), "移交失败") {
			found = true
		}
	}
	if !found {
		t.Fatal("应记系统通知")
	}
	// 失败移交不耗额度
	if state.TransferCount() != 1 {
		t.Fatalf("失败不耗额度：%d", state.TransferCount())
	}

	// 熔断：5 次后中止
	for i := 0; i < 5; i++ {
		state.SetTransferRequest("developer", "", "")
		p.ResolveSupervisorRoute(flow.NewContext(), trace, "")
	}
	state.SetTransferRequest("developer", "", "")
	if next := p.ResolveSupervisorRoute(flow.NewContext(), trace, ""); next != "" {
		t.Fatalf("熔断后不路由：%q", next)
	}
}

func TestTeamA2AGraphShape(t *testing.T) {
	cfg, _ := newTeamTrace(t,
		&simpleAgent{name: "designer", role: "r1"},
		&simpleAgent{name: "developer", role: "r2"})
	p := team.NewA2AProtocol(cfg)
	spec := flow.NewGraphSpec("a2a")
	p.BuildGraph(spec)
	hasHandover := spec.GetNode("handover") != nil
	hasStart := spec.GetNode(agent.IDStart) != nil
	hasSupervisor := spec.GetNode("supervisor") != nil
	if !hasHandover || !hasStart || !hasSupervisor {
		t.Fatal("A2A 拓扑缺节点")
	}
	// Start 直连首位专家
	g, err := spec.Create()
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range g.GetLinks() {
		if l.PrevID() == agent.IDStart && l.NextID() == "designer" {
			return
		}
	}
	t.Fatal("Start 应直连首位专家")
}

// ---- ContractNet ----

func TestTeamContractNetBiddingFlow(t *testing.T) {
	py := &simpleAgent{name: "py_dev", role: "Python 开发", caps: []string{"python", "backend"}}
	java := &simpleAgent{name: "java_dev", role: "Java 后端 开发", caps: []string{"java", "backend"}}
	cfg, trace := newTeamTrace(t, py, java)
	p := team.NewContractNetProtocol(cfg)
	state := p.State(trace)

	// 招标轮次：两次可开、第三次拒
	if !state.TryStartNewBidding("need python backend api") {
		t.Fatal("首轮应可开")
	}
	if state.Rounds() != 1 {
		t.Fatalf("轮次：%d", state.Rounds())
	}
	// 首轮自动投标（有标后开新轮才会归档——Java bids 非空才归档同款）
	task := team.NewContractNetBiddingTask(cfg, p)
	task.Run(trace.Context(), nil)
	if !state.HasBids() {
		t.Fatal("自动投标应有标")
	}
	// capability 加权：python 任务 py_dev 分高（requirement 优先于 prompt）
	pyBid0, javaBid0 := state.GetBid("py_dev"), state.GetBid("java_dev")
	if !pyBid0.AutoBid || pyBid0.Score <= javaBid0.Score {
		t.Fatalf("python 任务 py_dev 应分高：%v vs %v", pyBid0, javaBid0)
	}
	if !strings.Contains(pyBid0.Plan, "python") {
		t.Fatalf("plan 记录命中：%s", pyBid0.Plan)
	}
	if !state.TryStartNewBidding("need java api") {
		t.Fatal("第二轮应可开")
	}
	// 旧标归档、bids 清空
	if state.HistoryCount() != 1 {
		t.Fatalf("归档：%d", state.HistoryCount())
	}
	if state.HasBids() {
		t.Fatal("新一轮应清标")
	}
	if state.TryStartNewBidding("again") {
		t.Fatal("第三轮应拒")
	}
	if state.Rounds() != 2 {
		t.Fatalf("轮次不涨：%d", state.Rounds())
	}

	// 第二轮自动投标：java 任务 java_dev 分高（requirement 已覆写）
	task.Run(trace.Context(), nil)
	if javaBid2 := state.GetBid("java_dev"); javaBid2 == nil || javaBid2.Score <= state.GetBid("py_dev").Score {
		t.Fatalf("java 任务 java_dev 应分高：%v vs %v", state.GetBid("java_dev"), state.GetBid("py_dev"))
	}
}

func TestTeamContractNetGuards(t *testing.T) {
	chef := &simpleAgent{name: "chef", role: "鲁菜 厨师"}
	cfg, trace := newTeamTrace(t, chef, &simpleAgent{name: "worker", role: "打杂"})
	p := team.NewContractNetProtocol(cfg)
	state := p.State(trace)

	// 无标指派：系统指引 + 开新标
	trace.SetRoute("")
	next := p.ResolveSupervisorRoute(flow.NewContext(), trace, "任务交给 chef 处理")
	if next != "bidding" {
		t.Fatalf("无标指派应开标：%q", next)
	}
	found := false
	for _, r := range trace.Records() {
		if r.Source() == "bidding" && strings.Contains(r.Content(), "指派无效") {
			found = true
		}
	}
	if !found {
		t.Fatal("应记系统指引")
	}

	// 有标定标（大小写不敏感）
	team.NewContractNetBiddingTask(cfg, p).Run(trace.Context(), nil)
	next = p.ResolveSupervisorRoute(flow.NewContext(), trace, "Chef")
	if next != "chef" || state.AwardedAgent() != "chef" {
		t.Fatalf("定标：%q winner=%s", next, state.AwardedAgent())
	}

	// isLogicFinished：定标未执行不放行
	if p.IsLogicFinished(trace) {
		t.Fatal("定标未执行应不放行")
	}
	trace.AddRecord(ai.RoleAssistant, "chef", "已完成烹饪", 0)
	if !p.IsLogicFinished(trace) {
		t.Fatal("中标者产出后应放行")
	}

	// OnSupervisorRouting：无标直路 → bidding
	trace2cfg, trace2 := newTeamTrace(t, chef)
	p2 := team.NewContractNetProtocol(trace2cfg)
	p2.OnSupervisorRouting(trace2.Context(), trace2, "chef")
	if trace2.Route() != "bidding" {
		t.Fatalf("旁路守卫：%q", trace2.Route())
	}

	// 看板序列化
	s := state.String()
	if !strings.Contains(s, "AWARDED") || !strings.Contains(s, "chef") {
		t.Fatalf("看板：%s", s)
	}
}

func TestTeamContractNetScoreNormalization(t *testing.T) {
	if team.NormalizeBidScore("85") != 85 {
		t.Fatal("字符串解析")
	}
	if team.NormalizeBidScore(float64(150)) != 100 {
		t.Fatal("上限 clamp")
	}
	if team.NormalizeBidScore(nil) != 50 {
		t.Fatal("缺省 50")
	}
	if team.NormalizeBidScore("abc") != 50 {
		t.Fatal("畸形缺省")
	}
	// 词边界：java 不误命中 javascript
	if team.MatchesKeyword("need javascript frontend", "java") {
		t.Fatal("词边界应防误命中")
	}
	if !team.MatchesKeyword("need java backend", "java") {
		t.Fatal("词边界应命中")
	}
}

// ---- Blackboard ----

func TestTeamBlackboardTodoLifecycle(t *testing.T) {
	s := team.NewBoardState()

	// 入队：分隔归一 + 序号前缀剥离 + 去重
	s.ParseAndAddTodos("1. 完成接口文档, 2、修复登录; 3. 补充测试\n完成接口文档")
	todos := s.Todos()
	if len(todos) != 3 {
		t.Fatalf("入队：%v", todos)
	}

	// completed_todo 精确删
	s.CompleteTodos("完成接口文档")
	if len(s.Todos()) != 2 {
		t.Fatalf("精确删除：%v", s.Todos())
	}

	// result 命中原文清空
	s.CompleteMatchingTodos("已修复登录功能并验证通过")
	if len(s.Todos()) != 1 || s.Todos()[0] != "补充测试" {
		t.Fatalf("命中清理：%v", s.Todos())
	}

	// merge：结构化状态
	s.Merge("backend", map[string]any{
		"api": "ok", "status": "COMPLETED", "result": "补充测试完成，全部通过",
		"todo": "生成部署清单",
	})
	if s.Data()["api"] != "ok" {
		t.Fatalf("KV 合并：%v", s.Data())
	}
	if len(s.Todos()) != 1 || s.Todos()[0] != "生成部署清单" {
		t.Fatalf("result 完成联动 todo：%v", s.Todos())
	}
	if s.LastUpdater() != "backend" {
		t.Fatalf("回写者：%s", s.LastUpdater())
	}

	// FAILED 状态
	s.Merge("qa", map[string]any{"status": "FAILED"})
	if s.Status() != "FAILED" {
		t.Fatalf("状态覆盖：%s", s.Status())
	}
}

func TestTeamBlackboardGatesAndPrompt(t *testing.T) {
	backend := &simpleAgent{name: "backend", role: "r"}
	cfg, trace := newTeamTrace(t, backend, &simpleAgent{name: "frontend", role: "r"})
	p := team.NewBlackboardProtocol(cfg)
	state := p.State(trace)

	// todos 非空：resolve 不路由（不得当 Agent 名）
	state.AddDirect("backend", "阶段产出", "完善接口文档")
	marker := cfg.FinishMarker()
	if next := p.ResolveSupervisorRoute(flow.NewContext(), trace, marker); next != "" {
		t.Fatalf("todo 未清不得路由：%q", next)
	}
	// shouldRoute 物理拦截
	if p.ShouldSupervisorRoute(flow.NewContext(), trace, marker) {
		t.Fatal("todo 未清应拦截 FINISH")
	}

	// todo 清空后放行（SOP：需已有专家产出与决策轮次）
	state.CompleteTodos("完善接口文档")
	trace.AddRecord(ai.RoleAssistant, "backend", "接口文档已完善", 0)
	trace.NextTurn()
	if !p.ShouldSupervisorRoute(flow.NewContext(), trace, marker) {
		t.Fatal("todo 清空后应放行（SOP 交基类）")
	}

	// FAILED 回派最后回写者
	state.Merge("backend", map[string]any{"status": "FAILED"})
	if next := p.ResolveSupervisorRoute(flow.NewContext(), trace, marker); next != "backend" {
		t.Fatalf("FAILED 回派：%q", next)
	}

	// 快照注入（copy-before-mutate：副本追加，原 prompt 不污染）
	state.Merge("backend", map[string]any{"score": 99})
	original := ai.PromptOf("原始任务")
	prompt := p.PrepareAgentPrompt(trace, backend, original)
	if len(original.Messages()) != 1 {
		t.Fatalf("原 prompt 被污染：%d", len(original.Messages()))
	}
	last := prompt.Messages()[len(prompt.Messages())-1]
	if !strings.Contains(messageTextOfForTeam(last), "协作黑板快照") ||
		!strings.Contains(messageTextOfForTeam(last), "99") {
		t.Fatalf("快照注入：%s", messageTextOfForTeam(last))
	}
}

func TestTeamBlackboardTools(t *testing.T) {
	backend := &simpleAgent{name: "backend", role: "r"}
	cfg, trace := newTeamTrace(t, backend)
	p := team.NewBlackboardProtocol(cfg)

	var tools []ai.FunctionTool
	p.InjectAgentTools(trace.Context(), backend, func(tool ai.FunctionTool) { tools = append(tools, tool) })
	if len(tools) != 1 || tools[0].Name() != "__sync_to_blackboard__" {
		t.Fatalf("工具：%v", tools)
	}
	state := p.State(trace)
	r := tools[0].Call(nil, map[string]any{
		"result": "api=ok 且接口文档已完善", "todo": "编写集成测试",
		"completed_todo": "完善接口文档",
	})
	if r == nil || r.IsError {
		t.Fatalf("工具执行：%+v", r)
	}
	if state.Data()["output_backend"] != "api=ok 且接口文档已完善" {
		t.Fatalf("直接回写：%v", state.Data())
	}
	// result 含「接口文档」字样 → completed_todo 已删 + result 联动
	if len(state.Todos()) != 1 || state.Todos()[0] != "编写集成测试" {
		t.Fatalf("todo 同步：%v", state.Todos())
	}

	// state JSON 形态
	r = tools[0].Call(nil, map[string]any{
		"state": `{"ui":"done","todo":"联调前后端"}`,
	})
	if r == nil || r.IsError {
		t.Fatal("state 形态失败")
	}
	if state.Data()["ui"] != "done" || len(state.Todos()) != 2 {
		t.Fatalf("state 合并：%v %v", state.Data(), state.Todos())
	}
}

// ---- Market ----

func TestTeamMarketSettlement(t *testing.T) {
	cheap := &simpleAgent{name: "junior", role: "初级 开发"}
	coder := &simpleAgent{name: "coder", role: "高级 开发"}
	cfg, trace := newTeamTrace(t, cheap, coder)
	p := team.NewMarketBasedProtocol(cfg)
	state := p.State(trace)

	// 结算：EMA 更新 + 成交计数
	trace.AddRecord(ai.RoleAssistant, "junior", "```go\nfunc main() {}\n``` [Done]", 1000)
	trace.SetLastAgentName("junior")
	p.OnAgentEnd(trace, cheap)
	junior := state.Profile("junior")
	if junior.CompletedTasks != 1 || junior.Quality <= 0.5 {
		t.Fatalf("结算：%+v", junior)
	}

	// 失败惩罚分（EMA 后相对上次结算值下降）
	qAfterSuccess := state.Profile("junior").Quality
	trace.AddRecord(ai.RoleAssistant, "junior", "Error: something failed", 0)
	trace.SetLastAgentName("junior")
	p.OnAgentEnd(trace, cheap)
	if state.Profile("junior").Quality >= qAfterSuccess {
		t.Fatalf("失败应拉低质量分：%v → %v", qAfterSuccess, state.Profile("junior").Quality)
	}

	// 定价：base_price 元数据
	expert := &simpleAgent{name: "expert", role: "专家"}
	expert.Profile().MetaPut("base_price", 2.5)
	p2state := team.NewMarketState()
	p2state.RecordTransaction("expert", 0.9, 0.9, expert)
	if p2state.Profile("expert").BasePrice != 2.5 {
		t.Fatalf("base_price 元数据：%+v", p2state.Profile("expert"))
	}
	if p2state.Profile("expert").CurrentPrice < 2.5 {
		t.Fatalf("成交后身价上涨：%+v", p2state.Profile("expert"))
	}

	// null profile 不炸
	p3state := team.NewMarketState()
	p3state.RecordTransaction("ghost", 0.5, 0.5, nil)
	if p3state.Profile("ghost").CompletedTasks != 1 {
		t.Fatal("null profile 结算")
	}
}

func TestTeamMarketInstruction(t *testing.T) {
	cfg, trace := newTeamTrace(t,
		&simpleAgent{name: "junior", role: "r"},
		&simpleAgent{name: "senior", role: "r"})
	p := team.NewMarketBasedProtocol(cfg)

	var sb strings.Builder
	p.PrepareSupervisorInstruction(trace.Context(), trace, &sb)
	out := sb.String()
	if !strings.Contains(out, "专家人才市场") || !strings.Contains(out, "ROI King") || !strings.Contains(out, "Budget Pick") {
		t.Fatalf("市场指令：%s", out)
	}
	var sb2 strings.Builder
	p.InjectSupervisorInstruction(&sb2)
	if !strings.Contains(sb2.String(), "调度准则") {
		t.Fatalf("静态指令：%s", sb2.String())
	}
}

func messageTextOfForTeam(m *ai.Message) string {
	if m == nil {
		return ""
	}
	if s, ok := m.Content.(string); ok {
		return s
	}
	return ""
}

var _ = json.Marshal
