# Aifei-Go db：Active Record 风格的数据库访问层

> **Db + Row + Dao + Enjoy SQL**，零外部依赖。一张表配一段 `#sql` 模板就能搞定 90% 的 CRUD；多命名 `Config`、懒连接池、链式 `Dao`、`Active Record` 风格的 `Row`、批处理、事务、多表关联映射一应俱全。

---

## 1. 背景与定位

`db` 是 Aifei-Go 的数据库访问模块，对应 Java Aifei 的 `aifei-db`（约 7K 行）。Go 版精简到约 5,000 行，保留了 Java 版的核心 API 形态（`Db`、`Row`、`Dao`、`Page`、`Dialect`），同时用 Go 的方式重写了底层：

- **零外部依赖**：只依赖 Go 标准库 `database/sql` 和同仓库的 [enjoy](enjoy.md) 模板引擎；驱动由用户自己 `import`（`modernc.org/sqlite`、`go-sql-driver/mysql`、`lib/pq` 等）。
- **三种 SQL 风格并存**：链式单表 CRUD、原始 SQL、Enjoy SQL 模板（`#sql` / `#para` / `#where` / `#and` / `#orderBy` 指令）。
- **Active Record 风格的 `Row`**：`Set` 追踪变更用于 UPDATE，`Put` 不记录变更，`Keep` 收窄字段集。
- **多命名 `Config` + 懒连接池**：每个 Config 一个独立连接池，首次使用才 `Open + Ping`。
- **可插拔的 `DbHookKit`**：六类 hook（Insert/Update/Delete/Find/Query/Paginate）覆盖全部执行路径，是 [dataisolate](data-isolate.md) 插件改写 SQL 的挂载点。

| Java Aifei `aifei-db` | Go Aifei-Go `db` |
|-----------------------|------------------|
| `Db.use()` 静态门面 | `db.Use()` 顶层函数（Go 无静态） |
| `Row` + `Record` | `Row`（合并为单一类型） |
| `Dao` 链式查询 | `Dao` 链式查询（API 对照） |
| `Db.tx(...)` 线程局部 | `db.TransactionCtx(ctx, ...)` 显式 ctx 传播 |
| `SqlKit` + Enjoy | `SqlKit`（基于 Go 版 [enjoy](enjoy.md)） |
| `Dialect` for MySQL/PostgreSQL/Oracle/... | `Dialect` for MySQL/PostgreSQL/SQLite |
| `Table` + `Builder` | `Table` 运行时表元数据（供 [generator](generator.md) 与多表映射使用） |

---

## 2. 总体架构

```
                    应用代码
                       │
   ┌───────────────────┼───────────────────┐
   │                   │                   │
   ▼                   ▼                   ▼
db.Use()         db.Sql(...)          db.WithCtx(ctx)
   │                   │                   │
   └──────┬────────────┴───────────────────┘
          ▼
        *Dao（链式构建器）
          │  sqlStr / sqlArgs / sqlPara(dbsql.SqlPara)
          │  selFields / fromTable / table / multi / autoTables
          │  ctx（携带 *sql.Tx 时走事务）
          ▼
       executor.go（30+ 个 execXxx）
          │   组装 SQL → DbHookKit.Before → Config.runner(ctx)
          ▼
       DBConn（*sql.DB 池 或 *sql.Tx）
          │
          ▼
     database/sql 驱动
```

核心类型一览：

| 类型 | 职责 |
|------|------|
| `Config` | 一个数据库的连接配置 + 懒连接池 + Dialect + SqlKit + HookKit |
| `Dao` | 链式构建器 + 执行器入口；每条 SQL 在此组装后由 executor 派发 |
| `Row` | Active Record 行：字段 map + 变更集 + 主键 + 表名 |
| `Page` | 分页结果：`PageNum/PageSize/TotalRows/TotalPages/Rows` |
| `Batch` | 批处理：同构/异构分组、分块提交、生成键回收 |
| `Dialect` | 方言：标识符引用、CRUD 模板、分页、`InsertOrUpdate` |
| `Table` | 运行时表元数据（列名 + 主键 + 列 Go 类型 + 生成列） |
| `DbHookKit` | 六类 hook 的容器，按需装配 |
| `SqlKit`（`db/sql`） | Enjoy SQL 引擎封装，注册 7 条 SQL 指令、缓存模板、热重载 |
| `SqlPara`（`db/sql`） | 一条 SQL 渲染后的字符串 + 占位符参数列表 |

---

## 3. 连接管理：Config 与懒连接池

`Config` 是一切 db 操作的起点。`db.Init` 初始化默认 Config（id 为 `"main"`），`db.InitWithID` 初始化命名 Config：

```go
import _ "modernc.org/sqlite"
import _ "github.com/go-sql-driver/mysql"

// 默认 Config（id="main"）
_ = db.Init("sqlite", "file::memory:?cache=shared")

// 命名 Config：多数据源 / 租户专属库（见 dataisolate 策略①/②）
_ = db.InitWithID("tenant_acme", "mysql",
    "user:pass@tcp(10.0.0.1:3306)/acme?parseTime=true",
    db.WithMaxOpen(50),
    db.WithMaxIdle(10),
    db.WithMaxLife(30*time.Minute),
    db.WithPrinter(func(sql string, args ...interface{}) {
        log.Printf("[SQL] %s %v", sql, args)
    }),
)

// 取用
db.Use()                  // → 默认 main
db.UseWithID("tenant_acme")
db.GetConfig("tenant_acme").GetDialect()  // → MySQLDialect
```

要点：

- **懒连接池**：`Init*` 只登记配置，不连库。首次 `Config.Pool()` 才 `sql.Open` + `Ping`，并应用 `MaxOpen/MaxIdle/MaxLife`。
- **多数据源**：一个进程可同时持有任意数量的命名 `Config`，用 `UseWithID(id)` 切换。典型场景：主库 + 只读副本、独立缓存库、每租户独立库（见 [dataisolate](data-isolate.md) 策略①/②）。
- **Dialect 自动推断**：未显式传 `WithDialect` 时由 `NewDialect(driverName)` 决定——`mysql` → `MySQLDialect`、`postgres`/`pgx` → `PostgresDialect`、`sqlite`/`sqlite3`（及未识别） → `SQLiteDialect`。
- **功能选项**：`WithDialect` / `WithMaxOpen` / `WithMaxIdle` / `WithMaxLife` / `WithPrinter` / `WithSqlKit`（懒建默认 SqlKit）/ `WithHookKit`（默认 nil，全 hook 无操作）/ `WithAutoTableMapping(true)`（Config 级开启自动多表映射，可被 `Dao.AutoTables()` 覆盖）。
- **事务传播统一入口**：`Config.runner(ctx)` —— ctx 携带 `*sql.Tx` 时返回事务连接，否则返回池。
- **资源释放**：`Config.Close()` 关池；`db.ResetConfigs()` 清空注册表（测试用）。

---

## 4. 顶层便捷函数：`Db.use()` 的 Go 版

`db.go` 暴露了一组顶层函数，等价于 `db.Use().Xxx(...)`，覆盖 90% 的 CRUD 场景。调用者无需手动创建 `Dao`：

| 分类 | 函数 |
|------|------|
| **Dao 工厂** | `Use()`、`WithCtx(ctx)`、`UseWithID(id)`、`Select(fields)` |
| **原始 SQL** | `RawSql(q, args...)`、`Sql(tpl, data)` / `SqlWithArgs(tpl, args...)`、`SqlById(id, data)` / `SqlByIdWithArgs(id, args...)` |
| **SQL 模板注册** | `AddSql(id, sql)`、`AddSqlFile(file)`、`AddSqlDir(dir)`、`ParseSqlFile()`、`SetBaseSqlFilePath(p)`、`SetSqlFileHotReloading(bool)` |
| **行 CRUD** | `Insert(row)`、`InsertOrUpdate(row)`、`Update(row)`、`Delete(row)` |
| **按主键** | `FindByID(table, id)` / `FindByIDWithPK(table, pk, id)`、`DeleteByID[Pk]`、`FindInIds`、`DeleteInIds` |
| **复合主键** | `FindByCompositeId(t, k1, k2, id1, id2)`、`FindByCompositeIds(t, keys, ids...)`（任意元数）、对应 `DeleteByCompositeId[s]` |
| **按条件** | `FindBy(table, whereOrField, args...)`、`FindFirstBy(...)`、`FindIn(table, field, vals...)`、`DeleteBy(...)`、`Count(table)` / `CountBy(...)` |
| **批处理 / ctx 感知 / 事务** | `NewBatch()` / `NewBatchCtx(ctx)`；`InsertCtx/UpdateCtx/DeleteCtx/DeleteByIDCtx/FindByIDCtx/FindByCtx`（事务里用）；`Transaction` 家族与 `TransactionOf[R]`、`TxBegin()`、`WithTx(ctx, tx)`、`TxFromContext(ctx)` |

最小示例（来自 `_test/db_test`）：

```go
db.Init("sqlite", "file::memory:?cache=shared")
db.Use().RawSql(`CREATE TABLE user (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT, age INTEGER, email TEXT)`).Update()

// 插入
db.Insert(db.NewRow("user").Set("name", "james").Set("age", 18))

// 查询（裸字段名 → field = ?；含空格 → 当作 WHERE 片段）
row, _ := db.FindByID("user", 1)
rows, _ := db.FindBy("user", "age > ?", 22)
rows, _ = db.FindBy("user", "name", "alice") // 等价 name = ?

// 更新（只生成 SET age=? 这一类变更列）
ok, _ := db.Update(db.NewRow("user").ID(1).Set("age", 19))

// 删除
db.DeleteByID("user", 1)
```

---

## 5. Dao：链式查询构建器

`Dao` 是 SQL 的链式组装器。Builder 方法返回 `*Dao` 自身，查询/DML 方法触发执行并返回结果。`dao.go` 中的核心结构（关键字段）：

```go
type Dao struct {
    config     *Config
    ctx        context.Context  // 携带 *sql.Tx 时走事务
    sqlStr     string           // 原始 SQL
    sqlArgs    []interface{}
    sqlPara    *dbsql.SqlPara   // Enjoy SQL 渲染结果
    selFields  string
    table      string           // 单表 hint（写路径 + JSON 列解码）
    multi      []TableRef       // 多表显式 hint
    autoTables bool             // 自动解析 SQL 做多表映射
    hasGroupBy bool
    failErr    error            // hook veto：非空时 runner() 直接返回该错
}
```

### Builder 方法

| 方法 | 作用 |
|------|------|
| `RawSql(q, args...)` | 直接设置原始 SQL + 参数 |
| `Select(fields)` | 设置 SELECT 投影字段 |
| `Table(name)` | 声明原始 SQL 结果行所属表（带主键 + JSON 列解码） |
| `Tables(refs...)` | 声明多表（首元素为主表，决定写路径） |
| `AutoTables()` | 开启自动 SQL 解析做多表映射 |
| `HasGroupBy(bool)` | 标记是否含 GROUP BY（影响分页 COUNT 子查询） |
| `Sql(tpl, data)` / `SqlById(id, data)` | Enjoy SQL（命名/位置参数） |
| `SqlWithArgs(tpl, args...)` / `SqlByIdWithArgs(id, args...)` | 位置参数版 |
| `Sql0(tpl)` / `SqlById0(id)` | 不带参数 |
| `SqlPara(sp)` | 直接塞一个渲染好的 `SqlPara` |
| `Ctx(ctx)` | 绑定 ctx（事务传播） |

### 查询 / DML / 聚合 / 高级方法

| 分类 | 方法 |
|------|------|
| 列表/首行 | `Find()`、`FindFirst()`、`FindAll(table)`、`FindOne[WithMsg]()`、`FindOneOrNull[WithMsg]`、`FindExists()`、`ForEach(fn)` |
| 条件查 | `FindBy(table, where, args...)`、`FindFirstBy(...)`、`FindIn(table, field, vals...)`、`FindInIds(...)`、`FindByCompositeId[s]` |
| 主键查 | `FindByID(table, id)` / `FindByIDWithPK(table, pk, id)` |
| 分页 | `Paginate(pageNum, pageSize)`、`PaginateWithTotalRows(...)`、`ForEachPage(size, fn)`、`ForEachPageRange(start, end, size, fn)` |
| 聚合 | `Count(table)`、`CountBy(...)` |
| 写 | `InsertRow` / `InsertOrUpdateRow` / `UpdateRow` / `DeleteRow` / `DeleteByID[Pk]` / `DeleteBy` / `DeleteIn` / `DeleteInIds` / `DeleteByCompositeId[s]` / `Update()`（裸 DML） |
| 原始查询 | `Query` / `QueryFirst` / `QueryOne[OrNull]` / `QueryField[Or]` / `QueryStr/QueryInt/QueryInt64/QueryFloat64/QueryTime/QueryBytes/QueryBool` |

`#sql` 模板 + `Dao.Sql` 的典型用法：

```go
tpl := `SELECT * FROM user
         #where(age, ">", age)
         #and(name, "like", name)
         #orderBy(updated, age)`
data := db.NewKv().
    Set("age", 18).
    Set("name", "james").
    Set("orderBy", map[string]any{"field": "updated", "order": "desc"})

rows, err := db.Sql(tpl, data).Find()
```

> **hook veto**：`Dao.Fail(err)` 由 Before hook 调用；下一次 `runner()` 不再返回连接而是该错误，整条语句中止（[dataisolate](data-isolate.md) fail-closed 即基于此）。`Dao.SqlAndArgs()` 是 hook 读取/改写 SQL 的入口，`Dao.Context()` 让 hook 取出 Principal。

---

## 6. Row：Active Record + 变更追踪

`Row` 是一张数据行的载体，字段以 `map[string]interface{}` 存储；变更集 `change map[string]struct{}` 决定 UPDATE 时哪些列进入 `SET`。

### Set / Put 家族

| 方法 | 变更追踪 | 用途 |
|------|---------|------|
| `Set(field, value)` | **记录变更** | 写入并标记 dirty（UPDATE 用） |
| `SetMap(map)` | 记录 | 批量 Set |
| `SetIfNotNull(field, value)` | 视情况 | value 非 nil 才 Set |
| `SetIfNotBlank(field, value)` | 视情况 | 字符串非空才 Set |
| `SetOrPut(field, value)` | 视情况 | 字段已存在 → Set，否则 Put |
| `Put(field, value)` | **不记录** | 仅写值（载入数据、装配外联列） |
| `PutMap(map)` | 不记录 | 批量 Put |

### 变更集管理与取值

| 方法 | 作用 |
|------|------|
| `ChangeSet()` / `ChangedFields()` | 当前变更集 / 排序后的变更字段名切片 |
| `ClearChange()` | 清空变更集（保留 data） |
| `Keep(fields...)` | 仅保留指定字段（同时清理 data 和 change） |
| `Remove(fields...)` / `RemoveNullFields()` / `Clear()` | 删除字段 / 移除 nil 值 / 全清 |
| `Get` / `GetDefault` | `interface{}`（缺席返回 nil/默认） |
| `GetStr/GetInt/GetInt64/GetFloat64/GetBool/GetTime/GetBytes` | 已转型（调用 `type_converter.go` 的 `ToString/ToInt/...`） |
| 各类型的 `GetXxxDefault(field, def)` | 缺席返回 `def` |
| `RowAs[T](r, field, fn)` | nil 安全的泛型读取（fn 不在 nil 时调用） |
| `Has` / `Size` / `FieldNames` / `FieldValues` / `ForEach(fn)` | 元信息 |

> **Java 对照**：Java `AifeiRow.setOrPut` 按表列定义（`columnDefined`）判定；Go 基础 `Row` 无列注册表，以「字段是否已存在于 data」近似——已存在则 `Set`，否则 `Put`。

### Active Record 直写

`Row` 自身提供 `Insert()` / `InsertOrUpdate()` / `Update()` / `Delete()` 四个 Active Record 方法，直接转发到 `db.Insert/...`；构造器 `NewRow(table)` 默认主键 `id`，`NewRowWithPK(table, pk)` 指定单主键，`NewRowWithCompositePK(table, pk1, pk2)` 指定复合主键。

```go
row := db.NewRow("user").
    Set("name", "alice").
    Set("age", 20).
    SetIfNotBlank("email", "")   // 不会写入空串
row.Insert()                      // INSERT INTO user (name, age) VALUES (?, ?)

// 载入后只改一列再 UPDATE
found, _ := db.FindByID("user", 1)
found.Set("age", 21)
found.Update()                    // UPDATE user SET age=? WHERE id=?
```

`Update` 的字段来源是 `row.ChangedFields()`（经 `filterWritableFields` 过滤掉生成列）；`Put` 装配的字段不会被写入。操作成功后 `ClearChange()` 被自动调用，`Row` 可直接复用。

### JSON 与列类型

- `MarshalJSON` / `UnmarshalJSON` 由 `json_codec.go` 提供；`DefaultKeyFormat`（`KeyFormatSnake`）控制全局键形，`Row.SetKeyFormat` 可逐行覆盖；`TimeFormat` 默认 `"2006-01-02 15:04:05"`。
- 复合类型列（struct / slice / map）由 `Table.FieldTypes` 声明，读出时自动从 JSON 字符串物化为 Go 类型（见 §10）。
- `normalizeSQLValue` 将 map/slice/struct 在写入前序列化为 JSON 字符串；`time.Time` 与 `[]byte`（BLOB）原样下推给驱动。

---

## 7. Batch：批处理

`Batch` 支持同构/异构批插入与批更新、裸 SQL 批执行、分块提交、生成键回收：

```go
b := db.NewBatch()
b.BatchSize(500).CommitOnBatchSize(true).GetGeneratedKeys(true)

rows := []*db.Row{
    db.NewRow("user").Set("name", "a"),
    db.NewRow("user").Set("name", "b").Set("email", "b@x"),
    db.NewRow("user").Set("name", "c"),
}
res, err := b.Insert(rows)
// res.RowsAffected / res.UpdateCounts / res.GeneratedKeys / res.Error
```

| 方法 | 说明 |
|------|------|
| `Insert(rows)` / `InsertWithTable(table, rows)` | 同表同列同构批插 |
| `InsertGroup(rows)` | **异构**批插：行可跨表/列不同，按 `(table, field set)` 分组各跑一条 prepared |
| `Update(rows)` / `UpdateWithTable(table, rows)` | 同表同变更字段同构 |
| `UpdateGroup(rows)` | **异构**批更：按 `(table, changed-field tuple, pk tuple)` 分组 |
| `Execute(sql, argsList)` | 同一 SQL 多组 args |
| `ExecuteSQLs(sqls)` | 多条裸 SQL |

要点：

- **分组键**：`(表, 字段集)` 决定一个 prepared 语句桶；行少的列以 `NULL`/缺省由驱动处理，每组复用同一 prepared。
- **BatchSize + CommitOnBatchSize**：每 N 行 `Commit + Begin` 一次，避免大事务无界膨胀。**慎用**：已提交数据后续异常无法回滚。Batch 不在事务上下文中时会自启一个事务管理分块（`beginForChunking` / `commitTail`）。
- **GetGeneratedKeys(true)**：把每条 INSERT 的 `LastInsertId` 收集到 `BatchResult.GeneratedKeys`（驱动相关，MySQL/SQLite 支持）。
- **hook 透明**：批插入逐行触发 `InsertHook.BeforeRowInsert`（用于行盖章）；批更新/删除/裸执行触发 `UpdateHook/ DeleteHook` 的 Before\*，注入的尾随参数（如 `AND tenant_id=?`）在每行 args 后追加。详见 [dataisolate §11 批处理透明化](data-isolate.md)。

---

## 8. Transaction：ctx 传播的事务

Go 没有 thread-local，事务加入是**显式**的：`Transaction` 把 `*sql.Tx` 塞进 ctx，回调里的 `db.WithCtx(ctx)` 调用全部共用这条事务。

### 基础事务

```go
err := db.Transaction(func(ctx context.Context) error {
    if _, err := db.WithCtx(ctx).InsertRow(row1); err != nil { return err }
    if _, err := db.WithCtx(ctx).UpdateRow(row2); err != nil { return err }
    return nil   // 返回 nil 自动 Commit；返回 err 自动 Rollback
})
```

- `Transaction(fn)` / `TransactionWithID(id, fn)` / `TransactionCtx(ctx, fn)` / `TransactionCtxWithID(...)`：四种入口，ctx 版本支持嵌套。
- **嵌套自动 join**：检测到 ctx 已携带 tx 时直接 `fn(ctx)`，不开新事务、不重复提交——交由最外层 owner 决定 commit/rollback。
- **手动事务**：`TxBegin(configID...)` 返回 `*sql.Tx`，配合 `db.WithTx(ctx, tx)` 传播；`TxFromContext(ctx)` 查询当前 tx。

### 泛型事务 + 业务回滚决策

```go
type OrderResult struct { OrderID int64; Code int }

res, err := db.TransactionOf(func(ctx context.Context, tx *db.Tx) (OrderResult, error) {
    row, e := db.WithCtx(ctx).InsertRow(orderRow)
    if e != nil { return OrderResult{}, e }
    // 业务侧主动要求回滚（不靠返回 err）
    if !validate() { tx.Rollback(); return OrderResult{Code: -1}, nil }
    return OrderResult{OrderID: row.GetID().(int64), Code: 0}, nil
})
// 返回 ErrRollback 时：res 仍是原子结果，但事务已回滚
```

事务 commit 与否则由三条规则决定（任一命中即 rollback 并返回 `(result, ErrRollback)`）：

1. `fn` 返回非 nil error；
2. 原子调用了 `tx.Rollback()`；
3. 返回值实现 `RollbackDecision` 且 `ShouldRollback()==true`。

`server.Out` 实现了 `ShouldRollback()`（`code != 0` 为 true），所以 service 可以直接把业务结果从事务里返回，让 code 驱动回滚——与 [aifei](aifei-go.md) 的 `{code, msg, data}` 输出语义天然对齐。

---

## 9. Dialect 与 TypeConverter

### Dialect

`Dialect` 接口抽象了方言差异——标识符引用、CRUD 模板、`InsertOrUpdate`、分页：

| 方言 | 引用符 | `InsertOrUpdate` |
|------|--------|------------------|
| `MySQLDialect` | `` ` `` | `INSERT ... ON DUPLICATE KEY UPDATE col = VALUES(col)` |
| `PostgresDialect` | `"` | `INSERT ... ON CONFLICT (pk) DO UPDATE SET col = EXCLUDED.col` |
| `SQLiteDialect` | （无） | `INSERT OR REPLACE INTO ...` |

三者都实现：`ForSelect` / `ForFindBy` / `ForFindIn` / `ForCount` / `ForCountBy` / `ForDeleteBy` / `ForDeleteIn` / `ForFindByID` / `ForDeleteByID` / `ForInsert` / `ForUpdate` / `ForInsertOrUpdate` / `ForCountSubquery` / `ForPaginate`。分页统一 `LIMIT n OFFSET m`（`offset = (pageNum-1) * pageSize`）。

`FindBy`/`DeleteBy`/`CountBy` 的 `whereOrField` 参数有「两种形态」：含空格 → 当作完整 WHERE 片段（如 `"age > ?"`）；否则当作字段名（自动补 `= ?`）。

### TypeConverter

`ToInt` / `ToInt64` / `ToFloat64` / `ToBool` / `ToString` / `ToTime` 都是 nil 安全、多源适配的转换函数，`Row.GetStr/GetInt/...` 与 `Kv.GetStr/...` 都基于它们。`ToTime` 依次尝试 `"2006-01-02 15:04:05"`、RFC3339、`"2006-01-02"` 等多种 layout。

---

## 10. Table 元数据与多表关联映射

`Table` 是运行时的表元数据，由 [generator](generator.md) 生成的 `base.go` 在 `init()` 中调用 `db.RegisterTable(&db.Table{...})` 注册：

```go
type Table struct {
    Name             string
    Fields           string                 // 逗号分隔的列名（内部用）
    PrimaryKeys      []string
    FieldTypes       map[string]reflect.Type // 列名 → Go 类型（JSON 列解码依据）
    GeneratedColumns []string                // 生成/计算列，INSERT/UPDATE 时自动剔除
}
```

注册后的 `Table` 支撑三个机制：

1. **写路径过滤**：`filterWritableFields` 剔除 `GeneratedColumns`，避免向生成列写值。
2. **读路径解码**：`Table.FieldTypes` 声明的复合类型列（struct/slice/map），读出时从 JSON 字符串物化为声明的 Go 类型——typed Dao 的 getter 才能拿到正确类型。
3. **多表关联映射**：JOIN 查询的结果行套用多张表的元数据。

### 多表映射的三种 hint

```go
// ① 单表 hint：结果行绑定到 user 表
db.RawSql(`SELECT * FROM user WHERE id = ?`, 1).Table("user").FindFirst()

// ② 多表显式 hint：第一个是主表，决定 row.Table() 和写路径
db.RawSql(`SELECT u.*, d.config AS dept_config
           FROM user u JOIN dept d ON u.dept_id = d.id`).
    Tables(db.TableRef{Table: "user", Alias: "u"},
           db.TableRef{Table: "dept", Alias: "d"}).Find()

// ③ 自动解析：根据 SQL 文本自行提取表/别名/投影
db.RawSql(`SELECT u.*, d.name AS dept_name FROM user u JOIN dept d ON ...`).
    AutoTables().Find()
```

三种 hint 的优先级：`Tables()` > `AutoTables()` > `Table()`。自动解析由 `db/sql/parse.go` 完成（见 [docs/arch/multi-table-mapping.md](../arch/multi-table-mapping.md)）——纯字符串扫描，O(n)，零依赖，对不识别的构造优雅降级。列归属判定三步走：

1. SELECT 投影中的别名前缀（`u.col`）——最强信号；
2. 唯一归属（仅一张注册表声明此列）；
3. 冲突 → 主表赢。

---

## 11. db/sql：Enjoy SQL 模板

`db/sql` 子包把 [enjoy](enjoy.md) 模板引擎嫁接到 SQL 渲染上。`NewSqlKit(name)` 内部 `engine.AddDirective` 注册了 7 条 SQL 指令（`sql` / `where` / `and` / `or` / `orderBy` / `para` / `p`），并维护 `sqlID → *enjoy.Template` 缓存、外部 `.sql` 文件登记表和 dev 模式热重载。

### 七条 SQL 指令

| 指令 | 形式 | 说明 |
|------|------|------|
| `#sql(id) ... #end` | 块级 | 定义一段命名 SQL 模板，注册到 cache |
| `#para(name)` / `#p(name)` | 行内 | 命名参数：输出 `?` 并把 `data[name]` 追加到 `SqlPara.Paras` |
| `#para(int)` | 行内 | 位置参数：按下标从 `args` 取值 |
| `#para(name, type)` | 行内 | 带类型：`like`/`%like`/`like%` 包裹，或 `in` 展开成 `(?, ?, ...)` |
| `#where(field, op, para)` | 行内 | 首个非空条件用 `WHERE` 开头，否则跳过 |
| `#and(field, op, para)` / `#or(...)` | 行内 | 后续条件；`#where` 后用 `AND`/`OR` 连接 |
| `#orderBy(...)` | 行内 | 白名单排序：支持 `$name` 自定义数据键、`sqlField:clientField` 映射 |

`#where` 的精髓是**空值自动省略**：当参数为 `nil` 或 `""` 时整条条件不出现在 SQL 里，也不留 `WHERE` 关键字。这让动态查询模板写起来像静态 SQL：

```sql
#sql("searchUsers")
  SELECT * FROM users
  #where(status, "=", status)
    #and(name, "like", name)
    #and(age, "between", ageRange)
    #and(dept_id, "in", deptIds)
  #end
  #orderBy($sort, created_at, name)
#end
```

传入 `{status:"active", name:"alice", ageRange:[18,60], deptIds:[1,2,3], sort:{field:"created_at",order:"desc"}}` 渲染得到：

```sql
SELECT * FROM users
WHERE status = ? AND name LIKE ? AND age BETWEEN ? AND ? AND dept_id IN (?, ?, ?)
ORDER BY created_at DESC
-- Paras: ["active", "%alice%", 18, 60, 1, 2, 3]
```

### 操作符表（18 种逻辑操作）

`#where` / `#and` / `#or` 的 `op` 参数由 `SqlOperatorFrom(key)` 查表，同一 SQL 形态有多种 key 别名（全大写、全小写、驼峰都行——内部注册了 `strings.ToLower` 别名）：

| 分类 | key（不区分大小写） | 生成 SQL | 参数数 |
|------|-----|---------|--------|
| 比较 | `=` / `!=` / `<>` / `>` / `>=` / `<` / `<=` | 同名（`<>` 归一为 `!=`） | 1 |
| 集合 | `in` / `not in` | `IN (...)` / `NOT IN (...)`（slice/数组展开；单值退化为 `(?)`） | 1（slice） |
| 范围 | `between` / `not between` | `BETWEEN ? AND ?` / `NOT BETWEEN ? AND ?`（必须 2 元） | 1（2 元 slice） |
| NULL | `is null` / `is not null` | `IS NULL` / `IS NOT NULL`（**零参数**；仅判 scope 中 field 是否存在） | 0 |
| LIKE | `like` / `not like` | `LIKE ?` / `NOT LIKE ?`（值包成 `%value%`） | 1 |
| LIKE | `contains` / `notContains` | `LIKE %value%` / `NOT LIKE %value%` | 1 |
| LIKE | `startsWith` / `endsWith` | `LIKE value%` / `LIKE %value` | 1 |

> **IS NULL 是零参数**：`#where(nickname, "is null")` 只要 scope 里存在 `nickname` 键即生成 `WHERE nickname IS NULL`。其他操作符遇 `nil`/`""` 自动跳过整条条件。

### SqlKit API

`SqlKit` 的方法与 §4 的顶层函数一一对应（`db.GetConfig().GetSqlKit()` 是它们的实际落点）：

| 方法 | 说明 |
|------|------|
| `GetSqlPara(tpl, data)` / `GetSqlParaWithArgs(tpl, args...)` | 渲染内联模板（命名/位置参数） |
| `GetSqlParaByID(id, data)` / `GetSqlParaByIDWithArgs(id, args...)` | 取缓存模板渲染 |
| `AddSql(id, sql)` | 内联注册一段含 `#sql(id)...#end` 的模板 |
| `AddSqlFile(path)` / `AddSqlDir(dir)` + `ParseSqlFile()` | 登记并解析外部 `.sql` 文件，ID 冲突报错 |
| `SetBaseSqlFilePath(p)` / `SetSqlFileHotReloading(bool)` | 相对路径基准 / dev 模式热重载（不影响 `AddSql` 内联缓存） |
| `Engine()` | 暴露底层 enjoy 引擎，可塞自定义指令/共享对象 |

`SqlPara`（`{ID, Sql, Paras, Enjoy}`）作为 SQL + 参数的载体，被 `Dao.sqlPara` 持有；`Dao.SqlAndArgs()` 返回 `(sql, args)` 给 hook 检视/改写。

---

## 12. Hook 扩展点：DbHookKit

`Config.HookKit` 是六类 hook 的容器，默认全 nil（无操作）。每条 hook 都分 Before/After 两阶段，Before 可改写 dao 的 `sqlPara`（通过 `Dao.SqlAndArgs()` 读取再写回）：

| Hook | 触发范围 | 典型用途 |
|------|---------|---------|
| `InsertHook` | `InsertRow`、`Batch.Insert/InsertGroup`（逐行 `BeforeRowInsert`） | 行盖章（tenant_id、created_time） |
| `UpdateHook` | `UpdateRow`、`Dao.Update()`、`Batch.Update/UpdateGroup/Execute`（UPDATE 类） | 自动填 updated_time、缓存失效 |
| `DeleteHook` | `DeleteRow`、`DeleteBy/In/Ids`、`Batch`（DELETE 类） | 软删、归档 |
| `FindHook` | `Find/FindBy/FindByID/FindAll/FindIn` 及 First 变体 | 拦截 `SELECT *`、慢查询统计 |
| `QueryHook` | `Dao.Find()` 在设了原始 SQL 时、所有 `Query*` 方法 | 原始 SQL 审计 |
| `PaginateHook` | `Paginate` / `PaginateWithTotalRows` | 缓存 totalRows、改写分页 SQL |

关键设计：

- **fail-closed 入口**：Before hook 调用 `Dao.Fail(err)` → `runner()` 返回该错 → executor 直接中止（[dataisolate](data-isolate.md) 无法安全改写时即走此路径）。
- **Row 与 Sql 双 hook**：`UpdateHook`/`DeleteHook` 都有 `BeforeRowUpdate`/`BeforeSqlUpdate` 两个分支，对应 `UpdateRow(row)` 与 `Dao.Update()`（裸 DML）；executor 自动分流。
- **批处理复用**：批插入逐行触发 `BeforeRowInsert`（行盖章），批更新/删除每组触发一次 Sql 版 Before，注入的尾随参数追加到每行 args。

---

## 13. 模块结构

```
db/
├── db.go              # 顶层便捷函数（Use/Sql/Insert/FindByID/Transaction...）
├── dao.go             # Dao 链式构建器 + 执行方法签名
├── executor.go        # 30+ 个 execXxx + decodeRows + 多表映射解析
├── row.go             # Row Active Record + 变更追踪
├── kv.go              # Kv 流式 map（Sql 的 data 参数常用形态）
├── page.go            # Page 分页结果
├── batch.go           # Batch 批处理（同构/异构分组、分块提交）
├── transaction.go     # Transaction / TransactionOf / Tx / RollbackDecision
├── tx_context.go      # DBConn 接口、withTx / txFromContext / WithTx
├── config.go          # Config + 功能选项 + 懒连接池 + 注册表
├── dialect.go         # MySQL/PostgreSQL/SQLite Dialect
├── table.go           # Table 注册 + 多表映射（tableMapping）
├── type_converter.go  # ToInt/ToInt64/ToFloat64/ToBool/ToString/ToTime
├── json_codec.go      # Row JSON 编解码 + normalizeSQLValue（JSON 列）
├── hook.go            # DbHookKit + 六类 Hook 接口
└── sql/               # Enjoy SQL 子包
    ├── kit.go                # SqlKit（引擎封装 + 模板缓存 + 文件 + 热重载）
    ├── para.go / keys.go     # SqlPara + scope key（SqlCacheKey / SqlParaKey / ParaArrayKey）
    ├── operator.go           # 18 种 SqlOperator（含 LIKE 模式）
    ├── directive.go          # #sql(id)...#end
    ├── where/and/or_directive.go  # #where / #and / #or
    ├── para_directive.go     # #para(name) / #p(name) / #para(int) / 带类型
    ├── orderby_directive.go  # #orderBy 白名单排序
    ├── condition.go          # SqlCondition：参数解析 + SQL 生成（IN/BETWEEN/LIKE）
    └── parse.go              # 轻量 SQL parser：FROM/JOIN 表 + SELECT 投影（多表映射用）
```

源码约 6,800 行（含 `db/sql` 约 1,900 行），测试在 `_test/db_test/`（约 1,700 行，CRUD / 分页 / 事务 / 批处理 / 复合主键 / JSON 列 / 多表映射 / Enjoy SQL 全套集成测试），用 `modernc.org/sqlite` 跑内存库。

---

## 14. 总结

Aifei-Go 的 `db` 模块围绕几个核心设计原则构建：

1. **零外部依赖**：核心只依赖标准库 `database/sql` + 同仓库的 [enjoy](enjoy.md)；驱动由用户自选。
2. **三套 API 形态并存**：顶层便捷函数、`Dao` 链式构建器、`Row` Active Record——同一份数据可以按场景用最合适的姿势。
3. **ctx 显式传播的事务**：Go 无 thread-local，事务加入靠 `ctx`；嵌套自动 join，业务侧可用 `Tx.Rollback()` 或 `RollbackDecision` 主动回滚。
4. **声明式 SQL 模板**：Enjoy SQL 的 `#where`/`#and`/`#orderBy` 让动态查询写得像静态 SQL——空值自动省略、参数自动对齐，杜绝字符串拼接。
5. **元数据驱动**：注册的 `Table` 既支撑写路径过滤（生成列）、读路径解码（JSON 列），又让多表 JOIN 的结果行自动绑定正确类型——typed Dao 在多表场景下依然 work。
6. **可插拔 hook**：`DbHookKit` 的六类 hook 是 SQL 改写、审计、缓存的统一挂载点，[dataisolate](data-isolate.md) 即基于此实现透明的租户/行/列隔离。

### 延伸阅读

- [enjoy](enjoy.md) — SqlKit 底层的模板引擎（DKFF 词法 + DLRD 语法）
- [generator](generator.md) — 基于 `Table` 元数据生成 typed Dao / Service
- [config](config.md) — 分层配置加载（`Config` 在此之前由应用初始化）
- [dataisolate](data-isolate.md) — 基于 `DbHookKit` 的数据隔离插件
- [docs/arch/multi-table-mapping.md](../arch/multi-table-mapping.md) — 多表关联映射设计文档
- [docs/arch/03-phase3-db.md](../arch/03-phase3-db.md) — db 模块原始设计
