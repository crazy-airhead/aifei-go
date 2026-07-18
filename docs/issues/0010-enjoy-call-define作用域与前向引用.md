# ISSUE-0010 — enjoy `#call` / `#define` 作用域隔离与前向引用

> **编号**：0010　**状态**：🟢 已修复　**严重程度**：⚠️ 一般
> **发现日期**：2026-07-16　**相关任务**：enjoy 模块（对照 `docs/java-go-comparison.md` §3.2）

## 问题描述

`#call` / `#define` 两处缺陷：① `#call` 调用时丢失外围作用域；② `#define` 不支持前向引用。

## 期望行为

1. `#call` 的被调函数体运行在 caller 的子作用域，可见外层变量（`new Scope(callerScope)`）。
2. `#define` 在 parse 阶段即注册，模板中后面的 define 也能被前面的 call 调用（前向引用）。

## 实际行为（Go 现状）

1. `CallStat` 用 `NewScope(empty)`（无 parent），`#define` 函数体内看不到外层变量。
2. `#define` 执行时才 `env.AddFunction`（`stat_parser.go:159-161`），文档顺序靠后的 define 无法被前面的 call 调用。

## 影响范围

依赖外层变量的函数式模板；组织为「先调用、后定义」的模板结构。

## 相关文件 / 符号

- `enjoy/stat_parser.go:159-161` — `define` 执行期注册
- `enjoy/stat.go` / `stat_parser.go` — `CallStat` 作用域构建
- 对照 Java：`aifei-enjoy/stat/ast/Define.java`、`stat/Parser.java:100-103`（parse 期注册）

## 建议方案

`CallStat` 以 caller scope 为 parent 构造子作用域；`#define` 移到 parse 期注册（预扫描 statList 收集 define）。

## 解决记录

- 修复提交 / PR：`fix: 修复问题0010`
- 改动：
  - `enjoy/stat_parser.go` — `callDefine`（`#@name(args)` 与 `#call(...)` 共用）：子作用域由 `NewScope(make(...))`（无 parent，函数体读不到外层变量）改为 `scope.NewChild()`（caller scope 为 parent，对照 Java `Define.call` 的 `new Scope(scope)`），函数体内可见外层变量；body 执行后 `ctrl.Reset()` 消化函数体内的 `#return/#break/#continue`，使其不外泄到调用方（对照 Java `Define.call` 末尾 `scope.getCtrl().setJumpNone()`）。
  - `enjoy/stat_parser.go` — 新增 `registerDefine(stat, env)`：在 **parse 期** 把 `#define` 注册到 `env`（对照 Java `Parser.statList` 的 `stat instanceof Define → env.addFunction`），使文档顺序靠后的 define 也能被前面的 call 调用（前向引用）。分别在 `parseStatList`（顶层）与 `collectUntil`（`#if/#for/#define` 等块体，对照 Java 递归 `statList`）中调用。
  - **有意保留 `DefineStat` 在 stat 列表、`DefineStat.Exec` 仍注册**（不像 Java 那样 `continue` 剔除）：Go 的 `#include` 走「子模板独立编译 + 在父 env 中执行」路径，被 include 模板里的 define 需在 exec 期（父 env）再次注册才能渗入父模板（Java 靠 include 内联重 parse 达成同等效果）。故 parse 期注册覆盖同 env 前向引用，exec 期注册覆盖 include 渗入——两者并存，同 env 下重复注册幂等无害。
  - `_example/enjoy_test/stat_parser_test.go` — 新增 7 个用例：`TestCallSeesOuterScope`（函数体可见外层变量）、`TestCallLocalDoesNotLeak`（函数内 `#set` 落子作用域不污染外层）、`TestCallForwardReference`/`TestCallDynamicForwardReference`（call 在 define 之前的前向引用，分别覆盖 `#@` 静态糖与 `#call` 动态指令）、`TestCallNestedDefine`（外层 define 体内的内层 define 可注册并调用）、`TestCallReturnConsumedInDefine`/`TestCallBreakConsumedInDefine`（函数体内 `#return/#break` 在 define 边界消化、不外泄）。
- 校验：`go build` / `go vet`（`enjoy`、`_example/enjoy_test`）0 新错；`go test` 覆盖 `enjoy_test` 全绿（含原有 `TestCallDynamicDirective`/`TestCallAtSugarDirective` 回归），下游 `db/sql`、`server`、`tools/generator`、`tools/damigen`、`_example/demo`、`_example/db_sqlite_test` 及全部 `plugins/*` 均绿。
- 验收：`#define` 函数体内可读外层变量（`Hello #(user)` → `Hello Aifei`）；call 出现在 define 之前能前向调用（`#@greet("Sam")` 在 `#define(greet(name))` 之前 → `Hi Sam`）；嵌套 define 可注册调用；函数体内 `#return`/`#break` 不外泄（`#define(f())A#return#end#@f()|TAIL` → `A|TAIL`；`#define(f())X#break#end#for(i:list)#@f()#end` 对 `[1,2,3]` → `XXX`）。
