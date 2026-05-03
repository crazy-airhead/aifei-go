# Phase 2 实施提示词: Enjoy 模板引擎 (特色模块)

## Prompt 2.1: 核心框架 — Engine, Template, Env, Scope

```
在 aifei 框架的 enjoy/ 子包中创建 Enjoy 模板引擎核心组件。
这是一个完整的模板引擎，对应 Java 版 cn.aifei.enjoy 包。
同时也是 Enjoy SQL 的基础引擎。

1. enjoy/scope.go — 变量作用域:
   Scope 结构体:
   - data map[string]interface{}
   - parent *Scope

   方法:
   - NewScope(data map[string]interface{}) *Scope
   - (s *Scope) Get(key string) interface{} — 从当前 scope 向上查找
   - (s *Scope) Set(key string, value interface{})
   - (s *Scope) SetLocal(key string, value interface{}) — 只设在当前 scope
   - (s *Scope) SetGlobal(key string, value interface{}) — 沿 parent 链设到根 scope
   - (s *Scope) Exists(key string) bool — 只检查当前 scope (不查 parent)
   - (s *Scope) NewChild() *Scope — 创建子 scope

2. enjoy/ctrl.go — 执行控制:
   Ctrl 结构体:
   - Break bool
   - Continue bool
   - Return bool
   - Wisdom bool
   - NullSafe bool
   - Attachment interface{}

3. enjoy/directive.go — 指令基类:
   ExprList 类型: exprList []Expr

   Directive 接口:
   - SetExprList(exprList *ExprList)
   - SetStat(stat Stat)
   - Exec(env *Env, scope *Scope, writer io.Writer)
   - HasEnd() bool

   BaseDirective 结构体 (提供默认实现):
   - exprList *ExprList
   - stat Stat
   - location *Location

4. enjoy/env.go — 模板环境:
   Env 结构体:
   - engineConfig *EngineConfig
   - functionMap map[string]*DefineStat

   方法:
   - NewEnv(config *EngineConfig) *Env
   - GetConfig() *EngineConfig
   - GetFunction(name string) *DefineStat
   - AddFunction(name string, def *DefineStat)

5. enjoy/engine_config.go — 引擎配置:
   EngineConfig 结构体:
   - directiveMap map[string]func() Directive  // 指令工厂
   - sharedFunctionMap map[string]Stat         // 共享函数
   - sharedObjectMap map[string]interface{}
   - baseTemplatePath string
   - encoding string
   - datePattern string
   - devMode bool

6. enjoy/template.go — 模板:
   Template 结构体:
   - env *Env
   - ast Stat
   - source Source

   方法:
   - (t *Template) Render(data map[string]interface{}, writer io.Writer) error
     实现: 创建 Scope(data) → ast.Exec(env, scope, writer)
   - (t *Template) RenderToString(data map[string]interface{}) string
     实现: Render 到 strings.Builder
   - (t *Template) IsModified() bool

7. enjoy/engine.go — 引擎入口:
   Engine 结构体:
   - config *EngineConfig
   - templateCache sync.Map
   - name string

   方法:
   - NewEngine(name string) *Engine
   - GetTemplate(fileName string) *Template
     实现: 查缓存 → 没有则 loadSource → Lexer → Parser → 缓存
   - GetTemplateByString(content string) *Template
   - SetDevMode(bool)
   - SetBaseTemplatePath(string)
   - SetEncoding(string)
   - SetDatePattern(string)
   - AddSharedFunction(fileName string)
   - AddDirective(name string, factory func() Directive)
   - AddSharedObject(name string, obj interface{})
   - GetConfig() *EngineConfig
   - RemoveAllTemplateCache()

注意:
- Stat 和 Expr 接口先定义占位，后续 Prompt 实现
- 使用 io.Writer 作为输出接口 (Go 标准库)
- 模板缓存使用 sync.Map (并发安全)
```

---

## Prompt 2.2: 表达式层 — Lexer, Parser, AST

```
在 aifei 框架的 enjoy/expr/ 子包中创建表达式引擎:

1. enjoy/expr/ast.go — 表达式基类:
   Expr 接口:
   - Eval(scope *Scope) interface{}

2. enjoy/expr/id.go — 变量标识符:
   Id 结构体: id string
   - Eval(scope) → scope.Get(id)

3. enjoy/expr/const.go — 常量:
   Const 结构体:
   - valueType int (TYPE_STR/TYPE_INT/TYPE_LONG/TYPE_FLOAT/TYPE_DOUBLE/TYPE_BOOL/TYPE_NULL)
   - strVal string
   - intVal int
   - longVal int64
   - floatVal float64
   - boolVal bool

   方法: Eval, IsStr, IsInt, IsLong, IsFloat, IsDouble, IsBool, IsNull
   GetStr, GetInt, GetLong, GetFloat, GetDouble, GetBool

4. enjoy/expr/arith.go — 算术运算:
   Arith 结构体: op int (ADD/SUB/MUL/DIV/MOD), left Expr, right Expr
   - Eval: 递归计算 left op right
   - 支持数字类型自动提升 (int→int64→float64)

5. enjoy/expr/compare.go — 比较:
   Compare 结构体: op int (EQ/NE/LT/LE/GT/GE), left Expr, right Expr
   - Eval: 返回 bool

6. enjoy/expr/logic.go — 逻辑:
   Logic 结构体: op int (AND/OR/NOT), left Expr, right Expr (NOT 只有 left)
   - Eval: 短路求值

7. enjoy/expr/ternary.go — 三元:
   Ternary 结构体: cond Expr, trueExpr Expr, falseExpr Expr
   - Eval: cond ? trueExpr : falseExpr

8. enjoy/expr/null_safe.go — 空安全:
   NullSafe 结构体: op int (NULL_COALESCE/OPTIONAL_CHAIN), left Expr, right Expr
   - ??: left != nil → left, else → right
   - ?.: left == nil → nil, else → right (字段/方法访问)

9. enjoy/expr/field.go — 字段访问:
   Field 结构体: expr Expr, fieldName string
   - Eval: 先 expr.Eval → 通过反射获取字段值
   - 支持 map[key], struct.field

10. enjoy/expr/method.go — 方法调用:
    Method 结构体: expr Expr, methodName string, args []Expr
    - Eval: expr.Eval → 通过反射调用方法

11. enjoy/expr/index.go — 索引:
    Index 结构体: expr Expr, index Expr
    - Eval: expr.Eval → [index] 访问 (slice/array/map)

12. enjoy/expr/assign.go — 赋值:
    Assign 结构体: id string, expr Expr
    - Eval: scope.Set(id, expr.Eval)

13. enjoy/expr/array.go — 数组字面量:
    ArrayExpr 结构体: elements []Expr
    - Eval: 返回 []interface{}

14. enjoy/expr/map.go — Map 字面量:
    MapExpr 结构体: keys []Expr, values []Expr
    - Eval: 返回 map[string]interface{}

15. enjoy/expr/range.go — 范围:
    Range 结构体: start Expr, end Expr
    - Eval: 返回 []int{start, start+1, ..., end}

16. enjoy/expr/expr_lexer.go — 表达式词法分析器:
    ExprLexer 结构体: input string, pos int
    - NewExprLexer(input string) *ExprLexer
    - (l *ExprLexer) Scan() (tok Tok, value string)
    Tok 类型: int (IDENT/STRING/INT/LONG/FLOAT/DOUBLE/PLUS/MINUS/STAR/SLASH/...)
    Sym 常量定义所有运算符

17. enjoy/expr/expr_parser.go — 表达式语法分析器:
    ExprParser 结构体: lexer *ExprLexer, currentTok Tok, currentVal string
    - NewExprParser(input string) *ExprParser
    - (p *ExprParser) Parse() Expr
    - (p *ExprParser) ParseExprList() *ExprList

    递归下降方法 (运算符优先级从低到高):
    - parseAssign() → parseTernary()
    - parseTernary() → parseLogicOr()
    - parseLogicOr() → parseLogicAnd()
    - parseLogicAnd() → parseEqual()
    - parseEqual() → parseCompare()
    - parseCompare() → parseAdd()
    - parseAdd() → parseMul()
    - parseMul() → parseUnary()
    - parseUnary() → parsePostfix()
    - parsePostfix() → parseAtom()  (处理 . [] () ?. ??)
    - parseAtom() → Id/Const/(expr)/[array]/{map}

注意:
- 数字类型自动提升: int + int64 → int64, int + float → float64
- 所有 Eval 返回 interface{}, nil 表示空值
- 字段/方法访问使用 reflect 包
- ExprParser 要处理运算符优先级和结合性
```

---

## Prompt 2.3: 语句层 — Lexer, Parser, AST

```
在 aifei 框架的 enjoy/stat/ 子包中创建语句层:

1. enjoy/stat/ast.go — 语句基类:
   Stat 接口:
   - Exec(env *Env, scope *Scope, writer io.Writer, ctrl *Ctrl)

2. enjoy/stat/token.go — Token:
   Token 结构体: Type int, Value string, Location *Location
   TokType 常量: TEXT, OUTPUT, IF, ELSEIF, ELSE, END, FOR, SET, DEFINE,
                INCLUDE, CALL, SWITCH, CASE, DEFAULT, BREAK, CONTINUE,
                RETURN, ID, PARA

3. enjoy/stat/location.go — 位置:
   Location 结构体: FileName string, Line int

4. enjoy/stat/stat_list.go — 语句列表:
   StatList 结构体: stats []Stat
   - Exec: 顺序执行所有 stat，遇到 ctrl.Break/Continue/Return 停止

5. enjoy/stat/text.go — 纯文本:
   TextStat 结构体: text string
   - Exec: writer.Write(text)

6. enjoy/stat/output.go — 表达式输出:
   OutputStat 结构体: expr Expr
   - Exec: val := expr.Eval(scope) → writer.Write(fmt.Sprint(val))

7. enjoy/stat/if.go — 条件:
   IfStat 结构体:
   - cond Expr
   - stat Stat (if body)
   - elseIfList []ElseIfStat
   - elseStat Stat

   ElseIfStat 结构体: cond Expr, stat Stat

   - Exec: cond.Eval → true → 执行 stat → 否则检查 elseif → else

8. enjoy/stat/for.go — 循环:
   ForStat 结构体:
   - forType int (ITERATOR/C_STYLE)
   // 迭代器模式: #for(id : list)
   - id string, listExpr Expr
   // C 风格: #for(init; cond; update)
   - forCtrl *ForCtrl
   - stat Stat (循环体)
   - elseStat Stat (空集合时执行)

   ForCtrl 结构体: init Stat, cond Expr, update Stat

   - Exec:
     迭代器: list → range 遍历 → scope.Set(id, item) → stat.Exec
     C风格: init → while cond → stat.Exec → update

9. enjoy/stat/set.go — 赋值:
   SetStat 结构体: setType int (SET/SET_LOCAL/SET_GLOBAL), id string, expr Expr
   - Exec: 根据 setType 调用 scope.Set/SetLocal/SetGlobal

10. enjoy/stat/define.go — 函数定义:
    DefineStat 结构体: name string, paramNames []string, stat Stat
    - Exec: env.AddFunction(name, this) (注册函数，不执行)
    - Call: 创建子 scope，绑定参数 → stat.Exec

11. enjoy/stat/include.go — 包含:
    IncludeStat 结构体: fileNameExpr Expr, dataExpr Expr
    - Exec: 解析 fileName → engine.GetTemplate → template.Render

12. enjoy/stat/call.go — 函数调用:
    CallStat 结构体: funcName string, args []Expr
    - Exec: env.GetFunction → define.Call(args)

13. enjoy/stat/switch.go — 开关:
    SwitchStat 结构体: expr Expr, cases []CaseStat, defaultStat Stat
    CaseStat 结构体: values []Expr, stat Stat
    - Exec: expr.Eval → 匹配 case values → 执行对应 stat

14. enjoy/stat/flow.go — 流程控制:
    BreakStat, ContinueStat, ReturnStat
    - Exec: 设置 ctrl.Break/Continue/Return = true

15. enjoy/stat/lexer.go — 模板词法分析器:
    Lexer 结构体: input string, pos int

    核心方法:
    - NewLexer(input string, fileName string) *Lexer
    - (l *Lexer) Scan() Token

    实现逻辑:
    - 普通文本: 扫描到 '#' 或 EOF 为止
    - '#(' → OUTPUT 指令，扫描括号内表达式
    - '#if' → IF token
    - '#elseif' → ELSEIF token
    - '#else' → ELSE token
    - '#end' → END token
    - '#for' → FOR token
    - '#set' → SET token
    - '#define' → DEFINE token
    - '#include' → INCLUDE token
    - '#call' → CALL token
    - '#switch' → SWITCH token
    - '#case' → CASE token
    - '#default' → DEFAULT token
    - '#break' → BREAK token
    - '#continue' → CONTINUE token
    - '#return' → RETURN token
    - '#--' → 注释块，扫描到 '--#'
    - '#[[' → 原始文本块，扫描到 ']]#'
    - '###' → 单行注释
    - 其他 '#' 开头 → ID token (自定义指令)
    - 指令参数: '(' ... ')' 内的内容

16. enjoy/stat/parser.go — 模板语法分析器:
    Parser 结构体: tokens []Token, pos int, config *EngineConfig

    核心方法:
    - NewParser(tokens []Token, config *EngineConfig) *Parser
    - (p *Parser) Parse() (Stat, error)
    - (p *Parser) parseStatList(endTokens ...int) (Stat, error)
    - (p *Parser) parseStat() (Stat, error)

    解析逻辑:
    - TEXT → TextStat
    - OUTPUT → OutputStat (用 ExprParser 解析参数)
    - IF → parseIf() → IfStat (递归解析 elseif/else/end)
    - FOR → parseFor() → ForStat
    - SET → parseSet() → SetStat
    - DEFINE → parseDefine() → DefineStat
    - INCLUDE → parseInclude() → IncludeStat
    - CALL → parseCall() → CallStat
    - SWITCH → parseSwitch() → SwitchStat
    - 自定义指令 → 查找 directiveMap → 创建 Directive → 设置参数和 body

注意:
- Lexer 要正确处理 '#' 转义和边界情况
- Parser 对 #end 的匹配要严格 (每个有 #end 的指令必须配对)
- ForStat 的迭代器模式要支持 for(index, item : list) 双变量
- 所有错误要包含 Location 信息 (文件名+行号)
```

---

## Prompt 2.4: Source 加载 + IO + SqlKit 集成

```
在 aifei 框架中创建模板源加载和 Enjoy SQL 集成:

1. enjoy/source/source.go — Source 接口:
   type Source interface {
       IsModified() bool
       GetCacheKey() string
       GetContent() string
   }

2. enjoy/source/file_source.go — 文件源:
   FileSource 结构体: fileName string, encoding string, lastModified time.Time
   - IsModified: 检查文件修改时间
   - GetContent: os.ReadFile 读取文件

3. enjoy/source/string_source.go — 字符串源:
   StringSource 结构体: content string, cacheKey string
   - IsModified: 始终返回 false
   - GetContent: 返回 content

4. enjoy/io/writer.go — Writer 接口 (简化版):
   直接使用 Go 标准库 io.Writer，不需要自定义 Writer

5. db/sql/sql_para.go — SQL 参数容器:
   SqlPara 结构体:
   - ID string
   - Sql string
   - Paras []interface{}
   - enjoySql bool

   方法:
   - (p *SqlPara) AddPara(v interface{})
   - (p *SqlPara) SetSql(sql string)
   - (p *SqlPara) IsEnjoySql() bool

6. db/sql/sql_kit.go — Enjoy SQL 引擎封装:
   SqlKit 结构体:
   - configName string
   - engine *enjoy.Engine
   - cache sync.Map
   - sqlFiles []string
   - sqlFromFileCache sync.Map

   构造函数:
   NewSqlKit(configName string) *SqlKit
   实现:
   - 创建 enjoy.Engine 实例
   - 注册 SQL 指令: #sql, #where, #and, #orderBy, #para, #p

   方法:
   - AddSqlFile(file string) — 添加外部 SQL 文件
   - AddSql(sqlID, sql string) — 添加 SQL 字符串
   - ParseSqlFile() — 解析外部 SQL 文件
   - GetSqlPara(sqlID string, data map[string]interface{}) *SqlPara
   - GetSqlParaByArgs(sqlID string, args ...interface{}) *SqlPara
   - GetSqlParaFromString(sql string, data map[string]interface{}) *SqlPara
   - GetSqlParaFromStringByArgs(sql string, args ...interface{}) *SqlPara

7. db/sql/sql_directive.go — #sql 指令:
   SqlDirective 结构体: id string
   - SetExprList: 解析 ID 字符串常量
   - HasEnd: true
   - Exec: 将 body 注册到 sqlCache[id]

8. db/sql/para_directive.go — #para 指令:
   ParaDirective 结构体:
   - index int (-1 表示命名参数)
   - paraName string
   - paraType int (TYPE_PLAIN/TYPE_LIKE/TYPE_LIKE_LEFT/TYPE_LIKE_RIGHT/TYPE_IN)

   功能:
   - #para(0): 位置参数 → 写入 "?" + 添加 paras[0]
   - #para(name): 命名参数 → scope.Get(name) → 写入 "?" + 添加值
   - #para(name, "like"): → 写入 "?" + 添加 "%" + value + "%"
   - #para(name, "in"): → 写入 "(?, ?, ?)" + 添加所有值
   - #para(0, "like"): 位置参数 + like

9. db/sql/where_directive.go — #where 指令:
   WhereDirective 结构体: condition *Condition
   - Exec:
     1. 创建 firstCondition = [true]
     2. 传递给后续 #and 使用
     3. 调用 condition.Generate()
     4. 第一个条件生成 "WHERE"，后续生成 "AND"
     5. 参数值为 null 时不生成

10. db/sql/and_directive.go — #and 指令:
    AndDirective 结构体: condition *Condition
    - Exec: 与 #where 相同逻辑，但不创建 firstCondition (复用 #where 的)

11. db/sql/condition.go — 条件生成器:
    Condition 结构体:
    - field string
    - operator Operator
    - para Expr
    - location *Location

    构造: ConditionFromExprList(exprList *ExprList, directive string, loc *Location)
    解析 #where(field, operator, para) 或 #where(field, operator)

    Generate(scope, writer, firstCondition, sqlPara):
    - IS NULL/IS NOT NULL: scope.Exists(field) 时生成
    - 其他操作符: para.Eval(scope) 非 nil 时生成
    - 生成的 SQL: "WHERE/AND field op ?" 或 "WHERE/AND field IN (?, ?)"

12. db/sql/orderby_directive.go — #orderBy 指令:
    OrderByDirective 结构体:
    - paraName string (默认 "orderBy")
    - fieldWhitelist map[string]string (前端字段 → SQL 字段映射)

    功能:
    - #orderBy(id, name, created_at) — 白名单字段
    - #orderBy('created_at:createdAt') — 字段映射
    - #orderBy($myOrder, id) — 自定义参数名
    - 前端传 {field: "id", order: "desc"} → 生成 "ORDER BY id DESC"
    - 支持多字段排序 (数组)
    - order 值只允许 ASC/DESC

注意:
- SqlKit 的 engine 是独立的 enjoy.Engine 实例 (不与模板引擎共享)
- #para 指令通过 SqlKit.SQL_PARA_KEY 在 scope 中传递 SqlPara
- #where/#and 的条件值从 scope 中获取 (对应 data map 的 key)
- Condition 的 Operator 枚举复用 db/operator.go 的定义
```

---

## 验证 Prompt

```
验证 aifei enjoy 模板引擎 Phase 2 是否完成:

1. 测试基本模板渲染:
   engine := enjoy.NewEngine("test")
   t := engine.GetTemplateByString("Hello #(name)!")
   result := t.RenderToString(map[string]interface{}{"name": "Aifei"})
   验证 result == "Hello Aifei!"

2. 测试 #if/#else:
   t := engine.GetTemplateByString("#if(age > 18)adult#else(child)#end")
   验证 age=20 → "adult", age=10 → "child"

3. 测试 #for:
   t := engine.GetTemplateByString("#for(item : list)#(item) #end")
   验证 list=["a","b","c"] → "a b c "

4. 测试 #set:
   t := engine.GetTemplateByString("#set(x = 1 + 2)#(x)")
   验证 result == "3"

5. 测试 #define/#call:
   t := engine.GetTemplateByString("#define hello(name)Hello #(name)!#end#@hello('World')")
   验证 result == "Hello World!"

6. 测试表达式:
   - 算术: #(1 + 2 * 3) → 7
   - 比较: #(1 > 2) → false
   - 三元: #(true ? 'yes' : 'no') → "yes"
   - 空安全: #(a ?? 'default') → "default" (当 a 为 nil)
   - 数组: #([1,2,3].len()) → 3

7. 测试 Enjoy SQL #para:
   kit := NewSqlKit("test")
   para := kit.GetSqlParaFromString("select * from user where id = #para(0) and name = #para(1)", 123, "james")
   验证 para.Sql == "select * from user where id = ? and name = ?"
   验证 para.Paras == [123, "james"]

8. 测试 Enjoy SQL #where/#and:
   filter := map[string]interface{}{"age": 18, "name": "james"}
   para := kit.GetSqlParaFromString(
       "select * from user #where(age, '>', age) #and(name, 'contains', name)",
       filter,
   )
   验证 para.Sql 包含 "WHERE age > ? AND name LIKE ?"
   验证 para.Paras == [18, "%james%"]

9. 测试 #where 值为 nil 时不生成:
   filter := map[string]interface{}{"age": 18} // name 为 nil
   para := kit.GetSqlParaFromString(
       "select * from user #where(age, '>', age) #and(name, '=', name)",
       filter,
   )
   验证 para.Sql == "select * from user WHERE age > ?" (没有 AND name)
   验证 para.Paras == [18]

10. 测试 #orderBy:
    filter := map[string]interface{}{
        "orderBy": map[string]interface{}{"field": "id", "order": "desc"},
    }
    para := kit.GetSqlParaFromString("select * from user #orderBy(id, name)", filter)
    验证 para.Sql == "select * from user ORDER BY id DESC"
```
