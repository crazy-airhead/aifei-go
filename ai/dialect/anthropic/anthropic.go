// Package anthropic 是 Anthropic Claude Messages 方言：/v1/messages 端点、
// x-api-key + anthropic-version 鉴权、max_tokens 必填、连续 tool 消息合并为
// user tool_result 块、thinking 签名协议状态回放、input_json_delta 流式参数
// 聚合与 cache_control 断点（Prompt Caching 计量）。
//
// P1-b 范围裁剪（对标 Java 4.1 AnthropicChatDialect）：服务端工具原始块留存/回放
// （server_tool_use、*_tool_result、container_upload、container）、text citations、
// CONTENT_BLOCKS_RAW 有序块回放、output_config 结构化输出未迁——核心事件仍透出
// SERVER_TOOL_* / RAW，仅无跨轮原样回传载体。
package anthropic

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/internal/dialectkit"
)

// Dialect Anthropic Claude Messages 方言。
type Dialect struct{}

func init() { ai.RegisterDialect(&Dialect{}, 40) }

func (d *Dialect) Name() string    { return "anthropic" }
func (d *Dialect) IsDefault() bool { return false }

// Matched standard/provider 命中（claude/anthropic/anthropic-messages）；否则空
// standard 且 apiUrl 路径以 /messages 结尾。
func (d *Dialect) Matched(cfg *ai.Config) bool {
	if cfg == nil {
		return false
	}
	switch strings.ToLower(cfg.StandardOrProvider()) {
	case "claude", "anthropic", "anthropic-messages":
		return true
	}
	if cfg.StandardOrProvider() != "" {
		return false
	}
	return strings.HasSuffix(apiPath(cfg.APIURL), "/messages")
}

// CustomizeHeader x-api-key（非 Bearer，清除框架注入的 Authorization）+
// anthropic-version + 流式 Accept；anthropic-beta 能力协商（config.headers 已有值
// 与 anthropic_beta/betas 选项按声明序合并去重）。
func (d *Dialect) CustomizeHeader(cfg *ai.Config, opts *ai.ChatOptions, isStream bool, header map[string]string) {
	if cfg != nil && cfg.APIKey != "" {
		header["x-api-key"] = cfg.APIKey
		delete(header, "Authorization")
	}
	header["anthropic-version"] = "2023-06-01"
	if isStream {
		header["Accept"] = "text/event-stream"
	}
	if beta := resolveBetaHeader(cfg, opts); beta != "" {
		header["anthropic-beta"] = beta
	}
}

// resolveBetaHeader 汇总 beta 声明（逗号分隔、按声明序去重）。协议上 beta 只走
// 请求头（GA 请求体无 betas 字段）。
func resolveBetaHeader(cfg *ai.Config, opts *ai.ChatOptions) string {
	betas := []string{}
	seen := map[string]struct{}{}
	collect := func(value string) {
		for _, part := range strings.Split(value, ",") {
			if beta := strings.TrimSpace(part); beta != "" {
				if _, ok := seen[beta]; !ok {
					seen[beta] = struct{}{}
					betas = append(betas, beta)
				}
			}
		}
	}
	if cfg != nil {
		for k, v := range cfg.Headers {
			if strings.EqualFold(k, "anthropic-beta") {
				collect(v)
			}
		}
	}
	if opts != nil {
		for _, key := range []string{"anthropic_beta", "betas"} {
			if v := opts.OptionGet(key); v != nil {
				collect(fmt.Sprint(v))
			}
		}
	}
	if len(betas) == 0 {
		return ""
	}
	return strings.Join(betas, ",")
}

// ---- URL ----

// versionPathPattern 匹配 /v1、/v4/ 等版本段结尾（自动补全 messages 用）。
var versionPathPattern = regexp.MustCompile(`/v\d+/?$`)

// buildAPIURL 自动补全端点：已带 /messages 原样；版本段结尾补 messages；
// 其余补 v1/messages。query 只在路径补全后接回。
func buildAPIURL(apiURL string) string {
	if i := strings.Index(apiURL, "#"); i >= 0 {
		apiURL = apiURL[:i]
	}
	query := ""
	if i := strings.Index(apiURL, "?"); i >= 0 {
		query = apiURL[i:]
		apiURL = apiURL[:i]
	}
	if strings.HasSuffix(apiURL, "/messages") {
		return apiURL + query
	}
	if versionPathPattern.MatchString(apiURL) {
		if strings.HasSuffix(apiURL, "/") {
			return apiURL + "messages" + query
		}
		return apiURL + "/messages" + query
	}
	if strings.HasSuffix(apiURL, "/") {
		return apiURL + "v1/messages" + query
	}
	return apiURL + "/v1/messages" + query
}

func apiPath(apiURL string) string {
	if i := strings.Index(apiURL, "#"); i >= 0 {
		apiURL = apiURL[:i]
	}
	if i := strings.Index(apiURL, "?"); i >= 0 {
		apiURL = apiURL[:i]
	}
	return apiURL
}

// ---- 协议状态（thinking 签名 / redacted_thinking 回放）----

const (
	protocolID       = "anthropic.messages"
	protocolVersion  = 1
	stateSignature   = "thinkingSignature"
	stateRedactedBlk = "redactedThinkingBlocks"
)

// createState 终态协议状态（data 为空 → nil）。
func createState(data map[string]any) *ai.ProtocolState {
	if len(data) == 0 {
		return nil
	}
	return &ai.ProtocolState{Version: protocolVersion, Data: data}
}

// resolveState 按语义摘要校验后取回可回放数据：版本不符或消息语义已变即降级。
func resolveState(m *ai.AssistantMessage) *ai.ProtocolState {
	if m == nil || m.ProtocolStates == nil {
		return nil
	}
	v, ok := m.ProtocolStates[protocolID]
	if !ok {
		return nil
	}
	state := protocolStateOf(v)
	if state == nil || state.Version != protocolVersion || state.SemanticHash == "" ||
		state.SemanticHash != ai.SemanticHash(m) {
		return nil
	}
	return state
}

// protocolStateOf 状态值宽容归一（*ai.ProtocolState | 反序列化 map 形态）。
func protocolStateOf(v any) *ai.ProtocolState {
	switch s := v.(type) {
	case *ai.ProtocolState:
		return s
	case ai.ProtocolState:
		return &s
	case map[string]any:
		data, err := json.Marshal(s)
		if err != nil {
			return nil
		}
		st := &ai.ProtocolState{}
		if json.Unmarshal(data, st) != nil {
			return nil
		}
		return st
	}
	return nil
}

// resolveThinkingSignature 协议状态里的思考签名。
func resolveThinkingSignature(m *ai.AssistantMessage) string {
	state := resolveState(m)
	if state == nil {
		return ""
	}
	if sig, ok := state.Data[stateSignature].(string); ok {
		return sig
	}
	return ""
}

// resolveRedactedBlocks 协议状态里的 redacted_thinking 分块列表（不可拼接）。
func resolveRedactedBlocks(m *ai.AssistantMessage) []string {
	state := resolveState(m)
	if state == nil {
		return nil
	}
	items, _ := state.Data[stateRedactedBlk].([]any)
	var out []string
	for _, item := range items {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// hasReplayableState 是否有可回放的 Anthropic 协议载荷（纯思考消息过滤参考）。
func hasReplayableState(m *ai.AssistantMessage) bool {
	return resolveState(m) != nil && len(resolveState(m).Data) > 0
}

// ---- 请求侧 ----

const (
	// defaultThinkingBudget thinking=true / Map 简化入口未给预算时的默认思考预算
	//（必须始终小于 max_tokens，见 clampThinkingBudgetToMaxTokens）。
	defaultThinkingBudget = 10000
	// cacheBreakpointLimit 每请求 cache_control 断点上限：静态前缀（system 或
	// tools）占 1 个，其余让给对话历史滚动断点。
	cacheBreakpointLimit = 4
)

// cacheControlUnsupportedBlocks 协议上没有 cache_control 字段的块类型
// （挂上去会被服务端按 schema 拒掉）。
var cacheControlUnsupportedBlocks = map[string]struct{}{
	"thinking": {}, "redacted_thinking": {},
}

// cacheTTLAllowed 协议 CacheControlEphemeral.Ttl 合法取值。
var cacheTTLAllowed = map[string]struct{}{"5m": {}, "1h": {}}

// unsupportedOptionKeys OpenAI 风格但 Messages API 不接受的顶层选项键（出站前
// 剔除，否则整条 400）。黑名单而非白名单：白名单会把新增合法字段一并拦掉。
var unsupportedOptionKeys = map[string]struct{}{
	"frequency_penalty": {}, "presence_penalty": {}, "logit_bias": {}, "logprobs": {},
	"top_logprobs": {}, "n": {}, "seed": {}, "response_format": {}, "stream_options": {},
	"prompt_cache_key": {}, "max_completion_tokens": {},
}

// dialectOnlyOptionKeys 方言自身消费、不进请求体的合成选项键。
var dialectOnlyOptionKeys = map[string]struct{}{
	"structured_outputs": {}, "strict_tools": {}, "anthropic_tools": {},
	"anthropic_beta": {}, "betas": {},
}

var schemaInstructionT = "\n\n## [IMPORTANT: OUTPUT FORMAT]\n" +
	"Format your response as a JSON object strictly following this schema:\n" +
	"<output_schema>\n%s\n</output_schema>\n" +
	"Output only the raw JSON, beginning with '{' and ending with '}'."

// BuildRequestJSON {model, max_tokens, system?, messages, stream?, thinking?, tools?}：
// system 上提、连续 tool 消息合并 user tool_result、thinking/参数经统一选项映射、
// cache_control 断点预算分配（静态 1 + 滚动余量）。
func (d *Dialect) BuildRequestJSON(cfg *ai.Config, opts *ai.ChatOptions, msgs []*ai.Message, isStream bool) ([]byte, string, error) {
	if opts == nil {
		opts = &ai.ChatOptions{}
	}
	root := map[string]any{}

	if cfg != nil && cfg.Model != "" {
		root["model"] = cfg.Model
	}

	// max_tokens 必填；统一 API 的 max_completion_tokens（OpenAI 风格别名）并入
	maxTokens := opts.OptionGet("max_tokens")
	if maxTokens == nil {
		maxTokens = opts.OptionGet("max_completion_tokens")
	}
	if maxTokens == nil {
		maxTokens = 32000
	}
	root["max_tokens"] = maxTokens

	// 缓存控制：仅 Anthropic 风格（type 非空）才打块级断点；用户直接给了原生顶层
	// cache_control（服务端自动断点）时让出，两套机制叠加会超 4 断点上限而 400
	cacheControl := opts.CacheControl
	nativeAutoCache := opts.OptionGet("cache_control") != nil
	cacheEnabled := !nativeAutoCache && cacheControl.Type != ""

	// system 上提（缓存预算判断与节点构建复用）
	systemMessage := extractSystemMessage(msgs)
	hasSystem := systemMessage != ""
	hasTools := len(opts.Tools) > 0

	// 断点预算：渲染序 tools → system → messages，system 断点连带缓存 tools——
	// 静态前缀只占 1 个断点，省下的全给滚动历史（20-block 回溯窗口抗性）
	cacheOnTools := cacheEnabled && !hasSystem && hasTools
	staticBreakpoints := 0
	if cacheEnabled && (hasSystem || hasTools) {
		staticBreakpoints = 1
	}

	if hasSystem {
		if cacheEnabled {
			block := map[string]any{"type": "text", "text": systemMessage}
			writeCacheControl(block, &cacheControl)
			root["system"] = []any{block}
		} else {
			root["system"] = systemMessage
		}
	}

	// 消息数组（system 过滤；连续 ToolMessage 合并为一个 user tool_result 节点）
	messagesNode := []any{}
	var pendingToolResult map[string]any
	for _, m := range msgs {
		if m.Role == ai.RoleSystem || isSkippableThinkingOnly(m) {
			continue
		}
		if t, isTool := m.Content.(*ai.ToolMessage); isTool {
			if pendingToolResult == nil {
				pendingToolResult = map[string]any{"role": "user", "content": []any{}}
			}
			pendingToolResult["content"] = append(
				pendingToolResult["content"].([]any), buildToolResultBlock(m, t))
		} else {
			if pendingToolResult != nil {
				messagesNode = append(messagesNode, pendingToolResult)
				pendingToolResult = nil
			}
			messagesNode = append(messagesNode, buildMessageNode(m))
		}
	}
	if pendingToolResult != nil {
		messagesNode = append(messagesNode, pendingToolResult)
	}
	root["messages"] = messagesNode

	if cacheEnabled {
		rollingQuota := cacheBreakpointLimit - staticBreakpoints
		applyMessageCacheBreakpoints(messagesNode, &cacheControl, rollingQuota)
	}

	if isStream {
		root["stream"] = true
	}

	// 选项循环：thinking/reasoning_effort/parallel_tool_calls 延后统一处理
	var thinkingSwitch any
	for k, v := range opts.Raw {
		switch k {
		case "stream", "max_tokens":
			continue
		case "thinking":
			if _, isBool := v.(bool); isBool {
				thinkingSwitch = v
				continue
			}
			buildThinkingNode(root, v)
			continue
		case "reasoning_effort":
			continue
		case "tool_choice":
			buildToolChoiceNode(root, v)
			continue
		case "stop":
			writeStopSequences(root, v)
			continue
		case "user":
			if s, isStr := v.(string); isStr && s != "" {
				metadata, _ := root["metadata"].(map[string]any)
				if metadata == nil {
					metadata = map[string]any{}
					root["metadata"] = metadata
				}
				metadata["user_id"] = s
			}
			continue
		case "parallel_tool_calls":
			continue
		}
		if _, bad := unsupportedOptionKeys[k]; bad {
			continue
		}
		if _, own := dialectOnlyOptionKeys[k]; own {
			continue
		}
		root[k] = v
	}

	applyUnifiedThinkingOptions(root, cfg, thinkingSwitch, opts)
	clampThinkingBudgetToMaxTokens(root, opts)

	buildToolsNode(root, opts, cacheOnTools)
	applyParallelToolUse(root, opts)

	body, err := json.Marshal(root)
	if err != nil {
		return nil, "", err
	}
	url := ""
	if cfg != nil {
		url = buildAPIURL(cfg.APIURL)
	}
	return body, url, nil
}

func (d *Dialect) PrepareOutputSchemaInstruction(schema string, sb *strings.Builder) {
	sb.WriteString(fmt.Sprintf(schemaInstructionT, schema))
}

// PrepareOutputFormatOptions 置空：Anthropic 无 response_format 字段，基类默认
// 注入的 {type:json_object} 在这里是非法顶层字段（直接 400）。结构化输出走提示词。
func (d *Dialect) PrepareOutputFormatOptions(opts *ai.ChatOptions) {}

// isSkippableThinkingOnly 无正文/工具/媒体且无 Anthropic 回放状态的纯思考历史。
func isSkippableThinkingOnly(m *ai.Message) bool {
	am := dialectkit.AssistantOf(m)
	if am == nil {
		return false
	}
	if am.Text != "" {
		return false
	}
	if !am.ThinkingOnly() {
		return false
	}
	return !hasReplayableState(am)
}

// extractSystemMessage 多个 system 消息合并（\n 连接）。
func extractSystemMessage(msgs []*ai.Message) string {
	var sb strings.Builder
	for _, m := range msgs {
		if m.Role != ai.RoleSystem {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(dialectkit.ContentText(m.Content))
	}
	return sb.String()
}

// buildMessageNode contents 元素：role assistant/user（system/tool 归一 user）。
func buildMessageNode(m *ai.Message) map[string]any {
	roleStr := "user"
	switch m.Role {
	case ai.RoleAssistant:
		roleStr = "assistant"
	case ai.RoleSystem, ai.RoleTool:
		roleStr = "user"
	default:
		roleStr = strings.ToLower(string(m.Role))
	}
	node := map[string]any{"role": roleStr}

	switch content := m.Content.(type) {
	case *ai.ToolMessage:
		node["content"] = []any{buildToolResultBlock(m, content)}
	case *ai.AssistantMessage:
		buildAssistantMessageNode(node, content)
	default:
		buildNormalMessageNode(node, m)
	}
	return node
}

// buildToolResultBlock tool_result 块：is_error（经外层 Message.metadata 透传）+
// content 可为块数组（多模态结果）。tool_use_id 协议必填，空串照发交给服务端明确拒。
func buildToolResultBlock(m *ai.Message, t *ai.ToolMessage) map[string]any {
	block := map[string]any{"type": "tool_result", "tool_use_id": t.CallID}

	if m != nil && m.Metadata != nil {
		isError := m.Metadata["is_error"]
		if isError == nil {
			isError = m.Metadata["isError"]
		}
		if b, ok := isError.(bool); ok && b {
			block["is_error"] = true
		} else if s, ok := isError.(string); ok && strings.EqualFold(s, "true") {
			block["is_error"] = true
		}
	}

	var blocks []ai.ContentBlock
	if t.Result != nil {
		blocks = t.Result.Blocks
	}
	if len(blocks) > 0 {
		resultBlocks := []any{}
		for _, b := range blocks {
			switch v := b.(type) {
			case ai.TextBlock:
				if v.Text != "" {
					resultBlocks = append(resultBlocks, map[string]any{"type": "text", "text": v.Text})
				}
			case ai.ImageBlock:
				if image := claudeImageBlock(v); image != nil {
					resultBlocks = append(resultBlocks, image)
				}
			default:
				// 音频/视频等协议上无 tool_result 变体：退化为 text 而非静默丢弃
				if s := blockContentText(b); s != "" {
					resultBlocks = append(resultBlocks, map[string]any{"type": "text", "text": s})
				}
			}
		}
		if len(resultBlocks) > 0 {
			block["content"] = resultBlocks
			return block
		}
	}
	block["content"] = t.ResultText()
	return block
}

// blockContentText 块的文本投影兜底。
func blockContentText(b ai.ContentBlock) string {
	switch v := b.(type) {
	case ai.AudioBlock:
		return v.URL
	case ai.VideoBlock:
		return v.URL
	}
	return ""
}

// buildAssistantMessageNode assistant 回放：thinking 块仅签名有效时回传
// （无 signature 的 thinking 在兼容网关上触发 EMPTY_RESPONSE）、redacted_thinking
// 逐块回传、tool_use（input 净化为 object）。
func buildAssistantMessageNode(node map[string]any, am *ai.AssistantMessage) {
	if len(am.ToolCalls) > 0 {
		contentArray := []any{}
		if reasoning, signature := am.Thinking, resolveThinkingSignature(am); reasoning != "" && signature != "" {
			contentArray = append(contentArray, map[string]any{
				"type": "thinking", "thinking": reasoning, "signature": signature,
			})
		}
		contentArray = appendRedactedThinkingBlocks(contentArray, am)
		if am.Text != "" {
			contentArray = append(contentArray, map[string]any{"type": "text", "text": am.Text})
		}
		contentArray = appendAssistantMediaBlocks(contentArray, am)
		for i := range am.ToolCalls {
			call := &am.ToolCalls[i]
			input := map[string]any{}
			if m, ok := dialectkit.ExtractJSONObject(dialectkit.SanitizeCall(call)); ok {
				input = m
			}
			contentArray = append(contentArray, map[string]any{
				"type": "tool_use", "id": call.ID, "name": call.Name, "input": input,
			})
		}
		node["content"] = contentArray
		return
	}

	if len(am.Blocks) > 0 {
		contentArray := []any{}
		hasText := false
		for _, b := range am.Blocks {
			switch v := b.(type) {
			case ai.TextBlock:
				if v.Text != "" {
					contentArray = append(contentArray, map[string]any{"type": "text", "text": v.Text})
					hasText = true
				}
			case ai.ImageBlock:
				if image := claudeImageBlock(v); image != nil {
					contentArray = append(contentArray, image)
				}
			default:
				if s := blockContentText(b); s != "" {
					contentArray = append(contentArray, map[string]any{"type": "text", "text": s})
				}
			}
		}
		if !hasText && am.Text != "" {
			contentArray = append(contentArray, map[string]any{"type": "text", "text": am.Text})
		}
		node["content"] = contentArray
		return
	}

	// 纯文本按原样回传（assistant prefill 的精确续写前缀不被改变）
	if am.Text != "" {
		node["content"] = am.Text
		return
	}
	// 仅有思考：签名有效才回传 thinking；否则空 content 数组（Claude 必须有 content）
	contentArray := []any{}
	if reasoning, signature := am.Thinking, resolveThinkingSignature(am); reasoning != "" && signature != "" {
		contentArray = append(contentArray, map[string]any{
			"type": "thinking", "thinking": reasoning, "signature": signature,
		})
	}
	contentArray = appendRedactedThinkingBlocks(contentArray, am)
	node["content"] = contentArray
}

// appendAssistantMediaBlocks assistant 媒体块（协议支持 image）。
func appendAssistantMediaBlocks(contentArray []any, am *ai.AssistantMessage) []any {
	for _, b := range am.Blocks {
		if v, ok := b.(ai.ImageBlock); ok {
			if image := claudeImageBlock(v); image != nil {
				contentArray = append(contentArray, image)
			}
		}
	}
	return contentArray
}

// appendRedactedThinkingBlocks redacted_thinking 分块原样回传（拼接会损坏 opaque 数据）。
func appendRedactedThinkingBlocks(contentArray []any, am *ai.AssistantMessage) []any {
	for _, data := range resolveRedactedBlocks(am) {
		contentArray = append(contentArray, map[string]any{"type": "redacted_thinking", "data": data})
	}
	return contentArray
}

// buildNormalMessageNode user 多模态 → text/image 块；纯文本保持 string。
func buildNormalMessageNode(node map[string]any, m *ai.Message) {
	blocks := dialectkit.MessageBlocks(m.Content)
	if len(blocks) > 0 {
		contentArray := []any{}
		for _, b := range blocks {
			switch v := b.(type) {
			case ai.TextBlock:
				if v.Text != "" {
					contentArray = append(contentArray, map[string]any{"type": "text", "text": v.Text})
				}
			case ai.ImageBlock:
				if image := claudeImageBlock(v); image != nil {
					contentArray = append(contentArray, image)
				}
			default:
				if s := blockContentText(b); s != "" {
					contentArray = append(contentArray, map[string]any{"type": "text", "text": s})
				}
			}
		}
		if len(contentArray) > 0 {
			node["content"] = contentArray
			return
		}
	}
	content := dialectkit.ContentText(m.Content)
	if content != "" {
		node["content"] = content
		return
	}
	node["content"] = []any{}
}

// claudeImageBlock image 块：base64 / Files API file_id / url 三种 source。
// file_id 约定用 url 位置表达：anthropic-file: 前缀或裸 file_xxx（无协议头与路径
// 分隔，避免把真实 https://host/file_1.png 误判为 file_id）。
func claudeImageBlock(image ai.ImageBlock) map[string]any {
	mediaType := image.Mime
	if mediaType == "" {
		mediaType = "image/jpeg"
	}
	if len(image.Data) > 0 {
		return map[string]any{"type": "image", "source": map[string]any{
			"type": "base64", "media_type": mediaType,
			"data": base64.StdEncoding.EncodeToString(image.Data),
		}}
	}
	if image.URL != "" {
		if fileId := resolveAnthropicFileID(image.URL); fileId != "" {
			return map[string]any{"type": "image", "source": map[string]any{
				"type": "file", "file_id": fileId,
			}}
		}
		return map[string]any{"type": "image", "source": map[string]any{
			"type": "url", "url": image.URL,
		}}
	}
	return nil
}

func resolveAnthropicFileID(url string) string {
	value := strings.TrimSpace(url)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "anthropic-file:") {
		return strings.TrimSpace(strings.TrimPrefix(value, "anthropic-file:"))
	}
	if strings.HasPrefix(value, "file_") && !strings.ContainsAny(value, "/:") {
		return value
	}
	return ""
}

// ---- 选项映射 ----

// buildToolChoiceNode OpenAI tool_choice → Anthropic：auto→auto、required→any、
// none→none、{function:{name}}→tool+name；原生形态透传。
func buildToolChoiceNode(root map[string]any, toolChoice any) {
	tcNode, _ := root["tool_choice"].(map[string]any)
	if tcNode == nil {
		tcNode = map[string]any{}
		root["tool_choice"] = tcNode
	}
	switch choice := toolChoice.(type) {
	case map[string]any:
		typeVal, _ := choice["type"].(string)
		if typeVal == "function" {
			if fn, ok := choice["function"].(map[string]any); ok {
				name, _ := fn["name"].(string)
				tcNode["type"] = "tool"
				if name != "" {
					tcNode["name"] = name
				}
			} else {
				tcNode["type"] = "tool"
			}
		} else if typeVal == "auto" || typeVal == "any" || typeVal == "none" || typeVal == "tool" {
			tcNode["type"] = typeVal
			if name, ok := choice["name"].(string); ok && typeVal == "tool" && name != "" {
				tcNode["name"] = name
			}
		} else {
			tcNode["type"] = "auto"
		}
		if disable, ok := choice["disable_parallel_tool_use"].(bool); ok && disable {
			if tcNode["type"] != "none" {
				tcNode["disable_parallel_tool_use"] = true
			}
		}
	case string:
		switch choice {
		case "required":
			tcNode["type"] = "any"
		case "none":
			tcNode["type"] = "none"
		default:
			tcNode["type"] = "auto"
		}
	}
}

// writeStopSequences 统一 stop → 协议 stop_sequences（string → 单元素数组）。
func writeStopSequences(root map[string]any, stop any) {
	switch v := stop.(type) {
	case string:
		if v != "" {
			root["stop_sequences"] = []any{v}
		}
	case []any:
		if len(v) > 0 {
			root["stop_sequences"] = v
		}
	}
}

// applyParallelToolUse parallel_tool_calls=false → tool_choice.disable_parallel_tool_use。
// true 不写（并行是默认）；无 tools 且无 tool_choice 不写；type=none 不写（协议无该字段）。
func applyParallelToolUse(root map[string]any, opts *ai.ChatOptions) {
	flag := opts.OptionGet("parallel_tool_calls")
	if flag == nil {
		return
	}
	disabled := false
	if b, ok := flag.(bool); ok {
		disabled = !b
	} else if strings.EqualFold(fmt.Sprint(flag), "false") {
		disabled = true
	}
	if !disabled {
		return
	}
	if _, hasChoice := root["tool_choice"]; !hasChoice && len(opts.Tools) == 0 {
		return
	}
	tcNode, _ := root["tool_choice"].(map[string]any)
	if tcNode == nil {
		tcNode = map[string]any{}
		root["tool_choice"] = tcNode
	}
	if _, hasType := tcNode["type"]; !hasType {
		tcNode["type"] = "auto" // 未显式指定时补 auto 以承载开关
	}
	if tcNode["type"] == "none" {
		return
	}
	tcNode["disable_parallel_tool_use"] = true
}

// buildToolsNode 工具定义：{name, description, input_schema}；最后一个工具可挂
// cache_control 断点（由 build 统一协调：仅无 system 时才落在 tools 上）。
func buildToolsNode(root map[string]any, opts *ai.ChatOptions, cacheOnTools bool) {
	cacheControl := opts.CacheControl
	cacheEnabled := cacheOnTools && cacheControl.Type != ""
	if len(opts.Tools) == 0 {
		return
	}

	toolsNode := make([]any, 0, len(opts.Tools))
	for i, fn := range opts.Tools {
		toolNode := map[string]any{
			"name":        fn.Name(),
			"description": ai.DescriptionAndMeta(fn),
		}
		schema := buildToolInputSchema(fn.InputSchema())
		if schema != nil {
			toolNode["input_schema"] = schema
		}
		if cacheEnabled && i == len(opts.Tools)-1 {
			writeCacheControl(toolNode, &cacheControl)
		}
		toolsNode = append(toolsNode, toolNode)
	}
	root["tools"] = toolsNode
}

// buildToolInputSchema 工具 schema 解析：非法/非 object 回退 {type:object,
// properties:{}}，避免发出必然被拒的定义。
func buildToolInputSchema(inputSchema string) map[string]any {
	schema := map[string]any{"type": "object", "properties": map[string]any{}}
	if inputSchema == "" {
		return schema
	}
	var parsed map[string]any
	if json.Unmarshal([]byte(inputSchema), &parsed) != nil || parsed == nil {
		return schema
	}
	if t, has := parsed["type"]; has {
		if s, ok := t.(string); ok && s != "object" {
			return schema
		}
	}
	if _, has := parsed["properties"]; !has {
		parsed["properties"] = map[string]any{}
	}
	return parsed
}

// writeCacheControl 挂 cache_control（type + 合法 ttl；非法 ttl 拦掉避免 400）。
func writeCacheControl(blockNode map[string]any, cc *ai.CacheControl) {
	ccNode := map[string]any{"type": cc.Type}
	if _, allowed := cacheTTLAllowed[cc.Ttl]; allowed {
		ccNode["ttl"] = cc.Ttl
	}
	blockNode["cache_control"] = ccNode
}

// applyMessageCacheBreakpoints 从最后一条消息往前放滚动断点直到配额用满；
// thinking/redacted_thinking 块协议上无 cache_control 字段，跳过继续往前找。
func applyMessageCacheBreakpoints(messagesNode []any, cc *ai.CacheControl, quota int) {
	if quota <= 0 {
		return
	}
	placed := 0
	for i := len(messagesNode) - 1; i >= 0 && placed < quota; i-- {
		node, ok := messagesNode[i].(map[string]any)
		if !ok {
			continue
		}
		if markMessageCacheBreakpoint(node, cc) {
			placed++
		}
	}
}

// markMessageCacheBreakpoint 单条消息挂断点：content 数组从后往前找第一个可承载块；
// 字符串 content 转 text 块数组（空串不转）。
func markMessageCacheBreakpoint(node map[string]any, cc *ai.CacheControl) bool {
	switch content := node["content"].(type) {
	case []any:
		for i := len(content) - 1; i >= 0; i-- {
			block, ok := content[i].(map[string]any)
			if !ok {
				continue
			}
			blockType, _ := block["type"].(string)
			if _, unsupported := cacheControlUnsupportedBlocks[blockType]; unsupported {
				continue
			}
			writeCacheControl(block, cc)
			return true
		}
		return false
	case string:
		if content == "" {
			return false
		}
		block := map[string]any{"type": "text", "text": content}
		writeCacheControl(block, cc)
		node["content"] = []any{block}
		return true
	}
	return false
}

// ---- 统一 thinking 选项 ----

// buildThinkingNode 供应商原生 thinking 配置：Boolean 简化开关 / Number 直给预算 /
// Map（enabled、type、budget_tokens|budgetTokens|thinkingBudget、display）。
// 关闭变体（enabled=false / type=disabled）只写 type，不携带 enabled 专属字段。
func buildThinkingNode(root map[string]any, value any) {
	if value == nil {
		return
	}
	thinkingNode, _ := root["thinking"].(map[string]any)
	if thinkingNode == nil {
		thinkingNode = map[string]any{}
		root["thinking"] = thinkingNode
	}

	switch v := value.(type) {
	case bool:
		if v {
			thinkingNode["type"] = "enabled"
			thinkingNode["budget_tokens"] = defaultThinkingBudget
		} else {
			thinkingNode["type"] = "disabled"
		}
	case float64:
		thinkingNode["type"] = "enabled"
		thinkingNode["budget_tokens"] = int64(v)
	case int:
		thinkingNode["type"] = "enabled"
		thinkingNode["budget_tokens"] = v
	case int64:
		thinkingNode["type"] = "enabled"
		thinkingNode["budget_tokens"] = v
	case map[string]any:
		enabled, hasEnabled := v["enabled"]
		configuredType, hasType := v["type"]
		if (hasEnabled && !enabled.(bool)) || (hasType && configuredType == "disabled") {
			thinkingNode["type"] = "disabled"
			return
		}
		if hasEnabled && enabled.(bool) {
			thinkingNode["type"] = "enabled"
		} else if hasType && configuredType != nil {
			thinkingNode["type"] = configuredType
		} else {
			thinkingNode["type"] = "enabled"
		}

		budget := v["budget_tokens"]
		if budget == nil {
			budget = v["budgetTokens"]
		}
		if budget == nil {
			budget = v["thinkingBudget"]
		}
		if n, ok := numberOf(budget); ok {
			thinkingNode["budget_tokens"] = n
		} else if thinkingNode["type"] == "enabled" {
			// Map 简化入口声明 enabled 但没给预算：用默认值，避免缺 budget_tokens 的非法请求
			thinkingNode["budget_tokens"] = defaultThinkingBudget
		}
		if display, ok := v["display"].(string); ok && display != "" {
			thinkingNode["display"] = display
		}
	}
}

// numberOf JSON 数值宽容转换。
func numberOf(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int:
		return int64(n), true
	case int64:
		return n, true
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return i, true
		}
	case string:
		if i, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64); err == nil {
			return i, true
		}
	}
	return 0, false
}

// applyUnifiedThinkingOptions 统一 thinking 开关 + reasoning_effort 映射。
// 优先级：原生 Map/Number thinking > thinking(false) 关闭 > reasoning_effort
// 开启并设档 > thinking(true) 默认开启。adaptive 模型（4.6+/opus-4.7+/sonnet-5+/
// fable-5）用 type=adaptive + output_config.effort；经典模型 enabled+budget_tokens。
func applyUnifiedThinkingOptions(root map[string]any, cfg *ai.Config, thinkingSwitch any, opts *ai.ChatOptions) {
	if _, has := root["thinking"]; has {
		return
	}
	if b, ok := thinkingSwitch.(bool); ok && !b {
		buildThinkingNode(root, false)
		return
	}
	if effort := opts.OptionGet("reasoning_effort"); effort != nil {
		buildThinkingFromEffort(root, cfg, effort, opts)
		if _, has := root["thinking"]; has {
			return
		}
	}
	if b, ok := thinkingSwitch.(bool); ok && b {
		if isAdaptiveModel(cfg) {
			buildAdaptiveThinking(root, "medium", cfg)
		} else {
			buildThinkingNode(root, true)
		}
	}
}

// buildThinkingFromEffort reasoning_effort → thinking：经典模型 budget_tokens
// （low=4k/medium=10k/high=16k/max=32k，钳制 < max_tokens）；adaptive 模型 effort 档。
func buildThinkingFromEffort(root map[string]any, cfg *ai.Config, value any, opts *ai.ChatOptions) {
	effort := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
	if effort == "" {
		return
	}
	if isAdaptiveModel(cfg) {
		adaptiveEffort := clampAdaptiveEffort(effort, cfg)
		if adaptiveEffort == "" {
			return
		}
		buildAdaptiveThinking(root, adaptiveEffort, cfg)
		return
	}

	var budget int64
	switch effort {
	case "low":
		budget = 4000
	case "medium":
		budget = 10000
	case "high":
		budget = 16000
	case "max":
		budget = 32000
	default:
		return
	}
	maxTokens := resolveMaxTokens(root, opts)
	if maxTokens <= 1 {
		return
	}
	// budget 必须严格小于 max_tokens：极小 max_tokens 下退化为"尽量占满"（-1）
	if budget >= maxTokens {
		budget = maxTokens - 1
	}
	buildThinkingNode(root, map[string]any{"type": "enabled", "budget_tokens": budget})
}

// buildAdaptiveThinking adaptive thinking：type=adaptive（无 budget_tokens）+
// output_config.effort；新模型默认 display=omitted，强制 summarized 才有思考摘要。
func buildAdaptiveThinking(root map[string]any, effort string, cfg *ai.Config) {
	thinkingNode, _ := root["thinking"].(map[string]any)
	if thinkingNode == nil {
		thinkingNode = map[string]any{}
		root["thinking"] = thinkingNode
	}
	thinkingNode["type"] = "adaptive"
	if isOmitsThinkingModel(cfg) {
		thinkingNode["display"] = "summarized"
	}
	outputConfig, _ := root["output_config"].(map[string]any)
	if outputConfig == nil {
		outputConfig = map[string]any{}
		root["output_config"] = outputConfig
	}
	outputConfig["effort"] = effort
}

// clampThinkingBudgetToMaxTokens 已写出的 budget_tokens 钳制：< max_tokens 且
// >= 1024（协议下限）；越界即整块放弃 thinking，避免构造必然被拒的请求。
func clampThinkingBudgetToMaxTokens(root map[string]any, opts *ai.ChatOptions) {
	thinkingNode, ok := root["thinking"].(map[string]any)
	if !ok {
		return
	}
	budget, ok := numberOf(thinkingNode["budget_tokens"])
	if !ok {
		return
	}
	maxTokens := resolveMaxTokens(root, opts)
	if maxTokens <= 1 {
		delete(root, "thinking")
		return
	}
	if budget >= maxTokens {
		budget = maxTokens - 1
	}
	if budget < 1024 {
		delete(root, "thinking")
		return
	}
	thinkingNode["budget_tokens"] = budget
}

func resolveMaxTokens(root map[string]any, opts *ai.ChatOptions) int64 {
	if opts != nil {
		if n, ok := numberOf(opts.OptionGet("max_tokens")); ok {
			return n
		}
	}
	if n, ok := numberOf(root["max_tokens"]); ok {
		return n
	}
	return 32000
}

// ---- adaptive 模型代次识别 ----

var (
	// 版本段边界用消耗式字符类表达（Go RE2 不支持 (?!...) 前瞻）：major/minor
	// 之后必须是分隔符或非数字结尾，防止把 "425" 前缀误读成 "4"。
	claudeVersionPattern = regexp.MustCompile(
		`(?:opus|sonnet|haiku)[.-](\d{1,2})(?:[.-](\d{1,2}))?(?:[^0-9.]|$)`)
	claudeVersionInvertedPattern = regexp.MustCompile(
		`claude[.-](\d{1,2})(?:[.-](\d{1,2}))?[.-](?:opus|sonnet|haiku)`)
	opusVersionPattern           = regexp.MustCompile(`opus-(\d+)[.-](\d+)(?:[.@-]|$)`)
	opusVersionInvertedPattern   = regexp.MustCompile(`claude-(\d+)[.-](\d+)-opus(?:[.@-]|$)`)
	sonnetVersionPattern         = regexp.MustCompile(`sonnet-(\d+)(?:[.@-]|$)`)
	sonnetVersionInvertedPattern = regexp.MustCompile(`claude-(\d+)-sonnet(?:[.@-]|$)`)
)

func modelOf(cfg *ai.Config) string {
	if cfg == nil {
		return ""
	}
	return strings.ToLower(cfg.Model)
}

// isAdaptiveModel adaptive 模型族：opus/sonnet 4.6、opus-4.7+、sonnet-5+、fable-5
// 及倒置命名（claude-4.7-opus / claude-5-sonnet）。
func isAdaptiveModel(cfg *ai.Config) bool {
	model := modelOf(cfg)
	if model == "" {
		return false
	}
	if strings.Contains(model, "fable-5") {
		return true
	}
	if isOpus47OrLater(model) || isSonnet5OrLater(model) {
		return true
	}
	for _, token := range []string{
		"opus-4-6", "opus-4.6", "4-6-opus", "4.6-opus",
		"sonnet-4-6", "sonnet-4.6", "4-6-sonnet", "4.6-sonnet",
	} {
		if strings.Contains(model, token) {
			return true
		}
	}
	return false
}

// isOmitsThinkingModel 新 adaptive 模型默认 display=omitted（需写 summarized）。
func isOmitsThinkingModel(cfg *ai.Config) bool {
	model := modelOf(cfg)
	return strings.Contains(model, "fable-5") || isOpus47OrLater(model) || isSonnet5OrLater(model)
}

func isOpus47OrLater(model string) bool {
	if m := opusVersionPattern.FindStringSubmatch(model); m != nil {
		return versionAtLeast(m[1], m[2], 4, 7)
	}
	if m := opusVersionInvertedPattern.FindStringSubmatch(model); m != nil {
		return versionAtLeast(m[1], m[2], 4, 7)
	}
	return false
}

func isSonnet5OrLater(model string) bool {
	if m := sonnetVersionPattern.FindStringSubmatch(model); m != nil {
		return versionAtLeast(m[1], "0", 5, 0)
	}
	if m := sonnetVersionInvertedPattern.FindStringSubmatch(model); m != nil {
		return versionAtLeast(m[1], "0", 5, 0)
	}
	return false
}

func versionAtLeast(majorStr, minorStr string, major, minor int64) bool {
	majorV, err := strconv.ParseInt(majorStr, 10, 64)
	if err != nil {
		return false
	}
	minorV, err := strconv.ParseInt(minorStr, 10, 64)
	if err != nil {
		minorV = 0
	}
	return majorV > major || (majorV == major && minorV >= minor)
}

// clampAdaptiveEffort adaptive 档位：low/medium/high/max 直通；xhigh 仅 4.7+/
// sonnet-5+/fable-5（其余降 max）；min/minimal → low；none/auto/非法 → 空（不发）。
func clampAdaptiveEffort(effort string, cfg *ai.Config) string {
	switch effort {
	case "low", "medium", "high", "max":
		return effort
	case "xhigh":
		model := modelOf(cfg)
		if isOmitsThinkingModel(cfg) || strings.Contains(model, "4-7") || strings.Contains(model, "4.7") {
			return "xhigh"
		}
		return "max"
	case "min", "minimal":
		return "low"
	}
	return ""
}

// ---- 工具调用流式聚合消息 ----

// BuildAssistantToolCallMessage 流式聚合的工具调用 → assistant 消息（thinking
// 签名与 redacted 块随终态协议状态一并携带，供下一轮回放）。
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
			ArgumentsStr: dialectkit.SanitizeArguments(b.Args(), b.Name),
		})
	}
	if len(calls) == 0 {
		return nil
	}
	msg := &ai.AssistantMessage{ToolCalls: calls}
	if m := acc.Message(); m != nil {
		msg.Text, msg.Thinking = m.Text, m.Thinking
		// 终态协议袋（anthropic.messages：签名 + redacted 块）随行；摘要按出站
		// 消息形态重绑（聚合快照的字段形态与此不同，沿用旧摘要会校验失败）
		dialectkit.AdoptProtocolStates(msg, m.ProtocolStates)
	}
	return &ai.Message{Role: ai.RoleAssistant, Content: msg}
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
