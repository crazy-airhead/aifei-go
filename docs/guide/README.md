# Aifei-Go Guide

Aifei-Go 各模块的说明文档集合。每篇深入讲解一个模块的**背景定位 → 总体架构 → 核心 API → 核心机制 → 配置与集成 → 模块结构 → 总结**，所有 API、类型签名、配置键均逐项对照源码核实。

> 第一次接触本项目？先读 [Aifei-Go 框架总览](aifei-go.md) 了解整体设计（Just Service、HIO 架构、模块全景、快速开始），再按需深入下方各模块。

---

## 框架总览

| 文档 | 说明 |
|------|------|
| [aifei-go](aifei-go.md) | 整个框架的定位、核心理念、HIO 架构、模块全景、快速开始 |

## Core（核心框架）

| 文档 | 模块 | 说明 |
|------|------|------|
| [core](core.md) | `./aifei` | 核心包：`Aifei` 入口、radix tree 路由、`Input`/`Output`/`Handler`、`Interceptor` 方法级 AOP、`Config` 函数式选项、`Plugin` 生命周期 |

## Core Library（零外部依赖基础库）

| 文档 | 模块 | 说明 |
|------|------|------|
| [enjoy](enjoy.md) | `./enjoy` | 模板/表达式引擎：DKFF 词法 + DLRD 解析、全套指令、null-safe 与静态访问表达式 |
| [db](db.md) | `./db` | 数据库访问：`Db`/`Dao`/`Row` 变更追踪、`Batch`/`Transaction`、三方言、`db/sql` Enjoy SQL |
| [json](json.md) | `./json` | `encoding/json` 轻量封装，容错降级（出错返回 `{}`） |
| [log](log.md) | `./log` | 日志接口抽象，一行替换即可对接 zap/logrus/slog |
| [config](config.md) | `./config` | 分层加载（L1-L5）、泛型 `Props`/`Sub`/`Bind`、`CloudLoader` 扩展点 |

## Runtime（net/http 适配与生产启动）

| 文档 | 模块 | 说明 |
|------|------|------|
| [http](http.md) | `./http` | `net/http` ↔ aifei 桥接：`HttpContext`/`HttpHandler`/`Server` |
| [server](server.md) | `./server` | 生产启动层：`In`/`Out`、内置中间件、`Run` 优雅启停、`Register` 路由规则、`TxInterceptor` |
| [server 定制](server-customization.md) | 应用自带 | server 包定制方法：多模式响应、JWT、RPC 鉴权透传、定制路由 |

## Standalone Framework（独立框架，不依赖 aifei）

| 文档 | 模块 | 说明 |
|------|------|------|
| [nami](nami.md) | `./nami` | HTTP RPC 客户端框架：`Channel`/`Encoder`/`Filter`/`Upstream`/`Discovery` |
| [dami](dami.md) | `./dami` | 进程内事件总线：`send`/`call`/`stream`/`lpc` 四种交互模式 |

## Code Generation（代码生成）

| 文档 | 模块 | 说明 |
|------|------|------|
| [generator](generator.md) | `./tools/generator` | schema → 类型安全 per-table 代码（base/model/dao/service/tables） |
| [damigen](damigen.md) | `./tools/damigen` | dami 接口 → provider/client 代码（`//dami:provider` 注解驱动） |

## Plugin（可选集成，按需引入第三方库）

| 文档 | 模块 | 说明 |
|------|------|------|
| [cache](cache.md) | `./plugins/cache` | 本地（FreeCache/TinyLFU）+ Redis 两级缓存，基于 jetcache-go |
| [storage](storage.md) | `./plugins/storage` | 本地 + S3 兼容（Minio/OSS/COS）统一存储，基于 minio-go |
| [kafka](kafka.md) | `./plugins/kafka` | Kafka 生产/消费，at-least-once，基于 franz-go |
| [nacos](nacos.md) | `./plugins/nacos` | 服务注册 / 配置中心 / 发现，基于 nacos-sdk-go |
| [elasticsearch](elasticsearch.md) | `./plugins/elasticsearch` | Elasticsearch 客户端封装，基于 go-elasticsearch v8 |
| [xxljob](xxljob.md) | `./plugins/xxljob` | XXL-JOB 分布式任务调度执行器 |
| [swagger](swagger.md) | `./plugins/swagger` | knife4j-vue3 OpenAPI 文档 UI，基于 swaggo/swag |
| [dami-plugin](dami-plugin.md) | `./plugins/dami` | dami 事件总线的 aifei 插件封装（生命周期托管） |
| [data-isolate](data-isolate.md) | `./plugins/dataisolate` | 数据隔离：租户 + 行范围 + 列脱敏，AST SQL 改写 |

---

## 阅读建议

- **Web 服务开发主线**：[core](core.md) → [http](http.md) → [server](server.md) → [db](db.md) → [generator](generator.md)
- **模板与动态 SQL**：[enjoy](enjoy.md) → [db](db.md)（`db/sql`）
- **事件驱动**：[dami](dami.md) → [damigen](damigen.md) → [dami-plugin](dami-plugin.md)
- **RPC 调用**：[nami](nami.md) → [nacos](nacos.md)（服务发现）
- **多租户/权限**：[data-isolate](data-isolate.md) → [config](config.md)

## 相关文档

- 写作规范：[_STYLE.md](_STYLE.md)（新增/修订模块文档时遵循）
- 架构设计文档：[../arch/](../arch/)（`00-overview.md` ~ `06-phase6-example.md` 及专题设计）
- 已知问题与修复记录：[../issues/](../issues/)

---

共 22 篇说明文档（含本索引与框架总览），约 10,500 行。
