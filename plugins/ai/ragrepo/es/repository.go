// Package es Elasticsearch 向量仓储（对标 solon-ai-repo-elasticsearch 的
// ElasticsearchRepository；复用 plugins/elasticsearch 的 Client 接口）。
// 查询按现代 ES8 语义重写（05 §6 决策）：VECTOR+APPROXIMATE 走顶层 knn
//（Java 把 knn 子句误包进 bool.must——ES8 顶层 knn 才生效）；EXACT_KNN 走
// script_score 余弦；HYBRID 恒走 bool should [match + script_score]（顶层 knn
// 不能参与 bool 组合）。Save 后的 _refresh 在 Client 接口无对应面——ES 近实时
// 可见性依赖 refresh_interval（Java 逐批刷索引的行为差异已在文档注明）。
package es

import (
	"context"
	"fmt"

	"github.com/crazy-airhead/aifei-go/ai/rag"
	esclient "github.com/crazy-airhead/aifei-go/plugins/elasticsearch"
	ragrepo "github.com/crazy-airhead/aifei-go/plugins/ai/ragrepo"
)

// FieldType 元数据字段类型（对标 MetadataField.FieldType）。
type FieldType string

const (
	FieldText    FieldType = "text"
	FieldNumeric FieldType = "numeric"
	FieldBoolean FieldType = "boolean"
	FieldDate    FieldType = "date"
	FieldKeyword FieldType = "keyword"
)

// MetadataField 平铺到顶层的元数据字段（过滤翻译的目标列）。
type MetadataField struct {
	Name string
	Type FieldType
}

// VectorSearchType 向量检索类型（Java 同名枚举）。
type VectorSearchType uint8

const (
	ApproximateKNN VectorSearchType = iota // 顶层 knn（性能高）
	ExactKNN                               // script_score 余弦（精确）
)

// Repository ES 向量仓储。
type Repository struct {
	client   esclient.Client
	embedder embedder
	index    string

	metadataFields    []MetadataField
	vectorSearchType  VectorSearchType
	embeddingDim      int
}

// embedder 嵌入模型的仓储消费面（embedding.Model 的窄接口，测试可替换）。
type embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedDocs(ctx context.Context, docs []*rag.Document) error
	Dimensions(ctx context.Context) (int, error)
	BatchSize() int
}

// Option 仓储选项。
type Option func(*Repository)

// WithMetadataFields 平铺元数据字段（建 mapping + 过滤目标）。
func WithMetadataFields(fields ...MetadataField) Option {
	return func(r *Repository) { r.metadataFields = append(r.metadataFields, fields...) }
}

// WithVectorSearchType 向量检索类型（缺省 ApproximateKNN）。
func WithVectorSearchType(t VectorSearchType) Option {
	return func(r *Repository) { r.vectorSearchType = t }
}

// NewRepository 构造并初始化索引（对标 Java 构造期 initRepository）。
// embedder 参数接受 *embedding.Model。
func NewRepository(client esclient.Client, model embedder, indexName string, opts ...Option) (*Repository, error) {
	if client == nil {
		return nil, fmt.Errorf("ragrepo/es: client is required")
	}
	if model == nil {
		return nil, fmt.Errorf("ragrepo/es: embedding model is required")
	}
	r := &Repository{client: client, embedder: model, index: indexName}
	for _, o := range opts {
		o(r)
	}
	if err := r.InitRepository(context.Background()); err != nil {
		return nil, err
	}
	return r, nil
}

// InitRepository 索引存在性检查 + 建 mapping（content/metadata/embedding
// dense_vector cosine + 元数据字段平铺）。
func (r *Repository) InitRepository(ctx context.Context) error {
	exists, err := r.client.IndicesExists(ctx, r.index)
	if err != nil {
		return fmt.Errorf("ragrepo/es: index exists check: %w", err)
	}
	if exists {
		return nil
	}
	dims, err := r.embedder.Dimensions(ctx)
	if err != nil {
		return err
	}
	r.embeddingDim = dims

	props := map[string]any{
		"content":   map[string]any{"type": "text"},
		"metadata":  map[string]any{"type": "object"},
		"embedding": map[string]any{"type": "dense_vector", "dims": dims, "index": true, "similarity": "cosine"},
	}
	for _, f := range r.metadataFields {
		var t string
		switch f.Type {
		case FieldNumeric:
			t = "float"
		case FieldBoolean:
			t = "boolean"
		case FieldDate:
			t = "date"
		case FieldText:
			t = "text"
		default:
			t = "keyword"
		}
		props[f.Name] = map[string]any{"type": t}
	}
	return r.client.IndicesCreate(ctx, r.index, map[string]any{
		"mappings": map[string]any{"properties": props},
	})
}

// DropRepository 注销索引。
func (r *Repository) DropRepository(ctx context.Context) error {
	return r.client.IndicesDelete(ctx, r.index)
}

// Search 简单检索。
func (r *Repository) Search(ctx context.Context, query string) ([]*rag.Document, error) {
	return r.SearchQuery(ctx, &rag.Query{Query: query})
}

// SearchQuery 条件检索：embed → 检索 DSL（knn/script_score/全文/混合）→
// 命中映射 → rag.Refilter（阈值/新鲜度/FilterFunc）。
func (r *Repository) SearchQuery(ctx context.Context, q *rag.Query) ([]*rag.Document, error) {
	if q == nil || q.Query == "" {
		return nil, fmt.Errorf("ragrepo/es: query text cannot be empty for vector search")
	}
	vec, err := r.embedder.Embed(ctx, q.Query)
	if err != nil {
		return nil, err
	}

	body, err := r.buildSearchBody(q, vec)
	if err != nil {
		return nil, err
	}
	result, err := r.client.Search(ctx, r.index, body)
	if err != nil {
		return nil, err
	}
	return r.parseHits(result, q)
}

// TransformFilter 实现 rag.FilterTransformer：enjoy 子集 → ES bool query。
func (r *Repository) TransformFilter(expr string) (any, bool) {
	node, ok := ragrepo.ParseFilterExpr(expr)
	if !ok {
		return nil, false
	}
	query := ragrepo.ToESQuery(node)
	if query == nil {
		return nil, false
	}
	return query, true
}

// buildSearchBody 检索 DSL 组装（对标 translate + build*Query 族）。
func (r *Repository) buildSearchBody(q *rag.Query, vec []float32) (map[string]any, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = rag.DefaultLimit
	}
	body := map[string]any{"size": limit}
	if q.SimilarityThreshold > 0 {
		body["min_score"] = q.SimilarityThreshold
	}

	baseFilter := map[string]any{"match_all": map[string]any{}}
	if q.FilterExpr != "" {
		if query, ok := r.TransformFilter(q.FilterExpr); ok {
			baseFilter = query.(map[string]any)
		}
	}

	searchType := q.SearchType
	switch searchType {
	case rag.SearchFullText:
		body["query"] = map[string]any{"bool": map[string]any{
			"must":   []any{r.fullTextClause(q, 1.0)},
			"filter": baseFilter,
		}}
	case rag.SearchHybrid:
		fullWeight := 1.0
		vectorWeight := 1.0
		if q.HybridParams != nil {
			fullWeight = q.HybridParams.FullTextWeight
			vectorWeight = q.HybridParams.VectorWeight
		}
		body["query"] = map[string]any{"bool": map[string]any{
			"should":               []any{r.fullTextClause(q, fullWeight), r.scriptScoreClause(vec, vectorWeight)},
			"minimum_should_match": 1,
			"filter":               baseFilter,
		}}
	default: // vector
		if r.vectorSearchType == ApproximateKNN {
			// ES8 顶层 knn（filter 内嵌；k 与 num_candidates 同 Java）
			body["knn"] = map[string]any{
				"field":          "embedding",
				"query_vector":   vec,
				"k":              limit,
				"num_candidates": min(limit*10, 10000),
				"filter":         baseFilter,
			}
		} else {
			body["query"] = map[string]any{"script_score": map[string]any{
				"query": baseFilter,
				"script": map[string]any{
					"source": "cosineSimilarity(params.query_vector, 'embedding') + 1.0",
					"params": map[string]any{"query_vector": vec},
				},
			}}
		}
	}
	return body, nil
}

// fullTextClause 全文 match 子句（boost = 全文权重）。
func (r *Repository) fullTextClause(q *rag.Query, boost float64) map[string]any {
	content := map[string]any{"query": q.Query}
	if boost > 0 && boost != 1.0 {
		content["boost"] = boost
	}
	return map[string]any{"match": map[string]any{"content": content}}
}

// scriptScoreClause 精确余弦子句（权重经 script 加权——knn 的 boost 位对
// script_score 不可用，混合检索恒走此形态）。
func (r *Repository) scriptScoreClause(vec []float32, weight float64) map[string]any {
	source := "cosineSimilarity(params.query_vector, 'embedding') + 1.0"
	if weight > 0 && weight != 1.0 {
		source = fmt.Sprintf("(cosineSimilarity(params.query_vector, 'embedding') + 1.0) * %g", weight)
	}
	return map[string]any{"script_score": map[string]any{
		"query": map[string]any{"match_all": map[string]any{}},
		"script": map[string]any{
			"source": source,
			"params": map[string]any{"query_vector": vec},
		},
	}}
}

// parseHits 命中映射 + Refilter（阈值/新鲜度/FilterFunc 的统一收口）。
func (r *Repository) parseHits(result *esclient.SearchResult, q *rag.Query) ([]*rag.Document, error) {
	var docs []*rag.Document
	if result.Hits != nil {
		for i := range result.Hits.Hits {
			hit := &result.Hits.Hits[i]
			doc := rag.NewDocument("")
			doc.ID = hit.ID
			if hit.Source != nil {
				doc.Content, _ = hit.Source["content"].(string)
				if m, ok := hit.Source["metadata"].(map[string]any); ok {
					doc.Metadata = m
				}
				if u, ok := hit.Source["url"].(string); ok && u != "" {
					doc.Meta("url", u)
				}
			}
			if hit.Score != nil {
				doc.SetScore(*hit.Score)
			}
			docs = append(docs, doc)
		}
	}
	return rag.Refilter(docs, q)
}

// ---- Storer ----

// Save 分批嵌入 + 批量写入（元数据平铺顶层；进度按批回调）。
func (r *Repository) Save(ctx context.Context, docs []*rag.Document, progress func(done, total int)) error {
	if len(docs) == 0 {
		if progress != nil {
			progress(0, 0)
		}
		return nil
	}
	for _, doc := range docs {
		if doc.ID == "" {
			doc.ID = ragrepo.NewUUID()
		}
	}
	batchSize := max(r.embedder.BatchSize(), 1)
	batches := (len(docs) + batchSize - 1) / batchSize
	for i := 0; i < batches; i++ {
		batch := docs[i*batchSize : min((i+1)*batchSize, len(docs))]
		if err := r.embedder.EmbedDocs(ctx, batch); err != nil {
			return err
		}
		bulk := make([]esclient.BulkDoc, 0, len(batch))
		for _, doc := range batch {
			source := map[string]any{
				"content":   doc.Content,
				"metadata":  doc.Metadata,
				"embedding": doc.Embedding,
			}
			if u := doc.URL(); u != "" {
				source["url"] = u
			}
			for k, v := range doc.Metadata { // 平铺（Java 同款）
				if _, exists := source[k]; !exists {
					source[k] = v
				}
			}
			bulk = append(bulk, esclient.BulkDoc{ID: doc.ID, Doc: source})
		}
		result, err := r.client.BulkIndex(ctx, r.index, bulk)
		if err != nil {
			return err
		}
		if result != nil && result.Errors {
			return fmt.Errorf("ragrepo/es: bulk write reported errors")
		}
		if progress != nil {
			progress(i+1, batches)
		}
	}
	return nil
}

// DeleteByID 按 id 删除（不支持星号——Java 同款注释语义，逐条删）。
func (r *Repository) DeleteByID(ctx context.Context, ids ...string) error {
	for _, id := range ids {
		if _, err := r.client.Delete(ctx, r.index, id); err != nil {
			return err
		}
	}
	return nil
}

// ExistsByID 按 id 探测（经 Get.found——Client 接口无 HEAD 文档面）。
func (r *Repository) ExistsByID(ctx context.Context, id string) (bool, error) {
	result, err := r.client.Get(ctx, r.index, id)
	if err != nil {
		return false, err
	}
	return result != nil && result.Found, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

var _ rag.Repository = (*Repository)(nil)
var _ rag.Storer = (*Repository)(nil)
var _ rag.Lifecycle = (*Repository)(nil)
var _ rag.FilterTransformer = (*Repository)(nil)
