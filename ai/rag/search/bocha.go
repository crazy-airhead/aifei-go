// bocha.go Bocha 联网搜索（对标 BochaWebSearchRepository；Java 无默认端点——
// Go 补官方 open.bochaai.com 缺省，可经 WithAPIURL 覆盖）。
package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/crazy-airhead/aifei-go/ai/httpx"
	"github.com/crazy-airhead/aifei-go/ai/rag"
)

// BochaDefaultAPIURL 官方端点（Go 增量缺省；Java 要求显式传入 apiUrl）。
const BochaDefaultAPIURL = "https://open.bochaai.com/api/v1/web-search"

// BochaRepository Bocha 搜索即仓储（只读；实现 rag.Repository）。此实现亦
// 可作为对接其它搜索服务的参考样板（Java 同款定位）。
type BochaRepository struct {
	apiURL   string
	apiKey   string
	http     *httpx.Client
	embedder rag.Embedder
}

// NewBocha 建 Bocha 仓储（apiKey 缺省取 BOCHA_API_KEY）。
func NewBocha(opts ...Option) *BochaRepository {
	c := &config{}
	for _, o := range opts {
		o(c)
	}
	apiURL := c.apiURL
	if apiURL == "" {
		apiURL = BochaDefaultAPIURL
	}
	return &BochaRepository{
		apiURL:   apiURL,
		apiKey:   resolveEnv(c.apiKey, EnvBochaAPIKey),
		http:     newHTTPClient(c),
		embedder: c.embedder,
	}
}

// bochaResponse Bocha 响应形状：code + data.webPages.value[]（snippet/title/url）。
type bochaResponse struct {
	Code int `json:"code"`
	Data struct {
		WebPages struct {
			Value []struct {
				Snippet string `json:"snippet"`
				Title   string `json:"title"`
				URL     string `json:"url"`
			} `json:"value"`
		} `json:"webPages"`
	} `json:"data"`
}

// Search 简单检索（缺省条件）。
func (r *BochaRepository) Search(ctx context.Context, query string) ([]*rag.Document, error) {
	return r.SearchQuery(ctx, rag.NewQuery(query))
}

// SearchQuery 条件检索：query/count/freshness 三参 POST → code!=200 报错 →
// data.webPages.value[] 映射（snippet 为内容）→ 可选嵌入重排。
func (r *BochaRepository) SearchQuery(ctx context.Context, q *rag.Query) ([]*rag.Document, error) {
	if q == nil {
		q = rag.NewQuery("")
	}
	if r.apiKey == "" { // 晚失败（同 tavily）
		return nil, errors.New("search: bocha apiKey is required (set " + EnvBochaAPIKey + " or use WithAPIKey)")
	}
	limit := q.Limit
	if limit <= 0 {
		limit = rag.DefaultLimit
	}
	// count 为字符串（Bocha API 形状；Java String.valueOf 同款）
	body := map[string]any{"query": q.Query, "count": strconv.Itoa(limit)}
	if f := bochaFreshness(q.Freshness); f != "" {
		body["freshness"] = f
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	respJSON, _, err := r.http.PostJSON(ctx, r.apiURL, data,
		map[string]string{"Authorization": "Bearer " + r.apiKey})
	if err != nil {
		return nil, err
	}
	var resp bochaResponse
	if err := json.Unmarshal(respJSON, &resp); err != nil {
		return nil, err
	}
	if resp.Code != 200 { // Java: throw new IOException(respNode.toJson())
		return nil, fmt.Errorf("search: bocha error response: %s", string(respJSON))
	}
	docs := make([]*rag.Document, 0, len(resp.Data.WebPages.Value))
	for _, hit := range resp.Data.WebPages.Value {
		docs = append(docs, rag.NewDocument(hit.Snippet).
			Meta("title", hit.Title).
			Meta("url", hit.URL))
	}
	return embedRefilter(ctx, r.embedder, docs, q)
}

var _ rag.Repository = (*BochaRepository)(nil)
