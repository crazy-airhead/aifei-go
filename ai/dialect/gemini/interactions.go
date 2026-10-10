package gemini

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/internal/dialectkit"
)

// InteractionsDialect Gemini Interactions API 方言（/interactions 端点，model
// 在请求体而非 URL 路径）：input[] step 数组替代 contents[]、system_instruction
// 纯字符串、generation_config snake_case 白名单、tools 扁平数组、流式
// event_type 帧协议（interaction.created/step.start/delta/stop/...）、
// thought signature 经 google.interactions 协议状态回放。
type InteractionsDialect struct{}

func init() {
	// 先于 generateContent 方言（30）与默认 openai（100）：URL 特征互斥，
	// interactions 后缀优先探测。
	ai.RegisterDialect(&InteractionsDialect{}, 29)
}

func (d *InteractionsDialect) Name() string    { return "google-interactions" }
func (d *InteractionsDialect) IsDefault() bool { return false }

// Matched standard 命中（google-interactions；gemini-interactions 为弃用别名）；
// 否则空 standard 且 URL（剥 query/fragment 后）以 /interactions 结尾。
// 注意是 endsWith 判定（javadoc 写「包含」不准确，Java 测试锁定后缀语义）。
func (d *InteractionsDialect) Matched(cfg *ai.Config) bool {
	if cfg == nil {
		return false
	}
	switch strings.ToLower(cfg.StandardOrProvider()) {
	case "google-interactions", "gemini-interactions":
		return true
	}
	if cfg.StandardOrProvider() != "" {
		return false
	}
	apiURL := cfg.APIURL
	if i := strings.Index(apiURL, "#"); i >= 0 {
		apiURL = apiURL[:i]
	}
	if i := strings.Index(apiURL, "?"); i >= 0 {
		apiURL = apiURL[:i]
	}
	return strings.HasSuffix(apiURL, "/interactions")
}

// CustomizeHeader 与 generateContent 方言一致：x-goog-api-key 鉴权 + 流式 Accept。
func (d *InteractionsDialect) CustomizeHeader(cfg *ai.Config, opts *ai.ChatOptions, isStream bool, header map[string]string) {
	if cfg != nil && cfg.APIKey != "" {
		header["x-goog-api-key"] = cfg.APIKey
		delete(header, "Authorization")
	}
	if isStream {
		header["Accept"] = "text/event-stream"
	}
}

// BuildRequestJSON 委托请求构建器；URL 归一（版本段补 v1/、interactions 后缀），
// 流式追加 alt=sse（仅缺失时，只加不删——对照 chat 方言的非流式删行为）。
func (d *InteractionsDialect) BuildRequestJSON(cfg *ai.Config, opts *ai.ChatOptions, msgs []*ai.Message, isStream bool) ([]byte, string, error) {
	body, err := buildInteractionsRequest(cfg, opts, msgs, isStream)
	if err != nil {
		return nil, "", err
	}
	url := interactionsURL(cfg.APIURL)
	if isStream && !altSSEPresent(url) {
		url = appendQueryParameter(url, "alt", "sse")
	}
	return body, url, nil
}

func (d *InteractionsDialect) PrepareOutputSchemaInstruction(schema string, sb *strings.Builder) {
	// interactions 用 response_format 数组承载结构化输出，无指令注入
}
func (d *InteractionsDialect) PrepareOutputFormatOptions(opts *ai.ChatOptions) {}

// BuildAssistantToolCallMessage 工具循环回放消息：ToolCalls + thought 签名
// 协议状态（google.interactions 命名空间）。请求侧的 steps 数组形态由
// buildInteractionsRequest 从该消息重建（thought step + function_call steps）。
func (d *InteractionsDialect) BuildAssistantToolCallMessage(acc *ai.Accumulator, builders map[string]*ai.ToolCallBuilder) *ai.Message {
	var calls []ai.ToolCall
	for _, key := range acc.ToolCallOrder() {
		b, ok := builders[key]
		if !ok {
			continue
		}
		calls = append(calls, ai.ToolCall{
			Index: b.Index, ID: b.ID, Name: b.Name,
			ArgumentsStr: sanitizeSnapshotArguments(b.Args()),
		})
	}
	if len(calls) == 0 {
		return nil
	}
	msg := &ai.AssistantMessage{ToolCalls: calls}
	if m := acc.Message(); m != nil {
		msg.Text, msg.Thinking = m.Text, m.Thinking
	}
	if sig := acc.ThinkingSignature(); sig != "" {
		msg.ProtocolStates = map[string]any{
			interactionsProtocolID: createSignatureState(&calls[0], 0, sig),
		}
		ai.BindProtocolStateHashes(msg)
	}
	return &ai.Message{Role: ai.RoleAssistant, Content: msg}
}

// BuildAssistantMessageByToolMessages returnDirect：工具结果合成最终 assistant 消息
//（基类默认实现，与 generateContent 方言一致）。
func (d *InteractionsDialect) BuildAssistantMessageByToolMessages(msg *ai.AssistantMessage, toolMsgs []*ai.Message) *ai.AssistantMessage {
	return (&Dialect{}).BuildAssistantMessageByToolMessages(msg, toolMsgs)
}

// ParseResponseJSON 委托响应解析器（interactions 协议无 [DONE] 终标）。
func (d *InteractionsDialect) ParseResponseJSON(ctx *ai.StreamContext, body string) {
	parseInteractionsResponse(ctx, body)
}

// ParseAssistantMessage steps 数组节点 → 聚合助手消息（思考签名独立 thought
// step + function_call steps；model_output 提供正文）。
func (d *InteractionsDialect) ParseAssistantMessage(acc *ai.Accumulator, node map[string]any) []*ai.AssistantMessage {
	arr, ok := node["steps"].([]any)
	if !ok {
		// steps 缺失时把 node 本身视作单 step（宽容网关）
		if _, isStep := node["type"]; isStep {
			arr = []any{node}
		} else {
			return nil
		}
	}

	var text strings.Builder
	var toolCalls []ai.ToolCall
	var pendingSig string
	for i, v := range arr {
		step, ok := v.(map[string]any)
		if !ok {
			continue
		}
		switch dialectkit.GetStr(step, "type") {
		case "thought":
			sig := dialectkit.GetStr(step, "signature")
			if sig == "" {
				sig = dialectkit.GetStr(step, "thought_signature") // 旧网关兼容
			}
			if sig != "" {
				pendingSig = sig
			}
		case "function_call":
			name := dialectkit.GetStr(step, "name")
			id := dialectkit.GetStr(step, "id")
			if id == "" {
				id = name
			}
			argsStrB, argsMap := stepArguments(step["arguments"])
			argsStr := string(argsStrB)
			toolCalls = append(toolCalls, ai.ToolCall{
				Index: strconv.Itoa(i), ID: id, Name: name,
				ArgumentsStr: argsStr, Arguments: argsMap,
			})
		case "model_output":
			if s := contentArrayText(step["content"]); s != "" {
				text.WriteString(s)
			}
		}
	}

	if len(toolCalls) == 0 && text.Len() == 0 {
		return nil
	}
	msg := &ai.AssistantMessage{Text: text.String(), ToolCalls: toolCalls}
	if len(toolCalls) > 0 && pendingSig != "" {
		if state := createSignatureState(&toolCalls[0], 0, pendingSig); state != nil {
			msg.ProtocolStates = map[string]any{interactionsProtocolID: state}
		}
		ai.BindProtocolStateHashes(msg)
	}
	return []*ai.AssistantMessage{msg}
}

// stepArguments arguments 仅 object 采纳（字符串走多根串接净化，截断降级 {}）。
func stepArguments(v any) ([]byte, map[string]any) {
	switch args := v.(type) {
	case map[string]any:
		return mustJSON(args), args
	case string:
		m, _ := lastJSONObject(args)
		if m == nil {
			m, _ = dialectkit.ExtractJSONObject(args)
		}
		if m == nil {
			m = map[string]any{}
		}
		return mustJSON(m), m
	default:
		return []byte("{}"), map[string]any{}
	}
}

// contentArrayText content 数组的文本拼接（无数组时回退字符串）。
func contentArrayText(v any) string {
	arr, ok := v.([]any)
	if !ok {
		if s, ok := v.(string); ok {
			return s
		}
		return ""
	}
	var sb strings.Builder
	for _, item := range arr {
		if m, ok := item.(map[string]any); ok {
			if s := dialectkit.GetStr(m, "text"); s != "" {
				sb.WriteString(s)
			}
		}
	}
	return sb.String()
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("null")
	}
	return b
}

// interactionsURL 端点归一（先剥 #fragment 再分离 ?query；重拼回）：
// 已以 /interactions 结尾原样；/interactions/ 去尾斜杠；否则确保尾部 /、
// 版本段缺失补 v1/、追加 interactions（/v1alpha/ 中 /v1 子串命中不双补）。
func interactionsURL(apiURL string) string {
	query := ""
	if i := strings.Index(apiURL, "#"); i >= 0 {
		apiURL = apiURL[:i]
	}
	if i := strings.Index(apiURL, "?"); i >= 0 {
		query = apiURL[i+1:]
		apiURL = apiURL[:i]
	}

	switch {
	case strings.HasSuffix(apiURL, "/interactions"):
		// 原样
	case strings.HasSuffix(apiURL, "/interactions/"):
		apiURL = strings.TrimSuffix(apiURL, "/")
	default:
		if !strings.HasSuffix(apiURL, "/") {
			apiURL += "/"
		}
		if !interactionsVersionRegex.MatchString(apiURL) {
			apiURL += "v1/"
		}
		apiURL += "interactions"
	}

	if query != "" {
		return apiURL + "?" + query
	}
	return apiURL
}

// interactionsVersionRegex /v\d+(beta)?/? 子串探测（find 语义）。
var interactionsVersionRegex = regexp.MustCompile(`/v\d+(?:beta)?/?`)

func altSSEPresent(url string) bool {
	return altSSERegex.MatchString(url)
}

var altSSERegex = regexp.MustCompile(`[?&]alt=sse(?:&.*)?$`)

// appendQueryParameter 追加查询参数（已有 query 用 & 连接；与 gemini.go 的
// addQueryParameter（query 段内操作）职责不同）。
func appendQueryParameter(url, key, value string) string {
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	return url + sep + key + "=" + value
}

// InteractionsURL 端点归一的导出面（测试与诊断用）。
func InteractionsURL(apiURL string) string { return interactionsURL(apiURL) }
