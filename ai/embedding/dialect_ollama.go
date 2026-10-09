package embedding

import (
	"encoding/json"
	"fmt"
	"strings"

	ai "github.com/crazy-airhead/aifei-go/ai"
)

// ollamaDialect Ollama 嵌入方言（/api/embed 端点，{model, input, embeddings}）。
type ollamaDialect struct{}

func (d *ollamaDialect) Name() string    { return "ollama" }
func (d *ollamaDialect) IsDefault() bool { return false }

// Matched standard/provider 为 "ollama"，或两者皆空且 apiUrl 以 /api/embed 结尾
// （使用方直接给端点全路径，Java 同款约定）。
func (d *ollamaDialect) Matched(cfg *ai.Config) bool {
	if cfg == nil {
		return false
	}
	if cfg.StandardOrProvider() == "ollama" {
		return true
	}
	if cfg.StandardOrProvider() != "" {
		return false
	}
	return strings.HasSuffix(cfg.APIURL, "/api/embed")
}

// BuildRequestJSON 抽象形态（ollama 容忍 encoding_format 等多余字段，与 Java 一致）。
func (d *ollamaDialect) BuildRequestJSON(cfg *ai.Config, opts map[string]any, input []string) ([]byte, error) {
	return buildAbstractRequestJSON(cfg, opts, input)
}

// ParseResponseJSON {model?, embeddings:[[f]], prompt_eval_count?}：embeddings 无
// index 字段，返回序即输入序（不排序）。
func (d *ollamaDialect) ParseResponseJSON(body []byte) ([][]float32, error) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("ai: embedding response parse: %w", err)
	}
	if errNode, has := root["error"]; has && errNode != nil {
		if s, ok := errNode.(string); ok && s != "" {
			return nil, fmt.Errorf("ai: embedding error: %s", s)
		}
		return nil, parseErrorNode(errNode)
	}

	list, _ := root["embeddings"].([]any)
	out := make([][]float32, 0, len(list))
	for _, v := range list {
		arr, ok := v.([]any)
		if !ok {
			continue
		}
		vec := make([]float32, len(arr))
		for i, f := range arr {
			n, _ := f.(float64)
			vec[i] = float32(n)
		}
		out = append(out, vec)
	}
	return out, nil
}
