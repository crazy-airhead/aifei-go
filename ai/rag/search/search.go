// Package search 搜索即仓储（契约 05-rag-design §8，对标 solon-ai-rag-searchs
// 三件）：tavily / bocha / baidu 联网搜索实现 rag.Repository 只读面——网络
// 搜索型仓储天然不可写，故均不实现 Storer（使用方类型断言可探测）。命中映射
// 为 rag.Document{URL, Content, Score, Metadata}；APIKey 缺省经环境变量注入
// （TAVILY_API_KEY / BOCHA_API_KEY / BAIDU_API_KEY）。HTTP 走 ai/httpx 缝
// （对标 Java AiConfig.createHttpUtils / HttpUtils）。
package search

import (
	"context"
	"os"
	"time"

	"github.com/crazy-airhead/aifei-go/ai/httpx"
	"github.com/crazy-airhead/aifei-go/ai/rag"
)

// 三家 APIKey 的环境变量名。
const (
	EnvTavilyAPIKey = "TAVILY_API_KEY"
	EnvBochaAPIKey  = "BOCHA_API_KEY"
	EnvBaiduAPIKey  = "BAIDU_API_KEY"
)

// config 三仓储共用配置（apiURL 语义随厂家：tavily 为 API base，bocha/baidu
// 为完整端点）。
type config struct {
	apiKey   string
	apiURL   string
	timeout  time.Duration
	proxy    string
	embedder rag.Embedder
}

// Option 三仓储共用构建选项。
type Option func(*config)

// WithAPIKey 显式 API 密钥（缺省取对应环境变量）。
func WithAPIKey(key string) Option { return func(c *config) { c.apiKey = key } }

// WithAPIURL 显式 API 地址（tavily 为 base，如 https://api.tavily.com；
// bocha/baidu 为完整端点 URL；缺省取各家官方端点）。
func WithAPIURL(url string) Option { return func(c *config) { c.apiURL = url } }

// WithTimeout 请求超时（缺省 60s，对标 Java Duration.ofSeconds(60)）。
func WithTimeout(d time.Duration) Option { return func(c *config) { c.timeout = d } }

// WithProxy HTTP(S) 代理地址。
func WithProxy(raw string) Option { return func(c *config) { c.proxy = raw } }

// WithEmbedder 嵌入模型（命中后相似度重排 + 二次过滤；不设则原样返回——
// Java embeddingModel 可空同款）。
func WithEmbedder(e rag.Embedder) Option { return func(c *config) { c.embedder = e } }

// resolveEnv 显式值优先，否则取环境变量。
func resolveEnv(explicit, envName string) string {
	if explicit != "" {
		return explicit
	}
	return os.Getenv(envName)
}

// newHTTPClient 按配置建 httpx 客户端（Authorization 头由各仓储按鉴权形状
// 自行追加——baidu 用 X-Appbuilder-Authorization，不在此处统一）。
func newHTTPClient(c *config) *httpx.Client {
	var opts []httpx.Option
	if c.timeout > 0 {
		opts = append(opts, httpx.WithTimeout(c.timeout))
	}
	if c.proxy != "" {
		opts = append(opts, httpx.WithProxy(c.proxy))
	}
	return httpx.New(opts...)
}

// embedRefilter 可选嵌入重排（三仓储共用，Java embeddingModel != null 分支：
// embeddingModel.embed(docs) → embed(query) → SimilarityUtil.score 逐个余弦
// 打分副本 → refilter）。e 为 nil 时原样返回（不重排也不过滤——Java 同款：
// 无嵌入模型时原生得分即终态）。
func embedRefilter(ctx context.Context, e rag.Embedder, docs []*rag.Document, q *rag.Query) ([]*rag.Document, error) {
	if e == nil {
		return docs, nil
	}
	if q == nil {
		q = rag.NewQuery("")
	}
	// 文档内容分批嵌入回填（对标 embeddingModel.embed(docs)）
	for _, batch := range rag.Partition(docs, e.BatchSize()) {
		texts := make([]string, len(batch))
		for i, d := range batch {
			texts[i] = d.Content
		}
		vecs, err := e.EmbedTexts(ctx, texts)
		if err != nil {
			return nil, err
		}
		for i, d := range batch {
			d.Embedding = vecs[i]
		}
	}
	qv, err := e.Embed(ctx, q.Query)
	if err != nil {
		return nil, err
	}
	scored := make([]*rag.Document, 0, len(docs))
	for _, d := range docs {
		s, err := rag.CosineSimilarity(qv, d.Embedding)
		if err != nil {
			return nil, err
		}
		scored = append(scored, rag.CopyAndScore(d, s))
	}
	return rag.Refilter(scored, q)
}

// window Go Freshness 时间窗宽度（End 缺省按现在计；零窗 = 不限）。
func window(f *rag.Freshness) time.Duration {
	if f == nil || f.Start.IsZero() {
		return 0
	}
	end := f.End
	if end.IsZero() {
		end = time.Now()
	}
	return end.Sub(f.Start)
}

// tavilyTimeRange Go Freshness 是真实时间窗（Java 为 oneDay…oneYear 枚举），
// 按窗宽映射 tavily time_range：≤1 天 day、≤7 天 week、≤30 天 month、
// ≤365 天 year，更长或不限则不传（Java default→null 同款）。
func tavilyTimeRange(f *rag.Freshness) string {
	d := window(f)
	switch {
	case d <= 0:
		return ""
	case d <= 24*time.Hour:
		return "day"
	case d <= 7*24*time.Hour:
		return "week"
	case d <= 30*24*time.Hour:
		return "month"
	case d <= 365*24*time.Hour:
		return "year"
	default:
		return ""
	}
}

// bochaFreshness 同上映射 bocha freshness 枚举值（Java Freshness.value 字符串）。
func bochaFreshness(f *rag.Freshness) string {
	d := window(f)
	switch {
	case d <= 0:
		return ""
	case d <= 24*time.Hour:
		return "oneDay"
	case d <= 7*24*time.Hour:
		return "oneWeek"
	case d <= 30*24*time.Hour:
		return "oneMonth"
	case d <= 365*24*time.Hour:
		return "oneYear"
	default:
		return "" // 超一年无对应枚举（Java noLimit 由调用方显式声明，非窗宽推导）
	}
}
