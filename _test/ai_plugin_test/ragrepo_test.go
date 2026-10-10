// ragrepo_test.go plugins/ai/ragrepo 黑盒测试（P5-V）：过滤表达式解析与双翻译
// 黄金表、ES 仓储 stub Client 全链路（建索引 mapping/knn 检索形态/批量写入
// 平铺/命中映射/Refilter）、pgvector SQL 骨架黄金。
package ai_plugin_test

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/crazy-airhead/aifei-go/ai/rag"
	ragrepo "github.com/crazy-airhead/aifei-go/plugins/ai/ragrepo"
	esrepo "github.com/crazy-airhead/aifei-go/plugins/ai/ragrepo/es"
	pgvector "github.com/crazy-airhead/aifei-go/plugins/ai/ragrepo/pgvector"
	elasticsearch "github.com/crazy-airhead/aifei-go/plugins/elasticsearch"
	es8 "github.com/elastic/go-elasticsearch/v8"
)

// ---- 过滤表达式解析 + 双翻译 ----

func TestRagrepoFilterParseMatrix(t *testing.T) {
	okCases := []string{
		`category == "berry"`, `category != "x"`, `price > 10`, `price >= 10`,
		`price < 9`, `price <= 9`, `lang == "en" && price > 5`,
		`lang == "en" || lang == "fr"`, `!(lang == "en")`, `flag`, `true`, `false`,
		`(a == 1 || b == 2) && c == 3`, `name == '单引号'`, `n == 1.5`,
	}
	for _, expr := range okCases {
		if _, ok := ragrepo.ParseFilterExpr(expr); !ok {
			t.Errorf("expr %q 应可解析", expr)
		}
	}
	badCases := []string{
		`title.contains("App")`, `title.startsWith("A")`, ``, `a ==`,
		`== "x"`, `(a == 1`, `a == 1 &&`, `a in (1,2)`,
	}
	for _, expr := range badCases {
		if _, ok := ragrepo.ParseFilterExpr(expr); ok {
			t.Errorf("expr %q 应不可解析（回退内存过滤）", expr)
		}
	}
}

func TestRagrepoToESQueryGolden(t *testing.T) {
	cases := []struct {
		expr string
		want string
	}{
		{`category == "berry"`, `{"term":{"category":"berry"}}`},
		{`category != "x"`, `{"bool":{"must_not":[{"term":{"category":"x"}}]}}`},
		{`price >= 10`, `{"range":{"price":{"gte":10}}}`},
		{`flag`, `{"exists":{"field":"flag"}}`},
		{`true`, `{"match_all":{}}`},
		{`lang == "en" && price > 5`, `{"bool":{"must":[{"term":{"lang":"en"}},{"range":{"price":{"gt":5}}}]}}`},
		{`lang == "en" || lang == "fr"`, `{"bool":{"minimum_should_match":1,"should":[{"term":{"lang":"en"}},{"term":{"lang":"fr"}}]}}`},
	}
	for _, c := range cases {
		node, ok := ragrepo.ParseFilterExpr(c.expr)
		if !ok {
			t.Fatalf("expr %q 解析失败", c.expr)
		}
		got := mustJSONString(ragrepo.ToESQuery(node))
		if got != c.want {
			t.Errorf("expr %q:\n got %s\nwant %s", c.expr, got, c.want)
		}
	}
}

func TestRagrepoToSQLWhereGolden(t *testing.T) {
	col := func(name string) (string, bool) {
		if name == "category" {
			return `"category"`, true
		}
		return "(metadata->>'" + name + "')", true
	}
	cases := []struct {
		expr string
		want string
		args []any
	}{
		{`category == "berry"`, `"category" = ?`, []any{"berry"}},
		{`price > 10`, `(metadata->>'price') > ?`, []any{float64(10)}},
		{`lang == "en" && price >= 5`, `((metadata->>'lang') = ? AND (metadata->>'price') >= ?)`, []any{"en", float64(5)}},
		{`!(lang == "en")`, `NOT ((metadata->>'lang') = ?)`, []any{"en"}},
	}
	for _, c := range cases {
		node, ok := ragrepo.ParseFilterExpr(c.expr)
		if !ok {
			t.Fatalf("expr %q 解析失败", c.expr)
		}
		clause, args, ok := ragrepo.ToSQLWhere(node, col)
		if !ok || clause != c.want || len(args) != len(c.args) {
			t.Errorf("expr %q: got (%s, %v, %v)", c.expr, clause, args, ok)
			continue
		}
		for i := range args {
			if args[i] != c.args[i] {
				t.Errorf("expr %q args[%d] = %v want %v", c.expr, i, args[i], c.args[i])
			}
		}
	}
	// 裸字段真值 SQL 不可译
	node, _ := ragrepo.ParseFilterExpr(`flag`)
	if _, _, ok := ragrepo.ToSQLWhere(node, col); ok {
		t.Error("裸字段真值应 SQL 不可译")
	}
}

// ---- ES 仓储全链路（stub Client）----

// stubESClient 记录调用并回放 canned 结果。
type stubESClient struct {
	indexExists bool
	createdMappings []map[string]any
	searchBodies    []map[string]any
	bulkDocs        [][]elasticsearch.BulkDoc
	deleted         []string
	getFound        map[string]bool

	nextResult *elasticsearch.SearchResult
}

func (s *stubESClient) Name() string { return "stub" }
func (s *stubESClient) Search(ctx context.Context, index string, query map[string]any) (*elasticsearch.SearchResult, error) {
	s.searchBodies = append(s.searchBodies, query)
	if s.nextResult != nil {
		return s.nextResult, nil
	}
	return &elasticsearch.SearchResult{}, nil
}
func (s *stubESClient) SearchRaw(ctx context.Context, index string, body io.Reader) (*elasticsearch.SearchResult, error) {
	return &elasticsearch.SearchResult{}, nil
}
func (s *stubESClient) Index(ctx context.Context, index, id string, doc any) (*elasticsearch.IndexResult, error) {
	return &elasticsearch.IndexResult{}, nil
}
func (s *stubESClient) Get(ctx context.Context, index, id string) (*elasticsearch.GetResult, error) {
	return &elasticsearch.GetResult{Found: s.getFound[id]}, nil
}
func (s *stubESClient) Delete(ctx context.Context, index, id string) (*elasticsearch.DeleteResult, error) {
	s.deleted = append(s.deleted, id)
	return &elasticsearch.DeleteResult{Result: "deleted"}, nil
}
func (s *stubESClient) BulkIndex(ctx context.Context, index string, docs []elasticsearch.BulkDoc) (*elasticsearch.BulkResult, error) {
	s.bulkDocs = append(s.bulkDocs, docs)
	return &elasticsearch.BulkResult{}, nil
}
func (s *stubESClient) IndicesExists(ctx context.Context, index string) (bool, error) {
	return s.indexExists, nil
}
func (s *stubESClient) IndicesCreate(ctx context.Context, index string, mappings map[string]any) error {
	s.createdMappings = append(s.createdMappings, mappings)
	return nil
}
func (s *stubESClient) IndicesDelete(ctx context.Context, index string) error { return nil }
func (s *stubESClient) Ping(ctx context.Context) error                        { return nil }
func (s *stubESClient) Close() error                                          { return nil }
func (s *stubESClient) ESClient() *es8.Client                                 { return nil }

// stubEmbedder 确定性嵌入：内容首字符的 ASCII 码重复 dims 次。
type stubEmbedder struct{ dims, batch int }

func (e *stubEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return e.vec(text), nil
}
func (e *stubEmbedder) EmbedDocs(ctx context.Context, docs []*rag.Document) error {
	for _, d := range docs {
		d.Embedding = e.vec(d.Content)
	}
	return nil
}
func (e *stubEmbedder) Dimensions(ctx context.Context) (int, error) { return e.dims, nil }
func (e *stubEmbedder) BatchSize() int                              { return e.batch }
func (e *stubEmbedder) vec(s string) []float32 {
	v := make([]float32, e.dims)
	code := float32(1)
	if len(s) > 0 {
		code = float32(s[0])
	}
	for i := range v {
		v[i] = code / 128
	}
	return v
}

func TestRagrepoEsInitMapping(t *testing.T) {
	client := &stubESClient{indexExists: false}
	_, err := esrepo.NewRepository(client, &stubEmbedder{dims: 4, batch: 10}, "docs",
		esrepo.WithMetadataFields(
			esrepo.MetadataField{Name: "category", Type: esrepo.FieldKeyword},
			esrepo.MetadataField{Name: "price", Type: esrepo.FieldNumeric},
		))
	if err != nil {
		t.Fatal(err)
	}
	if len(client.createdMappings) != 1 {
		t.Fatal("应建一次索引")
	}
	props := client.createdMappings[0]["mappings"].(map[string]any)["properties"].(map[string]any)
	if emb := props["embedding"].(map[string]any); emb["type"] != "dense_vector" || emb["dims"] != 4 || emb["similarity"] != "cosine" {
		t.Fatalf("embedding mapping：%v", emb)
	}
	if props["category"].(map[string]any)["type"] != "keyword" {
		t.Fatalf("category mapping：%v", props["category"])
	}
	if props["price"].(map[string]any)["type"] != "float" {
		t.Fatalf("price mapping：%v", props["price"])
	}

	// 已存在不重建
	client2 := &stubESClient{indexExists: true}
	esrepo.NewRepository(client2, &stubEmbedder{dims: 4, batch: 10}, "docs")
	if len(client2.createdMappings) != 0 {
		t.Fatal("索引已存在不应重建")
	}
}

func TestRagrepoEsVectorSearchBody(t *testing.T) {
	client := &stubESClient{}
	r, err := esrepo.NewRepository(client, &stubEmbedder{dims: 3, batch: 10}, "docs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.SearchQuery(context.Background(), &rag.Query{
		Query: "apple", Limit: 5, SimilarityThreshold: 0.4, FilterExpr: `category == "fruit"`,
	}); err != nil {
		t.Fatal(err)
	}
	body := client.searchBodies[0]
	if body["size"] != 5 {
		t.Fatalf("size：%v", body["size"])
	}
	if body["min_score"] != 0.4 {
		t.Fatalf("min_score：%v", body["min_score"])
	}
	knn := body["knn"].(map[string]any)
	if knn["field"] != "embedding" || knn["k"] != 5 {
		t.Fatalf("knn：%v", knn)
	}
	if knn["num_candidates"] != 50 {
		t.Fatalf("num_candidates = limit*10：%v", knn["num_candidates"])
	}
	filter := knn["filter"].(map[string]any)
	if filter["term"].(map[string]any)["category"] != "fruit" {
		t.Fatalf("knn filter：%v", filter)
	}

	// EXACT_KNN：script_score 形态
	client3 := &stubESClient{}
	r3, _ := esrepo.NewRepository(client3, &stubEmbedder{dims: 3, batch: 10}, "docs",
		esrepo.WithVectorSearchType(esrepo.ExactKNN))
	r3.SearchQuery(context.Background(), &rag.Query{Query: "a", Limit: 4})
	if _, has := client3.searchBodies[0]["query"].(map[string]any)["script_score"]; !has {
		t.Fatalf("EXACT_KNN 应 query.script_score：%v", client3.searchBodies[0])
	}

	// 全文与混合
	client4 := &stubESClient{}
	r4, _ := esrepo.NewRepository(client4, &stubEmbedder{dims: 3, batch: 10}, "docs")
	r4.SearchQuery(context.Background(), &rag.Query{Query: "a", Limit: 4, SearchType: rag.SearchFullText})
	query := client4.searchBodies[0]["query"].(map[string]any)
	if match := query["bool"].(map[string]any)["must"].([]any)[0].(map[string]any)["match"]; match == nil {
		t.Fatal("全文应为 match")
	}
	r4.SearchQuery(context.Background(), &rag.Query{Query: "a", Limit: 4, SearchType: rag.SearchHybrid,
		HybridParams: rag.NewHybridParams(0.7)})
	query = client4.searchBodies[1]["query"].(map[string]any)
	should := query["bool"].(map[string]any)["should"].([]any)
	if len(should) != 2 {
		t.Fatalf("混合应 match + script_score：%v", should)
	}
}

func TestRagrepoEsSaveSearchLoop(t *testing.T) {
	client := &stubESClient{getFound: map[string]bool{}}
	r, err := esrepo.NewRepository(client, &stubEmbedder{dims: 2, batch: 2}, "docs")
	if err != nil {
		t.Fatal(err)
	}

	// 保存：5 文档分 3 批（batch=2），进度回调；metadata 平铺顶层；id 补齐
	var progressCalls [][2]int
	docs := []*rag.Document{
		rag.NewDocument("apple").Meta("category", "fruit"),
		rag.NewDocument("banana").Meta("category", "fruit"),
		rag.NewDocument("carrot").Meta("category", "veg"),
		rag.NewDocument("date"),
		rag.NewDocument("eggplant"),
	}
	if err := r.Save(context.Background(), docs, func(done, total int) {
		progressCalls = append(progressCalls, [2]int{done, total})
	}); err != nil {
		t.Fatal(err)
	}
	if len(progressCalls) != 3 || progressCalls[2] != [2]int{3, 3} {
		t.Fatalf("进度回调：%v", progressCalls)
	}
	if len(client.bulkDocs) != 3 {
		t.Fatalf("批量次数：%d", len(client.bulkDocs))
	}
	first := client.bulkDocs[0][0]
	if first.ID == "" {
		t.Fatal("id 应补齐")
	}
	source := first.Doc.(map[string]any)
	if source["content"] != "apple" || source["category"] != "fruit" {
		t.Fatalf("写入 source 平铺：%v", source)
	}
	if emb := source["embedding"].([]float32); len(emb) != 2 {
		t.Fatalf("嵌入维度：%v", emb)
	}

	// 检索命中映射 + Refilter（相似度阈值）
	client.nextResult = &elasticsearch.SearchResult{Hits: &elasticsearch.SearchHits{
		Hits: []elasticsearch.SearchHitItem{
			{ID: "d1", Score: f64p(0.9), Source: map[string]any{
				"content": "apple", "metadata": map[string]any{"category": "fruit"}, "url": "https://x/a"}},
			{ID: "d2", Score: f64p(0.2), Source: map[string]any{
				"content": "carrot", "metadata": map[string]any{"category": "veg"}}},
		},
	}}
	got, err := r.SearchQuery(context.Background(), &rag.Query{Query: "apple", SimilarityThreshold: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "d1" || got[0].Score() != 0.9 {
		t.Fatalf("命中映射 + 阈值：%+v", got)
	}
	if got[0].URL() != "https://x/a" {
		t.Fatalf("url 元数据：%v", got[0].Metadata)
	}

	// 删除与存在性
	if err := r.DeleteByID(context.Background(), "d1", "d2"); err != nil {
		t.Fatal(err)
	}
	if len(client.deleted) != 2 {
		t.Fatalf("逐条删除：%v", client.deleted)
	}
	client.getFound["d1"] = true
	ok, _ := r.ExistsByID(context.Background(), "d1")
	if !ok {
		t.Fatal("ExistsByID 应命中")
	}

	// 空查询报错（Java IllegalArgumentException 同款）
	if _, err := r.SearchQuery(context.Background(), &rag.Query{}); err == nil {
		t.Fatal("空查询应报错")
	}
}

// ---- pgvector SQL 骨架黄金 ----

func TestRagrepoPgvectorSQLSkeleton(t *testing.T) {
	ddl := pgvector.BuildCreateTableDDL("docs", 1536, []pgvector.MetadataField{
		{Name: "category", Type: pgvector.FieldText},
		{Name: "score", Type: pgvector.FieldNumeric},
	})
	want := `CREATE TABLE docs (id VARCHAR(255) PRIMARY KEY, content TEXT NOT NULL, embedding VECTOR(1536), metadata JSONB, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP, "category" TEXT, "score" NUMERIC)`
	if ddl != want {
		t.Fatalf("建表 DDL：\n got %s\nwant %s", ddl, want)
	}

	q := pgvector.BuildSearchSQL("docs", "", 0)
	if q != "SELECT id, content, metadata, 1 - (embedding <=> $1::vector) as similarity FROM docs ORDER BY embedding <=> $2::vector LIMIT $3" {
		t.Fatalf("检索 SQL：%s", q)
	}
	q = pgvector.BuildSearchSQL("docs", `"category" = $2`, 1)
	if q != `SELECT id, content, metadata, 1 - (embedding <=> $1::vector) as similarity FROM docs WHERE "category" = $2 ORDER BY embedding <=> $3::vector LIMIT $4` {
		t.Fatalf("带过滤检索 SQL：%s", q)
	}

	up := pgvector.BuildUpsertSQL("docs", []string{"category"})
	wantUp := `INSERT INTO docs (id, content, embedding, metadata, "category") VALUES ($1, $2, $3, $4, $5) ON CONFLICT (id) DO UPDATE SET content = EXCLUDED.content, embedding = EXCLUDED.embedding, metadata = EXCLUDED.metadata, "category" = EXCLUDED."category"`
	if up != wantUp {
		t.Fatalf("upsert SQL：\n got %s\nwant %s", up, wantUp)
	}

	if v := pgvector.VectorLiteral([]float32{0.5, -0.25}); v != "[0.5,-0.25]" {
		t.Fatalf("向量字面量：%s", v)
	}
}

func f64p(v float64) *float64 { return &v }

func mustJSONString(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return err.Error()
	}
	return string(b)
}
