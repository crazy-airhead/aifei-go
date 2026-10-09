// agent_base_test.go P2-a 基座端到端：Agent 放进 flow 图当节点（AddActivityNamed）、
// _SESSION_ 会话复用、团队缝（拦截 veto / NodeStart/NodeEnd / 轨迹记录 / 协议后处理）、
// Metadata / RenderVars。
package ai_agent_test

import (
	"strings"
	"testing"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/flow"
)

// echoAgent 桩智能体：记录调用，返回「回声: 末条 user 文本」。
type echoAgent struct {
	*agent.AgentBase
	name        string
	calls       int
	lastPrompt  *ai.Prompt
	lastSession agent.Session
}

func newEchoAgent(name ...string) *echoAgent {
	e := &echoAgent{name: "echo"}
	if len(name) > 0 {
		e.name = name[0]
	}
	e.AgentBase = agent.NewAgentBase(e)
	return e
}

func (e *echoAgent) Name() string  { return e.name }
func (e *echoAgent) Title() string { return "回声" }
func (e *echoAgent) Role() string  { return "#{project} 的值班助手" }
func (e *echoAgent) Profile() *agent.Profile {
	return agent.NewProfile().CapabilityAdd("writer")
}

func (e *echoAgent) RoleFor(ctx flow.Context) string { return agent.RenderVars(e.Role(), ctx) }

func (e *echoAgent) Call(p *ai.Prompt, s agent.Session) (*ai.AssistantMessage, error) {
	e.calls++
	e.lastPrompt, e.lastSession = p, s
	text := ""
	if p != nil {
		for _, m := range p.Messages() {
			if m.Role == ai.RoleUser {
				if s2, ok := m.Content.(string); ok {
					text = s2
				}
			}
		}
	}
	return &ai.AssistantMessage{Text: "回声: " + text}, nil
}

func TestAgentAsFlowNodeEndToEnd(t *testing.T) {
	echo := newEchoAgent()
	g, err := flow.Create("echo-flow", func(s *flow.GraphSpec) {
		s.AddStart("s").LinkAdd("echo")
		s.AddActivityNamed(echo).LinkAdd("e")
		s.AddEnd("e")
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	e := flow.NewEngine()
	ctx := flow.NewContext("echo-flow")
	if err := e.Eval(g, ctx); err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if echo.calls != 1 {
		t.Fatalf("调用次数 = %d", echo.calls)
	}
	sess, ok := ctx.Get(agent.KeySession).(agent.Session)
	if !ok || sess == nil {
		t.Fatalf("ctx 未装载 _SESSION_: %v", ctx.Get(agent.KeySession))
	}

	// 二次 eval（新 ctx 预装 _SESSION_——同 ctx 重复 Eval 是续跑语义，不重放）：
	// 会话复用（同一实例）
	ctx2 := flow.NewContext("echo-flow")
	ctx2.Put(agent.KeySession, sess)
	if err := e.Eval(g, ctx2); err != nil {
		t.Fatalf("Eval 2: %v", err)
	}
	if echo.calls != 2 {
		t.Fatalf("二次调用后次数 = %d", echo.calls)
	}
	if sess2, _ := ctx2.Get(agent.KeySession).(agent.Session); sess2 != sess {
		t.Fatalf("会话应复用，得到新实例")
	}
}

func TestAgentSessionSharedAcrossNodes(t *testing.T) {
	a1, a2 := newEchoAgent("first"), newEchoAgent("second")
	g, err := flow.Create("two-flow", func(s *flow.GraphSpec) {
		s.AddStart("s").LinkAdd("first")
		s.AddActivityNamed(a1).LinkAdd("second")
		s.AddActivityNamed(a2).LinkAdd("e")
		s.AddEnd("e")
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := flow.NewEngine().Eval(g, flow.NewContext("two")); err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if a1.lastSession == nil || a1.lastSession != a2.lastSession {
		t.Fatalf("同图两节点应共享会话")
	}
}

// ---- 团队缝桩 ----

type teamProtocolStub struct {
	preparedPrompt  *ai.Prompt
	injected        strings.Builder
	tools           []string
	resolvedPrefix  string
	onAgentEndCalls int
}

func (p *teamProtocolStub) PrepareAgentPrompt(trace agent.TeamTrace, ag agent.Agent, wm *ai.Prompt) *ai.Prompt {
	return p.preparedPrompt
}
func (p *teamProtocolStub) InjectAgentInstruction(ctx flow.Context, ag agent.Agent, sb *strings.Builder) {
	sb.WriteString("## 当前接力任务断面\n已完成：A 步")
}
func (p *teamProtocolStub) InjectAgentTools(ctx flow.Context, ag agent.Agent, add func(ai.FunctionTool)) {
}
func (p *teamProtocolStub) ResolveAgentOutput(trace agent.TeamTrace, ag agent.Agent, content string) string {
	if content == "" {
		return "" // Java：兜底文案只在协议解析结果为空时生效
	}
	return "[" + p.resolvedPrefix + "] " + content
}
func (p *teamProtocolStub) OnAgentEnd(trace agent.TeamTrace, ag agent.Agent) {
	p.onAgentEndCalls++
}

type teamInterceptorStub struct {
	name      string
	enabled   bool
	continue_ bool
	endCalls  int
}

func (i *teamInterceptorStub) Name() string    { return i.name }
func (i *teamInterceptorStub) IsEnabled() bool { return i.enabled }
func (i *teamInterceptorStub) ShouldAgentContinue(agent.TeamTrace, agent.Agent) bool {
	return i.continue_
}
func (i *teamInterceptorStub) OnAgentEnd(agent.TeamTrace, agent.Agent) { i.endCalls++ }

type teamTraceStub struct {
	runID     string
	lastAgent string
	metrics   *agent.Metrics
	protocol  *teamProtocolStub
	ics       []agent.TeamInterceptor
	sink      agent.EventSink

	events      []*agent.Event
	records     []string
	addedDeltas []*agent.Metrics
}

func (s *teamTraceStub) RunID() string                         { return s.runID }
func (s *teamTraceStub) AgentName() string                     { return s.lastAgent }
func (s *teamTraceStub) BeginTimeMs() int64                    { return 0 }
func (s *teamTraceStub) OriginalPrompt() *ai.Prompt            { return nil }
func (s *teamTraceStub) WorkingMemory() *ai.Prompt             { return ai.NewPrompt() }
func (s *teamTraceStub) Metrics() *agent.Metrics               { return s.metrics }
func (s *teamTraceStub) SetLastAgentName(n string)             { s.lastAgent = n }
func (s *teamTraceStub) LastAgentName() string                 { return s.lastAgent }
func (s *teamTraceStub) Protocol() agent.TeamProtocol          { return s.protocol }
func (s *teamTraceStub) Interceptors() []agent.TeamInterceptor { return s.ics }
func (s *teamTraceStub) HasStreamSink() bool                   { return s.sink != nil }
func (s *teamTraceStub) StreamSink() agent.EventSink           { return s.sink }
func (s *teamTraceStub) PushAgentEvent(ev *agent.Event) {
	if s.sink != nil {
		_ = s.sink.OnEvent(ev)
	}
	s.events = append(s.events, ev)
}
func (s *teamTraceStub) AddRecord(role ai.Role, agentName, content string, durationMs int64) {
	s.records = append(s.records, agentName+"|"+content)
}

func TestAgentTeamInterceptorVeto(t *testing.T) {
	echo := newEchoAgent()
	ic := &teamInterceptorStub{name: "BudgetGuard", enabled: true, continue_: false}
	trace := &teamTraceStub{runID: "run-1", metrics: &agent.Metrics{},
		protocol: &teamProtocolStub{resolvedPrefix: "P"}, ics: []agent.TeamInterceptor{ic}}

	ctx := flow.NewContext("g")
	ctx.Put(agent.KeyCurrentTeamTraceKey, "team")
	ctx.Put("team", agent.TeamTrace(trace))

	if err := echo.Run(ctx, nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	if echo.calls != 0 {
		t.Fatalf("veto 后不应调用智能体")
	}
	if len(trace.records) != 1 || !strings.HasPrefix(trace.records[0], "echo|[Skipped] Cancelled by BudgetGuard") {
		t.Fatalf("记录 = %v", trace.records)
	}
	if trace.lastAgent != "echo" {
		t.Fatalf("lastAgentName = %q", trace.lastAgent)
	}
}

func TestAgentTeamRecordAndNodeEvents(t *testing.T) {
	echo := newEchoAgent()
	rec := &eventRecorder{}
	proto := &teamProtocolStub{resolvedPrefix: "P",
		preparedPrompt: ai.NewPrompt(ai.OfUser("团队问题"))}
	ic := &teamInterceptorStub{name: "Audit", enabled: true, continue_: true}
	trace := &teamTraceStub{runID: "run-2", metrics: &agent.Metrics{}, protocol: proto,
		ics: []agent.TeamInterceptor{ic}, sink: rec}

	ctx := flow.NewContext("g")
	ctx.Put(agent.KeyCurrentTeamTraceKey, "team")
	ctx.Put("team", agent.TeamTrace(trace))

	if err := echo.Run(ctx, nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	if echo.calls != 1 || echo.lastPrompt == nil || echo.lastPrompt.Messages()[0].Content != "团队问题" {
		t.Fatalf("协议提示词未生效: calls=%d prompt=%v", echo.calls, echo.lastPrompt)
	}
	// NodeStart/NodeEnd 事件（经团队流出口推送；NodeEnd 携带产出）
	starts, ends := rec.of(agent.KindNodeStart), rec.of(agent.KindNodeEnd)
	if len(starts) != 1 || len(ends) != 1 {
		t.Fatalf("NodeStart/NodeEnd = %d/%d", len(starts), len(ends))
	}
	if starts[0].RunID != "run-2" || starts[0].AgentName != "echo" {
		t.Fatalf("NodeStart = %+v", starts[0])
	}
	if ends[0].Message == nil || ends[0].Message.Text != "回声: 团队问题" {
		t.Fatalf("NodeEnd 消息 = %+v", ends[0].Message)
	}
	// 记录内容 = 协议解析产出
	if len(trace.records) != 1 || trace.records[0] != "echo|[P] 回声: 团队问题" {
		t.Fatalf("记录 = %v", trace.records)
	}
	if proto.onAgentEndCalls != 1 || ic.endCalls != 1 {
		t.Fatalf("OnAgentEnd：协议 %d 次 / 拦截器 %d 次", proto.onAgentEndCalls, ic.endCalls)
	}
}

func TestAgentTeamEmptyOutputFallbackText(t *testing.T) {
	// Call 返回空文本 → 兜底文案
	empty := &nilAgent{}
	empty.AgentBase = agent.NewAgentBase(empty)
	trace := &teamTraceStub{runID: "r", metrics: &agent.Metrics{}, protocol: &teamProtocolStub{resolvedPrefix: "P"}}
	ctx := flow.NewContext("g")
	ctx.Put(agent.KeyCurrentTeamTraceKey, "team")
	ctx.Put("team", agent.TeamTrace(trace))
	if err := empty.Run(ctx, nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(trace.records) != 1 || !strings.Contains(trace.records[0],
		"Agent [nil] processed but returned no textual content.") {
		t.Fatalf("空产出兜底 = %v", trace.records)
	}
}

type nilAgent struct {
	*agent.AgentBase
}

func (n *nilAgent) Name() string                { return "nil" }
func (n *nilAgent) Title() string               { return "nil" }
func (n *nilAgent) Role() string                { return "" }
func (n *nilAgent) RoleFor(flow.Context) string { return "" }
func (n *nilAgent) Profile() *agent.Profile {
	return nil
}
func (n *nilAgent) Call(*ai.Prompt, agent.Session) (*ai.AssistantMessage, error) {
	return nil, nil // 中断语义：nil 消息
}

func TestMetadataAndRenderVars(t *testing.T) {
	echo := newEchoAgent()
	ctx := flow.NewContext("g")
	ctx.Put("project", "aifei-go")

	if got := echo.RoleFor(ctx); got != "aifei-go 的值班助手" {
		t.Fatalf("RoleFor = %q", got)
	}
	meta := echo.Metadata(ctx)
	if meta["name"] != "echo" || meta["role"] != "aifei-go 的值班助手" {
		t.Fatalf("metadata = %v", meta)
	}
	caps, _ := meta["capabilities"].([]string)
	if len(caps) != 2 || caps[1] != "writer" {
		t.Fatalf("capabilities = %v（应含缺省 assistant）", caps)
	}
	if _, has := meta["inputModes"]; !has {
		t.Fatalf("inputModes 应输出: %v", meta)
	}

	// 未知键原样保留
	if got := agent.RenderVars("#{missing} 保底", ctx); got != "#{missing} 保底" {
		t.Fatalf("未知键渲染 = %q", got)
	}
}
