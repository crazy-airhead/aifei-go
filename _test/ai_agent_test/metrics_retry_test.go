// metrics_retry_test.go P2-a 指标/轨迹/重试单测：AddUsage/AddMetrics（不含耗时）、
// DeltaSince、Snapshot 隔离、CacheRate 钳制、NewTrace 惰性 RunID、
// RetryTask 成功/谓词/耗尽/取消。
package ai_agent_test

import (
	"context"
	"errors"
	"testing"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/ai/agent/simple"
)

func TestMetricsUsageDeltaAndAggregation(t *testing.T) {
	m := &agent.Metrics{}
	baseline := m.Snapshot()

	m.AddUsage(&ai.Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5})
	m.AddTotalDuration(7)

	d := m.DeltaSince(baseline)
	if d.PromptTokens() != 3 || d.CompletionTokens() != 2 || d.TotalTokens() != 5 {
		t.Fatalf("增量 = %d/%d/%d", d.PromptTokens(), d.CompletionTokens(), d.TotalTokens())
	}
	if d.TotalDuration() != 7 {
		t.Fatalf("增量耗时应参与差值计算: %d", d.TotalDuration())
	}

	// 汇总到父轨迹：不含耗时
	parent := &agent.Metrics{}
	parent.AddMetrics(m)
	if parent.PromptTokens() != 3 || parent.TotalTokens() != 5 {
		t.Fatalf("父轨迹用量 = %d/%d", parent.PromptTokens(), parent.TotalTokens())
	}
	if parent.TotalDuration() != 0 {
		t.Fatalf("AddMetrics 不应汇总耗时: %d", parent.TotalDuration())
	}
}

func TestMetricsSnapshotIsolation(t *testing.T) {
	m := &agent.Metrics{}
	snap := m.Snapshot()
	m.AddUsage(&ai.Usage{PromptTokens: 9})
	if snap.PromptTokens() != 0 {
		t.Fatalf("快照应隔离后续变更: %d", snap.PromptTokens())
	}
	m.Reset()
	if m.PromptTokens() != 0 || m.TotalDuration() != 0 {
		t.Fatalf("Reset 未清零")
	}
}

func TestMetricsCacheRate(t *testing.T) {
	m := &agent.Metrics{}
	if m.CacheRate() != 0 {
		t.Fatalf("无 prompt 令牌应为 0")
	}
	m.AddUsage(&ai.Usage{PromptTokens: 100, CacheReadInputTokens: 33})
	if r := m.CacheRate(); r != 33 {
		t.Fatalf("CacheRate = %v", r)
	}
	m.AddUsage(&ai.Usage{CacheReadInputTokens: 200}) // 超限钳到 100
	if r := m.CacheRate(); r != 100 {
		t.Fatalf("钳制后 CacheRate = %v", r)
	}
}

func TestTraceLazyRunID(t *testing.T) {
	if tr := simple.NewTrace(); tr.RunID() == "" {
		t.Fatalf("RunID 应惰性生成")
	}
}

func TestRetryTaskAttemptsAndPredicates(t *testing.T) {
	// 第二次成功
	task := &agent.RetryTask{MaxRetries: 3, InitialDelayMs: 1, MaxDelayMs: 1}
	calls := 0
	v, err := task.Run(context.Background(), func() (string, error) {
		calls++
		if calls < 2 {
			return "", errors.New("boom")
		}
		return "ok", nil
	})
	if err != nil || v != "ok" || calls != 2 {
		t.Fatalf("v=%q err=%v calls=%d", v, err, calls)
	}

	// RetryIf=false：不重试，返回原始错误
	task = &agent.RetryTask{MaxRetries: 3, InitialDelayMs: 1, MaxDelayMs: 1,
		RetryIf: func(error) bool { return false }}
	calls = 0
	_, err = task.Run(context.Background(), func() (string, error) {
		calls++
		return "", errors.New("business")
	})
	if calls != 1 || err == nil || err.Error() != "business" {
		t.Fatalf("calls=%d err=%v", calls, err)
	}

	// OnRetry 边界：每次失败后、下一次前回调；最后一次失败不回调
	var attempts []int
	task = &agent.RetryTask{MaxRetries: 3, InitialDelayMs: 1, MaxDelayMs: 1,
		OnRetry: func(attempt int, err error) { attempts = append(attempts, attempt) }}
	calls = 0
	_, err = task.Run(context.Background(), func() (string, error) {
		calls++
		return "", errors.New("always")
	})
	if calls != 3 || err == nil || err.Error() != "always" {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	if len(attempts) != 2 || attempts[0] != 1 || attempts[1] != 2 {
		t.Fatalf("OnRetry 回调 = %v", attempts)
	}

	// ctx 取消：中止重试
	ctx, cancel := context.WithCancel(context.Background())
	task = &agent.RetryTask{MaxRetries: 3, InitialDelayMs: 1, MaxDelayMs: 1}
	calls = 0
	_, err = task.Run(ctx, func() (string, error) {
		calls++
		cancel()
		return "", errors.New("boom")
	})
	if calls != 1 || !errors.Is(err, context.Canceled) {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}
