package embedding

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"

	ai "github.com/crazy-airhead/aifei-go/ai"
)

// openaiDialect OpenAI 兼容嵌入方言（默认补位；/v1/embeddings 形态）。
type openaiDialect struct{}

func (d *openaiDialect) Name() string    { return "openai" }
func (d *openaiDialect) IsDefault() bool { return true }

// Matched 恒 false：仅作默认补位（对标 Java matched() { return false; }）。
func (d *openaiDialect) Matched(cfg *ai.Config) bool { return false }

// buildAbstractRequestJSON 抽象请求形态（对标 AbstractEmbeddingDialect）：
// {model?, input:[...], encoding_format:"float", ...options}。
func buildAbstractRequestJSON(cfg *ai.Config, opts map[string]any, input []string) ([]byte, error) {
	root := map[string]any{}
	if cfg != nil && cfg.Model != "" {
		root["model"] = cfg.Model
	}
	arr := make([]any, len(input))
	for i, t := range input {
		arr[i] = t
	}
	root["input"] = arr
	root["encoding_format"] = "float"
	for k, v := range opts {
		root[k] = v
	}
	return json.Marshal(root)
}

// BuildRequestJSON 抽象形态。
func (d *openaiDialect) BuildRequestJSON(cfg *ai.Config, opts map[string]any, input []string) ([]byte, error) {
	return buildAbstractRequestJSON(cfg, opts, input)
}

// ParseResponseJSON {model, data:[{index, embedding:[f] | "base64"}], usage?}；
// data 按 index 排序。embedding 为字符串时按 little-endian IEEE-754 float32 解码
// （OpenAI base64 编码格式；请求虽声明 float，兼容端点仍可能回 base64）。
func (d *openaiDialect) ParseResponseJSON(body []byte) ([][]float32, error) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("ai: embedding response parse: %w", err)
	}
	if errNode, has := root["error"]; has && errNode != nil {
		return nil, parseErrorNode(errNode)
	}

	data, _ := root["data"].([]any)
	items := make([]indexedVector, 0, len(data))
	for _, v := range data {
		item, ok := v.(map[string]any)
		if !ok {
			continue
		}
		node, has := item["embedding"]
		if !has || node == nil {
			continue
		}
		var vec []float32
		switch emb := node.(type) {
		case []any:
			vec = make([]float32, len(emb))
			for i, f := range emb {
				n, _ := f.(float64)
				vec[i] = float32(n)
			}
		case string:
			decoded, err := decodeBase64Embedding(emb)
			if err != nil {
				return nil, err
			}
			vec = decoded
		default:
			continue
		}
		idx, _ := item["index"].(float64)
		items = append(items, indexedVector{index: int(idx), vec: vec})
	}
	return sortVectors(items), nil
}

// parseErrorNode error 节点：message 优先，缺失回退整体 JSON。
func parseErrorNode(node any) error {
	if m, ok := node.(map[string]any); ok {
		if msg, _ := m["message"].(string); msg != "" {
			return fmt.Errorf("ai: embedding error: %s", msg)
		}
	}
	return fmt.Errorf("ai: embedding error: %v", node)
}

// decodeBase64Embedding base64 → little-endian float32 数组（字节长非 4 的倍数报错）。
func decodeBase64Embedding(value string) ([]float32, error) {
	bytes, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("ai: embedding base64 decode: %w", err)
	}
	if len(bytes)%4 != 0 {
		return nil, fmt.Errorf("ai: invalid OpenAI base64 embedding byte length: %d", len(bytes))
	}
	vec := make([]float32, len(bytes)/4)
	for i := range vec {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(bytes[i*4:]))
	}
	return vec, nil
}
