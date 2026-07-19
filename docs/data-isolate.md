# 数据隔离方案（Data Isolation）

> **数据隔离 = 租户隔离 + 行/列隔离**。以**插件**方式（`plugins/dataisolate`）为 `db` 增加数据隔离能力；`db` 仅接受 **4 项纯新增、向后兼容**的改动，其余全部逻辑落在插件与 `server` 中间件。设计目标：**应用代码除配置外零隔离感知**。
>
> - **租户隔离**（Tenant Isolation）：多租户互不可见。三种策略——库隔离 / Schema 隔离 / 共享表+判别列。共享表策略用 `TenantPolicy`（WHERE `tenant_id=?`）。
> - **行/列隔离**（Row/Column Isolation）：在租户之内，不同用户经同一接口得到**不同结果集**（行）或**不同字段**（列）。用 `DataScopePolicy`（WHERE 范围谓词）与 `FieldMaskPolicy`（SELECT 投影脱敏）。

---

## 目录

1. 概述与设计原则
2. 隔离的两个维度
3. 总体架构
4. db 的必要支持（4 项，纯新增）
5. 主体 Principal
6. Policy 抽象与链
7. AST 改写器（共享机制）
8. 租户隔离
9. 行隔离·数据范围
10. 列隔离·字段权限
11. DbHookKit 集成：各操作的改写分流
12. db.Sql 的处理
13. 批处理的透明化
14. 逃逸口：Bypass / As
15. 配置参考
16. 插件结构（plugins/dataisolate）
17. 应用集成示例
18. 安全与边界考量
19. 测试计划
20. 实现步骤
21. 边界与未来扩展
22. 附：db 衔接点速查

---

## 1. 概述与设计原则

- **插件化**：数据隔离是 `plugins/dataisolate`，核心库（`aifei`/`db`/`enjoy`）保持零外部依赖；SQL 解析依赖（`vitess-sqlparser`）只进插件。
- **db 最小改动**：仅 4 项纯新增（`Dao.Context()`、导出 `SqlAndArgs()`、`db.Batch` 触发 hook、hook veto `Dao.Fail`），全部向后兼容；**仅用库/Schema 隔离时零 db 改动**。
- **透明**：应用照常 `db.WithCtx(ctx)` / `db.NewBatchCtx(ctx)` / `db.Sql(...)`，隔离自动生效；除配置外无感知代码。
- **统一机制**：租户、行范围、字段脱敏都是「从 context 取主体 → 改写 SQL」，共用 Principal / Policy 链 / AST 改写器 / hook。**租户是最简的行 policy**。
- **配置驱动 + 元数据自发现**：哪些表/字段受控由 `db.Table` 注册元数据自动判定，配置只做覆盖。
- **安全优先（fail-closed）**：无法安全解析/改写的语句**报错中止**，绝不静默放过未隔离的查询（原样下发 = 数据泄漏）。仅当「解析成功且确认无受控表/列」（本就无需隔离）才放行。可用 `on_failure: passthrough` 按路径放宽（迁移/特殊语句）。
- **安全职责分离**：插件只做「取值」与「过滤」，不做「认证」——请求可信由应用/网关保证。

---

## 2. 隔离的两个维度

| 维度       | 隔离对象          | 改写什么                                               | 典型场景                | Policy            |
| -------- | ------------- | -------------------------------------------------- | ------------------- | ----------------- |
| **租户隔离** | 租户之间          | WHERE `tenant_id=?`（共享表策略）或 Config 路由（库/Schema 策略） | 多租户互不可见             | `TenantPolicy`    |
| **行隔离**  | 租户内，按用户/部门/角色 | WHERE 范围谓词（本人/本部门/部门树/地区/自定义）                      | 不同用户同接口得到不同结果集      | `DataScopePolicy` |
| **列隔离**  | 租户内，按用户/角色    | SELECT 投影（脱敏/移除字段）                                 | `password` 仅本人或超管可见 | `FieldMaskPolicy` |

> 三者可叠加：一次查询 = `WHERE tenant_id=? AND <范围谓词>` + 投影脱敏。租户是「始终对租户表生效」的硬隔离；行/列是「按角色规则」的软隔离。

---

## 3. 总体架构

```
              HTTP 请求（已认证）
                    │
   ┌────────────────┴────────────────┐
   │ dataisolate.Middleware          │  ← 解析 Principal
   │   Principal{Tenant,User,Dept,   │     （子域/header 仅租户；JWT/session 全量）
   │     DeptTree,Roles,Perms}       │
   │   in.SetContext(WithPrincipal)  │
   └────────────────┬────────────────┘
                    │ in.Context() 携带 Principal
                    ▼
            Service → db.WithCtx(ctx)
                    │
                    ▼
              *Dao（dao.ctx 携带 Principal）
                    │ hook 触发
   ┌────────────────┴────────────────┐
   │ 解析 SQL → AST（一次）         │
   │ Policy 链依次改写 AST：        │
   │   ① FieldMask（投影脱敏）      │
   │   ② Tenant（WHERE）            │
   │   ③ DataScope（WHERE 范围）    │
   │ 重建 SQL + 参数重排            │
   └─────────────────────────────────┘
```

- **Principal**：当前用户完整身份（租户 + 用户 + 部门 + 部门树 + 角色 + 权限），进 context。
- **Policy 链**：每条 policy 据 Principal + 规则改写 AST 的一部分；互不冲突（投影与 WHERE 正交，WHERE 间用 AND 合并）。
- **改写器**：解析一次、链式改写、重建一次 + 参数重排。

---

## 4. db 的必要支持（4 项，纯新增）

这是本方案对 `db` 的**全部**改动。仅用库/Schema 隔离（策略①/②）时一项都不需要。

### 4.1 访问器：让 hook 能读 ctx 与当前 SQL

`db/hook.go` 的 hook 注释已把「prevent missing WHERE clause」「soft delete」列为用例——这些**本就需要读 SQL**。当前 `Dao.ctx`、`Dao.sqlAndArgs()` 均非导出，外部插件读不到。补两个对称 getter（`db/dao.go`）：

```go
// Context 返回绑定到本 Dao 的 context（由 Dao.Ctx / db.WithCtx 设置，事务时携带 *sql.Tx）。
// hook 据此读取主体（Principal）。未绑定时返回 nil。
func (d *Dao) Context() context.Context { return d.ctx }

// SqlAndArgs 返回当前已暂存、即将下发的 SQL 与参数（既有内部 sqlAndArgs 的导出版本）。
// hook 据此在执行前检视/改写语句；随后用 dao.SqlPara(sp) 写回改写结果。
func (d *Dao) SqlAndArgs() (string, []interface{}) {
    if d.sqlPara != nil {
        return d.sqlPara.Sql, d.sqlPara.Paras
    }
    return d.sqlStr, d.sqlArgs
}
```

> `SqlAndArgs` 仅导出现成内部函数 `sqlAndArgs`；`Context()` 与既有 setter `Ctx(ctx)` 对称（`Ctx` 名被 setter 占用，getter 取名 `Context`）。

### 4.2 批处理触发 hook

单条执行天然走 `exec*` 触发 hook；但 `db.Batch` 是并列的**第二条执行面**，原本不经 `exec*`、不触发任何 hook（见 §13）。为满足透明化，需让 `db.Batch` 在执行前对每行/每组 SQL 同样触发 `DbHookKit`。

> 以上 4 项**均为纯新增**：不删改既有符号、无 HookKit 时行为与今天完全一致。

### 4.3 hook veto：fail-closed 的中止能力

为支持 fail-closed（§1），Before hook 在「无法安全解析/改写」时需能**中止语句**、返回错误，而非原样放行（放行 = 未隔离查询执行 = 数据泄漏）。当前 hook 返回 `interface{}`（仅作 After 回调 state），无错误通道。补一个 veto（`db/dao.go`）：

```go
type Dao struct {
    ...
    failErr error   // hook veto：非空时 runner() 返回该错误，中止语句
}

// Fail 标记本 Dao 的语句应中止。Before hook 在无法安全改写时调用。
func (d *Dao) Fail(err error) { if d.failErr == nil { d.failErr = err } }

// runner 增加 veto 检查（各 executor 已检查 runner() 的 error，故调用点零改动）：
func (d *Dao) runner() (DBConn, error) {
    if d.failErr != nil { return nil, d.failErr }
    return d.config.runner(d.ctx)
}
```

> 关键：hook 在 `dao.runner()` **之前**触发（见 `db/executor.go` 的 `Before*` → `dao.runner()` 顺序），故 hook 调 `dao.Fail(err)` 后，紧接的 `dao.runner()` 即返回该错误，executor 把它向上抛给调用方（Service → `server.Fail` 响应）。**executor 代码零改动**（它们本就检查 `runner()` 的 error）。

---

## 5. 主体 Principal

`plugins/dataisolate/principal.go`：把「租户 id」升级为完整主体，仍是 context 载体（goroutine 安全）。

```go
package dataisolate

type Principal struct {
    TenantID string        // 租户（TenantPolicy 用）
    UserID   any           // 用户 id（int/string）
    UserName string
    DeptID   any           // 部门 id
    DeptTree []any         // 本部门 + 子部门树（预解析，避免改写时递归查库）
    RegionID any           // 地区（ScopeRegion 用）
    Roles    []string      // 角色（规则匹配）
    Perms    []string      // 权限点（细粒度规则匹配）
}

type principalKey struct{}

func WithPrincipal(ctx context.Context, p *Principal) context.Context {
    if ctx == nil { ctx = context.Background() }
    return context.WithValue(ctx, principalKey{}, p)
}
func PrincipalFrom(ctx context.Context) (*Principal, bool) {
    if ctx == nil { return nil, false }
    p, ok := ctx.Value(principalKey{}).(*Principal)
    return p, ok && p != nil
}
```

**解析**（`resolver.go` + `middleware.go`）：`PrincipalResolver` 接口，应用可换实现。

- 内置 `SubdomainHeaderResolver`：从 Host 子域 + 请求头取，**仅填 TenantID**（安全由应用自负，插件不鉴权）。
- 应用提供 `JWTResolver`/`SessionResolver`：从已认证 token/session 填**全部字段**（含 DeptTree 闭包预算）。

```go
type ctxSetter interface{ SetContext(context.Context) }

func Middleware(opts ...Option) aifei.Handler {
    cfg := resolveCfg(opts)
    return func(next aifei.HandlerFunc) aifei.HandlerFunc {
        return func(in aifei.Input) aifei.Output {
            p := cfg.Resolver.Resolve(in)
            if p == nil {
                if cfg.Enforce { return server.Fail("missing principal") }
                return next(in)
            }
            ctx := WithPrincipal(in.Context(), p)
            if s, ok := in.(ctxSetter); ok { s.SetContext(ctx) }
            return next(in)
        }
    }
}
```

接入：`app.Use(server.Logger(), server.Recover(), dataisolate.Middleware())`。与 `TxInterceptor()` 协同：后者以 `in.Context()` 为父派生事务 ctx，Principal 随之透传。

---

## 6. Policy 抽象与链

`plugins/dataisolate/policy.go`：每条 policy 在 AST 上改写，并通过 `ParamCollector` 登记参数（保证重建后占位与参数对齐）。

```go
// Policy 改写一条已解析语句的某一面。
type Policy interface {
    Name() string
    // Apply 就地改写 stmt；通过 pc 登记新增参数（按遍历序）。返回 changed。
    Apply(stmt sqlparser.Statement, p *Principal, pc *ParamCollector) bool
}
type PolicyChain []Policy
```

链顺序约定（投影与 WHERE 正交，WHERE 间互不影响）：

1. `FieldMaskPolicy`（改 SELECT 投影）—— 先做，确定最终列集。
2. `TenantPolicy`（WHERE `tenant_id=?`）。
3. `DataScopePolicy`（WHERE 范围谓词）。

WHERE 谓词以 `AND` 合并：`... WHERE tenant_id=? AND <范围谓词> ...`。

---

## 7. AST 改写器（共享机制）

引入成熟的 SQL 解析库，把 SQL 解析成 AST，在 AST 层精确改写再重建。两类改写的**递归策略不同**：**WHERE 注入**（租户/范围）须**递归**进入子查询 / UNION 各分支 / CTE（每次受控表访问都要过滤）；**投影改写**（字段脱敏）**只作用最外层 SELECT**，不递归（见 §10.2）。两者都按**表别名**限定，并**保证 `?` 占位与参数顺序始终对齐**。

### 7.1 选库

| 库                                                 | 采用                                                                                  |
| ------------------------------------------------- | ----------------------------------------------------------------------------------- |
| **`github.com/blastrain/vitess-sqlparser`**（默认推荐） | 独立、纯 Go、持续维护；Vitess 本就为「查询改写」而生（分片键注入），`Parse`→改 AST→`String()` + `WalkSubtree` 最贴合 |
| `github.com/pingcap/tidb/pkg/parser`              | 备选，MySQL 8.0 最全但更重                                                                  |
| ~~`github.com/xwb1989/sqlparser`~~                | 已停维护，勿用                                                                             |
| `github.com/pganalyze/pg_query_go`（可选，PG）         | 精确但 cgo；仅 PG 专用语法必须解析时按方言启用                                                         |

### 7.2 API

```go
type Status int
const (
    StatusRewritten     Status = iota // 已改写
    StatusSkippedNoScoped            // 解析成功，但本语句无受控表/列 → 无需隔离，放行
    StatusFailed                     // 解析失败/受控表无法安全改写 → fail-closed，hook 应中止
)

// Rewrite 解析 sql，跑 Policy 链改写 AST，重建 sql 并重排 args 使占位与参数对齐。
// StatusSkippedNoScoped：无需隔离，原样返回；StatusFailed：无法安全处理，由 hook 调 dao.Fail 中止。
func Rewrite(sql string, args []interface{}, p *Principal, chain PolicyChain) (string, []interface{}, Status)
```

### 7.3 实现要点（以 vitess-sqlparser 为例）

1. `stmt, err := sqlparser.Parse(sql)`；`err != nil` → `StatusFailed`（fail-closed，勿 panic，由 hook 中止）。
2. 依次跑 `PolicyChain`：WHERE 注入类 policy（Tenant/DataScope）经 `WalkSubtree` **递归**遍历（含子查询/UNION/CTE）；`FieldMaskPolicy` **只访问最外层 `SelectExprs`**，不递归（见 §10.2）。各 policy 通过 `ParamCollector` 登记新参数。
3. **参数重排（关键）**：原 `?` 是按源序的 bind 节点——遍历时按序绑定到 `args[i]`；新注入谓词/脱敏用一个新 bind 节点、把值直接挂其上。改写完成后按遍历序收集所有 bind 节点 → 输出重建 `sql` 与对齐的 `args`。**这是 AST 方案相对字符串注入的根本优势**。
4. `sqlOut := sqlparser.String(stmt)`（规范化重建）。

> **方言边界**：hook 层看到的已是 `?` 占位 SQL（aifei 按方言渲染成 `?`），`$1`/命名占位不成问题；但 PG 专用语法（`::`、`RETURNING`、`ARRAY[...]`、`INTERVAL`）可能解析失败 → **fail-closed 报错**（无法证明该语句已隔离，宁可中止也不放行）。DDL 类语句通常能正常解析且无受控表 → `StatusSkippedNoScoped` 放行，不受影响。SQL 注入安全：值只进 bind 节点（最终是 `?` + 参数），绝不字符串拼接。

---

## 8. 租户隔离

### 8.1 三种租户隔离策略

| 策略                                 | 隔离方式                 | 改写 SQL            | db 改动 | 适用               |
| ---------------------------------- | -------------------- | ----------------- | ----- | ---------------- |
| **① 库隔离**（Database-per-tenant）     | 每租户一个独立 DB           | 否                 | 零     | 强隔离、租户少、可独立备份/迁移 |
| **② Schema 隔离**（Schema-per-tenant） | 共库、每租户一个 schema      | 否（方言层）            | 零     | 中等隔离、共库省运维       |
| **③ 共享表 + 判别列**（Discriminator）     | 共库共表，靠 `tenant_id` 列 | 是（`TenantPolicy`） | 4 项   | 租户多、省库资源、最常见     |

- 要求**完全不修改 `db`** → ① 或 ②，复用 `db` 既有**多命名 `Config`**（`InitWithID`/`GetConfig(id)`/`UseWithID`）。
- 要**省库资源 + 自动判别列** → ③。
- 三者可混用（不同库/表不同策略）。

### 8.2 策略①/②：库 / Schema 隔离路由（零 db 改动）

为每个租户初始化一个命名 `Config`：

```go
db.InitWithID("tenant_acme",  "mysql", dsnAcme)
db.InitWithID("tenant_globex", "mysql", dsnGlobex)
// Schema 隔离（PG）：同 DSN + currentSchema，或在方言/连接初始化层切 search_path
```

插件侧路由（`use.go`）：

```go
// Use 返回当前请求租户对应的 Dao（ctx 解析租户 → configID → db.UseWithID）。
func Use(ctx context.Context) *db.Dao {
    p, ok := PrincipalFrom(ctx)
    if !ok || p.TenantID == "" { return db.Use() }
    cfgID := defaultRouter.ConfigID(p.TenantID)    // tid → "tenant_acme"
    return db.UseWithID(cfgID).Ctx(ctx)            // 仍透传 ctx 以参与事务
}
```

`Manager` 维护 `tenants map[tenantID]→ConfigID`，由 `tenant.tenants.<id>.config` 配置驱动。**不改写任何 SQL**，隔离强度最高。

### 8.3 策略③：TenantPolicy（WHERE `tenant_id=?`）

共享表策略。插件 `Start()` 把覆盖全部 6 类 hook 的 `DbHookKit` 装到目标 `db.Config`。

**挂载点**：`db/executor.go` 在每个执行器里，调用 `Before*` 之后**回读** `dao.sqlPara` 作为真正下发的 SQL；`Dao` 提供 setter `dao.SqlPara(sp)`。故 **hook 把改写后的 `SqlPara` 设回 `dao`，执行器即用改写后的 SQL**——`db` 无需为此改动。

**TenantPolicy.Apply**：对每个租户表注入 `<alias>.tenant_id = ?`（无 WHERE 则新建），递归进入子查询/UNION/CTE，参数经 `ParamCollector` 登记。

**INSERT 行盖章**：不走 SQL 解析。`BeforeRowInsert(dao, row)` 拿到 `*Row`，直接 `row.Set("tenant_id", tid)` 让该列随 INSERT 写入。`InsertOrUpdateRow` 同走此路。

**UPDATE/DELETE**：不要 `row.Set("tenant_id")`（进 SET 子句、改变主键判定）；统一在 WHERE 注入 `AND tenant_id=?`，把更新/删除**限定在本租户行内**，防越权。

### 8.4 标记租户表（默认零配置）

**元数据自发现（主）**：生成器在 `init()` 把每张表的**完整列元数据**注册进 `db`（`db.RegisterTable` → `db.GetTableByName`；`FieldTypes` 含全部列）。故「是否租户表」由**该表是否声明了租户列**决定：

```go
func isTenantTable(name, col string) bool {
    t := db.GetTableByName(name)
    if t == nil { return false }          // 未注册 → 交给 mode
    _, ok := t.FieldTypes[col]            // col 默认 "tenant_id"
    return ok
}
```

与 typed Dao + Table 注册表一脉相承，**改 schema 即生效**。配置覆盖（`tenant.scope`）：`ignore_tables`（豁免全局/跨租户表）、`tables`（强制纳入）、`mode`（`auto` 默认｜`whitelist`｜`all`）。

**判定顺序**：`ignore_tables` 优先 → `tables` 强制 → mode → 元数据。

---

## 9. 行隔离·数据范围（DataScopePolicy）

在租户之内，按用户/部门/角色限定可见行。规则信息分三层（配置哲学见 §15）：

- **范围类型**（Self/Dept/…）—— **动态**：由**应用实现**的 `ScopeRuleProvider` 按 `(表, Principal)` 运行时解析（数据源与表结构由应用自定）。
- **身份列名**（creator/dept/region 列叫什么）—— **稳定**：按表注册（`RegisterTableMeta`）或按约定自发现。
- 列值（UserID/DeptID/DeptTree/RegionID）—— 来自 `Principal`。

### 9.1 范围类型（动态决策）

```go
type DataScopeType int
const (
    ScopeAll          DataScopeType = iota // 全部：不加谓词
    ScopeSelf                              // 本人：creator = ?
    ScopeDept                              // 本部门：dept = ?
    ScopeDeptAndBelow                      // 本部门及以下：dept IN (?)
    ScopeRegion                            // 某地区：region = ?
    ScopeCustom                             // 自定义范围：Column Op Values（结构化，非裸 SQL）
)
// ScopeOp：自定义字段操作符（枚举，非字符串，防注入）。
type ScopeOp int
const (
    OpEq ScopeOp = iota  // =
    OpNeq                // <>
    OpIn                 // IN (...)
    OpNotIn              // NOT IN (...)
    OpLike               // LIKE
    OpLt, OpLte, OpGt, OpGte
    OpBetween            // BETWEEN ? AND ?
)
// ScopeRule 是按 (表, Principal) 解析出的范围决策（动态）。
// 预设档（Self/Dept/…）的列名与值由 TableMeta + Principal 隐式绑定；
// ScopeCustom 显式指定「字段 + 操作符 + 值」，值由 provider 按 Principal 算好传入。
type ScopeRule struct {
    Type DataScopeType
    // Type=ScopeCustom 时：结构化自定义（字段 + 操作符 + 值）。
    Column string
    Op     ScopeOp
    Values []any   // 单值（=/<>）或多值（IN）/双值（BETWEEN）
}
```

### 9.2 身份列注册（`table_meta.go`，稳定）

不同表的列名各异（`created_by`/`creator_id`/`uid`、`dept_code`/`dept_id`、…）。**按表注册**，或按约定自发现：

```go
type TableMeta struct {
    TenantCol, CreatorCol, DeptCol, RegionCol string
}
// RegisterTableMeta 显式注册某表的身分列（schema 稳定，启动时注册一次）。
func RegisterTableMeta(table string, m TableMeta)
// TableMetaOf 解析身分列：显式注册 > 约定匹配 > 空。
func TableMetaOf(table string) TableMeta
```

> 约定自发现：未显式注册时，按可配列名约定从 `db.Table.FieldTypes` 匹配（如 creator ∈ {creator_id, created_by, create_by, owner_id}，dept ∈ {dept_id, dept_code, department_id, org_id}，region ∈ {region_id, region_code, area_id}，tenant ∈ {tenant_id, tenant_code, tid}）。非约定列名须 `RegisterTableMeta` 显式声明。

### 9.3 ScopeRuleProvider（动态）

```go
// ScopeRuleProvider 按 (表, Principal) 返回范围类型。插件只定义接口；实现与数据源（表/外部服务）由应用提供。
type ScopeRuleProvider interface {
    ScopeRule(table string, p *Principal) (ScopeRule, bool)
}
```

- **插件只定义接口**；实现、底层规则表/外部策略源、缓存与刷新**全部由应用负责**。插件不内置任何动态规则实现，也不规定表结构（表名/字段由应用自定）。
- **启用即须注册**：`policies` 含 `scope` 时，应用须注册 `ScopeRuleProvider`；未注册则该 policy 空转（不注入范围谓词）。
- **性能职责在应用**：插件按查询调用 provider，故 provider 实现应自带缓存（按 `(table, roles)`），避免每查询查库。
- **多角色合并**：取**最宽**档生效（viewer=本人 + admin=全部 → 全部；`scope.merge: strict` 取最窄）——`merge` 是稳定全局策略（§15）。
- **哪些表有数据范围**：预设档需 `TableMetaOf(table)` 有对应列；`ScopeCustom` 由 provider 对该表返回规则即纳入（其 `Column` 须是该表注册列）。

### 9.4 改写

`DataScopePolicy.Apply` 构造谓词：预设档用 `TableMeta` 列名 + `Principal` 值；`ScopeCustom` 用规则里的 `Column`+`Values`（provider 已按 Principal 算好）：

| 类型           | 注入谓词                      | 参数          |
| ------------ | ------------------------- | ----------- |
| All          | （无）                       | —           |
| Self         | `<creator_col> = ?`       | UserID      |
| Dept         | `<dept_col> = ?`          | DeptID      |
| DeptAndBelow | `<dept_col> IN (?, ?, …)` | DeptTree... |
| Region       | `<region_col> = ?`        | RegionID    |
| Custom       | `<column> <op> ?`（或 `IN (…)` / `BETWEEN ? AND ?`） | Values... |

> `ScopeCustom.Column` 须是该表注册列（校验 `db.Table.FieldTypes`，防列名注入）；`Op` 为枚举；值只走参数占位 `?`，绝不字符串拼接。

与租户组合：`WHERE tenant_id=? AND <creator_col>=?`，参数重排对齐。

### 9.5 关键约束

- **DeptTree 预解析**：部门的「本部门及以下」必须在中间件里算成 id 列表放进 `Principal.DeptTree`，**不能在改写时递归查库**（否则 N+1）。
- **Principal 缺失**：规则需 UserID 等却缺失 → 按 `enforce` 处理：`false`（默认）跳过该规则，`true` 报错（生产建议 `true`，与 fail-closed 一致）。

---

## 10. 列隔离·字段权限（FieldMaskPolicy）

在租户之内，按用户/角色脱敏字段。这是相对租户**真正新增**的改写能力：改 SELECT 投影，而非 WHERE。

### 10.1 规则模型

```go
type FieldMode int
const (
    FieldAllowlist FieldMode = iota // Fields 为允许列（其余脱敏/移除）
    FieldDenylist                   // Fields 为禁止列
)
type MaskStrategy int
const (
    MaskNull      MaskStrategy = iota // NULL AS col（默认，保留形状）
    MaskConstant                       // <常量> AS col
    MaskRemove                         // 直接移除该列
)
type FieldRule struct {
    Mode     FieldMode
    Fields   []string
    Mask     MaskStrategy   // 默认 MaskNull
    Constant any            // Mask=MaskConstant 时
}
type FieldRuleProvider interface {
    Rule(table string, p *Principal) (FieldRule, bool)
}
```

> **默认脱敏策略 = `MaskNull`（`NULL AS col`），保留列形状**——下游 `row.Get("password")` 与生成的 typed Dao getter 仍能取到列（值为零值），不破坏兼容。逐规则可改 `MaskRemove`/`MaskConstant`。

> 字段规则**动态**：由**应用实现**的 `FieldRuleProvider` 按 `(表, Principal)` 运行时解析（数据源/表结构由应用自定）；插件只定义接口。`field.default_mask`（§15）仅作全局默认脱敏策略，逐规则可在 `FieldRule.Mask` 覆盖。同 §9.3：启用即须注册、缓存由应用负责。

### 10.2 投影改写

`FieldMaskPolicy.Apply` **只改写最外层 SELECT 的 `SelectExprs`**，**不递归**进入子查询 / UNION 各分支 / CTE 体 / 标量子查询。原因：内层投影的列常被外层引用（`IN/EXISTS` 子查询、JOIN/条件依赖、UNION 各分支列数须对齐）；改写内层会破坏这些约束、生成异常 SQL。脱敏只需作用在最终返回调用方的最外层投影——内层中间结果不外泄。具体改写：

- **`SELECT *` / `t.*` 展开**：用注册的 `db.Table.Fields`（生成器已提供全列）展开为显式列，再套字段规则。
- **显式 `t.col` / 裸 `col`（属该表）**：按规则处理——允许则保留；禁止·脱敏则替换为 `NULL AS col`（或 `? AS col` 常量）**保留别名**；禁止·移除则从投影剔除。
- **JOIN**：按「表.字段」逐表套规则；裸列归属用别名解析。
- **不可过滤**：`COUNT(*)`/聚合/表达式列（含标量子查询作投影，如 `(SELECT salary…) AS sal`）、`SELECT 1`、无 FROM → 该列不脱敏（逐列 best-effort，非语句级失败；这类列通常非敏感）。
- **只作用于 SELECT**：INSERT/UPDATE/DELETE 不投影（`RETURNING` 属边角）。

### 10.3 列归属与未注册表

列归属依赖注册 `db.Table`；未注册表的 `*` 无法展开 → **跳过该表的字段过滤**（逐表 best-effort，不报错；若该表配了字段规则，建议先注册元数据以免漏脱敏）。裸列在多表 JOIN 下归属歧义 → 主表优先。

---

## 11. DbHookKit 集成：各操作的改写分流

一个 `hookKit` 实现全部 6 类 hook，按操作分流 policy：

| 操作                                          | 触发 hook    | Tenant            | DataScope                                    | FieldMask  |
| ------------------------------------------- | ---------- | ----------------- | -------------------------------------------- | ---------- |
| **INSERT**（`BeforeRowInsert`）               | 行盖章        | 盖 `tenant_id`     | 盖 `creator_id`/`dept_id`（供后续 SELF/DEPT 查询命中） | —          |
| **UPDATE/DELETE**（`BeforeSqlUpdate/Delete`） | WHERE 注入   | `AND tenant_id=?` | `AND <范围谓词>`（限本租户+本人/本部门，防越权）                | —          |
| **SELECT**（`BeforeFind/Query`，Paginate 两段）  | 投影 + WHERE | `AND tenant_id=?` | `AND <范围谓词>`                                 | 改投影（脱敏/移除） |

要点：

- **INSERT 行盖章**：TenantPolicy 盖 `tenant_id`，DataScopePolicy 盖 `creator_id`/`dept_id`（若表有这些列且有规则）——插入的数据后续能被 SELF/DEPT 查询命中。不碰 SQL 文本。
- **UPDATE/DELETE**：**不要** `row.Set` 范围列（进 SET）；统一 WHERE 注入，防越权改/删。
- **SELECT**：先投影改写，再 WHERE 注入。
- 其余 `After*` / `RowUpdate/Delete` 的 `Before*` 返回 nil 即可。

`hookKit.applyRead/applyWrite` 读 `dao.Context()` 取 Principal、`dao.SqlAndArgs()` 取 SQL，解析后跑 Policy 链，重建后 `dao.SqlPara(sp)` 写回。**fail-closed**：`Rewrite` 返回 `StatusFailed`（解析失败/受控表无法安全改写）时调 `dao.Fail(err)` 中止语句（§4.3），绝不原样放行未隔离查询；`StatusSkippedNoScoped`（无受控表）正常放行。**双重注入守卫**：改写前 `containsColumnWord(sql, col)` 检查——SQL 已含该列 token 就跳过（防与 db.Sql 模板里的 `#and` 撞车，见 §12）。与既有 `HookKit` 经 `compose.go` 链式合并。

```go
switch st {
case rewriter.StatusRewritten:
    dao.SqlPara(&dbsql.SqlPara{Sql: sql2, Paras: args2})
case rewriter.StatusSkippedNoScoped:
    // 无受控表/列，正常放行
case rewriter.StatusFailed:
    if cfg.OnFailure == "passthrough" {
        h.log.Warn("dataisolate: rewrite failed, passthrough: %s", sql)
    } else {
        dao.Fail(fmt.Errorf("dataisolate: cannot safely rewrite: %s", sql)) // fail-closed 中止
    }
}
```

---

## 12. db.Sql 的处理

`db.Sql` / `SqlById` 走 Enjoy SQL，渲染出的是**手写 SQL**。两条路径：

1. **hook 路径（透明，对所有 db.Sql 生效）**：`Dao.Sql()` 设 `sqlPara` 后，`.Find()` 走 `execFind(d, d.isRawSQL())`，`isRawSQL()` 对 `db.Sql` 为 `true`，于是 `BeforeFind`+`BeforeQuery` 都触发（`Update/Delete/Query` 同理）。bypass 在最前面短路。
2. **指令路径（精确，适合静态场景）**：`#and(field, operator, para)`（`db/sql/condition.go`）值取自作用域，**值为空自动跳过**：

```sql
#sql("listOrders")
  SELECT * FROM orders
  #where(1, "=", 1)
    #and(status, "=", status)
    #and(tenant_id, "=", tenantId)      -- 租户过滤；空则省略
    #and(creator_id, "=", userId)       -- 数据范围（静态写法）
  #end
#end
```

`#and` 指令**只看 data map，拿不到 context.Context**——故 Bypass/As 必须由 `dataisolate.Sql` helper 在「写作用域变量」时落实：

```go
// plugins/dataisolate/sql.go
const VarTenant = "tenantId"

func Sql(ctx context.Context, sqlStr string, data map[string]interface{}) *db.Dao {
    p, _ := PrincipalFrom(ctx)
    if data == nil { data = map[string]interface{}{} }
    if IsBypass(ctx) {
        data[VarTenant] = nil            // #and 见空即省略
    } else if p != nil {
        data[VarTenant] = p.TenantID
        data["userId"] = p.UserID        // 供 #and(creator_id,"=",userId)
    }
    return db.WithCtx(ctx).Sql(sqlStr, data)
}
// SqlById(ctx, id, data) / SqlWithArgs(...) 同理
```

> **数据范围/字段权限是规则驱动的**（哪条规则取决于角色），天然走 **hook**；指令路径只适合静态租户/范围，**字段脱敏无指令等价物**，必走 hook。建议 db.Sql + 隔离统一走 `dataisolate.Sql` helper，让 Bypass/As 语义对齐。

---

## 13. 批处理的透明化

`db.Batch`（`db/batch.go`）原与 `exec*` 并列、不经执行器、不触发 DbHookKit。为满足「除配置外无隔离感知」，**让 `db.Batch` 在执行前同样触发 DbHookKit**（§4.2 的 db 改动，纯新增，无 HookKit 时行为不变）：

- **行式批插入**（`Insert/InsertWithTable/InsertGroup`）：在 `groupRowsForInsert` **之前**，对每行触发 `InsertHook.BeforeRowInsert(synthDao, row)`，`synthDao := &Dao{config: b.config, ctx: b.ctx}`。hook 给行盖 `tenant_id`/`creator_id`/`dept_id` → 随后被 `filterTableFields` 纳入 `g.fields` → 进预编译 INSERT 列与 args。批插只取 InsertHook 的「行变更」语义，不消费 sqlPara。
- **批更新/删除**（`Update/UpdateWithTable/UpdateGroup`）：每个 group 用 `Dialect` 生成基础 SQL 置入 `synthDao.sqlPara`，触发 `BeforeSqlUpdate`/`BeforeSqlDelete` → 读回改写后的 `sqlPara.Sql`（已注入 `AND tenant_id=? AND <范围>`）与 `sqlPara.Paras`（尾随参数）；`Prepare(sqlPara.Sql)`，每行 `execArgs = perRowArgs + sqlPara.Paras`。

  > 基础 SQL 的既有 `?` 是**每行模板槽**（非预绑定值），故改写器只追加尾随谓词 + 返回尾随参数，不重排既有 `?`。
- **`Execute(sql, argsList)`**：对 sql 触发一次改写，`Prepare(改写后)`，每组 args 追加 `sqlPara.Paras`。caveat：sql 是 INSERT 时 WHERE 注入不适用——用行式 `Batch.Insert`。
- **`ExecuteSQLs(sqls)`**：逐条经改写器再 `r.Exec`。

字段权限不作用于批处理（无投影）。`Bypass`/`As` 经 `synthDao.ctx` 同样生效。

---

## 14. 逃逸口：Bypass / As

某些操作需绕过隔离（种子/迁移/系统表/跨租户导入），或以指定主体身份操作（超管代写）。**用 context 标记，作用于整条 Policy 链**：

```go
type bypassKey struct{}

func Bypass(ctx context.Context) context.Context { return context.WithValue(ctx, bypassKey{}, true) }
func IsBypass(ctx context.Context) bool { v, _ := ctx.Value(bypassKey{}).(bool); return v }
func As(ctx context.Context, p *Principal) context.Context { return WithPrincipal(ctx, p) }
```

hook 最前面短路：

```go
func (h *hookKit) principalOf(dao *db.Dao) (*Principal, bool) {
    ctx := dao.Context()
    if ctx == nil { return nil, false }
    if IsBypass(ctx) { return nil, true }            // bypass
    p, _ := PrincipalFrom(ctx)
    return p, false
}
```

用法（**仅作用单次调用**；勿放请求级中间件，否则整条请求脱钩）：

```go
db.WithCtx(dataisolate.Bypass(in.Context())).Insert(row)          // 种子/迁移/系统表
db.WithCtx(dataisolate.As(in.Context(), adminPrincipal)).Find()   // 超管见全部
```

**安全闸**：`dataisolate.allow_bypass: false`（默认）使 `Bypass()` 在严格部署降级为 no-op/panic；每次 bypass 打日志。最强隔离：跨租户运维走**单独的、未装 HookKit 的 `db.Config`**。

---

## 15. 配置参考

```yaml
dataisolate:
  # —— 稳定的全局开关（仅这些进 app.yml）——
  policies: [field, tenant, scope]   # 启用的 Policy 链（顺序：投影先于 WHERE）
  enforce: false                     # 命中受控项却无 Principal 时是否报错（默认 false=跳过）
  allow_bypass: false                # 是否允许 dataisolate.Bypass(ctx) 逃逸口
  on_failure: error                  # 无法安全改写时：error（默认，fail-closed）| passthrough
  configs: ["main"]                  # 装 hook 的 db.Config id 列表（策略③/行/列用）

  principal:
    resolver: subdomain_header       # Principal 解析（应用可换 jwt/session）

  tenant:                            # 租户隔离（TenantPolicy）
    strategy: shared                 # database | schema | shared
    column: tenant_id                # 租户列全局默认（按表可在 TableMeta 覆盖）
    scope: { mode: auto, ignore_tables: [sys_dict, region, sys_log] }
    tenants:                         # 策略①/②：租户 → config（稳定映射）
      acme:    { config: tenant_acme }
      globex:  { config: tenant_globex }

  scope:
    merge: broadest                  # 多角色取最宽档（broadest|strict）——稳定全局策略

  field:
    default_mask: null               # 全局默认脱敏：null（保留形状）| constant | remove

  # —— 以下不进 app.yml，走「注册」或「动态」——
  # • 身份列名（每表 creator/dept/region/tenant 列）：启动时 RegisterTableMeta（§9.2），或按约定自发现。
  # • 范围类型（每 (表,角色) 是 self/dept/…）：应用实现的 ScopeRuleProvider 运行时解析（§9.3）。
  # • 字段规则（每 (表,角色) 允许/禁止哪些列 + 脱敏）：应用实现的 FieldRuleProvider 运行时解析（§10）。
```

> **配置哲学**：app.yml 只放**最重要且不变**的规则（全局开关、稳定租户映射、列默认、合并与脱敏策略）。**按表且稳定**的（身份列名）走注册（§9.2）；**按 (表,角色) 且会变**的（范围类型、字段规则）走**应用实现**的 `RuleProvider` 动态解析（§9.3/§10，插件只出接口）。只用租户：`policies: [tenant]` + `tenant.*`。`LoadConfig` 仿 `plugins/storage/config.go`，用 `config.GetStr` + `config.SubBind`。

---

## 16. 插件结构（plugins/dataisolate）

```
plugins/dataisolate/
├── go.mod                 # module .../plugins/dataisolate；require aifei/config/log/db/http/server + vitess-sqlparser
├── plugin.go              # Plugin 实现 aifei.Plugin（Start 装 HookKit）
├── principal.go           # Principal + WithPrincipal/PrincipalFrom
├── middleware.go          # Middleware()：解析 Principal → in.SetContext
├── resolver.go            # PrincipalResolver（subdomain_header/jwt/session；应用可换）
├── policy.go              # Policy 接口 + PolicyChain + ParamCollector
├── rewriter.go            # AST 改写（WHERE 注入 + 投影改写）+ 参数重排
├── tenant.go              # TenantPolicy（租户·WHERE）
├── scope.go               # DataScopePolicy + ScopeRule + ScopeRuleProvider（行·WHERE）
├── field.go               # FieldMaskPolicy + FieldRule + FieldRuleProvider（列·投影）
├── table_meta.go          # TableMeta/RegisterTableMeta（身分列）+ isTenantTable/isDataScopeTable
├── hook.go                # DbHookKit：跑 Policy 链，按操作分流
├── compose.go             # 与既有 HookKit 链式合并
├── context.go             # Bypass/As/IsBypass
├── sql.go                 # dataisolate.Sql/SqlById：注 Principal 变量进 data map
├── use.go                 # 策略①/②：Use(ctx) → 按 TenantID 路由 db.Config
└── type.go                # Principal/Rule/Status 等类型与常量
```

`go.mod`（对齐 `plugins/storage/go.mod`，内部依赖走 `replace ../../*`）：

```go
module github.com/crazy-airhead/aifei-go/plugins/dataisolate

go 1.26

require (
    github.com/crazy-airhead/aifei-go/aifei v0.0.41
    github.com/crazy-airhead/aifei-go/config v0.0.41
    github.com/crazy-airhead/aifei-go/db v0.0.41
    github.com/crazy-airhead/aifei-go/http v0.0.41
    github.com/crazy-airhead/aifei-go/log v0.0.41
    github.com/crazy-airhead/aifei-go/server v0.0.41
    github.com/blastrain/vitess-sqlparser <latest>   # SQL AST 解析（§7）；版本由 go get 确定
)

replace (
    github.com/crazy-airhead/aifei-go/aifei => ../../aifei
    github.com/crazy-airhead/aifei-go/config => ../../config
    github.com/crazy-airhead/aifei-go/db => ../../db
    github.com/crazy-airhead/aifei-go/http => ../../http
    github.com/crazy-airhead/aifei-go/log => ../../log
    github.com/crazy-airhead/aifei-go/server => ../../server
)
```

`go.work` 的 `use (...)` 块新增 `./plugins/dataisolate`。

`Plugin` 主体（仿 `plugins/storage/plugin.go`）：

```go
var _ aifei.Plugin = (*Plugin)(nil)

type Plugin struct {
    prefix string
    log    log.Logger
    mgr    *Manager
}

func NewPlugin(logger log.Logger, prefix ...string) (*Plugin, error) {
    p := "dataisolate"
    if len(prefix) > 0 && prefix[0] != "" { p = prefix[0] }
    if logger == nil { logger = log.Default() }
    return &Plugin{prefix: p, log: logger}, nil
}

func (p *Plugin) Start() error {
    cfg, err := LoadConfig(p.prefix)
    if err != nil { return err }
    mgr, err := NewManager(cfg, p.log)
    if err != nil { return err }
    p.mgr = mgr
    SetDefault(mgr)
    if needsHookKit(cfg) {                 // 策略③ 或 启用 scope/field
        installHookKit(mgr, cfg, p.log)    // 含与既有 HookKit 合并
    }
    p.log.Info("dataisolate plugin started, policies=%v tenants=%v", cfg.Policies, mgr.Names())
    return nil
}

func (p *Plugin) Stop() error { return nil }
func (p *Plugin) Manager() *Manager { return p.mgr }
```

---

## 17. 应用集成示例

```go
func main() {
    // 1) db（策略③示例：单库共享表）
    _ = db.Init("sqlite", "file:app.db", db.WithDialect(db.NewDialect("sqlite")))

    // 2) 配置 + 插件
    if err := config.Init(os.Args); err != nil { log.Fatal(err) }
    tp, err := dataisolate.NewPlugin(nil)
    if err != nil { log.Fatal(err) }

    // 3) 应用：隔离中间件放在业务/事务之前
    app := aifei.New(aifei.WithPlugin(tp))
    app.Use(
        server.Logger(),
        server.Recover(),
        dataisolate.Middleware(),         // 解析 Principal → in.Context()
        // server.WithInterceptors(server.TxInterceptor()), // 可选
    )
    server.AutoRegisterServices(app)
    server.Run(app, ":8080")
}
```

Service 内**无需感知隔离**——用 ctx 感知的 db 入口即可：

```go
func (s *Service) List(in aifei.Input) aifei.Output {
    rows, err := db.WithCtx(in.Context()).         // ← ctx 感知入口
        Sql(listSql, in.GetMap()).Find()           // hook 自动注入 tenant_id + 范围 + 字段脱敏
    // ...
}
// 策略①/② 用：dataisolate.Use(in.Context()).Find()      // 路由到该租户的 Config
// db.Sql + 指令：dataisolate.SqlById(in.Context(), "listOrders", in.GetMap()).Find()
// 批处理：db.NewBatchCtx(in.Context()).Insert(rows)      // 自动盖 tenant/creator/dept
// 超管代查：dataisolate.As(in.Context(), adminP).Find()
```

> **约定**：请求上下文里访问 db 统一用 `db.WithCtx(in.Context())` / `dao.Ctx(in.Context())` / `db.*Ctx(ctx)` 系列。后台任务手动 `ctx := dataisolate.WithPrincipal(context.Background(), p)` 后同样用 `db.WithCtx(ctx)`。

---

## 18. 安全与边界考量

| 议题                    | 处理                                                                                                                        |
| --------------------- | ------------------------------------------------------------------------------------------------------------------------- |
| **db 改动面**            | 仅 4 项纯新增（`Dao.Context()` / `SqlAndArgs()` / `db.Batch` 触发 hook / `Dao.Fail` veto）；库/Schema 隔离零改动。                         |
| **主体可信**              | 解析器（子域/header/JWT）不鉴权，**请求可信由应用自负**（网关鉴权后注入 header、子域经 DNS/TLS 绑定、JWT 由应用校验）。插件只取值不认证。                                    |
| **字段脱敏默认**            | `MaskNull`（`NULL AS col`）保留列形状，typed Dao getter 返回零值不崩；逐规则可 `Remove`/`Constant`。                                          |
| **脱敏盲区**              | 字段权限只改 **SELECT 投影**；若被脱敏列仍出现在 WHERE/JOIN/ORDER/索引，其**值在条件里仍被使用**（投影脱敏 ≠ 完全不可见）。需配合行权限或应用层把关。                             |
| **DeptTree 预解析**      | 「部门及以下」闭包须在中间件算好放进 Principal，禁止改写时递归查库。                                                                                   |
| **规则缓存刷新**            | 动态规则（范围类型/字段）由应用 `RuleProvider` 提供，**其缓存与刷新由应用负责**；插件不缓存规则结果（按查询实时向 provider 取）。                                          |
| **多角色合并**             | 默认取最宽档（`merge: broadest`）；严格场景 `strict`（取最窄）。                                                                             |
| **Principal 缺失**      | `enforce=false`（默认）跳过；`enforce=true` 命中受控项报错，防裸查。                                                                         |
| **后台/无 ctx**          | 忘 `WithPrincipal` → 无 Principal → 跳过（默认）→ **可能越权**；严格部署开 `enforce=true`，或显式 `Bypass`。                                     |
| **批处理透明**             | `db.Batch` 触发 hook（§13），批插盖列、批改/删注入范围 WHERE；字段权限不作用（无投影）。                                                                 |
| **改写失败（fail-closed）** | 解析失败/受控表无法安全改写 → `StatusFailed` → `dao.Fail` **报错中止**，绝不原样放行未隔离查询。DDL 能正常解析且无受控表 → 放行；可用 `on_failure: passthrough` 按路径放宽。 |
| **防越权写**              | UPDATE/DELETE 强制追加 `AND tenant_id=? AND <范围>`，即便 PK 命中也只动本租户+本范围行；INSERT 强制盖当前主体列。                                        |
| **SQL 注入安全**          | 值**只走参数占位** `?`，绝不字符串拼接。                                                                                                  |
| **唯一约束**              | 共享表的全局唯一索引（如 `email`）跨租户冲突；**唯一索引须含 `tenant_id`**（如 `(tenant_id, email)`）——schema 约束。                                     |
| **裸 DML 语义变化**        | `DELETE FROM t`/`UPDATE t SET`（无 WHERE）被改写成「本租户+本范围全表」而非全局；全局操作须 `Bypass(ctx)`。                                           |
| **元数据/Schema 漂移**     | 元数据驱动判定依赖注册 `Table` 与真实库一致；列缺失 → 运行时 SQL 报错。迁移须同步。                                                                        |
| **改写性能**              | Policy 链每查询跑一次；按 SQL 串缓存 `(AST, 改写计划)`，每次只重排参数。                                                                           |
| **与缓存插件**             | `plugins/cache` 的 key 须含 Principal 维度（至少 tenant，理想含 user/dept/roles 摘要），防跨主体污染。                                           |
| **并发/事务**             | Principal 走 context，随事务 ctx 透传；无可变状态。                                                                                     |

---

## 19. 测试计划

独立 Go module `_test/dataisolate_test`，`package dataisolate_test`，外部测试包，`modernc.org/sqlite` 自包含。

- **rewriter 单测（无 DB）**：
  - WHERE 注入：SELECT/UPDATE/DELETE 有/无 WHERE；`GROUP BY`/`ORDER BY`/`LIMIT`/`HAVING` 前定位；`UNION` 各分支均注入；子查询/CTE 递归；多表 JOIN 按别名注入。
  - 投影改写：`SELECT *` 展开后按规则脱敏/移除；显式列脱敏保留别名；JOIN 逐表规则；`COUNT(*)`/无 FROM 跳过；默认 `MaskNull` 保留形状。**断言只改最外层 SELECT**：子查询/UNION 各分支/CTE 内投影保持不变（不破坏 `IN/EXISTS`/UNION 列对齐）；标量子查询投影列不脱敏。
  - 参数对齐：占位与 args 顺序（含 IN 列表、多谓词）；PG 专用语法/DDL → `StatusFailed`（fail-closed 报错）。
- **Policy 链集成测（注册多表 + sqlite）**：
  - 租户：插入自动带 `tenant_id`；读取仅本租户；跨租户不可见。
  - 行范围：同接口不同 Principal（本人/本部门/部门树/全部）返回不同结果集；多角色取最宽；DeptTree `IN(...)`。
  - 字段脱敏：`password` 对普通用户为 NULL、对超管可见；typed Dao getter 不崩。
  - INSERT 盖 `tenant/creator/dept`；UPDATE/DELETE 越权 id 受范围 WHERE 保护 → 0 affected。
  - `enforce=true` + 缺 Principal → 报错；`ignore_tables`/无相关列 → 不改写。
  - Paginate 的 count 与 data 均改写。
  - `Bypass`：全链不触发；`As(adminPrincipal)`：以超管见全部。
  - 与既有 HookKit 共存；双重注入守卫（db.Sql `#and`）生效。
- **批处理测**：批插盖列、批改/删注入范围 WHERE、bypass 不注入、裸 `Execute` 改写。
- **db.Sql 测**：hook 路径对 db.Sql 的范围/字段改写生效；`dataisolate.Sql` + `#and` 指令路径精确；bypass 经 helper 落实。
- **策略①/② 测**：两个 `InitWithID` + 路由器，`Use(ctx)` 命中各自 Config、互不串数据。
- **中间件测**：子域 / `X-Tenant-ID` 头 / 子域→header 回退链 / JWT 各注入正确 Principal。
- **RuleProvider 契约测**：用 stub provider 验证插件按 `(表, Principal)` 正确调用并应用返回的 `ScopeRule`/`FieldRule`；具体业务实现与表由应用自测。
- **性能测**：同 SQL 模板多次调用命中改写计划缓存。

---

## 20. 实现步骤

1. **db 必要支持**（纯新增，可独立合入）：
   - (a) `db/dao.go` 增 `Dao.Context()` 与导出 `SqlAndArgs()`；
   - (b) 让 `db.Batch` 在执行前触发 DbHookKit（行式批插 fire `InsertHook`、批改/删与裸批走 sqlPara 改写）；
   - (c) hook veto：`Dao.Fail(err)` + `runner()` 检查（fail-closed，§4.3）。
     均向后兼容。
2. **Principal + 中间件 + resolver**：`principal.go`/`middleware.go`/`resolver.go`；`subdomain_header` 内置（仅 TenantID），应用可接 JWT/session。
3. **rewriter**：引入 `blastrain/vitess-sqlparser`，实现 WHERE 注入 + 投影改写 + 参数重排，全量单测。
4. **Policy 链 + TenantPolicy**：迁入租户行为，验证与原租户方案等价（回归保护）。
5. **策略①/② 路由**：`use.go` + `Manager`。
6. **DataScopePolicy + 定义 `ScopeRuleProvider` 接口**：五档谓词、多角色合并、DeptTree（接口由插件定义，实现交应用）。
7. **FieldMaskPolicy + 定义 `FieldRuleProvider` 接口**：脱敏（默认 NULL）/移除/常量（接口由插件定义，实现交应用）。
8. **DbHookKit + compose**：按操作分流，装到 `db.Config`；补集成测与示例。

每步独立编译/测试/合入。建议先 1–5（含租户全策略，回归保护），再 6–7（行/列新维度）。

---

## 21. 边界与未来扩展

- **策略级逃逸**：`WithoutPolicy(ctx, "field")` 仅跳某条 policy（如运维需见全部字段但保留租户隔离）。
- **行权限表达式增强**：基于权限点（`Perms`）的细粒度规则（如「有 order:view.all 则全部」）。
- **字段级审计**：记录被脱敏字段访问（合规）。
- **解析器方言扩展**：为 PostgreSQL 接 `pg_query_go`（cgo）精确解析 `::`/`RETURNING`/`ARRAY`，避免 fail-closed 误报。
- **DB 层 RLS / 视图**：把行/列权限下推到 PG RLS 或 MySQL 视图，作为应用层改写的替代/补充；可作 `strategy: rls`。
- **缓存键带主体**：与 `plugins/cache` 联动，自动把 Principal 摘要纳入 key。
- **生成器集成**：`tools/generator` 识别受控表，生成 `tenant_id`/`creator_id`/`dept_id` 列与索引；typed Dao 标注脱敏字段。
- **`server` 导出 `ctxSetter`**：复用，避免重复定义。
- **Schema 隔离方言**：PG `SET search_path`、MySQL `USE`，在 `Config.Pool()` 或方言层按租户切换。

---

## 22. 附：db 衔接点速查

| 现有符号                                                               | 位置                             | 本方案改动                                         |
| ------------------------------------------------------------------ | ------------------------------ | --------------------------------------------- |
| `Dao.ctx` / `Dao.Ctx(ctx)`                                         | `db/dao.go`                    | 新增 `Dao.Context()` getter                     |
| `Dao.sqlAndArgs`                                                   | `db/dao.go`                    | 导出为 `Dao.SqlAndArgs()`                        |
| `db.Batch`（`exec*Groups`/`Execute`/`ExecuteSQLs`）                  | `db/batch.go`                  | 执行前触发 DbHookKit（透明化，§13）                      |
| `Dao.runner()` + 新增 `Dao.Fail(err)`                                | `db/dao.go`                    | hook veto：fail-closed，无法安全改写即中止（§4.3）         |
| `Config.HookKit` / `WithHookKit` / `GetDbHookKit`                  | `db/config.go`                 | 插件 `Start` 经其安装/合并 hook                       |
| `DbHookKit` 六接口                                                    | `db/hook.go`                   | 插件实现之；`Before*` 用 `dao.SqlPara(sp)` 写回        |
| `Row.Set`                                                          | `db/row.go`                    | `BeforeRowInsert` 给行盖 `tenant/creator/dept` 列 |
| `RegisterTable` / `GetTableByName` / `Table.Fields` / `FieldTypes` | `db/table.go`                  | 元数据自发现受控表/字段（§8.4/§9.2/§10.3）                 |
| `db.InitWithID` / `GetConfig` / `UseWithID`                        | `db/config.go`,`db.go`         | 策略①/② 租户→Config 路由                            |
| `db.WithCtx` / `*Ctx` 系列 / `NewBatchCtx`                           | `db/db.go`,`batch.go`          | 应用侧 ctx 感知入口                                  |
| `#and` / `#where` / SqlKit 作用域                                     | `db/sql/condition.go`,`kit.go` | db.Sql 指令路径（静态租户/范围）                          |
| `aifei.Input.Context/Header`                                       | `aifei/input.go`               | 中间件读取/注入 Principal                            |
| `HttpContext.SetContext`                                           | `http/context.go`              | 经 `ctxSetter` 把 Principal 写回 `in`             |
| `aifei.Handler` / `app.Use`                                        | `aifei/handler.go`,`server`    | `dataisolate.Middleware()` 接入                 |
| 插件范式 `plugin.go`/`manager.go`/`*_default.go`/`config.go`           | `plugins/storage` 等            | `plugins/dataisolate` 照搬结构                    |
| `config.SubBind` / `GetStr`                                        | `config/props.go`              | 读 `dataisolate.*` 配置                          |
