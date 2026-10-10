// tavily.go Tavily 联网搜索（对标 TavilyClient + TavilySimpleSearchRepository
// + TavilyWebSearchRepository——Java 薄委托版与全功能版在 Go 合为一型：
// TavilyRepository 实现 rag.Repository，同时经 Client()/SearchFull/Extract/
// Crawl/Map 暴露完整功能）。REST + APIKey，零外部依赖（net/http 经 httpx 缝）。
package search

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai/httpx"
	"github.com/crazy-airhead/aifei-go/ai/rag"
)

// TavilyDefaultAPIBase 官方端点（Java DEFAULT_API_BASE）。
const TavilyDefaultAPIBase = "https://api.tavily.com"

// ---- 条件（对标 SearchCondition/ExtractCondition/CrawlCondition/MapCondition）----

// TavilySearchCondition 搜索条件（零值字段不发送——Java setIfNotNull 语义）。
type TavilySearchCondition struct {
	Query                    string
	MaxResults               int    // <=0 视为 5（Java 缺省）
	SearchDepth              string // basic / advanced
	ChunksPerSource          int    // <=0 不传
	Topic                    string // general / news
	TimeRange                string // day / week / month / year
	StartDate                string
	EndDate                  string
	IncludeAnswer            string
	IncludeRawContent        string
	IncludeImages            bool // true 才发送（Java 同款）
	IncludeImageDescriptions bool
	IncludeFavicon           bool
	IncludeDomains           []string // 非空才发送
	ExcludeDomains           []string
	Country                  string
	AutoParameters           bool
	ExactMatch               bool
}

// NewTavilySearch 建搜索条件（max_results 缺省 5）。
func NewTavilySearch(query string) *TavilySearchCondition {
	return &TavilySearchCondition{Query: query, MaxResults: 5}
}

// TavilyExtractCondition 提取条件。
type TavilyExtractCondition struct {
	URLs            []string
	Query           string
	ChunksPerSource int // <=0 不传
	ExtractDepth    string
	Format          string
	Timeout         int // <=0 不传（毫秒，原样透传）
	IncludeImages   bool
	IncludeFavicon  bool
}

// NewTavilyExtract 建提取条件。
func NewTavilyExtract(urls ...string) *TavilyExtractCondition {
	return &TavilyExtractCondition{URLs: urls}
}

// TavilyCrawlCondition 爬取条件（maxDepth/maxBreadth/limit/allowExternal 有
// Java 同款缺省，经 NewTavilyCrawl 预填）。
type TavilyCrawlCondition struct {
	URL             string
	Instructions    string
	ChunksPerSource int
	MaxDepth        int
	MaxBreadth      int
	Limit           int
	SelectPaths     []string
	SelectDomains   []string
	ExcludePaths    []string
	ExcludeDomains  []string
	AllowExternal   bool
	IncludeImages   bool
	IncludeFavicon  bool
	ExtractDepth    string
	Format          string
	Timeout         int
}

// NewTavilyCrawl 建爬取条件（1/20/50/true——Java 字段缺省）。
func NewTavilyCrawl(url string) *TavilyCrawlCondition {
	return &TavilyCrawlCondition{URL: url, MaxDepth: 1, MaxBreadth: 20, Limit: 50, AllowExternal: true}
}

// TavilyMapCondition 站点映射条件（缺省同 NewTavilyMap 预填）。
type TavilyMapCondition struct {
	URL            string
	Instructions   string
	MaxDepth       int
	MaxBreadth     int
	Limit          int
	SelectPaths    []string
	SelectDomains  []string
	ExcludePaths   []string
	ExcludeDomains []string
	AllowExternal  bool
	Timeout        int
}

// NewTavilyMap 建映射条件（1/20/50/true——Java 字段缺省）。
func NewTavilyMap(url string) *TavilyMapCondition {
	return &TavilyMapCondition{URL: url, MaxDepth: 1, MaxBreadth: 20, Limit: 50, AllowExternal: true}
}

// ---- 响应 ----

// TavilyImage 搜索图片（数组项可为对象或纯 URL 字符串——Java 同款双形态）。
type TavilyImage struct {
	URL         string
	Description string
}

// UnmarshalJSON 兼容 {"url":…,"description":…} 与 "https://…" 双形态。
func (img *TavilyImage) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		img.URL, img.Description = s, ""
		return nil
	}
	var raw struct {
		URL         string `json:"url"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	img.URL, img.Description = raw.URL, raw.Description
	return nil
}

// TavilySearchResult 搜索命中。
type TavilySearchResult struct {
	Title      string  `json:"title"`
	URL        string  `json:"url"`
	Content    string  `json:"content"`
	Score      float64 `json:"score"`
	RawContent string  `json:"raw_content"`
	Favicon    string  `json:"favicon"`
}

// ToDocument 映射为 rag.Document（rawContent/favicon 进元数据——Java 同款）。
func (r *TavilySearchResult) ToDocument() *rag.Document {
	doc := rag.NewDocument(r.Content).
		Meta("title", r.Title).
		Meta("url", r.URL).
		SetScore(r.Score)
	if r.RawContent != "" {
		doc.Meta("rawContent", r.RawContent)
	}
	if r.Favicon != "" {
		doc.Meta("favicon", r.Favicon)
	}
	return doc
}

// TavilySearchResponse 搜索响应（answer/images/results 等完整信息）。
type TavilySearchResponse struct {
	Query        string               `json:"query"`
	Answer       string               `json:"answer"`
	Images       []TavilyImage        `json:"images"`
	Results      []TavilySearchResult `json:"results"`
	ResponseTime float64              `json:"response_time"`
	RequestID    string               `json:"request_id"`
}

// ToDocuments 命中批量映射。
func (r *TavilySearchResponse) ToDocuments() []*rag.Document {
	docs := make([]*rag.Document, 0, len(r.Results))
	for i := range r.Results {
		docs = append(docs, r.Results[i].ToDocument())
	}
	return docs
}

// TavilyExtractResult 提取成功项。
type TavilyExtractResult struct {
	URL        string   `json:"url"`
	RawContent string   `json:"raw_content"`
	Favicon    string   `json:"favicon"`
	Images     []string `json:"images"`
}

// TavilyFailedResult 提取失败项。
type TavilyFailedResult struct {
	URL   string `json:"url"`
	Error string `json:"error"`
}

// TavilyExtractResponse 提取响应。
type TavilyExtractResponse struct {
	Results       []TavilyExtractResult `json:"results"`
	FailedResults []TavilyFailedResult  `json:"failed_results"`
	ResponseTime  float64               `json:"response_time"`
	RequestID     string                `json:"request_id"`
}

// TavilyCrawlResult 爬取命中。
type TavilyCrawlResult struct {
	URL        string `json:"url"`
	RawContent string `json:"raw_content"`
	Favicon    string `json:"favicon"`
}

// TavilyCrawlResponse 爬取响应。
type TavilyCrawlResponse struct {
	BaseURL      string              `json:"base_url"`
	Results      []TavilyCrawlResult `json:"results"`
	ResponseTime float64             `json:"response_time"`
	RequestID    string              `json:"request_id"`
}

// TavilyMapResponse 站点映射响应（Results 为发现的 URL 列表）。
type TavilyMapResponse struct {
	BaseURL      string   `json:"base_url"`
	Results      []string `json:"results"`
	ResponseTime float64  `json:"response_time"`
	RequestID    string   `json:"request_id"`
}

// ---- 客户端（对标 TavilyClient）----

// TavilyClient Tavily API 客户端（Search/Extract/Crawl/Map 四操作）。
type TavilyClient struct {
	apiBase string
	apiKey  string
	http    *httpx.Client
}

// NewTavilyClient 建客户端（apiKey 缺省取 TAVILY_API_KEY；apiURL 为 base，
// 缺省 https://api.tavily.com，尾斜杠剥除——Java stripTrailingSlash 同款）。
func NewTavilyClient(opts ...Option) *TavilyClient {
	c := &config{}
	for _, o := range opts {
		o(c)
	}
	apiBase := TavilyDefaultAPIBase
	if c.apiURL != "" {
		apiBase = strings.TrimSuffix(c.apiURL, "/")
	}
	return &TavilyClient{apiBase: apiBase, apiKey: resolveEnv(c.apiKey, EnvTavilyAPIKey), http: newHTTPClient(c)}
}

// Search 完整参数搜索。
func (c *TavilyClient) Search(ctx context.Context, cond *TavilySearchCondition) (*TavilySearchResponse, error) {
	if cond == nil {
		return nil, errors.New("search: tavily search condition must not be nil")
	}
	maxResults := cond.MaxResults
	if maxResults <= 0 {
		maxResults = 5
	}
	body := map[string]any{
		"query":       cond.Query,
		"max_results": maxResults,
	}
	setStr(body, "search_depth", cond.SearchDepth)
	setStr(body, "topic", cond.Topic)
	setStr(body, "time_range", cond.TimeRange)
	setStr(body, "start_date", cond.StartDate)
	setStr(body, "end_date", cond.EndDate)
	setStr(body, "include_answer", cond.IncludeAnswer)
	setStr(body, "include_raw_content", cond.IncludeRawContent)
	setStr(body, "country", cond.Country)
	setPositiveInt(body, "chunks_per_source", cond.ChunksPerSource)
	setTrue(body, "include_images", cond.IncludeImages)
	setTrue(body, "include_image_descriptions", cond.IncludeImageDescriptions)
	setTrue(body, "include_favicon", cond.IncludeFavicon)
	setTrue(body, "auto_parameters", cond.AutoParameters)
	setTrue(body, "exact_match", cond.ExactMatch)
	setStrArray(body, "include_domains", cond.IncludeDomains)
	setStrArray(body, "exclude_domains", cond.ExcludeDomains)

	out := &TavilySearchResponse{}
	if err := c.post(ctx, "/search", body, out); err != nil {
		return nil, err
	}
	return out, nil
}

// Extract 提取指定 URL 的内容。
func (c *TavilyClient) Extract(ctx context.Context, cond *TavilyExtractCondition) (*TavilyExtractResponse, error) {
	if cond == nil {
		return nil, errors.New("search: tavily extract condition must not be nil")
	}
	body := map[string]any{"urls": cond.URLs}
	setStr(body, "query", cond.Query)
	setStr(body, "extract_depth", cond.ExtractDepth)
	setStr(body, "format", cond.Format)
	setPositiveInt(body, "chunks_per_source", cond.ChunksPerSource)
	setPositiveInt(body, "timeout", cond.Timeout)
	setTrue(body, "include_images", cond.IncludeImages)
	setTrue(body, "include_favicon", cond.IncludeFavicon)

	out := &TavilyExtractResponse{}
	if err := c.post(ctx, "/extract", body, out); err != nil {
		return nil, err
	}
	return out, nil
}

// Crawl 从根 URL 爬取网站内容。
func (c *TavilyClient) Crawl(ctx context.Context, cond *TavilyCrawlCondition) (*TavilyCrawlResponse, error) {
	if cond == nil {
		return nil, errors.New("search: tavily crawl condition must not be nil")
	}
	body := map[string]any{
		"url":            cond.URL,
		"max_depth":      cond.MaxDepth,
		"max_breadth":    cond.MaxBreadth,
		"limit":          cond.Limit,
		"allow_external": cond.AllowExternal,
	}
	setStr(body, "instructions", cond.Instructions)
	setStr(body, "extract_depth", cond.ExtractDepth)
	setStr(body, "format", cond.Format)
	setPositiveInt(body, "chunks_per_source", cond.ChunksPerSource)
	setPositiveInt(body, "timeout", cond.Timeout)
	setTrue(body, "include_images", cond.IncludeImages)
	setTrue(body, "include_favicon", cond.IncludeFavicon)
	setStrArray(body, "select_paths", cond.SelectPaths)
	setStrArray(body, "select_domains", cond.SelectDomains)
	setStrArray(body, "exclude_paths", cond.ExcludePaths)
	setStrArray(body, "exclude_domains", cond.ExcludeDomains)

	out := &TavilyCrawlResponse{}
	if err := c.post(ctx, "/crawl", body, out); err != nil {
		return nil, err
	}
	return out, nil
}

// Map 映射网站结构，返回发现的 URL 列表。
func (c *TavilyClient) Map(ctx context.Context, cond *TavilyMapCondition) (*TavilyMapResponse, error) {
	if cond == nil {
		return nil, errors.New("search: tavily map condition must not be nil")
	}
	body := map[string]any{
		"url":            cond.URL,
		"max_depth":      cond.MaxDepth,
		"max_breadth":    cond.MaxBreadth,
		"limit":          cond.Limit,
		"allow_external": cond.AllowExternal,
	}
	setStr(body, "instructions", cond.Instructions)
	setPositiveInt(body, "timeout", cond.Timeout)
	setStrArray(body, "select_paths", cond.SelectPaths)
	setStrArray(body, "select_domains", cond.SelectDomains)
	setStrArray(body, "exclude_paths", cond.ExcludePaths)
	setStrArray(body, "exclude_domains", cond.ExcludeDomains)

	out := &TavilyMapResponse{}
	if err := c.post(ctx, "/map", body, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *TavilyClient) post(ctx context.Context, path string, body map[string]any, out any) error {
	if c.apiKey == "" { // Java ClientBuilder 构造即抛；Go 随 env 解析晚失败（P4-a 惯例）
		return errors.New("search: tavily apiKey is required (set " + EnvTavilyAPIKey + " or use WithAPIKey)")
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, _, err := c.http.PostJSON(ctx, c.apiBase+path, data,
		map[string]string{"Authorization": "Bearer " + c.apiKey})
	if err != nil {
		return err
	}
	return json.Unmarshal(resp, out)
}

// setStr 非空才写（Java setIfNotNull）。
func setStr(body map[string]any, key, value string) {
	if value != "" {
		body[key] = value
	}
}

// setPositiveInt 正数才写（Java Integer null 判定）。
func setPositiveInt(body map[string]any, key string, value int) {
	if value > 0 {
		body[key] = value
	}
}

// setTrue 真才写（Java if(isXxx()) 同款——false 不占位）。
func setTrue(body map[string]any, key string, value bool) {
	if value {
		body[key] = true
	}
}

// setStrArray 非空才写（Java setStringArray）。
func setStrArray(body map[string]any, key string, values []string) {
	if len(values) > 0 {
		body[key] = values
	}
}

// ---- 仓储 ----

// TavilyRepository Tavily 搜索即仓储（只读；实现 rag.Repository）。
type TavilyRepository struct {
	client   *TavilyClient
	embedder rag.Embedder
}

// NewTavily 建 Tavily 仓储。
func NewTavily(opts ...Option) *TavilyRepository {
	c := &config{}
	for _, o := range opts {
		o(c)
	}
	return &TavilyRepository{client: NewTavilyClient(opts...), embedder: c.embedder}
}

// Client 内部完整功能客户端（对标 getClient）。
func (r *TavilyRepository) Client() *TavilyClient { return r.client }

// Search 简单检索（缺省条件）。
func (r *TavilyRepository) Search(ctx context.Context, query string) ([]*rag.Document, error) {
	return r.SearchQuery(ctx, rag.NewQuery(query))
}

// SearchQuery 条件检索：Query 映射 TavilySearchCondition（limit→max_results、
// Freshness 时间窗→time_range）→ 完整搜索 → 命中映射 → 可选嵌入重排。
func (r *TavilyRepository) SearchQuery(ctx context.Context, q *rag.Query) ([]*rag.Document, error) {
	if q == nil {
		q = rag.NewQuery("")
	}
	cond := NewTavilySearch(q.Query)
	if q.Limit > 0 {
		cond.MaxResults = q.Limit
	}
	cond.TimeRange = tavilyTimeRange(q.Freshness)
	resp, err := r.client.Search(ctx, cond)
	if err != nil {
		return nil, err
	}
	return embedRefilter(ctx, r.embedder, resp.ToDocuments(), q)
}

// SearchFull 完整 Tavily 参数搜索（返回完整响应——answer/images 等）。
func (r *TavilyRepository) SearchFull(ctx context.Context, cond *TavilySearchCondition) (*TavilySearchResponse, error) {
	return r.client.Search(ctx, cond)
}

// Extract 提取指定 URL 的内容。
func (r *TavilyRepository) Extract(ctx context.Context, cond *TavilyExtractCondition) (*TavilyExtractResponse, error) {
	return r.client.Extract(ctx, cond)
}

// Crawl 从根 URL 爬取网站内容。
func (r *TavilyRepository) Crawl(ctx context.Context, cond *TavilyCrawlCondition) (*TavilyCrawlResponse, error) {
	return r.client.Crawl(ctx, cond)
}

// Map 映射网站结构。
func (r *TavilyRepository) Map(ctx context.Context, cond *TavilyMapCondition) (*TavilyMapResponse, error) {
	return r.client.Map(ctx, cond)
}

var _ rag.Repository = (*TavilyRepository)(nil)
