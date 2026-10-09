// session_test.go P2-a 会话与挂起：system 过滤、_SESSION_ 自动装载、
// Pending 状态机（Stop+SetStopped / 清标记）、MemorySessionOfContext 共享。
package ai_agent_test

import (
	"testing"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/flow"
)

func TestSessionAddMessageFiltersSystem(t *testing.T) {
	s := agent.NewMemorySession("s1")
	s.AddMessage(ai.OfSystem("系统提示"), ai.OfUser("问题"))
	s.AddMessage(ai.OfSystem("又一条系统"))

	msgs := s.Messages()
	if len(msgs) != 1 || msgs[0].Role != ai.RoleUser {
		t.Fatalf("消息 = %+v（system 应被过滤）", msgs)
	}
}

func TestSessionAutoPutIntoSnapshot(t *testing.T) {
	s := agent.NewMemorySession("s1")
	if got := s.Context().Get(agent.KeySession); got != agent.Session(s) {
		t.Fatalf("快照未自动装载 _SESSION_: %v", got)
	}

	ctx := flow.NewContext("graph")
	s2 := agent.NewMemorySessionOfContext(ctx)
	if s2.Context() != ctx {
		t.Fatalf("OfContext 未共享传入上下文")
	}
	if got := ctx.Get(agent.KeySession); got != agent.Session(s2) {
		t.Fatalf("图 ctx 未装载 _SESSION_: %v", got)
	}
}

func TestSessionPendingLifecycle(t *testing.T) {
	s := agent.NewMemorySession("s1")

	if s.IsPending() {
		t.Fatalf("初始不应挂起")
	}

	s.Pending(true, "等待人工审批")
	if !s.IsPending() || !s.Context().IsStopped() {
		t.Fatalf("挂起后 IsPending/IsStopped 应为 true")
	}
	if s.PendingReason() != "等待人工审批" {
		t.Fatalf("挂起原因 = %q", s.PendingReason())
	}

	// 恢复：清挂起标记（SetStopped(false)，Java stopped(false) 同款）
	s.Pending(false, "")
	if s.IsPending() || s.Context().IsStopped() {
		t.Fatalf("恢复后 IsPending 应为 false")
	}
	if s.PendingReason() != "" {
		t.Fatalf("恢复后原因应清除，得到 %q", s.PendingReason())
	}

	// 换原因挂起
	s.Pending(true, "二次确认")
	if s.PendingReason() != "二次确认" {
		t.Fatalf("换原因挂起 = %q", s.PendingReason())
	}
}

func TestSessionPendingStopsBoundGraphContext(t *testing.T) {
	// 会话绑定图 ctx：挂起必须让图引擎可感知（IsStopped）
	ctx := flow.NewContext("g")
	s := agent.NewMemorySessionOfContext(ctx)
	s.Pending(true, "hitl")
	if !ctx.IsStopped() {
		t.Fatalf("图 ctx 应随会话挂起停止")
	}
	s.Pending(false, "")
	if ctx.IsStopped() {
		t.Fatalf("图 ctx 应随会话恢复解除停止")
	}
}

func TestSessionOfContextUnlimitedWindow(t *testing.T) {
	ctx := flow.NewContext("g")
	s := agent.NewMemorySessionOfContext(ctx)
	for i := 0; i < 60; i++ {
		s.AddMessage(ai.OfUser("q"))
	}
	if len(s.Messages()) != 60 {
		t.Fatalf("of(context) 会话容量应不限，得到 %d 条", len(s.Messages()))
	}
}
