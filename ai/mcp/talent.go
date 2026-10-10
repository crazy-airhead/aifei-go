// MCP×Talent 桥（对标 McpTalentClient/McpTalentServer，06 §8）：把一个 MCP
// 服务端的 talent 协议端点包装为本地 ai.Talent（NewTalent），或反向把本地
// ai.Talent 导出为 MCP 服务端资源 + 四个 hide 管理工具（RegisterTalent）。
//
// 协议端点（与 Java 对齐）：
//   - 资源 talent://metadataMcp：元信息 JSON（name/description）
//   - 工具 isSupportedMcp/onAttachMcp/getInstructionMcp/getToolsMcp：入参
//     promptJson（Prompt 的跨进程线形态），meta 标记 hide（不向最终 LLM 暴露）
//
// Go 的 Talent 接口无 error 返回位（对照 Java 抛异常）：远程失败的降级策略为
// IsSupported fail-closed（false）、Instruction/Tools 空值、OnAttach 记日志。
package aimcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/log"
	"github.com/crazy-airhead/aifei-go/mcp"
)

// TalentURI 元数据资源地址（Java talent://metadataMcp）。
const TalentURI = "talent://metadataMcp"

// 管理工具名（Java 同名）。
const (
	toolIsSupported    = "isSupportedMcp"
	toolOnAttach       = "onAttachMcp"
	toolGetInstruction = "getInstructionMcp"
	toolGetTools       = "getToolsMcp"
)

// TalentMetadata MCP 交换的 talent 元信息（Java TalentMetadata 的 Go 契约子集：
// Talent SPI 未含 Metadata() 访问器，跨进程只需 name/description）。
type TalentMetadata struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// promptWire Prompt 的跨进程线形态（Java 以带类名序列化往返；Go 用显式 DTO，
// Message 自带 JSON 往返契约）。
type promptWire struct {
	Messages []*ai.Message  `json:"messages"`
	Attrs    map[string]any `json:"attrs,omitempty"`
}

func marshalPrompt(p *ai.Prompt) (string, error) {
	if p == nil {
		return "{}", nil
	}
	b, err := json.Marshal(promptWire{Messages: p.Messages(), Attrs: p.Attrs()})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func unmarshalPrompt(s string) (*ai.Prompt, error) {
	var w promptWire
	if err := json.Unmarshal([]byte(s), &w); err != nil {
		return nil, err
	}
	p := ai.NewPrompt(w.Messages...)
	for k, v := range w.Attrs {
		p.SetAttr(k, v)
	}
	return p, nil
}

// promptJSONSchema 管理工具共用入参 schema。
const promptJSONSchema = `{"type":"object","properties":{"promptJson":{"type":"string","description":"ai.Prompt 的跨进程 JSON 形态"}},"required":["promptJson"]}`

// ==================== 客户端（McpTalentClient 对应物） ====================

// talentClient MCP 服务端 talent 端点 → 本地 ai.Talent。
type talentClient struct {
	provider *Provider
	metadata TalentMetadata
	timeout  time.Duration
	enabled  bool
}

// TalentOption NewTalent 选项。
type TalentOption func(*talentClient)

// WithTalentTimeout 远程调用单次超时（默认 30s）。
func WithTalentTimeout(d time.Duration) TalentOption {
	return func(t *talentClient) { t.timeout = d }
}

// NewTalent 把 MCP 服务端的 talent 协议端点包装为本地 Talent（06 §8）：构造期
// 读取 talent://metadataMcp 资源取元信息（失败即返回错误——Java 构造期同样
// 直接抛出）。
func NewTalent(p *Provider, opts ...TalentOption) (ai.Talent, error) {
	if p == nil {
		return nil, fmt.Errorf("aimcp: provider is required")
	}
	t := &talentClient{provider: p, timeout: 30 * time.Second, enabled: true}
	for _, o := range opts {
		o(t)
	}

	ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
	defer cancel()
	result, err := p.client.ReadResource(ctx, TalentURI)
	if err != nil {
		return nil, fmt.Errorf("aimcp: read talent metadata failed: %w", err)
	}
	var text string
	if len(result.Contents) > 0 {
		if tc, ok := result.Contents[0].(*mcp.TextResourceContents); ok {
			text = tc.Text
		}
	}
	if text == "" {
		return nil, fmt.Errorf("aimcp: talent metadata resource is empty")
	}
	if err := json.Unmarshal([]byte(text), &t.metadata); err != nil {
		return nil, fmt.Errorf("aimcp: talent metadata parse: %w", err)
	}
	return t, nil
}

func (t *talentClient) IsEnabled() bool     { return t.enabled }
func (t *talentClient) SetEnabled(v bool)   { t.enabled = v }
func (t *talentClient) Name() string        { return t.metadata.Name }
func (t *talentClient) Description() string { return t.metadata.Description }

func (t *talentClient) IsSupported(p *ai.Prompt) bool {
	content, err := t.callMcp(toolIsSupported, p)
	if err != nil {
		log.Error("aimcp: isSupportedMcp failed (fail-closed): %v", err)
		return false
	}
	return content == "true"
}

func (t *talentClient) OnAttach(p *ai.Prompt) {
	if _, err := t.callMcp(toolOnAttach, p); err != nil {
		log.Error("aimcp: onAttachMcp failed: %v", err)
	}
}

func (t *talentClient) Instruction(p *ai.Prompt) string {
	content, err := t.callMcp(toolGetInstruction, p)
	if err != nil {
		log.Error("aimcp: getInstructionMcp failed: %v", err)
		return ""
	}
	return content
}

// Tools 远端 getToolsMcp 裁剪后的工具流（自动剔除 meta.hide 的管理工具）：
// 返回 null/解析失败 → 全量；空数组 → 无；名单 → 过滤。
func (t *talentClient) Tools(p *ai.Prompt) []ai.FunctionTool {
	stream := t.toolsStream()

	content, err := t.callMcp(toolGetTools, p)
	if err != nil {
		log.Error("aimcp: getToolsMcp failed (fallback to all tools): %v", err)
		return stream
	}
	if content == "" || content == "null" {
		return stream
	}
	var names []string
	if err := json.Unmarshal([]byte(content), &names); err != nil {
		log.Error("aimcp: getToolsMcp parse failed (fallback to all tools): %v", err)
		return stream
	}
	if len(names) == 0 {
		return nil
	}
	allow := make(map[string]struct{}, len(names))
	for _, n := range names {
		allow[n] = struct{}{}
	}
	var out []ai.FunctionTool
	for _, tool := range stream {
		if _, ok := allow[tool.Name()]; ok {
			out = append(out, tool)
		}
	}
	return out
}

// toolsStream 工具流（剔除 meta 标记 hide 的管理工具）。
func (t *talentClient) toolsStream() []ai.FunctionTool {
	tools := t.provider.Tools()
	out := make([]ai.FunctionTool, 0, len(tools))
	for _, tool := range tools {
		if m := tool.Meta(); m != nil {
			if _, hide := m["hide"]; hide {
				continue
			}
		}
		out = append(out, tool)
	}
	return out
}

// callMcp 调管理工具：promptJson 入参，取首个文本内容（Java getContent）。
func (t *talentClient) callMcp(name string, p *ai.Prompt) (string, error) {
	promptJSON, err := marshalPrompt(p)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
	defer cancel()
	result, err := executeWithRetry(t.provider.client, func(c *mcp.Client) (*mcp.CallToolResult, error) {
		return c.CallTool(ctx, name, map[string]any{"promptJson": promptJSON})
	})
	if err != nil {
		return "", err
	}
	if result.IsError {
		return "", fmt.Errorf("aimcp: tool %s returned error: %s", name, textOfContent(result.Content))
	}
	return textOfContent(result.Content), nil
}

// textOfContent 首个文本块内容（空串兜底）。
func textOfContent(contents []mcp.Content) string {
	for _, c := range contents {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

// ==================== 服务端（McpTalentServer 对应物） ====================

// ToolsNameProvider 名单裁剪缝（对标 McpTalentServer.getToolsName 的可覆写位）：
// nil = 所有工具；空 = 无匹配；非空 = 按名过滤。RegisterTalent 导出的
// getToolsMcp 工具读取此缝（Java 靠继承覆写，Go 用包变量）。
var toolsNameProvider func(p *ai.Prompt) []string

// SetToolsNameProvider 注册名单裁剪逻辑（getToolsMcp 工具消费；nil 还原全量）。
func SetToolsNameProvider(fn func(p *ai.Prompt) []string) { toolsNameProvider = fn }

// RegisterTalent 把本地 Talent 导出为 MCP 服务端协议端点：元数据资源 + 四个
// hide 管理工具（对标 McpTalentServer 的 @ResourceMapping/@ToolMapping 组合；
// Java 为抽象基类由用户继承，Go 命令式注册）。t 的 Tools() 不经此桥导出——
// 业务工具请另行 s.Tool 注册（Java getTools 为 final null 同语义）。
func RegisterTalent(s *mcp.Server, t ai.Talent) {
	meta := TalentMetadata{Name: t.Name(), Description: t.Description()}
	spec := s.Resource(TalentURI, "talent metadata", func(ctx *mcp.CallCtx) (any, error) {
		b, err := json.Marshal(meta)
		if err != nil {
			return nil, err
		}
		return string(b), nil
	})
	spec.Resource.Meta = map[string]any{"hide": 1}

	hideMeta := map[string]any{"hide": 1}

	s.Tool(toolIsSupported, promptJSONSchema, func(ctx *mcp.CallCtx, args map[string]any) (*mcp.CallToolResult, error) {
		p, err := unmarshalPrompt(argString(args, "promptJson"))
		if err != nil {
			return errorResult(err), nil
		}
		return textResult(fmt.Sprintf("%t", t.IsSupported(p))), nil
	}).Desc("禁止 llm 使用").Meta(hideMeta)

	s.Tool(toolGetInstruction, promptJSONSchema, func(ctx *mcp.CallCtx, args map[string]any) (*mcp.CallToolResult, error) {
		p, err := unmarshalPrompt(argString(args, "promptJson"))
		if err != nil {
			return errorResult(err), nil
		}
		return textResult(t.Instruction(p)), nil
	}).Desc("禁止 llm 使用").Meta(hideMeta)

	s.Tool(toolGetTools, promptJSONSchema, func(ctx *mcp.CallCtx, args map[string]any) (*mcp.CallToolResult, error) {
		p, err := unmarshalPrompt(argString(args, "promptJson"))
		if err != nil {
			return errorResult(err), nil
		}
		// nil = 全量（线形态 "null"）；空列表 = 无（"[]"）
		if toolsNameProvider == nil {
			return textResult("null"), nil
		}
		names := toolsNameProvider(p)
		b, err := json.Marshal(names)
		if err != nil {
			return errorResult(err), nil
		}
		return textResult(string(b)), nil
	}).Desc("禁止 llm 使用").Meta(hideMeta)

	s.Tool(toolOnAttach, promptJSONSchema, func(ctx *mcp.CallCtx, args map[string]any) (*mcp.CallToolResult, error) {
		p, err := unmarshalPrompt(argString(args, "promptJson"))
		if err != nil {
			return errorResult(err), nil
		}
		t.OnAttach(p)
		return textResult(""), nil
	}).Desc("禁止 llm 使用").Meta(hideMeta)
}

// textResult 单文本块结果。
func textResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent(s)}}
}

// errorResult 错误结果（IsError + 文本说明）。
func errorResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{mcp.NewTextContent(err.Error())}}
}

func argString(args map[string]any, key string) string {
	s, _ := args[key].(string)
	return s
}
