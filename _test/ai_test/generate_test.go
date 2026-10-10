// generate_test.go 生成模型黑盒测试（02 §11 契约；黄金样本搬译 Java
// OpenaiGenerateDialectTest / OllamaGenerateDialectTest / DashscopeGenerateDialectTest）。
// usage 不进 Go 契约（与 embedding/rerank 对齐），Java 用例中 usage 断言面不搬。
package ai_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/generate"
)

const (
	genTaskURL   = "https://api.example.com/v1/tasks/"
	genNativeURL = "https://dashscope.aliyuncs.com/api/v1/services/aigc/text2image/image-synthesis"
)

func genCfg() *generate.Config {
	return &generate.Config{ai.Config{Model: "gpt-image-1", APIURL: "https://api.example.com/v1"}, genTaskURL}
}

// ---- openai 方言（搬译 异步 task_id / 官方 Images / 兼容端点 / 无内容 / 畸形 / error 形态）----

func TestGenerateOpenaiParseResponse(t *testing.T) {
	d := generate.SelectGenerateDialect(&generate.Config{ai.Config{Standard: "openai"}, ""})

	// 异步：task_id + taskUrl 裸拼接为 url
	resp, err := d.ParseResponseJSON(genCfg(), []byte(`{"model":"gpt-image-1","task_id":"task_9527"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !resp.HasData() || resp.Data[0].URL != genTaskURL+"task_9527" {
		t.Fatalf("异步应得任务查询 url，得 %+v", resp.Data)
	}

	// 官方 Images：b64_json→Data、revised_prompt→Text、output_format→mime 兜底、model 回退
	resp, err = d.ParseResponseJSON(genCfg(), []byte(`{"created":1710000000,"background":"transparent",`+
		`"output_format":"png","data":[{"b64_json":"QUJD","revised_prompt":"a revised prompt"},`+
		`{"url":"https://cdn.example.com/a.png"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Model != "gpt-image-1" {
		t.Fatalf("model 应回退请求配置，得 %q", resp.Model)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("应有 2 条内容，得 %d", len(resp.Data))
	}
	if resp.Data[0].Data != "QUJD" || resp.Data[0].Text != "a revised prompt" || resp.Data[0].MimeType != "image/png" {
		t.Fatalf("data[0] 错误：%+v", resp.Data[0])
	}
	if resp.Data[1].URL != "https://cdn.example.com/a.png" {
		t.Fatalf("data[1].url 错误：%+v", resp.Data[1])
	}

	// 兼容端点：data 键承载 b64、mimeType 显式；Value 优先级 url > data > text
	resp, err = d.ParseResponseJSON(genCfg(), []byte(`{"model":"gpt-image-1","data":[`+
		`{"url":"https://x/a.png","mimeType":"image/png"},`+
		`{"data":"QUJD","mimeType":"image/jpeg"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Data[0].Value() != "https://x/a.png" || resp.Data[1].Value() != "QUJD" {
		t.Fatalf("Value 优先级错误：%v %v", resp.Data[0].Value(), resp.Data[1].Value())
	}

	// 无内容
	resp, err = d.ParseResponseJSON(genCfg(), []byte(`{"model":"gpt-image-1"}`))
	if err != nil || resp.HasData() || resp.Content() != nil {
		t.Fatalf("无内容应空集，得 %+v %v", resp, err)
	}

	// 畸形防御：data 非数组 / task_id 空串 + data null
	for _, body := range []string{`{"data":{},"model":"gpt-image-1"}`, `{"task_id":"","data":null,"model":"gpt-image-1"}`} {
		resp, err = d.ParseResponseJSON(genCfg(), []byte(body))
		if err != nil || resp.HasData() {
			t.Fatalf("畸形防御失败（%s）：%+v %v", body, resp, err)
		}
	}
	// task_id 有但 taskUrl 未配置 → 不产生拼接 url，落回 data 解析（空）
	resp, err = d.ParseResponseJSON(&generate.Config{ai.Config{Model: "gpt-image-1", APIURL: "https://x"}, ""},
		[]byte(`{"task_id":"task_1","data":null,"model":"gpt-image-1"}`))
	if err != nil || resp.HasData() {
		t.Fatalf("taskUrl 未配置不应产生拼接 url：%+v %v", resp, err)
	}

	// error:null 非错误
	resp, err = d.ParseResponseJSON(genCfg(), []byte(`{"error":null,"data":[]}`))
	if err != nil || resp.HasData() {
		t.Fatalf("error:null 不应判错：%v %v", resp, err)
	}

	// error 带 code → [code] message
	_, err = d.ParseResponseJSON(genCfg(), []byte(`{"error":{"message":"content policy violation","code":"content_filter"}}`))
	if err == nil || err.Error() != "ai: generate error: [content_filter] content policy violation" {
		t.Fatalf("错误形态不符，得 %v", err)
	}
}

// ---- ollama 方言（搬译 matched / 请求形态 / 错误 / 文本 / 图像）----

func TestGenerateOllama(t *testing.T) {
	d := generate.SelectGenerateDialect(&generate.Config{ai.Config{Standard: "ollama"}, ""})
	if d == nil || d.Name() != "ollama" {
		t.Fatalf("standard=ollama 应选 ollama 方言，得 %v", d)
	}
	// "OLLAMA" 不命中（区分大小写）
	if d2 := generate.SelectGenerateDialect(&generate.Config{ai.Config{Standard: "OLLAMA"}, ""}); d2 == nil || d2.Name() != "openai" {
		t.Fatalf("OLLAMA 大写不应命中（区分大小写），得 %v", d2)
	}
	if d3 := generate.SelectGenerateDialect(&generate.Config{ai.Config{Provider: "ollama"}, ""}); d3.Name() != "ollama" {
		t.Fatalf("provider=ollama 应回落命中，得 %v", d3)
	}

	// 请求-str：options 平铺根级
	cfg := &generate.Config{ai.Config{Model: "qwen3:8b", APIURL: "http://localhost:11434/api/generate"}, ""}
	body, err := d.BuildRequestJSON(cfg, map[string]any{"size": "1024x1024"}, "画一只猫", nil)
	if err != nil {
		t.Fatal(err)
	}
	var req map[string]any
	json.Unmarshal(body, &req)
	if req["model"] != "qwen3:8b" || req["prompt"] != "画一只猫" || req["size"] != "1024x1024" {
		t.Fatalf("扁平请求形态错误：%v", req)
	}

	// 请求-map：model 省略、promptMap 平铺根级
	req = map[string]any{}
	body, _ = d.BuildRequestJSON(&generate.Config{}, map[string]any{"stream": false},
		"", map[string]any{"prompt": "p", "negative_prompt": "n"})
	json.Unmarshal(body, &req)
	if _, has := req["model"]; has {
		t.Fatal("model 空应省略键")
	}
	if req["prompt"] != "p" || req["negative_prompt"] != "n" || req["stream"] != false {
		t.Fatalf("promptMap 应平铺根级：%v", req)
	}

	// 错误：error 键存在即错
	_, err = d.ParseResponseJSON(cfg, []byte(`{"error":"model 'qwen3:8b' not found"}`))
	if err == nil || !contains(err.Error(), "not found") {
		t.Fatalf("error 键应判错，得 %v", err)
	}

	// 文本：response 单条
	resp, err := d.ParseResponseJSON(cfg, []byte(`{"model":"qwen3:8b","response":"从前有座山","done":true,"prompt_eval_count":12}`))
	if err != nil {
		t.Fatal(err)
	}
	if !resp.HasData() || resp.Content().Text != "从前有座山" || resp.Content().Value() != "从前有座山" {
		t.Fatalf("文本形态错误：%+v", resp)
	}

	// 图像：data 数组按 Content 字段名反序列化
	resp, err = d.ParseResponseJSON(cfg, []byte(`{"model":"qwen-image","data":[`+
		`{"url":"https://x/a.png","mimeType":"image/png"},`+
		`{"data":"QUJD","mimeType":"image/jpeg"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Data[0].URL != "https://x/a.png" || resp.Data[0].MimeType != "image/png" {
		t.Fatalf("data[0] 错误：%+v", resp.Data[0])
	}
	if resp.Data[1].Data != "QUJD" || resp.Data[1].Value() != "QUJD" {
		t.Fatalf("data[1] 错误：%+v", resp.Data[1])
	}

	// 无内容帧（prompt_eval_count 无 data）：usage 不进 Go 契约，仅断言不报错
	resp, err = d.ParseResponseJSON(cfg, []byte(`{"model":"qwen3:8b","done":true,"prompt_eval_count":5}`))
	if err != nil || resp.HasData() {
		t.Fatalf("无内容帧应空集：%+v %v", resp, err)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// ---- dashscope 方言（搬译 matched / 三层请求 / 错误 / 异步 / 同步 / 未知形态）----

func TestGenerateDashscope(t *testing.T) {
	d := generate.SelectGenerateDialect(&generate.Config{ai.Config{Standard: "dashscope"}, ""})
	cfg := &generate.Config{ai.Config{Model: "wanx-v1", APIURL: genNativeURL}, genTaskURL}

	// matched：URL 前缀命中（standard=openai 时方言级 matched 仍真；Go 选择器
	// standard 精确命中优先——与 chat/embedding/rerank 对齐，此处断言 Matched）
	if !d.Matched(&generate.Config{ai.Config{Standard: "openai", APIURL: genNativeURL}, ""}) {
		t.Fatal("百炼原生端点方言级应匹配")
	}
	if d.Matched(&generate.Config{ai.Config{APIURL: "https://dashscope.aliyuncs.com/compatible-mode/v1"}, ""}) {
		t.Fatal("compatible-mode 端点不应匹配")
	}

	// 请求-str：三层 model/input/parameters
	body, err := d.BuildRequestJSON(cfg, map[string]any{"size": "1024*1024"}, "一只猫", nil)
	if err != nil {
		t.Fatal(err)
	}
	var req map[string]any
	json.Unmarshal(body, &req)
	if req["model"] != "wanx-v1" {
		t.Fatalf("model 错误：%v", req["model"])
	}
	input := req["input"].(map[string]any)
	if input["prompt"] != "一只猫" {
		t.Fatalf("input.prompt 错误：%v", input)
	}
	params := req["parameters"].(map[string]any)
	if params["size"] != "1024*1024" {
		t.Fatalf("parameters.size 错误：%v", params)
	}

	// 请求-map：input 为整个 map；parameters 空也输出 {}
	req = map[string]any{}
	body, _ = d.BuildRequestJSON(cfg, nil, "", map[string]any{
		"prompt":         "换成夜景",
		"base_image_url": "https://example.com/a.png",
	})
	json.Unmarshal(body, &req)
	input = req["input"].(map[string]any)
	if input["prompt"] != "换成夜景" || input["base_image_url"] != "https://example.com/a.png" {
		t.Fatalf("图生图 input 应为整个 map：%v", input)
	}
	if _, has := req["parameters"]; !has {
		t.Fatal("parameters 恒输出（空也写 {}）")
	}

	// str 优先于 map
	body, _ = d.BuildRequestJSON(cfg, nil, "字符串提示", map[string]any{"prompt": "map提示"})
	json.Unmarshal(body, &req)
	input = req["input"].(map[string]any)
	if input["prompt"] != "字符串提示" {
		t.Fatalf("str 应优先：%v", input)
	}
	if _, has := input["base_image_url"]; has {
		t.Fatal("str 优先时 map 不参与")
	}

	// 双空：无 input/model 键，parameters 仍在
	req = map[string]any{}
	body, _ = d.BuildRequestJSON(&generate.Config{}, nil, "", nil)
	json.Unmarshal(body, &req)
	if _, has := req["input"]; has {
		t.Fatal("双空应无 input 键")
	}
	if _, has := req["model"]; has {
		t.Fatal("model 空应省略键")
	}
	if _, has := req["parameters"]; !has {
		t.Fatal("parameters 恒输出")
	}

	// 错误：code 非空 → "code: message"
	_, err = d.ParseResponseJSON(cfg, []byte(`{"code":"InvalidParameter","message":"size invalid","request_id":"req-1"}`))
	if err == nil || err.Error() != "ai: generate error: InvalidParameter: size invalid" {
		t.Fatalf("错误形态不符，得 %v", err)
	}

	// 空 code 非错误
	resp, err := d.ParseResponseJSON(cfg, []byte(`{"code":"","model":"wanx-v1","output":{"results":[{"url":"https://x/a.png"}]}}`))
	if err != nil || !resp.HasData() || resp.Data[0].URL != "https://x/a.png" {
		t.Fatalf("空 code 应正常解析：%+v %v", resp, err)
	}

	// 异步：task_id → taskURL 裸拼接
	resp, err = d.ParseResponseJSON(cfg, []byte(`{"output":{"task_id":"t-123","task_status":"PENDING"},"request_id":"req-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !resp.HasData() || resp.Data[0].URL != genTaskURL+"t-123" {
		t.Fatalf("异步 url 拼接错误：%+v", resp)
	}

	// 同步：output.results 列表
	resp, err = d.ParseResponseJSON(cfg, []byte(`{"output":{"results":[{"url":"https://x/a.png"},{"url":"https://x/b.png"}]}}`))
	if err != nil || len(resp.Data) != 2 || resp.Data[1].URL != "https://x/b.png" {
		t.Fatalf("同步 results 错误：%+v %v", resp, err)
	}

	// 未知形态：无 task_id/results
	resp, err = d.ParseResponseJSON(cfg, []byte(`{"output":{"task_status":"RUNNING"}}`))
	if err != nil || resp.HasData() {
		t.Fatalf("未知形态应无数据无错误：%+v %v", resp, err)
	}
}

// ---- 模型全链路（httptest 假服务回放 openai 形态 + 模型级默认强类型转换）----

func TestGenerateModelFlow(t *testing.T) {
	var seen map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"error":{"message":"bad key","type":"auth"}}`)
			return
		}
		json.NewDecoder(r.Body).Decode(&seen)
		io.WriteString(w, `{"model":"gpt-image-1","data":[{"b64_json":"QUJD","revised_prompt":"rp"}]}`)
	}))
	defer srv.Close()

	// 模型级默认走配置形态字符串：强类型转换 "1"→int64、"true"→bool（对标
	// GenerateOptions.putAll）
	m, err := generate.NewBuilder(srv.URL).
		APIKey("sk-test").Model("gpt-image-1").TaskURL(genTaskURL).
		Options(map[string]any{"n": "1", "background": "true", "size": "1024x1024"}).
		Build()
	if err != nil {
		t.Fatal(err)
	}

	resp, err := m.Prompt("a cat").Size("512x512").Call(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !resp.HasData() || resp.Content().Data != "QUJD" {
		t.Fatalf("内容解析错误：%+v", resp)
	}
	// 请求级 size 覆盖模型级默认；其余默认生效且已强转
	if seen["size"] != "512x512" {
		t.Fatalf("请求级应覆盖模型级默认 size，得 %v", seen["size"])
	}
	if seen["n"] != float64(1) || seen["background"] != true {
		t.Fatalf("模型级默认应强类型化，得 n=%v(%T) background=%v(%T)",
			seen["n"], seen["n"], seen["background"], seen["background"])
	}
	if seen["prompt"] != "a cat" || seen["model"] != "gpt-image-1" {
		t.Fatalf("prompt/model 应平铺根级：%v", seen)
	}

	// promptMap 形态（图生图）
	seen = nil
	if _, err := m.PromptMap(map[string]any{"prompt": "换成夜景", "base_image_url": "https://x/a.png"}).
		Call(context.Background()); err != nil {
		t.Fatal(err)
	}
	if seen["base_image_url"] != "https://x/a.png" || seen["prompt"] != "换成夜景" {
		t.Fatalf("promptMap 应平铺根级：%v", seen)
	}
}
