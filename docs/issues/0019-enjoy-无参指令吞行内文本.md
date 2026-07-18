# ISSUE-0019 — enjoy 无参指令吞掉行内文本

> **编号**：0019　**状态**：🟢 已修复　**严重程度**：⚠️ 一般
> **发现日期**：2026-07-18　**相关任务**：enjoy 模块（修复 ISSUE-0008 时顺带发现）

## 问题描述

`enjoy/lexer.go` 的 `scanDirective` 在指令名后无 `(` 时，会把「指令名后到行尾（`\n`）或下一个 `#`」的整段文本当作指令参数 `para` 消费。对**无参指令**（`#else`/`#end`/`#break`/`#continue`/`#default`）这是错的——解析器从不读取这些指令的 `para`（`#else`/`#end`/`#default` 是 `collectUntil` 的停止 token；`#break`/`#continue` 直接构造 `BreakStat`/`ContinueStat`），于是这段文本被默默吞掉、永不出现在输出里。

## 复现步骤

1. 模板 `#if(1>2)A#else B#end`（条件为假，期望走 else 分支输出 ` B`）。
2. 渲染：`engine.GetTemplateByString(...).RenderToString(nil)`。

## 期望行为

`#if(1>2)A#else B#end` → ` B`（else 分支体保留）。

## 实际行为（修复前）

输出为空串 `""`——`#else B` 的 ` B` 被并入了 `#else` 的 `para`，else 分支体为空。同理 `#if(true)X#end tail` 的 ` tail`、`#switch(9)#case(1)one#default two#end` 的 ` two` 均被吞。

## 影响范围

- 直接削弱 ISSUE-0008 引入的 `isEmpty`/`notEmpty` 的内联 `#if` 用法（`#if(notEmpty(list)) ... #else ... #end` 在假分支下输出空）。
- 任何「同行」的无参指令后跟文本的写法都丢失文本。多行写法（`#else` 独占一行）因指令后即 `\n` 不受影响，故长期未被发现。

## 相关文件 / 符号

- `enjoy/lexer.go` — `scanDirective` 无参分支（原 164-170 行）。
- 对照：`stat_parser.go` `parseOneStat` 中 `TokBreak/TokContinue` 不用 `tok.Val`；`collectUntil` 把 `TokElse/TokEnd/TokDefault` 当停止 token，也不用 `tok.Val`。

## 建议方案

无参指令不应消费尾部文本：先 `mapDirective` 求得 `tokType`，对无参指令跳过 `para` 消费——行首（独占一行）时吃掉尾随水平空白与换行（保留「避免空行」优化），行内时保留尾部文本作为后续 `TokText`。其余指令（含 `#include "file"` 这类合法的行内参数）按原逻辑解析参数。

## 解决记录

- 修复提交 / PR：修复问题0019
- 改动：
  - `enjoy/lexer.go` — `scanDirective`：把 `tokType := l.mapDirective(name)` 提到参数解析之前；新增 `isParameterLessDirective(TokType)`（命中 `TokElse/TokEnd/TokBreak/TokContinue/TokDefault`）。无参指令走新分支：行首时跳过尾随 `[ \t]*` 并吃掉 `\n`/`\r\n`，行内时不推进 `l.pos`、`para=""`，尾部文本交由后续 `TokText` 输出；非无参指令保留原「跳空格 → 括号 para / 行内 para」逻辑。
  - `_example/enjoy_test/inline_directive_test.go`（新增）— 回归：行内 `#else B`→` B`、`#else非空`→`非空`、`#end tail`→`X tail`、行内 `#default two`→` two`、结合 `notEmpty` 的行内 `#if/#else`、多行写法不变、`#continue skip` 行内文本不丢。
- 取舍：`#elseB`（无分隔、`elseB` 被识别为单个标识符）属无效模板，不在修复范围（Java enjoy 同样按贪婪标识符匹配，不在此歧义上做特殊处理）。
- 校验：`go vet`/`go build` enjoy 0 新错；`gofmt -l` 干净；`go test` enjoy（via `_example/enjoy_test`，含 0008 与本次新增用例）、db、`_example/db_sqlite_test`、tools/generator、server 全绿。
- 验收：`#if(1>2)A#else B#end`→` B`、`#if(false)A#else非空#end`→`非空`、`#if(true)X#end tail`→`X tail`、`#switch(9)#case(1)one#default two#end`→` two`、多行 `#if(false)\nA\n#else\nB\n#end`→`B\n`（不变）。

## 关联

- ISSUE-0008「遗留」中提到的同源缺陷，现由本 issue 修复。修复后 0008 的 `isEmpty`/`notEmpty` 可在同行 `#if(...)#else...#end` 中正常使用。
