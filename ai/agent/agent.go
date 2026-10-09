// Package agent 提供 solon-ai-agent 的智能体基座（对标 org.noear.solon.ai.agent）：
// Agent 接口（flow.NamedTaskComponent）+ 默认 run 管线、会话/挂起、事件模型、
// 轨迹与用量指标、重试工具，以及团队协作（P2-c）的结构化缝。
package agent

import (
	"strings"
	"time"

	"github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/flow"
)

// Agent 智能体核心接口（对标 Java Agent；泛型 Req/Resp 在 Go 经各实现的
// Prompt 构造器表达——如 simple.Agent.Prompt）。实现 flow.NamedTaskComponent，
// 可经 GraphSpec.AddActivityNamed 直接接入 flow 图。
type Agent interface {
	flow.NamedTaskComponent

	// Role 角色职责（用于 Prompt 提示与协作分发参考）。
	Role() string
	// RoleFor 动态角色描述：以 #{key} 引用 ctx.Vars() 本地渲染（对标
	// SnelUtil.render；契约文档的 enjoy 方案改此轻量实现，未知键保留原样）。
	RoleFor(ctx flow.Context) string
	// Profile 智能体档案（可 nil）。
	Profile() *Profile
	// Metadata 动态元数据（协作传递；对标 AgentUtil.toMetadataNode）。
	Metadata(ctx flow.Context) map[string]any

	// Call 指定指令的任务执行（prompt 为 nil 表示继续之前的话题）。
	Call(prompt *ai.Prompt, session Session) (*ai.AssistantMessage, error)
}

// AgentBase 默认 run 管线（CRTP：NewAgentBase(self) 由具体智能体构造时回填）。
// 语义对标 Java Agent.run 默认实现：
//  1. 会话 computeIfAbsent（_SESSION_，缺省 NewMemorySession("tmp")）；
//  2. 团队轨迹：记 lastAgentName → 拦截器 veto（[Skipped] 记录后直接返回）→
//     有流出口时推 NodeStart；
//  3. 团队协议组装生效提示词（无团队则为 nil），调 self.Call；
//  4. 团队轨迹收尾：NodeEnd → 协议 ResolveAgentOutput（空产出兜底文案）→
//     AddRecord → 协议/拦截器 OnAgentEnd。
type AgentBase struct {
	self Agent
}

// NewAgentBase 绑定具体智能体（run 经 self 多态回调 Call/Name 等）。
func NewAgentBase(self Agent) *AgentBase { return &AgentBase{self: self} }

// Run flow 节点运行实现（引擎在 eval 到该节点时调用）。
func (b *AgentBase) Run(ctx flow.Context, node *flow.Node) error {
	self := b.self

	// 1. 获取或初始化会话
	session, _ := ctx.Get(KeySession).(Session)
	if session == nil {
		session = NewMemorySession("tmp")
		ctx.Put(KeySession, session)
	}

	// 2. 团队协作轨迹与拦截
	trace := CurrentTeamTrace(ctx)
	if trace != nil {
		trace.SetLastAgentName(self.Name())
		for _, ic := range trace.Interceptors() {
			if ic == nil || !ic.IsEnabled() {
				continue
			}
			if !ic.ShouldAgentContinue(trace, self) {
				trace.AddRecord(ai.RoleAssistant, self.Name(),
					"[Skipped] Cancelled by "+ic.Name(), 0)
				return nil
			}
		}
		if trace.HasStreamSink() {
			trace.PushAgentEvent(NodeStartEventOf(node, trace))
		}
	}

	// 3. 准备提示词并执行推理
	var effectivePrompt *ai.Prompt
	if trace != nil {
		effectivePrompt = trace.Protocol().PrepareAgentPrompt(trace, self, trace.WorkingMemory())
	}

	start := time.Now()
	msg, err := self.Call(effectivePrompt, session)
	duration := time.Since(start).Milliseconds()

	// 4. 同步执行轨迹与结果处理
	if trace != nil {
		if trace.HasStreamSink() {
			trace.PushAgentEvent(NodeEndEventOf(node, trace, msg))
		}
		if err == nil { // 出错路径无产出可记（错误由引擎上报）
			rawContent := ""
			if msg != nil {
				rawContent = strings.TrimSpace(msg.Text)
			}
			finalResult := trace.Protocol().ResolveAgentOutput(trace, self, rawContent)
			if finalResult == "" {
				finalResult = "Agent [" + self.Name() + "] processed but returned no textual content."
			}
			trace.AddRecord(ai.RoleAssistant, self.Name(), finalResult, duration)
			trace.Protocol().OnAgentEnd(trace, self)
			for _, ic := range trace.Interceptors() {
				if ic != nil && ic.IsEnabled() {
					ic.OnAgentEnd(trace, self)
				}
			}
		}
	}
	return err
}

// Metadata 对标 AgentUtil.toMetadataNode：{name, role?(roleFor 渲染),
// capabilities?, inputModes?}。
func (b *AgentBase) Metadata(ctx flow.Context) map[string]any {
	self := b.self
	node := map[string]any{"name": self.Name()}
	if role := self.Role(); role != "" {
		node["role"] = self.RoleFor(ctx)
	}
	if p := self.Profile(); p != nil {
		if caps := p.Capabilities(); len(caps) > 0 {
			node["capabilities"] = caps
		}
		if ims := p.InputModes(); len(ims) > 0 {
			node["inputModes"] = ims
		}
	}
	return node
}

// RenderVars #{key} 模板渲染（对标 SnelUtil.render：取 ctx.Vars()；未知键与
// 非字符串值原样保留）。RoleFor 等动态文案的统一实现。
func RenderVars(tpl string, ctx flow.Context) string {
	if !strings.Contains(tpl, "#{") || ctx == nil {
		return tpl
	}
	var sb strings.Builder
	for {
		i := strings.Index(tpl, "#{")
		if i < 0 {
			sb.WriteString(tpl)
			return sb.String()
		}
		sb.WriteString(tpl[:i])
		rest := tpl[i+2:]
		j := strings.IndexByte(rest, '}')
		if j < 0 {
			sb.WriteString(tpl[i:])
			return sb.String()
		}
		key := rest[:j]
		if v, ok := ctx.Get(key).(string); ok {
			sb.WriteString(v)
		} else {
			sb.WriteString("#{")
			sb.WriteString(key)
			sb.WriteString("}")
		}
		tpl = rest[j+1:]
	}
}
