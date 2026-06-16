# Phase 2: Enjoy 模板引擎 (特色模块)

> 目标：完整移植 Java 版 Enjoy 模板引擎到 Go，这是 Aifei 的核心特色模块。
> Enjoy 不仅用于 HTML 模板渲染，更重要的是作为 **Enjoy SQL** 引擎驱动数据库条件查询。

## 1. 模块总览

Enjoy 使用**扁平文件结构**（非子目录结构），共 15 个文件：

```
enjoy/
├── engine.go              # Engine 入口 + 模板缓存
├── engine_config.go       # 引擎配置
├── template.go            # Template 编译执行
├── env.go                 # 模板执行环境
├── directive.go           # Directive 接口
├── scope.go               # Scope 变量作用域
├── ctrl.go                # 执行控制 (break/continue/return)
├── lexer.go               # 模板词法分析器 (DKFF 算法)
├── stat_parser.go         # 模板语法分析器 (DLRD 递归下降)
├── tok.go                 # Token 类型定义
├── stat.go                # 语句 AST 节点
├── expr.go                # 表达式 AST 接口
├── expr_eval.go           # 表达式求值
├── expr_lexer.go          # 表达式词法分析器
├── expr_parser.go         # 表达式语法分析器 (运算符优先级)
├── expr_list.go           # 表达式列表
└── source/
    └── source.go          # FileSource + StringSource
```

## 2. 编译管线

```
Source (文件/字符串) → Lexer (词法分析) → Token 流 → Parser (语法分析) → AST → Template (缓存)
                                                                         ↓
执行: Template.render(data) → 创建 Scope → AST 递归执行 → Writer 输出
```

## 3. Engine (对应 `cn.aifei.enjoy.Engine`)

```go
package enjoy

type Engine struct {
    config        *EngineConfig
    templateCache sync.Map  // string → *Template
}

func NewEngine(name string) *Engine

// 模板获取
func (e *Engine) GetTemplate(fileName string) *Template
func (e *Engine) GetTemplateByString(content string) *Template

// 配置
func (e *Engine) SetDevMode(devMode bool)
func (e *Engine) SetBaseTemplatePath(path string)
func (e *Engine) AddSharedFunction(name string, fn interface{})
func (e *Engine) AddDirective(name string, directive DirectiveFactory)
func (e *Engine) AddSharedObject(name string, obj interface{})

// 内部
func (e *Engine) GetConfig() *EngineConfig
func (e *Engine) RemoveAllTemplateCache()
```

> 注：`SetEncoding` 和 `SetDatePattern` 未在 Go 版中实现（Go 统一使用 UTF-8，日期格式化通过 Go 标准方式处理）。

## 4. EngineConfig (对应 `cn.aifei.enjoy.EngineConfig`)

```go
type EngineConfig struct {
    directiveMap      map[string]DirectiveFactory
    sharedFunctionMap map[string]*Define  // 共享函数
    sharedObjectMap   map[string]interface{}
    baseTemplatePath  string
    devMode           bool
}

func NewEngineConfig() *EngineConfig
```

## 5. Template (对应 `cn.aifei.enjoy.Template`)

```go
type Template struct {
    env    *Env
    ast    Stat
    source Source
}

func (t *Template) Render(data map[string]interface{}, writer io.Writer) error
func (t *Template) RenderToString(data map[string]interface{}) string
func (t *Template) IsModified() bool
```

## 6. Env (对应 `cn.aifei.enjoy.Env`)

```go
type Env struct {
    engineConfig *EngineConfig
    functionMap  map[string]*Define  // 模板局部函数
    sourceList   []Source
}

func NewEnv(config *EngineConfig) *Env
func (e *Env) GetEngineConfig() *EngineConfig
func (e *Env) GetFunction(name string) *Define
func (e *Env) AddFunction(name string, def *Define)
```

## 7. Directive (对应 `cn.aifei.enjoy.Directive`)

```go
type Directive interface {
    SetExprList(exprList *ExprList)
    SetStat(stat Stat)
    Exec(env *Env, scope *Scope, writer io.Writer)
    HasEnd() bool
}

type DirectiveFactory func() Directive
```

## 8. Scope (对应 `cn.aifei.enjoy.stat.Scope`)

```go
type Scope struct {
    data   map[string]interface{}
    parent *Scope
}

func NewScope(data map[string]interface{}) *Scope
func (s *Scope) Get(key string) (interface{}, bool)
func (s *Scope) Set(key string, value interface{})
func (s *Scope) SetLocal(key string, value interface{})
func (s *Scope) SetGlobal(key string, value interface{})
func (s *Scope) Exists(key string) bool
func (s *Scope) NewChild() *Scope  // 创建子作用域
func (s *Scope) Data() map[string]interface{}
```

## 9. Ctrl (对应 `cn.aifei.enjoy.stat.Ctrl`)

```go
type Ctrl struct {
    Break    bool
    Continue bool
    Return   bool
    Wisdom   bool  // 智能赋值模式
    NullSafe bool  // 空安全模式
    Attachment interface{} // 附带数据
}

func NewCtrl() *Ctrl
func (c *Ctrl) Reset()
```

## 10. Lexer — 模板词法分析 (对应 `cn.aifei.enjoy.stat.Lexer`)

DKFF (Dynamic Key Feature Forward) 算法，将模板文本转为 Token 流。

**支持的 Token 类型：**
```
TEXT         — 纯文本内容
OUTPUT       — 输出指令 #()
IF           — #if
ELSEIF       — #elseif
ELSE         — #else
END          — #end
FOR          — #for
SET          — #set / #setLocal / #setGlobal
DEFINE       — #define
INCLUDE      — #include
CALL         — #call / #@name
SWITCH       — #switch
CASE         — #case
DEFAULT      — #default
BREAK        — #break
CONTINUE     — #continue
RETURN       — #return / #returnIf
ID           — 自定义指令标识符
PARA         — 指令参数
```

**特殊语法：**
- `#-- ... --#` — 注释块
- `#[[ ... ]]#` — 原始文本块 (不解析)
- `###` — 单行注释
- `#(expr)` — 表达式输出

## 11. Parser — 模板语法分析 (对应 `cn.aifei.enjoy.stat.Parser`)

DLRD (Double Layer Recursive Descent) 递归下降解析器。

```go
func ParseTemplate(lexer *Lexer, env *Env) (Stat, error)
```

解析过程：
1. `statList()` → 解析语句列表
2. `parseOneStat()` → 解析单条语句
3. 遇到 `#if` → 创建 IfStat AST 节点，递归解析 elseif/else/end
4. 遇到 `#for` → 创建 ForStat AST 节点，递归解析循环体
5. 遇到 `#set` → 解析赋值表达式，创建设置语句
6. 遇到 `#define` → 创建 DefineStat 节点
7. 遇到自定义指令 → 查找 directiveMap，调用 factory 创建

## 12. ExprLexer — 表达式词法分析 (对应 `cn.aifei.enjoy.expr.ExprLexer`)

```go
type ExprLexer struct { ... }

func NewExprLexer(input string) *ExprLexer
func (l *ExprLexer) Scan() (ETok, string)
```

**扫描能力：**
- 标识符和关键字
- 运算符: `+`, `-`, `*`, `/`, `%`, `==`, `!=`, `<`, `<=`, `>`, `>=`
- 逻辑: `&&`, `||`, `!`
- 字符串: `'...'`, `"..."`
- 数字: int, long, float, double
- 特殊: `??`, `?.`, `..`, `::`, `++`, `--`

## 13. ExprParser — 表达式语法分析 (对应 `cn.aifei.enjoy.expr.ExprParser`)

**运算符优先级 (从低到高)：**
```
1. 赋值:      =
2. 三元:      ? :
3. 逻辑或:    ||
4. 逻辑与:    &&
5. 相等:      == !=
6. 比较:      < <= > >=
7. 算术加减:  + -
8. 算术乘除:  * / %
9. 一元:      ! - ++ --
10. 后缀:     . [] () ?. ??
11. 原子:     id const (expr) [array] {map}
```

```go
func ParseExpr(input string) (Expr, error)
```

## 14. 表达式 AST 节点

| Go 类型 | 功能 |
|---------|------|
| `IDExpr` | 变量标识符 |
| `ConstExpr` | 常量 (string/int/float/bool/null) |
| `ArithExpr` | 算术: + - * / % |
| `CompareExpr` | 比较: == != < <= > >= |
| `LogicExpr` | 逻辑: && \|\| ! |
| `TernaryExpr` | 三元: cond ? a : b |
| `NullCoalesceExpr` | 空合并: ?? |
| `NullSafeExpr` | 空安全: ?. |
| `FieldExpr` | 字段访问: obj.field |
| `MethodExpr` | 方法调用: obj.method(args) |
| `IndexExpr` | 索引: arr[i] |
| `AssignExpr` | 赋值: x = expr |
| `IncDecExpr` | 自增自减: ++ -- |
| `ArrayExpr` | 数组: [1, 2, 3] |
| `MapExpr` | Map: {"k": "v"} |
| `RangeExpr` | 范围: [0..10] |

所有节点类型在 `expr.go` 中定义，求值逻辑集中在 `expr_eval.go`。

## 15. 语句 AST 节点

| Go 类型 | 功能 | 状态 |
|---------|------|------|
| `StatList` | 语句列表 (AST 根) | 已实现 |
| `Text` | 纯文本输出 | 已实现 |
| `Output` | 表达式输出 #() | 已实现 |
| `IfStat` | 条件 #if / #elseif / #else | 已实现 |
| `ForStat` | 循环 #for | 已实现 |
| `SetStat` | 赋值 #set / #setLocal / #setGlobal | 已实现 |
| `DefineStat` | 函数定义 #define | 已实现 |
| `CallStat` | 函数调用 #call / #@name | 已实现 |
| `BreakStat` | 跳出 #break | 已实现 |
| `ContinueStat` | 继续 #continue | 已实现 |
| `ReturnStat` | 返回 #return | 已实现 |
| `NullStat` | 空语句 | 已实现 |
| `DirectiveStat` | 自定义指令 | 已实现 |
| `SetAsStat` | #set 带 as 别名 | 已实现 |
| `IncludeStat` | #include 模板包含 | 已实现 |
| `SwitchStat` | #switch / #case / #default | 已实现 |

所有节点类型在 `stat.go` 中定义，解析逻辑在 `stat_parser.go`。

## 16. 源加载系统

```go
// source/source.go
type Source interface {
    IsModified() bool
    GetCacheKey() string
    GetContent() string
}

type FileSource struct { ... }    // 文件系统
type StringSource struct { ... }  // 字符串

func NewFileSource(filePath string) *FileSource
func NewStringSource(content string) *StringSource
```

## 17. Enjoy SQL

Enjoy SQL 是 enjoy 引擎在数据库模块中的重要应用，详见 [`03-phase3-db.md` 第 2 节](03-phase3-db.md#2-enjoy-sql-模板引擎dbsql已实现)。
