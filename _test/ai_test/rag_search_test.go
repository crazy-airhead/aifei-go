// rag_search_test.go P4-c 搜索即仓储测试：三家 httptest 假回放（各自响应
// 形状 + 请求形状 + 鉴权头）、APIKey 环境变量回退、Tavily 全功能四操作、
// 嵌入重排覆盖原生得分、空查询免发请求、错误面、只读断言（Repository 但
// 不可断言为 Storer）。
package ai_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/crazy-airhead/aifei-go/ai/rag"
	"github.com/crazy-airhead/aifei-go/ai/rag/search"
)

// reqCapture 捕获一次请求（后到覆盖先到——逐操作断言）。
type reqCapture struct {
	method string
	path   string
	header http.Header
	body   map[string]any
}

// newCaptureServer 回放固定 JSON 响应并捕获请求。
func newCaptureServer(t *testing.T, resp string) (*httptest.Server, *reqCapture) {
	t.Helper()
	cap := &reqCapture{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		cap.method, cap.path, cap.header = r.Method, r.URL.Path, r.Header.Clone()
		_ = json.Unmarshal(data, &cap.body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(ts.Close)
	return ts, cap
}

// ---- Tavily ----

const tavilySearchResp = `{
  "query": "golang",
  "answer": "Go is a programming language.",
  "images": [{"url": "https://img/1", "description": "gopher"}, "https://img/2"],
  "results": [
    {"title": "Go官网", "url": "https://go.dev", "content": "Go is a language",
     "score": 0.99, "raw_content": "raw-1", "favicon": "https://go.dev/fav.ico"},
    {"title": "Go百科", "url": "https://wiki/go", "content": "Go wiki page", "score": 0.55}
  ],
  "response_time": 1.2,
  "request_id": "req-1"
}`

func TestRagSearchTavily(t *testing.T) {
	ctx := context.Background()
	ts, cap := newCaptureServer(t, tavilySearchResp)
	repo := search.NewTavily(search.WithAPIURL(ts.URL), search.WithAPIKey("tk"))

	q := rag.NewQuery("golang")
	q.Limit = 2
	q.Freshness = rag.LastWeek()
	docs, err := repo.SearchQuery(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	// 请求形状：POST {base}/search + Bearer 鉴权 + query/max_results/time_range
	if cap.method != "POST" || cap.path != "/search" {
		t.Errorf("request = %s %s, want POST /search", cap.method, cap.path)
	}
	if got := cap.header.Get("Authorization"); got != "Bearer tk" {
		t.Errorf("Authorization = %q", got)
	}
	if cap.body["query"] != "golang" || cap.body["max_results"] != float64(2) {
		t.Errorf("body = %v", cap.body)
	}
	if cap.body["time_range"] != "week" { // LastWeek 时间窗 → week
		t.Errorf("time_range = %v", cap.body["time_range"])
	}

	// 命中映射：Content/Title/URL/Score + rawContent/favicon 元数据
	if len(docs) != 2 {
		t.Fatalf("docs = %d, want 2", len(docs))
	}
	d0 := docs[0]
	if d0.Content != "Go is a language" || d0.Title() != "Go官网" ||
		d0.URL() != "https://go.dev" || d0.Score() != 0.99 {
		t.Errorf("doc0 = %s", d0)
	}
	if d0.Metadata["rawContent"] != "raw-1" || d0.Metadata["favicon"] != "https://go.dev/fav.ico" {
		t.Errorf("doc0 metadata = %v", d0.Metadata)
	}
	if _, ok := docs[1].Metadata["rawContent"]; ok {
		t.Errorf("doc1 rawContent should be absent: %v", docs[1].Metadata)
	}

	// 无密钥晚失败（提示环境变量名）
	_, err = search.NewTavily(search.WithAPIURL(ts.URL)).Search(ctx, "x")
	if err == nil || !strings.Contains(err.Error(), search.EnvTavilyAPIKey) {
		t.Errorf("missing key err = %v", err)
	}
}

func TestRagSearchTavilyEnvKey(t *testing.T) {
	t.Setenv(search.EnvTavilyAPIKey, "env-key")
	ts, cap := newCaptureServer(t, tavilySearchResp)
	if _, err := search.NewTavily(search.WithAPIURL(ts.URL)).Search(context.Background(), "golang"); err != nil {
		t.Fatal(err)
	}
	if got := cap.header.Get("Authorization"); got != "Bearer env-key" {
		t.Errorf("Authorization = %q, want env fallback", got)
	}
}

func TestRagSearchTavilyFullOps(t *testing.T) {
	ctx := context.Background()
	cap := &reqCapture{}
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		cap.method, cap.path, cap.header = r.Method, r.URL.Path, r.Header.Clone()
		_ = json.Unmarshal(data, &cap.body)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/search":
			_, _ = w.Write([]byte(tavilySearchResp))
		case "/extract":
			_, _ = w.Write([]byte(`{"results":[{"url":"https://a","raw_content":"RA","favicon":"f","images":["i1"]}],
			  "failed_results":[{"url":"https://b","error":"timeout"}],"response_time":0.3,"request_id":"r"}`))
		case "/crawl":
			_, _ = w.Write([]byte(`{"base_url":"https://site","results":[{"url":"https://site/1","raw_content":"C1"}],
			  "response_time":0.5,"request_id":"r"}`))
		case "/map":
			_, _ = w.Write([]byte(`{"base_url":"https://site","results":["https://site/1","https://site/2"],
			  "response_time":0.2,"request_id":"r"}`))
		}
	}))
	ts.Start()
	t.Cleanup(ts.Close)
	repo := search.NewTavily(search.WithAPIURL(ts.URL), search.WithAPIKey("tk"))

	// SearchFull：完整响应（answer + images 双形态解析）
	sf, err := repo.SearchFull(ctx, search.NewTavilySearch("golang"))
	if err != nil {
		t.Fatal(err)
	}
	if sf.Answer != "Go is a programming language." || sf.RequestID != "req-1" {
		t.Errorf("searchFull = answer:%q id:%q", sf.Answer, sf.RequestID)
	}
	if len(sf.Images) != 2 || sf.Images[0].URL != "https://img/1" ||
		sf.Images[0].Description != "gopher" || sf.Images[1].URL != "https://img/2" {
		t.Errorf("images = %+v", sf.Images)
	}

	// Extract：urls 必传 + query 可选；成功/失败双列表解析
	extCond := search.NewTavilyExtract("https://a", "https://b")
	extCond.Query = "q"
	ext, err := repo.Extract(ctx, extCond)
	if err != nil {
		t.Fatal(err)
	}
	if cap.path != "/extract" || cap.body["query"] != "q" {
		t.Errorf("extract request = %s %v", cap.path, cap.body)
	}
	if urls, _ := cap.body["urls"].([]any); len(urls) != 2 {
		t.Errorf("extract urls = %v", cap.body["urls"])
	}
	if len(ext.Results) != 1 || ext.Results[0].RawContent != "RA" || len(ext.Results[0].Images) != 1 {
		t.Errorf("extract results = %+v", ext.Results)
	}
	if len(ext.FailedResults) != 1 || ext.FailedResults[0].URL != "https://b" ||
		ext.FailedResults[0].Error != "timeout" {
		t.Errorf("failed results = %+v", ext.FailedResults)
	}

	// Crawl：NewTavilyCrawl 缺省 1/20/50/true 全量发送
	cr, err := repo.Crawl(ctx, search.NewTavilyCrawl("https://site"))
	if err != nil {
		t.Fatal(err)
	}
	if cap.path != "/crawl" || cap.body["max_depth"] != float64(1) ||
		cap.body["max_breadth"] != float64(20) || cap.body["limit"] != float64(50) ||
		cap.body["allow_external"] != true {
		t.Errorf("crawl request body = %v", cap.body)
	}
	if cr.BaseURL != "https://site" || len(cr.Results) != 1 || cr.Results[0].RawContent != "C1" {
		t.Errorf("crawl resp = %+v", cr)
	}

	// Map：结果为 URL 字符串列表
	mr, err := repo.Map(ctx, search.NewTavilyMap("https://site"))
	if err != nil {
		t.Fatal(err)
	}
	if cap.path != "/map" || len(mr.Results) != 2 || mr.Results[1] != "https://site/2" {
		t.Errorf("map = %s %+v", cap.path, mr.Results)
	}
}

// ---- Bocha ----

func TestRagSearchBocha(t *testing.T) {
	ctx := context.Background()
	ts, cap := newCaptureServer(t, `{"code":200,"data":{"webPages":{"value":[
	  {"snippet":"Go 是静态语言","title":"Go 百科","url":"https://go.dev"},
	  {"snippet":"Go wiki","title":"Wiki","url":"https://wiki/go"}]}}}`)

	repo := search.NewBocha(search.WithAPIURL(ts.URL), search.WithAPIKey("bk"))
	q := rag.NewQuery("Go 语言")
	q.Limit = 2
	q.Freshness = rag.LastDay()
	docs, err := repo.SearchQuery(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	// 请求形状：Bearer + query/count（字符串）/freshness
	if got := cap.header.Get("Authorization"); got != "Bearer bk" {
		t.Errorf("Authorization = %q", got)
	}
	if cap.body["query"] != "Go 语言" || cap.body["count"] != "2" {
		t.Errorf("body = %v", cap.body)
	}
	if cap.body["freshness"] != "oneDay" { // LastDay 时间窗 → oneDay
		t.Errorf("freshness = %v", cap.body["freshness"])
	}
	// 命中映射：snippet 为内容
	if len(docs) != 2 || docs[0].Content != "Go 是静态语言" ||
		docs[0].Title() != "Go 百科" || docs[0].URL() != "https://go.dev" {
		t.Fatalf("docs = %v", docs)
	}

	// code != 200 → 携带原始响应报错
	bad, _ := newCaptureServer(t, `{"code":429,"message":"rate limited"}`)
	badRepo := search.NewBocha(search.WithAPIURL(bad.URL), search.WithAPIKey("bk"))
	if _, err := badRepo.Search(ctx, "x"); err == nil || !strings.Contains(err.Error(), "429") {
		t.Errorf("bocha error = %v", err)
	}
}

// ---- Baidu ----

const baiduRefsResp = `{
  "references": [
    {"content":"Go 是一门语言","title":"Go 百科","url":"https://go.dev",
     "id":"1","date":"2026-10-01","type":"web","web_anchor":"锚","icon":"https://icon"},
    {"content":"Go wiki 页面","title":"Wiki","url":"https://wiki/go"}
  ]
}`

func TestRagSearchBaiduBasic(t *testing.T) {
	ctx := context.Background()
	ts, cap := newCaptureServer(t, baiduRefsResp)

	repo := search.NewBaidu(search.BaiduSearchBasic,
		search.WithAPIURL(ts.URL), search.WithAPIKey("bd-key"))
	q := rag.NewQuery("Go 语言")
	q.Limit = 3
	docs, err := repo.SearchQuery(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	// 请求形状：X-Appbuilder-Authorization 鉴权 + messages 单轮 + limit 承载于
	// resource_type_filter；基础搜索不带 model
	if got := cap.header.Get("X-Appbuilder-Authorization"); got != "Bearer bd-key" {
		t.Errorf("X-Appbuilder-Authorization = %q", got)
	}
	if got := cap.header.Get("Authorization"); got != "" {
		t.Errorf("plain Authorization should be absent, got %q", got)
	}
	msgs, _ := cap.body["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %v", cap.body["messages"])
	}
	msg := msgs[0].(map[string]any)
	if msg["role"] != "user" || msg["content"] != "Go 语言" {
		t.Errorf("message = %v", msg)
	}
	if _, hasModel := cap.body["model"]; hasModel {
		t.Errorf("basic search must not carry model: %v", cap.body)
	}
	rtf, _ := cap.body["resource_type_filter"].([]any)
	if len(rtf) != 1 {
		t.Fatalf("resource_type_filter = %v", cap.body["resource_type_filter"])
	}
	f0 := rtf[0].(map[string]any)
	if f0["type"] != "web" || f0["top_k"] != float64(3) {
		t.Errorf("filter[0] = %v", f0)
	}

	// 命中映射：id/date/默认 type=web 元数据 + source 标记
	if len(docs) != 2 {
		t.Fatalf("docs = %d", len(docs))
	}
	d0 := docs[0]
	if d0.Content != "Go 是一门语言" || d0.Title() != "Go 百科" || d0.URL() != "https://go.dev" {
		t.Errorf("doc0 = %s", d0)
	}
	if d0.Metadata["id"] != "1" || d0.Metadata["date"] != "2026-10-01" ||
		d0.Metadata["type"] != "web" || d0.Metadata["web_anchor"] != "锚" ||
		d0.Metadata["icon"] != "https://icon" || d0.Metadata["source"] != "baidu_search" {
		t.Errorf("doc0 metadata = %v", d0.Metadata)
	}
	if docs[1].Metadata["type"] != "web" { // 缺 type 默认 web
		t.Errorf("doc1 type = %v", docs[1].Metadata["type"])
	}

	// 空查询不发请求直接空集
	empty, err := repo.Search(ctx, "   ")
	if err != nil || len(empty) != 0 {
		t.Errorf("blank query = %v err=%v", empty, err)
	}
}

func TestRagSearchBaiduAI(t *testing.T) {
	ctx := context.Background()
	ts, cap := newCaptureServer(t, `{
	  "choices":[{"message":{"content":"智能总结：Go 很棒。"}}],
	  "references":[{"content":"参考一","url":"https://r1"}]
	}`)

	repo := search.NewBaidu(search.BaiduSearchAI,
		search.WithAPIURL(ts.URL), search.WithAPIKey("bd")).Model("deepseek-r1")
	docs, err := repo.Search(ctx, "Go")
	if err != nil {
		t.Fatal(err)
	}
	// AI 态请求形状：model/stream/开关组
	if cap.body["model"] != "deepseek-r1" || cap.body["stream"] != false ||
		cap.body["enable_corner_markers"] != true ||
		cap.body["enable_deep_search"] != false || cap.body["enable_followup_queries"] != false {
		t.Errorf("ai body = %v", cap.body)
	}
	// 智能回答居首 + 检索命中随后
	if len(docs) != 2 {
		t.Fatalf("docs = %d, want 2", len(docs))
	}
	if docs[0].Content != "智能总结：Go 很棒。" || docs[0].Title() != "AI 智能回答" ||
		docs[0].Metadata["type"] != "ai_answer" || docs[0].Metadata["source"] != "baidu_ai_search" {
		t.Errorf("ai answer doc = %s", docs[0])
	}
	if docs[1].Content != "参考一" || docs[1].URL() != "https://r1" {
		t.Errorf("ref doc = %s", docs[1])
	}

	// code != 0 错误面
	bad, _ := newCaptureServer(t, `{"code":400100,"message":"invalid api key"}`)
	badRepo := search.NewBaidu(search.BaiduSearchAI, search.WithAPIURL(bad.URL), search.WithAPIKey("bd"))
	if _, err := badRepo.Search(ctx, "x"); err == nil || !strings.Contains(err.Error(), "400100") {
		t.Errorf("baidu error = %v", err)
	}

	// 无任何可解析内容 → 报错（附响应片段）
	dry, _ := newCaptureServer(t, `{"search_variables":{}}`)
	dryRepo := search.NewBaidu(search.BaiduSearchBasic, search.WithAPIURL(dry.URL), search.WithAPIKey("bd"))
	if _, err := dryRepo.Search(ctx, "x"); err == nil || !strings.Contains(err.Error(), "no content found") {
		t.Errorf("empty response err = %v", err)
	}
}

// ---- 嵌入重排 + 只读断言 ----

// 卡片要求：可选嵌入重排覆盖原生得分（tavily 原生 score 0.9 的 banana 命中
// 因与查询正交被阈值过滤，原生 0.1 的 apple 升为 1.0）。内容用 fakeRagEmbedder
// 词表的精确词 apple/banana，保证矢量正交可算。
func TestRagSearchEmbedderRefilter(t *testing.T) {
	ctx := context.Background()
	ts, _ := newCaptureServer(t, `{"results":[
	  {"title":"b","url":"https://b","content":"banana","score":0.9},
	  {"title":"a","url":"https://a","content":"apple","score":0.1}]}`)

	repo := search.NewTavily(search.WithAPIURL(ts.URL), search.WithAPIKey("tk"),
		search.WithEmbedder(&fakeRagEmbedder{batchSize: 10}))
	docs, err := repo.Search(ctx, "apple")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("docs = %d, want 1 (banana 正交被滤): %v", len(docs), docs)
	}
	if docs[0].Content != "apple" {
		t.Errorf("survivor = %s", docs[0])
	}
	if s := docs[0].Score(); s < 0.999 || s > 1.001 {
		t.Errorf("rerank score = %v, want ~1.0 (覆盖原生 0.1)", s)
	}
}

// 卡片要求：三家均为 Repository 只读面，不可断言为 Storer。
func TestRagSearchRepositoriesAreReadOnly(t *testing.T) {
	ts, _ := newCaptureServer(t, `{}`)
	repos := []rag.Repository{
		search.NewTavily(search.WithAPIURL(ts.URL), search.WithAPIKey("k")),
		search.NewBocha(search.WithAPIURL(ts.URL), search.WithAPIKey("k")),
		search.NewBaidu(search.BaiduSearchBasic, search.WithAPIURL(ts.URL), search.WithAPIKey("k")),
	}
	for _, r := range repos {
		if _, ok := r.(rag.Storer); ok {
			t.Errorf("%T must not implement rag.Storer (网络搜索只读)", r)
		}
		if _, ok := r.(rag.Lifecycle); ok {
			t.Errorf("%T must not implement rag.Lifecycle", r)
		}
	}
}
