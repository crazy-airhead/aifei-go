package rerank

import (
	"encoding/json"
	"fmt"
	"sort"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/rag"
)

// dashscopeURLPrefix 百炼服务端点前缀（URL 特征匹配用）。
const dashscopeURLPrefix = "https://dashscope.aliyuncs.com/api/v1/services/"

// dashscopeDialect DashScope 原生重排方言（gte-rerank 形态：input 嵌套 + output 回包）。
type dashscopeDialect struct{}

func (d *dashscopeDialect) Name() string    { return "dashscope" }
func (d *dashscopeDialect) IsDefault() bool { return false }

// Matched standard=dashscope 或端点命中百炼前缀（对标 Java matched）。
func (d *dashscopeDialect) Matched(cfg *ai.Config) bool {
	if cfg == nil {
		return false
	}
	if cfg.StandardOrProvider() == "dashscope" {
		return true
	}
	return len(cfg.APIURL) >= len(dashscopeURLPrefix) && cfg.APIURL[:len(dashscopeURLPrefix)] == dashscopeURLPrefix
}

// BuildRequestJSON {model?, input:{query, documents:[...]}, ...options}。
func (d *dashscopeDialect) BuildRequestJSON(cfg *ai.Config, opts map[string]any, query string, documents []*rag.Document) ([]byte, error) {
	root := map[string]any{}
	if cfg != nil && cfg.Model != "" {
		root["model"] = cfg.Model
	}
	docs := make([]any, len(documents))
	for i, doc := range documents {
		docs[i] = doc.Content
	}
	root["input"] = map[string]any{"query": query, "documents": docs}
	for k, v := range opts {
		root[k] = v
	}
	return json.Marshal(root)
}

// ParseResponseJSON {model, message?} | {model, output:{results:[{index,
// document:{text}, relevance_score}]}}；错误走 message 节点（百炼错误形态）。
func (d *dashscopeDialect) ParseResponseJSON(body []byte) (*Response, error) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("ai: rerank response parse: %w", err)
	}
	if msg, has := root["message"]; has && msg != nil {
		text, _ := msg.(string)
		return nil, fmt.Errorf("ai: rerank error: %s", text)
	}

	model, _ := root["model"].(string)
	output, _ := root["output"].(map[string]any)
	arr, _ := output["results"].([]any)
	results := make([]Result, 0, len(arr))
	for _, v := range arr {
		item, ok := v.(map[string]any)
		if !ok {
			continue
		}
		doc, _ := item["document"].(map[string]any)
		text, _ := doc["text"].(string)
		idx, _ := item["index"].(float64)
		score, _ := item["relevance_score"].(float64)
		results = append(results, Result{Index: int(idx), Text: text, RelevanceScore: score})
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Index < results[j].Index })
	return &Response{Model: model, Results: results}, nil
}
