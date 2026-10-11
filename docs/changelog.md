# 更新记录

> Aifei-Go 演进时间线，按版本倒序。本仓库为 Go workspace 多模块架构，所有库模块**锁步发布**（`scripts/tag.sh` 统一打 `<mod>/vX.Y.Z` tag），下文版本号即锁步版本。

## v1.2.0 · 2026-10-11

**全仓锁步 v1.2.0**（上一轮 v1.1.0 仅覆盖 5 个 AI 模块、存量模块停在 v1.0.3，本次归一）：

- **mcp**、**sandbox**、**ai/flow**、**ai/harness**、**ai/loop** 五个模块首次纳入锁步发布，可独立 `go get`
- [plugins/ai](/guide/ai-talent)：talent `lucene` → `bleve` 更名（Bleve + gse 纯 Go 中文分词，以实现技术命名）
- [plugins/ai/ragloader](/guide/ai-rag)：docx / pptx / xlsx / pdf 重格式处理统一收敛到 markitdown
- 发布工具 `scripts/tag.sh` 锁步清单补齐 10 个 AI 专项模块与 8 个测试模块

## v1.1.0 · 2026-10

**AI 能力专项（solon-ai 迁移，P0~P7）全量落地。** 从聊天核心到智能体、MCP、RAG、能力包、编码代理容器，AI 全家桶一次到位：

| 领域 | 内容 |
| --- | --- |
| [聊天核心](/guide/ai) | 消息 / 内容块、事件模型与聚合器、模型执行管线（`call` + `autoToolCall`）、流式三通道（`Stream` / `StreamChan` / `CallStream`）、方言矩阵（OpenAI / Anthropic / Gemini（含 Interactions API）/ Ollama / DashScope / OpenAI Responses）、embedding、rerank、generate、router 四策略、FileSession 原子写、ui/aisdk 流协议适配、agui 事件族 |
| [智能体](/guide/ai-agent) | `Simple` / `ReAct` 两类 Agent 与拦截器；Team 五协议：A2A / ContractNet / Blackboard / MarketBased / None；`ai/flow` AI 流编排 12 组件 |
| [MCP](/guide/mcp) | 协议层（schema / types / version / session）、客户端门面（stdio 传输 + 心跳重连）、服务端门面（stdio / 旧式 SSE / streamable HTTP）、`ai/mcp` 桥、`plugins/ai` 配置装配插件 |
| [RAG](/guide/ai-rag) | SPI 与内置实现、切分器四件 + Pipeline + 检索辅助、搜索即仓储（Tavily / Bocha / 百度）、ragloader 7 件（docx / pptx / xlsx / pdf 重格式统一经 markitdown）、ragrepo 11 个向量仓储后端（ES / pgvector / Redis / SQL / Chroma / DashVector / OpenSearch / Weaviate / 腾讯 vectordb 等） |
| [能力包](/guide/ai-talent) | 26 件 talent：零依赖批（file / sys / diff / web（含 crawler，JINA / Firecrawl 双驱动）/ generation / social / mail / mount / memory / code / data）+ 依赖批（text2sql / bleve（Bleve + gse 纯 Go 中文分词）/ gateway / pdf / cli / lsp） |
| [编码代理](/guide/ai-harness) | `ai/harness` 编码代理容器 + 上下文压缩拦截器；`ai/loop` 持久循环引擎（agent / flow / harness 三集成桥）；`sandbox` 命令沙箱四后端（darwin Seatbelt / linux bwrap / 代理服务器三件 / windows WFP） |

配套新增 AI 能力[六篇使用指南](/guide/)与 [`docs/arch/ai/`](/arch/ai/00-overview) 八篇契约设计。专项中途锁步点为 v1.0.3（P4 收口），收口后新增 AI 模块以 v1.1.0 发布。

## v1.0.2 · 2026-09-28

- **修复**：路由字面量子节点改为最长公共前缀匹配，修复同首字符兄弟节点互相遮蔽
- **修复**：`db.NewPage` 空 rows 归一为空切片，JSON 序列化为 `[]` 而非 `null`
- **修复**：`http` `GetMap` JSON body 场景以 query 补充缺失键（body 键优先）
- 修复问题记录 0021（见 `docs/issues`）

## v1.0.1 · 2026-09-16

- **生成器**：typed Dao 终端委托 `db` 泛型 `As` 家族，`XxxPage` 改为 `PageAs` 别名
- **server**：`Fail` / `SetMsg` 收窄为字面消息并新增 `Failf` / `SetMsgf`，日志统一走 `log` 模块
- 修复问题记录 0020

## v1.0.0 · 2026-09-11

1.0 正式版：

- **Go 1.27 泛型方法升级专项**（P0 / P1 / P2）落地；JSON v2 评估留档（见[架构设计](/arch/json-v2)）
- 文档站 pnpm 包整体迁入 `docs/`，根目录去 node 化；CI 缓存键显式指向 `docs/pnpm-lock.yaml`；补齐 logo 位图（32 / 128 / 256 / 512）

## v0.1.0 · 2026-08

- **Java v1.1.0 同步**：时间类型化（宽松版保旧名，严格版 E 后缀）、连接池线程安全、事务隔离级别（详见[同步记录](/arch/java-v1.1.0-sync)）
- **VitePress 文档站上线**，自动部署到 gh-pages；CI 文档工作流升级到 Node 24 运行时

## v0.0.x · 2026-05 ~ 07

起步与生态建设阶段：

- **2026-05**：项目启动，`server` 抽象与 `db` 初始导入
- **2026-06**：框架核心成型 —— [Interceptor](/guide/core)、RESTful 路由注册、generator 模板迭代、[enjoy](/guide/enjoy) 独立测试、多表关联映射
- **2026-06 ~ 07 插件生态**：[nami](/guide/nami) / [nacos](/guide/nacos) / [config](/guide/config) / [cache](/guide/cache) / [storage](/guide/storage) / [dami](/guide/dami) / [kafka](/guide/kafka) / [swagger](/guide/swagger)（多分组 + knife4j）/ [xxljob](/guide/xxljob) / [elasticsearch](/guide/elasticsearch) 陆续就位；`go-http` 更名 `http`，`plugins` 目录确立
- **2026-07**：[db](/guide/db) JSON 字段处理强化（插入 / 更新 / 驼峰蛇形配置）、SqlKit 空参与数值容错、GetBean / GetMap 完善
