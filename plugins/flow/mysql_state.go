package flowplugin

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/crazy-airhead/aifei-go/db"
	"github.com/crazy-airhead/aifei-go/flow"
	"github.com/crazy-airhead/aifei-go/flow/workflow"
	"github.com/crazy-airhead/aifei-go/log"
)

// goContexter is satisfied by *flow.flowContext (which has GoContext). Used to
// propagate db transactions / cancellation without widening the flow.Context API.
type goContexter interface {
	GoContext() context.Context
}

func goCtx(ctx flow.Context) context.Context {
	if gc, ok := ctx.(goContexter); ok {
		return gc.GoContext()
	}
	return context.Background()
}

// PersistFunc writes the instance states and vars maps (upsert). Swappable for testing.
type PersistFunc func(ctx flow.Context, instanceID string, states map[string]int, vars map[string]any, isNew bool) error

// LoadFunc reads the instance states and vars maps. Swappable for testing.
type LoadFunc func(ctx flow.Context, instanceID string) (map[string]int, map[string]any, error)

// MysqlStateRepository implements workflow.StateRepository backed by a MySQL table
// (bpm_flow_repository). One row per instance holds the whole states map as JSON,
// upserted by instant_id (ON DUPLICATE KEY UPDATE via db.InsertOrUpdateRow). States
// are cached in memory per instance (lazy load, write-through) so the engine's
// per-node reads don't each hit the DB.
type MysqlStateRepository struct {
	schema     RepoSchema
	log        log.Logger
	persist    PersistFunc
	load       LoadFunc
	graphTexts map[string]string // graphID -> graph JSON text (instance snapshots)
	cache      sync.Map          // instanceID -> *stateCache
}

type stateCache struct {
	mu      sync.Mutex
	states  map[string]int // "graphId:nodeId" -> TaskState.Code()
	vars    map[string]any // persisted run vars (filtered; restored on node start)
	loaded  bool
	created bool // whether the instance row has been created
}

// NewMysqlStateRepository builds a MySQL-backed repository. The default persistence
// uses db.WithCtx (so it joins any ambient transaction via flow.Context.GoContext()).
func NewMysqlStateRepository(logger log.Logger, opts ...RepoOption) *MysqlStateRepository {
	r := &MysqlStateRepository{schema: DefaultRepoSchema(), log: logger}
	for _, o := range opts {
		o(&r.schema)
	}
	r.persist = r.persistDefault
	r.load = r.loadDefault
	return r
}

// RepoOption configures the schema.
type RepoOption func(*RepoSchema)

// WithRepoTable overrides the repository table name.
func WithRepoTable(name string) RepoOption {
	return func(s *RepoSchema) { s.RepoTable = name }
}

// WithTaskTable overrides the task-history table name.
func WithTaskTable(name string) RepoOption {
	return func(s *RepoSchema) { s.TaskTable = name }
}

// SetStatePersisters injects custom persist/load funcs (for testing or alternate
// backends). The default uses db (MySQL).
func (r *MysqlStateRepository) SetStatePersisters(p PersistFunc, l LoadFunc) {
	r.persist = p
	r.load = l
}

// SetGraphTexts supplies graph JSON texts (graphID -> text). On an instance's
// first persist the root graph's text is written to the repo's `graph` column
// as the instance snapshot. Graphs without a text are stored with a NULL graph.
func (r *MysqlStateRepository) SetGraphTexts(texts map[string]string) {
	r.graphTexts = texts
}

// graphTextOf resolves the instance's root graph JSON: the graph id comes from
// the active WorkflowIntent (root graph of the running operation), falling back
// to the common "graphId:" prefix of the persisted state keys (flat graphs).
func (r *MysqlStateRepository) graphTextOf(ctx flow.Context, states map[string]int) string {
	if len(r.graphTexts) == 0 {
		return ""
	}
	var graphID string
	if v := ctx.Get(workflow.IntentKey); v != nil {
		if it, ok := v.(*workflow.WorkflowIntent); ok && it.RootGraph() != nil {
			graphID = it.RootGraph().GetID()
		}
	}
	if graphID == "" {
		for k := range states {
			if i := strings.IndexByte(k, ':'); i > 0 {
				graphID = k[:i]
				break
			}
		}
	}
	return r.graphTexts[graphID]
}

// VarsGet returns the persisted run vars for the instance (merged into the context
// by WorkflowDriver.OnNodeStart at every node entry, so gateway `when` conditions
// re-evaluate consistently across requests). Transient per-request keys (actor,
// creator, auditor, WorkflowIntent) are never persisted and thus never restored.
func (r *MysqlStateRepository) VarsGet(ctx flow.Context, _ *flow.Node) map[string]any {
	c := r.cacheOf(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	r.ensureLoaded(ctx, c)
	return c.vars
}

// StateGet returns the node's state (cached, lazy-loaded).
func (r *MysqlStateRepository) StateGet(ctx flow.Context, node *flow.Node) workflow.TaskState {
	c := r.cacheOf(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	r.ensureLoaded(ctx, c)
	return workflow.TaskStateOf(c.states[stateKey(node)])
}

// StatePut writes the state (cached + write-through).
func (r *MysqlStateRepository) StatePut(ctx flow.Context, node *flow.Node, state workflow.TaskState) {
	c := r.cacheOf(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	r.ensureLoaded(ctx, c)
	c.states[stateKey(node)] = state.Code()
	_ = r.flush(ctx, c)
}

// StateRemove clears the node's state.
func (r *MysqlStateRepository) StateRemove(ctx flow.Context, node *flow.Node) {
	c := r.cacheOf(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	r.ensureLoaded(ctx, c)
	delete(c.states, stateKey(node))
	_ = r.flush(ctx, c)
}

// StateClear clears all states and vars for the instance.
func (r *MysqlStateRepository) StateClear(ctx flow.Context) {
	c := r.cacheOfID(ctx.InstanceID())
	c.mu.Lock()
	defer c.mu.Unlock()
	c.states = map[string]int{}
	c.vars = map[string]any{}
	c.loaded = true
	_ = r.flush(ctx, c)
}

// Evict drops the in-memory cache for an instance (call when an instance ends).
func (r *MysqlStateRepository) Evict(instanceID string) { r.cache.Delete(instanceID) }

func (r *MysqlStateRepository) cacheOf(ctx flow.Context) *stateCache {
	return r.cacheOfID(ctx.InstanceID())
}

func (r *MysqlStateRepository) cacheOfID(instanceID string) *stateCache {
	v, _ := r.cache.LoadOrStore(instanceID, &stateCache{states: map[string]int{}})
	return v.(*stateCache)
}

// stateKey mirrors workflow.stateKey: "graphId:nodeId".
func stateKey(node *flow.Node) string {
	return node.Graph().GetID() + ":" + node.ID()
}

// ensureLoaded lazily loads the states and vars maps once (single SELECT).
func (r *MysqlStateRepository) ensureLoaded(ctx flow.Context, c *stateCache) {
	if c.loaded {
		return
	}
	states, vars, err := r.load(ctx, ctx.InstanceID())
	if err != nil && r.log != nil {
		r.log.Error("flow mysql: load states failed: %v", err)
	}
	if states != nil {
		c.states = states
	} else {
		c.states = map[string]int{}
	}
	if vars != nil {
		c.vars = vars
	} else {
		c.vars = map[string]any{}
	}
	c.loaded = true
}

// flush writes the states and vars maps back (single upsert).
func (r *MysqlStateRepository) flush(ctx flow.Context, c *stateCache) error {
	err := r.persist(ctx, ctx.InstanceID(), c.states, c.vars, !c.created)
	if err == nil {
		c.created = true
	} else if r.log != nil {
		r.log.Error("flow mysql: persist states failed: %v", err)
	}
	return err
}

// ---- default persistence via db ----

func (r *MysqlStateRepository) persistDefault(ctx flow.Context, instanceID string, states map[string]int, vars map[string]any, isNew bool) error {
	statesJSON, _ := json.Marshal(states)
	row := db.NewRow(r.schema.RepoTable).
		Set("instant_id", instanceID).
		Set("states", statesJSON).
		Set("vars", marshalVars(vars)).
		Set("updater", auditor(ctx)).
		Set("update_time", time.Now())
	if isNew {
		row.Set("creator", auditor(ctx)).Set("create_time", time.Now())
		// instance graph snapshot: the graph definition this instance runs under
		if text := r.graphTextOf(ctx, states); text != "" {
			row.Set("graph", text)
		}
	}
	_, err := db.WithCtx(goCtx(ctx)).InsertOrUpdateRow(row)
	return err
}

func (r *MysqlStateRepository) loadDefault(ctx flow.Context, instanceID string) (map[string]int, map[string]any, error) {
	row, err := db.WithCtx(goCtx(ctx)).FindFirstBy(r.schema.RepoTable, "instant_id", instanceID)
	if err != nil || row == nil {
		return nil, nil, err
	}
	var states map[string]int
	if b := row.GetBytes("states"); len(b) > 0 {
		if err := json.Unmarshal(b, &states); err != nil {
			return nil, nil, err
		}
	}
	var vars map[string]any
	if b := row.GetBytes("vars"); len(b) > 0 {
		if err := json.Unmarshal(b, &vars); err != nil {
			return nil, nil, err
		}
	}
	return states, vars, nil
}

// ---- run vars persistence (bpm_flow_repository.vars) ----

// transientVars are per-request keys never persisted: restoring them would
// overwrite the current user (actor/creator/auditor) or carry engine-internal
// state (WorkflowIntent) across requests.
var transientVars = map[string]bool{
	"actor": true, "creator": true, "auditor": true,
	"instanceId": true, "context": true,
	workflow.IntentKey: true,
}

// persistableVars filters a context's vars down to what belongs in the vars
// column: drops transient keys and non-JSON values.
func persistableVars(ctx flow.Context) map[string]any {
	out := map[string]any{}
	for k, v := range ctx.Vars() {
		if transientVars[k] {
			continue
		}
		switch v.(type) {
		case func(), *flow.Exchanger, *flow.Context:
			continue
		}
		if _, err := json.Marshal(v); err != nil {
			continue
		}
		out[k] = v
	}
	return out
}

func marshalVars(vars map[string]any) []byte {
	if len(vars) == 0 {
		return nil
	}
	b, _ := json.Marshal(vars)
	return b
}

// LoadCtx rebuilds a flow.Context for the instance from the persisted vars
// (gateway conditions like agree/finish/formData survive across requests).
// A missing row yields a fresh context bound to instanceID.
func (r *MysqlStateRepository) LoadCtx(instanceID string) (flow.Context, error) {
	ctx := flow.NewContext(instanceID)
	row, err := db.FindFirstBy(r.schema.RepoTable, "instant_id", instanceID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return ctx, nil
	}
	b := row.GetBytes("vars")
	if len(b) == 0 {
		return ctx, nil
	}
	var vars map[string]any
	if err := json.Unmarshal(b, &vars); err != nil {
		return nil, err
	}
	for k, v := range vars {
		ctx.Put(k, v)
	}
	return ctx, nil
}

// SaveCtx persists the context's run vars (filtered) for the instance.
// Call before engine operations so WorkflowDriver.OnNodeStart merges the same
// values instead of stale ones.
func (r *MysqlStateRepository) SaveCtx(ctx flow.Context) error {
	c := r.cacheOfID(ctx.InstanceID())
	c.mu.Lock()
	defer c.mu.Unlock()
	r.ensureLoaded(ctx, c)
	c.vars = persistableVars(ctx)
	return r.flush(ctx, c)
}

// auditor returns the current user from the context vars (nil if unset).
func auditor(ctx flow.Context) any {
	if v := ctx.Get("auditor"); v != nil {
		return v
	}
	return ctx.Get("creator")
}
