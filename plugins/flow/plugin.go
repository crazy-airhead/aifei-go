package flowplugin

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/crazy-airhead/aifei-go/aifei"
	"github.com/crazy-airhead/aifei-go/flow"
	"github.com/crazy-airhead/aifei-go/flow/workflow"
	"github.com/crazy-airhead/aifei-go/log"
)

var _ aifei.Plugin = (*Plugin)(nil)

// Plugin assembles a flow engine + workflow executor with the built-in MySQL state
// repository and task-history recorder, and loads graphs from configured URIs.
// Implements aifei.Plugin.
type Plugin struct {
	log             log.Logger
	uris            []string // graph file paths (.yml/.yaml/.json)
	repoEnabled     bool
	controller      workflow.StateController
	recorderEnabled bool

	engine    *flow.Engine
	executor  *workflow.Executor
	container *flow.MapContainer
	repo      workflow.StateRepository
	mysqlRepo *MysqlStateRepository
	recorder  *TaskHistoryRecorder
}

// PluginOption configures the Plugin.
type PluginOption func(*Plugin)

// WithGraphURIs adds graph file paths to load on Start.
func WithGraphURIs(uris ...string) PluginOption {
	return func(p *Plugin) { p.uris = append(p.uris, uris...) }
}

// WithGraphDir adds all .yml/.yaml/.json graphs in a directory.
func WithGraphDir(dir string) PluginOption {
	return func(p *Plugin) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if ext == ".yml" || ext == ".yaml" || ext == ".json" {
				p.uris = append(p.uris, filepath.Join(dir, e.Name()))
			}
		}
	}
}

// WithMySQL enables the MySQL state repository (default InMemory).
func WithMySQL() PluginOption { return func(p *Plugin) { p.repoEnabled = true } }

// WithRecordHistory enables the bpm_flow_task history recorder.
func WithRecordHistory() PluginOption { return func(p *Plugin) { p.recorderEnabled = true } }

// WithStateController overrides the state controller (default BlockStateController).
func WithStateController(c workflow.StateController) PluginOption {
	return func(p *Plugin) { p.controller = c }
}

// NewPlugin builds the Plugin (call before Start; register components on Container()).
func NewPlugin(logger log.Logger, opts ...PluginOption) *Plugin {
	if logger == nil {
		logger = log.Default()
	}
	p := &Plugin{log: logger, controller: workflow.NewBlockStateController()}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Start builds the engine, repository, executor, and loads graphs.
func (p *Plugin) Start() error {
	p.container = flow.NewMapContainer()
	p.engine = flow.NewEngine(flow.NewSimpleDriver(flow.WithContainer(p.container)))

	if p.repoEnabled {
		p.mysqlRepo = NewMysqlStateRepository(p.log)
		p.repo = p.mysqlRepo
	} else {
		p.repo = workflow.NewInMemoryStateRepository()
	}
	if p.recorderEnabled {
		p.recorder = NewTaskHistoryRecorder(p.log)
	}
	p.executor = workflow.NewExecutor(p.engine, p.controller, p.repo)

	for _, uri := range p.uris {
		if err := p.loadGraph(uri); err != nil {
			return err
		}
	}

	setDefault(p.engine, p.executor)
	p.log.Info("flow plugin started, mysql=%v history=%v graphs=%d", p.repoEnabled, p.recorderEnabled, len(p.uris))
	return nil
}

// Stop is a no-op (the engine is stateless; db outlives the plugin).
func (p *Plugin) Stop() error { return nil }

func (p *Plugin) loadGraph(uri string) error {
	data, err := os.ReadFile(uri)
	if err != nil {
		return err
	}
	g, err := flow.GraphFromText(string(data))
	if err != nil {
		return err
	}
	p.engine.Load(g)
	return nil
}

// Engine returns the assembled engine (nil before Start).
func (p *Plugin) Engine() *flow.Engine { return p.engine }

// Executor returns the assembled workflow executor (nil before Start).
func (p *Plugin) Executor() *workflow.Executor { return p.executor }

// Container returns the component container (register @components here).
func (p *Plugin) Container() *flow.MapContainer { return p.container }

// MysqlRepo returns the MySQL state repository (nil when MySQL is disabled).
func (p *Plugin) MysqlRepo() *MysqlStateRepository { return p.mysqlRepo }

// Recorder returns the task-history recorder (nil when history is disabled).
func (p *Plugin) Recorder() *TaskHistoryRecorder { return p.recorder }
