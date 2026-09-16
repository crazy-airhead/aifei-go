# Aifei-Go 代码生成器：从数据库 schema 到类型安全的 ORM 与 Service

> **一份 schema，五个产物，零样板。**`tools/generator` 读取数据库元数据（MySQL/PostgreSQL/SQLite），通过嵌入式 Enjoy 模板批量产出 `base.go` / `model.go` / `dao.go` / `service.go` / `init.go`，把 `db.Row` 包装成强类型的 Active Record 与开箱即用的 HTTP Service。

---

## 1. 背景与定位

Aifei-Go 的数据访问层 [db](db.md) 提供 `Row`（Active Record）与 `Dao`（链式查询）两套 API，对单表 CRUD 几乎不需要手写 SQL。但每个业务表仍要做一堆重复劳动：

- 为每列写类型安全的 getter/setter（`r.GetInt("user_id")` 写到处都是）
- 重复写 `FindByID` / `FindBy` / `DeleteByID` 等同构方法
- 在 HTTP 服务里把 `in.GetBean(...)`、`FindById(...)`、`server.Of(...)` 来回拼装
- 维护 `db.Table` 元数据，保证 `db.RegisterTable` 能被框架发现

`tools/generator` 就是把这些样板代码**生成出来**的工具：一次 `Generate()` 调用，按表产出五个 Go 文件，并自动注册到 [db](db.md) 与 [server](server.md)。它是 Java Aifei `jfinal-generator` 的 Go 对应物，核心思路一致（schema → 代码、生成 typed Dao），只是模板引擎换成了项目自带的 Enjoy。

依赖范围克制到最小：

| 依赖 | 用途 |
|------|------|
| `github.com/crazy-airhead/aifei-go/db` | 复用 `db.Dialect` / `db.KeyFormat` / `db.Table` 定义，不重复发明 |
| `github.com/crazy-airhead/aifei-go/enjoy` | 模板引擎，渲染 `.af` 文件 |
| Go 标准库 `database/sql` | 元数据读取的统一接口 |
| Go 标准库 `go/embed` | 把 `.af` 模板编译进二进制 |

用户需自行提供驱动（如 `modernc.org/sqlite` 或 `go-sql-driver/mysql`），generator 自身不绑任何驱动。

---

## 2. 总体架构

```mermaid
flowchart TD
    GEN["Generator.Generate()<br/>（入口：generator.go）"] --> P1["1. 元数据读取<br/>MetaReader.Read ↓<br/>MySQL/PG/SQLite MetaDialect"]
    GEN --> P2["2. 命名派生<br/>PkgName / StructName / BaseName<br/>+ Names（names.go：标识符参数化）"]
    GEN --> P3["3. 逐表生成<br/>base → model → dao → service"]
    GEN --> P4["4. init.go<br/>汇总空白导入"]
    P1 --> ENJ["Enjoy Engine（共享 u: TemplateUtil）<br/>渲染 5 个 .af 模板"]
    P2 --> ENJ
    P3 --> ENJ
    P4 --> ENJ
    ENJ --> OUT["outputDir/&lt;pkg&gt;/base.go · model.go · dao.go · service.go<br/>outputDir/init.go（空白 import 触发各 base/service 的 init()）"]
```

核心抽象（关键类型一览）：

| 类型 | 文件 | 职责 |
|------|------|------|
| `Generator` | `generator.go` | 入口；持有所有子 generator 与 Engine |
| `Engine` | `generator.go` | 享元式 Enjoy 引擎；模板编译结果按内容缓存 |
| `MetaReader` | `meta_reader.go` | 读库元数据，产出 `[]*TableInfo` |
| `MetaDialect` | `meta_dialect.go` | 在 `db.Dialect` 之上加元数据查询能力 |
| `TypeMapping` | `type_mapping.go` | SQL 类型 → Go 类型（30+ 映射，可覆盖） |
| `TableInfo` / `FieldInfo` | `types.go` | 生成过程的中间数据模型 |
| `buildNames`（`Names`） | `names.go` | 标识符参数化：一表一包用裸名，多表一包用表限定名（见 §7.1） |
| `TemplateUtil` | `template_util.go` | 模板里可调用的 `u.PkgName(...)` 等辅助方法 |
| `*Generator`（5 个） | `*_generator.go` | 每个负责一种产物文件 |

---

## 3. 关键 API

### 3.1 入口：`generator.New`

```go
func New(pool *sql.DB, dialect MetaDialect, outputDir, importRoot string) *Generator
```

四个参数：

| 参数 | 含义 | 示例 |
|------|------|------|
| `pool` | 已连上的 `*sql.DB`，元数据读取直接用它 | `pool, _ := db.GetConfig().Pool()` |
| `dialect` | 元数据方言（`MySQLMetaDialect` / `PostgresMetaDialect` / `SQLiteMetaDialect`） | `&generator.SQLiteMetaDialect{}` |
| `outputDir` | 生成代码的根目录（每张表会在此目录下建子目录） | `./internal` |
| `importRoot` | `outputDir` 对应的 Go import 路径，用于 `init.go` 的空白导入 | `github.com/x/y/internal` |

`Generator` 暴露的可定制字段与方法：

```go
type Generator struct {
    // 字段
    TablePrefix    string                                  // 表名前缀，生成前剥离（如 "sys_"）
    Qualified      bool                                    // 多表一包：包级标识符带表名（见 §7.1）
    Force          bool                                    // 强制覆盖 model/dao/service（默认已存在则跳过）
    PkgNameFunc    func(string) string                     // 表名 → 包名
    StructNameFunc func(string) string                     // 表名 → 结构体名
    BaseNameFunc   func(string) string                     // 结构体名 → base 结构体名

    // 链式配置（深入子 generator）
    func (g *Generator) ConfigMetaReader(fn func(*MetaReader)) *Generator
    func (g *Generator) ConfigBaseGenerator(fn func(*BaseGenerator)) *Generator
    func (g *Generator) ConfigModelGenerator(fn func(*ModelGenerator)) *Generator
    func (g *Generator) ConfigDaoGenerator(fn func(*DaoGenerator)) *Generator
    func (g *Generator) ConfigServiceGenerator(fn func(*ServiceGenerator)) *Generator

    // 执行
    func (g *Generator) Generate() error
}
```

`Qualified` 与 `Force` 两个字段控制的是两个正交的维度：

| 字段 | 解决的问题 | 代价 |
|------|-----------|------|
| `Qualified` | 多张表共享一个包时裸名冲突（两个 `Table`、两个 `NewDao`） | 包级标识符全部带表名（`TableUser` / `NewUserDao` / `UserFindById`），单表一包时略显冗长 |
| `Force` | 模板升级后想整体刷新存量 `model.go` / `dao.go` / `service.go` | 手写定制会被覆盖，属于一次性破坏动作 |

两者默认关闭——不设置时生成产物与历史行为完全一致。

### 3.2 最小可用示例

```go
package main

import (
    "github.com/crazy-airhead/aifei-go/db"
    "github.com/crazy-airhead/aifei-go/tools/generator"
    _ "modernc.org/sqlite"
)

func main() {
    _ = db.Init("sqlite", "./app.db")
    pool, _ := db.GetConfig().Pool()

    gen := generator.New(
        pool,
        &generator.SQLiteMetaDialect{},
        "./internal",
        "github.com/me/app/internal",
    )
    gen.TablePrefix = "sys_"          // sys_user → user，sys_login_log → login_log
    gen.ConfigServiceGenerator(func(s *generator.ServiceGenerator) {
        s.APIPrefix = "/api/v1"        // 路由前缀
    })
    gen.ConfigMetaReader(func(mr *generator.MetaReader) {
        mr.AddBlacklist("sys_log")     // 跳过日志表
    })

    if err := gen.Generate(); err != nil { panic(err) }
}
```

执行后产物结构（假设库中有 `user` 与 `sys_login_log` 两表）：

```
internal/
├── init.go              # import _ "./user"  ./loginlog" 触发自注册
├── user/
│   ├── base.go            # 总是覆盖
│   ├── model.go           # 已存在则跳过
│   ├── dao.go             # 已存在则跳过
│   └── service.go         # 已存在则跳过
└── loginlog/
    ├── base.go
    ├── model.go
    ├── dao.go
    └── service.go
```

---

## 4. 元数据读取：MetaReader

`MetaReader` 是生成质量的基础——它读得越准，生成代码越贴近真实 schema。

### 4.1 核心字段

```go
type MetaReader struct {
    TypeMapping       *TypeMapping          // SQL → Go 类型表
    FieldToAttrFn     func(string) string   // 列名 → Go 字段名（默认 snake → Pascal）
    ReadView          bool                  // 是否处理视图（视图无 PK 时塞 fake_id）
    ReadRemarks       bool                  // 是否读取表/列注释
    ReadAutoIncrement bool                  // 是否推断自增标记
    ResolveNullable   bool                  // NULL 列是否映射成 sql.Null*
    KeyFormat         db.KeyFormat          // json tag 的命名风格
    // ... 内部：whitelist / blacklist / filter / skip
}
```

默认配置（`NewMetaReader`）：

| 字段 | 默认值 | 含义 |
|------|--------|------|
| `TypeMapping` | `NewTypeMapping()` | 30+ SQL→Go 映射 |
| `FieldToAttrFn` | `FieldToAttr` | snake_case → PascalCase |
| `ReadRemarks` | `true` | 读注释 |
| `KeyFormat` | `db.KeyFormatCamel` | json tag 为 camelCase，与 `db.DefaultKeyFormat` 对齐 |

### 4.2 表过滤：四层优先级

```go
mr.AddWhitelist("user", "order")   // 只生成这几张表
mr.AddBlacklist("sys_log")          // 排除某几张表
mr.SetFilter(func(t string) bool { return !strings.HasPrefix(t, "tmp_") })
mr.SetSkip(func(t string) bool { return strings.HasPrefix(t, "bak_") })
```

判定顺序（`shouldProcess`）：

```mermaid
flowchart TD
    W{"whitelist 非空？"} -->|"是"| WL["只取白名单"]
    W -->|"否则"| F{"filter 已设？"}
    F -->|"是"| FT["走 filter"]
    F -->|"否则"| B{"blacklist 命中？"}
    B -->|"是"| SKIP["跳过"]
    B -->|"否"| S{"skip 命中？"}
    S -->|"是"| SKIP
    S -->|"否"| DEF["默认处理"]
```

### 4.3 NULL 处理与 KeyFormat

- **默认 `ResolveNullable=false`**：NULL 列按普通 Go 类型生成（如 `string`）。运行时由 [db](db.md) 的 `Row` getter 处理 NULL（返回零值）。这是与 Java 版一致的体验。
- **`ResolveNullable=true`**：生成 `sql.NullInt64` / `sql.NullString` / `sql.NullTime` 等，类型层面区分 NULL 与零值。
- **`KeyFormat=db.KeyFormatSnake`**：json tag 直接用列名（`"user_id"`），而非默认的 camelCase（`"userId"`）。与运行时 `db.DefaultKeyFormat` 必须保持一致，否则生成代码的 json tag 与 `Row` 序列化字段对不上。

### 4.4 双路读列：information_schema vs 驱动反射

`MetaReader.readFieldInfo` 会按方言能力分流：

```go
if cr, ok := dialect.(ColumnMetaReader); ok {
    return mr.readFieldsFromMeta(...)   // MySQL/PG：information_schema
}
return mr.readFieldsFromDriver(...)      // SQLite 等回退：rows.ColumnTypes()
```

| 路径 | 方言 | 能拿到 | 不能拿到 |
|------|------|--------|----------|
| `ColumnMetaReader.ReadColumns` | MySQL、PostgreSQL | 注释、`NULL` 标记、自增、**生成列**（VIRTUAL/STORED） | — |
| `readFieldsFromDriver` | SQLite、其他 | 类型、可空（来自驱动） | 注释、生成列标记 |

对生成列的处理尤其关键——MySQL 的 `DEFAULT CURRENT_TIMESTAMP`（`DEFAULT_GENERATED`）**不算**生成列（生成器会专门排除它，确保 `gmt_create` 这种字段仍能进 INSERT/UPDATE），只有 `VIRTUAL GENERATED` / `STORED GENERATED` 才被列入 `Table.GeneratedColumns`，从而在 INSERT/UPDATE 时跳过。

---

## 5. 方言层：MetaDialect

`MetaDialect` 在 [db](db.md) 的 `Dialect`（SQL 方言）之上，加了两个生成器专用的方法：

```go
type MetaDialect interface {
    db.Dialect
    QueryTableNames(pool *sql.DB) ([]string, error)
    QueryTableInfo(table string) string
}
```

两个**可选能力接口**（能力检测，按需组合）：

```go
type ColumnMetaReader interface {
    ReadColumns(pool *sql.DB, table string) ([]ColumnMeta, error)
}
type TableMetaReader interface {
    ReadTableRemarks(pool *sql.DB) (map[string]string, error)
}
```

三个内置实现：

| 方言 | 表名来源 | 列元数据来源 | 注释 | 生成列 |
|------|----------|--------------|------|--------|
| `MySQLMetaDialect` | `SHOW TABLES` | `INFORMATION_SCHEMA.COLUMNS`（含 `COLUMN_COMMENT`、`EXTRA`） | ✅ | ✅（VIRTUAL/STORED） |
| `PostgresMetaDialect` | `pg_catalog.pg_tables` | `information_schema.columns` + `col_description` + `identity_generation` | ✅ | ✅（`is_generated='ALWAYS'`） |
| `SQLiteMetaDialect` | `sqlite_master` | `rows.ColumnTypes()`（驱动反射） | ❌ | ❌ |

> 注：PostgreSQL 的列元数据查询按 `information_schema` 规范编写，CI 未接真实 PG 实例，但 SQLite + MySQL 的同等路径在 `_test/generator_test` 与 `_test/demo` 中持续验证。

工厂函数从 `db.Dialect` 出发构造：

```go
d := generator.NewMetaDialect(db.NewDialect("sqlite"))   // → *SQLiteMetaDialect
```

---

## 6. 类型映射：TypeMapping

30+ 条内置映射覆盖主流 SQL 类型：

| 分类 | SQL 类型 | Go 类型 |
|------|----------|---------|
| 整数 | `INT` / `INTEGER` / `TINYINT` / `SMALLINT` / `MEDIUMINT` | `int` |
| 大整数 | `BIGINT` / `SERIAL` / `BIGSERIAL` | `int64` |
| 浮点 | `FLOAT` / `DOUBLE` / `REAL` | `float64` |
| 精度 | `DECIMAL` / `NUMERIC` | `string`（避免精度丢失） |
| 字符串 | `VARCHAR` / `CHAR` / `TEXT` / `LONGTEXT` / `MEDIUMTEXT` / `TINYTEXT` / `ENUM` / `SET` | `string` |
| 时间 | `DATE` / `DATETIME` / `TIMESTAMP` / `TIME` | `time.Time` |
| 布尔 | `BOOL` / `BOOLEAN` / `BIT` | `bool` |
| 二进制 | `BLOB` / `LONGBLOB` / `MEDIUMBLOB` / `TINYBLOB` / `VARBINARY` / `BINARY` | `[]byte` |
| JSON | `JSON` / `JSONB` | `string`（见 §9） |
| 未识别 | 其他 | `string`（兜底） |

可按需覆盖：

```go
tm := generator.NewTypeMapping()
tm.AddMapping("MONEY", "float64")     // 新增
tm.RemoveMapping("JSON")              // 删除，回退到 string
```

`MetaReader` 默认持有 `NewTypeMapping()`，要替换只需在 `ConfigMetaReader` 里改 `mr.TypeMapping` 字段。

---

## 7. 命名派生

生成器对名字的控制全在 `TemplateUtil` 里，通过 `Generator.PkgNameFunc` / `StructNameFunc` / `BaseNameFunc` 三个字段暴露，默认实现：

| 函数 | 输入 → 输出 | 规则 |
|------|-------------|------|
| `PkgName` | `sys_user` → `sysuser` | 去掉所有下划线直接拼接 |
| `StructName` | `login_log` → `LoginLog` | snake_case → PascalCase |
| `BaseName` | `LoginLog` → `BaseLoginLog` | `"Base" + StructName` |
| `FieldToAttr` | `user_id` → `UserId` | snake_case → PascalCase（列名 → Go 字段） |
| `ToCamelCase` | `UserId` → `userId` | PascalCase → camelCase（json tag、ServicePrefix） |
| `EscapeKeyword` | `type` → `type_` | Go 关键字加尾下划线（短 setter 用） |

`TablePrefix` 剥离发生在命名之前：

```mermaid
flowchart LR
    SRC["sys_login_log"] -->|"strip sys_"| STRIP["login_log"]
    STRIP --> P["PkgName<br/>loginlog"]
    STRIP --> S["StructName<br/>LoginLog"]
    STRIP --> T["TableName（代码里）<br/>#quot;sys_login_log#quot; ← 原始名保留"]
```

测试明确验证：包名/结构体名用去前缀的版本，但 `base.go` 里 `Table.Name` 字段仍是原始 `sys_login_log`，保证运行时 SQL 打到正确的表。

### 7.1 标识符参数化：`names.go` 与 Qualified 模式

生成代码里的**包级标识符**（`Table`、`NewDao`、`FindById`…）历史上都是裸名——这隐含了"一个包只能放一张表"的约束。`names.go` 把这套标识符抽成模板变量（`TableInfo.Names`），同一套模板按两种作用域渲染：

| 标识符键 | 默认（一表一包） | Qualified（多表一包） |
|----------|------------------|------------------------|
| `tableVar` | `Table` | `TableUser` |
| `newBaseFn` / `newWithRowFn` | `NewBase` / `NewWithRow` | `NewBaseUser` / `NewUserWithRow` |
| `initRowFn` | `initRow` | `initUserRow` |
| `fromRowFn` / `fromRowsFn` | `FromRow` / `FromRows` | `UserFromRow` / `UserFromRows` |
| `newFn` | `New` | `NewUser` |
| `daoType` / `newDaoFn` | `Dao` / `NewDao` | `UserDao` / `NewUserDao` |
| `pageType` | `UserPage`（两模式同名） | `UserPage` |
| `findByIdFn` / `findByIdsFn` / `deleteByIdsFn` | `FindById` / `FindByIds` / `DeleteByIds` | `UserFindById` / `UserFindByIds` / `UserDeleteByIds` |
| `findByFn` / `findFirstByFn` / `deleteByFn` | `FindBy` / `FindFirstBy` / `DeleteBy` | `UserFindBy` / `UserFindFirstBy` / `UserDeleteBy` |
| `countFn` / `countByFn` | `Count` / `CountBy` | `UserCount` / `UserCountBy` |
| `serviceType` / `prefixConst` / `listSqlVar` | `Service` / `ServicePrefix` / `listSql` | `UserService` / `UserServicePrefix` / `userListSql` |
| 文件名 | `base.go` / `model.go` / `dao.go` / `service.go` | `base_user.go` / `model_user.go` / `dao_user.go` / `service_user.go` |

设计取舍：

- **机械前缀而非复数**：`UserFindBy` 而不是 `FindUsersBy`——复数需要 pluralizer，会产生 `FlowTaskIndexs` 这类畸形名。
- **`TableUser` 前置、函数名后缀位置不变**：与既有的 `BaseUser` 命名习惯对齐（限定词在前），函数/类型则保持 `主语 + 动词` 语序（`UserFindBy`）。
- **`pageType` 两模式同名**：`UserPage` 本身就含结构体名，永不冲突。
- **默认模式字节兼容**：不开启 Qualified 时渲染结果与参数化之前完全一致（现有测试与存量代码不受影响）。

若多张表被 `PkgNameFunc` 映射进同一个包而未开 `Qualified`，`Generate()` 会直接报错并列出冲突表名——宁可失败，不产出互相覆盖的坏代码。

### 7.2 多表一包与循环依赖

按领域把多张表放进一个包（如 `sys_flow_task` + `sys_flow_log` → `flow`），配合 `Qualified=true`：

```go
gen.TablePrefix = "sys_"
gen.Qualified = true
gen.PkgNameFunc = func(tableName string) string {
    return "flow"        // flow_task / flow_log 都进 flow 包
}
```

产物变为 `flow/base_flow_task.go`、`flow/dao_flow_log.go` 等每表一组文件，`init.go` 里该包只空白导入一次。

**为什么是"包 = 领域"而不是把 service 拆去独立目录？** Go 禁止包循环依赖，而 Java 允许服务互注，直接翻译 Java 的分包方式（service 一层、dao 一层）很容易在 Go 里织出环。"多表一包 + 单向依赖"才是 Go 的惯用解。四条规则：

1. **包 = 领域**：一个业务领域的表（含它们的 model/dao/service）放同一个包，包内自由互调，无环可言。
2. **跨包只向下**：service 可以 import 他包的 dao/model（查数据天然无副作用）；dao/model 永远不 import 任何 service。
3. **service ↔ 他包 service 走三阀门**：消费者侧定义窄接口 + 装配处注入；或改用 [dami](dami.md) 事件解耦；若连接口都拆不出来，说明边界划错了——合并成一个包。
4. **共享 DTO 放叶子包**：多个 service 都要返回的类型放一个只被依赖、不依赖任何人的 `api` 包。

```mermaid
flowchart TD
    subgraph FLOW["flow 包（领域）"]
        FT["FlowTask model/dao/service"]
        FL["FlowLog model/dao/service"]
    end
    ORD["order 包 service"] -->|"规则2：向下单向"| FU["user 包 dao/model"]
    ORD -->|"规则3：窄接口注入<br/>或 dami 事件"| UU["user 包 service"]
    ORD --> API["api 包（规则4：共享 DTO，叶子）"]
    FLOW --> FU
```

跨包拿到 `[]*db.Row` 想类型化时，用 base.go 导出的桥（见 §10.3）：`user.UserFromRow(row)`。

`_test/demo` 演示了完整链路：`sys_flow_task` + `sys_flow_log` 两表一包进 `flow` 包（Qualified 命名、文件 `base_flow_task.go` 等），其 `FlowTaskService.GetById` 组合 user 包模型拼装响应视图——service → 他包 dao/model 是普通 import，编译器保证无环；且该手写改动在重跑生成器后保留（service.go 已存在即跳过）。

---

## 8. 五种产物与覆盖策略

生成器对"是否覆盖"采用**三档策略**：

| 文件 | 生成器 | 覆盖策略 | 文件头注释 |
|------|--------|----------|-----------|
| `base.go` | `BaseGenerator` | **总是覆盖** | `// Generated by Aifei Generator. DO NOT EDIT.` |
| `init.go` | `InitGenerator` | **总是覆盖** | `// Generated by Aifei Generator. DO NOT EDIT.` |
| `model.go` | `ModelGenerator` | **已存在则跳过** | `// This file is NOT overwritten on re-generation. Add custom logic here.` |
| `dao.go` | `DaoGenerator` | **已存在则跳过** | `// This file is NOT overwritten on re-generation. Add custom queries here.` |
| `service.go` | `ServiceGenerator` | **已存在则跳过** | `// This file is NOT overwritten on re-generation. Add custom queries here.` |

设计意图：

- **base.go / init.go 是机械的、纯元数据派生的** → 永远以最新 schema 为准，反复覆盖。
- **model.go / dao.go / service.go 是要被开发者编辑的** → 只在首次生成；后续重跑 generator 不破坏你的业务代码。
- **`Generator.Force = true` 把"跳过"变成"覆盖"**：三个业务产物也强制重生成。适合模板升级后整体刷新（如本批新增的 FromRow 桥、批量 IN 之于存量 dao.go）；手写改动会被丢弃，跑之前先提交或备份。
- **文件名有两种形态**：默认 `base.go` 等固定名；`Qualified=true` 时按表命名（`base_flow_task.go` 等），多表共存一包互不覆盖（见 §7.1）。

每张表的生成顺序固定为 `base → model → dao → service`（见 `Generator.Generate` 主循环），保证依赖方向正确：model 依赖 base 的 `NewBase()`；dao 依赖 model 的结构体；service 依赖 dao 的 typed 方法。

### 8.1 base.go 产物（每张表都生成）

`BaseGenerator.buildData` 装配的数据：表名、表注释、字段名串、主键、生成列、字段类型 map、每列的 `RowGetter` 与 `Zero`。生成的 `base.go` 包含：

```go
var Table = &db.Table{
    Name:             "user",
    Fields:           "id,name,age,email,created_at",
    PrimaryKeys:      []string{"id"},
    GeneratedColumns: []string{},          // MySQL VIRTUAL/STORED 才会出现
    FieldTypes: map[string]reflect.Type{
        "id":         reflect.TypeOf(int(0)),
        "name":       reflect.TypeOf(""),
        "created_at": reflect.TypeOf(time.Time{}),
        // ...
    },
}

type BaseUser struct { *db.Row }
func NewBase() *BaseUser { return &BaseUser{Row: db.NewRow(Table.Name)} }
// NewWithRow 会跑 initRow：包出来的对象带表名/主键/解码后的 JSON 列，可直接 Update()/Delete()
func NewWithRow(row *db.Row) *BaseUser { return &BaseUser{Row: initRow(row)} }

// 三类方法（每列一组，JSON 列除外）：
func (r *BaseUser) Age() int            { return r.GetInt("age") }              // typed getter
func (r *BaseUser) SetAge(v int) *BaseUser { r.Set("age", v); return r }        // typed setter
func (r *BaseUser) Age_(v int) *BaseUser { return r.SetAge(v) }                 // 短 setter（链式）

// 实例级 CRUD：
func (r *BaseUser) Insert() (*BaseUser, error) { ... }
func (r *BaseUser) Update() (bool, error)      { ... }
func (r *BaseUser) Delete() (bool, error)      { ... }

// 导出的类型化桥（把裸 Row 包装回模型，见 §10.3）：
func FromRow(row *db.Row) *User
func FromRows(rows []*db.Row) []*User

func init() { db.RegisterTable(Table) }       // ← 自注册到 db 全局表注册表
```

关键设计：

- **`NewWithRow` 执行完整 init**：历史上的实现是裸结构体字面量（`Row: row`），包出来的对象缺表名与主键，`Update()` / `Delete()` 会失败；现在统一走 `initRow`。
- **JSON 列从 base.go 里剔除**（不出 typed getter/setter）——把方法名让给 `model.go`，方便用户在 model 里覆盖成结构体类型（见 §9）。
- **短 setter（`Age_(v)`）** 默认开启（`GenerateShortSetter=true`），让链式构造更顺眼：`New().Name_("alice").Age_(30).Insert()`。列名是 Go 关键字时（如 `type`、`select`）自动加 `_`（`EscapeKeyword`）避免冲突。
- **`init()` 自注册**：`db.RegisterTable(Table)` 让框架在启动时就能找到表元数据，给 [dataisolate](data-isolate.md) 这类插件做字段过滤、给 JSON 自动解码等。

### 8.2 model.go 产物（首次生成）

```go
type User struct { *BaseUser }              // 嵌入 BaseUser，获得全部 getter/setter/CRUD
func New() *User { return &User{BaseUser: NewBase()} }

// JSON 列的脚手架（默认 string，可升级成 struct）：
func (m *User) Profile() string { return m.GetStr("profile") }
func (m *User) SetProfile(v string) *User { m.Set("profile", v); return m }
```

`model.go` 是用户加自定义业务逻辑、自定义类型、覆盖 JSON 列类型的地方，所以只生成一次。

### 8.3 dao.go 产物（首次生成，typed Dao）

`DaoGenerator` 产出的 `dao.go` 是生成器最核心的产物——**类型安全的 Dao**。详见 §10。

### 8.4 service.go 产物（首次生成）

直接产出一个可用的 HTTP Service，按 [server](server.md) 的命名约定挂路由：

```go
const (
    ServicePrefix = "/api/v1/user"
    listSql = `SELECT * FROM user
    #where(name, '=', name)
    #and(age, '=', age)
    ORDER BY id DESC`
)

func init() { server.RegisterService(ServicePrefix, &Service{}) }
type Service struct{}

func (s *Service) List(in aifei.Input) aifei.Output      { ... }
func (s *Service) Paginate(in aifei.Input) aifei.Output   { ... }
func (s *Service) Create(in aifei.Input) aifei.Output     { ... }
func (s *Service) GetById(in aifei.Input) aifei.Output    { ... }   // 单主键才有
func (s *Service) UpdateById(in aifei.Input) aifei.Output { ... }   // 单主键才有
func (s *Service) DeleteById(in aifei.Input) aifei.Output { ... }   // 单主键才有
```

要点：

- **`listSql` 用 Enjoy SQL 指令**（`#where` / `#and`）自动拼查询条件——`buildQueryConditions` 遍历所有非主键列，每列生成一条 `#and(col, '=', col)`，请求里没传该参数则该条件被 Enjoy 引擎自动省略（详见 [db](db.md) 的 Enjoy SQL 部分）。
- **主键参数解析按类型分流**（`buildPKParamParse`）：`int` 用 `strconv.Atoi`、`int64` 用 `strconv.ParseInt`、`string` 直接取。其余类型退化为 `string`。
- **`ServicePrefix` 用 camelCase**：`ToCamelCase(StructName)` 把 `LoginLog` 转成 `/loginLog`，符合 REST 路径风格。
- **自注册**：`init()` 调 `server.RegisterService` 把前缀与实现登记进全局注册表。`server.AutoRegisterServices(app)` 在应用启动时遍历这个表，按方法名映射路由（`GetById` → `GET /:id`、`Create` → `POST /`，详见 [server](server.md)）。

### 8.5 init.go 产物（汇总）

`init.go` 位于 `outputDir` 根目录，用**空白导入**把所有子包拉进来，从而触发它们的 `init()`：

```go
package internal

import (
    _ "github.com/me/app/internal/user"
    _ "github.com/me/app/internal/loginlog"
)

// Tables are registered via init() functions in each per-table package.
// Use db.Tables() to retrieve all registered tables.
```

应用代码只要 import 一次 init.go 所在的包，就完成了全部表与服务的注册——无需手动罗列。

---

## 9. JSON 列的升级路径

JSON/JSONB 列在数据库里是 `json` 类型，生成器默认按 `string` 处理，但留好了升级通道。`_model.af` 模板为每个 JSON 列生成带有详细注释的脚手架：

```go
// profile is a JSON column. It defaults to string. To expose it as a struct:
//   1. define the struct type in this file (e.g. type Profile struct{...})
//   2. in an init() here, register it: Table.FieldTypes["profile"] = reflect.TypeOf(Profile{})
//      (db.Row.DecodeJSONFields will then auto-decode it on read)
//   3. replace these two methods with typed versions:
func (m *User) Profile() string { return m.GetStr("profile") }
func (m *User) SetProfile(v string) *User { m.Set("profile", v); return m }
```

升级流程只需三步：

1. 在 `model.go` 里定义结构体类型（`type Profile struct { ... }`）
2. 在 `init()` 里注册到 `Table.FieldTypes`
3. 把脚手架的两个方法替换成类型化版本

之后 `db.Row.DecodeJSONFields`（在 `base.go` 的 `initRow` 里被调用）会在读取时自动把 JSON 解码成注册的结构体类型。注意：升级后该方法名在 model 里覆盖掉了 base 里脚手架的版本，Go 的方法解析自然优先用 model 的——这正是 JSON 列从 base.go 里被剔除的原因。

---

## 10. 生成的 typed Dao

`_dao.af` 产出的 `dao.go` 把通用 `db.Dao` 包成一个**绑定到具体表、返回具体类型**的 Dao：

```go
type Dao struct { *db.Dao }
func NewDao() *Dao { return &Dao{Dao: db.Use()} }    // 默认 db config
```

### 10.1 两段式 API：setup → terminal

链式查询分两层，类型签名在每层都正确返回 `*Dao`：

| 层 | 方法 | 作用 |
|----|------|------|
| **setup**（返回 `*Dao`） | `Sql(tpl, data)` / `SqlWithArgs(tpl, args...)` / `SqlById(id, data)` / `SqlByIdWithArgs(id, args...)` / `RawSql(sql, args...)` / `Select(fields)` | 设置查询来源 |
| **terminal**（返回结果） | `Find()` → `[]*User`；`FindFirst()` → `*User`；`FindOne()` → `*User`；`FindExists()` → `bool`；`Paginate(p,s)` → `*UserPage`（见 §10.4）；`Count()` / `CountBy(...)`；`FindBy(...)` / `FindFirstBy(...)`；`FindIn(field, vals...)`（IN 查询）；`FindByID(id)` / `DeleteByID(id)`；`FindByIds(ids...)` / `DeleteByIds(ids...)`（批量 IN，见 §10.4） | 真正执行 |

`db.Dao` 本身要求每次调用都传表名，typed Dao 把表名在构造时就绑定好（`Table.Name`），后续调用无需再传。

### 10.2 包级便捷函数

除了 `NewDao().Xxx()`，还生成了一组包级函数（委托到 `NewDao()`），更接近 Java Aifei 的 `Db.findById(...)` 风格：

```go
user, err := FindById(42)                          // 单主键时才有
users, err := FindByIds(1, 2, 3)                   // 批量 IN（单主键时才有）
rows, err := FindBy("age > ?", 18)                 // 按 where 查
n, err := DeleteBy("status = ?", "deleted")        // 按条件删
deleted, err := DeleteByIds(1, 2)                  // 批量删除（单主键时才有）
total, err := Count()                              // 全表计数
```

| 方法 | 生成条件 |
|------|----------|
| `FindByID` / `DeleteByID` / `FindByIds` / `DeleteByIds`（实例与包级两种） | **仅单主键**生成；复合主键不生成，避免签名歧义 |
| 其他包级函数 | 总是生成 |

### 10.3 typed row 转换：FromRow / FromRows 桥

`db.Dao.Find()` 返回的是 `[]*db.Row`，typed Dao 通过 base.go **导出的** `FromRow` / `FromRows` 包成 `[]*User`：

```go
func FromRow(row *db.Row) *User {
    if row == nil { return nil }
    return &User{BaseUser: NewWithRow(row)}        // NewWithRow 内部跑 initRow
}
func FromRows(rows []*db.Row) []*User { ... }       // 循环调 FromRow
```

`initRow`（在 `base.go` 里）干三件事：

1. `row.SetTable(Table.Name)` —— 把表名写回 Row（db.Dao 查询出来的 Row 不知道原表）
2. `row.SetPrimaryKeys(Table.PrimaryKeys...)` —— 写回主键列名（`Row.Update()` / `Delete()` 需要）
3. `db.DecodeJSONFields(row)` —— 按注册的 `Table.FieldTypes` 把 JSON 列解码成结构体（见 §9）

桥放在 base.go 且导出，有两个用意：

- **存量项目零成本升级**：base.go 总是覆盖，重跑一次生成器就得到桥，不用动冻结的 model/dao。
- **手写 SQL 的类型化出口**：`db.Sql(...)` / 关联查询拿回 `[]*db.Row` 后，包外也能一行类型化（Qualified 模式下为 `UserFromRow` / `UserFromRows`）：

```go
rows, _ := db.Sql(`SELECT * FROM user WHERE age > #para(0)`, 18).Find()
typed := user.FromRows(rows)        // []*user.User
```

### 10.4 typed 批量 IN 与分页

**批量 IN** 消灭了"绕过类型层裸拼表名"的诱惑（底层是 `db.Dao.FindIn` / `DeleteInIds`）：

```go
users, err := user.NewDao().FindByIds(1, 2, 3)          // WHERE id IN (1,2,3)
aged, err := user.NewDao().FindIn("age", 25, 35)        // 任意列的 IN
n, err := user.NewDao().DeleteByIds(7, 8)               // DELETE ... WHERE id IN (7,8)
```

**typed 分页**为每张表生成 `UserPage`——元数据字段与 `db.Page` 同名同 json tag，唯一区别是 `Rows` 装的是 `[]*User` 而非 `[]*db.Row`，前端拿到的 JSON 结构不变：

```go
type UserPage struct {
    PageNum    int      `json:"pageNum"`
    PageSize   int      `json:"pageSize"`
    TotalRows  int64    `json:"totalRows"`
    TotalPages int      `json:"totalPages"`
    Rows       []*User  `json:"rows"`
}

page, err := user.NewDao().Sql(listSql, filter).Paginate(1, 20)
for _, u := range page.Rows { fmt.Println(u.Name()) }   // 元素直接是 *User
```

---

## 11. 模板引擎：嵌入式 Enjoy

五个 `.af` 模板（`templates/_base.af`、`_model.af`、`_dao.af`、`_service.af`、`_init.af`）通过 `//go:embed` 编进二进制：

```go
//go:embed templates/_base.af
var baseTemplateContent string
```

模板渲染统一走 `Engine`（`generator.go`）：

```go
func NewEngine() *Engine {
    e := enjoy.NewEngine("generator")
    e.AddSharedObject("u", &TemplateUtil{})       // 模板里 #u.PkgName(...) 可直接调用
    return &Engine{enjoy: e}
}
```

`Engine.RenderTemplate(content, data)` 用 `sync.Map` 按模板内容做编译缓存——同一份模板多次渲染只编译一次，对批量生成（几十上百张表）性能有意义。

模板里用到的 Enjoy 语法（与 [db](db.md) 的 SQL 模板同源）：

| 语法 | 含义 | 在哪些模板里用 |
|------|------|-----------------|
| `#(expr)` | 输出表达式值 | 所有 |
| `#if (cond) ... #end` | 条件 | `_base.af`（表注释）、`_dao.af`（单主键分支）、`_service.af` |
| `#for (x : list) ... #end` | 遍历 | 所有（字段循环、import 循环、表循环） |
| `u.Method(...)` | 调用共享对象 | `_base.af`（`u.RowGetter` 等） |

`TemplateUtil` 暴露的方法：`RowGetter(type)` → `GetInt` / `GetStr` / `GetTime`...；`ZeroValue(type)` → `int(0)` / `""`...；`ImportPath(type)` → `time` 或空；`PkgName` / `StructName` / `BaseName` / `EscapeKeyword` / `JoinNames` / `Quote`。

### 11.1 自定义 service 模板

默认 `service.go` 模板面向 aifei 自带的 `server` 包。若项目有自己的服务层（自定义响应格式、路由约定），不必 fork 生成器——用 `ServiceGenerator.Template` 注入自己的模板：

```go
gen.ConfigServiceGenerator(func(s *generator.ServiceGenerator) {
    s.Template = myServiceTpl        // Enjoy 模板字符串；空则用内嵌默认
    s.Force = true                   // 可选：覆盖已存在的 service.go
})
```

模板渲染的数据键契约（两类，同一张 map）：

| 键 | 来源 | 示例值（`sys_flow_task`，Qualified） |
|----|------|--------------------------------------|
| `pkgName` / `structName` / `tableName` / `servicePath` | 命名派生 | `flow` / `FlowTask` / `sys_flow_task` / `flowTask` |
| `apiPrefix` | `ServiceGenerator.APIPrefix` | `/api/v1` |
| `hasSinglePK` / `pkName` / `pkParamParse` / `needStrconv` | 主键派生 | `true` / `id` / 生成的解析代码 / `true` |
| `queryConditions` | 非主键列拼的 `#where` / `#and` 串 | 多行 Enjoy SQL 指令 |
| `tableVar` / `newFn` / `newDaoFn` / `findByIdFn` / `deleteByIdFn` / `serviceType` / `prefixConst` / `listSqlVar` / …（§7.1 全表） | `names.go` 标识符参数化 | `TableFlowTask` / `NewFlowTask` / `NewFlowTaskDao` / … |

约束：渲染结果必须是合法 Go 源码——`Engine.RenderTemplate` 会过 `go/format`，非法输出直接报错（连带渲染原文，便于定位）。Base/Dao/Model/Init 模板不开放覆盖：base/init 是纯机械产物没有定制诉求，dao/model 的扩展点本来就在生成后的文件里手写。

---

## 12. 配置与集成

`tools/generator` 是库 + 命令行（用户自行写 `main` 调 `Generate()`），不读配置文件。典型集成两种形态：

### 12.1 形态一：项目内独立的 `cmd/gen`

`_test/demo/cmd/gen/main.go` 是范例——一个独立 main，按需手动跑：

```bash
go run ./cmd/gen        # 生成到 ./internal
go run .                # 启动 demo，自动注册所有表与服务
```

好处：生成动作显式，可在生成前后插自定义步骤（如生成完打 patch、跑 swag 等）。

### 12.2 形态二：`go:generate` 批量

在业务包里写一行：

```go
//go:generate go run ../../cmd/gen
package myapp
```

随后 `go generate ./...` 就会批量重生成。由于 `model.go` / `dao.go` / `service.go` 已存在即跳过，重跑是安全的——只有 `base.go` / `init.go` 会随 schema 变更刷新。

### 12.3 接入应用的代码

应用 `main.go` 只需 import 生成代码所在包一次，再调 `server.AutoRegisterServices`：

```go
import (
    _ "github.com/me/app/internal"     // 触发 init.go 的空白导入链
    "github.com/crazy-airhead/aifei-go/server"
)

func main() {
    app := aifei.New()
    server.AutoRegisterServices(app)    // 遍历 RegisterService 注册过的 service
    server.Run(app, ":8080")
}
```

整个链路：`import _ ".../internal"` → `init.go` 的空白导入 → 各子包 `base.go` 的 `init()` 调 `db.RegisterTable`、各 `service.go` 的 `init()` 调 `server.RegisterService` → `AutoRegisterServices` 把它们映射成路由。

---

## 13. 模块结构

```
tools/generator/
├── generator.go         # Generator 入口 + Engine（Enjoy 封装）
├── meta_reader.go       # MetaReader：读库元数据 → []*TableInfo
├── meta_dialect.go      # MetaDialect 接口 + MySQL/Postgres/SQLite 三实现
├── type_mapping.go      # TypeMapping：30+ SQL → Go 类型映射
├── types.go             # TableInfo / FieldInfo 中间数据模型
├── names.go             # buildNames：标识符参数化（一表一包裸名 / Qualified 表限定名）
├── field_to_attr.go     # snake_case → PascalCase 命名派生
├── go_keyword.go        # Go 关键字检测 + 转义
├── template_util.go     # 模板里可调用的 TemplateUtil（u 共享对象）
├── base_generator.go    # BaseGenerator：base.go（总是覆盖）
├── model_generator.go   # ModelGenerator：model.go（已存在则跳过）
├── dao_generator.go     # DaoGenerator：dao.go（已存在则跳过）
├── service_generator.go # ServiceGenerator：service.go（已存在则跳过）
├── init_generator.go  # InitGenerator：init.go（总是覆盖）
└── templates/
    ├── _base.af         # base.go 模板
    ├── _model.af        # model.go 模板（含 JSON 列升级注释）
    ├── _dao.af          # dao.go 模板（typed Dao）
    ├── _service.af      # service.go 模板（HTTP Service + listSql）
    └── _init.af       # init.go 模板（空白 import）
```

源码约 2,400 行（含模板）；测试在 `_test/generator_test`（黑盒，SQLite + 驱动反射路径，含 Qualified 多表一包 / Force / 自定义模板用例）与 `_test/demo/cmd/gen`（端到端样例）。

---

## 14. 总结

Aifei-Go 代码生成器的设计原则：

1. **schema 即真理**：类型、主键、生成列、注释全来自数据库，生成代码与 schema 严格对齐；改库即生效，无需手写 model struct。
2. **三档覆盖策略 + Force 逃生门**：机械产物（base/init）每次覆盖，业务产物（model/dao/service）只生成首次——保留用户的定制空间；模板升级需要整体刷新时再开 `Force`。
3. **自注册、零连线**：`base.go` 的 `init()` 注册 `db.Table`、`service.go` 的 `init()` 注册 Service，应用代码 import 一次就接入框架。
4. **typed Dao 不妥协**：`db.Dao` 的表名/类型痛点由 typed Dao 解决，调用方拿到的是 `[]*User` 而非 `[]*db.Row`，重构友好；手写 SQL 的结果也有导出桥 `FromRow` / `FromRows` 兜底。
5. **包结构服务业务，而非反过来**：默认一表一包；按领域多表一包时开 `Qualified`，标识符参数化让同一套模板服务两种作用域，配合 §7.2 的依赖规则在 Go 里织不出环。
6. **复用而非重发明**：在 `db.Dialect` 之上加 `MetaDialect`、用项目自己的 Enjoy 做模板、与 [server](server.md) 的命名约定对齐——生成器只补 schema→代码这一段缺口。
7. **依赖极简**：只依赖 `db` + `enjoy` + 标准库；驱动由使用者提供，不绑任何具体数据库。

### 延伸阅读

- [db](db.md) —— `Row` / `Dao` / `Table` / `Dialect` 的运行时定义，生成器的下游
- [server](server.md) —— Service 注册与路由命名约定（`GetById` → `GET /:id` 等）
- [data-isolate](data-isolate.md) —— 直接消费生成器产出的 `db.Table` 元数据做字段/行隔离
- [enjoy](enjoy.md) —— 生成器与 db SQL 模板共用的渲染内核（`#()` / `#for` / `#if` 等指令）
