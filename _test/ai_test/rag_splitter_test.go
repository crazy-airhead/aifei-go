// rag_splitter_test.go P4-b 切分器测试：Regex（缺省/自定义/保留分隔/坏模式/
// 元数据拷贝独立）、TokenSize（句读截断/keepSeparator 拍平/maxChunkCount 软上限/
// 假 Tokenizer 对照）、ApproxTokenCount 单元、Json（数组/嵌套/选择器/映射器/
// 父元信息 putIfAbsent/坏 JSON/标量丢弃）、Semantic（假嵌入主题切点/短文直返/
// 最小句数/超长组再切/校验错误面）、Pipeline 顺序组合。
package ai_test

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/crazy-airhead/aifei-go/ai/rag"
)

// ---- RegexTextSplitter ----

func TestRagRegexSplitter(t *testing.T) {
	ctx := context.Background()
	docs, err := rag.NewRegexTextSplitter().Split(ctx, []*rag.Document{
		rag.NewDocument("para one\n\npara two\n\n\npara three").Meta("k", "v"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 3 { // 空块跳过
		t.Fatalf("chunks = %d, want 3: %v", len(docs), docs)
	}
	// 元数据逐块拷贝独立
	docs[0].Metadata["k"] = "mutated"
	if docs[1].Metadata["k"] != "v" {
		t.Error("chunk metadata must be independent copies")
	}

	// 自定义模式
	s, err := rag.NewRegexTextSplitterWith(`\d+`)
	if err != nil {
		t.Fatal(err)
	}
	docs, _ = s.Split(ctx, []*rag.Document{rag.NewDocument("a1b22c333")})
	if len(docs) != 3 || docs[0].Content != "a" || docs[2].Content != "c" {
		t.Fatalf("custom pattern = %v", docs)
	}

	// 保留分隔（Go 增量）
	keep, err := rag.NewRegexTextSplitterWith("\n\n")
	if err != nil {
		t.Fatal(err)
	}
	docs, _ = keep.KeepSeparator(true).Split(ctx, []*rag.Document{rag.NewDocument("p1\n\np2\n\np3")})
	if len(docs) != 3 || !strings.Contains(docs[0].Content, "\n\n") ||
		!strings.HasPrefix(docs[2].Content, "p3") {
		t.Fatalf("keep separator = %q", docsContent(docs))
	}

	// 坏模式报错
	if _, err := rag.NewRegexTextSplitterWith("["); err == nil {
		t.Error("bad regex should error")
	}
}

func docsContent(docs []*rag.Document) []string {
	out := make([]string, len(docs))
	for i, d := range docs {
		out[i] = d.Content
	}
	return out
}

// ---- TokenSizeTextSplitter ----

// runeTokenizer 每 rune 1 token（注入对照用）。
type runeTokenizer struct{}

func (runeTokenizer) TokenCount(s string) int { return utf8.RuneCountInString(s) }

func TestRagTokenSizeSplitterPunctuation(t *testing.T) {
	ctx := context.Background()
	s := rag.NewTokenSizeTextSplitterWith(20, 10, 5, 1000, true).Tokenizer(runeTokenizer{})
	docs, err := s.Split(ctx, []*rag.Document{rag.NewDocument(
		"aaaa bbbb cccc. dddd eeee ffff. gggg hhhh iiii. jjjj")})
	if err != nil {
		t.Fatal(err)
	}
	// 每 20-rune 块截回上一个句读 → 三块整句，尾巴 "jjjj"（4 rune）不足嵌入下限丢弃
	want := []string{"aaaa bbbb cccc.", "dddd eeee ffff.", "gggg hhhh iiii."}
	if len(docs) != len(want) {
		t.Fatalf("chunks = %d (%v), want %d", len(docs), docsContent(docs), len(want))
	}
	for i, w := range want {
		if docs[i].Content != w {
			t.Errorf("chunk[%d] = %q, want %q", i, docs[i].Content, w)
		}
	}
}

func TestRagTokenSizeSplitterFlattenAndCap(t *testing.T) {
	ctx := context.Background()
	// keepSeparator=false：块内换行拍平为空格
	s := rag.NewTokenSizeTextSplitterWith(30, 5, 1, 1000, false).Tokenizer(runeTokenizer{})
	docs, _ := s.Split(ctx, []*rag.Document{rag.NewDocument("line1 aaa.\nline2 bbb.\nline3 ccc.")})
	if len(docs) == 0 {
		t.Fatal("no chunks")
	}
	for _, d := range docs {
		if strings.Contains(d.Content, "\n") {
			t.Errorf("chunk %q still has newline", d.Content)
		}
	}
	// maxChunkCount 为软上限：循环截断后剩余尾巴仍会补上（Java 同款怪癖）
	capped := rag.NewTokenSizeTextSplitterWith(20, 10, 5, 2, true).Tokenizer(runeTokenizer{})
	docs, _ = capped.Split(ctx, []*rag.Document{rag.NewDocument(
		"aaaa bbbb cccc. dddd eeee ffff. gggg hhhh iiii. jjjj")})
	if len(docs) != 3 || docs[0].Content != "aaaa bbbb cccc." || docs[1].Content != "dddd eeee ffff." {
		t.Fatalf("capped chunks = %v", docsContent(docs))
	}
	// 空白文本无块
	docs, _ = s.Split(ctx, []*rag.Document{rag.NewDocument("   \n  ")})
	if len(docs) != 0 {
		t.Errorf("blank text chunks = %v", docsContent(docs))
	}
}

// 卡片要求：注入假 Tokenizer 对照——同文本同 chunkSize，按 rune 计数的切块
// 数量与块长显著小于字符近似（ASCII 每 4 字符 1 token）。
func TestRagTokenSizeTokenizerVsApprox(t *testing.T) {
	ctx := context.Background()
	var sb strings.Builder
	for i := 0; i < 40; i++ {
		sb.WriteString("word ")
	}
	text := sb.String() // 200 rune 纯 ASCII，无句读 → 无截断

	byRune, _ := rag.NewTokenSizeTextSplitterWith(10, 300, 1, 10000, true).
		Tokenizer(runeTokenizer{}).Split(ctx, []*rag.Document{rag.NewDocument(text)})
	byApprox, _ := rag.NewTokenSizeTextSplitterWith(10, 300, 1, 10000, true).
		Split(ctx, []*rag.Document{rag.NewDocument(text)})

	if len(byRune) != 20 {
		t.Fatalf("rune-token chunks = %d, want 20", len(byRune))
	}
	for _, d := range byRune {
		if utf8.RuneCountInString(d.Content) > 10 {
			t.Errorf("rune chunk %q exceeds 10 runes", d.Content)
		}
	}
	if len(byApprox) >= len(byRune) {
		t.Fatalf("approx chunks = %d (rune = %d)；ASCII 4字符/token 应显著更少块",
			len(byApprox), len(byRune))
	}
	for _, d := range byApprox {
		if utf8.RuneCountInString(d.Content) > 40 {
			t.Errorf("approx chunk %q exceeds 40 runes", d.Content)
		}
	}
}

func TestRagApproxTokenCount(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{
		{"", 0},
		{"a", 1},
		{"abcdefgh", 2},  // 8 ASCII / 4
		{"你好", 2},        // 2 CJK × 1
		{"你好abc", 3},     // 2 CJK + ceil(3/4)
		{"苹果apple香蕉", 6}, // 4 CJK×1 + ceil(5/4)=2
	} {
		if got := rag.ApproxTokenCount(tc.in); got != tc.want {
			t.Errorf("ApproxTokenCount(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// ---- JsonSplitter ----

func TestRagJsonSplitter(t *testing.T) {
	ctx := context.Background()
	parent := rag.NewDocument(`[{"name":"苹果","price":5,"note":"甜"},{"name":"香蕉","price":3}]`).
		Meta("src", "api")

	// 全字段平铺（行序不定 → 行集合断言）
	docs, err := rag.NewJsonSplitter().Split(ctx, []*rag.Document{parent})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("docs = %d, want 2", len(docs))
	}
	lines := map[string]bool{}
	for _, line := range strings.Split(docs[0].Content, "\n") {
		lines[line] = true
	}
	if !lines["name: 苹果"] || !lines["price: 5"] || !lines["note: 甜"] {
		t.Errorf("content lines = %v", lines)
	}
	if docs[0].Metadata["src"] != "api" { // 父元信息继承
		t.Errorf("parent metadata not inherited: %v", docs[0].Metadata)
	}

	// 内容选择器（缺键 → null）+ 元数据映射器（映射键优先于父元信息）
	docs, err = rag.NewJsonSplitter().
		ContentSelector("name", "missing").
		MetadataMapper(func(m map[string]any) map[string]any {
			return map[string]any{"name": m["name"], "title": m["name"].(string) + "条目"}
		}).
		Split(ctx, []*rag.Document{rag.NewDocument(`[{"name":"苹果"}]`).Meta("name", "parent")})
	if err != nil {
		t.Fatal(err)
	}
	if docs[0].Content != "name: 苹果\nmissing: null\n" {
		t.Errorf("selector content = %q", docs[0].Content)
	}
	if docs[0].Metadata["name"] != "苹果" || docs[0].Title() != "苹果条目" {
		t.Errorf("mapper metadata = %v", docs[0].Metadata)
	}

	// 嵌套数组递归 + 根对象 + 标量丢弃
	nested, err := rag.NewJsonSplitter().Split(ctx, []*rag.Document{
		rag.NewDocument(`[[{"a":1},{"a":2}],{"a":3}]`)})
	if err != nil || len(nested) != 3 {
		t.Fatalf("nested = %v err=%v", docsContent(nested), err)
	}
	single, err := rag.NewJsonSplitter().Split(ctx, []*rag.Document{rag.NewDocument(`{"a":1}`)})
	if err != nil || len(single) != 1 {
		t.Fatalf("object root = %v err=%v", docsContent(single), err)
	}
	scalars, err := rag.NewJsonSplitter().Split(ctx, []*rag.Document{rag.NewDocument(`5`)})
	if err != nil || len(scalars) != 0 {
		t.Fatalf("scalar root = %v err=%v", docsContent(scalars), err)
	}

	// 坏 JSON 报错
	if _, err := rag.NewJsonSplitter().Split(ctx, []*rag.Document{rag.NewDocument(`{bad`)}); err == nil {
		t.Error("bad json should error")
	}
}

// ---- SemanticSplitter ----

// topicEmbedder 按主题词给确定性矢量：苹果→[1,0]、香蕉→[0,1]、其他→[1,1]。
type topicEmbedder struct{ calls int }

func (e *topicEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	e.calls++
	switch {
	case strings.Contains(text, "苹果"):
		return []float32{1, 0}, nil
	case strings.Contains(text, "香蕉"):
		return []float32{0, 1}, nil
	default:
		return []float32{1, 1}, nil
	}
}

func (e *topicEmbedder) EmbedTexts(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, s := range texts {
		v, err := e.Embed(ctx, s)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func (e *topicEmbedder) BatchSize() int { return 2 }

// semanticText 四句两主题：句内相似度 1、跨主题 0（窗口 1）。
const semanticText = "苹果是一种水果。苹果很甜。香蕉是黄色的。香蕉很软。"

func TestRagSemanticSplitter(t *testing.T) {
	ctx := context.Background()
	e := &topicEmbedder{}
	s, err := rag.NewSemanticSplitterWith(e, 0.5, 512, 1, 1, rag.ChineseDelim)
	if err != nil {
		t.Fatal(err)
	}
	docs, err := s.Split(ctx, []*rag.Document{rag.NewDocument(semanticText).Meta("k", "v")})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("chunks = %q, want 2 (主题切点)", docsContent(docs))
	}
	if docs[0].Content != "苹果是一种水果。苹果很甜。" || docs[1].Content != "香蕉是黄色的。香蕉很软。" {
		t.Fatalf("chunks = %q", docsContent(docs))
	}
	if docs[0].Metadata["k"] != "v" {
		t.Error("metadata not inherited")
	}
	if e.calls == 0 {
		t.Error("embedder never called")
	}
}

func TestRagSemanticSplitterShortAndConstraints(t *testing.T) {
	ctx := context.Background()
	// 句子数 ≤ 窗口：整文直返，不求嵌入
	e := &topicEmbedder{}
	s, _ := rag.NewSemanticSplitterWith(e, 0.5, 512, 3, 1, rag.ChineseDelim)
	docs, err := s.Split(ctx, []*rag.Document{rag.NewDocument("苹果很甜。")})
	if err != nil || len(docs) != 1 || docs[0].Content != "苹果很甜。" || e.calls != 0 {
		t.Fatalf("short doc = %q err=%v embedCalls=%d", docsContent(docs), err, e.calls)
	}
	// 空白文档无块
	docs, err = s.Split(ctx, []*rag.Document{rag.NewDocument("  ")})
	if err != nil || len(docs) != 0 {
		t.Fatalf("blank doc = %v err=%v", docs, err)
	}

	// 最小句数约束挡住切点：min=3 → 相邻切点间距不足 → 整文一块
	s2, _ := rag.NewSemanticSplitterWith(&topicEmbedder{}, 0.5, 512, 1, 3, rag.ChineseDelim)
	docs, err = s2.Split(ctx, []*rag.Document{rag.NewDocument(semanticText)})
	if err != nil || len(docs) != 1 {
		t.Fatalf("minSentences=3 chunks = %q err=%v", docsContent(docs), err)
	}

	// 超长组再切：rune 计数 max=8 → 每句各成一块（8+5 > 8）
	s3, _ := rag.NewSemanticSplitterWith(&topicEmbedder{}, 0.5, 8, 1, 1, rag.ChineseDelim)
	s3.Tokenizer(runeTokenizer{})
	docs, err = s3.Split(ctx, []*rag.Document{rag.NewDocument(semanticText)})
	if err != nil || len(docs) != 4 {
		t.Fatalf("oversized chunks = %q err=%v", docsContent(docs), err)
	}
}

func TestRagSemanticSplitterValidation(t *testing.T) {
	_, err := rag.NewSemanticSplitter(nil)
	if err == nil || !strings.Contains(err.Error(), "must not be nil") {
		t.Errorf("nil embedder err = %v", err)
	}
	if _, err := rag.NewSemanticSplitterWith(&topicEmbedder{}, 0, 512, 1, 1, rag.ChineseDelim); err == nil {
		t.Error("threshold 0 should error")
	}
	if _, err := rag.NewSemanticSplitterWith(&topicEmbedder{}, 1, 512, 1, 1, rag.ChineseDelim); err == nil {
		t.Error("threshold 1 should error")
	}
	if _, err := rag.NewSemanticSplitterWith(&topicEmbedder{}, 0.5, 0, 1, 1, rag.ChineseDelim); err == nil {
		t.Error("maxChunkTokenSize 0 should error")
	}
	if _, err := rag.NewSemanticSplitterWith(&topicEmbedder{}, 0.5, 512, 0, 1, rag.ChineseDelim); err == nil {
		t.Error("window 0 should error")
	}
	if _, err := rag.NewSemanticSplitterWith(&topicEmbedder{}, 0.5, 512, 1, 0, rag.ChineseDelim); err == nil {
		t.Error("minSentences 0 should error")
	}
	if _, err := rag.NewSemanticSplitterWith(&topicEmbedder{}, 0.5, 512, 1, 1, nil); err == nil {
		t.Error("empty delimiters should error")
	}
	// 缺省构造可用（分隔符常量表非空）
	if _, err := rag.NewSemanticSplitter(&topicEmbedder{}); err != nil {
		t.Errorf("default constructor err = %v", err)
	}
}

// ---- SplitterPipeline ----

func TestRagSplitterPipeline(t *testing.T) {
	ctx := context.Background()
	pipeline := rag.NewSplitterPipeline(). // 空管线直返
						Next(rag.NewRegexTextSplitter()) // 按空行
	if docs, err := pipeline.Split(ctx, []*rag.Document{rag.NewDocument("p1\n\np2\n\np3")}); err != nil ||
		len(docs) != 3 {
		t.Fatalf("regex stage = %v err=%v", docsContent(docs), err)
	}

	// 两级：空行 → 空格（词级）。"a b\nc" 按空格切 = ["a","b\nc"]，"d e f" = 3 词
	wordSplit, err := rag.NewRegexTextSplitterWith(" ")
	if err != nil {
		t.Fatal(err)
	}
	full := rag.NewSplitterPipeline(rag.NewRegexTextSplitter()).Next(wordSplit)
	docs, err := full.Split(ctx, []*rag.Document{rag.NewDocument("a b\nc\n\nd e f")})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 5 {
		t.Fatalf("pipeline chunks = %v, want 5", docsContent(docs))
	}
}
