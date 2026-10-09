// Package gemini 是 Google Gemini generateContent 方言：/models/{model}:generateContent
// 端点（流式 :streamGenerateContent + alt=sse）、x-goog-api-key 鉴权、
// contents/systemInstruction/generationConfig 请求形态、thought 思考分片、
// functionCall 累计快照聚合与 thoughtSignature 协议状态回放。
package gemini

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/internal/dialectkit"
)

// Dialect Gemini generateContent 方言。
type Dialect struct{}

func init() { ai.RegisterDialect(&Dialect{}, 30) }

func (d *Dialect) Name() string    { return "gemini" }
func (d *Dialect) IsDefault() bool { return false }

// Matched standard 命中（google/google-models/google-generate；gemini/gemini-models
// 为弃用别名）；否则空 standard 且 apiUrl 同时含 /v1|v1beta models 路径与端点标记。
func (d *Dialect) Matched(cfg *ai.Config) bool {
	if cfg == nil {
		return false
	}
	switch strings.ToLower(cfg.StandardOrProvider()) {
	case "google", "google-models", "google-generate", "gemini", "gemini-models":
		return true
	}
	if cfg.StandardOrProvider() != "" {
		return false
	}
	apiURL := cfg.APIURL
	if i := strings.Index(apiURL, "#"); i >= 0 {
		apiURL = apiURL[:i]
	}
	if apiURL == "" {
		return false
	}
	return (strings.Contains(apiURL, "/v1/models/") || strings.Contains(apiURL, "/v1beta/models/")) &&
		(strings.Contains(apiURL, ":generateContent") || strings.Contains(apiURL, ":streamGenerateContent"))
}

// CustomizeHeader x-goog-api-key 鉴权（非 Bearer）+ 流式 Accept: text/event-stream。
// 清除框架默认注入的 Authorization（Gemini 不认 Bearer，避免网关 401）。
func (d *Dialect) CustomizeHeader(cfg *ai.Config, opts *ai.ChatOptions, isStream bool, header map[string]string) {
	if cfg != nil && cfg.APIKey != "" {
		header["x-goog-api-key"] = cfg.APIKey
		delete(header, "Authorization")
	}
	if isStream {
		header["Accept"] = "text/event-stream"
	}
}

// ---- 请求侧 ----

// rootOptionKeys 直接落到请求根的选项（其余生成参数进 generationConfig）。
var rootOptionKeys = map[string]struct{}{"safetySettings": {}, "labels": {}}

// BuildRequestJSON {cachedContent?, systemInstruction?, contents, generationConfig?,
// tools?, toolConfig?}。生成类选项映射进 generationConfig（snake_case → camelCase）；
// 统一 thinking/reasoning_effort 最后合并（防被 generationConfig 反覆盖）。
func (d *Dialect) BuildRequestJSON(cfg *ai.Config, opts *ai.ChatOptions, msgs []*ai.Message, isStream bool) ([]byte, string, error) {
	if opts == nil {
		opts = &ai.ChatOptions{}
	}
	root := map[string]any{}

	// cachedContent：Gemini 显式 Context Caching（超长上下文/系统知识库）
	cachedContent := opts.OptionGet("cachedContent")
	if cachedContent == nil {
		cachedContent = opts.OptionGet("context_cache_id")
	}
	if s := fmt.Sprint(cachedContent); cachedContent != nil && s != "" {
		root["cachedContent"] = s
	}

	// systemInstruction：contents[].role 仅接受 user/model，system 消息上提
	if sysInst := buildSystemInstructionNode(msgs); sysInst != nil {
		root["systemInstruction"] = sysInst
	}

	contents := []any{}
	for _, m := range msgs {
		if m.Role == ai.RoleSystem {
			continue // 已在 systemInstruction 处理
		}
		if isSkippableThinkingOnly(m) {
			continue // 纯思考历史没有可安全回放的签名/正文
		}
		contents = append(contents, buildMessageNode(m))
	}
	root["contents"] = contents

	var reasoningEffort any
	var thinkingSwitch any
	if native, ok := opts.OptionGet("generationConfig").(map[string]any); ok {
		root["generationConfig"] = normalizeGenerationConfig(native)
	} else if native, ok := opts.OptionGet("generation_config").(map[string]any); ok {
		root["generationConfig"] = normalizeGenerationConfig(native)
	}
	for k, v := range opts.Raw {
		switch k {
		case "stream", "cachedContent", "context_cache_id", "tool_choice", "response_format":
			continue
		case "reasoning_effort":
			reasoningEffort = v
			continue
		case "thinking":
			if b, ok := v.(bool); ok {
				thinkingSwitch = b
				continue
			}
		case "generationConfig", "generation_config":
			if _, ok := v.(map[string]any); ok {
				continue
			}
		}
		if isGenerationOption(k) {
			generationConfig, _ := root["generationConfig"].(map[string]any)
			if generationConfig == nil {
				generationConfig = map[string]any{}
				root["generationConfig"] = generationConfig
			}
			setGenerationOption(generationConfig, k, v)
		} else if _, isRoot := rootOptionKeys[k]; isRoot {
			root[k] = v
		}
	}

	// outputSchema 使用 Gemini 原生结构化输出，而不只依赖提示词约束
	if opts.OutputSchema != "" {
		generationConfig, _ := root["generationConfig"].(map[string]any)
		if generationConfig == nil {
			generationConfig = map[string]any{}
			root["generationConfig"] = generationConfig
		}
		generationConfig["responseMimeType"] = "application/json"
		var schema map[string]any
		if json.Unmarshal([]byte(opts.OutputSchema), &schema) == nil && schema != nil {
			generationConfig["responseJsonSchema"] = schema
		}
		// 非法 schema 不写入协议字段；上层仍可通过提示词约束输出
	}

	// 最后合并 thinking / reasoning_effort，确保不被 generationConfig 反覆盖
	applyUnifiedThinkingOptions(root, cfg, opts, thinkingSwitch, reasoningEffort)

	buildToolsNode(root, opts)

	body, err := json.Marshal(root)
	if err != nil {
		return nil, "", err
	}
	url := ""
	if cfg != nil {
		url = buildAPIURL(cfg.APIURL, cfg.Model, isStream)
	}
	return body, url, nil
}

// buildAPIURL {base}/models/{model}:{generateContent|streamGenerateContent}：
// 已带端点标记则规范化并按需切换动词；未带版本段补 v1beta/；流式加 alt=sse。
func buildAPIURL(baseURL, model string, isStream bool) string {
	if baseURL == "" {
		return baseURL
	}
	if i := strings.Index(baseURL, "#"); i >= 0 {
		baseURL = baseURL[:i]
	}
	query := ""
	if i := strings.Index(baseURL, "?"); i >= 0 {
		query = baseURL[i+1:]
		baseURL = baseURL[:i]
	}

	if strings.Contains(baseURL, ":generateContent") || strings.Contains(baseURL, ":streamGenerateContent") {
		baseURL = strings.ReplaceAll(baseURL, ":streamGenerateContent", ":generateContent")
		if isStream {
			baseURL = strings.ReplaceAll(baseURL, ":generateContent", ":streamGenerateContent")
			query = addQueryParameter(query, "alt", "sse")
		} else {
			query = removeQueryParameter(query, "alt", "sse")
		}
		if query == "" {
			return baseURL
		}
		return baseURL + "?" + query
	}

	var sb strings.Builder
	sb.WriteString(baseURL)
	if !strings.HasSuffix(baseURL, "/") {
		sb.WriteByte('/')
	}
	// 版本段探测对裸结尾（…/v1beta）同样生效（Java 版只认带斜杠形态，裸版本段会
	// 重复补全出 …/v1beta/v1beta/…）；末尾补一个斜杠后统一按段内匹配
	versionProbe := strings.TrimRight(baseURL, "/") + "/"
	if !strings.Contains(versionProbe, "v1beta/") && !strings.Contains(versionProbe, "v1/") &&
		!strings.Contains(versionProbe, "v1alpha/") {
		sb.WriteString("v1beta/")
	}
	sb.WriteString("models/")
	sb.WriteString(model)
	if isStream {
		sb.WriteString(":streamGenerateContent")
		query = addQueryParameter(query, "alt", "sse")
	} else {
		sb.WriteString(":generateContent")
	}
	if query == "" {
		return sb.String()
	}
	return sb.String() + "?" + query
}

func addQueryParameter(query, key, value string) string {
	cleaned := removeQueryParameter(query, key, "")
	parameter := key + "=" + value
	if cleaned == "" {
		return parameter
	}
	return cleaned + "&" + parameter
}

func removeQueryParameter(query, key, expectedValue string) string {
	if query == "" {
		return ""
	}
	var result []string
	for _, item := range strings.Split(query, "&") {
		if item == "" {
			continue
		}
		pair := strings.SplitN(item, "=", 2)
		sameKey := key == pair[0]
		sameValue := expectedValue == "" || (len(pair) > 1 && expectedValue == pair[1])
		if sameKey && sameValue {
			continue
		}
		result = append(result, item)
	}
	return strings.Join(result, "&")
}

// PrepareOutputSchemaInstruction 输出 schema 指令仍走系统提示（协议侧由请求构建器
// 写入 generationConfig.responseMimeType/responseJsonSchema）。
func (d *Dialect) PrepareOutputSchemaInstruction(schema string, sb *strings.Builder) {
	sb.WriteString(fmt.Sprintf(schemaInstructionT, schema))
}

var schemaInstructionT = "\n\n## [IMPORTANT: OUTPUT FORMAT]\n" +
	"Format your response as a JSON object strictly following this schema:\n" +
	"<output_schema>\n%s\n</output_schema>\n" +
	"Output only the raw JSON, beginning with '{' and ending with '}'."

// PrepareOutputFormatOptions 由请求构建器写入 generationConfig（此处无操作）。
func (d *Dialect) PrepareOutputFormatOptions(opts *ai.ChatOptions) {}

// isGenerationOption 统一生成参数键（→ generationConfig）。
func isGenerationOption(key string) bool {
	switch key {
	case "temperature", "top_p", "top_k", "max_tokens", "max_completion_tokens",
		"frequency_penalty", "presence_penalty", "stop", "response_mime_type":
		return true
	}
	return false
}

func setGenerationOption(config map[string]any, key string, value any) {
	var target string
	switch key {
	case "top_p":
		target = "topP"
	case "top_k":
		target = "topK"
	case "max_tokens", "max_completion_tokens":
		target = "maxOutputTokens"
	case "frequency_penalty":
		target = "frequencyPenalty"
	case "presence_penalty":
		target = "presencePenalty"
	case "stop":
		target = "stopSequences"
	case "response_mime_type":
		target = "responseMimeType"
	default:
		target = key
	}
	config[target] = normalizedScalar(target, value)
}

// normalizedScalar YAML 常见字符串标量的 JSON 类型修正。
func normalizedScalar(key string, value any) any {
	s, ok := value.(string)
	if !ok {
		return value
	}
	text := strings.TrimSpace(s)
	switch key {
	case "maxOutputTokens", "topK":
		if n, err := parseIntStrict(text); err == nil {
			return n
		}
	case "temperature", "topP", "frequencyPenalty", "presencePenalty":
		if f, err := parseFloatStrict(text); err == nil {
			return f
		}
	}
	return value
}

func parseIntStrict(text string) (int64, error) { return strconv.ParseInt(text, 10, 64) }

func parseFloatStrict(text string) (float64, error) { return strconv.ParseFloat(text, 64) }

// normalizeGenerationConfig 完整保留原生 generationConfig，仅修正 YAML 常见
// 字符串标量的 JSON 类型。
func normalizeGenerationConfig(source map[string]any) map[string]any {
	config := make(map[string]any, len(source))
	for k, v := range source {
		config[k] = v
	}
	normalizeNumber(config, "temperature", false)
	normalizeNumber(config, "topP", false)
	normalizeNumber(config, "topK", true)
	normalizeNumber(config, "maxOutputTokens", true)
	normalizeNumber(config, "candidateCount", true)
	normalizeNumber(config, "seed", true)
	normalizeNumber(config, "presencePenalty", false)
	normalizeNumber(config, "frequencyPenalty", false)
	normalizeNumber(config, "logprobs", true)
	normalizeBoolean(config, "responseLogprobs")
	if thinking, ok := config["thinkingConfig"].(map[string]any); ok {
		normalizeBoolean(thinking, "includeThoughts")
		normalizeNumber(thinking, "thinkingBudget", true)
	}
	return config
}

func normalizeNumber(node map[string]any, key string, integer bool) {
	value, ok := node[key]
	if !ok || value == nil {
		return
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" {
		return
	}
	if integer {
		if n, err := parseIntStrict(text); err == nil {
			node[key] = n
		}
	} else if f, err := parseFloatStrict(text); err == nil {
		node[key] = f
	}
}

func normalizeBoolean(node map[string]any, key string) {
	value, ok := node[key]
	if !ok || value == nil {
		return
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if strings.EqualFold(text, "true") {
		node[key] = true
	} else if strings.EqualFold(text, "false") {
		node[key] = false
	}
}

// buildSystemInstructionNode 多个 system 消息合并为一段文本 → {parts:[{text}]}。
func buildSystemInstructionNode(msgs []*ai.Message) map[string]any {
	var sb strings.Builder
	for _, m := range msgs {
		if m.Role != ai.RoleSystem {
			continue
		}
		content := dialectkit.ContentText(m.Content)
		if content == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(content)
	}
	if sb.Len() == 0 {
		return nil
	}
	return map[string]any{"parts": []any{map[string]any{"text": sb.String()}}}
}

// buildMessageNode contents 元素：role user/model（tool 归一为 user）；parts 承载
// 文本/媒体/functionCall/functionResponse。
func buildMessageNode(m *ai.Message) map[string]any {
	roleStr := "user"
	switch m.Role {
	case ai.RoleAssistant:
		roleStr = "model"
	case ai.RoleTool:
		roleStr = "user"
	default:
		roleStr = strings.ToLower(string(m.Role))
	}
	node := map[string]any{"role": roleStr}

	switch content := m.Content.(type) {
	case *ai.ToolMessage:
		buildToolMessageNode(node, content)
	case *ai.AssistantMessage:
		buildAssistantMessageNode(node, content)
	default:
		buildNormalMessageNode(node, m)
	}
	return node
}

// buildToolMessageNode functionResponse part：response 必须是 JSON object
// （数组/标量/纯文本一律包装 {"result": ...}）；Gemini 3 规范要求回传真实调用 id。
func buildToolMessageNode(node map[string]any, t *ai.ToolMessage) {
	response := map[string]any{}
	content := t.ResultText()
	if content != "" {
		var parsed any
		if json.Unmarshal([]byte(content), &parsed) == nil {
			if m, ok := parsed.(map[string]any); ok {
				response = m
			} else {
				response = map[string]any{"result": parsed}
			}
		} else {
			response = map[string]any{"result": content}
		}
	}
	fn := map[string]any{"name": t.Name, "response": response}
	// Gemini 3+ 官方规范：functionResponse 需回传与 functionCall 相同的 id。
	// 仅当 id 为服务端真实 id（即与 name 不同）时写出，兼容 Gemini 2.5/代理端。
	if t.CallID != "" && t.CallID != t.Name {
		fn["id"] = t.CallID
	}
	node["parts"] = []any{map[string]any{"functionResponse": fn}}
}

// buildAssistantMessageNode assistant 回放：functionCall parts（args 必须 object、
// 仅真实 id 回传）+ thoughtSignature（part 级、仅第一个 part）；无工具时按
// 文本/媒体 parts。
func buildAssistantMessageNode(node map[string]any, am *ai.AssistantMessage) {
	if len(am.ToolCalls) > 0 {
		parts := []any{}
		parts = append(parts, assistantContentParts(am)...)

		for position, call := range am.ToolCalls {
			part := map[string]any{"functionCall": buildFunctionCallNode(&call)}
			// thoughtSignature 位于 part 级别（functionCall 的同级），仅第一个 part 需要
			if position == 0 {
				if signature := resolveSignature(am, generateContentProtocolID, &call, 0); signature != "" {
					part["thoughtSignature"] = signature
				}
			}
			parts = append(parts, part)
		}
		node["parts"] = parts
		return
	}
	// 与多模态路径对齐
	parts := assistantContentParts(am)
	if len(parts) == 0 {
		node["parts"] = []any{}
		return
	}
	node["parts"] = parts
}

// buildFunctionCallNode {name, args(object), id?}：出站兜底净化——截断/双重编码的
// arguments 禁止原样回传。
func buildFunctionCallNode(call *ai.ToolCall) map[string]any {
	fn := map[string]any{"name": call.Name}
	// Gemini 3+：仅回传服务端生成的真实 id（fallback 的 name 不写）
	if call.ID != "" && call.ID != call.Name {
		fn["id"] = call.ID
	}
	args := map[string]any{}
	if m, ok := dialectkit.ExtractJSONObject(dialectkit.SanitizeCall(call)); ok {
		args = m
	}
	fn["args"] = args
	return fn
}

// assistantContentParts assistant 文本/媒体 → parts；blocks 无 TextBlock 时补文本
// 投影兜底。
func assistantContentParts(am *ai.AssistantMessage) []any {
	var parts []any
	if len(am.Blocks) > 0 {
		hasText := false
		for _, block := range am.Blocks {
			if tb, ok := block.(ai.TextBlock); ok {
				if tb.Text != "" {
					parts = append(parts, map[string]any{"text": tb.Text})
					hasText = true
				}
				continue
			}
			if part := mediaPart(block); part != nil {
				parts = append(parts, part)
			}
		}
		if !hasText && am.Text != "" {
			parts = append(parts, map[string]any{"text": am.Text})
		}
	} else if am.Text != "" {
		parts = append(parts, map[string]any{"text": am.Text})
	}
	return parts
}

// buildNormalMessageNode user 多模态 → blocks 映射 parts；纯文本单 part。
func buildNormalMessageNode(node map[string]any, m *ai.Message) {
	blocks := dialectkit.MessageBlocks(m.Content)
	if len(blocks) > 0 {
		parts := []any{}
		for _, block := range blocks {
			if tb, ok := block.(ai.TextBlock); ok {
				if tb.Text != "" {
					parts = append(parts, map[string]any{"text": tb.Text})
				}
				continue
			}
			if part := mediaPart(block); part != nil {
				parts = append(parts, part)
			}
		}
		node["parts"] = parts
		return
	}
	content := dialectkit.ContentText(m.Content)
	if content != "" {
		node["parts"] = []any{map[string]any{"text": content}}
		return
	}
	node["parts"] = []any{}
}

// mediaPart 媒体块 → inlineData{mimeType,data} | fileData{mimeType,fileUri} part。
// Session 截断后 data/url 皆空时跳过，避免写出空 part。
func mediaPart(b ai.ContentBlock) map[string]any {
	var url string
	var data []byte
	var mime string
	switch v := b.(type) {
	case ai.ImageBlock:
		url, data, mime = v.URL, v.Data, v.Mime
	case ai.AudioBlock:
		url, data, mime = v.URL, v.Data, v.Mime
	case ai.VideoBlock:
		url, data, mime = v.URL, v.Data, v.Mime
	default:
		return nil
	}
	if len(data) == 0 && url == "" {
		return nil
	}
	if mime == "" {
		switch b.(type) {
		case ai.ImageBlock:
			mime = "image/jpeg"
		case ai.AudioBlock:
			mime = "audio/mpeg"
		case ai.VideoBlock:
			mime = "video/mp4"
		}
	}
	if len(data) > 0 {
		return map[string]any{"inlineData": map[string]any{
			"mimeType": mime,
			"data":     base64.StdEncoding.EncodeToString(data),
		}}
	}
	return map[string]any{"fileData": map[string]any{
		"mimeType": mime,
		"fileUri":  url,
	}}
}

// buildToolsNode tools → [{functionDeclarations:[...]}]；tool_choice → toolConfig
// （none→NONE、required→ANY、指定函数→ANY+allowedFunctionNames；auto 为默认省略）。
func buildToolsNode(root map[string]any, opts *ai.ChatOptions) {
	if len(opts.Tools) == 0 {
		return
	}
	toolsNode := make([]any, 0, len(opts.Tools))
	for _, fn := range opts.Tools {
		decl := map[string]any{
			"name":        fn.Name(),
			"description": ai.DescriptionAndMeta(fn),
		}
		parameters := map[string]any{"type": "object", "properties": map[string]any{}}
		if schema := fn.InputSchema(); schema != "" {
			var parsed map[string]any
			if json.Unmarshal([]byte(schema), &parsed) == nil && parsed != nil {
				parameters = parsed
			}
		}
		decl["parameters"] = parameters
		toolsNode = append(toolsNode, map[string]any{"functionDeclarations": []any{decl}})
	}
	root["tools"] = toolsNode

	toolChoice := opts.OptionGet("tool_choice")
	if toolChoice == nil {
		return
	}
	switch choice := toolChoice.(type) {
	case string:
		switch choice {
		case "none":
			root["toolConfig"] = map[string]any{"functionCallingConfig": map[string]any{"mode": "NONE"}}
		case "required":
			root["toolConfig"] = map[string]any{"functionCallingConfig": map[string]any{"mode": "ANY"}}
		}
	case map[string]any:
		if fn, ok := choice["function"].(map[string]any); ok {
			if name := fn["name"]; name != nil {
				root["toolConfig"] = map[string]any{"functionCallingConfig": map[string]any{
					"mode":                 "ANY",
					"allowedFunctionNames": []any{fmt.Sprint(name)},
				}}
			}
		}
	}
	// "auto" 是 Gemini 默认模式，无需显式写出（保持请求简洁）
}

// isSkippableThinkingOnly 纯思考历史没有可安全回放的 Gemini 签名/正文。
func isSkippableThinkingOnly(m *ai.Message) bool {
	am := dialectkit.AssistantOf(m)
	return am != nil && am.ThinkingOnly()
}

// BuildAssistantToolCallMessage 流式聚合的工具调用 → assistant 消息。附带
// thoughtSignature 协议状态（首调用），供下一轮请求回放。
func (d *Dialect) BuildAssistantToolCallMessage(acc *ai.Accumulator, builders map[string]*ai.ToolCallBuilder) *ai.Message {
	var calls []ai.ToolCall
	for _, key := range acc.ToolCallOrder() {
		b := builders[key]
		if b == nil {
			continue
		}
		calls = append(calls, ai.ToolCall{
			Index:        b.Index,
			ID:           b.ID,
			Name:         b.Name,
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
	// Gemini Models 的 args 是累计快照；仅在此方言聚合边界允许从多根拼接中取最后快照
	if signature := acc.ThinkingSignature(); signature != "" && len(calls) > 0 {
		msg.ProtocolStates = map[string]any{
			generateContentProtocolID: createSignatureState(&calls[0], 0, signature),
		}
		// 摘要在物化边界绑定（resolveSignature 回放时按语义校验，未绑定的状态必然被拒）
		ai.BindProtocolStateHashes(msg)
	}
	return &ai.Message{Role: ai.RoleAssistant, Content: msg}
}

// sanitizeSnapshotArguments 多个完整 JSON 快照串接时取最后一个完整 object；
// 截断或无完整快照按公共安全策略降级 "{}"（不泄漏到其他方言的净化语义）。
func sanitizeSnapshotArguments(raw string) string {
	if m, ok := lastJSONObject(raw); ok {
		if data, err := json.Marshal(m); err == nil {
			return string(data)
		}
	}
	if m, ok := dialectkit.ExtractJSONObject(raw); ok {
		if data, err := json.Marshal(m); err == nil {
			return string(data)
		}
	}
	return "{}"
}

// lastJSONObject 串接的多根 JSON 中最后一个完整 object（对标 snack4 readLast）。
func lastJSONObject(raw string) (map[string]any, bool) {
	dec := json.NewDecoder(strings.NewReader(raw))
	var last map[string]any
	for {
		var v any
		if err := dec.Decode(&v); err != nil {
			break
		}
		if m, ok := v.(map[string]any); ok {
			last = m
		}
	}
	return last, last != nil
}

// BuildAssistantMessageByToolMessages returnDirect：工具结果合成最终 assistant 消息
// （基类默认实现，与 openai 方言一致）。
func (d *Dialect) BuildAssistantMessageByToolMessages(msg *ai.AssistantMessage, toolMsgs []*ai.Message) *ai.AssistantMessage {
	var buf strings.Builder
	var mergedBlocks []ai.ContentBlock
	for _, tm := range toolMsgs {
		t, _ := tm.Content.(*ai.ToolMessage)
		if t == nil {
			continue
		}
		if buf.Len() > 0 {
			buf.WriteByte('\n')
		}
		buf.WriteString(t.ResultText())
		if t.Result != nil {
			for _, b := range t.Result.Blocks {
				if _, isText := b.(ai.TextBlock); !isText {
					mergedBlocks = append(mergedBlocks, b)
				}
			}
		}
	}
	out := &ai.AssistantMessage{Text: buf.String(), Blocks: mergedBlocks}
	out.AddMetadata("reason", "tool")
	out.AddMetadata("source", msg.Text)
	return out
}

// ParseAssistantMessage message/content 节点 → 聚合消息：parts 形态走思考处理器；
// 其余走基类默认。
func (d *Dialect) ParseAssistantMessage(acc *ai.Accumulator, node map[string]any) []*ai.AssistantMessage {
	if node != nil {
		if _, hasParts := node["parts"]; hasParts {
			return parseThoughtContent(acc, node)
		}
		if _, hasPart := node["part"]; hasPart {
			return parseThoughtContent(acc, node)
		}
	}
	return dialectkit.ParseAssistantMessage(acc, node)
}

// ---- 统一 thinking 选项 ----

// applyUnifiedThinkingOptions 统一 thinking 开关 + reasoning_effort →
// generationConfig.thinkingConfig（显式 thinkingConfig 优先）。按 model 分流：
// Gemini 2.5 → thinkingBudget（关=0）；Gemini 3.x → thinkingLevel（关=minimal）。
func applyUnifiedThinkingOptions(root map[string]any, cfg *ai.Config, opts *ai.ChatOptions, thinkingSwitch, reasoningEffort any) {
	if hasExplicitThinkingConfig(root, opts) {
		return
	}
	useBudget := usesThinkingBudget(cfg)

	if b, ok := thinkingSwitch.(bool); ok && !b {
		tc := map[string]any{"includeThoughts": false}
		if useBudget {
			tc["thinkingBudget"] = 0
		} else {
			// Gemini 3.x：minimal 表示关闭/极低思考
			tc["thinkingLevel"] = "minimal"
		}
		generationConfig, _ := root["generationConfig"].(map[string]any)
		if generationConfig == nil {
			generationConfig = map[string]any{}
			root["generationConfig"] = generationConfig
		}
		generationConfig["thinkingConfig"] = tc
		return
	}

	if reasoningEffort != nil {
		applyReasoningEffort(root, cfg, reasoningEffort)
		if gc, ok := root["generationConfig"].(map[string]any); ok {
			if _, has := gc["thinkingConfig"]; has {
				return
			}
		}
	}

	if b, ok := thinkingSwitch.(bool); ok && b {
		// 开启且无 effort：2.5 用 medium 预算；3.x 默认 high
		effort := "high"
		if useBudget {
			effort = "medium"
		}
		applyReasoningEffort(root, cfg, effort)
	}
}

func applyReasoningEffort(root map[string]any, cfg *ai.Config, value any) {
	if value == nil || hasExplicitThinkingConfig(root, nil) {
		return
	}
	effort := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
	useBudget := usesThinkingBudget(cfg)

	tc := map[string]any{"includeThoughts": true}
	if useBudget {
		budget, ok := mapEffortToThinkingBudget(effort, cfg)
		if !ok {
			return
		}
		tc["thinkingBudget"] = budget
	} else {
		level, ok := mapEffortToThinkingLevel(effort, cfg)
		if !ok {
			return
		}
		// API 侧常用小写 level，避免 enum 名全大写序列化
		tc["thinkingLevel"] = level
	}
	generationConfig, _ := root["generationConfig"].(map[string]any)
	if generationConfig == nil {
		generationConfig = map[string]any{}
		root["generationConfig"] = generationConfig
	}
	generationConfig["thinkingConfig"] = tc
}

// usesThinkingBudget Gemini 2.5 使用 thinkingBudget；其余（含 Gemini 3/3.1）使用
// thinkingLevel。
func usesThinkingBudget(cfg *ai.Config) bool {
	model := ""
	if cfg != nil {
		model = strings.ToLower(cfg.Model)
	}
	return strings.Contains(model, "2.5") || strings.Contains(model, "2-5")
}

func hasExplicitThinkingConfig(root map[string]any, opts *ai.ChatOptions) bool {
	if opts != nil {
		if genCfg, ok := opts.OptionGet("generationConfig").(map[string]any); ok {
			if _, has := genCfg["thinkingConfig"]; has {
				return true
			}
		}
	}
	if root != nil {
		if genNode, ok := root["generationConfig"].(map[string]any); ok {
			if _, has := genNode["thinkingConfig"]; has {
				return true
			}
		}
	}
	return false
}

// mapEffortToThinkingBudget reasoning_effort → thinkingBudget（Gemini 2.5）。
// high=16k；max 对 2.5 pro=32768，其余 2.5（含 flash）=24576。
func mapEffortToThinkingBudget(effort string, cfg *ai.Config) (int64, bool) {
	model := ""
	if cfg != nil {
		model = strings.ToLower(cfg.Model)
	}
	is25Pro := (strings.Contains(model, "2.5") || strings.Contains(model, "2-5")) &&
		strings.Contains(model, "pro") && !strings.Contains(model, "flash")
	switch effort {
	case "low":
		return 1024, true
	case "medium":
		return 4096, true
	case "high":
		return 16000, true
	case "max":
		if is25Pro {
			return 32768, true
		}
		return 24576, true
	}
	return 0, false
}

// mapEffortToThinkingLevel reasoning_effort → thinkingLevel（Gemini 3.x）。
// flash → minimal/low/medium/high；pro → low/medium/high；统一 API 的 max → high；
// 非 flash 的 minimal → low。
func mapEffortToThinkingLevel(effort string, cfg *ai.Config) (string, bool) {
	model := ""
	if cfg != nil {
		model = strings.ToLower(cfg.Model)
	}
	isFlash := strings.Contains(model, "flash")
	// Gemini 3 族（含 3.1/3-1，且 gemini+3 交叠覆盖）普遍支持 medium；非 3 的 google 模型仅 low/high
	isGemini3 := strings.Contains(model, "gemini-3") || strings.Contains(model, "gemini3") ||
		strings.Contains(model, "3.1") || strings.Contains(model, "3-1") ||
		(strings.Contains(model, "gemini") && strings.Contains(model, "3"))

	switch effort {
	case "minimal", "min":
		if isFlash {
			return "minimal", true
		}
		return "low", true
	case "low":
		return "low", true
	case "medium":
		if isGemini3 {
			return "medium", true
		}
		return "high", true
	case "high", "max":
		return "high", true
	}
	return "", false
}
