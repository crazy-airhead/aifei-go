package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
)

type toolContextCtxKey struct{}

// ContextWithToolContext 工具上下文注入 ctx（模型层组装；Handle 内取用）。
func ContextWithToolContext(ctx context.Context, tc map[string]any) context.Context {
	return context.WithValue(ctx, toolContextCtxKey{}, tc)
}

// ToolContextOf 从 ctx 取工具上下文（session id 等运行期标注；无则 nil）。
func ToolContextOf(ctx context.Context) map[string]any {
	tc, _ := ctx.Value(toolContextCtxKey{}).(map[string]any)
	return tc
}

// FunctionTool 函数工具（对标 Java FunctionTool）：名称/描述/schema/处理器。
type FunctionTool interface {
	Name() string
	Title() string
	Description() string
	Meta() map[string]any
	Strict() *bool
	ReturnDirect() bool
	InputSchema() string // JSON Schema（draft-07）字符串
	OutputSchema() string
	ReturnType() reflect.Type
	Handle(ctx context.Context, args map[string]any) (any, error)
	// Call 包装 Handle：panic recover、error → ToolResultErr（前缀 "Error: "）。
	Call(ctx context.Context, args map[string]any) *ToolResult
}

// ToolProvider 工具提供者（可注册到 options / mcp 桥 / talent）。
type ToolProvider interface {
	Tools() []FunctionTool
}

// ParamSpec 声明式参数描述。
type ParamSpec struct {
	Name     string
	Type     string // "string" | "integer" | "number" | "boolean" | "array" | "object"
	Required bool
	Desc     string
	Default  any
	Format   string // date / date-time / ...
	Enum     []any
	Items    map[string]any // type=array 时的 items 子 schema
	Schema   map[string]any // 完整节点直通（reflect 绑定的嵌套 object/array，优先于 Type 等散字段）
}

// ToolDesc 声明式工具构建器（主路径）。
type ToolDesc struct {
	name, title, desc string
	params            []ParamSpec
	inputSchemaStr    string
	outputSchemaStr   string
	strict            *bool
	returnDirect      bool
	meta              map[string]any
	resultConverter   func(any) (any, error)
	handler           func(ctx context.Context, args map[string]any) (any, error)
	returnType        reflect.Type
}

func NewTool(name string) *ToolDesc { return &ToolDesc{name: name} }

func (t *ToolDesc) Title(s string) *ToolDesc      { t.title = s; return t }
func (t *ToolDesc) Desc(s string) *ToolDesc       { t.desc = s; return t }
func (t *ToolDesc) Strict(b bool) *ToolDesc       { t.strict = &b; return t }
func (t *ToolDesc) ReturnDirect(b bool) *ToolDesc { t.returnDirect = b; return t }

func (t *ToolDesc) Param(p ParamSpec) *ToolDesc {
	t.params = append(t.params, p)
	return t
}

func (t *ToolDesc) StringParam(name, desc string, required bool) *ToolDesc {
	return t.Param(ParamSpec{Name: name, Type: "string", Desc: desc, Required: required})
}

func (t *ToolDesc) IntParam(name, desc string, required bool) *ToolDesc {
	return t.Param(ParamSpec{Name: name, Type: "integer", Desc: desc, Required: required})
}

func (t *ToolDesc) FloatParam(name, desc string, required bool) *ToolDesc {
	return t.Param(ParamSpec{Name: name, Type: "number", Desc: desc, Required: required})
}

func (t *ToolDesc) BoolParam(name, desc string, required bool) *ToolDesc {
	return t.Param(ParamSpec{Name: name, Type: "boolean", Desc: desc, Required: required})
}

func (t *ToolDesc) DateParam(name, desc string, required bool) *ToolDesc {
	return t.Param(ParamSpec{Name: name, Type: "string", Format: "date", Desc: desc, Required: required})
}

func (t *ToolDesc) DateTimeParam(name, desc string, required bool) *ToolDesc {
	return t.Param(ParamSpec{Name: name, Type: "string", Format: "date-time", Desc: desc, Required: required})
}

// InputSchemaJSON 直接给定 schema（跳过生成）。
func (t *ToolDesc) InputSchemaJSON(s string) *ToolDesc { t.inputSchemaStr = s; return t }

// OutputSchemaJSON 直接给定输出 schema。
func (t *ToolDesc) OutputSchemaJSON(s string) *ToolDesc { t.outputSchemaStr = s; return t }

func (t *ToolDesc) MetaSet(k string, v any) *ToolDesc {
	if t.meta == nil {
		t.meta = map[string]any{}
	}
	t.meta[k] = v
	return t
}

func (t *ToolDesc) ResultConverter(fn func(any) (any, error)) *ToolDesc {
	t.resultConverter = fn
	return t
}

func (t *ToolDesc) Handler(fn func(ctx context.Context, args map[string]any) (any, error)) *ToolDesc {
	t.handler = fn
	return t
}

// Build 生成 FunctionTool。
func (t *ToolDesc) Build() FunctionTool {
	c := *t
	return &toolImpl{ToolDesc: &c}
}

type toolImpl struct{ *ToolDesc }

// MetaSet meta 单项写入（talent 染色等外部注入缝；遮蔽 Builder 的同名链式方法，
// 签名须为 MetaSet(string, any) 方可命中鸭子接口）。
func (i *toolImpl) MetaSet(k string, v any) {
	if i.meta == nil {
		i.meta = map[string]any{}
	}
	i.meta[k] = v
}

func (i *toolImpl) Name() string             { return i.name }
func (i *toolImpl) Title() string            { return i.title }
func (i *toolImpl) Description() string      { return i.desc }
func (i *toolImpl) Meta() map[string]any     { return i.meta }
func (i *toolImpl) Strict() *bool            { return i.strict }
func (i *toolImpl) ReturnDirect() bool       { return i.returnDirect }
func (i *toolImpl) ReturnType() reflect.Type { return i.returnType }

func (i *toolImpl) InputSchema() string {
	if i.inputSchemaStr != "" {
		return i.inputSchemaStr
	}
	return buildInputSchema(i.params)
}

func (i *toolImpl) OutputSchema() string { return i.outputSchemaStr }

func (i *toolImpl) Handle(ctx context.Context, args map[string]any) (any, error) {
	if i.handler == nil {
		return nil, fmt.Errorf("ai: tool %q has no handler", i.name)
	}
	return i.handler(ctx, args)
}

func (i *toolImpl) Call(ctx context.Context, args map[string]any) *ToolResult {
	result, err := i.callDo(ctx, args)
	if err != nil {
		return ToolResultErr("Error: " + err.Error())
	}
	// 处理器直返富结果（MCP 桥等）：块/IsError/Meta 原样透传，不再字符串化。
	if tr, ok := result.(*ToolResult); ok {
		return tr
	}
	return ToolResultOf(resultConvert(result))
}

func (i *toolImpl) callDo(ctx context.Context, args map[string]any) (result any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	result, err = i.Handle(ctx, args)
	if err != nil {
		return nil, err
	}
	if i.resultConverter != nil {
		result, err = i.resultConverter(result)
	}
	return result, err
}

// resultConvert 工具结果字符串化：nil → 空；string 直出；其余 JSON 序列化
// （对标 ToolSchemaUtil.resultConvert）。
func resultConvert(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(data)
}

// ToolCall 模型发起的工具调用（对标 Java ToolCall）。
type ToolCall struct {
	Index        string         `json:"index,omitempty"` // 流式聚合键（"idx:<n>" 形式，index 优先于 id）
	ID           string         `json:"id,omitempty"`
	Name         string         `json:"name,omitempty"`
	ArgumentsStr string         `json:"arguments,omitempty"` // 原始参数 JSON 字符串（流式期间可能截断）
	Arguments    map[string]any `json:"argumentsMap,omitempty"`
}

// ToolResult 工具执行结果（= Contents + success/error）。
type ToolResult struct {
	Text    string         `json:"text,omitempty"`
	Blocks  []ContentBlock `json:"blocks,omitempty"`
	IsError bool           `json:"isError,omitempty"`
	Meta    map[string]any `json:"metas,omitempty"`
}

func ToolResultOf(s string) *ToolResult  { return &ToolResult{Text: s} }
func ToolResultErr(s string) *ToolResult { return &ToolResult{Text: s, IsError: true} }

// ContentText 文本形态的结果（Text 或首文本块）。
func (r *ToolResult) ContentText() string {
	if r.Text != "" {
		return r.Text
	}
	for _, b := range r.Blocks {
		if tb, ok := b.(TextBlock); ok {
			return tb.Text
		}
	}
	return ""
}

func (r *ToolResult) Clone() *ToolResult {
	n := *r
	n.Blocks = cloneBlocks(r.Blocks)
	n.Meta = copyMap(r.Meta)
	return &n
}

func (r *ToolResult) UnmarshalJSON(data []byte) error {
	var raw struct {
		Text    string          `json:"text"`
		Blocks  json.RawMessage `json:"blocks"`
		IsError bool            `json:"isError"`
		Meta    map[string]any  `json:"metas"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	r.Text, r.IsError, r.Meta = raw.Text, raw.IsError, raw.Meta
	if len(raw.Blocks) > 0 && string(raw.Blocks) != "null" {
		blocks, err := unmarshalBlocks(raw.Blocks)
		if err != nil {
			return err
		}
		r.Blocks = blocks
	}
	return nil
}

// ToolMessage 工具消息（对标 Java ToolMessage：name/toolCallId/returnDirect 为一等字段，
// 非 metadata；线格式由方言展开为 role=tool + name + tool_call_id + content）。
type ToolMessage struct {
	Result       *ToolResult `json:"result"`
	Name         string      `json:"name,omitempty"`
	CallID       string      `json:"toolCallId,omitempty"`
	ReturnDirect bool        `json:"returnDirect,omitempty"`
}

func (t *ToolMessage) ResultText() string {
	if t.Result == nil {
		return ""
	}
	return t.Result.ContentText()
}

// compacted 返回结果块降级后的浅拷贝（会话持久化路径）。
func (t *ToolMessage) compacted() *ToolMessage {
	if t.Result == nil || len(t.Result.Blocks) == 0 {
		return t
	}
	n := *t
	r := *t.Result
	r.Blocks = compactBlocks(t.Result.Blocks)
	n.Result = &r
	return &n
}
