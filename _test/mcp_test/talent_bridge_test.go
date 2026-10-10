// talent_bridge_test.go MCP×Talent 桥黑盒测试（06 §8）：真 mcp.Server 经
// httptest 挂载，RegisterTalent 导出本地 talent；aimcp.NewTalent 从远端读元
// 数据，isSupported/instruction/onAttach/tools 走跨进程管理工具回环。
package mcp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	ai "github.com/crazy-airhead/aifei-go/ai"
	aimcp "github.com/crazy-airhead/aifei-go/ai/mcp"
	"github.com/crazy-airhead/aifei-go/aifei"
	"github.com/crazy-airhead/aifei-go/mcp"
	"github.com/crazy-airhead/aifei-go/server"
)

// stubTalent 本地 talent：isSupported 按 prompt attrs 判定；onAttach 计数。
type stubTalent struct {
	ai.TalentBase
	attachCount int
}

func (s *stubTalent) Name() string                  { return "demo-talent" }
func (s *stubTalent) Description() string           { return "演示能力包" }
func (s *stubTalent) IsSupported(p *ai.Prompt) bool { return p.Attrs()["allow"] == true }
func (s *stubTalent) OnAttach(p *ai.Prompt)         { s.attachCount++ }
func (s *stubTalent) Instruction(p *ai.Prompt) string {
	if p.IsEmpty() {
		return "空提示指令"
	}
	return "使用演示工具完成任务"
}

// failStore 可翻转的服务端故障开关（fail-closed 用例；不直接 ts.Close()——
// streamable HTTP 客户端持有长连 GET 流，Close 会等活跃连接而挂死）。
var failStore atomic.Bool

func newTalentServer(t *testing.T, talent *stubTalent) *httptest.Server {
	t.Helper()
	app := aifei.New()
	mcps := mcp.NewServer(mcp.Implementation{Name: "talent-srv", Version: "1.0"})
	// 业务工具（应出现在 talent.Tools()）
	mcps.Tool("weather", `{"type":"object","properties":{"city":{"type":"string"}}}`,
		func(cc *mcp.CallCtx, args map[string]any) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent("sunny")}}, nil
		})
	aimcp.RegisterTalent(mcps, talent)
	io := server.NewIoHandler(app)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failStore.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		mcps.Middleware("/mcp")(io).ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	// LIFO：先强制断开客户端长连（自愈 GET 流的活跃请求会让 Close 等不完）
	t.Cleanup(ts.CloseClientConnections)
	return ts
}

func TestTalentBridgeRoundtrip(t *testing.T) {
	talent := &stubTalent{}
	ts := newTalentServer(t, talent)

	c := mcp.DialHTTP(ts.URL + "/mcp")
	defer c.Close()
	if _, err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := aimcp.NewProvider(c)
	tl, err := aimcp.NewTalent(p)
	if err != nil {
		t.Fatalf("NewTalent: %v", err)
	}

	// 元数据
	if tl.Name() != "demo-talent" || tl.Description() != "演示能力包" {
		t.Fatalf("元数据错误：%q %q", tl.Name(), tl.Description())
	}

	// 指令（跨进程往返）
	prompt := ai.PromptOf("写天气")
	if got := tl.Instruction(prompt); got != "使用演示工具完成任务" {
		t.Fatalf("Instruction 往返错误：%q", got)
	}
	if got := tl.Instruction(ai.NewPrompt()); got != "空提示指令" {
		t.Fatalf("空 Prompt 指令错误：%q", got)
	}

	// 准入（fail-open 于远端真值：attr allow）
	if tl.IsSupported(prompt) {
		t.Fatal("无 allow attr 应不支持")
	}
	prompt.SetAttr("allow", true)
	if !tl.IsSupported(prompt) {
		t.Fatal("allow=true 应支持")
	}

	// onAttach 副作用计数（经远端执行）
	tl.OnAttach(prompt)
	if talent.attachCount != 1 {
		t.Fatalf("onAttach 应回端执行一次，得 %d", talent.attachCount)
	}

	// 工具流：管理工具（hide）剔除、业务工具保留
	tools := tl.Tools(prompt)
	if len(tools) == 0 {
		t.Fatal("应至少有 weather 业务工具")
	}
	for _, tool := range tools {
		switch tool.Name() {
		case "weather":
		case "isSupportedMcp", "getInstructionMcp", "getToolsMcp", "onAttachMcp":
			t.Fatalf("管理工具 %s 不应暴露", tool.Name())
		default:
			t.Fatalf("意外工具 %s", tool.Name())
		}
	}

	// 名单裁剪：getToolsMcp 返回 []（无工具）
	aimcp.SetToolsNameProvider(func(p *ai.Prompt) []string { return []string{} })
	if got := tl.Tools(prompt); len(got) != 0 {
		t.Fatalf("空名单应无工具，得 %d", len(got))
	}
	// 名单裁剪：null（全量）
	aimcp.SetToolsNameProvider(func(p *ai.Prompt) []string { return nil })
	if got := tl.Tools(prompt); len(got) != 1 {
		t.Fatalf("null 名单应全量，得 %d", len(got))
	}
	// 名单裁剪：按名过滤（不存在的名字）
	aimcp.SetToolsNameProvider(func(p *ai.Prompt) []string { return []string{"nonexistent"} })
	if got := tl.Tools(prompt); len(got) != 0 {
		t.Fatalf("无匹配名应无工具，得 %d", len(got))
	}
	aimcp.SetToolsNameProvider(nil)
}

// TestTalentBridgeFailClosed 服务端全程 500：IsSupported fail-closed、Instruction
// 空值、Tools 回退全量。
func TestTalentBridgeFailClosed(t *testing.T) {
	t.Cleanup(func() { failStore.Store(false) })
	talent := &stubTalent{}
	ts := newTalentServer(t, talent)

	c := mcp.DialHTTP(ts.URL + "/mcp")
	defer c.Close()
	if _, err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := aimcp.NewProvider(c)
	tl, err := aimcp.NewTalent(p, aimcp.WithTalentTimeout(300*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	// 预热工具列表缓存（Tools 回退全量依赖本地缓存）
	if got := tl.Tools(ai.PromptOf("warmup")); len(got) != 1 {
		t.Fatalf("预热应得 1 个业务工具，得 %d", len(got))
	}
	failStore.Store(true)

	prompt := ai.PromptOf("q")
	prompt.SetAttr("allow", true)
	if tl.IsSupported(prompt) {
		t.Fatal("远端不可达应 fail-closed（false）")
	}
	if got := tl.Instruction(prompt); got != "" {
		t.Fatalf("远端不可达 Instruction 应为空，得 %q", got)
	}
	// Tools 回退全量（本地缓存列表）
	tools := tl.Tools(prompt)
	if len(tools) != 1 || tools[0].Name() != "weather" {
		t.Fatalf("Tools 应回退全量，得 %v", tools)
	}
}

// TestTalentBridgeMetadataMissing 无 talent 资源的服务端：NewTalent 报错。
func TestTalentBridgeMetadataMissing(t *testing.T) {
	app := aifei.New()
	mcps := mcp.NewServer(mcp.Implementation{Name: "plain", Version: "1"})
	io := server.NewIoHandler(app)
	ts := httptest.NewServer(mcps.Middleware("/mcp")(io))
	defer ts.Close()

	c := mcp.DialHTTP(ts.URL + "/mcp")
	defer c.Close()
	if _, err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := aimcp.NewTalent(aimcp.NewProvider(c)); err == nil {
		t.Fatal("无元数据资源应报错")
	}
}
