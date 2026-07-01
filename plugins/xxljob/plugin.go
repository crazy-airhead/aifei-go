package xxljob

import (
	"github.com/crazy-airhead/aifei-go/aifei"
	"github.com/crazy-airhead/aifei-go/log"
)

// Compile-time assertion that Plugin satisfies aifei.Plugin.
var _ aifei.Plugin = (*Plugin)(nil)

// Plugin integrates xxl-job executor with the aifei framework. On Start it
// reads the "xxljob" subtree from the global config, creates an executor,
// initializes it (which starts the registry heartbeat), and installs it as the
// package-level default so that the top-level helpers work.
//
// On Stop it deregisters from the scheduling center and shuts down the HTTP
// server.
//
// Usage:
//
//	if err := config.Init(os.Args); err != nil { ... }
//	p, err := xxljob.NewPlugin(nil)
//	p.RegTask("myHandler", func(ctx context.Context, param *xxljob.RunReq) string { ... })
//	app := aifei.New(aifei.WithPlugin(p))
//	server.Run(app, ":8080")
type Plugin struct {
	prefix string
	log    log.Logger
	exec   Executor
}

// NewPlugin creates an xxl-job Plugin that reads its configuration from the
// global config under the given prefix (empty defaults to "xxljob"). A nil
// logger falls back to log.Default().
func NewPlugin(logger log.Logger, prefix ...string) (*Plugin, error) {
	p := "xxljob"
	if len(prefix) > 0 && prefix[0] != "" {
		p = prefix[0]
	}
	if logger == nil {
		logger = log.Default()
	}
	return &Plugin{prefix: p, log: logger}, nil
}

// Start loads the xxl-job config from the global config, creates and
// initializes the executor, and starts the background HTTP server.
func (p *Plugin) Start() error {
	cfg, err := LoadConfig(p.prefix)
	if err != nil {
		return err
	}

	opts := []Option{SetLogger(p.log)}
	opts = append(opts, cfg.toOptions()...)

	p.exec = NewExecutor(opts...)
	p.exec.Init()

	// Apply any pending task registrations
	for _, pt := range pendingTasks {
		p.exec.RegTask(pt.pattern, pt.task)
	}
	pendingTasks = nil

	SetDefault(p.exec)

	// Start the executor HTTP server in background
	if err := p.exec.Run(); err != nil {
		return err
	}

	p.log.Info("xxljob plugin started, registryKey=%s", cfg.RegistryKey)
	return nil
}

// Stop deregisters from the scheduling center and shuts down the HTTP server.
func (p *Plugin) Stop() error {
	if p.exec != nil {
		p.exec.Stop()
	}
	p.log.Info("xxljob plugin stopped")
	return nil
}

// Executor returns the underlying executor, or nil if Start has not run.
func (p *Plugin) Executor() Executor { return p.exec }

// RegTask registers a task handler on the plugin. Can be called before or
// after Start — if called before, the registration is deferred until Start.
func (p *Plugin) RegTask(pattern string, task TaskFunc) {
	if p.exec != nil {
		p.exec.RegTask(pattern, task)
		return
	}
	pendingTasks = append(pendingTasks, pendingTask{pattern, task})
}

type pendingTask struct {
	pattern string
	task    TaskFunc
}

var pendingTasks []pendingTask

// Use adds middleware to the plugin's executor.
func (p *Plugin) Use(middlewares ...Middleware) {
	if p.exec != nil {
		p.exec.Use(middlewares...)
	}
}

// LogHandler sets the log query handler on the plugin's executor.
func (p *Plugin) LogHandler(handler LogHandler) {
	if p.exec != nil {
		p.exec.LogHandler(handler)
	}
}
