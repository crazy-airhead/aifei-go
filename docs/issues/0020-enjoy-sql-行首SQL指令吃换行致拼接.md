# ISSUE-0020 — enjoy-sql 行首 SQL 指令吃掉换行导致渲染 SQL 粘连

> **编号**：0020　**状态**：🟢 已解决　**严重程度**：🚨 阻断
> **发现日期**：2026-09-14　**相关任务**：enjoy / db/sql（SqlKit）

## 问题描述

反引号多行字符串写的 Enjoy SQL 模板中，`#where`/`#and`/`#or`/`#para`/`#p`/`#orderBy` 独占一行（行首）时，lexer 按默认规则吃掉该行尾换行；而这些指令产出的 SQL 片段（`WHERE age > ?`、`?` 等）又无前导空白，相邻片段直接拼成坏 SQL（如 `?AND`）。

## 复现步骤

1. 用反引号多行字符串写 SQL 模板，指令独占一行：

   ```go
   kit.GetSqlPara(`select * from user
   #where(age, '>', age)
   #and(name, 'like', name)`, map[string]interface{}{"age": 18, "name": "a"})
   ```

2. 渲染。

## 期望行为

```sql
select * from user
WHERE age > ?
AND name LIKE ?
```

对照 Java SqlKit（`SqlKit.java` 构造器）：产出 SQL 片段的指令均以 `addDirective(name, class, keepLineBlank=true)` 注册——行首独占行时**保留**行尾换行；仅容器指令 `#sql` 为 `false`。

## 实际行为

```sql
select * from user
WHERE age > ?AND name LIKE ?     -- ?AND 粘连，SQL 语法错误
```

其余实测粘连形态：`WHERE age > ?order by id`（指令后跟文本行）、`where id =\n?and status = ?`（行首 `#para`）、`#for` 循环内行首 `#and` 产出 `AND age > ?AND age > ?`。行内写法（指令与 SQL 文本同行，既有测试的全部写法）不受影响——这也是既有用例无一失败的原因。

## 影响范围

- `db/sql`（SqlKit）全部产出片段的指令；`db.Sql`/`db.SqlById` 等经 SqlKit 的 API。
- 多行模板写法（Go 侧最自然的反引号写法）必踩；单行内联写法无感。
- 移植偏差：Go 版把 Java 的按指令名集合 `keepLineBlankDirectives: Set<String>` 简化成了全局 bool，且 SqlKit 未注册任何名字，等价于全部按 false 处理。

## 相关文件 / 符号

- `enjoy/lexer.go` — `Lexer.keepLineBlank`（全局 bool）→ 增加 `keepLineBlankNames` 按名覆盖表与 `keepLineBlankFor(name)`；`scanDirective` 两处换行消费点、`scanAtCall`（仅取全局默认，对照 Java 排除 Symbol.CALL）
- `enjoy/engine_config.go` — `EngineConfig.SetKeepLineBlank(name, bool)`（对照 Java setKeepLineBlank）；`AddSharedFunction` 的 lexer 一并接线
- `enjoy/template.go` — `compileSource` 传递按名表；新增 `Engine.SetKeepLineBlank` 透传
- `db/sql/kit.go` — `NewSqlKit`：where/and/or/orderBy/para/p 注册 `keepLineBlank=true`（对照 Java SqlKit.java，`or` 为 Go 增补同族指令）
- `db/sql/condition.go` — `writeConditionHead`（产出 `WHERE `/`AND ` 无前导空格，是粘连的另一半成因，保持与 Java 一致不改）

## 解决记录

- 修复提交 / PR：本次会话（v0 分支工作树）
- 改动：上述文件；enjoy 既有全局 API（`SetKeepLineBlankDirectives(bool)`/`Lexer.SetKeepLineBlank(bool)`）语义不变，按名表优先、全局默认兜底。
- 校验：`go build` 全模块通过；`_test/db_test`、`_test/enjoy_test`、`_test/generator_test` 全绿，demo 可编译。
- 验收：`_test/db_test/issue0020_sql_test.go` 10 个用例——行首 where/and、where/or、指令后跟文本行、行首 para、行首 orderBy、#sql 块多行（含 nil 条件跳过）、#for+行首 and、行内写法回归保护、行尾写法（指令不在行首，换行属后续 TEXT token 天然保留）、行尾接行首混排。
