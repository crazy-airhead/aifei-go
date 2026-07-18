package enjoy

// Scope manages variable scopes with parent chain lookup.
type Scope struct {
	data            map[string]interface{}
	parent          *Scope
	global          *Scope
	sharedObjectMap map[string]interface{}
}

// NewScope creates a new top-level Scope with the given data and no shared objects.
// For templates that registered shared objects via Engine.AddSharedObject, use
// NewScopeWithShared so Get can fall back to them (对照 Java new Scope(data, sharedObjectMap))。
func NewScope(data map[string]interface{}) *Scope {
	return NewScopeWithShared(data, nil)
}

// NewScopeWithShared creates a new top-level Scope bound to a shared object map.
// Shared objects are consulted by Get only after the data/parent chain misses
// (对照 Java EngineConfig.sharedObjectMap + Scope 回退)。
func NewScopeWithShared(data, sharedObjectMap map[string]interface{}) *Scope {
	s := &Scope{data: data, sharedObjectMap: sharedObjectMap}
	s.global = s
	return s
}

// Get looks up a variable by name, searching up the scope chain; if the data
// chain misses, it falls back to the shared object map (对照 Java Scope.get)。
func (s *Scope) Get(key string) interface{} {
	for cur := s; cur != nil; cur = cur.parent {
		if cur.data != nil {
			if v, ok := cur.data[key]; ok {
				return v
			}
		}
	}
	// data 链未命中，回退共享对象（沿链查找任意层持有的 sharedObjectMap）。
	for cur := s; cur != nil; cur = cur.parent {
		if cur.sharedObjectMap != nil {
			if v, ok := cur.sharedObjectMap[key]; ok {
				return v
			}
		}
	}
	return nil
}

// GetSharedObject returns a shared object by name reachable up the scope chain
// (对照 Java Scope.getSharedObject)。
func (s *Scope) GetSharedObject(key string) interface{} {
	for cur := s; cur != nil; cur = cur.parent {
		if cur.sharedObjectMap != nil {
			if v, ok := cur.sharedObjectMap[key]; ok {
				return v
			}
		}
	}
	return nil
}

// Set 自内向外查找已存在变量并就地改写；整条链都未命中时存入顶层作用域
// （对照 Java Scope.set：HashMap 允许 null 值，故用 containsKey 判断而非判空）。
// 与 SetLocal（仅写当前层）、SetGlobal（仅写顶层）相对。
func (s *Scope) Set(key string, value interface{}) {
	for cur := s; ; cur = cur.parent {
		if cur.data != nil {
			if _, ok := cur.data[key]; ok {
				cur.data[key] = value
				return
			}
		}
		if cur.parent == nil {
			if cur.data == nil { // 支持顶层 data 为 nil
				cur.data = make(map[string]interface{})
			}
			cur.data[key] = value
			return
		}
	}
}

// SetLocal 仅在当前作用域写入变量（对照 Java Scope.setLocal）。
// 注意：不能复用 Set，否则会沿 parent 链向上改写或落到顶层。
func (s *Scope) SetLocal(key string, value interface{}) {
	if s.data == nil {
		s.data = make(map[string]interface{})
	}
	s.data[key] = value
}

// SetGlobal sets a variable in the global (root) scope.
func (s *Scope) SetGlobal(key string, value interface{}) {
	s.global.Set(key, value)
}

// Exists 自内向外查找变量是否存在（对照 Java Scope.exists，含 containsKey 语义）。
func (s *Scope) Exists(key string) bool {
	for cur := s; cur != nil; cur = cur.parent {
		if cur.data != nil {
			if _, ok := cur.data[key]; ok {
				return true
			}
		}
	}
	return false
}

// NewChild creates a child scope.
func (s *Scope) NewChild() *Scope {
	return &Scope{
		data:            make(map[string]interface{}),
		parent:          s,
		global:          s.global,
		sharedObjectMap: s.sharedObjectMap,
	}
}

// Data returns the scope's data map.
func (s *Scope) Data() map[string]interface{} {
	return s.data
}
