# Java aifei v1.0.4 → v1.1.0+ 演进同步评估

> **核对日期**：2026-08-22
> **2026-08-23 拍板**：P0-2 时间转换采用方案 B —— `ToTime`/`GetTime` 直接改签名返回 `(time.Time, error)`（放弃 `ToTimeE` 兼容路径），详见 2.1 P0-2；P1-4 MaxHeaderBytes 维持 Go 默认 1MB 不收紧，仅暴露 Option，详见 2.1 P1-4
> **对照基准**：Java 版 `/Users/airhead/WorkSpace/aifei/aifei`（dev 分支，HEAD=b25c056，2026-08-22 rebase）vs Go 版 `/Users/airhead/WorkSpace/aifei/aifei-go`（master，dcdb1a5）
> **移植基线**：aifei-go 核心移植完成于 2026-06-25；本文评估此后的 Java 变更（约 110 个提交，其间发布 v1.0.4 2026-07-17、v1.1.0 2026-08-17，后续至 08-20）
> **范围**：aifei core、aifei-db、aifei-enjoy、aifei-proxy、aifei-undertow。**排除**：新模块 `aifei-json-snack4`、`aifei-feathttp`（不关注）
> **方法**：逐提交核对 Java 行为变更（git log/show/diff + 当前源码）+ 盘点 Go 对应实现现状；文中行号基于各自当前 HEAD，审核时如有漂移以符号名为准

---

## 总览

**核心判断**：Java 版这轮演进的重心是**数据正确性**（类型映射、时间语义、事务硬化），不是新功能。Go 侧大部分改动因语言/范式差异天然免疫（Go 只有 `time.Time`、事务走 context、无代理机制），**真正需要跟进的集中在 8 项**，其中 2 项是 Go 侧真实存在的 bug/数据风险：

| 类别 | 数量 | 代表 |
|------|------|------|
| 需要实现 | 8（P0×2 / P1×4 / P2×2） | 连接池线程安全、时间列读取 |
| 已天然覆盖 | 5 | rollbackOnly、事务传播、启动失败退出、Byte/Short 折叠 |
| 不适用（语言/生态差异） | 6 | LocalDate 拆分、AOP 异常契约、代理缓存隔离等 |

---

## 一、Java 版演进归纳（2026-06-30 → 2026-08-22）

### 1.1 JDBC 类型映射体系（07-12 ~ 08-19，最大主题）

`TypeMapping` 拆成**双表**：

1. `classNameToJavaType` — 以 `ResultSetMetaData.getColumnClassName(i)` 返回的类名为键；
2. `jdbcTypeToJavaType`（c894444 新增）— 以 `getColumnType(i)` 的 JDBC `Types` 常量为键的**兜底表**。

07-12~13 的 6 个 "complete JDBC type mappings" 提交把两张表补到全 JDBC 覆盖；兜底默认值从 String 改为 `Object`。关键决策：

- `REAL→Float`、`FLOAT→Double`（按 JDBC 4.2 附录 B，不能按直觉互换）；
- `TIMESTAMP_WITH_TIMEZONE`/`TIME_WITH_TIMEZONE` **故意不设兜底**（H2 老版本返回厂商类，兜底会生成运行时 ClassCastException 的 getter，宁可退到 Object）；
- **Byte/Short → Integer 归一化**（02131c7/7ac1baa）：跨驱动保持生成模型稳定；顺带删除 `AifeiRow.getByte()`/`queryByte()` 运行时 API；
- **方言特化**：Oracle NUMBER 按 scale/precision 收窄（scale==0 且 precision 1..9→Integer、10..18→Long、precision≤0 或负 scale 视为未知**不收窄**，889f05e）；SQLite 浮点一律 Double（5c8888b）。

制度化原则：**生成类型必须与 Dialect 运行时读取路径一致** —— `Dialect.resolveColumnValueClassName()`（generator 侧推断）与 `readColumnValue()`（运行时读取）成对方言钩子（97a1fde→c78255d）。

### 1.2 列值读取 Dialect 化（07-13 ~ 08-17）

列值读取从 RowFactory 移入 `Dialect.readColumnValue(ResultSet, int, int)`，按方言可覆盖：

- 默认实现：**DATE→`getDate()`、TIMESTAMP→`getTimestamp()`** 类型化读取（统一各驱动返回类型），其余 `getObject` + LOB 物化（仅当实际返回 Blob/Clob 对象；空 LOB→空数组、超 `Integer.MAX_VALUE` 抛异常、循环读满防短读）；
- SqliteDialect：仅 BOOLEAN→`getBoolean()`+`wasNull()` 区分 false/null；
- 方向曾两次回调（e6608be 全 getObject → b41147e 移入 Dialect → 39d55c8 恢复类型化读取），最终语义是「类型化读取保证返回类型稳定，生成器按同一路径推断类型」。

**08-18/19 的 BREAKING**：`java.sql.Date`→`java.time.LocalDate`（262bfb3）、`java.sql.Time`→`java.time.LocalTime`（7ec1958），配套 `getLocalDate()/getLocalTime()` 等 API。

### 1.3 时间解析与转换硬化（07-19 ~ 07-22，横跨 enjoy/db/core 三模块）

一套统一规则：**宽度宽松、取值严格、整串消费、毫秒恰 3 位**。

- `TimeUtil`（enjoy）：`DateTimeFormatterBuilder` + `parseLenient()`（数字宽度可变，`"2026-1-2 3:4:5"` 可解析）+ `parseDefaulting(ERA/HOUR_OF_DAY)`（date-only 串解析为当天零点）+ `ResolverStyle.STRICT`（拒绝 `"2020-2-30"`）；`SimpleDateFormat` 一律 `setLenient(false)`；`parse` 用 `ParsePosition` 校验**整串消费**（拒绝尾部垃圾）；自动探测 pattern 时毫秒必须**恰好 3 位**；
- `TypeConverter`（db）：字符串解析全部委托 TimeUtil；类型间转换**不隐式补日期/时间、不丢时区** —— `toLocalTime(sql.Date)`/`toLocalDate(sql.Time)` 等缺半边的转换直接抛 IAE，`OffsetDateTime`→Timestamp 走 `toInstant()` 不丢 offset，`LocalDateTime`→Timestamp 按本地字段语义不走系统时区；
- `toBoolean` 先判 `Number`（1/0）再判 `String`，其它抛 IAE（828ce12）；
- core 的 action 参数注入（`BasicArguments`）新增 `TemporalPattern` 枚举，同规则探测格式。

SQL 参数绑定路径**不做**字符串日期解析（String 原样 `setObject`）；硬化影响的是值转换三处：Row/Kv/getter 转换、action 参数注入、enjoy 模板时间函数。

### 1.4 事务体系硬化（07-27 ~ 08-06）

- **6 状态状态机**（4335c40）替代三个布尔：`NEW→ACTIVE→COMMITTING/ROLLING_BACK→COMMITTED/ROLLED_BACK`；`rollbackOnly` 与状态**正交**（单向标记，置位后事务仍可操作但永久失去提交资格）；
- begin 失败不残留 ThreadLocal（`setTransaction` 移到 begin 之后）、原始值用包装类型 null 判断（半途失败不恢复未读取的值）、`catch (Error)` 也回滚但原样抛出（1bea27e）；
- `onCommitSuccess` 回调**延迟到连接恢复/close/ThreadLocal 清理之后**执行（6335823，回调里的 DB 操作不再误用已提交事务的连接）；
- **onException 回调守卫**（808671d）：begin 失败不执行回调；回调执行期间禁止 `tx.getConnection()`/同 DbConfig 再开事务；
- **删除 `Isolation.NONE`**（801b85f，无事务语义与隐式提交设计矛盾）；
- **Transactional 拦截器注入 `Transaction` 参数**（14eb9e2/8e2399c）：ThreadLocal 暴露当前事务 + `TransactionArgument` 参数注入；嵌套 Transactional 保存/恢复外层事务；`Db.transaction` 返回值（含异常回调兜底值）显式写回 Invocation。

### 1.5 Aifei 核心生命周期（07-31 ~ 08-16）

最终契约：启动路径任何 Throwable → 记日志 → **`System.exit(1)`**（非 daemon 线程会让失败进程假活）；关闭顺序 = server.stop → onStop 回调 → **插件逆序停止**；每步错误 `reportError` 吞掉并**继续**；`volatile started` 标志（start 重入抛 ISE、stop 幂等）；命令行参数 `split("=", 2)` 支持 value 含 `=`。

### 1.6 db 连接池生命周期线程安全（08-16，b7e6bbb）

`AifeiDb.start()/stop()` 加 synchronized + `started` 守卫；修复「未 start 过的实例 stop 会误删同 id 另一实例配置」；Druid/Hikari 供应商 get() 改双检锁语义。

### 1.7 其他

- `undertow.maxHeaderSize` 配置，**默认 16KB**（794d6aa，收紧 Undertow 默认 1MB）；
- PropKit active profiles 向**所有** `use()` 调用传播，system property 每次优先（4d79119）；
- `Operator.from()`→`of()` 改名（8a787d7）；
- 依赖升级：Undertow 2.2.40.Final、log4j2、fastjson2 2.0.63、javassist。

---

## 二、Go 侧差距评估

### 2.1 需要实现（按优先级）

#### 🔴 P0-1：db.Config 连接池线程安全

- **Go 现状**：`db/config.go` — `configs` map（约 :85）读写**无互斥**；`Pool()`（:120-142）是 check-then-act 懒初始化，并发首次调用存在 data race（可能重复 `sql.Open`/`Ping` 泄漏连接）；`Close()`（:166-171）、`ResetConfigs()`（:174-179）同样无锁。
- **Java 对应**：b7e6bbb，真实 bug 级别（含「stop 误删他人配置」这类 Go 侧 `ResetConfigs` 同样存在的模式）。
- **建议**：`sync.RWMutex` 保护 configs map；`Pool()` 用 `sync.Once` 或 mutex 内双检；`InitWithID`/`GetConfig`/`Close`/`ResetConfigs` 全部走锁。

#### 🔴 P0-2：时间列类型化读取 + 解析失败不静默

- **Go 现状**：`db/executor.go` `scanRows`（:1116-1152）与 `execForEach`（:732-814）每列扫进 `interface{}` 后只做二分：`isBinaryColumnType`（:1231-1239）保留 `[]byte`，**其余全部 `bytesToStr` 转 string**。DATE/TIME/TIMESTAMP 无任何专门映射，Row 中时间列类型完全由驱动决定（MySQL 未开 `parseTime` 即 string）；`Row.GetTime`（`db/row.go:328`）→ `ToTime`（`db/type_converter.go:144-165`）按 5 种 layout 依次试错，**全部失败静默返回零值 `time.Time{}`** —— 脏数据被吞掉。
- **Java 对应**：1.2 节整套（类型化读取 + `resolveColumnValueClassName` 一致性原则）。
- **建议**：
  1. scan 到 `interface{}` 后**保留 `time.Time` 原生值**（多数驱动已返回），`[]byte`/string 仅在列非时间型时转换；判断依据用 `columnType.DatabaseTypeName()`（与 `isBinaryColumnType` 同一模式，可加 `isTemporalColumnType`）；
  2. **（已拍板 2026-08-23：方案 B，放弃 `ToTimeE` 兼容路径）** `ToTime`/`GetTime` 改签名返回 `(time.Time, error)`：
     - 字符串解析失败 → 返回明确 error（含原始值与已尝试的 layout 提示），**零值静默消除**；
     - `nil`（列 NULL）→ `(time.Time{}, nil)`：NULL 是缺值不是错误，与解析失败严格区分；
     - `GetTimeDefault(field, def)` 语义调整为：NULL/缺失 → 默认值；解析失败 → error（默认值只兜 NULL，不兜脏数据）；
     - 库内调用点同步适配：`json_codec.go` 的 `parseTimeValue`（JSON 输入路径）、`Row.GetTime` 之外的一切 `ToTime` 使用处；
     - v0.x 阶段允许破坏性变更，随 minor 标签发布；`_test/db_test` 补正反用例（合法格式、NULL、脏数据三类）；
  3. 文档注明 MySQL DSN 需 `parseTime=true`（可在 `db.Init` 帮助层校验/提示）。

#### 🟡 P1-1：事务隔离级别支持

- **Go 现状**：`TransactionCtx`（`db/transaction.go:37-65`）用无参 `pool.Begin()`，**无 `TxOptions`**，全库 grep 无 IsolationLevel；`tx.Rollback()` 返回值被**静默忽略**。
- **Java 对应**：1.4 节（Java 支持隔离级别且嵌套只升不降；删除 NONE）。
- **建议**：加 `TransactionOpts` 变体（`BeginTx(ctx, &sql.TxOptions{Isolation: ...})`）；rollback 错误至少 `log` 记录（不覆盖原始错误）。Go 的 `sql.IsolationLevel` 无 NONE 语义问题，天然无需处理。

#### 🟡 P1-2：插件逆序 Stop

- **Go 现状**：`server/run.go`（约 :110）插件**正序** `p.Stop()`；错误已忽略不中断（`_ =`，与 Java 「遇错继续」一致 ✅）。
- **建议**：改逆序循环，一行改动。`started` 幂等标志对 Go 的一次性 `Run` 流程意义不大，可不做。

#### 🟡 P1-3：config 扩展配置的 env 传播

- **Go 现状**：`app-{env}.yml` 只对 L1 base 文件生效（`config/config.go:156-169`）；L2 扩展配置（`config.include`，`collectExtensionPaths` :255-281）**只按 glob 加载，没有 env 变体机制** —— 即子配置加载时 env 不传播。
- **Java 对应**：4d79119（所有 `PropKit.use` 都追加 `file-{profile}.ext`）。
- **建议**：`collectExtensionPaths` 收集到的每个文件，同样尝试 `{base}-{env}.yml` 变体（存在则追加，env 变体优先级高于 base）。

#### 🟡 P1-4：MaxHeaderBytes 配置

- **Go 现状**：`http/server.go:28-34` 构造 `&http.Server{Addr, Handler}`，未设置 `MaxHeaderBytes`（Go 默认 1MB）也无配置暴露。
- **Java 对应**：794d6aa（默认收紧 16KB）。
- **建议**（已拍板 2026-08-23）：默认**维持 Go 默认 1MB**（不设置 `MaxHeaderBytes`，不对齐 Java 的 16KB——大 cookie/session 场景有破坏风险，Go 生态习惯显式收紧）；新增 `server.WithMaxHeaderBytes(n)` Option，穿透到 `http.Server.MaxHeaderBytes`；demo/文档示例中演示收紧到 16KB 的用法。

#### 🟢 P2-1：ToBool 补类型分支

- **Go 现状**：`db/type_converter.go:102-119` 覆盖 bool/int/int64/float64/string；int8/int16/int32/uint 及各无符号整型落入 default 返回 false；字符串仅认 `"true"/"1"/"yes"` 大小写有限集合。
- **建议**：default 分支用反射或显式列举补全整数族；字符串解析用 `strconv.ParseBool` + 现有集合。

#### 🟢 P2-2：generator 分层兜底映射

- **Go 现状**：`tools/generator/type_mapping.go` 单层 37 条 map，`GetType`（:68-73）未命中**一律返回 string**；`meta_reader.go:283/:322` 两个调用点（information_schema 路径与驱动反射 fallback 路径）无归一化层 —— PG 的 `timestamp with time zone` 这类带空格类型名直接落到 string。
- **Java 对应**：1.1 节双表 + 方言钩子。
- **建议**：不必照搬 JDBC 双表（Go 的 `ColumnType.DatabaseTypeName()` 已是驱动归一后的名字）；值得做的是①`NormalizeDataType` 归一化别名（剥长度后缀、`character varying`→`VARCHAR` 等）；②未命中默认值改为按 `ScanType` 推断或保持 string 但 log 提示。

### 2.2 已天然覆盖 / 无需跟进

| Java 变更 | Go 侧结论 |
|-----------|-----------|
| DATE→LocalDate / TIME→LocalTime（BREAKING） | **无需跟进**：Go 只有 `time.Time` 一个时间类型，Java 拆三个类型的动机（字段语义/时区陷阱）在 Go 不存在；映射表 DATE/TIME/TIMESTAMP→`time.Time` 已是对等答案 |
| rollbackOnly 状态机 | **语义已覆盖**：`Tx` 句柄主动 `Rollback()` 打标（`db/transaction.go:102-108`）+ `RollbackDecision`（:90-92）+ `ErrRollback` 哨兵（:97），`runAtomDecision`（:182-195）判定 fn 出错/打标/`ShouldRollback()` 三条件 |
| Transactional 参数注入 + 嵌套恢复 | **范式等价**：Go 无法注入方法参数，`server.TxInterceptor`（`tx_interceptor.go:25-44`）经 `ctxSetter` 把携带 tx 的 ctx 塞回 Input，方法内 `db.Ctx(in.Context())` 加入事务；嵌套恢复 ctx 天然正确（子 ctx 不影响父） |
| onCommitSuccess 延迟 / onException 回调守卫 | **不适用**：Go 事务无回调机制 |
| AOP 异常契约收窄（Exception）、反射拆包 | **不适用**：Go 无受检异常，`HandlerFunc` 返回 `Output`、panic 由 `server.Recover()` 承担 |
| 代理拦截器缓存按目标类隔离（269bb97 修串类 bug） | **不适用**：Go 无 CGLIB/Javassist；`server.Register` 反射天然按具体类型，无共享 Method 缓存问题 |
| 启动失败 terminate JVM | **已等价**：`server.Run` 插件启动失败/serve 失败均 `log.Fatalf` 退出进程 |
| Byte/Short→Integer 归一化 | **已等价**：映射表 TINYINT/SMALLINT/MEDIUMINT→`int` 天然折叠 |
| PropKit profile 主流程 | **已覆盖**：`resolveEnv`（CLI→`<PREFIX>_ENV`/`_PROFILE`）+ L1 env 变体；差距仅 2.1 P1-3 那条扩展配置传播 |
| Oracle NUMBER 收窄 | **不适用**：Go 无 Oracle dialect（`NewDialect` 未知驱动 fallback SQLite） |
| 依赖升级（Undertow/log4j2/fastjson2/javassist） | **不适用**：Go 零外部依赖 |
| `Operator.from`→`of` | Go 侧无对应命名习惯，不跟 |
| aifei-json-snack4 / aifei-feathttp 新模块 | **排除**（不关注）：Java 生态 JSON 实现/HTTP 服务器选型；Go 侧 `encoding/json` + `net/http` |

---

## 三、建议落地顺序

1. **P0-1 连接池加锁** —— 改动小、真 bug，独立可测（`-race` 压测）；
2. **P0-2 时间列读取** —— 数据正确性核心，涉及 `scanRows`/`ToTime` 两处 + `_test/db_test` 补用例；
3. **P1-2 逆序 Stop / P1-1 隔离级别 / P1-3 env 传播** —— 各自十几行以内，按顺手程度排；
4. **P1-4 MaxHeaderBytes** —— 已拍板：默认维持 1MB，新增 `WithMaxHeaderBytes` Option（demo 示例演示 16KB 收紧用法）；
5. **P2 两项** —— 视真实需求。

总量估计：P0+P1 合计约 300~400 行（含测试）。P0-2 已拍板采用方案 B，是全部跟进项中**唯一的破坏性 API 变更**（`ToTime`/`GetTime`/`GetTimeDefault` 返回值加 error）；v0.x 阶段可直接实施，随 minor 标签发布。

---

## 四、落地记录（2026-08-23 实施完成）

全部 8 项已实现并测试通过：

| 项 | 实现 | 主要改动 | 测试 |
|----|------|----------|------|
| P0-1 | ✅ | `db/config.go`：`configsMu sync.RWMutex` 保护 registry；`Config.mu` 保护 pool/SqlKit 惰性初始化；`Pool()` 失败时关闭已打开的句柄 | `config_concurrent_test.go`（3 用例，`-race` 通过） |
| P0-2 | ✅ | `ToTime/GetTime/GetTimeDefault` 改签名 `(time.Time, error)`；新增宽松访问器 `Row.GetTimeOrZero`（NULL/缺失/脏数据→零值，服务生成代码）；`scanRows`/`execForEach` 对 DATE/TIME/DATETIME/TIMESTAMP 列 scan 时类型化（`temporalLayoutsFor`+`scanTemporal`），脏数据在查询处报错；`ToStr` 支持 time.Time；生成器时间 getter 映射改 `GetTimeOrZero`，**base.go 签名保持单返回值不变** | `temporal_test.go`（6 用例：类型化 scan/JSON 格式稳定/NULL/脏数据报错/ToTime 契约/GetTimeOrZero） |
| P1-1 | ✅ | `TxOption`（`WithIsolation`/`WithReadOnly`）变参挂在 4 个 Ctx 入口，经 `BeginTx` 下发；`rollbackTx` 统一 4 处回滚（失败 `log.Warn`，不掩盖原始错误）；db 模块新增内部依赖 log | `tx_options_test.go`（2 用例：隔离级别提交/回滚路径 + 嵌套 join） |
| P1-2 | ✅ | `server/run.go` 插件 Stop 改逆序 | 既有 server_test 回归 |
| P1-3 | ✅ | `config/config.go`：`loadExtensionWithEnv`/`loadEnvVariantsFor`——L2 扩展配置（含 glob）每个文件加载后追加 `-{env}` 变体；L3 迟到 env 也补挂扩展变体 | `ext_env_test.go`（3 用例：单文件/glob/迟到 env） |
| P1-4 | ✅ | `http.DefaultServer.MaxHeaderBytes` 字段 + `server.WithMaxHeaderBytes(n)` Option；默认 0=net/http 1MB；demo 演示 `16<<10` | 既有 server_test 回归 |
| P2-1 | ✅ | `ToBool`：整数族/浮点族全类型（含 uint/无符号、`[]byte`），字符串 `strconv.ParseBool`+yes/y/on 大小写不敏感，Number 先于 String | `type_converter_test.go`（~50 断言） |
| P2-2 | ✅ | `NormalizeDataType`（剥长度后缀、PG 拼写别名折叠、int2/4/8）；`TypeMapping.Lookup` 归一化查找；`AddMapping/RemoveMapping` 归一化键；映射表补 UUID/BYTEA/CLOB 等；驱动路径未命中按 `ScanType` 推断 | `type_normalize_test.go`（3 用例） |

**验证**：CLAUDE.md 全量测试清单通过（唯一失败 `_test/flow_plugin_test` 为动手前 master 上既有问题，与本批改动无关——测试文件函数签名与 `flowplugin.PersistFunc` 不匹配）。demo 构建通过。

**行为变化摘要**（发布时值得写进变更说明）：
1. 时间列 scan 即类型化 + 脏数据报错（原先静默 string/零值）；
2. `ToTime/GetTime/GetTimeDefault` 改双返回值（唯一破坏性变更，仅影响手写调用；生成器时间 getter 经 `GetTimeOrZero` 保持单返回值，base.go 签名不变）；
3. `AddMapping/RemoveMapping/GetType` 键经归一化（大小写/别名/后缀不敏感）；
4. db 模块新增内部依赖 `log`（仍零外部依赖）。
