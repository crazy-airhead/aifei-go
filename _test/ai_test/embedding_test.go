// embedding_test.go P1-c 嵌入模型测试：openai（float/base64/index 排序/批量/维度/
// 条数校验）、ollama（/api/embed 匹配 + embeddings）、dashscope（texts/parameters
// 形态 + text_index）、SelectEmbeddingDialect 优先级表。
package ai_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/embedding"
)

// fakeEmbed 嵌入假服务：记录请求体，顺序回放固定响应。
type fakeEmbed struct {
	mu       sync.Mutex
	requests []map[string]any
	srv      *httptest.Server
}

func newFakeEmbed(t *testing.T, bodies ...string) *fakeEmbed {
	t.Helper()
	f := &fakeEmbed{}
	var idx int
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		i := idx
		idx++
		f.requests = append(f.requests, body)
		f.mu.Unlock()

		if i >= len(bodies) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"no more scripted responses"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(bodies[i]))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeEmbed) request(t *testing.T, i int) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.requests) {
		t.Fatalf("嵌入请求 %d 未捕获（共 %d 次）", i, len(f.requests))
	}
	return f.requests[i]
}

func (f *fakeEmbed) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func embedModel(t *testing.T, f *fakeEmbed, standard string) *embedding.Model {
	t.Helper()
	b := embedding.NewBuilder(f.srv.URL).APIKey("sk-test").Model("text-embedding-3-small")
	if standard != "" {
		b.Standard(standard)
	}
	m, err := b.Build()
	if err != nil {
		t.Fatalf("embedding.NewBuilder: %v", err)
	}
	return m
}

// TestEmbeddingOpenAI 单文本 + 批量：请求形态 {model, input, encoding_format}、
// data 乱序按 index 归位、条数校验、模型级默认选项透传。
func TestEmbeddingOpenAI(t *testing.T) {
	f := newFakeEmbed(t,
		`{"model":"text-embedding-3-small","data":[{"index":0,"embedding":[0.1,0.2]}]}`,
		`{"model":"text-embedding-3-small","data":[{"index":2,"embedding":[0.7,0.8]},{"index":0,"embedding":[0.1,0.2]},{"index":1,"embedding":[0.5,0.6]}]}`,
	)
	m := embedModel(t, f, "openai")
	m.Options(map[string]any{"dimensions": 2})

	vec, err := m.Embed(context.Background(), "你好")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != 2 || vec[0] != 0.1 || vec[1] != 0.2 {
		t.Fatalf("vec = %v，应为 [0.1 0.2]", vec)
	}
	req := f.request(t, 0)
	if req["model"] != "text-embedding-3-small" {
		t.Fatalf("model = %v", req["model"])
	}
	input, _ := req["input"].([]any)
	if len(input) != 1 || input[0] != "你好" {
		t.Fatalf("input = %v", req["input"])
	}
	if req["encoding_format"] != "float" {
		t.Fatalf("encoding_format = %v", req["encoding_format"])
	}
	if req["dimensions"] != float64(2) { // 模型级默认选项透传
		t.Fatalf("dimensions = %v", req["dimensions"])
	}

	vecs, err := m.EmbedTexts(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("EmbedTexts: %v", err)
	}
	// data 乱序（2,0,1 返回）按 index 归位后与 texts 下标对齐
	if len(vecs) != 3 || vecs[0][0] != 0.1 || vecs[1][0] != 0.5 || vecs[2][0] != 0.7 {
		t.Fatalf("vecs = %v，乱序 data 应按 index 归位", vecs)
	}

	// 维度探测：以 "test" 文本真实请求一次
	f2 := newFakeEmbed(t, `{"model":"m","data":[{"index":0,"embedding":[0.1,0.2,0.3,0.4]}]}`)
	m2 := embedModel(t, f2, "openai")
	dim, err := m2.Dimensions(context.Background())
	if err != nil || dim != 4 {
		t.Fatalf("Dimensions = %d, %v", dim, err)
	}
	if input, _ := f2.request(t, 0)["input"].([]any); len(input) != 1 || input[0] != "test" {
		t.Fatalf("Dimensions 探测请求 input = %v，应为 [test]", input)
	}

	// 条数不等（对标 Java EmbeddingException）
	f3 := newFakeEmbed(t, `{"model":"m","data":[{"index":0,"embedding":[0.1]}]}`)
	m3 := embedModel(t, f3, "openai")
	if _, err := m3.EmbedTexts(context.Background(), []string{"a", "b"}); err == nil {
		t.Fatalf("条数不等应报错")
	}
}

// TestEmbeddingOpenAIBase64 base64 向量按 little-endian float32 解码。
func TestEmbeddingOpenAIBase64(t *testing.T) {
	// [1.0, -2.0] → little-endian float32 字节序列 → base64
	encoded := base64.StdEncoding.EncodeToString([]byte{0, 0, 0x80, 0x3F, 0, 0, 0, 0xC0})
	f := newFakeEmbed(t, `{"model":"m","data":[{"index":0,"embedding":"`+encoded+`"}]}`)
	m := embedModel(t, f, "openai")
	vec, err := m.Embed(context.Background(), "x")
	if err != nil {
		t.Fatalf("Embed(base64): %v", err)
	}
	if len(vec) != 2 || math.Abs(float64(vec[0])-1.0) > 1e-6 || math.Abs(float64(vec[1])+2.0) > 1e-6 {
		t.Fatalf("base64 解码 = %v，应为 [1, -2]", vec)
	}
}

// TestEmbeddingOllama /api/embed 端点匹配（standard 空时 URL 后缀判定）+
// embeddings 数组按返回序（无 index 不排序）+ error 键。
func TestEmbeddingOllama(t *testing.T) {
	f := newFakeEmbed(t,
		`{"model":"nomic-embed-text","embeddings":[[0.1,0.2],[0.3,0.4]],"prompt_eval_count":12}`,
		`{"error":"model not found"}`,
	)
	// 不给 standard：靠 URL 以 /api/embed 结尾命中 ollama 方言
	b := embedding.NewBuilder(f.srv.URL + "/api/embed").Model("nomic-embed-text")
	m, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if m.Dialect().Name() != "ollama" {
		t.Fatalf("dialect = %q，应为 ollama（URL 后缀匹配）", m.Dialect().Name())
	}
	vecs, err := m.EmbedTexts(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("EmbedTexts: %v", err)
	}
	if len(vecs) != 2 || vecs[0][0] != 0.1 || vecs[1][0] != 0.3 {
		t.Fatalf("vecs = %v", vecs)
	}
	req := f.request(t, 0)
	if req["encoding_format"] != "float" { // 抽象形态共用（Java 同款）
		t.Fatalf("encoding_format = %v", req["encoding_format"])
	}
	if _, err := m.Embed(context.Background(), "x"); err == nil || err.Error() == "" {
		t.Fatalf("error 键应转为错误")
	}
}

// TestEmbeddingDashscope 百炼形态：请求 {model, input.texts, parameters}、响应
// output.embeddings 按 text_index 归位、{code,message} 错误。
func TestEmbeddingDashscope(t *testing.T) {
	f := newFakeEmbed(t,
		`{"output":{"embeddings":[{"text_index":1,"embedding":[0.5,0.6]},{"text_index":0,"embedding":[0.1,0.2]}]},"usage":{"total_tokens":8},"request_id":"r1"}`,
		`{"code":"InvalidParameter","message":"texts is empty","request_id":"r2"}`,
	)
	m := embedModel(t, f, "dashscope") // standard 精确匹配
	if m.Dialect().Name() != "dashscope" {
		t.Fatalf("dialect = %q", m.Dialect().Name())
	}
	vecs, err := m.EmbedTextsWith(context.Background(), []string{"a", "b"}, map[string]any{"text_type": "query"})
	if err != nil {
		t.Fatalf("EmbedTextsWith: %v", err)
	}
	if len(vecs) != 2 || vecs[0][0] != 0.1 { // text_index 排序
		t.Fatalf("vecs = %v", vecs)
	}
	req := f.request(t, 0)
	input, _ := req["input"].(map[string]any)
	texts, _ := input["texts"].([]any)
	if len(texts) != 2 || texts[0] != "a" {
		t.Fatalf("input.texts = %v", input)
	}
	params, _ := req["parameters"].(map[string]any)
	if params["text_type"] != "query" { // 逐请求选项进 parameters
		t.Fatalf("parameters = %v", params)
	}
	if _, has := req["encoding_format"]; has { // 百炼形态不带抽象字段
		t.Fatalf("encoding_format 不应出现：%v", req)
	}
	if _, err := m.Embed(context.Background(), "x"); err == nil {
		t.Fatalf("code/message 错误形态应报错")
	}
}

// TestSelectEmbeddingDialect 表驱动：standard 精确 > 特征匹配 > openai 补位。
func TestSelectEmbeddingDialect(t *testing.T) {
	cases := []struct {
		name string
		cfg  *ai.Config
		want string
	}{
		{"standard openai", &ai.Config{Standard: "openai"}, "openai"},
		{"standard ollama", &ai.Config{Standard: "ollama"}, "ollama"},
		{"standard dashscope", &ai.Config{Standard: "dashscope"}, "dashscope"},
		{"provider ollama", &ai.Config{Provider: "ollama"}, "ollama"},
		{"provider dashscope", &ai.Config{Provider: "dashscope"}, "dashscope"},
		{"URL /api/embed", &ai.Config{APIURL: "http://localhost:11434/api/embed"}, "ollama"},
		{"URL 百炼前缀", &ai.Config{APIURL: embedding.URLPrefix + "embeddings/text-embedding/text-embedding-v3"}, "dashscope"},
		{"空配置补位", &ai.Config{}, "openai"},
		{"未知 standard 补位", &ai.Config{Standard: "not-registered", APIURL: "https://x.example/v1/embeddings"}, "openai"},
	}
	for _, c := range cases {
		d := embedding.SelectEmbeddingDialect(c.cfg)
		if d == nil || d.Name() != c.want {
			t.Fatalf("%s：SelectEmbeddingDialect = %v，want %s", c.name, d, c.want)
		}
	}
}

// TestEmbeddingValidation apiUrl/model 必填；BatchSize 缺省 10、非正值守卫。
func TestEmbeddingValidation(t *testing.T) {
	if _, err := embedding.New(ai.Config{Model: "m"}); err == nil {
		t.Fatalf("缺 apiUrl 应报错")
	}
	if _, err := embedding.New(ai.Config{APIURL: "https://x.example"}); err == nil {
		t.Fatalf("缺 model 应报错")
	}
	f := newFakeEmbed(t)
	m := embedModel(t, f, "openai")
	if m.BatchSize() != 10 {
		t.Fatalf("BatchSize = %d，缺省应为 10", m.BatchSize())
	}
	m2, err := embedding.NewBuilder(f.srv.URL).Model("m").Standard("openai").BatchSize(0).BatchSize(32).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if m2.BatchSize() != 32 { // 非正值不生效（0 被忽略，32 生效）
		t.Fatalf("BatchSize = %d，应为 32", m2.BatchSize())
	}
}
