// simple_stream_test.go P2-a 流式：SimpleStart/SimpleDelta 转发（TEXT 载荷）、
// SimpleEnd 终态聚合、消费方取消（无 SimpleEnd）、StreamChan、团队流出口继承。
package ai_agent_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/ai/agent/simple"
	"github.com/crazy-airhead/aifei-go/flow"
)

// collectText 拼接 SimpleDelta 转发的 TEXT_DELTA 载荷。
func collectText(evs []*agent.Event) string {
	var sb strings.Builder
	for _, ev := range evs {
		if ev.ChatEvent != nil && ev.ChatEvent.Type == ai.EventTextDelta {
			sb.WriteString(ev.ChatEvent.Text)
		}
	}
	return sb.String()
}

func TestSimpleStreamForwardAndAggregate(t *testing.T) {
	srv := newScripted(t, scriptedResp{sse: true, body: chatSSE("你好")})
	a, err := simple.NewModelBuilder(agentModel(t, srv)).Name("s1").Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	rec := &eventRecorder{}
	if err := a.Prompt("嗨").Stream(context.Background(), rec); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	starts := rec.of(agent.KindSimpleStart)
	deltas := rec.of(agent.KindSimpleDelta)
	ends := rec.of(agent.KindSimpleEnd)
	if len(starts) != 1 || len(ends) != 1 {
		t.Fatalf("SimpleStart/SimpleEnd = %d/%d", len(starts), len(ends))
	}
	if len(deltas) != 2 { // openai 管线只发 TEXT_DELTA（无 START/END 帧），两段文本
		t.Fatalf("SimpleDelta = %d 条", len(deltas))
	}
	if got := collectText(deltas); got != "你好" {
		t.Fatalf("TEXT_DELTA 聚合 = %q", got)
	}
	// 终态聚合：SimpleEnd 携带完整消息
	if ends[0].Message == nil || ends[0].Message.Text != "你好" {
		t.Fatalf("SimpleEnd 消息 = %+v", ends[0].Message)
	}
	if ends[0].AgentName != "s1" || ends[0].RunID != starts[0].RunID {
		t.Fatalf("RunID/AgentName 不一致: %+v vs %+v", ends[0], starts[0])
	}
}

func TestSimpleStreamCancelledNoSimpleEnd(t *testing.T) {
	srv := newScripted(t, scriptedResp{sse: true, body: chatSSE("你好呀")})
	a, err := simple.NewModelBuilder(agentModel(t, srv)).Name("s2").Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	rec := &eventRecorder{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelling := agent.EventSinkFunc(func(ev *agent.Event) error {
		_ = rec.OnEvent(ev)
		if ev.Kind == agent.KindSimpleDelta { // 首个增量后取消
			cancel()
		}
		return nil
	})

	if err := a.Prompt("嗨").Stream(ctx, cancelling); err != nil {
		t.Fatalf("Stream（取消应视同正常收尾）: %v", err)
	}
	if len(rec.of(agent.KindSimpleEnd)) != 0 {
		t.Fatalf("取消后不应补发 SimpleEnd")
	}
	if len(rec.of(agent.KindSimpleStart)) != 1 {
		t.Fatalf("SimpleStart 应已发出")
	}
}

func TestSimpleStreamChan(t *testing.T) {
	srv := newScripted(t, scriptedResp{sse: true, body: chatSSE("流式通道")})
	a, err := simple.NewModelBuilder(agentModel(t, srv)).Name("s3").Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ch, err := a.Prompt("嗨").StreamChan(context.Background())
	if err != nil {
		t.Fatalf("StreamChan: %v", err)
	}
	var got []*agent.Event
	for ev := range ch {
		got = append(got, ev)
	}
	var deltas, ends int
	for _, ev := range got {
		switch ev.Kind {
		case agent.KindSimpleDelta:
			deltas++
		case agent.KindSimpleEnd:
			ends++
		}
	}
	if deltas != 2 || ends != 1 {
		t.Fatalf("deltas=%d ends=%d（通道事件 = %d 条）", deltas, ends, len(got))
	}
	if got[len(got)-1].Kind != agent.KindSimpleEnd {
		t.Fatalf("末条事件应为 SimpleEnd: %+v", got[len(got)-1])
	}
}

func TestSimpleStreamSinkErrorSwallowed(t *testing.T) {
	// 出口报错被吞掉（事件通道不影响主流程）；Handler 智能体零 HTTP
	a, err := simple.NewBuilder().Name("s5").
		Handler(func(p *ai.Prompt, s agent.Session) (*ai.AssistantMessage, error) {
			return &ai.AssistantMessage{Text: "答"}, nil
		}).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	failing := agent.EventSinkFunc(func(*agent.Event) error { return errors.New("sink down") })
	if err := a.Prompt("问").Stream(context.Background(), failing); err != nil {
		t.Fatalf("出口报错应被吞掉: %v", err)
	}
}

func TestSimpleStreamTeamSinkInherited(t *testing.T) {
	// 团队流出口继承：普通 Call 也走流式路径（父轨迹 HasStreamSink）
	srv := newScripted(t, scriptedResp{sse: true, body: chatSSE("团队流式")})
	a, err := simple.NewModelBuilder(agentModel(t, srv)).Name("s4").Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	rec := &eventRecorder{}
	trace := &teamTraceStub{runID: "team-run", metrics: &agent.Metrics{}, sink: rec}
	ctx := flow.NewContext("g")
	ctx.Put(agent.KeyCurrentTeamTraceKey, "team")
	ctx.Put("team", agent.TeamTrace(trace))
	sess := agent.NewMemorySessionOfContext(ctx)

	resp, err := a.Prompt("团队问题").Session(sess).Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Content() != "团队流式" {
		t.Fatalf("Content = %q", resp.Content())
	}
	if len(rec.of(agent.KindSimpleStart)) != 1 || len(rec.of(agent.KindSimpleDelta)) != 2 {
		t.Fatalf("团队流出口继承失败: start=%d delta=%d",
			len(rec.of(agent.KindSimpleStart)), len(rec.of(agent.KindSimpleDelta)))
	}
}
