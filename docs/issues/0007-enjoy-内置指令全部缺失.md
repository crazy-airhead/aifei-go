# ISSUE-0007 — enjoy 内置指令全部缺失（#escape / #date / #number / #random / #render / #string）

> **编号**：0007　**状态**：🟢 已修复　**严重程度**：💡 体验
> **发现日期**：2026-07-16　**相关任务**：enjoy 模块（对照 `docs/java-go-comparison.md` §3.2 / §3.3）

## 问题描述

Java enjoy 默认注册 7 个内置指令，覆盖日期格式化、HTML 转义、数字格式化等模板常用能力；Go 版一个都没有注册，模板内无法完成这些常见渲染需求。

## 期望行为（应有功能清单）

- [x] `#date(date, pattern?)` — 日期格式化
- [x] `#escape(text) { ... }` — HTML 转义块 `< > " ' &`
- [x] `#number(num, pattern?)` — 数字格式化（DecimalFormat 子集）
- [x] `#random` — 输出随机整数
- [x] `#render(name)` — 动态渲染子模板
- [x] `#string(name) { ... }` — 多行字符串变量定义
- [x] `#call` — 动态调用（表达式函数名，区别于 `#@` 语法糖）

## 实际行为（Go 现状）

`engine_config.go` 的 `directiveMap` 为空，无任何内置指令；HTML 转义无显式入口（Java 默认 `#()` 也不转义，但靠 `#escape` 显式开启，Go 连 `#escape` 都没有）。

## 影响范围

通用 HTML 模板渲染场景；数字/日期/转义相关输出。

## 相关文件 / 符号

- `enjoy/engine_config.go` — `directiveMap` 默认空
- `enjoy/directive.go` — 指令接口
- 对照 Java：`aifei-enjoy/ext/directive/{Date,Escape,Number,Random,Render,String,Call}Directive.java`

## 建议方案

实现 `Directive` 接口的 7 个内置指令并在 `NewEngineConfig()` 默认注册（参考 Java 实现，日期/数字格式用 Go `time.Format` / `strconv` / `text/template` fmt）。

## 解决记录

- 修复提交 / PR：修复问题0007
- 改动：
  - `enjoy/engine_config.go` — 新增 `defaultDatePattern` 常量（`yyyy-MM-dd HH:mm`）、`GetDatePattern/SetDatePattern/GetBaseTemplatePath` 访问器；`NewEngineConfig` 设默认 pattern 并调用 `addBuiltinDirectives()` 注册 7 个指令（date/escape/number/random/render/string/call）。
  - `enjoy/builtin_directives.go`（新增）— 实现 6 个内置指令（`DateDirective/EscapeDirective/NumberDirective/RandomDirective/RenderDirective/StringDirective`），并附 `javaDatePatternToGo`（Java SimpleDateFormat→Go layout）、`htmlEscape`（逐字符转义 `& < > " '`）、`formatNumber`（DecimalFormat 子集：小数位 `#/0`、千分位 `,`、百分号 `%`、字面前后缀）、`groupBy3` 等辅助。`#date` 仅格式化 `time.Time`（对照 Java 仅支持 Date/Temporal），`#render` 按 `#include` 同款方式动态取子模板并把赋值参数绑定到子作用域。
  - `enjoy/stat_parser.go` — `CallStat` 收敛为 `#@name(args)` 静态糖专用（`FuncName` 来自词法器 `tok.Name`，`NullSafe` 来自 `#@name?(args)`）；抽出 `callDefine` 复用函数体执行逻辑。
  - `_example/enjoy_test/builtin_directive_test.go`（新增）+ `testdata/_hot.html` — 覆盖 6 个指令、动态 `#call` 与 `#@` 糖。
- 校验：`go vet ./enjoy ./db` / `go build` 改动模块 0 新错；`go test ./enjoy`(via `_example/enjoy_test`)、`./db`、`_example/db_sqlite_test` 全绿（db 的 SqlKit 在已注册内置指令的 engine 上叠加 SQL 指令，无冲突）。
- 验收：`#number(3.1415926,"#.##")`→`3.14`、`#number(0.9518,"#.##%")`→`95.18%`、`#number(1299792458,",###")`→`1,299,792,458`、`#date`/`#escape`/`#random`/`#string`/`#render`/动态 `#call`/`#@` 糖均按预期输出。

### 第二轮：消除 `#call` 启发式 + 修复 `#@` 词法器缺陷

- **反馈 / 触发**：首轮遗留两点——(1) 动态 `#call` 靠 `isStaticCallForm` 启发式（参数串首部为 `标识符(` 判静态、否则动态）判定，脆弱（如 `#call greet`（无括号静态）被误判为动态、函数名含数字等边界）；(2) `#@name(args)` 语法糖存在既有词法器缺陷：`scanDirective` 的 `@` 分支只前进一步、未越过名称/参数，导致 `#@` 内容被当作文本重扫。
- **根因**：Go 词法器把 `call` 注册为关键字（`TokCall`），与 Java 模型不一致——Java 关键字表**不含** `call`，`#call(...)` 走自定义指令路径（`CallDirective`），`#@id(p)` 走词法器静态糖（state 20）。Go 把 `#@` 与 `#call` 都塞进 `TokCall`，又额外发明了 Java 没有的 `#call name(args)` 裸形态，三者共用一个 `parseCallStat` 才需要启发式。
- **处理**（对照 Java `Lexer`/`Symbol`/`CallDirective`）：
  - `enjoy/lexer.go` — 从 `mapDirective` 移除 `call`（`call`→`TokID`→指令路径）；重写 `#@` 分支为 `scanAtCall`：正确越过 `@`、函数名、可选 `?` 与参数括号（修复重扫缺陷），函数名经 `Token.Name` 传递；新增 `#@id?(p)` 的 `TokCallIfDefined` 形态（对照 Java `CALL_IF_DEFINED`）；补 `skipBlanks/isIdentStart/isIdentChar`。
  - `enjoy/tok.go` — 新增 `TokCallIfDefined`。
  - `enjoy/stat_parser.go` — `parseCallStat(tok)` 改为静态专用（`FuncName=tok.Name`、`NullSafe=tok.Type==TokCallIfDefined`），删除 `isStaticCallForm/parseDynamicCallStat` 启发式；`CallStat` 去掉 `FuncExpr`。
  - `enjoy/builtin_directives.go` — 新增 `CallDirective`（动态 `#call(funcName,args...)`，首位可选 `true` 为 nullSafe），与 `CallStat` 共用 `callDefine`。
  - `enjoy/engine_config.go` — `addBuiltinDirectives` 注册 `call` 指令。
  - 测试 — `_example/enjoy_test`：`TestDefineAndCall` 与裸形态用例改用 `#@` 糖；新增 `TestCallAtSugarDirective`（含 `#@f()|tail` 重扫回归测试与 `#@missing?` nullSafe）。
- **校验**：`go vet ./enjoy` / `go build` 0 新错；`gofmt -l` 干净；`go test ./_example/enjoy_test ./db ./_example/db_sqlite_test` 全绿。
- **遗留**：无。

### 说明

- `#number` 为 DecimalFormat 子集（分组/小数位/百分号/字面前后缀），不含货币、科学计数法等；`#date` 仅支持 `time.Time`（Java 的 `Date`/`Temporal` 在 Go 统一为 `time.Time`）。
- `#call` 与 `#@` 函数不存在时 Go 版本宽松跳过（Java 非 nullSafe 时抛异常）；`#call(true,...)` / `#@name?(...)` 的 nullSafe 语法已支持，但因 Go 本就跳过，运行时行为一致。
