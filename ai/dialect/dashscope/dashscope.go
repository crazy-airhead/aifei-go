// Package dashscope 是阿里云百炼（DashScope）原生方言：{input, parameters}
// 包装、流式由 X-DashScope-SSE 请求头开启、incremental_output 增量流
// （快照流由 SnapshotDeltaNormalizer 兜底归一）、联网搜索 search_info 注入。
package dashscope

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/internal/dialectkit"
)

// URL_PREFIX 百炼原生服务地址前缀（apiUrl 未声明 standard/provider 时的匹配依据）。
const URL_PREFIX = "https://dashscope.aliyuncs.com/api/v1/services/"

// 流式输出由请求头控制（见官方"流式输出"文档）：cURL 需设置 X-DashScope-SSE 为 enable。
const headerDashScopeSSE = "X-DashScope-SSE"

var schemaInstructionT = "\n\n## [IMPORTANT: OUTPUT FORMAT]\n" +
	"Format your response as a JSON object strictly following this schema:\n" +
	"<output_schema>\n%s\n</output_schema>\n" +
	"Output only the raw JSON, beginning with '{' and ending with '}'."

// 跨帧状态挂载键（挂 StreamContext，方言单例不可放实例字段——并发请求会串话）。
const (
	snapshotStateKey = "__dashscopeSnapshotState"
	searchEmittedKey = "__dashscopeEmittedSearchResults"
)

// Dialect DashScope 原生聊天方言。
type Dialect struct{}

func init() { ai.RegisterDialect(&Dialect{}, 10) }

func (d *Dialect) Name() string    { return "dashscope" }
func (d *Dialect) IsDefault() bool { return false }

func (d *Dialect) Matched(cfg *ai.Config) bool {
	if cfg == nil {
		return false
	}
	if strings.EqualFold(cfg.StandardOrProvider(), "dashscope") {
		return true
	}
	return strings.HasPrefix(cfg.APIURL, URL_PREFIX)
}

// CustomizeHeader 流式时开启 DashScope SSE（HeaderDialect 能力）。
func (d *Dialect) CustomizeHeader(cfg *ai.Config, opts *ai.ChatOptions, isStream bool, header map[string]string) {
	if isStream {
		header[headerDashScopeSSE] = "enable"
	}
}

// ---- 请求侧 ----

// BuildRequestJSON {model, input:{messages}, stream, parameters}：parameters 承载
// 全部选项（thinking→enable_thinking；reasoning_effort 仅作隐式开启信号不透传；
// result_format 固定 message；流式补 incremental_output=true；tools 也在 parameters）。
// URL 原样使用配置值（百炼要求完整端点地址，不做补全）。
func (d *Dialect) BuildRequestJSON(cfg *ai.Config, opts *ai.ChatOptions, msgs []*ai.Message, isStream bool) ([]byte, string, error) {
	if opts == nil {
		opts = &ai.ChatOptions{}
	}

	root := map[string]any{}
	if cfg != nil && cfg.Model != "" {
		root["model"] = cfg.Model
	}

	messages := []any{}
	for _, m := range msgs {
		node := d.buildMessageNode(cfg, m)
		if node == nil || d.isEmptyAssistantReplay(m, node) {
			continue
		}
		messages = append(messages, node)
	}
	root["input"] = map[string]any{"messages": messages}
	root["stream"] = isStream

	parameters := map[string]any{}
	thinkingSwitch := any(nil)
	hasReasoningEffort := false
	for k, v := range opts.Raw {
		// 统一 thinking 开关 → DashScope 原生 parameters.enable_thinking
		if k == "thinking" {
			if b, ok := v.(bool); ok {
				thinkingSwitch = b
				parameters["enable_thinking"] = b
				continue
			}
		}
		// 统一 reasoning_effort 不透传为 parameters 字段；仅作"开启思考"信号
		if k == "reasoning_effort" {
			if v != nil {
				e := strings.ToLower(strings.TrimSpace(fmt.Sprint(v)))
				if e != "" && e != "auto" && e != "none" {
					hasReasoningEffort = true
				}
			}
			continue
		}
		parameters[k] = v
	}

	// 仅设 reasoning_effort 时隐式开启 enable_thinking；thinking(false) 优先；
	// 已写出 enable_thinking 不覆盖
	if hasReasoningEffort && thinkingSwitch != false {
		if _, has := parameters["enable_thinking"]; !has {
			parameters["enable_thinking"] = true
		}
	}

	parameters["result_format"] = "message"

	// 原生协议 incremental_output 缺省 false（每帧全量快照），而聚合器按流式
	// 协议只接受增量——流式时显式开启让服务端直接下发增量；用户显式指定时不覆盖。
	if isStream {
		if _, has := parameters["incremental_output"]; !has {
			parameters["incremental_output"] = true
		}
	}

	if len(opts.Tools) > 0 {
		parameters["tools"] = dialectkit.BuildToolsNode(opts.Tools)
	}
	root["parameters"] = parameters

	body, err := json.Marshal(root)
	if err != nil {
		return nil, "", err
	}
	url := ""
	if cfg != nil {
		url = cfg.APIURL
	}
	return body, url, nil
}

func (d *Dialect) PrepareOutputSchemaInstruction(schema string, sb *strings.Builder) {
	sb.WriteString(fmt.Sprintf(schemaInstructionT, schema))
}

func (d *Dialect) PrepareOutputFormatOptions(opts *ai.ChatOptions) {
	opts.OptionSet("response_format", map[string]any{"type": "json_object"})
}

func (d *Dialect) buildMessageNode(cfg *ai.Config, m *ai.Message) map[string]any {
	switch m.Role {
	case ai.RoleSystem:
		return map[string]any{"role": "system", "content": dialectkit.ContentText(m.Content)}
	case ai.RoleUser:
		return buildUserMessageNode(m)
	case ai.RoleAssistant:
		return d.buildAssistantMessageNode(m)
	case ai.RoleTool:
		return buildToolMessageNode(m)
	}
	return nil
}

// buildUserMessageNode 多模态才用原生 content 数组（[{image|audio|video|text}]，
// video 可带 fps/max_frames）；纯文本保持 string（兼容文本模型）。
func buildUserMessageNode(m *ai.Message) map[string]any {
	node := map[string]any{"role": "user"}
	text := dialectkit.ContentText(m.Content)
	if blocks := dialectkit.MessageBlocks(m.Content); len(blocks) > 0 {
		if content := buildContentArray(blocks, text); len(content) > 0 {
			node["content"] = content
			return node
		}
	}
	node["content"] = text
	return node
}

// buildContentArray DashScope 原生 content 块数组（session 截断后的空媒体跳过）。
// blocks 中无 TextBlock 时补文本投影兜底。
func buildContentArray(blocks []ai.ContentBlock, textFallback string) []any {
	var content []any
	hasTextBlock := false
	for _, b := range blocks {
		switch v := b.(type) {
		case ai.ImageBlock:
			if s := mediaValue(v.URL, v.Data, v.Meta, "image", v.Mime); s != "" {
				content = append(content, map[string]any{"image": s})
			}
		case ai.AudioBlock:
			if s := mediaValue(v.URL, v.Data, v.Meta, "audio", v.Mime); s != "" {
				content = append(content, map[string]any{"audio": s})
			}
		case ai.VideoBlock:
			s := mediaValue(v.URL, v.Data, v.Meta, "video", v.Mime)
			if s == "" {
				continue
			}
			item := map[string]any{"video": s}
			// 可选 VL 参数
			if v := v.Meta["fps"]; v != nil {
				item["fps"] = v
			}
			if v := v.Meta["max_frames"]; v != nil {
				item["max_frames"] = v
			}
			content = append(content, item)
		case ai.TextBlock:
			if v.Text != "" {
				content = append(content, map[string]any{"text": v.Text})
				hasTextBlock = true
			}
		}
	}
	if !hasTextBlock && textFallback != "" {
		content = append(content, map[string]any{"text": textFallback})
	}
	return content
}

func mediaValue(url string, data []byte, metas map[string]any, kind, mime string) string {
	if !dialectkit.IsMediaPlayable(url, data, metas) {
		return ""
	}
	return dialectkit.MediaDataString(url, data, kind, mime)
}

// buildAssistantMessageNode 多模态用 [{image|audio|video|text}]、单模态保持 string；
// 不回传 reasoning（百炼多轮建议）；tool_calls 为 OpenAI 兼容形态（arguments 字符串）。
func (d *Dialect) buildAssistantMessageNode(m *ai.Message) map[string]any {
	node := map[string]any{"role": "assistant"}

	am := dialectkit.AssistantOf(m)
	if am == nil {
		node["content"] = dialectkit.ContentText(m.Content)
		return node
	}

	if len(am.Blocks) > 0 {
		if content := buildContentArray(am.Blocks, am.Text); len(content) > 0 {
			node["content"] = content
		}
	} else if am.Text != "" {
		node["content"] = am.Text
	}

	if calls := dialectkit.BuildOutboundToolCalls(am.ToolCalls); len(calls) > 0 {
		node["tool_calls"] = calls
	}
	return node
}

func buildToolMessageNode(m *ai.Message) map[string]any {
	node := map[string]any{"role": "tool"}
	t, _ := m.Content.(*ai.ToolMessage)
	if t == nil {
		node["content"] = dialectkit.ContentText(m.Content)
		return node
	}
	if t.Name != "" {
		node["name"] = t.Name
	}
	if t.CallID != "" {
		node["tool_call_id"] = t.CallID
	}
	var blocks []ai.ContentBlock
	if t.Result != nil {
		blocks = t.Result.Blocks
	}
	if len(blocks) == 0 {
		node["content"] = t.ResultText()
		return node
	}
	// 多模态工具结果：文本项入 OpenAI content 数组，媒体以 URL 形态混排
	content := []any{}
	rest := t.ResultText()
	for _, b := range blocks {
		if tb, ok := b.(ai.TextBlock); ok {
			if tb.Text != "" {
				content = append(content, map[string]any{"type": "text", "text": tb.Text})
			}
			continue
		}
		if s := blockDataURL(b); s != "" {
			content = append(content, map[string]any{"type": "image_url", "image_url": map[string]any{"url": s}})
		}
	}
	if len(content) > 0 {
		node["content"] = content
	} else {
		node["content"] = rest
	}
	return node
}

func blockDataURL(b ai.ContentBlock) string {
	switch v := b.(type) {
	case ai.ImageBlock:
		return mediaValue(v.URL, v.Data, v.Meta, "image", v.Mime)
	case ai.AudioBlock:
		return mediaValue(v.URL, v.Data, v.Meta, "audio", v.Mime)
	case ai.VideoBlock:
		return mediaValue(v.URL, v.Data, v.Meta, "video", v.Mime)
	}
	return ""
}

// isEmptyAssistantReplay DashScope 原生历史不接收 assistant thinking：移除思考后
// 仅剩 role 的 assistant 必须过滤；仍有正文或工具调用的消息保留。
func (d *Dialect) isEmptyAssistantReplay(m *ai.Message, node map[string]any) bool {
	if _, isAssistant := m.Content.(*ai.AssistantMessage); !isAssistant {
		return false
	}
	_, hasContent := node["content"]
	_, hasCalls := node["tool_calls"]
	return !hasContent && !hasCalls
}

// BuildAssistantToolCallMessage 流式聚合的工具调用 → assistant 消息
// （content/thinking 取 acc 聚合；arguments 净化）。
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

// ---- 响应侧 ----

// ParseAssistantMessage message 节点 → 聚合消息（基类默认：通用解析管线）。
func (d *Dialect) ParseAssistantMessage(acc *ai.Accumulator, node map[string]any) []*ai.AssistantMessage {
	return dialectkit.ParseAssistantMessage(acc, node)
}

// ParseResponseJSON 解析一帧：[DONE] 收尾；code+message 错误；request_id 供应商
// 标识；output.choices[0].message 为主干（search_info.search_results 注入）；
// usage 四项 token 计数。
func (d *Dialect) ParseResponseJSON(ctx *ai.StreamContext, body string) {
	acc := ctx.Acc()

	if body == "[DONE]" { // 不是数据结构
		ctx.AttrRemove(snapshotStateKey) // 本步结束，释放跨帧累积基准
		ctx.AttrRemove(searchEmittedKey) // 本步结束，释放搜索事件去重状态
		if !acc.IsFinished() {
			acc.SetFinished(true)
		}
		return
	}

	var root map[string]any
	if err := json.Unmarshal([]byte(body), &root); err != nil || root == nil {
		return
	}

	d.parseFrameNode(ctx, acc, root)

	if acc.Err() != nil {
		dialectkit.EmitError(ctx, acc.Err(), root)
	}
}

func (d *Dialect) parseFrameNode(ctx *ai.StreamContext, acc *ai.Accumulator, root map[string]any) {
	if code := dialectkit.GetStr(root, "code"); code != "" {
		acc.SetError(errors.New(code + ": " + dialectkit.GetStr(root, "message")))
		return
	}

	acc.SetModel(ctx.Config().Model)
	// 供应商响应标识（req-xxx）：百炼每个流式帧都携带，记录后本步事件自动预填
	ctx.SetProviderResponseID(dialectkit.GetStr(root, "request_id"))

	output := dialectkit.GetNode(root, "output")
	if output != nil {
		// 百炼联网搜索：search_info 在 output 层级，需注入到 message 供通用解析
		searchInfo := dialectkit.GetNode(output, "search_info")
		var searchResults []any
		if searchInfo != nil {
			searchResults = dialectkit.GetArray(searchInfo, "search_results")
		}

		for choiceIndex, choiceItem := range dialectkit.GetArray(output, "choices") {
			choice, _ := choiceItem.(map[string]any)
			if choice == nil {
				continue
			}
			finishReason := dialectkit.GetStr(choice, "finish_reason")
			message := dialectkit.GetNode(choice, "message")
			if message == nil {
				message = map[string]any{}
			}
			if searchResults != nil {
				message["search_results"] = searchResults
			}
			// 兜底：incremental_output 未生效时把全量快照转成增量
			d.normalizeSnapshotMessage(ctx, choiceIndex, message)

			for _, msg := range dialectkit.ParseAssistantMessage(acc, message) {
				if ctx.IsStream() {
					dialectkit.PublishMessageEvents(ctx, msg)
					publishSearchResultEvents(ctx, msg, searchResults, choiceIndex)
				} else {
					acc.SetTerminalMessage(msg)
				}
			}

			if finishReason != "" {
				acc.SetFinished(true)
				acc.SetFinishReason(finishReason)
			}

			// Response 是单结果模型：只消费首个 choice，避免多候选混入同一聚合器。
			break
		}
	}

	if acc.IsFinished() && !ctx.IsStream() && !acc.TerminalPresent() {
		// 流式事件已由事件归一化在终态补齐边界；不再用空消息制造结束信号
		acc.SetTerminalMessage(&ai.AssistantMessage{})
	}

	if usage := dialectkit.GetNode(root, "usage"); usage != nil {
		u := &ai.Usage{
			PromptTokens:     dialectkit.GetInt(usage, "input_tokens"),
			ThinkTokens:      dialectkit.GetInt(usage, "think_tokens"),
			CompletionTokens: dialectkit.GetInt(usage, "output_tokens"),
			TotalTokens:      dialectkit.GetInt(usage, "total_tokens"),
		}
		if raw, err := json.Marshal(usage); err == nil {
			u.Source = raw
		}
		acc.SetUsage(u)
	}
}

// publishSearchResultEvents message 上的类型化搜索结果 → SEARCH_RESULT 事件。
// search_info.search_results 在累计快照和完成帧中可能重复出现；按 choice 与结果
// 稳定身份只发一次（状态挂 StreamContext）。
func publishSearchResultEvents(ctx *ai.StreamContext, msg *ai.AssistantMessage, rawResults []any, choiceIndex int) {
	if len(msg.SearchResults) == 0 {
		return
	}
	emitted, _ := ctx.AttrIfAbsent(searchEmittedKey, func() any {
		return map[string]struct{}{}
	}).(map[string]struct{})

	for ordinal, result := range msg.SearchResults {
		key := searchResultEventKey(choiceIndex, ordinal, &result)
		if _, ok := emitted[key]; ok {
			continue
		}
		emitted[key] = struct{}{}

		ev := ctx.Event(ai.EventSearchResult)
		ev.RawType = "search_info.search_results"
		ev.ItemID = result.ID
		ev.Text = result.Title
		r := result
		ev.SearchResult = &r
		if result.Index > 0 { // Index 为 int：0 视同缺省（omitempty 同口径）
			ev.Index = result.Index
		}
		if ordinal < len(rawResults) {
			if data, err := json.Marshal(rawResults[ordinal]); err == nil {
				ev.Raw = data
			}
		}
		ctx.Emit(ev)
	}
}

// searchResultEventKey 搜索结果的流内稳定身份：供应商 id、index 优先；
// 均缺失时按公共语义字段去重。
func searchResultEventKey(choiceIndex, ordinal int, r *ai.SearchResult) string {
	prefix := fmt.Sprintf("choice:%d:", choiceIndex)
	if r.ID != "" {
		return prefix + "id:" + r.ID
	}
	if r.Index > 0 {
		return prefix + "index:" + fmt.Sprint(r.Index)
	}
	return fmt.Sprintf("%ssemantic:%d\x00%s\x00%s\x00%s",
		prefix, ordinal, r.Title, r.URL, r.Snippet)
}

// ---- 快照 → 增量兜底归一 ----

// snapshotDeltaNormalizer 单通道（正文/思考/工具参数）的「累计快照 → 流式增量」
// 归一器（Java 同名类镜像，与 openai 方言规则一致）：
//   - 累积达 MIN_MATCH_LENGTH 才允许首次判定（短增量在流首极易偶然形成前缀关系）；
//   - 首次判定还要求本帧严格长于累积；一旦判定为快照流即粘滞（此后可吃掉完全相等的重复帧）；
//   - 代价：真快照流的前 MIN_MATCH_LENGTH 个字符仍会追加一次——宁可少截、不可错截。
type snapshotDeltaNormalizer struct {
	accumulated  strings.Builder
	snapshotMode bool
}

const minMatchLength = 8

func (n *snapshotDeltaNormalizer) startsWithAccumulated(value string) bool {
	l := n.accumulated.Len()
	if len(value) < l {
		return false
	}
	return strings.HasPrefix(value, n.accumulated.String())
}

// normalize 返回本帧真正的新增文本（快照重复帧返回空串），并把新增部分记入累积。
// deterministic 模式（用户显式 incremental_output=false）从首帧起按确定快照处理。
func (n *snapshotDeltaNormalizer) normalize(value string) string {
	if value == "" {
		return value
	}
	delta := value
	l := n.accumulated.Len()
	if l > 0 {
		if n.snapshotMode {
			if n.startsWithAccumulated(value) {
				delta = value[l:] // 含完全相等 → ""
			}
		} else if l >= minMatchLength && len(value) > l && n.startsWithAccumulated(value) {
			n.snapshotMode = true
			delta = value[l:]
		}
	}
	n.accumulated.WriteString(delta)
	return delta
}

// snapshotState 流式快照归一状态（按 choice 序号隔离；正文/思考/工具参数各自独立）。
type snapshotState struct {
	deterministicSnapshot bool
	contents              map[int]*snapshotDeltaNormalizer
	reasonings            map[int]*snapshotDeltaNormalizer
	toolArgs              map[int]map[string]*snapshotDeltaNormalizer
}

func (s *snapshotState) content(index int) *snapshotDeltaNormalizer {
	if s.contents == nil {
		s.contents = map[int]*snapshotDeltaNormalizer{}
	}
	if n, ok := s.contents[index]; ok {
		return n
	}
	n := &snapshotDeltaNormalizer{snapshotMode: s.deterministicSnapshot}
	s.contents[index] = n
	return n
}

func (s *snapshotState) reasoning(index int) *snapshotDeltaNormalizer {
	if s.reasonings == nil {
		s.reasonings = map[int]*snapshotDeltaNormalizer{}
	}
	if n, ok := s.reasonings[index]; ok {
		return n
	}
	n := &snapshotDeltaNormalizer{snapshotMode: s.deterministicSnapshot}
	s.reasonings[index] = n
	return n
}

// toolArguments 工具参数状态按 choice 与调用身份双重隔离；调用身份只使用 index、
// 稳定 id 或数组位置，绝不使用可能相同的函数名。
func (s *snapshotState) toolArguments(choiceIndex int, toolCall map[string]any, position int) *snapshotDeltaNormalizer {
	if s.toolArgs == nil {
		s.toolArgs = map[int]map[string]*snapshotDeltaNormalizer{}
	}
	choiceArgs := s.toolArgs[choiceIndex]
	if choiceArgs == nil {
		choiceArgs = map[string]*snapshotDeltaNormalizer{}
		s.toolArgs[choiceIndex] = choiceArgs
	}
	positionKey := fmt.Sprintf("position:%d", position)
	index := identityValue(toolCall["index"])
	id := identityValue(toolCall["id"])
	indexKey := ""
	if index != "" {
		indexKey = "index:" + index
	}
	idKey := ""
	if id != "" {
		idKey = "id:" + id
	}

	var normalizer *snapshotDeltaNormalizer
	if indexKey != "" {
		normalizer = choiceArgs[indexKey]
	}
	if normalizer == nil && idKey != "" {
		normalizer = choiceArgs[idKey]
	}
	if normalizer == nil {
		normalizer = choiceArgs[positionKey]
	}
	if normalizer == nil {
		normalizer = &snapshotDeltaNormalizer{snapshotMode: s.deterministicSnapshot}
	}

	if indexKey != "" {
		choiceArgs[indexKey] = normalizer
	}
	if idKey != "" {
		choiceArgs[idKey] = normalizer
	}
	// 数组位置是显式身份迟到或后续缺失时的桥接别名，必须始终与 index/id 指向同一状态。
	choiceArgs[positionKey] = normalizer
	return normalizer
}

func identityValue(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	if s := fmt.Sprint(v); s != "" {
		return s
	}
	return ""
}

// normalizeSnapshotMessage 兜底：把"累计快照"帧归一为真正的流式增量（原地改写
// message 节点——该节点本轮解析完即弃）。主修在请求侧（流式显式 incremental_output=true），
// 这里只处理用户显式关闭或中转端点忽略参数的残局；服务端已给增量时原文原样返回，
// 不会双重处理。覆盖 string 形态的 content、reasoning_content/reasoning 与
// tool_calls[].function.arguments（仅字符串形态）。
func (d *Dialect) normalizeSnapshotMessage(ctx *ai.StreamContext, choiceIndex int, message map[string]any) {
	if !ctx.IsStream() {
		return // 非流式：一帧即全部，截前缀只会丢内容
	}

	contentRaw, _ := message["content"].(string) // 只对 string 形态判定（多模态数组不是文本追加语义）

	// 推理字段名与通用解析保持同一优先级：reasoning_content 优先，其次 reasoning
	reasoningKey := ""
	if _, has := message["reasoning_content"]; has {
		reasoningKey = "reasoning_content"
	} else if _, has := message["reasoning"]; has {
		reasoningKey = "reasoning"
	}
	reasoningRaw := ""
	if reasoningKey != "" {
		reasoningRaw = dialectkit.GetStr(message, reasoningKey)
	}

	toolCalls := dialectkit.GetArray(message, "tool_calls")
	if contentRaw == "" && reasoningRaw == "" && len(toolCalls) == 0 {
		return // 无可归一化字段
	}

	state, _ := ctx.AttrIfAbsent(snapshotStateKey, func() any {
		return &snapshotState{deterministicSnapshot: isExplicitSnapshotMode(ctx)}
	}).(*snapshotState)

	if contentRaw != "" {
		if delta := state.content(choiceIndex).normalize(contentRaw); delta != contentRaw {
			message["content"] = delta
		}
	}

	if reasoningRaw != "" {
		if delta := state.reasoning(choiceIndex).normalize(reasoningRaw); delta != reasoningRaw {
			message[reasoningKey] = delta
		}
	}

	for position, item := range toolCalls {
		toolCall, _ := item.(map[string]any)
		if toolCall == nil {
			continue
		}
		fn := dialectkit.GetNode(toolCall, "function")
		if fn == nil {
			continue
		}
		argsRaw, _ := fn["arguments"].(string) // 只处理字符串参数；对象形态不是流式字符串追加语义
		if argsRaw == "" {
			continue
		}
		if delta := state.toolArguments(choiceIndex, toolCall, position).normalize(argsRaw); delta != argsRaw {
			fn["arguments"] = delta
		}
	}
}

// isExplicitSnapshotMode 用户显式关闭增量输出时，响应必按累计快照解释（首帧起
// 确定性归一）；缺省或 true 则保持保守启发式。
func isExplicitSnapshotMode(ctx *ai.StreamContext) bool {
	opts := ctx.Options()
	if opts == nil {
		return false
	}
	v, ok := opts.OptionGet("incremental_output").(bool)
	return ok && !v
}
