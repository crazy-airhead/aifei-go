# ISSUE-0021 — enjoy `#(name??)` 后缀省略不支持 + 表达式首尾空白越界 panic

> **编号**：0021　**状态**：🟢 已解决　**严重程度**：🚨 阻断
> **发现日期**：2026-09-28　**相关任务**：enjoy（表达式解析器/词法器）

## 问题描述

两个叠加问题：

1. **`??` 右操作数省略（后缀）写法不支持**。Java 版 Enjoy 支持 `#(name??)` / `#(name ?? )`（左值为 null 时输出空），Go 版 `parseAtom` 对 `)` 等终结符 token 一律报 `unexpected token`。
2. **表达式内容首尾带空白时词法器越界 panic**。`NewExprLexer` 对 `input` 做了 `TrimSpace` 但 `length` 仍取未修剪长度，`#(name ?? )`、`#( name )`、`#if( x )` 这类括号内带空白的写法在 `pos == len(trimmed) < length` 处 `index out of range`，被 stat 层 recover 成 `template parse: runtime error: ...` 错误——整张模板不可用。

## 复现步骤

```go
engine.GetTemplateByString("[#(name??)]").RenderToString0(nil)
// line 1: output expression error: unexpected token: 0 ()

engine.GetTemplateByString("[#(name ?? )]").RenderToString0(nil)
// template parse: runtime error: index out of range [7] with length 7

engine.GetTemplateByString("[#( name )]").RenderToString0(nil)
// 同上越界（首部空白同理）
```

## 期望行为

对照 Java（aifei-enjoy）：

- `ExprParser.atom()`（ExprParser.java:545-558）对 RPAREN/RBRACK/RBRACE/RANGE/COLON/QUESTION/AND/OR/EQUAL/NOTEQUAL/COMMA/SEMICOLON/EOF 这些**不可能作为表达式开头**的 token 返回 null 而非抛错，注释明确 `// support "(a.b ??)"` 等；
- `NullSafe` AST 构造器只要求 left 非空，**right 允许为 null**；eval 为 `right != null ? right.eval(scope) : null` → `#(name??)` 在 name 为 null 时输出空；
- 其余 AST 构造器（Arith/Logic/Ternary）对 null 操作数给干净 ParseException，而非运行期 NPE。

即：`#(name??)` 应输出 name 的值（非 null）或空（null）；`#( a )` 应正常输出。

## 实际行为

见复现步骤。附带影响：所有既有用例都没写括号内空白（`#( name )` / `#if( x )` 形态零覆盖），故越界一直未暴露。

## 影响范围

- 模板路径全部输出/指令表达式：`#(...)`、`#if(...)`、`#set(...)`、`#for(...)` 等（内容均未经 trim 直达 `NewExprLexer`，见 `lexer.go` `scanOutput`/`scanDirective` 的 para 提取）。
- 从 Java 迁移的模板里 `#(title ??)` 后缀写法直接解析失败。
- 独立解析路径 `enjoy.ParseExpr`（db SqlKit 等）同样受益于越界修复。

## 相关文件 / 符号

- `enjoy/expr_lexer.go` — `NewExprLexer`：`length` 改取修剪后长度（越界根因）
- `enjoy/expr_parser.go` — `parseAtom`：新增终结符 token 集返回 `(nil, nil)`（对照 Java `atom()` null 分支，token 集逐一对应，Go 无 SEMICOLON 故缺席）；`opTargetBlank` 错误助手 + 六个二元循环（or/and/eq-ne/lt-le-gt-ge/add-sub/mul-div-mod）双侧校验、`parseTernary` 三参校验、`parseNullSafe` 仅左校验（右可空）、`parseUnary` 一元操作数校验、`parseAssign` 右值校验、`parseArrayOrRange`/`parseMap`/`parseCallArgs` 元素校验（对照 Java Arith/Logic/Ternary/NullSafe/Output 构造器 null 检查）；`parseExprWithConfig` 顶层 nil 兜底（对照 Java Output "can not be blank"）
- `enjoy/expr_eval.go` — `NullCoalesceExpr.Eval`：`Right` 为 nil 时返回 nil（对照 Java NullSafe.eval）

## 解决记录

- 修复提交 / PR：本次会话（v0 分支工作树）
- 改动：上述文件。合法二元/链式/嵌套 `??` 行为不变；新增后缀省略形态与 Java 逐例对齐（`(a.b ??)`、`[start ?? .. end]`、`{key : value ??}`、`c ?? ? a : b`、`c ? a ?? : b`、`a.b ?? && expr` 等）。
- 校验：`go build` 全模块通过；`_test/enjoy_test` 全绿（含新增 4 组用例），`_test/db_test`、`_test/flow_test`、`_test/flow_plugin_test`、`_test/generator_test`、`_test/damigen_test` 及其余 `_test/*_test` 全绿，demo 可编译。
- 验收：`_test/enjoy_test/null_coalesce_suffix_test.go`——后缀基本形态（紧贴/带空格/级联/链式/三目与逻辑混排）、复合上下文（数组元素、Map 值、区间起点、赋值右值、括号包裹）、首尾空白回归（`#( name )` 等 4 例）、空操作数干净报错 9 例（不再出现 `runtime error`）。
