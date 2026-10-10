// search.go 实时搜索能力包（对标 solon-ai-talent-web:WebsearchTalent 的
// Go 落地版）。Java 走 Exa MCP 服务；06 契约改为委托 ai/rag/search 仓储
// （tavily/bocha/baidu 任一注入，亦可自定义）。超时文案对齐 opencode（Java
// timeout 关键字映射）；空结果兜底文案逐字保留；结果呈现为编号 Markdown
// 列表（Go 自定格式——契约授予的自由度，Exa 原文格式不可复刻）。
package web

import (
	"context"
	"fmt"
	"strings"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/rag"
)

// searchDefaultNumResults 缺省返回条数（Java DEFAULT_NUM_RESULTS = 8）。
const searchDefaultNumResults = 8

// searchSnippetLimit 单条内容摘要截断（rune）。
const searchSnippetLimit = 500

// Search 实时搜索能力包（websearch 工具，纯工具集形态）。
type Search struct {
	ai.TalentBase
	repo       rag.Repository
	maxRetries int
}

// NewSearch 以任意 rag 仓储（tavily/bocha/baidu 或自定义）构造。
func NewSearch(repo rag.Repository) *Search {
	return &Search{repo: repo, maxRetries: 3}
}

// RetryConfig 覆写最大重试次数（>=1）。
func (t *Search) RetryConfig(n int) *Search {
	if n >= 1 {
		t.maxRetries = n
	}
	return t
}

func (t *Search) Name() string { return "websearch" }

func (t *Search) Tools(*ai.Prompt) []ai.FunctionTool {
	return []ai.FunctionTool{
		ai.NewTool("websearch").
			Desc("执行实时web搜索").
			StringParam("query", "查询关键字", true).
			Param(ai.ParamSpec{Name: "numResults", Type: "integer", Required: false,
				Default: searchDefaultNumResults, Desc: "返回的结果数量"}).
			Handler(func(ctx context.Context, args map[string]any) (any, error) {
				return t.Search(ctx, argStr(args, "query"), argInt(args, "numResults"))
			}).Build(),
	}
}

// Search 检索主流程：重试 → 错误面（超时文案映射）→ 空结果兜底 → 编号列表。
func (t *Search) Search(ctx context.Context, query string, numResults int) (string, error) {
	if numResults <= 0 {
		numResults = searchDefaultNumResults
	}
	cond := rag.NewQuery(query)
	cond.Limit = numResults

	var docs []*rag.Document
	var err error
	for attempt := 0; ; attempt++ {
		docs, err = t.repo.SearchQuery(ctx, cond)
		if err == nil {
			break
		}
		if attempt >= t.maxRetries-1 {
			if strings.Contains(strings.ToLower(err.Error()), "timeout") {
				return "", fmt.Errorf("Search request timed out")
			}
			return "", err
		}
	}
	if len(docs) == 0 {
		return "No search results found. Please try a different query.", nil
	}

	var sb strings.Builder
	for i, d := range docs {
		fmt.Fprintf(&sb, "%d. ", i+1)
		if title := d.Title(); title != "" {
			if url := d.URL(); url != "" {
				fmt.Fprintf(&sb, "[%s](%s)", title, url)
			} else {
				sb.WriteString(title)
			}
		} else if url := d.URL(); url != "" {
			sb.WriteString(url)
		}
		sb.WriteString("\n")
		snippet := strings.TrimSpace(d.Content)
		if snippet == "" {
			snippet = d.Summary()
		}
		if snippet != "" {
			if r := []rune(snippet); len(r) > searchSnippetLimit {
				snippet = string(r[:searchSnippetLimit]) + "…"
			}
			sb.WriteString("   ")
			sb.WriteString(snippet)
			sb.WriteString("\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}
