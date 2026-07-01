// Package xxljob integrates xxl-job executor into the aifei framework
// as a standard aifei Plugin.
//
// It is a source-level port of github.com/xxl-job/xxl-job-executor-go,
// adapted to use aifei's config, log, and plugin systems.
//
// Quick start:
//
//	import "github.com/crazy-airhead/aifei-go/plugins/xxljob"
//
//	// In your app.yml:
//	//   xxljob:
//	//     serverAddr: "http://127.0.0.1:8080/xxl-job-admin"
//	//     registryKey: "my-app-jobs"
//
//	p, _ := xxljob.NewPlugin(nil)
//	p.RegTask("myHandler", func(ctx context.Context, param *xxljob.RunReq) string {
//	    return "done"
//	})
//	app := aifei.New(aifei.WithPlugin(p))
//	server.Run(app, ":8080")
package xxljob

import (
	"errors"
	"sync"
)

var (
	defaultExec Executor
	defaultMu   sync.RWMutex
)

// ErrNoDefault is returned by top-level helpers when no default executor has
// been installed.
var ErrNoDefault = errors.New("xxljob: no default executor configured")

// SetDefault installs exec as the package-level default used by the top-level
// helpers.
func SetDefault(exec Executor) {
	defaultMu.Lock()
	defaultExec = exec
	defaultMu.Unlock()
}

// DefaultExecutor returns the installed package-level default executor.
func DefaultExecutor() Executor {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultExec
}

// RegTask registers a task handler on the default executor.
func RegTask(pattern string, task TaskFunc) {
	exec := DefaultExecutor()
	if exec != nil {
		exec.RegTask(pattern, task)
	}
}
