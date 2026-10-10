// factory.go 代理工厂（对标 harness.agent.AgentFactory）：definition →
// react.Builder。工具名映射按 Go 已有 talents 收窄（Java 的 TOOL_ALL_PUBLIC
// 引用的 terminal/code/memory/skill/lsp 族工具尚未迁移——经 ToolMapper 缝
// 对接，缺省映射到 file/sys/diff/web 四件 talent 的工具集）。
package harness

import (
	"strings"

	"github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent/react"
	"github.com/crazy-airhead/aifei-go/log"
)

// 工具组常量（Java TOOL_ALL_PUBLIC/TOOL_PI 的 Go 收窄版；* = 全部公开工具，
// ** = 全部（含管理类），pi = 读/写/编辑/执行最小集）。
var (
	toolAllPublic = []string{"read", "write", "glob", "zip", "unzip", "shell", "python", "nodejs",
		"clock", "apply-diff", "apply-patch", "webfetch", "websearch", "task", "generate"}
	toolPi = []string{"read", "write", "shell"}
)

// ToolMapper 工具名 → 工具实例的解析缝（缺省 defaultToolMapper 按 Go talent
// 工具名直配；后续 terminal/code/memory talent 解锁后经此注入）。
type ToolMapper func(engine *Engine, name string) (ai.FunctionTool, bool)

// SetToolMapper 注入自定义解析器（引擎级）。
func (e *Engine) SetToolMapper(m ToolMapper) {
	e.mu.Lock()
	e.toolMapper = m
	e.mu.Unlock()
	e.RefreshMainAgent()
}

// defaultToolMapper 缺省映射：直接以工具名在引擎工具池按名匹配。
func defaultToolMapper(engine *Engine, name string) (ai.FunctionTool, bool) {
	for _, tool := range engine.toolPool {
		if tool != nil && tool.Name() == name {
			return tool, true
		}
	}
	return nil, false
}

// GetAgentBuilder definition → react.Builder（对标 AgentFactory.create）。
// sessionModel 为会话选中模型名（definition.model 优先）。
func (e *Engine) GetAgentBuilder(def *AgentDefinition, sessionModel string) *react.Builder {
	model := e.model(sessionModel)
	if def.Metadata.Model != "" {
		if m := e.ModelOrNil(def.Metadata.Model); m != nil {
			model = m
		}
	}
	builder := react.NewBuilder(model).
		Name(def.Name()).
		MaxTurns(e.maxTurns).
		AutoRethink(e.autoRethink).
		SessionWindow(e.sessionWindowSize)

	if def.Metadata.Description != "" {
		builder.Role(def.Metadata.Description)
	}
	if def.SystemPrompt != "" {
		builder.Instruction(def.SystemPrompt)
	}

	// 工具接线（tools 空 = 不接任何工具——Java 同语义）
	disallowed := map[string]bool{}
	for _, t := range def.Metadata.DisallowedTools {
		disallowed[t] = true
	}
	for _, t := range e.disallowedTools {
		disallowed[t] = true
	}
	names := expandToolNames(def.Metadata.Tools)
	clockAdded := false
	for _, name := range names {
		if disallowed[name] {
			continue
		}
		if tool, ok := e.toolMapper(e, name); ok {
			builder.ToolAdd(tool)
			if !clockAdded {
				// 有任何工具时补 clock（Java 同款收尾）
				if c, ok := e.toolMapper(e, "clock"); ok {
					builder.ToolAdd(c)
					clockAdded = true
				}
			}
		} else if name != "task" && name != "generate" && name != "hitl" {
			log.Warn("harness: tool '%s' not available (talent not migrated?)", name)
		}
	}

	// 子代理调度与动态生成（引擎内置 talent）
	if containsTool(names, "task") && !disallowed["task"] {
		builder.TalentAdd(e.taskTalent)
	}
	if containsTool(names, "generate") && !disallowed["generate"] {
		builder.TalentAdd(e.generateTalent)
	}

	// 拦截器：权限（hitl 显式列出或引擎启用）+ StopLoop
	if containsTool(names, "hitl") || e.hitlEnabled {
		builder.InterceptorAdd(e.permissionInterceptor)
	}
	builder.InterceptorAdd(react.NewStopLoopInterceptorOf(3, 8))

	// 权限规则（agent 级 delta 经 attr 携带，策略侧合并）
	if len(def.Metadata.PermissionRules) > 0 {
		builder.Attr(AttrPermissionContext, &PermissionContext{Rules: def.Metadata.PermissionRules})
	}

	// 工作目录注入（工具上下文）
	if e.workspace != "" {
		builder.ToolContextPut(AttrCWD, e.workspace)
	}

	// 扩展 configure（isEnabled 过滤）
	for _, ext := range e.extensions {
		if ext.IsEnabled() {
			ext.Configure(e, def.Name(), builder)
		}
	}
	return builder
}

// AttrCWD 工具上下文键（Java ATTR_CWD）。
const AttrCWD = "__cwd"

// AttrPermissionContext agent 级权限规则 attr 键（Java ATTR_PERMISSION_CONTEXT）。
const AttrPermissionContext = "__permissionContext"

// expandToolNames 通配展开（** 全量 / * 公开 / pi 最小集；其余原样）。
func expandToolNames(tools []string) []string {
	var out []string
	for _, t := range tools {
		switch t {
		case "**":
			out = append(out, toolAllPublic...)
		case "*":
			out = append(out, toolAllPublic...)
		case "pi":
			out = append(out, toolPi...)
		default:
			out = append(out, t)
		}
	}
	return out
}

func containsTool(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// GetAgentOrMain 按名取代理（空/未知名 → 主代理）。
func (e *Engine) GetAgentOrMain(name string) (*react.Agent, error) {
	if name == "" || name == AgentMain {
		return e.MainAgent(), nil
	}
	def := e.agentManager.Get(name)
	if def == nil {
		return e.MainAgent(), nil
	}
	return e.GetAgentBuilder(def, "").Build()
}

// MainAgent 主代理（双检锁懒加载；配置变更经 RefreshMainAgent 置空重建）。
func (e *Engine) MainAgent() *react.Agent {
	e.mu.Lock()
	defer e.mu.Unlock()
	agent := e.mainAgent
	if e.mainAgent != nil {
		return e.mainAgent
	}
	def := NewDefinitionBuilder(AgentMain).
		Primary(true).
		Description("主代理").
		ToolsAdd(e.defaultTools...).
		SystemPrompt(e.systemPrompt).
		Build()
	agent, err := e.GetAgentBuilder(def, "").Build()
	if err != nil {
		log.Error("harness: main agent build failed: %v", err)
		return nil
	}
	e.mainAgent = agent
	return agent
}

// RefreshMainAgent 置空主代理（下次访问重建）。
func (e *Engine) RefreshMainAgent() {
	e.mu.Lock()
	e.mainAgent = nil
	e.mu.Unlock()
}

// model 缺省/回退模型。
func (e *Engine) model(sessionModel string) *ai.ChatModel {
	if sessionModel != "" {
		if m := e.ModelOrNil(sessionModel); m != nil {
			return m
		}
	}
	if e.defaultModel != nil {
		return e.defaultModel
	}
	for _, m := range e.models {
		return m
	}
	return nil
}

// normalizeToolName 工具名归一（list → read 域；edit → apply-diff）。
func normalizeToolName(name string) string {
	switch name {
	case "ls", "list":
		return "read"
	case "edit":
		return "apply-diff"
	case "bash", "sh":
		return "shell"
	}
	return strings.TrimSpace(name)
}
