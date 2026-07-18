package enjoy

// defaultDatePattern is the default date format pattern (Java-style) used by
// the #date directive when no explicit pattern is given (对照 Java EngineConfig.datePattern)。
const defaultDatePattern = "yyyy-MM-dd HH:mm"

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

// NewEngineConfig creates a default EngineConfig with the builtin directives
// registered (对照 Java EngineConfig 构造器默认注册 date/escape/number/random/render/string）。
func NewEngineConfig() *EngineConfig {
	c := &EngineConfig{
		directiveMap:      make(map[string]DirectiveFactory),
		sharedFunctionMap: make(map[string]*DefineStat),
		sharedObjectMap:   make(map[string]interface{}),
		datePattern:       defaultDatePattern,
	}
	c.addBuiltinDirectives()
	return c
}

// GetDatePattern returns the date format pattern used by #date (对照 Java getDatePattern)。
func (c *EngineConfig) GetDatePattern() string {
	if c.datePattern == "" {
		return defaultDatePattern
	}
	return c.datePattern
}

// SetDatePattern sets the date format pattern used by #date (对照 Java setDatePattern)。
func (c *EngineConfig) SetDatePattern(pattern string) {
	if pattern == "" {
		return
	}
	c.datePattern = pattern
}

// GetBaseTemplatePath returns the base template path.
func (c *EngineConfig) GetBaseTemplatePath() string {
	return c.baseTemplatePath
}

// addBuiltinDirectives registers the builtin directives
// (对照 Java EngineConfig 构造器默认注册 date/escape/number/random/render/string/call)。
// #call(...) 走指令路径（动态函数名）；#@name(args) 静态糖在词法器 scanAtCall +
// parseCallStat 中处理，不在此注册。
func (c *EngineConfig) addBuiltinDirectives() {
	c.directiveMap["date"] = func() Directive { return &DateDirective{} }
	c.directiveMap["escape"] = func() Directive { return &EscapeDirective{} }
	c.directiveMap["number"] = func() Directive { return &NumberDirective{} }
	c.directiveMap["random"] = func() Directive { return &RandomDirective{} }
	c.directiveMap["render"] = func() Directive { return &RenderDirective{} }
	c.directiveMap["string"] = func() Directive { return &StringDirective{} }
	c.directiveMap["call"] = func() Directive { return &CallDirective{} }
}
