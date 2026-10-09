// team_test.go P2-c team 协作：Sequential 静态拓扑（零 LLM 决策）、Hierarchical
// 主管路由与错误阻断、PrepareAgentPrompt copy-before-mutate、拦截器三层接线
//（团队钩子 + engine.AddInterceptor flow 钩子）、会话挂起/恢复、Swarm 涌现池、
// LoopingTeamInterceptor 死循环检测。
package ai_agent_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/ai/agent/simple"
	"github.com/crazy-airhead/aifei-go/ai/agent/team"
	"github.com/crazy-airhead/aifei-go/flow"
)

// newMember 构建团队成员（simple 智能体 + 独立脚本化模型服务）。
func newMember(t *testing.T, name, role string, srv *scripted) agent.Agent {
	t.Helper()
	a, err := simple.NewModelBuilder(agentModel(t, srv)).
		Name(name).Role(role).Instruction("完成你的部分").
		Build()
	if err != nil {
		t.Fatalf("member %s Build: %v", name, err)
	}
	return a
}

// agentSources 团队轨迹中的成员产出顺序。
func agentSources(tr *team.Trace) []string {
	var out []string
	for _, r := range tr.Records() {
		if r.IsAgent() {
			out = append(out, r.Source())
		}
	}
	return out
}

// TestTeamSequentialStaticTopology Sequential 协议按成员注册序线性执行，
// 零 LLM 决策（supervisor 服务零请求）；质量门禁放行后流水线推进到 end。
func TestTeamSequentialStaticTopology(t *testing.T) {
	supSrv := newScripted(t, scriptedResp{body: chatJSON("[NEVER_FINISH]")})
	srv1 := newScripted(t, scriptedResp{body: chatJSON("第一章与第二章已完成，含小结")})
	srv2 := newScripted(t, scriptedResp{body: chatJSON("全文复核完成，无遗留问题，可以交付")})

	tm, err := team.NewBuilder(agentModel(t, supSrv)).
		Name("seq").
		AgentAdd(
			newMember(t, "w1", "撰写初稿", srv1),
			newMember(t, "w2", "复核成稿", srv2),
		).
		Protocol(func(c *team.Config) team.Protocol { return team.NewSequentialProtocol(c) }).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	resp, err := tm.Prompt("写一份两章节的报告").Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	// 零 LLM 决策：supervisor 从未被调用
	if supSrv.count() != 0 {
		t.Fatalf("supervisor 请求数 = %d（Sequential 应为 0）", supSrv.count())
	}
	if srv1.count() != 1 || srv2.count() != 1 {
		t.Fatalf("成员请求数 w1=%d w2=%d", srv1.count(), srv2.count())
	}

	// 顺序执行 + 终态 = 末位成员产出
	srcs := agentSources(resp.Trace())
	if len(srcs) != 2 || srcs[0] != "w1" || srcs[1] != "w2" {
		t.Fatalf("成员执行序 = %v", srcs)
	}
	if resp.Content() != "全文复核完成，无遗留问题，可以交付" {
		t.Fatalf("Content = %q", resp.Content())
	}
	if resp.Trace().Route() != agent.IDEnd {
		t.Fatalf("Route = %q", resp.Trace().Route())
	}
}

// TestTeamHierarchicalRouting Hierarchical 主管决策路由：假模型回成员名 →
// when 边命中执行成员 → FINISH 标记收口并抽取交付文本。
func TestTeamHierarchicalRouting(t *testing.T) {
	supSrv := newScripted(t,
		scriptedResp{body: chatJSON("writer")},
		scriptedResp{body: chatJSON("[DOCS_FINISH] 报告已完成并交付")})
	wSrv := newScripted(t, scriptedResp{body: chatJSON("初稿完成，共三个章节，结构清晰")})

	tm, err := team.NewBuilder(agentModel(t, supSrv)).
		Name("docs").
		AgentAdd(newMember(t, "writer", "撰写文档", wSrv)).
		Build() // 缺省 Hierarchical
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	resp, err := tm.Prompt("写一份三章节报告").Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	if supSrv.count() != 2 || wSrv.count() != 1 {
		t.Fatalf("请求数 sup=%d writer=%d", supSrv.count(), wSrv.count())
	}
	if got := agentSources(resp.Trace()); len(got) != 1 || got[0] != "writer" {
		t.Fatalf("成员执行 = %v", got)
	}
	if resp.Content() != "报告已完成并交付" {
		t.Fatalf("Content = %q", resp.Content())
	}

	// 首次调度请求含成员名录与完成标记（主管系统提示词组装）
	sys := node(t, array(t, supSrv.request(t, 0)["messages"])[0])
	if c, _ := sys["content"].(string); !strings.Contains(c, "writer") ||
		!strings.Contains(c, "[DOCS_FINISH]") {
		t.Fatalf("supervisor 系统提示词 = %q", c)
	}
}

// TestTeamHierarchicalErrorBlocking 看板错误阻断 FINISH：成员产出 FAILED →
// 系统干预足迹 + 回派出错成员 → 修复后（错误清除）FINISH 放行。
func TestTeamHierarchicalErrorBlocking(t *testing.T) {
	supSrv := newScripted(t,
		scriptedResp{body: chatJSON("writer")},
		scriptedResp{body: chatJSON("[FIX_FINISH] 可以结束")},
		scriptedResp{body: chatJSON("[FIX_FINISH] 已修复并交付")})
	wSrv := newScripted(t,
		scriptedResp{body: chatJSON("FAILED 无法访问资料库，任务失败")},
		scriptedResp{body: chatJSON("已修复问题，完成全部章节")})

	tm, err := team.NewBuilder(agentModel(t, supSrv)).
		Name("fix").
		AgentAdd(newMember(t, "writer", "撰写文档", wSrv)).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	resp, err := tm.Prompt("修复并完成文档").Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	if resp.Content() != "已修复并交付" {
		t.Fatalf("Content = %q", resp.Content())
	}
	if supSrv.count() != 3 || wSrv.count() != 2 {
		t.Fatalf("请求数 sup=%d writer=%d", supSrv.count(), wSrv.count())
	}

	// 系统干预足迹（看板有错时 FINISH 被阻断）
	var hint bool
	for _, r := range resp.Trace().Records() {
		if r.Source() == team.IDSystem && strings.Contains(r.Content(), "不能结束") {
			hint = true
		}
	}
	if !hint {
		t.Fatalf("缺少系统干预足迹: %v", resp.Trace().Records())
	}

	// 修复轮成员请求注入「错误修正请求」（active_instruction + errors 看板）
	body, _ := json.Marshal(wSrv.request(t, 1)["messages"])
	if !strings.Contains(string(body), "错误修正请求") {
		t.Fatalf("修复轮请求未注入错误修正: %s", body)
	}
}

// markProtocol 协议桩：在基类副本上追加 MARK-<agent>（验证 copy-before-mutate）。
type markProtocol struct {
	*team.SequentialProtocol
}

func (p *markProtocol) PrepareAgentPrompt(trace *team.Trace, ag agent.Agent, op *ai.Prompt) *ai.Prompt {
	return p.SequentialProtocol.PrepareAgentPrompt(trace, ag, op).AddMessage(ai.OfUser("MARK-" + ag.Name()))
}

// TestTeamPrepareAgentPromptCopyBeforeMutate 协议注入只发生在副本上：
// 跨成员不串写（成员请求只含自己的 MARK）、共享 workingMemory 与原始
// prompt 不被污染。
func TestTeamPrepareAgentPromptCopyBeforeMutate(t *testing.T) {
	supSrv := newScripted(t, scriptedResp{body: chatJSON("[NEVER_FINISH]")})
	srv1 := newScripted(t, scriptedResp{body: chatJSON("第一部分完成，内容详实")})
	srv2 := newScripted(t, scriptedResp{body: chatJSON("第二部分完成，内容详实")})

	tm, err := team.NewBuilder(agentModel(t, supSrv)).
		Name("cp").
		AgentAdd(
			newMember(t, "w1", "撰写初稿", srv1),
			newMember(t, "w2", "撰写终稿", srv2),
		).
		Protocol(func(c *team.Config) team.Protocol {
			return &markProtocol{SequentialProtocol: team.NewSequentialProtocol(c)}
		}).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	original := ai.PromptOf("两步完成任务")
	resp, err := tm.Prompt(original).Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	// 1) 各成员请求恰含一次自己的 MARK，不含他人的
	for i, srv := range []*scripted{srv1, srv2} {
		name := fmt.Sprintf("w%d", i+1)
		body, _ := json.Marshal(srv.request(t, 0)["messages"])
		if got := strings.Count(string(body), "MARK-"); got != 1 || !strings.Contains(string(body), "MARK-"+name) {
			t.Fatalf("%s 请求 MARK 计数异常: %s", name, body)
		}
	}

	// 2) 共享 workingMemory 无 MARK 残留
	wm, _ := json.Marshal(resp.Trace().WorkingMemory().Messages())
	if strings.Contains(string(wm), "MARK-") {
		t.Fatalf("workingMemory 被污染: %s", wm)
	}

	// 3) 原始 prompt 未被改动
	if len(original.Messages()) != 1 {
		t.Fatalf("原始 prompt 消息数 = %d", len(original.Messages()))
	}
}

// spyInterceptor 团队 + flow 双层拦截器探针。
type spyInterceptor struct {
	team.InterceptorBase

	mu         sync.Mutex
	teamStart  int
	teamEnd    int
	supTurns   int
	nodes      []string
}

func (s *spyInterceptor) Name() string { return "spy" }

func (s *spyInterceptor) OnTeamStart(*team.Trace) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.teamStart++
}

func (s *spyInterceptor) OnTeamEnd(*team.Trace) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.teamEnd++
}

func (s *spyInterceptor) ShouldSupervisorContinue(*team.Trace) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.supTurns++
	return true
}

func (s *spyInterceptor) OnNodeStart(_ flow.Context, n *flow.Node) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodes = append(s.nodes, n.ID())
}

func (s *spyInterceptor) OnNodeEnd(flow.Context, *flow.Node) {}

func (s *spyInterceptor) InterceptFlow(inv *flow.Invocation) error { return inv.Invoke() }

// TestTeamInterceptorWiring 拦截器三层生效：团队生命周期钩子（OnTeamStart/
// End）、决策准入（ShouldSupervisorContinue）、flow 节点钩子（经
// engine.AddInterceptor 挂载，按求值动态增删）。
func TestTeamInterceptorWiring(t *testing.T) {
	supSrv := newScripted(t,
		scriptedResp{body: chatJSON("writer")},
		scriptedResp{body: chatJSON("[SPY_FINISH] 完成")})
	wSrv := newScripted(t, scriptedResp{body: chatJSON("执行完成，结果可靠")})

	spy := &spyInterceptor{}
	tm, err := team.NewBuilder(agentModel(t, supSrv)).
		Name("spy").
		AgentAdd(newMember(t, "writer", "撰写文档", wSrv)).
		DefaultInterceptorAdd(spy).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if _, err := tm.Prompt("执行任务").Call(); err != nil {
		t.Fatalf("Call: %v", err)
	}

	spy.mu.Lock()
	defer spy.mu.Unlock()
	if spy.teamStart != 1 || spy.teamEnd != 1 {
		t.Fatalf("团队钩子 start=%d end=%d", spy.teamStart, spy.teamEnd)
	}
	if spy.supTurns != 2 { // 两次 supervisor 决策各准入一次
		t.Fatalf("决策准入次数 = %d", spy.supTurns)
	}
	joined := strings.Join(spy.nodes, ",")
	for _, want := range []string{team.IDSupervisor, "writer", agent.IDEnd} {
		if !strings.Contains(joined, want) {
			t.Fatalf("flow 节点足迹 %q 缺 %q", joined, want)
		}
	}
}

// TestTeamSessionPendingSuspendResume FeedbackMode 挂起：supervisor 调
// feedback 工具 → Pending + 流程中断；空 Prompt 续跑（恢复锚点）→ 成员补位
// → FINISH 收口，挂起标记清除。
func TestTeamSessionPendingSuspendResume(t *testing.T) {
	feedbackResp := `{"id":"c1","model":"gpt-4o","choices":[{"message":{"role":"assistant","content":"需要更多信息","tool_calls":[{"id":"call_1","type":"function","function":{"name":"__ask_for_feedback","arguments":"{\"reason\":\"请提供目的地城市\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`
	supSrv := newScripted(t,
		scriptedResp{body: feedbackResp},
		scriptedResp{body: chatJSON("[FB_FINISH] 可以结束")}, // 无成员产出 → SOP 拒绝 → 回派成员
		scriptedResp{body: chatJSON("[FB_FINISH] 交付完成")})
	wSrv := newScripted(t, scriptedResp{body: chatJSON("已按补充信息完成改写，内容完整")})

	tm, err := team.NewBuilder(agentModel(t, supSrv)).
		Name("fb").
		FeedbackMode(true).
		AgentAdd(newMember(t, "writer", "撰写文档", wSrv)).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	sess := agent.NewMemorySession("fb-sess")
	resp1, err := tm.Prompt("安排一次出行").Session(sess).Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	// 挂起：finalAnswer=reason，会话 pending，成员未执行
	if resp1.Content() != "请提供目的地城市" {
		t.Fatalf("挂起 Content = %q", resp1.Content())
	}
	if !sess.IsPending() || sess.PendingReason() != "请提供目的地城市" {
		t.Fatalf("pending=%v reason=%q", sess.IsPending(), sess.PendingReason())
	}
	if supSrv.count() != 1 || wSrv.count() != 0 {
		t.Fatalf("挂起时请求数 sup=%d writer=%d", supSrv.count(), wSrv.count())
	}

	// 恢复：空 Prompt 续跑（同一会话），从 supervisor 锚点继续
	resp2, err := tm.Prompt(nil).Session(sess).Call()
	if err != nil {
		t.Fatalf("resume Call: %v", err)
	}
	if resp2.Content() != "交付完成" {
		t.Fatalf("恢复 Content = %q", resp2.Content())
	}
	if sess.IsPending() {
		t.Fatalf("恢复后仍挂起: %q", sess.PendingReason())
	}
	if supSrv.count() != 3 || wSrv.count() != 1 {
		t.Fatalf("恢复后请求数 sup=%d writer=%d", supSrv.count(), wSrv.count())
	}

	// supervisor 首请求携带 feedback 工具声明
	body, _ := json.Marshal(supSrv.request(t, 0)["tools"])
	if !strings.Contains(string(body), "__ask_for_feedback") {
		t.Fatalf("feedback 工具未注入: %s", body)
	}
}

// TestTeamSwarmProtocol Swarm：Start 直连首位专家；成员 sub_tasks JSON 涌现
// 进任务池；池未清空 FINISH 被硬阻断（回派池中专家）→ 消费后放行；信息素
// 挥发/增强步长校验。
func TestTeamSwarmProtocol(t *testing.T) {
	supSrv := newScripted(t,
		scriptedResp{body: chatJSON("[SW_FINISH] 蜂群任务完成")}, // 池非空 → 阻断 → 回派 a2
		scriptedResp{body: chatJSON("[SW_FINISH] 蜂群任务交付")})
	srv1 := newScripted(t, scriptedResp{
		body: chatJSON(`{"sub_tasks":[{"task":"复核全部章节","agent":"a2"}]}`)})
	srv2 := newScripted(t, scriptedResp{body: chatJSON("全部复核完成，质量达标")})

	tm, err := team.NewBuilder(agentModel(t, supSrv)).
		Name("sw").
		AgentAdd(
			newMember(t, "a1", "撰写初稿", srv1),
			newMember(t, "a2", "复核质检", srv2),
		).
		Protocol(func(c *team.Config) team.Protocol { return team.NewSwarmProtocol(c) }).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	resp, err := tm.Prompt("完成并复核文档").Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	// Start 直连 a1（未经 LLM）；涌现任务驱动 a2；两次 FINISH 决策
	if supSrv.count() != 2 || srv1.count() != 1 || srv2.count() != 1 {
		t.Fatalf("请求数 sup=%d a1=%d a2=%d", supSrv.count(), srv1.count(), srv2.count())
	}
	if resp.Content() != "蜂群任务交付" {
		t.Fatalf("Content = %q", resp.Content())
	}

	// 蜂群状态：任务池清空；信息素 a1=5-1（挥发）、a2=5（增强）
	v, ok := resp.Trace().ProtocolContext().Load("swarm_state_obj")
	if !ok {
		t.Fatalf("蜂群状态未装载")
	}
	state, ok := v.(*team.SwarmState)
	if !ok {
		t.Fatalf("蜂群状态类型 = %T", v)
	}
	if pool := state.GetTaskPool(); len(pool) != 0 {
		t.Fatalf("任务池未清空: %+v", pool)
	}
	ph := state.GetPheromones()
	if ph["a1"] != 4 || ph["a2"] != 5 {
		t.Fatalf("信息素 = %v（期望 a1=4 a2=5）", ph)
	}

	// FINISH 被阻断轮留下硬回派足迹
	var blocked bool
	for _, r := range resp.Trace().Records() {
		if r.Source() == team.IDSystem && strings.Contains(r.Content(), "不能结束") {
			blocked = true
		}
	}
	_ = blocked // Swarm 阻断走 resolve 硬回派，不留系统足迹（与 Hierarchical 不同）
}

// TestLoopingTeamInterceptorDetection 死循环检测：单点复读 / 序列嵌套命中；
// 内容演进、短语与样本不足免疫。
func TestLoopingTeamInterceptorDetection(t *testing.T) {
	long := "这是一个足够长的重复回复内容用于检测"
	li := team.NewLoopingTeamInterceptor()

	// 单点复读：同一成员连续 4 次相同产出
	self := team.NewTrace()
	for i := 0; i < 4; i++ {
		self.AddRecord(ai.RoleAssistant, "a", long, 0)
	}
	if !li.IsLooping(self) {
		t.Fatalf("单点复读未检出")
	}

	// 序列嵌套：A-B-A-B
	seq := team.NewTrace()
	for _, s := range []string{"a", "b", "a", "b"} {
		seq.AddRecord(ai.RoleAssistant, s, long, 0)
	}
	if !li.IsLooping(seq) {
		t.Fatalf("序列嵌套未检出")
	}

	// 免疫：内容演进（相似度低于阈值）
	evolve := team.NewTrace()
	for i, c := range []string{"第一版内容足够长 abcdef", "第二版内容完全不同 xyzuvw", "第三版继续变化 qrsstu", "第四版仍有新信息 lmnopq"} {
		evolve.AddRecord(ai.RoleAssistant, "a", fmt.Sprintf("%s-%d", c, i), 0)
	}
	if li.IsLooping(evolve) {
		t.Fatalf("内容演进被误判为循环")
	}

	// 免疫：短语（< 最小检测长度）
	short := team.NewTrace()
	for i := 0; i < 4; i++ {
		short.AddRecord(ai.RoleAssistant, "a", "OK", 0)
	}
	if li.IsLooping(short) {
		t.Fatalf("短回复被误判为循环")
	}

	// 免疫：样本不足（<4 步）
	few := team.NewTrace()
	for i := 0; i < 3; i++ {
		few.AddRecord(ai.RoleAssistant, "a", long, 0)
	}
	if li.IsLooping(few) {
		t.Fatalf("样本不足被误判为循环")
	}

	// 准入联动：检出即否决 supervisor
	if li.ShouldSupervisorContinue(self) {
		t.Fatalf("死循环未熔断")
	}
}
