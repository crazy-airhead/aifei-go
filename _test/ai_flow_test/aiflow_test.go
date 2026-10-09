// aiflow_test.go P2-d ai/flow 组件编排（黑盒）：chat_case1 逐字搬译执行、
// stream:true 事件流句柄（冷启动语义）、chatConfig 附件缓存（D8）、节点事件
// EventBus 订阅、exclusive 网关 + enjoy 条件（react1 场景）、注册表与桩组件。
package ai_flow_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/embedding"
	aiflow "github.com/crazy-airhead/aifei-go/ai/flow"
	"github.com/crazy-airhead/aifei-go/dami"
	"github.com/crazy-airhead/aifei-go/flow"
)

// newEngine 构建 RegisterDefaults 装配的默认引擎。
func newEngine() (*flow.Engine, *flow.MapContainer) {
	c := aiflow.RegisterDefaults(flow.NewMapContainer())
	return flow.NewEngine(flow.NewSimpleDriver(flow.WithContainer(c))), c
}

// chatCase1YAML solon-ai-flow 测试资源 flow/chat_case1.chain.yml 逐字搬译
//（JSON 布局即 YAML 子集，节点 id/title/task/meta/link 原样保留）；仅
// chatConfig 的 provider/apiUrl 指向本地假服务（Go 侧假服务说 openai 方言）。
func chatCase1YAML(apiURL string) string {
	return `{
  "id": "chat_case1",
  "layout": [
    {
      "id": "node_sfeqrc0wb",
      "type": "start",
      "title": "开始",
      "link": [{"nextId": "node_sxha48u9mh"}]
    },
    {
      "id": "node_sxha48u9mh",
      "type": "activity",
      "title": "变量输入",
      "task": "@VarInput",
      "meta": {"message": "你好"}
    },
    {
      "id": "node_au0ju21yz",
      "type": "activity",
      "title": "大模型",
      "task": "@ChatModel",
      "meta": {
        "systemPrompt": "你是个聊天助手",
        "stream": false,
        "chatConfig": {
          "provider": "openai",
          "model": "gpt-4o",
          "apiUrl": "` + apiURL + `"
        }
      },
      "link": [{"nextId": "node_xlhk0gfbxh"}]
    },
    {
      "id": "node_xlhk0gfbxh",
      "type": "activity",
      "title": "控制台输出",
      "task": "@ConsoleOutput",
      "meta": {},
      "link": []
    }
  ]
}`
}

// TestChatCase1Chain chat_case1 逐字搬译：VarInput → VarCopy（原版含 VarCopy
// 节点，此处同构并入数据链）→ ChatModel（systemPrompt/chatConfig 从 meta
// 构建）→ ConsoleOutput。断言请求数据流与控制台输出。
func TestChatCase1Chain(t *testing.T) {
	srv := newScripted(t, scriptedResp{body: chatJSON("你好！我是聊天助手。")})
	// 与 Java 原版同构：VarInput 与 ChatModel 之间补 VarCopy 节点（message → var2）
	yamlText := strings.Replace(chatCase1YAML(srv.srv.URL),
		`"task": "@VarInput",
      "meta": {"message": "你好"}
    },`,
		`"task": "@VarInput",
      "meta": {"message": "你好"},
      "link": [{"nextId": "node_var_copy"}]
    },
    {
      "id": "node_var_copy",
      "type": "activity",
      "title": "变量复制",
      "task": "@VarCopy",
      "meta": {"var2": "message"},
      "link": [{"nextId": "node_au0ju21yz"}]
    },`, 1)

	engine, _ := newEngine()
	g, err := flow.GraphFromText(yamlText)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	console := &bytes.Buffer{}
	aiflow.ConsoleWriter = console
	t.Cleanup(func() { aiflow.ConsoleWriter = os.Stdout })

	ctx := flow.NewContext("case1")
	if err := engine.Eval(g, ctx); err != nil {
		t.Fatalf("eval: %v", err)
	}

	// 请求数据流：system 提示词 + VarInput 注入的用户消息
	if srv.count() != 1 {
		t.Fatalf("请求数 = %d", srv.count())
	}
	msgs := srv.request(t, 0)["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages = %v", msgs)
	}
	sys, _ := msgs[0].(map[string]any)
	// ai 核心 prepare 在系统提示词后拼 "\n\n" 段分隔（schema/talent 段接续），
	// 故按 TrimSpace 比较
	if sys["role"] != "system" || strings.TrimSpace(sys["content"].(string)) != "你是个聊天助手" {
		t.Fatalf("system 消息 = %v", msgs[0])
	}
	usr, _ := msgs[1].(map[string]any)
	if usr["role"] != "user" || usr["content"] != "你好" {
		t.Fatalf("user 消息 = %v", msgs[1])
	}

	// VarCopy：message → var2（表达式求值）
	if ctx.Get("var2") != "你好" {
		t.Fatalf("var2 = %v", ctx.Get("var2"))
	}

	// ConsoleOutput：写回 message 变量 + 控制台打印
	if got := ctx.Get("message"); got != "你好！我是聊天助手。" {
		t.Fatalf("message = %v", got)
	}
	if !strings.Contains(console.String(), "你好！我是聊天助手。") {
		t.Fatalf("控制台输出 = %q", console.String())
	}
}

// TestChatModelStreamHandleLazy stream:true 输出事件流句柄：句柄是冷的——
// eval 完成时请求未发起；首次消费才发起并按 TEXT_DELTA 聚合。
func TestChatModelStreamHandleLazy(t *testing.T) {
	srv := newScripted(t, scriptedResp{body: chatSSE("流式回复内容"), sse: true})
	yamlText := `id: stream_case
layout:
  - { id: s, type: start, link: in }
  - { id: in, type: activity, task: "@VarInput", meta: {message: "你好"}, link: cm }
  - { id: cm, type: activity, task: "@ChatModel", meta: {stream: true, chatConfig: {provider: openai, model: gpt-4o, apiUrl: "` + srv.srv.URL + `"}}, link: e }
  - { id: e, type: end }
`
	engine, _ := newEngine()
	g, err := flow.GraphFromText(yamlText)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	ctx := flow.NewContext("st")
	if err := engine.Eval(g, ctx); err != nil {
		t.Fatalf("eval: %v", err)
	}

	handle, ok := ctx.Get("message").(*aiflow.StreamHandle)
	if !ok {
		t.Fatalf("stream 输出形态 = %T", ctx.Get("message"))
	}
	if srv.count() != 0 {
		t.Fatalf("句柄应冷启动（eval 后 0 请求），实为 %d", srv.count())
	}

	s, err := aiflow.InputAsString(handle)
	if err != nil {
		t.Fatalf("聚合: %v", err)
	}
	if s != "流式回复内容" {
		t.Fatalf("聚合文本 = %q", s)
	}
	if srv.count() != 1 {
		t.Fatalf("消费后请求数 = %d", srv.count())
	}
}

// TestWebOutputStreamEndToEnd ChatModel(stream) → WebOutput：逐帧写出（每
// delta 一行）+ 汇总写回 message 变量。
func TestWebOutputStreamEndToEnd(t *testing.T) {
	srv := newScripted(t, scriptedResp{body: chatSSE("分段流式正文"), sse: true})
	yamlText := `id: web_stream
layout:
  - { id: s, type: start, link: in }
  - { id: in, type: activity, task: "@VarInput", meta: {message: "讲个故事"}, link: cm }
  - { id: cm, type: activity, task: "@ChatModel", meta: {stream: true, chatConfig: {provider: openai, model: gpt-4o, apiUrl: "` + srv.srv.URL + `"}}, link: wo }
  - { id: wo, type: activity, task: "@WebOutput", link: e }
  - { id: e, type: end }
`
	engine, _ := newEngine()
	g, err := flow.GraphFromText(yamlText)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	web := &bytes.Buffer{}
	aiflow.WebWriter = web
	t.Cleanup(func() { aiflow.WebWriter = os.Stdout })

	ctx := flow.NewContext("ws")
	if err := engine.Eval(g, ctx); err != nil {
		t.Fatalf("eval: %v", err)
	}

	if srv.count() != 1 {
		t.Fatalf("请求数 = %d", srv.count())
	}
	// 两个 TEXT delta → 两行帧写出
	lines := strings.Split(strings.TrimRight(web.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("写出帧数 = %d（%q）", len(lines), web.String())
	}
	if got := ctx.Get("message"); got != "分段流式正文" {
		t.Fatalf("message = %v", got)
	}
}

// TestChatModelAttachmentCache chatConfig 从 meta 构建后缓存于节点附件（D8）：
// 第二次执行不重建——上下文属性 chatModel 与节点附件为同一实例。
func TestChatModelAttachmentCache(t *testing.T) {
	srv := newScripted(t,
		scriptedResp{body: chatJSON("第一轮")},
		scriptedResp{body: chatJSON("第二轮")})

	engine, _ := newEngine()
	g, err := flow.GraphFromText(chatCase1YAML(srv.srv.URL))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	aiflow.ConsoleWriter = &bytes.Buffer{} // 静音控制台
	t.Cleanup(func() { aiflow.ConsoleWriter = os.Stdout })

	nodeCM := g.GetNode("node_au0ju21yz")

	ctx1 := flow.NewContext("c1")
	if err := engine.Eval(g, ctx1); err != nil {
		t.Fatalf("eval 1: %v", err)
	}
	// 附件缓存的是构建产物（模型+工具的私有包装），黑盒只断言非空
	if nodeCM.Attachment("aiflow.chatModel") == nil {
		t.Fatalf("节点附件未写入")
	}
	prop1 := chatModelProperty(t, ctx1)

	ctx2 := flow.NewContext("c2")
	if err := engine.Eval(g, ctx2); err != nil {
		t.Fatalf("eval 2: %v", err)
	}

	// 重建会产生新 *ai.ChatModel 实例；同实例即缓存命中
	if prop2 := chatModelProperty(t, ctx2); prop2 != prop1 {
		t.Fatalf("第二次执行重建了模型（实例不同）——附件缓存未命中")
	}
	if srv.count() != 2 {
		t.Fatalf("两次执行请求数 = %d", srv.count())
	}
}

// chatModelProperty 取上下文属性 property_chatModel 的首值（*ai.ChatModel）。
func chatModelProperty(t *testing.T, ctx flow.Context) *ai.ChatModel {
	t.Helper()
	l, ok := flow.GetAs[[]any](ctx, "property_chatModel")
	if !ok || len(l) == 0 {
		t.Fatalf("属性 property_chatModel 未写入")
	}
	m, ok := l[0].(*ai.ChatModel)
	if !ok {
		t.Fatalf("属性类型 = %T", l[0])
	}
	return m
}

// TestNodeEventsSubscribable 节点事件 flow.node.start/end 经 ctx.EventBus()
// 可订阅：每个 AI 组件节点 start/end 成对、按执行序。
func TestNodeEventsSubscribable(t *testing.T) {
	yamlText := `id: events_case
layout:
  - { id: s, type: start, link: a }
  - { id: a, type: activity, task: "@VarInput", meta: {message: "hi"}, link: b }
  - { id: b, type: activity, task: "@VarCopy", meta: {var2: "message"}, link: c }
  - { id: c, type: activity, task: "@ConsoleOutput", link: e }
  - { id: e, type: end }
`
	engine, _ := newEngine()
	g, err := flow.GraphFromText(yamlText)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	aiflow.ConsoleWriter = &bytes.Buffer{}
	t.Cleanup(func() { aiflow.ConsoleWriter = os.Stdout })

	var seq []string
	ctx := flow.NewContext("ev")
	un1 := dami.ListenOn[*aiflow.NodeEvent](ctx.EventBus(), aiflow.EventFlowNodeStart, func(e *dami.Event[*aiflow.NodeEvent]) error {
		seq = append(seq, "start:"+e.Payload.Node.ID())
		return nil
	})
	defer un1()
	un2 := dami.ListenOn[*aiflow.NodeEvent](ctx.EventBus(), aiflow.EventFlowNodeEnd, func(e *dami.Event[*aiflow.NodeEvent]) error {
		seq = append(seq, "end:"+e.Payload.Node.ID())
		return nil
	})
	defer un2()

	if err := engine.Eval(g, ctx); err != nil {
		t.Fatalf("eval: %v", err)
	}

	want := []string{
		"start:a", "end:a",
		"start:b", "end:b",
		"start:c", "end:c",
	}
	if strings.Join(seq, ",") != strings.Join(want, ",") {
		t.Fatalf("节点事件序 = %v（want %v）", seq, want)
	}
}

// TestExclusiveGatewayEnjoyCondition react1 场景：exclusive 网关 when 条件
//（Java SnEL '"APPROVED".equals(review_status)' 的 enjoy 本地语法等价翻译
// review_status == "APPROVED"）按变量分流。
func TestExclusiveGatewayEnjoyCondition(t *testing.T) {
	yamlText := `id: review_gate
layout:
  - { id: s, type: start, link: in }
  - { id: in, type: activity, task: "@VarInput", meta: {review_status: "%s"}, link: x }
  - { id: x, type: exclusive, link: [{nextId: approve, when: 'review_status == "APPROVED"'}, {nextId: revise, when: 'review_status == "NEEDS_CHANGE"'}, {nextId: fail}] }
  - { id: approve, type: activity, task: "@VarInput", meta: {result: "approved"}, link: e }
  - { id: revise, type: activity, task: "@VarInput", meta: {result: "revised"}, link: e }
  - { id: fail, type: activity, task: "@VarInput", meta: {result: "failed"}, link: e }
  - { id: e, type: end }
`
	engine, _ := newEngine()

	for _, tc := range []struct {
		status string
		want   string
	}{
		{"APPROVED", "approved"},
		{"NEEDS_CHANGE", "revised"},
		{"REJECTED", "failed"}, // 兜底默认边
	} {
		g, err := flow.GraphFromText(strings.Replace(yamlText, "%s", tc.status, 1))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		ctx := flow.NewContext("gate-" + tc.status)
		if err := engine.Eval(g, ctx); err != nil {
			t.Fatalf("eval(%s): %v", tc.status, err)
		}
		if got := ctx.Get("result"); got != tc.want {
			t.Fatalf("review_status=%s 分流结果 = %v（want %s）", tc.status, got, tc.want)
		}
	}
}

// TestConsoleOutputFormat format meta 模板渲染（#{expr} 与 ${name} 两类标记）。
func TestConsoleOutputFormat(t *testing.T) {
	yamlText := `id: fmt_case
layout:
  - { id: s, type: start, link: in }
  - { id: in, type: activity, task: "@VarInput", meta: {message: "hi", name: "Go"}, link: out }
  - { id: out, type: activity, task: "@ConsoleOutput", meta: {format: "用户 #{name} 说：${message}"}, link: e }
  - { id: e, type: end }
`
	engine, _ := newEngine()
	g, err := flow.GraphFromText(yamlText)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	console := &bytes.Buffer{}
	aiflow.ConsoleWriter = console
	t.Cleanup(func() { aiflow.ConsoleWriter = os.Stdout })

	ctx := flow.NewContext("fmt")
	if err := engine.Eval(g, ctx); err != nil {
		t.Fatalf("eval: %v", err)
	}
	if got := strings.TrimSpace(console.String()); got != "用户 Go 说：hi" {
		t.Fatalf("格式化输出 = %q", got)
	}
	// format 分支打印渲染值；message 变量仍写入聚合值
	if ctx.Get("message") != "hi" {
		t.Fatalf("message = %v", ctx.Get("message"))
	}
}

// TestEmbeddingModelComponent embeddingConfig 构建 + 附件缓存（Go 版补写回，
// 二次执行同实例）+ 属性 embeddingModel 转入上下文。
func TestEmbeddingModelComponent(t *testing.T) {
	srv := newScripted(t) // 构建不打服务；占位
	yamlText := `id: emb_case
layout:
  - { id: s, type: start, link: em }
  - { id: em, type: activity, task: "@EmbeddingModel", meta: {embeddingConfig: {provider: openai, model: text-embedding-3-small, apiUrl: "` + srv.srv.URL + `"}}, link: e }
  - { id: e, type: end }
`
	engine, _ := newEngine()
	g, err := flow.GraphFromText(yamlText)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	ctx1 := flow.NewContext("e1")
	if err := engine.Eval(g, ctx1); err != nil {
		t.Fatalf("eval 1: %v", err)
	}
	l, ok := flow.GetAs[[]any](ctx1, "property_embeddingModel")
	if !ok || len(l) == 0 {
		t.Fatalf("属性 embeddingModel 未写入")
	}
	m1, ok := l[0].(*embedding.Model)
	if !ok {
		t.Fatalf("属性类型 = %T", l[0])
	}

	ctx2 := flow.NewContext("e2")
	if err := engine.Eval(g, ctx2); err != nil {
		t.Fatalf("eval 2: %v", err)
	}
	l2, _ := flow.GetAs[[]any](ctx2, "property_embeddingModel")
	if m2, ok := l2[0].(*embedding.Model); !ok || m2 != m1 {
		t.Fatalf("二次执行应复用嵌入模型实例")
	}
	if srv.count() != 0 {
		t.Fatalf("构建不应发起请求（实为 %d）", srv.count())
	}
}

// TestRegisterDefaultsAndStubs 12 组件全量注册 + P4/P5 桩显式报错。
func TestRegisterDefaultsAndStubs(t *testing.T) {
	_, c := newEngine()
	for _, name := range []string{
		"VarInput", "VarCopy", "ConsoleInput", "WebInput",
		"VarOutput", "ConsoleOutput", "WebOutput",
		"ChatModel", "EmbeddingModel", "GenerateModel",
		"InMemoryRepository", "RedisRepository",
	} {
		if c.GetComponent(name) == nil {
			t.Errorf("组件 %s 未注册", name)
		}
	}

	stubYAML := `id: stub_%s
layout:
  - { id: s, type: start, link: n }
  - { id: n, type: activity, task: "@%s", link: e }
  - { id: e, type: end }
`
	for _, tc := range []struct{ task, want string }{
		{"GenerateModel", "P5"},
		{"InMemoryRepository", "P4"},
		{"RedisRepository", "P5"},
	} {
		engine, _ := newEngine()
		g, err := flow.GraphFromText(strings.Replace(stubYAML, "%s", tc.task, 2))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		err = engine.Eval(g, flow.NewContext("stub"))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s 桩应报错含 %q，实为 %v", tc.task, tc.want, err)
		}
	}
}
