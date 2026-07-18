# ISSUE-0012 — enjoy 语义差异与 EngineConfig 配置项杂项

> **编号**：0012　**状态**：🟢 已修复（第二轮收敛）　**严重程度**：💡 体验
> **发现日期**：2026-07-16　**相关任务**：enjoy 模块（对照 `docs/java-go-comparison.md` §3.2 / §3.4）

## 问题描述

enjoy 多项低优先级语义差异与 `EngineConfig` 配置项缺失，影响模板兼容性与可观测性。

## 期望行为（应有功能清单）

- [x] `??` 优先级：Java 为低优先级（可链式 `a ?? b ?? c`）；Go 当 postfix 高优先级 — 第一轮对齐 Java `nullSafe()`（mulDivMod 与 unary 间、左结合）
- [x] `#@name?()` 安全调用：Go 把 `?` 并入函数名导致 `GetFunction("name?")` 失败并静默跳过 — 词法层此前已修；第一轮补回归测试 + 非 nullSafe 严格化
- [x] `::` 静态访问：Java 真正 `StaticMethod`/`StaticField`（按类名反射，默认关闭）；Go 当 `IDExpr` 上的 field/method 且默认开启（伪实现） — 第一轮默认禁用对齐 Java（**Go 限制：无 Class.forName，真反射不可行**）
- [x] `#include` 相对路径：Java 相对父文件目录；Go 只相对 baseTemplatePath — 第一轮改为相对父文件目录，回退 basePath
- [x] Call 参数个数不匹配：Java 抛异常；Go 静默忽略 — 第一轮严格化对齐 Java
- [x] 错误无行号定位：Java `Location`/`ParseException` 带文件名+行号；Go `errorStat` 只输出错误字符串 — 第一轮解析期 + directive 参数期带「文件名:行号」；第二轮补齐渲染期节点级 location（Stat 持行号、StatList.Exec 跟踪 curLine）
- [x] `EngineConfig` 配置项：缺 `compressor` / `outputDirectiveFactory` / `sourceFactory` / `sharedMethodKit` / `keepLineBlankDirectives` / `roundingMode` / `staticMethod` / `Field` / `addSharedFunction(file)` — 第一轮补齐（见解决记录逐项）

## 实际行为（Go 现状）

上述各项均与 Java 语义不符或缺失；`engine_config.go` 仅有 `directiveMap` / `sharedFunctionMap` / `sharedObjectMap` / `baseTemplatePath` / `encoding` / `datePattern` / `devMode`（后三项基本未接线）。

## 影响范围

从 Java enjoy 迁移模板的兼容性、错误排查体验、可配置性。

## 相关文件 / 符号

- `enjoy/expr_parser.go` — `??` / 赋值 / 安全调用
- `enjoy/engine_config.go` — 配置项
- `enjoy/template.go:148-155` — `errorStat` 无行号
- `enjoy/lexer.go` — `::` / `#@` 解析

## 建议方案

按 checklist 逐项对齐 Java 语义；优先级最高的是 `??` 优先级、错误行号定位（便于排错）、安全调用 `#@`。

## 解决记录

- 修复提交 / PR：`fix: 修复问题0012`（第一轮）
- 改动（文件级）：
  - `enjoy/expr_parser.go` — `??` 优先级对齐 Java `ExprParser.nullSafe()`：新增 `parseNullSafe()`（位于 `parseMul` 与 `parseUnary` 之间，for 循环左结合，右操作数 `parseUnary`），`parseMul` 改调 `parseNullSafe`，`parsePostfix` 删除 `ETokNullCoalesce` case。新增 `parseExprWithConfig(input, cfg)` 与 `exprContainsStatic`：`::` 静态访问在模板路径（cfg 非 nil）下默认禁用——预扫描整个表达式是否含 `::` token（词法器跳过字符串字面量，覆盖 `a::b` 与 `a.b.c::d`），禁用时抛 "static ... not enabled"。`parseAtom` 的 `::` 分支生成 `StaticMethodExpr`（不再走伪实现），开启后查注册表。
  - `enjoy/shared_methods.go` — 新增 `StaticMethodKit`（命名空间 `alias→对象`，反射调用其导出方法）+ 进程级 `staticMethodKit` + 包级 `AddStatic`/`RemoveStatic`。以「注册 struct 实例、反射其方法」作 Java 静态方法/导入工具类的等价（Go 无法反射 import 包的包级函数，故用 struct 方法落地）。
  - `enjoy/expr_eval.go` — 新增 `StaticMethodExpr{Cls,Name,Args}` 节点：`Eval` 查 `staticMethodKit.Call(cls, name, args)`，未注册返回 nil。
  - `enjoy/engine_config.go` — 补齐配置项（对照 Java `EngineConfig`）：`staticMethodExpressionEnabled`/`staticFieldExpressionEnabled`（默认 false + getter/setter）、`keepLineBlankDirectives`（默认 false）、`roundingMode`（默认 `HALF_EVEN`，常量 `RoundingModeHalfEven`/`RoundingModeHalfUp`）、`compressor`（`Compressor` 接口，预留扩展点未接线）、`sourceFactory`（默认 `NewFileSource`）、`AddSharedFunction(fileName)`（经 sourceFactory 读取→独立解析→合并 `#define` 进 `sharedFunctionMap`）、`GetSharedMethodKit()`（暴露进程级 `sharedMethodKit`）。`NewEngineConfig` 初始化 sourceFactory/roundingMode。
  - `enjoy/tok.go` — `Token` 增 `Line int`（token 所在行，对照 Java `Location.row`）。
  - `enjoy/lexer.go` — 行追踪：`Lexer` 预计算 `lineStarts`（每行首字符 pos），`lineOf(pos)` 二分查表；所有 `Token{...}` emit 填 `Line`。`keepLineBlank` 字段：三处行首指令吃换行（`scanDirective` 无参分支/普通分支尾部、`scanAtCall` 尾部）改为 `!keepLineBlank` 条件（对照 Java `keepLineBlankDirectives`，默认 false 行为不变）。
  - `enjoy/env.go` — `Env` 增 `fileName`（错误定位）+ `currentFile`（include 相对父目录，仅文件模板设）+ getter/setter。
  - `enjoy/template.go` — `compileSource(key, src, isFile)`：文件模板设 `fileName`+`currentFile`，字符串模板 fileName 留空（错误只标行号，对照 Java "String template line N"）；创建 lexer 后 `SetKeepLineBlank`；`GetTemplate` 用 `config.GetSourceFactory()` 替代直接 `NewFileSource`；`exec` 渲染期 recover 错误带文件名。
  - `enjoy/stat_parser.go` — `parseOneStat` 命名返回 + defer：所有解析期 error 自动经 `locErr` 附加「文件名:行号」（`locateError` 类型，嵌套 `parseOneStat` 不重复包装）；`parseDirectiveStat` defer recover：把 `#date`/`#number`/`#string`/`#render`/`#call` 的 SetExprList panic 转为带行号 error。`CallStat.Exec`/`callDefine`：函数不存在非 nullSafe 抛异常、参数个数不匹配抛异常（nullSafe 仍跳过）。`parseIncludeStat`：相对路径优先 `filepath.Dir(env.currentFile)`，无父目录回退 basePath。模板路径所有 `ParseExpr` 改 `parseExprWithConfig(..., env.GetEngineConfig())`（`parseExprList`/`parseSetStat`/`parseReturnStat`/`parseReturnIfStat`/`parseCallStat` 加 cfg 参数）。
  - `enjoy/builtin_directives.go` — `CallDirective.Exec` 非 nullSafe 函数不存在抛异常；`formatNumber` 增 `roundingMode` 参数，`HALF_UP` 用 `math.Round`（远离 0），`NumberDirective.Exec` 取 `cfg.GetRoundingMode()` 传入。
  - `_example/enjoy_test/issue0012_test.go` — 新增 18 个用例覆盖全部 7 项；`testdata/sub/_parent.html`+`_child.html` 测 include 相对父目录。
- **Go 限制说明**：
  - `::` 真正静态反射（`Class.forName`）在 Go 不可行（无运行时按全限定名查类型）。**默认禁用**并对齐 Java `isStaticMethodExpressionEnabled=false`（模板内出现 `::` 报 "static ... not enabled"）。Go 也没有结构体「静态方法」，更**无法整体反射一个 import 包的包级函数**（包不是运行时 value）——故「导入整个包」只能落到「注册一个 struct 实例，反射其所有导出方法」。**开启** `SetStaticMethodExpressionEnabled(true)` 后，`AddStatic(alias, obj)` 注册一个命名空间对象，其所有导出方法自动可在模板 `alias::method(args)` 反射调用（建议传 `&Obj{}` 覆盖值/指针接收者）；未注册命名空间或方法返回 nil。这与共享对象访问 `obj.method`（`.` 语法、需把实例注入 scope）刻意区分——`::` 是静态语法糖，命名空间注册一次即对所有模板可用，不占 scope。要导出标准库包级函数（如 `strings.ToUpper`），需用一个 struct 包一层。开启路径有用例 `TestIssue0012StaticEnableCallsRegisteredFunc`（`AddStatic("Str", i12StrUtil{})`，`Str::Upper/Str::Lower` 自动可用）、`TestIssue0012StaticEnableUnregisteredIsNil`（未注册静默 nil）。新增 API：`enjoy.AddStatic/RemoveStatic`、`Engine.AddStatic`、`EngineConfig.GetStaticMethodKit`、AST 节点 `StaticMethodExpr`、注册表 `StaticMethodKit`（命名空间→对象）。
  - `compressor` 仅留接口扩展点，渲染管线未接线（Java 的 CharTable/LineCompressor 性能优化对 Go 流式 writer 收益有限）。
- 校验：`go build`/`go vet`（`enjoy`、`_example/enjoy_test`）0 新错；`go test ./_example/enjoy_test` 全绿（含 0012 新 18 用例 + 现有回归）；`go test ./db ./_example/db_sqlite_test` 确认 `ParseExpr` 兼容。
- 验收：`#(1 ?? 2 + 3)`→`4`（Java `(1??2)+3`）；`#(nil ?? -5)`→`-5`（右操作数支持一元）；`#@missing?()`→``（跳过）、`#@missing()`→error；`#(Str::isBlank("x"))`→error「not enabled」，开启后 `AddStatic("Str", i12StrUtil{})` + `#(Str::Upper("hi"))`→`HI`、`#(Str::Lower("HI"))`→`hi`（整包导入，未注册静默 nil）；`#@f(1)`（define f(a,b)）→error「mismatch」；`#for(bad)`→error「line 1: ...」；include `testdata/sub/_parent.html`→`#include("_child.html")` 相对父目录解析；`#number(2.5,"#")` 默认→`2`（HALF_EVEN）、`HALF_UP`→`3`。
- 遗留（交下轮）：渲染期节点级 location——当前 `Stat`/`Expr` 节点未持 `Location`，渲染期 panic（如 reflect 调用异常）只能带文件名、无精确行号；若需精确，需给节点加 location 字段并在解析期填充（系统性机械改动）。`compressor` 接线。

---

### 第二轮（渲染期节点级 location + compressor 接线）

- **反馈 / 触发**：用户要求继续修第一轮遗留——渲染期 panic 只带文件名无精确行号；`compressor` 仅为预留接口未接线。
- **根因**：① `Stat`/`Expr` 节点不持 location，渲染期 panic 经顶层 `exec` recover 时丢失源码行；② `compressor` 字段存在但无内置实现、未接入解析/渲染管线。
- **处理（文件 / 符号级）**：
  - `enjoy/stat.go` — 新增 `nodeLoc{line}` + `setLine`（指针 receiver，写）/ `Line`（值 receiver，读）+ `locater`/`lineSetter` 接口 + `setStatLoc`/`statLine` 辅助。Stat 接口**未改**（db/sql 等外部实现无需改造），用可选 `locater` 断言访问。
  - `enjoy/stat_parser.go` — 所有有 `Exec` 的 Stat struct 嵌入 `nodeLoc`（StatList/Text/Output/IfStat/ForStat/SetStat/DefineStat/CallStat/Break/Continue/Return/ReturnIf/Null/Include/Switch/Case/Default/DirectiveStat）；`parseOneStat` 的 defer 统一 `setStatLoc(stat, tok.Line)`——所有顶层与嵌套 stat（经 collectUntil→parseStatList→parseOneStat）都在此设行号，**无需改各解析函数签名**。`StatList.Exec` 改调 `execStat`：执行前把当前 stat 行号记入 `ctrl.curLine`，嵌套 StatList 继续更新，故 panic 总能定位到最近 stat 行。
  - `enjoy/ctrl.go` — `Ctrl` 增 `curLine int`。
  - `enjoy/template.go` — `exec` recover 改用 `renderError(env, ctrl.curLine, r)` 输出「文件名:行号」；新增 `renderError` 辅助。
  - `enjoy/engine_config.go` — `Compressor` 接口补文档（编译期压静态 Text）；新增内置 `LineCompressor`（端口 Java `stat.Compressor.compress`：连续空白合并，含换行→Separator 默认 `\n`、纯空格→单空格，非空白原样）+ `NewLineCompressor()`。
  - `enjoy/stat_parser.go` — `parseOneStat` 的 `TokText` 分支在 `cfg.GetCompressor() != nil` 时对 `tok.Val` 压缩（编译期、只压静态文本，对照 Java「指令输出不压、缓存只压一次」）。
  - `_example/enjoy_test/issue0012_test.go` — 新增 `TestIssue0012RenderErrorLine`（reflect panic 带 line 3）、`TestIssue0012Compressor`（静态空白压缩）、`TestIssue0012CompressorSkipsDirectiveOutput`（指令输出不压缩）。
- **校验**：`gofmt -l` 干净；`go build`/`go vet`（`enjoy`、`_example/enjoy_test`）0 新错；`go test ./_example/enjoy_test` 全绿（含第二轮新用例）；`go test ./db ./server ./tools/{generator,damigen}` 全绿（节点嵌入 nodeLoc 不破坏 db/sql 的 Stat 实现）。
- **验收**：`#(fn(123))`（fn 签名 `func(string)string`）→ error「template render: line N: reflect: Call using int64 as type string」（N 为所在行）；`SetCompressor(NewLineCompressor())` 后 `"hello   world\n\n\nfoo"`→`"hello world\nfoo"`，指令 `#(x)` 输出（x="a   b"）原样不压缩。
- **遗留**：无。本轮两项遗留全部收敛，ISSUE-0012 状态 🟢。（compressor 仅接线编译期 Text 压缩；流式 `Render(writer)` 不单独压缩——Java 亦为编译期压缩，行为一致。）
