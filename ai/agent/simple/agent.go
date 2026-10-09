package simple

import (
	"fmt"

	"github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/flow"
)

// Handler 自定义处理器兜底（无 ChatModel 时走此路径；对标 AgentHandler）。
type Handler func(prompt *ai.Prompt, session agent.Session) (*ai.AssistantMessage, error)

// Config SimpleAgent 配置（对标 SimpleAgentConfig；缺省值见 NewConfig）。
type Config struct {
	Name              string
	Role              string
	Instruction       string
	Profile           *agent.Profile
	SystemPrompt      *SystemPrompt
	ChatModel         *ai.ChatModel
	DefaultOptions    *Options
	Handler           Handler
	MaxRetries        int
	RetryDelayMs      int64
	SessionWindowSize int
	OutputKey         string
	OutputSchema      string
}

// NewConfig 缺省配置（name/simple_agent、默认系统提示词、3 次重试 / 1000ms、
// 历史窗口 5——Java 同款缺省）。
func NewConfig() *Config {
	return &Config{
		Name:              "simple_agent",
		SystemPrompt:      DefaultSystemPrompt(),
		DefaultOptions:    &Options{},
		MaxRetries:        3,
		RetryDelayMs:      1000,
		SessionWindowSize: 5,
	}
}

// TraceKey 轨迹存放键（"__"+name 惰性缓存——Go 直接计算）。
func (c *Config) TraceKey() string { return "__" + c.Name }

// Locale 协议指令语言（Java 固定 Locale.CHINESE）。
func (c *Config) Locale() string { return "zh" }

// SetRetryConfig 重试参数（max>=1、delayMs>=500——Java 钳制同款）。
func (c *Config) SetRetryConfig(maxRetries int, retryDelayMs int64) *Config {
	if maxRetries < 1 {
		maxRetries = 1
	}
	if retryDelayMs < 500 {
		retryDelayMs = 500
	}
	c.MaxRetries, c.RetryDelayMs = maxRetries, retryDelayMs
	return c
}

// SetSessionWindowSize 历史窗口（<=0 关闭）。
func (c *Config) SetSessionWindowSize(n int) *Config {
	if n < 0 {
		n = 0
	}
	c.SessionWindowSize = n
	return c
}

// SystemPromptFor 渲染系统提示词（#{key} 以 ctx.Vars() 解析——对标
// getSystemPromptFor + SnelUtil.render）。
func (c *Config) SystemPromptFor(trace *Trace, ctx flow.Context) string {
	raw := c.SystemPrompt.GetSystemPrompt(trace)
	if ctx == nil || raw == "" {
		return raw
	}
	return agent.RenderVars(raw, ctx)
}

// Agent 简单智能体（对标 SimpleAgent）：单次直接响应；指令增强、历史窗口、
// 仅传输错误重试、JSON 结构化输出、流式转发。
type Agent struct {
	*agent.AgentBase
	cfg *Config
}

// New 由配置构建（一般经 Builder）。
func New(cfg *Config) (*Agent, error) {
	if cfg == nil {
		cfg = NewConfig()
	}
	if cfg.Handler == nil && cfg.ChatModel == nil {
		return nil, errAgentRequiresModelOrHandler
	}
	if cfg.Name == "" {
		cfg.Name = "simple_agent"
	}
	if cfg.SystemPrompt == nil {
		cfg.SystemPrompt = DefaultSystemPrompt()
	}
	if cfg.DefaultOptions == nil {
		cfg.DefaultOptions = &Options{}
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}
	a := &Agent{cfg: cfg}
	a.AgentBase = agent.NewAgentBase(a)
	return a, nil
}

// Config 配置（含 Builder 构建后的最终值）。
func (a *Agent) Config() *Config { return a.cfg }

func (a *Agent) Name() string { return a.cfg.Name }

// Title flow 节点标题（NamedTaskComponent 缺省 = name）。
func (a *Agent) Title() string { return a.cfg.Name }

// Role 角色职责（缺省回落 name——Java SimpleAgent.role 同款）。
func (a *Agent) Role() string {
	if a.cfg.Role == "" {
		return a.cfg.Name
	}
	return a.cfg.Role
}

// RoleFor 动态角色（#{key} 渲染）。
func (a *Agent) RoleFor(ctx flow.Context) string {
	return agent.RenderVars(a.Role(), ctx)
}

// Profile 档案（惰性缺省）。
func (a *Agent) Profile() *agent.Profile {
	if a.cfg.Profile == nil {
		a.cfg.Profile = agent.NewProfile()
	}
	return a.cfg.Profile
}

// Prompt 创建请求构建器：接受 nil（恢复执行）/ string / *ai.Prompt /
// []*ai.Message；其余类型按字符串化单条 user 消息处理。
func (a *Agent) Prompt(p any) *Request {
	var prompt *ai.Prompt
	switch v := p.(type) {
	case nil:
	case string:
		prompt = ai.PromptOf(v)
	case *ai.Prompt:
		prompt = v
	case []*ai.Message:
		prompt = ai.NewPrompt(v...)
	default:
		prompt = ai.PromptOf(fmt.Sprint(v))
	}
	return &Request{ag: a, prompt: prompt, options: a.cfg.DefaultOptions.Copy()}
}

// Call 任务执行（Agent 接口；options 走默认副本）。
func (a *Agent) Call(prompt *ai.Prompt, session agent.Session) (*ai.AssistantMessage, error) {
	return a.call(prompt, session, nil)
}

// Builder 流式构建器（对标 SimpleAgent.Builder）。
type Builder struct {
	cfg *Config
}

// NewBuilder 空构建器（Handler 或 Model 二选一，Build 时校验）。
func NewBuilder() *Builder { return &Builder{cfg: NewConfig()} }

// NewModelBuilder 以 ChatModel 起步（对标 SimpleAgent.of(chatModel)）。
func NewModelBuilder(model *ai.ChatModel) *Builder {
	b := NewBuilder()
	b.cfg.ChatModel = model
	return b
}

func (b *Builder) Name(name string) *Builder              { b.cfg.Name = name; return b }
func (b *Builder) Role(role string) *Builder              { b.cfg.Role = role; return b }
func (b *Builder) Profile(p *agent.Profile) *Builder      { b.cfg.Profile = p; return b }
func (b *Builder) Instruction(s string) *Builder          { b.cfg.Instruction = s; return b }
func (b *Builder) SystemPrompt(sp *SystemPrompt) *Builder { b.cfg.SystemPrompt = sp; return b }
func (b *Builder) Model(m *ai.ChatModel) *Builder         { b.cfg.ChatModel = m; return b }
func (b *Builder) Handler(h Handler) *Builder             { b.cfg.Handler = h; return b }

// InstructionFn 动态指令（每轮以 Trace 求值——对标 Builder.instruction(Function)）。
func (b *Builder) InstructionFn(fn func(trace *Trace) string) *Builder {
	b.cfg.SystemPrompt = NewPromptBuilder().Instruction(fn).Build()
	return b
}

// Retry 重试参数（max>=1、delayMs>=500）。
func (b *Builder) Retry(maxRetries int, retryDelayMs int64) *Builder {
	b.cfg.SetRetryConfig(maxRetries, retryDelayMs)
	return b
}

// SessionWindow 历史窗口大小（<=0 关闭）。
func (b *Builder) SessionWindow(n int) *Builder {
	b.cfg.SetSessionWindowSize(n)
	return b
}

func (b *Builder) OutputKey(key string) *Builder  { b.cfg.OutputKey = key; return b }
func (b *Builder) OutputSchema(s string) *Builder { b.cfg.OutputSchema = s; return b }

// OutputSchemaOf[T] 由类型反推输出 schema（对标 Builder.outputSchema(Type)）。
func OutputSchemaOf[T any]() string { return ai.OutputSchemaOf[T]() }

// ModelOptions 调整默认选项（工具/拦截器/Raw 参数等）。
func (b *Builder) ModelOptions(fn func(o *Options)) *Builder {
	if fn != nil {
		fn(b.cfg.DefaultOptions)
	}
	return b
}

// Attr 写默认选项 attr。
func (b *Builder) Attr(name string, val any) *Builder {
	b.cfg.DefaultOptions.AttrPut(name, val)
	return b
}

// ToolAdd 追加默认工具。
func (b *Builder) ToolAdd(tools ...ai.FunctionTool) *Builder {
	o := b.cfg.DefaultOptions
	o.Tools = append(o.Tools, tools...)
	return b
}

// TalentAdd 追加默认能力包。
func (b *Builder) TalentAdd(talents ...ai.Talent) *Builder {
	o := b.cfg.DefaultOptions
	o.Talents = append(o.Talents, talents...)
	return b
}

// InterceptorAdd 追加默认 Simple 拦截器。
func (b *Builder) InterceptorAdd(ics ...Interceptor) *Builder {
	o := b.cfg.DefaultOptions
	o.SimpleInterceptors = append(o.SimpleInterceptors, ics...)
	return b
}

// ToolContextPut 写默认工具上下文。
func (b *Builder) ToolContextPut(key string, val any) *Builder {
	o := b.cfg.DefaultOptions
	if o.ToolContext == nil {
		o.ToolContext = map[string]any{}
	}
	o.ToolContext[key] = val
	return b
}

// Build 构建智能体（Handler 或 ChatModel 必须提供——Java IllegalStateException
// 同款校验）。
func (b *Builder) Build() (*Agent, error) {
	return New(b.cfg)
}
