# ISSUE-0006 — db `Row.Keep()` 未清理 change 集合

> **编号**：0006　**状态**：🟢 已修复　**严重程度**：⚠️ 一般
> **发现日期**：2026-07-16　**相关任务**：db 模块（对照 `docs/java-go-comparison.md` §2.1 Bug 2）

## 问题描述

`Row.Keep(fields)` 只过滤 `data`，未同步清理 `change` 集合，导致被 keep 移除的字段仍标记为「已变更」，后续 `Update` 会用不存在的字段生成 SQL。

## 复现步骤

1. `row := db.Row{}.Set("a",1).Set("b",2).Keep("a")`
2. 调用 `row.Update()`
3. 观察：生成的 `UPDATE` 仍含字段 `b`（已被 `Keep` 移除）

## 期望行为

`Keep` 同时清理 `data` 与 `change`，移除的字段不参与更新。

## 实际行为

`change` 仍含 `b`，`UPDATE` 语句带上已不存在的字段。

## 影响范围

所有用 `Keep` 精简行后再 `Update` 的场景。

## 相关文件 / 符号

- `db/row.go:155` — `Keep` 只删 `r.data`
- 对照同文件 `Remove` / `RemoveNullFields` — 都正确同时清理两者
- 对照 Java：`aifei-db/core/Row.java` `keep` 同时过滤 `data` 与 `change`

## 建议方案

`Keep` 内补一行清理 `r.change` 中不在保留集的键。

## 解决记录

- 修复提交 / PR：
- 改动：`db/row.go` — `Keep` 在过滤 `r.data` 后，新增循环清理 `r.change` 中不在保留集的键（与 `Remove`/`RemoveNullFields` 行为一致）；回归测试 `TestRowKeepClearsChange` 在 `_example/db_sqlite_test/row_test.go`（原 `db/db_test.go` 整体迁入 `_example/db_sqlite_test`，外部测试，依赖未导出符号的用例改为导出 API / SQLite 实测）。
- 校验：`go build ./db` / `go vet ./db` / `go vet ./_example/db_sqlite_test` / `go test ./_example/db_sqlite_test` 全部通过，0 新错。
- 验收：`db.Row{}.Set("a",1).Set("b",2).Keep("a")` 后 `ChangedFields()` 仅含 `a`，`Update` 不再为已移除的字段生成 SQL。
