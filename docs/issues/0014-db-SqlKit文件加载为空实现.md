# ISSUE-0014 — db SqlKit SQL 文件加载与热重载为空实现

> **编号**：0014　**状态**：🔴 未处理　**严重程度**：⚠️ 一般
> **发现日期**：2026-07-16　**相关任务**：db 模块（对照 `docs/java-go-comparison.md` §2.2）

## 问题描述

Java `SqlKit` 支持 `addSqlFile` 加载外部 SQL 模板文件、`setBaseSqlFilePath` 扫描目录、`setSqlFileHotReloading` 热重载。Go `db/sql/kit.go` 的 `ParseSqlFile()` 是空操作 stub（注释 "Already parsed inline"），只能 `AddSql(sqlID, sql)` 内联，无法批量加载 `.sql` 文件。

## 期望行为（应有功能清单）

- [ ] `ParseSqlFile(file)` 实现：解析 `.sql` 文件为 `SqlSource` 并按 sqlID 注册
- [ ] 目录扫描：`SetBaseSqlFilePath(path)` 批量加载目录下 `.sql`
- [ ] 热重载：文件变更时重新解析（可选，devMode 下）

## 实际行为（Go 现状）

`db/sql/kit.go:144` `ParseSqlFile()` 空操作；只能 `AddSql` 内联，工程化不便。

## 影响范围

团队协作、SQL 与代码分离、SQL 集中管理的场景。

## 相关文件 / 符号

- `db/sql/kit.go:144` — `ParseSqlFile` 空 stub
- 对照 Java：`aifei-db/sql/SqlKit.java`（`addSqlFile` / `setBaseSqlFilePath` / `setSqlFileHotReloading`）

## 建议方案

实现 `.sql` 文件解析（文件内按 sqlID 分段，如 `-- #id` 注释约定），注册到 kit 的 sqlMap；提供目录扫描与（可选）`fsnotify` 风格热重载。

## 解决记录

- 修复提交 / PR：
- 改动：
- 校验：`go build ./...` / `go vet ./...` 改动文件 0 新错
- 验收：
