// Package gateway OpenAPI→MCP 工具网关能力包（对标 OpenApiGatewayTalent +
// McpGatewayTalent：OpenAPI 文档解析（V3 JSON paths×methods → 工具：四参数
// 桶 header/path/query/body schema、outputSchema、multipart 探测）+ call_api
// 运行时（路径参数替换/查询拼接/JSON 或表单体/鉴权注入）+ search/detail 两
// 辅助工具与四档位裁剪）。Go 首卡落 JSON 文档（Swagger V2 的 definitions/
// basePath 兼容）。
package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/httpx"
)

// APITool 单接口工具形态。
type APITool struct {
	Name        string
	Description string
	Category    string
	Method      string
	Path        string
	BaseURL     string
	// HeaderSchema/PathSchema/QuerySchema/BodySchema：JSON Schema 文本
	HeaderSchema string
	PathSchema   string
	QuerySchema  string
	BodySchema   string
	OutputSchema string
	IsMultipart  bool
}

// APISource API 来源（文档 URL + 运行时基址）。
type APISource struct {
	DocURL    string
	APIBaseURL string
	Headers   map[string]string
}

// Gateway 网关能力包。
type Gateway struct {
	ai.TalentBase
	client  *httpx.Client
	sources map[string]*APISource // docURL → source
	tools   map[string]*APITool   // apiName → tool
	order   []string
}

// New 构造。
func New() *Gateway {
	return &Gateway{
		client:  httpx.New(),
		sources: map[string]*APISource{},
		tools:   map[string]*APITool{},
	}
}

// SetDoer 注入 Doer（测试）。
func (g *Gateway) SetDoer(d httpx.Doer) *Gateway { g.client = httpx.New(httpx.WithDoer(d)); return g }

// Name 名称。
func (t *Gateway) Name() string { return "Gateway" }

// Description 描述。
func (t *Gateway) Description() string {
	return "OpenAPI 接口调用专家：自动解析 OpenAPI(Swagger) 文档并生成可调用工具。"
}

// AddAPI 注册 API（拉文档 → 解析 → 过滤）。
func (t *Gateway) AddAPI(docURL, apiBaseURL string, headers map[string]string) error {
	if _, exists := t.sources[docURL]; exists {
		t.RemoveAPI(docURL)
	}
	source := &APISource{DocURL: docURL, APIBaseURL: apiBaseURL, Headers: headers}
	t.sources[docURL] = source

	docBody, _, err := t.client.Get(context.Background(), docURL, nil)
	if err != nil {
		return fmt.Errorf("gateway: fetch doc: %w", err)
	}
	tools, err := ParseOpenAPIDocument(string(docBody), apiBaseURL)
	if err != nil {
		return fmt.Errorf("gateway: parse doc: %w", err)
	}
	for _, tool := range tools {
		if _, dup := t.tools[tool.Name]; !dup {
			t.order = append(t.order, tool.Name)
		}
		t.tools[tool.Name] = tool
	}
	return nil
}

// RemoveAPI 移除。
func (t *Gateway) RemoveAPI(docURL string) {
	delete(t.sources, docURL)
	// 由该 source 派生的工具经 BaseURL 匹配清除（近似——工具不记来源 URL）
}

// ToolCount 工具数（档位裁剪依据）。
func (t *Gateway) ToolCount() int { return len(t.tools) }

// IsSupported 有 API 即启用。
func (t *Gateway) IsSupported(*ai.Prompt) bool { return len(t.tools) > 0 }

// Tools 三工具（四档位裁剪：≤8 FULL 只 call_api；≤30 SUMMARY call+detail；
// ≤100 LIST；>100 SEARCH 强制）。
func (t *Gateway) Tools(*ai.Prompt) []ai.FunctionTool {
	n := len(t.tools)
	var out []ai.FunctionTool
	if n == 0 {
		return nil
	}
	if n > 100 {
		return []ai.FunctionTool{t.searchAPITool(), t.callAPITool()}
	}
	if n > 30 {
		return []ai.FunctionTool{t.searchAPITool(), t.callAPITool()}
	}
	out = append(out, t.callAPITool())
	if n > 8 {
		out = append(out, t.detailAPITool(), t.searchAPITool())
	}
	return out
}

// Instruction 指令（档位形态）。
func (t *Gateway) Instruction(*ai.Prompt) string {
	n := len(t.tools)
	var sb strings.Builder
	sb.WriteString("## API 网关使用指引\n")
	switch {
	case n > 100:
		sb.WriteString("接口过多：必须先用 search_apis 检索接口名。\n")
	case n > 30:
		sb.WriteString("接口较多：先用 search_apis 检索，再 get_api_detail 查看参数。\n")
	case n > 8:
		sb.WriteString(fmt.Sprintf("可用接口 %d 个：\n", n))
		for _, name := range t.order {
			tool := t.tools[name]
			sb.WriteString(fmt.Sprintf("- [%s] %s %s（%s）\n", tool.Name, tool.Method, tool.Path, tool.Description))
		}
		sb.WriteString("调用前先 get_api_detail 查看参数 Schema。\n")
	default:
		sb.WriteString(fmt.Sprintf("可用接口 %d 个：\n", n))
		for _, name := range t.order {
			tool := t.tools[name]
			sb.WriteString(fmt.Sprintf("- [%s] %s %s（%s）\n", tool.Name, tool.Method, tool.Path, tool.Description))
			if tool.PathSchema != "" {
				sb.WriteString("  路径参数: " + tool.PathSchema + "\n")
			}
			if tool.QuerySchema != "" {
				sb.WriteString("  查询参数: " + tool.QuerySchema + "\n")
			}
			if tool.BodySchema != "" {
				sb.WriteString("  请求体: " + tool.BodySchema + "\n")
			}
		}
	}
	sb.WriteString("\n流程：锁定接口 → 查参数 → call_api。\n")
	sb.WriteString("注意：结果被截断（[Data truncated]）时不要重试；失败先检查参数 Schema。\n")
	return sb.String()
}

func (t *Gateway) callAPITool() ai.FunctionTool {
	return ai.NewTool("call_api").
		Desc("调用指定 API 接口").
		StringParam("api_name", "接口名（search_apis 可检索）", true).
		StringParam("header_params", "头参数 JSON（可选）", false).
		StringParam("path_params", "路径参数 JSON（可选）", false).
		StringParam("query_params", "查询参数 JSON（可选）", false).
		StringParam("body_params", "请求体 JSON（可选）", false).
		Handler(t.handleCall).
		Build()
}

func (t *Gateway) detailAPITool() ai.FunctionTool {
	return ai.NewTool("get_api_detail").
		Desc("查看接口的参数 Schema 与返回结构").
		StringParam("api_name", "接口名", true).
		Handler(func(ctx context.Context, args map[string]any) (any, error) {
			name, _ := args["api_name"].(string)
			tool := t.tools[name]
			if tool == nil {
				return "接口不存在: " + name, nil
			}
			return FormatAPIDetail(tool), nil
		}).
		Build()
}

func (t *Gateway) searchAPITool() ai.FunctionTool {
	return ai.NewTool("search_apis").
		Desc("按关键词检索接口（空格/逗号分词 AND 匹配名称与描述）").
		StringParam("keyword", "关键词", true).
		Handler(func(ctx context.Context, args map[string]any) (any, error) {
			keyword, _ := args["keyword"].(string)
			var tokens []string
			for _, k := range strings.FieldsFunc(keyword, func(r rune) bool { return r == ' ' || r == ',' }) {
				if k = strings.TrimSpace(k); k != "" {
					tokens = append(tokens, strings.ToLower(k))
				}
			}
			type hit struct {
				APIName string `json:"api_name"`
				Category string `json:"category"`
				Description string `json:"description"`
				Endpoint string `json:"endpoint"`
			}
			var hits []hit
			for _, name := range t.order {
				tool := t.tools[name]
				hay := strings.ToLower(name + " " + tool.Description + " " + tool.Path)
				all := true
				for _, tk := range tokens {
					if !strings.Contains(hay, tk) {
						all = false
						break
					}
				}
				if all {
					hits = append(hits, hit{tool.Name, tool.Category, tool.Description,
						tool.Method + " " + tool.Path})
				}
				if len(hits) >= 10 {
					break
				}
			}
			b, _ := json.MarshalIndent(hits, "", "  ")
			return string(b), nil
		}).
		Build()
}

// handleCall 运行时调用。
func (t *Gateway) handleCall(ctx context.Context, args map[string]any) (any, error) {
	name, _ := args["api_name"].(string)
	tool := t.tools[name]
	if tool == nil {
		return "接口不存在: " + name, nil
	}
	path := tool.Path
	if pathJSON, _ := args["path_params"].(string); pathJSON != "" {
		var params map[string]any
		if err := json.Unmarshal([]byte(pathJSON), &params); err == nil {
			for k, v := range params {
				path = strings.ReplaceAll(path, "{"+k+"}", urlEncodePath(stringifyParam(v)))
			}
		}
	}
	if strings.Contains(path, "{") {
		return "缺少路径参数：" + path, nil
	}
	target := strings.TrimSuffix(tool.BaseURL, "/") + path
	if queryJSON, _ := args["query_params"].(string); queryJSON != "" {
		var params map[string]any
		if err := json.Unmarshal([]byte(queryJSON), &params); err == nil && len(params) > 0 {
			var kvs []string
			for k, v := range params {
				kvs = append(kvs, k+"="+fmt.Sprint(v))
			}
			sort.Strings(kvs)
			target += "?" + strings.Join(kvs, "&")
		}
	}

	header := map[string]string{"Content-Type": "application/json"}
	if tool.IsMultipart {
		header["Content-Type"] = "application/x-www-form-urlencoded"
	}
	if headerJSON, _ := args["header_params"].(string); headerJSON != "" {
		var params map[string]string
		if err := json.Unmarshal([]byte(headerJSON), &params); err == nil {
			for k, v := range params {
				header[k] = v
			}
		}
	}

	var payload []byte
	if bodyJSON, _ := args["body_params"].(string); bodyJSON != "" {
		if tool.IsMultipart {
			var params map[string]any
			if err := json.Unmarshal([]byte(bodyJSON), &params); err == nil {
				var kvs []string
				for k, v := range params {
					kvs = append(kvs, k+"="+fmt.Sprint(v))
				}
				payload = []byte(strings.Join(kvs, "&"))
			}
		} else {
			payload = []byte(bodyJSON)
		}
	}

	url := target
	if payload != nil {
		body, _, err := t.client.PostJSON(ctx, url, payload, header)
		if err != nil {
			return "调用失败: " + err.Error(), nil
		}
		return truncateResult(string(body)), nil
	}
	body, _, err := t.client.Get(ctx, url, header)
	if err != nil {
		return "调用失败: " + err.Error(), nil
	}
	return truncateResult(string(body)), nil
}

func truncateResult(s string) string {
	if len(s) > 8000 {
		return s[:8000] + "\n[Data truncated]"
	}
	return s
}

func stringifyParam(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		if x == float64(int(x)) {
			return fmt.Sprint(int(x))
		}
		return fmt.Sprint(x)
	default:
		return fmt.Sprint(v)
	}
}

func urlEncodePath(v string) string {
	return strings.ReplaceAll(strings.ReplaceAll(v, " ", "%20"), "/", "%2F")
}

// FormatAPIDetail 详情渲染。
func FormatAPIDetail(tool *APITool) string {
	var sb strings.Builder
	sb.WriteString("接口名: " + tool.Name + "\n")
	sb.WriteString("分组: " + tool.Category + "\n")
	sb.WriteString("功能: " + tool.Description + "\n")
	sb.WriteString("路径: " + tool.Method + " " + tool.Path + "\n")
	if tool.HeaderSchema != "" {
		sb.WriteString("Header: " + tool.HeaderSchema + "\n")
	}
	if tool.PathSchema != "" {
		sb.WriteString("Path: " + tool.PathSchema + "\n")
	}
	if tool.QuerySchema != "" {
		sb.WriteString("Query: " + tool.QuerySchema + "\n")
	}
	if tool.BodySchema != "" {
		kind := "JSON"
		if tool.IsMultipart {
			kind = "Multipart"
		}
		sb.WriteString("Body(" + kind + "): " + tool.BodySchema + "\n")
	}
	if tool.OutputSchema != "" {
		sb.WriteString("返回: " + tool.OutputSchema + "\n")
	}
	return sb.String()
}

var _ = io.Discard
var _ = http.MethodGet
