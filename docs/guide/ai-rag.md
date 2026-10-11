# Aifei-Go ai/rag：检索增强生成（SPI / 加载器 / 向量仓储）

> **一条六阶段流水线打通 RAG**：`Loader → Splitter → Embedder → Repository → Query → PromptAugment`，核心 SPI 零依赖，7 种文档格式加载器与 11 个向量仓储后端按需插拔。

## 1. 背景与定位

RAG（Retrieval-Augmented Generation）让 LLM 回答建立在你的私有数据上。`./ai/rag` 包（属 [ai 模块](ai.md)）定义流水线的全部接口与内存实现；重型能力放在两个插件模块：

- `plugins/ai/ragloader`：Word/PPT/Excel/PDF/HTML/Markdown/DDL 七种格式加载器
- `plugins/ai/ragrepo`：ES / pgvector / Redis / OpenSearch 系等 11 个向量仓储后端

对标 solon-ai 的 `rag` 包（Document/Query/Splitter/Repository 契约一致，黄金样本测试搬译自 Java）。

## 2. 总体架构：六阶段流水线

```text
 文档源                处理                      存储/检索               消费
 ────────              ────                      ──────────             ────
 rag.Loader     →   rag.Splitter        →   rag.Storer.Save       →   rag.PromptAugment
 (TextLoader/        (Regex/TokenSize/       (嵌入回填 Embedding)      (检索结果拼进消息)
  ragloader 7种)      Json/Semantic)               │
                                                  ▼
                                    rag.Repository.SearchQuery   →   rag.NewRepositoryTool
                                    (向量/全文/混合 + 过滤)          (作为工具交给模型自主检索)
```

| SPI | 方法 | 内置实现 |
|-----|------|---------|
| `Loader` | `AdditionalMetadata(k, v) Loader` / `Load(ctx) ([]*Document, error)` | TextLoader + ragloader 7 件 |
| `Splitter` | `Split(ctx, docs) ([]*Document, error)` | 4 切分器 + Pipeline |
| `Embedder` | `Embed` / `EmbedTexts` / `BatchSize` | `*ai/embedding.Model` 直接满足 |
| `Repository` | `Search` / `SearchQuery` | InMemory + ragrepo 14 后端 + web 搜索 3 家 |
| `Storer` | `Save` / `DeleteByID` / `ExistsByID` | InMemory + ragrepo 13 家（web 搜索只读除外） |
| `Lifecycle` | `InitRepository` / `DropRepository` | 建删索引/表 |

## 3. Document 与 Query

```go
doc := rag.NewDocument("苹果公司发布新款手机……").
    Meta("title", "科技新闻").
    Meta("category", "fruit").
    Meta("url", "https://x/n/1")      // nil 值自动跳过；Title()/URL()/Summary() 是 Metadata 快捷读
```

检索侧 `Query`（`rag.NewQuery("apple")` 预填 limit=4 / threshold=0.4 / 向量检索）：

```go
q := rag.NewQuery("apple")
q.Limit = 10
q.SimilarityThreshold = 0.8                 // 相似度阈值（向量距离过滤）
q.Freshness = rag.LastWeek()                // 时间窗：LastDay/LastWeek/LastMonth/LastYear
                                            // 或 rag.NewFreshness(start, end)（含端点）
q.FilterExpr = `category == "berry"`        // enjoy 表达式过滤，Metadata 为作用域
q.FilterFunc = func(d *rag.Document) bool { return d.URL() != "" }  // 与 FilterExpr 叠加
q.SearchType = rag.SearchHybrid             // SearchVector(缺省)/SearchFullText/SearchHybrid
q.HybridParams = rag.NewHybridParams(0.7)   // 向量权重 0.7、全文 0.3（钳制 [0,1]）
docs, err := repo.SearchQuery(ctx, q)
```

阈值、排序、截断由仓储完成；`FilterExpr`/`FilterFunc`/`Freshness` 这类内存过滤经 `rag.Refilter(docs, q)` 统一执行（不支持原生下推的后端在返回前调用）。相似度工具函数：`CosineSimilarity` / `DotProduct` / `EuclideanDistance` / `EuclideanSimilarity`；`rag.CopyAndScore(d, s)` 生成带分副本、`rag.Partition(list, size)` 通用分批。

`Query` 默认值（`query.go` 常量）：

| 常量 | 值 | 说明 |
|---|---|---|
| `DefaultLimit` | 4 | 返回条数 |
| `DefaultSimilarityThreshold` | 0.4 | 相似度阈值 |
| `DefaultBatchSize` | 10 | 嵌入批次大小 |

检索类型对照：

| `SearchType` | 行为 | 依赖 |
|---|---|---|
| `SearchVector`（缺省） | 查询向量 × 文档向量相似度 | 仓储需存向量 |
| `SearchFullText` | 关键词/全文检索 | 仓储需全文索引（ES、Bleve 等） |
| `SearchHybrid` | 两路加权融合（`HybridParams`，缺省 0.5/0.5） | 仓储需同时支持两路 |

`Freshness` 过滤按 `Metadata["createdAt"]`（时间戳）判定，零值字段视为不限。

## 4. Loader SPI 与四件切分器

```go
// 文本加载（核心包自带）
docs, err := rag.NewTextLoaderFromFile("a.txt").
    AdditionalMetadata("source", "file").
    AdditionalMetadata("title", "A").
    Load(ctx)                              // Charset("utf-16") 可切编码
```

切分器按粒度递进：

| 切分器 | 构造 | 机制 |
|---|---|---|
| `RegexTextSplitter` | `NewRegexTextSplitter()`（缺省 `\n\n`）/ `NewRegexTextSplitterWith(re)` | 正则切段；`KeepSeparator(b)` 保留分隔符 |
| `TokenSizeTextSplitter` | `NewTokenSizeTextSplitter()`（500/300/5/1000/true）/ `NewTokenSizeTextSplitterWith(chunkSize, minChunkSizeChars, minChunkLengthToEmbed, maxChunkCount, keepSeparator)` | 按 token 预算合并/切分；`Tokenizer(t)` 注入精确计数器（缺省 `ApproxTokenCount`：CJK 每字 1、其余每 4 字符 1） |
| `JsonSplitter` | `NewJsonSplitter()`；`.ContentSelector("body")` `.MetadataMapper(fn)` | JSON 行/数组展开，键选择与元数据抽取 |
| `SemanticSplitter` | `NewSemanticSplitter(embedder)` / `NewSemanticSplitterWith(e, threshold, maxChunkTokenSize, similarityWindow, minSentences, delimiters)` | 句向量相似度断点聚类；`ChineseDelim`/`EnglishDelim`/`AllCommonDelim` 句界 |

多级组合用管道：

```go
pipe := rag.NewSplitterPipeline(
    rag.NewJsonSplitter().ContentSelector("text"),
).Next(rag.NewTokenSizeTextSplitter())      // 依次穿过
split, err := pipe.Split(ctx, docs)
```

TokenSize 缺省参数（`splitter.go` 常量）：

| 常量 | 值 | 含义 |
|---|---|---|
| `DefaultChunkSize` | 500 | 目标块 token 数 |
| `DefaultMinChunkSizeChars` | 300 | 触发合并的最小字符块 |
| `DefaultMinChunkLengthToEmbed` | 5 | 小于该长度的碎块丢弃 |
| `DefaultMaxChunkCount` | 1000 | 单文档最大切块数（防爆） |

`ApproxTokenCount` 的估算规则：CJK 字符每字记 1 token、其余每 4 字符记 1——与 BPE 实际值有偏差但对中英文混合文本够用；需要精确时实现 `Tokenizer` 接口注入。

## 5. ragloader：七种文档格式加载器

`plugins/ai/ragloader`（独立 go.mod）全部实现 `rag.Loader`；重格式（Word/PPT/Excel/PDF）统一经 [conductor-oss/markitdown](https://github.com/conductor-oss/markitdown) 转 Markdown 再切分：

| 加载器 | 构造（均有 FromFile 变体） | 切分模式 |
|---|---|---|
| `WordLoader` | `NewWordLoader(r)` / `NewWordLoaderNamed(r, filename)`（按扩展名路由 .doc/.docx） | `WordParagraph`（每 Markdown 段落一文档，缺省）/ `WordSingle` |
| `PptLoader` | `NewPptLoader(r)` / `NewPptLoaderNamed(r, filename)` | `PptSingle`（缺省）/ `PptPage`（每页一文档）；`PageDelimiter(d)` 缺省 `\n\n\n` |
| `ExcelLoader` | `NewExcelLoader(r)` / `NewExcelLoaderNamed(r, filename)`（可路由 .xls） | `DocumentMaxRows(n)`：每文档非空行上限（缺省 200，`-1` 关闭分批） |
| `PdfLoader` | `NewPdfLoader(r)` / `NewPdfLoaderNamed(r, filename)` | `PdfPage`（每段一文档，缺省）/ `PdfSingle`；`PageDelimiter(d)` 缺省 `\n\n` |
| `HtmlSimpleLoader` | `NewHtmlSimpleLoader(r)` / `...FromFile` / `...FromURL(url)` | 正文抽取；`Charset(cs)` / `BaseURI(u)` 相对链接解析 |
| `MarkdownLoader` | `NewMarkdownLoader(r)` / `...FromFile` / `...FromString(s)` | `Options(fn)`：`HorizontalLineAsNew` / `CodeBlockAsNew` / `BlockquoteAsNew` 分段开关 |
| `DdlLoader` | `NewDdlLoader(db)` / `NewDdlLoaderWithConfig(db, DefaultMysqlDdlLoadConfig())` | `LoadOptions(schema, table)` 导出表结构 DDL（schema 空=全库）；每表一文档 |

```go
docs, err := ragloader.NewPptLoaderFromFile("培训.pptx").
    LoadMode(ragloader.PptPage).
    AdditionalMetadata("source", "training").
    Load(ctx)
```

markitdown 引擎（`markitdown.go`）是重格式的统一中枢：`convertToMarkdown` 负责 docx/pptx/xlsx/pdf → Markdown 的格式转换，再按各加载器的模式切分——`splitMarkdownSlides`（PPT 按页）、`splitMarkdownSheets`（Excel 按行批次）、`splitMarkdownParagraphs`（Word 按段落）。因此四种格式的行为差异只在"怎么切"，转换语义完全一致，新格式只需给引擎加一个转换分支。

## 6. ragrepo：十一个向量仓储后端

`plugins/ai/ragrepo`（独立 go.mod，7 个子包），每个后端实现 `Repository + Storer + Lifecycle` 三面（`embedder` 参数均接受 `*ai/embedding.Model`）：

| 后端子包 | 构造 | 备注 |
|---|---|---|
| `es` | `NewRepository(client, model, indexName, opts...)` | `client` 为 [plugins/elasticsearch](elasticsearch.md) 的 Client；构造期即 InitRepository；`WithMetadataFields` 声明过滤字段、`WithVectorSearchType` 选 ApproximateKNN(缺省)/ExactKNN |
| `pgvector` | `NewRepository(db, model, tableName, opts...)` | 字段类型 TEXT/NUMERIC/JSONB；FilterExpr 译成 SQL 谓词 |
| `chroma` | `NewChromaRepository(baseURL, model, collection)` | `SetTenantDatabase(tenant, db)` 多租户 |
| `dashvector` | `NewDashVectorRepository(baseURL, apiKey, model, collection)` | 阿里 DashVector，`dashvector-auth-token` 头 |
| `sqlrepo` | `NewMySQLRepository(db, emb, table, fields...)` / `NewMariaDBRepository(...)` | SQL 存向量，无向量插件的兼容路径 |
| `redisrepo` | `New(client, emb, index, keyPrefix, fields...)` | Redis 向量索引 |
| `vectorest` | `NewOpenSearchRepository(...)` / `NewWeaviateRepository(...)` / `NewVectoRexRepository(...)` | REST 直连三件 |
| `vectorest` | `NewTcVectorDBRepository(baseURL, user, key, db, collection, _)` | 腾讯云 VectorDB；**服务端 embedding**，embedder 参数被忽略 |
| （核心包） | `rag.NewInMemoryRepository(embedder)` | 暴力余弦检索；embedder 为 nil 时保存/检索报错（晚失败） |

`MetadataField`/`MetadataFieldX` 声明可过滤字段（TEXT/NUMERIC/JSON/BOOLEAN/DATE/KEYWORD 按后端取子集）；`FilterExpr` 由根包 `ParseFilterExpr` 解析为节点树，能译则下推（如 ES 的 bool 查询），不能译自动回退内存 `rag.Refilter`——**过滤表达式一份，处处生效**。

后端能力对照（选型速查）：

| 后端 | FilterExpr 下推 | 说明 |
|---|---|---|
| `es` | ✓（bool 查询） | 全文+向量混合检索，生产首选 |
| `pgvector` | ✓（SQL 谓词） | 复用既有 PostgreSQL |
| `chroma` / `dashvector` | 回退内存 | REST 直连，部署轻 |
| `sqlrepo`（MySQL/MariaDB） | 回退内存 | 无向量插件时的兼容路径 |
| `redisrepo` | 回退内存 | 依赖 Redis 向量索引 |
| `vectorest`（OpenSearch/Weaviate/VectoRex/TcVectorDB） | 回退内存 | REST 四件；TcVectorDB 服务端 embedding |
| `rag.NewInMemoryRepository` | 内存即本体 | 暴力余弦；Save 自动嵌入回填、空 ID 补随机 UUID；原型与单测用 |

`RestRepositoryBase`（根包）为 REST 系后端提供 save 分批与 Refilter 基座，新接后端只需实现传输与查询两块。

## 7. 消费：拼提示 vs 交给模型自主检索

### 7.1 PromptAugment（检索结果直接拼进消息）

```go
msg, err := rag.PromptAugment(ctx, repo, "公司年假政策？")
// 生成 "%s\n\n Now: %s\n\n References: %s" 增强消息，直接加入 Prompt
p := ai.NewPrompt(msg)
res, _ := m.Prompt(p).Call()
```

### 7.2 RepositoryTool（模型自己决定何时检索）

```go
tool := rag.NewRepositoryTool(repo, rag.WithReranker(rerankModel))
// 工具名 repository_query；参数 queries（数组，取前 5 生效）+ topK（缺省 3）
// 多重查询结果汇总为 Markdown 返回给模型

res, err := m.Prompt("对比 A 与 B 两款产品的保修条款").
    Options(func(o *ai.ChatOptions) {
        on := true
        o.AutoToolCall = &on
        o.Tools = []ai.FunctionTool{tool}
    }).Call()
```

`rag.WithReranker(r)` 接受任意 `rag.Reranker`（[ai/rerank](ai.md) 的 `*rerank.Model` 开箱即用），检索后先重排再截断。生产推荐此形态：检索时机与查询词改写都交给模型。

两种消费形态对比：

| | PromptAugment | RepositoryTool |
|---|---|---|
| 检索时机 | 每次调用前固定执行 | 模型自主决定 |
| 查询词 | 原始问题 | 模型可改写/多查询（最多 5 组） |
| Token 成本 | 必付 | 命中工具才付 |
| 适用 | FAQ、固定语料问答 | 综合分析、多跳问题 |

## 8. rag/search：三家 web 搜索仓储

`ai/rag/search` 把商业搜索 API 包装成只读 `rag.Repository`（无 Storer）：

```go
repo := search.NewTavily(search.WithAPIKey("tk"))      // 或环境变量 TAVILY_API_KEY
q := rag.NewQuery("golang generics"); q.Freshness = rag.LastWeek()
docs, _ := repo.SearchQuery(ctx, q)

bocha := search.NewBocha(search.WithAPIKey(bochaKey))  // 博查
baidu := search.NewBaidu(search.BaiduSearchAI, search.WithAPIKey(bdKey))  // 百度 AI 搜索
baidu.Model("ernie-4.5-8k")                             // 覆写模型
```

公共选项：`WithAPIKey` / `WithAPIURL` / `WithTimeout`（60s）/ `WithProxy` / `WithEmbedder(e)`（命中后本地嵌入重排 + Refilter）。Tavily 额外提供完整客户端面：`NewTavilyClient(...)` 的 `SearchFull` / `Extract` / `Crawl` / `Map`（`NewTavilySearch(query)` 构造条件，`MaxResults` 缺省 5）。多引擎可组合成切片轮询/并联检索：

```go
repos := []rag.Repository{
    search.NewTavily(search.WithAPIKey(tk)),
    search.NewBocha(search.WithAPIKey(bk)),
    search.NewBaidu(search.BaiduSearchBasic, search.WithAPIKey(bd)),
}
// 应用层聚合：并发查询三家、合并去重后交给 Reranker
```

| 引擎 | API Key 环境变量 | 特点 |
|---|---|---|
| Tavily | `TAVILY_API_KEY` | 检索+抽取+爬站+站点地图全家桶 |
| Bocha 博查 | `BOCHA_API_KEY` | 国内可直连 |
| 百度 | `BAIDU_API_KEY` | `BaiduSearchBasic` 普通版 / `BaiduSearchAI` AI 搜索（`Model()` 覆写，缺省 `ernie-3.5-8k`） |

## 9. 端到端示例

生产化清单（从原型到上线）：

| 阶段 | 动作 |
|---|---|
| 原型 | `rag.NewInMemoryRepository(embedder)` + MarkdownLoader 跑通问答 |
| 规模化 | 换 ragrepo 后端；`InitRepository(ctx)` 建索引；声明 `MetadataField` 支持过滤下推 |
| 增量 | 新文档走 Loader → Splitter → `Save`；变更用 `DeleteByID` + 重存 |
| 提质 | 挂 `WithReranker`；中文语料配 `SemanticSplitter` + `ChineseDelim` |
| 观测 | `Save` 的 progress 回调记入库进度；检索失败回退 PromptAugment |

```go
// ① 嵌入模型与仓储
emb, _ := embedding.NewBuilder(embURL).APIKey("k").Model("text-embedding-v3").Build()
repo := rag.NewInMemoryRepository(emb)   // 生产替换为 ragrepo 后端并先 InitRepository(ctx)

// ② 加载 → 切分 → 入库
docs, _ := ragloader.NewWordLoaderFromFile("产品手册.docx").Load(ctx)
split, _ := rag.NewSplitterPipeline(
    rag.NewTokenSizeTextSplitter(),
).Split(ctx, docs)
_ = repo.Save(ctx, split, func(done, total int) { log.Info("index", "done", done, "total", total) })

// ③ 挂成工具，问答
tool := rag.NewRepositoryTool(repo)
on := true
res, _ := m.Prompt("重启设备的步骤？").
    Options(func(o *ai.ChatOptions) {
        o.AutoToolCall = &on
        o.Tools = []ai.FunctionTool{tool}
    }).Call()
fmt.Println(res.Text())
```

## 10. 模块结构

| 路径 | 职责 |
|---|---|
| `ai/rag/document.go` `query.go` | 文档模型、检索查询、相似度与 Refilter |
| `ai/rag/loader.go` `splitter*.go` | Loader SPI、四切分器与管道 |
| `ai/rag/repository.go` `memory.go` | 三 SPI + PromptAugment + RepositoryTool + 内存实现 |
| `ai/rag/search/` | Tavily / Bocha / 百度三家搜索仓储 |
| `plugins/ai/ragloader/` | markitdown.go 统一转换 + 7 格式加载器 |
| `plugins/ai/ragrepo/` | filter.go（表达式翻译）+ rest.go 基座 + 7 个后端子包 |
| `_test/ai_test/rag*.go` | 核心契约测试；`_test/ai_plugin_test/` 插件级测试 |

## 11. 总结

- **SPI 极简**：六个接口撑起整条流水线，接口之和不超过十个方法
- **过滤一份语义两处执行**：enjoy `FilterExpr` 能下推则下推、不能则内存兜底，调用方无感
- **格式问题交给 markitdown**：重格式统一转 Markdown，加载器只负责"怎么切"
- **两种消费姿势**：PromptAugment 简单可控；RepositoryTool 让模型自主检索（配 Reranker 更准）
- **后端按需引入**：核心零依赖起步（InMemory），规模化时换 ragrepo 后端，业务代码不动

### 延伸阅读

- [ai — 聊天核心与 embedding 模型](ai.md)
- [ai-talent — web.Search / web.Crawler / bleve 等检索类能力包](ai-talent.md)
- [elasticsearch — ES 客户端（ragrepo/es 依赖）](elasticsearch.md)
- [db — 数据库访问（pgvector/sqlrepo 依赖）](db.md)
- 架构设计：[../arch/ai/05-rag-design.md](../arch/ai/05-rag-design.md)
