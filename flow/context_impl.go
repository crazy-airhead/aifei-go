package flow

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/crazy-airhead/aifei-go/dami"
)

// flowContext is the default Context implementation. Mirrors Java's FlowContextDefault.
type flowContext struct {
	mu        sync.RWMutex
	vars      map[string]any
	trace     *Trace
	exchanger *Exchanger
	eventBus  *dami.Bus
	stopped   bool
	goCtx     context.Context // optional Go context (for db tx/cancellation propagation)
}

// NewContext creates a Context, optionally with an instance id.
func NewContext(instanceID ...string) Context {
	id := ""
	if len(instanceID) > 0 {
		id = instanceID[0]
	}
	c := &flowContext{vars: map[string]any{}, trace: NewTrace()}
	c.Put("instanceId", id)
	c.Put("context", c) // self-ref (excluded from serialization)
	return c
}

// ContextFromJSON rebuilds a Context from a ToJSON snapshot.
func ContextFromJSON(j string) (Context, error) {
	c := NewContext().(*flowContext)
	if j == "" {
		return c, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(j), &m); err != nil {
		return nil, err
	}
	if v, ok := m["stopped"]; ok {
		if b, ok := v.(bool); ok {
			c.stopped = b
		}
	}
	if vm, ok := m["vars"].(map[string]any); ok {
		for k, v := range vm {
			c.vars[k] = v
		}
	}
	if tm, ok := m["trace"].(map[string]any); ok {
		c.trace = traceFromMap(tm)
	}
	return c, nil
}

// ToJSON serializes the context (vars minus non-serializable entries, stopped, trace).
func (c *flowContext) ToJSON() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := map[string]any{
		"stopped": c.stopped,
		"vars":    c.serVarsLocked(),
	}
	if c.trace != nil {
		out["trace"] = traceToMap(c.trace)
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// serVarsLocked returns vars excluding non-serializable values (context self-ref,
// *Exchanger, funcs). Mirrors Java's serVars + NonSerializableEncoder.
func (c *flowContext) serVarsLocked() map[string]any {
	m := map[string]any{}
	for k, v := range c.vars {
		if k == "context" {
			continue
		}
		if !isSerializable(v) {
			continue
		}
		m[k] = v
	}
	return m
}

// isSerializable reports whether v is JSON-serializable for snapshots.
func isSerializable(v any) bool {
	switch v.(type) {
	case *Exchanger, *flowContext:
		return false
	}
	return true
}

// ---- internal setters used by the engine/exchanger ----

func (c *flowContext) setExchanger(ex *Exchanger) { c.exchanger = ex }
func (c *flowContext) setStopped(b bool) {
	c.mu.Lock()
	c.stopped = b
	c.mu.Unlock()
}

// GoContext returns the Go context bound to this flow context (for db transaction /
// cancellation propagation). Defaults to context.Background(). Not part of the
// Context interface; plugins type-assert to use it.
func (c *flowContext) GoContext() context.Context {
	c.mu.RLock()
	g := c.goCtx
	c.mu.RUnlock()
	if g != nil {
		return g
	}
	return context.Background()
}

// SetGoContext binds a Go context (e.g. a db transaction context).
func (c *flowContext) SetGoContext(ctx context.Context) {
	c.mu.Lock()
	c.goCtx = ctx
	c.mu.Unlock()
}

// ---- Context interface ----

func (c *flowContext) InstanceID() string { v, _ := c.Get("instanceId").(string); return v }

func (c *flowContext) Vars() map[string]any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.vars
}

func (c *flowContext) Put(key string, value any) Context {
	if value == nil {
		return c
	}
	c.mu.Lock()
	c.vars[key] = value
	c.mu.Unlock()
	return c
}

func (c *flowContext) PutIfAbsent(key string, value any) Context {
	if value == nil {
		return c
	}
	c.mu.Lock()
	if _, ok := c.vars[key]; !ok {
		c.vars[key] = value
	}
	c.mu.Unlock()
	return c
}

func (c *flowContext) Get(key string) any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.vars[key]
}

func (c *flowContext) GetAs(key string) any { return c.Get(key) }

func (c *flowContext) GetOrDefault(key string, def any) any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if v, ok := c.vars[key]; ok {
		return v
	}
	return def
}

func (c *flowContext) ContainsKey(key string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.vars[key]
	return ok
}

func (c *flowContext) Remove(key string) {
	c.mu.Lock()
	delete(c.vars, key)
	c.mu.Unlock()
}

func (c *flowContext) With(key string, value any, fn func() error) error {
	bak := c.Get(key)
	c.Put(key, value)
	err := fn()
	if bak == nil {
		c.Remove(key)
	} else {
		c.Put(key, bak)
	}
	return err
}

func (c *flowContext) Stop() {
	if c.exchanger != nil {
		c.exchanger.Stop()
	}
}

func (c *flowContext) IsStopped() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.stopped
}

func (c *flowContext) Interrupt() {
	if c.exchanger != nil {
		c.exchanger.Interrupt()
	}
}

func (c *flowContext) Trace() *Trace { return c.trace }

func (c *flowContext) EnableTrace(enable bool) Context {
	c.trace.Enable(enable)
	return c
}

func (c *flowContext) LastRecord() *NodeRecord { return c.trace.LastRecord("") }

func (c *flowContext) LastNodeID() string { return c.trace.LastNodeID("") }

func (c *flowContext) EventBus() *dami.Bus {
	if c.eventBus == nil {
		c.eventBus = dami.New()
	}
	return c.eventBus
}

// traceToMap/traceFromMap serialize the trace (rootGraphId + last records).
func traceToMap(t *Trace) map[string]any {
	if t == nil {
		return nil
	}
	records := map[string]any{}
	for k, r := range t.last {
		records[k] = map[string]any{
			"graphId": r.GraphID, "id": r.ID, "title": r.Title,
			"type": r.Type.String(), "timestamp": r.Timestamp,
		}
	}
	return map[string]any{"rootGraphId": t.rootGraphID, "last": records}
}

func traceFromMap(m map[string]any) *Trace {
	t := NewTrace()
	if r, ok := m["rootGraphId"].(string); ok {
		t.rootGraphID = r
	}
	if last, ok := m["last"].(map[string]any); ok {
		for k, v := range last {
			vm, ok := v.(map[string]any)
			if !ok {
				continue
			}
			rec := NodeRecord{
				GraphID: getStr(vm, "graphId"), ID: getStr(vm, "id"),
				Title: getStr(vm, "title"),
			}
			if ts, ok := vm["timestamp"].(float64); ok {
				rec.Timestamp = int64(ts)
			}
			if ty, ok := vm["type"].(string); ok {
				rec.Type = NodeTypeOf(ty)
			}
			t.last[k] = rec
		}
	}
	return t
}
