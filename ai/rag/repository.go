// repository.go 知识库 SPI（对标 Repository）+ 能力接口（Java 用接口继承
// RepositoryStorable/RepositoryLifecycle，Go 用独立能力接口——使用方以类型
// 断言探测，database/sql 风格：`if s, ok := repo.(Storer); ok { … }`）
// + 检索辅助（PromptAugment / RepositoryTool / Reranker 接口位）。
package rag

import (
	"context"
	"fmt"
	"strings"

	ai "github.com/crazy-airhead/aifei-go/ai"
)

// Repository 知识库主接口（只读检索面；网络搜索型仓储天然只有此面）。
type Repository interface {
	// Search 简单检索（缺省 Query 条件）。
	Search(ctx context.Context, query string) ([]*Document, error)
	// SearchQuery 条件检索。
	SearchQuery(ctx context.Context, q *Query) ([]*Document, error)
}

// Storer 可存储能力接口（Java RepositoryStorable；Java 的 asyncSave 不迁——
// Go 调用方 `go` 一句即得，不替调用方起协程）。progress 为批次粒度回调
// （done 已完成批数，total 总批数）。
type Storer interface {
	Save(ctx context.Context, docs []*Document, progress func(done, total int)) error
	DeleteByID(ctx context.Context, ids ...string) error
	ExistsByID(ctx context.Context, id string) (bool, error)
}

// Lifecycle 生命周期能力接口（Java RepositoryLifecycle；ctx 为 Go 增量）。
type Lifecycle interface {
	InitRepository(ctx context.Context) error
	DropRepository(ctx context.Context) error
}

// FilterTransformer 过滤表达式翻译能力接口（后端可选实现，Java FilterTransformer
// 对应物）：能把 enjoy 过滤表达式译成原生过滤（ES bool query、SQL WHERE…）的
// 后端返回 (native, true)；否则 (nil, false)，使用方回退内存过滤（对齐 Java
// disableRefilter=false 的默认行为）。
type FilterTransformer interface {
	TransformFilter(expr string) (native any, ok bool)
}

// ---- 检索辅助（契约 §2.3）----

// Reranker 重排缝（对标 RerankingModel.rerank 的接口位；具体重排模型 P5 按
// 方言落地——返回重排后的文档集并回写 Score）。
type Reranker interface {
	Rerank(ctx context.Context, query string, docs []*Document) ([]*Document, error)
}

// PromptAugment 检索命中拼进用户消息（对标 Repository.promptAugment 默认方法：
// search(query) → ai.OfUserAugment）。Java 接口默认方法抛 IOException → Go
// 包级函数返错误。
func PromptAugment(ctx context.Context, repo Repository, query string) (*ai.Message, error) {
	docs, err := repo.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	return ai.OfUserAugment(query, docs), nil
}

// RepoToolOption RepositoryTool 选项。
type RepoToolOption func(*repositoryTool)

// WithReranker 命中后重排（Java 双参构造器对应物）。
func WithReranker(r Reranker) RepoToolOption {
	return func(rt *repositoryTool) { rt.reranker = r }
}

type repositoryTool struct {
	repo     Repository
	reranker Reranker
}

// NewRepositoryTool 把任意 Repository 变成 agent 工具（对标 RepositoryTool，
// 被动检索 → 主动求知）：工具名 repository_query，参数 queries（≤5 生效）+
// topK（<=0 → 3），多重检索结果汇总为 Markdown。
func NewRepositoryTool(repo Repository, opts ...RepoToolOption) ai.FunctionTool {
	rt := &repositoryTool{repo: repo}
	for _, o := range opts {
		o(rt)
	}
	if rt.repo == nil { // Java Objects.requireNonNull
		panic("rag: repository must not be nil")
	}
	return ai.NewTool("repository_query").
		Desc("知识库检索工具。当需要查询特定领域知识、文档或背景资料时调用。").
		Param(ai.ParamSpec{Name: "queries", Type: "array", Required: true,
			Items: map[string]any{"type": "string"},
			Desc:  "查询词列表（1个或多个，最多不要超过5个）"}).
		Param(ai.ParamSpec{Name: "topK", Type: "integer", Default: 3, Desc: "返回结果的数"}).
		Handler(rt.query).
		Build()
}

func (rt *repositoryTool) query(ctx context.Context, args map[string]any) (any, error) {
	queries := toStrings(args["queries"])
	if len(queries) == 0 {
		return "提示：未提供有效的查询关键词。", nil
	}
	// 限制单次查询词数量，防止模型一次性搜太多导致上下文溢出
	maxQueries := len(queries)
	if maxQueries > 5 {
		maxQueries = 5
	}
	topK := toInt(args["topK"])
	if topK <= 0 {
		topK = 3
	}

	var sb strings.Builder
	sb.WriteString("### 多重检索结果汇总\n\n")
	for i := 0; i < maxQueries; i++ {
		q := strings.TrimSpace(queries[i])
		if q == "" {
			continue
		}
		cond := NewQuery(q)
		cond.Limit = topK
		result, err := rt.repo.SearchQuery(ctx, cond)
		if err != nil {
			return nil, err
		}
		if rt.reranker != nil {
			if result, err = rt.reranker.Rerank(ctx, q, result); err != nil {
				return nil, err
			}
		}
		fmt.Fprintf(&sb, "#### 针对关键词 [%s] 的结果：\n", q)
		sb.WriteString(rt.formatResults(result))
		sb.WriteString("\n---\n")
	}
	return sb.String(), nil
}

func (rt *repositoryTool) formatResults(docs []*Document) string {
	if len(docs) == 0 {
		return "  > 未找到相关的知识库内容。\n"
	}
	var sb strings.Builder
	for i, doc := range docs {
		// 二级列表项（Markdown 缩进）划分来源
		fmt.Fprintf(&sb, "  - **来源 [%d]**\n", i+1)
		if doc.Title() != "" {
			fmt.Fprintf(&sb, "    - 标题: %s\n", doc.Title())
		}
		fmt.Fprintf(&sb, "    - 相关度: %.2f\n", doc.Score())
		if doc.Content != "" {
			content := strings.TrimSpace(doc.Content)
			// 截断保护（按 rune 截——Java substring 按 UTF-16 可劈代理对，Go 侧加固）
			if rs := []rune(content); len(rs) > 2000 {
				content = string(rs[:2000]) + "... (内容过长已截断)"
			}
			fmt.Fprintf(&sb, "    - 内容: %s\n", content)
		}
		if doc.URL() != "" {
			fmt.Fprintf(&sb, "    - 引用地址: %s\n", doc.URL())
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// toStrings 参数值转字符串切片（JSON 数组 → []any 各元素宽容转换）。
func toStrings(v any) []string {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		switch s := item.(type) {
		case string:
			out = append(out, s)
		case nil:
			out = append(out, "")
		default:
			out = append(out, fmt.Sprint(s))
		}
	}
	return out
}

// toInt 参数值转 int（JSON number → float64 / 原生 int/int64）。
func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}
