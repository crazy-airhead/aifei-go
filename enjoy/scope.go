package enjoy

// Scope manages variable scopes with parent chain lookup.
type Scope struct {
	data   map[string]interface{}
	parent *Scope
	global *Scope
}

// NewScope creates a new Scope with the given data.
func NewScope(data map[string]interface{}) *Scope {
	s := &Scope{data: data}
	s.global = s
	return s
}

// Get looks up a variable by name, searching up the scope chain.
func (s *Scope) Get(key string) interface{} {
	if v, ok := s.data[key]; ok {
		return v
	}
	if s.parent != nil {
		return s.parent.Get(key)
	}
	return nil
}

// Set sets a variable in the current scope.
func (s *Scope) Set(key string, value interface{}) {
	if s.data == nil {
		s.data = make(map[string]interface{})
	}
	s.data[key] = value
}

// SetLocal sets a variable in the current scope (same as Set).
func (s *Scope) SetLocal(key string, value interface{}) {
	s.Set(key, value)
}

// SetGlobal sets a variable in the global (root) scope.
func (s *Scope) SetGlobal(key string, value interface{}) {
	s.global.Set(key, value)
}

// Exists checks if a variable exists in the current scope chain.
func (s *Scope) Exists(key string) bool {
	if _, ok := s.data[key]; ok {
		return true
	}
	if s.parent != nil {
		return s.parent.Exists(key)
	}
	return false
}

// NewChild creates a child scope.
func (s *Scope) NewChild() *Scope {
	return &Scope{
		data:   make(map[string]interface{}),
		parent: s,
		global: s.global,
	}
}

// Data returns the scope's data map.
func (s *Scope) Data() map[string]interface{} {
	return s.data
}
