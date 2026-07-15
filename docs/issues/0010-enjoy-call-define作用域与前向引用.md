# ISSUE-0010 — enjoy `#call` / `#define` 作用域隔离与前向引用

> **编号**：0010　**状态**：🔴 未处理　**严重程度**：⚠️ 一般
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

- 修复提交 / PR：
- 改动：
- 校验：`go build ./...` / `go vet ./...` 改动文件 0 新错
- 验收：
