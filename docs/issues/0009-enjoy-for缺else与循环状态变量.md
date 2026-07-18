# ISSUE-0009 — enjoy `#for` 缺 else 分支与循环状态变量

> **编号**：0009　**状态**：🟢 已修复　**严重程度**：💡 体验
> **发现日期**：2026-07-16　**相关任务**：enjoy 模块（对照 `docs/java-go-comparison.md` §3.2）

## 问题描述

`#for` 有两处缺口：① 缺 `#else` 分支；② 循环状态变量不完整且访问范式不兼容。

## 期望行为

1. 循环一次未执行时执行 `#else` 体（空集合回退分支）。
2. 循环内可对象式访问状态：`for.index` / `for.count` / `for.first` / `for.last` / `for.odd` / `for.even` / `for.size` / `for.outer`。

## 实际行为（Go 现状）

- `ForStat` 无 else 概念，不支持 `#for ... #else ... #end`。
- 只设扁平变量 `index` / `size` / `first` / `last`，缺 `count` / `odd` / `even` / `outer`；且为裸变量，与 Java 的 `for.index` 对象式访问不兼容。

## 影响范围

空集合回退渲染、奇偶行高亮、分页等依赖循环状态的模板。

## 相关文件 / 符号

- `enjoy/stat_parser.go:91-108` — `ForStat` 构建（无 else）
- `enjoy/scope.go` — 循环变量设置
- 对照 Java：`aifei-enjoy/stat/ast/For.java:90-92`（else）、`ForLoopStatus.java`、`ForIteratorStatus.java`

## 建议方案

`ForStat` 增加 else 子 stat（集合为空时执行）；把循环状态聚合成一个 loop 对象（含 index/count/first/last/odd/even/size/outer）注入作用域，模板用 `for.index` 访问。

## 解决记录

- 修复提交 / PR：`fix: 修复问题0009`
- 改动：
  - `enjoy/stat_parser.go` — `ForStat` 增加 `Else Stat` 字段，并**移除 C 风格 `for(init;cond;update)` 支持**（连同 `Init`/`Cond`/`Update`/`IsRange` 字段、`execTrad`、以及失去引用的 `SetAsStat`），收敛 `#for` 为仅迭代型 `#for(id : expr)` / `#for(id in expr)`。header 不匹配迭代型语法时**报语法错误**（`RenderToString` 返回非空 error），而非静默忽略。
  - `enjoy/stat_parser.go` — `parseForStat` 收集体遇 `#else`/`#end` 停止，命中 `#else` 再收 else 体；`ForStat.Exec` 捕获 `outer = scope.Get("for")`，每轮注入 `for` 状态对象（`forIteratorStatus`），用 `ran` 标志在循环一次未执行时跑 `#else`；并修正跳转语义（`#return` 透传不在此复位、`#break` 跳出、`#continue` 跳过本次），取代原先每轮 `ctrl.Reset()`（会吞掉末轮 `#return`、`#break` 形同 `#continue`）的写法。
  - `enjoy/expr_eval.go` — 新增 `forIteratorStatus`（index/count/first/last/odd/even/size/outer）map 构造器，沿用 `forEntry` 的 map 范式（`FieldExpr.getField` 只识别 map key 与导出 struct 字段，无法回退 getter）；odd/even 按 count 计数，与 Java `getOdd()=index%2==0` / `getEven()=index%2!=0` 一致。
  - 移除原先注入的裸变量 `index`/`size`/`first`/`last`（与 Java `for.xxx` 对象式访问不兼容）；`_example/enjoy_test/stat_parser_test.go` 的 `TestForIterateSingleObject` 改用 `for.index`。
- 连带改造：`RenderToString` / `Render` 返回 error（原 `RenderToString` 只返回 string，错误被 `errorStat` 烘进输出，调用方无法区分正常与错误结果）。
  - `enjoy/template.go` — `Template` 增加 `parseErr`；新增 `exec(scope, writer)` 统一执行（解析错误直接返回；运行期 panic 经 `recover` 转为 error），`Render`/`RenderToString` 委托之；`RenderToString` 改为 `(string, error)`（出错返回 `("", err)`）；新增 `RenderToString0`（不返回 error 的便捷版本，内部调用 `RenderToString`，出错 panic，供不想逐处处理 error 的调用方使用）；新增 `parseTemplateRecovered` 在解析期 `recover`（把 `#date`/`#number` 等 directive `SetExprList` 的参数校验 panic 转为 parseErr）；删除 `errorStat`。
  - `db/sql/kit.go` — 6 处 SQL 渲染改用 `tpl.RenderToString0(data)`（出错 panic，与现有 `AddSql` 一致）；`db/dao.go` 无需改动（panic 自然向上传播，不破坏链式 Dao API）。
  - `tools/generator`（`RenderTemplate` → `(string, error)`，5 个生成器 + 测试 propagate）、`tools/damigen/gen.go` propagate；`server/io_handler.go` 原已处理 `Render` 的 error，自动获得真实错误并记录日志。
  - `_example/enjoy_test/` — 新增测试包级 helper `renderToString(t, tpl, data)`（统一 err 处理，保持用例简洁）；约 45 处 happy-path 用例改用之；`TestReturnIfEmptyParam`/`TestForCStyleNotSupported`/`TestInclude` 改为断言 `RenderToString` 的 error 返回；新增 `TestDirectiveParsePanicBecomesError`（#date 参数过多 → error，不再打崩）与 `TestRenderToString0`（便捷版本：正常渲染返回 string、出错 panic）。
- 校验：`go build` / `go vet`（`enjoy`、`db`、`db/sql`、`tools/generator`、`tools/damigen`、`server`、`_example/enjoy_test`）0 新错；`go test` 覆盖上述模块 + `db_sqlite_test` + 核心模块全绿。
- 验收：迭代型 `#for(x : list)` 内 `for.index/count/first/last/odd/even/size` 与 `for.outer.index`（嵌套）输出与 Java ForIteratorStatus 一致；空集合执行 `#else` 体，非空或首次即 `#break` 不触发 `#else`；`#break/#continue/#return` 跳转正确；C 风格 `for(init;cond;update)` 已移除并经 `RenderToString` 返回 error。`RenderToString` 现以 `(string, error)` 明确区分正常结果与错误（解析错误 + 解析/渲染期 panic 均覆盖）。
