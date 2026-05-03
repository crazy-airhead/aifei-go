# Phase 2: Enjoy 模板引擎 (特色模块)

> 目标：完整移植 Java 版 Enjoy 模板引擎到 Go，这是 Aifei 的核心特色模块。
> Enjoy 不仅用于 HTML 模板渲染，更重要的是作为 **Enjoy SQL** 引擎驱动数据库条件查询。

## 1. 模块总览

```
enjoy/
├── engine.go              # Engine 入口 + 模板缓存
├── engine_config.go       # 引擎配置
├── template.go            # Template 编译执行
├── env.go                 # 模板执行环境
├── directive.go           # Directive 基类
├── scope.go               # Scope 变量作用域
├── ctrl.go                # 执行控制 (break/continue/return)
├── stat/                  # 语句层 AST
├── expr/                  # 表达式层 AST
├── io/                    # 输出抽象
└── source/                # 模板源加载
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
func (e *Engine) SetEncoding(encoding string)
func (e *Engine) SetDatePattern(pattern string)
func (e *Engine) AddSharedFunction(fileName string)
func (e *Engine) AddDirective(name string, directive DirectiveFactory)
func (e *Engine) AddSharedMethod(name string, fn interface{})
func (e *Engine) AddSharedObject(name string, obj interface{})

// 内部
func (e *Engine) GetConfig() *EngineConfig
func (e *Engine) RemoveAllTemplateCache()
```

## 4. EngineConfig (对应 `cn.aifei.enjoy.EngineConfig`)

```go
type EngineConfig struct {
    directiveMap     map[string]DirectiveFactory
    sharedFunctionMap map[string]*Define  // 共享函数
    sharedObjectMap  map[string]interface{}
    writerBuffer     *WriterBuffer
    sourceFactory    SourceFactory
    baseTemplatePath string
    encoding         string
    datePattern      string
    devMode          bool
}
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
```

## 8. Scope (对应 `cn.aifei.enjoy.stat.Scope`)

```go
type Scope struct {
    data   map[string]interface{}
    parent *Scope
}

func NewScope(data map[string]interface{}) *Scope
func (s *Scope) Get(key string) interface{}
func (s *Scope) Set(key string, value interface{})
func (s *Scope) SetLocal(key string, value interface{})
func (s *Scope) SetGlobal(key string, value interface{})
func (s *Scope) Exists(key string) bool
func (s *Scope) NewChild() *Scope  // 创建子作用域
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
func Parse(lexer *Lexer, config *EngineConfig) (Stat, error)
```

解析过程：
1. `statList()` → 解析语句列表
2. `stat()` → 解析单条语句
3. 遇到 `#if` → 创建 If AST 节点，递归解析 elseif/else/end
4. 遇到 `#for` → 创建 For AST 节点，递归解析循环体
5. 遇到 `#set` → 解析赋值表达式
6. 遇到 `#define` → 创建 Define 节点
7. 遇到自定义指令 → 查找 directiveMap，调用 factory 创建

## 12. ExprLexer — 表达式词法分析 (对应 `cn.aifei.enjoy.expr.ExprLexer`)

```go
type ExprLexer struct { ... }

func NewExprLexer(input string) *ExprLexer
func (l *ExprLexer) Scan() (Tok, string)
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

## 14. 表达式 AST 节点

| Go 类型 | 对应 Java | 功能 |
|---------|-----------|------|
| `Id` | Id | 变量标识符 |
| `Const` | Const | 常量 (string/int/float/bool/null) |
| `Arith` | Arith | 算术: + - * / % |
| `Compare` | Compare | 比较: == != < <= > >= |
| `Logic` | Logic | 逻辑: && \|\| ! |
| `Ternary` | Ternary | 三元: cond ? a : b |
| `NullSafe` | NullSafe | 空安全: ?? ?. |
| `Field` | Field | 字段访问: obj.field |
| `Method` | Method | 方法调用: obj.method(args) |
| `Index` | Index | 索引: arr[i] |
| `Assign` | Assign | 赋值: x = expr |
| `IncDec` | IncDec | 自增自减: ++ -- |
| `Array` | Array | 数组: [1, 2, 3] |
| `MapExpr` | Map | Map: {"k": "v"} |
| `Range` | RangeArray | 范围: [0..10] |
| `SharedMethod` | SharedMethod | 共享方法 |
| `StaticMethod` | StaticMethod | 静态方法: Class::method() |
| `StaticField` | StaticField | 静态字段: Class::field |
| `NullExpr` | NullExpr | null 表达式 |

## 15. 语句 AST 节点

| Go 类型 | 对应 Java | 功能 |
|---------|-----------|------|
| `StatList` | StatList | 语句列表 (AST 根) |
| `Text` | Text | 纯文本输出 |
| `Output` | Output | 表达式输出 #() |
| `If` | If | 条件 #if / #elseif / #else |
| `For` | For | 循环 #for (item : list) / #for (i=0; i<n; i++) |
| `Set` | Set | 赋值 #set / #setLocal / #setGlobal |
| `Define` | Define | 函数定义 #define |
| `Include` | Include | 包含 #include |
| `Call` | Call | 函数调用 #call / #@name |
| `Switch` | Switch | 开关 #switch / #case / #default |
| `Break` | Break | 跳出 #break |
| `Continue` | Continue | 继续 #continue |
| `Return` | Return | 返回 #return |
| `NullStat` | NullStat | 空语句 |

## 16. 源加载系统

```go
type Source interface {
    IsModified() bool
    GetCacheKey() string
    GetContent() string
}

type FileSource struct { ... }    // 文件系统
type StringSource struct { ... }  // 字符串
```

## 17. Enjoy SQL — DB 模块中的 enjoy 应用

这是 enjoy 引擎最重要的应用场景，在 `db/sql/` 子包中实现：

### SqlKit (对应 `cn.aifei.db.sql.SqlKit`)

```go
type SqlKit struct {
    configName string
    engine     *enjoy.Engine
    cache      sync.Map  // sqlId → *enjoy.Template
}

func NewSqlKit(configName string) *SqlKit

// 配置
func (k *SqlKit) SetBaseSqlFilePath(path string)
func (k *SqlKit) AddSqlFile(sqlFile string)
func (k *SqlKit) AddSql(sqlID, sql string)

// 获取 SQL + 参数
func (k *SqlKit) GetSqlPara(sqlID string, data map[string]interface{}) *SqlPara
func (k *SqlKit) GetSqlParaByArgs(sqlID string, args ...interface{}) *SqlPara
func (k *SqlKit) GetSqlParaFromString(sql string, data map[string]interface{}) *SqlPara
func (k *SqlKit) GetSqlParaFromStringByArgs(sql string, args ...interface{}) *SqlPara
```

### SQL 指令

| 指令 | 文件 | 功能 |
|------|------|------|
| `#sql("id") ... #end` | sql_directive.go | 定义 SQL 片段 (用于外部文件) |
| `#para(n)` | para_directive.go | 位置参数 → `?` |
| `#para(name)` | para_directive.go | 命名参数 → `?` (从 Map 取值) |
| `#para(name, "like")` | para_directive.go | LIKE 参数 → `%value%` |
| `#para(name, "in")` | para_directive.go | IN 参数 → `(?, ?, ?)` |
| `#where(field, op, para)` | where_directive.go | 动态 WHERE (值为 null 时不生成) |
| `#and(field, op, para)` | and_directive.go | 动态 AND (值为 null 时不生成) |
| `#orderBy(f1, f2, ...)` | orderby_directive.go | 动态 ORDER BY (白名单防注入) |

### Enjoy SQL 使用示例

```go
// 1. 基本参数查询
// Java: Db.sql("select * from user where id = #para(0)", 123).find();
// Go:
rows, err := db.SQL("select * from user where id = #para(0)", 123).Find()

// 2. 命名参数查询
// Java: Db.sql("select * from user where name = #para(name) and age > #para(age)", kv).find();
// Go:
data := map[string]interface{}{"name": "james", "age": 18}
rows, err := db.SQLWithData("select * from user where name = #para(name) and age > #para(age)", data).Find()

// 3. #where / #and 动态条件 (核心特色)
// Java: Db.sql("select * from user #where(age, '>', age) #and(name, 'contains', name)", filter).find();
// Go:
filter := map[string]interface{}{"age": 18, "name": "james"}
rows, err := db.SQLWithData(
    "select * from user #where(age, '>', age) #and(name, 'contains', name)",
    filter,
).Find()
// 生成: select * from user WHERE age > ? AND name LIKE ?
// 参数: [18, "%james%"]

// 4. #orderBy 动态排序 (防 SQL 注入)
// Java: Db.sql("select * from user #orderBy(id, name)", data).find();
// Go:
data := map[string]interface{}{
    "orderBy": map[string]interface{}{"field": "id", "order": "desc"},
}
rows, err := db.SQLWithData("select * from user #orderBy(id, name)", data).Find()
// 生成: select * from user ORDER BY id DESC

// 5. 外部 SQL 文件
// 文件: sql/user.sql
// #sql("findById")
//   select * from user where id = #para(0)
// #end
rows, err := db.SQLByID("findById", 123).Find()
```

### Operator 支持的完整列表

| 操作符 | SQL 生成 | 说明 |
|--------|---------|------|
| `=` | `field = ?` | 等于 |
| `!=` | `field != ?` | 不等于 |
| `>` | `field > ?` | 大于 |
| `>=` | `field >= ?` | 大于等于 |
| `<` | `field < ?` | 小于 |
| `<=` | `field <= ?` | 小于等于 |
| `like` | `field LIKE ?` | 模糊匹配 |
| `not like` | `field NOT LIKE ?` | 反模糊 |
| `contains` | `field LIKE ?` (值: %v%) | 包含 |
| `notContains` | `field NOT LIKE ?` (值: %v%) | 不包含 |
| `startsWith` | `field LIKE ?` (值: v%) | 开头匹配 |
| `endsWith` | `field LIKE ?` (值: %v) | 结尾匹配 |
| `in` | `field IN (?, ?, ?)` | IN 查询 |
| `not in` | `field NOT IN (?, ?, ?)` | NOT IN |
| `between` | `field BETWEEN ? AND ?` | 范围 |
| `not between` | `field NOT BETWEEN ? AND ?` | 反范围 |
| `is null` | `field IS NULL` | 空判断 (无参数) |
| `is not null` | `field IS NOT NULL` | 非空判断 (无参数) |
