// Package rerank 重排模型（对标 solon-ai RerankingModel，02-chat-core §11）：
// 与 chat/embedding 同构的 Config + 方言 + httpx，契约从简——「query + 文档数组 →
// 相关性打分排序」。Model.Rerank 即 rag.Reranker 的满足面（P4-b 预留接口位的
// 内置实现）；usage 不进 Go 契约（与 embedding 对齐）。
package rerank

import (
	"context"
	"fmt"
	"sort"
	"time"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/httpx"
	"github.com/crazy-airhead/aifei-go/ai/rag"
)

// Result 单条重排结果（对标 Reranking）：index 与请求 documents 下标对齐。
type Result struct {
	Index          int
	Text           string
	RelevanceScore float64
}

// Response 重排响应：results 按 index 升序（对标 RerankingResponse 构造期排序）。
type Response struct {
	Model   string
	Results []Result
}

// RequestDesc 重排请求描述（对标 RerankingRequestDesc）：options 链式 + Call。
type RequestDesc struct {
	model     *Model
	query     string
	documents []*rag.Document
	options   map[string]any
}

func (r *RequestDesc) Options(opts map[string]any) *RequestDesc {
	for k, v := range opts {
		r.options[k] = v
	}
	return r
}

// Option 单个选项（top_n / return_documents / user 等，方言透传）。
func (r *RequestDesc) Option(key string, val any) *RequestDesc {
	r.options[key] = val
	return r
}

// TopN 重排序返回条数（对标 RerankingOptions.top_n）。
func (r *RequestDesc) TopN(n int) *RequestDesc { return r.Option("top_n", n) }

// Call 发起重排请求（options = 模型级默认 + 请求级覆盖）。
func (r *RequestDesc) Call(ctx context.Context) (*Response, error) {
	opts := map[string]any{}
	for k, v := range r.model.options {
		opts[k] = v
	}
	for k, v := range r.options {
		opts[k] = v
	}

	body, err := r.model.dialect.BuildRequestJSON(r.model.config, opts, r.query, r.documents)
	if err != nil {
		return nil, err
	}

	if r.model.config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.model.config.Timeout)
		defer cancel()
	}
	respBody, _, err := r.model.client.PostJSON(ctx, r.model.config.APIURL, body, r.model.requestHeader())
	if err != nil {
		return nil, err
	}
	return r.model.dialect.ParseResponseJSON(respBody)
}

// Model 重排模型。
type Model struct {
	config  *ai.Config
	dialect Dialect
	client  *httpx.Client
	options map[string]any // 模型级默认选项（top_n 等，逐请求可覆盖）
}

// New 构建并选定方言（apiUrl/model 必填，与 NewChatModel 同错误面）。
func New(cfg ai.Config) (*Model, error) {
	if cfg.APIURL == "" {
		return nil, fmt.Errorf("ai: the config.apiUrl is required")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("ai: the config.model is required")
	}
	d := SelectRerankDialect(&cfg)
	if d == nil {
		return nil, fmt.Errorf("ai: the rerank dialect(provider) no matched, check config or dependencies")
	}

	var hopts []httpx.Option
	if cfg.Proxy != "" {
		hopts = append(hopts, httpx.WithProxy(cfg.Proxy))
	}
	if cfg.Doer != nil {
		hopts = append(hopts, httpx.WithDoer(cfg.Doer))
	}
	return &Model{
		config:  &cfg,
		dialect: d,
		client:  httpx.New(hopts...),
		options: map[string]any{},
	}, nil
}

// Builder 链式构建（NewBuilder(apiURL).APIKey(k).Model(m).Build()）。
type Builder struct {
	cfg     ai.Config
	options map[string]any
}

func NewBuilder(apiURL string) *Builder {
	return &Builder{cfg: ai.Config{APIURL: apiURL}, options: map[string]any{}}
}

func (b *Builder) APIKey(k string) *Builder   { b.cfg.APIKey = k; return b }
func (b *Builder) Model(m string) *Builder    { b.cfg.Model = m; return b }
func (b *Builder) Provider(p string) *Builder { b.cfg.Provider = p; return b }
func (b *Builder) Standard(s string) *Builder { b.cfg.Standard = s; return b }
func (b *Builder) Doer(d httpx.Doer) *Builder { b.cfg.Doer = d; return b }
func (b *Builder) Name(n string) *Builder     { b.cfg.Name = n; return b }
func (b *Builder) Timeout(t time.Duration) *Builder {
	b.cfg.Timeout = t
	return b
}

// Options 模型级默认选项（同名键逐请求可覆盖）。
func (b *Builder) Options(opts map[string]any) *Builder {
	for k, v := range opts {
		b.options[k] = v
	}
	return b
}

// Config 剩余字段的逃生舱配置。
func (b *Builder) Config(fn func(*ai.Config)) *Builder {
	fn(&b.cfg)
	return b
}

func (b *Builder) Build() (*Model, error) {
	m, err := New(b.cfg)
	if err != nil {
		return nil, err
	}
	m.options = b.options
	return m, nil
}

func (m *Model) Dialect() Dialect     { return m.dialect }
func (m *Model) Config() *ai.Config   { return m.config }
func (m *Model) Options() map[string]any { return m.options }

// Input 构建请求描述（对标 RerankingModel.input）。
func (m *Model) Input(query string, documents []*rag.Document) *RequestDesc {
	return &RequestDesc{model: m, query: query, documents: documents, options: map[string]any{}}
}

// Rerank 重排并返回按得分降序的文档（对标 RerankingModel.rerank）：响应条数与
// 输入不等时报错（防越界——Java 直接 get(i) 会 IndexOutOfBounds，Go 收敛为显式
// 错误）；原地写回 score 后排序。
func (m *Model) Rerank(ctx context.Context, query string, documents []*rag.Document) ([]*rag.Document, error) {
	resp, err := m.Input(query, documents).Call(ctx)
	if err != nil {
		return nil, err
	}
	if len(resp.Results) != len(documents) {
		return nil, fmt.Errorf("ai: the rerank results (%d) is not equal to the size of documents (%d)",
			len(resp.Results), len(documents))
	}
	for i, doc := range documents {
		doc.SetScore(resp.Results[i].RelevanceScore)
	}
	sorted := append([]*rag.Document(nil), documents...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Score() > sorted[j].Score() })
	return sorted, nil
}

// requestHeader 鉴权与默认头（重排方言无 HeaderDialect 定制面，与 embedding 对齐）。
func (m *Model) requestHeader() map[string]string {
	header := map[string]string{}
	for k, v := range m.config.Headers {
		header[k] = v
	}
	if m.config.APIKey != "" {
		header["Authorization"] = "Bearer " + m.config.APIKey
	}
	if m.config.UserAgent != "" {
		header["User-Agent"] = m.config.UserAgent
	}
	return header
}
