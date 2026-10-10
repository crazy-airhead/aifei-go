// Package redisrepo RediSearch 向量仓储（对标 solon-ai-repo-redis：FT.CREATE
// ON JSON VECTOR HNSW FLOAT32 COSINE + FT.SEARCH KNN Dialect2 + JSON.SET，
// 经 go-redis 原生命令——与 plugins/cache 同一客户端，不引入第二套）。
package redisrepo

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/crazy-airhead/aifei-go/ai/rag"
	ragrepo "github.com/crazy-airhead/aifei-go/plugins/ai/ragrepo"
)

// RedisEmbedder 嵌入面。
type RedisEmbedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedDocs(ctx context.Context, docs []*rag.Document) error
	Dimensions(ctx context.Context) (int, error)
	BatchSize() int
}

// RedisRepository RediSearch 后端。
type RedisRepository struct {
	client    redis.UniversalClient
	embedder  RedisEmbedder
	index     string
	keyPrefix string
	metadata  []ragrepo.MetadataFieldX
}

// New 构造（index 名 + key 前缀）。
func New(client redis.UniversalClient, embedder RedisEmbedder, index, keyPrefix string, metadata ...ragrepo.MetadataFieldX) (*RedisRepository, error) {
	r := &RedisRepository{client: client, embedder: embedder, index: index, keyPrefix: keyPrefix, metadata: metadata}
	if err := r.InitRepository(context.Background()); err != nil {
		return nil, err
	}
	return r, nil
}

// InitRepository FT.CREATE（已存在则跳过）。
func (r *RedisRepository) InitRepository(ctx context.Context) error {
	if err := r.client.Do(ctx, "FT.INFO", r.index).Err(); err == nil {
		return nil // 已存在
	}
	dims, err := r.embedder.Dimensions(ctx)
	if err != nil {
		return err
	}
	args := []any{"FT.CREATE", r.index, "ON", "JSON", "PREFIX", "1", r.keyPrefix, "SCHEMA",
		"$.embedding", "AS", "embedding", "VECTOR", "HNSW", "6",
		"TYPE", "FLOAT32", "DIM", dims, "DISTANCE_METRIC", "COSINE"}
	for _, f := range r.metadata {
		args = append(args, "$."+f.Name, "AS", f.Name, string(sqlToRedisType(f.Type)))
	}
	if err := r.client.Do(ctx, args...).Err(); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			return nil
		}
		return fmt.Errorf("ragrepo/redis: ft.create: %w", err)
	}
	return nil
}

func sqlToRedisType(t ragrepo.FieldTypeX) string {
	switch t {
	case ragrepo.FieldNumericX:
		return "NUMERIC"
	case ragrepo.FieldJSONX:
		return "TEXT"
	default:
		return "TEXT"
	}
}

// DropRepository 删索引（不 FLUSHDB——Java 的危险操作在 Go 收敛掉）。
func (r *RedisRepository) DropRepository(ctx context.Context) error {
	if err := r.client.Do(ctx, "FT.DROPINDEX", r.index).Err(); err != nil &&
		!strings.Contains(err.Error(), "Unknown index") {
		return err
	}
	return nil
}

// Search 简单检索。
func (r *RedisRepository) Search(ctx context.Context, query string) ([]*rag.Document, error) {
	return r.SearchQuery(ctx, &rag.Query{Query: query})
}

// SearchQuery 条件检索（KNN + BLOB 小端字节；score = 1 - 距离）。
func (r *RedisRepository) SearchQuery(ctx context.Context, q *rag.Query) ([]*rag.Document, error) {
	if q == nil || q.Query == "" {
		return nil, fmt.Errorf("ragrepo/redis: query text cannot be empty")
	}
	vec, err := r.embedder.Embed(ctx, q.Query)
	if err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = rag.DefaultLimit
	}
	filter := "*"
	if q.FilterExpr != "" {
		if node, ok := ragrepo.ParseFilterExpr(q.FilterExpr); ok {
			filter = ToRedisFilter(node)
		}
	}
	blob := Float32LEBytes(vec)
	res, err := r.client.Do(ctx,
		"FT.SEARCH", r.index,
		fmt.Sprintf("%s=>[KNN %d @embedding $BLOB AS score]", filter, limit),
		"PARAMS", "2", "BLOB", blob,
		"SORTBY", "score", "LIMIT", "0", limit,
		"DIALECT", "2").Result()
	if err != nil {
		return nil, fmt.Errorf("ragrepo/redis: ft.search: %w", err)
	}
	docs, err := parseFTSearch(res)
	if err != nil {
		return nil, err
	}
	for _, doc := range docs {
		doc.SetScore(clamp01(1 - doc.Score()))
	}
	return ragrepo.Refilter(docs, q)
}

// parseFTSearch FT.SEARCH 响应（[total, key1, [field,val,...], ...]）。
func parseFTSearch(res any) ([]*rag.Document, error) {
	arr, ok := res.([]any)
	if !ok || len(arr) == 0 {
		return nil, nil
	}
	var docs []*rag.Document
	for i := 1; i+1 < len(arr); i += 2 {
		key, _ := arr[i].(string)
		fields, _ := arr[i+1].([]any)
		doc := rag.NewDocument("")
		doc.ID = strings.TrimPrefix(key, "")
		for j := 0; j+1 < len(fields); j += 2 {
			name, _ := fields[j].(string)
			value, _ := fields[j+1].(string)
			switch name {
			case "$.content":
				doc.Content = value
			case "$.metadata":
				_ = json.Unmarshal([]byte(value), &doc.Metadata)
			case "score":
				var f float64
				_, _ = fmt.Sscanf(value, "%g", &f)
				doc.SetScore(f)
			}
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

// Float32LEBytes 小端字节（RediSearch VECTOR BLOB 形态；导出供诊断/测试）。
func Float32LEBytes(vec []float32) []byte {
	out := make([]byte, len(vec)*4)
	for i, v := range vec {
		bits := math.Float32bits(v)
		out[i*4] = byte(bits)
		out[i*4+1] = byte(bits >> 8)
		out[i*4+2] = byte(bits >> 16)
		out[i*4+3] = byte(bits >> 24)
	}
	return out
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// Save 批量写入（JSON.SET pipeline）。
func (r *RedisRepository) Save(ctx context.Context, docs []*rag.Document, progress func(done, total int)) error {
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
	batchSize := r.embedder.BatchSize()
	if batchSize <= 0 {
		batchSize = 10
	}
	batches := (len(docs) + batchSize - 1) / batchSize
	for i := 0; i < batches; i++ {
		end := (i + 1) * batchSize
		if end > len(docs) {
			end = len(docs)
		}
		batch := docs[i*batchSize : end]
		if err := r.embedder.EmbedDocs(ctx, batch); err != nil {
			return err
		}
		pipe := r.client.Pipeline()
		for _, doc := range batch {
			obj := map[string]any{
				"content":   doc.Content,
				"embedding": doc.Embedding,
				"metadata":  doc.Metadata,
			}
			for k, v := range doc.Metadata {
				obj[k] = v
			}
			b, _ := json.Marshal(obj)
			pipe.Do(ctx, "JSON.SET", r.keyPrefix+doc.ID, "$", string(b))
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return fmt.Errorf("ragrepo/redis: json.set: %w", err)
		}
		if progress != nil {
			progress(i+1, batches)
		}
	}
	return nil
}

// DeleteByID 删除。
func (r *RedisRepository) DeleteByID(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = r.keyPrefix + id
	}
	return r.client.Del(ctx, keys...).Err()
}

// ExistsByID 存在性。
func (r *RedisRepository) ExistsByID(ctx context.Context, id string) (bool, error) {
	n, err := r.client.Exists(ctx, r.keyPrefix+id).Result()
	return n > 0, err
}

// ToRedisFilter 过滤节点 → RediSearch 语法（@field:{val}、区间 [v +inf]、
// AND 空格、OR |、NOT - 前缀——对标 RedisFilterTransformer）。
func ToRedisFilter(n ragrepo.FilterNode) string {
	switch node := n.(type) {
	case *ragrepo.CmpNode:
		switch node.Op {
		case "==":
			return "@" + node.Field + ":{" + redisQuote(node.Value) + "}"
		case "!=":
			return "-@" + node.Field + ":{" + redisQuote(node.Value) + "}"
		case ">", ">=":
			return "@" + node.Field + ":[" + redisNum(node.Value) + " +inf]"
		case "<", "<=":
			return "@" + node.Field + ":[-inf " + redisNum(node.Value) + "]"
		}
	case *ragrepo.NotNode:
		inner := ToRedisFilter(node.Inner)
		if strings.HasPrefix(inner, "-") {
			return inner[1:]
		}
		return "-(" + inner + ")"
	case *ragrepo.AndNode:
		return "(" + ToRedisFilter(node.Left) + " " + ToRedisFilter(node.Right) + ")"
	case *ragrepo.OrNode:
		return "(" + ToRedisFilter(node.Left) + " | " + ToRedisFilter(node.Right) + ")"
	}
	return "*"
}

func redisQuote(v any) string {
	switch x := v.(type) {
	case string:
		return x
	default:
		return fmt.Sprint(x)
	}
}

func redisNum(v any) string {
	return fmt.Sprint(v)
}
