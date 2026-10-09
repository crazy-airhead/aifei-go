package openai

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai"
)

// responsesUnsupportedKeys Chat Completions 专属、Responses API 不接受的参数
// （官方端点会 400）；构建期统一剔除，便于同一份 ChatOptions 在两种协议间切换。
var responsesUnsupportedKeys = map[string]struct{}{
	"stop": {}, "frequency_penalty": {}, "presence_penalty": {},
	"logit_bias": {}, "n": {}, "seed": {},
}

// includeReasoningEncrypted 无状态（store=false）多轮回放 reasoning 所需的 include 项。
const includeReasoningEncrypted = "reasoning.encrypted_content"

// buildResponsesRequest 组装请求体（对标 OpenaiResponsesRequestBuilder.build）：
// model → system 消息提取到顶层 instructions → input 数组 → stream →
// options 归一透传（专属键剔除/映射）→ 统一思考开关 → store=false 补 include →
// prompt_cache_key → tools（扁平形态）。
func buildResponsesRequest(cfg *ai.Config, opts *ai.ChatOptions, msgs []*ai.Message, isStream bool) ([]byte, string, error) {
	if opts == nil {
		opts = &ai.ChatOptions{}
	}
	root := map[string]any{}
	var model string
	if cfg != nil {
		model = cfg.Model
	}
	if model != "" {
		root["model"] = model
	}

	// Responses API：system 消息提取到顶层 instructions（多条以空行连接）
	var instructions []string
	for _, m := range msgs {
		if m.Role != ai.RoleSystem {
			continue
		}
		if text := contentText(m.Content); text != "" {
			instructions = append(instructions, text)
		}
	}

	inputArray := []any{}
	allowInputAudio := opts.OptionGet(OptionResponsesInputAudioEnabled) == true
	replayReasoning := responsesReasoningReplayEnabled(model, opts)
	for _, m := range msgs {
		if m.Role == ai.RoleSystem {
			continue
		}
		if err := appendResponsesInputItem(&inputArray, m, allowInputAudio, replayReasoning); err != nil {
			return nil, "", err
		}
	}
	root["input"] = inputArray
	root["stream"] = isStream

	// options 归一透传
	var thinkingSwitch any
	var promptCacheBreakpoint, promptCacheBreakpoints any
	var optionInstructions string
	for key, val := range opts.Raw {
		switch key {
		case "stream", "response_format":
			// response_format 不适用于 Responses API（用 text.format 替代）
		case "stream_options":
			// Responses 的 stream_options 与 Chat Completions 不是同一组字段；仅
			// include_obfuscation 可转发，include_usage 属于 Chat Completions。
			if isStream {
				if so, ok := val.(map[string]any); ok {
					if v, has := so["include_obfuscation"]; has {
						root["stream_options"] = map[string]any{"include_obfuscation": v}
					}
				}
			}
		case "stop", "frequency_penalty", "presence_penalty", "logit_bias", "n", "seed":
			// Chat Completions 专属参数：剔除避免 400
		case "max_tokens", "max_completion_tokens":
			// → max_output_tokens（已显式配置时不覆盖）
			if _, has := opts.Raw["max_output_tokens"]; !has {
				root["max_output_tokens"] = val
			}
		case "instructions":
			// 与 system 消息合并（延后处理，避免互相覆盖）
			if val != nil {
				optionInstructions = fmt.Sprint(val)
			}
		case "prompt_cache_breakpoint":
			// SDK 断点挂在 input content 上而非请求顶层；延后挂到最后一个可缓存内容项
			promptCacheBreakpoint = val
		case "prompt_cache_breakpoints":
			// Solon 兼容扩展：按输入顺序最多四个显式断点挂到末尾可缓存内容块
			promptCacheBreakpoints = val
		case OptionResponsesReasoningDeltaMode, OptionResponsesInputAudioEnabled, OptionResponsesReasoningReplayEnabled:
			// 仅控制方言本地兼容行为，不得发送给服务端
		case "prompt_cache_options":
			applyResponsesPromptCacheOptions(root, val)
		case "prompt_cache_retention":
			applyResponsesPromptCacheRetention(root, val)
		case "thinking":
			if _, ok := val.(bool); ok {
				thinkingSwitch = val // 统一思考开关（Boolean）延后处理
			} else {
				root[key] = val
			}
		case "reasoning_effort":
			// 统一推理水平 → reasoning.effort（与 thinking 开关一起在循环后处理）
		case "reasoning":
			buildResponsesReasoningNode(root, val)
		case "tool_choice":
			applyResponsesToolChoice(root, val)
		default:
			root[key] = val
		}
	}

	// 显式缓存断点挂到输入内容项（ResponseInputText/Image/File.prompt_cache_breakpoint）
	if promptCacheBreakpoints != nil {
		applied := applyResponsesPromptCacheBreakpoints(inputArray, promptCacheBreakpoints)
		if applied > 3 {
			if _, has := root["prompt_cache_options"]; !has {
				root["prompt_cache_options"] = map[string]any{"mode": "explicit"}
			}
		}
	} else {
		applyResponsesPromptCacheBreakpoint(inputArray, promptCacheBreakpoint)
	}

	// instructions：system 消息优先在前，options 逃生舱追加在后
	if optionInstructions != "" {
		instructions = append(instructions, optionInstructions)
	}
	if len(instructions) > 0 {
		root["instructions"] = strings.Join(instructions, "\n\n")
	}

	applyResponsesUnifiedReasoning(root, opts, thinkingSwitch, model)
	applyResponsesReasoningInclude(root)

	if opts.CacheControl.PromptCacheKey != "" {
		// CacheControl.type/ttl 是 Anthropic 消息级语义，不能解释为 Responses
		// PromptCacheOptions；仅缓存路由提示键可映射
		root["prompt_cache_key"] = opts.CacheControl.PromptCacheKey
	}

	if len(opts.Tools) > 0 {
		root["tools"] = buildResponsesToolsNode(opts.Tools)
	}

	body, err := json.Marshal(root)
	if err != nil {
		return nil, "", err
	}
	return body, BuildAPIURL(cfg.APIURL, "responses"), nil
}

// responsesReasoningReplayEnabled 多轮 reasoning item 回放开关：显式 Boolean 优先；
// GLM 的 Responses 兼容层会把 input item 转成 Anthropic messages，但 reasoning
// item 没有 message.content，回放后会被其内部校验拒绝——glm-* 默认关。
func responsesReasoningReplayEnabled(model string, opts *ai.ChatOptions) bool {
	if opts != nil {
		if b, ok := opts.OptionGet(OptionResponsesReasoningReplayEnabled).(bool); ok {
			return b
		}
	}
	return model == "" || !strings.HasPrefix(strings.ToLower(model), "glm-")
}

// ---- input item 构建 ----

func appendResponsesInputItem(inputArray *[]any, m *ai.Message, allowInputAudio, replayReasoning bool) error {
	switch m.Role {
	case ai.RoleTool:
		appendResponsesToolMessageItem(inputArray, m)
		return nil
	case ai.RoleAssistant:
		return appendResponsesAssistantItems(inputArray, m, allowInputAudio, replayReasoning)
	case ai.RoleUser:
		node := map[string]any{"role": "user"}
		blocks := messageBlocks(m.Content)
		if blocks == nil {
			node["content"] = contentText(m.Content)
		} else {
			// 多模态（用户文本不做 think 剔除，避免正常含 think 字样的文本被清空）
			var arr []any
			for _, b := range blocks {
				if item, err := responsesInputContent(b, false, allowInputAudio); err != nil {
					return err
				} else if item != nil {
					arr = append(arr, item)
				}
			}
			if len(arr) == 0 {
				// 全部媒体被截断时补文本投影，避免出站空 content 数组
				node["content"] = contentText(m.Content)
			} else {
				node["content"] = arr
			}
		}
		*inputArray = append(*inputArray, node)
		return nil
	}
	// 其他角色消息：小写角色名 + 文本投影
	role := strings.ToLower(string(m.Role))
	if role == "" {
		role = "user"
	}
	*inputArray = append(*inputArray, map[string]any{"role": role, "content": contentText(m.Content)})
	return nil
}

// appendResponsesToolMessageItem 函数工具输出：output 支持字符串或
// input_text/input_image/input_file 内容数组（官方 ResponseFunctionCallOutputItem）。
func appendResponsesToolMessageItem(inputArray *[]any, m *ai.Message) {
	t, _ := m.Content.(*ai.ToolMessage)
	item := map[string]any{"type": "function_call_output"}
	text := ""
	var blocks []ai.ContentBlock
	if t != nil {
		item["call_id"] = t.CallID
		text = t.ResultText()
		if t.Result != nil {
			blocks = t.Result.Blocks
		}
	}
	if len(blocks) == 0 {
		item["output"] = text
		*inputArray = append(*inputArray, item)
		return
	}
	var arr []any
	for _, b := range blocks {
		if out := functionCallOutputContent(b); out != nil {
			arr = append(arr, out)
		}
	}
	if len(arr) == 0 {
		// output 为必填字段；不支持或已截断的媒体回退文本投影，最终以空串占位
		item["output"] = text
	} else {
		item["output"] = arr
	}
	*inputArray = append(*inputArray, item)
}

func functionCallOutputContent(b ai.ContentBlock) map[string]any {
	switch blk := b.(type) {
	case ai.TextBlock:
		if blk.Text != "" {
			return map[string]any{"type": "input_text", "text": blk.Text}
		}
	case ai.ImageBlock:
		if url := mediaDataString(blk.URL, blk.Data, "image", blk.Mime); url != "" &&
			!strings.HasPrefix(url, "image-generation://") {
			return map[string]any{"type": "input_image", "image_url": url, "detail": "auto"}
		}
	case ai.BlobBlock:
		if len(blk.Data) > 0 {
			return map[string]any{"type": "input_file", "file_data": base64Of(blk.Data)}
		}
	}
	return nil
}

func base64Of(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// appendResponsesAssistantItems assistant 历史 → input items。
// 优先按 Responses 原始 output_index 回放完整 output item（避免按类型重排或字段降级）；
// 无协议状态时合成：reasoning 项先行（官方要求在其后续项之前）→ 多 message 项 →
// image_generation_call 回传 → 文本/多模态 → function_call 项。
func appendResponsesAssistantItems(inputArray *[]any, m *ai.Message, allowInputAudio, replayReasoning bool) error {
	am := assistantOf(m)
	if am == nil {
		*inputArray = append(*inputArray, map[string]any{"role": "assistant", "content": contentText(m.Content)})
		return nil
	}

	// 优先回放原始 output items
	if appendResponsesOutputItems(inputArray, am, replayReasoning) {
		return nil
	}

	// 1) reasoning 项先行（完整消息允许同时含 text/thinking，不能按消息分类二选一）
	reasoningEmitted := replayReasoning && appendResponsesReasoningItem(inputArray, am)
	messagesEmitted := appendResponsesMessageItems(inputArray, am)

	// 2) 纯思考消息：reasoning 已回放，无正文/工具/媒体时不再补空 assistant 项
	if am.ThinkingOnly() {
		return nil
	}

	appendResponsesSynthesizedItems(inputArray, am, reasoningEmitted, messagesEmitted, allowInputAudio)
	return nil
}

// appendResponsesOutputItems 按协议状态回放原始 output items（output_index 排序；
// reasoning 项受回放开关控制；function_call 参数出站净化）。
func appendResponsesOutputItems(inputArray *[]any, am *ai.AssistantMessage, replayReasoning bool) bool {
	data := resolveResponsesData(am)
	if data == nil {
		return false
	}
	wrappers, _ := data[respDataOutputItems].([]any)
	type indexed struct {
		index int
		item  map[string]any
	}
	var items []indexed
	for _, w := range wrappers {
		wrapper, ok := w.(map[string]any)
		if !ok {
			continue
		}
		item, ok := wrapper["item"].(map[string]any)
		if !ok {
			continue
		}
		idx := int(getInt(wrapper, "output_index"))
		if v, has := wrapper["output_index"]; !has || v == nil {
			idx = 1 << 30 // 缺省排最后（Java Integer.MAX_VALUE）
		}
		items = append(items, indexed{idx, item})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].index < items[j].index })
	emitted := false
	for _, it := range items {
		if !replayReasoning && getStr(it.item, "type") == "reasoning" {
			continue
		}
		if getStr(it.item, "type") == "function_call" {
			it.item["arguments"] = SanitizeArguments(getStr(it.item, "arguments"), getStr(it.item, "name"))
		}
		*inputArray = append(*inputArray, it.item)
		emitted = true
	}
	return emitted
}

// appendResponsesReasoningItem 回放或合成 reasoning 项：
// 协议状态 REASONING_ITEMS 逐项回放（id/encrypted 身份去重）→ 单一 id/encrypted
// 合成 → 无官方元数据时退化为 reasoning_text（DeepSeek 私有扩展）。
// 官方 reasoning item 的 summary 为必填（可为空数组），统一补位。
func appendResponsesReasoningItem(inputArray *[]any, am *ai.AssistantMessage) bool {
	if data := resolveResponsesData(am); data != nil {
		if replayItems, ok := data[respDataReasoningItems].([]any); ok {
			emitted := false
			identities := map[string]struct{}{}
			for _, obj := range replayItems {
				item, ok := obj.(map[string]any)
				if !ok {
					continue
				}
				id := getStr(item, "id")
				encrypted := getStr(item, "encrypted_content")
				if id == "" && encrypted == "" {
					continue
				}
				identity := "id:" + id
				if id == "" {
					identity = "encrypted:" + encrypted
				}
				if _, dup := identities[identity]; dup {
					continue
				}
				identities[identity] = struct{}{}
				reasoning := map[string]any{"type": "reasoning", "summary": []any{}}
				if id != "" {
					reasoning["id"] = id
				}
				if encrypted != "" {
					reasoning["encrypted_content"] = encrypted
				}
				*inputArray = append(*inputArray, reasoning)
				emitted = true
			}
			if emitted {
				return true
			}
		}
		id := ""
		encrypted := ""
		if v, ok := data[respDataReasoningID]; ok && v != nil {
			id = fmt.Sprint(v)
		}
		if v, ok := data[respDataReasoningEnc]; ok && v != nil {
			encrypted = fmt.Sprint(v)
		}
		if id != "" || encrypted != "" {
			reasoning := map[string]any{"type": "reasoning", "summary": []any{}}
			if id != "" {
				reasoning["id"] = id
			}
			if encrypted != "" {
				reasoning["encrypted_content"] = encrypted
			}
			*inputArray = append(*inputArray, reasoning)
			return true
		}
	}

	// 无官方元数据：退化为 reasoning_text（DeepSeek 扩展）
	if am.Thinking != "" {
		*inputArray = append(*inputArray, map[string]any{
			"type":    "reasoning",
			"summary": []any{},
			"content": []any{map[string]any{"type": "reasoning_text", "text": am.Thinking}},
		})
		return true
	}
	return false
}

// appendResponsesMessageItems 协议状态含多个 message 项时按各自 phase 原样回放。
func appendResponsesMessageItems(inputArray *[]any, am *ai.AssistantMessage) bool {
	data := resolveResponsesData(am)
	if data == nil {
		return false
	}
	items, ok := data[respDataMessageItems].([]any)
	if !ok || len(items) <= 1 {
		return false
	}
	for _, obj := range items {
		item, ok := obj.(map[string]any)
		if !ok {
			continue
		}
		node := map[string]any{"role": "assistant", "content": getStr(item, "text")}
		if phase := getStr(item, "phase"); phase == "commentary" || phase == "final_answer" {
			node["phase"] = phase
		}
		*inputArray = append(*inputArray, node)
	}
	return true
}

// appendResponsesSynthesizedItems 合成 assistant input items：image_generation_call
// 历史项（按 id 回传）→ 文本/多模态 content（input_* 形态）→ function_call 项。
func appendResponsesSynthesizedItems(inputArray *[]any, am *ai.AssistantMessage, reasoningEmitted, messagesEmitted, allowInputAudio bool) error {
	// 1) image_generation_call 历史项（官方多轮约定：按 id 而非 data 回传）
	for _, b := range am.Blocks {
		ib, ok := b.(ai.ImageBlock)
		if !ok || !isImageGenerationBlock(ib) {
			continue
		}
		if genID := imageGenerationID(ib); genID != "" {
			*inputArray = append(*inputArray, map[string]any{"type": "image_generation_call", "id": genID})
		}
	}

	// 2) 文本 / 多模态 content；多 message 项已按各自 phase 回放时不再聚合重复写入
	hasToolCalls := len(am.ToolCalls) > 0
	if !messagesEmitted {
		if len(am.Blocks) > 0 {
			// 官方约束：EasyInputMessage(role=assistant) 的 content 仅接受
			// input_*；output_text 只能出现在带 id 的 output message 项里
			msgNode := map[string]any{"role": "assistant"}
			applyResponsesPhase(msgNode, am)
			var arr []any
			for _, b := range am.Blocks {
				if ib, ok := b.(ai.ImageBlock); ok && isImageGenerationBlock(ib) {
					continue // 已以 image_generation_call id 回传，跳过 data 再写
				}
				item, err := responsesInputContent(b, true, allowInputAudio)
				if err != nil {
					return err
				}
				if item != nil {
					arr = append(arr, item)
				}
			}
			if len(arr) == 0 {
				arr = append(arr, map[string]any{"type": "input_text", "text": am.Text})
			}
			msgNode["content"] = arr
			*inputArray = append(*inputArray, msgNode)
		} else {
			// 纯文本：有 tool_calls 或已输出 reasoning 项时仅非空才写（避免多余空 assistant 项）
			if hasToolCalls || reasoningEmitted {
				if am.Text != "" {
					node := map[string]any{"role": "assistant", "content": am.Text}
					applyResponsesPhase(node, am)
					*inputArray = append(*inputArray, node)
				}
			} else {
				node := map[string]any{"role": "assistant", "content": am.Text}
				applyResponsesPhase(node, am)
				*inputArray = append(*inputArray, node)
			}
		}
	}

	// 3) 工具调用 items（出站兜底净化：截断/双重编码的 arguments 禁止原样回传）
	for i := range am.ToolCalls {
		call := &am.ToolCalls[i]
		*inputArray = append(*inputArray, map[string]any{
			"type":      "function_call",
			"call_id":   call.ID,
			"name":      call.Name,
			"arguments": SanitizeCall(call),
		})
	}
	return nil
}

// responsesInputContent Responses 规范的输入内容项（input_text/input_image/input_audio）。
// 音频为 {type:input_audio,input_audio:{data,format}} 嵌套形态（部分厂商扩展），
// 未启用时报错；仅 URL 无 base64 时降级为说明文本，避免把 URL 塞进 data。
func responsesInputContent(b ai.ContentBlock, stripThink, allowInputAudio bool) (map[string]any, error) {
	switch blk := b.(type) {
	case ai.TextBlock:
		text := blk.Text
		if stripThink {
			text = ai.StripThinkTags(text)
		}
		if text != "" {
			return map[string]any{"type": "input_text", "text": text}, nil
		}
	case ai.ImageBlock:
		// Session 截断后 data/url 皆空时跳过；image_generation 产出已按 id 回传
		if url := mediaDataString(blk.URL, blk.Data, "image", blk.Mime); url != "" &&
			!strings.HasPrefix(url, "image-generation://") {
			return map[string]any{"type": "input_image", "image_url": url, "detail": "auto"}, nil
		}
	case ai.AudioBlock:
		if !allowInputAudio {
			return nil, errors.New("openai-responses: ResponseInputContent does not support input_audio; " +
				"set responses_input_audio_enabled=true only for a compatible gateway")
		}
		if len(blk.Data) > 0 {
			format := ""
			if mime := blk.Mime; strings.HasPrefix(mime, "audio/") {
				format = strings.TrimPrefix(mime, "audio/")
			}
			inputAudio := map[string]any{"data": base64Of(blk.Data)}
			if format != "" {
				inputAudio["format"] = format
			}
			return map[string]any{"type": "input_audio", "input_audio": inputAudio}, nil
		}
		if blk.URL != "" && !strings.HasPrefix(blk.URL, "audio://") {
			return map[string]any{"type": "input_text", "text": "[audio]" + blk.URL}, nil
		}
	}
	return nil, nil
}

// isImageGenerationBlock 是否为 image_generation_call 产出的图像块（多轮回传靠 id 而非 data）。
func isImageGenerationBlock(b ai.ImageBlock) bool {
	if b.Meta == nil {
		return false
	}
	if v, ok := b.Meta["source_type"]; ok && fmt.Sprint(v) == "image_generation_call" {
		return true
	}
	_, has := b.Meta["image_generation_id"]
	return has
}

// imageGenerationID 读取 image_generation_call 的服务端 id。
func imageGenerationID(b ai.ImageBlock) string {
	return firstMetaStr(b.Meta, "image_generation_id", "id")
}

// applyResponsesPhase 单一 phase（commentary/final_answer）挂到合成 assistant 节点
// （来自协议状态的 phase 数据）。
func applyResponsesPhase(node map[string]any, am *ai.AssistantMessage) {
	data := resolveResponsesData(am)
	if data == nil {
		return
	}
	v, ok := data[respDataPhase]
	if !ok || v == nil {
		return
	}
	phase := strings.TrimSpace(fmt.Sprint(v))
	if phase == "commentary" || phase == "final_answer" {
		node["phase"] = phase
	}
}

// ---- reasoning / tool_choice / include ----

// buildResponsesReasoningNode 显式 reasoning 选项 → reasoning 节点：
// effort 档位归一（未知值透传）、summary/generate_summary 归一为官方字符串枚举、
// context/mode 透传，未知字段不出站；字符串形态视为 effort 简写。
func buildResponsesReasoningNode(root map[string]any, value any) {
	if value == nil {
		return
	}
	reasoning, _ := root["reasoning"].(map[string]any)
	if reasoning == nil {
		reasoning = map[string]any{}
		root["reasoning"] = reasoning
	}
	switch v := value.(type) {
	case map[string]any:
		for key, val := range v {
			if key == "" || val == nil {
				continue
			}
			switch key {
			case "effort":
				if normalized := normalizeResponsesEffort(val, true); normalized != "" {
					reasoning["effort"] = normalized
				}
			case "summary", "generate_summary":
				// 官方 Reasoning.summary 是字符串枚举（auto/concise/detailed），
				// 数组形态只属于输出侧 ReasoningItem.summary，写成数组会 400
				if summary := normalizeResponsesSummary(val); summary != "" {
					reasoning[key] = summary
				}
			case "context", "mode":
				reasoning[key] = val
			}
		}
	case string:
		if normalized := normalizeResponsesEffort(v, true); normalized != "" {
			reasoning["effort"] = normalized
		}
	}
}

// normalizeResponsesSummary 归一为官方字符串形态：集合/数组/"[x]" 历史写法取首个非空值。
func normalizeResponsesSummary(value any) string {
	switch v := value.(type) {
	case []any:
		for _, s := range v {
			if text := knownResponsesSummary(s); text != "" {
				return text
			}
		}
		return ""
	case string:
		s := strings.TrimSpace(v)
		if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") && len(s) > 1 {
			for _, part := range strings.Split(s[1:len(s)-1], ",") {
				if text := knownResponsesSummary(strings.ReplaceAll(strings.TrimSpace(part), "\"", "")); text != "" {
					return text
				}
			}
			return ""
		}
		return knownResponsesSummary(s)
	}
	return knownResponsesSummary(value)
}

func knownResponsesSummary(value any) string {
	if value == nil {
		return ""
	}
	s := strings.TrimSpace(fmt.Sprint(value))
	if s == "auto" || s == "concise" || s == "detailed" {
		return s
	}
	return ""
}

// applyResponsesUnifiedReasoning 统一 thinking 开关 + reasoning_effort。
// 显式 reasoning 优先（不受模型能力门限限制）；统一选项仅对 GPT-5/o 系模型自动映射：
// thinking(false) → effort=none；reasoning_effort → effort（严格归一）；
// thinking(true) → summary=auto（reasoning_effort 单独设置时不加 summary）。
func applyResponsesUnifiedReasoning(root map[string]any, opts *ai.ChatOptions, thinkingSwitch any, model string) {
	if _, has := root["reasoning"]; has {
		return
	}
	if !isReasoningCapableModel(strings.ToLower(model)) {
		return
	}
	reasoning := map[string]any{}

	if b, ok := thinkingSwitch.(bool); ok && !b {
		reasoning["effort"] = "none"
		root["reasoning"] = reasoning
		return
	}

	if opts != nil {
		if v, has := opts.Raw["reasoning_effort"]; has && v != nil {
			if effort := normalizeResponsesEffort(v, false); effort != "" {
				reasoning["effort"] = effort
			}
		}
	}
	// 只有显式 thinking(true) 才表示调用方要求可观察的推理摘要
	if b, ok := thinkingSwitch.(bool); ok && b {
		reasoning["summary"] = "auto"
	}
	if len(reasoning) > 0 {
		root["reasoning"] = reasoning
	}
}

// normalizeResponsesEffort 规范化 reasoning.effort：保留官方档位
// none/minimal/low/medium/high/xhigh/max（min → low）；统一 reasoning_effort 严格
// 归一（空/auto/非法返回空），显式 reasoning 未知值透传（兼容厂商扩展）。
func normalizeResponsesEffort(value any, passthroughUnknown bool) string {
	if value == nil {
		return ""
	}
	effort := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
	switch effort {
	case "":
		return ""
	case "auto":
		return ""
	case "none", "minimal", "low", "medium", "high", "xhigh", "max":
		return effort
	case "min":
		return "low"
	}
	if passthroughUnknown {
		return effort
	}
	return ""
}

// applyResponsesToolChoice tool_choice 归一：Chat Completions 的
// {type:function,function:{name}} → Responses 扁平 {type:function,name}；
// 字符串（none/auto/required）与其它类型（allowed_tools/mcp 等）原样透传。
func applyResponsesToolChoice(root map[string]any, value any) {
	if value == nil {
		return
	}
	if node, ok := value.(map[string]any); ok && getStr(node, "type") == "function" {
		if fn, ok := node["function"].(map[string]any); ok && fn != nil {
			flat := map[string]any{"type": "function"}
			if name := getStr(fn, "name"); name != "" {
				flat["name"] = name
			}
			root["tool_choice"] = flat
			return
		}
	}
	root["tool_choice"] = value
}

// applyResponsesReasoningInclude store=false 时补 include=reasoning.encrypted_content：
// encrypted_content 仅在 include 显式请求时返回，无状态场景又必须靠它回放 reasoning 项。
func applyResponsesReasoningInclude(root map[string]any) {
	if store, ok := root["store"].(bool); !ok || store {
		return // 默认 store=true：服务端保留 reasoning 项，靠 id 即可回放
	}
	include, ok := root["include"].([]any)
	if !ok {
		include = nil
	}
	for _, item := range include {
		if s, ok := item.(string); ok && s == includeReasoningEncrypted {
			return
		}
	}
	root["include"] = append(include, includeReasoningEncrypted)
}

// ---- prompt cache ----

// applyResponsesPromptCacheBreakpoint 单断点挂到最后一个可缓存内容项
// （Responses 官方断点的 mode 是协议值 explicit；after_tools 等只是上层挂载策略）。
func applyResponsesPromptCacheBreakpoint(inputArray []any, value any) {
	if !validResponsesBreakpoint(value) {
		return
	}
	contents := responsesCacheableContents(inputArray)
	if len(contents) > 0 {
		contents[len(contents)-1]["prompt_cache_breakpoint"] = map[string]any{"mode": "explicit"}
	}
}

// applyResponsesPromptCacheBreakpoints 多断点（Solon 兼容扩展）：有效描述符计数
// （上限 4）挂到末尾若干可缓存内容块；返回实际挂载数。
func applyResponsesPromptCacheBreakpoints(inputArray []any, value any) int {
	count := 0
	if requested, ok := value.([]any); ok {
		for _, descriptor := range requested {
			if validResponsesBreakpoint(descriptor) {
				count++
			}
		}
	} else if validResponsesBreakpoint(value) {
		count = 1
	}
	if count > 4 {
		count = 4
	}
	contents := responsesCacheableContents(inputArray)
	if count > len(contents) {
		count = len(contents)
	}
	for i := len(contents) - count; i < len(contents); i++ {
		contents[i]["prompt_cache_breakpoint"] = map[string]any{"mode": "explicit"}
	}
	return count
}

// validResponsesBreakpoint Boolean true / {mode:"explicit"} / "explicit"|"after_tools"。
func validResponsesBreakpoint(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case map[string]any:
		return getStr(v, "mode") == "explicit"
	case string:
		s := strings.TrimSpace(v)
		return s == "explicit" || s == "after_tools"
	}
	return false
}

// responsesCacheableContents 收集可挂断点的输入内容项（input_text/input_image/
// input_file）；string content 项先转成 content 数组再收集。
func responsesCacheableContents(inputArray []any) []map[string]any {
	var result []map[string]any
	for _, raw := range inputArray {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch content := item["content"].(type) {
		case []any:
			for _, part := range content {
				p, ok := part.(map[string]any)
				if !ok {
					continue
				}
				switch getStr(p, "type") {
				case "input_text", "input_image", "input_file":
					result = append(result, p)
				}
			}
		case string:
			part := map[string]any{"type": "input_text", "text": content}
			item["content"] = []any{part}
			result = append(result, part)
		}
	}
	return result
}

// applyResponsesPromptCacheOptions prompt_cache_options 白名单归一
// （mode: implicit|explicit；ttl: 30m；未知字段忽略）。
func applyResponsesPromptCacheOptions(root map[string]any, value any) {
	source, ok := value.(map[string]any)
	if !ok {
		return
	}
	target := map[string]any{}
	for key, option := range source {
		switch key {
		case "mode":
			if mode := fmt.Sprint(option); mode == "implicit" || mode == "explicit" {
				target["mode"] = mode
			}
		case "ttl":
			if ttl := fmt.Sprint(option); ttl == "30m" {
				target["ttl"] = ttl
			}
		}
	}
	if len(target) > 0 {
		root["prompt_cache_options"] = target
	}
}

// applyResponsesPromptCacheRetention prompt_cache_retention 白名单（in_memory|24h）。
func applyResponsesPromptCacheRetention(root map[string]any, value any) {
	if value == nil {
		return
	}
	retention := strings.TrimSpace(fmt.Sprint(value))
	if retention == "in_memory" || retention == "24h" {
		root["prompt_cache_retention"] = retention
	}
}

// ---- tools ----

// buildResponsesToolsNode Responses 扁平工具形态：{type:function,name,description,
// parameters,strict(必填),output_schema?}——无 Chat Completions 的 function 嵌套。
// 非法 input schema 回退空参数骨架；非法 output schema 丢弃（不污染请求）。
// Java 的 strict+非 strict schema 校验（ToolSchemaUtil）不迁移：Go 方言矩阵
// 一致不做 schema 前置校验，交由服务端报错。
func buildResponsesToolsNode(tools []ai.FunctionTool) []any {
	out := make([]any, 0, len(tools))
	for _, t := range tools {
		node := map[string]any{
			"type":        "function",
			"name":        t.Name(),
			"description": ai.DescriptionAndMeta(t),
		}
		schemaNode := map[string]any(nil)
		if s := t.InputSchema(); s != "" {
			var cand map[string]any
			if json.Unmarshal([]byte(s), &cand) == nil && cand != nil {
				schemaNode = cand
			}
		}
		if schemaNode == nil {
			schemaNode = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		node["parameters"] = schemaNode
		// openai-java 4.52.0 Responses FunctionTool 将 strict 定义为必填字段
		if strict := t.Strict(); strict != nil {
			node["strict"] = *strict
		} else {
			node["strict"] = false
		}
		if s := t.OutputSchema(); s != "" {
			var cand map[string]any
			if json.Unmarshal([]byte(s), &cand) == nil && cand != nil {
				node["output_schema"] = cand
			}
		}
		out = append(out, node)
	}
	return out
}
