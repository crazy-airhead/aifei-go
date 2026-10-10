// result.go 迭代与终态结果 + 循环上下文（对标 IterationResult/LoopResult/
// LoopContext）。
package loop

import "time"

// IterationResult 单次迭代结果。
type IterationResult struct {
	Number    int // 第几轮（1 起）
	Total     int // MaxIterations
	State     State // 策略建议态：EXECUTING/VERIFYING/FIXING/COMPLETED/FAILED
	Result    any
	Success   bool
	Message   string
	Duration  time.Duration
	StartTime time.Time
	EndTime   time.Time
	Metadata  map[string]any
}

// LoopResult 循环终态结果。
type LoopResult struct {
	SessionID           string
	FinalState          State
	Result              any
	Success             bool // FinalState == COMPLETED
	Message             string
	TotalDuration       time.Duration
	TotalIterations     int
	SuccessfulIterations int
	SuccessRate         float64
	StartTime, EndTime  time.Time
	IterationHistory    []IterationResult
	Metadata            map[string]any
}

// Context 循环上下文（对标 LoopContext；iterationCount 每轮真实递增——
// Java 恒 0 的缺陷已修）。
type Context struct {
	SessionID       string
	TaskDescription string
	CurrentState    State
	IterationCount  int
	MaxIterations   int
	StartTime       time.Time
	Parameters       map[string]any
	ContextData     map[string]any
}

// ExecutionTime 执行时长。
func (c *Context) ExecutionTime() time.Duration { return time.Since(c.StartTime) }

// IsMaxIterationsReached 保险丝。
func (c *Context) IsMaxIterationsReached() bool { return c.IterationCount >= c.MaxIterations }

// GetParameter 启动参数。
func (c *Context) GetParameter(k string) any { return c.Parameters[k] }

// Get 上下文数据。
func (c *Context) Get(k string) any { return c.ContextData[k] }

// Put 写上下文数据。
func (c *Context) Put(k string, v any) {
	if c.ContextData == nil {
		c.ContextData = map[string]any{}
	}
	c.ContextData[k] = v
}
