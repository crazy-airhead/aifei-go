package flowplugin

import (
	"sync"

	"github.com/crazy-airhead/aifei-go/flow"
	"github.com/crazy-airhead/aifei-go/flow/workflow"
)

// Package-level defaults, set by Plugin.Start so business code can use
// flowplugin.DefaultEngine() / flowplugin.DefaultExecutor() without threading them.
var (
	defMu       sync.RWMutex
	defEngine   *flow.Engine
	defExecutor *workflow.Executor
)

func setDefault(engine *flow.Engine, exec *workflow.Executor) {
	defMu.Lock()
	defer defMu.Unlock()
	defEngine = engine
	defExecutor = exec
}

// DefaultEngine returns the engine of the last-started Plugin (nil if none).
func DefaultEngine() *flow.Engine {
	defMu.RLock()
	defer defMu.RUnlock()
	return defEngine
}

// DefaultExecutor returns the workflow executor of the last-started Plugin.
func DefaultExecutor() *workflow.Executor {
	defMu.RLock()
	defer defMu.RUnlock()
	return defExecutor
}
