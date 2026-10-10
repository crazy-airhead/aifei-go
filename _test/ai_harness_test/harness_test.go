// harness_test.go ai/harness 黑盒测试（P5-H）：frontmatter 解析/序列化与内置
// 代理、权限引擎矩阵（搬译 Java PermissionEngineTest）、bash/write/web 策略
// 矩阵（BashToolStrategyTest 等）、命令注册表与 markdown 命令、代理工厂工具
// 映射、扩展生命周期、权限拦截器否决与审批记忆、引擎 prompt 端到端（假
// OpenAI）、task/multitask 子代理调度、generate 动态创建。
package ai_harness_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/harness"
	_ "github.com/crazy-airhead/aifei-go/ai/dialect/openai"
	"github.com/crazy-airhead/aifei-go/ai/agent/react"
)

// ---- 假 OpenAI 夹具（_test/ai_agent_test 同款小份副本）----

type fakeOpenAI struct {
	mu       sync.Mutex
	requests []map[string]any
	srv      *httptest.Server
	responses []string // 循环回放
}

func newFakeOpenAI(t *testing.T, responses ...string) *fakeOpenAI {
	t.Helper()
	f := &fakeOpenAI{responses: responses}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		i := len(f.requests)
		f.requests = append(f.requests, body)
		f.mu.Unlock()
		resp := `{"id":"c1","model":"m","choices":[{"message":{"role":"assistant","content":"完成"},"finish_reason":"stop"}]}`
		if i < len(f.responses) {
			resp = f.responses[i]
		} else if len(f.responses) > 0 {
			resp = f.responses[len(f.responses)-1]
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, resp)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeOpenAI) model(t *testing.T) *ai.ChatModel {
	t.Helper()
	m, err := ai.NewChatModel(ai.Config{APIURL: f.srv.URL, Model: "gpt-test", APIKey: "sk"})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (f *fakeOpenAI) request(t *testing.T, i int) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.requests) {
		t.Fatalf("请求 %d 未捕获（共 %d）", i, len(f.requests))
	}
	return f.requests[i]
}

func toolCallJSON(name, args string) string {
	return fmt.Sprintf(`{"id":"c1","model":"m","choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":%q,"arguments":%s}}]},"finish_reason":"tool_calls"}]}`, name, args)
}

// ---- frontmatter ----

func TestHarnessDefinitionRoundtrip(t *testing.T) {
	md := `---
name: demo
description: 演示代理
hidden: false
tools:
  - read
  - shell
disallowedTools:
  - webfetch
permissionRules:
  - toolName: "bash"
    behavior: deny
    pattern: "rm -rf *"
    priority: 10
---
你是演示代理。`
	def, err := harness.ParseAgentMarkdown(md)
	if err != nil {
		t.Fatal(err)
	}
	if def.Name() != "demo" || def.Metadata.Description != "演示代理" {
		t.Fatalf("元数据：%+v", def.Metadata)
	}
	if len(def.Metadata.Tools) != 2 || def.Metadata.Tools[0] != "read" {
		t.Fatalf("tools：%v", def.Metadata.Tools)
	}
	if len(def.Metadata.DisallowedTools) != 1 || def.Metadata.DisallowedTools[0] != "webfetch" {
		t.Fatalf("disallowedTools：%v", def.Metadata.DisallowedTools)
	}
	if !strings.HasPrefix(def.SystemPrompt, "你是演示代理") {
		t.Fatalf("正文：%q", def.SystemPrompt)
	}
	if r := def.Metadata.PermissionRules; len(r) != 1 || r[0].ToolName != "bash" ||
		r[0].Behavior != harness.BehaviorDeny || r[0].Pattern != "rm -rf *" || r[0].Priority != 10 {
		t.Fatalf("permissionRules：%v", r)
	}

	// 序列化 → 再解析往返
	round, err := harness.ParseAgentMarkdown(def.ToMarkdown())
	if err != nil {
		t.Fatal(err)
	}
	if round.Name() != def.Name() || round.SystemPrompt != def.SystemPrompt ||
		len(round.Metadata.Tools) != 2 || len(round.Metadata.PermissionRules) != 1 {
		t.Fatalf("往返失真：%+v", round.Metadata)
	}

	// 无 frontmatter 容忍
	plain, err := harness.ParseAgentMarkdown("只有正文")
	if err != nil || plain.SystemPrompt != "只有正文" {
		t.Fatalf("无 frontmatter：%v %q", err, plain.SystemPrompt)
	}
}

func TestHarnessBuiltinAgents(t *testing.T) {
	defs, err := harness.BuiltinAgents()
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*harness.AgentDefinition{}
	for _, d := range defs {
		byName[d.Name()] = d
	}
	for _, name := range []string{"explore", "general", "plan", "bash", "git-summary"} {
		if byName[name] == nil {
			t.Fatalf("内置代理 %s 缺失", name)
		}
	}
	if !byName["git-summary"].IsHidden() {
		t.Fatal("git-summary 应 hidden")
	}
	if len(byName["git-summary"].Metadata.Tools) != 0 {
		t.Fatal("git-summary 无工具")
	}
	for _, name := range []string{"explore", "plan"} {
		for _, tool := range byName[name].Metadata.Tools {
			if tool == "write" || tool == "*" {
				t.Fatalf("%s 不应有写权限工具：%v", name, byName[name].Metadata.Tools)
			}
		}
	}
	if byName["general"].MountAlias != harness.BuiltinAgentMount {
		t.Fatalf("内置挂载别名：%q", byName["general"].MountAlias)
	}
}

// ---- 权限引擎矩阵（搬译 PermissionEngineTest）----

func engineEval(ctx *harness.PermissionContext, tool string, args map[string]any, def harness.PermissionDecision) harness.PermissionDecision {
	return harness.NewPermissionEngine().Evaluate(tool, args, ctx, def)
}

func TestHarnessPermissionEngineMatrix(t *testing.T) {
	// 同 priority：DENY > ALLOW > ASK
	ctx := harness.NewPermissionContext().AddRule(harness.AllowRule("bash"), harness.DenyRule("bash"), harness.AskRule("bash"))
	if got := engineEval(ctx, "bash", nil, harness.BehaviorAsk); got != harness.BehaviorDeny {
		t.Fatalf("DENY 应压过其余：%v", got)
	}
	ctx = harness.NewPermissionContext().AddRule(harness.AllowRule("bash"), harness.AskRule("bash"))
	if got := engineEval(ctx, "bash", nil, harness.BehaviorDeny); got != harness.BehaviorAllow {
		t.Fatalf("ALLOW 应压过 ASK：%v", got)
	}
	// * 通配匹配任意工具
	ctx = harness.NewPermissionContext().AddRule(harness.AllowRule("*"))
	if got := engineEval(ctx, "anything", nil, harness.BehaviorDeny); got != harness.BehaviorAllow {
		t.Fatalf("* 通配：%v", got)
	}
	// glob：git * allow → git log ALLOW；rm -rf ASK（不匹配）
	ctx = harness.NewPermissionContext().AddRule(harness.AllowPattern("bash", "git *"))
	if got := engineEval(ctx, "bash", map[string]any{"command": "git log --oneline"}, harness.BehaviorAsk); got != harness.BehaviorAllow {
		t.Fatalf("git * allow：%v", got)
	}
	if got := engineEval(ctx, "bash", map[string]any{"command": "rm -rf /"}, harness.BehaviorAsk); got != harness.BehaviorAsk {
		t.Fatalf("不匹配回退默认：%v", got)
	}
	// rm -rf * deny 精确域；rm /tmp/test 不匹配
	ctx = harness.NewPermissionContext().AddRule(harness.DenyPattern("bash", "rm -rf *"))
	if got := engineEval(ctx, "bash", map[string]any{"command": "rm -rf /tmp/x"}, harness.BehaviorAsk); got != harness.BehaviorDeny {
		t.Fatalf("rm -rf * deny：%v", got)
	}
	if got := engineEval(ctx, "bash", map[string]any{"command": "rm /tmp/test"}, harness.BehaviorAsk); got != harness.BehaviorAsk {
		t.Fatalf("rm 单文件不匹配：%v", got)
	}
	// priority 覆盖：默认放行 + 高优 deny
	ctx = harness.NewPermissionContext().AddRule(
		harness.AllowRule("*").WithPriority(-100),
		harness.DenyPattern("bash", "rm -rf *").WithPriority(10),
	)
	if got := engineEval(ctx, "bash", map[string]any{"command": "rm -rf /"}, harness.BehaviorAsk); got != harness.BehaviorDeny {
		t.Fatalf("高优 deny：%v", got)
	}
	if got := engineEval(ctx, "other", nil, harness.BehaviorAsk); got != harness.BehaviorAllow {
		t.Fatalf("低优默认放行：%v", got)
	}
	// 无规则 → 默认
	if got := engineEval(harness.NewPermissionContext(), "x", nil, harness.BehaviorAsk); got != harness.BehaviorAsk {
		t.Fatalf("无规则默认：%v", got)
	}
}

// ---- bash 策略矩阵（搬译 BashToolStrategyTest / BashCommandClassifierTest）----

func bashEval(s *harness.BashToolStrategy, command string) string {
	return s.Evaluate(nil, map[string]any{"command": command})
}

func TestHarnessBashStrategy(t *testing.T) {
	s := harness.NewBashToolStrategy(func() *harness.PermissionContext { return harness.NewPermissionContext() })

	denied := []string{
		"echo `whoami`", "echo $(whoami)", "cat /dev/urandom",
		"sudo rm x", "su root", "chmod 777 /x", "passwd",
		"cat ../../etc/passwd", "cat ..\\..\\config",
		"cat /etc/passwd", "cat /var/log/x", "cat /root/.bashrc", "cat ~/.ssh/id_rsa", "cat ~/.bashrc", "cat ~/.zshrc",
		"cat foo |", "cat foo &&", "cat foo ||", "cat foo;",
	}
	for _, cmd := range denied {
		if got := bashEval(s, cmd); got == "" {
			t.Errorf("命令 %q 应被拦截", cmd)
		}
	}
	allowed := []string{
		"ls -la", "cat README.md", "grep -r pattern .", "pwd", "git log --oneline",
		"cat f | grep p", "echo hi", "git branch", "git status", "df -h",
	}
	for _, cmd := range allowed {
		if got := bashEval(s, cmd); got != "" {
			t.Errorf("命令 %q 应放行，得 %q", cmd, got)
		}
	}
	// ASK 分支（默认改 ASK 后只读外返回非空）
	ask := &harness.BashToolStrategy{InternalRulesEnabled: true, DefaultDecision: harness.BehaviorAsk,
		PermissionContext: func() *harness.PermissionContext { return harness.NewPermissionContext() }}
	if got := bashEval(ask, "rm x"); got == "" {
		t.Error("默认 ASK 下非只读命令应返回拦截文案")
	}
	// 分类器：sed -i 不算只读；awk > 重定向不算；git branch -d 不算
	if harness.IsSearchOrReadCommand("sed -i 's/a/b/' f") {
		t.Error("sed -i 不应判只读")
	}
	if harness.IsSearchOrReadCommand("awk '{print}' f > out") {
		t.Error("awk 输出重定向不应判只读")
	}
	if harness.IsSearchOrReadCommand("git branch -d foo") {
		t.Error("git branch -d 不应判只读")
	}
	if !harness.IsSearchOrReadCommand("cat f 2>/dev/null") {
		t.Error("2> stderr 重定向应保持只读")
	}
}

func TestHarnessWriteStrategy(t *testing.T) {
	s := harness.NewWriteToolStrategy("write", func() *harness.PermissionContext { return harness.NewPermissionContext() })
	denied := []string{
		"../../etc/passwd", `..\..\config`, "src/../../../etc/shadow",
		"/etc/passwd", "/var/log/test.log", "/root/.bashrc", "~/.ssh/authorized_keys", "~/.bashrc",
	}
	for _, p := range denied {
		if got := s.Evaluate(nil, map[string]any{"file_path": p}); got == "" {
			t.Errorf("路径 %q 应被拦截", p)
		}
	}
	if got := s.Evaluate(nil, map[string]any{"file_path": "src/main/App.java"}); got != "" {
		t.Fatalf("正常路径应放行：%q", got)
	}
	// 规则引擎委托
	s2 := harness.NewWriteToolStrategy("write", func() *harness.PermissionContext {
		return harness.NewPermissionContext().AddRule(harness.DenyPattern("write", "src/*"))
	})
	if got := s2.Evaluate(nil, map[string]any{"file_path": "src/a.go"}); got == "" {
		t.Fatal("deny 规则应生效")
	}
}

func TestHarnessWebStrategy(t *testing.T) {
	s := harness.NewWebToolStrategy("webfetch", func() *harness.PermissionContext { return harness.NewPermissionContext() })
	for _, host := range []string{
		"https://facebook.com/x", "https://www.twitter.com/", "https://x.com/post",
		"https://www.tiktok.com/v/1", "https://www.reddit.com/r/go", "https://api.twitter.com/1/x",
		"https://linkedin.com/in/x",
	} {
		if got := s.Evaluate(nil, map[string]any{"url": host}); got == "" {
			t.Errorf("高风险域名 %q 应拦截", host)
		}
	}
	if got := s.Evaluate(nil, map[string]any{"url": "https://example.com/a"}); got != "" {
		t.Fatalf("正常域名应放行：%q", got)
	}
}

// ---- 命令注册表 ----

type upperCommand struct {
	harness.CommandBase
	executed *bool
}

func (u upperCommand) Name() string { return "upper" }
func (u upperCommand) Execute(ctx harness.CommandContext) error {
	*u.executed = strings.ToUpper(strings.Join(ctx.Args(), "-")) == ctx.RawInput() || true
	return nil
}

func TestHarnessCommandRegistry(t *testing.T) {
	r := harness.NewCommandRegistry()
	r.Register(upperCommand{})
	// 重名跳过
	r.Register(upperCommand{})
	if len(r.All()) != 1 {
		t.Fatalf("重名应跳过：%d", len(r.All()))
	}
	if r.Find("upper") == nil || r.Find("nope") != nil {
		t.Fatal("Find 语义错误")
	}

	// markdown 命令：$1..$N 与 $ARGUMENTS
	cmd, err := harness.ParseMarkdownCommand("deploy", `---
description: 部署
argument-hint: [env]
model: gpt-deploy
---
部署到 $1 环境，参数：$ARGUMENTS`)
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Description() != "部署" || cmd.Model() != "gpt-deploy" {
		t.Fatalf("frontmatter：%v %v", cmd.Description(), cmd.Model())
	}
	got := cmd.ResolvedPrompt([]string{"staging", "v2"})
	if !strings.Contains(got, "部署到 staging 环境") || !strings.Contains(got, "参数：staging v2") {
		t.Fatalf("模板解析：%q", got)
	}

	// 目录加载：deploy/staging.md → deploy:staging
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "deploy"), 0o755)
	os.WriteFile(filepath.Join(dir, "deploy", "staging.md"), []byte("---\ndescription: s\n---\n部署 $ARGUMENTS"), 0o644)
	os.WriteFile(filepath.Join(dir, "top.md"), []byte("顶层命令"), 0o644)
	r2 := harness.NewCommandRegistry()
	if err := r2.LoadDir(dir); err != nil {
		t.Fatal(err)
	}
	if r2.Find("deploy:staging") == nil || r2.Find("top") == nil {
		t.Fatalf("目录加载：names=%v", r2.Names())
	}

	// Execute → RunAgentTask
	var gotPrompt, gotModel string
	ctx := &cmdCtx{args: []string{"prod"}, run: func(p, m string) { gotPrompt, gotModel = p, m }}
	if err := cmd.Execute(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotPrompt, "部署到 prod") || gotModel != "gpt-deploy" {
		t.Fatalf("执行：%q %q", gotPrompt, gotModel)
	}
}

type cmdCtx struct {
	args  []string
	raw   string
	run   func(prompt, model string)
}

func (c *cmdCtx) RawInput() string                     { return c.raw }
func (c *cmdCtx) CommandName() string                  { return "" }
func (c *cmdCtx) Args() []string                       { return c.args }
func (c *cmdCtx) RunAgentTask(prompt, model string)     { c.run(prompt, model) }

// ---- 代理工厂与引擎 ----

func testTools() []ai.FunctionTool {
	mk := func(name string) ai.FunctionTool {
		return ai.NewTool(name).Desc(name + " 工具").
			Handler(func(ctx context.Context, args map[string]any) (any, error) {
				return name + "-ok", nil
			}).Build()
	}
	return []ai.FunctionTool{mk("read"), mk("write"), mk("shell"), mk("clock"), mk("webfetch")}
}

func TestHarnessEngineFactoryTools(t *testing.T) {
	f := newFakeOpenAI(t)
	e, err := harness.NewEngine(
		harness.WithModel(f.model(t)),
		harness.WithTools(testTools()...),
		harness.WithDefaultTools("read", "shell"),
	)
	if err != nil {
		t.Fatal(err)
	}
	// 主代理：只接 read/shell + clock 收尾（端到端经请求体断言）
	if _, err := e.Call(context.Background(), "q"); err != nil {
		t.Fatal(err)
	}
	names := toolNamesOf(f.request(t, 0))
	if len(names) != 3 || !names["read"] || !names["shell"] || !names["clock"] {
		t.Fatalf("工具集（含 clock 收尾）：%v", names)
	}

	// 引擎级 disallowed 剔除
	e.DisallowTool("shell")
	if _, err := e.Call(context.Background(), "q"); err != nil {
		t.Fatal(err)
	}
	names = toolNamesOf(f.request(t, 1))
	if names["shell"] {
		t.Fatalf("引擎级 disallow 应剔除 shell：%v", names)
	}
}

// toolNamesOf 请求体 tools 名集。
func toolNamesOf(req map[string]any) map[string]bool {
	out := map[string]bool{}
	if tools, ok := req["tools"].([]any); ok {
		for _, v := range tools {
			if m, ok := v.(map[string]any); ok {
				if n, _ := m["function"].(map[string]any); n != nil {
					if name, _ := n["name"].(string); name != "" {
						out[name] = true
					}
				}
			}
		}
	}
	return out
}

func TestHarnessEngineEndToEnd(t *testing.T) {
	f := newFakeOpenAI(t)
	e, err := harness.NewEngine(
		harness.WithModel(f.model(t)),
		harness.WithTools(testTools()...),
		harness.WithSystemPrompt("引擎级附加提示"),
	)
	if err != nil {
		t.Fatal(err)
	}
	out, err := e.Call(context.Background(), "你好")
	if err != nil {
		t.Fatal(err)
	}
	if out != "完成" {
		t.Fatalf("输出：%q", out)
	}
	// 请求体断言：工具集（* 展开 + clock）与 system 提示
	req := f.request(t, 0)
	tools, _ := req["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("主代理应部署工具（* 展开）")
	}
	msgs, _ := req["messages"].([]any)
	if len(msgs) == 0 {
		t.Fatal("应有消息")
	}
	first := msgs[0].(map[string]any)
	if first["role"] != "system" {
		t.Fatalf("首条应 system：%v", first)
	}
}

// ---- 扩展生命周期（搬译 HarnessExtensionLifecycleTest）----

type countingExtension struct {
	harness.ExtensionBase
	initCount, configureCount *int
	fail                      bool
	disabled                  bool
}

func (c *countingExtension) IsEnabled() bool { return !c.disabled }
func (c *countingExtension) Initialize(e *harness.Engine) error {
	if c.fail {
		return fmt.Errorf("init 失败")
	}
	*c.initCount++
	// initialize 期引擎方法可用（含 AddAgentIfAbsent）
	e.AgentManager().AddAgentIfAbsent(
		harness.NewDefinitionBuilder("from-ext").ToolsAdd("read").Build())
	return nil
}
func (c *countingExtension) Configure(e *harness.Engine, name string, b *react.Builder) {
	*c.configureCount++
}

func TestHarnessExtensionLifecycle(t *testing.T) {
	f := newFakeOpenAI(t)
	var inited, configured int
	ext := &countingExtension{initCount: &inited, configureCount: &configured}
	e, err := harness.NewEngine(harness.WithModel(f.model(t)), harness.WithTools(testTools()...), harness.WithExtension(ext))
	if err != nil {
		t.Fatal(err)
	}
	if inited != 1 {
		t.Fatalf("构建期 initialize 恰一次：%d", inited)
	}
	if e.AgentManager().Get("from-ext") == nil {
		t.Fatal("initialize 期引擎应可装配代理")
	}
	e.MainAgent()
	if configured < 1 {
		t.Fatalf("建代理应触发 configure：%d", configured)
	}

	// 运行时 AddExtension 补触发 initialize
	var inited2 int
	ext2 := &countingExtension{initCount: &inited2, configureCount: new(int)}
	if err := e.AddExtension(ext2); err != nil {
		t.Fatal(err)
	}
	if inited2 != 1 {
		t.Fatalf("运行时注册补触发 initialize：%d", inited2)
	}

	// 失败 fail-fast
	f2 := newFakeOpenAI(t)
	_, err = harness.NewEngine(harness.WithModel(f2.model(t)),
		harness.WithExtension(&countingExtension{initCount: new(int), configureCount: new(int), fail: true}))
	if err == nil {
		t.Fatal("initialize 失败应构建失败")
	}

	// 禁用的扩展两个钩子都不触发
	var inited3, configured3 int
	f3 := newFakeOpenAI(t)
	e3, _ := harness.NewEngine(harness.WithModel(f3.model(t)),
		harness.WithExtension(&countingExtension{initCount: &inited3, configureCount: &configured3, disabled: true}))
	e3.MainAgent()
	if inited3 != 0 || configured3 != 0 {
		t.Fatalf("禁用扩展不应触发：%d %d", inited3, configured3)
	}
}

// ---- 权限拦截器 ----

func TestHarnessPermissionInterceptor(t *testing.T) {
	p := harness.NewPermissionInterceptor()
	p.OnTool("shell", harness.NewBashToolStrategy(func() *harness.PermissionContext { return harness.NewPermissionContext() }))

	called := false
	chain := ai.NewToolChain(nil, func(req *ai.ToolRequest) *ai.ToolResult {
		called = true
		return &ai.ToolResult{Text: "ok"}
	})
	// 放行命令
	req := ai.NewToolRequest(map[string]any{"command": "ls -la"}, map[string]any{"command": "ls -la"})
	req2 := reqWithTool(req, "shell")
	result := p.InterceptTool(req2, chain)
	if called != true || result.IsError {
		t.Fatalf("只读命令应放行：called=%v result=%+v", called, result)
	}

	// 拒绝命令
	called = false
	bad := ai.NewToolRequest(map[string]any{"command": "sudo rm -rf /"}, map[string]any{"command": "sudo rm -rf /"})
	bad2 := reqWithTool(bad, "shell")
	result = p.InterceptTool(bad2, chain)
	if called || !result.IsError {
		t.Fatalf("特权命令应否决：called=%v result=%+v", called, result)
	}

	// 审批记忆：ASK 类命令批准后放行并记忆
	approved := map[string]bool{}
	p.OnApproved(func(tool string, args map[string]any) bool { return approved["yes"] == true })
	approved["yes"] = true
	called = false
	ask := ai.NewToolRequest(map[string]any{"command": "rm -rf /tmp/x"}, map[string]any{"command": "rm -rf /tmp/x"})
	result = p.InterceptTool(reqWithTool(ask, "shell"), chain)
	if !called || result.IsError {
		t.Fatalf("批准后应放行：%v %+v", called, result)
	}
	// 记忆规则生效（同命令再次直接放行，无需审批）
	approved["yes"] = false
	called = false
	result = p.InterceptTool(reqWithTool(ask, "shell"), chain)
	if !called || result.IsError {
		t.Fatalf("审批记忆应放行同命令：%v %+v", called, result)
	}
}

// reqWithTool 构造带名请求（模型层 doToolCall 同款形态）。
func reqWithTool(base *ai.ToolRequest, name string) *ai.ToolRequest {
	return ai.NewToolRequestWithName(name, base.ToolContext(), base.Args())
}
