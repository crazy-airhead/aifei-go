// rag_tool_test.go P4-b 检索辅助测试：Document.String、PromptAugment（命中拼进
// 用户消息）、RepositoryTool（schema/直接调用格式化/topK 与查询数上限/空查询
// 提示/Reranker 重排/假 agent 循环被选中并回注结果）。
package ai_test

import (
	"context"
	"strings"
	"testing"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/rag"
)

// seedToolRepo 工具测试库：apple（带 title/url，查询 apple 得 1.0）、
// fruit（0.6）、banana（正交 0）。
func seedToolRepo(t *testing.T) *rag.InMemoryRepository {
	t.Helper()
	r := rag.NewInMemoryRepository(&fakeRagEmbedder{batchSize: 10})
	docs := []*rag.Document{
		rag.NewDocument("apple").Meta("title", "苹果条目").Meta("url", "https://x/apple"),
		rag.NewDocument("fruit"),
		rag.NewDocument("banana"),
	}
	if err := r.Save(context.Background(), docs, nil); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRagDocumentString(t *testing.T) {
	s := rag.NewDocument("x").Meta("a", 1).SetScore(0.5).String()
	for _, want := range []string{"score=0.5", "content='x'", "metadata=map[a:1]"} {
		if !strings.Contains(s, want) {
			t.Errorf("String() = %q, missing %q", s, want)
		}
	}
}

func TestRagPromptAugment(t *testing.T) {
	msg, err := rag.PromptAugment(context.Background(), seedToolRepo(t), "apple")
	if err != nil {
		t.Fatal(err)
	}
	if msg.Role != ai.RoleUser {
		t.Fatalf("role = %v", msg.Role)
	}
	content, _ := msg.Content.(string)
	for _, want := range []string{"apple", "Now:", "References:", "Document{id=", "苹果条目"} {
		if !strings.Contains(content, want) {
			t.Errorf("content missing %q:\n%s", want, content)
		}
	}
}

func TestRagRepositoryToolDirect(t *testing.T) {
	ctx := context.Background()
	tool := rag.NewRepositoryTool(seedToolRepo(t))

	if tool.Name() != "repository_query" {
		t.Fatalf("name = %q", tool.Name())
	}
	schema := tool.InputSchema()
	for _, want := range []string{`"queries"`, `"type":"array"`, `"topK"`, `"type":"integer"`, `"default":3`} {
		if !strings.Contains(schema, want) {
			t.Errorf("schema missing %s: %s", want, schema)
		}
	}

	// 直接调用：格式化断言（topK=2 → apple(1.0)+fruit(0.6)，banana(0) 出局）
	out, err := tool.Handle(ctx, map[string]any{
		"queries": []any{"apple"}, "topK": 2})
	if err != nil {
		t.Fatal(err)
	}
	text, ok := out.(string)
	if !ok {
		t.Fatalf("handle result = %T", out)
	}
	for _, want := range []string{
		"### 多重检索结果汇总",
		"#### 针对关键词 [apple] 的结果：",
		"**来源 [1]**", "**来源 [2]**",
		"标题: 苹果条目",
		"相关度: 1.00",
		"内容: apple",
		"引用地址: https://x/apple",
		"\n---\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("result missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "banana") {
		t.Errorf("banana (score 0) should be filtered by threshold:\n%s", text)
	}

	// 缺省 topK=3：阈值过滤后仍 2 条
	out, _ = tool.Handle(ctx, map[string]any{"queries": []any{"apple"}})
	if n := strings.Count(out.(string), "**来源 ["); n != 2 {
		t.Errorf("default topK sources = %d, want 2", n)
	}

	// 查询数 >5 截断
	out, _ = tool.Handle(ctx, map[string]any{
		"queries": []any{"apple", "fruit", "banana", "apple", "fruit", "apple"}})
	if n := strings.Count(out.(string), "#### 针对关键词"); n != 5 {
		t.Errorf("query sections = %d, want 5 (cap)", n)
	}
	// 前五之内的空查询词跳过（Java continue 同款）
	out, _ = tool.Handle(ctx, map[string]any{"queries": []any{"apple", "", "fruit"}})
	if n := strings.Count(out.(string), "#### 针对关键词"); n != 2 {
		t.Errorf("query sections = %d, want 2 (blank skipped)", n)
	}

	// 空查询 → 友好提示
	out, _ = tool.Handle(ctx, map[string]any{"queries": []any{}})
	if out.(string) != "提示：未提供有效的查询关键词。" {
		t.Errorf("empty queries = %q", out)
	}
	if out, _ = tool.Handle(ctx, nil); out.(string) != "提示：未提供有效的查询关键词。" {
		t.Errorf("nil args = %q", out)
	}
}

// reverseReranker 反序回排（记录调用）。
type reverseReranker struct{ calls int }

func (r *reverseReranker) Rerank(_ context.Context, _ string, docs []*rag.Document) ([]*rag.Document, error) {
	r.calls++
	out := make([]*rag.Document, len(docs))
	for i, d := range docs {
		out[len(docs)-1-i] = d
	}
	return out, nil
}

func TestRagRepositoryToolReranker(t *testing.T) {
	ctx := context.Background()
	rr := &reverseReranker{}
	tool := rag.NewRepositoryTool(seedToolRepo(t), rag.WithReranker(rr))
	out, err := tool.Handle(ctx, map[string]any{"queries": []any{"apple"}, "topK": 2})
	if err != nil {
		t.Fatal(err)
	}
	text := out.(string)
	if rr.calls != 1 {
		t.Fatalf("reranker calls = %d", rr.calls)
	}
	// 原序 [apple, fruit] → 反序 [fruit, apple]：首来源块内容为 fruit
	iFruit, iApple := idxOf(t, text, "内容: fruit"), idxOf(t, text, "内容: apple")
	if !(iFruit < iApple) {
		t.Errorf("rerank order wrong:\n%s", text)
	}
}

// 卡片要求：RepositoryTool 在假 agent 循环中被选中并回注结果。
const respRagToolCallJSON = `{"id":"c-rag","model":"gpt-4o",
 "choices":[{"index":0,"message":{"role":"assistant","content":null,
   "tool_calls":[{"id":"call_rag","type":"function","index":0,
     "function":{"name":"repository_query","arguments":"{\"queries\":[\"apple\"],\"topK\":2}"}}]},"finish_reason":"tool_calls"}],
 "usage":{"prompt_tokens":5,"completion_tokens":5,"total_tokens":10}}`

func TestRagRepositoryToolAgentLoop(t *testing.T) {
	f := newFakeOpenAI(t, respRagToolCallJSON, respFinalJSON)
	m := fakeModel(t, f)

	res, err := m.Prompt("苹果是什么味道？").Options(func(o *ai.ChatOptions) {
		o.AutoToolCall = autoToolOn()
		o.Tools = append(o.Tools, rag.NewRepositoryTool(seedToolRepo(t)))
	}).Call()
	if err != nil {
		t.Fatal(err)
	}
	if res.Text() != "A1 已发货" { // respFinalJSON 的固定文案，证明走到第二请求
		t.Fatalf("final = %q", res.Text())
	}
	if f.count() != 2 {
		t.Fatalf("requests = %d, want 2", f.count())
	}

	// 第一请求携带工具定义
	tools, _ := f.request(t, 0).body["tools"].([]any)
	found := false
	for _, tl := range tools {
		tm, _ := tl.(map[string]any)
		fn, _ := tm["function"].(map[string]any)
		if fn != nil && fn["name"] == "repository_query" {
			found = true
		}
	}
	if !found {
		t.Errorf("request 0 tools missing repository_query: %v", tools)
	}

	// 第二请求回注工具结果（多重检索汇总进 messages）
	injected := false
	for _, msg := range f.messagesOf(t, 1) {
		if msg["role"] == "tool" {
			content, _ := msg["content"].(string)
			if strings.Contains(content, "多重检索结果汇总") && strings.Contains(content, "苹果条目") {
				injected = true
			}
		}
	}
	if !injected {
		t.Error("tool result not fed back into request 1 messages")
	}
}
