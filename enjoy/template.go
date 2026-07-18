package enjoy

import (
	"bytes"
	"fmt"
	"io"
	"reflect"
	"sync"

	"github.com/crazy-airhead/aifei-go/enjoy/source"
)

// Template represents a compiled template.
type Template struct {
	engine   *Engine
	ast      Stat
	source   source.Source
	env      *Env
	parseErr error // 解析期错误；非 nil 时 Render/RenderToString 直接返回它，不渲染。
}

// exec 在给定作用域上执行模板 AST，返回渲染期错误。
//
// 错误来源：① 解析期错误（parseErr，如 #for 语法错、#returnIf() 空参）直接返回；
// ② 运行期 panic（如 #date/#number 参数错、reflect 调用异常）经 recover 转为 error。
// 这样调用方可明确区分「正常渲染结果」与「错误」，错误不再烘进输出。
//
// 注意：流式 Render(writer) 时若中途 panic，已写入 writer 的部分内容无法撤回——
// 调用方据返回的 error 自行处理（server 端 io_handler 会记录日志）。
func (t *Template) exec(scope *Scope, writer io.Writer) (err error) {
	if t.parseErr != nil {
		return t.parseErr
	}
	ctrl := NewCtrl()
	defer func() {
		if r := recover(); r != nil {
			// 渲染期 panic 带「文件名:行号」——行号来自 StatList.Exec 跟踪的 ctrl.curLine
			// （最近执行的 stat 行，对照 Java TemplateException 的 Location）。
			err = renderError(t.env, ctrl.curLine, r)
		}
	}()
	t.ast.Exec(t.env, scope, &IOAdapter{w: writer}, ctrl)
	return nil
}

// renderError 构造渲染期错误信息，附加文件名与行号（行号 0 表示未知，如节点未持 location）。
func renderError(env *Env, line int, r interface{}) error {
	file := ""
	if env != nil {
		file = env.fileName
	}
	msg := fmt.Sprintf("%v", r)
	switch {
	case line > 0 && file != "":
		return fmt.Errorf("template render %q: line %d: %s", file, line, msg)
	case line > 0:
		return fmt.Errorf("template render: line %d: %s", line, msg)
	case file != "":
		return fmt.Errorf("template render %q: %s", file, msg)
	default:
		return fmt.Errorf("template render: %s", msg)
	}
}

// Render executes the template with the given data and writes to writer.
func (t *Template) Render(data map[string]interface{}, writer io.Writer) error {
	scope := NewScopeWithShared(data, t.sharedObjectMap())
	return t.exec(scope, writer)
}

// RenderToString0 executes the template and returns the result as a string.
// 渲染出错时返回 ("", err)（半成品输出被丢弃），调用方据 err 区分正常结果与错误。
func (t *Template) RenderToString0(data map[string]interface{}) (string, error) {
	var buf bytes.Buffer
	scope := NewScopeWithShared(data, t.sharedObjectMap())
	if err := t.exec(scope, &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// RenderToString 是 RenderToString0 的「不返回 error」便捷版本：内部调用 RenderToString0，
// 渲染出错时 panic（而非返回 error）。适合调用方确定模板无误、不想逐处处理 error 的场景
// （如 db SqlKit 渲染 SQL、代码生成器）。注意：出错会 panic，调用方需自行决定是否 recover。
func (t *Template) RenderToString(data map[string]interface{}) string {
	out, err := t.RenderToString0(data)
	if err != nil {
		panic(err)
	}
	return out
}

// sharedObjectMap returns the engine-level shared objects bound to this template's
// env, or nil when the template has no env/config (对照 Java env.engineConfig.sharedObjectMap)。
func (t *Template) sharedObjectMap() map[string]interface{} {
	if t.env == nil {
		return nil
	}
	if cfg := t.env.GetEngineConfig(); cfg != nil {
		return cfg.sharedObjectMap
	}
	return nil
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

	src := e.config.GetSourceFactory()(fileName)
	return e.compileSource(fileName, src, true)
}

// GetTemplateByString compiles a template from a string.
func (e *Engine) GetTemplateByString(content string) *Template {
	src := source.NewStringSource(content)
	return e.compileSource(content, src, false)
}

func (e *Engine) compileSource(key string, src source.Source, isFile bool) *Template {
	content := src.GetContent()
	lexer := NewLexer(content)
	lexer.SetKeepLineBlank(e.config.KeepLineBlankDirectives())
	env := NewEnv(e.config)
	env.engine = e
	if isFile {
		// 文件模板：fileName 用于错误定位（路径: 行号），currentFile 用于 #include 相对父目录。
		env.fileName = key
		env.currentFile = key
	}
	// 字符串模板：fileName 留空，错误只标行号（对照 Java "String template line N"）。

	ast, err := parseTemplateRecovered(lexer, env)
	if err != nil {
		// 解析错误存入 parseErr：Render/RenderToString 会直接返回它（不再烘进输出）。
		// ast 置 NullStat 以防调用方忽略 err 时也不会误渲染。
		return &Template{
			engine:   e,
			ast:      &NullStat{},
			source:   src,
			env:      env,
			parseErr: err,
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

// parseTemplateRecovered 编译 token 为 Stat AST，并把解析期的 panic（如 #date/#number
// 等 directive 的 SetExprList 参数校验 panic）转为 error 返回，避免打崩 GetTemplateByString 调用方。
// 与 exec 的运行期 recover 分工：此处覆盖解析期，exec 覆盖渲染期。
func parseTemplateRecovered(lexer *Lexer, env *Env) (stat Stat, err error) {
	defer func() {
		if r := recover(); r != nil {
			stat, err = nil, fmt.Errorf("template parse: %v", r)
		}
	}()
	return ParseTemplate(lexer, env)
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

// AddSharedMethod registers a shared method callable as a bare `name(args)` in templates
// (对照 Java EngineConfig.sharedMethodKit.addSharedMethod)。默认已注册 isEmpty/notEmpty。
// 注意：共享方法为进程级注册（与 Java 扩展方法一致），对所有 engine 生效。
func (e *Engine) AddSharedMethod(name string, fn SharedMethod) { AddSharedMethod(name, fn) }

// AddExtensionMethod registers an extension method on the given reflect.Kind, callable as
// `value.method(args)` in templates (对照 Java MethodKit.addExtensionMethod)。默认已注册
// String 与全部数值 kind 的 toInt/toLong/toBoolean/... 等。
// 注意：扩展方法为进程级注册，对所有 engine 生效。
func (e *Engine) AddExtensionMethod(kind reflect.Kind, name string, fn ExtensionMethod) {
	AddExtensionMethod(kind, name, fn)
}

// AddStatic registers a namespace object whose exported methods are callable as
// `alias::method(args)` in templates (对照 Java 导入工具类的静态方法)。需先
// SetStaticMethodExpressionEnabled(true)（默认禁用）。Go 无静态方法/Class.forName，以反射一个
// struct 实例的导出方法为等价——建议传 `&Obj{}`。进程级注册，对所有 engine 生效。
func (e *Engine) AddStatic(alias string, obj interface{}) { AddStatic(alias, obj) }

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
