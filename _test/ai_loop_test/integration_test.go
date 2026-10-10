// integration_test.go 集成桥黑盒测试：Agent 桥（Ralph 驱动/错误转文案/
// NOT_FOUND 状态）、Flow 桥（变量提取/回写/flowId 状态）、Harness 桥
//（caller 注入/未注入文案/UltraQA 启动）。
package ai_loop_test

import (
	"context"
	"testing"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/ai/loop"
	"github.com/crazy-airhead/aifei-go/flow"
)

// stubAgent 最小智能体（固定文本/错误）。
type stubAgent struct {
	text string
	fail error
}

func (s *stubAgent) Name() string  { return "stub" }
func (s *stubAgent) Title() string { return "stub" }
func (s *stubAgent) Run(ctx flow.Context, node *flow.Node) error { return nil }
func (s *stubAgent) Role() string        { return "" }
func (s *stubAgent) RoleFor(flow.Context) string { return "" }
func (s *stubAgent) Profile() *agent.Profile { return nil }
func (s *stubAgent) Metadata(flow.Context) map[string]any { return nil }
func (s *stubAgent) Call(p *ai.Prompt, session agent.Session) (*ai.AssistantMessage, error) {
	if s.fail != nil {
		return nil, s.fail
	}
	return &ai.AssistantMessage{Text: s.text}, nil
}

func TestLoopAgentIntegration(t *testing.T) {
	e := loop.NewEngine(nil)
	integ := loop.NewAgentIntegration(e)

	// 桥错误面：未注入
	if got := integ.Bridge().ExecutePrompt("x"); got != "Agent error: agent not injected. Call SetAgent() first." {
		t.Fatalf("未注入错误面：%q", got)
	}
	// 桥错误面：执行失败转文案
	integ.Bridge().Agent = &stubAgent{fail: context.DeadlineExceeded}
	if got := integ.Bridge().ExecutePrompt("x"); got != "Agent error: context deadline exceeded" {
		t.Fatalf("执行失败文案：%q", got)
	}

	// 智能体驱动 Ralph：stub 输出含验收关键词 → 单故事完成
	a := &stubAgent{text: "实现了登录成功功能"}
	s, err := integ.StartAgentDrivenRalphLoop("实现登录", a)
	if err != nil {
		t.Fatal(err)
	}
	// PRD 未注入 → Ralph shouldContinue 首轮 false → 立即收口（引擎行为）
	r := s.WaitForCompletion()
	if r == nil || !r.Success {
		t.Fatalf("智能体驱动循环：%+v", r)
	}

	// Ralph 便捷启动 + 状态查询
	s2, err := integ.StartRalphLoop("任务", nil)
	if err != nil {
		t.Fatal(err)
	}
	st := integ.GetAgentExecutionStatus(s2.ID())
	if st.SessionID != s2.ID() || st.State == "NOT_FOUND" {
		t.Fatalf("状态：%+v", st)
	}
	s2.WaitForCompletion()

	// NOT_FOUND
	if st := integ.GetAgentExecutionStatus("nope"); st.State != "NOT_FOUND" {
		t.Fatalf("无效会话状态：%+v", st)
	}

	// UltraQA 便捷启动冒烟
	s3, _ := integ.StartUltraQALoop("qa", nil)
	integ.Stop(s3.ID())
	integ.Pause(s3.ID())  // 无效操作不炸
	integ.Resume(s3.ID())
}

// newFlowContext 真 flow.Context（vars 域）。
func newFlowContext(vars map[string]any) flow.Context {
	ctx := flow.NewContext("loop-test")
	for k, v := range vars {
		ctx.Put(k, v)
	}
	return ctx
}

func TestLoopFlowIntegration(t *testing.T) {
	e := loop.NewEngine(nil)
	integ := loop.NewFlowIntegration(e)

	// 变量提取（nil 桥 → 空）
	if vars := integ.Bridge().ExtractFlowVariables(); len(vars) != 0 {
		t.Fatalf("nil 桥变量：%v", vars)
	}
	fc := newFlowContext(map[string]any{"env": "prod", "retries": 3})
	integ.Bridge().Context = fc
	vars := integ.Bridge().ExtractFlowVariables()
	if vars["env"] != "prod" || vars["retries"] != 3 {
		t.Fatalf("变量提取：%v", vars)
	}

	// flowId 关联状态
	s, err := integ.StartFlowRalphLoop("flow-1", "流程任务", nil)
	if err != nil {
		t.Fatal(err)
	}
	st := integ.GetFlowExecutionStatus(s.ID())
	if st.FlowID != "flow-1" {
		t.Fatalf("flowId 状态：%+v", st)
	}
	s.WaitForCompletion()

	// flow 驱动管道（变量并入参数）
	team := loop.NewTeamStrategy()
	s2, err := integ.StartFlowDrivenPipeline("flow-2", team)
	if err != nil {
		t.Fatal(err)
	}
	ctxParams := s2.Context().Parameters
	if ctxParams["flowId"] != "flow-2" || ctxParams["env"] != "prod" {
		t.Fatalf("flow 变量并入：%v", ctxParams)
	}
	integ.Stop(s2.ID())

	// 回写（结果 nil 也不炸）
	if err := integ.WriteBackResult("nope", "k", "v"); err == nil {
		t.Fatal("无效会话回写应报错")
	}
	integ.Bridge().Context = nil // nil 桥 no-op
}

type stubHarnessCaller struct{}

func (stubHarnessCaller) CallPrompt(ctx context.Context, prompt string) (string, error) {
	return "harness 输出", nil
}

func TestLoopHarnessIntegration(t *testing.T) {
	e := loop.NewEngine(nil)
	integ := loop.NewHarnessIntegration(e)

	// 未注入文案
	if got := integ.Bridge().ExecuteWithHarness("x"); got != "Harness execution error: harness not injected" {
		t.Fatalf("未注入文案：%q", got)
	}
	// 注入执行
	integ.Bridge().Caller = stubHarnessCaller{}
	if got := integ.Bridge().ExecuteWithHarness("x"); got != "harness 输出" {
		t.Fatalf("执行输出：%q", got)
	}

	// 工具驱动 + 状态
	s, _ := integ.StartToolUltraQALoop("任务", nil)
	st := integ.GetToolExecutionStatus(s.ID())
	if !st.HarnessAvailable {
		t.Fatal("已注入 caller 应可用")
	}
	s.WaitForCompletion()

	// harness 驱动 UltraQA 启动冒烟（验证关、5 轮）
	s2, _ := integ.StartHarnessDrivenUltraQA("任务", stubHarnessCaller{})
	integ.Stop(s2.ID())
}
