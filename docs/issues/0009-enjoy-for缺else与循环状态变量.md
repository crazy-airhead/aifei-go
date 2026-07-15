# ISSUE-0009 — enjoy `#for` 缺 else 分支与循环状态变量

> **编号**：0009　**状态**：🔴 未处理　**严重程度**：💡 体验
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

- 修复提交 / PR：
- 改动：
- 校验：`go build ./...` / `go vet ./...` 改动文件 0 新错
- 验收：
