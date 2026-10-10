// talent_web_test.go P5-t4 web 能力包黑盒测试：webfetch 三格式转换与请求头
// （Markdown 尽力转换/纯文本抽取/原文透传）、图片 data URI 与 svg 例外、
// Cloudflare 403+challenge 换 UA 穿透、错误面（URL 校验/状态码/5MB 上限）、
// 传输错误重试、超时；websearch 委托 rag 仓储（参数透传/重试/超时文案映射/
// 空结果兜底/编号列表格式）；管线端到端。
package ai_test

import (
	"encoding/json"
	"io"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/rag"
	"github.com/crazy-airhead/aifei-go/ai/talent/web"
)

// ---- webfetch ----

const webPageHTML = `<html><head><title>T</title><script>evil()</script></head><body>` +
	`<h1>标题</h1><p>你好 <strong><em>世界</em></strong>，<a href="/x">链接</a>。</p>` +
	`<ul><li>甲</li><li>乙</li></ul><pre><code>x := 1</code></pre>` +
	`<img src="i.png" alt="图"></body></html>`

func TestWebFetchFormats(t *testing.T) {
	var gotUA, gotAccept, gotLang string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA, gotAccept, gotLang = r.Header.Get("User-Agent"), r.Header.Get("Accept"), r.Header.Get("Accept-Language")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(webPageHTML))
	}))
	defer srv.Close()
	f := web.NewFetch()
	tool := f.Tools(nil)[0]

	// markdown（缺省）：标签语义转 Markdown，script 剥除
	res := callFileTool(t, tool, map[string]any{"url": srv.URL})
	if res.IsError {
		t.Fatalf("markdown fetch = %v", res.ContentText())
	}
	md := res.ContentText()
	for _, want := range []string{"# 标题", "***世界***", "[链接](/x)", "- 甲", "- 乙", "```", "x := 1", "![图](i.png)"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "evil") {
		t.Errorf("script content leaked:\n%s", md)
	}
	if !strings.Contains(gotAccept, "text/markdown") || gotUA == "" || !strings.Contains(gotLang, "en-US") {
		t.Fatalf("headers: accept=%q ua=%q lang=%q", gotAccept, gotUA, gotLang)
	}

	// text：剥全部标签折叠空白
	res = callFileTool(t, tool, map[string]any{"url": srv.URL, "format": "text"})
	if res.IsError {
		t.Fatalf("text fetch = %v", res.ContentText())
	}
	txt := res.ContentText()
	if strings.Contains(txt, "<") || strings.Contains(txt, "evil") {
		t.Fatalf("text extraction has tags/script:\n%s", txt)
	}
	if !strings.Contains(txt, "你好 世界") || !strings.Contains(txt, "标题") {
		t.Fatalf("text missing content:\n%s", txt)
	}

	// html：原文透传
	res = callFileTool(t, tool, map[string]any{"url": srv.URL, "format": "html"})
	if res.IsError || !strings.Contains(res.ContentText(), "<h1>标题</h1>") {
		t.Fatalf("html passthrough = %q err=%v", res.ContentText(), res.IsError)
	}
}

func TestWebFetchImage(t *testing.T) {
	pngBytes := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 1, 2, 3}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/svg" {
			w.Header().Set("Content-Type", "image/svg+xml")
			w.Write([]byte("<svg></svg>"))
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(pngBytes)
	}))
	defer srv.Close()
	tool := web.NewFetch().Tools(nil)[0]

	res := callFileTool(t, tool, map[string]any{"url": srv.URL})
	if res.IsError {
		t.Fatalf("image fetch = %v", res.ContentText())
	}
	if !strings.HasPrefix(res.ContentText(), "data:image/png;base64,") {
		t.Fatalf("data uri prefix missing: %q", clip(res.ContentText()))
	}
	b64 := strings.TrimPrefix(res.ContentText(), "data:image/png;base64,")
	if dec, err := base64.StdEncoding.DecodeString(b64); err != nil || string(dec) != string(pngBytes) {
		t.Fatalf("base64 roundtrip failed: %v", err)
	}

	// svg 例外：不走 data URI，原文透传
	res = callFileTool(t, tool, map[string]any{"url": srv.URL + "/svg"})
	if res.IsError || res.ContentText() != "<svg></svg>" {
		t.Fatalf("svg = %q err=%v", res.ContentText(), res.IsError)
	}
}

// Cloudflare 盾穿透：403 + cf-mitigated: challenge → 换 UA "opencode" 重试。
func TestWebFetchCloudflare(t *testing.T) {
	var secondUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "opencode" {
			secondUA = "opencode"
			w.Write([]byte("passed"))
			return
		}
		w.Header().Set("cf-mitigated", "challenge")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("blocked"))
	}))
	defer srv.Close()

	res := callFileTool(t, web.NewFetch().Tools(nil)[0], map[string]any{"url": srv.URL})
	if res.IsError || res.ContentText() != "passed" {
		t.Fatalf("cf bypass = %q err=%v", res.ContentText(), res.IsError)
	}
	if secondUA != "opencode" {
		t.Fatal("retry did not switch UA to opencode")
	}
}

func TestWebFetchErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/missing":
			http.NotFound(w, r)
		case "/huge":
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte(strings.Repeat("a", 5*1024*1024+1)))
		}
	}))
	defer srv.Close()
	tool := web.NewFetch().Tools(nil)[0]

	// URL 校验
	res := callFileTool(t, tool, map[string]any{"url": "ftp://example.com/x"})
	if !res.IsError || res.ContentText() != "Error: URL must start with http:// or https://" {
		t.Fatalf("url validation = %q err=%v", res.ContentText(), res.IsError)
	}
	// 状态码
	res = callFileTool(t, tool, map[string]any{"url": srv.URL + "/missing"})
	if !res.IsError || res.ContentText() != "Error: Request failed with status code: 404" {
		t.Fatalf("404 = %q err=%v", res.ContentText(), res.IsError)
	}
	// 5MB 上限
	res = callFileTool(t, tool, map[string]any{"url": srv.URL + "/huge"})
	if !res.IsError || res.ContentText() != "Error: Response too large (exceeds 5MB limit)" {
		t.Fatalf("oversize = %q err=%v", clip(res.ContentText()), res.IsError)
	}
}

// 传输错误重试：前两次连接中断，第三次成功。
func TestWebFetchRetry(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) <= 2 {
			panic(http.ErrAbortHandler) // 断连：不出响应
		}
		w.Write([]byte("third-time"))
	}))
	defer srv.Close()

	res := callFileTool(t, web.NewFetch().Tools(nil)[0], map[string]any{"url": srv.URL})
	if res.IsError || res.ContentText() != "third-time" {
		t.Fatalf("retry = %q err=%v", res.ContentText(), res.IsError)
	}
	if n := atomic.LoadInt32(&calls); n != 3 {
		t.Fatalf("attempts = %d, want 3", n)
	}
}

func TestWebFetchTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(300 * time.Millisecond):
		}
		w.Write([]byte("late"))
	}))
	defer srv.Close()

	res := callFileTool(t, web.NewFetch().RetryConfig(1).Tools(nil)[0],
		map[string]any{"url": srv.URL, "timeoutMs": 100})
	if !res.IsError || !strings.Contains(res.ContentText(), "exceeded") {
		t.Fatalf("timeout = %q err=%v", res.ContentText(), res.IsError)
	}
}

// ---- websearch ----

// stubSearchRepo rag.Repository 桩（记录捕获的查询；前 failFirst 次报错）。
type stubSearchRepo struct {
	docs      []*rag.Document
	err       error
	failFirst int
	calls     int
	captured  *rag.Query
}

func (s *stubSearchRepo) Search(context.Context, string) ([]*rag.Document, error) {
	return nil, fmt.Errorf("unused")
}

func (s *stubSearchRepo) SearchQuery(_ context.Context, q *rag.Query) ([]*rag.Document, error) {
	s.calls++
	s.captured = q
	if s.calls <= s.failFirst {
		return nil, s.err
	}
	return s.docs, nil
}

func TestWebSearch(t *testing.T) {
	repo := &stubSearchRepo{docs: []*rag.Document{
		rag.NewDocument("第一条约谈内容").Meta("title", "结果一").Meta("url", "https://a.example/1"),
		rag.NewDocument("第二条内容").Meta("title", "结果二").Meta("url", "https://b.example/2"),
	}}
	s := web.NewSearch(repo)
	tool := s.Tools(nil)[0]

	res := callFileTool(t, tool, map[string]any{"query": "测试"})
	if res.IsError {
		t.Fatalf("websearch = %v", res.ContentText())
	}
	out := res.ContentText()
	for _, want := range []string{"1. [结果一](https://a.example/1)", "   第一条约谈内容", "2. [结果二](https://b.example/2)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if repo.captured == nil || repo.captured.Query != "测试" {
		t.Fatalf("query not passed: %+v", repo.captured)
	}

	// numResults 透传（缺省 8）
	callFileTool(t, tool, map[string]any{"query": "q", "numResults": 3})
	if repo.captured.Limit != 3 {
		t.Fatalf("numResults = %d, want 3", repo.captured.Limit)
	}
	callFileTool(t, tool, map[string]any{"query": "q"})
	if repo.captured.Limit != 8 {
		t.Fatalf("default numResults = %d, want 8", repo.captured.Limit)
	}

	// 空结果兜底文案
	empty := web.NewSearch(&stubSearchRepo{})
	res = callFileTool(t, empty.Tools(nil)[0], map[string]any{"query": "空"})
	if res.IsError || res.ContentText() != "No search results found. Please try a different query." {
		t.Fatalf("empty = %q err=%v", res.ContentText(), res.IsError)
	}

	// 重试：前两次超时类错误 → 第三次成功
	flaky := &stubSearchRepo{failFirst: 2, err: fmt.Errorf("dial tcp: i/o timeout"),
		docs: repo.docs}
	res = callFileTool(t, web.NewSearch(flaky).Tools(nil)[0], map[string]any{"query": "q"})
	if res.IsError || !strings.Contains(res.ContentText(), "结果一") {
		t.Fatalf("retry = %q err=%v", res.ContentText(), res.IsError)
	}
	if flaky.calls != 3 {
		t.Fatalf("retry calls = %d, want 3", flaky.calls)
	}

	// 重试耗尽 + 超时文案映射
	hopeless := &stubSearchRepo{failFirst: 99, err: fmt.Errorf("request Timeout")}
	res = callFileTool(t, web.NewSearch(hopeless).Tools(nil)[0], map[string]any{"query": "q"})
	if !res.IsError || res.ContentText() != "Error: Search request timed out" {
		t.Fatalf("timeout mapping = %q err=%v", res.ContentText(), res.IsError)
	}
}

// ---- 管线端到端 ----

func TestWebTalentPipeline(t *testing.T) {
	f := newFakeOpenAI(t, respTextJSON)
	m := fakeModel(t, f)

	_, err := m.Prompt("搜一下").Options(func(o *ai.ChatOptions) {
		o.Talents = []ai.Talent{web.NewFetch(), web.NewSearch(&stubSearchRepo{})}
	}).Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	body := f.request(t, 0).body
	tools, _ := body["tools"].([]any)
	got := map[string]bool{}
	for _, tv := range tools {
		tm, _ := tv.(map[string]any)
		got[tm["function"].(map[string]any)["name"].(string)] = true
	}
	if !got["webfetch"] || !got["websearch"] {
		t.Fatalf("tools not deployed: %v", got)
	}

	msgs := f.messagesOf(t, 0)
	sysPrompt, _ := msgs[0]["content"].(string)
	if strings.Contains(sysPrompt, "# [Talent:") {
		t.Fatalf("pure toolsets must not emit instruction blocks:\n%s", sysPrompt)
	}

	opts := &ai.ChatOptions{}
	opts.Talents = []ai.Talent{web.NewFetch(), web.NewSearch(&stubSearchRepo{})}
	ai.ActiveTalents(opts, nil, new(strings.Builder))
	for _, tl := range opts.Tools {
		name, _ := tl.Meta()["talent"].(string)
		if name != "webfetch" && name != "websearch" {
			t.Fatalf("tool %q meta.talent = %q", tl.Name(), name)
		}
	}
}

// ---- crawler（P5 补全件：JINA/Firecrawl 双驱动）----

// stubCrawlerDoer 记录请求并回放响应。
type stubCrawlerDoer struct {
	status  int
	body    string
	lastReq *http.Request
	lastBody []byte
}

func (d *stubCrawlerDoer) Do(req *http.Request) (*http.Response, error) {
	d.lastReq = req
	if req.Body != nil {
		d.lastBody, _ = io.ReadAll(req.Body)
		req.Body.Close()
	}
	return &http.Response{
		StatusCode: d.status, Body: io.NopCloser(strings.NewReader(d.body)),
		Header: http.Header{"Content-Type": []string{"text/markdown"}},
	}, nil
}

func TestTalentWebCrawlerJina(t *testing.T) {
	doer := &stubCrawlerDoer{status: 200, body: "# 页面标题\n\n正文内容"}
	c := web.NewCrawler(web.NewJinaCrawlerDriver(doer)).APIKey("jk")

	tools := c.Tools(nil)
	if len(tools) != 1 || tools[0].Name() != "crawl_url" {
		t.Fatalf("工具：%v", tools)
	}
	if c.Name() != "Crawler" || c.Description() == "" {
		t.Fatalf("能力包元数据：%s %s", c.Name(), c.Description())
	}

	out := callCrawlerTool(t, tools[0], "https://example.com/x")
	if out != "# 页面标题\n\n正文内容" {
		t.Fatalf("输出：%q", out)
	}
	// 请求形态：GET r.jina.ai/{url} + markdown 头 + Bearer
	if doer.lastReq.Method != "GET" || doer.lastReq.URL.String() != "https://r.jina.ai/https://example.com/x" {
		t.Fatalf("JINA 请求：%s %s", doer.lastReq.Method, doer.lastReq.URL)
	}
	if doer.lastReq.Header.Get("X-Return-Format") != "markdown" ||
		doer.lastReq.Header.Get("Authorization") != "Bearer jk" {
		t.Fatalf("JINA 头：%v", doer.lastReq.Header)
	}

	// 无效 URL
	if out := callCrawlerTool(t, tools[0], "ftp://x"); out != "错误：URL 无效。" {
		t.Fatalf("无效 URL：%q", out)
	}
	if out := callCrawlerTool(t, tools[0], ""); out != "错误：URL 无效。" {
		t.Fatalf("空 URL：%q", out)
	}
}

func TestTalentWebCrawlerFirecrawl(t *testing.T) {
	doer := &stubCrawlerDoer{status: 200,
		body: `{"success":true,"data":{"markdown":"# MD 内容"}}`}
	c := web.NewCrawler(web.NewFirecrawlCrawlerDriver(doer)).APIKey("fk")
	tools := c.Tools(nil)

	out := callCrawlerTool(t, tools[0], "https://example.com")
	if out != "# MD 内容" {
		t.Fatalf("输出：%q", out)
	}
	// 请求形态：POST /v1/scrape + body 形状
	if doer.lastReq.Method != "POST" || doer.lastReq.URL.String() != "https://api.firecrawl.dev/v1/scrape" {
		t.Fatalf("Firecrawl 请求：%s %s", doer.lastReq.Method, doer.lastReq.URL)
	}
	if doer.lastReq.Header.Get("Authorization") != "Bearer fk" {
		t.Fatalf("鉴权头：%v", doer.lastReq.Header)
	}
	var body map[string]any
	json.Unmarshal(doer.lastBody, &body)
	if body["url"] != "https://example.com" || body["onlyMainContent"] != true {
		t.Fatalf("body：%v", body)
	}
	if formats := body["formats"].([]any); len(formats) != 1 || formats[0] != "markdown" {
		t.Fatalf("formats：%v", formats)
	}

	// 失败形态：success=false + error
	doer.body = `{"success":false,"error":"payment required"}`
	if out := callCrawlerTool(t, tools[0], "https://example.com"); out != "抓取失败: web: firecrawl: payment required" {
		t.Fatalf("失败文案：%q", out)
	}
	// 无 success 的错误回退
	doer.body = `{"success":false}`
	if out := callCrawlerTool(t, tools[0], "https://example.com"); !strings.Contains(out, "unsuccessful scrape") {
		t.Fatalf("缺省失败：%q", out)
	}
}

func callCrawlerTool(t *testing.T, tool ai.FunctionTool, url string) string {
	t.Helper()
	args := map[string]any{}
	if url != "" {
		args["url"] = url
	}
	r := tool.Call(context.Background(), args)
	if r == nil {
		t.Fatal("空结果")
	}
	return r.ContentText()
}
