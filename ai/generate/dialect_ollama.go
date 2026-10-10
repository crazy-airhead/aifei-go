package generate

import (
	"encoding/json"
	"fmt"

	"github.com/crazy-airhead/aifei-go/ai/internal/dialectkit"
)

// ollamaDialect Ollama 生成方言（/api/generate 形态：文本 response 或图像 data）。
type ollamaDialect struct{}

func (d *ollamaDialect) Name() string    { return "ollama" }
func (d *ollamaDialect) IsDefault() bool { return false }

// Matched standard/provider == "ollama"（区分大小写，不匹配 URL）。
func (d *ollamaDialect) Matched(cfg *Config) bool {
	return cfg != nil && cfg.StandardOrProvider() == "ollama"
}

// BuildRequestJSON 扁平形态。
func (d *ollamaDialect) BuildRequestJSON(cfg *Config, opts map[string]any, promptStr string, promptMap map[string]any) ([]byte, error) {
	return buildFlatRequestJSON(cfg, opts, promptStr, promptMap)
}

// ParseResponseJSON：
//   - error 键存在即错（不校验非空——Java hasKey 语义忠实保留）；
//   - response（文本单条）优先于 data（图像数组，按 Content 字段反序列化）；
//   - usage（prompt_eval_count）不进 Go 契约。
func (d *ollamaDialect) ParseResponseJSON(cfg *Config, body []byte) (*Response, error) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("ai: generate response parse: %w", err)
	}
	model := dialectkit.GetStr(root, "model")
	if _, has := root["error"]; has {
		return nil, fmt.Errorf("ai: generate error: %s", dialectkit.GetStr(root, "error"))
	}

	var data []Content
	if _, has := root["response"]; has {
		data = []Content{{Text: dialectkit.GetStr(root, "response")}}
		return &Response{Model: model, Data: data}, nil
	}
	if arr, ok := root["data"].([]any); ok {
		raw, err := json.Marshal(arr)
		if err == nil {
			_ = json.Unmarshal(raw, &data) // 按 Content 网络字段名（text/data/url/mimeType）
		}
	}
	return &Response{Model: model, Data: data}, nil
}
