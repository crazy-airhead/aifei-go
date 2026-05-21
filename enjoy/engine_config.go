package enjoy

// EngineConfig holds configuration for a template engine.
type EngineConfig struct {
	directiveMap      map[string]DirectiveFactory
	sharedFunctionMap map[string]*DefineStat
	sharedObjectMap   map[string]interface{}
	baseTemplatePath  string
	encoding          string
	datePattern       string
	devMode           bool
}

// NewEngineConfig creates a default EngineConfig.
func NewEngineConfig() *EngineConfig {
	return &EngineConfig{
		directiveMap:      make(map[string]DirectiveFactory),
		sharedFunctionMap: make(map[string]*DefineStat),
		sharedObjectMap:   make(map[string]interface{}),
	}
}
