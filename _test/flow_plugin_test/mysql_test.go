package flow_plugin_test

import (
	"os"
	"testing"

	"github.com/crazy-airhead/aifei-go/db"
	flow "github.com/crazy-airhead/aifei-go/flow"
	"github.com/crazy-airhead/aifei-go/flow/workflow"
	flowplugin "github.com/crazy-airhead/aifei-go/plugins/flow"
)

// Compile-time check: MysqlStateRepository satisfies the workflow.StateRepository
// interface (so it can be passed to workflow.NewExecutor).
var _ workflow.StateRepository = (*flowplugin.MysqlStateRepository)(nil)

// memStore is a fake instance->states/vars store backing PersistFunc/LoadFunc.
type memStore struct {
	data map[string]map[string]int
	vars map[string]map[string]any
}

func newMemStore() *memStore {
	return &memStore{
		data: map[string]map[string]int{},
		vars: map[string]map[string]any{},
	}
}

func (s *memStore) persist(_ flow.Context, id string, states map[string]int, vars map[string]any, _ bool) error {
	cp := map[string]int{}
	for k, v := range states {
		cp[k] = v
	}
	s.data[id] = cp
	vcp := map[string]any{}
	for k, v := range vars {
		vcp[k] = v
	}
	s.vars[id] = vcp
	return nil
}

func (s *memStore) load(_ flow.Context, id string) (map[string]int, map[string]any, error) {
	return s.data[id], s.vars[id], nil
}

func buildGraph(t *testing.T) (*flow.Graph, *flow.Node) {
	t.Helper()
	g, err := flow.Create("g", func(s *flow.GraphSpec) {
		s.AddStart("s").LinkAdd("a")
		s.AddActivity("a").LinkAdd("e")
		s.AddEnd("e")
	})
	if err != nil {
		t.Fatal(err)
	}
	return g, g.GetNode("a")
}

// TestStateKeyAndCodec: the state key is "graphId:nodeId" and states round-trip.
func TestStateKeyAndRoundTrip(t *testing.T) {
	_, a := buildGraph(t)
	store := newMemStore()

	repo1 := flowplugin.NewMysqlStateRepository(nil)
	repo1.SetStatePersisters(store.persist, store.load)
	ctx := flow.NewContext("inst1")

	repo1.StatePut(ctx, a, workflow.TaskStateWaiting)

	// persisted key shape
	states := store.data["inst1"]
	if _, ok := states["g:a"]; !ok {
		t.Errorf("persisted states = %v, want key g:a", states)
	}

	// persisted vars snapshot (SaveCtx filters transient keys)
	ctx.Put("amount", 42)
	if err := repo1.SaveCtx(ctx); err != nil {
		t.Fatal(err)
	}
	if got := store.vars["inst1"]["amount"]; got != 42 {
		t.Errorf("persisted vars = %v, want amount=42", store.vars["inst1"])
	}

	// a fresh repo (same store) loads it back via lazy load
	repo2 := flowplugin.NewMysqlStateRepository(nil)
	repo2.SetStatePersisters(store.persist, store.load)
	if got := repo2.StateGet(ctx, a); got != workflow.TaskStateWaiting {
		t.Errorf("StateGet = %v, want WAITING", got)
	}
	if got := repo2.VarsGet(ctx, a)["amount"]; got != 42 {
		t.Errorf("VarsGet amount = %v, want 42 (vars snapshot restored)", got)
	}
}

// TestStateRemoveClear: remove and clear propagate to persistence.
func TestStateRemoveClear(t *testing.T) {
	_, a := buildGraph(t)
	store := newMemStore()
	repo := flowplugin.NewMysqlStateRepository(nil)
	repo.SetStatePersisters(store.persist, store.load)
	ctx := flow.NewContext("inst2")

	repo.StatePut(ctx, a, workflow.TaskStateCompleted)
	repo.StateRemove(ctx, a)
	if got := repo.StateGet(ctx, a); got != workflow.TaskStateUnknown {
		t.Errorf("after remove = %v, want UNKNOWN", got)
	}

	repo.StatePut(ctx, a, workflow.TaskStateWaiting)
	repo.StateClear(ctx)
	if got := repo.StateGet(ctx, a); got != workflow.TaskStateUnknown {
		t.Errorf("after clear = %v, want UNKNOWN", got)
	}
}

// TestStateCacheLazyLoad: reads after the first are served from cache (no extra loads).
func TestStateCacheLazyLoad(t *testing.T) {
	_, a := buildGraph(t)
	loads := 0
	store := newMemStore()
	load := func(c flow.Context, id string) (map[string]int, map[string]any, error) {
		loads++
		return store.load(c, id)
	}
	repo := flowplugin.NewMysqlStateRepository(nil)
	repo.SetStatePersisters(store.persist, load)
	ctx := flow.NewContext("inst3")

	repo.StatePut(ctx, a, workflow.TaskStateWaiting)
	for i := 0; i < 5; i++ {
		_ = repo.StateGet(ctx, a)
	}
	if loads != 1 {
		t.Errorf("load called %d times, want 1 (lazy load + cache)", loads)
	}
}

// TestTaskHistoryRecorder: the recorder invokes the configured insert func.
func TestTaskHistoryRecorder(t *testing.T) {
	g, a := buildGraph(t)
	var recorded flowplugin.TaskRecord
	rec := flowplugin.NewTaskHistoryRecorder(nil)
	rec.SetInserter(func(ctx flow.Context, r flowplugin.TaskRecord) error {
		recorded = r
		return nil
	})
	ctx := flow.NewContext("inst4")
	if err := rec.Record(ctx, flowplugin.TaskRecord{
		GraphID: g.GetID(), TaskID: a.ID(), Source: a, Target: a,
		Assignee: "alice", State: workflow.TaskStateCompleted, Message: "ok",
	}); err != nil {
		t.Fatal(err)
	}
	if recorded.Assignee != "alice" || recorded.State != workflow.TaskStateCompleted {
		t.Errorf("recorded = %+v", recorded)
	}
}

// TestMysqlStateRepository_Integration is a REAL MySQL round-trip. It skips unless
// FLOW_MYSQL_DSN is set and a "mysql" driver is registered. To run:
//
//	import _ "github.com/go-sql-driver/mysql"  // in a tagged file or main
//	FLOW_MYSQL_DSN=user:pass@tcp(127.0.0.1:3306)/testdb go test ./_test/flow_plugin_test
func TestMysqlStateRepository_Integration(t *testing.T) {
	dsn := os.Getenv("FLOW_MYSQL_DSN")
	if dsn == "" {
		t.Skip("FLOW_MYSQL_DSN not set; skipping MySQL integration test")
	}
	if err := db.Init("mysql", dsn); err != nil {
		t.Skipf("mysql init failed (import a mysql driver to run): %v", err)
	}
	pool, err := db.GetConfig().Pool()
	if err != nil {
		t.Fatal(err)
	}
	// ensure the table exists (subset of the reference DDL, IF NOT EXISTS)
	const ddl = `CREATE TABLE IF NOT EXISTS bpm_flow_repository (
		id BIGINT NOT NULL AUTO_INCREMENT,
		instant_id VARCHAR(64) NOT NULL,
		states JSON DEFAULT NULL,
		creator VARCHAR(64) DEFAULT NULL,
		create_time DATETIME DEFAULT NULL,
		updater VARCHAR(64) DEFAULT NULL,
		update_time DATETIME DEFAULT NULL,
		PRIMARY KEY (id),
		UNIQUE KEY uniq_instantId (instant_id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`
	if _, err := pool.Exec(ddl); err != nil {
		t.Fatalf("create table: %v", err)
	}
	defer func() { _, _ = pool.Exec("DELETE FROM bpm_flow_repository WHERE instant_id LIKE 'gotest_%'") }()

	_, a := buildGraph(t)
	repo := flowplugin.NewMysqlStateRepository(nil) // real db persistence
	ctx := flow.NewContext("gotest_inst1")

	repo.StatePut(ctx, a, workflow.TaskStateWaiting)
	if got := repo.StateGet(ctx, a); got != workflow.TaskStateWaiting {
		t.Fatalf("StateGet after put = %v, want WAITING", got)
	}
	// a fresh repo (cold cache) must load the persisted state from the DB
	repo2 := flowplugin.NewMysqlStateRepository(nil)
	repo2.Evict(ctx.InstanceID())
	if got := repo2.StateGet(ctx, a); got != workflow.TaskStateWaiting {
		t.Errorf("cold-load StateGet = %v, want WAITING (persisted)", got)
	}
	repo.StateClear(ctx)
	if got := repo2.StateGet(ctx, a); got != workflow.TaskStateUnknown {
		t.Errorf("after clear = %v, want UNKNOWN", got)
	}
}
