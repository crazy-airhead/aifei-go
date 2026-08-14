package workflow

import (
	"sync"

	"github.com/crazy-airhead/aifei-go/flow"
)

// StateRepository stores per-instance, per-node task states (and optional vars).
// Mirrors Java's StateRepository.
type StateRepository interface {
	// VarsGet returns extra context vars for the node (merged into the context on
	// node start); nil/empty for none.
	VarsGet(ctx flow.Context, node *flow.Node) map[string]any
	// StateGet returns the node's task state (TaskStateUnknown if none).
	StateGet(ctx flow.Context, node *flow.Node) TaskState
	// StatePut sets the node's task state.
	StatePut(ctx flow.Context, node *flow.Node, state TaskState)
	// StateRemove clears the node's task state.
	StateRemove(ctx flow.Context, node *flow.Node)
	// StateClear clears all node states for the instance.
	StateClear(ctx flow.Context)
}

// stateKey mirrors Java: "graphId:nodeId".
func stateKey(node *flow.Node) string {
	return node.Graph().GetID() + ":" + node.ID()
}

// InMemoryStateRepository stores states in nested maps keyed by instance id and
// "graphId:nodeId". Mirrors Java's InMemoryStateRepository.
type InMemoryStateRepository struct {
	mu     sync.RWMutex
	states map[string]map[string]int
}

// NewInMemoryStateRepository creates an empty InMemoryStateRepository.
func NewInMemoryStateRepository() *InMemoryStateRepository {
	return &InMemoryStateRepository{states: map[string]map[string]int{}}
}

// States returns the state map for an instance (for inspection/testing).
func (r *InMemoryStateRepository) States(instanceID string) map[string]int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := map[string]int{}
	for k, v := range r.states[instanceID] {
		out[k] = v
	}
	return out
}

func (r *InMemoryStateRepository) statesFor(instanceID string) map[string]int {
	m, ok := r.states[instanceID]
	if !ok {
		m = map[string]int{}
		r.states[instanceID] = m
	}
	return m
}

// VarsGet returns nil (no extra vars by default).
func (r *InMemoryStateRepository) VarsGet(flow.Context, *flow.Node) map[string]any { return nil }

// StateGet returns the node's state.
func (r *InMemoryStateRepository) StateGet(ctx flow.Context, node *flow.Node) TaskState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	code, ok := r.states[ctx.InstanceID()][stateKey(node)]
	if !ok {
		return TaskStateUnknown
	}
	return TaskStateOf(code)
}

// StatePut sets the node's state.
func (r *InMemoryStateRepository) StatePut(ctx flow.Context, node *flow.Node, state TaskState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statesFor(ctx.InstanceID())[stateKey(node)] = state.Code()
}

// StateRemove clears the node's state.
func (r *InMemoryStateRepository) StateRemove(ctx flow.Context, node *flow.Node) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.statesFor(ctx.InstanceID()), stateKey(node))
}

// StateClear clears all states for the instance.
func (r *InMemoryStateRepository) StateClear(ctx flow.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.states, ctx.InstanceID())
}
