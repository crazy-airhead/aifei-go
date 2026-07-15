# ISSUE-0004 — enjoy `#for` 无法迭代 Map

> **编号**：0004　**状态**：🔴 未处理　**严重程度**：⚠️ 一般
> **发现日期**：2026-07-16　**相关任务**：enjoy 模块（对照 `docs/java-go-comparison.md` §3.1 Bug #3）

## 问题描述

`#for x : collection` 对迭代源只接受 slice/array 的 reflect kind，传入 map 时被当作单个元素，循环体只执行一次且取到 map 本身。

## 复现步骤

1. 模板 `#for(entry : myMap) { #(entry.key)=#(entry.value) }`
2. 观察：循环 0/1 次，输出错误，无法遍历 map 的 entry

## 期望行为

支持 Collection / Map(Entry) / 数组 / Iterator / Iterable / 单对象（非集合自动包成单元素列表），Map 迭代产生 key/value 项。

## 实际行为

map 因 `kind != Slice/Array` 被当作单元素，迭代失败。

## 影响范围

模板遍历 map 字典的所有场景。

## 相关文件 / 符号

- `enjoy/stat_parser.go:91-108` — `ForStat` 构建
- `enjoy/expr_eval.go:512-525` — `toSlice` 仅接受 Slice/Array kind
- 对照 Java：`aifei-enjoy/stat/ast/For.java` + `ForIteratorStatus`（支持 Collection/Map/数组/Iterator/Iterable/Enumeration/单对象）

## 建议方案

扩展 `toSlice` 支持 map（转 `[]entry{key,value}`）、ptr-to-slice；非集合单对象包成单元素列表。

## 解决记录

- 修复提交 / PR：
- 改动：
- 校验：`go build ./...` / `go vet ./...` 改动文件 0 新错
- 验收：
