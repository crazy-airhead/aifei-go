package ai_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	ai "github.com/crazy-airhead/aifei-go/ai"
)

// ---- 假 OpenAI 服务：顺序回放固定响应体，记录每次请求 ----

type capturedRequest struct {
	body   map[string]any
	header http.Header
	url    string
}

type fakeOpenAI struct {
	mu       sync.Mutex
	requests []capturedRequest
	srv      *httptest.Server
}

func newFakeOpenAI(t *testing.T, bodies ...string) *fakeOpenAI {
	t.Helper()
	f := &fakeOpenAI{}
	var idx int
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		i := idx
		idx++
		f.requests = append(f.requests, capturedRequest{body: body, header: r.Header.Clone(), url: r.URL.String()})
		f.mu.Unlock()

		if i >= len(bodies) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"no more scripted responses"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(bodies[i]))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeOpenAI) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fakeOpenAI) request(t *testing.T, i int) capturedRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.requests) {
		t.Fatalf("request %d 未捕获（共 %d 次）", i, len(f.requests))
	}
	return f.requests[i]
}

// messagesOf 第 i 次请求的 messages 节点。
func (f *fakeOpenAI) messagesOf(t *testing.T, i int) []map[string]any {
	t.Helper()
	list, _ := f.request(t, i).body["messages"].([]any)
	out := make([]map[string]any, 0, len(list))
	for _, v := range list {
		m, _ := v.(map[string]any)
		out = append(out, m)
	}
	return out
}

func fakeModel(t *testing.T, f *fakeOpenAI) *ai.ChatModel {
	t.Helper()
	m, err := ai.NewChatModel(ai.Config{APIURL: f.srv.URL, Model: "gpt-4o", APIKey: "sk-test"})
	if err != nil {
		t.Fatalf("NewChatModel: %v", err)
	}
	return m
}

const (
	respTextJSON = `{"id":"c-text","model":"gpt-4o",
	 "choices":[{"index":0,"message":{"role":"assistant","content":"好的，简答。"},"finish_reason":"stop"}],
	 "usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`

	respToolCallJSON = `{"id":"c-tool","model":"gpt-4o",
	 "choices":[{"index":0,"message":{"role":"assistant","content":null,
	   "tool_calls":[{"id":"call_1","type":"function","index":0,
	     "function":{"name":"query-order","arguments":"{\"orderId\":\"A1\"}"}}]},"finish_reason":"tool_calls"}],
	 "usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`

	respFinalJSON = `{"id":"c-final","model":"gpt-4o",
	 "choices":[{"index":0,"message":{"role":"assistant","content":"A1 已发货"},"finish_reason":"stop"}],
	 "usage":{"prompt_tokens":20,"completion_tokens":8,"total_tokens":28}}`

	respNowTimeJSON = `{"id":"c-now","model":"gpt-4o",
	 "choices":[{"index":0,"message":{"role":"assistant","content":null,
	   "tool_calls":[{"id":"call_9","type":"function","index":0,
	     "function":{"name":"now-time","arguments":"{}"}}]},"finish_reason":"tool_calls"}],
	 "usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`
)

func idxOf(t *testing.T, s, sub string) int {
	t.Helper()
	i := strings.Index(s, sub)
	if i < 0 {
		t.Fatalf("未找到 %q 于:\n%s", sub, s)
	}
	return i
}

// ---- ChatModel 构建 ----

func TestChatModelBuildErrors(t *testing.T) {
	if _, err := ai.NewChatModel(ai.Config{Model: "m"}); err == nil || !strings.Contains(err.Error(), "apiUrl") {
		t.Fatalf("缺 apiURL 应报错，got %v", err)
	}
	if _, err := ai.NewChatModel(ai.Config{APIURL: "http://x"}); err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("缺 model 应报错，got %v", err)
	}
}

func TestChatModelBuilder(t *testing.T) {
	m, err := ai.NewChatModelURL("http://localhost:8000/v1/chat/completions").
		APIKey("k").Model("gpt-x").Name("生产模型").Timeout(30).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if m.Config().Model != "gpt-x" || m.Config().APIKey != "k" ||
		m.Config().Name != "生产模型" || m.Config().Timeout != 30*time.Second {
		t.Fatalf("config = %+v", m.Config())
	}
	if m.Dialect().Name() != "openai-completions" {
		t.Fatalf("方言 = %q", m.Dialect().Name())
	}
}

// ---- 基本调用 ----

func TestChatModelCallBasic(t *testing.T) {
	f := newFakeOpenAI(t, respTextJSON)
	m := fakeModel(t, f)

	res, err := m.Prompt("你好").Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if res.Text() != "好的，简答。" || res.FinishReason() != "stop" {
		t.Fatalf("res = %q / %q", res.Text(), res.FinishReason())
	}
	if u := res.Usage(); u == nil || u.TotalTokens != 15 {
		t.Fatalf("usage = %+v", u)
	}

	if f.count() != 1 {
		t.Fatalf("请求数 = %d", f.count())
	}
	req := f.request(t, 0)
	if req.body["model"] != "gpt-4o" {
		t.Fatalf("model = %v", req.body["model"])
	}
	if !strings.HasSuffix(req.url, "/chat/completions") {
		t.Fatalf("url = %q", req.url)
	}
	if got := req.header.Get("Authorization"); got != "Bearer sk-test" {
		t.Fatalf("Authorization = %q", got)
	}

	msgs := f.messagesOf(t, 0)
	if len(msgs) != 1 || msgs[0]["role"] != "user" || msgs[0]["content"] != "你好" {
		t.Fatalf("messages = %v", msgs)
	}
}

func TestChatModelPromptForms(t *testing.T) {
	m := fakeModel(t, newFakeOpenAI(t, respTextJSON))

	// 各形态构造不 panic；字符串/*Message/*[]Message/*Prompt/nil 均可
	_ = m.Prompt("hi")
	_ = m.Prompt(ai.OfUser("hi"))
	_ = m.Prompt([]*ai.Message{ai.OfUser("hi")})
	_ = m.Prompt(ai.PromptOf("hi"))
	_ = m.Prompt(nil)

	// 非法形态 panic
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("非法 prompt 形态应 panic")
			}
		}()
		_ = m.Prompt(42)
	}()
}

// ---- 系统提示组装序 ----

type wikiTalent struct {
	ai.TalentBase
}

func (w *wikiTalent) Name() string        { return "wiki" }
func (w *wikiTalent) Description() string { return "知识库检索" }
func (w *wikiTalent) Instruction(*ai.Prompt) string {
	return "回答前先检索知识库。"
}
func (w *wikiTalent) Tools(*ai.Prompt) []ai.FunctionTool {
	return []ai.FunctionTool{ai.NewTool("wiki-search").Desc("检索知识库").
		StringParam("query", "关键词", true).
		Handler(func(ctx context.Context, args map[string]any) (any, error) {
			return "wiki result", nil
		}).Build()}
}

type appendInterceptor struct {
	ai.InterceptorBase
	marker string
}

func (a *appendInterceptor) OnPrepare(_ ai.Session, _ *ai.ChatOptions, _ *ai.Prompt, system *strings.Builder) {
	system.WriteString("\n## " + a.marker + "\n")
}

func TestCallSystemPromptAssemblyOrder(t *testing.T) {
	f := newFakeOpenAI(t, respTextJSON)
	m := fakeModel(t, f)

	schema := `{"type":"object","properties":{"total":{"type":"integer"}}}`
	res, err := m.Prompt("一共几单").
		Role("订单客服").
		Instruction("只回答订单相关问题").
		Options(func(o *ai.ChatOptions) {
			o.OutputSchema = schema
			o.Talents = []ai.Talent{&wikiTalent{}}
			o.Interceptors = []ai.Interceptor{&appendInterceptor{marker: "审计拦截器"}}
		}).
		Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	_ = res

	msgs := f.messagesOf(t, 0)
	if len(msgs) != 2 || msgs[0]["role"] != "system" {
		t.Fatalf("messages = %v", msgs)
	}
	sys, _ := msgs[0]["content"].(string)

	// 组装序：角色 → 执行指令 → schema 指令 → 拦截器追加 → talent 指令块
	roleAt := idxOf(t, sys, "## 你的角色\n订单客服")
	instrAt := idxOf(t, sys, "## 执行指令\n只回答订单相关问题")
	schemaAt := idxOf(t, sys, "## [IMPORTANT: OUTPUT FORMAT]")
	schemaBodyAt := idxOf(t, sys, "<output_schema>\n"+schema+"\n</output_schema>")
	icAt := idxOf(t, sys, "## 审计拦截器")
	talentAt := idxOf(t, sys, "# [Talent: wiki] - 知识库检索")
	unless := idxOf(t, sys, "工具作用域")
	if !(roleAt < instrAt && instrAt < schemaAt && schemaAt < schemaBodyAt &&
		schemaBodyAt < icAt && icAt < talentAt && talentAt < unless) {
		t.Fatalf("系统提示组装序错误:\n%s", sys)
	}
	if !strings.Contains(sys, "<Talent:wiki>") || !strings.Contains(sys, "</Talent:wiki>") {
		t.Fatalf("talent 包裹标签缺失:\n%s", sys)
	}
	if !strings.Contains(sys, "`wiki-search`") {
		t.Fatalf("talent 工具作用域未列出工具:\n%s", sys)
	}

	// schema 顺带 response_format 选项
	body := f.request(t, 0).body
	rf, _ := body["response_format"].(map[string]any)
	if rf["type"] != "json_object" {
		t.Fatalf("response_format = %v", body["response_format"])
	}

	// talent 工具已部署且 meta 染色进描述前缀
	tools, _ := body["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %v", body["tools"])
	}
	fn, _ := tools[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "wiki-search" {
		t.Fatalf("tool.name = %v", fn["name"])
	}
	if desc, _ := fn["description"].(string); !strings.HasPrefix(desc, "[talent:wiki] 检索知识库") {
		t.Fatalf("tool.description = %q（应含 talent meta 前缀）", desc)
	}
}

func TestCallSystemPromptPriority(t *testing.T) {
	f := newFakeOpenAI(t, respTextJSON)
	m := fakeModel(t, f)

	_, err := m.Prompt("hi").
		SystemPrompt("你是直接给定的系统提示。").
		Role("应被忽略的角色").
		Instruction("应被忽略的指令").
		Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	msgs := f.messagesOf(t, 0)
	sys, _ := msgs[0]["content"].(string)
	if !strings.HasPrefix(sys, "你是直接给定的系统提示。") {
		t.Fatalf("SystemPrompt 应优先: %q", sys)
	}
	if strings.Contains(sys, "你的角色") || strings.Contains(sys, "执行指令") {
		t.Fatalf("SystemPrompt 存在时不应再组装 Role/Instruction: %q", sys)
	}
}

func TestCallConfigDefaultOptions(t *testing.T) {
	f := newFakeOpenAI(t, respTextJSON)
	tool := ai.NewTool("noop").Desc("空").
		Handler(func(ctx context.Context, args map[string]any) (any, error) { return "", nil }).Build()
	m, err := ai.NewChatModel(ai.Config{
		APIURL: f.srv.URL, Model: "gpt-4o",
		DefaultOptions: func(o *ai.ChatOptions) {
			o.Role = "默认角色"
			o.Tools = []ai.FunctionTool{tool}
		},
	})
	if err != nil {
		t.Fatalf("NewChatModel: %v", err)
	}

	if _, err := m.Prompt("hi").Call(); err != nil {
		t.Fatalf("Call: %v", err)
	}
	msgs := f.messagesOf(t, 0)
	sys, _ := msgs[0]["content"].(string)
	if !strings.Contains(sys, "默认角色") {
		t.Fatalf("DefaultOptions 未生效: %q", sys)
	}
	if _, ok := f.request(t, 0).body["tools"]; !ok {
		t.Fatal("DefaultOptions 注册的工具应进请求")
	}
}

// ---- autoToolCall 递归（两轮） ----

func autoToolOn() *bool {
	b := true
	return &b
}

func TestAutoToolCallRecursion(t *testing.T) {
	f := newFakeOpenAI(t, respToolCallJSON, respFinalJSON)
	m := fakeModel(t, f)

	var gotArgs map[string]any
	var gotToolCtx map[string]any
	tool := ai.NewTool("query-order").Desc("查订单").
		StringParam("orderId", "订单号", true).
		Handler(func(ctx context.Context, args map[string]any) (any, error) {
			gotArgs = args
			gotToolCtx = ai.ToolContextOf(ctx)
			return "A1 已发货", nil
		}).Build()

	sess := ai.NewMemorySession("s1")
	res, err := m.Prompt("查一下订单 A1").Session(sess).
		Options(func(o *ai.ChatOptions) {
			o.AutoToolCall = autoToolOn()
			o.Tools = []ai.FunctionTool{tool}
		}).
		Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	// 工具收到了方言解析后的参数与会话 id（经 toolContext）
	if gotArgs["orderId"] != "A1" {
		t.Fatalf("args = %v", gotArgs)
	}
	if gotToolCtx == nil || gotToolCtx["sessionId"] != "s1" {
		t.Fatalf("toolContext = %v", gotToolCtx)
	}

	// 两轮请求
	if f.count() != 2 {
		t.Fatalf("请求数 = %d（应两轮：tool_calls → 终答）", f.count())
	}
	// 第二轮请求 = 会话历史回放：user → assistant(tool_calls) → tool
	msgs := f.messagesOf(t, 1)
	if len(msgs) != 3 {
		t.Fatalf("第二轮 messages = %v", msgs)
	}
	if msgs[0]["role"] != "user" || msgs[1]["role"] != "assistant" || msgs[2]["role"] != "tool" {
		t.Fatalf("第二轮 roles = %v", msgs)
	}
	tc, _ := msgs[1]["tool_calls"].([]any)
	tcm, _ := tc[0].(map[string]any)
	if tcm["id"] != "call_1" {
		t.Fatalf("assistant 节点 tool_calls = %v", tc)
	}
	tfn, _ := tcm["function"].(map[string]any)
	if tfn["name"] != "query-order" || tfn["arguments"] != `{"orderId":"A1"}` {
		t.Fatalf("function = %v", tfn)
	}
	if msgs[2]["tool_call_id"] != "call_1" || msgs[2]["name"] != "query-order" ||
		msgs[2]["content"] != "A1 已发货" {
		t.Fatalf("tool 节点 = %v", msgs[2])
	}

	// 最终响应取末轮
	if res.Text() != "A1 已发货" || res.FinishReason() != "stop" {
		t.Fatalf("res = %q / %q", res.Text(), res.FinishReason())
	}
	if u := res.Usage(); u == nil || u.TotalTokens != 28 {
		t.Fatalf("usage = %+v", u)
	}

	// 会话消息表：user → assistant(tool_calls) → tool → assistant(终答)
	hist := sess.Messages()
	if len(hist) != 4 {
		t.Fatalf("会话消息 = %d 条: %v", len(hist), hist)
	}
	if hist[0].Role != ai.RoleUser || hist[3].Role != ai.RoleAssistant ||
		hist[3].Content.(*ai.AssistantMessage).Text != "A1 已发货" {
		t.Fatalf("会话首末消息 = %v / %v", hist[0], hist[3])
	}
	am, _ := hist[1].Content.(*ai.AssistantMessage)
	if len(am.ToolCalls) != 1 || am.ToolCalls[0].ID != "call_1" {
		t.Fatalf("assistant(tool_calls) = %+v", am)
	}
	tm, _ := hist[2].Content.(*ai.ToolMessage)
	if tm == nil || tm.Name != "query-order" || tm.CallID != "call_1" || tm.ResultText() != "A1 已发货" {
		t.Fatalf("tool 消息 = %+v", tm)
	}

	// 事件全轨迹：RESPONSE_START → (STEP_START/STEP_END)×2 → RESPONSE_END
	assertFullTrace(t, res,
		[]ai.EventType{ai.EventResponseStart, ai.EventStepStart, ai.EventStepEnd,
			ai.EventStepStart, ai.EventStepEnd, ai.EventResponseEnd},
		[]int{0, 0, 1, 1})
}

// assertFullTrace 校验 call 路径的 lifecycle/step 事件序列、step 编号，
// 以及 RESPONSE_END 携带最终聚合。
func assertFullTrace(t *testing.T, res *ai.Response, wantTypes []ai.EventType, wantSteps []int) {
	t.Helper()
	var types []ai.EventType
	var steps []int
	var end *ai.ChatEvent
	var stepEnds []*ai.ChatEvent
	for _, ev := range res.Events() {
		switch ev.Type {
		case ai.EventResponseStart, ai.EventResponseEnd, ai.EventStepStart, ai.EventStepEnd:
			types = append(types, ev.Type)
			steps = append(steps, ev.Step)
			if ev.Type == ai.EventResponseEnd {
				end = ev
			}
			if ev.Type == ai.EventStepEnd {
				stepEnds = append(stepEnds, ev)
			}
		}
	}
	if len(types) != len(wantTypes) {
		t.Fatalf("lifecycle/step 轨迹 = %v（len=%d，应为 %v）", types, len(types), wantTypes)
	}
	for i := range wantTypes {
		if types[i] != wantTypes[i] {
			t.Fatalf("轨迹[%d] = %v，应为 %v", i, types, wantTypes)
		}
	}
	for i, want := range wantSteps {
		if steps[i+1] != want { // 跳过 RESPONSE_START（step=0 占位）
			t.Fatalf("step 编号 = %v，应为 %v", steps[1:], wantSteps)
		}
	}
	if end == nil || end.Response() == nil || end.Response() != res {
		t.Fatalf("RESPONSE_END 应携带最终响应")
	}
	if len(stepEnds) != len(wantSteps)/2 {
		t.Fatalf("STEP_END 数 = %d", len(stepEnds))
	}
}

// ---- returnDirect 单轮合成 ----

func TestAutoToolCallReturnDirect(t *testing.T) {
	f := newFakeOpenAI(t, respNowTimeJSON)
	m := fakeModel(t, f)

	tool := ai.NewTool("now-time").Desc("当前时间").ReturnDirect(true).
		Handler(func(ctx context.Context, args map[string]any) (any, error) {
			return "12:00", nil
		}).Build()

	sess := ai.NewMemorySession()
	res, err := m.Prompt("现在几点").Session(sess).
		Options(func(o *ai.ChatOptions) {
			o.AutoToolCall = autoToolOn()
			o.Tools = []ai.FunctionTool{tool}
		}).
		Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	if f.count() != 1 {
		t.Fatalf("returnDirect 不应递归，请求数 = %d", f.count())
	}
	if res.Text() != "12:00" {
		t.Fatalf("text = %q（应为工具结果合成）", res.Text())
	}
	if res.FinishReason() != "tool" {
		t.Fatalf("finishReason = %q", res.FinishReason())
	}
	if reason, _ := res.Message().Metadata["reason"].(string); reason != "tool" {
		t.Fatalf("metadata.reason = %v", res.Message().Metadata["reason"])
	}
	if u := res.Usage(); u == nil || u.TotalTokens != 3 {
		t.Fatalf("usage = %+v", u)
	}

	// 会话补了合成终答
	hist := sess.Messages()
	if len(hist) != 4 || hist[3].Content.(*ai.AssistantMessage).Text != "12:00" {
		t.Fatalf("会话消息 = %v", hist)
	}

	// STEP_END 快照为合成终态（returnDirect 轮）
	var stepEnd *ai.ChatEvent
	for _, ev := range res.Events() {
		if ev.Type == ai.EventStepEnd {
			stepEnd = ev
		}
	}
	if stepEnd == nil || stepEnd.Response() == nil || stepEnd.Response().Text() != "12:00" {
		t.Fatalf("STEP_END 快照 = %+v", stepEnd)
	}
}

// ---- 工具未注册 ----

func TestAutoToolCallToolNotFound(t *testing.T) {
	f := newFakeOpenAI(t, respToolCallJSON)
	m := fakeModel(t, f)

	sess := ai.NewMemorySession()
	_, err := m.Prompt("查 A1").Session(sess).
		Options(func(o *ai.ChatOptions) { o.AutoToolCall = autoToolOn() }).
		Call()
	if err == nil || !strings.Contains(err.Error(), "not found") || !strings.Contains(err.Error(), "query-order") {
		t.Fatalf("err = %v（应报 tool not found）", err)
	}
	if f.count() != 1 {
		t.Fatalf("请求数 = %d", f.count())
	}
	// assistant(tool_calls) 已入会话，tool 未入
	hist := sess.Messages()
	if len(hist) != 2 || hist[1].Role != ai.RoleAssistant {
		t.Fatalf("会话消息 = %v", hist)
	}
}

// ---- 调用期错误 ----

func TestCallHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	t.Cleanup(srv.Close)

	m, err := ai.NewChatModel(ai.Config{APIURL: srv.URL, Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("NewChatModel: %v", err)
	}
	res, err := m.Prompt("hi").Call()
	if err == nil {
		t.Fatal("HTTP 500 应报错")
	}
	if res != nil {
		t.Fatalf("失败时应无响应，got %+v", res)
	}
}

func TestCallUnrecognizableBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><body>gateway home</body></html>"))
	}))
	t.Cleanup(srv.Close)

	m, _ := ai.NewChatModel(ai.Config{APIURL: srv.URL, Model: "gpt-4o"})
	_, err := m.Prompt("hi").Call()
	if err == nil || !strings.Contains(err.Error(), "unrecognizable") {
		t.Fatalf("err = %v（应报 unrecognizable）", err)
	}
}

func TestCallEmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(srv.Close)

	m, _ := ai.NewChatModel(ai.Config{APIURL: srv.URL, Model: "gpt-4o"})
	_, err := m.Prompt("hi").Call()
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("err = %v（应报 empty）", err)
	}
}

// ---- 拦截器 ----

type orderedCallInterceptor struct {
	ai.InterceptorBase
	name  string
	order *[]string
}

func (a *orderedCallInterceptor) OnPrepare(_ ai.Session, _ *ai.ChatOptions, _ *ai.Prompt, _ *strings.Builder) {
	*a.order = append(*a.order, "prepare:"+a.name)
}

func (a *orderedCallInterceptor) InterceptCall(req *ai.CallRequest, chain *ai.CallChain) (*ai.Response, error) {
	*a.order = append(*a.order, "call:"+a.name)
	return chain.DoIntercept(req)
}

type disabledInterceptor struct {
	ai.InterceptorBase
}

func (d *disabledInterceptor) OnPrepare(_ ai.Session, _ *ai.ChatOptions, _ *ai.Prompt, system *strings.Builder) {
	system.WriteString("\n## 不应出现（已禁用）\n")
}

func (d *disabledInterceptor) IsEnabled() bool { return false }

func TestCallInterceptors(t *testing.T) {
	f := newFakeOpenAI(t, respTextJSON)
	m := fakeModel(t, f)

	var order []string
	_, err := m.Prompt("hi").
		Options(func(o *ai.ChatOptions) {
			o.Interceptors = []ai.Interceptor{
				&orderedCallInterceptor{name: "a", order: &order},
				&disabledInterceptor{},
				&orderedCallInterceptor{name: "b", order: &order},
			}
		}).
		Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	want := []string{"prepare:a", "prepare:b", "call:a", "call:b"}
	if strings.Join(order, "|") != strings.Join(want, "|") {
		t.Fatalf("拦截顺序 = %v，应为 %v", order, want)
	}
	sys, _ := f.messagesOf(t, 0)[0]["content"].(string)
	if strings.Contains(sys, "不应出现") {
		t.Fatalf("禁用拦截器不应生效: %q", sys)
	}
}

type toolAuditInterceptor struct {
	ai.InterceptorBase
	seen *[]string
}

func (a *toolAuditInterceptor) InterceptTool(req *ai.ToolRequest, chain *ai.ToolChain) *ai.ToolResult {
	*a.seen = append(*a.seen, "tool:"+fmtArgs(req.Args()))
	return chain.DoIntercept(req)
}

func fmtArgs(args map[string]any) string {
	b, _ := json.Marshal(args)
	return string(b)
}

func TestToolInterceptorInAutoToolCall(t *testing.T) {
	f := newFakeOpenAI(t, respToolCallJSON, respFinalJSON)
	m := fakeModel(t, f)

	tool := ai.NewTool("query-order").Desc("查订单").
		StringParam("orderId", "订单号", true).
		Handler(func(ctx context.Context, args map[string]any) (any, error) {
			return "A1 已发货", nil
		}).Build()

	var seen []string
	res, err := m.Prompt("查 A1").
		Options(func(o *ai.ChatOptions) {
			o.AutoToolCall = autoToolOn()
			o.Tools = []ai.FunctionTool{tool}
			o.Interceptors = []ai.Interceptor{&toolAuditInterceptor{seen: &seen}}
		}).
		Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if len(seen) != 1 || !strings.Contains(seen[0], `"orderId":"A1"`) {
		t.Fatalf("tool 拦截 = %v", seen)
	}
	if res.Text() != "A1 已发货" {
		t.Fatalf("拦截链直通后结果 = %q", res.Text())
	}
}

// ---- 事件过滤器 ----

func TestCallEventFilter(t *testing.T) {
	f := newFakeOpenAI(t, respToolCallJSON, respFinalJSON)
	m := fakeModel(t, f)

	tool := ai.NewTool("query-order").Desc("查订单").
		StringParam("orderId", "订单号", true).
		Handler(func(ctx context.Context, args map[string]any) (any, error) {
			return "A1 已发货", nil
		}).Build()

	res, err := m.Prompt("查 A1").
		EventFilter(ai.EventFilterFunc(func(ev *ai.ChatEvent) bool {
			return ev.Type != ai.EventStepEnd
		})).
		Options(func(o *ai.ChatOptions) {
			o.AutoToolCall = autoToolOn()
			o.Tools = []ai.FunctionTool{tool}
		}).
		Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	for _, ev := range res.Events() {
		if ev.Type == ai.EventStepEnd {
			t.Fatalf("STEP_END 应被过滤: %v", ev.Type)
		}
	}
	hasStart, hasEnd := false, false
	for _, ev := range res.Events() {
		if ev.Type == ai.EventResponseStart {
			hasStart = true
		}
		if ev.Type == ai.EventResponseEnd {
			hasEnd = true
		}
	}
	if !hasStart || !hasEnd {
		t.Fatalf("RESPONSE_START/END 不应被过滤，events = %d 条", len(res.Events()))
	}
}

// ---- HTTPCustomize ----

func TestCallHTTPCustomize(t *testing.T) {
	f := newFakeOpenAI(t, respTextJSON)
	m := fakeModel(t, f)

	_, err := m.Prompt("hi").
		Options(func(o *ai.ChatOptions) {
			o.HTTPCustomize = func(r *http.Request) {
				r.Header.Set("X-Audit", "1")
			}
		}).
		Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got := f.request(t, 0).header.Get("X-Audit"); got != "1" {
		t.Fatalf("X-Audit = %q", got)
	}
	// customize 包装不应丢失鉴权头
	if got := f.request(t, 0).header.Get("Authorization"); got != "Bearer sk-test" {
		t.Fatalf("Authorization = %q", got)
	}
}

// ---- MemorySession ----

func TestMemorySessionBasics(t *testing.T) {
	s := ai.NewMemorySession("s1")
	if s.SessionID() != "s1" {
		t.Fatalf("id = %q", s.SessionID())
	}
	if id := ai.NewMemorySession().SessionID(); id == "" || len(id) != 32 {
		t.Fatalf("缺省 id 应为 32 位随机十六进制，got %q", id)
	}
	if !s.IsEmpty() {
		t.Fatal("新会话应为空")
	}
	s.Attrs()["tenant"] = "t1"
	if s.Attrs()["tenant"] != "t1" {
		t.Fatalf("attrs = %v", s.Attrs())
	}

	s.AddMessage(ai.OfUser("u1"), ai.OfAssistant("a1"))
	if s.IsEmpty() {
		t.Fatal("加消息后不应为空")
	}
	s.Clear()
	if !s.IsEmpty() {
		t.Fatal("Clear 后应为空")
	}
}

func TestMemorySessionLatestMessages(t *testing.T) {
	s := ai.NewMemorySession()
	s.AddMessage(
		ai.OfUser("u1"), ai.OfAssistant("a1"),
		ai.OfUser("u2"), ai.OfAssistant("a2"), ai.OfAssistant("a3"),
	)

	got := s.LatestMessages(2)
	if len(got) != 3 || got[0].Content != "u2" {
		t.Fatalf("window=2 应前向对齐 user 锚点: %v", got)
	}

	if all := s.LatestMessages(0); len(all) != 5 {
		t.Fatalf("window<=0 返回全部: %d", len(all))
	}

	// 窗口内无 user（全 assistant/tool）：兜底向后找，找不到则原窗口起点
	s2 := ai.NewMemorySession()
	s2.AddMessage(ai.OfAssistant("a1"),
		ai.OfTool(ai.ToolResultOf("r"), "t", "c1", false),
		ai.OfAssistant("a2"))
	got2 := s2.LatestMessages(2)
	if len(got2) != 2 || got2[0].Role != ai.RoleTool {
		t.Fatalf("无 user 锚点时 = %v", got2)
	}
}

func TestMemorySessionRemoveLatestMessage(t *testing.T) {
	asstTC := &ai.Message{Role: ai.RoleAssistant, Content: &ai.AssistantMessage{
		ToolCalls: []ai.ToolCall{{ID: "c1", Name: "t"}},
	}}
	toolMsg := func() *ai.Message { return ai.OfTool(ai.ToolResultOf("r"), "t", "c1", false) }

	t.Run("末尾 tool：回溯删孤立的 toolCalls assistant", func(t *testing.T) {
		s := ai.NewMemorySession()
		s.AddMessage(ai.OfUser("u"), asstTC, toolMsg())
		s.RemoveLatestMessage(1)
		if got := s.Messages(); len(got) != 1 || got[0].Content != "u" {
			t.Fatalf("移除后 = %v", got)
		}
	})

	t.Run("末尾 assistant(toolCalls)：连带后续 tool", func(t *testing.T) {
		s := ai.NewMemorySession()
		s.AddMessage(ai.OfUser("u"), asstTC)
		s.RemoveLatestMessage(1)
		if got := s.Messages(); len(got) != 1 || got[0].Content != "u" {
			t.Fatalf("移除后 = %v", got)
		}
	})

	t.Run("多条 tool 级联", func(t *testing.T) {
		s := ai.NewMemorySession()
		s.AddMessage(ai.OfUser("u"), asstTC, toolMsg(), toolMsg())
		s.RemoveLatestMessage(1)
		if got := s.Messages(); len(got) != 1 || got[0].Content != "u" {
			t.Fatalf("移除后 = %v", got)
		}
	})

	t.Run("普通消息逐条", func(t *testing.T) {
		s := ai.NewMemorySession()
		s.AddMessage(ai.OfUser("u1"), ai.OfUser("u2"))
		s.RemoveLatestMessage(1)
		if got := s.Messages(); len(got) != 1 || got[0].Content != "u1" {
			t.Fatalf("移除后 = %v", got)
		}
	})
}

func TestMemorySessionMaxMessages(t *testing.T) {
	sys := ai.OfSystem("sys")
	asstTC := &ai.Message{Role: ai.RoleAssistant, Content: &ai.AssistantMessage{
		ToolCalls: []ai.ToolCall{{ID: "c1", Name: "t"}},
	}}
	tool := func() *ai.Message { return ai.OfTool(ai.ToolResultOf("r"), "t", "c1", false) }

	t.Run("逐条添加：超限从头移除非 system", func(t *testing.T) {
		s := ai.NewMemorySessionMax(3)
		s.AddMessage(sys, ai.OfUser("u1"), ai.OfAssistant("a1"))
		s.AddMessage(ai.OfUser("u2")) // 4 > 3 → 移除头部 u1
		got := s.Messages()
		if len(got) != 3 || got[0].Role != ai.RoleSystem || got[1].Content != "a1" || got[2].Content != "u2" {
			t.Fatalf("裁剪后 = %v", got)
		}
	})

	t.Run("批量添加：按新增数量从头裁剪（toolCalls 级联）", func(t *testing.T) {
		s := ai.NewMemorySessionMax(3)
		s.AddMessage(sys)
		s.AddMessage(ai.OfUser("u1"), asstTC, tool(), ai.OfUser("u2"))
		got := s.Messages()
		if len(got) != 1 || got[0].Role != ai.RoleSystem {
			t.Fatalf("裁剪后 = %v（Java 同语义：按新增条数从头裁，级联可超出）", got)
		}
	})
}
