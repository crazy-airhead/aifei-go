# ISSUE-0008 — enjoy 缺少共享方法库与类型扩展方法

> **编号**：0008　**状态**：🔴 未处理　**严重程度**：💡 体验
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

- 修复提交 / PR：
- 改动：
- 校验：`go build ./...` / `go vet ./...` 改动文件 0 新错
- 验收：
