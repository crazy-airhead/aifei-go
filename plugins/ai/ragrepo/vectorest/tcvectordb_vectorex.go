// tcvectordb.go + vectorex.go 两小后端（服务端 embedding 的腾讯向量库 +
// javpower 开源库的 HTTP 面）。
package vectorest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai/rag"
	ragrepo "github.com/crazy-airhead/aifei-go/plugins/ai/ragrepo"
)

// ==================== TcVectorDB（服务端 embedding） ====================

// TcVectorDBRepository 腾讯向量数据库后端（唯一一个不做客户端嵌入的仓储：
// save 不调 embed、search 传原文——由服务端模型向量化）。
type TcVectorDBRepository struct {
	client    *ragrepo.RESTClient
	database  string
	collection string
}

// NewTcVectorDBRepository 构造（username/key 经 basic 头；embedder 参数被
// 忽略——服务端 embedding，保留参数位以统一构造面）。
func NewTcVectorDBRepository(baseURL, username, key, database, collection string, _ Embedder) (*TcVectorDBRepository, error) {
	c := ragrepo.NewRESTClient(baseURL)
	c.Header["Authorization"] = "Bearer " + username + ":" + key
	r := &TcVectorDBRepository{client: c, database: database, collection: collection}
	if err := r.InitRepository(context.Background()); err != nil {
		return nil, err
	}
	return r, nil
}

// InitRepository 建 database + collection（FilterIndex + VectorIndex + 服务端
// embedding 声明：vectorField/vector/__text/BGE 模型）。
func (r *TcVectorDBRepository) InitRepository(ctx context.Context) error {
	// database 幂等建
	_, _, _ = r.client.Post(ctx, "/database/create", ragrepo.JSONBytes(map[string]any{"database": r.database}))
	body, status, err := r.client.Post(ctx, "/"+r.database+"/collection/create", ragrepo.JSONBytes(map[string]any{
		"database":     r.database,
		"collection":   r.collection,
		"indexes": []map[string]any{
			{"fieldName": "id", "fieldType": "string", "indexType": "primaryKey"},
			{"fieldName": "vector", "fieldType": "vector", "indexType": "HNSW",
				"dimension": 1024, "metricType": "COSINE",
				"params": map[string]any{"M": 16, "efConstruction": 400}},
			{"fieldName": "__text", "fieldType": "string", "indexType": "filter"},
		},
		"embedding": map[string]any{
			"vectorField": "vector", "textField": "__text", "modelName": "BGE_LARGE_ZH_V1P5",
		},
	}))
	if err != nil {
		return err
	}
	if status >= 400 && !strings.Contains(string(body), "already exist") {
		return fmt.Errorf("ragrepo/tcvectordb: create collection status %d: %s", status, body)
	}
	return nil
}

// SearchQuery 检索（传原文——服务端向量化 + ANN；filter 字符串）。
func (r *TcVectorDBRepository) SearchQuery(ctx context.Context, q *rag.Query) ([]*rag.Document, error) {
	if q == nil || q.Query == "" {
		return nil, fmt.Errorf("ragrepo/tcvectordb: query text cannot be empty")
	}
	limit := q.Limit
	if limit <= 0 {
		limit = rag.DefaultLimit
	}
	filter := ""
	if q.FilterExpr != "" {
		if node, ok := ragrepo.ParseFilterExpr(q.FilterExpr); ok {
			filter = ToTcFilter(node)
		}
	}
	req := map[string]any{
		"database": r.database, "collection": r.collection,
		"search": map[string]any{
			"embeddingItems": []map[string]any{{"data": q.Query}},
			"limit":          limit,
			"params":         map[string]any{"ef": 500},
		},
		"output": []string{"id", "__text"},
	}
	if filter != "" {
		req["filter"] = filter
	}
	body, status, err := r.client.Post(ctx, "/"+r.database+"/document/search", ragrepo.JSONBytes(req))
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		return nil, fmt.Errorf("ragrepo/tcvectordb: search status %d: %s", status, body)
	}
	var resp struct {
		Documents []struct {
			ID      string  `json:"id"`
			Text    string  `json:"__text"`
			Score   float64 `json:"score"`
		} `json:"documents"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	var docs []*rag.Document
	for _, hit := range resp.Documents {
		doc := rag.NewDocument(hit.Text)
		doc.ID = hit.ID
		doc.SetScore(hit.Score)
		docs = append(docs, doc)
	}
	return ragrepo.Refilter(docs, q)
}

// Search 简单检索。
func (r *TcVectorDBRepository) Search(ctx context.Context, query string) ([]*rag.Document, error) {
	return r.SearchQuery(ctx, &rag.Query{Query: query})
}

// Save 批量写入（不做客户端嵌入——分批仅限流；__text 承载正文）。
func (r *TcVectorDBRepository) Save(ctx context.Context, docs []*rag.Document, progress func(done, total int)) error {
	if len(docs) == 0 {
		if progress != nil {
			progress(0, 0)
		}
		return nil
	}
	batchSize := 10
	batches := (len(docs) + batchSize - 1) / batchSize
	for i := 0; i < batches; i++ {
		end := (i + 1) * batchSize
		if end > len(docs) {
			end = len(docs)
		}
		batch := docs[i*batchSize : end]
		items := make([]map[string]any, len(batch))
		for j, d := range batch {
			fields := map[string]any{"__text": d.Content}
			for k, v := range d.Metadata {
				if k != "__text" {
					fields[k] = v
				}
			}
			items[j] = map[string]any{"id": d.ID, "fields": fields}
		}
		body, status, err := r.client.Post(ctx, "/"+r.database+"/document/upsert",
			ragrepo.JSONBytes(map[string]any{"database": r.database, "collection": r.collection,
				"documents": items, "buildIndex": true}))
		if err != nil {
			return err
		}
		if status >= 400 {
			return fmt.Errorf("ragrepo/tcvectordb: upsert status %d: %s", status, body)
		}
		if progress != nil {
			progress(i+1, batches)
		}
	}
	return nil
}

// ToTcFilter 过滤节点 → tcvectordb 字符串（field = "str"、and/or/not 小写）。
func ToTcFilter(n ragrepo.FilterNode) string {
	switch node := n.(type) {
	case *ragrepo.CmpNode:
		op := map[string]string{"==": "=", "!=": "!=", ">": ">", ">=": ">=", "<": "<", "<=": "<="}[node.Op]
		if _, isStr := node.Value.(string); isStr {
			return fmt.Sprintf(`%s %s "%v"`, node.Field, op, node.Value)
		}
		return fmt.Sprintf(`%s %s %v`, node.Field, op, node.Value)
	case *ragrepo.NotNode:
		return "not (" + ToTcFilter(node.Inner) + ")"
	case *ragrepo.AndNode:
		return "(" + ToTcFilter(node.Left) + " and " + ToTcFilter(node.Right) + ")"
	case *ragrepo.OrNode:
		return "(" + ToTcFilter(node.Left) + " or " + ToTcFilter(node.Right) + ")"
	}
	return ""
}

// ==================== VectoRex（javpower 开源库 HTTP 面） ====================

// VectoRexRepository VectoRex 后端（basic auth；余弦距离 score=1-clamp）。
type VectoRexRepository struct {
	base       ragrepo.RestRepositoryBase
	client     *ragrepo.RESTClient
	collection string
}

// NewVectoRexRepository 构造。
func NewVectoRexRepository(baseURL, username, password string, embedder Embedder, collection string) (*VectoRexRepository, error) {
	c := ragrepo.NewRESTClient(baseURL)
	if username != "" {
		c.Header["Authorization"] = "Bearer " + username + ":" + password
	}
	r := &VectoRexRepository{client: c, collection: collection}
	r.base.Embedder = embedder
	if err := r.InitRepository(context.Background()); err != nil {
		return nil, err
	}
	return r, nil
}

// InitRepository 建集合（标量 + 向量字段；幂等依赖服务端——Java 同款无条件建）。
func (r *VectoRexRepository) InitRepository(ctx context.Context) error {
	dims, err := r.base.Embedder.Dimensions(ctx)
	if err != nil {
		return err
	}
	_, _, err = r.client.Post(ctx, "/collection/create", ragrepo.JSONBytes(map[string]any{
		"collectionName": r.collection,
		"scalarFields":   []map[string]any{{"name": "id", "isPrimaryKey": true}},
		"vectorFileds": []map[string]any{{"name": "embedding",
			"metricType": "FLOAT_COSINE_DISTANCE", "dimensions": dims}},
	}))
	return err
}

// SearchQuery 检索（vector + topK + filter DSL）。
func (r *VectoRexRepository) SearchQuery(ctx context.Context, q *rag.Query) ([]*rag.Document, error) {
	if q == nil || q.Query == "" {
		return nil, fmt.Errorf("ragrepo/vectorex: query text cannot be empty")
	}
	vec, err := r.base.Embedder.Embed(ctx, q.Query)
	if err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = rag.DefaultLimit
	}
	req := map[string]any{"collectionName": r.collection, "vector": vec, "topK": limit}
	if q.FilterExpr != "" {
		if node, ok := ragrepo.ParseFilterExpr(q.FilterExpr); ok {
			if m := ToVectoRexQuery(node); m != nil {
				req["query"] = m
			}
		}
	}
	body, status, err := r.client.Post(ctx, "/collection/data/query", ragrepo.JSONBytes(req))
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		return nil, fmt.Errorf("ragrepo/vectorex: query status %d: %s", status, body)
	}
	var resp struct {
		Result []struct {
			Metadata map[string]any `json:"metadata"`
			Score    float64        `json:"score"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	var docs []*rag.Document
	for _, hit := range resp.Result {
		doc := rag.NewDocument("")
		if id, ok := hit.Metadata["id"].(string); ok {
			doc.ID = id
		}
		if c, ok := hit.Metadata["content"].(string); ok {
			doc.Content = c
		}
		if m, ok := hit.Metadata["metadata"].(map[string]any); ok {
			doc.Metadata = m
		}
		doc.SetScore(ragrepo.ClampScore(1 - hit.Score))
		docs = append(docs, doc)
	}
	return ragrepo.Refilter(docs, q)
}

// Search 简单检索。
func (r *VectoRexRepository) Search(ctx context.Context, query string) ([]*rag.Document, error) {
	return r.SearchQuery(ctx, &rag.Query{Query: query})
}

// Save 逐条写入（Java 同款每文档一次调用）。
func (r *VectoRexRepository) Save(ctx context.Context, docs []*rag.Document, progress func(done, total int)) error {
	return r.base.SaveBatches(ctx, docs, func(ctx context.Context, batch []*rag.Document) error {
		for _, d := range batch {
			meta := map[string]any{"id": d.ID, "content": d.Content, "metadata": d.Metadata, "embedding": d.Embedding}
			for k, v := range d.Metadata {
				meta[k] = v
			}
			if _, _, err := r.client.Post(ctx, "/collection/data/add", ragrepo.JSONBytes(map[string]any{
				"collectionName": r.collection, "metadata": meta,
			})); err != nil {
				return err
			}
		}
		return nil
	}, progress)
}

// ToVectoRexQuery 过滤节点 → 查询 DSL（eq/gt/ge/lt/le + and/or 链）。
func ToVectoRexQuery(n ragrepo.FilterNode) map[string]any {
	switch node := n.(type) {
	case *ragrepo.CmpNode:
		op := map[string]string{"==": "eq", "!=": "ne", ">": "gt", ">=": "ge", "<": "lt", "<=": "le"}[node.Op]
		return map[string]any{"field": node.Field, "op": op, "value": node.Value}
	case *ragrepo.AndNode:
		return map[string]any{"and": []any{ToVectoRexQuery(node.Left), ToVectoRexQuery(node.Right)}}
	case *ragrepo.OrNode:
		return map[string]any{"or": []any{ToVectoRexQuery(node.Left), ToVectoRexQuery(node.Right)}}
	case *ragrepo.NotNode:
		return map[string]any{"not": ToVectoRexQuery(node.Inner)}
	}
	return nil
}
