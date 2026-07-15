# ISSUE-0018 — aifei Input 缺少时间类型访问器

> **编号**：0018　**状态**：🔴 未处理　**严重程度**：💡 体验
> **发现日期**：2026-07-16　**相关任务**：aifei 模块（对照 `docs/java-go-comparison.md` §1.1）

## 问题描述

Java `Input` 提供 `getDate` / `getLocalDate` / `getLocalTime` / `getLocalDateTime`，按字符串长度自动推断日期格式。Go `aifei/input.go` 接口与 `http/context.go` 实现均无任何时间类型访问器，只能 `GetStr` 后自行 `time.Parse`。

## 期望行为

`Input` 提供时间访问器，支持显式 layout 与按字符串长度的默认格式推断，如 `GetTime(key string, layout ...string) (time.Time, error)`。

## 实际行为（Go 现状）

`aifei/input.go:22-49` 与 `http/context.go` 无时间访问器；`GetBean(&t)` 对 `time.Time` 也不友好。

## 影响范围

表单 / 查询参数含日期时间的接口（日期范围查询、调度时间提交等）。

## 相关文件 / 符号

- `aifei/input.go:22-49` — `Input` 接口无时间方法
- `http/context.go` — `HttpContext` 实现
- `server/in.go` — `In` 实现
- 对照 Java：`aifei/core/Input.java` `getDate` / `getLocalDate` / ...

## 建议方案

`Input` 增加 `GetTime(key string, layout ...string) (time.Time, error)`，无 layout 时按字符串长度匹配常见格式（参考 Java `getLocalDateTime` 的长度推断）；在 `HttpContext` / `In` 实现。

## 解决记录

- 修复提交 / PR：
- 改动：
- 校验：`go build ./...` / `go vet ./...` 改动文件 0 新错
- 验收：
