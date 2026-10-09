package openai

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/internal/dialectkit"
)

// OptionResponsesReasoningDeltaMode 官方 reasoning 增量帧按累计快照语义下发时，
// 设为 "snapshot" 启用后缀归一；缺省按官方 delta（新增内容）追加。
const OptionResponsesReasoningDeltaMode = "responses_reasoning_delta_mode"

// OptionResponsesInputAudioEnabled 允许出站 input_audio 嵌套形态（仅兼容网关支持）。
const OptionResponsesInputAudioEnabled = "responses_input_audio_enabled"

// OptionResponsesReasoningReplayEnabled 多轮 reasoning item 回放开关（GLM 兼容层默认关）。
const OptionResponsesReasoningReplayEnabled = "responses_reasoning_replay_enabled"

// ResponsesDialect OpenAI Responses API 方言（/v1/responses，P1-e）。
type ResponsesDialect struct{}

func init() { ai.RegisterDialect(&ResponsesDialect{}, 100) }

func (d *ResponsesDialect) Name() string    { return "openai-responses" }
func (d *ResponsesDialect) IsDefault() bool { return false }

// Matched 显式 standard/provider "openai-responses"，或标准名缺省且 APIURL 以
// /responses 结尾（去尾斜杠/查询串/# 后缀后比较）。
func (d *ResponsesDialect) Matched(cfg *ai.Config) bool {
	if cfg == nil {
		return false
	}
	if cfg.StandardOrProvider() == "openai-responses" {
		return true
	}
	return cfg.StandardOrProvider() == "" &&
		strings.HasSuffix(normalizeResponsesURL(cfg.APIURL), "/responses")
}

func normalizeResponsesURL(apiURL string) string {
	if i := strings.Index(apiURL, "#"); i >= 0 {
		apiURL = apiURL[:i]
	}
	if i := strings.Index(apiURL, "?"); i >= 0 {
		apiURL = apiURL[:i]
	}
	return strings.TrimRight(apiURL, "/")
}

// ---- 协议状态（对标 OpenaiResponsesMessageStateSupport）----

const (
	responsesProtocolID      = "openai.responses"
	responsesProtocolVersion = 1
)

// 协议 data 键（终态协议状态内，随会话回放）。
const (
	respDataOutputItems    = "responses_output_items" // [{output_index, item}]
	respDataMessageItems   = "response_message_items" // [{id?, phase?, text}]
	respDataReasoningItems = "reasoning_items"        // [{id?, encrypted_content?}]
	respDataReasoningID    = "reasoning_item_id"
	respDataReasoningEnc   = "reasoning_encrypted_content"
	respDataPhase          = "phase"
)

// 聚合工作台键（acc.AggregationMeta()，仅解析期间存在；提升时映射成上面的稳定协议键）。
const (
	aggOutputItems    = "__openai_responses.output_items"
	aggMessageItems   = "__openai_responses.message_items"
	aggReasoningItems = "__openai_responses.reasoning_items"
	aggReasoningID    = "__openai_responses.reasoning_item_id"
	aggReasoningEnc   = "__openai_responses.reasoning_encrypted_content"
	aggPhase          = "__openai_responses.phase"
)

var responsesAggKeyPairs = [][2]string{
	{aggOutputItems, respDataOutputItems},
	{aggMessageItems, respDataMessageItems},
	{aggReasoningItems, respDataReasoningItems},
	{aggReasoningID, respDataReasoningID},
	{aggReasoningEnc, respDataReasoningEnc},
	{aggPhase, respDataPhase},
}

// responsesStateOf 状态值宽容归一（*ai.ProtocolState | 反序列化 map 形态）。
func responsesStateOf(v any) *ai.ProtocolState {
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

// resolveResponsesData 按语义摘要校验后取回协议回放数据（fail-closed：版本不符、
// 摘要缺失或消息语义已变即降级）。Go 无 Java 的 legacy metadata 回退路径——
// 旧消息形态在 Go 不存在，状态只从 ProtocolStates 读取。
func resolveResponsesData(m *ai.AssistantMessage) map[string]any {
	if m == nil || m.ProtocolStates == nil {
		return nil
	}
	v, ok := m.ProtocolStates[responsesProtocolID]
	if !ok {
		return nil
	}
	state := responsesStateOf(v)
	if state == nil || state.Version != responsesProtocolVersion || state.SemanticHash == "" ||
		state.SemanticHash != ai.SemanticHash(m) {
		return nil
	}
	return state.Data
}

// responsesStateFromAggregation 从聚合工作台创建协议状态（只复制 Responses 自己的键，
// 不把应用元数据误纳入；全空 → nil）。
func responsesStateFromAggregation(meta map[string]any) *ai.ProtocolState {
	if len(meta) == 0 {
		return nil
	}
	data := map[string]any{}
	for _, pair := range responsesAggKeyPairs {
		if v, ok := meta[pair[0]]; ok {
			data[pair[1]] = v
		}
	}
	if len(data) == 0 {
		return nil
	}
	return &ai.ProtocolState{Version: responsesProtocolVersion, Data: data}
}

// removeResponsesAggregationKeys 清理聚合工作台的协议键（提升后调用，
// 避免重复提升 / 泄漏）。
func removeResponsesAggregationKeys(meta map[string]any) {
	for _, pair := range responsesAggKeyPairs {
		delete(meta, pair[0])
	}
}

// attachResponsesReplayState 把流式聚合的回放数据提交到终态协议状态。
func attachResponsesReplayState(acc *ai.Accumulator) {
	if acc == nil {
		return
	}
	meta := acc.AggregationMeta()
	if state := responsesStateFromAggregation(meta); state != nil {
		acc.PutTerminalProtocolState(responsesProtocolID, state)
		removeResponsesAggregationKeys(meta)
	}
}

// ---- 请求侧入口 ----

// BuildRequestJSON 组装 Responses 请求体（对标 OpenaiResponsesRequestBuilder.build）。
func (d *ResponsesDialect) BuildRequestJSON(cfg *ai.Config, opts *ai.ChatOptions, msgs []*ai.Message, isStream bool) ([]byte, string, error) {
	return buildResponsesRequest(cfg, opts, msgs, isStream)
}

// PrepareOutputSchemaInstruction 输出 schema 指令（Java AbstractChatDialect 默认模板，
// 与 Chat Completions 方言共享同一 OpenAI 家族文案）。
func (d *ResponsesDialect) PrepareOutputSchemaInstruction(schema string, sb *strings.Builder) {
	sb.WriteString(fmt.Sprintf(schemaInstructionT, schema))
}

// PrepareOutputFormatOptions 结构化输出走 text.format（而非 response_format）：
// schema 可解析为 object 时升级 json_schema（strict 模式补全 additionalProperties/
// required），否则退回 json_object 保留「输出为合法 JSON」的协议保证。
func (d *ResponsesDialect) PrepareOutputFormatOptions(opts *ai.ChatOptions) {
	if opts == nil || opts.OutputSchema == "" {
		return
	}
	format := map[string]any{}
	var schemaNode map[string]any
	if err := json.Unmarshal([]byte(opts.OutputSchema), &schemaNode); err != nil || schemaNode == nil {
		format["type"] = "json_object"
	} else {
		applyResponsesStrictSchema(schemaNode)
		format["type"] = "json_schema"
		format["name"] = "output_schema"
		format["schema"] = schemaNode
		format["strict"] = true
	}
	opts.OptionSet("text", map[string]any{"format": format})
}

// applyResponsesStrictSchema 递归为 strict 模式补 additionalProperties=false 与
// required=全部属性键（统一重建修复 partial required）；仅覆盖 object.properties
// 与 array.items 内的子 schema（组合子内不补全——官方 strict 对其支持本身受限）。
func applyResponsesStrictSchema(node map[string]any) {
	if node == nil {
		return
	}
	if getStr(node, "type") == "object" {
		node["additionalProperties"] = false
		if props, ok := node["properties"].(map[string]any); ok && props != nil {
			required := make([]any, 0, len(props))
			for k := range props {
				required = append(required, k)
			}
			node["required"] = required
			for _, v := range props {
				if sub, ok := v.(map[string]any); ok {
					applyResponsesStrictSchema(sub)
				}
			}
		}
	}
	if items, ok := node["items"].(map[string]any); ok && items != nil {
		applyResponsesStrictSchema(items)
	}
}

// ---- 聚合出口（流式工具调用轮）----

// BuildAssistantToolCallMessage 流式聚合 → assistant 会话消息。与 Java 的差异：
// 不经线协议 JSON 往返，直接取聚合快照（文本/思考/媒体事件聚合优先、终态载体兜底、
// 工具调用以终态载体为先）；回放状态优先终态协议袋（完成帧已提升），
// 否则从聚合工作台即时提升，随出站消息克隆并重绑语义摘要。
func (d *ResponsesDialect) BuildAssistantToolCallMessage(acc *ai.Accumulator, builders map[string]*ai.ToolCallBuilder) *ai.Message {
	if acc == nil {
		return nil
	}
	snap := acc.Message()
	if snap.Text == "" && snap.Thinking == "" && len(snap.ToolCalls) == 0 && len(snap.Blocks) == 0 {
		return nil
	}
	calls := make([]ai.ToolCall, 0, len(snap.ToolCalls))
	for _, c := range snap.ToolCalls {
		calls = append(calls, ai.ToolCall{
			Index: c.Index,
			ID:    c.ID,
			Name:  c.Name,
			// 出站兜底净化：截断/损坏的 arguments 禁止原样入历史；参数交核心统一补解析
			ArgumentsStr: SanitizeArguments(c.ArgumentsStr, c.Name),
		})
	}
	am := &ai.AssistantMessage{
		Text:      snap.Text,
		Thinking:  snap.Thinking,
		ToolCalls: calls,
		Blocks:    snap.Blocks,
	}

	var state *ai.ProtocolState
	if v := acc.TerminalProtocolState(responsesProtocolID); v != nil {
		if s := responsesStateOf(v); s != nil {
			state = s
		}
	}
	if state == nil {
		state = responsesStateFromAggregation(acc.AggregationMeta())
		removeResponsesAggregationKeys(acc.AggregationMeta())
	}
	if state != nil {
		dialectkit.AdoptProtocolStates(am, map[string]any{responsesProtocolID: state})
	}
	return &ai.Message{Role: ai.RoleAssistant, Content: am}
}

// BuildAssistantMessageByToolMessages returnDirect：工具结果合成最终 assistant 消息
// （Java 未覆写，沿用 Chat Completions 家族实现）。
func (d *ResponsesDialect) BuildAssistantMessageByToolMessages(msg *ai.AssistantMessage, toolMsgs []*ai.Message) *ai.AssistantMessage {
	return (&Dialect{}).BuildAssistantMessageByToolMessages(msg, toolMsgs)
}

// ParseAssistantMessage 恒空：Responses 的 thinking/text 分帧由解析器直接产出事件，
// 从不经 Chat Completions 形态的消息节点状态机（Go 聚合出口直接构造消息，无此入口）。
func (d *ResponsesDialect) ParseAssistantMessage(*ai.Accumulator, map[string]any) []*ai.AssistantMessage {
	return nil
}
