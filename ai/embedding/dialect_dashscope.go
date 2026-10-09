package embedding

import (
	"encoding/json"
	"fmt"
	"strings"

	ai "github.com/crazy-airhead/aifei-go/ai"
)

// URLPrefix 百炼服务端点前缀（chat 方言同值；本包自持一份——仅为常量引入
// chat 方言包会连带触发其 init 注册副作用，得不偿失）。
const URLPrefix = "https://dashscope.aliyuncs.com/api/v1/services/"

// dashscopeDialect 阿里云百炼嵌入方言（text-embedding 端点）。
type dashscopeDialect struct{}

func (d *dashscopeDialect) Name() string    { return "dashscope" }
func (d *dashscopeDialect) IsDefault() bool { return false }

// Matched standard/provider 为 "dashscope"，或 apiUrl 以前缀开头（与 Java 同序：
// URL 前缀判断在 standard 分流之后，但对非 dashscope 的 standard 同样可能命中——
// Java 原语义如此，Go 忠实保留）。
func (d *dashscopeDialect) Matched(cfg *ai.Config) bool {
	if cfg == nil {
		return false
	}
	if cfg.StandardOrProvider() == "dashscope" {
		return true
	}
	return strings.HasPrefix(cfg.APIURL, URLPrefix)
}

// BuildRequestJSON 百炼私有形态：{model?, input:{texts:[...]}, parameters:{...options}}。
func (d *dashscopeDialect) BuildRequestJSON(cfg *ai.Config, opts map[string]any, input []string) ([]byte, error) {
	root := map[string]any{}
	if cfg != nil && cfg.Model != "" {
		root["model"] = cfg.Model
	}
	texts := make([]any, len(input))
	for i, t := range input {
		texts[i] = t
	}
	root["input"] = map[string]any{"texts": texts}
	parameters := map[string]any{}
	for k, v := range opts {
		parameters[k] = v
	}
	root["parameters"] = parameters
	return json.Marshal(root)
}

// ParseResponseJSON {output:{embeddings:[{text_index|index, embedding:[f]}]}}；
// 错误形态 {code, message}。embeddings 按 text_index（兼容 index）排序。
func (d *dashscopeDialect) ParseResponseJSON(body []byte) ([][]float32, error) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("ai: embedding response parse: %w", err)
	}
	if code, _ := root["code"].(string); code != "" {
		message, _ := root["message"].(string)
		return nil, fmt.Errorf("ai: embedding error: %s: %s", code, message)
	}

	items := []indexedVector{}
	output, _ := root["output"].(map[string]any)
	embeddings, _ := output["embeddings"].([]any)
	for _, v := range embeddings {
		item, ok := v.(map[string]any)
		if !ok {
			continue
		}
		idx := 0
		if n, ok := item["text_index"].(float64); ok {
			idx = int(n)
		} else if n, ok := item["index"].(float64); ok {
			idx = int(n)
		}
		arr, _ := item["embedding"].([]any)
		vec := make([]float32, len(arr))
		for i, f := range arr {
			n, _ := f.(float64)
			vec[i] = float32(n)
		}
		items = append(items, indexedVector{index: idx, vec: vec})
	}
	return sortVectors(items), nil
}
