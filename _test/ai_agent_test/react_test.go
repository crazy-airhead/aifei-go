// react_test.go P2-b ReAct 智能体：reason→action→reason 两轮收敛、maxTurns
// 保险丝、autoRethink 放宽、会话窗口、StopLoop 死循环指纹、ToolRetry 物理
// 重试/参数自愈（stubmodel 脚本驱动，黑盒断言导出面）。
package ai_agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/ai/agent/react"
)

// weatherTool 计数型天气工具（args 捕获城市）。
func weatherTool(calls *int32, cities *[]string) ai.FunctionTool {
	return ai.NewTool("get_weather").
		Desc("查询城市天气").
		StringParam("city", "城市名", true).
		Handler(func(ctx context.Context, args map[string]any) (any, error) {
			atomic.AddInt32(calls, 1)
			*cities = append(*cities, fmt.Sprint(args["city"]))
			return map[string]string{"weather": "晴"}, nil
		}).
		Build()
}

// toolCallJSON 非流式 tool_calls 响应体。
func toolCallJSON(id, name, args string) string {
	return fmt.Sprintf(`{"id":"chatcmpl-t","model":"gpt-4o","choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":%q,"type":"function","function":{"name":%q,"arguments":%q}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`, id, name, args)
}

// toolCallSSE 流式 tool_calls 帧串（两段参数增量 + 收尾 usage）。
func toolCallSSE(id, name, args string) string {
	return fmt.Sprintf(`{"id":"c1","object":"chat.completion.chunk","choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":%q,"type":"function","function":{"name":%q,"arguments":""}}]}}]}

{"id":"c1","object":"chat.completion.chunk","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":%q}}]}}]}

{"id":"c1","object":"chat.completion.chunk","choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}

[DONE]`, id, name, args)
}

// bodyString 请求体序列化文本（窗口/提示词注入断言用）。
func bodyString(t *testing.T, body map[string]any) string {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	return string(data)
}

// messagesOf 请求体 messages 数组。
func messagesOf(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	arr := array(t, body["messages"])
	out := make([]map[string]any, 0, len(arr))
	for _, m := range arr {
		out = append(out, node(t, m))
	}
	return out
}

// messageContent 消息 content 的字符串投影（string 直出，其余 JSON）。
func messageContent(t *testing.T, m map[string]any) string {
	t.Helper()
	if s, ok := m["content"].(string); ok {
		return s
	}
	return bodyString(t, m)
}

// TestReActReasonActionTwoTurns 两轮收敛：reason(tool_calls)→action→reason(终答)。
func TestReActReasonActionTwoTurns(t *testing.T) {
	srv := newScripted(t,
		scriptedResp{body: toolCallJSON("call_1", "get_weather", `{"city":"北京"}`)},
		scriptedResp{body: chatJSON("北京今天晴，25 度")})

	var calls int32
	var cities []string
	a, err := react.NewBuilder(agentModel(t, srv)).
		Name("weather").
		ToolAdd(weatherTool(&calls, &cities)).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	resp, err := a.Prompt("北京天气怎么样？").Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	if got := resp.Content(); got != "北京今天晴，25 度" {
		t.Fatalf("Content = %q", got)
	}
	if srv.count() != 2 {
		t.Fatalf("请求数 = %d，期望 2", srv.count())
	}
	if calls != 1 || len(cities) != 1 || cities[0] != "北京" {
		t.Fatalf("工具调用 = %d 次, cities = %v", calls, cities)
	}

	// 请求 1：system 协议头 + tools 清单
	req0 := srv.request(t, 0)
	msgs := messagesOf(t, req0)
	if msgs[0]["role"] != "system" || !strings.Contains(messageContent(t, msgs[0]), "ReAct") {
		t.Fatalf("首消息非 system 协议头: %v", msgs[0])
	}
	if msgs[len(msgs)-1]["role"] != "user" {
		t.Fatalf("末消息非 user: %v", msgs[len(msgs)-1])
	}
	tools := array(t, req0["tools"])
	if len(tools) != 1 {
		t.Fatalf("tools = %v", tools)
	}

	// 请求 2：assistant tool_calls + tool observation 成套回放
	msgs2 := messagesOf(t, srv.request(t, 1))
	var hasAssistantCalls, hasObservation bool
	for _, m := range msgs2 {
		switch m["role"] {
		case "assistant":
			if _, ok := m["tool_calls"]; ok {
				hasAssistantCalls = true
			}
		case "tool":
			if strings.Contains(messageContent(t, m), "晴") {
				hasObservation = true
			}
		}
	}
	if !hasAssistantCalls || !hasObservation {
		t.Fatalf("第二轮请求缺成套回放: assistant=%v observation=%v, msgs=%v",
			hasAssistantCalls, hasObservation, msgs2)
	}

	// 轨迹收口：END、工具计数、双轮用量汇总、会话归档
	trace := resp.Trace()
	if trace.Route() != react.IDEnd {
		t.Fatalf("route = %q", trace.Route())
	}
	if trace.ToolCallCount() != 1 {
		t.Fatalf("ToolCallCount = %d", trace.ToolCallCount())
	}
	if got := resp.Metrics().TotalTokens(); got != 10 {
		t.Fatalf("TotalTokens = %d，期望 10", got)
	}
	if trace.TurnCount() != 2 {
		t.Fatalf("TurnCount = %d", trace.TurnCount())
	}
	sessMsgs := resp.Session().Messages()
	if len(sessMsgs) != 2 || sessMsgs[0].Role != ai.RoleUser || sessMsgs[1].Role != ai.RoleAssistant {
		t.Fatalf("会话归档 = %+v", sessMsgs)
	}
}

// TestReActMaxTurnsFuse 回合保险丝：超限即熔断，异常文案收口。
func TestReActMaxTurnsFuse(t *testing.T) {
	srv := newScripted(t, scriptedResp{body: toolCallJSON("call_x", "get_weather", `{"city":"北京"}`)})

	var calls int32
	var cities []string
	a, err := react.NewBuilder(agentModel(t, srv)).
		Name("fuse").
		MaxTurns(2).
		ToolAdd(weatherTool(&calls, &cities)).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	resp, err := a.Prompt("反复查天气").Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	// 回合 1/2 各一次 reason（均返回 tool_calls → action）；回合 3 触发熔断（先于请求）
	if got := resp.Content(); got != "Agent error: Maximum turns reached (2)." {
		t.Fatalf("Content = %q", got)
	}
	if srv.count() != 2 || calls != 2 {
		t.Fatalf("请求数 = %d, 工具调用 = %d", srv.count(), calls)
	}
	if !resp.Trace().IsAbnormal() {
		t.Fatalf("熔断应为异常结束")
	}
}

// TestReActAutoRethink 自动反思：达阈值扩容回合上限并注入自我反思提示。
func TestReActAutoRethink(t *testing.T) {
	tc := toolCallJSON("call_r", "get_weather", `{"city":"北京"}`)
	srv := newScripted(t,
		scriptedResp{body: tc},
		scriptedResp{body: tc},
		scriptedResp{body: chatJSON("任务完成")})

	var calls int32
	var cities []string
	a, err := react.NewBuilder(agentModel(t, srv)).
		Name("rethink").
		MaxTurns(2).
		AutoRethink(true).
		ToolAdd(weatherTool(&calls, &cities)).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	resp, err := a.Prompt("查天气直到完成").Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	// MaxTurns=2 → 阈值 max(1, 2*4/5)=1：回合 1 即扩容 +10（初始 2 的一半不足 10）
	if got := resp.Trace().Options().EffectiveMaxTurns(); got != 12 {
		t.Fatalf("EffectiveMaxTurns = %d，期望 12", got)
	}
	if !strings.Contains(bodyString(t, srv.request(t, 0)), "自我反思") {
		t.Fatalf("首请求应含自我反思提示")
	}
	if got := resp.Content(); got != "任务完成" {
		t.Fatalf("Content = %q", got)
	}
	if srv.count() != 3 {
		t.Fatalf("请求数 = %d，期望 3（熔断未触发）", srv.count())
	}
	if resp.Trace().IsAbnormal() {
		t.Fatalf("正常收敛不应异常结束")
	}
}

// TestReActSessionWindow 会话窗口：第二轮请求携带窗口内历史。
func TestReActSessionWindow(t *testing.T) {
	srv := newScripted(t,
		scriptedResp{body: chatJSON("答一")},
		scriptedResp{body: chatJSON("答二")})
	a, err := react.NewBuilder(agentModel(t, srv)).
		Name("win").SessionWindow(8).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	sess := agent.NewMemorySession("react-w1")
	if _, err := a.Prompt("第一问").Session(sess).Call(); err != nil {
		t.Fatalf("Call 1: %v", err)
	}
	resp2, err := a.Prompt("第二问").Session(sess).Call()
	if err != nil {
		t.Fatalf("Call 2: %v", err)
	}

	msgs := messagesOf(t, srv.request(t, 1))
	if len(msgs) != 4 {
		t.Fatalf("第二轮消息数 = %d，期望 4（system+历史2+新问）", len(msgs))
	}
	body := bodyString(t, srv.request(t, 1))
	if !strings.Contains(body, "第一问") || !strings.Contains(body, "答一") {
		t.Fatalf("第二轮请求未携带窗口历史: %s", body)
	}
	if got := resp2.Content(); got != "答二" {
		t.Fatalf("Content = %q", got)
	}
}

// TestReActStopLoopDetect 死循环指纹：同参工具调用连续重复触发自纠提示注入。
func TestReActStopLoopDetect(t *testing.T) {
	tc := toolCallJSON("call_l", "get_weather", `{"city":"北京"}`)
	srv := newScripted(t,
		scriptedResp{body: tc},
		scriptedResp{body: tc},
		scriptedResp{body: tc},
		scriptedResp{body: chatJSON("已收敛")})

	var calls int32
	var cities []string
	a, err := react.NewBuilder(agentModel(t, srv)).
		Name("loop").
		InterceptorAdd(react.NewStopLoopInterceptor()).
		ToolAdd(weatherTool(&calls, &cities)).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	resp, err := a.Prompt("查天气").Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	// 回合 3 指纹计数达 3 → 本轮 Action 结束后注入自纠提示 → 回合 4 请求可见
	if calls != 3 {
		t.Fatalf("工具调用 = %d，期望 3", calls)
	}
	if srv.count() != 4 {
		t.Fatalf("请求数 = %d，期望 4", srv.count())
	}
	if !strings.Contains(bodyString(t, srv.request(t, 3)), "检测到潜在循环") {
		t.Fatalf("第 4 次请求应含死循环自纠提示: %s", bodyString(t, srv.request(t, 3)))
	}
	if got := resp.Content(); got != "已收敛" {
		t.Fatalf("Content = %q", got)
	}
}

// TestReActToolRetryPhysical 物理失败重试：前两次失败、第三次成功。
func TestReActToolRetryPhysical(t *testing.T) {
	srv := newScripted(t,
		scriptedResp{body: toolCallJSON("call_p", "get_weather", `{"city":"北京"}`)},
		scriptedResp{body: chatJSON("完成")})

	var calls int32
	tool := ai.NewTool("get_weather").
		Desc("查询城市天气").
		StringParam("city", "城市名", true).
		Handler(func(ctx context.Context, args map[string]any) (any, error) {
			n := atomic.AddInt32(&calls, 1)
			if n < 3 {
				return nil, errors.New("db down")
			}
			return "恢复结果", nil
		}).
		Build()

	a, err := react.NewBuilder(agentModel(t, srv)).
		Name("retry").
		InterceptorAdd(react.NewToolRetryInterceptorOf(3, 500)).
		ToolAdd(tool).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	resp, err := a.Prompt("查天气").Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	if calls != 3 {
		t.Fatalf("工具执行 = %d 次，期望 3（2 失败 + 1 成功）", calls)
	}
	// 重试耗尽前的失败不落 Observation；成功结果作为观测回填
	var observation string
	for _, m := range messagesOf(t, srv.request(t, 1)) {
		if m["role"] == "tool" {
			observation = messageContent(t, m)
		}
	}
	if observation != "恢复结果" {
		t.Fatalf("tool observation = %q", observation)
	}
	if got := resp.Content(); got != "完成" {
		t.Fatalf("Content = %q", got)
	}
}

// TestReActToolRetryInvalidArgs 参数错误自愈：不重试，直接回馈修正提示。
func TestReActToolRetryInvalidArgs(t *testing.T) {
	srv := newScripted(t,
		scriptedResp{body: toolCallJSON("call_a", "get_weather", `{"city":"北京"}`)},
		scriptedResp{body: chatJSON("修正后完成")})

	var calls int32
	tool := ai.NewTool("get_weather").
		Desc("查询城市天气").
		StringParam("city", "城市名", true).
		Handler(func(ctx context.Context, args map[string]any) (any, error) {
			atomic.AddInt32(&calls, 1)
			return nil, fmt.Errorf("city 必填: %w", react.ErrInvalidArguments)
		}).
		Build()

	a, err := react.NewBuilder(agentModel(t, srv)).
		Name("argsfix").
		InterceptorAdd(react.NewToolRetryInterceptor()).
		ToolAdd(tool).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	resp, err := a.Prompt("查天气").Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	if calls != 1 {
		t.Fatalf("参数错误不应重试，执行 = %d 次", calls)
	}
	var observation string
	for _, m := range messagesOf(t, srv.request(t, 1)) {
		if m["role"] == "tool" {
			observation = messageContent(t, m)
		}
	}
	if !strings.Contains(observation, "Invalid arguments for tool [get_weather]") ||
		!strings.Contains(observation, "Please fix the arguments and try again.") {
		t.Fatalf("自愈提示 = %q", observation)
	}
	if got := resp.Content(); got != "修正后完成" {
		t.Fatalf("Content = %q", got)
	}
}
