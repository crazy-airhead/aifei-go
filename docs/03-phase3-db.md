# Phase 3: 数据库模块

> 目标：实现 Aifei Go 的数据库访问层，包含 Db、Row、Dao、Page、Dialect、Enjoy SQL 模板引擎、Generator 代码生成器

## 1. 模块结构

```
db/                       # 数据库访问库（零外部依赖，完全不知道 generator 的存在）
├── db.go                # Db 入口 (快捷方法)
├── dao.go               # Dao 链式调用对象
├── row.go               # Row 数据行 (Active Record)
├── page.go              # Page 分页结果
├── batch.go             # Batch 批量操作
├── dialect.go           # Dialect 接口 + MySQL/PostgreSQL/SQLite 实现
├── config.go            # 数据库配置 (含 SqlKit 集成)
├── transaction.go       # 事务管理
├── type_converter.go    # 类型转换器
├── table.go             # Table 运行时元数据
│
└── sql/                 # Enjoy SQL 模板引擎 (已实现)
    ├── kit.go               # SqlKit — 包装 Enjoy 引擎
    ├── para.go              # SqlPara — SQL + 参数
    ├── directive.go         # Directive 接口
    ├── para_directive.go    # #para / #p 指令
    ├── where_directive.go   # #where 指令
    ├── and_directive.go     # #and 指令
    ├── orderby_directive.go # #orderBy 指令
    ├── condition.go         # SqlCondition 类型
    ├── keys.go              # SQL 模板内部 key 常量
    └── operator.go          # SqlOperator 枚举 (18 种操作符)

generator/                # 代码生成器独立模块（import db + enjoy）
    ├── generator.go         # Generator — 主入口
    ├── meta_reader.go       # MetaReader — 读取数据库元数据
    ├── meta_dialect.go      # MetaDialect — generator 专用方言接口
    ├── type_mapping.go      # TypeMapping — SQL 类型 → Go 类型
    ├── field_to_attr.go     # FieldToAttr — 字段名 → 属性名
    ├── go_keyword.go        # GoKeyword — Go 保留字检查
    ├── types.go             # TableInfo / FieldInfo 数据结构
    ├── template_util.go     # TemplateUtil — Enjoy 模板辅助方法
    ├── base_generator.go    # BaseGenerator — 生成 base.go (覆盖写入)
    ├── model_generator.go   # ModelGenerator — 生成 model 文件 (存在则跳过)
    ├── dao_generator.go     # DaoGenerator — 生成 dao.go (存在则跳过)
    ├── service_generator.go # ServiceGenerator — 生成 service.go (存在则跳过)
    ├── tables_generator.go  # TablesGenerator — 生成 tables.go (覆盖写入)
    └── templates/           # Enjoy 模板文件 (.af)
        ├── _base.af             # 生成 BaseXxx + Table + getter/setter
        ├── _model.af            # 生成 Xxx struct
        ├── _dao.af              # 生成 Dao + 查询函数
        ├── _service.af          # 生成 Service + HTTP 路由
        └── _tables.af           # 生成 Tables 集合
```

**依赖方向（无循环）：**
```
generator/       ──import──→  db/, enjoy/
生成的 user/ etc ──import──→  db/
db/              ──不依赖──→  generator 和生成代码
enjoy/           ──零依赖
```

---

## 2. Enjoy SQL 模板引擎（`db/sql/`，已实现）

### 概述

Enjoy SQL 模板引擎是 Aifei db 的核心亮点之一。它利用 Enjoy 模板引擎的自定义指令（Directive）机制，在 SQL 中嵌入动态逻辑（条件 where、动态排序、参数占位等），解决传统 SQL 拼接的痛点。

### 架构

```
SqlKit
  └── Enjoy Engine (含 SQL 指令注册)
        └── 模板缓存 (sync.Map)
              └── SqlPara (SQL + 参数列表)
```

### 核心 API

```go
// 创建 SqlKit
sk := sql.NewSqlKit("main")

// 获取 SqlPara — 直接传入 SQL 模板字符串
sp := sk.GetSqlPara(
    "select * from user #where() #and(age > #para(age)) #and(name like #para(name)) #orderBy(orderBy)",
    map[string]interface{}{"age": 18, "name": "%james%", "orderBy": "id desc"},
)
// sp.Sql = "select * from user where age > ? and name like ? order by id desc"
// sp.Paras = [18, "%james%"]

// 缓存 SQL 模板 — 通过 ID 引用
sk.AddSql("user.list", "select * from user #where() #and(status = #para(status))")
sp := sk.GetSqlParaByID("user.list", map[string]interface{}{"status": 1})

// 位置参数方式
sp := sk.GetSqlParaWithArgs("select * from user where id = #para(0)", 123)
```

### 与 Db/Dao 集成

SqlKit 通过 Config 绑定到每个数据库连接：

```go
// Db 层面的快捷方法
db.Sql("select * from user #where() #and(age > #para(age))", map[string]interface{}{"age": 18}).Find()
db.SqlWithArgs("select * from user where id = #para(0)", 123).FindFirst()
db.SqlById("user.list", map[string]interface{}{"status": 1}).Find()
db.AddSql("user.list", "select * from user #where() #and(status = #para(status))")
```

---

## 3. Db 入口（已实现）

```go
package db

// 初始化
func Init(driverName, dsn string, opts ...ConfigOption) error
func InitWithID(configID, driverName, dsn string, opts ...ConfigOption) error

// 获取配置
func GetConfig(id ...string) *Config
func ResetConfigs()

// 获取 Dao
func Use() *Dao
func UseWithID(configID string) *Dao

// SQL 入口 (链式查询起点)
func SQL(query string, args ...interface{}) *Dao                    // 原始 SQL
func Sql(sqlStr string, data map[string]interface{}) *Dao           // Enjoy SQL 模板
func SqlWithArgs(sqlStr string, args ...interface{}) *Dao           // Enjoy SQL 模板 + 位置参数
func SqlById(sqlID string, data map[string]interface{}) *Dao        // 缓存 SQL 模板
func SqlByIdWithArgs(sqlID string, args ...interface{}) *Dao        // 缓存 SQL 模板 + 位置参数
func AddSql(sqlID, sql string)                                      // 缓存 SQL 模板
func AddSqlWithID(configID, sqlID, sql string)                      // 指定配置缓存
func Select(fields string) *Dao

// 快捷 CRUD
func Insert(row *Row) (*Row, error)
func InsertOrUpdate(row *Row) (*Row, error)
func Update(row *Row) (bool, error)
func Delete(row *Row) (bool, error)
func DeleteByID(table string, id interface{}) (bool, error)
func DeleteByIDWithPK(table, pk string, id interface{}) (bool, error)
func DeleteBy(table, whereOrField string, args ...interface{}) (int64, error)
func FindByID(table string, id interface{}) (*Row, error)
func FindByIDWithPK(table, pk string, id interface{}) (*Row, error)
func FindBy(table, whereOrField string, args ...interface{}) ([]*Row, error)
func FindFirstBy(table, whereOrField string, args ...interface{}) (*Row, error)
func FindIn(table, field string, values ...interface{}) ([]*Row, error)
func Count(table string) (int64, error)
func CountBy(table, whereOrField string, args ...interface{}) (int64, error)

// 事务 / 批量
func Transaction(fn func() error) error
func TransactionWithID(configID string, fn func() error) error
func TxBegin(configID ...string) (*sql.Tx, error)
func NewBatch() *Batch

// Row 工厂
func NewRow(table string) *Row
func NewRowWithPK(table, pk string) *Row
func NewRowWithCompositePK(table, pk1, pk2 string) *Row

// Table 元数据
func RegisterTable(t *Table)
func Tables() []*Table
```

---

## 4. Dao（已实现）

```go
type Dao struct {
    config    *Config
    sqlStr    string
    sqlArgs   []interface{}
    sqlPara   *sql.SqlPara
    selFields string
    fromTable string
}

// 链式入口
func (d *Dao) SQL(query string, args ...interface{}) *Dao
func (d *Dao) Sql(sqlStr string, data map[string]interface{}) *Dao
func (d *Dao) SqlWithArgs(sqlStr string, args ...interface{}) *Dao
func (d *Dao) SqlById(sqlID string, data map[string]interface{}) *Dao
func (d *Dao) SqlByIdWithArgs(sqlID string, args ...interface{}) *Dao
func (d *Dao) Select(fields string) *Dao
func (d *Dao) SqlPara(sp *SqlPara) *Dao

// 链式终止 (查询)
func (d *Dao) Find() ([]*Row, error)
func (d *Dao) FindFirst() (*Row, error)
func (d *Dao) FindAll() ([]*Row, error)
func (d *Dao) Paginate(pageNum, pageSize int) (*Page, error)

// 链式终止 (更新)
func (d *Dao) Update() (int64, error)
func (d *Dao) InsertRow(row *Row) (*Row, error)
func (d *Dao) InsertOrUpdateRow(row *Row) (*Row, error)
func (d *Dao) UpdateRow(row *Row) (bool, error)
func (d *Dao) DeleteRow(row *Row) (bool, error)

// Table CRUD
func (d *Dao) FindByID(table string, id interface{}) (*Row, error)
func (d *Dao) FindByIDWithPK(table, pk string, id interface{}) (*Row, error)
func (d *Dao) FindBy(table, whereOrField string, args ...interface{}) ([]*Row, error)
func (d *Dao) FindFirstBy(table, whereOrField string, args ...interface{}) (*Row, error)
func (d *Dao) DeleteByID(table string, id interface{}) (bool, error)
func (d *Dao) DeleteByIDWithPK(table, pk string, id interface{}) (bool, error)
func (d *Dao) DeleteBy(table, whereOrField string, args ...interface{}) (int64, error)
func (d *Dao) Count(table string) (int64, error)
func (d *Dao) CountBy(table, whereOrField string, args ...interface{}) (int64, error)
func (d *Dao) FindIn(table, field string, values ...interface{}) ([]*Row, error)
func (d *Dao) DeleteIn(table, field string, values ...interface{}) (int64, error)
```

---

## 5. Row（已实现）

```go
type Row struct {
    table       string
    primaryKeys []string
    data        map[string]interface{}
    change      map[string]struct{} // 追踪 Set 操作的字段
}

// 工厂方法 (通过 db.NewRow / db.NewRowWithPK)

// Set 系列 (追踪 change — 用于 UPDATE)
func (r *Row) Set(field string, value interface{}) *Row
func (r *Row) SetMap(data map[string]interface{}) *Row
func (r *Row) SetIfNotNull(field string, value interface{}) *Row
func (r *Row) SetIfNotBlank(field string, value interface{}) *Row

// Put 系列 (不追踪 change — 仅填充数据)
func (r *Row) Put(field string, value interface{}) *Row
func (r *Row) PutMap(data map[string]interface{}) *Row

// 字段操作
func (r *Row) Remove(fields ...string) *Row
func (r *Row) RemoveNullFields() *Row
func (r *Row) Keep(fields ...string) *Row
func (r *Row) Clear() *Row

// Change 追踪
func (r *Row) ChangeSet() map[string]interface{}
func (r *Row) ClearChange()
func (r *Row) ChangedFields() []string

// 类型安全 Getter
func (r *Row) GetStr(field string) string
func (r *Row) GetInt(field string) int
func (r *Row) GetInt64(field string) int64
func (r *Row) GetFloat64(field string) float64
func (r *Row) GetBool(field string) bool
func (r *Row) GetTime(field string) time.Time
func (r *Row) GetBytes(field string) []byte
func (r *Row) Get(field string) interface{}
// 带默认值版本: GetStr, GetInt

// 主键
func (r *Row) ID(id interface{}) *Row
func (r *Row) GetID() interface{}
func (r *Row) CompositeID(id1, id2 interface{}) *Row

// 元数据
func (r *Row) Table() string
func (r *Row) SetTable(table string)
func (r *Row) PrimaryKeys() []string
func (r *Row) SetPrimaryKeys(pks []string)
func (r *Row) Has(field string) bool
func (r *Row) Size() int
func (r *Row) FieldNames() []string
func (r *Row) FieldValues() []interface{}
func (r *Row) ForEach(fn func(field string, value interface{}))

// JSON
func (r *Row) MarshalJSON() ([]byte, error)
func (r *Row) UnmarshalJSON(data []byte) error

// Active Record
func (r *Row) Insert() (*Row, error)
func (r *Row) InsertOrUpdate() (*Row, error)
func (r *Row) Update() (bool, error)
func (r *Row) Delete() (bool, error)
```

---

## 6. Page / Dialect / Batch / Transaction（已实现）

### Page

```go
type Page struct {
    PageNum    int    `json:"pageNum"`
    PageSize   int    `json:"pageSize"`
    TotalRows  int64  `json:"totalRows"`
    TotalPages int    `json:"totalPages"`
    Rows       []*Row `json:"rows"`
}

func NewPage(pageNum, pageSize int, totalRows int64, rows []*Row) *Page
func (p *Page) IsFirstPage() bool
func (p *Page) IsLastPage() bool
func (p *Page) HasPreviousPage() bool
func (p *Page) HasNextPage() bool
```

### Dialect

```go
type Dialect interface {
    Name() string
    DefaultPrimaryKeys() []string
    ForCountSubquery(query string) string
    ForPaginate(query string, pageNum, pageSize int) string
    ForFindByID(table string, primaryKeys []string) string
    ForDeleteByID(table string, primaryKeys []string) string
    ForInsert(table string, fields []string) string
    ForUpdate(table string, fields []string, primaryKeys []string) string
    ForInsertOrUpdate(table string, fields []string, primaryKeys []string) string
}

// 内置实现
type MySQLDialect struct{}
type PostgresDialect struct{}
type SQLiteDialect struct{}

func NewDialect(driverName string) Dialect
```

### Batch / Transaction

```go
// Batch
type Batch struct { ... }
func NewBatch() *Batch
func (b *Batch) Insert(rows []*Row) (*BatchResult, error)
func (b *Batch) InsertWithTable(table string, rows []*Row) (*BatchResult, error)
func (b *Batch) Update(rows []*Row) (*BatchResult, error)
func (b *Batch) UpdateWithTable(table string, rows []*Row) (*BatchResult, error)
func (b *Batch) Execute(sql string, argsList [][]interface{}) (*BatchResult, error)
func (b *Batch) ExecuteSQLs(sqls []string) (*BatchResult, error)

type BatchResult struct {
    RowsAffected int64
    Error        error
}

// Transaction
func Transaction(fn func() error) error
func TransactionWithID(configID string, fn func() error) error
func TxBegin(configID ...string) (*sql.Tx, error)
```

---

## 7. Generator 代码生成器（已实现）

### 7.1 设计动机

在 Java Aifei 中，Generator 是 `aifei-db` 的两个核心组件之一。它通过读取数据库元数据自动生成类型安全的 CRUD 代码：

- **消除手写样板代码**：每个数据表需要手写大量 getter/setter/CRUD 方法，Generator 自动生成类型安全的代码
- **编译期类型检查**：生成的 `GetName() string` 远比 `GetStr("name")` 安全
- **Schema 同步**：数据库字段变更后重新生成 base.go 即可，model/dao/service 因已存在而不会被覆盖
- **Short Setter**：`user.New().Name_("james").Age_(28)` 链式调用风格

### 7.2 生成策略：每表一个包

Java 按层级分 `base/`、`model/`、`dao/` 三个包。Go 采用**每表一个包**策略：

```
myapp/db/
├── tables.go              # 每次覆盖 — 所有表的 Table 元数据集合
│
├── user/                  # package user
│   ├── base.go            # 每次覆盖 — BaseUser + Table + getter/setter + 实例 CRUD
│   ├── user.go            # 存在则跳过 — User struct（用户扩展）
│   ├── dao.go             # 存在则跳过 — Dao + FindById 等查询方法
│   └── service.go         # 存在则跳过 — Service + HTTP 方法路由
│
├── order/                 # package order
│   ├── base.go
│   ├── order.go
│   ├── dao.go
│   └── service.go
```

**同一包内互引无需 import，彻底消除循环依赖的风险。**

| Java 层级方案 | Go 每表一包 |
|---|---|
| `base.FindUserById(id)` | `user.FindById(id)` |
| `model.User` | `user.User` |
| `dao.NewUserDao()` | `user.NewDao()` |

### 7.3 架构

Generator 作为独立模块 `generator/`，与 `db/` 完全解耦。它 `import db` 但 `db` 不知道它的存在。

**MetaDialect 接口**（generator 内部定义，不污染 `db.Dialect`）：

```go
package generator

// MetaDialect 是 generator 专用的方言接口，在 db.Dialect 基础上增加元数据查询方法。
type MetaDialect interface {
    db.Dialect
    QueryTableNames(db *sql.DB) ([]string, error)
    QueryTableInfo(table string) string
}
```

### 7.4 数据结构

```go
type FieldInfo struct {
    Name            string // 数据库字段名 (snake_case)
    GoType          string // Go 类型，如 "int", "string", "time.Time"
    AttrName        string // Go 导出字段名 (CamelCase)
    Remarks         string // 字段注释
    IsAutoIncrement bool   // 是否自增
}

type TableInfo struct {
    Name       string       // 表名
    PrimaryKey []string     // 主键列名
    Remarks    string       // 表注释
    IsView     bool         // 是否为视图
    Fields     []*FieldInfo // 字段列表
    PkgName    string       // 包名
    StructName string       // 结构体名
    BaseName   string       // 基础结构体名
}
```

### 7.5 Generator — 主入口

```go
package generator

type Generator struct {
    pool       *sql.DB
    dialect    MetaDialect
    outputDir  string
    importRoot string

    metaReader        *MetaReader
    baseGenerator     *BaseGenerator
    modelGenerator    *ModelGenerator
    daoGenerator      *DaoGenerator
    serviceGenerator  *ServiceGenerator
    tablesGenerator   *TablesGenerator
}

func New(pool *sql.DB, dialect MetaDialect, outputDir, importRoot string) *Generator

func (g *Generator) ConfigMetaReader(fn func(*MetaReader)) *Generator
func (g *Generator) ConfigBaseGenerator(fn func(*BaseGenerator)) *Generator
func (g *Generator) ConfigServiceGenerator(fn func(*ServiceGenerator)) *Generator

func (g *Generator) Generate() error
```

### 7.6 使用示例

```go
import (
    "github.com/crazy-airhead/aifei-go/db"
    "github.com/crazy-airhead/aifei-go/generator"
)

func main() {
    db.Init("sqlite3", "app.db")
    pool, _ := db.GetConfig().Pool()

    dialect := generator.NewMetaDialect(db.GetConfig().GetDialect())
    gen := generator.New(pool, dialect, "./myapp/db", "myapp/db")

    gen.ConfigMetaReader(func(mr *generator.MetaReader) {
        mr.AddBlacklist("migrations", "schema_version")
    })

    gen.Generate() // 每个表一个包：user/、order/、product/
}
```

### 7.7 Go vs Java 生成差异对比

| 方面 | Java 版 | Go 版 |
|------|---------|-------|
| 包结构 | `base/` `model/` `dao/` 三层 | 每表一包 + 汇总 `tables.go` |
| 模型注册 | `ModelSet` — `LinkedHashSet<Class>` 用于 DI 扫描 | `Tables` — `[]*db.Table` 用于跨表遍历 |
| 继承方式 | `BaseUser<M> extends AifeiRow<M>` + CRTP | `BaseUser` 内嵌 `*db.Row` |
| 模型类 | `User extends BaseUser<User>` | 同包内 `User` 嵌入 `*BaseUser` |
| 查询方法 | `User.findById(123)` | `user.FindById(123)` — 包名即命名空间 |
| Dao 实例 | `new UserDao()` | `user.NewDao()` — 无冗余命名 |
| Service | Controller 层 | `service.go` — 同包内 HTTP 方法路由 |
| 模板引擎 | Enjoy 模板 (.af) | Enjoy 模板 (.af) — 相同引擎 |
| Short Setter | `user.name("james")` | `user.New().Name_("james")` |

### 7.8 Enjoy 代码生成模板

使用 Enjoy 模板引擎生成代码（与 Java 版一致），通过 `//go:embed` 嵌入：

- **`_base.af`** — 生成 `base.go`：`BaseXxx` 结构体、`Table` 常量、类型安全 getter/setter/short setter、CRUD 实例方法。每次覆盖写入。
- **`_model.af`** — 生成 model 文件：`Xxx` 结构体嵌入 `*BaseXxx`。存在则跳过。
- **`_dao.af`** — 生成 `dao.go`：`Dao` 结构体、包级查询函数（`FindById`、`FindBy`、`DeleteById`、`Count` 等）、Dao 实例方法。存在则跳过。
- **`_service.af`** — 生成 `service.go`：`Service` 结构体、`init()` 自注册、HTTP 方法路由（`List`、`Create`、`GetById`、`UpdateById`、`DeleteById`）、`MethodInterceptors()`。存在则跳过。
- **`_tables.af`** — 生成 `tables.go`：汇总所有表的 `*db.Table` 引用。每次覆盖写入。

---

## 8. 完整使用示例对比

### 原生 Row API

```go
// 插入
row := db.NewRow("user").Set("name", "james").Set("age", 18)
db.Insert(row)

// 查询
rows, _ := db.FindBy("user", "age > ?", 18)
name := rows[0].GetStr("name") // 无编译期类型检查

// 更新
db.Update(db.NewRow("user").ID(123).Set("name", "james zhan"))
```

### Generator 生成的类型安全 API（每表一包）

```go
import "myapp/db/user"

// 插入 — 类型安全 + Short Setter
u := user.New().Name_("james").Age_(28)
u.Insert()

// 查询 — 包名即命名空间
u, _ := user.FindById(123)
fmt.Println(u.Name()) // 类型安全，IDE 自动补全

list, _ := user.FindBy("age > ?", 18)

// 更新
u, _ := user.FindById(123)
u.SetName("james zhan").Update()

// 删除
user.DeleteById(123)
```

### Enjoy SQL 模板 + 生成的 Dao

```go
import "myapp/db/user"

// 注册 SQL 模板
db.AddSql("user.listByAge", `
    select * from user
    #where()
    #and(age > #para(minAge))
    #and(status = #para(status))
    #orderBy(orderBy)
`)

// 包级函数 — 简单查询
list, _ := user.FindBy("age > ?", 18)

// Dao 链式调用 — 复杂查询
dao := user.NewDao()
page, _ := dao.SqlById("user.listByAge", map[string]interface{}{
    "minAge":  18,
    "status":  1,
    "orderBy": "id desc",
}).Paginate(1, 10)
```
