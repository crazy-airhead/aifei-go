# Aifei Go 实施提示词 — 快速开始指南

## 使用方法

1. 创建目标目录: `mkdir aifei-go && cd aifei-go`
2. 初始化 git: `git init`
3. 按阶段顺序执行 docs/prompts/ 中的提示词
4. 每个阶段完成后运行验证提示词

## 提示词执行顺序

| 步骤 | 文件 | 内容 | 输出文件 |
|------|------|------|----------|
| 1 | `prompts/01-phase1-core.md` Prompt 1.1 | 项目初始化 + Context | go.mod, aifei.go, context.go |
| 2 | `prompts/01-phase1-core.md` Prompt 1.2 | Handler + Middleware | handler.go, middleware.go |
| 3 | `prompts/01-phase1-core.md` Prompt 1.3 | Router | router.go |
| 4 | `prompts/01-phase1-core.md` Prompt 1.4 | Config + Plugin + Util | config.go, plugin.go, util.go, prop.go |
| 5 | `prompts/01-phase1-core.md` 验证 | Phase 1 验证 | main.go (测试用) |
| 6 | `prompts/02-phase2-enjoy.md` Prompt 2.1 | Engine + Scope + Env | enjoy/*.go |
| 7 | `prompts/02-phase2-enjoy.md` Prompt 2.2 | Expr Lexer/Parser/AST | enjoy/expr/*.go |
| 8 | `prompts/02-phase2-enjoy.md` Prompt 2.3 | Stat Lexer/Parser/AST | enjoy/stat/*.go |
| 9 | `prompts/02-phase2-enjoy.md` Prompt 2.4 | Source + SqlKit 集成 | enjoy/source/*.go, db/sql/*.go |
| 10 | `prompts/02-phase2-enjoy.md` 验证 | Phase 2 验证 | enjoy/enjoy_test.go, db/sql/sql_test.go |
| 11 | `prompts/03-phase3-db.md` Prompt 3.1 | DB Config + Dialect | db/config.go, db/dialect*.go |
| 12 | `prompts/03-phase3-db.md` Prompt 3.2 | Row + TypeConverter | db/row.go, db/type_converter.go |
| 13 | `prompts/03-phase3-db.md` Prompt 3.3 | Dao + Page | db/dao.go, db/page.go |
| 14 | `prompts/03-phase3-db.md` Prompt 3.4 | Db入口 + Batch + Transaction | db/db.go, db/batch.go, db/transaction.go, db/operator.go |
| 15 | `prompts/03-phase3-db.md` 验证 | Phase 3 验证 | db/db_test.go |
| 16 | `prompts/04-phase4-utils.md` Prompt 4.1 | JSON 模块 | json/json.go |
| 17 | `prompts/04-phase4-utils.md` Prompt 4.2 | 日志模块 | log/log.go |
| 18 | `prompts/05-phase5-advanced.md` Prompt 5.1 | 完善内置 Middleware | middleware.go 更新 |
| 19 | `prompts/05-phase5-advanced.md` Prompt 5.2 | Struct 注册增强 | router.go 更新 |
| 20 | `prompts/06-phase6-example.md` Prompt 6.1 | 完整示例 | _example/demo/main.go |
| 21 | `prompts/06-phase6-example.md` Prompt 6.2 | 集成测试 | *_test.go |
| 22 | `prompts/06-phase6-example.md` Prompt 6.3 | 最终整理 | doc.go, 注释完善 |

## 目标文件清单

```
aifei-go/
├── go.mod
├── go.sum
├── doc.go
├── aifei.go
├── context.go
├── handler.go
├── middleware.go
├── router.go
├── config.go
├── plugin.go
├── util.go
├── prop.go
│
├── enjoy/                         *** Enjoy 模板引擎 (特色) ***
│   ├── engine.go
│   ├── engine_config.go
│   ├── template.go
│   ├── env.go
│   ├── directive.go
│   ├── scope.go
│   ├── ctrl.go
│   ├── expr/
│   │   ├── ast.go
│   │   ├── id.go, const.go, arith.go, compare.go, logic.go
│   │   ├── ternary.go, null_safe.go, field.go, method.go
│   │   ├── index.go, assign.go, array.go, map.go, range.go
│   │   ├── expr_lexer.go, expr_parser.go
│   │   └── shared_method.go
│   ├── stat/
│   │   ├── ast.go, token.go, location.go
│   │   ├── lexer.go, parser.go
│   │   ├── stat_list.go, text.go, output.go
│   │   ├── if.go, for.go, set.go, define.go
│   │   ├── include.go, call.go, switch.go
│   │   ├── flow.go (break/continue/return)
│   │   └── null_stat.go
│   ├── io/
│   │   └── writer.go
│   └── source/
│       ├── source.go
│       ├── file_source.go
│       └── string_source.go
│
├── db/
│   ├── db.go
│   ├── dao.go
│   ├── row.go
│   ├── page.go
│   ├── batch.go
│   ├── operator.go
│   ├── condition.go
│   ├── dialect.go
│   ├── dialect_mysql.go
│   ├── dialect_postgres.go
│   ├── dialect_sqlite.go
│   ├── config.go
│   ├── type_converter.go
│   ├── transaction.go
│   └── sql/                      *** Enjoy SQL (基于 enjoy 引擎) ***
│       ├── sql_kit.go
│       ├── sql_para.go
│       ├── sql_directive.go
│       ├── para_directive.go
│       ├── where_directive.go
│       ├── and_directive.go
│       └── orderby_directive.go
│
├── json/
│   └── json.go
│
├── log/
│   └── log.go
│
└── _example/
    └── demo/
        ├── go.mod
        └── main.go
```

## 依赖关系

```
Phase 1 (Core) ──────────────────────────────────────────┐
                                                          │
Phase 2 (Enjoy) ──────────────────────┐                   │
                                      │                   │
Phase 3 (DB) ← 依赖 Phase 1 + Phase 2 (Enjoy SQL)         │
                                                          │
Phase 4 (JSON/Log) ← 独立，可与 Phase 2/3 并行              │
                                                          │
Phase 5 (Advanced) ← 依赖 Phase 1                         │
                                                          │
Phase 6 (Example) ← 依赖全部                                │
```

- Phase 1 和 Phase 2 可并行
- Phase 4 可与 Phase 2/3 并行
- Phase 3 依赖 Phase 2 (db/sql/ 使用 enjoy 引擎)
- Phase 6 依赖全部
