# RAG 设计（ai/rag）

> 本文是「Solon-AI 迁移到 Go」系列的第六篇（检索增强生成）。
> 前置：[`00-overview.md`](00-overview.md)、[`01-go-comparison.md`](01-go-comparison.md)、[`02-chat-core.md`](02-chat-core.md)（embedding 模型）。
> 覆盖：`solon-ai-core` 的 rag 域（SPI + 内置实现，约 3K 行）+ 三件套目录 `solon-ai-rag-{loaders,repositorys,searchs}`（25K 行）→ `./ai/rag` 子包（SPI + 零依赖内置实现 + 网络搜索）+ `./plugins/ai/ragloader`、`./plugins/ai/ragrepo`（带三方依赖的后端）。

---

## 0. TL;DR

- **SPI 极小**（Loader 3 方法、Splitter 1 方法、Repository 2 方法 + 两个可选能力接口），Java 侧本就是小接口设计，Go 化零成本；`QueryCondition` 的 SnEL 过滤表达式 → **enjoy**（仓库既有表达式引擎，flow 已用作默认求值器）。
- **后端矩阵分两半**：零外部依赖的（TextLoader、InMemory 仓储、4 切分器、3 家搜索、DdlLoader 复用 db）进 `./ai`；带三方依赖的（pdf/word/excel/ppt 解析、ES/向量库 SDK）进 plugins，且 **ES 后端直接复用 `./plugins/elasticsearch`**、pgvector/MySQL 复用 `./db`。
- Go 生态缺口（POI/Tika/PDFBox 无等价物）通过**分批引入 + 降级声明**处理：P4 先 text/markdown/html，重格式 P5 逐个（选型表见 §7）。

---

## 1. 包布局

```
ai/rag/
  ├ document.go        # Document + Metadata 快捷访问
  ├ loader.go          # Loader SPI + TextLoader
  ├ splitter.go        # Splitter SPI + Regex/TokenSize/Json/Semantic + Pipeline
  ├ query.go           # Query（QueryCondition 对应物）+ SearchType/Freshness/相似度工具
  ├ repository.go      # Repository SPI + 能力接口（Storer/Lifecycle）+ PromptAugment + RepositoryTool
  ├ memory.go          # InMemoryRepository（暴力余弦）
  └ search/
      ├ tavily.go bocha.go baidu.go     # 搜索即仓储
```

```
plugins/ai/ragloader/    # markdown/html/pdf/word/excel/ppt（按格式子包）
plugins/ai/ragrepo/      # es / pgvector / mysql / redis / milvus / qdrant …（按后端子包）
```

---

## 2. SPI 设计

### 2.1 Document

```go
type Document struct {
    ID        string
    Content   string
    Metadata  map[string]any     // title/url/summary 快捷访问（对标三个访问器）
    Embedding []float32          // EmbedDocs 填充
}
func NewDocument(content string) *Document
func (d *Document) Meta(k string, v any) *Document          // 链式 metadata
func (d *Document) Title() string / URL() string / Summary() string
func (d *Document) Score() float64                          // 命中得分（transient）
```

### 2.2 Loader / Splitter / Repository

```go
type Loader interface {
    AdditionalMetadata(k string, v any) Loader
    Load(ctx context.Context) ([]*Document, error)
}

type Splitter interface {
    Split(ctx context.Context, docs []*Document) ([]*Document, error)
}

// 主接口：只读检索（Java Repository 的 search 面）
type Repository interface {
    Search(ctx context.Context, query string) ([]*Document, error)      // 默认走 SearchQuery
    SearchQuery(ctx context.Context, q *Query) ([]*Document, error)
}

// 能力接口拆分（Java 用 RepositoryStorable/RepositoryLifecycle 接口继承，Go 用可选能力接口——
// 使用方以类型断言探测，database/sql 风格）
type Storer interface {
    Save(ctx context.Context, docs []*Document, progress func(done, total int)) error
    DeleteByID(ctx context.Context, ids ...string) error
    ExistsByID(ctx context.Context, id string) (bool, error)
}
type Lifecycle interface {
    InitRepository(ctx context.Context) error
    DropRepository(ctx context.Context) error
}
```

> 拆分理由：网络搜索型 Repository（§8）与只读快照不可 Storer；Java 靠接口分型，Go 靠能力接口 + 断言，语义一致且更灵活（`if s, ok := repo.(Storer); ok { … }`）。

### 2.3 检索辅助

```go
// 对标 Repository.promptAugment：命中拼进用户消息
func PromptAugment(ctx context.Context, repo Repository, query string) *ai.Message

// 对标 RepositoryTool：把任意 Repository 变成 agent 工具（被动检索 → 主动求知）
func NewRepositoryTool(repo Repository, opts ...RepoToolOption) ai.FunctionTool
//   工具名 repository_query；参数 queries []string + topK int；
//   可选 Reranker 在命中后重排（rerank.Model，P4）
```

---

## 3. Query 与过滤表达式（SnEL → enjoy）

```go
type SearchType uint8        // FullText / Vector / Hybrid
type Query struct {
    Query              string
    Limit              int            // 默认 4
    SimilarityThreshold float64       // 默认 0.4
    Freshness          *Freshness     // 时间窗过滤（Start/End）
    FilterExpr         string         // enjoy 表达式，如 status == "published" && lang == "zh"
    FilterFunc         func(*Document) bool   // 原生过滤（二选一或叠加）
    DisableRefilter    bool           // 关闭内存二次阈值过滤（信任后端）
    SearchType         SearchType     // 默认 Vector
    HybridParams       *HybridParams  // P5
}
```

**过滤表达式求值**（对标 SnEL `Expression<Boolean>` + 各后端 `FilterTransformer`）：

- 内存侧：enjoy 以 `doc.Metadata` 为作用域求值 `FilterExpr`（`Evaluation` 同 flow 默认求值器）；
- 后端侧：`FilterTransformer` 能力接口（`Transform(expr string) (native any, ok bool)`）——能翻成原生过滤（ES bool query、pgvector WHERE）的后端返回 native，否则回退**内存过滤**（拉回后过滤，行为对齐 Java 的 disableRefilter=false 默认）。

SnEL→enjoy 语法对照注意：`==`/`&&`/`||`/字符串方法（`contains`/`startsWith`）在 enjoy 中等价存在；`equals` 方法调用改为 `==`（文档给迁移表）。

---

## 4. 切分器

| 切分器 | Java 对应 | Go 要点 |
|--------|-----------|---------|
| `RegexTextSplitter` | 同名 | `regexp.Split`；保留分隔模式可选 |
| `TokenSizeTextSplitter` | 同名 | **tokenizer 问题**：jtokkit 无零依赖等价物 → 内置「字符近似」计数（CJK 1 字 ≈ 1 token 的经验系数）+ `Tokenizer` 接口可注入（`TokenCount(s string) int`）；插件侧可接 tiktoken 实现 |
| `JsonSplitter` | 同名 | 按 JSON 数组/对象边界切 |
| `SemanticSplitter` | 同名 | embedding 相邻块余弦突变检测切点（依赖 `embedding.Model`） |
| `Pipeline` | `SplitterPipeline` | 顺序组合 |

## 5. 内置实现

- `TextLoader`：string/[]byte/os.File/io.Reader 全源（Java 同名四构造）。
- `InMemoryRepository`：`[]*Document` + 读写锁，暴力余弦（go 例程并行可开），`Storer`+`Lifecycle` 全实现——**测试与演示的默认仓储**（对标同名）。

## 6. 仓储后端矩阵（plugins/ai/ragrepo）

| 后端 | Go 依赖 | 复用 | 优先级 | 要点 |
|------|---------|------|--------|------|
| **elasticsearch** | 无新增（`plugins/elasticsearch` 已有 go-elasticsearch v8） | `es.Client.Search/Index/BulkIndex` | **P5 首选** | dense_vector + `knn` 查询（Java 用旧 HighLevelClient 的 script_score，Go 版按现代 `knn` 重写）；`map[string]any` 查询面现成 |
| **pgvector** | 无新增（`./db`） | `db.Dao`/`db.SQL` | P5 | 建表 DDL（vector 列）+ 余弦 ORDER BY；enjoy→SQL Where 的 FilterTransformer |
| **mysql** | 无新增（`./db`） | 同上 | P5 | JSON 存 embedding + 内存余弦（Java mysql 仓储同款策略） |
| **redis** | 经 `./plugins/cache` 的通道 | cache L2 配置 | P5 | 决策 G：不引入第二套 Redis 客户端 |
| milvus / qdrant / chroma / weaviate / opensearch / dashvector / tcvectordb / vectorex / mariadb | 各官方 Go SDK | — | P5+ 按需 | 每后端一子包；filter 表达式能译则译、不能则内存过滤 |

统一实现骨架（每后端约 150–300 行）：`Search` = embed(query) → 后端 ANN → `FilterExpr` 内存/原生过滤 → 阈值重排 → 返回带 Score 的 Documents；`Save` = 分批 `EmbedDocs` → 写入（进度回调）。`EmbeddingModel` 由使用方注入（构造参数），仓储自身不懂模型。

## 7. Loader 后端矩阵（plugins/ai/ragloader）

| 格式 | 时机 | Go 方案 | 备注 |
|------|------|---------|------|
| text | P4（`./ai` 内置 TextLoader） | 标准库 | — |
| **markdown** | P4 | 内置轻量标题切分（`#` 前缀状态机，~100 行零依赖）；完整 AST 需要时接 goldmark | 对标 commonmark + SplitVisitor 的按标题切分 |
| **html** | P4 | `golang.org/x/net/html` 抽正文（goquery 可选） | 对标 jsoup |
| ddl | P4 | 复用 `./db` 元数据读取（generator 的 `MetaReader` 已有表/列读取！） | `DdlLoadConfig` → 表结构文档化；**与 tools/generator 协同的亮点** |
| pdf | P5 | `pdfcpu` / `ledongthuc/pdf`（文本层抽取） | 扫描件 OCR 明确不支持（声明） |
| word | P5 | docx = zip+XML 自解（纯 Go 可控）或三方库 | .doc 旧格式不支持 |
| excel | P5 | `xuri/excelize` | sheet → document |
| ppt | P5 | pptx = zip+XML 自解 | 降级声明（文本框优先） |

> 每格式一子包 + 统一 `AdditionalMetadata` 基座；`TEST.md` 式矩阵在 `_test/ai_plugin_test` 逐格式补（样例文件 embed 进测试包）。

## 8. 搜索即仓储（rag/search）

| 搜索源 | API 面 | 备注 |
|--------|--------|------|
| tavily | `Search/Extract/Crawl/Map`（对标 TavilyClient 全功能 + `TavilyRepository` 薄委托） | REST + APIKey；零依赖 |
| bocha | `BochaRepository` | 同上 |
| baidu | `BaiduRepository`（ofBasic/ofAI 两形态；AI 态可选 embedding 重排） | 对标 `AiConfig.createHttpUtils` → httpx |

三者都是 `Repository`（只读、不可 Storer），走 `ai/httpx`；命中映射为 `Document{URL,Content,Score,Metadata}`。

## 9. 测试策略（`_test/ai_test` + `_test/ai_plugin_test`）

1. SPI 黑盒：TextLoader→Pipeline 切分→InMemory 存取→SearchQuery 过滤/阈值/窗口全参数矩阵（纯内存，零服务）。
2. 过滤表达式：enjoy 求值表驱动（SnEL 迁移表逐条对照）。
3. SemanticSplitter：注入确定性假 embedding（长度编码余弦）验证切点。
4. RepositoryTool：stub 仓储 + 假函数调用循环（与 agent 测试共享夹具）。
5. 后端（plugins）：ES 用 `plugins/elasticsearch` 的测试基建（若有 docker-less stub 则用，否则接口级单测 + 环境变量开关真连）；pgvector/mysql 用 sqlite 验证 SQL 骨架 + 环境变量真连（对照 db_test 的 FLOW_MYSQL_DSN 模式）。

## 10. 实现顺序建议（对齐 07 的 P4）

1. Document/Loader/Splitter/Repository SPI + TextLoader + InMemory → 2. Query + enjoy 过滤 + 相似度工具 → 3. 切分器四件 + Pipeline → 4. search 三家 + PromptAugment + RepositoryTool（rerank 接口预留）→ 5. plugins/ai 装配的 rag 段（仓库/加载器配置化）→ 6.（P5）ragloader 的 markdown/html/ddl → 7.（P5）ragrepo 的 es/pgvector 两个样板后端 → 其余按需。
