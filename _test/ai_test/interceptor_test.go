// interceptor_test.go P1-c 拦截器链测试：OnPrepare 改写系统提示、InterceptCall
// 包裹链序（禁用跳过）、InterceptStream 包装流式、InterceptTool 包裹工具执行。
package ai_test

import (
	"context"
	"strings"
	"testing"

	ai "github.com/crazy-airhead/aifei-go/ai"
)

// sysRuleInterceptor 准备期拦截：向系统提示追加一条规则。
type sysRuleInterceptor struct {
	ai.InterceptorBase
	rule string
	hit  int // OnPrepare 被调用次数
}

func (i *sysRuleInterceptor) OnPrepare(_ ai.Session, _ *ai.ChatOptions, _ *ai.Prompt, system *strings.Builder) {
	i.hit++
	system.WriteString("\n补充规则：" + i.rule)
}

// TestInterceptorOnPrepareRewritesSystemPrompt 系统提示组装中段追加，出站请求
// 的 system 消息须同时含基础提示与追加段。
func TestInterceptorOnPrepareRewritesSystemPrompt(t *testing.T) {
	f := newFakeOpenAI(t, respTextJSON)
	m := fakeModel(t, f)
	rule := &sysRuleInterceptor{rule: "只准用中文回答"}

	resp, err := m.Prompt("hello").Options(func(o *ai.ChatOptions) {
		o.SystemPrompt = "你是助手"
		o.Interceptors = []ai.Interceptor{rule}
	}).Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Text() == "" {
		t.Fatalf("resp.Text 为空")
	}
	if rule.hit != 1 {
		t.Fatalf("OnPrepare 调用 %d 次，应为 1（prepare 只跑一次）", rule.hit)
	}
	msgs := f.messagesOf(t, 0)
	if len(msgs) == 0 || msgs[0]["role"] != "system" {
		t.Fatalf("messages = %v，首条应为 system", msgs)
	}
	sys, _ := msgs[0]["content"].(string)
	if !strings.Contains(sys, "你是助手") || !strings.Contains(sys, "补充规则：只准用中文回答") {
		t.Fatalf("system = %q，基础提示与追加段都应在", sys)
	}
}

// callTraceInterceptor 调用期拦截：进出各记一笔（链序 = 嵌套序）。
type callTraceInterceptor struct {
	ai.InterceptorBase
	name  string
	trace *[]string
}

func (c *callTraceInterceptor) InterceptCall(req *ai.CallRequest, chain *ai.CallChain) (*ai.Response, error) {
	*c.trace = append(*c.trace, "enter:"+c.name)
	resp, err := chain.DoIntercept(req)
	*c.trace = append(*c.trace, "exit:"+c.name)
	return resp, err
}

// disabledCountingInterceptor 恒禁用：链上应被跳过（含 OnPrepare）。
type disabledCountingInterceptor struct {
	ai.InterceptorBase
	hit *int
}

func (d *disabledCountingInterceptor) IsEnabled() bool { return false }
func (d *disabledCountingInterceptor) OnPrepare(ai.Session, *ai.ChatOptions, *ai.Prompt, *strings.Builder) {
	*d.hit++
}

// TestInterceptorCallChainOrder 链序：A(启用) → D(禁用，跳过) → B(启用) → 真实调用；
// 出入配对呈嵌套形，响应原样回传。
func TestInterceptorCallChainOrder(t *testing.T) {
	f := newFakeOpenAI(t, respTextJSON)
	m := fakeModel(t, f)

	var trace []string
	disabledHit := 0
	resp, err := m.Prompt("hello").Options(func(o *ai.ChatOptions) {
		o.Interceptors = []ai.Interceptor{
			&callTraceInterceptor{name: "A", trace: &trace},
			&disabledCountingInterceptor{hit: &disabledHit},
			&callTraceInterceptor{name: "B", trace: &trace},
		}
	}).Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got := strings.Join(trace, ","); got != "enter:A,enter:B,exit:B,exit:A" {
		t.Fatalf("链序 = %s，应为嵌套 enter:A,enter:B,exit:B,exit:A", got)
	}
	if disabledHit != 0 {
		t.Fatalf("禁用拦截器 OnPrepare 被调 %d 次", disabledHit)
	}
	if resp.Text() != "好的，简答。" {
		t.Fatalf("resp.Text = %q，应原样回传", resp.Text())
	}
}

// streamTraceInterceptor 流式调用拦截：包装整轮流式请求。
type streamTraceInterceptor struct {
	ai.InterceptorBase
	trace *[]string
}

func (s *streamTraceInterceptor) InterceptStream(ctx context.Context, req *ai.CallRequest, chain *ai.StreamChain) error {
	*s.trace = append(*s.trace, "stream:open:"+req.Messages()[len(req.Messages())-1].Content.(string))
	err := chain.DoIntercept(ctx, req)
	*s.trace = append(*s.trace, "stream:close")
	return err
}

// TestInterceptorStreamChainOrder 流式链：拦截器包裹 doStream，事件完整到达 sink。
func TestInterceptorStreamChainOrder(t *testing.T) {
	s := newSSEResponder(t,
		`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"你"}}]}`,
		`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"好"}}]}`,
		`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`[DONE]`,
	)
	m := sseModel(t, s)

	var trace []string
	rec := &sinkRecorder{}
	err := m.Prompt("你好").Options(func(o *ai.ChatOptions) {
		o.Interceptors = []ai.Interceptor{&streamTraceInterceptor{trace: &trace}}
	}).Stream(context.Background(), rec)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if strings.Join(trace, ",") != "stream:open:你好,stream:close" {
		t.Fatalf("流式链序 = %v", trace)
	}
	var text strings.Builder
	for _, ev := range rec.ofType(ai.EventTextDelta) {
		text.WriteString(ev.Text)
	}
	if text.String() != "你好" {
		t.Fatalf("事件正文 = %q", text.String())
	}
}

// toolTraceInterceptor 工具级拦截：记录 ToolRequest 暴露面后放行。
type toolTraceInterceptor struct {
	ai.InterceptorBase
	trace *[]string
}

func (ti *toolTraceInterceptor) InterceptTool(req *ai.ToolRequest, chain *ai.ToolChain) *ai.ToolResult {
	args, _ := req.Args()["word"].(string)
	*ti.trace = append(*ti.trace, "tool:echo:"+args)
	return chain.DoIntercept(req)
}

// TestInterceptorToolChain 工具拦截链包裹工具执行，参数与会话上下文可见。
func TestInterceptorToolChain(t *testing.T) {
	echoCallJSON := `{"id":"c-echo","model":"gpt-4o",
	 "choices":[{"index":0,"message":{"role":"assistant","content":null,
	   "tool_calls":[{"id":"call_7","type":"function","index":0,
	     "function":{"name":"echo","arguments":"{\"word\":\"你好\"}"}}]},"finish_reason":"tool_calls"}],
	 "usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`
	f := newFakeOpenAI(t, echoCallJSON, respTextJSON)
	m := fakeModel(t, f)

	var trace []string
	echo := ai.NewTool("echo").
		Desc("回声").
		StringParam("word", "词", true).
		Handler(func(ctx context.Context, args map[string]any) (any, error) {
			return args["word"].(string) + "!", nil
		}).
		Build()

	resp, err := m.Prompt("hi").Options(func(o *ai.ChatOptions) {
		o.AutoToolCall = boolPtr(true)
		o.Tools = []ai.FunctionTool{echo}
		o.Interceptors = []ai.Interceptor{&toolTraceInterceptor{trace: &trace}}
	}).Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got := strings.Join(trace, ","); got != "tool:echo:你好" {
		t.Fatalf("工具链 = %s", got)
	}
	// 工具结果进入会话并随第二轮请求回传
	msgs := f.messagesOf(t, 1)
	last := msgs[len(msgs)-1]
	if last["role"] != "tool" {
		t.Fatalf("第 2 轮末消息 = %v，应为 tool", last)
	}
	if resp.Text() == "" {
		t.Fatalf("resp.Text 为空")
	}
}

func boolPtr(b bool) *bool { return &b }
