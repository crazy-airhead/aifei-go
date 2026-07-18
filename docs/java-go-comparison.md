# Java Aifei → Go Aifei-Go 移植对照报告

> **核对日期**：2026-07-16
> **对照基准**：Java 版 `/Users/airhead/WorkSpace/aifei/aifei` vs Go 版 `/Users/airhead/WorkSpace/aifei/aifei-go`
> **范围**：aifei（core）、db、enjoy 三个模块
> **方法**：逐方法签名核对，附文件路径与行号。所有结论均基于实际源码阅读，非臆测。

---

## 总览

| 模块 | Java 规模 | Go 规模 | 完成度 | 主要问题性质 |
|------|-----------|---------|--------|--------------|
| **aifei core** | 58 文件 | 8 文件 | ~90–95% | 基本无缺失，范式转换合理 |
| **db** | ~90 文件 | ~27 文件 | ~85% | 有 **1 个严重功能性 bug** + 方言/SQL 文件缺口 |
| **enjoy** | ~120 文件 | 16 文件 | ~65%（最低） | 多处 **语义 bug**（不是没写，是写错了） |

**核心判断**：enjoy 与 db 各存在若干「已实现但行为错误」的真 bug，应优先于「缺功能」处理。

---

## 一、aifei core

核心契约（`Input`/`Output`/`Handler`/`Interceptor`/`Plugin`/`Config`）主干齐全，多处做了贴合 Go 的增强。**无阻塞性缺失。**

### 1.1 真正缺失（值得补齐）

| 优先级 | 缺失功能 | Java 现状 | Go 现状 | 是否补齐 |
|--------|----------|-----------|---------|----------|
| 中 | **路由表内省 API** | `Router.getActionMapping()` 返回 `actionPath → Action` 全量映射，用于权限 key、文档生成、调试 | `aifei/router.go` 仅 `Lookup(method, path)`，**无枚举全部路由的 API**（`Walk`/`Routes`/`List`） | 是。建议加 `Walk(visit func(method, path string, handlers []HandlerFunc))` |
| 中 | **Input 时间访问器** | `Input.getDate/getLocalDate/getLocalTime/getLocalDateTime`，按字符串长度自动推断格式 | `input.go:22-49` 与 `http/context.go` **无任何时间类型访问器**，只能 `GetStr` 后自行 `time.Parse` | 较值得。可加 `GetTime(key string, layout ...string) time.Time` |
| 中低 | 路由创建回调 | `Router.setOnActionCreated(Consumer<Action>)`，注册即回调（自动注册权限表、MCP 工具） | `Router.Handle` 无回调钩子 | 可选 |
| 低 | Input 集合类型绑定 | `getList(name, elementType)` / `getArray(name, componentType)` | 无 `GetList`/`GetArray`；但 `GetBean(&[]T{})` 已覆盖大部分场景 | 不太必要 |
| 低 | 类型化路径参数 | `getStr(int)`/`getInt(int)` 等按下标取类型化 path 参数 | path 参数只能 `PathPara(index)` 取 string；且 `GetStr/GetInt` 只读 query/form 不读 path | 低 |
| 低 | BigDecimal/Enum | `getBigDecimal(name)`；枚举经 `EnumArgument` 自动 `valueOf` | 无 `GetDecimal`（Go 无原生 decimal） | 低 |

### 1.2 有意省略（语言/范式差异，非缺陷）

| Java 功能 | Go 处理方式 | 说明 |
|-----------|-------------|------|
| IoC 容器（`@Inject`/`@Singleton`/`Aop`/`AopFactory`/`AopKit`） | 无 IoC | Go 无类似机制，合理省略 |
| CGLIB/Javassist 代理（`proxy/` 全包、`PrototypeInterceptor`） | `Handler` wrapper 链 + `Interceptor` 接口 | 范式转换，等价覆盖 |
| `@Before`/`@Clear`/`@Path`/`@Para`/`@NoPath` 注解 + `scanner/` classpath 扫描 | 代码注册 + `server.Register` 结构反射 | Go 无注解/无 classpath 扫描 |
| `argument/` 反射式参数注入 | handler 直接收 `Input` | Go 不做方法参数反射注入 |
| `AifeiConfig` + `Routes`/`Plugins`/`Settings` 抽象 | 函数式选项（`WithHandlers`/`WithPlugin`/`WithOnStart`/`WithOnStop`） | 有意重新表达 |
| `server/Dispatcher`/`Server` | `./http` + `./server` 模块 | 有意拆分 |
| `util/Prop`/`PropKit` | 独立 `config` 模块 | 通用配置工具，非 core 职责 |
| `log/` 全包 | 独立 `log` 模块 | 有意拆分 |
| `util/AppHome`/`PathUtil`/`StrUtil`/`ComputeCache` | Go 标准库 + `sync.Map` | Java 部署/通用工具特有 |

> Java 路由是 flat HashMap、**无 HTTP method 维度**、仅支持尾部单段 pathPara；Go 用 radix 树 + 每 method 一棵 + `:param`/`*catchAll`，**反而更丰富**。

### 1.3 Go 反而超出 Java 的地方

- **`Output` 接口**：Java `core/Output.java` 是空标记接口；Go 有 `Code()/Msg()/Data()` 结构化实现（`output.go`）。
- **路由路径模式**：支持任意位置具名参数与 catchAll，按 HTTP method 分树。
- **`Input` 的 `Meta`**：额外纳入 `Context()/Header()/Path()/Body()`（transport-agnostic 元数据），分层更清晰。

---

## 二、db

核心 API（Db 门面、Dao 链式、Row Active Record、Page 分页、SqlKit Enjoy SQL、6 类 Hook、TypeConverter 主干、dialect SQL 构建）移植**忠实且对等**：18 个操作符、5 个指令、Condition 生成逻辑全部一一对应。

### 2.1 真 Bug（必须修）

#### 🔴 Bug 1：事务连接不传播（最严重，功能性缺陷）

- **Java**：`TransactionKit` 用 `ThreadLocal` 把事务 `Connection` 绑定到当前线程；事务内所有 `Db.sql()`/`Dao` 调用自动复用该连接。
- **Go 现状**：`TransactionWithID`（`db/transaction.go`）只做 `pool.Begin()` → `fn()` → `Commit/Rollback`，但 `fn` **拿不到 `*sql.Tx`**；且所有 executor（如 `db/executor.go:36` `config.Pool().Exec`）一律直接走 `config.Pool()` 取新连接并自动提交。
- **后果**：`db.Transaction(func(){ db.Insert(...); db.Delete(...) })` 里的 Insert/Delete **各自独立提交、不在同一事务**。透明事务形同虚设。
- **修复方向**：引入 context 传播机制，让 executor 在事务上下文内使用 `*sql.Tx`。

#### 🟡 Bug 2：`Row.Keep()` 未同步清理 change 集合

- **Java**：`keep(fields)` 同时过滤 `data` 和 `change`。
- **Go 现状**：`db/row.go:155` 的 `Keep` 只删 `r.data`，**不动 `r.change`**（对比同文件 `Remove`/`RemoveNullFields` 都正确清理两者）。
- **后果**：`Set("a",1).Set("b",2).Keep("a")` 后，`change` 仍含 `"b"`，后续 `Update()` 会用已不存在的字段生成 SQL。
- **修复**：一行修改，补上对 `r.change` 的清理。

### 2.2 真正缺失（高优先级）

| 缺失功能 | Java 现状 | Go 现状 | 说明 |
|----------|-----------|---------|------|
| **4 种数据库方言** | 8 种 dialect | `db/dialect.go` 仅 MySQL/PostgreSQL/SQLite（`NewDialect` 识别 `mysql`/`postgres`/`pgx`/`sqlite`/`sqlite3`） | 缺 Oracle/SqlServer/H2/Informix（含各自分页窗口函数、Oracle `.nextval`）。详见 2.4 |
| **原生连接逃逸口 `call(JdbcFun)`/FunExecutor** | `Dao.call(JdbcFun)` 暴露裸 `Connection`，附 `JdbcKit`（Enjoy-SQL 解析 + ResultSet→Row） | 无等价物；`TxBegin()` 仅返回 `*sql.Tx`，Dao 无 `Call`/`WithConn` 钩子 | 存储过程、同连接多语句、临时表优化无法实现 |
| **外部 SQL 模板文件加载 + 热重载** | `SqlKit.addSqlFile(file)`、`setBaseSqlFilePath`、`setSqlFileHotReloading(true)` | `db/sql/kit.go:144` `ParseSqlFile()` 是**空操作 stub**（注释 "Already parsed inline"），只能 `AddSql(sqlID, sql)` 内联 | 无法批量加载 `.sql` 文件，工程化不便 |

### 2.3 真正缺失（中/低优先级）

| 优先级 | 缺失功能 | 说明 |
|--------|----------|------|
| 中 | Batch 能力较薄 | 缺 `batchSize`/`commitOnBatchSize`（分块提交）、`getGeneratedKeys`（批量回填自增主键）、`UpdateCounts`、异构批分组 `BatchGroup`（多表/混列批）。`db/batch.go` 假定同构，读 `rows[0]` 取字段 |
| 中 | TypeConverter 缺 BigDecimal 等 | Java 15 个方法；Go 仅 6 个（`ToInt/ToInt64/ToFloat64/ToBool/ToString/ToTime`）。缺 `toBigDecimal`（金融痛点）、`toBigInteger`/`toNumber`/`toShort`/`toByte`/`toFloat` |
| 中 | 事务表达式力降级 | Java `transaction(Atom<R>)` 返回泛型业务结果，可成功路径主动回滚；Go `Transaction(fn func() error) error` 只返回 error，`server.TxInterceptor` 硬编码 `Code()!=0` 回滚 |
| 中 | 复合主键仅 2 列 | Java `findByCompositeId(table, String[], Object[])` 任意列数；Go 固定 2 列版 |
| 低 | `Kv` 有序 fluent 参数 Map | 用裸 `map[string]interface{}` 替代，人体工学差 |
| 低 | 自定义错误消息回调 | `findOne(Function<Integer,String>)` 无 Go 版 |
| 低 | `queryField(defaultValue)` | 无默认值重载 |
| 低 | Row 小缺口 | `setOrPut`、`data()` 批量读写、`get(field, converter)` 函数式转换器、schema 感知钩子 |
| 低 | SqlKit 小缺口 | `#orderBy` 未 trim 字段/方向、错误类型静默跳过、`ParaDirective.setCheckParaAssigned` 全局开关、operator 全小写别名未注册 |
| 低 | ext 简化 | `SqlLog`/`SqlFormatter` 仅压缩空白，无格式化打印 |

### 2.4 方言缺口

| Java 方言 | Go 等价 | 状态 |
|-----------|---------|------|
| `MysqlDialect` | `MySQLDialect` | ✅ |
| `PostgreSqlDialect` | `PostgresDialect` | ✅ |
| `SqliteDialect` | `SQLiteDialect` | ✅ |
| `OracleDialect` | — | ❌ 缺（`rownum` 三层子查询分页、`.nextval`、`getGeneratedKeys`） |
| `SqlServerDialect` | — | ❌ 缺（`ROW_NUMBER() OVER`/`OFFSET FETCH`、`[ ]` 引号） |
| `H2Dialect` | — | ❌ 缺（Oracle 风格 `rownum`） |
| `InformixDialect` | — | ❌ 缺（`SKIP N FIRST M`、空格填充引号） |

### 2.5 有意省略（非缺口）

- **连接池**：`DruidSupplier`/`HikariCpSupplier` → Go 用 `database/sql` 自带连接池（零外部依赖原则）。
- **7 个 Factory SPI**（`DaoFactory`/`BatchFactory`/`RowFactory`/`ChangeSetFactory`/`DataMapFactory`/`HashDataMapFactory`/`IdSqlFactory`）→ Go 用 `DbHookKit` 钩子作为扩展点。
- **`Murmur3Util`** → 仅支撑 `IdSqlFactory` 缓存键，Go 改用显式 `sqlID`。
- **`PageSqlUtil`** → 被子查询 count 方案替代（`SELECT COUNT(*) FROM (...) AS _cnt`，更稳健，无需 `removeOrderBy` 正则剥离）。
- **类型差异**：`BigDecimal/BigInteger/Number/Short/Byte` 在 Go 无直接对应；`Date/LocalDateTime/...` 合并为 `time.Time`。
- **旧 JFinal Model API**（`_setRowMap`/`toRecord`/`copy`/`reload`/`refresh`/`save`/`Model.java`）在当前 Java 版本不存在，非移植缺口。

### 2.6 Go 增量（Java 无）

- `ForInsertOrUpdate` upsert（`ON DUPLICATE KEY UPDATE`/`ON CONFLICT DO UPDATE`/`INSERT OR REPLACE`）。
- `Table`/`Tables`/`AutoTables` 多表元数据映射（`db/table.go`）。
- `KeyFormat` JSON 驼峰/蛇形切换。
- `Row` 一等 JSON 编解码（`db/json_codec.go`）+ `DecodeJSONFields` 类型化 JSON 列解码。
- 顶层 `AddSql` 门面。

---

## 三、enjoy

搭起了 enjoy 的**骨架**：DKFF 双层 lexer/parser、近乎全部指令名、表达式优先级阶梯、基本类型字面量。但「能解析」≠「语义正确」，核心执行语义存在多处偏差与缺口。

> 覆盖度：指令名解析 ~100%、表达式算子 ~95%；语法覆盖 ~90%，**语义正确度 ~60–65%**。
> 作为 **SQL 模板引擎**够用；作为**通用 HTML 模板引擎**尚有约 35% 缺口。

### 3.1 真 Bug（语义错误，高优先级）

| # | 位置 | Java 语义 | Go 当前行为 | 后果 |
|---|------|-----------|-------------|------|
| 1 | `enjoy/expr_eval.go:40-65` | `+` 任一为 String 时拼接；数值保留 int/long/double 类型 | 一律 `toFloat64` 两侧 | `"a"+"b"`→`0`；`1+2`→`3.0`；`10/3`→`3.333`（不拼字符串、不整除） |
| 2 | `enjoy/stat_parser.go:371-372` | `ReturnIf` 表达式是**条件**，为真才 return | 与 `TokReturn` 同走 `parseReturnStat`，expr 当**返回值**且**无条件** return | `#returnIf(x>0)` 恒返回 |
| 3 | `enjoy/stat_parser.go:91-108` + `toSlice`(`expr_eval.go:512-525`) | `ForIteratorStatus` 支持 Collection/Map(Entry)/数组/Iterator/Iterable/Enumeration/单对象 | `toSlice` 只接受 Slice/Array kind，map 被当单元素 | `#for x : map` 完全失效 |
| 4 | `enjoy/scope.go:18-26` | `Scope.Get` 找不到时回退 `sharedObjectMap` | `Get` 只查 `data`/`parent`，不回退 | `sharedObjectMap` 是**死代码**，`AddSharedObject`（`template.go:126`）声称支持但无效 |

### 3.2 真正缺失（中优先级）

| 缺失功能 | Java | Go | 说明 |
|----------|------|-----|------|
| `#for` 缺 `#else` | 循环一次未执行时执行 else 体（`For.java:90-92`） | `ForStat` 无 else 概念 | — |
| `#for` 循环状态变量不完整 | `for.index/count/first/last/odd/even/size/outer` 对象式访问 | 只设扁平 `index/size/first/last`，缺 `count/odd/even/outer`；且访问范式不兼容（裸变量 vs `for.index`） | 模板兼容性差 |
| `#call` 丢失外围作用域 | `Define` 用 `new Scope(scope)`（caller 子域，可见外层变量） | `CallStat` 用 `NewScope(empty)`（无 parent） | `#define` 函数体内看不到外层变量 |
| `#define` 不支持前向引用 | parse 阶段注册（`Parser.java:100-103`） | 执行时才 `env.AddFunction`（`stat_parser.go:159-161`） | 文档顺序靠后的 define 无法被前面的 call 调用 |
| 赋值不支持 `ID[expr]=expr` | `map[key]=v`/`list[i]=v`/`array[i]=v`，可无限连 | `expr_parser.go:45-49` 强制左侧为 `IDExpr` | 仅支持 `ID = expr` |
| `Scope.Set` 不向上查找 | set 自内向外找已存在变量就地改写，找不到存根 | `scope.go:29-34` 只写当前层 | `#for` 内 `#set(x=...)` 外层变量丢失 |
| 内置指令全部缺失 | 默认注册 7 个 | 0 个 | 详见 3.3 |
| 共享方法库 | `SharedMethodKit` + 默认 `SharedMethodLib`（`isEmpty`/`notEmpty`），可注册 | 无注册体系 | 模板内不能直接 `isEmpty(x)` |
| 扩展方法缺数值类 | 9 类 × ~9 方法（`toBoolean/toInt/.../toBigDecimal`） | 仅 `expr_eval.go:377-427` 硬编码 string 方法 | 无数值扩展，无注册机制 |
| `EngineConfig` 配置项大面积缺失 | compressor/outputDirectiveFactory/sourceFactory/sharedMethodKit/keepLineBlankDirectives/roundingMode/staticMethod/Field/addSharedFunction(file) | 仅 directiveMap/sharedFunctionMap/sharedObjectMap/baseTemplatePath/encoding/datePattern/devMode（后三项基本未接线） | — |
| Field 取值不支持 getter 约定 | 优先 `getXxx()` getter → public field → Model/Record/Map.get() | `getField`(`expr_eval.go:327-351`) 只 `reflect.FieldByName` + Map index | 只暴露 getter 的 POJO 不友好 |

### 3.3 缺失的内置指令 / 扩展方法 / 共享方法

**内置指令（Java 默认 7 个，Go 0 个）：**

| Java 指令 | 作用 | Go |
|-----------|------|-----|
| `#date` | 日期格式化 | 缺 |
| `#escape` | HTML 转义 `< > " ' &` | 缺 |
| `#number` | 数字格式化（DecimalFormat） | 缺 |
| `#random` | 输出随机整数 | 缺 |
| `#render` | 动态渲染子模板 | 缺 |
| `#string(name)` | 多行字符串变量定义 | 缺 |
| `#call` | 动态调用（表达式函数名） | Go 的 `#@` 是语法糖，非等价指令 |

> HTML 转义：Java 默认 `#(...)` 不转义（与 Go 一致），但转义靠 `#escape` 显式开启；Go 连 `#escape` 都没有，无法显式转义。

**扩展方法**：Java 9 类（Integer/Long/Short/Byte/Float/Double/BigDecimal/BigInteger/String Ext）× ~9 方法；Go 仅硬编码 string 方法（`length/trim/upper/contains/startsWith/.../isEmpty`），无数值扩展、无注册机制。

**共享方法**：Java 默认 `SharedMethodLib`（`isEmpty`/`notEmpty`）；Go 完全缺失。

### 3.4 低优先级差异

> 注：以下多项已由 ISSUE-0012 第一轮对齐 Java，标注 ✅；其余为仍存差异或 Go 限制。

- ✅ `??` 优先级：Java `nullSafe()` 位于 mulDivMod(* / %) 与 unary 之间、左结合链式；Go 已对齐（旧为 postfix 高优先级）。
- Map 字面量 key：Java 允许 number/bool/null/ID/STR；Go 只允许 ID/STR。
- ✅ `#@name?()` 安全调用：词法器正确剥离 `?`→`TokCallIfDefined`，函数名干净；nullSafe 跳过、非 nullSafe 抛异常（对齐 Java）。
- ✅ `::` 静态访问：模板路径默认禁用对齐 Java（`isStaticMethodExpressionEnabled=false`）；**Go 限制：无 Class.forName / 结构体静态方法，且无法整体反射 import 包的包级函数**——开启后以 `AddStatic(alias, obj)` 注册一个 struct 实例作命名空间，反射其所有导出方法，`alias::method(args)` 即可调用（作 Java 导入工具类静态方法的等价；要导标准库包级函数需用 struct 包一层）。
- ✅ `#include` 相对路径：相对父文件目录解析，无父目录回退 baseTemplatePath（对齐 Java）。
- ✅ Call 参数个数：不匹配抛异常（对齐 Java）；nullSafe 仍跳过。
- `ClassPathSource`：Java 有 classpath/file/string；Go 只有 file/string。
- ✅ 错误行号定位：解析期 + directive 参数期 error 带「文件名:行号」（`locateError`，对照 Java `Location`/`ParseException`）；`errorStat` 已在前序 issue 移除，错误改为 error 返回。渲染期节点级精确行号遗留。

### 3.5 有意省略（非缺口）

- `proxy/`（ProxyClass/ProxyClassLoader/ProxyCompiler，字节码生成编译器）— Go 无运行时动态子类需求。
- `io/` 性能 writer 体系（FloatingDecimal 1306 行 JDK 浮点格式化移植、FloatingWriter/IntegerWriter/LongWriter/.../Utf8Encoder）— Go 统一用 `io.Writer` + `fmt.Sprintf`。
- `expr/ast` 反射缓存基建（FastFieldGetter/FieldGetters/FieldKeyBuilder/MethodKeyBuilder/MethodKit/MethodInfo/...）— Go 直接用 `reflect`。
- `stat/Compressor`/`LineCompressor`/`CharTable` — 模板输出空白压缩优化。
- `util/`（HashUtil/ComputeCache/InstanceUtil/JavaKeyword）— 服务于 Java 反射缓存/实例化。

---

## 四、优先级建议（补齐路线）

### 第一阶段：修 Bug（性价比最高）

| Bug | 工作量 | 影响 |
|-----|--------|------|
| `#returnIf` 语义（enjoy） | 局部 | 模板控制流错误 |
| `Row.Keep()` change 清理（db） | 一行 | UPDATE 字段错误 |
| `sharedObjectMap` 回退（enjoy） | 局部 | 死代码激活 |
| 算术 `+` 字符串拼接 + int 保留（enjoy） | 局部 | 基础表达式能力 |
| Map 迭代（enjoy） | 局部 | `#for x : map` |
| **db 事务连接传播** | 中（需 context 贯穿 executor） | **最严重**，事务失效 |

### 第二阶段：enjoy 可用性提升

补 `#escape`/`#date`/`#number` 内置指令 → `#for` else + 循环状态变量 → `#call`/`#define` 作用域与前向引用 → 共享方法库 `isEmpty`。

### 第三阶段：db 工程化

去掉 SqlKit 空 stub，实现 `.sql` 文件加载 → 补 Oracle/SqlServer 方言 → Batch 回填主键/异构分组。

---

## 附录：关键文件路径

### Go 侧
- aifei core：`aifei/{input.go,output.go,aifei.go,router.go,handler.go,interceptor.go,config.go,plugin.go}`
- Input 实现：`http/context.go`、`server/in.go`
- db 事务：`db/transaction.go`、`db/executor.go`
- db `Keep` bug：`db/row.go:155`
- db 方言：`db/dialect.go`
- db Batch：`db/batch.go`
- db SQL 文件 stub：`db/sql/kit.go:144`
- db TypeConverter：`db/type_converter.go`
- enjoy 语义：`enjoy/stat_parser.go`、`enjoy/expr_parser.go`、`enjoy/expr_eval.go`、`enjoy/scope.go`、`enjoy/engine_config.go`、`enjoy/lexer.go`

### Java 侧（对照基准）
- core：`aifei/src/main/java/cn/aifei/core/{Input.java,Output.java}`
- enjoy：`aifei-enjoy/src/main/java/cn/aifei/enjoy/stat/Parser.java`、`stat/ast/{For.java,ReturnIf.java,Define.java}`、`stat/Scope.java`、`expr/ExprParser.java`、`expr/ast/{Arith.java,Assign.java}`、`EngineConfig.java`
- db：`aifei-db/src/main/java/cn/aifei/db/{core,transaction,dialect,executor,hook,sql}/`
