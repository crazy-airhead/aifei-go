# 问题登记（doc/issues/）

> 登记开发 / 使用中发现的问题，按编号归档。每个问题一个文件，复制 `_TEMPLATE.md` 起新问题。

## 命名约定

- 文件名：`<编号>-<简短标题>.md`，编号四位零填充、递增。例：`0001-右侧面板空白.md`、`0002-提交后状态不刷新.md`。
- 也可只用 `0001.md`，标题写在文件内首行。
- 编号不复用：废弃问题改状态为「⚪ 不修复」，不删除、不挪号。

## 状态图例

| 标记 | 含义 |
|------|------|
| 🔴 | 未处理 |
| 🟡 | 进行中 |
| 🟢 | 已修复 |
| ⚪ | 不修复 / 无需修复 |

## 解决记录约定

一个问题可能经**多轮调整**才最终收敛（如 0010 经多轮实机反馈迭代、0012 经 4 轮方向校正）。解决记录按下列方式记，便于回溯每一轮的反馈与改动：

- **单轮问题**：填「改动（文件级）+ 校验 + 验收」即可。
- **多轮问题**：按「**第 N 轮**」逐条追加，每轮写：
  - **反馈 / 触发**：用户或实机反馈原话（引号），或本轮要解决的偏差。
  - **根因**：上轮遗留或本轮发现的根本原因。
  - **处理**：文件 / 符号级改动（`file.vue` — 改了什么）。
  - **校验**：`pnpm typecheck` / `go build ./...` / `go vet ./...` 改动文件 0 新错（或注明未通过项）。
  - **遗留**：本轮未解决、交给下一轮的问题（无则省）。
- 「方案」节作为**最终设计速查**保留；「解决记录」是**按轮次的实施轨迹**。两者并存，不要因多轮而删方案。
- 未最终验收前状态保持 🟡 进行中；多轮全部收敛并验收后再改 🟢 已修复。范例见 0010 / 0012。

## 问题索引

> 新增问题后在此追加一行（可手写，也可让 Claude 维护）。

| 编号 | 标题 | 状态 | 相关任务 | 文件 |
|------|------|------|----------|------|
| 0001 | db 事务回调内 SQL 各自独立提交（不在同一事务） | 🟢 | db | [0001-db-事务不传播](0001-db-事务不传播.md) |
| 0002 | enjoy 算术 `+` 不支持字符串拼接、数值降级 float64 | 🟢 | enjoy | [0002-enjoy-算术加号降级float64](0002-enjoy-算术加号降级float64.md) |
| 0003 | enjoy `#returnIf` 被当成无条件 return | 🟢 | enjoy | [0003-enjoy-returnIf语义错误](0003-enjoy-returnIf语义错误.md) |
| 0004 | enjoy `#for` 无法迭代 Map | 🟢 | enjoy | [0004-enjoy-for无法迭代map](0004-enjoy-for无法迭代map.md) |
| 0005 | enjoy `sharedObjectMap` 为死代码 | 🟢 | enjoy | [0005-enjoy-sharedObjectMap死代码](0005-enjoy-sharedObjectMap死代码.md) |
| 0006 | db `Row.Keep()` 未清理 change 集合 | 🟢 | db | [0006-db-Row-Keep未清理change](0006-db-Row-Keep未清理change.md) |
| 0007 | enjoy 内置指令全部缺失（#escape/#date/#number/#random/#render/#string） | 🟢 | enjoy | [0007-enjoy-内置指令全部缺失](0007-enjoy-内置指令全部缺失.md) |
| 0008 | enjoy 缺共享方法库与类型扩展方法 | 🟢 | enjoy | [0008-enjoy-缺共享方法与扩展方法](0008-enjoy-缺共享方法与扩展方法.md) |
| 0009 | enjoy `#for` 缺 else 分支与循环状态变量 | 🟢 | enjoy | [0009-enjoy-for缺else与循环状态变量](0009-enjoy-for缺else与循环状态变量.md) |
| 0010 | enjoy `#call`/`#define` 作用域隔离与前向引用 | 🟢 | enjoy | [0010-enjoy-call-define作用域与前向引用](0010-enjoy-call-define作用域与前向引用.md) |
| 0011 | enjoy 作用域与取值缺陷（Set/赋值/Field getter） | 🟢 | enjoy | [0011-enjoy-作用域与取值缺陷](0011-enjoy-作用域与取值缺陷.md) |
| 0012 | enjoy 语义差异与 EngineConfig 配置项杂项 | 🟡 | enjoy | [0012-enjoy-语义差异与配置项杂项](0012-enjoy-语义差异与配置项杂项.md) |
| 0013 | db 缺 Oracle/SqlServer/H2/Informix 方言 | 🔴 | db | [0013-db-缺少4种方言](0013-db-缺少4种方言.md) |
| 0014 | db SqlKit SQL 文件加载与热重载为空实现 | 🔴 | db | [0014-db-SqlKit文件加载为空实现](0014-db-SqlKit文件加载为空实现.md) |
| 0015 | db 缺少原生连接逃逸口 call/FunExecutor | 🔴 | db | [0015-db-缺原生连接逃逸口](0015-db-缺原生连接逃逸口.md) |
| 0016 | db 细节补强（Batch/TypeConverter/复合主键/事务返回值/杂项） | 🔴 | db | [0016-db-细节补强](0016-db-细节补强.md) |
| 0017 | aifei 缺少路由表内省 API（Walk/Routes） | 🔴 | aifei | [0017-aifei-缺路由表内省API](0017-aifei-缺路由表内省API.md) |
| 0018 | aifei Input 缺少时间类型访问器 | 🔴 | aifei | [0018-aifei-Input缺时间访问器](0018-aifei-Input缺时间访问器.md) |
| 0019 | enjoy 无参指令（#else/#end/#break/#continue/#default）吞行内文本 | 🟢 | enjoy | [0019-enjoy-无参指令吞行内文本](0019-enjoy-无参指令吞行内文本.md) |

