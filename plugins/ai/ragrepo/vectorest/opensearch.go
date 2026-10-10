// Package vectorest REST 检索面后端三件（对标 opensearch / weaviate / tcvectordb /
// vectorex 四模块的 Go 统一形态）：opensearch = ES 近亲（knn_vector + knn DSL）；
// weaviate = GraphQL nearVector + certainty；tcvectordb = 服务端 embedding
//（不本地嵌入，检索传原文）；vectorex = javpower 开源库（余弦距离 + 自定义
// 查询 DSL 形状）。全部走 ragrepo.RESTClient。
package vectorest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai/rag"
	ragrepo "github.com/crazy-airhead/aifei-go/plugins/ai/ragrepo"
)

// Embedder 嵌入面（tcvectordb 不需要——服务端 embedding；其余需要）。
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedDocs(ctx context.Context, docs []*rag.Document) error
	Dimensions(ctx context.Context) (int, error)
	BatchSize() int
}

// ==================== OpenSearch（对标 solon-ai-repo-opensearch） ====================

// OpenSearchRepository OpenSearch 后端（knn_vector 映射 + 顶层 knn 查询——与
// ES 的差异仅在建索引 settings 与 knn 结构内联）。
type OpenSearchRepository struct {
	base    ragrepo.RestRepositoryBase
	client  *ragrepo.RESTClient
	index   string
	metadata []ragrepo.MetadataFieldX
}

// NewOpenSearchRepository 构造（basic auth 可选）。
func NewOpenSearchRepository(baseURL, username, password string, embedder Embedder, index string, metadata ...ragrepo.MetadataFieldX) (*OpenSearchRepository, error) {
	c := ragrepo.NewRESTClient(baseURL)
	if username != "" {
		c.Header["Authorization"] = "Basic " + basicAuth(username, password)
	}
	r := &OpenSearchRepository{client: c, index: index, metadata: metadata}
	r.base.Embedder = embedder
	if err := r.InitRepository(context.Background()); err != nil {
		return nil, err
	}
	return r, nil
}

// InitRepository 建索引（index.knn:true + knn_vector hsnw）。
func (r *OpenSearchRepository) InitRepository(ctx context.Context) error {
	body, status, _ := r.client.Get(ctx, "/"+r.index)
	if status == 200 && strings.Contains(string(body), "\""+r.index+"\"") {
		return nil
	}
	dims, err := r.base.Embedder.Dimensions(ctx)
	if err != nil {
		return err
	}
	props := map[string]any{
		"content":  map[string]any{"type": "text"},
		"metadata": map[string]any{"type": "object"},
		"embedding": map[string]any{
			"type": "knn_vector", "dimension": dims,
			"method": map[string]any{"name": "hnsw", "space_type": "cosinesimil",
				"engine": "nmslib", "parameters": map[string]any{"ef_construction": 512, "m": 16}},
		},
	}
	for _, f := range r.metadata {
		props[f.Name] = map[string]any{"type": "keyword"}
	}
	_, st, err := r.client.Post(ctx, "/"+r.index, ragrepo.JSONBytes(map[string]any{
		"settings": map[string]any{"index": map[string]any{"knn": true, "number_of_shards": 1, "number_of_replicas": 0}},
		"mappings": map[string]any{"properties": props},
	}))
	if err != nil {
		return err
	}
	if st >= 400 {
		return fmt.Errorf("ragrepo/opensearch: create index status %d", st)
	}
	return nil
}

// SearchQuery 条件检索（顶层 knn + filter——opensearch 的 knn 内联 filter）。
func (r *OpenSearchRepository) SearchQuery(ctx context.Context, q *rag.Query) ([]*rag.Document, error) {
	if q == nil || q.Query == "" {
		return nil, fmt.Errorf("ragrepo/opensearch: query text cannot be empty")
	}
	vec, err := r.base.Embedder.Embed(ctx, q.Query)
	if err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = rag.DefaultLimit
	}
	body := map[string]any{"size": limit}
	knn := map[string]any{"vector": vec, "k": limit}
	if q.FilterExpr != "" {
		if node, ok := ragrepo.ParseFilterExpr(q.FilterExpr); ok {
			if m := ToOpenSearchQuery(node); m != nil {
				knn["filter"] = m
			}
		}
	}
	body["query"] = map[string]any{"knn": map[string]any{"embedding": knn}}

	resp, status, err := r.client.Post(ctx, "/"+r.index+"/_search", ragrepo.JSONBytes(body))
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		return nil, fmt.Errorf("ragrepo/opensearch: search status %d: %s", status, resp)
	}
	var parsed struct {
		Hits struct {
			Hits []struct {
				ID     string         `json:"_id"`
				Score  *float64        `json:"_score"`
				Source map[string]any  `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(resp, &parsed); err != nil {
		return nil, err
	}
	var docs []*rag.Document
	for i := range parsed.Hits.Hits {
		hit := &parsed.Hits.Hits[i]
		doc := rag.NewDocument("")
		doc.ID = hit.ID
		if hit.Source != nil {
			doc.Content, _ = hit.Source["content"].(string)
			if m, ok := hit.Source["metadata"].(map[string]any); ok {
				doc.Metadata = m
			}
		}
		if hit.Score != nil {
			doc.SetScore(*hit.Score)
		}
		docs = append(docs, doc)
	}
	return ragrepo.Refilter(docs, q)
}

// Search 简单检索。
func (r *OpenSearchRepository) Search(ctx context.Context, query string) ([]*rag.Document, error) {
	return r.SearchQuery(ctx, &rag.Query{Query: query})
}

// Save 批量写入（_bulk NDJSON 逐条拼）。
func (r *OpenSearchRepository) Save(ctx context.Context, docs []*rag.Document, progress func(done, total int)) error {
	return r.base.SaveBatches(ctx, docs, func(ctx context.Context, batch []*rag.Document) error {
		var sb strings.Builder
		for _, d := range batch {
			meta := map[string]any{}
			for k, v := range d.Metadata {
				meta[k] = v
			}
			source := map[string]any{"content": d.Content, "metadata": meta, "embedding": d.Embedding}
			if u := d.URL(); u != "" {
				source["url"] = u
			}
			action, _ := json.Marshal(map[string]any{"index": map[string]any{"_index": r.index, "_id": d.ID}})
			doc, _ := json.Marshal(source)
			sb.Write(action)
			sb.WriteByte('\n')
			sb.Write(doc)
			sb.WriteByte('\n')
		}
		_, _, err := r.client.Post(ctx, "/_bulk", []byte(sb.String()))
		r.client.Post(ctx, "/"+r.index+"/_refresh", []byte("{}"))
		return err
	}, progress)
}

// ToOpenSearchQuery 过滤节点 → opensearch DSL（eq→term、gt..→range、AND/OR→bool）。
func ToOpenSearchQuery(n ragrepo.FilterNode) map[string]any {
	switch node := n.(type) {
	case *ragrepo.CmpNode:
		switch node.Op {
		case "==":
			return map[string]any{"term": map[string]any{node.Field: node.Value}}
		case "!=":
			return map[string]any{"bool": map[string]any{"must_not": []any{map[string]any{"term": map[string]any{node.Field: node.Value}}}}}
		case ">", ">=", "<", "<=":
			op := map[string]string{">": "gt", ">=": "gte", "<": "lt", "<=": "lte"}[node.Op]
			return map[string]any{"range": map[string]any{node.Field: map[string]any{op: node.Value}}}
		}
	case *ragrepo.NotNode:
		if inner := ToOpenSearchQuery(node.Inner); inner != nil {
			return map[string]any{"bool": map[string]any{"must_not": []any{inner}}}
		}
	case *ragrepo.AndNode:
		l, rr := ToOpenSearchQuery(node.Left), ToOpenSearchQuery(node.Right)
		if l != nil && rr != nil {
			return map[string]any{"bool": map[string]any{"must": []any{l, rr}}}
		}
	case *ragrepo.OrNode:
		l, rr := ToOpenSearchQuery(node.Left), ToOpenSearchQuery(node.Right)
		if l != nil && rr != nil {
			return map[string]any{"bool": map[string]any{"should": []any{l, rr}}}
		}
	}
	return nil
}

func basicAuth(username, password string) string {
	return username + ":" + password // stub（真实 base64 由使用方头注入）
}
