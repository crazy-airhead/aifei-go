// baidu.go 百度 AI 搜索（对标 BaiduWebSearchRepository：ofBasic/ofAI 两形态；
// AI 态可选 embedding 重排）。鉴权走 X-Appbuilder-Authorization 头（千帆
// AppBuilder 形状，与 tavily/bocha 的 Authorization 不同）。
package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai/httpx"
	"github.com/crazy-airhead/aifei-go/ai/rag"
)

// BaiduDefaultAPIURL 千帆 AI 搜索端点（Java 测试样例同款）。
const BaiduDefaultAPIURL = "https://qianfan.baidubce.com/v2/ai_search/chat/completions"

// BaiduSearchType 搜索形态（Java SearchType 枚举）。
type BaiduSearchType uint8

const (
	BaiduSearchBasic BaiduSearchType = iota // 基础搜索（不传 model）
	BaiduSearchAI                           // AI 搜索（智能总结，传 model + 开关组）
)

// BaiduRepository 百度搜索即仓储（只读；实现 rag.Repository）。
type BaiduRepository struct {
	apiURL     string
	apiKey     string
	searchType BaiduSearchType
	model      string
	http       *httpx.Client
	embedder   rag.Embedder
}

// NewBaidu 建百度仓储（ofBasic/ofAI 对应物：searchType 选形态；model 缺省
// "ernie-3.5-8k" 可经 Model 覆盖；apiKey 缺省取 BAIDU_API_KEY）。
func NewBaidu(searchType BaiduSearchType, opts ...Option) *BaiduRepository {
	c := &config{}
	for _, o := range opts {
		o(c)
	}
	apiURL := c.apiURL
	if apiURL == "" {
		apiURL = BaiduDefaultAPIURL
	}
	return &BaiduRepository{
		apiURL:     apiURL,
		apiKey:     resolveEnv(c.apiKey, EnvBaiduAPIKey),
		searchType: searchType,
		model:      "ernie-3.5-8k",
		http:       newHTTPClient(c),
		embedder:   c.embedder,
	}
}

// Model 覆盖 AI 搜索模型（如 deepseek-r1、ernie-4.0-turbo-8k；构建期 fluent
// 设置——Java Builder.model 对应物）。
func (r *BaiduRepository) Model(model string) *BaiduRepository {
	if model != "" {
		r.model = model
	}
	return r
}

// baiduResponse 百度响应形状：code/message 错误面 + choices[].message.content
// （AI 态智能回答）+ references[]（检索命中，两态都有）。
type baiduResponse struct {
	Code    *int   `json:"code"` // 缺键 = 无错误面（Java hasKey 判定）
	Message string `json:"message"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	References []struct {
		Content   string `json:"content"`
		Title     string `json:"title"`
		URL       string `json:"url"`
		ID        string `json:"id"`
		Date      string `json:"date"`
		Type      string `json:"type"`
		WebAnchor string `json:"web_anchor"`
		Icon      string `json:"icon"`
	} `json:"references"`
}

// Search 简单检索（缺省条件）。
func (r *BaiduRepository) Search(ctx context.Context, query string) ([]*rag.Document, error) {
	return r.SearchQuery(ctx, rag.NewQuery(query))
}

// SearchQuery 条件检索：messages 单轮 user 消息（AI 态附 model/stream/开关组）
// + resource_type_filter 承载 limit → 命中映射（AI 态首条为智能回答）→
// 可选嵌入重排。空查询词不发请求直接返回空集（Java 同款）。
func (r *BaiduRepository) SearchQuery(ctx context.Context, q *rag.Query) ([]*rag.Document, error) {
	if q == nil {
		q = rag.NewQuery("")
	}
	query := strings.TrimSpace(q.Query)
	if query == "" {
		return []*rag.Document{}, nil
	}
	if r.apiKey == "" { // 晚失败（同 tavily/bocha）
		return nil, errors.New("search: baidu apiKey is required (set " + EnvBaiduAPIKey + " or use WithAPIKey)")
	}

	messages := []map[string]any{{"role": "user", "content": query}}
	body := map[string]any{"messages": messages}
	if r.searchType == BaiduSearchAI {
		body["model"] = r.model
		body["stream"] = false
		body["enable_corner_markers"] = true
		body["enable_deep_search"] = false      // 默认不开启深搜索，后续扩展（Java 同款）
		body["enable_followup_queries"] = false // 默认不开启追问，后续扩展
	}
	if q.Limit > 0 {
		body["resource_type_filter"] = []map[string]any{
			{"type": "web", "top_k": q.Limit},
		}
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	respJSON, _, err := r.http.PostJSON(ctx, r.apiURL, data,
		map[string]string{"X-Appbuilder-Authorization": "Bearer " + r.apiKey})
	if err != nil {
		return nil, err
	}
	return r.parseResponse(ctx, respJSON, q)
}

func (r *BaiduRepository) parseResponse(ctx context.Context, respJSON []byte, q *rag.Query) ([]*rag.Document, error) {
	var resp baiduResponse
	if err := json.Unmarshal(respJSON, &resp); err != nil {
		return nil, err
	}
	if resp.Code != nil && *resp.Code != 0 {
		return nil, fmt.Errorf("search: BaiduAiSearch error (code=%d): %s", *resp.Code, resp.Message)
	}
	docs := make([]*rag.Document, 0, len(resp.References)+1)

	// AI 态：choices[0].message.content 为智能总结，居首
	if r.searchType == BaiduSearchAI && len(resp.Choices) > 0 {
		if content := strings.TrimSpace(resp.Choices[0].Message.Content); content != "" {
			docs = append(docs, rag.NewDocument(content).
				Meta("title", "AI 智能回答"). // Java "Solon AI智能回答"——品牌名替换
				Meta("type", "ai_answer").
				Meta("source", "baidu_ai_search"))
		}
	}
	// references：基础/AI 两态都有的检索命中
	for _, ref := range resp.References {
		content := strings.TrimSpace(ref.Content)
		if content == "" {
			continue
		}
		doc := rag.NewDocument(content)
		if t := strings.TrimSpace(ref.Title); t != "" {
			doc.Meta("title", t)
		}
		if u := strings.TrimSpace(ref.URL); u != "" {
			doc.Meta("url", u)
		}
		if ref.ID != "" {
			doc.Meta("id", ref.ID)
		}
		if ref.Date != "" {
			doc.Meta("date", ref.Date)
		}
		if ref.Type != "" {
			doc.Meta("type", ref.Type)
		} else {
			doc.Meta("type", "web") // 默认 web 类型（Java 同款）
		}
		if ref.WebAnchor != "" {
			doc.Meta("web_anchor", ref.WebAnchor)
		}
		if ref.Icon != "" {
			doc.Meta("icon", ref.Icon)
		}
		doc.Meta("source", "baidu_search")
		docs = append(docs, doc)
	}
	if len(docs) == 0 { // Java: 无任何可解析内容即异常（附响应前 200 字符）
		snippet := string(respJSON)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, errors.New("search: no content found in BaiduAiSearch response: " + snippet)
	}
	return embedRefilter(ctx, r.embedder, docs, q)
}

var _ rag.Repository = (*BaiduRepository)(nil)
