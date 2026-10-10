// query.go 检索条件（对标 QueryCondition）+ SearchType/Freshness + 相似度工具
// （对标 SimilarityUtil）+ 内存二次过滤（Refilter）。
package rag

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/crazy-airhead/aifei-go/enjoy"
)

// 缺省值（Java QueryCondition 常量）。
const (
	DefaultLimit               = 4
	DefaultSimilarityThreshold = 0.4
	// DefaultBatchSize 保存分批缺省（一切嵌入模型只接收 10 左右——Java
	// ListUtil.partition 注释同款）。
	DefaultBatchSize = 10
)

// SearchType 检索类型（Java 枚举；零值 = 向量 = 缺省，故 Vector 排首位）。
type SearchType uint8

const (
	SearchVector   SearchType = iota // 向量检索（缺省）
	SearchFullText                   // 全文检索
	SearchHybrid                     // 混合检索（HybridParams，后端消费）
)

func (t SearchType) String() string {
	switch t {
	case SearchFullText:
		return "full_text"
	case SearchHybrid:
		return "hybrid"
	default:
		return "vector"
	}
}

// HybridParams 混合检索权重（Java HybridSearchParams；两权重和为 1）。
type HybridParams struct {
	VectorWeight   float64
	FullTextWeight float64
}

// NewHybridParams 以向量权重构造（钳制 [0,1]，全文权重 = 1 − 向量权重；
// Java of() 对应物）。
func NewHybridParams(vectorWeight float64) *HybridParams {
	if vectorWeight < 0 {
		vectorWeight = 0
	} else if vectorWeight > 1 {
		vectorWeight = 1
	}
	return &HybridParams{VectorWeight: vectorWeight, FullTextWeight: 1 - vectorWeight}
}

// Freshness 时间窗过滤。Java 同名枚举（oneDay…noLimit）在 Java 侧无任何消费
// 方——Go 落成实际生效的内存过滤：文档时间取 Metadata["createdAt"]（接受
// time.Time / int64 Unix 秒 / RFC3339 字符串），窗口开启而无有效时间的文档被
// 排除（无时间即无法证明新鲜）。
type Freshness struct {
	Start time.Time // 含端点；零值 = 不限
	End   time.Time // 含端点；零值 = 不限
}

// NewFreshness 显式时间窗。
func NewFreshness(start, end time.Time) *Freshness { return &Freshness{Start: start, End: end} }

// LastDay 近一天（Java oneDay 对应物）。
func LastDay() *Freshness { return lastWindow(24 * time.Hour) }

// LastWeek 近一周（Java oneWeek）。
func LastWeek() *Freshness { return lastWindow(7 * 24 * time.Hour) }

// LastMonth 近一月（Java oneMonth）。
func LastMonth() *Freshness { return lastWindow(30 * 24 * time.Hour) }

// LastYear 近一年（Java oneYear）。
func LastYear() *Freshness { return lastWindow(365 * 24 * time.Hour) }

func lastWindow(d time.Duration) *Freshness {
	now := time.Now()
	return &Freshness{Start: now.Add(-d), End: now}
}

func (f *Freshness) contains(t time.Time) bool {
	if !f.Start.IsZero() && t.Before(f.Start) {
		return false
	}
	if !f.End.IsZero() && t.After(f.End) {
		return false
	}
	return true
}

// docTime 文档时间（createdAt 约定）。
func docTime(d *Document) (time.Time, bool) {
	switch v := d.Metadata["createdAt"].(type) {
	case time.Time:
		return v, true
	case int64:
		return time.Unix(v, 0), true
	case string:
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// Query 检索条件（对标 QueryCondition）。零值不安全——经 NewQuery 构造预填
// 缺省；Refilter 对 <=0 的 limit/threshold 亦有兜底（结构体字面量直造时）。
type Query struct {
	Query               string
	Limit               int                  // 缺省 4；<=0 视为缺省
	SimilarityThreshold float64              // 缺省 0.4；<=0 视为缺省
	Freshness           *Freshness           // 时间窗；nil = 不限
	FilterExpr          string               // enjoy 过滤表达式（Metadata 为作用域）
	FilterFunc          func(*Document) bool // 原生过滤（与 FilterExpr 叠加，Go 增量）
	DisableRefilter     bool                 // 关闭内存二次过滤（信任后端已过滤）
	SearchType          SearchType           // 缺省 SearchVector
	HybridParams        *HybridParams        // SearchHybrid 时生效；nil → 0.5/0.5
}

// NewQuery 建条件（缺省 limit=4 / threshold=0.4 / vector；Java 构造器字段
// 初始化对应物）。
func NewQuery(query string) *Query {
	return &Query{
		Query:               query,
		Limit:               DefaultLimit,
		SimilarityThreshold: DefaultSimilarityThreshold,
		SearchType:          SearchVector,
	}
}

// ---- 相似度工具（对标 SimilarityUtil；Java 抛异常 → Go 返错误）----

// CosineSimilarity 余弦相似度。nil 矢量 / 长度不等 / 零范数报错（Java 同款
// 三类 IllegalArgumentException）。
func CosineSimilarity(a, b []float32) (float64, error) {
	if a == nil || b == nil {
		return 0, fmt.Errorf("rag: embed must not be nil")
	}
	dot, err := DotProduct(a, b)
	if err != nil {
		return 0, err
	}
	na, _ := DotProduct(a, a)
	nb, _ := DotProduct(b, b)
	if na == 0 || nb == 0 {
		return 0, fmt.Errorf("rag: embed cannot be zero norm")
	}
	return dot / (sqrt64(na) * sqrt64(nb)), nil
}

// DotProduct 点积（长度不等报错）。
func DotProduct(a, b []float32) (float64, error) {
	if len(a) != len(b) {
		return 0, fmt.Errorf("rag: embed length must be equal (%d != %d)", len(a), len(b))
	}
	var sum float64
	for i := range a {
		sum += float64(a[i]) * float64(b[i])
	}
	return sum, nil
}

// EuclideanDistance 欧氏距离。
func EuclideanDistance(a, b []float32) (float64, error) {
	if a == nil || b == nil {
		return 0, fmt.Errorf("rag: embed must not be nil")
	}
	if len(a) != len(b) {
		return 0, fmt.Errorf("rag: embed length must be equal (%d != %d)", len(a), len(b))
	}
	var sum float64
	for i := range a {
		diff := float64(a[i]) - float64(b[i])
		sum += diff * diff
	}
	return sqrt64(sum), nil
}

// EuclideanSimilarity 欧氏相似度（1/(1+d)）。
func EuclideanSimilarity(a, b []float32) (float64, error) {
	d, err := EuclideanDistance(a, b)
	if err != nil {
		return 0, err
	}
	return 1 / (1 + d), nil
}

func sqrt64(v float64) float64 {
	if v <= 0 {
		return 0
	}
	return math.Sqrt(v)
}

// CopyAndScore 复制文档并打分（Java copyAndScore）：新 Metadata 表（后续对
// 原表写不波及副本；值浅共享），Embedding 切片共享（只读）。
func CopyAndScore(d *Document, score float64) *Document {
	cp := &Document{ID: d.ID, Content: d.Content, Embedding: d.Embedding,
		Metadata: make(map[string]any, len(d.Metadata))}
	for k, v := range d.Metadata {
		cp.Metadata[k] = v
	}
	cp.score = score
	return cp
}

// Partition 切片分片（Java ListUtil.partition；嵌入批次分批用）。
func Partition[T any](list []T, size int) [][]T {
	if size <= 0 {
		size = DefaultBatchSize
	}
	var out [][]T
	for i := 0; i < len(list); i += size {
		end := i + size
		if end > len(list) {
			end = len(list)
		}
		out = append(out, list[i:end])
	}
	return out
}

// ---- 内存二次过滤（对标 SimilarityUtil.refilter）----

// Refilter 阈值过滤 + Score 降序 + Limit 截断；DisableRefilter=false 时先施加
// FilterExpr（enjoy 求值，Metadata 为作用域）+ FilterFunc + Freshness。注意
// DisableRefilter 仅跳过前置过滤——阈值/排序/截断仍执行（与 Java 同名语义
// 一致：禁的是表达式重滤，非阈值重滤；InMemory 等无表达式翻译的后端，
// DisableRefilter=true 即完全信任调用方）。
func Refilter(docs []*Document, q *Query) ([]*Document, error) {
	if q == nil {
		q = NewQuery("")
	}
	limit, threshold := q.Limit, q.SimilarityThreshold
	if limit <= 0 {
		limit = DefaultLimit
	}
	if threshold <= 0 {
		threshold = DefaultSimilarityThreshold
	}

	if !q.DisableRefilter {
		var expr enjoy.Expr
		if q.FilterExpr != "" {
			var err error
			if expr, err = parseFilterExpr(q.FilterExpr); err != nil {
				return nil, err
			}
		}
		kept := make([]*Document, 0, len(docs))
		for _, d := range docs {
			if q.FilterFunc != nil && !q.FilterFunc(d) {
				continue
			}
			if expr != nil && !evalFilterExpr(expr, d) {
				continue
			}
			if q.Freshness != nil {
				t, ok := docTime(d)
				if !ok || !q.Freshness.contains(t) {
					continue
				}
			}
			kept = append(kept, d)
		}
		docs = kept
	}

	out := make([]*Document, 0, len(docs))
	for _, d := range docs {
		if d.score >= threshold {
			out = append(out, d)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// filterExprCache 已解析表达式缓存（enjoy AST 纯求值，可复用——flow
// EnjoyEvaluation 同款惯例）。
var filterExprCache sync.Map // string → enjoy.Expr

func parseFilterExpr(code string) (enjoy.Expr, error) {
	if v, ok := filterExprCache.Load(code); ok {
		return v.(enjoy.Expr), nil
	}
	expr, err := enjoy.ParseExpr(code)
	if err != nil {
		return nil, err
	}
	filterExprCache.Store(code, expr)
	return expr, nil
}

// evalFilterExpr 表达式求值（Metadata 为作用域；真值映射同 flow
// RunCondition：nil → false，bool → 原值，其余 → true）。
func evalFilterExpr(expr enjoy.Expr, d *Document) bool {
	meta := d.Metadata
	if meta == nil {
		meta = map[string]any{}
	}
	val := expr.Eval(enjoy.NewScope(meta), enjoy.NewCtrl())
	if val == nil {
		return false
	}
	if b, ok := val.(bool); ok {
		return b
	}
	return true
}
