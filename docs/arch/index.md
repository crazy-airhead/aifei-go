# 架构设计

Aifei-Go 的设计文档索引：从 Java 版 Aifei 的移植总览、六阶段实施方案，到各子系统的专项设计。

## 总览与对照

- [实施方案总览](00-overview.md) —— Java→Go 移植的总体规划、项目结构与设计决策
- [Java → Go 对照](java-go-comparison.md) —— Java 机制与 Go 实现的逐项映射
- [Java v1.1.0 同步](java-v1.1.0-sync.md) —— Java 版 v1.1.0 演进的跟进落地记录

## 六阶段实施

- [Phase 1 · 核心框架](01-phase1-core.md)
- [Phase 2 · Enjoy 引擎](02-phase2-enjoy.md)
- [Phase 3 · db 模块](03-phase3-db.md)
- [Phase 4 · 工具库](04-phase4-utils.md)
- [Phase 5 · 高级特性](05-phase5-advanced.md)
- [Phase 6 · 示例](06-phase6-example.md)

## 专项设计

- [多表关联映射](multi-table-mapping.md)
- [数据隔离](data-isolate.md)
- [日志插件](log-plugin.md)
- [微服务规划](microservice.md)
- [可观测性](observability.md)

## 子系统设计

- **Dami**：[01 · Go 生态对照](dami/01-go-comparison.md) / [02 · 迁移设计](dami/02-migration-design.md)
- **Flow**：[00 · 总览](flow/00-overview.md) / [01 · Go 对照](flow/01-go-comparison.md) / [02 · 核心设计](flow/02-core-design.md) / [03 · 配置与求值](flow/03-config-and-eval.md) / [04 · 工作流设计](flow/04-workflow-design.md) / [05 · TDD 计划](flow/05-tdd-plan.md) / [06 · MySQL 仓储](flow/06-mysql-repository.md)
- **Nami**：[01 · Java 对照](nami/01-java-comparison.md) / [02 · 契约设计](nami/02-design.md)
