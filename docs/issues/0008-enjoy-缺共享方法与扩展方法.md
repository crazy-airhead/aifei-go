# ISSUE-0008 — enjoy 缺少共享方法库与类型扩展方法

> **编号**：0008　**状态**：🟢 已修复　**严重程度**：💡 体验
> **发现日期**：2026-07-16　**相关任务**：enjoy 模块（对照 `docs/java-go-comparison.md` §3.2 / §3.3）

## 问题描述

Java enjoy 提供 `SharedMethodKit`（默认 `SharedMethodLib`：`isEmpty` / `notEmpty`）和 9 类基本类型的扩展方法（Integer/Long/Short/Byte/Float/Double/BigDecimal/BigInteger/String Ext，各 ~9 方法如 `toBoolean`/`toInt`/`toBigDecimal`）。Go 版完全缺失共享方法体系，扩展方法只在 `expr_eval.go` 硬编码了 string 的部分，无数值类型扩展、无注册机制。

## 期望行为（应有功能清单）

- [ ] `SharedMethodKit` + 默认 `isEmpty` / `notEmpty`
- [ ] 扩展方法注册机制（`addExtensionMethod`）
- [ ] 数值类型扩展方法（int/long/float/double 的 `toBoolean`/`toInt`/`toBigDecimal` 等）
- [ ] String 扩展方法补齐，并由硬编码改为可注册

## 实际行为（Go 现状）

`engine_config.go` 无 `sharedMethodMap` / `extensionMethod` 注册；`expr_eval.go:377-427` 硬编码 string 扩展方法（`length`/`trim`/`upper`/`contains`/`startsWith`/.../`isEmpty`），无数值扩展。

## 影响范围

模板内便捷方法调用（`isEmpty(x)`、数值格式转换等）。

## 相关文件 / 符号

- `enjoy/engine_config.go` — 缺 `sharedMethodMap` / 扩展方法注册
- `enjoy/expr_eval.go:377-427` — 硬编码 string 扩展方法
- 对照 Java：`aifei-enjoy/ext/sharedmethod/SharedMethodLib.java`、`aifei-enjoy/ext/extensionmethod/*Ext.java`

## 建议方案

在 `EngineConfig` 增加 `sharedMethodMap` 与扩展方法注册；提供默认 `isEmpty`/`notEmpty`；将 string 扩展方法抽到注册体系并补充数值类型扩展。

## 解决记录

- 修复提交 / PR：修复问题0008
- 改动：
  - `enjoy/shared_methods.go`（新增）— 两套进程级 kit：`SharedMethodKit`（按名注册 `SharedMethod`，`Add/Remove/Call`）与 `ExtensionMethodKit`（按 `reflect.Kind`+名注册 `ExtensionMethod`，`Add/Call`，指针解引用后按 Kind 分派）。默认注册：共享方法 `isEmpty`/`notEmpty`（对照 `SharedMethodLib`，Go 取宽松：nil/空集合/空串为空、其余类型非空、不抛异常）；String 扩展（原 `expr_eval.stringMethod` 的 `length/trim/upper/.../isEmpty` 全量迁入 + 补齐 `StringExt` 的 `toBoolean/toInt/toLong/toFloat/toDouble/toShort/toByte/toBigInteger/toBigDecimal`）；数值扩展（`toBoolean/toInt/toLong/toFloat/toDouble/toShort/toByte/toBigInteger/toBigDecimal`，注册到全部整型/浮点 kind，对照 `Integer/Long/Short/Byte/Float/Double Ext`）。导出包级 `AddSharedMethod/RemoveSharedMethod/AddExtensionMethod` 与 `Engine.AddSharedMethod/AddExtensionMethod`。
  - `enjoy/expr_eval.go` — `MethodExpr.Eval`：裸调用 `name(args)`（`Obj==nil`）在变量/共享对象未命中后查 `sharedMethodKit`；`obj.method(args)` 在 map 检查后查 `extensionMethodKit`，再走 reflect；删除硬编码 `stringMethod`（已迁入扩展方法库）。去掉未再使用的 `strings` 导入。
  - `enjoy/template.go` — `Engine` 增 `AddSharedMethod`/`AddExtensionMethod`（委托包级 kit，附 `reflect` 导入）。
  - `_example/enjoy_test/shared_method_test.go`（新增）— 覆盖 isEmpty/notEmpty（直接输出 + 多行 #if/#else）、全部数值 kind 的 toInt 与 toLong/toDouble/toBoolean、keepPara 场景（String/int 双形态）、原 string 方法回归、自定义共享/扩展方法注册。
- 取舍：kit 取「进程级」而非「per-EngineConfig」。理由——表达式 `Eval(scope, ctrl)` 入参无 env/config，且 `expr_eval_test.go` 单测直接 `NewScope(nil)` 构造 scope 并不持 config；进程级 kit（与 Java 扩展方法的 static `MethodKit` 一致）在「模板体 / for 子作用域 / 裸 NewScope 单测」中一致可用，免穿透 Scope。自定义共享/扩展方法为进程级注册（已注明，影响整个进程）。数值转换一律走 `numInfo`（覆盖 int8-64/uint8-64/float32-64），不使用 `toInt64/toFloat64`（后两者类型 switch 只含 int/int64/float64/float32，会漏 int32/uint 等 kind 落到 0）。Go 无 BigDecimal/BigInteger：`toBigDecimal`→float64、`toBigInteger`→int64（有损近似，已注明）。
- 校验：`go vet`/`go build` enjoy、db、tools/generator、plugins/cache、_example/demo 均 0 新错；`go test` enjoy（via `_example/enjoy_test`）、db、`_example/db_sqlite_test`、generator 全绿。
- 验收：`#(isEmpty([]))`→`true`、`#(notEmpty([a]))`→`true`、`#(int64(7).toInt())`→`7`、`#(int32(7).toInt())`→`7`、`#(uint(7).toInt())`→`7`、`age.toInt()>18`（age 为 `"20"` 或 `20`）→成年、`"3.5".toDouble()`→`3.5`、自定义共享/扩展方法注册后可调用。

### 遗留（非本 issue，已发现）

- **既有词法缺陷（非 0008 引入）**：`enjoy/lexer.go` `scanDirective` 的无参分支会把指令名后到行尾/`#` 的文本当作 `para` 吞掉，导致**同行的** `#else<文本>`/`#end<文本>`/`#break<文本>` 等把后续文本吃进指令 token，使 `#if(c)A#else B#end`（条件为假时）输出空而非 ` B`。多行写法（`#else` 独占一行）不受影响。→ **已由 [ISSUE-0019](0019-enjoy-无参指令吞行内文本.md) 修复**（无参指令不消费尾部文本）。修复后 0008 的 `isEmpty`/`notEmpty` 可在同行 `#if(...)#else...#end` 中正常使用。
