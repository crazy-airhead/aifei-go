// rag_test.go P4-a RAG SPI 测试：Document/TextLoader 基础、相似度工具、
// Partition、InMemory 生命周期（分批进度/自动 ID/删查/清库）、检索全参数矩阵
// （limit/阈值/缺省/新鲜度三形态/过滤表达式/原生过滤/叠加/禁重滤）、SnEL→enjoy
// 过滤表达式迁移表、混合权重钳制、nil 嵌入与无嵌入文档错误面。
package ai_test

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/crazy-airhead/aifei-go/ai/rag"
)

// ---- 假嵌入器：词表确定性矢量，未登录词回退单位化 [0.5,0.5] ----

// fakeVocab 与查询矢量的余弦有精确解：
//
//	apple·apple=1  apple·banana=0  apple·fruit=0.6  apple·回退=√½≈0.7071
var fakeVocab = map[string][]float32{
	"apple":  {1, 0},
	"banana": {0, 1},
	"fruit":  {0.6, 0.8},
}

type fakeRagEmbedder struct {
	batchSize int
	texts     []string // 逐批记录 EmbedTexts 收到的文本（断言分批）
}

func (f *fakeRagEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	if text == "no-vec" { // 特造内容：嵌入返回空 → 检索应报无嵌入错误
		return nil, nil
	}
	if v, ok := fakeVocab[text]; ok {
		return v, nil
	}
	return []float32{0.5, 0.5}, nil
}

func (f *fakeRagEmbedder) EmbedTexts(ctx context.Context, texts []string) ([][]float32, error) {
	f.texts = append(f.texts, texts...)
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v, err := f.Embed(ctx, t)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func (f *fakeRagEmbedder) BatchSize() int {
	if f.batchSize <= 0 {
		return 2
	}
	return f.batchSize
}

// ---- Document ----

func TestRagDocumentBasics(t *testing.T) {
	d := rag.NewDocument("hello").
		Meta("title", "T").
		Meta("url", "u/1").
		Meta("summary", "s").
		Meta("skip", nil) // nil 值忽略
	if d.Title() != "T" || d.URL() != "u/1" || d.Summary() != "s" {
		t.Fatalf("title/url/summary = %q/%q/%q", d.Title(), d.URL(), d.Summary())
	}
	if _, has := d.Metadata["skip"]; has {
		t.Error("nil meta value should be ignored")
	}
	if d.Score() != 0 {
		t.Errorf("initial score = %v, want 0", d.Score())
	}
	if d.SetScore(0.9) != d || d.Score() != 0.9 {
		t.Errorf("SetScore chain/score = %v", d.Score())
	}
	// MetaMap 批量 + 空 Metadata 兜底
	d2 := &rag.Document{}
	d2.MetaMap(map[string]any{"a": 1, "b": nil})
	if len(d2.Metadata) != 1 || d2.Metadata["a"] != 1 {
		t.Errorf("MetaMap on nil map = %v", d2.Metadata)
	}
	// 快捷访问缺键 → 空串
	if d2.Title() != "" {
		t.Errorf("missing title = %q", d2.Title())
	}
}

// ---- TextLoader ----

func TestRagTextLoader(t *testing.T) {
	ctx := context.Background()
	// string / bytes / reader 三源
	for _, tc := range []struct {
		name string
		load func() ([]*rag.Document, error)
	}{
		{"string", func() ([]*rag.Document, error) {
			return rag.NewTextLoaderFromString("héllo 中文").Load(ctx)
		}},
		{"bytes", func() ([]*rag.Document, error) {
			return rag.NewTextLoaderFromBytes([]byte("from bytes")).Load(ctx)
		}},
		{"reader", func() ([]*rag.Document, error) {
			return rag.NewTextLoader(strings.NewReader("from reader")).Load(ctx)
		}},
	} {
		docs, err := tc.load()
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(docs) != 1 {
			t.Fatalf("%s: %d docs, want 1", tc.name, len(docs))
		}
		if docs[0].Content == "" {
			t.Errorf("%s: empty content", tc.name)
		}
	}

	// 文件源 + 附加元数据
	dir := t.TempDir()
	name := filepath.Join(dir, "doc.txt")
	if err := os.WriteFile(name, []byte("from file"), 0o644); err != nil {
		t.Fatal(err)
	}
	docs, err := rag.NewTextLoaderFromFile(name).
		AdditionalMetadata("source", "file").
		AdditionalMetadata("title", "F").
		Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if docs[0].Content != "from file" || docs[0].Metadata["source"] != "file" || docs[0].Title() != "F" {
		t.Errorf("file load = %q %v", docs[0].Content, docs[0].Metadata)
	}

	// utf-16：BOM 小端 / BOM 大端（显式 le 被 BOM 纠正）/ 无 BOM 显式大端
	encodeUTF16 := func(s string, bomLE, bomBE bool, bigEndian bool) []byte {
		var out []byte
		if bomBE {
			out = append(out, 0xFE, 0xFF)
		} else if bomLE {
			out = append(out, 0xFF, 0xFE)
		}
		for _, u := range utf16.Encode([]rune(s)) {
			if bigEndian {
				out = append(out, byte(u>>8), byte(u))
			} else {
				out = append(out, byte(u), byte(u>>8))
			}
		}
		return out
	}
	text := "苹果 apple"
	for _, tc := range []struct {
		charset string
		data    []byte
	}{
		{"utf-16", encodeUTF16(text, true, false, false)},
		{"utf-16be", encodeUTF16(text, false, true, true)},
		{"utf-16le", encodeUTF16(text, false, true, true)}, // BOM 优先：显式 le 被 BE BOM 纠正为大端
	} {
		docs, err := rag.NewTextLoaderFromBytes(tc.data).Charset(tc.charset).Load(ctx)
		if err != nil {
			t.Fatalf("charset %s: %v", tc.charset, err)
		}
		if docs[0].Content != text {
			t.Errorf("charset %s decoded = %q, want %q", tc.charset, docs[0].Content, text)
		}
	}

	// 不支持的编码：显式报错（零依赖收窄面）
	if _, err := rag.NewTextLoaderFromString("x").Charset("gbk").Load(ctx); err == nil ||
		!strings.Contains(err.Error(), "unsupported charset") {
		t.Errorf("gbk err = %v, want unsupported charset", err)
	}

	// ctx 取消：开读前即拒
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := rag.NewTextLoaderFromString("x").Load(canceled); err != context.Canceled {
		t.Errorf("canceled ctx err = %v", err)
	}

	// 缺席文件：错误面收口在 Load
	if _, err := rag.NewTextLoaderFromFile(filepath.Join(dir, "nope")).Load(ctx); err == nil {
		t.Error("missing file should fail at Load")
	}
}

// ---- 相似度工具 ----

func TestRagSimilarityUtils(t *testing.T) {
	if d, _ := rag.DotProduct([]float32{1, 2}, []float32{3, 4}); d != 11 {
		t.Errorf("dot = %v, want 11", d)
	}
	c, err := rag.CosineSimilarity([]float32{1, 0}, []float32{0.6, 0.8})
	if err != nil || math.Abs(c-0.6) > 1e-6 { // float32 舍入（0.6f32 ≈ 0.6000000095）
		t.Errorf("cosine = %v err=%v, want 0.6", c, err)
	}
	if c, _ := rag.CosineSimilarity([]float32{1, 0}, []float32{0, 1}); math.Abs(c) > 1e-9 {
		t.Errorf("orthogonal cosine = %v, want 0", c)
	}
	if dist, err := rag.EuclideanDistance([]float32{0, 0}, []float32{3, 4}); err != nil || dist != 5 {
		t.Errorf("euclid = %v err=%v, want 5", dist, err)
	}
	if s, err := rag.EuclideanSimilarity([]float32{0, 0}, []float32{3, 4}); err != nil || math.Abs(s-1.0/6) > 1e-9 {
		t.Errorf("euclid sim = %v err=%v, want 1/6", s, err)
	}
	// 错误面：nil / 长度不等 / 零范数（Java IllegalArgumentException 对应物）
	if _, err := rag.CosineSimilarity(nil, []float32{1}); err == nil {
		t.Error("nil cosine should error")
	}
	if _, err := rag.DotProduct([]float32{1}, []float32{1, 2}); err == nil {
		t.Error("length mismatch should error")
	}
	if _, err := rag.CosineSimilarity([]float32{0, 0}, []float32{1, 1}); err == nil {
		t.Error("zero norm should error")
	}
	if _, err := rag.EuclideanDistance(nil, nil); err == nil {
		t.Error("nil euclid should error")
	}
}

func TestRagPartition(t *testing.T) {
	got := rag.Partition([]int{1, 2, 3, 4, 5}, 2)
	if len(got) != 3 || len(got[0]) != 2 || len(got[2]) != 1 {
		t.Errorf("partition = %v", got)
	}
	if got := rag.Partition([]int{1, 2, 3}, 0); len(got) != 1 || len(got[0]) != 3 {
		t.Errorf("size<=0 → default 10: %v", got)
	}
	if got := rag.Partition([]int{}, 3); len(got) != 0 {
		t.Errorf("empty → empty: %v", got)
	}
}

func TestRagHybridParamsClamp(t *testing.T) {
	for _, tc := range []struct {
		in, vWant float64
	}{{-1, 0}, {0, 0}, {0.7, 0.7}, {1, 1}, {2, 1}} {
		p := rag.NewHybridParams(tc.in)
		if p.VectorWeight != tc.vWant || math.Abs(p.VectorWeight+p.FullTextWeight-1) > 1e-9 {
			t.Errorf("NewHybridParams(%v) = %+v", tc.in, p)
		}
	}
	if s := rag.SearchHybrid.String(); s != "hybrid" {
		t.Errorf("hybrid string = %q", s)
	}
	if s := rag.SearchFullText.String(); s != "full_text" {
		t.Errorf("full_text string = %q", s)
	}
	if s := rag.SearchVector.String(); s != "vector" {
		t.Errorf("vector string = %q", s)
	}
}

// ---- InMemory 生命周期 ----

func TestRagInMemoryLifecycle(t *testing.T) {
	ctx := context.Background()
	e := &fakeRagEmbedder{batchSize: 2}
	r := rag.NewInMemoryRepository(e)

	// 空集：仅回调 (0,0)
	var calls [][2]int
	if err := r.Save(ctx, nil, func(done, total int) { calls = append(calls, [2]int{done, total}) }); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0] != [2]int{0, 0} {
		t.Fatalf("empty progress = %v", calls)
	}

	// 5 文档 / 批 2 → (1,3)(2,3)(3,3)；空 ID 补随机 ID
	calls = nil
	docs := make([]*rag.Document, 5)
	for i := range docs {
		docs[i] = rag.NewDocument("d" + strings.Repeat("x", i))
	}
	if err := r.Save(ctx, docs, func(done, total int) { calls = append(calls, [2]int{done, total}) }); err != nil {
		t.Fatal(err)
	}
	want := [][2]int{{1, 3}, {2, 3}, {3, 3}}
	if len(calls) != 3 {
		t.Fatalf("progress = %v, want %v", calls, want)
	}
	for i, c := range calls {
		if c != want[i] {
			t.Fatalf("progress = %v, want %v", calls, want)
		}
	}
	if len(e.texts) != 5 {
		t.Errorf("embedded texts = %v", e.texts)
	}
	ids := map[string]bool{}
	for _, d := range docs {
		if d.ID == "" || ids[d.ID] {
			t.Errorf("auto id = %q (dup=%v)", d.ID, ids[d.ID])
		}
		ids[d.ID] = true
		if len(d.Embedding) != 2 {
			t.Errorf("doc %q embedding = %v", d.ID, d.Embedding)
		}
		if ok, _ := r.ExistsByID(ctx, d.ID); !ok {
			t.Errorf("ExistsByID(%q) = false", d.ID)
		}
	}

	// 显式 ID 保存；同 ID 再存即覆盖
	first := rag.NewDocument("apple")
	first.ID = "explicit-1"
	if err := r.Save(ctx, []*rag.Document{first}, nil); err != nil {
		t.Fatal(err)
	}
	second := rag.NewDocument("fruit")
	second.ID = "explicit-1"
	if err := r.Save(ctx, []*rag.Document{second}, nil); err != nil {
		t.Fatal(err)
	}
	if ok, _ := r.ExistsByID(ctx, "explicit-1"); !ok {
		t.Fatal("explicit id missing")
	}
	got, err := r.SearchQuery(ctx, &rag.Query{Query: "apple", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range got {
		if d.Content == "apple" {
			t.Error("same-id save must overwrite, not append (apple doc still present)")
		}
	}

	// 删除 / 清库
	if err := r.DeleteByID(ctx, docs[0].ID, second.ID); err != nil {
		t.Fatal(err)
	}
	if ok, _ := r.ExistsByID(ctx, docs[0].ID); ok {
		t.Error("delete failed")
	}
	if err := r.InitRepository(ctx); err != nil {
		t.Errorf("init should be noop: %v", err)
	}
	if err := r.DropRepository(ctx); err != nil {
		t.Fatal(err)
	}
	got, err = r.Search(ctx, "apple")
	if err != nil || len(got) != 0 {
		t.Errorf("after drop search = %v err=%v", got, err)
	}
}

// ---- InMemory 检索：全参数矩阵 ----

// seedRagMatrix 建库：apple/banana 精确矢量，cherry/date 走回退矢量（√½ 同分）。
// 查询 "apple"（矢量 [1,0]）的余弦得分：apple=1、banana=0、cherry=date≈0.7071。
func seedRagMatrix(t *testing.T) *rag.InMemoryRepository {
	t.Helper()
	r := rag.NewInMemoryRepository(&fakeRagEmbedder{batchSize: 10})
	docs := []*rag.Document{
		rag.NewDocument("apple").Meta("category", "fruit"),
		rag.NewDocument("banana").Meta("category", "fruit").Meta("title", "hidden"),
		rag.NewDocument("cherry").Meta("category", "berry"),
		rag.NewDocument("date").Meta("category", "berry").Meta("title", "hidden"),
	}
	if err := r.Save(context.Background(), docs, nil); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRagSearchMatrix(t *testing.T) {
	ctx := context.Background()
	r := seedRagMatrix(t)
	search := func(q *rag.Query) []*rag.Document {
		t.Helper()
		docs, err := r.SearchQuery(ctx, q)
		if err != nil {
			t.Fatalf("search %+v: %v", q, err)
		}
		return docs
	}
	contents := func(docs []*rag.Document) string {
		var sb strings.Builder
		for i, d := range docs {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(d.Content)
		}
		return sb.String()
	}

	// 缺省（Search）：limit 4 / 阈值 0.4 → banana(0) 出局，其余按分降序
	got, err := r.Search(ctx, "apple")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Content != "apple" || math.Abs(got[0].Score()-1) > 1e-9 {
		t.Fatalf("default search = %s (score0=%v)", contents(got), got[0].Score())
	}

	// limit 截断：top-2 = apple + 回退档（cherry/date 平分，取一即可）
	got = search(&rag.Query{Query: "apple", Limit: 2})
	if len(got) != 2 || got[0].Content != "apple" || math.Abs(got[1].Score()-0.70710678) > 1e-6 {
		t.Errorf("limit 2 = %s", contents(got))
	}

	// 阈值抬升：0.8 → 仅 apple
	got = search(&rag.Query{Query: "apple", SimilarityThreshold: 0.8, Limit: 10})
	if len(got) != 1 || got[0].Content != "apple" {
		t.Errorf("threshold 0.8 = %s", contents(got))
	}
	// 阈值 <=0 → 缺省 0.4（偏差文档化：banana 仍出局）
	got = search(&rag.Query{Query: "apple", SimilarityThreshold: 0})
	if len(got) != 3 {
		t.Errorf("threshold 0 → default: %s", contents(got))
	}
	// limit <=0 → 缺省 4
	got = search(&rag.Query{Query: "apple", Limit: 0})
	if len(got) != 3 {
		t.Errorf("limit 0 → default: %s", contents(got))
	}

	// 表达式过滤：berry 档 → cherry,date（同分并列，快照序不定 → 集合断言）
	got = search(&rag.Query{Query: "apple", FilterExpr: `category == "berry"`, Limit: 10})
	set := map[string]bool{}
	for _, d := range got {
		set[d.Content] = true
	}
	if len(got) != 2 || !set["cherry"] || !set["date"] {
		t.Errorf("expr berry = %s", contents(got))
	}

	// 原生过滤：title != hidden
	got = search(&rag.Query{Query: "apple", Limit: 10,
		FilterFunc: func(d *rag.Document) bool { return d.Title() != "hidden" }})
	if len(got) != 2 || got[0].Content != "apple" {
		t.Errorf("filterfunc = %s", contents(got))
	}

	// 叠加：表达式 ∧ 原生 → cherry（berry 且非 hidden 只有 cherry…且 apple 非 berry 出局）
	got = search(&rag.Query{Query: "apple", Limit: 10,
		FilterExpr: `category == "berry"`,
		FilterFunc: func(d *rag.Document) bool { return d.Title() != "hidden" }})
	if len(got) != 1 || got[0].Content != "cherry" {
		t.Errorf("expr+func = %s", contents(got))
	}

	// 禁重滤：表达式不再求值（恒假表达式也全放行），阈值/排序/截断仍在
	got = search(&rag.Query{Query: "apple", Limit: 10, DisableRefilter: true,
		FilterExpr: `category == "nope"`, FilterFunc: func(*rag.Document) bool { return false }})
	if len(got) != 3 {
		t.Errorf("disable refilter = %s (want threshold still applied: banana out)", contents(got))
	}

	// 非法表达式 → 错误
	if _, err := r.SearchQuery(ctx, &rag.Query{Query: "apple", FilterExpr: `category ==`}); err == nil {
		t.Error("bad expr should error")
	}
}

func TestRagSearchFreshness(t *testing.T) {
	ctx := context.Background()
	r := rag.NewInMemoryRepository(&fakeRagEmbedder{batchSize: 10})
	now := time.Now()
	docs := []*rag.Document{
		rag.NewDocument("f-time").Meta("createdAt", now.Add(-time.Minute)),                     // time.Time 命中
		rag.NewDocument("f-int").Meta("createdAt", now.Add(-time.Minute).Unix()),               // int64 命中
		rag.NewDocument("f-str").Meta("createdAt", now.Add(-time.Minute).Format(time.RFC3339)), // 字符串命中
		rag.NewDocument("f-old").Meta("createdAt", now.Add(-48*time.Hour)),                     // 窗口外
		rag.NewDocument("f-none"), // 无时间 → 窗口开启即排除
	}
	if err := r.Save(ctx, docs, nil); err != nil {
		t.Fatal(err)
	}

	q := rag.NewQuery("apple")
	q.Limit = 10
	q.Freshness = rag.LastDay()
	got, err := r.SearchQuery(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 { // f-time/f-int/f-str（回退矢量 √½ 均过阈值）
		t.Fatalf("last-day = %d docs, want 3", len(got))
	}
	set := map[string]bool{}
	for _, d := range got {
		set[d.Content] = true
	}
	for _, want := range []string{"f-time", "f-int", "f-str"} {
		if !set[want] {
			t.Errorf("missing %s in %v", want, set)
		}
	}

	// 显式时间窗（NewFreshness）：Start/End 双端约束
	fresh := rag.NewFreshness(now.Add(-2*time.Hour), now.Add(-time.Hour))
	q.Freshness = fresh
	got, _ = r.SearchQuery(ctx, q)
	if len(got) != 0 {
		t.Errorf("window [-2h,-1h] matched %d docs (f-* at ~now), want 0", len(got))
	}
	// 全窗：LastWeek 等宽窗族覆盖全部有时间文档
	q.Freshness = rag.LastWeek()
	got, _ = r.SearchQuery(ctx, q)
	if len(got) != 4 { // f-old 也在一周内；f-none 仍无时间出局
		t.Errorf("last-week = %d docs, want 4", len(got))
	}
	// LastMonth/LastYear 存在且为宽窗
	if rag.LastMonth() == nil || rag.LastYear() == nil {
		t.Error("freshness constructors must not be nil")
	}
}

// ---- SnEL → enjoy 过滤表达式迁移表（契约 §3 对照） ----

func TestRagFilterExprTable(t *testing.T) {
	cases := []struct {
		expr string
		meta map[string]any
		want bool
	}{
		// SnEL `==` → enjoy `==`（Java `a.equals(b)` 迁移为 `a == b`）
		{`name == "apple"`, map[string]any{"name": "apple"}, true},
		{`name == "apple"`, map[string]any{"name": "banana"}, false},
		{`name != "apple"`, map[string]any{"name": "banana"}, true},
		// 数值比较
		{`price > 10`, map[string]any{"price": 15}, true},
		{`price > 10`, map[string]any{"price": 10}, false},
		{`price >= 10`, map[string]any{"price": 10}, true},
		{`price < 10`, map[string]any{"price": 9}, true},
		{`price <= 10`, map[string]any{"price": 10}, true},
		// && / || / !
		{`lang == "en" && price > 5`, map[string]any{"lang": "en", "price": 7}, true},
		{`lang == "en" && price > 5`, map[string]any{"lang": "en", "price": 3}, false},
		{`lang == "en" && price > 5`, map[string]any{"lang": "zh", "price": 7}, false},
		{`lang == "en" || lang == "fr"`, map[string]any{"lang": "fr"}, true},
		{`lang == "en" || lang == "fr"`, map[string]any{"lang": "zh"}, false},
		{`!(lang == "en")`, map[string]any{"lang": "zh"}, true},
		{`!(lang == "en")`, map[string]any{"lang": "en"}, false},
		// 字符串方法（SnEL contains/startsWith 对应物）
		{`title.contains("App")`, map[string]any{"title": "Apple pie"}, true},
		{`title.contains("Cake")`, map[string]any{"title": "Apple pie"}, false},
		{`title.startsWith("App")`, map[string]any{"title": "Apple pie"}, true},
		{`title.startsWith("app")`, map[string]any{"title": "Apple pie"}, false},
		{`title.endsWith("pie")`, map[string]any{"title": "Apple pie"}, true},
		{`title.endsWith("Pie")`, map[string]any{"title": "Apple pie"}, false},
		// 缺键：nil 参与相等比较 → false
		{`missing == "x"`, map[string]any{}, false},
		{`missing != "x"`, map[string]any{}, true},
		// 裸值真值映射：nil→false、bool→原值、非空串→true
		{`flag`, map[string]any{"flag": true}, true},
		{`flag`, map[string]any{"flag": false}, false},
		{`flag`, map[string]any{}, false},
		{`name`, map[string]any{"name": "apple"}, true},
	}
	for _, c := range cases {
		docs, err := rag.Refilter(
			[]*rag.Document{rag.NewDocument("x").SetScore(1).MetaMap(c.meta)},
			&rag.Query{FilterExpr: c.expr, Limit: 10, SimilarityThreshold: 0.4},
		)
		if err != nil {
			t.Errorf("expr %q: %v", c.expr, err)
			continue
		}
		if kept := len(docs) == 1; kept != c.want {
			t.Errorf("expr %q with %v: kept=%v, want %v", c.expr, c.meta, kept, c.want)
		}
	}
}

// ---- 错误面 ----

func TestRagNoEmbedderErrors(t *testing.T) {
	ctx := context.Background()
	r := rag.NewInMemoryRepository(nil)
	if err := r.Save(ctx, []*rag.Document{rag.NewDocument("a")}, nil); err == nil ||
		!strings.Contains(err.Error(), "no embedding model") {
		t.Errorf("save without embedder err = %v", err)
	}
	if _, err := r.Search(ctx, "a"); err == nil {
		t.Error("search without embedder should error")
	}
	// 空集保存不需要嵌入器（0 批次短路）
	if err := r.Save(ctx, nil, nil); err != nil {
		t.Errorf("empty save err = %v", err)
	}
}

func TestRagUnembeddedDocSearchError(t *testing.T) {
	ctx := context.Background()
	r := rag.NewInMemoryRepository(&fakeRagEmbedder{})
	// "no-vec" 内容的嵌入为空矢量 → 保存成功但检索报无嵌入
	if err := r.Save(ctx, []*rag.Document{rag.NewDocument("no-vec"), rag.NewDocument("apple")}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Search(ctx, "apple"); err == nil ||
		!strings.Contains(err.Error(), "has no embedding") {
		t.Errorf("search err = %v, want has no embedding", err)
	}
}

func TestRagQueryDefaults(t *testing.T) {
	q := rag.NewQuery("q")
	if q.Limit != rag.DefaultLimit || q.SimilarityThreshold != rag.DefaultSimilarityThreshold ||
		q.SearchType != rag.SearchVector || q.DisableRefilter {
		t.Errorf("NewQuery defaults = %+v", q)
	}
	if rag.DefaultLimit != 4 || rag.DefaultSimilarityThreshold != 0.4 || rag.DefaultBatchSize != 10 {
		t.Error("default constants drifted from Java (4/0.4/10)")
	}
	// Refilter nil 兜底
	got, err := rag.Refilter([]*rag.Document{rag.NewDocument("a").SetScore(1)}, nil)
	if err != nil || len(got) != 1 {
		t.Errorf("refilter nil query = %v err=%v", got, err)
	}
	// 表达式缓存复用：同表达式多次求值
	for i := 0; i < 3; i++ {
		got, err := rag.Refilter(
			[]*rag.Document{rag.NewDocument("a").SetScore(1).Meta("k", "v")},
			&rag.Query{FilterExpr: `k == "v"`, Limit: 10})
		if err != nil || len(got) != 1 {
			t.Errorf("cached eval #%d = %v err=%v", i, got, err)
		}
	}
}
