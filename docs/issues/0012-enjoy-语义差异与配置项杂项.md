# ISSUE-0012 — enjoy 语义差异与 EngineConfig 配置项杂项

> **编号**：0012　**状态**：🔴 未处理　**严重程度**：💡 体验
> **发现日期**：2026-07-16　**相关任务**：enjoy 模块（对照 `docs/java-go-comparison.md` §3.2 / §3.4）

## 问题描述

enjoy 多项低优先级语义差异与 `EngineConfig` 配置项缺失，影响模板兼容性与可观测性。

## 期望行为（应有功能清单）

- [ ] `??` 优先级：Java 为低优先级（可链式 `a ?? b ?? c`）；Go 当 postfix 高优先级
- [ ] `#@name?()` 安全调用：Go 把 `?` 并入函数名导致 `GetFunction("name?")` 失败并静默跳过
- [ ] `::` 静态访问：Java 真正 `StaticMethod`/`StaticField`（按类名反射，默认关闭）；Go 当 `IDExpr` 上的 field/method 且默认开启（伪实现）
- [ ] `#include` 相对路径：Java 相对父文件目录；Go 只相对 baseTemplatePath
- [ ] Call 参数个数不匹配：Java 抛异常；Go 静默忽略
- [ ] 错误无行号定位：Java `Location`/`ParseException` 带文件名+行号；Go `errorStat` 只输出错误字符串
- [ ] `EngineConfig` 配置项：缺 `compressor` / `outputDirectiveFactory` / `sourceFactory` / `sharedMethodKit` / `keepLineBlankDirectives` / `roundingMode` / `staticMethod` / `Field` / `addSharedFunction(file)`

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

- 修复提交 / PR：
- 改动：
- 校验：`go build ./...` / `go vet ./...` 改动文件 0 新错
- 验收：
