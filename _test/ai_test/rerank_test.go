// rerank_test.go 重排模型黑盒测试（02 §11 契约；黄金样本搬译 Java
// DashscopeRerankingDialectTest / OpenaiRerankingDialectTest）。usage 不进 Go
// 契约（与 embedding 对齐），Java 用例中 usage 断言面不搬。
package ai_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/rag"
	"github.com/crazy-airhead/aifei-go/ai/rerank"
)

// ---- 方言匹配（搬译 matchedByStandardOrProvider / matchedByNativeUrlPrefix / notMatchedForOtherEndpoints）----

func TestRerankDashscopeMatched(t *testing.T) {
	d := rerank.SelectRerankDialect(&ai.Config{Standard: "dashscope"})
	if d == nil || d.Name() != "dashscope" {
		t.Fatalf("standard=dashscope 应选 dashscope 方言，得 %v", d)
	}
	d = rerank.SelectRerankDialect(&ai.Config{Provider: "dashscope"})
	if d == nil || d.Name() != "dashscope" {
		t.Fatalf("provider=dashscope 应选 dashscope 方言，得 %v", d)
	}

	const nativeURL = "https://dashscope.aliyuncs.com/api/v1/services/rerank/text-rerank/text-rerank"
	// URL 前缀命中属于 Matched 层语义（搬译 matchedByNativeUrlPrefix：standard
	// 为 openai 时方言级 matched 仍真）；Go 选择器多一层 standard 精确命中优先
	// （与 chat/embedding 对齐），故此处断言 Matched 而非 Select。
	d = rerank.SelectRerankDialect(&ai.Config{Standard: "openai", APIURL: nativeURL})
	if d == nil || d.Name() != "openai" {
		t.Fatalf("standard 显式指定应精确命中（Go 约定），得 %v", nameOf(d))
	}

	for _, cfg := range []*ai.Config{
		{Standard: "openai", Provider: "openai", APIURL: "https://api.jina.ai/v1/rerank"},
		{APIURL: "https://dashscope.aliyuncs.com/compatible-mode/v1/rerank"},
	} {
		if d := rerank.SelectRerankDialect(cfg); d == nil || d.Name() != "openai" {
			t.Fatalf("非百炼端点不应匹配 dashscope（%s），得 %v", cfg.APIURL, nameOf(d))
		}
	}
}

func nameOf(d rerank.Dialect) string {
	if d == nil {
		return "<nil>"
	}
	return d.Name()
}

// ---- 请求体黄金断言（搬译 requestUsesNativeInputQueryAndDocuments / emptyDocumentList
// / optionsAreWrittenAtRootLevel / modelKeyOmittedWhenEmpty）----

func TestRerankDashscopeRequestJSON(t *testing.T) {
	d := rerank.SelectRerankDialect(&ai.Config{Standard: "dashscope"})
	cfg := &ai.Config{Model: "gte-rerank"}

	body, err := d.BuildRequestJSON(cfg, map[string]any{}, "杭州天气",
		[]*rag.Document{rag.NewDocument("杭州今天晴"), rag.NewDocument("上海有雨")})
	if err != nil {
		t.Fatal(err)
	}
	var req map[string]any
	json.Unmarshal(body, &req)
	if req["model"] != "gte-rerank" {
		t.Fatalf("model 应为 gte-rerank，得 %v", req["model"])
	}
	input := req["input"].(map[string]any)
	if input["query"] != "杭州天气" {
		t.Fatalf("input.query 错误：%v", input["query"])
	}
	docs := input["documents"].([]any)
	if len(docs) != 2 || docs[0] != "杭州今天晴" || docs[1] != "上海有雨" {
		t.Fatalf("documents 只送正文，得 %v", docs)
	}

	// 选项写在报文根级（非 parameters 下）
	req = map[string]any{}
	body, _ = d.BuildRequestJSON(cfg, map[string]any{"top_n": 3, "return_documents": true}, "杭州天气",
		[]*rag.Document{rag.NewDocument("杭州今天晴")})
	json.Unmarshal(body, &req)
	if req["top_n"] != float64(3) || req["return_documents"] != true {
		t.Fatalf("选项应写根级，得 %v", req)
	}
	if _, has := req["parameters"]; has {
		t.Fatal("不应有 parameters 节点")
	}

	// model 缺省时省略键
	req = map[string]any{}
	body, _ = d.BuildRequestJSON(&ai.Config{}, map[string]any{}, "杭州天气",
		[]*rag.Document{rag.NewDocument("杭州今天晴")})
	json.Unmarshal(body, &req)
	if _, has := req["model"]; has {
		t.Fatal("model 为空时应省略键")
	}

	// 空文档列表仍写 input 形状
	req = map[string]any{}
	body, _ = d.BuildRequestJSON(cfg, map[string]any{}, "杭州天气", nil)
	json.Unmarshal(body, &req)
	input = req["input"].(map[string]any)
	if arr, ok := input["documents"].([]any); !ok || len(arr) != 0 {
		t.Fatalf("空文档列表应为空数组，得 %v", input["documents"])
	}
}

// ---- openai 响应解析（搬译 documentAsObject / documentAsStringOrMissing /
// documentAsArray / missingOrNonArrayResults / errorNode）----

func TestRerankOpenaiParseResponse(t *testing.T) {
	d := rerank.SelectRerankDialect(&ai.Config{Standard: "openai"})

	// 乱序 results → 按 index 归位
	resp, err := d.ParseResponseJSON([]byte(`{"model":"bge-reranker-v2-m3","results":[` +
		`{"index":1,"relevance_score":0.25,"document":{"text":"第二篇"}},` +
		`{"index":0,"relevance_score":0.9,"document":{"text":"第一篇"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Model != "bge-reranker-v2-m3" || len(resp.Results) != 2 {
		t.Fatalf("模型名/条数错误：%+v", resp)
	}
	if resp.Results[0].Index != 0 || resp.Results[0].Text != "第一篇" || resp.Results[0].RelevanceScore != 0.9 {
		t.Fatalf("results[0] 错误：%+v", resp.Results[0])
	}
	if resp.Results[1].Index != 1 || resp.Results[1].Text != "第二篇" {
		t.Fatalf("results[1] 错误：%+v", resp.Results[1])
	}

	// document 为字符串 / 缺失 → 防御性接受
	resp, err = d.ParseResponseJSON([]byte(`{"model":"m","results":[` +
		`{"index":0,"relevance_score":0.5,"document":"纯字符串文档"},` +
		`{"index":1,"relevance_score":0.4}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Results[0].Text != "纯字符串文档" {
		t.Fatalf("字符串 document 应取文本，得 %q", resp.Results[0].Text)
	}
	if resp.Results[1].Text != "" {
		t.Fatalf("document 缺失时文本应为空，得 %q", resp.Results[1].Text)
	}

	// document 为数组：既非对象也非标量，放弃取文本不报错
	resp, err = d.ParseResponseJSON([]byte(`{"model":"m","results":[{"index":0,"relevance_score":0.1,"document":[]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 1 || resp.Results[0].Text != "" {
		t.Fatalf("数组 document 应容忍，得 %+v", resp.Results)
	}

	// results 缺失 / 非数组 → 空集合
	for _, body := range []string{`{"model":"m"}`, `{"model":"m","results":{}}`} {
		resp, err = d.ParseResponseJSON([]byte(body))
		if err != nil || len(resp.Results) != 0 {
			t.Fatalf("%s 应得空集合，得 %+v err=%v", body, resp, err)
		}
	}

	// error 节点：[type] message 形态
	_, err = d.ParseResponseJSON([]byte(`{"model":"m","error":{"message":"model not found","type":"invalid_request_error"}}`))
	if err == nil || err.Error() != "ai: rerank error: [invalid_request_error] model not found" {
		t.Fatalf("错误形态应为 [type] message，得 %v", err)
	}
}

// ---- dashscope 响应解析（搬译 resultsAreSortedByIndex /
// resultsWithoutDocumentTextAreAccepted / errorResponseCarriesMessage /
// emptyResultsMeansNoResults）----

func TestRerankDashscopeParseResponse(t *testing.T) {
	d := rerank.SelectRerankDialect(&ai.Config{Standard: "dashscope"})

	resp, err := d.ParseResponseJSON([]byte(`{"model":"gte-rerank","output":{"results":[` +
		`{"index":1,"document":{"text":"上海有雨"},"relevance_score":0.12},` +
		`{"index":0,"document":{"text":"杭州今天晴"},"relevance_score":0.98}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Results[0].Index != 0 || resp.Results[0].Text != "杭州今天晴" || resp.Results[0].RelevanceScore != 0.98 {
		t.Fatalf("results[0] 错误：%+v", resp.Results[0])
	}
	if resp.Results[1].Index != 1 || resp.Results[1].Text != "上海有雨" || resp.Results[1].RelevanceScore != 0.12 {
		t.Fatalf("results[1] 错误：%+v", resp.Results[1])
	}

	// return_documents=false：只有 index 与分值
	resp, err = d.ParseResponseJSON([]byte(`{"model":"gte-rerank","output":{"results":[{"index":0,"relevance_score":0.5}]}}`))
	if err != nil || len(resp.Results) != 1 || resp.Results[0].Text != "" || resp.Results[0].RelevanceScore != 0.5 {
		t.Fatalf("无 document 回包应容忍：%+v %v", resp, err)
	}

	// 错误形态：message 键判定（原生错误帧带 code/message）
	_, err = d.ParseResponseJSON([]byte(`{"code":"InvalidApiKey","message":"Invalid API-key provided.","request_id":"req-1"}`))
	if err == nil || err.Error() != "ai: rerank error: Invalid API-key provided." {
		t.Fatalf("message 错误形态不符，得 %v", err)
	}

	// 空 results
	resp, err = d.ParseResponseJSON([]byte(`{"model":"gte-rerank","output":{"results":[]}}`))
	if err != nil || len(resp.Results) != 0 {
		t.Fatalf("空 results 应得空集合：%+v %v", resp, err)
	}
}

// ---- 模型全链路（httptest 假服务回放 openai 形态；Rerank 写回 score + 降序）----

func TestRerankModelFlow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"error":{"message":"bad key","type":"auth"}}`)
			return
		}
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if _, has := req["query"]; !has {
			t.Errorf("请求体应有 query 键：%v", req)
		}
		io.WriteString(w, `{"model":"m","results":[`+
			`{"index":0,"relevance_score":0.2,"document":{"text":"d0"}},`+
			`{"index":1,"relevance_score":0.9,"document":{"text":"d1"}}]}`)
	}))
	defer srv.Close()

	m, err := rerank.NewBuilder(srv.URL).APIKey("sk-test").Model("bge-reranker-v2-m3").Build()
	if err != nil {
		t.Fatal(err)
	}

	// rag.Reranker 接口满足面（P4-b 预留接口位的内置实现）
	var _ rag.Reranker = m

	docs := []*rag.Document{rag.NewDocument("d0"), rag.NewDocument("d1")}
	sorted, err := m.Rerank(context.Background(), "q", docs)
	if err != nil {
		t.Fatal(err)
	}
	if len(sorted) != 2 || sorted[0].Content != "d1" || sorted[1].Content != "d0" {
		t.Fatalf("应按得分降序，得 %v,%v", sorted[0].Content, sorted[1].Content)
	}
	if sorted[0].Score() != 0.9 || sorted[1].Score() != 0.2 {
		t.Fatalf("score 写回错误：%v %v", sorted[0].Score(), sorted[1].Score())
	}
	// 原切片顺序不变（Rerank 复制后排序）
	if docs[0].Content != "d0" || docs[0].Score() != 0.2 {
		t.Fatalf("原切片不应被重排：%v", docs[0])
	}

	// 逐请求选项透传（top_n 走根级）
	var seen map[string]any
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&seen)
		io.WriteString(w, `{"model":"m","results":[{"index":0,"relevance_score":1.0}]}`)
	}))
	defer srv2.Close()
	m2, _ := rerank.NewBuilder(srv2.URL).Model("m").Build()
	if _, err := m2.Input("q", []*rag.Document{rag.NewDocument("a")}).TopN(1).Call(context.Background()); err != nil {
		t.Fatal(err)
	}
	if seen["top_n"] != float64(1) {
		t.Fatalf("top_n 应透传到根级，得 %v", seen)
	}

	// 条数不等的防御（Java 直接 get(i) 越界，Go 收敛为显式错误）
	m3, _ := rerank.NewBuilder(srv2.URL).Model("m").Build()
	_, err = m3.Rerank(context.Background(), "q", []*rag.Document{rag.NewDocument("a"), rag.NewDocument("b")})
	if err == nil {
		t.Fatal("results 条数与文档数不等应报错")
	}
}
