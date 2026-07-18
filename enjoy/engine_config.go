package enjoy

import (
	"fmt"
	"strings"

	"github.com/crazy-airhead/aifei-go/enjoy/source"
)

// defaultDatePattern is the default date format pattern (Java-style) used by
// the #date directive when no explicit pattern is given (对照 Java EngineConfig.datePattern)。
const defaultDatePattern = "yyyy-MM-dd HH:mm"

// 舍入模式常量（对照 Java RoundingMode），用于 #number 指令的格式化舍入。
const (
	RoundingModeHalfEven = "HALF_EVEN" // 银行家舍入（Java DecimalFormat 默认，Go FormatFloat 行为）
	RoundingModeHalfUp   = "HALF_UP"   // 四舍五入（远离 0）
)

// Compressor 压缩模板输出空白（对照 Java EngineConfig.compressor / LineCompressor）。
// 作用于编译期的静态 Text 节点（指令输出不压缩，对照 Java「只压静态文本、缓存只压一次」）。
type Compressor interface {
	Compress(text string) string
}

// LineCompressor 是内置的空白压缩器（对照 Java stat.Compressor 的基础算法）：
//   - 连续空白字符（空格/制表/换行等，<= ' '）合并为一段；
//   - 含换行的空白段压缩为 Separator（默认 '\n'），纯空格/制表段压缩为单个空格；
//   - 非空白字符原样保留。
//
// 用 EngineConfig.SetCompressor(NewLineCompressor()) 启用。注意：多个连续空格会被压成单空格，
// 需保留多空格的场景（如 <input value="a  ">）不要启用。
type LineCompressor struct {
	Separator byte // 含换行的空白段的压缩结果，默认 '\n'；可设为 ' '（无 JS 时）
}

// NewLineCompressor 创建默认分隔符为 '\n' 的 LineCompressor。
func NewLineCompressor() *LineCompressor { return &LineCompressor{Separator: '\n'} }

// Compress 按上述规则压缩静态文本（端口自 Java stat.Compressor.compress）。
func (c *LineCompressor) Compress(content string) string {
	sep := c.Separator
	if sep == 0 {
		sep = '\n'
	}
	var b strings.Builder
	b.Grow(len(content))
	i, n := 0, len(content)
	for i < n {
		// 扫描空白段：空格/制表/换行等（<= ' '）
		hasLF := false
		start := i
		for i < n && content[i] <= ' ' {
			if content[i] == '\n' {
				hasLF = true
			}
			i++
		}
		if i > start {
			if hasLF {
				b.WriteByte(sep)
			} else {
				b.WriteByte(' ')
			}
		}
		// 复制非空白段
		for i < n && content[i] > ' ' {
			b.WriteByte(content[i])
			i++
		}
	}
	return b.String()
}

// EngineConfig holds configuration for a template engine.
type EngineConfig struct {
	directiveMap      map[string]DirectiveFactory
	sharedFunctionMap map[string]*DefineStat
	sharedObjectMap   map[string]interface{}
	baseTemplatePath  string
	encoding          string
	datePattern       string
	devMode           bool

	// staticMethodExpressionEnabled / staticFieldExpressionEnabled 控制 `::` 静态访问
	// （对照 Java isStaticMethodExpressionEnabled / isStaticFieldExpressionEnabled，默认 false）。
	// Go 无运行时 Class.forName，默认禁用并在启用后仍抛出明确错误（见 parseAtom）。
	staticMethodExpressionEnabled bool
	staticFieldExpressionEnabled  bool

	// keepLineBlankDirectives 控制行首指令是否保留其后的空行（对照 Java
	// EngineConfig.keepLineBlankDirectives，默认 false：吃掉独占行的指令尾随换行）。
	keepLineBlankDirectives bool

	// roundingMode #number 指令的舍入模式（默认 HALF_EVEN，对照 Java DecimalFormat 默认）。
	roundingMode string

	// compressor 模板输出压缩器（默认 nil，预留扩展点）。
	compressor Compressor

	// sourceFactory 由文件名构造模板来源（默认 NewFileSource，对照 Java sourceFactory）。
	sourceFactory func(fileName string) source.Source
}

// NewEngineConfig creates a default EngineConfig with the builtin directives
// registered (对照 Java EngineConfig 构造器默认注册 date/escape/number/random/render/string）。
func NewEngineConfig() *EngineConfig {
	c := &EngineConfig{
		directiveMap:      make(map[string]DirectiveFactory),
		sharedFunctionMap: make(map[string]*DefineStat),
		sharedObjectMap:   make(map[string]interface{}),
		datePattern:       defaultDatePattern,
		roundingMode:      RoundingModeHalfEven,
		sourceFactory:     func(fileName string) source.Source { return source.NewFileSource(fileName) },
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

// IsStaticMethodExpressionEnabled reports whether `Cls::method()` static method
// expressions are enabled (对照 Java isStaticMethodExpressionEnabled，默认 false)。
func (c *EngineConfig) IsStaticMethodExpressionEnabled() bool { return c.staticMethodExpressionEnabled }

// SetStaticMethodExpressionEnabled toggles `::` static method expressions.
// 注意：Go 无 Class.forName / 静态方法，启用后 `alias::method(args)` 查进程级 staticMethodKit
// 中以 AddStatic(alias, obj) 注册的命名空间对象，反射其导出方法调用——以「struct 方法」作 Java
// 静态方法的等价（不依赖实例注入 scope，区别于共享对象访问 `obj.method`）；未注册返回 nil。
// 默认禁用：禁用时模板内出现 `::` 直接报 "static ... not enabled"。
func (c *EngineConfig) SetStaticMethodExpressionEnabled(b bool) { c.staticMethodExpressionEnabled = b }

// IsStaticFieldExpressionEnabled reports whether `Cls::field` static field
// expressions are enabled (对照 Java isStaticFieldExpressionEnabled，默认 false)。
func (c *EngineConfig) IsStaticFieldExpressionEnabled() bool { return c.staticFieldExpressionEnabled }

// SetStaticFieldExpressionEnabled toggles `::` static field expressions（alias::field 无参形式）。
// 同 SetStaticMethodExpressionEnabled：启用后 alias::field 反射调用命名空间对象的无参方法
// （以无参方法返回常量值，作 Java 静态字段的等价）；默认禁用。
func (c *EngineConfig) SetStaticFieldExpressionEnabled(b bool) { c.staticFieldExpressionEnabled = b }

// KeepLineBlankDirectives reports whether blank lines after line-start directives
// are kept (对照 Java keepLineBlankDirectives，默认 false)。
func (c *EngineConfig) KeepLineBlankDirectives() bool { return c.keepLineBlankDirectives }

// SetKeepLineBlankDirectives toggles keeping blank lines after line-start directives.
func (c *EngineConfig) SetKeepLineBlankDirectives(b bool) { c.keepLineBlankDirectives = b }

// GetRoundingMode returns the rounding mode used by #number (默认 HALF_EVEN)。
func (c *EngineConfig) GetRoundingMode() string {
	if c.roundingMode == "" {
		return RoundingModeHalfEven
	}
	return c.roundingMode
}

// SetRoundingMode sets the rounding mode used by #number (RoundingModeHalfUp / RoundingModeHalfEven)。
func (c *EngineConfig) SetRoundingMode(mode string) {
	if mode == "" {
		return
	}
	c.roundingMode = mode
}

// GetCompressor returns the output compressor (默认 nil)。
func (c *EngineConfig) GetCompressor() Compressor { return c.compressor }

// SetCompressor sets the output compressor (预留扩展点，当前渲染管线未接线)。
func (c *EngineConfig) SetCompressor(cp Compressor) { c.compressor = cp }

// GetSourceFactory returns the source factory (默认 NewFileSource)。
func (c *EngineConfig) GetSourceFactory() func(fileName string) source.Source {
	if c.sourceFactory == nil {
		return func(fileName string) source.Source { return source.NewFileSource(fileName) }
	}
	return c.sourceFactory
}

// SetSourceFactory sets the source factory (对照 Java sourceFactory)。
func (c *EngineConfig) SetSourceFactory(f func(fileName string) source.Source) { c.sourceFactory = f }

// GetSharedMethodKit returns the process-wide shared method kit used when a bare
// `name(args)` call misses variables/shared objects (对照 Java EngineConfig.sharedMethodKit)。
// 注意：与扩展方法一致，Go 版为进程级注册（全局 sharedMethodKit），非 per-engine。
func (c *EngineConfig) GetSharedMethodKit() *SharedMethodKit { return sharedMethodKit }

// GetStaticMethodKit returns the process-wide static method registry used by
// `Cls::method(args)` when static method expressions are enabled (对照 Java 静态方法)。
// Go 以注册的全局函数为 Java 静态方法的等价；进程级注册，非 per-engine。
func (c *EngineConfig) GetStaticMethodKit() *StaticMethodKit { return staticMethodKit }

// AddSharedFunction loads template functions (#define) from a file into the shared
// function map (对照 Java EngineConfig.addSharedFunction(file))。文件经 sourceFactory 读取、
// 独立解析，解析期 registerDefine 收集到的 #define 合并进 sharedFunctionMap，供所有模板调用。
func (c *EngineConfig) AddSharedFunction(fileName string) error {
	if c.sourceFactory == nil {
		return fmt.Errorf("sourceFactory not configured")
	}
	content := c.sourceFactory(fileName).GetContent()
	env := NewEnv(c)
	lexer := NewLexer(content)
	if _, err := parseTemplateRecovered(lexer, env); err != nil {
		return err
	}
	for name, def := range env.functionMap {
		c.sharedFunctionMap[name] = def
	}
	return nil
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
