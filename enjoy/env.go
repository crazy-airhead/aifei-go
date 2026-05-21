package enjoy

// Env holds the template execution environment.
type Env struct {
	engineConfig *EngineConfig
	functionMap  map[string]*DefineStat
	sourceList   []interface{}
}

// NewEnv creates a new Env.
func NewEnv(config *EngineConfig) *Env {
	return &Env{
		engineConfig: config,
		functionMap:  make(map[string]*DefineStat),
	}
}

// GetEngineConfig returns the engine config.
func (e *Env) GetEngineConfig() *EngineConfig {
	return e.engineConfig
}

// GetFunction returns a defined template function.
func (e *Env) GetFunction(name string) *DefineStat {
	if fn, ok := e.functionMap[name]; ok {
		return fn
	}
	if e.engineConfig != nil {
		if fn, ok := e.engineConfig.sharedFunctionMap[name]; ok {
			return fn
		}
	}
	return nil
}

// AddFunction registers a template function.
func (e *Env) AddFunction(name string, def *DefineStat) {
	e.functionMap[name] = def
}
