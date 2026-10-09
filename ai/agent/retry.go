package agent

import (
	"context"
	"math/rand"
	"time"
)

// RetryTask 指数退避重试（对标 solon-ai-core util.RetryTask，改为 ctx 感知）：
// 全抖动延迟 random[0, min(initial*2^i, max)]；ctx 取消视同 Java 线程中断，
// 立即以 ctx.Err() 返回；RetryIf 返回 false 时立即以原始错误返回。
// OnRetry 在每次失败后、下一次重试前回调（attempt 1 基；最后一次尝试失败
// 不回调——Java onRetry(i<maxRetries-1) 同款边界，SimpleAgent 的
// attempt==maxRetries 分支在 Java 即不可达死代码，Go 不复刻）。
type RetryTask struct {
	MaxRetries     int
	InitialDelayMs int64
	MaxDelayMs     int64
	RetryIf        func(err error) bool
	OnRetry        func(attempt int, err error)
}

// NewRetryTask 默认参数（3 次 / 1000ms 起 / 30000ms 封顶）。
func NewRetryTask() *RetryTask {
	return &RetryTask{MaxRetries: 3, InitialDelayMs: 1000, MaxDelayMs: 30000}
}

// Run 执行 fn 直至成功、不可重试或耗尽次数；耗尽后返回最后一次错误。
func (t *RetryTask) Run[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	var zero T
	lastErr := error(nil)
	for i := 0; i < t.MaxRetries; i++ {
		v, err := fn()
		if err == nil {
			return v, nil
		}
		lastErr = err

		if ctx != nil && ctx.Err() != nil {
			return zero, ctx.Err()
		}
		if t.RetryIf != nil && !t.RetryIf(err) {
			return zero, err
		}
		if i < t.MaxRetries-1 {
			if t.OnRetry != nil {
				t.OnRetry(i+1, err)
			}
			if !sleepCtx(ctx, t.delayOf(i)) {
				return zero, ctx.Err()
			}
		}
	}
	return zero, lastErr
}

// delayOf 第 i 次（0 基）失败后的全抖动延迟。
func (t *RetryTask) delayOf(i int) time.Duration {
	delay := t.InitialDelayMs << i
	if delay <= 0 || delay > t.MaxDelayMs {
		delay = t.MaxDelayMs
	}
	return time.Duration(rand.Int63n(delay+1)) * time.Millisecond
}

// sleepCtx 等待 d 或 ctx 取消；返回 false 表示被取消。
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if ctx == nil {
		time.Sleep(d)
		return true
	}
	if ctx.Err() != nil {
		return false
	}
	tm := time.NewTimer(d)
	defer tm.Stop()
	select {
	case <-tm.C:
		return true
	case <-ctx.Done():
		return false
	}
}
