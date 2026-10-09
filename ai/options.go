package ai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
)

// CacheControl 前缀缓存控制（OpenAI 兼容 prompt_cache_key 等）。
type CacheControl struct {
	PromptCacheKey string
}

// BaseOptions 请求选项基座（chat/embedding/rerank 共用面）。
// Raw 为 optionSet 逃生舱：厂商任意参数原样透传到请求 JSON 顶层。
type BaseOptions struct {
	AutoToolCall *bool
	Tools        []FunctionTool
	ToolChoice   string
	ToolContext  map[string]any
	Raw          map[string]any
	CacheControl CacheControl

	// HTTPCustomize 每请求前回调（对标 httpCustomizeAdd），可改 header/URL 细节。
	HTTPCustomize func(*http.Request)
}

// ChatOptions 聊天请求选项：BaseOptions 之上叠加请求描述层字段
// （对标 Java ChatOptions 的 role/instruction/systemPrompt/outputSchema/session）。
type ChatOptions struct {
	BaseOptions

	Role         string // 「## 你的角色」段（SystemPrompt 缺省时结构化组装用）
	Instruction  string // 「## 执行指令」段
	SystemPrompt string // 完整系统提示词（优先于 Role/Instruction 结构化组装）
	OutputSchema string // 结构化输出 schema（经 OutputSchemaOf[T] 反推或直接给）

	Session      Session       // 仅 RequestDesc 层注入（用户不经 Options 设置）
	Talents      []Talent      // 能力包（prepare 阶段统一激活）
	Interceptors []Interceptor // 准备期/调用期/工具期拦截器（按序）
}

// Tool 按名查找已注册工具（autoToolCall 执行用；未注册返回 nil）。
func (o *ChatOptions) Tool(name string) FunctionTool {
	for _, t := range o.Tools {
		if t != nil && t.Name() == name {
			return t
		}
	}
	return nil
}

// OptionGet 读 Raw 选项。
func (o *ChatOptions) OptionGet(k string) any {
	if o.Raw == nil {
		return nil
	}
	return o.Raw[k]
}

// OptionSet 写 Raw 选项（同名覆盖）。
func (o *ChatOptions) OptionSet(k string, v any) *ChatOptions {
	if o.Raw == nil {
		o.Raw = map[string]any{}
	}
	o.Raw[k] = v
	return o
}

// IsAutoToolCall 是否自动执行工具调用（nil = 关闭；上层可用 Config.DefaultAutoToolCall 预置）。
func (o *ChatOptions) IsAutoToolCall() bool {
	return o.AutoToolCall != nil && *o.AutoToolCall
}

// ToolAdd 追加工具提供者的全部工具。
func (o *ChatOptions) ToolAdd(p ToolProvider) *ChatOptions {
	if p != nil {
		o.Tools = append(o.Tools, p.Tools()...)
	}
	return o
}

// Set 任意 Raw 选项（OptionSet 别名，链式惯用名）。
func (o *ChatOptions) Set(k string, v any) *ChatOptions { return o.OptionSet(k, v) }

func (o *ChatOptions) Temperature(v float64) *ChatOptions { return o.OptionSet("temperature", v) }
func (o *ChatOptions) TopP(v float64) *ChatOptions        { return o.OptionSet("top_p", v) }
func (o *ChatOptions) MaxTokens(v int64) *ChatOptions     { return o.OptionSet("max_tokens", v) }

// ReasoningEffort 统一推理水平：low|medium|high|max（方言按模型族映射为
// reasoning_effort / thinking 等字段，P1-b 方言矩阵展开）。
func (o *ChatOptions) ReasoningEffort(s string) *ChatOptions {
	return o.OptionSet("reasoning_effort", s)
}

func (o *ChatOptions) Thinking(b bool) *ChatOptions { return o.OptionSet("thinking", b) }

func (o *ChatOptions) SetCacheControl(cc CacheControl) *ChatOptions {
	o.CacheControl = cc
	return o
}

// RawJSON Raw 的稳定序列化（测试与日志用；键排序保证字节稳定）。
func (o *ChatOptions) RawJSON() string {
	if len(o.Raw) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(o.Raw))
	for k := range o.Raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	buf := []byte("{")
	for i, k := range keys {
		if i > 0 {
			buf = append(buf, ',')
		}
		kb, _ := json.Marshal(k)
		vb, err := json.Marshal(o.Raw[k])
		if err != nil {
			vb = []byte(`null`)
		}
		buf = append(buf, kb...)
		buf = append(buf, ':')
		buf = append(buf, vb...)
	}
	return string(append(buf, '}'))
}

// DescriptionAndMeta 工具描述 + 元信息前缀（对标 Java FunctionTool.descriptionAndMeta：
// meta 以 "[k] "（bool true）/ "[k:v] " 前缀拼在描述前）。
func DescriptionAndMeta(t FunctionTool) string {
	meta := t.Meta()
	if len(meta) == 0 {
		return t.Description()
	}
	keys := make([]string, 0, len(meta))
	for k := range meta {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb []byte
	for _, k := range keys {
		v := meta[k]
		if b, ok := v.(bool); ok {
			if b {
				sb = append(sb, '[')
				sb = append(sb, k...)
				sb = append(sb, "] "...)
			}
			continue
		}
		sb = append(sb, '[')
		sb = append(sb, k...)
		sb = append(sb, ':')
		sb = append(sb, fmtAny(v)...)
		sb = append(sb, "] "...)
	}
	sb = append(sb, t.Description()...)
	return string(sb)
}

func fmtAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
