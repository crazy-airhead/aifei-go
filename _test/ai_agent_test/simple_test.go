// simple_test.go P2-a Simple 智能体：基础调用与会话归档、历史窗口请求体、
// 空提示词恢复（零 HTTP）、仅传输错误重试、OutputSchema 结构化输出、
// Handler 兜底、纯思考降级、工具调用不归档、OutputKey 写回、团队增量汇总、
// flow 图端到端（_SESSION_ 预绑定）。
package ai_agent_test

import (
	"errors"
	"strings"
	"testing"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/ai/agent/simple"
	"github.com/crazy-airhead/aifei-go/ai/httpx"
	"github.com/crazy-airhead/aifei-go/flow"
)

func TestSimpleCallBasic(t *testing.T) {
	srv := newScripted(t, scriptedResp{body: chatJSON("你好，我是简单智能体")})
	a, err := simple.NewModelBuilder(agentModel(t, srv)).
		Name("hello").Role("值班助手").Instruction("回答要简短").Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	resp, err := a.Prompt("你好").Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Content() != "你好，我是简单智能体" {
		t.Fatalf("Content = %q", resp.Content())
	}
	if srv.count() != 1 {
		t.Fatalf("请求数 = %d", srv.count())
	}

	// 轨迹装在会话上下文 "__hello"
	trace, ok := resp.Context().Get("__hello").(*simple.Trace)
	if !ok || trace == nil {
		t.Fatalf("ctx 未装载轨迹: %v", resp.Context().Get("__hello"))
	}
	// 终态消息带 runId 元数据；指标收到 usage
	if resp.Message().Metadata[agent.MetaRunID] != trace.RunID() {
		t.Fatalf("runId 元数据 = %v", resp.Message().Metadata)
	}
	if got := resp.Metrics().TotalTokens(); got != 5 {
		t.Fatalf("TotalTokens = %d", got)
	}

	// 会话归档：user 提问 + assistant 回答
	msgs := resp.Session().Messages()
	if len(msgs) != 2 || msgs[0].Role != ai.RoleUser || msgs[1].Role != ai.RoleAssistant {
		t.Fatalf("会话消息 = %+v", msgs)
	}
	if am, ok := msgs[1].Content.(*ai.AssistantMessage); !ok || am.Text != "你好，我是简单智能体" {
		t.Fatalf("assistant 归档 = %+v", msgs[1].Content)
	}
	// 工作记忆按轮归档模型请求消息
	if len(trace.WorkingMemory().Messages()) == 0 {
		t.Fatalf("工作记忆为空")
	}
}

func TestSimpleSessionWindowRequest(t *testing.T) {
	srv := newScripted(t,
		scriptedResp{body: chatJSON("答一")},
		scriptedResp{body: chatJSON("答二")},
		scriptedResp{body: chatJSON("答三")})
	a, err := simple.NewModelBuilder(agentModel(t, srv)).
		Name("win").Role("值班助手").Instruction("回答要简短").Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	sess := agent.NewMemorySession("w1")
	for _, q := range []string{"第一问", "第二问"} {
		if _, err := a.Prompt(q).Session(sess).Call(); err != nil {
			t.Fatalf("Call %s: %v", q, err)
		}
	}

	// 第二次请求：system + 历史窗口（user/asst）+ 新问题
	body := srv.request(t, 1)
	msgs := array(t, body["messages"])
	if len(msgs) != 4 {
		t.Fatalf("messages = %d 条: %v", len(msgs), msgs)
	}
	sys := node(t, msgs[0])
	if sys["role"] != "system" {
		t.Fatalf("首条应为 system: %v", sys)
	}
	if c, _ := sys["content"].(string); !strings.Contains(c, "值班助手") || !strings.Contains(c, "回答要简短") {
		t.Fatalf("system 内容 = %q", c)
	}
	if node(t, msgs[1])["content"] != "第一问" || node(t, msgs[2])["role"] != "assistant" ||
		node(t, msgs[2])["content"] != "答一" || node(t, msgs[3])["content"] != "第二问" {
		t.Fatalf("消息序列 = %v", msgs)
	}

	// 窗口收敛：SessionWindow(1) 只带最近 1 条历史
	a2, err := simple.NewModelBuilder(agentModel(t, srv)).
		Name("win2").SessionWindow(1).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	sess2 := agent.NewMemorySession("w2")
	for _, q := range []string{"第一问", "第二问", "第三问"} {
		if _, err := a2.Prompt(q).Session(sess2).Call(); err != nil {
			t.Fatalf("Call %s: %v", q, err)
		}
	}
	// a2 无 Role/Instruction → 无 system 消息；窗口 1 对齐到最近一轮
	//（LatestMessages 以 user 边界起步：user + assistant 成对带回）
	msgs = array(t, srv.request(t, 4)["messages"])
	if len(msgs) != 3 {
		t.Fatalf("窗口 1 时 messages = %d 条: %v", len(msgs), msgs)
	}
	if node(t, msgs[0])["content"] != "第二问" || node(t, msgs[1])["role"] != "assistant" ||
		node(t, msgs[1])["content"] != "答三" || node(t, msgs[2])["content"] != "第三问" {
		t.Fatalf("窗口 1 消息序列 = %v", msgs)
	}
}

func TestSimpleEmptyPromptResumeNoHTTP(t *testing.T) {
	srv := newScripted(t, scriptedResp{body: chatJSON("不应到达")})
	a, err := simple.NewModelBuilder(agentModel(t, srv)).Name("empty").Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	resp, err := a.Prompt(nil).Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Content() != "" {
		t.Fatalf("空提示词应返回空 assistant，得到 %q", resp.Content())
	}
	if srv.count() != 0 {
		t.Fatalf("不应发起 HTTP 请求，发了 %d 次", srv.count())
	}
}

func TestSimpleRetryTransportOnly(t *testing.T) {
	t.Run("传输错误重试后成功", func(t *testing.T) {
		srv := newScripted(t,
			scriptedResp{status: 500, body: `{"error":{"message":"upstream boom"}}`},
			scriptedResp{body: chatJSON("恢复了")})
		a, err := simple.NewModelBuilder(agentModel(t, srv)).
			Name("r1").Retry(3, 500).Build()
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		resp, err := a.Prompt("问").Call()
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		if resp.Content() != "恢复了" || srv.count() != 2 {
			t.Fatalf("content=%q requests=%d", resp.Content(), srv.count())
		}
	})

	t.Run("业务错误不重试", func(t *testing.T) {
		srv := newScripted(t, scriptedResp{status: 400, body: `{"error":{"message":"业务错误"}}`})
		a, err := simple.NewModelBuilder(agentModel(t, srv)).
			Name("r2").Retry(3, 500).Build()
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		_, err = a.Prompt("问").Call()
		if err == nil {
			t.Fatalf("业务错误应返回错误")
		}
		var he *httpx.HTTPError
		if !errors.As(err, &he) || he.Status != 400 {
			t.Fatalf("错误 = %v（应为 HTTP 400）", err)
		}
		if srv.count() != 1 {
			t.Fatalf("业务错误不应重试，请求 %d 次", srv.count())
		}
	})

	t.Run("重试耗尽返回最后错误", func(t *testing.T) {
		srv := newScripted(t, scriptedResp{status: 500, body: `{"error":{"message":"持续故障"}}`})
		a, err := simple.NewModelBuilder(agentModel(t, srv)).
			Name("r3").Retry(2, 500).Build()
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		_, err = a.Prompt("问").Call()
		var he *httpx.HTTPError
		if !errors.As(err, &he) || he.Status != 500 {
			t.Fatalf("错误 = %v（应为 HTTP 500）", err)
		}
		if srv.count() != 2 { // MaxRetries=2 → 共 2 次尝试
			t.Fatalf("耗尽后请求 %d 次", srv.count())
		}
	})
}

type weatherOut struct {
	City  string `json:"city"`
	TempC int    `json:"temp_c"`
}

func TestSimpleOutputSchema(t *testing.T) {
	srv := newScripted(t, scriptedResp{body: chatJSON(`{"city":"北京","temp_c":23}`)})
	a, err := simple.NewModelBuilder(agentModel(t, srv)).
		Name("weather").OutputSchema(simple.OutputSchemaOf[weatherOut]()).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	resp, err := a.Prompt("北京天气").Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	out, err := simple.Bind[weatherOut](resp)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if out.City != "北京" || out.TempC != 23 {
		t.Fatalf("结构化输出 = %+v", out)
	}
	// 请求体：response_format=json_object；system 含 schema 指令
	body := srv.request(t, 0)
	rf := node(t, body["response_format"])
	if rf["type"] != "json_object" {
		t.Fatalf("response_format = %v", rf)
	}
	sys := node(t, array(t, body["messages"])[0])
	if c, _ := sys["content"].(string); !strings.Contains(c, "OUTPUT FORMAT") {
		t.Fatalf("system 应含 schema 指令: %q", c)
	}
}

func TestSimpleHandlerFallback(t *testing.T) {
	var gotPrompt *ai.Prompt
	var gotSession agent.Session
	a, err := simple.NewBuilder().Name("local").Instruction("本地处理").
		Handler(func(p *ai.Prompt, s agent.Session) (*ai.AssistantMessage, error) {
			gotPrompt, gotSession = p, s
			return &ai.AssistantMessage{Text: "本地处理结果"}, nil
		}).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	resp, err := a.Prompt("问").Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Content() != "本地处理结果" {
		t.Fatalf("Content = %q", resp.Content())
	}
	if gotPrompt == nil || gotSession == nil {
		t.Fatalf("Handler 未被调用")
	}
	// Handler 收到组装后的最终提示词：system（含指令）+ user
	hm := gotPrompt.Messages()
	if len(hm) != 2 || hm[0].Role != ai.RoleSystem || hm[1].Role != ai.RoleUser {
		t.Fatalf("Handler 提示词 = %+v", hm)
	}
	if c, _ := hm[0].Content.(string); !strings.Contains(c, "本地处理") {
		t.Fatalf("system = %q", c)
	}
}

func TestSimpleThinkingOnlyDowngrade(t *testing.T) {
	body := `{"id":"c1","model":"gpt-4o","choices":[{"message":{"role":"assistant","content":"","reasoning_content":"先想一想再答"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	srv := newScripted(t, scriptedResp{body: body})
	a, err := simple.NewModelBuilder(agentModel(t, srv)).Name("think").Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	resp, err := a.Prompt("问").Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Content() != "先想一想再答" {
		t.Fatalf("纯思考应降级为正文，得到 %q", resp.Content())
	}
	if resp.Message().Thinking != "" {
		t.Fatalf("降级后不应保留思考字段: %+v", resp.Message())
	}
}

func TestSimpleToolCallNotArchived(t *testing.T) {
	body := `{"id":"c1","model":"gpt-4o","choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","index":0,"function":{"name":"get_weather","arguments":"{\"city\":\"北京\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	srv := newScripted(t, scriptedResp{body: body})
	a, err := simple.NewModelBuilder(agentModel(t, srv)).Name("tooluser").Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	resp, err := a.Prompt("北京天气").Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if len(resp.Message().ToolCalls) != 1 || resp.Message().ToolCalls[0].Name != "get_weather" {
		t.Fatalf("ToolCalls = %+v", resp.Message().ToolCalls)
	}
	// 带工具调用的回答不归档（等待工具结果回填），会话只有 user 提问
	if msgs := resp.Session().Messages(); len(msgs) != 1 || msgs[0].Role != ai.RoleUser {
		t.Fatalf("会话消息 = %+v", msgs)
	}
}

func TestSimpleOutputKeyWrittenToSessionContext(t *testing.T) {
	srv := newScripted(t, scriptedResp{body: chatJSON("答案在此")})
	a, err := simple.NewModelBuilder(agentModel(t, srv)).
		Name("ok").OutputKey("answer").Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	resp, err := a.Prompt("问").Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got := resp.Context().Get("answer"); got != "答案在此" {
		t.Fatalf("OutputKey 写回 = %v", got)
	}
}

func TestSimpleTeamMetricsDeltaAndNoArchive(t *testing.T) {
	srv := newScripted(t, scriptedResp{body: chatJSON("团队作答")})
	a, err := simple.NewModelBuilder(agentModel(t, srv)).Name("t1").Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	trace := &teamTraceStub{runID: "team-run", metrics: &agent.Metrics{}}
	ctx := flow.NewContext("g")
	ctx.Put(agent.KeyCurrentTeamTraceKey, "team")
	ctx.Put("team", agent.TeamTrace(trace))
	sess := agent.NewMemorySessionOfContext(ctx)

	resp, err := a.Prompt("团队问题").Session(sess).Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Content() != "团队作答" || srv.count() != 1 {
		t.Fatalf("content=%q requests=%d", resp.Content(), srv.count())
	}
	// 团队场景：用量增量汇总到父轨迹（Metrics().AddMetrics 只汇总本次执行增量）
	if trace.metrics.PromptTokens() != 3 || trace.metrics.TotalTokens() != 5 {
		t.Fatalf("父轨迹用量 = prompt %d / total %d",
			trace.metrics.PromptTokens(), trace.metrics.TotalTokens())
	}
	// 团队场景不归档会话（由协议 AddRecord 记账）
	if msgs := sess.Messages(); len(msgs) != 0 {
		t.Fatalf("团队场景会话不应归档: %+v", msgs)
	}
}

func TestSimpleAgentAsFlowNode(t *testing.T) {
	srv := newScripted(t, scriptedResp{body: chatJSON("图内回答")})
	a, err := simple.NewModelBuilder(agentModel(t, srv)).
		Name("flowagent").OutputKey("answer").Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// 会话预绑定图 ctx（_SESSION_ 装载复用），先直调一次建立轨迹提问
	gctx := flow.NewContext("g")
	sess := agent.NewMemorySessionOfContext(gctx)
	if _, err := a.Prompt("图的问题").Session(sess).Call(); err != nil {
		t.Fatalf("预置调用: %v", err)
	}

	g, err := flow.Create("f", func(s *flow.GraphSpec) {
		s.AddStart("s").LinkAdd("flowagent")
		s.AddActivityNamed(a).LinkAdd("e")
		s.AddEnd("e")
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := flow.NewEngine().Eval(g, gctx); err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if srv.count() != 2 {
		t.Fatalf("图执行后请求数 = %d（轨迹应复用原始提问）", srv.count())
	}
	if got := gctx.Get("answer"); got != "图内回答" {
		t.Fatalf("图 ctx OutputKey = %v", got)
	}
}

func TestSimpleTraceResetAcrossRuns(t *testing.T) {
	// Handler 智能体（零 HTTP）观察轨迹状态机：每轮 reset 换 runId、指标清零
	a, err := simple.NewBuilder().Name("tr").Instruction("直答").
		Handler(func(p *ai.Prompt, s agent.Session) (*ai.AssistantMessage, error) {
			return &ai.AssistantMessage{Text: "答"}, nil
		}).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	sess := agent.NewMemorySession("tr")
	resp1, err := a.Prompt("一问").Session(sess).Call()
	if err != nil {
		t.Fatalf("Call 1: %v", err)
	}
	trace := resp1.Trace()
	id1 := trace.RunID()

	resp2, err := a.Prompt("二问").Session(sess).Call()
	if err != nil {
		t.Fatalf("Call 2: %v", err)
	}
	if resp2.Trace() != trace {
		t.Fatalf("同会话应复用同一轨迹实例")
	}
	if trace.RunID() == id1 {
		t.Fatalf("新一轮应换 runId")
	}
	if op := trace.OriginalPrompt(); op == nil || len(op.Messages()) != 1 {
		t.Fatalf("OriginalPrompt 未更新: %v", op)
	}
}
