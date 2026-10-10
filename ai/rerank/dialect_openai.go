package rerank

import (
	"encoding/json"
	"fmt"
	"sort"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/internal/dialectkit"
	"github.com/crazy-airhead/aifei-go/ai/rag"
)

// openaiDialect OpenAI 兼容重排方言（默认补位；/v1/rerank 形态）。
type openaiDialect struct{}

func (d *openaiDialect) Name() string    { return "openai" }
func (d *openaiDialect) IsDefault() bool { return true }

// Matched 恒 false：仅作默认补位（对标 Java matched() { return false; }）。
func (d *openaiDialect) Matched(cfg *ai.Config) bool { return false }

// buildAbstractRequestJSON 抽象请求形态（对标 AbstractRerankingDialect）：
// {model?, query, documents:[...], ...options}。
func buildAbstractRequestJSON(cfg *ai.Config, opts map[string]any, query string, documents []*rag.Document) ([]byte, error) {
	root := map[string]any{}
	if cfg != nil && cfg.Model != "" {
		root["model"] = cfg.Model
	}
	root["query"] = query
	arr := make([]any, len(documents))
	for i, doc := range documents {
		arr[i] = doc.Content
	}
	root["documents"] = arr
	for k, v := range opts {
		root[k] = v
	}
	return json.Marshal(root)
}

// BuildRequestJSON 抽象形态。
func (d *openaiDialect) BuildRequestJSON(cfg *ai.Config, opts map[string]any, query string, documents []*rag.Document) ([]byte, error) {
	return buildAbstractRequestJSON(cfg, opts, query, documents)
}

// ParseResponseJSON {model, error?} | {model, results?:[{index, relevance_score,
// document:{text}|值}], usage?}。results 缺失/非数组/条目非对象均防御跳过（部分
// 兼容端点异常形态，对标 Java 空值防御分支）。
func (d *openaiDialect) ParseResponseJSON(body []byte) (*Response, error) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("ai: rerank response parse: %w", err)
	}
	if errNode, has := root["error"]; has && errNode != nil {
		return nil, parseErrorNode(errNode)
	}

	model, _ := root["model"].(string)
	results := make([]Result, 0)
	if arr, ok := root["results"].([]any); ok {
		for _, v := range arr {
			item, ok := v.(map[string]any)
			if !ok {
				continue
			}
			// document 可缺失或为值形态（部分兼容端点）
			var text string
			switch doc := item["document"].(type) {
			case map[string]any:
				text, _ = doc["text"].(string)
			case string:
				text = doc
			}
			idx, _ := item["index"].(float64)
			score, _ := item["relevance_score"].(float64)
			results = append(results, Result{Index: int(idx), Text: text, RelevanceScore: score})
		}
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Index < results[j].Index })
	return &Response{Model: model, Results: results}, nil
}

// parseErrorNode error 节点：走 dialectkit 标准「[type] message」形态（对标
// OpenaiDialectSupport.extractErrorMessage）。
func parseErrorNode(node any) error {
	return fmt.Errorf("ai: rerank error: %s", dialectkit.ExtractErrorMessage(node))
}
