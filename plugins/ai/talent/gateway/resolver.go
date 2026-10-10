// resolver.go OpenAPI 文档解析（V3 为主 + V2 兼容）：paths×methods → APITool；
// 参数四桶（header/path/query/body）JSON Schema；$ref 展开；multipart 探测；
// outputSchema 取 200/201/default 首个媒体类型。
package gateway

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ParseOpenAPIDocument 解析 OpenAPI JSON 文档（V3 或 Swagger V2）。
func ParseOpenAPIDocument(doc, apiBaseURL string) ([]*APITool, error) {
	var root map[string]any
	if err := json.Unmarshal([]byte(doc), &root); err != nil {
		return nil, fmt.Errorf("gateway: doc parse: %w", err)
	}

	// V3 vs V2 判定（openapi 键存在即 V3；两者解析路径同构，仅 requestBody 与
	// basePath 差异在 buildAPITool 内分别处理）
	_, _ = root["openapi"]
	pathsNode, _ := root["paths"].(map[string]any)
	if pathsNode == nil {
		return nil, fmt.Errorf("gateway: doc has no paths")
	}

	// 组件/definitions 表（$ref 展开源）
	components := map[string]any{}
	if v3c, ok := root["components"].(map[string]any); ok {
		if schemas, ok := v3c["schemas"].(map[string]any); ok {
			for k, v := range schemas {
				components[k] = v
			}
		}
	}
	if v2d, ok := root["definitions"].(map[string]any); ok {
		for k, v := range v2d {
			components[k] = v
		}
	}

	// 基址：V3 servers[0].url / V2 basePath + host
	baseURL := apiBaseURL
	if baseURL == "" {
		if servers, ok := root["servers"].([]any); ok && len(servers) > 0 {
			if s, ok := servers[0].(map[string]any); ok {
				baseURL, _ = s["url"].(string)
			}
		}
	}

	var tools []*APITool
	// 路径序稳定
	paths := make([]string, 0, len(pathsNode))
	for p := range pathsNode {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, path := range paths {
		item, _ := pathsNode[path].(map[string]any)
		if item == nil {
			continue
		}
		// PathItem 级公共参数（V3/V2 同键）
		shared := bucketParams(item["parameters"])

		for _, method := range []string{"get", "put", "post", "delete", "patch"} {
			op, _ := item[method].(map[string]any)
			if op == nil {
				continue
			}
			tool := buildAPITool(op, method, path, baseURL, components, shared)
			tools = append(tools, tool)
		}
	}
	return tools, nil
}

// buildAPITool 单操作 → 工具。
func buildAPITool(op map[string]any, method, path, baseURL string,
	components map[string]any, shared map[string]*paramBucket) *APITool {
	name := ""
	if v, ok := op["operationId"].(string); ok && v != "" {
		name = v
	} else {
		name = method + "_" + strings.ReplaceAll(strings.ReplaceAll(path, "/", "_"), "{", "")
		name = strings.ReplaceAll(name, "}", "")
	}

	desc := ""
	if v, ok := op["summary"].(string); ok && v != "" {
		desc = v
	} else if v, ok := op["description"].(string); ok {
		desc = v
	}

	category := "常规接口"
	if tags, ok := op["tags"].([]any); ok && len(tags) > 0 {
		if t, ok := tags[0].(string); ok && t != "" {
			category = t
		}
	}

	// 操作级参数合并进 PathItem 级（同 name+in 后者胜——Java 同名覆盖语义；
	// 其余保留共享桶，非整桶替换）
	buckets := map[string]*paramBucket{
		"header": cloneBucket(shared["header"]),
		"path":   cloneBucket(shared["path"]),
		"query":  cloneBucket(shared["query"]),
	}
	for in, bucket := range bucketParams(op["parameters"]) {
		dst := buckets[in]
		if dst == nil {
			buckets[in] = bucket
			continue
		}
		for name, prop := range bucket.Properties {
			dst.Properties[name] = prop
			// 覆盖：从 Required 先移除再按声明补
			dst.Required = removeStr(dst.Required, name)
		}
		for _, name := range bucket.Required {
			dst.Required = append(dst.Required, name)
		}
	}

	tool := &APITool{
		Name: name, Description: desc, Category: category,
		Method: strings.ToUpper(method), Path: path, BaseURL: baseURL,
	}
	if b := buckets["path"]; b != nil && len(b.Properties) > 0 {
		b.Required = allKeys(b.Properties) // path 参数全部必填
		tool.PathSchema = schemaJSON(b)
	}
	if b := buckets["header"]; b != nil && len(b.Properties) > 0 {
		tool.HeaderSchema = schemaJSON(b)
	}
	if b := buckets["query"]; b != nil && len(b.Properties) > 0 {
		tool.QuerySchema = schemaJSON(b)
	}

	// requestBody（V3）/ body+consumes（V2）
	isMultipart := false
	if rb, ok := op["requestBody"].(map[string]any); ok {
		if content, ok := rb["content"].(map[string]any); ok {
			for media, spec := range content {
				if strings.Contains(media, "multipart") || strings.Contains(media, "form-urlencoded") {
					isMultipart = true
				}
				if spec, ok := spec.(map[string]any); ok {
					if schema, ok := spec["schema"].(map[string]any); ok {
						expanded := expandRef(schema, components)
						tool.BodySchema = objectSchemaJSON(expanded)
						break
					}
				}
			}
		}
	} else if bodyParams, ok := op["parameters"].([]any); ok {
		for _, p := range bodyParams {
			pm, _ := p.(map[string]any)
			if pm == nil {
				continue
			}
			if in, _ := pm["in"].(string); in == "body" {
				if schema, ok := pm["schema"].(map[string]any); ok {
					tool.BodySchema = objectSchemaJSON(expandRef(schema, components))
				}
			}
		}
		if consumes, ok := op["consumes"].([]any); ok {
			for _, c := range consumes {
				if s, _ := c.(string); strings.Contains(s, "multipart") {
					isMultipart = true
				}
			}
		}
	}
	// body 为 object 时展开 properties/required 到顶层（Java 同款）；非 object 包 body 字段
	tool.IsMultipart = isMultipart

	// outputSchema：responses 200/201/default 首个媒体类型
	tool.OutputSchema = extractOutputSchema(op["responses"], components)

	return tool
}

// paramBucket 参数桶（in → properties/required）。
type paramBucket struct {
	Properties map[string]map[string]any
	Required   []string
}

func cloneBucket(b *paramBucket) *paramBucket {
	if b == nil {
		return &paramBucket{Properties: map[string]map[string]any{}}
	}
	out := &paramBucket{Properties: map[string]map[string]any{}, Required: append([]string(nil), b.Required...)}
	for k, v := range b.Properties {
		out.Properties[k] = v
	}
	return out
}

// bucketParams parameters 数组 → 三桶。
func bucketParams(v any) map[string]*paramBucket {
	out := map[string]*paramBucket{
		"header": {Properties: map[string]map[string]any{}},
		"path":   {Properties: map[string]map[string]any{}},
		"query":  {Properties: map[string]map[string]any{}},
	}
	arr, _ := v.([]any)
	for _, p := range arr {
		pm, _ := p.(map[string]any)
		if pm == nil {
			continue
		}
		in, _ := pm["in"].(string)
		name, _ := pm["name"].(string)
		if name == "" {
			continue
		}
		bucket, ok := out[in]
		if !ok {
			continue
		}
		prop := map[string]any{"type": "string"}
		if t, ok := pm["type"].(string); ok && t != "" {
			prop["type"] = t // V2 直挂
		} else if sc, ok := pm["schema"].(map[string]any); ok {
			if t, ok := sc["type"].(string); ok && t != "" {
				prop["type"] = t // V3 schema.type
			}
			if e, ok := sc["enum"].([]any); ok {
				prop["enum"] = e
			}
		}
		if d, ok := pm["description"].(string); ok {
			prop["description"] = d
		}
		if e, ok := pm["enum"].([]any); ok {
			prop["enum"] = e
		}
		bucket.Properties[name] = prop
		if req, _ := pm["required"].(bool); req {
			bucket.Required = append(bucket.Required, name)
		}
	}
	return out
}

func removeStr(list []string, s string) []string {
	out := list[:0]
	for _, v := range list {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}

func allKeys(m map[string]map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func schemaJSON(b *paramBucket) string {
	s := map[string]any{"type": "object", "properties": b.Properties}
	if len(b.Required) > 0 {
		s["required"] = b.Required
	}
	out, _ := json.Marshal(s)
	return string(out)
}

// expandRef $ref 展开（#/components/schemas/X 或 #/definitions/X）。
func expandRef(schema map[string]any, components map[string]any) map[string]any {
	ref, _ := schema["$ref"].(string)
	if ref == "" {
		return schema
	}
	// 取末段名
	parts := strings.Split(ref, "/")
	name := parts[len(parts)-1]
	if target, ok := components[name].(map[string]any); ok {
		return target
	}
	return schema
}

// objectSchemaJSON object schema 直接序列化；非 object 包成 body 字段。
func objectSchemaJSON(schema map[string]any) string {
	if t, _ := schema["type"].(string); t == "object" {
		out, _ := json.Marshal(schema)
		return string(out)
	}
	wrapped := map[string]any{
		"type":       "object",
		"properties": map[string]any{"body": schema},
		"required":   []string{"body"},
	}
	out, _ := json.Marshal(wrapped)
	return string(out)
}

// extractOutputSchema 响应 schema。
func extractOutputSchema(responses any, components map[string]any) string {
	respMap, _ := responses.(map[string]any)
	if respMap == nil {
		return ""
	}
	for _, code := range []string{"200", "201", "default"} {
		resp, ok := respMap[code].(map[string]any)
		if !ok {
			continue
		}
		if content, ok := resp["content"].(map[string]any); ok && len(content) > 0 {
			// 首个媒体类型
			media := sortedFirstKey(content)
			if spec, ok := content[media].(map[string]any); ok {
				if schema, ok := spec["schema"].(map[string]any); ok {
					out, _ := json.Marshal(expandRef(schema, components))
					return string(out)
				}
			}
		}
		// V2：schema 直挂
		if schema, ok := resp["schema"].(map[string]any); ok {
			out, _ := json.Marshal(expandRef(schema, components))
			return string(out)
		}
	}
	return ""
}

func sortedFirstKey(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		return keys[0]
	}
	return ""
}
