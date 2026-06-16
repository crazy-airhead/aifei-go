package enjoy

import (
	"bytes"
	"fmt"
	"io"
	"sync"

	"github.com/crazy-airhead/aifei-go/enjoy/source"
)

// Template represents a compiled template.
type Template struct {
	engine *Engine
	ast    Stat
	source source.Source
	env    *Env
}

// Render executes the template with the given data and writes to writer.
func (t *Template) Render(data map[string]interface{}, writer io.Writer) error {
	scope := NewScope(data)
	ctrl := NewCtrl()
	t.ast.Exec(t.env, scope, &IOAdapter{w: writer}, ctrl)
	return nil
}

// RenderToString executes the template and returns the result as a string.
func (t *Template) RenderToString(data map[string]interface{}) string {
	var buf bytes.Buffer
	scope := NewScope(data)
	ctrl := NewCtrl()
	t.ast.Exec(t.env, scope, &IOAdapter{w: &buf}, ctrl)
	return buf.String()
}

// IsModified checks if the template source has been modified.
func (t *Template) IsModified() bool {
	return t.source != nil && t.source.IsModified()
}

// Engine is the template engine entry point.
type Engine struct {
	config        *EngineConfig
	templateCache sync.Map // string → *Template
}

var engines = make(map[string]*Engine)

// NewEngine creates a new template engine.
func NewEngine(name string) *Engine {
	e := &Engine{
		config: NewEngineConfig(),
	}
	engines[name] = e
	return e
}

// Use returns the default engine.
func Use() *Engine {
	if e, ok := engines["main"]; ok {
		return e
	}
	return NewEngine("main")
}

// GetTemplate returns a cached or newly compiled template from file.
func (e *Engine) GetTemplate(fileName string) *Template {
	if !e.config.devMode {
		if cached, ok := e.templateCache.Load(fileName); ok {
			t := cached.(*Template)
			if !t.IsModified() {
				return t
			}
		}
	}

	src := source.NewFileSource(fileName)
	return e.compileSource(fileName, src)
}

// GetTemplateByString compiles a template from a string.
func (e *Engine) GetTemplateByString(content string) *Template {
	src := source.NewStringSource(content)
	return e.compileSource(content, src)
}

func (e *Engine) compileSource(key string, src source.Source) *Template {
	content := src.GetContent()
	lexer := NewLexer(content)
	env := NewEnv(e.config)
	env.engine = e

	ast, err := ParseTemplate(lexer, env)
	if err != nil {
		return &Template{
			engine: e,
			ast:    &errorStat{err: err},
			source: src,
			env:    env,
		}
	}

	t := &Template{
		engine: e,
		ast:    ast,
		source: src,
		env:    env,
	}
	e.templateCache.Store(key, t)
	return t
}

// SetDevMode sets development mode (disables cache).
func (e *Engine) SetDevMode(devMode bool) { e.config.devMode = devMode }

// SetBaseTemplatePath sets the base path for template files.
func (e *Engine) SetBaseTemplatePath(path string) { e.config.baseTemplatePath = path }

// AddDirective registers a custom directive.
func (e *Engine) AddDirective(name string, factory DirectiveFactory) {
	e.config.directiveMap[name] = factory
}

// AddSharedObject registers a shared object available in all templates.
func (e *Engine) AddSharedObject(name string, obj interface{}) {
	e.config.sharedObjectMap[name] = obj
}

// RemoveAllTemplateCache clears the template cache.
func (e *Engine) RemoveAllTemplateCache() {
	e.templateCache.Range(func(key, value interface{}) bool {
		e.templateCache.Delete(key)
		return true
	})
}

// GetConfig returns the engine config.
func (e *Engine) GetConfig() *EngineConfig {
	return e.config
}

// NewTemplate creates a Template from an Env and Stat (for use by SQL directives).
func NewTemplate(env *Env, ast Stat) *Template {
	return &Template{env: env, ast: ast}
}

// errorStat outputs an error message.
type errorStat struct {
	err error
}

func (s *errorStat) Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {
	writer.WriteString(fmt.Sprintf("<!-- template error: %s -->", s.err.Error()))
}
