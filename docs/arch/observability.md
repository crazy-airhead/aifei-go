# 可观测性设计（Observability）—— 统一存储 GreptimeDB + SkyWalking 链路追踪

> 目标：为 aifei-go 微服务栈提供**统一存储的可观测能力**——指标 / 日志 / 链路三信号**最终都落到 GreptimeDB**，链路追踪以 **Apache SkyWalking**（`killme2008/skywalking` fork `v11.0.0-greptimedb.3`，OAP 直写 GreptimeDB）作为 APM 分析层，UI 用 SkyWalking Horizon + Grafana。
>
> 本文是 `docs/arch/microservice.md` 第 7 节（P1-B 可观测）的**细化与替换**：原方案的 OTel+Prometheus 两套存储被替换为「GreptimeDB 单一存储」。本文不写实现代码，只给选型依据、数据流、组件部署、aifei-go 侧插件契约、配置与边界。

---

## 目录

1. 背景与目标
2. 选型依据
3. 总体架构
4. 组件与部署
5. Trace 方案（SkyWalking + GreptimeDB）
6. Metrics 方案（直写 GreptimeDB）
7. Logs 方案（直写 GreptimeDB）
8. aifei-go 侧插件设计
9. 数据模型与跨信号关联
10. 配置参考汇总
11. 部署参考（docker-compose 骨架）
12. 边界与限制
13. 与 microservice.md 的关系
14. 实现步骤建议
15. 未来扩展
16. 附：参考链接与衔接点速查

---

## 1. 背景与目标

`microservice.md` 把可观测列为 P1-B，原方案是 OTel（trace，Jaeger/Tempo）+ Prometheus（metrics）——**两套存储、两套查询**。本方案收敛为：

- **单一存储**：GreptimeDB 同时承载 metrics + logs + traces，可用 SQL 跨信号关联。
- **链路追踪**：接入 Apache SkyWalking，复用其成熟的拓扑图 / 依赖分析 / endpoint 级统计 / 告警能力；存储不绑 SkyWalking 自家 BanyanDB/ES，而用 `killme2008/skywalking` 这个把 GreptimeDB 作为 OAP storage 的 fork。
- **指标 / 日志**：直写 GreptimeDB，Grafana 看板（GreptimeDB 官方数据源插件）。

核心收益：**一个数据库替代 Prometheus + Loki/Tempo + Jaeger 整套**，运维与成本模型统一；链路仍享受 SkyWalking APM 增值。

---

## 2. 选型依据

### 2.1 GreptimeDB 能力概览

| 能力 | 说明 |
|------|------|
| 三信号统一 | metrics + logs + traces 一个数据库；可用 SQL 关联查询 |
| 原生 OTLP | OTLP/gRPC 与 OTLP/HTTP 全信号摄入（traces 自 v0.14 GA） |
| Prometheus 兼容 | 原生 `remote_write` 端点 + PromQL；Grafana 用 Prometheus 数据源直连 |
| 双语查询 | SQL + PromQL 同时原生支持 |
| 多值行（Wide Events） | 不止单值时序，可存宽事件 |
| 存算分离 | Frontend/Datanode 无状态，原生对象存储（S3/OSS/GCS），横向扩展 |
| 持续聚合 | 内置 SQL 聚合 + Flow 流计算引擎 |
| 自动建表 | 按 metric/OTLP 命名自动建表；traces 自动建 `opentelemetry_traces` 及 services/operations 辅助表 |
| 许可证 | Apache 2.0 |

traces 落 `opentelemetry_traces` 表，按 `trace_id` 分区；metrics 按 metric 名映射为表；logs 落 `opentelemetry_logs`。

### 2.2 GreptimeDB vs Prometheus（为何替换）

| 维度 | GreptimeDB | Prometheus / Thanos·Mimir |
|------|-----------|---------------------------|
| 查询语言 | SQL + PromQL（双语原生） | 仅 PromQL |
| 数据模型 | 多值行（Wide Events） | 单值时序 |
| 数据类型 | 指标 + 日志 + 链路 | 仅指标 |
| 存储 | 原生对象存储 | 本地磁盘 / 经 sidecar |
| 扩展模型 | 存算分离，无状态节点 | 仅联合 / 多组件（运维复杂） |
| OpenTelemetry | 原生 OTLP（全信号） | 仅指标（remote write） |
| 持续聚合 | 内置 SQL 聚合 + Flow | Recording Rules（有限） |
| 组件数 | 1 个数据库 | 5–8 个组件（Prometheus+Thanos/Mimir+...） |

结论：GreptimeDB 用一个数据库替代整个 Prometheus + Thanos/Mimir 栈，且额外吃下日志和链路。PromQL 兼容、`remote_write` 就绪，迁移可零停机重定向。

### 2.3 为何用 SkyWalking + `killme2008/skywalking` fork

- SkyWalking 提供裸 trace 存储给不了的 **APM 增值**：服务/实例/endpoint 拓扑、依赖矩阵、慢调用分析、告警规则、profiling。
- GreptimeDB 官方原生支持 OTLP traces，但**没有 APM 分析层**——查得到链路，没有拓扑与告警。
- `killme2008/skywalking`（`v11.0.0-greptimedb.3`）把 GreptimeDB 做成 OAP 的 storage backend：OAP 负责接收/聚合/分析/查询，GreptimeDB 负责存——**APM 能力 + 统一存储兼得**。
- 该 fork：基于 apache skywalking `11.0.0-SNAPSHOT`（commit `46129f18`），gRPC API 写、MySQL 兼容协议查；支持 metrics / records（traces/logs/alarms/events/zipkin/profiling）/ search / profiling / 管理数据 / 集群；CI 测 GreptimeDB v0.15.5 与 v1.1.2，手动测 v0.17.2。
- **风险**：社区构建，非 ASF 发布；基于 SNAPSHOT；有若干限制（见 §12）。需评估后再用于生产。

---

## 3. 总体架构

```
 ┌─────────────────────────── aifei-go 应用进程 ───────────────────────────┐
 │  plugins/trace   (OTel SDK: HTTP 中间件建 span + nami.Filter 透传)       │
 │  plugins/metrics (OTel meter / prom client)                             │
 │  log 抽象 + OTLP logs handler                                           │
 └───────┬───────────────────────┬──────────────────────────┬──────────────┘
         │ traces (OTLP)         │ metrics (OTLP/remote_write)│ logs (OTLP)
         ▼                       ▼                           ▼
 ┌───────────────────┐    ┌─────────────────────────────────────────────┐
 │ SkyWalking OAP    │    │ GreptimeDB（统一存储）                        │
 │ killme2008 fork   │    │  opentelemetry_traces / _logs / metric 表    │
 │ receiver-otel     │    │  PromQL + SQL · 存算分离 · 对象存储           │
 │ :4317 (OTLP in)   │    └──────────┬──────────────┬────────────────────┘
 │ :11800 (native)   │               │              │
 │ :12800 (HTTP/UI)  │   gRPC write  │              │ SQL/PromQL
 │ :9411  (Zipkin)   │ ─────────────►│              │
 └─────────┬─────────┘   :4001       │              │
           │  query(MySQL:4002)      │              │
           └─────────────────────────┘              │
           ▼                                        ▼
 ┌────────────────────┐                 ┌────────────────────┐
 │ SkyWalking Horizon │                 │ Grafana            │
 │  拓扑/链路/告警     │                 │  指标/日志看板      │
 │  :12800            │                 │  GreptimeDB 插件    │
 └────────────────────┘                 └────────────────────┘
```

三信号**最终都在 GreptimeDB**：
- **traces** 经 SkyWalking OAP（为了 APM 增值）→ GreptimeDB；
- **metrics / logs** 直写 GreptimeDB（最短路径，Grafana 直查）；
- SkyWalking OAP 因 storage 即 GreptimeDB，其 metric/trace 查询也落在同一库——**一套数据，两个视角**。

---

## 4. 组件与部署

| 组件 | 角色 | 端口 | 镜像/来源 |
|------|------|------|-----------|
| **GreptimeDB** | 统一存储（三信号） | 4000 HTTP / 4001 gRPC / 4002 MySQL / OTLP endpoint | `greptime/greptimedb`（Apache 2.0） |
| **SkyWalking OAP** | APM 分析层（receiver-otel 收 trace，存 GreptimeDB） | 11800 gRPC / 12800 HTTP·UI / 9411 Zipkin / 4317 OTLP | `ghcr.io/killme2008/greptimedb-oap:11.0.0-greptimedb.3`（社区构建） |
| **SkyWalking Horizon UI** | 拓扑/链路/告警看板 | 12800 | 随 OAP 镜像 |
| **Grafana** | 指标/日志看板 | 3000 | `grafana/grafana` + GreptimeDB 数据源插件 |
| **MySQL Connector/J** | OAP 经 MySQL 协议查 GreptimeDB 所需驱动 | — | GPL（Category X），需单独挂载 OAP 的 `/skywalking/ext-libs` |

> OAP 关键环境变量：`SW_STORAGE=greptimedb`、`SW_STORAGE_GREPTIMEDB_GRPC_ENDPOINTS=greptimedb:4001`、`SW_STORAGE_GREPTIMEDB_JDBC_ENDPOINTS=greptimedb:4002`、`SW_STORAGE_GREPTIMEDB_DATABASE=skywalking`，并启用 `receiver-otel` 的 `otlp-traces` handler。

---

## 5. Trace 方案（SkyWalking + GreptimeDB）

### 5.1 两条上报路径对比

Go 应用把 trace 送进 SkyWalking OAP 有两条路：

| 维度 | A：skywalking-go native agent | B：OTel SDK → OAP receiver-otel（推荐） |
|------|-------------------------------|------------------------------------------|
| 埋点方式 | 编译期 `-toolexec` 注入 + toolkit API | OTel SDK 手动 + 中间件/Filter |
| 线协议 | SkyWalking native（gRPC 11800） | OTLP（gRPC 4317 / HTTP 4318） |
| 与 aifei 契合度 | 低——aifei 非 skywalking-go 内置支持框架（Gin/gRPC/GORM…），路由层需手动 toolkit 或自写插件 | 高——aifei 的 `aifei.Handler` 链 + `nami.Filter` 天然是「中间件建 span」模型 |
| 构建影响 | 改 go build 流程，CI 复杂 | 无构建改动 |
| 后端可移植 | 紧耦合 SkyWalking（无 OTLP exporter） | vendor-neutral，未来可换 Jaeger/Tempo/直连 GreptimeDB |
| SkyWalking 功能 | 全（segment 层次、instance metrics、endpoint 命名） | traces 在 OAP 内转 Zipkin 格式，**轻微有损**（失去部分 native segment 语义） |
| metrics/logs 复用 | 各自独立 | 同一 OTel pipeline 可承载三信号 |

### 5.2 推荐：B（OTLP 路径）

**理由**：① 契合 aifei 的 Handler/nami.Filter 扩展点，埋点即「写一个中间件 + 一个 Filter」；② 不改构建流程；③ 与 metrics/logs 共用 OTel pipeline，符合「统一」理念；④ trace 仍享 SkyWalking APM（拓扑由 trace 数据自动生成，告警可用）。

**代价**：traces 在 OAP 内转为 Zipkin 风格，损失 SkyWalking native segment 的部分高级 UI 能力。若日后确认需要 SkyWalking 最全功能（instance 级关系、精确 endpoint 统计），可切方案 A——届时 `plugins/trace` 仅替换 exporter/埋点实现，中间件与 Filter 契约不变。

### 5.3 W3C traceparent 透传

- server 入站：中间件解析 `traceparent`，创建 span，写入 `in.Context()`。
- nami 出站：`NamiFilter` 从 context 读 `traceparent`，注入下游请求 header。
- 跨 aifei-go 服务调用链自动串联；进入其他语言/框架的 OTel 探针也兼容（W3C 标准）。

---

## 6. Metrics 方案（直写 GreptimeDB）

不经过 OAP，最短路径直写 GreptimeDB（OAP 不擅长大规模指标聚合，且 GreptimeDB 的 PromQL + Grafana 是指标看板的标准答案；OAP 仍可查同一库里的 metric）。

- **采集**：OTel meter SDK（与 trace 同一 pipeline）或 `prometheus/client_golang`。
- **写入**：二选一——
  - **OTLP**（推荐，与 trace/logs 统一）：发 GreptimeDB OTLP endpoint。
  - **remote_write**（已有 prom client 时）：发 GreptimeDB `remote_write` 端点，Grafana 用 Prometheus 数据源直连，现有仪表盘零改动。
- **默认指标**：`http_requests_total{method,route,code}`、`http_request_duration_seconds`（histogram）、`http_inflight`；db/kafka/cache 可按需补。
- **看板**：Grafana + `GreptimeTeam/greptimedb-grafana-datasource` 插件。

---

## 7. Logs 方案（直写 GreptimeDB）

- aifei-go 已有 `log` 抽象（5 级 Logger）。新增 OTLP logs exporter 或 file → OTel Collector → GreptimeDB 两条路。
- 结构化日志（JSON）落 `opentelemetry_logs` 表，与 trace 通过 `trace_id`/`span_id` 关联。
- **限制**：GreptimeDB 当前全文搜索用英文分析器；中文全文检索建议结构化字段（service/level/trace_id）+ 等值过滤，关键词用英文或走外部全文引擎。

---

## 8. aifei-go 侧插件设计

> 核心约束不变：`nami`/`aifei`/`db`/`enjoy` 保持零外部依赖；OTel/prom 依赖只进 `plugins/*`；治理与可观测一律以 `aifei.Handler` / `nami.Filter` / `aifei.Plugin` 接入。

### 8.1 `plugins/trace`（OTel SDK，OTLP 导出）

```go
package trace

// Plugin 装配 OTel tracer + OTLP exporter，默认指向 SkyWalking OAP 的 receiver-otel。
type Plugin struct { /* ... */ }
func NewPlugin(logger log.Logger) *Plugin

// HTTPMiddleware（func(http.Handler)http.Handler）由 server.WithHTTPHandler 挂载：
// 解析入站 W3C traceparent → 建 span → 写入 request.context。
func (p *Plugin) HTTPMiddleware() func(http.Handler) http.Handler

// NamiFilter 从 context 读 traceparent，注入出站请求 header（跨服务串联）。
func (p *Plugin) NamiFilter() nami.Filter

// SpanFromContext / StartSpan 供业务在 Handler/Service 内建子 span（如 db 调用）。
func SpanFromContext(ctx context.Context) trace.Span
```

**依赖**：`go.opentelemetry.io/otel`、`go.opentelemetry.io/otel/exporters/otlp/otlptrace/...`、`otelhttp`。

### 8.2 `plugins/metrics`（OTel meter 或 prom client，直写 GreptimeDB）

```go
package metrics

type Plugin struct { /* ... */ }
func NewPlugin(logger log.Logger) *Plugin

// HTTPMiddleware 统计 HTTP 维度指标（QPS/延迟分位/in-flight）。
func (p *Plugin) HTTPMiddleware() func(http.Handler) http.Handler

// Meter 暴露 OTel meter 或 prometheus.Registerer 供业务自定义指标。
func (p *Plugin) Meter() metric.Meter        // OTel
func (p *Plugin) PromRegisterer() prometheus.Registerer
```

**依赖**：`go.opentelemetry.io/otel/metric` + otlp exporter，或 `github.com/prometheus/client_golang`（remote_write 走 prom client + 自定义 push，或经 OTel Collector）。

### 8.3 log 适配（OTLP logs）

- `log` 包已有 `Logger` 接口；新增一个 OTLP logs 后端（实现写 OTLP 的 sink），或输出到文件由 OTel Collector 采集。
- 业务侧用法不变（仍调 `log.Info(...)`），输出端配置切换。

### 8.4 actuator 联动

`server.WithActuator` 预留的 `/metrics` 端点：装了 `plugins/metrics` 时可由它接管（本地调试用 pull）；生产走 push（OTLP/remote_write）直写 GreptimeDB，端点可选。

---

## 9. 数据模型与跨信号关联

GreptimeDB 自动建表：

| 信号 | 表 | 分区/键 |
|------|----|---------|
| traces | `opentelemetry_traces` | 按 `trace_id` 分区；含 service/operation 辅助表 |
| logs | `opentelemetry_logs` | `trace_id`/`span_id` 可关联 trace |
| metrics | `<metric_name>`（每指标一表，按命名映射） | 标签为列 |

**跨信号关联示例**（SQL）：

```sql
-- 找慢 trace，再拉对应日志
SELECT trace_id, duration FROM opentelemetry_traces
WHERE duration > 1_000_000_000 ORDER BY duration DESC LIMIT 20;

SELECT ts, body FROM opentelemetry_logs
WHERE trace_id = '<上面查到的 trace_id>' ORDER BY ts;

-- 指标与 trace 同库，可用 SQL join/子查询关联服务维度
```

SkyWalking OAP 经 MySQL 协议（4002）查同一批表，UI 展示拓扑/链路；Grafana 经 SQL/PromQL 看指标/日志——**同源数据，两个视角**。

---

## 10. 配置参考汇总

```yaml
observability:
  greptimedb:                # 统一存储连接
    endpoint: greptimedb:4000      # HTTP（OTLP/HTTP、SQL、remote_write）
    grpc-endpoint: greptimedb:4001 # OTLP/gRPC 与 OAP 写入
    db: aifei
    username: ${GREPTIMEDB_USER}
    password: ${GREPTIMEDB_PASS}

trace:                       # 链路 → SkyWalking OAP → GreptimeDB
  enabled: true
  exporter: otlp              # otlp | none
  endpoint: skywalking-oap:4317   # OAP receiver-otel
  service-name: ${app.name}
  sampling: { ratio: 0.1 }

metrics:                     # 指标 → 直写 GreptimeDB
  enabled: true
  exporter: greptimedb
  protocol: otlp             # otlp | remote_write
  endpoint: greptimedb:4000  # OTLP/HTTP 或 /v1/prometheus/write
  interval: 15s

logs:                        # 日志 → 直写 GreptimeDB
  enabled: true
  exporter: otlp             # otlp | file
  endpoint: greptimedb:4000

skywalking:
  oap:
    enabled: true
    image: ghcr.io/killme2008/greptimedb-oap:11.0.0-greptimedb.3
    grpc-write: greptimedb:4001   # SW_STORAGE_GREPTIMEDB_GRPC_ENDPOINTS
    jdbc-query: greptimedb:4002   # SW_STORAGE_GREPTIMEDB_JDBC_ENDPOINTS
    database: skywalking
    ui: :12800
```

---

## 11. 部署参考（docker-compose 骨架）

```yaml
services:
  greptimedb:
    image: greptime/greptimedb:latest
    ports: ["4000:4000", "4001:4001", "4002:4002"]
    command: standalone start --http-addr 0.0.0.0:4000

  skywalking-oap:
    image: ghcr.io/killme2008/greptimedb-oap:11.0.0-greptimedb.3
    ports: ["11800:11800", "12800:12800", "9411:9411", "4317:4317"]
    environment:
      SW_STORAGE: greptimedb
      SW_STORAGE_GREPTIMEDB_GRPC_ENDPOINTS: greptimedb:4001
      SW_STORAGE_GREPTIMEDB_JDBC_ENDPOINTS: greptimedb:4002
      SW_STORAGE_GREPTIMEDB_DATABASE: skywalking
      SW_HEALTH_CHECKER: default
      JAVA_OPTS: "-Xms1g -Xmx1g"
    volumes:
      - ./lib/mysql-connector-j.jar:/skywalking/ext-libs/mysql-connector-j.jar:ro
    depends_on: [greptimedb]

  grafana:
    image: grafana/grafana:latest
    ports: ["3000:3000"]
    # 安装 GreptimeTeam/greptimedb-grafana-datasource 插件后添加数据源
```

> aifei-go 应用的 `trace.endpoint` 指向 `skywalking-oap:4317`，`metrics/logs.endpoint` 指向 `greptimedb:4000`。

---

## 12. 边界与限制

**`killme2008/skywalking` fork 的限制（务必评估）**：

- **非 ASF 发布**：社区构建，基于 `11.0.0-SNAPSHOT`（commit `46129f18`），非官方正式版——生产前需自行验证稳定性与升级路径。
- **Trace V2 查询仅 BanyanDB 可用**：GreptimeDB 存储下走 Trace V1。
- **日志全文搜索用英文分析器**：中文全文检索受限（见 §7）。
- **无 TLS / CA 配置**：直接 TLS 未验证，内网或经 sidecar 终止 TLS。
- **schema 迁移不自动**：不兼容的 schema 变更需手动 drop 重建表。
- **MySQL Connector/J 是 GPL（Category X）**：ASF 不随镜像分发，需自行挂载 `ext-libs`；注意 GPL 合规。

**架构层面取舍**：

- OTLP→OAP 路径下，traces 在 OAP 内转 Zipkin 格式，**轻微有损** SkyWalking native 语义（见 §5.1）；要全功能则切 skywalking-go native（改构建流程 + aifei 手动埋点）。
- 若**不需要 SkyWalking APM**（只要存链路），可去掉 OAP，trace 直写 GreptimeDB（OTLP）+ Grafana 查看，架构更轻——但失去拓扑/告警。
- 分布式追踪不内建采样后端：本方案用 OTel head sampling（按 ratio），大规模可加 OTel Collector 做 tail sampling。

---

## 13. 与 microservice.md 的关系

本文**替换** `microservice.md` 第 7 节（P1-B 可观测）的原方案：

| 项 | 原方案（microservice.md §7） | 本方案 |
|----|------------------------------|--------|
| trace 存储 | OTel → Jaeger/Tempo | OTel → **SkyWalking OAP → GreptimeDB** |
| metrics 存储 | Prometheus client | OTel/prom → **GreptimeDB**（remote_write/OTLP） |
| 存储数 | 2+ 套 | **1 套**（GreptimeDB） |
| trace APM | 仅查询 | **SkyWalking 拓扑/告警** |
| aifei-go 契约 | `plugins/trace`/`plugins/metrics` | **不变**（仍是 HTTPMiddleware + NamiFilter），仅 exporter/endpoint 指向改变 |

`microservice.md` 的 P0（负载均衡、actuator）、P1-A（breaker/ratelimit/retry）、P2 均不受影响。actuator 的 `/metrics` 端点由本方案 `plugins/metrics` 接管。

---

## 14. 实现步骤建议（分期）

每步可独立编译、独立测试、独立合入。

**第一期（存储与链路打底）**
1. 部署 GreptimeDB（standalone 起步）+ `killme2008/skywalking` OAP + Grafana；跑通 docker-compose（§11）。
2. `plugins/trace`：OTel SDK + OTLP exporter（指向 OAP 4317）+ HTTPMiddleware + NamiFilter；`_test` 用 in-memory exporter 断言 span 创建与 traceparent 透传。
3. 端到端验证：aifei-go 多服务互调，SkyWalking Horizon 出拓扑 + 完整链路。

**第二期（指标与日志）**
4. `plugins/metrics`：OTel meter / prom client + OTLP/remote_write 直写 GreptimeDB；Grafana 看板。
5. log OTLP 后端：`opentelemetry_logs` 落库，验证 trace_id↔log 关联。

**第三期（生产化）**
6. 采样策略（head/tail）、TTL、告警规则、Grafana 仪表盘固化；评估 fork 稳定性后定生产方案。

---

## 15. 未来扩展

- **去 OAP 直连**：若 APM 增值非刚需，trace 直写 GreptimeDB + Grafana，架构更轻。
- **切 skywalking-go native**：需要 SkyWalking 全功能时（§5.1 方案 A），`plugins/trace` 换实现。
- **Flow 持续聚合**：用 GreptimeDB Flow 引擎做指标预聚合（如每分钟 P99），降低查询压力。
- **统一告警**：SkyWalking 告警 + Grafana alert 双源，最终都基于 GreptimeDB 数据。
- **eBPF profiling**：SkyWalking 支持 eBPF/async-profiler profiling 数据（fork 已含），按需启用。

---

## 16. 附：参考链接与衔接点速查

**参考链接**

- GreptimeDB vs Prometheus：https://greptime.cn/compare/prometheus
- GreptimeDB OTLP 摄入：https://docs.greptime.com/user-guide/ingest-data/for-observability/opentelemetry/
- GreptimeDB traces 读写：https://docs.greptime.com/user-guide/traces/read-write/
- GreptimeDB traces 数据模型：https://docs.greptime.com/user-guide/traces/data-model/
- GreptimeDB Prometheus 集成：https://docs.greptime.com/user-guide/ingest-data/for-observability/prometheus/
- Grafana GreptimeDB 数据源插件：https://github.com/GreptimeTeam/greptimedb-grafana-datasource
- killme2008/skywalking（GreptimeDB OAP fork）：https://github.com/killme2008/skywalking
- SkyWalking OTLP trace receiver：https://skywalking.apache.org/docs/main/next/en/setup/backend/otlp-trace/
- SkyWalking Go Agent：https://skywalking.apache.org/docs/skywalking-go/next/readme/

**与现有代码的衔接点**

| 现有符号 | 位置 | 本期改动 |
|----------|------|----------|
| `nami.Filter` / `FilterFunc` | `nami/nami.go` / `nami/invocation.go` | 不变；trace 以 `NamiFilter` 接入 |
| `server.WithHTTPHandler` | `server/run.go` | 挂 trace/metrics 的 `HTTPMiddleware` |
| `server.WithActuator` | `server/actuator.go`（P0-B 新增） | `/metrics` 端点由 `plugins/metrics` 接管 |
| `log.Logger` | `log` | 新增 OTLP logs 后端（实现/配置切换，接口不变） |
| `config.Props` / `Sub` | `config` | 新增 `observability.*` / `trace.*` / `metrics.*` / `logs.*` / `skywalking.*` 段 |
| `aifei.Plugin` | `aifei/plugin.go` | 新增 `plugins/trace`、`plugins/metrics` 实现之 |
