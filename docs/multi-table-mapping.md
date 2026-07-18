# Dao 多表关联：SQL 解析与别名映射设计

> 目标：把当前 Dao 的「单表 `table` 字段映射」扩展为「从 SQL 解析多张表（含别名）并建立 列 → 表 → Table 元数据 的映射」，让 JOIN 查询的结果行也能正确绑定主键、解码 JSON 列——和单表 typed Dao 一致，无需调用方逐列手动解码。

---

## 1. 背景与现状

`db.Dao` 近期新增了 `table` 字段（`db/dao.go:16`），用于声明原始 SQL 结果行所属的表：

```go
func (d *Dao) Table(name string) *Dao {
    d.table = name
    return d
}
```

它的工作链路（`db/executor.go`）：

- `execFind / execFindBy / execPaginate / execForEach` 在拿到结果后调用 `decodeRows(result, dao.table)`。
- `decodeRows` → `tableFor(table)` → `GetTableByName(name)` 拿到注册的 `*db.Table`（`db/table.go`）。
- `decodeRow(r, t)` 给每行 `SetTable` + `SetPrimaryKeys`，再调 `DecodeJSONFields(r)`（`db/json_codec.go`）。
- `DecodeJSONFields` 依据 `Table.FieldTypes`（`map[string]reflect.Type`）把 JSON 字符串列物化成声明的 Go 复合类型（struct / slice / map）。

**问题**：`dao.table` 是**单个字符串**，只能绑定一张表。而真实业务里更常见的是多表关联：

```sql
SELECT u.*, d.name AS dept_name, d.config AS dept_config
FROM user u JOIN dept d ON u.dept_id = d.id
```

结果列被打平成 `id, name, ..., dept_name, dept_config`。当前只能套用 `user` 一张表的元数据：

- `dept_config`（`dept` 表声明的 JSON 列）不会被解码，`row.Get("dept_config")` 拿到的是原始 JSON 字符串而非声明的复合类型。
- 主键只绑定 `user` 的，`dept` 的主键信息丢失。

---

## 2. 为什么 Go 里需要「解析 SQL」

Java 版（`aifei-db` 的 `RowFactory`）也只调 `getColumnLabel(i)`，**并不**用 `getTableName()`——Java 版结果同样是打平的，多表靠调用方自己处理。

但 JDBC **可以**拿到每列的源表（`ResultSetMetaData.getTableName(i)`），Go 的 `database/sql` **拿不到**：`sql.Rows` 只暴露列名（`Columns()`）和列类型（`ColumnTypes()`），**没有列对应的源表名**。

> 结论：在 Go 里若要知道「某一列属于哪张表」，只能从 **SQL 文本本身**解析（FROM / JOIN 表名与别名、SELECT 投影里的 `alias.col` / `AS label`），在执行前自行建立映射。

---

## 3. 设计目标

1. **解析表与别名**：从渲染后的 SQL（含 `db/sql` Enjoy SQL 模板渲染结果）提取 FROM / JOIN 的表引用 `{表名, 别名}`。
2. **列 → 表映射**：把结果列（列标签）映射到它所属的注册表，从而套用该表的 `FieldTypes` / `PrimaryKeys`。
3. **多表元数据绑定**：一个结果行可携带多张表的列；所有涉及的表的 JSON 列都能被解码；写路径仍以「主表」为准。
4. **零外部依赖**：仅用 Go 标准库，写一个**聚焦、容错**的 FROM/JOIN/SELECT 扫描器，不引入第三方 SQL parser。
5. **向后兼容**：`Dao.Table(name)` 单表语义不变；无任何 hint 的原始 SQL 默认行为不变（不解码）。

---

## 4. 总体方案

```
 渲染后的 SQL 串
        │
        ▼
 ┌──────────────────┐    ┌──────────────────────┐
 │ db/sqlparse 扫描  │ ─► │ []TableRef{Table,Alias}│   FROM/JOIN
 │ (FROM/JOIN/SELECT)│    │ alias→*Table          │   别名表
 └──────────────────┘    │ colLabel→*Table(投影)  │   SELECT 投影
        │                └──────────────────────┘
        ▼                          │
 ┌──────────────────┐              ▼
 │ TableMapping     │ ◄── 合并所有涉及表的 FieldTypes / PKs
 │  primary         │
 │  colOwner        │
 │  mergedFieldTypes│
 └──────────────────┘
        │
        ▼
 decodeRows(rows, mapping)  ──►  每行 SetTable(primary)、SetPrimaryKeys、
                                  用 mergedFieldTypes 解码 JSON 列
```

三个层次的入口（控制力从强到弱，见 §9）：

- `Dao.Tables(refs ...TableRef)` —— 显式声明涉及的表与别名，最可靠。
- `Dao.AutoTables()` / 配置开关 `Config.AutoTableMapping` —— 让 Dao 自动解析当前 SQL 的表与别名（opt-in，默认关，避免改变现有行为）。
- `Dao.Table(name)` —— 现有单表 API，等价于只绑定主表。

---

## 5. 核心数据结构

### 5.1 解析器层（新增 `db/sqlparse` 子包，纯字符串、零依赖、易测试）

```go
package sqlparse

// TableRef 表示 SQL 里一个表引用。
// Alias 为空表示没有别名（直接用 Table 名当别名）。
type TableRef struct {
	Table string // 真实表名（去引号、去 schema 前缀后的叶子名）
	Alias string // 别名；无别名时与 Table 相同
	FromSubquery bool // 派生表 (SELECT ...) AS x —— 无法解析列归属，标记跳过
}

// Projection 表示 SELECT 列表的一个投影项。
type Projection struct {
	TableAlias string // u.col 里的 "u"；为空表示裸列/表达式
	Column     string // 列名；"*" 表示通配
	Label      string // 输出列标签：AS 别名 或 列名；通配时为空
	Star       bool   // 是否 alias.* / *
}

// Result 是一次解析的全部产物。
type Result struct {
	Tables       []TableRef   // FROM/JOIN 出现的表，按出现顺序
	AliasToTable map[string]string // 别名 → 真实表名（含自引用 table→table）
	Projections  []Projection // SELECT 投影项
}
```

### 5.2 db 层（`db` 包内新增 `table_mapping.go`）

```go
package db

// TableRef 是 db 包对外的表引用（解耦 sqlparse，避免 db 公开依赖细节）。
type TableRef struct {
	Table string
	Alias string
}

// tableMapping 把「渲染后 SQL + 显式/解析得到的表引用」解析成可执行的元数据视图。
type tableMapping struct {
	primary          *Table                     // 第一张 FROM 表：决定 row.Table() 与写路径
	tables           []*Table                   // 所有涉及且已注册的表（去重、保序）
	colOwner         map[string]*Table          // 结果列标签 → 所属已注册表（best-effort）
	mergedFieldTypes map[string]reflect.Type    // 结果列标签 → 声明类型（用于 JSON 解码）
}
```

---

## 6. SQL 解析器设计（`db/sqlparse`）

### 6.1 设计原则

- **容错优先**：解析失败、遇到不支持的构造（子查询、复杂表达式）时，**静默跳过**，绝不抛错、绝不改写 SQL。映射缺失的列退化为「不解码」（与现状一致）。
- **只解析结构关键字**：`SELECT … FROM … <JOIN … ON …> …`，识别表引用与投影项；`WHERE / GROUP / HAVING / ORDER / LIMIT` 之后的内容不影响表集合，扫描到这些关键字即停止 FROM 收集。
- **大小写不敏感**地识别关键字（`select`/`Select`/`SELECT`）。

### 6.2 词法器（Tokenizer）

扫描出 token 序列，区分：

| 类别 | 例子 |
|------|------|
| 标识符（含引号） | `` `user` `` / `"user"` / `[user]` / `user` |
| 点号 | `.` |
| 逗号 | `,` |
| 星号 | `*` |
| 左右括号 | `( )` |
| 关键字 | `SELECT FROM JOIN INNER LEFT RIGHT FULL OUTER CROSS ON USING AS WHERE GROUP ORDER HAVING LIMIT UNION` |
| 注释 | `-- …` / `/* … */`（跳过） |

引号标识符去引号：`` `user` `` → `user`。同时记录「schema.table」的叶子名（`db.user` → `user`）。

### 6.3 FROM / JOIN 表引用收集

从 `FROM` 之后开始，直到遇到任一**子句边界关键字**（`WHERE/GROUP/ORDER/HAVING/LIMIT/UNION/EXCEPT/INTERSECT/RETURNING`）、或 `)`、或串尾：

- `T` / `T alias` / `T AS alias`：登记 `{Table: T, Alias: alias 或 T}`。
- `schema.T`：叶子名 `T`。
- `,`：隐式连接（笛卡尔积），下一项又是一张表。
- `JOIN` / `INNER|LEFT|RIGHT|FULL [OUTER] JOIN` / `CROSS JOIN` / `NATURAL JOIN`：新表引用，后跟 `ON <expr>` 或 `USING (...)` —— `ON` 后到下一个 `JOIN`/子句边界都属连接条件，跳过。
- `( SELECT … ) [AS] x`：**派生表**，登记 `{FromSubquery: true, Alias: x}`，用括号配对跳过内部内容；无法推断列归属，后续按「列标签唯一归属」兜底。

> 别名表 `AliasToTable` 同时包含 `alias→table` 与 `table→table`（自引用），方便统一查。

### 6.4 SELECT 投影收集

在 `SELECT` 与第一个 `FROM` 之间，按**顶层逗号**切分（括号内逗号不计），逐项解析：

| 写法 | 解析结果 |
|------|----------|
| `*` | `Projection{Star:true}`（展开为所有涉及表的全部列） |
| `u.*` | `Projection{TableAlias:"u", Star:true}` |
| `u.profile` | `Projection{TableAlias:"u", Column:"profile", Label:"profile"}` |
| `profile`（裸列） | `Projection{Column:"profile", Label:"profile"}` |
| `u.profile AS p` | `Projection{TableAlias:"u", Column:"profile", Label:"p"}` |
| `COUNT(*) AS c` | `Projection{Label:"c"}`（无 TableAlias，归属未知） |

输出列标签 `Label` 的取值（与驱动 `rows.Columns()` 对齐）：`AS` 别名优先，否则列名，否则跳过。

### 6.5 已知边界（文档需明确，不阻塞实现）

- **子查询（派生表 / 标量子查询）**：无法静态推断列归属，退化为兜底。
- **`SELECT *` 的列顺序与同名冲突**：依赖驱动返回顺序；同名列归属按「唯一归属」规则（§7），冲突时主表优先。
- **UNION / WITH**：只取首个分支的 FROM/SELECT 做映射；复杂场景建议用显式 `Tables()`。
- **窗口函数、CTE 别名**：仅识别其 `AS label`，不追溯来源表。

这些场景一律**安全降级**（不绑该列、不解码），与「无 hint」现状等价，绝不报错。

---

## 7. 列 → 表 映射策略

拿到解析结果后，结合**已注册**的 `db.Table` 注册表（`GetTableByName`）建立 `colOwner` / `mergedFieldTypes`。优先级从高到低：

1. **SELECT 投影里的别名前缀**（最强信号）：
   `u.profile` / `u.profile AS p` → 通过 `AliasToTable["u"]` → `user` 表；取 `user.FieldTypes["profile"]`。即使别名前缀在结果标签里被驱动抹掉，我们也能在执行前从投影项还原归属。
2. **输出标签的唯一归属**（最常见、最省事）：
   对 `*` / 裸列 / 未带前缀的投影，逐个已注册表查 `FieldTypes`；若标签名**恰好出现在一张表**里 → 归该表；若**多张表都有** → 冲突，按主表（第一张 FROM 表）优先（或可配置「冲突不下发」）。
3. **不归属任何注册表**：保持原值，不绑定、不解码（与现状一致）。

`mergedFieldTypes` 即上述解析出的 `标签 → 类型` 合并表；同名冲突取主表。

> 该策略覆盖绝大多数 JOIN：只要 SELECT 的列要么带别名前缀、要么列名在各表间不重名（或重名但有 `AS` 唯一标签），就能正确归表与解码。

---

## 8. 解码流程改造

把现有「按单表名解码」泛化为「按 `tableMapping` 解码」，保持调用点签名兼容。

### 8.1 `decodeRows` / `decodeRow`（`db/executor.go`）

```go
// 旧：decodeRows(rows, table string)
// 新：内部构造单表 mapping 后委托
func decodeRows(rows []*Row, table string) {
	if m := buildSingleTableMapping(table); m != nil {
		decodeRowsWithMapping(rows, m)
	}
}

func decodeRowsWithMapping(rows []*Row, m *tableMapping) {
	for _, r := range rows {
		decodeRowWithMapping(r, m)
	}
}

func decodeRowWithMapping(r *Row, m *tableMapping) {
	r.SetTable(m.primary.Name)
	if len(m.primary.PrimaryKeys) > 0 {
		r.SetPrimaryKeys(m.primary.PrimaryKeys...)
	}
	DecodeJSONFieldsWith(r, m.mergedFieldTypes) // 用合并后的类型表
}
```

### 8.2 `DecodeJSONFields`（`db/json_codec.go`）

```go
// 旧：DecodeJSONFields(r) 内部用 GetTableByName(r.table).FieldTypes
// 新：抽出可注入类型表的核心函数
func DecodeJSONFields(r *Row) *Row {               // 保留：单表入口（initRow 用）
	t := GetTableByName(r.table)
	if t == nil { return r }
	return decodeJSONFieldsWith(r, t.FieldTypes)
}

func DecodeJSONFieldsWith(r *Row, ft map[string]reflect.Type) *Row { // 多表入口
	return decodeJSONFieldsWith(r, ft)
}

func decodeJSONFieldsWith(r *Row, ft map[string]reflect.Type) *Row {
	// 与现有 DecodeJSONFields 体逻辑一致，只是 FieldTypes 改为参数 ft
	...
}
```

### 8.3 写路径不变

`Insert/Update/Delete` 仍以 `row.table`（主表）为准：`filterTableFields(row.table, …)`、`dialect.ForInsert(row.table, …)`。多表行是**只读投影**，写回需用各自的单表 typed Dao。这一点在文档里明确。

---

## 9. API 设计

### 9.1 Dao 新增方法（`db/dao.go`）

```go
// Tables 显式声明多表查询涉及的表与别名（控制力最强，推荐用于 JOIN 自定义 SQL）。
// 覆盖 Table(name)。第一个为主表（决定 row.Table() 与写路径）。
func (d *Dao) Tables(refs ...TableRef) *Dao {
	d.multi = append(d.multi[:0], refs...)
	return d
}

// AutoTables 让 Dao 在执行时自动解析当前 SQL 的表与别名（opt-in）。
func (d *Dao) AutoTables() *Dao {
	d.autoTables = true
	return d
}
```

`Dao` 字段扩展：

```go
type Dao struct {
	...
	table     string        // 单表 hint（现有）
	multi     []TableRef    // 多表显式 hint（新）
	autoTables bool         // 自动解析开关（新）
}
```

### 9.2 选择优先级（在 `execFind` 等入口计算一次 mapping）

```
显式 Tables(...)        → buildMappingFromRefs(dao.multi, sql)     // 不解析 SQL，只查注册表
否则 Config.AutoTableMapping=true 或 dao.AutoTables()
                        → buildMappingFromSQL(sql)                 // 自动解析
否则 dao.table 非空      → buildSingleTableMapping(dao.table)      // 现状
否则                     → nil（不解码，现状）
```

mapping 按 SQL 串缓存（`sync.Map`），避免每次查询重复解析。

### 9.3 配置开关（`db/config.go`）

```go
type Config struct {
	...
	// AutoTableMapping 让所有原始 SQL 查询自动解析涉及的表并绑定元数据。
	// 默认 false，保持现有行为；设 true 后无需逐次 AutoTables()。
	AutoTableMapping bool
}
```

### 9.4 调用示例

```go
// (a) 最省事：开全局自动解析
db.GetConfig().AutoTableMapping = true
rows, _ := db.RawSql(`SELECT u.*, d.config AS dept_config
                      FROM user u JOIN dept d ON u.dept_id = d.id`).Find()
// dept_config 按 dept 表声明的类型自动解码

// (b) 单次显式声明（不依赖自动解析，最稳）
rows, _ := db.Use().
	Tables(db.TableRef{Table: "user", Alias: "u"}, db.TableRef{Table: "dept", Alias: "d"}).
	RawSql(`SELECT u.*, d.config AS dept_config FROM user u JOIN dept d ON u.dept_id = d.id`).
	Find()

// (c) 单表（现状不变）
rows, _ := db.Use().Table("user").RawSql(`SELECT * FROM user`).Find()
```

Enjoy SQL 模板同样适用：`Sql(...)`/`SqlById(...)` 渲染后的 `sqlPara.Sql` 会被解析（`#para(x)` 已展开成 `?`，不影响 FROM/JOIN 结构）。

---

## 10. 向后兼容性

| 场景 | 现状 | 新方案 |
|------|------|--------|
| `Table(name)` 单表 | 按 `name` 解码 | 完全不变 |
| 无 hint 原始 SQL | 不解码 | 默认仍不解码（仅当 `AutoTableMapping` 或 `AutoTables()` 时才解析） |
| 生成器 typed Dao（`initRow`） | `DecodeJSONFields` 单表 | 不变；`DecodeJSONFields` 保留旧签名 |
| 写路径（Insert/Update/Delete） | 以 `row.table` 为准 | 不变 |
| `decodeRows`/`DecodeJSONFields` 签名 | — | 保留旧签名，内部委托到新增 `*With` 变体 |

唯一行为变化仅在**显式开启**自动解析时发生，且只影响「列恰好命中已注册表 JSON 列」的解码——这是用户期望的新能力，非回归。

---

## 11. 边界与限制

- **派生表 / 子查询列**：无法静态归表，退化为不解码；可用显式 `Tables()` + 在投影里用别名前缀兜底。
- **同名列冲突**：默认主表优先；如需严格，可在 mapping 构造时配置「冲突列不下发」（不解码）。
- **schema 前缀 / 引号**：解析器取叶子表名并去引号，与 `Table.Name` 注册值（叶子名）对齐。
- **方言差异**：解析器只看关键字与标点，不依赖方言；`` ` `` / `"` / `[]` 三种引号都识别。
- **性能**：单次查询只解析一次并按 SQL 串缓存；解析器为线性扫描 + 括号配对，O(n)。
- **写路径**：多表映射是只读的；更新需走单表 typed Dao。

---

## 12. 测试计划

`db/sqlparse` 独立单测（纯字符串、无 DB）：

- FROM 单表 / 多表逗号 / `AS` / 无 `AS` / 引号 / schema 前缀。
- `INNER|LEFT|RIGHT|FULL|CROSS JOIN … ON …` / `USING(...)`。
- `SELECT *` / `u.*` / `u.col` / `col AS label` / 函数 `AS label`。
- 子查询派生表跳过、嵌套括号配对、注释跳过、大小写混用。
- 边界：UNION 取首分支、`SELECT` 无 FROM（`SELECT 1`）、空串。

`db` 包集成测（`_test/db_test` 风格，使用已注册多表）：

- 两表 JOIN，`d.config` JSON 列正确解码为 `dept` 声明类型。
- 显式 `Tables()` vs 自动解析结果一致。
- 同名列冲突 → 主表优先，断言解码归属。
- 无 hint / 未注册表 → 不解码（回归保护）。
- 写路径：对 join 结果行 `row.Table()` 仍是主表，`Update()` 仅作用于主表列。

---

## 13. 实现步骤（建议分 4 步合入）

1. **解析器**：新建 `db/sqlparse`，实现 `Parse(sql) (*Result, error)` + 全量单测。纯函数、零依赖。
2. **mapping 构造**：`db/table_mapping.go`，`buildSingleTableMapping` / `buildMappingFromRefs` / `buildMappingFromSQL`，含 `colOwner` / `mergedFieldTypes` 与缓存。
3. **解码改造**：抽出 `decodeJSONFieldsWith`、`decodeRowsWithMapping`；`decodeRows`/`DecodeJSONFields` 保留旧签名委托；exec 入口按 §9.2 计算 mapping。
4. **API + 开关 + 集成测**：`Dao.Tables/AutoTables`、`Config.AutoTableMapping`、多表集成测试与文档示例。

每步可独立编译、独立测试、独立合入，互不阻塞。

---

## 14. 未来扩展（非本期）

- **生成器集成**：`_dao.af` 增加 `Join()`/`Tables()` 链式助手，生成「多表视图」typed Dao（只读）。
- **嵌套 Row（关系映射）**：`row.Get("dept").(*Row)` 把 join 结果按表拆成子行，接近 ORM relations；可作为本方案的分层增强，先不动当前打平模型。
- **方言感知的列名匹配**：用 `Dialect` 反引号/双引号规则做更精确的引号剥离。
- **映射诊断 API**：`Dao.Explain() *tableMapping`，便于调试列归属。

---

## 附：与现有代码的衔接点速查

| 现有符号 | 位置 | 本期改动 |
|----------|------|----------|
| `Dao.table` | `db/dao.go:16` | 新增 `multi []TableRef`、`autoTables bool` |
| `Dao.Table` | `db/dao.go:38` | 不变；新增 `Tables` / `AutoTables` |
| `decodeRows` / `decodeRow` | `db/executor.go:1031/1051` | 内部委托 `*WithMapping` 变体 |
| `DecodeJSONFields` | `db/json_codec.go:221` | 抽出 `decodeJSONFieldsWith`，新增 `DecodeJSONFieldsWith` |
| `GetTableByName` / `Table.FieldTypes` | `db/table.go` | 解析后多表查询入口复用 |
| `Config` | `db/config.go` | 新增 `AutoTableMapping bool` |
| `db/sql` Enjoy SQL | `db/sql/kit.go` | 无改动（解析发生在渲染后） |
