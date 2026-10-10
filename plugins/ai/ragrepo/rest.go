// rest.go REST 类向量仓储后端（对标 chroma/dashvector/dashvector-official/
// opensearch/weaviate/tcvectordb/vectorex 七家的 Go 统一实现）：统一走
// ai/httpx（零新增依赖），子包各自实现端点/请求形状差异。协议共性：
// init 建集合 → save 批量 upsert → search 服务端 ANN → refilter 客户端
// 二段过滤（阈值/表达式/limit）。
package ragrepo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/rag"
)

// RESTClient 统一 REST 缝（httpx.Client + BaseURL + 头）。
type RESTClient struct {
	BaseURL string
	Header  map[string]string
	client  interface {
		Get(ctx context.Context, rawURL string, header map[string]string) ([]byte, int, error)
		PostJSON(ctx context.Context, rawURL string, body []byte, header map[string]string) ([]byte, int, error)
	}
}

// NewRESTClient 构造。
func NewRESTClient(baseURL string) *RESTClient {
	return &RESTClient{BaseURL: baseURL, Header: map[string]string{}}
}

// SetClient 注入测试客户端（缺省 httpx.New()）。
func (c *RESTClient) SetClient(get func(ctx context.Context, rawURL string, header map[string]string) ([]byte, int, error),
	post func(ctx context.Context, rawURL string, body []byte, header map[string]string) ([]byte, int, error)) {
	c.client = &clientShim{get: get, post: post}
}

type clientShim struct {
	get  func(ctx context.Context, rawURL string, header map[string]string) ([]byte, int, error)
	post func(ctx context.Context, rawURL string, body []byte, header map[string]string) ([]byte, int, error)
}

func (s *clientShim) Get(ctx context.Context, u string, h map[string]string) ([]byte, int, error) {
	return s.get(ctx, u, h)
}
func (s *clientShim) PostJSON(ctx context.Context, u string, b []byte, h map[string]string) ([]byte, int, error) {
	return s.post(ctx, u, b, h)
}

// mergeHeader 请求头合并。
func (c *RESTClient) mergeHeader(extra map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range c.Header {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// Get GET 请求。
func (c *RESTClient) Get(ctx context.Context, path string) ([]byte, int, error) {
	return c.client.Get(ctx, c.BaseURL+path, c.mergeHeader(nil))
}

// Post POST JSON。
func (c *RESTClient) Post(ctx context.Context, path string, body []byte) ([]byte, int, error) {
	return c.client.PostJSON(ctx, c.BaseURL+path, body, c.mergeHeader(map[string]string{"Content-Type": "application/json"}))
}

// Put PUT（经 Post 通道的形状差异由子包拼 path 时表达）。
func (c *RESTClient) Delete(ctx context.Context, path string, body []byte) ([]byte, int, error) {
	if body == nil {
		return c.Post(ctx, path, []byte("{}"))
	}
	return c.Post(ctx, path, body)
}

// RestEmbedder 嵌入面（与 es/pgvector 相同的窄接口）。
type RestEmbedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedDocs(ctx context.Context, docs []*rag.Document) error
	Dimensions(ctx context.Context) (int, error)
	BatchSize() int
}

// RestRepositoryBase REST 后端共用基座：save 分批嵌入 + 进度回调、
// search 后 Refilter、id 补齐。
type RestRepositoryBase struct {
	// Embedder 嵌入面（子包构造时注入）。
	Embedder RestEmbedder
	mu       sync.Mutex
}

// SaveBatches 分批保存（子包提供 upsert 一批的回调）。
func (b *RestRepositoryBase) SaveBatches(ctx context.Context, docs []*rag.Document,
	upsert func(ctx context.Context, batch []*rag.Document) error,
	progress func(done, total int)) error {
	if len(docs) == 0 {
		if progress != nil {
			progress(0, 0)
		}
		return nil
	}
	for _, doc := range docs {
		if doc.ID == "" {
			doc.ID = NewUUID()
		}
	}
	batchSize := b.Embedder.BatchSize()
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
		if err := b.Embedder.EmbedDocs(ctx, batch); err != nil {
			return err
		}
		if err := upsert(ctx, batch); err != nil {
			return err
		}
		if progress != nil {
			progress(i+1, batches)
		}
	}
	return nil
}

// Refilter 命中后统一客户端二段过滤。
func Refilter(docs []*rag.Document, q *rag.Query) ([]*rag.Document, error) {
	return rag.Refilter(docs, q)
}

// ClampScore 分数钳制 [0,1]（chroma/vectorex 的 1-clamp(distance) 形态）。
func ClampScore(v float64) float64 {
	return math.Max(0, math.Min(1, v))
}

// jsonBytes 序列化便捷。
func JSONBytes(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}

var _ = bytes.MinRead
var _ = fmt.Sprintf
var _ = ai.Config{}
