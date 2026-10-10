// Package dashvector DashVector 向量仓储（对标 solon-ai-repo-dashvector 与
// dashvector-official：REST 形态统一——官方 SDK 即该 REST 的封装，Go 侧一套
// 实现覆盖两模块；dashvector-auth-token 头、fields_schema 承载 content/url、
// SQL 风格 filter 字符串）。
package dashvector

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai/rag"
	ragrepo "github.com/crazy-airhead/aifei-go/plugins/ai/ragrepo"
)

// DashVectorRepository DashVector 后端（REST + 官方 SDK 双形态）。
type DashVectorRepository struct {
	base    ragrepo.RestRepositoryBase
	client  *ragrepo.RESTClient
	apiKey  string
	collection string
}

// NewDashVectorRepository 构造（baseURL 形如 https://dashvector.aliyuncs.com；
// apiKey 经 dashvector-auth-token 头）。
func NewDashVectorRepository(baseURL, apiKey string, model ragrepo.RestEmbedder, collection string) (*DashVectorRepository, error) {
	c := ragrepo.NewRESTClient(baseURL)
	c.Header["dashvector-auth-token"] = apiKey
	r := &DashVectorRepository{client: c, apiKey: apiKey, collection: collection}
	r.base.Embedder = model
	if err := r.InitRepository(context.Background()); err != nil {
		return nil, err
	}
	return r, nil
}

// InitRepository 建 collection（fields_schema：__content/__url + 维度）。
func (r *DashVectorRepository) InitRepository(ctx context.Context) error {
	dims, err := r.base.Embedder.Dimensions(ctx)
	if err != nil {
		return err
	}
	body, status, err := r.client.Post(ctx, "/v1/collections", ragrepo.JSONBytes(map[string]any{
		"name":      r.collection,
		"dimension": dims,
		"dataType":  "FLOAT",
		"metric":    "cosine",
		"fields_schema": map[string]any{
			"__content": "STRING",
			"__url":     "STRING",
		},
	}))
	if err != nil {
		return fmt.Errorf("ragrepo/dashvector: create collection: %w", err)
	}
	// 已存在（重复创建）容错
	if status >= 400 && !strings.Contains(string(body), "already exist") {
		return fmt.Errorf("ragrepo/dashvector: create collection status %d: %s", status, body)
	}
	return nil
}

// DropRepository 删 collection。
func (r *DashVectorRepository) DropRepository(ctx context.Context) error {
	_, _, err := r.client.Delete(ctx, "/v1/collections/"+r.collection, nil)
	return err
}

// Search 简单检索。
func (r *DashVectorRepository) Search(ctx context.Context, query string) ([]*rag.Document, error) {
	return r.SearchQuery(ctx, &rag.Query{Query: query})
}

// SearchQuery 条件检索。
func (r *DashVectorRepository) SearchQuery(ctx context.Context, q *rag.Query) ([]*rag.Document, error) {
	if q == nil || q.Query == "" {
		return nil, fmt.Errorf("ragrepo/dashvector: query text cannot be empty")
	}
	vec, err := r.base.Embedder.Embed(ctx, q.Query)
	if err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = rag.DefaultLimit
	}
	filter := ""
	if q.FilterExpr != "" {
		if node, ok := ragrepo.ParseFilterExpr(q.FilterExpr); ok {
			filter = ToDashVectorFilter(node)
		}
	}
	req := map[string]any{"vector": vec, "topk": limit}
	if filter != "" {
		req["filter"] = filter
	}
	body, status, err := r.client.Post(ctx, "/v1/collections/"+r.collection+"/query", ragrepo.JSONBytes(req))
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		return nil, fmt.Errorf("ragrepo/dashvector: query status %d: %s", status, body)
	}
	var resp struct {
		Output []struct {
			ID     string         `json:"id"`
			Vector []float32      `json:"vector"`
			Score  float64        `json:"score"`
			Fields map[string]any `json:"fields"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("ragrepo/dashvector: response parse: %w", err)
	}
	var docs []*rag.Document
	for _, hit := range resp.Output {
		doc := rag.NewDocument("")
		doc.ID = hit.ID
		if c, ok := hit.Fields["__content"].(string); ok {
			doc.Content = c
		}
		if u, ok := hit.Fields["__url"].(string); ok && u != "" {
			doc.Meta("url", u)
		}
		for k, v := range hit.Fields {
			if k != "__content" && k != "__url" {
				doc.Meta(k, v)
			}
		}
		doc.SetScore(ragrepo.ClampScore(1 - hit.Score))
		docs = append(docs, doc)
	}
	return ragrepo.Refilter(docs, q)
}

// Save 批量写入（fields 平铺）。
func (r *DashVectorRepository) Save(ctx context.Context, docs []*rag.Document, progress func(done, total int)) error {
	return r.base.SaveBatches(ctx, docs, func(ctx context.Context, batch []*rag.Document) error {
		items := make([]map[string]any, len(batch))
		for i, d := range batch {
			fields := map[string]any{"__content": d.Content}
			if u := d.URL(); u != "" {
				fields["__url"] = u
			}
			for k, v := range d.Metadata {
				fields[k] = v
			}
			items[i] = map[string]any{"id": d.ID, "vector": d.Embedding, "fields": fields}
		}
		body, status, err := r.client.Post(ctx, "/v1/collections/"+r.collection+"/docs",
			ragrepo.JSONBytes(map[string]any{"docs": items}))
		if err != nil {
			return err
		}
		if status >= 400 {
			return fmt.Errorf("ragrepo/dashvector: upsert status %d: %s", status, body)
		}
		return nil
	}, progress)
}

// DeleteByID 删除。
func (r *DashVectorRepository) DeleteByID(ctx context.Context, ids ...string) error {
	_, _, err := r.client.Post(ctx, "/v1/collections/"+r.collection+"/docs/delete",
		ragrepo.JSONBytes(map[string]any{"ids": ids}))
	return err
}

// ExistsByID 存在性（GET docs?ids=）。
func (r *DashVectorRepository) ExistsByID(ctx context.Context, id string) (bool, error) {
	body, status, err := r.client.Get(ctx, "/v1/collections/"+r.collection+"/docs?ids="+id)
	if err != nil || status >= 400 {
		return false, err
	}
	var resp struct {
		Output []map[string]any `json:"output"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return false, nil
	}
	return len(resp.Output) > 0, nil
}

// ToDashVectorFilter 过滤节点 → SQL 风格字符串（单引号转义——对标
// DashVectorFilterTransformer）。
func ToDashVectorFilter(n ragrepo.FilterNode) string {
	switch node := n.(type) {
	case *ragrepo.CmpNode:
		op := map[string]string{"==": "=", "!=": "!=", ">": ">", ">=": ">=", "<": "<", "<=": "<="}[node.Op]
		return fmt.Sprintf("%s %s %s", node.Field, op, quoteDv(node.Value))
	case *ragrepo.NotNode:
		return "not (" + ToDashVectorFilter(node.Inner) + ")"
	case *ragrepo.AndNode:
		return "(" + ToDashVectorFilter(node.Left) + " and " + ToDashVectorFilter(node.Right) + ")"
	case *ragrepo.OrNode:
		return "(" + ToDashVectorFilter(node.Left) + " or " + ToDashVectorFilter(node.Right) + ")"
	}
	return ""
}

func quoteDv(v any) string {
	switch x := v.(type) {
	case string:
		return "'" + strings.ReplaceAll(x, "'", `\'`) + "'"
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprint(v)
	}
}
