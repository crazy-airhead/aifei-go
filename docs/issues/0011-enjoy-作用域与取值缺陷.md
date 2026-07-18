# ISSUE-0011 — enjoy 作用域与取值缺陷（Set 不向上 / 赋值不支持索引键 / Field 不支持 getter）

> **编号**：0011　**状态**：🟢 已修复　**严重程度**：⚠️ 一般
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

- 修复提交 / PR：`fix: 修复问题0011`
- 改动：
  - `enjoy/scope.go` — `Set` 改为自内向外查找已存在变量（`containsKey` 语义）并就地改写，整条链未命中才存顶层（对照 Java `Scope.set`）；`SetLocal` 不再复用 `Set`，改为仅写当前层（对照 Java `Scope.setLocal`）；`Exists` 顺带修 nil-data panic 并对齐 Java `containsKey` 语义。`SetGlobal` 不变（`s.global.Set` 现自然落到顶层）。
  - `enjoy/expr_parser.go` — `parseAssign` 左侧除 `*IDExpr` 外再接受 `*IndexExpr`，右结合递归支持无限连写 `a = m[k] = arr[i=0] = v`（对照 Java `Assign`）。
  - `enjoy/expr_eval.go` — `AssignExpr` 新增 `Target` 字段（索引赋值左侧 `*IndexExpr`），`Eval` 分派普通/索引两种赋值；新增 `assignElement`（按 Java 顺序先 container→index→value 求值，宽松不抛异常）与 `setIndex`（map `SetMapIndex` / slice、array `Index().Set()`，类型不匹配或不可寻址静默跳过）。`getField` 改为 Java 优先级：`GetXxx()` getter（首字母大写、零参，值/指针接收者均测）→ 导出 struct 字段 → map key；新增 `firstCharToUpperCase`。`forEntry` 注释随之订正。
  - `enjoy/stat_parser.go` — `#set/#setLocal/#setGlobal` 由「按首个 `=` 拆字面名字」改为整体 `ParseExpr` 解析（`SetStat` 改持 `Assign *AssignExpr`），使 `#set(m['k']=v)`、`#set(arr[2]=99)`、`#set(a=b=1)` 生效，并顺带消除 `#set(x = a==b)` 的 `==` 误拆。`ForStat` 循环变量与 `for` 状态、`callDefine` 形参、`IncludeStat` 赋值参数全部由 `Set` 改 `SetLocal`（对照 Java `For.setLocal` / `Define.setLocal` / `Include.setLocalAssignment`），避免被新的向上查找误写到顶层。
  - `enjoy/builtin_directives.go` — `#include` 指令的赋值参数绑定由 `Set` 改 `SetLocal`（同上理由，对照 Java `Include.evalAssignExpression` 的 `setLocalAssignment`）。
  - `_example/enjoy_test/stat_parser_test.go` — `TestCallLocalDoesNotLeak`（0010 旧用例，依赖旧 Set「只写当前层」）改为 `TestCallSetLeaksToCaller`：define 函数体内 `#set` 走 wisdom 会改写调用方同名变量（与 `#for` 体一致，忠于 Java），期望 `[inner]`；并新增 `TestCallParamIsLocal` 守住「形参仍是局部 setLocal」。
  - `_example/enjoy_test/issue0011_test.go` — 新增 13 个用例覆盖三处修复：Set 向上查找（for 体改写外层、嵌套循环、循环变量局部回归）；索引赋值（map 写入/覆盖、list[i]、连写 `a=m[k]=7`、索引内赋值 `arr[i=0]=11`、表达式内赋值）；Field getter（值接收者、指针接收者、getter 优先于同名字段、无 getter 回退导出字段）。
- **关键决策（与 0010 的冲突）**：0011 让 `Set` 向上查找后，`#define` 函数体内的 `#set` 也会改写调用方变量（这正是 Java 的真实行为，已核对 `Define.call` 的 `new Scope(callerScope)` + `Scope.set`）。此举与 0010 加的 `TestCallLocalDoesNotLeak`（期望 define 内 `#set` 不外泄）冲突。经确认为「**忠于 Java：define 内 #set 也外泄**」，故改写该用例；仅 `#define` 形参仍按 Java 用 `setLocal` 局部绑定。
- 校验：`go build` / `go vet`（`enjoy`、`_example/enjoy_test`）0 新错；`go test` 全绿——`enjoy_test`（含 0011 新用例与改写的 define 用例）、`db`、`server`、`tools/generator`、`tools/damigen`、`_example/{db_sqlite_test,cache_redis_test,kafka_test,demo}`、全部 `plugins/*`。
- 验收：`#set(x=0)#for(i:[1,2,3])#set(x=x+i)#end#(x)` → `6`（循环改写外层）；`#set(m['k']='v')#(m['k'])` → `v`（map 索引赋值）；`#set(a=m['k']=7)#(a)-#(m['k'])` → `7-7`（连写）；私有字段 POJO `#(user.name)` 经 `GetName()` → `james`（getter）；`#(user.Name)` 在同名字段+getter 并存时 → `FROM-GETTER`（getter 优先）。
