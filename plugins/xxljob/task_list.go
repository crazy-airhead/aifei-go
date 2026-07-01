package xxljob

import "sync"

// taskList is a thread-safe map of [JobID]*Task.
type taskList struct {
	mu   sync.RWMutex
	data map[string]*Task
}

// Set stores a task.
func (t *taskList) Set(key string, val *Task) {
	t.mu.Lock()
	t.data[key] = val
	t.mu.Unlock()
}

// Get retrieves a task.
func (t *taskList) Get(key string) *Task {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.data[key]
}

// GetAll returns all tasks.
func (t *taskList) GetAll() map[string]*Task {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.data
}

// Del deletes a task.
func (t *taskList) Del(key string) {
	t.mu.Lock()
	delete(t.data, key)
	t.mu.Unlock()
}

// Len returns the number of tasks.
func (t *taskList) Len() int {
	return len(t.data)
}

// Exists checks if a key exists.
func (t *taskList) Exists(key string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.data[key]
	return ok
}
