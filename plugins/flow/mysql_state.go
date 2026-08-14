package flowplugin

import (
	"context"
	"encoding/json"
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

// PersistFunc writes the instance states map (upsert). Swappable for testing.
type PersistFunc func(ctx flow.Context, instanceID string, states map[string]int, isNew bool) error

// LoadFunc reads the instance states map. Swappable for testing.
type LoadFunc func(ctx flow.Context, instanceID string) (map[string]int, error)

// MysqlStateRepository implements workflow.StateRepository backed by a MySQL table
// (bpm_flow_repository). One row per instance holds the whole states map as JSON,
// upserted by instant_id (ON DUPLICATE KEY UPDATE via db.InsertOrUpdateRow). States
// are cached in memory per instance (lazy load, write-through) so the engine's
// per-node reads don't each hit the DB.
type MysqlStateRepository struct {
	schema  RepoSchema
	log     log.Logger
	persist PersistFunc
	load    LoadFunc
	cache   sync.Map // instanceID -> *stateCache
}

type stateCache struct {
	mu      sync.Mutex
	states  map[string]int // "graphId:nodeId" -> TaskState.Code()
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

// VarsGet returns nil (no extra vars by default).
func (r *MysqlStateRepository) VarsGet(flow.Context, *flow.Node) map[string]any { return nil }

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

// StateClear clears all states for the instance.
func (r *MysqlStateRepository) StateClear(ctx flow.Context) {
	c := r.cacheOfID(ctx.InstanceID())
	c.mu.Lock()
	defer c.mu.Unlock()
	c.states = map[string]int{}
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

// ensureLoaded lazily loads the states map once (single SELECT).
func (r *MysqlStateRepository) ensureLoaded(ctx flow.Context, c *stateCache) {
	if c.loaded {
		return
	}
	loaded, err := r.load(ctx, ctx.InstanceID())
	if err != nil && r.log != nil {
		r.log.Error("flow mysql: load states failed: %v", err)
	}
	if loaded != nil {
		c.states = loaded
	} else {
		c.states = map[string]int{}
	}
	c.loaded = true
}

// flush writes the whole states map back (single upsert).
func (r *MysqlStateRepository) flush(ctx flow.Context, c *stateCache) error {
	err := r.persist(ctx, ctx.InstanceID(), c.states, !c.created)
	if err == nil {
		c.created = true
	} else if r.log != nil {
		r.log.Error("flow mysql: persist states failed: %v", err)
	}
	return err
}

// ---- default persistence via db ----

func (r *MysqlStateRepository) persistDefault(ctx flow.Context, instanceID string, states map[string]int, isNew bool) error {
	statesJSON, _ := json.Marshal(states)
	row := db.NewRow(r.schema.RepoTable).
		Set("instant_id", instanceID).
		Set("states", statesJSON).
		Set("updater", auditor(ctx)).
		Set("update_time", time.Now())
	if isNew {
		row.Set("creator", auditor(ctx)).Set("create_time", time.Now())
	}
	_, err := db.WithCtx(goCtx(ctx)).InsertOrUpdateRow(row)
	return err
}

func (r *MysqlStateRepository) loadDefault(ctx flow.Context, instanceID string) (map[string]int, error) {
	row, err := db.WithCtx(goCtx(ctx)).FindFirstBy(r.schema.RepoTable, "instant_id", instanceID)
	if err != nil || row == nil {
		return nil, err
	}
	var states map[string]int
	if b := row.GetBytes("states"); len(b) > 0 {
		if err := json.Unmarshal(b, &states); err != nil {
			return nil, err
		}
	}
	return states, nil
}

// auditor returns the current user from the context vars (nil if unset).
func auditor(ctx flow.Context) any {
	if v := ctx.Get("auditor"); v != nil {
		return v
	}
	return ctx.Get("creator")
}
