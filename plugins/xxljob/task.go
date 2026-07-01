package xxljob

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/crazy-airhead/aifei-go/log"
)

// TaskFunc is the task execution function.
type TaskFunc func(cxt context.Context, param *RunReq) string

// Task represents a registered task.
type Task struct {
	Id        int64
	Name      string
	Ext       context.Context
	Param     *RunReq
	fn        TaskFunc
	Cancel    context.CancelFunc
	StartTime int64
	EndTime   int64
	log       log.Logger
}

// Run executes the task and calls callback with the result.
func (t *Task) Run(callback func(code int64, msg string)) {
	defer func(cancel func()) {
		if err := recover(); err != nil {
			t.log.Info(t.Info()+" panic: %v", err)
			debug.PrintStack()
			callback(FailureCode, fmt.Sprintf("task panic:%v", err))
			cancel()
		}
	}(t.Cancel)
	msg := t.fn(t.Ext, t.Param)
	callback(SuccessCode, msg)
}

// Info returns task info string.
func (t *Task) Info() string {
	return fmt.Sprintf("Task ID[%d] Name[%s] Params:%s", t.Id, t.Name, t.Param.ExecutorParams)
}
