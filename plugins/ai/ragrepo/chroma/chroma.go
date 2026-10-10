// Package chroma Chroma 向量仓储（对标 solon-ai-repo-chroma：REST v1/v2 自适应、
// collection by id、双层列表响应、where 字典过滤）。
package chroma

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai/rag"
	ragrepo "github.com/crazy-airhead/aifei-go/plugins/ai/ragrepo"
)

// ChromaRepository Chroma 后端。
type ChromaRepository struct {
	base   ragrepo.RestRepositoryBase
	client *ragrepo.RESTClient
	tenant, database string
	collectionID string
	collectionName string
}

// NewChromaRepository 构造（baseURL 形如 http://localhost:8000）。
func NewChromaRepository(baseURL string, model ragrepo.RestEmbedder, collectionName string) (*ChromaRepository, error) {
	r := &ChromaRepository{
		client:        ragrepo.NewRESTClient(baseURL),
		tenant:        "default_tenant",
		database:      "default_database",
		collectionName: collectionName,
	}
	r.base.Embedder = model
	if err := r.InitRepository(context.Background()); err != nil {
		return nil, err
	}
	return r, nil
}

// SetTenantDatabase v2 多租户面。
func (r *ChromaRepository) SetTenantDatabase(tenant, database string) *ChromaRepository {
	r.tenant, r.database = tenant, database
	return r
}

// chromaPrefix v2 探测（GET /api/v2/heartbeat 含 nanosecond heartbeat）。
func (r *ChromaRepository) chromaPrefix(ctx context.Context) string {
	body, status, _ := r.client.Get(ctx, "/api/v2/heartbeat")
	if status == 200 && strings.Contains(string(body), "nanosecond heartbeat") {
		return fmt.Sprintf("/api/v2/tenants/%s/databases/%s", r.tenant, r.database)
	}
	return "/api/v1"
}

// InitRepository 建 collection（404 → create；持 id）。
func (r *ChromaRepository) InitRepository(ctx context.Context) error {
	prefix := r.chromaPrefix(ctx)
	body, status, _ := r.client.Get(ctx, prefix+"/collections/"+r.collectionName)
	if status == 200 {
		var existing struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(body, &existing) == nil && existing.ID != "" {
			r.collectionID = existing.ID
			return nil
		}
	}
	create, status, err := r.client.Post(ctx, prefix+"/collections", ragrepo.JSONBytes(map[string]any{
		"name": r.collectionName,
		"metadata": map[string]any{
			"description": "aifei-go rag repository",
			"created_at":  "auto",
			"hnsw:space":  "cosine",
		},
	}))
	if err != nil {
		return fmt.Errorf("ragrepo/chroma: create collection: %w", err)
	}
	if status >= 400 {
		return fmt.Errorf("ragrepo/chroma: create collection status %d: %s", status, create)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create, &created); err != nil || created.ID == "" {
		return fmt.Errorf("ragrepo/chroma: create collection parse: %s", create)
	}
	r.collectionID = created.ID
	return nil
}

// DropRepository 删 collection。
func (r *ChromaRepository) DropRepository(ctx context.Context) error {
	prefix := r.chromaPrefix(ctx)
	_, _, err := r.client.Delete(ctx, prefix+"/collections/"+r.collectionID, nil)
	return err
}

// Search 简单检索。
func (r *ChromaRepository) Search(ctx context.Context, query string) ([]*rag.Document, error) {
	return r.SearchQuery(ctx, &rag.Query{Query: query})
}

// SearchQuery 条件检索（query → ANN topN → 双层列表展开 → refilter）。
func (r *ChromaRepository) SearchQuery(ctx context.Context, q *rag.Query) ([]*rag.Document, error) {
	if q == nil || q.Query == "" {
		return nil, fmt.Errorf("ragrepo/chroma: query text cannot be empty")
	}
	vec, err := r.base.Embedder.Embed(ctx, q.Query)
	if err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = rag.DefaultLimit
	}
	where := map[string]any{}
	if q.FilterExpr != "" {
		if node, ok := ragrepo.ParseFilterExpr(q.FilterExpr); ok {
			if m := ToChromaWhere(node); m != nil {
				where = m
			}
		}
	}
	req := map[string]any{
		"query_embeddings": [][]float32{vec},
		"n_results":        limit,
		"include":          []string{"documents", "metadatas", "distances"},
	}
	if len(where) > 0 {
		req["where"] = where
	}
	prefix := r.chromaPrefix(ctx)
	body, status, err := r.client.Post(ctx, prefix+"/collections/"+r.collectionID+"/query", ragrepo.JSONBytes(req))
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		return nil, fmt.Errorf("ragrepo/chroma: query status %d: %s", status, body)
	}
	var resp struct {
		IDs        [][]string   `json:"ids"`
		Documents  [][]string   `json:"documents"`
		Metadatas  [][]map[string]any `json:"metadatas"`
		Distances  [][]float64  `json:"distances"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("ragrepo/chroma: response parse: %w", err)
	}
	var docs []*rag.Document
	for row := range resp.IDs {
		for col, id := range resp.IDs[row] {
			doc := rag.NewDocument("")
			doc.ID = id
			if row < len(resp.Documents) && col < len(resp.Documents[row]) {
				doc.Content = resp.Documents[row][col]
			}
			if row < len(resp.Metadatas) && col < len(resp.Metadatas[row]) {
				doc.Metadata = resp.Metadatas[row][col]
			}
			if row < len(resp.Distances) && col < len(resp.Distances[row]) {
				doc.SetScore(ragrepo.ClampScore(1 - resp.Distances[row][col]))
			}
			docs = append(docs, doc)
		}
	}
	return ragrepo.Refilter(docs, q)
}

// Save 批量写入。
func (r *ChromaRepository) Save(ctx context.Context, docs []*rag.Document, progress func(done, total int)) error {
	prefix := r.chromaPrefix(ctx)
	return r.base.SaveBatches(ctx, docs, func(ctx context.Context, batch []*rag.Document) error {
		ids := make([]string, len(batch))
		docs2 := make([]string, len(batch))
		embeds := make([][]float32, len(batch))
		metas := make([]map[string]any, len(batch))
		for i, d := range batch {
			ids[i] = d.ID
			docs2[i] = d.Content
			embeds[i] = d.Embedding
			m := map[string]any{}
			for k, v := range d.Metadata {
				m[k] = v
			}
			if u := d.URL(); u != "" {
				m["url"] = u
			}
			metas[i] = m
		}
		body, status, err := r.client.Post(ctx, prefix+"/collections/"+r.collectionID+"/add",
			ragrepo.JSONBytes(map[string]any{"ids": ids, "embeddings": embeds, "documents": docs2, "metadatas": metas}))
		if err != nil {
			return err
		}
		if status >= 400 {
			return fmt.Errorf("ragrepo/chroma: add status %d: %s", status, body)
		}
		return nil
	}, progress)
}

// DeleteByID 删除。
func (r *ChromaRepository) DeleteByID(ctx context.Context, ids ...string) error {
	prefix := r.chromaPrefix(ctx)
	_, _, err := r.client.Post(ctx, prefix+"/collections/"+r.collectionID+"/delete", ragrepo.JSONBytes(map[string]any{"ids": ids}))
	return err
}

// ExistsByID 存在性（get 端点）。
func (r *ChromaRepository) ExistsByID(ctx context.Context, id string) (bool, error) {
	prefix := r.chromaPrefix(ctx)
	body, status, err := r.client.Post(ctx, prefix+"/collections/"+r.collectionID+"/get", ragrepo.JSONBytes(map[string]any{"ids": []string{id}}))
	if err != nil || status >= 400 {
		return false, err
	}
	var resp struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return false, nil
	}
	return len(resp.IDs) > 0, nil
}

// ToChromaWhere 过滤节点 → Chroma where 字典（eq 裸值、其余 $op、
// AND/OR/$not 组合——对标 ChromaFilterTransformer）。
func ToChromaWhere(n ragrepo.FilterNode) map[string]any {
	switch node := n.(type) {
	case *ragrepo.FieldNode:
		return map[string]any{node.Name: map[string]any{"$ne": ""}} // 存在性近似
	case *ragrepo.BoolNode:
		if node.Value {
			return map[string]any{}
		}
		return map[string]any{"$and": []any{map[string]any{"a": map[string]any{"$eq": 1}}, map[string]any{"a": map[string]any{"$ne": 1}}}}
	case *ragrepo.CmpNode:
		switch node.Op {
		case "==":
			return map[string]any{node.Field: node.Value}
		case "!=":
			return map[string]any{node.Field: map[string]any{"$ne": node.Value}}
		case ">":
			return map[string]any{node.Field: map[string]any{"$gt": node.Value}}
		case ">=":
			return map[string]any{node.Field: map[string]any{"$gte": node.Value}}
		case "<":
			return map[string]any{node.Field: map[string]any{"$lt": node.Value}}
		case "<=":
			return map[string]any{node.Field: map[string]any{"$lte": node.Value}}
		}
	case *ragrepo.NotNode:
		if inner := ToChromaWhere(node.Inner); len(inner) > 0 {
			return map[string]any{"$not": inner}
		}
	case *ragrepo.AndNode:
		l, r := ToChromaWhere(node.Left), ToChromaWhere(node.Right)
		if len(l) > 0 && len(r) > 0 {
			return map[string]any{"$and": []any{l, r}}
		}
	case *ragrepo.OrNode:
		l, r := ToChromaWhere(node.Left), ToChromaWhere(node.Right)
		if len(l) > 0 && len(r) > 0 {
			return map[string]any{"$or": []any{l, r}}
		}
	}
	return nil
}
