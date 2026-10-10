package generate

import (
	"encoding/json"
	"fmt"

	"github.com/crazy-airhead/aifei-go/ai/internal/dialectkit"
)

// openaiDialect OpenAI 兼容生成方言（默认补位；Images/generate 形态）。
type openaiDialect struct{}

func (d *openaiDialect) Name() string    { return "openai" }
func (d *openaiDialect) IsDefault() bool { return true }

// Matched 恒 false：仅作默认补位（对标 Java matched() { return false; }）。
func (d *openaiDialect) Matched(cfg *Config) bool { return false }

// buildFlatRequestJSON 扁平形态（对标 AbstractGenerateDialect）：
// {model?, prompt | <promptMap 平铺>, ...options}；options 最后写入可覆盖同名键。
func buildFlatRequestJSON(cfg *Config, opts map[string]any, promptStr string, promptMap map[string]any) ([]byte, error) {
	root := map[string]any{}
	if cfg != nil && cfg.Model != "" {
		root["model"] = cfg.Model
	}
	if promptStr != "" {
		root["prompt"] = promptStr
	} else if promptMap != nil {
		for k, v := range promptMap {
			root[k] = v
		}
	}
	for k, v := range opts {
		root[k] = v
	}
	return json.Marshal(root)
}

// BuildRequestJSON 扁平形态。
func (d *openaiDialect) BuildRequestJSON(cfg *Config, opts map[string]any, promptStr string, promptMap map[string]any) ([]byte, error) {
	return buildFlatRequestJSON(cfg, opts, promptStr, promptMap)
}

// ParseResponseJSON 官方 Images/异步任务/兼容端点三形态：
//   - error 节点 → error（[type|code] message 形态）；
//   - task_id 且 cfg.TaskURL 非空 → 单条 url 内容（裸拼接）；
//   - data 数组：b64_json→Data（data 键兜底）、revised_prompt→Text（text 键兜底）、
//     mimeType 或 output_format 兜底（"jpg" 归一 "jpeg"，image/{fmt} 小写）；
//   - model 缺失回退 cfg.Model（Images API 回包无 model）。
func (d *openaiDialect) ParseResponseJSON(cfg *Config, body []byte) (*Response, error) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("ai: generate response parse: %w", err)
	}
	if errNode, has := root["error"]; has && errNode != nil {
		return nil, fmt.Errorf("ai: generate error: %s", dialectkit.ExtractErrorMessage(errNode))
	}

	model := dialectkit.GetStr(root, "model")
	if model == "" && cfg != nil {
		model = cfg.Model
	}

	var data []Content
	if taskID := dialectkit.GetStr(root, "task_id"); taskID != "" && cfg != nil && cfg.TaskURL != "" {
		data = []Content{{URL: cfg.TaskURLAndId(taskID)}}
		return &Response{Model: model, Data: data}, nil
	}

	if arr, ok := root["data"].([]any); ok {
		defaultMime := toImageMime(dialectkit.GetStr(root, "output_format"))
		for _, v := range arr {
			item, ok := v.(map[string]any)
			if !ok {
				continue
			}
			b64 := dialectkit.GetStr(item, "b64_json")
			if b64 == "" {
				b64 = dialectkit.GetStr(item, "data")
			}
			text := dialectkit.GetStr(item, "revised_prompt")
			if text == "" {
				text = dialectkit.GetStr(item, "text")
			}
			mime := dialectkit.GetStr(item, "mimeType")
			if mime == "" {
				mime = defaultMime
			}
			data = append(data, Content{
				Text:     text,
				Data:     b64,
				URL:      dialectkit.GetStr(item, "url"),
				MimeType: mime,
			})
		}
	}
	return &Response{Model: model, Data: data}, nil
}

// toImageMime output_format → mime（"" 保持空；jpg 归一 jpeg；image/{小写}）。
func toImageMime(format string) string {
	if format == "" {
		return ""
	}
	lower := lowerASCII(format)
	if lower == "jpg" {
		return "jpeg"
	}
	return "image/" + lower
}
