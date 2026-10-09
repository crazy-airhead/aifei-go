// agent.go 团队协作智能体（对标 team.TeamAgent）：多智能体协作容器，底层依托
// flow 状态机引擎执行协议构建的协作图。嵌套团队经嵌入的 AgentBase.Run 走团队
// 缝（NodeStart/End、父轨迹记录），self.Call 即本文件的核心生命周期。
package team

import (
	"fmt"
	"time"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/flow"
)

// 图内置节点 id（对标 TeamAgent.ID_SYSTEM / ID_SUPERVISOR）。
const (
	IDSystem     = "system"
	IDSupervisor = "supervisor"
)

// Agent 团队协作智能体。
type Agent struct {
	*agent.AgentBase // 嵌套团队时作为图节点：默认 run 管线（团队缝）

	cfg    *Config
	graph  *flow.Graph
	engine *flow.Engine
}

// NewAgent 依据配置构建（构图 + 引擎初始化）。
func NewAgent(cfg *Config) (*Agent, error) {
	if cfg == nil {
		return nil, fmt.Errorf("team: missing config")
	}
	a := &Agent{cfg: cfg, engine: flow.NewEngine()}
	a.AgentBase = agent.NewAgentBase(a)

	graph, err := a.buildGraph()
	if err != nil {
		return nil, err
	}
	a.graph = graph
	return a, nil
}

// buildGraph 依据协作协议构建计算图（协议构图 + 用户微调）。
func (a *Agent) buildGraph() (*flow.Graph, error) {
	return flow.Create(a.cfg.TraceKey(), func(spec *flow.GraphSpec) {
		// 1. 始终由协议构图。Sequential 等可不依赖 Supervisor LLM；
		// 需要 ChatModel 的 Supervisor 节点在运行期再校验。
		a.cfg.ProtocolWide().BuildGraph(spec)

		// 2. 执行用户自定义的图结构微调
		if adj := a.cfg.GraphAdjuster(); adj != nil {
			adj(spec)
		}
	})
}

// Graph 协作拓扑图（测试/可视化用）。
func (a *Agent) Graph() *flow.Graph { return a.graph }

// Config 团队配置。
func (a *Agent) Config() *Config { return a.cfg }

// GetTrace 会话中的团队协作轨迹（无则 nil）。
func (a *Agent) GetTrace(session agent.Session) *Trace {
	if session == nil || session.Context() == nil {
		return nil
	}
	trace, _ := session.Context().Get(a.cfg.TraceKey()).(*Trace)
	return trace
}

// getTrace 轨迹 computeIfAbsent（键 "__"+name）。
func (a *Agent) getTrace(ctx flow.Context) *Trace {
	if trace, ok := ctx.Get(a.cfg.TraceKey()).(*Trace); ok && trace != nil {
		return trace
	}
	trace := NewTrace()
	ctx.Put(a.cfg.TraceKey(), trace)
	return trace
}

func (a *Agent) Name() string  { return a.cfg.Name() }
func (a *Agent) Title() string { return a.cfg.Name() }

// Role 角色职责（缺省回落 name——Java 同款）。
func (a *Agent) Role() string {
	if a.cfg.Role() == "" {
		return a.cfg.Name()
	}
	return a.cfg.Role()
}

// RoleFor 动态角色（#{key} 渲染）。
func (a *Agent) RoleFor(ctx flow.Context) string {
	return agent.RenderVars(a.Role(), ctx)
}

// Profile 档案（惰性缺省）。
func (a *Agent) Profile() *agent.Profile { return a.cfg.Profile() }

// Metadata 动态元数据。
func (a *Agent) Metadata(ctx flow.Context) map[string]any {
	return a.AgentBase.Metadata(ctx)
}

// Prompt 创建团队请求（nil 恢复执行 / string / *ai.Prompt / []*ai.Message）。
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
	return &Request{ag: a, prompt: prompt}
}

// Call 任务执行（Agent 接口；嵌套团队时由 AgentBase.Run 回调；options 走默认副本）。
func (a *Agent) Call(prompt *ai.Prompt, session agent.Session) (*ai.AssistantMessage, error) {
	return a.call(prompt, session, nil)
}

// call 核心生命周期（对标 TeamAgent.call 全语义：轨迹准备/历史加载/拦截器/
// 引擎驱动/结果收敛）。
func (a *Agent) call(prompt *ai.Prompt, session agent.Session, options *Options) (*ai.AssistantMessage, error) {
	context := session.Context()
	parentTeamTrace := agent.CurrentTeamTrace(context)

	// 初始化或获取本次协作的轨迹快照
	trace := a.getTrace(context)

	if options == nil {
		options = a.cfg.DefaultOptions().Copy()
	}

	if parentTeamTrace != nil && parentTeamTrace.HasStreamSink() {
		// 传递流控（父团队流出口继承）
		options.SetStreamSink(parentTeamTrace.StreamSink())
	}

	// 1. 运行时环境准备
	trace.prepare(a.cfg, options, session, a.cfg.Name())

	if prompt == nil || prompt.IsEmpty() {
		// 可能是旧问题（之前中断的）
		prompt = trace.OriginalPrompt()
		if prompt == nil || prompt.IsEmpty() {
			return &ai.AssistantMessage{}, nil // Java: warn + ofAssistant("")
		}
	} else {
		// 新问题（重置数据）
		trace.reset(prompt)
		if _, ok := prompt.Attrs()[ai.AttrSessionID]; !ok {
			prompt.Attrs()[ai.AttrSessionID] = session.SessionID()
		}
		// 新问题锚点：删除该图的 last-record，flow 恢复逻辑回退到 start
		context.Trace().RecordNode(a.graph, nil)

		// 加载历史上下文（短期记忆；仅工作记忆为空且无父团队——顶层团队
		// 才从 Session 加载）
		if trace.WorkingMemory().IsEmpty() && options.SessionWindowSize > 0 && parentTeamTrace == nil {
			for _, message := range session.LatestMessages(options.SessionWindowSize) {
				message.AddMetadata(agent.MetaFirst, 1) // 初心
				trace.WorkingMemory().AddMessage(message)
			}
		}

		// 新的问题
		for _, message := range prompt.Messages() {
			message.AddMetadata(agent.MetaRunID, trace.RunID())
			if parentTeamTrace == nil {
				session.AddMessage(message)
			}
			message.AddMetadata(agent.MetaFirst, 1) // 初心
			trace.WorkingMemory().AddMessage(message)
		}

		// 更新下快照（记录上面的数据）
		session.UpdateSnapshot()
	}

	// 如果提示词没问题，开始部署才能
	trace.activeTalents()

	// 触发团队启动拦截
	for _, ic := range options.Interceptors {
		if ic != nil && ic.IsEnabled() {
			ic.OnTeamStart(trace)
		}
	}

	if trace.HasStreamSink() {
		trace.PushAgentEvent(trace.event(agent.KindTeamStart))
	}

	// 资源清理与协议后置处理（不受 onTeamEnd 异常影响；Java finally 同款，
	// 覆盖自此之后的所有返回路径）
	defer a.cfg.ProtocolWide().OnTeamFinished(context, trace)

	if !session.IsPending() {
		// 3. 驱动 Flow 引擎：在协议上下文中求值执行图
		startMs := time.Now().UnixMilli()
		baseline := trace.Metrics().Snapshot()

		// 每次求值动态挂载团队拦截器的 flow 视图（求值后摘除，避免跨调用累积）
		var flowIcs []flow.Interceptor
		for _, ic := range options.Interceptors {
			if ic != nil && ic.IsEnabled() {
				flowIcs = append(flowIcs, ic)
			}
		}
		for _, fic := range flowIcs {
			a.engine.AddInterceptor(fic)
		}

		err := context.With(agent.KeyCurrentTeamTraceKey, a.cfg.TraceKey(), func() error {
			return context.With(agent.KeyProtocol, a.cfg.protocolNarrow(), func() error {
				return a.engine.Eval(a.graph, context)
			})
		})

		for _, fic := range flowIcs {
			a.engine.RemoveInterceptor(fic)
		}

		// 累计本次活跃执行片段的性能指标（不包含任务挂起等待时间）
		trace.Metrics().AddTotalDuration(time.Now().UnixMilli() - startMs)

		// 父一级团队轨迹：只汇总本次执行增量，避免恢复执行时重复累计历史用量
		if pt, ok := parentTeamTrace.(*Trace); ok {
			pt.Metrics().AddMetrics(trace.Metrics().DeltaSince(baseline))
		}

		if err != nil {
			return nil, err
		}
	} else {
		// 如果有挂起
		if !trace.HasFinalAnswer() {
			trace.SetFinalAnswer(session.PendingReason())
		}
	}

	// 4. 结果收敛：从轨迹中提取最终答案
	result := trace.FinalAnswer()
	if !trace.HasFinalAnswer() {
		// 兜底策略：若无显式标记的 Final Answer，取最后一个执行专家的回复
		result = trace.LastAgentContent()
	}
	trace.SetFinalAnswer(result)

	// 回填业务上下文
	if a.cfg.OutputKey() != "" {
		context.Put(a.cfg.OutputKey(), result)
	}

	assistantMessage := &ai.AssistantMessage{Text: result}
	assistantMessage.AddMetadata(agent.MetaRunID, trace.RunID())
	if result != "" {
		if parentTeamTrace == nil {
			session.AddMessage(&ai.Message{Role: ai.RoleAssistant, Content: assistantMessage})
		}
		trace.WorkingMemory().AddMessage(&ai.Message{Role: ai.RoleAssistant, Content: assistantMessage})
	}

	session.UpdateSnapshot()

	// 5. 触发完成拦截
	for _, ic := range options.Interceptors {
		if ic != nil && ic.IsEnabled() {
			ic.OnTeamEnd(trace)
		}
	}

	return assistantMessage, nil
}

// ---- Builder（对标 TeamAgent.Builder）----

// Builder 团队智能体构建器。
type Builder struct {
	cfg *Config
}

// NewBuilder 以 Supervisor 模型起步（对标 TeamAgent.of(chatModel)）。
func NewBuilder(chatModel *ai.ChatModel) *Builder {
	return &Builder{cfg: NewConfig(chatModel)}
}

// Then 构建中途介入定制。
func (b *Builder) Then(fn func(*Builder)) *Builder {
	if fn != nil {
		fn(b)
	}
	return b
}

func (b *Builder) Name(name string) *Builder     { b.cfg.name = name; return b }
func (b *Builder) Role(role string) *Builder     { b.cfg.role = role; return b }
func (b *Builder) Profile(p *agent.Profile) *Builder {
	b.cfg.profile = p
	return b
}

// ProfileFn 档案定制（惰性档案上操作）。
func (b *Builder) ProfileFn(fn func(*agent.Profile)) *Builder {
	if fn != nil {
		fn(b.cfg.Profile())
	}
	return b
}

func (b *Builder) Instruction(s string) *Builder { b.cfg.instruction = s; return b }

// InstructionFn 动态指令（每轮以 Trace 求值）。
func (b *Builder) InstructionFn(fn func(*Trace) string) *Builder {
	b.cfg.systemPrompt = NewSystemPromptBuilder().InstructionFn(fn).Build()
	return b
}

func (b *Builder) SystemPrompt(sp SystemPrompt) *Builder {
	b.cfg.systemPrompt = sp
	return b
}

// AgentAdd 添加团队成员。
func (b *Builder) AgentAdd(agents ...agent.Agent) *Builder {
	for _, ag := range agents {
		b.cfg.AddAgent(ag)
	}
	return b
}

func (b *Builder) FinishMarker(s string) *Builder { b.cfg.finishMarker = s; return b }
func (b *Builder) OutputKey(s string) *Builder    { b.cfg.outputKey = s; return b }

func (b *Builder) RetryConfig(maxRetries int, retryDelayMs int64) *Builder {
	b.cfg.DefaultOptions().SetRetryConfig(maxRetries, retryDelayMs)
	return b
}

func (b *Builder) MaxTurns(maxTurns int) *Builder {
	b.cfg.DefaultOptions().SetMaxTurns(maxTurns)
	return b
}

func (b *Builder) SessionWindowSize(n int) *Builder {
	b.cfg.DefaultOptions().SetSessionWindowSize(n)
	return b
}

func (b *Builder) RecordWindowSize(n int) *Builder {
	b.cfg.DefaultOptions().SetRecordWindowSize(n)
	return b
}

// Protocol 指定团队协作协议（工厂式：Sequential/Hierarchical/Swarm 等）。
func (b *Builder) Protocol(factory func(*Config) Protocol) *Builder {
	b.cfg.SetProtocol(factory)
	return b
}

// GraphAdjuster 编排或调整计算图逻辑。
func (b *Builder) GraphAdjuster(fn func(*flow.GraphSpec)) *Builder {
	b.cfg.graphAdjuster = fn
	return b
}

// ModelOptions 调整主管模型默认选项（工具/拦截器/Raw 参数等）。
func (b *Builder) ModelOptions(fn func(o *Options)) *Builder {
	if fn != nil {
		fn(b.cfg.DefaultOptions())
	}
	return b
}

// Attr 写默认选项 attr。
func (b *Builder) Attr(name string, val any) *Builder {
	b.cfg.DefaultOptions().AttrPut(name, val)
	return b
}

func (b *Builder) FeedbackMode(on bool) *Builder {
	b.cfg.DefaultOptions().SetFeedbackMode(on)
	return b
}

func (b *Builder) FeedbackDescription(description string) *Builder {
	b.cfg.DefaultOptions().SetFeedbackDescriptionProvider(func(*Trace) string { return description })
	return b
}

func (b *Builder) FeedbackDescriptionFn(fn func(*Trace) string) *Builder {
	b.cfg.DefaultOptions().SetFeedbackDescriptionProvider(fn)
	return b
}

func (b *Builder) FeedbackReasonDescription(description string) *Builder {
	b.cfg.DefaultOptions().SetFeedbackReasonDescriptionProvider(func(*Trace) string { return description })
	return b
}

func (b *Builder) FeedbackReasonDescriptionFn(fn func(*Trace) string) *Builder {
	b.cfg.DefaultOptions().SetFeedbackReasonDescriptionProvider(fn)
	return b
}

// DefaultToolAdd 追加主管默认工具。
func (b *Builder) DefaultToolAdd(tools ...ai.FunctionTool) *Builder {
	o := b.cfg.DefaultOptions()
	o.Tools = append(o.Tools, tools...)
	return b
}

// DefaultToolContextPut 写主管默认工具上下文。
func (b *Builder) DefaultToolContextPut(key string, value any) *Builder {
	o := b.cfg.DefaultOptions()
	if o.ToolContext == nil {
		o.ToolContext = map[string]any{}
	}
	o.ToolContext[key] = value
	return b
}

// DefaultInterceptorAdd 追加默认团队拦截器。
func (b *Builder) DefaultInterceptorAdd(ics ...Interceptor) *Builder {
	o := b.cfg.DefaultOptions()
	o.Interceptors = append(o.Interceptors, ics...)
	return b
}

// Build 构建团队智能体（校验：至少一名成员或 graphAdjuster）。
func (b *Builder) Build() (*Agent, error) {
	if b.cfg.name == "" {
		b.cfg.name = "team_agent"
	}
	if len(b.cfg.agentNames) == 0 && b.cfg.graphAdjuster == nil {
		return nil, fmt.Errorf("team: at least one agent or a graphAdjuster is required")
	}
	return NewAgent(b.cfg)
}
