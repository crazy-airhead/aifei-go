package flowplugin

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/crazy-airhead/aifei-go/aifei"
	"github.com/crazy-airhead/aifei-go/db"
	"github.com/crazy-airhead/aifei-go/flow"
	"github.com/crazy-airhead/aifei-go/flow/workflow"
	"github.com/crazy-airhead/aifei-go/log"
)

// Graph DB source: the OA process-definition table and its deploy column.
// graph_bpmn is where the designer deploys to (PostDeploy / PostUploadAndDeploy);
// the engine-format graph JSON is stored/read there. Kept as constants
// (matching the reference schema); parametrize when needed.
const (
	GraphTable      = "oa_process"
	GraphBpmnColumn = "graph_bpmn"
)

var _ aifei.Plugin = (*Plugin)(nil)

// Plugin assembles a flow engine + workflow executor with the built-in MySQL state
// repository and task-history recorder, and loads graphs from configured URIs
// and/or the process-definition table (oa_process.graph_data).
// Implements aifei.Plugin.
type Plugin struct {
	log             log.Logger
	uris            []string // graph file paths (.yml/.yaml/.json)
	graphDB         bool     // load graph definitions from oa_process.graph_data
	repoEnabled     bool
	controller      workflow.StateController
	recorderEnabled bool

	engine     *flow.Engine
	executor   *workflow.Executor
	container  *flow.MapContainer
	repo       workflow.StateRepository
	mysqlRepo  *MysqlStateRepository
	recorder   *TaskHistoryRecorder
	graphTexts map[string]string // graphID -> source JSON (instance snapshots)
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

// WithGraphDB loads graph definitions from the process-definition table's
// deploy column (default oa_process.graph_bpmn) on Start: every valid row is
// parsed with flow.GraphFromText. Rows that don't parse (e.g. legacy Flowable
// BPMN XML) are skipped with a warning instead of failing Start. Loaded graph
// texts are kept for instance snapshots (bpm_flow_repository.graph).
func WithGraphDB() PluginOption { return func(p *Plugin) { p.graphDB = true } }

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
	p.graphTexts = map[string]string{}

	if p.repoEnabled {
		p.mysqlRepo = NewMysqlStateRepository(p.log)
		p.mysqlRepo.SetGraphTexts(p.graphTexts)
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
	if p.graphDB {
		if err := p.loadGraphsFromDB(); err != nil {
			return err
		}
	}

	setDefault(p.engine, p.executor, p.mysqlRepo, p.recorder)
	p.log.Info("flow plugin started, mysql=%v history=%v graphs=%d", p.repoEnabled, p.recorderEnabled, len(p.graphTexts))
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
	p.graphTexts[g.GetID()] = string(data)
	return nil
}

// loadGraphsFromDB loads graph definitions from the process-definition table's
// deploy column (graph_bpmn): rows with valid=1 and a non-empty graph_bpmn are
// parsed and registered. Rows that don't parse into a graph with an id and
// nodes (legacy Flowable BPMN XML / old designer exports) are skipped with a
// warning — they must be migrated to the engine format separately.
func (p *Plugin) loadGraphsFromDB() error {
	rows, err := db.FindBy(GraphTable, "valid", 1)
	if err != nil {
		return err
	}
	loaded, skipped := 0, 0
	for _, row := range rows {
		text := row.GetStr(GraphBpmnColumn)
		if text == "" {
			continue
		}
		g, err := flow.GraphFromText(text)
		// an engine-format graph always carries an id (processKey) and nodes
		if err != nil || g.GetID() == "" || len(g.GetNodes()) == 0 {
			skipped++
			continue
		}
		p.engine.Load(g)
		p.graphTexts[g.GetID()] = text
		loaded++
	}
	if skipped > 0 {
		p.log.Warn("flow graph db: skipped %d %s rows not in engine format (legacy bpmn/designer data)", skipped, GraphBpmnColumn)
	}
	p.log.Info("flow graph db: loaded %d graphs from %s.%s", loaded, GraphTable, GraphBpmnColumn)
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
