package openai

import (
	"encoding/json"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai"
)

// BuildAPIURL 接口地址自动补全（Java OpenaiDialectSupport.buildApiUrl 对标）：
// 去 fragment；已带端点路径原样；已带版本号（/v1、/v4…）补端点；Azure 部署形态
// （/openai/deployments/{name}）只补端点；否则补 /v1 + 端点。查询串原样保留。
func BuildAPIURL(apiURL, endpointPath string) string {
	if apiURL == "" {
		return apiURL
	}
	if i := strings.Index(apiURL, "#"); i >= 0 {
		apiURL = apiURL[:i]
	}
	query := ""
	if i := strings.Index(apiURL, "?"); i >= 0 {
		query = apiURL[i:]
		apiURL = apiURL[:i]
	}
	base := strings.TrimRight(apiURL, "/")

	var result string
	switch {
	case strings.HasSuffix(base, "/"+endpointPath):
		result = base
	case azureDeployRe.MatchString(base), versionRe.MatchString(base):
		result = base + "/" + endpointPath
	default:
		result = base + "/v1/" + endpointPath
	}
	return result + query
}

// MatchesModelFamily 模型族匹配：片段须在 token 边界（开头或供应商分隔符 / : . _ -
// 之后；结尾或 - . 之前），兼容 azure/o4-mini、openai:gpt-5 等拼接形态。
func MatchesModelFamily(model, family string) bool {
	from := 0
	for from < len(model) {
		start := strings.Index(model[from:], family)
		if start < 0 {
			return false
		}
		start += from
		end := start + len(family)
		validPrefix := start == 0 || isModelTokenBoundary(model[start-1])
		validSuffix := end == len(model) || model[end] == '-' || model[end] == '.'
		if validPrefix && validSuffix {
			return true
		}
		from = start + 1
	}
	return false
}

// IsModelFamily MatchesModelFamily 的兼容扩展：部分网关省略 GPT 主系列连字符（gpt5）。
func IsModelFamily(model, family string) bool {
	if MatchesModelFamily(model, family) {
		return true
	}
	if strings.HasPrefix(family, "gpt-") && len(family) > 4 {
		return MatchesModelFamily(model, "gpt"+family[4:])
	}
	return false
}

func isModelTokenBoundary(ch byte) bool {
	return ch == '/' || ch == ':' || ch == '.' || ch == '_' || ch == '-'
}

// SanitizeArguments 出站参数净化（Java ToolCallJsonSanitizer 对标）：仅保留能严格
// 解析为 JSON object 的串（重序列化规范化），其余（空/标量/数组/截断损坏）替换 "{}"——
// 半截参数被执行比缺参更危险，由工具执行链路报参数缺失驱动模型重发。
func SanitizeArguments(argStr, fnName string) string {
	if argStr == "" {
		return "{}"
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(argStr), &parsed); err == nil && parsed != nil {
		if data, err := json.Marshal(parsed); err == nil {
			return string(data)
		}
	}
	return "{}"
}

// SanitizeCall 类型化工具调用的安全参数：ArgumentsStr 为权威来源，非法降级 "{}"；
// 为空时用结构化 Arguments 序列化。
func SanitizeCall(call *ai.ToolCall) string {
	if call == nil {
		return "{}"
	}
	if call.ArgumentsStr != "" {
		return SanitizeArguments(call.ArgumentsStr, call.Name)
	}
	if call.Arguments == nil {
		return "{}"
	}
	data, err := json.Marshal(call.Arguments)
	if err != nil {
		return "{}"
	}
	return string(data)
}
