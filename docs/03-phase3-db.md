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
├── sql_builder.go       # SQL 构建器 (编程式链式 API)
├── condition.go         # 条件构建
├── dialect.go           # Dialect 接口 + 实现
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
    ├── sql_directive.go     # #sql / #@ / #include 指令
    ├── condition.go         # Condition 类型
    ├── keys.go              # SQL 模板内部 key 常量
    └── operator.go          # Operator 枚举

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
    ├── model_generator.go   # ModelGenerator — 生成 user.go (存在则跳过)
    ├── dao_generator.go     # DaoGenerator — 生成 dao.go (存在则跳过)
    ├── tables_generator.go  # TablesGenerator — 生成 tables.go (覆盖写入)
    └── templates/           # Enjoy 模板文件 (.af)
        ├── _base.af             # 生成 BaseXxx + Table + getter/setter
        ├── _model.af            # 生成 Xxx struct
        ├── _dao.af              # 生成 Dao + 查询函数
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

Java 版 Enjoy SQL 指令及其 Go 实现状态：

| 指令 | 功能 | Go 实现 |
|------|------|---------|
| `#para(name)` | 命名参数占位 | `ParaDirective` — 注册为 `#para` / `#p` |
| `#para(int)` | 位置参数占位 | 同上 |
| `#where()` | 动态 WHERE 子句（自动处理 AND 前缀） | `WhereDirective` |
| `#and()` | 动态 AND 条件（自动处理 AND 前缀） | `AndDirective` |
| `#orderBy()` | 动态 ORDER BY 子句 | `OrderByDirective` |
| `#sql(id) ... #end` | 定义命名 SQL 片段 | `SqlDirective` |
| `#@id()` | 引用命名 SQL 片段 | `SqlDirective` |
| `#include(file)` | 包含外部 SQL 文件 | `SqlDirective` |

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
// Config 内嵌 SqlKit
type Config struct {
    // ...
    SqlKit *dbsql.SqlKit
}

// Db 层面的快捷方法
db.Sql("select * from user #where() #and(age > #para(age))", map[string]interface{}{"age": 18}).Find()
db.SqlWithArgs("select * from user where id = #para(0)", 123).FindFirst()
db.SqlById("user.list", map[string]interface{}{"status": 1}).Find()
db.AddSql("user.list", "select * from user #where() #and(status = #para(status))")
```

### 实现细节

SqlPara 持有渲染后的 SQL 和参数列表：

```go
type SqlPara struct {
    ID    string
    Sql   string        // 渲染后的 SQL
    Paras []interface{} // 参数列表（通过 #para 收集）
    Enjoy bool
}
```

SqlKit 缓存机制：`AddSql(sqlID, sql)` 时编译并缓存 SQL 模板，后续通过 `SqlById` 直接使用缓存模板渲染，避免重复解析。

---

## 3. SQLBuilder 编程式链式 API（`db/sql_builder.go`，已实现）

除了 SQL 模板引擎，还提供纯 Go 链式 API 构建动态 SQL：

```go
// 创建 SQLBuilder
b := db.NewSQL("select * from user")

// 动态条件
b.Where("age > ?", 18)
b.AndIf("name like ?", "%james%", name != "")
b.Or("status = ?", 1)

// 排序
b.OrderBy("id desc")

// 分页
b.Paginate(1, 10)

// 构建
sql, args := b.Build()
```

与 Enjoy SQL 模板的关系：两者互补。简单动态查询用 SQLBuilder，复杂多条件查询用 Enjoy SQL 模板。

---

## 4. Db 入口（已实现）

```go
package db

// 初始化
func Init(driverName, dsn string, opts ...ConfigOption) error
func InitWithID(configID, driverName, dsn string, opts ...ConfigOption) error

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
func Select(fields string) *Dao

// 快捷 CRUD
func Insert(row *Row) (*Row, error)
func InsertOrUpdate(row *Row) (*Row, error)
func Update(row *Row) (bool, error)
func Delete(row *Row) (bool, error)
func DeleteByID(table string, id interface{}) (bool, error)
func DeleteBy(table, whereOrField string, args ...interface{}) (int64, error)
func FindByID(table string, id interface{}) (*Row, error)
func FindBy(table, whereOrField string, args ...interface{}) ([]*Row, error)
func FindFirstBy(table, whereOrField string, args ...interface{}) (*Row, error)
func FindIn(table, field string, values ...interface{}) ([]*Row, error)
func Count(table string) (int64, error)
func CountBy(table, whereOrField string, args ...interface{}) (int64, error)

// 事务 / 批量
func Transaction(fn func() error) error
func NewBatch() *Batch
```

---

## 5. Dao（已实现）

```go
type Dao struct {
    config    *Config
    sqlStr    string
    sqlArgs   []interface{}
    sqlPara   *sql.SqlPara
    selFields string
}

// 链式入口
func (d *Dao) SQL(query string, args ...interface{}) *Dao
func (d *Dao) Sql(sqlStr string, data map[string]interface{}) *Dao
func (d *Dao) SqlWithArgs(sqlStr string, args ...interface{}) *Dao
func (d *Dao) SqlById(sqlID string, data map[string]interface{}) *Dao
func (d *Dao) Select(fields string) *Dao

// 链式终止 (查询)
func (d *Dao) Find() ([]*Row, error)
func (d *Dao) FindFirst() (*Row, error)
func (d *Dao) FindOne() (*Row, error)
func (d *Dao) Paginate(pageNum, pageSize int) (*Page, error)

// 链式终止 (更新/查询)
func (d *Dao) Exec() (sql.Result, error)
func (d *Dao) Update() (int64, error)
func (d *Dao) Query() (*sql.Rows, error)
func (d *Dao) QueryInt() (int, error)
func (d *Dao) QueryInt64() (int64, error)
func (d *Dao) QueryString() (string, error)

// Table CRUD
func (d *Dao) FindByID(table string, id interface{}) (*Row, error)
func (d *Dao) FindBy(table, whereOrField string, args ...interface{}) ([]*Row, error)
// ... 更多 Table 操作
```

---

## 6. Row（已实现）

```go
type Row struct {
    table       string
    primaryKeys []string
    data        map[string]interface{}
    change      map[string]struct{} // 追踪 Set 操作的字段
}

// 工厂方法
func NewRow(table string) *Row
func NewRowWithPK(table, primaryKey string) *Row

// Set 系列 (追踪 change)
func (r *Row) Set(field string, value interface{}) *Row
func (r *Row) SetMap(data map[string]interface{}) *Row
func (r *Row) SetIfNotNull(field string, value interface{}) *Row

// Put 系列 (不追踪 change)
func (r *Row) Put(field string, value interface{}) *Row

// 类型安全 Getter
func (r *Row) GetStr(field string) string
func (r *Row) GetInt(field string) int
func (r *Row) GetInt64(field string) int64
func (r *Row) GetFloat64(field string) float64
func (r *Row) GetBool(field string) bool
func (r *Row) GetTime(field string) time.Time
// ...

// Active Record
func (r *Row) ID(id interface{}) *Row
func (r *Row) Insert() (*Row, error)
func (r *Row) Update() (bool, error)
func (r *Row) Delete() (bool, error)
```

---

## 7. Page / Dialect / Batch / Transaction（已实现）

### Page

```go
type Page struct {
    PageNum    int    `json:"pageNum"`
    PageSize   int    `json:"pageSize"`
    TotalRows  int64  `json:"totalRows"`
    TotalPages int    `json:"totalPages"`
    Rows       []*Row `json:"rows"`
}
```

### Dialect

```go
// db.Dialect 接口只包含运行时 SQL 生成方法，不含任何 generator 相关方法。
// generator 需要的元数据查询方法定义在 generator/ 模块的 MetaDialect 接口中。
type Dialect interface {
    Name() string
    DefaultPrimaryKeys() []string
    ForPaginate(selectSQL, fromWhereSQL string, pageNum, pageSize int) string
    ForFindByID(table string, primaryKeys []string) string
    ForDeleteByID(table string, primaryKeys []string) string
    ForInsert(table string, fields []string) string
    ForUpdate(table string, fields []string, primaryKeys []string) string
    ForInsertOrUpdate(table string, fields []string, primaryKeys []string) string
}
```

### Batch / Transaction

```go
func Transaction(fn func() error) error
func (b *Batch) Insert(rows []*Row) (*BatchResult, error)
func (b *Batch) Update(rows []*Row) (*BatchResult, error)
func (b *Batch) Execute(sql string, argsList [][]interface{}) (*BatchResult, error)
```

---

## 8. Generator 代码生成器（核心新增，待实现）

### 8.1 设计动机

在 Java Aifei 中，Generator 是 `aifei-db` 的两个核心组件之一（另一个是 Enjoy SQL）。它通过读取数据库元数据自动生成 Model / BaseModel / Dao / ModelSet 四个层级的代码：

- **消除手写样板代码**：每个数据表需要手写大量 getter/setter/CRUD 方法，Generator 自动生成类型安全的代码
- **编译期类型检查**：生成的 `GetName() string` 远比 `GetStr("name")` 安全
- **Schema 同步**：数据库字段变更后重新生成 BaseModel 即可，Model 和 Dao 因已存在而不会被覆盖
- **Short Setter**：`user.Name("james").Age(28)` 链式调用风格，提升开发体验

### 8.2 生成策略：每表一个包

Java 按层级分 `base/`、`model/`、`dao/` 三个包，利用类名做命名空间（`User.findById`、`Order.findById`）。Go 的包名就是命名空间，如果照搬 Java 层级方案会导致 `base.FindUserById` 这种包名+函数名冗余。

Go 更自然的做法是**每表一个包**，同一张表的 `BaseXxx`、`Xxx`、`Dao` 都在同一个包内：

```
myapp/db/
├── tables.go              # 每次覆盖 — 所有表的 Table 元数据集合
│
├── user/                  # package user
│   ├── base.go            # 每次覆盖 — BaseUser + Table + getter/setter + 实例 CRUD
│   ├── user.go            # 存在则跳过 — User struct（用户扩展）
│   └── dao.go             # 存在则跳过 — Dao + FindById 等查询方法
│
├── order/                 # package order
│   ├── base.go
│   ├── order.go
│   └── dao.go
│
└── product/               # package product
    ├── base.go
    ├── product.go
    └── dao.go
```

**同一包内互引无需 import，彻底消除循环依赖的风险。** 命名简洁自然：

| Java 层级方案 | Go 每表一包 |
|---|---|
| `base.FindUserById(id)` | `user.FindById(id)` |
| `model.User` | `user.User` |
| `dao.NewUserDao()` | `user.NewDao()` |
| `base.NewBaseUser()` | `user.New()` |

### 8.3 架构

Generator 作为独立模块 `generator/`，与 `db/` 完全解耦。它 `import db` 但 `db` 不知道它的存在。

```
generator/               # 独立模块，import db，零外部依赖
├── Generator (主入口)
├── MetaReader           # 读取数据库元数据
│   ├── TypeMapping      # SQL 类型 → Go 类型映射
│   ├── FieldToAttr      # 字段名 → Go 导出字段名转换
│   └── GoKeyword        # Go 保留字检查
├── MetaDialect          # generator 专用方言接口（内嵌 db.Dialect + 元数据查询方法）
│
├── BaseGenerator        # 生成 base.go (覆盖写入)
├── ModelGenerator       # 生成 user.go (存在则跳过)
├── DaoGenerator         # 生成 dao.go (存在则跳过)
└── TablesGenerator      # 生成 tables.go (覆盖写入)
```

**MetaDialect 接口**（generator 内部定义，不污染 `db.Dialect`）：

```go
package generator

import "database/sql"

// MetaDialect 是 generator 专用的方言接口，在 db.Dialect 基础上增加元数据查询方法。
// 定义在 generator 包内，db 包完全不知道这个接口的存在。
type MetaDialect interface {
    db.Dialect
    // QueryTableNames 返回数据库中所有表名
    QueryTableNames(db *sql.DB) ([]string, error)
    // QueryTableInfo 返回用于读取表字段元数据的 SQL（如 SELECT * FROM table LIMIT 0）
    QueryTableInfo(table string) string
}
```

### 8.4 数据结构

```go
package generator

// FieldInfo 封装数据库表字段信息
type FieldInfo struct {
    Name            string // 数据库字段名 (snake_case)
    GoType          string // Go 类型，如 "int", "string", "time.Time"
    AttrName        string // Go 导出字段名 (CamelCase)
    Remarks         string // 字段注释
    IsAutoIncrement bool   // 是否自增
}

// TableInfo 封装数据库表元信息
type TableInfo struct {
    Name       string       // 表名 (snake_case)
    PrimaryKey []string     // 主键列名
    Remarks    string       // 表注释
    IsView     bool         // 是否为视图
    Fields     []*FieldInfo // 字段列表

    // 以下字段在生成阶段赋值
    PkgName      string // 包名，如 "user"
    StructName   string // 结构体名，如 "User"
    BaseName     string // 基础结构体名，如 "BaseUser"
}

// Table 运行时表元数据（生成到 base.go 文件中）
type Table struct {
    Name        string
    Fields      string
    PrimaryKeys []string
    FieldTypes  map[string]reflect.Type
}
```

### 8.5 MetaReader — 读取数据库元数据

MetaReader 通过 `database/sql` 标准库的 `DB.Query()` 和 `Rows.ColumnTypes()` 读取数据库表结构：

```go
package generator

type MetaReader struct {
    TypeMapping        *TypeMapping
    FieldToAttr        func(string) string  // snake_case → CamelCase
    ReadView           bool                 // 是否读取视图
    ReadRemarks        bool                 // 是否读取字段注释
    ReadAutoIncrement  bool                 // 是否读取自增属性
    whitelist          map[string]bool      // 白名单表
    blacklist          map[string]bool      // 黑名单表
    tableFilter        func(string) bool    // 自定义表过滤
    tableSkip          func(string) bool    // 自定义表跳过
}

func (mr *MetaReader) Read(pool *sql.DB, dialect MetaDialect) ([]*TableInfo, error) {
    // 1. 通过 dialect.QueryTableNames() 获取表名列表
    // 2. 对每张表执行 dialect.QueryTableInfo(table) 获取字段元数据的 SQL
    // 3. 执行查询并通过 rows.ColumnTypes() 获取字段名、类型信息
    // 4. 通过 TypeMapping 将 DB 类型映射为 Go 类型
    // 5. 通过 FieldToAttr 将字段名转为 Go 导出字段名
    // 6. 读取主键信息
    // 7. 应用 whitelist/blacklist/filter/skip 过滤
}
```

**ColumnTypes 方案**（推荐）：
```go
// Go 1.8+ sql.Rows.ColumnTypes() 返回 []*sql.ColumnType
// ColumnType 提供：
//   Name()         — 列名
//   DatabaseTypeName() — 数据库类型名 (如 "VARCHAR", "INT")
//   ScanType()     — Go 扫描类型 (如 string, int64)
//   Length()       — 长度
//   Nullable()     — 是否可空
//   DecimalSize()  — 精度/小数位
```

**主键读取方案**：
- MySQL: `SHOW KEYS FROM table WHERE Key_name = 'PRIMARY'`
- PostgreSQL: `SELECT a.attname FROM pg_index i JOIN pg_attribute a ...`
- SQLite: `PRAGMA table_info(table)` 中 `pk > 0` 的列
- 通用方案：通过 `database/sql` 驱动提供的 `information_schema` 查询

**表列表读取方案**：
```go
// MySQL
rows, _ := db.Query("SHOW TABLES")
// PostgreSQL
rows, _ := db.Query("SELECT tablename FROM pg_catalog.pg_tables WHERE schemaname = 'public'")
// SQLite
rows, _ := db.Query("SELECT name FROM sqlite_master WHERE type='table'")
```

### 8.6 TypeMapping — SQL 类型 → Go 类型映射

```go
package generator

type TypeMapping struct {
    mappings map[string]string // DB类型名 → Go类型名
}

func NewTypeMapping() *TypeMapping {
    return &TypeMapping{
        mappings: map[string]string{
            // 整数
            "INT":       "int",
            "INTEGER":   "int",
            "TINYINT":   "int",
            "SMALLINT":  "int",
            "MEDIUMINT": "int",
            "BIGINT":    "int64",
            "SERIAL":    "int64",
            "BIGSERIAL": "int64",

            // 浮点
            "FLOAT":   "float64",
            "DOUBLE":  "float64",
            "REAL":    "float64",
            "DECIMAL": "string",
            "NUMERIC": "string",

            // 字符串
            "VARCHAR":  "string",
            "CHAR":     "string",
            "TEXT":     "string",
            "LONGTEXT": "string",
            "MEDIUMTEXT": "string",
            "TINYTEXT": "string",
            "ENUM":     "string",
            "SET":      "string",

            // 时间
            "DATE":       "time.Time",
            "DATETIME":   "time.Time",
            "TIMESTAMP":  "time.Time",
            "TIME":       "time.Time",

            // 布尔
            "BOOL":    "bool",
            "BOOLEAN": "bool",
            "BIT":     "bool",

            // 二进制
            "BLOB":       "[]byte",
            "LONGBLOB":   "[]byte",
            "MEDIUMBLOB": "[]byte",
            "TINYBLOB":   "[]byte",
            "VARBINARY":  "[]byte",
            "BINARY":     "[]byte",

            // JSON
            "JSON":  "string",
            "JSONB": "string",
        },
    }
}

func (tm *TypeMapping) GetType(dbType string) string
func (tm *TypeMapping) AddMapping(dbType, goType string)
func (tm *TypeMapping) RemoveMapping(dbType string)
```

**可空字段处理**：当检测到字段可为 NULL 时，自动使用 `sql.NullString`、`sql.NullInt64`、`sql.NullTime` 等包装类型，或提供指针类型（如 `*string`、`*int`）。

### 8.7 FieldToAttr — 字段名转换

```go
package generator

// FieldToAttr 将数据库字段名转为 Go 可导出字段名
// 默认实现：snake_case → CamelCase
// 用户可通过 Generator 定制

func DefaultFieldToAttr(fieldName string) string {
    // user_id    → UserID
    // created_at → CreatedAt
    // name       → Name
    // 对于全大写的 Oracle 字段，先转小写再 camelCase
    return strcase.ToCamel(fieldName)
}
```

### 8.8 GoKeyword — Go 保留字检查

```go
package generator

var GoKeywords = map[string]bool{
    "break": true, "default": true, "func": true, "interface": true, "select": true,
    "case": true, "defer": true, "go": true, "map": true, "struct": true,
    "chan": true, "else": true, "goto": true, "package": true, "switch": true,
    "const": true, "fallthrough": true, "if": true, "range": true, "type": true,
    "continue": true, "for": true, "import": true, "return": true, "var": true,
}

// 如果字段名是 Go 保留字，则添加后缀下划线
// 例如：type → type_, select → select_
```

### 8.9 BaseGenerator — 生成 base.go（每次覆盖）

每表一个包，base.go 是包的核心文件。以 `user` 表（字段 `id int`、`name string`、`age int`、`created_at time.Time`）为例：

```go
// Code generated by Aifei Generator. DO NOT EDIT.

package user

import (
    "reflect"
    "time"

    "github.com/crazy-airhead/aifei-go/db"
)

// Table 是 user 表的运行时元数据。
var Table = &db.Table{
    Name:        "user",
    Fields:      "id,name,age,created_at",
    PrimaryKeys: []string{"id"},
    FieldTypes: map[string]reflect.Type{
        "id":         reflect.TypeOf(int(0)),
        "name":       reflect.TypeOf(""),
        "age":        reflect.TypeOf(int(0)),
        "created_at": reflect.TypeOf(time.Time{}),
    },
}

// BaseUser 是 user 表的 BaseRow，内嵌 *db.Row。
// 重新生成时会覆盖此文件，请勿手动修改。
type BaseUser struct {
    *db.Row
}

// New 创建 BaseUser。同包内无需 user.NewUser() 那样冗余。
func New() *BaseUser {
    return &BaseUser{Row: db.NewRow(Table.Name)}
}

// NewWithRow 用已有的 *db.Row 创建 BaseUser。
func NewWithRow(row *db.Row) *BaseUser {
    return &BaseUser{Row: row}
}

// ==================== 类型安全的 Getters ====================

func (u *BaseUser) Id() int              { return u.GetInt("id") }
func (u *BaseUser) Name() string         { return u.GetStr("name") }
func (u *BaseUser) Age() int             { return u.GetInt("age") }
func (u *BaseUser) CreatedAt() time.Time { return u.GetTime("created_at") }

// ==================== 类型安全的 Setters（链式调用） ====================

func (u *BaseUser) SetId(v int) *BaseUser           { u.Set("id", v); return u }
func (u *BaseUser) SetName(v string) *BaseUser       { u.Set("name", v); return u }
func (u *BaseUser) SetAge(v int) *BaseUser           { u.Set("age", v); return u }
func (u *BaseUser) SetCreatedAt(v time.Time) *BaseUser { u.Set("created_at", v); return u }

// ==================== Short Setters ====================

func (u *BaseUser) Id_(v int) *BaseUser        { return u.SetId(v) }
func (u *BaseUser) Name_(v string) *BaseUser   { return u.SetName(v) }
func (u *BaseUser) Age_(v int) *BaseUser       { return u.SetAge(v) }
func (u *BaseUser) CreatedAt_(v time.Time) *BaseUser { return u.SetCreatedAt(v) }

// ==================== CRUD 实例方法 ====================

func (u *BaseUser) Insert() (*BaseUser, error) { _, err := u.Row.Insert(); return u, err }
func (u *BaseUser) Update() (bool, error)      { return u.Row.Update() }
func (u *BaseUser) Delete() (bool, error)      { return u.Row.Delete() }
```

**包级查询函数放在 dao.go 中（由 DaoGenerator 生成），保持 base.go 的职责单一。**

### 8.10 ModelGenerator — 生成 user.go（存在则跳过）

同包内嵌入，无需跨包 import：

```go
// Code generated by Aifei Generator.

package user

// User 表示 user 表的数据模型。
// 重新生成不会覆盖此文件，可在此添加业务逻辑。
type User struct {
    *BaseUser
}
```

### 8.11 DaoGenerator — 生成 dao.go（存在则跳过）

Dao 也放在同一个包内。同包引用 Table、BaseUser 都无需 import：

```go
// Code generated by Aifei Generator.

package user

import "github.com/crazy-airhead/aifei-go/db"

// Dao 提供 user 表的类型安全查询方法。
// 重新生成不会覆盖此文件，可在此添加缓存、自定义查询等逻辑。
type Dao struct {
    *db.Dao
}

// NewDao 创建 Dao。
func NewDao() *Dao {
    return &Dao{Dao: db.Use()}
}

// FindById 通过主键查询。
func FindById(id int) (*User, error) {
    row, err := db.FindByID(Table.Name, id)
    if err != nil {
        return nil, err
    }
    return &User{BaseUser: NewWithRow(row)}, nil
}

// FindBy 通过条件查询。
func FindBy(whereOrField string, args ...interface{}) ([]*User, error) {
    rows, err := db.FindBy(Table.Name, whereOrField, args...)
    if err != nil {
        return nil, err
    }
    result := make([]*User, len(rows))
    for i, row := range rows {
        result[i] = &User{BaseUser: NewWithRow(row)}
    }
    return result, nil
}

// FindFirstBy 查询第一条记录。
func FindFirstBy(whereOrField string, args ...interface{}) (*User, error) {
    row, err := db.FindFirstBy(Table.Name, whereOrField, args...)
    if err != nil {
        return nil, err
    }
    return &User{BaseUser: NewWithRow(row)}, nil
}

// DeleteById 通过主键删除。
func DeleteById(id int) (bool, error) {
    return db.DeleteByID(Table.Name, id)
}

// DeleteBy 通过条件删除。
func DeleteBy(whereOrField string, args ...interface{}) (int64, error) {
    return db.DeleteBy(Table.Name, whereOrField, args...)
}

// Count 统计数量。
func Count() (int64, error) {
    return db.Count(Table.Name)
}

// CountBy 按条件统计。
func CountBy(whereOrField string, args ...interface{}) (int64, error) {
    return db.CountBy(Table.Name, whereOrField, args...)
}

// ---- Dao 实例方法（链式调用） ----

func (d *Dao) FindById(id int) (*User, error) {
    row, err := d.Dao.FindByID(Table.Name, id)
    if err != nil {
        return nil, err
    }
    return &User{BaseUser: NewWithRow(row)}, nil
}

func (d *Dao) FindBy(whereOrField string, args ...interface{}) ([]*User, error) {
    rows, err := d.Dao.FindBy(Table.Name, whereOrField, args...)
    if err != nil {
        return nil, err
    }
    result := make([]*User, len(rows))
    for i, row := range rows {
        result[i] = &User{BaseUser: NewWithRow(row)}
    }
    return result, nil
}
```

#### 使用对比

```go
// 简单查询 — 包级函数
u := user.FindById(123)
list := user.FindBy("age > ?", 18)

// 链式查询 — Dao 实例
dao := user.NewDao().Select("name, age")
list, _ := dao.Where("status = ?", 1).OrderBy("id desc").Find()

// Enjoy SQL 模板
list := user.NewDao().SqlById("user.listByAge", data).Find()
```

### 8.12 TablesGenerator — 生成 tables.go（每次覆盖）

Go 没有 DI 容器自动扫描 Model 类，但跨表遍历仍然有用（schema 迁移、admin 面板、批量操作等）。`tables.go` 持有所有表的 `*db.Table` 句柄，按需排列在 `Tables` 切片中供遍历：

```go
// Code generated by Aifei Generator. DO NOT EDIT.

package db

import (
    "myapp/db/user"
    "myapp/db/order"
    "myapp/db/product"

    aifeidb "github.com/crazy-airhead/aifei-go/db"
)

// Tables 包含所有生成的表元数据，用于跨表操作（schema 迁移、admin 面板等）。
var Tables = []*aifeidb.Table{
    user.Table,
    order.Table,
    product.Table,
}
```

**依赖方向：** `tables.go` 所在的包 import `user/`、`order/` 等子包，而子包只 import 框架的 `db`，不会 reverse import 父包，无循环。

**与 Java ModelSet 的对比：**

| Java | Go |
|------|-----|
| `LinkedHashSet<Class<? extends AifeiRow>>` | `[]*aifeidb.Table` |
| 需要反射 `Class.forName()` | 直接编译期引用 |
| 用于 DI 扫描 + 运行时注册 | 只需要跨表遍历，不需要 DI |

### 8.13 Generator — 主入口

每个表一个包，外加一个汇总的 `tables.go`：

```go
package generator

type Generator struct {
    pool       *sql.DB
    dialect    MetaDialect
    outputDir  string // 生成文件输出根目录，如 "./myapp/db"

    metaReader       *MetaReader
    baseGenerator    *BaseGenerator
    modelGenerator   *ModelGenerator
    daoGenerator     *DaoGenerator
    tablesGenerator  *TablesGenerator

    // 命名函数（可定制）
    PkgNameFunc    func(tableName string) string // 默认 snake_case，如 "user_profile" → "user_profile"
    StructNameFunc func(tableName string) string // 默认 snake→PascalCase，如 "user_profile" → "UserProfile"
    BaseNameFunc   func(structName string) string // 默认 "Base" + structName
}

func New(pool *sql.DB, dialect MetaDialect, outputDir string) *Generator

func (g *Generator) ConfigMetaReader(fn func(*MetaReader)) *Generator
func (g *Generator) ConfigBaseGenerator(fn func(*BaseGenerator)) *Generator

// Generate 执行生成
func (g *Generator) Generate() error {
    // 1. metaReader.Read() 读取表信息
    // 2. 为每张表计算 pkgName / structName / baseName
    // 3. 对每张表：
    //    a. 创建 outputDir/user/ 目录
    //    b. baseGenerator.Generate()  → 覆盖写入 base.go
    //    c. modelGenerator.Generate() → 存在则跳过 user.go
    //    d. daoGenerator.Generate()   → 存在则跳过 dao.go
    // 4. tablesGenerator.Generate() → 覆盖写入 outputDir/tables.go
}
```

### 8.14 使用示例

```go
package main

import (
    "database/sql"
    _ "github.com/mattn/go-sqlite3"

    "github.com/crazy-airhead/aifei-go/db"
    "github.com/crazy-airhead/aifei-go/generator"
)

func main() {
    // 1. 初始化数据库连接
    db.Init("sqlite3", "app.db")
    pool, _ := db.GetConfig().Pool()

    // 2. 创建 generator 专属方言
    dialect := &generator.SQLiteMetaDialect{SQLiteDialect: db.GetConfig().Dialect}

    // 3. 创建生成器（只需指定输出目录）
    gen := generator.New(pool, dialect, "./myapp/db")

    // 4. 自定义配置（可选）
    gen.ConfigMetaReader(func(mr *generator.MetaReader) {
        mr.AddBlacklist("migrations", "schema_version")
    }).ConfigBaseGenerator(func(bg *generator.BaseGenerator) {
        bg.GenerateShortSetter = false
    })

    // 5. 执行生成 → 每个表一个包：user/、order/、product/
    if err := gen.Generate(); err != nil {
        panic(err)
    }
}
```

### 8.15 Go vs Java 生成差异对比

| 方面 | Java 版 | Go 版 |
|------|---------|-------|
| 包结构 | `base/` `model/` `dao/` 三层 | 每表一包 `user/` `order/` + 汇总 `tables.go` |
| 模型注册 | `ModelSet` — `LinkedHashSet<Class>` 用于 DI 扫描 | `Tables` — `[]*db.Table` 用于跨表遍历 |
| 继承方式 | `BaseUser<M> extends AifeiRow<M>` + CRTP | `BaseUser` 内嵌 `*db.Row` |
| 模型类 | `User extends BaseUser<User>` | 同包内 `User` 嵌入 `*BaseUser` |
| 查询方法 | `User.findById(123)` | `user.FindById(123)` — 包名即命名空间 |
| Dao 实例 | `new UserDao()` | `user.NewDao()` — 无冗余命名 |
| 链式 setter | `return (M)this` | `return u`（接收者本身就是指针）|
| 模板引擎 | Enjoy 模板 (.af) | `text/template` + `embed` |
| Short Setter | `user.name("james")` | `user.New().Name_("james")` |

### 8.16 模板引擎选择

Go 版 Generator **使用 Enjoy 模板引擎**生成代码，与 Java 版一致。`generator/` 是独立模块，可以 `import enjoy`—— Enjoy 本身零外部依赖，不存在循环引用。

```go
package generator

import (
    _ "embed"
    "github.com/crazy-airhead/aifei-go/enjoy"
)

// 模板文件通过 embed 内嵌
var (
    //go:embed templates/_base.af
    baseTemplate string
    //go:embed templates/_model.af
    modelTemplate string
    //go:embed templates/_dao.af
    daoTemplate string
    //go:embed templates/_tables.af
    tablesTemplate string
)

// Generator 初始化时创建 Enjoy Engine 并加载模板
func New(pool *sql.DB, dialect MetaDialect, outputDir string) *Generator {
    engine := enjoy.NewEngine("generator")
    engine.SetStaticFieldExpression(true).
        SetStaticMethodExpression(true).
        AddSharedMethod(&TemplateUtil{})  // 共享工具方法

    return &Generator{
        // ...
        engine: engine,
    }
}
```

### 8.17 Enjoy 代码生成模板

与 Java 版 `_base_model_template.af` / `_model_template.af` / `_dao_template.af` / `_model_set_template.af` 对应，Go 版使用相同风格的四组 Enjoy 模板。

#### 8.17.1 `templates/_base.af` — 生成 base.go

这是最核心的模板，为每张表生成 `BaseXxx` 结构体、`Table` 常量、类型安全的 getter/setter/short setter：

```enjoy
### 参考 Java 版 _base_model_template.af 的 Enjoy 模板
package #(pkgName)

import (
    #for (imp : imports)
    "#(imp)"
    #end

    "github.com/crazy-airhead/aifei-go/db"
)

### TABLE 常量
var Table = &db.Table{
    Name:        "#(tableInfo.Name)",
    Fields:      "#(fields)",
    PrimaryKeys: []string{#for (i, pk : tableInfo.PrimaryKey)#if (i > 0), #end"#(pk)"#end},
    FieldTypes: map[string]reflect.Type{
        #for (field : tableInfo.Fields)
        "#(field.Name)": reflect.TypeOf(#(field.ZeroValue)),
        #end
    },
}

### BaseXxx 结构体
type #(baseName) struct {
    *db.Row
}

func New() *#(baseName) {
    return &#(baseName){Row: db.NewRow(Table.Name)}
}

func NewWithRow(row *db.Row) *#(baseName) {
    return &#(baseName){Row: row}
}

### 类型安全的 Getters
#for (field : tableInfo.Fields)
func (r *#(baseName)) #(field.GetterName)() #(field.GoType) {
    return r.#(field.RowGetter)("#(field.Name)")
}
#end

### 类型安全的 Setters（链式调用）
#for (field : tableInfo.Fields)
func (r *#(baseName)) Set#(field.GetterName)(v #(field.GoType)) *#(baseName) {
    r.Set("#(field.Name)", v)
    return r
}
#end

### Short Setters（#if (generateShortSetter)）
#for (field : tableInfo.Fields)
func (r *#(baseName)) #(field.AttrName)_(v #(field.GoType)) *#(baseName) {
    return r.Set#(field.GetterName)(v)
}
#end

### CRUD 实例方法
func (r *#(baseName)) Insert() (*#(baseName), error) {
    _, err := r.Row.Insert()
    return r, err
}

func (r *#(baseName)) Update() (bool, error) {
    return r.Row.Update()
}

func (r *#(baseName)) Delete() (bool, error) {
    return r.Row.Delete()
}
```

#### 8.17.2 `templates/_model.af` — 生成 user.go

最简模板，仅在同包内嵌入 BaseXxx（存在则跳过）：

```enjoy
### 参考 Java 版 _model_template.af
package #(pkgName)

### Model 结构体 — 重新生成不会覆盖此文件，可添加业务逻辑
type #(structName) struct {
    *#(baseName)
}
```

#### 8.17.3 `templates/_dao.af` — 生成 dao.go

生成类型安全的 Dao，包含包级查询函数和 Dao 实例方法（存在则跳过）：

```enjoy
### 参考 Java 版 _dao_template.af
package #(pkgName)

import "github.com/crazy-airhead/aifei-go/db"

### Dao 结构体 — 重新生成不会覆盖此文件
type Dao struct {
    *db.Dao
}

func NewDao() *Dao {
    return &Dao{Dao: db.Use()}
}

### 包级查询函数
#if (len(tableInfo.PrimaryKey) == 1)
func FindById(id #(pkType)) (*#(structName), error) {
    row, err := db.FindByID(Table.Name, id)
    if err != nil {
        return nil, err
    }
    return &#(structName){#(baseName): NewWithRow(row)}, nil
}
#else if (len(tableInfo.PrimaryKey) == 2)
func FindByCompositeId(key1, key2 string, id1, id2 interface{}) (*#(structName), error) {
    row, err := db.FindByCompositeId(Table.Name, key1, key2, id1, id2)
    // ...
}
#end

func FindBy(whereOrField string, args ...interface{}) ([]*#(structName), error) { ... }
func FindFirstBy(whereOrField string, args ...interface{}) (*#(structName), error) { ... }
func DeleteById(id #(pkType)) (bool, error) { ... }
func DeleteBy(whereOrField string, args ...interface{}) (int64, error) { ... }
func Count() (int64, error) { ... }
func CountBy(whereOrField string, args ...interface{}) (int64, error) { ... }

### Dao 实例方法（链式查询）
func (d *Dao) FindById(id #(pkType)) (*#(structName), error) { ... }
func (d *Dao) FindBy(whereOrField string, args ...interface{}) ([]*#(structName), error) { ... }
```

#### 8.17.4 `templates/_tables.af` — 生成 tables.go

汇集所有表的 `*db.Table`（每次覆盖）：

```enjoy
### 参考 Java 版 _model_set_template.af
package #(outputPkgName)

import (
    #for (tableInfo : allTables)
    "#(importRoot)/#(tableInfo.PkgName)"
    #end

    aifeidb "github.com/crazy-airhead/aifei-go/db"
)

### Tables 包含所有生成的表元数据
var Tables = []*aifeidb.Table{
    #for (tableInfo : allTables)
    #(tableInfo.PkgName).Table,
    #end
}
```

#### 8.17.5 `TemplateUtil` — 模板辅助方法

与 Java 的 `BaseModelGeneratorUtil` 对应，将模板中不便书写的逻辑（类型映射、命名转换等）抽取为 Enjoy 共享方法：

```go
package generator

// TemplateUtil 提供 Enjoy 模板中使用的辅助方法。
// 通过 engine.AddSharedMethod(&TemplateUtil{}) 注册为共享方法。
type TemplateUtil struct{}

// RowGetter 返回 Row 上对应 Go 类型的 getter 方法名。
// 例如 "int" → "GetInt", "string" → "GetStr", "time.Time" → "GetTime"
func (tu *TemplateUtil) RowGetter(goType string) string {
    switch goType {
    case "int":       return "GetInt"
    case "int64":     return "GetInt64"
    case "float64":   return "GetFloat64"
    case "string":    return "GetStr"
    case "bool":      return "GetBool"
    case "time.Time": return "GetTime"
    case "[]byte":    return "GetBytes"
    default:          return "Get"
    }
}

// ZeroValue 返回 Go 类型的零值表达式，用于 reflect.TypeOf。
func (tu *TemplateUtil) ZeroValue(goType string) string {
    switch goType {
    case "int", "int64":     return "int(0)"
    case "float64":          return "float64(0)"
    case "string":           return "\"\""
    case "bool":             return "false"
    case "time.Time":        return "time.Time{}"
    case "[]byte":           return "[]byte{}"
    default:                 return "nil"
    }
}

// ImportPath 返回 Go 类型对应的 import 路径（仅 time.Time 需要）。
func (tu *TemplateUtil) ImportPath(goType string) string {
    if goType == "time.Time" { return "time" }
    return ""
}
```

---

## 9. 完整使用示例对比

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

// 查询 — 包名即命名空间，无冗余
u, _ := user.FindById(123)
fmt.Println(u.Name()) // 类型安全，IDE 自动补全

list, _ := user.FindBy("age > ?", 18)

// 更新
u, _ := user.FindById(123)
u.SetName("james zhan").Update()

// 删除
user.DeleteById(123)

// 统计
count, _ := user.CountBy("status = ?", 1)
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

// Dao + Select + 条件
list, _ := user.NewDao().Select("name, age").Where("status = ?", 1).Find()
```
