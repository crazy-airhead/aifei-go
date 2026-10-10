// engine.go 容器主类（对标 HarnessEngine）：装配（模型注册表/工具池/talent/
// 权限拦截器/命令注册表/代理管理器/扩展）+ 主代理懒加载 + 运行时热更新 +
// prompt 入口。
package harness

import (
	"context"
	"sync"

	"github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/ai/agent/react"
	"github.com/crazy-airhead/aifei-go/log"
)

// agentCallSessionName 引擎便捷调用的会话名。
const agentCallSessionName = "harness"

func agentSession() agent.Session {
	return agent.NewMemorySession(agentCallSessionName)
}

// Engine 编码代理容器。
type Engine struct {
	mu sync.Mutex

	models       map[string]*ai.ChatModel
	defaultModel *ai.ChatModel
	defaultModelName string

	toolPool []ai.FunctionTool // 工具池（ToolMapper 按名解析的源）
	toolMapper ToolMapper

	defaultTools    []string // 主代理工具集
	disallowedTools []string

	agentManager        *AgentManager
	commandRegistry     *CommandRegistry
	permissionInterceptor *PermissionInterceptor
	taskTalent          *TaskTalent
	generateTalent      *GenerateTalent
	extensions          []Extension

	workspace    string
	systemPrompt string
	maxTurns     int
	autoRethink  bool
	sessionWindowSize int
	hitlEnabled  bool
	multitaskMax int

	mainAgent *react.Agent
}

// Option 引擎选项。
type Option func(*Engine)

// WithWorkspace 工作目录（工具上下文 __cwd 注入）。
func WithWorkspace(dir string) Option { return func(e *Engine) { e.workspace = dir } }

// WithSystemPrompt 主代理系统提示追加段。
func WithSystemPrompt(sp string) Option { return func(e *Engine) { e.systemPrompt = sp } }

// WithDefaultTools 主代理工具集（Java Builder.toolsAdd）。
func WithDefaultTools(tools ...string) Option {
	return func(e *Engine) { e.defaultTools = append(e.defaultTools, tools...) }
}

// WithDisallowedTools 全局禁用工具。
func WithDisallowedTools(tools ...string) Option {
	return func(e *Engine) { e.disallowedTools = append(e.disallowedTools, tools...) }
}

// WithModel 模型注册（首个为缺省；defaultModel 显式指定优先）。
func WithModel(m *ai.ChatModel, names ...string) Option {
	return func(e *Engine) {
		name := ""
		if len(names) > 0 {
			name = names[0]
		} else if m != nil {
			name = m.Config().Name
		}
		if name == "" {
			name = "default"
		}
		e.models[name] = m
		if e.defaultModel == nil {
			e.defaultModel = m
			e.defaultModelName = name
		}
	}
}

// WithDefaultModel 缺省模型名。
func WithDefaultModel(name string) Option {
	return func(e *Engine) {
		e.defaultModelName = name
		if m, ok := e.models[name]; ok {
			e.defaultModel = m
		}
	}
}

// WithTools 工具池追加（ToolMapper 按名解析的源；talent 工具集）。
func WithTools(tools ...ai.FunctionTool) Option {
	return func(e *Engine) { e.toolPool = append(e.toolPool, tools...) }
}

// WithTalent 能力包的工具并入工具池。
func WithTalent(t ai.Talent) Option {
	return func(e *Engine) {
		if t == nil || !t.IsEnabled() {
			return
		}
		e.toolPool = append(e.toolPool, t.Tools(nil)...)
	}
}

// WithAgentCatalogDir 自定义代理目录（AgentManager 回源）。
func WithAgentCatalogDir(dir string) Option {
	return func(e *Engine) {
		if m, err := NewAgentManager(dir); err == nil {
			e.agentManager = m
		}
	}
}

// WithPermissionRule 全局权限规则（并入拦截器记忆上下文）。
func WithPermissionRule(rules ...PermissionRule) Option {
	return func(e *Engine) {
		e.permissionInterceptor.memory = e.permissionInterceptor.memory.AddRule(rules...)
	}
}

// WithHitlEnabled HITL 启用（缺省 false——Java 同款）。
func WithHitlEnabled(on bool) Option { return func(e *Engine) { e.hitlEnabled = on } }

// WithMaxTurns 回合上限（缺省 20）。
func WithMaxTurns(n int) Option {
	return func(e *Engine) {
		if n > 0 {
			e.maxTurns = n
		}
	}
}

// WithSessionWindowSize 会话窗口（缺省 12）。
func WithSessionWindowSize(n int) Option { return func(e *Engine) { e.sessionWindowSize = n } }

// WithMultitaskMax 并行子代理上限（缺省 5）。
func WithMultitaskMax(n int) Option { return func(e *Engine) { e.multitaskMax = n } }

// WithExtension 扩展注册。
func WithExtension(ext Extension) Option {
	return func(e *Engine) { e.extensions = append(e.extensions, ext) }
}

// WithCommandDir 命令目录加载（markdown 自定义命令）。
func WithCommandDir(dir string) Option {
	return func(e *Engine) {
		if err := e.commandRegistry.LoadDir(dir); err != nil {
			log.Warn("harness: command dir load: %v", err)
		}
	}
}

// NewEngine 构造（对标 Builder.build：装配 → 扩展 initialize 逐个触发）。
func NewEngine(opts ...Option) (*Engine, error) {
	e := &Engine{
		models:           map[string]*ai.ChatModel{},
		maxTurns:         20,
		autoRethink:      true,
		sessionWindowSize: 12,
		multitaskMax:     5,
		toolMapper:       defaultToolMapper,
	}
	e.agentManager, _ = NewAgentManager("")
	e.commandRegistry = NewCommandRegistry()
	e.permissionInterceptor = NewPermissionInterceptor()

	for _, o := range opts {
		o(e)
	}

	// 内置 talent 装配（子代理调度/生成）
	e.taskTalent = NewTaskTalent(e, e.multitaskMax)
	e.generateTalent = NewGenerateTalent(e)

	// 缺省工具集
	if len(e.defaultTools) == 0 {
		e.defaultTools = []string{"*"}
	}

	// 扩展 initialize（构建期一次性；失败 fail-fast）
	for _, ext := range e.extensions {
		if !ext.IsEnabled() {
			continue
		}
		if err := ext.Initialize(e); err != nil {
			return nil, err
		}
	}
	return e, nil
}

// AddExtension 运行时扩展注册（补触发 initialize + 重建主代理——Java 同款）。
func (e *Engine) AddExtension(ext Extension) error {
	if ext == nil {
		return nil
	}
	e.mu.Lock()
	e.extensions = append(e.extensions, ext)
	e.mu.Unlock()
	if ext.IsEnabled() {
		if err := ext.Initialize(e); err != nil {
			return err
		}
	}
	e.RefreshMainAgent()
	return nil
}

// AgentManager 代理管理器。
func (e *Engine) AgentManager() *AgentManager { return e.agentManager }

// CommandRegistry 命令注册表。
func (e *Engine) CommandRegistry() *CommandRegistry { return e.commandRegistry }

// PermissionInterceptor 权限拦截器。
func (e *Engine) PermissionInterceptor() *PermissionInterceptor { return e.permissionInterceptor }

// TaskTalent 子代理调度能力包。
func (e *Engine) TaskTalent() *TaskTalent { return e.taskTalent }

// GenerateTalent 子代理生成能力包。
func (e *Engine) GenerateTalent() *GenerateTalent { return e.generateTalent }

// Workspace 工作目录。
func (e *Engine) Workspace() string { return e.workspace }

// ModelOrNil 按名取模型（缺 nil）。
func (e *Engine) ModelOrNil(name string) *ai.ChatModel {
	e.mu.Lock()
	defer e.mu.Unlock()
	if name == "" {
		return e.defaultModel
	}
	return e.models[name]
}

// AddModel 模型注册（命中主代理则重建）。
func (e *Engine) AddModel(name string, m *ai.ChatModel) {
	e.mu.Lock()
	e.models[name] = m
	if e.defaultModelName == "" || e.defaultModelName == name {
		e.defaultModel = m
		if e.defaultModelName == "" {
			e.defaultModelName = name
		}
	}
	e.mu.Unlock()
	e.RefreshMainAgent()
}

// SetDefaultModel 缺省模型切换。
func (e *Engine) SetDefaultModel(name string) {
	e.mu.Lock()
	e.defaultModelName = name
	e.defaultModel = e.models[name]
	e.mu.Unlock()
	e.RefreshMainAgent()
}

// AllowTool 工具级放行（规则记忆 + 重建）。
func (e *Engine) AllowTool(name string) {
	e.mu.Lock()
	e.permissionInterceptor.memory = e.permissionInterceptor.memory.AddRule(AllowRule(name))
	e.mu.Unlock()
	e.RefreshMainAgent()
}

// DisallowTool 工具级禁用。
func (e *Engine) DisallowTool(name string) {
	e.mu.Lock()
	e.disallowedTools = append(e.disallowedTools, name)
	e.mu.Unlock()
	e.RefreshMainAgent()
}

// Prompt 主代理请求入口（对标 prompt()：附加缺省参数）。
func (e *Engine) Prompt(input string) *react.Request {
	req := e.MainAgent().Prompt(input)
	req.Options(func(o *react.Options) {
		o.MaxRetries = 3
		o.SetMaxTurns(e.maxTurns)
		o.SetSessionWindowSize(e.sessionWindowSize)
	})
	return req
}

// Call 便捷一问一答。
func (e *Engine) Call(ctx context.Context, input string) (string, error) {
	agent := e.MainAgent()
	if agent == nil {
		return "", contextErr(ctx)
	}
	msg, err := agent.Call(ai.PromptOf(input), agentSession())
	if err != nil {
		return "", err
	}
	if msg == nil {
		return "", nil
	}
	return msg.Text, nil
}

func contextErr(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errNoModel
}

var errNoModel = &engineError{"harness: no model configured"}

type engineError struct{ msg string }

func (e *engineError) Error() string { return e.msg }

// Extension 扩展 SPI（对标 HarnessExtension：引擎级一次性 initialize + 每次
// 建代理 configure）。
type Extension interface {
	IsEnabled() bool
	Initialize(e *Engine) error
	Configure(e *Engine, agentName string, builder *react.Builder)
}

// ExtensionBase 默认实现（恒启用、无操作）。
type ExtensionBase struct{}

// IsEnabled 恒启用。
func (ExtensionBase) IsEnabled() bool { return true }

// Initialize 无操作。
func (ExtensionBase) Initialize(*Engine) error { return nil }

// Configure 无操作。
func (ExtensionBase) Configure(*Engine, string, *react.Builder) {}
