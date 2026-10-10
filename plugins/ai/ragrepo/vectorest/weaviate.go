// weaviate.go Weaviate 向量仓储（对标 solon-ai-repo-weaviate：REST 建类 +
// batch/objects 写入 + GraphQL nearVector 检索 + certainty 得分；类名首字母
// 强制大写；vectorizer "none"）。
package vectorest

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/crazy-airhead/aifei-go/ai/rag"
	ragrepo "github.com/crazy-airhead/aifei-go/plugins/ai/ragrepo"
)

// WeaviateRepository Weaviate 后端。
type WeaviateRepository struct {
	base       ragrepo.RestRepositoryBase
	client     *ragrepo.RESTClient
	className  string
	metadata   []ragrepo.MetadataFieldX
}

// NewWeaviateRepository 构造（类名首字母自动大写——Java 同款）。
func NewWeaviateRepository(baseURL string, embedder Embedder, class string, metadata ...ragrepo.MetadataFieldX) (*WeaviateRepository, error) {
	r := &WeaviateRepository{
		client:    ragrepo.NewRESTClient(baseURL),
		className: capitalizeFirst(class),
		metadata:  metadata,
	}
	r.base.Embedder = embedder
	if err := r.InitRepository(context.Background()); err != nil {
		return nil, err
	}
	return r, nil
}

func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// InitRepository 建类（POST /v1/schema；properties content/url + 元数据）。
func (r *WeaviateRepository) InitRepository(ctx context.Context) error {
	props := []map[string]any{
		{"name": "content", "dataType": []string{"text"}},
		{"name": "url", "dataType": []string{"string"}},
	}
	for _, f := range r.metadata {
		props = append(props, map[string]any{"name": f.Name, "dataType": []string{"string"}})
	}
	_, status, err := r.client.Post(ctx, "/v1/schema", ragrepo.JSONBytes(map[string]any{
		"class":        r.className,
		"properties":   props,
		"vectorizer":   "none",
		"vectorIndexConfig": map[string]any{"skip": false, "indexType": "hnsw",
			"hnsw": map[string]any{"distance": "cosine"}},
	}))
	if err != nil {
		return err
	}
	if status >= 400 {
		return fmt.Errorf("ragrepo/weaviate: create schema status %d", status)
	}
	return nil
}

// DropRepository 删类。
func (r *WeaviateRepository) DropRepository(ctx context.Context) error {
	_, _, err := r.client.Delete(ctx, "/v1/schema/"+r.className, nil)
	return err
}

// SearchQuery 条件检索（GraphQL nearVector + where 过滤 + certainty 得分）。
func (r *WeaviateRepository) SearchQuery(ctx context.Context, q *rag.Query) ([]*rag.Document, error) {
	if q == nil || q.Query == "" {
		return nil, fmt.Errorf("ragrepo/weaviate: query text cannot be empty")
	}
	vec, err := r.base.Embedder.Embed(ctx, q.Query)
	if err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = rag.DefaultLimit
	}
	fields := []string{"content", "url", "_additional { id certainty }"}
	for _, f := range r.metadata {
		fields = append(fields, f.Name)
	}
	gql := map[string]any{
		"query": fmt.Sprintf(`{ Get { %s(nearVector: {vector: %s}, limit: %d%s) { %s } } }`,
			r.className, vectorLiteralJSON(vec), limit, r.whereClause(q), strings.Join(fields, " ")),
	}
	resp, status, err := r.client.Post(ctx, "/v1/graphql", ragrepo.JSONBytes(gql))
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		return nil, fmt.Errorf("ragrepo/weaviate: graphql status %d: %s", status, resp)
	}
	var parsed struct {
		Data struct {
			Get []map[string]any `json:"Get"`
		} `json:"data"`
	}
	// Get 键为类名——动态解析
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(resp, &raw); err != nil {
		return nil, err
	}
	if dataRaw, ok := raw["data"]; ok {
		var data map[string]json.RawMessage
		if err := json.Unmarshal(dataRaw, &data); err == nil {
			if getRaw, ok := data["Get"]; ok {
				var get map[string][]map[string]any
				if err := json.Unmarshal(getRaw, &get); err == nil {
					for _, list := range get {
						parsed.Data.Get = append(parsed.Data.Get, list...)
					}
				}
			}
		}
	}
	var docs []*rag.Document
	for _, item := range parsed.Data.Get {
		doc := rag.NewDocument("")
		if c, ok := item["content"].(string); ok {
			doc.Content = c
		}
		if u, ok := item["url"].(string); ok && u != "" {
			doc.Meta("url", u)
		}
		for _, f := range r.metadata {
			if v, ok := item[f.Name]; ok {
				doc.Meta(f.Name, v)
			}
		}
		if add, ok := item["_additional"].(map[string]any); ok {
			if id, ok := add["id"].(string); ok {
				doc.ID = id
			}
			if cert, ok := add["certainty"].(float64); ok {
				doc.SetScore(cert) // certainty 已是 0-1 相似度
			}
		}
		docs = append(docs, doc)
	}
	sort.SliceStable(docs, func(i, j int) bool { return docs[i].Score() > docs[j].Score() })
	return ragrepo.Refilter(docs, q)
}

// Search 简单检索。
func (r *WeaviateRepository) Search(ctx context.Context, query string) ([]*rag.Document, error) {
	return r.SearchQuery(ctx, &rag.Query{Query: query})
}

// whereClause GraphQL where 子句（operator 枚举不加引号）。
func (r *WeaviateRepository) whereClause(q *rag.Query) string {
	if q.FilterExpr == "" {
		return ""
	}
	node, ok := ragrepo.ParseFilterExpr(q.FilterExpr)
	if !ok {
		return ""
	}
	where := toWeaviateWhere(node)
	if where == "" {
		return ""
	}
	return ", where: " + where
}

func toWeaviateWhere(n ragrepo.FilterNode) string {
	switch node := n.(type) {
	case *ragrepo.CmpNode:
		op := map[string]string{"==": "Equal", "!=": "NotEqual", ">": "GreaterThan",
			">=": "GreaterThanEqual", "<": "LessThan", "<=": "LessThanEqual"}[node.Op]
		val := graphqlValue(node.Value)
		return fmt.Sprintf("{operator: %s, path: [%q], value%s: %s}",
			op, node.Field, valueSuffix(node.Value), val)
	case *ragrepo.NotNode:
		return fmt.Sprintf("{operator: Not, operands: [%s]}", toWeaviateWhere(node.Inner))
	case *ragrepo.AndNode:
		return fmt.Sprintf("{operator: And, operands: [%s, %s]}",
			toWeaviateWhere(node.Left), toWeaviateWhere(node.Right))
	case *ragrepo.OrNode:
		return fmt.Sprintf("{operator: Or, operands: [%s, %s]}",
			toWeaviateWhere(node.Left), toWeaviateWhere(node.Right))
	}
	return ""
}

func valueSuffix(v any) string {
	switch v.(type) {
	case string:
		return "String"
	case bool:
		return "Boolean"
	case float64:
		return "Number"
	}
	return "String"
}

func graphqlValue(v any) string {
	switch x := v.(type) {
	case string:
		return `"` + strings.ReplaceAll(x, `"`, `\"`) + `"`
	default:
		return fmt.Sprint(v)
	}
}

func vectorLiteralJSON(vec []float32) string {
	parts := make([]string, len(vec))
	for i, v := range vec {
		parts[i] = fmt.Sprintf("%g", v)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// Save 批量写入（POST /v1/batch/objects）。
func (r *WeaviateRepository) Save(ctx context.Context, docs []*rag.Document, progress func(done, total int)) error {
	return r.base.SaveBatches(ctx, docs, func(ctx context.Context, batch []*rag.Document) error {
		objects := make([]map[string]any, len(batch))
		for i, d := range batch {
			props := map[string]any{"content": d.Content}
			if u := d.URL(); u != "" {
				props["url"] = u
			}
			for k, v := range d.Metadata {
				props[k] = v
			}
			vec := make([]float64, len(d.Embedding)) // weaviate 向量为 double
			for j, f := range d.Embedding {
				vec[j] = float64(f)
			}
			objects[i] = map[string]any{"id": d.ID, "class": r.className, "properties": props, "vector": vec}
		}
		body, status, err := r.client.Post(ctx, "/v1/batch/objects", ragrepo.JSONBytes(map[string]any{"objects": objects}))
		if err != nil {
			return err
		}
		if status >= 400 || strings.Contains(string(body), "\"errors\"") {
			return fmt.Errorf("ragrepo/weaviate: batch status %d", status)
		}
		return nil
	}, progress)
}

// DeleteByID 删除。
func (r *WeaviateRepository) DeleteByID(ctx context.Context, ids ...string) error {
	for _, id := range ids {
		if _, _, err := r.client.Delete(ctx, "/v1/objects/"+r.className+"/"+id, nil); err != nil {
			return err
		}
	}
	return nil
}

// ExistsByID 存在性。
func (r *WeaviateRepository) ExistsByID(ctx context.Context, id string) (bool, error) {
	body, status, err := r.client.Get(ctx, "/v1/objects/"+r.className+"/"+id)
	if err != nil || status >= 400 {
		return false, err
	}
	return strings.Contains(string(body), `"class"`), nil
}
