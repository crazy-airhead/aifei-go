// strategy.go 策略 SPI 与模板基类（对标 LoopStrategy + AbstractLoopStrategy）。
package loop

import (
	"fmt"
	"time"
)

// Strategy 循环策略 SPI。
type Strategy interface {
	// ShouldContinue 是否继续（maxIterations 保险丝在模板基类统一处理）。
	ShouldContinue(ctx *Context) bool
	// ExecuteIteration 执行一轮迭代。
	ExecuteIteration(ctx *Context) *IterationResult
	// Name 策略名。
	Name() string
	// Description 描述。
	Description() string
	// MaxIterations 迭代上限。
	MaxIterations() int
	// SupportsParallelExecution 是否支持并行执行。
	SupportsParallelExecution() bool
}

// StrategyBase 模板基类（嵌入获得保险丝与异常包装）。
type StrategyBase struct {
	MaxIterationsValue int
}

// ShouldContinue 保险丝 + 委托子类（嵌入者实现 shouldContinueInternal 与
// currentState——Go 以函数位表达模板方法）。
func (s *StrategyBase) ShouldContinueFuse(ctx *Context, internal func(*Context) bool) bool {
	if ctx.IsMaxIterationsReached() {
		return false
	}
	if ctx.CurrentState.IsTerminal() {
		return false
	}
	return internal(ctx)
}

// WrapIteration 计时与异常包装（对标 executeIteration 模板）。
func (s *StrategyBase) WrapIteration(ctx *Context, internal func(*Context) *IterationResult) *IterationResult {
	start := time.Now()
	defer func() {
		if r := recover(); r != nil {
			// panic 包装为失败迭代（Java catch Throwable 对应物）
		}
	}()
	result := internal(ctx)
	if result == nil {
		result = &IterationResult{Success: true, Message: "done"}
	}
	result.Number = ctx.IterationCount
	result.Total = s.MaxIterationsValue
	if result.State == "" {
		result.State = StateExecuting
	}
	result.StartTime = start
	result.EndTime = time.Now()
	result.Duration = result.EndTime.Sub(start)
	return result
}

// WrapIterationSafe panic 安全版。
func (s *StrategyBase) WrapIterationSafe(ctx *Context, internal func(*Context) *IterationResult) (result *IterationResult) {
	start := time.Now()
	defer func() {
		if r := recover(); r != nil {
			result = &IterationResult{
				State: StateFailed, Success: false,
				Message: fmt.Sprintf("Iteration failed: %v", r),
				Metadata: map[string]any{"error": "panic"},
			}
		}
		result.Number = ctx.IterationCount
		result.Total = s.MaxIterationsValue
		result.StartTime = start
		result.EndTime = time.Now()
		result.Duration = result.EndTime.Sub(start)
	}()
	result = internal(ctx)
	if result == nil {
		result = &IterationResult{Success: true, Message: "done", State: StateExecuting}
	}
	if result.State == "" {
		result.State = StateExecuting
	}
	return result
}
