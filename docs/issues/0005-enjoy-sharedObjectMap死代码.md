# ISSUE-0005 — enjoy `sharedObjectMap` 为死代码

> **编号**：0005　**状态**：🟢 已修复　**严重程度**：⚠️ 一般
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

- 修复提交 / PR：（待提交）
- 改动（对照 Java `Scope(data, sharedObjectMap)` + `Scope.get` 回退 `sharedObjectMap` + `Scope.getSharedObject`）：
  - `enjoy/scope.go`：
    - `Scope` 新增 `sharedObjectMap map[string]interface{}` 字段；`NewChild()` 继承父层 `sharedObjectMap`（对照 Java 子 Scope 构造 `this.sharedObjectMap = parent.sharedObjectMap`）。
    - 保留 `NewScope(data)` 向后兼容（内部委托 `NewScopeWithShared(data, nil)`）；新增 `NewScopeWithShared(data, sharedObjectMap)` 创建带共享对象的顶层 Scope（对照 Java `new Scope(data, sharedObjectMap)`）。`NewScope` 签名不变 → `stat_parser.go` 两处隔离作用域与既有测试零改动。
    - 重写 `Get`：先沿 parent 链查 `data`（命中即返回），未命中再沿链回退 `sharedObjectMap`（任一层持有的共享对象都算，比 Java「仅本层」更健壮，覆盖 include 等带 parent 链的隔离作用域）。
    - 新增 `GetSharedObject(key)`：沿链查共享对象（对照 Java `Scope.getSharedObject`）。
    - `Exists` 不回退共享对象（对照 Java `Scope.exists` 仅查 data 链），保持不变。
  - `enjoy/template.go`：`Render`/`RenderToString` 改用 `NewScopeWithShared(data, t.sharedObjectMap())`，新增私有 `sharedObjectMap()` 从 `t.env.GetEngineConfig().sharedObjectMap` 取（带 env/config nil 兜底）。这是「死代码」变活的接线点。
  - `_example/enjoy_test/shared_object_test.go`（新增，黑盒 `package enjoy_test`）：`TestSharedObjectFallback`（`AddSharedObject` 注册的字符串/整型在 `#(x)` 可取）、`TestSharedObjectMissStillEmpty`（未注册标识符仍输出空）、`TestSharedObjectShadowedByData`（局部 data 同名 key 优先）、`TestSharedObjectInForBody`（for 循环体 NewChild 子作用域内可访问共享对象）、`TestSharedObjectScopeAPI`（`NewScopeWithShared`/`Get`/`GetSharedObject`/`NewChild` 作用域 API 直测，含普通 `NewScope` 不回退的对照）、`TestSharedObjectMethodCall`（sharedObject 的主要用途：`#(tool.Up("hi"))` 反射调方法 → `HI`，并对照同一对象经 data 注入同样可调，证明取值/方法调用能力两者等价）。
- 校验：`go build ./enjoy` / `go vet ./enjoy` 0 新错；下游 `db` / `server` / `tools/generator` `go build ./...` 均通过；`go test ./_example/enjoy_test` 全绿（含 6 个新增测试）。
- 验收：
  - `engine.AddSharedObject("greeting","hello")` + `#(greeting)` → `hello` ✓
  - `AddSharedObject("count", 42)` + `#(count)` → `42` ✓
  - `AddSharedObject("tool", upperUtil{})` + `#(tool.Up("hi"))` → `HI`（方法调用链路打通：`MethodExpr` 经 `scope.Get` 取到对象 → `reflect.MethodByName`）✓
  - data 同名 key 遮蔽共享对象（data 链先命中）✓
  - `#for` 体内（NewChild）可取共享对象 ✓
  - 未注册标识符 `#(missing)` 仍输出空，不凭空造值 ✓
