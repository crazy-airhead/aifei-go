# ISSUE-0007 — enjoy 内置指令全部缺失（#escape / #date / #number / #random / #render / #string）

> **编号**：0007　**状态**：🔴 未处理　**严重程度**：💡 体验
> **发现日期**：2026-07-16　**相关任务**：enjoy 模块（对照 `docs/java-go-comparison.md` §3.2 / §3.3）

## 问题描述

Java enjoy 默认注册 7 个内置指令，覆盖日期格式化、HTML 转义、数字格式化等模板常用能力；Go 版一个都没有注册，模板内无法完成这些常见渲染需求。

## 期望行为（应有功能清单）

- [ ] `#date(date, pattern?)` — 日期格式化
- [ ] `#escape(text) { ... }` — HTML 转义块 `< > " ' &`
- [ ] `#number(num, pattern?)` — 数字格式化（DecimalFormat）
- [ ] `#random` — 输出随机整数
- [ ] `#render(name)` — 动态渲染子模板
- [ ] `#string(name) { ... }` — 多行字符串变量定义
- [ ] `#call` — 动态调用（表达式函数名，区别于 `#@` 语法糖）

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

- 修复提交 / PR：
- 改动：
- 校验：`go build ./...` / `go vet ./...` 改动文件 0 新错
- 验收：
