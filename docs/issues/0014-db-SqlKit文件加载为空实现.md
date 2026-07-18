# ISSUE-0014 — db SqlKit SQL 文件加载与热重载为空实现

> **编号**：0014　**状态**：🟢 已修复　**严重程度**：⚠️ 一般
> **发现日期**：2026-07-16　**相关任务**：db 模块（对照 `docs/java-go-comparison.md` §2.2）

## 问题描述

Java `SqlKit` 支持 `addSqlFile` 加载外部 SQL 模板文件、`setBaseSqlFilePath` 扫描目录、`setSqlFileHotReloading` 热重载。Go `db/sql/kit.go` 的 `ParseSqlFile()` 是空操作 stub（注释 "Already parsed inline"），只能 `AddSql(sqlID, sql)` 内联，无法批量加载 `.sql` 文件。

## 期望行为（应有功能清单）

- [x] `ParseSqlFile()` 实现：解析 `.sql` 文件为 `SqlSource` 并按 sqlID 注册
- [x] 目录扫描：`AddSqlDir(dir)` 批量加载目录下 `.sql`（`SetBaseSqlFilePath` 仅设基础路径，对齐 Java）
- [x] 热重载：文件变更时重新解析（devMode 下，`SetSqlFileHotReloading(true)`）

## 实际行为（Go 现状）

~~`db/sql/kit.go:144` `ParseSqlFile()` 空操作；只能 `AddSql` 内联，工程化不便。~~ 已补齐 Java 三件套（`AddSqlFile`/`SetBaseSqlFilePath`/`SetSqlFileHotReloading`）+ 真正的 `ParseSqlFile` + `AddSqlDir`（批量）+ devMode 热重载。

## 影响范围

团队协作、SQL 与代码分离、SQL 集中管理的场景。

## 相关文件 / 符号

- `db/sql/kit.go` — `ParseSqlFile`（原空 stub）、新增 `AddSqlFile`/`AddSqlDir`/`SetBaseSqlFilePath`/`SetSqlFileHotReloading`/热重载
- `enjoy/engine_config.go` — 新增 `EngineConfig.IsDevMode()`
- `db/db.go` — 顶层包装
- 对照 Java：`aifei-db/sql/SqlKit.java`（`addSqlFile` / `setBaseSqlFilePath` / `setSqlFileHotReloading` / `parseSqlFile` / `getSqlTemplate` 热重载分支）

## 建议方案

~~实现 `.sql` 文件解析（文件内按 sqlID 分段，如 `-- #id` 注释约定），注册到 kit 的 sqlMap；提供目录扫描与（可选）`fsnotify` 风格热重载。~~

实际采用：文件内分段**沿用 `#sql` 指令**（`#sql("id") ... #end`），与 Java 一致、与内联 `AddSql` 同一套语法（见解决记录「设计决策」）。

## 解决记录

- 修复提交 / PR：`fix: 修复问题 0014`
- 改动（文件级）：
  - `enjoy/engine_config.go` — 新增 `EngineConfig.IsDevMode() bool`（暴露 devMode 状态；`Engine.SetDevMode` 已存在、设 `config.devMode`，此前无 getter）。SqlKit 据此判定是否走热重载分支。
  - `db/sql/kit.go` —
    - SqlKit 增字段：`sqlFileList []string`（登记的文件路径）、`sqlFromSqlFile map[string]*enjoy.Template`（文件来源的 sqlID→body Template，reload 时据此精准移除 cache）、`fileTemplates map[string]*enjoy.Template`（解析路径→文件 Template，用于 isModified 判定）、`fileMu sync.RWMutex`（保护上述三者并发读写）。
    - `AddSqlFile(sqlFile)`（对齐 Java `addSqlFile`）：登记路径到 `sqlFileList`，空值 panic。文件内容需含 `#sql` 指令。
    - `AddSqlDir(dir)`（**Go 增强，Java 无对应**）：`os.ReadDir` 扫描目录下 `.sql`（非递归，忽略子目录与非 `.sql`），批量登记。满足「目录扫描」诉求。
    - `SetBaseSqlFilePath(path)` = `engine.SetBaseTemplatePath`；`SetSqlFileHotReloading(enable)` = `engine.SetDevMode(enable)`（对齐 Java）。
    - `ParseSqlFile() error`：从空操作改为真正实现——遍历 `sqlFileList`，`resolveSqlFile` 解析路径，`engine.GetTemplate` 渲染（`#sql` 指令把 sqlID→body Template 填入 `_SQL_CACHE_`），按 sqlID 注册到 `cache` + `sqlFromSqlFile`；sqlID 与已有（内联或文件来源）冲突返回 `error`（对齐 Java `IllegalArgumentException("sqlId already exists")`）；渲染/解析错误包裹返回。
    - `getSqlTemplate(sqlID)` 加 devMode 热重载（对齐 Java `getSqlTemplate` 热重载分支）：cache hit 且 devMode 且 `isSqlTemplateModified()` → `reloadModifiedSqlTemplate()`；cache miss 且 devMode 且文件已修改 → reload 后重取。
    - `reloadModifiedSqlTemplate()`（对齐 Java）：`engine.RemoveAllTemplateCache()` → 仅删 `sqlFromSqlFile` 的 sqlID（保留 `AddSql`/`Db.sql` 内联缓存）→ 清空 maps → 重新 `parseSqlFileLocked`。`fileMu` 下双检查避免重复 reload。
    - `resolveSqlFile(sqlFile)`：绝对路径原样返回；相对路径拼接 `SetBaseSqlFilePath` 的基础路径。
  - `db/db.go` — 顶层包装（对齐 `AddSql`/`AddSqlWithID` 风格）：`AddSqlFile`/`AddSqlFileWithID`、`AddSqlDir`/`AddSqlDirWithID`、`ParseSqlFile`/`ParseSqlFileWithID`、`SetBaseSqlFilePath`、`SetSqlFileHotReloading`。
  - `_example/db_sqlite_test/issue0014_test.go` — 新增 6 用例（对齐 0012 置 `_example/` 的惯例；package `db_sqlite_test`，经独立 `dbsql.NewSqlKit(name)` 实例隔离，不经 `db.Init`/sqlite）：`ParseSqlFile` 单文件多 `#sql` 注册、sqlID 冲突报错、`AddSqlDir` 批量（忽略非 `.sql`）、`SetBaseSqlFilePath` 相对路径、热重载改文件生效且保留内联缓存、未开热重载返回旧内容。
- **设计决策**：
  - 文件内分段**沿用 `#sql` 指令**（`#sql("id") ... #end`），忠于 Java，**不采用**建议方案猜测的 `-- #id` 注释约定——后者 Java 不存在，且 Go 的 `SqlDirective` 已支持 `#sql`，内联 `AddSql` 与文件加载共用一套语法，一致性好。一个 `.sql` 文件可含任意多个 `#sql` 段。
  - `ParseSqlFile()` 返回 `error`（Go 工程化友好；Java `parseSqlFile` 是 unchecked throw）。冲突/IO/解析错误统一以 error 返回；空 sqlFile 仍 panic（对齐 `AddSql` 的 blank 校验风格）。签名从无参空操作改为 `() error`——无外部调用方（grep 确认仅 stub 自身），改动安全。
  - 热重载判定方式与 Java 不同但语义等价：Java 遍历 `sqlFromSqlFile`（sqlID→body Template）调 `isModified()`；Go 的 `#sql` body Template 由 `enjoy.NewTemplate(env, ast)` 创建、`source` 为 nil、`IsModified` 恒 false。故改用**文件 Template**（`engine.GetTemplate` 返回、持 `FileSource`）的 `IsModified()` 判定底层文件 mtime 变更——即「文件变了才 reload」，与 Java 效果一致。
  - 热重载精准移除（对照 Java `sqlFromSqlFile` 注释 1）：reload 时仅删文件来源的 sqlID，保留 `AddSql`/`Db.sql("|id|")` 内联缓存，避免其失效抛「找不到 sql」。
- 校验：`go build`/`go vet`（`enjoy`、`db`、`db/sql`、`_example/db_sqlite_test`）0 新错；`go test ./_example/db_sqlite_test` 全绿（含 0014 新 6 用例 + 现有回归）；`go test ./db/sql`、`go test ./_example/enjoy_test` 确认无回归。
- 验收：单文件多 `#sql` 按 sqlID 注册、`GetSqlParaByIDWithArgs` 可用；sqlID 冲突返回 `sqlId already exists: <id>` error；`AddSqlDir` 批量加载目录 `.sql`（忽略 `.txt` 等）；`SetBaseSqlFilePath(dir)` + `AddSqlFile("rel.sql")` 相对路径解析；devMode 下改文件内容（`user`→`admin`）后 `GetSqlParaByIDWithArgs` 自动反映新内容且内联 `AddSql` 缓存不受影响；未开热重载则返回旧内容（`from user`）。
- 遗留：无。
