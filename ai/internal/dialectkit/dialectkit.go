// Package dialectkit 跨方言共享辅助（P1-b 方言矩阵沉淀）：JSON 节点读取、
// 消息内容读取、接口地址补全、出站参数净化、错误消息提取、帧消息事件发布。
// 依赖方向：dialectkit → 根包 ai；各方言子包 import 两者（无环）。
package dialectkit

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai"
)

var (
	// VersionRe 已带版本号（/v1、/v4…）的 URL 后缀。
	VersionRe = regexp.MustCompile(`/v\d+/?$`)
	// AzureDeployRe Azure 部署形态（/openai/deployments/{name}）后缀。
	AzureDeployRe = regexp.MustCompile(`/openai/deployments/[^/]+/?$`)
)

// ---- JSON 节点读取小工具（各方言解析侧共用）----

// GetStr 节点字符串值（nil 安全；非字符串以 fmt.Sprint 投影）。
func GetStr(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[k]; ok && v != nil {
		return fmt.Sprint(v)
	}
	return ""
}

// GetInt 节点整数值（float64/int64/string 宽容转换）。
func GetInt(m map[string]any, k string) int64 {
	if m == nil {
		return 0
	}
	switch v := m[k].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	case json.Number:
		n, err := v.Int64()
		if err == nil {
			return n
		}
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		if err == nil {
			return n
		}
	}
	return 0
}

// GetBool 节点布尔值（nil / 缺省 false）。
func GetBool(m map[string]any, k string) bool {
	if m == nil {
		return false
	}
	if b, ok := m[k].(bool); ok {
		return b
	}
	return false
}

// GetMapStr 子节点字符串值（m[outer][inner]）。
func GetMapStr(m map[string]any, outer, inner string) string {
	sub, _ := m[outer].(map[string]any)
	return GetStr(sub, inner)
}

// GetNode 子对象节点（nil 安全）。
func GetNode(m map[string]any, k string) map[string]any {
	sub, _ := m[k].(map[string]any)
	return sub
}

// GetArray 子数组节点（nil 安全）。
func GetArray(m map[string]any, k string) []any {
	arr, _ := m[k].([]any)
	return arr
}

// ---- 消息内容读取 ----

// ContentText 消息内容的文本投影（string / *Contents / TextBlock / 聚合消息）。
func ContentText(content any) string {
	switch c := content.(type) {
	case nil:
		return ""
	case string:
		return c
	case *ai.Contents:
		return c.Text()
	case ai.TextBlock:
		return c.Text
	case *ai.AssistantMessage:
		return c.Text
	case *ai.ToolMessage:
		return c.ResultText()
	}
	return ""
}

// MessageBlocks 多模态块形态（[]ContentBlock / ContentBlock / *Contents 的块部分）；
// 纯 string 返回 nil。
func MessageBlocks(content any) []ai.ContentBlock {
	switch c := content.(type) {
	case ai.ContentBlock:
		return []ai.ContentBlock{c}
	case []ai.ContentBlock:
		return c
	case *ai.Contents:
		if c.IsMultiModal() {
			return c.Blocks()
		}
		return nil
	case *ai.AssistantMessage:
		return c.Blocks
	}
	return nil
}

// AssistantOf assistant 消息的聚合内容（非聚合形态返回 nil）。
func AssistantOf(m *ai.Message) *ai.AssistantMessage {
	if m == nil {
		return nil
	}
	if am, ok := m.Content.(*ai.AssistantMessage); ok {
		return am
	}
	return nil
}

// FirstMetaStr 块 metas 中第一个非空键值（audio_id/id 等侧车引用）。
func FirstMetaStr(metas map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := metas[k]; ok {
			if s := fmt.Sprint(v); s != "" {
				return s
			}
		}
	}
	return ""
}

// IsMediaPlayable 会话压缩后 data 空且无 url/侧车 id 时不可回传。
func IsMediaPlayable(url string, data []byte, metas map[string]any) bool {
	if url != "" || len(data) > 0 {
		return true
	}
	return FirstMetaStr(metas, "audio_id", "id", "image_generation_id") != ""
}

// MediaDataString URL 优先；否则 data:base64。可播性由调用方判断。
func MediaDataString(url string, data []byte, kind, mime string) string {
	if !IsMediaPlayable(url, data, nil) {
		return ""
	}
	if url != "" {
		return url
	}
	if mime == "" {
		switch kind {
		case "image":
			mime = "image/jpeg"
		case "audio":
			mime = "audio/mpeg"
		default:
			mime = "video/mpeg"
		}
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// DecodeDataURL data:<mime>;base64,<payload> 解码。
func DecodeDataURL(u string) ([]byte, string) {
	rest := strings.TrimPrefix(u, "data:")
	semi := strings.Index(rest, ";")
	comma := strings.Index(rest, ",")
	if semi < 0 || comma < 0 || semi > comma {
		return nil, ""
	}
	mime := rest[:semi]
	data, err := base64.StdEncoding.DecodeString(rest[comma+1:])
	if err != nil {
		return nil, ""
	}
	return data, mime
}

// ---- 接口地址补全 ----

// BuildAPIURL OpenAI 风格地址自动补全（Java OpenaiDialectSupport.buildApiUrl 对标）：
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
	case AzureDeployRe.MatchString(base), VersionRe.MatchString(base):
		result = base + "/" + endpointPath
	default:
		result = base + "/v1/" + endpointPath
	}
	return result + query
}

// ResolveChatCompletionsThinkingField 解析目标 Chat Completions 协议用于回放
// 历史思考的消息字段（Java AbstractChatDialect.resolveAssistantThinkingField）：
// OpenRouter → reasoning；DeepSeek 系 → reasoning_content；其余空（不回放）。
// 字段由目标配置决定，禁止把源消息的 reasoningFieldName 直接当 key。
func ResolveChatCompletionsThinkingField(cfg *ai.Config) string {
	if cfg == nil {
		return ""
	}
	provider := strings.ToLower(cfg.Provider)
	apiURL := strings.ToLower(cfg.APIURL)
	if strings.Contains(provider, "openrouter") || strings.Contains(apiURL, "openrouter") {
		return "reasoning"
	}
	model := strings.ToLower(cfg.Model)
	if strings.Contains(model, "deepseek") || strings.Contains(provider, "deepseek") ||
		strings.Contains(apiURL, "deepseek") {
		return "reasoning_content"
	}
	return ""
}

// ---- 出站参数净化 ----

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

// SanitizeArgsMap 结构化参数净化：仅 object 形态原样；其余 nil。
func SanitizeArgsMap(args map[string]any) map[string]any {
	if args == nil {
		return nil
	}
	return args
}

// ---- 错误提取 ----

// ExtractErrorMessage 标准 error 节点 → "[type] message"；非对象原样。
func ExtractErrorMessage(node any) string {
	if node == nil {
		return "Unknown error"
	}
	if m, ok := node.(map[string]any); ok {
		message := GetStr(m, "message")
		typ := GetStr(m, "type")
		if typ == "" {
			typ = GetStr(m, "code")
		}
		if message == "" {
			message = "Unknown error"
		}
		if typ != "" {
			return "[" + typ + "] " + message
		}
		return message
	}
	if s := fmt.Sprint(node); s != "" {
		return s
	}
	return "Unknown error"
}

// ---- 帧消息事件发布（message/delta 帧 → assistant 消息 → 事件管线）----

// PublishMessageEvents 帧消息 → 事件通道（聚合由 Accumulator.Merge 统一完成）：
// 终态载体合入 + THINKING/TEXT delta + 工具调用 START 去重 + 媒体块 MEDIA_DONE。
// openai/ollama/dashscope 的帧消息管线共用。
func PublishMessageEvents(ctx *ai.StreamContext, msg *ai.AssistantMessage) {
	acc := ctx.Acc()
	acc.MergeTerminalMessage(msg)

	if msg.Thinking != "" {
		ev := ctx.Event(ai.EventThinkingDelta)
		ev.Text = msg.Thinking
		ctx.Emit(ev)
	}
	if msg.Text != "" {
		ev := ctx.Event(ai.EventTextDelta)
		ev.Text = msg.Text
		ctx.Emit(ev)
	}

	if len(msg.ToolCalls) > 0 {
		started, _ := ctx.AttrIfAbsent("__startedToolCalls", func() any {
			return map[string]struct{}{}
		}).(map[string]struct{})
		for _, call := range msg.ToolCalls {
			key := ToolCallEventKey(&call)
			if key == "" {
				EmitToolCallEvent(ctx, ai.EventToolCallStart, &call, "")
			} else if _, ok := started[key]; !ok {
				started[key] = struct{}{}
				EmitToolCallEvent(ctx, ai.EventToolCallStart, &call, "")
			}
			if call.ArgumentsStr != "" {
				EmitToolCallEvent(ctx, ai.EventToolCallArgsDelta, &call, call.ArgumentsStr)
			}
		}
	}

	for _, b := range msg.Blocks {
		if b == nil || b.Kind() == "text" {
			continue
		}
		ev := ctx.Event(ai.EventMediaDone)
		ev.Block = b
		ctx.Emit(ev)
	}
}

// EmitToolCallEvent 工具调用事件（"idx:<n>" 形态 Index 解析为数字 index）。
func EmitToolCallEvent(ctx *ai.StreamContext, t ai.EventType, call *ai.ToolCall, text string) {
	ev := ctx.Event(t)
	ev.ToolCall = call
	ev.ToolCallID = call.ID
	ev.Text = text
	if idx := ToolCallIndex(call); idx >= 0 {
		ev.Index = idx
	}
	ctx.Emit(ev)
}

// ToolCallIndex "idx:<n>" → n；其余 -1。
func ToolCallIndex(call *ai.ToolCall) int {
	if call == nil || call.Index == "" {
		return -1
	}
	idx := strings.TrimPrefix(call.Index, "idx:")
	n, err := strconv.Atoi(idx)
	if err != nil {
		return -1
	}
	return n
}

// ToolCallEventKey 工具调用 START 去重键（index 优先于 id）。
func ToolCallEventKey(call *ai.ToolCall) string {
	if call == nil {
		return ""
	}
	if call.Index != "" {
		return "index:" + call.Index
	}
	if call.ID != "" {
		return "id:" + call.ID
	}
	return ""
}

// EmitError 错误事件（可选携带原始帧）。
func EmitError(ctx *ai.StreamContext, err error, raw map[string]any) {
	ev := ctx.Event(ai.EventError)
	ev.RawType = "error"
	ev.Err = err
	if raw != nil {
		if data, e := json.Marshal(raw); e == nil {
			ev.Raw = data
		}
	}
	ctx.Emit(ev)
}

// AdoptProtocolStates 把聚合侧协议状态迁移到出站消息：克隆状态值并清空旧摘要，
// 再按出站消息的语义重新补绑。BuildAssistantToolCallMessage 组装的消息与
// acc.Message() 聚合快照字段形态不同（工具调用有无 Arguments map、参数经净化），
// 沿用聚合边界绑定的摘要会让回放校验必然失败（签名静默丢失）。
func AdoptProtocolStates(dst *ai.AssistantMessage, src map[string]any) {
	if dst == nil || len(src) == 0 {
		return
	}
	states := make(map[string]any, len(src))
	for k, v := range src {
		switch s := v.(type) {
		case *ai.ProtocolState:
			states[k] = &ai.ProtocolState{Version: s.Version, Data: s.Data}
		default:
			states[k] = v
		}
	}
	dst.ProtocolStates = states
	ai.BindProtocolStateHashes(dst)
}
