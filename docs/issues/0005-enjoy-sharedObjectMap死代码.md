# ISSUE-0005 — enjoy `sharedObjectMap` 为死代码

> **编号**：0005　**状态**：🔴 未处理　**严重程度**：⚠️ 一般
> **发现日期**：2026-07-16　**相关任务**：enjoy 模块（对照 `docs/java-go-comparison.md` §3.1 Bug #4）

## 问题描述

`Template.AddSharedObject` 声称注册共享对象供模板访问，但 `Scope.Get` 在找不到变量时未回退到 `sharedObjectMap`，导致注册的对象在模板里取不到。

## 复现步骤

1. `engine.AddSharedObject("now", time.Now())` 或 template 级注册
2. 模板 `#(now)` → 取不到，报未定义

## 期望行为

作用域链查不到的标识符回退到 `sharedObjectMap`。

## 实际行为

不回退，`sharedObjectMap` 永不生效，`AddSharedObject` 是无效 API。

## 影响范围

所有依赖共享对象注入模板的场景。

## 相关文件 / 符号

- `enjoy/scope.go:18-26` — `Get` 只查 `data`/`parent`
- `enjoy/template.go:126` — `AddSharedObject` 注册
- 对照 Java：`aifei-enjoy/EngineConfig.sharedObjectMap` + Scope 回退

## 建议方案

`Scope.Get` 在 `data`/`parent` 未命中后回退 `sharedObjectMap`；或在模板执行时把 `sharedObjectMap` 作为根 scope 数据。

## 解决记录

- 修复提交 / PR：
- 改动：
- 校验：`go build ./...` / `go vet ./...` 改动文件 0 新错
- 验收：
