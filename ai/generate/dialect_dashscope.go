package generate

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai/internal/dialectkit"
)

// dashscopeURLPrefix 百炼服务端点前缀（URL 特征匹配用）。
const dashscopeURLPrefix = "https://dashscope.aliyuncs.com/api/v1/services/"

// dashscopeDialect DashScope 生成方言（wanx 等文生图/图生图：三层 model/input/parameters）。
type dashscopeDialect struct{}

func (d *dashscopeDialect) Name() string    { return "dashscope" }
func (d *dashscopeDialect) IsDefault() bool { return false }

// Matched standard/provider == "dashscope"（equals 区分大小写——与 chat 方言的
// equalsIgnoreCase 不同，generate 原语义如此）或端点命中百炼前缀。
func (d *dashscopeDialect) Matched(cfg *Config) bool {
	if cfg == nil {
		return false
	}
	if cfg.StandardOrProvider() == "dashscope" {
		return true
	}
	return strings.HasPrefix(cfg.APIURL, dashscopeURLPrefix)
}

// BuildRequestJSON {model?, input:{prompt} | <promptMap 整体>, parameters:{...}}：
// parameters 恒输出（空也写 {}）；str 优先于 map。
func (d *dashscopeDialect) BuildRequestJSON(cfg *Config, opts map[string]any, promptStr string, promptMap map[string]any) ([]byte, error) {
	root := map[string]any{}
	if cfg != nil && cfg.Model != "" {
		root["model"] = cfg.Model
	}
	if promptStr != "" {
		root["input"] = map[string]any{"prompt": promptStr}
	} else if promptMap != nil {
		root["input"] = promptMap
	}
	parameters := map[string]any{}
	for k, v := range opts {
		parameters[k] = v
	}
	root["parameters"] = parameters
	return json.Marshal(root)
}

// ParseResponseJSON 百炼回包：
//   - 错误条件：code 键存在且非空串，消息 "{code}: {message}"；
//   - output.task_id（异步）→ 单条 url 内容（taskURL 裸拼接）；
//   - output.results（同步）→ []Content（url/data/text/mimeType 字段名）；
//   - 永不解析 usage。
func (d *dashscopeDialect) ParseResponseJSON(cfg *Config, body []byte) (*Response, error) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("ai: generate response parse: %w", err)
	}
	if code := dialectkit.GetStr(root, "code"); code != "" {
		return nil, fmt.Errorf("ai: generate error: %s: %s", code, dialectkit.GetStr(root, "message"))
	}

	model := dialectkit.GetStr(root, "model")
	output, _ := root["output"].(map[string]any)

	var data []Content
	if _, has := output["task_id"]; has {
		// 异步模式只返回任务 id（键存在即走此分支，即使空串——Java hasKey 语义）；
		// taskUrl 未配置时 url 为裸 taskId 拼接结果（Java 无守卫，忠实保留——
		// 对照 openai 方言有 taskId/taskUrl 双非空守卫）
		url := ""
		if cfg != nil {
			url = cfg.TaskURLAndId(dialectkit.GetStr(output, "task_id"))
		}
		data = []Content{{URL: url}}
		return &Response{Model: model, Data: data}, nil
	}
	if arr, ok := output["results"].([]any); ok {
		raw, err := json.Marshal(arr)
		if err == nil {
			_ = json.Unmarshal(raw, &data)
		}
	}
	return &Response{Model: model, Data: data}, nil
}
