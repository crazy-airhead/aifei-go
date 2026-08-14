package flow

import "sync"

// Temporary holds per-run, per-graph mutable state used by gateway join semantics:
// stacks (for inclusive/loop aggregation) and counters (for parallel/inclusive
// incoming-branch counting). The map is the authoritative store; callers use the
// Stack*/Count* methods. Not serialized. Mirrors Java's Temporary.
type Temporary struct {
	mu     sync.Mutex
	counts map[string]int
	stacks map[string][]any
}

// newTemporary creates an empty Temporary.
func newTemporary() *Temporary {
	return &Temporary{counts: map[string]int{}, stacks: map[string][]any{}}
}

// StackPush pushes v onto the (graphID, key) stack.
func (t *Temporary) StackPush(graphID, key string, v any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	k := graphID + "/" + key
	t.stacks[k] = append(t.stacks[k], v)
}

// StackPeek returns the top of the (graphID, key) stack (nil if empty).
func (t *Temporary) StackPeek(graphID, key string) any {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.stacks[graphID+"/"+key]
	if len(s) == 0 {
		return nil
	}
	return s[len(s)-1]
}

// StackPop removes and returns the top of the (graphID, key) stack (nil if empty).
func (t *Temporary) StackPop(graphID, key string) any {
	t.mu.Lock()
	defer t.mu.Unlock()
	k := graphID + "/" + key
	s := t.stacks[k]
	if len(s) == 0 {
		return nil
	}
	top := s[len(s)-1]
	t.stacks[k] = s[:len(s)-1]
	return top
}

// StackSize returns the size of the (graphID, key) stack.
func (t *Temporary) StackSize(graphID, key string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.stacks[graphID+"/"+key])
}

// CountIncr atomically increments and returns the counter for (graphID, key).
func (t *Temporary) CountIncr(graphID, key string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	k := graphID + "/" + key
	t.counts[k]++
	return t.counts[k]
}

// CountSet sets the counter for (graphID, key).
func (t *Temporary) CountSet(graphID, key string, value int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.counts[graphID+"/"+key] = value
}

// Count returns the counter for (graphID, key).
func (t *Temporary) Count(graphID, key string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.counts[graphID+"/"+key]
}
