// memory.go 内存知识库（对标 InMemoryRepository）：Storer + Lifecycle 全实现，
// 测试与演示的默认仓储；检索为暴力余弦。
package rag

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"github.com/crazy-airhead/aifei-go/ai/embedding"
)

// Embedder 嵌入模型缝（*embedding.Model 满足之；测试与插件注入假实现——
// Go 无接口化 EmbeddingModel，rag 自持窄接口）。
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedTexts(ctx context.Context, texts []string) ([][]float32, error)
	BatchSize() int
}

var _ Embedder = (*embedding.Model)(nil)

// InMemoryRepository 内存知识库。
type InMemoryRepository struct {
	embedder Embedder
	mu       sync.RWMutex
	store    map[string]*Document
}

// NewInMemoryRepository 建仓储（e 为 nil 时保存/检索报错——晚失败优于构造
// panic，可选装配路径友好）。
func NewInMemoryRepository(e Embedder) *InMemoryRepository {
	return &InMemoryRepository{embedder: e, store: map[string]*Document{}}
}

// Search 简单检索（缺省条件）。
func (r *InMemoryRepository) Search(ctx context.Context, query string) ([]*Document, error) {
	return r.SearchQuery(ctx, NewQuery(query))
}

// SearchQuery 条件检索：嵌入查询 → 全量余弦打分（副本，不动存量得分）→
// Refilter。SearchType 被忽略（内存仓储只有向量一路——Java 同款）。
func (r *InMemoryRepository) SearchQuery(ctx context.Context, q *Query) ([]*Document, error) {
	if r.embedder == nil {
		return nil, errors.New("rag: no embedding model")
	}
	if q == nil {
		q = NewQuery("")
	}
	qv, err := r.embedder.Embed(ctx, q.Query)
	if err != nil {
		return nil, err
	}
	r.mu.RLock()
	docs := make([]*Document, 0, len(r.store))
	for _, d := range r.store {
		docs = append(docs, d)
	}
	r.mu.RUnlock()

	scored := make([]*Document, 0, len(docs))
	for _, d := range docs {
		if len(d.Embedding) == 0 {
			return nil, fmt.Errorf("rag: document %q has no embedding", d.ID)
		}
		s, err := CosineSimilarity(qv, d.Embedding)
		if err != nil {
			return nil, err
		}
		scored = append(scored, CopyAndScore(d, s))
	}
	return Refilter(scored, q)
}

// Save 保存文档：按嵌入批次大小分批，逐批嵌入（回填 Embedding）→ 空位 ID
// 补随机 ID → 入表；progress 为批次粒度（done/total，空集时回调 (0,0)）。
func (r *InMemoryRepository) Save(ctx context.Context, docs []*Document, progress func(done, total int)) error {
	if len(docs) == 0 {
		if progress != nil {
			progress(0, 0)
		}
		return nil
	}
	if r.embedder == nil {
		return errors.New("rag: no embedding model")
	}
	batches := Partition(docs, r.embedder.BatchSize())
	for i, batch := range batches {
		texts := make([]string, len(batch))
		for j, d := range batch {
			texts[j] = d.Content
		}
		vecs, err := r.embedder.EmbedTexts(ctx, texts)
		if err != nil {
			return err
		}
		r.mu.Lock()
		for j, d := range batch {
			d.Embedding = vecs[j]
			if d.ID == "" {
				d.ID = newDocID()
			}
			r.store[d.ID] = d
		}
		r.mu.Unlock()
		if progress != nil {
			progress(i+1, len(batches))
		}
	}
	return nil
}

// DeleteByID 按 ID 删除。
func (r *InMemoryRepository) DeleteByID(_ context.Context, ids ...string) error {
	r.mu.Lock()
	for _, id := range ids {
		delete(r.store, id)
	}
	r.mu.Unlock()
	return nil
}

// ExistsByID 是否存在。
func (r *InMemoryRepository) ExistsByID(_ context.Context, id string) (bool, error) {
	r.mu.RLock()
	_, ok := r.store[id]
	r.mu.RUnlock()
	return ok, nil
}

// InitRepository 空操作（内存仓储无需初始化——Java 同款）。
func (r *InMemoryRepository) InitRepository(context.Context) error { return nil }

// DropRepository 清空。
func (r *InMemoryRepository) DropRepository(context.Context) error {
	r.mu.Lock()
	r.store = map[string]*Document{}
	r.mu.Unlock()
	return nil
}

var (
	_ Repository = (*InMemoryRepository)(nil)
	_ Storer     = (*InMemoryRepository)(nil)
	_ Lifecycle  = (*InMemoryRepository)(nil)
)

// newDocID 随机文档 ID（Java Utils.uuid 对应物，crypto/rand v4 形态）。
func newDocID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand 失败即系统级故障
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
