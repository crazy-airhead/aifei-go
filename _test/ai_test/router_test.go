// router_test.go P1-c 路由器测试：显式 attr 路由 > 策略、RoundRobin 轮询序、
// smooth WRR 加权分布（精确统计）、拓扑冻结、规则首命中、Smart（假分类模型决策）、
// 构造校验。
package ai_test

import (
	"strconv"
	"strings"
	"testing"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/router"
)

// routerFixture 三路由共挂一个假服务，以请求体 model 字段区分被选中的路由。
type routerFixture struct {
	f     *fakeOpenAI
	fast  *ai.ChatModel
	smart *ai.ChatModel
	big   *ai.ChatModel
}

func newRouterFixture(t *testing.T, bodies ...string) *routerFixture {
	t.Helper()
	f := newFakeOpenAI(t, bodies...)
	mk := func(model string) *ai.ChatModel {
		m, err := ai.NewChatModel(ai.Config{APIURL: f.srv.URL, Model: model})
		if err != nil {
			t.Fatalf("NewChatModel(%s): %v", model, err)
		}
		return m
	}
	return &routerFixture{f: f, fast: mk("m-fast"), smart: mk("m-smart"), big: mk("m-big")}
}

func (fx *routerFixture) routes(t *testing.T) []router.Route {
	t.Helper()
	return []router.Route{mustRoute(t, "fast", "快速小模型，短问题", 1, fx.fast),
		mustRoute(t, "smart", "推理型模型，复杂问题", 1, fx.smart),
		mustRoute(t, "big", "大上下文模型，长文档", 1, fx.big)}
}

func mustRoute(t *testing.T, id, desc string, weight int, m *ai.ChatModel) router.Route {
	t.Helper()
	r, err := router.NewRoute(id, desc, weight, m)
	if err != nil {
		t.Fatalf("NewRoute(%s): %v", id, err)
	}
	return r
}

// servedModelOf 第 i 次请求实际命中的模型（请求体 model 字段）。
func (fx *routerFixture) servedModel(t *testing.T, i int) string {
	t.Helper()
	m, _ := fx.f.request(t, i).body["model"].(string)
	return m
}

// routeCall 路由 + 调用一步到位（Router.Prompt 两段式：路由期错误在此浮出）。
func routeCall(t *testing.T, r *router.Router, p any) (*ai.Response, error) {
	t.Helper()
	desc, err := r.Prompt(p)
	if err != nil {
		return nil, err
	}
	return desc.Call()
}

// TestRouterExplicitAttrBeatsStrategy 显式标注优先：RoundRobin 首选本应是 fast，
// 标注 big 后直落 big（策略不参与——游标不动，下一次未标注请求仍取 fast）。
func TestRouterExplicitAttrBeatsStrategy(t *testing.T) {
	fx := newRouterFixture(t, respTextJSON, respTextJSON, respTextJSON)
	r, err := router.NewRouter(fx.routes(t), &router.RoundRobinStrategy{})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	prompt := ai.PromptOf("长文档分析")
	prompt.SetAttr(router.AttrRouteID, "big")
	resp, err := routeCall(t, r, prompt)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Text() == "" {
		t.Fatalf("resp.Text 为空")
	}
	if got := fx.servedModel(t, 0); got != "m-big" {
		t.Fatalf("显式路由命中 %q，应为 m-big", got)
	}

	// 未标注请求走策略（游标未被显式路由消耗，首轮仍取 fast）
	if _, err := routeCall(t, r, "普通问题"); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got := fx.servedModel(t, 1); got != "m-fast" {
		t.Fatalf("策略轮首选 %q，应为 m-fast（显式路由不得消耗轮询游标）", got)
	}
}

// TestRouterExplicitAttrInvalid 显式标注类型/取值非法与未知路由 id 均报错。
func TestRouterExplicitAttrInvalid(t *testing.T) {
	fx := newRouterFixture(t)
	r, err := router.NewRouter(fx.routes(t), &router.RoundRobinStrategy{})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	p := ai.PromptOf("x")
	p.SetAttr(router.AttrRouteID, 123) // 非字符串
	if _, err := routeCall(t, r, p); err == nil {
		t.Fatalf("非字符串标注应报错")
	}
	p2 := ai.PromptOf("x")
	p2.SetAttr(router.AttrRouteID, "  ") // 空白
	if _, err := routeCall(t, r, p2); err == nil {
		t.Fatalf("空白标注应报错")
	}
	p3 := ai.PromptOf("x")
	p3.SetAttr(router.AttrRouteID, "nope") // 未知 id
	if _, err := routeCall(t, r, p3); err == nil || !strings.Contains(err.Error(), "unknown route id") {
		t.Fatalf("未知路由 id 应报错，got %v", err)
	}
}

// TestRouterRoundRobin 轮询序确定性：3 路由 7 次 a,b,c,a,b,c,a。
func TestRouterRoundRobin(t *testing.T) {
	fx := newRouterFixture(t)
	routes := fx.routes(t)
	s := &router.RoundRobinStrategy{}
	ctx, err := router.NewContext(ai.PromptOf("x"))
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	var got []string
	for i := 0; i < 7; i++ {
		d, err := s.Select(ctx, routes)
		if err != nil {
			t.Fatalf("Select: %v", err)
		}
		got = append(got, d.RouteID)
		if d.Reasoning != "round-robin" {
			t.Fatalf("reasoning = %q", d.Reasoning)
		}
	}
	want := []string{"fast", "smart", "big", "fast", "smart", "big", "fast"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("轮询序 = %v，want %v", got, want)
	}
}

// TestRouterWeightedDistribution smooth WRR 精确分布：权重 5:1:1 的 7 次选择
// 恰为一轮（fast×5 + smart + big），70 次即 50/10/10（确定性算法无需容差）。
func TestRouterWeightedDistribution(t *testing.T) {
	fx := newRouterFixture(t)
	routes := []router.Route{
		mustRoute(t, "fast", "快", 5, fx.fast),
		mustRoute(t, "smart", "智", 1, fx.smart),
		mustRoute(t, "big", "大", 1, fx.big),
	}
	s := &router.WeightedRoundRobinStrategy{}
	ctx, _ := router.NewContext(ai.PromptOf("x"))

	counts := map[string]int{}
	for i := 0; i < 70; i++ {
		d, err := s.Select(ctx, routes)
		if err != nil {
			t.Fatalf("Select: %v", err)
		}
		counts[d.RouteID]++
	}
	if counts["fast"] != 50 || counts["smart"] != 10 || counts["big"] != 10 {
		t.Fatalf("加权分布 = %v，want fast:50 smart:10 big:10", counts)
	}

	// 首轮 7 次内部也应平滑交错（5:1:1 的 smooth 序列，不会 5 连 fast 后 2 个）
	s2 := &router.WeightedRoundRobinStrategy{}
	var seq []string
	for i := 0; i < 7; i++ {
		d, _ := s2.Select(ctx, routes)
		seq = append(seq, d.RouteID)
	}
	joined := strings.Join(seq, ",")
	if joined == "fast,fast,fast,fast,fast,smart,big" {
		t.Fatalf("非平滑序列：%s", joined)
	}
	if d, _ := s2.Select(ctx, routes); d.Reasoning != "smooth weighted round-robin" {
		t.Fatalf("reasoning = %q", d.Reasoning)
	}
}

// TestRouterWeightedTopologyFrozen 拓扑冻结：首次使用后改路由表报错。
func TestRouterWeightedTopologyFrozen(t *testing.T) {
	fx := newRouterFixture(t)
	routes := fx.routes(t)
	s := &router.WeightedRoundRobinStrategy{}
	ctx, _ := router.NewContext(ai.PromptOf("x"))
	if _, err := s.Select(ctx, routes); err != nil {
		t.Fatalf("Select: %v", err)
	}
	// 同 id 改权重 → 冻结违规
	changed := []router.Route{
		mustRoute(t, "fast", "快", 9, fx.fast),
		mustRoute(t, "smart", "智", 1, fx.smart),
		mustRoute(t, "big", "大", 1, fx.big),
	}
	if _, err := s.Select(ctx, changed); err == nil || !strings.Contains(err.Error(), "cannot change after first use") {
		t.Fatalf("改权重应报错，got %v", err)
	}
	// 换 id 集合 → 同样违规
	other := []router.Route{
		mustRoute(t, "fast", "快", 1, fx.fast),
		mustRoute(t, "smart", "智", 1, fx.smart),
	}
	if _, err := s.Select(ctx, other); err == nil {
		t.Fatalf("改路由集合应报错")
	}
}

// TestRouterRuleBased 规则首命中 + 全不命中报错 + 策略入参形态。
func TestRouterRuleBased(t *testing.T) {
	fx := newRouterFixture(t)
	routes := fx.routes(t)
	rules := []router.Rule{
		mustRule(t, "big", func(ctx *router.Context) bool {
			last := ctx.Prompt().LastMessage()
			if last == nil {
				return false
			}
			s, _ := last.Content.(string)
			return strings.Contains(s, "文档")
		}),
		mustRule(t, "fast", func(ctx *router.Context) bool { return true }), // 兜底规则
	}
	s, err := router.NewRuleBasedStrategy(rules)
	if err != nil {
		t.Fatalf("NewRuleBasedStrategy: %v", err)
	}
	ctx, _ := router.NewContext(ai.PromptOf("帮我总结文档"))

	d, err := s.Select(ctx, routes)
	if err != nil || d.RouteID != "big" {
		t.Fatalf("规则路由 = %v, %v，应为 big", d, err)
	}
	if d.Reasoning != "matched rule: big" {
		t.Fatalf("reasoning = %q", d.Reasoning)
	}

	// 策略不得改动原始提示（上下文持副本）
	if msgs := ctx.Prompt().Messages(); msgs[0].Role != ai.RoleUser {
		t.Fatalf("上下文消息被改动：%v", msgs)
	}

	d2, err := s.Select(mustContext(t, ai.PromptOf("随便问点啥")), routes)
	if err != nil || d2.RouteID != "fast" {
		t.Fatalf("兜底规则 = %v, %v，应为 fast", d2, err)
	}

	// 无兜底规则时全不命中报错
	s2, _ := router.NewRuleBasedStrategy([]router.Rule{
		mustRule(t, "big", func(*router.Context) bool { return false }),
	})
	if _, err := s2.Select(mustContext(t, ai.PromptOf("x")), routes); err == nil ||
		!strings.Contains(err.Error(), "no routing rule matched") {
		t.Fatalf("全不命中应报错，got %v", err)
	}
}

func mustRule(t *testing.T, id string, predicate func(*router.Context) bool) router.Rule {
	t.Helper()
	r, err := router.NewRule(id, predicate)
	if err != nil {
		t.Fatalf("NewRule(%s): %v", id, err)
	}
	return r
}

func mustContext(t *testing.T, p *ai.Prompt) *router.Context {
	t.Helper()
	c, err := router.NewContext(p)
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	return c
}

// TestRouterSmart 假分类模型决策：分类请求带候选清单与 outputSchema，
// 决策命中后落到目标路由；未知 routeId / 空 reasoning 报错。
func TestRouterSmart(t *testing.T) {
	decisionJSON := `{"routeId":"smart","reasoning":"问题需要推理"}`
	fx := newRouterFixture(t,
		`{"id":"c-cls","model":"m-cls","choices":[{"index":0,"message":{"role":"assistant","content":`+q(decisionJSON)+`},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`,
		respTextJSON,
	)
	classifier, err := ai.NewChatModel(ai.Config{APIURL: fx.f.srv.URL, Model: "m-cls"})
	if err != nil {
		t.Fatalf("NewChatModel: %v", err)
	}
	s, err := router.NewSmartStrategy(classifier)
	if err != nil {
		t.Fatalf("NewSmartStrategy: %v", err)
	}
	r, err := router.NewRouter(fx.routes(t), s)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	resp, err := routeCall(t, r, "证明哥德巴赫猜想")
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Text() == "" {
		t.Fatalf("resp.Text 为空")
	}
	// 第 0 次请求 = 分类器调用：候选清单 + schema 指令
	clsReq := fx.f.request(t, 0)
	msgs := fx.f.messagesOf(t, 0)
	var userText string
	for _, m := range msgs {
		if m["role"] == "user" {
			userText, _ = m["content"].(string)
		}
	}
	if !strings.Contains(userText, "Candidate routes:") ||
		!strings.Contains(userText, "- smart: 推理型模型，复杂问题") ||
		!strings.Contains(userText, "user: 证明哥德巴赫猜想") {
		t.Fatalf("分类器提示 = %q", userText)
	}
	sysText := systemTextOf(t, msgs)
	if !strings.Contains(sysText, "output_schema") { // 结构化输出指令进了系统提示
		t.Fatalf("分类器系统提示缺 schema 指令：%q", sysText)
	}
	if clsReq.body["model"] != "m-cls" {
		t.Fatalf("分类器 model = %v", clsReq.body["model"])
	}
	// 第 1 次请求 = 决策命中的目标模型
	if got := fx.servedModel(t, 1); got != "m-smart" {
		t.Fatalf("Smart 决策命中 %q，应为 m-smart", got)
	}
}

// TestRouterSmartInvalidDecision 决策校验：未知 routeId、空 reasoning。
func TestRouterSmartInvalidDecision(t *testing.T) {
	clsResp := func(content string) string {
		return `{"id":"c","model":"m-cls","choices":[{"index":0,"message":{"role":"assistant","content":` + q(content) + `},"finish_reason":"stop"}]}`
	}
	cases := []struct {
		name string
		body string
		want string
	}{
		{"未知 routeId", clsResp(`{"routeId":"nope","reasoning":"x"}`), "unknown classifier route id"},
		{"空 reasoning", clsResp(`{"routeId":"fast","reasoning":""}`), "classifier reasoning is required"},
		{"空 routeId", clsResp(`{"routeId":"","reasoning":"x"}`), "classifier routeId is required"},
		{"非 JSON 输出", clsResp(`我认为选快模型`), "classification failed"},
	}
	for _, c := range cases {
		fx := newRouterFixture(t, c.body)
		classifier, _ := ai.NewChatModel(ai.Config{APIURL: fx.f.srv.URL, Model: "m-cls"})
		s, _ := router.NewSmartStrategy(classifier)
		r, err := router.NewRouter(fx.routes(t), s)
		if err != nil {
			t.Fatalf("NewRouter: %v", err)
		}
		_, err = routeCall(t, r, "问题")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s：应报 %q，got %v", c.name, c.want, err)
		}
		if fx.f.count() != 1 { // 决策失败不得触发目标模型调用
			t.Fatalf("%s：目标模型被调用 %d 次", c.name, fx.f.count()-1)
		}
	}
}

// TestRouterConstructionValidation 构造校验：空路由/空策略/重复 id/非法路由。
func TestRouterConstructionValidation(t *testing.T) {
	fx := newRouterFixture(t)
	if _, err := router.NewRouter(nil, &router.RoundRobinStrategy{}); err == nil {
		t.Fatalf("空路由应报错")
	}
	if _, err := router.NewRouter(fx.routes(t), nil); err == nil {
		t.Fatalf("空策略应报错")
	}
	dup := append(fx.routes(t), mustRoute(t, "fast", "重复", 1, fx.fast))
	if _, err := router.NewRouter(dup, &router.RoundRobinStrategy{}); err == nil ||
		!strings.Contains(err.Error(), "duplicate route id") {
		t.Fatalf("重复 id 应报错，got %v", err)
	}
	if _, err := router.NewRouter([]router.Route{{}}, &router.RoundRobinStrategy{}); err == nil {
		t.Fatalf("零值路由应报错")
	}
	if _, err := router.NewRoute(" ", "d", 1, fx.fast); err == nil {
		t.Fatalf("空 id 应报错")
	}
	if _, err := router.NewRoute("a", " ", 1, fx.fast); err == nil {
		t.Fatalf("空描述应报错")
	}
	if _, err := router.NewRoute("a", "d", 0, fx.fast); err == nil {
		t.Fatalf("非正权重应报错")
	}
	if _, err := router.NewRoute("a", "d", 1, nil); err == nil {
		t.Fatalf("空模型应报错")
	}
}

// q JSON 字符串字面量包装。
func q(s string) string { return strconv.Quote(s) }

// systemTextOf messages 中 system 角色的文本。
func systemTextOf(t *testing.T, msgs []map[string]any) string {
	t.Helper()
	for _, m := range msgs {
		if m["role"] == "system" {
			s, _ := m["content"].(string)
			return s
		}
	}
	return ""
}
