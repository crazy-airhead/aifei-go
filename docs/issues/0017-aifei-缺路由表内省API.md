# ISSUE-0017 — aifei 缺少路由表内省 API（Walk / Routes）

> **编号**：0017　**状态**：🔴 未处理　**严重程度**：⚠️ 一般
> **发现日期**：2026-07-16　**相关任务**：aifei 模块（对照 `docs/java-go-comparison.md` §1.1）

## 问题描述

Java `Router.getActionMapping()` 返回 `actionPath → Action` 的全量映射，用于权限 key 生成、OpenAPI 文档生成、调试输出。Go `aifei/router.go` 仅提供 `Lookup(method, path)`，无枚举全部已注册路由的 API。

## 期望行为

提供遍历全部已注册路由的 API，如 `Walk(visit func(method, path string, handlers []HandlerFunc))` 或 `Routes() []RouteInfo`，供权限/文档/调试模块消费。

## 实际行为（Go 现状）

`aifei/router.go` 只有 `Lookup`，无法在启动后枚举路由表。

## 影响范围

基于路由自动注册权限表、自动生成接口文档、启动期路由自检、调试输出路由清单。

## 相关文件 / 符号

- `aifei/router.go` — `Router` 仅有 `Lookup`，无遍历入口
- 对照 Java：`aifei/router/Router.java` `getActionMapping()`

## 建议方案

为 `Router` 增加 `Walk(visit func(method, path string, handlers []HandlerFunc))`，遍历每棵 method 树的 radix 节点回调；`RouterGroup` 聚合时一并覆盖。

## 解决记录

- 修复提交 / PR：
- 改动：
- 校验：`go build ./...` / `go vet ./...` 改动文件 0 新错
- 验收：
