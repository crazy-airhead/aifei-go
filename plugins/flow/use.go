package flowplugin

import (
	"sync"

	"github.com/crazy-airhead/aifei-go/flow"
	"github.com/crazy-airhead/aifei-go/flow/workflow"
)

// Package-level defaults, set by Plugin.Start so business code can use
// flowplugin.DefaultEngine() / flowplugin.DefaultExecutor() / DefaultMysqlRepo()
// / DefaultRecorder() without threading them.
var (
	defMu       sync.RWMutex
	defEngine   *flow.Engine
	defExecutor *workflow.Executor
	defRepo     *MysqlStateRepository
	defRecorder *TaskHistoryRecorder
)

func setDefault(engine *flow.Engine, exec *workflow.Executor, repo *MysqlStateRepository, recorder *TaskHistoryRecorder) {
	defMu.Lock()
	defer defMu.Unlock()
	defEngine = engine
	defExecutor = exec
	defRepo = repo
	defRecorder = recorder
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

// DefaultMysqlRepo returns the MySQL state repository of the last-started Plugin
// (nil when MySQL persistence is disabled — LoadCtx/SaveCtx are unavailable then).
func DefaultMysqlRepo() *MysqlStateRepository {
	defMu.RLock()
	defer defMu.RUnlock()
	return defRepo
}

// DefaultRecorder returns the task-history recorder of the last-started Plugin
// (nil when history recording is disabled).
func DefaultRecorder() *TaskHistoryRecorder {
	defMu.RLock()
	defer defMu.RUnlock()
	return defRecorder
}
