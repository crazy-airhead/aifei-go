package enjoy

// Env holds the template execution environment.
type Env struct {
	engineConfig *EngineConfig
	engine       *Engine
	functionMap  map[string]*DefineStat
	sourceList   []interface{}
	fileName     string // 当前模板文件名/键，用于错误定位（对照 Java Location.templateFile）
	currentFile  string // 当前正在编译的文件路径（#include 相对父目录解析用，仅文件模板设置）
}

// NewEnv creates a new Env.
func NewEnv(config *EngineConfig) *Env {
	return &Env{
		engineConfig: config,
		functionMap:  make(map[string]*DefineStat),
	}
}

// GetFileName returns the current template file name / key for error location.
func (e *Env) GetFileName() string { return e.fileName }

// SetCurrentFile records the file path currently being compiled (for #include
// relative-to-parent-directory resolution)。仅文件模板设置。
func (e *Env) SetCurrentFile(f string) { e.currentFile = f }

// GetCurrentFile returns the file path currently being compiled.
func (e *Env) GetCurrentFile() string { return e.currentFile }

// GetEngineConfig returns the engine config.
func (e *Env) GetEngineConfig() *EngineConfig {
	return e.engineConfig
}

// GetEngine returns the engine.
func (e *Env) GetEngine() *Engine {
	return e.engine
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
