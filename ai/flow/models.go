// models.go 模型组件（对标 components/models）：ChatModelCom（chatConfig
// 构建 + D8 附件缓存 + 流式句柄）、EmbeddingModelCom（embeddingConfig 构建）、
// GenerateModelCom（P5 桩）。
package aiflow

import (
	"encoding/json"
	"errors"
	"fmt"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/embedding"
	"github.com/crazy-airhead/aifei-go/flow"
)

// 模型组件私有 meta 键。
const (
	MetaSystemPrompt     = "systemPrompt"
	MetaStream           = "stream"
	MetaChatConfig       = "chatConfig"
	MetaToolProviders    = "toolProviders"
	MetaMcpServers       = "mcpServers"
	MetaChatSession      = "chatSession"
	MetaEmbeddingConfig  = "embeddingConfig"
	MetaGenerateConfig   = "genConfig"
	MetaDocumentSources  = "documentSources"
	MetaSplitPipeline    = "splitPipeline"
	MetaRedisConfig      = "redisConfig"
)

// 节点附件缓存键（Java node.attachment 公有字段的键控对应物——D8 方案 A）。
const (
	attachChatModel      = "aiflow.chatModel"
	attachEmbeddingModel = "aiflow.embeddingModel"
)

// chatModelCache ChatModel 节点预热产物：模型 + 注册表解析出的默认工具
//（Java builder.defaultToolAdd 的缓存位；Go 工具经每请求 Options 注入）。
type chatModelCache struct {
	model *ai.ChatModel
	tools []ai.FunctionTool
}

// ChatModel 聊天模型组件：从 chatConfig meta 构建（或复用节点附件缓存的）
// 模型，输入 string/Message/Prompt，非流式输出 *ai.Response、流式
//（stream:true）输出 *StreamHandle；模型转入上下文属性 chatModel。
type ChatModel struct{ ComponentBase }

// NewChatModel 构建聊天模型组件。
func NewChatModel() *ChatModel { return &ChatModel{} }

// Name 注册名。
func (c *ChatModel) Name() string { return "ChatModel" }

// Title 同注册名。
func (c *ChatModel) Title() string { return "ChatModel" }

// Description 组件描述。
func (c *ChatModel) Description() string { return "聊天模型组件" }

// Run 节点事件包装。
func (c *ChatModel) Run(ctx flow.Context, node *flow.Node) error {
	return nodeEvents(ctx, node, c.doRun)
}

func (c *ChatModel) doRun(ctx flow.Context, node *flow.Node) error {
	// 构建聊天模型（预热后缓存于节点附件——D8；二次执行不重建）
	var cache *chatModelCache
	if v := node.Attachment(attachChatModel); v != nil {
		cache, _ = v.(*chatModelCache)
	}
	if cache == nil {
		cc, err := buildChatModelCache(node)
		if err != nil {
			return err
		}
		cache = cc
		node.SetAttachment(attachChatModel, cache)
	}

	// 模型转入上下文属性
	c.SetProperty(ctx, PropChatModel, cache.model)

	// 获取数据
	data := c.Input(ctx, node)
	if data == nil {
		return nil // Java data null-skip
	}

	// 构建会话（可在发起流程时传递；键可由 chatSession meta 覆写）
	sessKey := CtxChatSession
	if node.HasMeta(MetaChatSession) {
		sessKey = node.MetaAsString(MetaChatSession)
	}
	ctx.PutIfAbsent(sessKey, ai.NewMemorySession())
	sess, _ := ctx.Get(sessKey).(ai.Session)

	isStream := node.MetaAsString(MetaStream) == "true"
	systemPrompt := node.MetaAsString(MetaSystemPrompt)
	if systemPrompt != "" {
		sp, err := evalTmpl(systemPrompt, ctx.Vars())
		if err != nil {
			return err
		}
		systemPrompt = sp
	}

	prompt, err := promptOf(data)
	if err != nil {
		return err
	}

	req := cache.model.Prompt(prompt).Session(sess)
	if systemPrompt != "" {
		req = req.SystemPrompt(systemPrompt)
	}
	if len(cache.tools) > 0 {
		tools := cache.tools
		req = req.Options(func(o *ai.ChatOptions) {
			o.Tools = append(o.Tools, tools...)
		})
	}

	if isStream {
		c.SetOutput(ctx, node, NewStreamHandle(req))
		return nil
	}
	resp, err := req.Call()
	if err != nil {
		return err
	}
	c.SetOutput(ctx, node, resp)
	return nil
}

// buildChatModelCache 从 meta 构建（chatConfig 必填；toolProviders 走注册表；
// mcpServers 为 P3 之后的缝，当前配置即显式报错）。
func buildChatModelCache(node *flow.Node) (*chatModelCache, error) {
	cfg, err := configFromMeta(node, MetaChatConfig)
	if err != nil {
		return nil, err
	}
	model, err := ai.NewChatModel(cfg)
	if err != nil {
		return nil, err
	}

	var tools []ai.FunctionTool
	for _, name := range metaStringSlice(node, MetaToolProviders) {
		tools = append(tools, ToolProvider(name)...)
	}

	if node.Meta(MetaMcpServers) != nil {
		return nil, errors.New("aiflow: ChatModel: mcpServers not supported yet (requires P3, ai/mcp)")
	}

	return &chatModelCache{model: model, tools: tools}, nil
}

// configFromMeta meta map → ai.Config（Java ONode.ofBean(...).toBean(ChatConfig)
// 的 JSON 往返对应物；字段名大小写不敏感匹配——apiUrl→APIURL 等）。
func configFromMeta(node *flow.Node, key string) (ai.Config, error) {
	raw := node.Meta(key)
	if raw == nil {
		return ai.Config{}, fmt.Errorf("aiflow: the meta '%s' is required", key)
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return ai.Config{}, fmt.Errorf("aiflow: meta '%s': %w", key, err)
	}
	var cfg ai.Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return ai.Config{}, fmt.Errorf("aiflow: meta '%s': %w", key, err)
	}
	return cfg, nil
}

// promptOf 输入形态适配：string/Message/Prompt，其余 IAE（Java 同款面）。
func promptOf(data any) (*ai.Prompt, error) {
	switch d := data.(type) {
	case string:
		return ai.PromptOf(d), nil
	case *ai.Message:
		return ai.NewPrompt(d), nil
	case *ai.Prompt:
		return d, nil
	default:
		return nil, fmt.Errorf("aiflow: ChatModel unsupported data type: %T", data)
	}
}

// EmbeddingModel 嵌入模型组件：从 embeddingConfig meta 构建（或复用节点附件
// 缓存的）模型，转入上下文属性 embeddingModel。
type EmbeddingModel struct{ ComponentBase }

// NewEmbeddingModel 构建嵌入模型组件。
func NewEmbeddingModel() *EmbeddingModel { return &EmbeddingModel{} }

// Name 注册名。
func (c *EmbeddingModel) Name() string { return "EmbeddingModel" }

// Title 同注册名。
func (c *EmbeddingModel) Title() string { return "EmbeddingModel" }

// Description 组件描述。
func (c *EmbeddingModel) Description() string { return "嵌入模型组件" }

// Run 节点事件包装。
func (c *EmbeddingModel) Run(ctx flow.Context, node *flow.Node) error {
	return nodeEvents(ctx, node, c.doRun)
}

func (c *EmbeddingModel) doRun(ctx flow.Context, node *flow.Node) error {
	var model *embedding.Model
	if v := node.Attachment(attachEmbeddingModel); v != nil {
		model, _ = v.(*embedding.Model)
	}
	if model == nil {
		cfg, err := configFromMeta(node, MetaEmbeddingConfig)
		if err != nil {
			return err
		}
		m, err := embedding.New(cfg)
		if err != nil {
			return err
		}
		model = m
		// Java 版读附件却不写回（注释言「预热后会缓存」但实现漏写，每次
		// 重建）；Go 补上写回，与 InMemoryRepositoryCom 的缓存意图一致。
		node.SetAttachment(attachEmbeddingModel, model)
	}
	c.SetProperty(ctx, PropEmbeddingModel, model)
	return nil
}

// GenerateModel 生成模型组件（图像/多模态生成；P5 generate 家族迁移后实现，
// 当前显式报错——注册占位保持 YAML 兼容）。
type GenerateModel struct{ ComponentBase }

// NewGenerateModel 构建生成模型组件。
func NewGenerateModel() *GenerateModel { return &GenerateModel{} }

// Name 注册名。
func (c *GenerateModel) Name() string { return "GenerateModel" }

// Title 同注册名。
func (c *GenerateModel) Title() string { return "GenerateModel" }

// Description 组件描述。
func (c *GenerateModel) Description() string { return "生成模型组件" }

// Run 节点事件包装。
func (c *GenerateModel) Run(ctx flow.Context, node *flow.Node) error {
	return nodeEvents(ctx, node, c.doRun)
}

func (c *GenerateModel) doRun(flow.Context, *flow.Node) error {
	return errors.New("aiflow: GenerateModel requires P5 (generate model not yet migrated)")
}

// metaStringSlice meta 列表值 → []string（单字符串值容错包一层）。
func metaStringSlice(node *flow.Node, key string) []string {
	v := node.Meta(key)
	if v == nil {
		return nil
	}
	if s, ok := v.(string); ok {
		return []string{s}
	}
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, e := range list {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
