# ISSUE-0011 — enjoy 作用域与取值缺陷（Set 不向上 / 赋值不支持索引键 / Field 不支持 getter）

> **编号**：0011　**状态**：🔴 未处理　**严重程度**：⚠️ 一般
> **发现日期**：2026-07-16　**相关任务**：enjoy 模块（对照 `docs/java-go-comparison.md` §3.2）

## 问题描述

三处相互关联的作用域 / 取值缺陷，影响模板变量的改写与对象字段访问。

## 期望行为（对照 Java）

1. `Scope.Set` 自内向外查找已存在变量并就地改写，找不到才存根。
2. 赋值支持索引键：`map[key]=v` / `list[i]=v` / `array[i]=v`，可无限连写。
3. Field 取值优先级：`getXxx()` getter → public field → `Model/Record/Map.get()`。

## 实际行为（Go 现状）

1. `Scope.Set`（`scope.go:29-34`）只写当前层，`#for` 内 `#set(x=...)` 无法改写外层变量。
2. `expr_parser.go:45-49` 强制赋值左侧为 `IDExpr`，仅支持 `ID = expr`。
3. `getField`（`expr_eval.go:327-351`）只 `reflect.FieldByName` + Map index，只暴露 getter 的 POJO 取不到字段。

## 影响范围

循环内改写外层变量、向 map/list 赋值、以 getter 为主的 Java 风格 POJO 模板渲染。

## 相关文件 / 符号

- `enjoy/scope.go:29-34` — `Set` 不向上查找
- `enjoy/expr_parser.go:45-49` — 赋值左侧强制 `IDExpr`
- `enjoy/expr_eval.go:327-351` — `getField` 不调 getter
- 对照 Java：`aifei-enjoy/stat/Scope.java`、`expr/ast/Assign.java`、`expr/ast/Field.java`

## 建议方案

`Set` 沿 parent 链查找；赋值左侧支持 `Index`/`Field` 表达式（递归求址后写）；`getField` 增加 `GetXxx()` 方法探测（首字母大写）。

## 解决记录

- 修复提交 / PR：
- 改动：
- 校验：`go build ./...` / `go vet ./...` 改动文件 0 新错
- 验收：
