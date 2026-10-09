package react

import (
	"errors"
	"fmt"
	"strings"
	"time"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/flow"
)

// Config ReAct 智能体配置（对标 ReActAgentConfig）。
type Config struct {
	Name         string
	Role         string
	Instruction  string
	Profile      *agent.Profile
	SystemPrompt SystemPrompt
	ChatModel    *ai.ChatModel

	// FinishMarker 终止标识符（缺省 "[NAME_FINISH]" 惰性生成）。
	FinishMarker string
	// OutputKey 结果回填 Key。
	OutputKey string
	// Style 执行风格（缺省 StyleNativeTool）。
	Style Style

	// DefaultOptions 默认运行选项（回合/重试/窗口等）。
	DefaultOptions *Options
}

// NewConfig 以 ChatModel 构建缺省配置（Java Objects.requireNonNull 同款：
// model 为空时报错）。
func NewConfig(model *ai.ChatModel) *Config {
	if model == nil {
		panic("react: chatModel is required")
	}
	return &Config{
		Name:           "react_agent",
		SystemPrompt:   DefaultSystemPromptCN(),
		Style:          StyleNativeTool,
		DefaultOptions: NewOptions(),
		ChatModel:      model,
	}
}

// TraceKey 轨迹存放键（"__"+name 惰性缓存——Go 直接计算）。
func (c *Config) TraceKey() string { return "__" + c.Name }

// GetFinishMarker 完成标记（未设置时缺省 "[NAME_FINISH]"）。
func (c *Config) GetFinishMarker() string {
	if c.FinishMarker == "" {
		c.FinishMarker = "[" + strings.ToUpper(c.Name) + "_FINISH]"
	}
	return c.FinishMarker
}

// Locale 协议指令语言（跟随系统提示词）。
func (c *Config) Locale() string { return c.SystemPrompt.Locale() }

// SystemPromptFor 动态渲染系统提示词（#{key} 以 ctx.Vars() 解析 + 才能指令
// 追加子标题段）。
func (c *Config) SystemPromptFor(trace *Trace, ctx flow.Context) string {
	if c.SystemPrompt == nil {
		return ""
	}
	prompt := c.SystemPrompt.GetSystemPrompt(trace)
	if prompt == "" {
		return prompt
	}
	if ctx != nil {
		prompt = agent.RenderVars(prompt, ctx)
	}
	if ins := trace.Options().TalentInstruction(); ins != "" {
		prompt = prompt + "\n" + ins + "\n"
	}
	return prompt
}

// Agent ReAct 智能体（对标 ReActAgent）：reason→action 循环编排。可直接
// Prompt() 直调，也可作为 flow 图节点（AgentBase 默认 run）。
type Agent struct {
	*agent.AgentBase
	cfg *Config

	reasonTask *ReasonTask
	actionTask *ActionTask
}

// New 由配置构建（一般经 Builder）。
func New(cfg *Config) (*Agent, error) {
	if cfg == nil {
		return nil, errors.New("react: missing config")
	}
	if cfg.ChatModel == nil {
		return nil, errors.New("react: chatModel is required")
	}
	if cfg.Name == "" {
		cfg.Name = "react_agent"
	}
	if cfg.SystemPrompt == nil {
		cfg.SystemPrompt = DefaultSystemPromptCN()
	}
	if cfg.DefaultOptions == nil {
		cfg.DefaultOptions = NewOptions()
	}
	if cfg.Style != StyleStructuredText {
		cfg.Style = StyleNativeTool
	}
	a := &Agent{cfg: cfg}
	a.reasonTask = &ReasonTask{cfg: cfg, ag: a}
	a.actionTask = &ActionTask{cfg: cfg}
	a.AgentBase = agent.NewAgentBase(a)
	return a, nil
}

// Config 配置。
func (a *Agent) Config() *Config { return a.cfg }

// getTrace 轨迹 computeIfAbsent（键 "__"+name）。
func (a *Agent) getTrace(ctx flow.Context) *Trace {
	if trace, ok := ctx.Get(a.cfg.TraceKey()).(*Trace); ok && trace != nil {
		return trace
	}
	trace := NewTrace()
	ctx.Put(a.cfg.TraceKey(), trace)
	return trace
}

func (a *Agent) Name() string  { return a.cfg.Name }
func (a *Agent) Title() string { return a.cfg.Name }

// Role 角色职责（缺省回落 name——Java 同款）。
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

// Metadata 动态元数据。
func (a *Agent) Metadata(ctx flow.Context) map[string]any {
	return a.AgentBase.Metadata(ctx)
}

// Prompt 创建请求构建器（nil 恢复执行 / string / *ai.Prompt / []*ai.Message）。
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

// Call 任务执行（Agent 接口；options 走默认副本）。
func (a *Agent) Call(prompt *ai.Prompt, session agent.Session) (*ai.AssistantMessage, error) {
	return a.call(prompt, session, nil)
}

// call 核心调用流程（对标 ReActAgent.call：会话上下文/轨迹/拦截器/循环执行/
// 终态收口全语义）。
func (a *Agent) call(prompt *ai.Prompt, session agent.Session, options *Options) (*ai.AssistantMessage, error) {
	context := session.Context()
	protocol := agent.ProtocolOf(context)
	parentTeamTrace := agent.CurrentTeamTrace(context)

	// 初始化或恢复推理痕迹
	trace := a.getTrace(context)

	if options == nil {
		options = a.cfg.DefaultOptions.Copy()
	}

	if parentTeamTrace != nil && parentTeamTrace.HasStreamSink() {
		// 传递流控（团队流出口继承）
		options.SetStreamSink(parentTeamTrace.StreamSink())
	}

	// 添加必要的工具上下文
	if options.ToolContext == nil {
		options.ToolContext = map[string]any{}
	}
	options.ToolContext[ai.AttrSessionID] = session.SessionID()

	trace.prepare(a.cfg, options, session, protocol, a.cfg.Name)

	if protocol != nil {
		protocol.InjectAgentTools(context, a, trace.AddProtocolTool)
	}

	if prompt.IsEmpty() {
		// 可能是恢复执行（之前中断的）
		prompt = trace.OriginalPrompt()
		if prompt.IsEmpty() {
			return &ai.AssistantMessage{}, nil // Java: warn + ofAssistant("")
		}
	} else {
		// 新任务（重置相关数据）
		trace.reset(prompt)
		if _, ok := prompt.Attrs()[ai.AttrSessionID]; !ok {
			prompt.Attrs()[ai.AttrSessionID] = session.SessionID()
		}

		// 1. 加载历史上下文（短期记忆；仅工作记忆为空且非团队场景）
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
			message.AddMetadata(agent.MetaFirst, 1)
			trace.WorkingMemory().AddMessage(message)
		}

		session.UpdateSnapshot()
	}

	// 激活才能（计划生成 talent 随后续卡迁移；工具上下文并入 attrs）
	trace.activeTalents()

	// 反馈模式：追加挂起工具
	if options.FeedbackMode {
		options.Tools = append(options.Tools,
			FeedbackTool(options.FeedbackDescription(trace), options.FeedbackReasonDescription(trace)))
	}

	// 拦截器：任务开始
	for _, ic := range options.Interceptors {
		if ic != nil && ic.IsEnabled() {
			ic.OnAgentStart(trace)
		}
	}

	if trace.HasStreamSink() {
		trace.PushAgentEvent(trace.event(agent.KindRunStart))
	}

	if !session.IsPending() {
		startTime := time.Now()
		metricsBaseline := trace.Metrics().Snapshot()

		// 核心执行：route 循环（当前单元轨迹键临时指向本轨迹）
		var evalErr error
		_ = context.With(agent.KeyCurrentUnitTraceKey, a.cfg.TraceKey(), func() error {
			evalErr = a.evalDo(trace, context)
			return nil
		})

		// 累计本次活跃执行片段的性能指标（不含挂起等待时间）+ 团队增量汇总
		trace.Metrics().AddTotalDuration(time.Since(startTime).Milliseconds())
		if parentTeamTrace != nil {
			parentTeamTrace.Metrics().AddMetrics(trace.Metrics().DeltaSince(metricsBaseline))
		}
		if evalErr != nil {
			return nil, evalErr
		}
	} else {
		// 有挂起：以挂起原因为最终回答
		if !trace.HasFinalAnswer() {
			trace.SetFinalAnswer(session.PendingReason())
		}
	}

	result := trace.FinalAnswer()

	// 结果回填
	if a.cfg.OutputKey != "" && result != "" {
		context.Put(a.cfg.OutputKey, result)
	}

	// 终态收口（media 合并、思考/工具/搜索载体保留、协议状态语义筛留）
	assistantMessage := a.buildFinalAssistantMessage(result, trace)
	assistantMessage.AddMetadata(agent.MetaRunID, trace.RunID())

	// media-only 时 result 可能为空，仍需落 Session / WorkingMemory
	if result != "" || hasMediaBlocks(assistantMessage.Blocks) {
		if parentTeamTrace == nil {
			session.AddMessage(&ai.Message{Role: ai.RoleAssistant, Content: assistantMessage})
		}
		trace.WorkingMemory().AddMessage(&ai.Message{Role: ai.RoleAssistant, Content: assistantMessage})
	}

	session.UpdateSnapshot()

	if trace.IsAbnormal() {
		// 非 ai 结束的，要补位（流式输出兜底文案）
		if trace.HasStreamSink() {
			trace.PushAgentEvent(reasonDeltaOf(trace, &ai.ChatEvent{Type: ai.EventTextDelta, Text: assistantMessage.Text}))
			trace.PushAgentEvent(reasonDeltaOf(trace, &ai.ChatEvent{Type: ai.EventTextEnd}))
		}
	}

	// 拦截器：任务结束
	for _, ic := range options.Interceptors {
		if ic != nil && ic.IsEnabled() {
			ic.OnAgentEnd(trace)
		}
	}

	return assistantMessage, nil
}

// evalDo ReAct 调度执行（route 循环——Java ReActAgent.evalDo 逐字对标；
// 非真实 flow 图求值）。
func (a *Agent) evalDo(trace *Trace, context flow.Context) error {
	if trace.Route() == "" {
		trace.SetRoute(IDReason)
	}

	for {
		if trace.Session().IsPending() {
			break
		}

		switch trace.Route() {
		case IDReason:
			if err := a.reasonTask.run(trace, context); err != nil {
				return err
			}
		case IDAction:
			if err := a.actionTask.run(trace, context); err != nil {
				return err
			}
		default:
			return nil
		}
	}
	return nil
}

// buildFinalAssistantMessage 构造终态消息：文本用 finalAnswer，合并两类终态
// media（lastReason 自带 media + trace.finalMediaBlocks），保留思考/工具/搜索
// 载体并按语义摘要筛留协议回放状态（对标 ReActAgent.buildFinalAssistantMessage）。
func (a *Agent) buildFinalAssistantMessage(result string, trace *Trace) *ai.AssistantMessage {
	text := result
	lastReason := trace.LastReasonMessage()

	var finalBlocks []ai.ContentBlock
	var existingMedia []ai.ContentBlock
	textReplaced := false

	if lastReason != nil {
		for _, block := range lastReason.Blocks {
			if block == nil {
				continue
			}
			if _, ok := block.(ai.TextBlock); ok {
				// 以原正文块的位置替换正文，丢弃其它旧正文投影
				if !textReplaced && text != "" {
					finalBlocks = append(finalBlocks, ai.TextBlock{Text: text})
					textReplaced = true
				}
				continue
			}
			finalBlocks = append(finalBlocks, block)
			existingMedia = append(existingMedia, block)
		}
	}

	// returnDirect media 追加到基底 blocks 之后；仅去重追加，不改写基底顺序
	for _, block := range trace.FinalMediaBlocks() {
		if block == nil {
			continue
		}
		if _, ok := block.(ai.TextBlock); ok {
			continue
		}
		if containsEquivalentMedia(existingMedia, block) {
			continue
		}
		finalBlocks = append(finalBlocks, block)
		existingMedia = append(existingMedia, block)
	}

	// 有媒体但没有正文块时，正文投影放首位；纯文本消息保持无 blocks
	if !textReplaced && text != "" && len(finalBlocks) > 0 {
		finalBlocks = append([]ai.ContentBlock{ai.TextBlock{Text: text}}, finalBlocks...)
	}

	if lastReason == nil {
		return &ai.AssistantMessage{Text: text, Blocks: finalBlocks}
	}

	semanticMessage := &ai.AssistantMessage{
		Text:          text,
		Thinking:      lastReason.Thinking,
		ToolCalls:     lastReason.ToolCalls,
		Blocks:        finalBlocks,
		SearchResults: lastReason.SearchResults,
		Citations:     lastReason.Citations,
		Metadata:      lastReason.Metadata,
	}

	// 协议状态按语义摘要筛留（语义被改写的陈旧快照拒绝回放）
	if len(lastReason.ProtocolStates) > 0 {
		hash := ai.SemanticHash(semanticMessage)
		retained := map[string]any{}
		for k, v := range lastReason.ProtocolStates {
			if stateMatchesHash(v, hash) {
				retained[k] = v
			}
		}
		if len(retained) > 0 {
			semanticMessage.ProtocolStates = retained
		}
	}
	return semanticMessage
}

// stateMatchesHash 协议状态携带的语义摘要是否仍匹配当前消息。
func stateMatchesHash(v any, hash string) bool {
	var state *ai.ProtocolState
	switch s := v.(type) {
	case *ai.ProtocolState:
		state = s
	case ai.ProtocolState:
		state = &s
	default:
		return false
	}
	return state.SemanticHash != "" && state.SemanticHash == hash
}

// containsEquivalentMedia 两媒体块是否同一份载荷（同类型+同 mimeType+同内容）。
func containsEquivalentMedia(mediaBlocks []ai.ContentBlock, candidate ai.ContentBlock) bool {
	for _, existing := range mediaBlocks {
		if existing == candidate {
			return true
		}
		if existing != nil && sameMediaType(existing, candidate) &&
			existing.MimeType() == candidate.MimeType() &&
			equalContent(existing.Content(), candidate.Content()) &&
			candidate.Content() != nil {
			return true
		}
	}
	return false
}

func sameMediaType(a, b ai.ContentBlock) bool {
	return fmt.Sprintf("%T", a) == fmt.Sprintf("%T", b)
}

func equalContent(a, b any) bool {
	return fmt.Sprint(a) == fmt.Sprint(b)
}

// hasMediaBlocks 是否含非文本 media 块。
func hasMediaBlocks(blocks []ai.ContentBlock) bool {
	for _, block := range blocks {
		if block == nil {
			continue
		}
		if _, ok := block.(ai.TextBlock); !ok {
			return true
		}
	}
	return false
}

// reasonDeltaOf Reason 增量事件（转发模型 ChatEvent 或补位帧）。
func reasonDeltaOf(trace *Trace, ev *ai.ChatEvent) *agent.Event {
	e := trace.event(agent.KindReasonDelta)
	e.ChatEvent = ev
	e.Text = ev.Text
	return e
}

// ---- Builder（对标 ReActAgent.Builder 全链式面）----

// Builder ReAct 智能体构建器。
type Builder struct {
	cfg *Config
}

// NewBuilder 以 ChatModel 起步（对标 ReActAgent.of(chatModel)）。
func NewBuilder(model *ai.ChatModel) *Builder {
	return &Builder{cfg: NewConfig(model)}
}

func (b *Builder) Style(style Style) *Builder            { b.cfg.Style = style; return b }
func (b *Builder) Name(name string) *Builder             { b.cfg.Name = name; return b }
func (b *Builder) Role(role string) *Builder             { b.cfg.Role = role; return b }
func (b *Builder) Profile(p *agent.Profile) *Builder     { b.cfg.Profile = p; return b }
func (b *Builder) Instruction(s string) *Builder         { b.cfg.Instruction = s; return b }
func (b *Builder) SystemPrompt(sp SystemPrompt) *Builder { b.cfg.SystemPrompt = sp; return b }
func (b *Builder) FinishMarker(s string) *Builder        { b.cfg.FinishMarker = s; return b }

// InstructionFn 动态指令（每轮以 Trace 求值）。
func (b *Builder) InstructionFn(fn func(trace *Trace) string) *Builder {
	b.cfg.SystemPrompt = NewPromptBuilder().InstructionFn(fn).Build()
	return b
}

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

// InterceptorAdd 追加默认 ReAct 拦截器（头部插入——Java defaultInterceptorAdd
// 的 index 0 同款，后加者靠前）。
func (b *Builder) InterceptorAdd(ics ...Interceptor) *Builder {
	o := b.cfg.DefaultOptions
	o.Interceptors = append(append([]Interceptor(nil), ics...), o.Interceptors...)
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

// Retry 重试参数（max>=1、delayMs>=500；delay 缺省不动）。
func (b *Builder) Retry(maxRetries int, retryDelayMs ...int64) *Builder {
	if len(retryDelayMs) > 0 {
		b.cfg.DefaultOptions.SetRetryConfig(maxRetries, retryDelayMs[0])
	} else if maxRetries >= 1 {
		b.cfg.DefaultOptions.MaxRetries = maxRetries
	}
	return b
}

// MaxTurns 初始回合上限。
func (b *Builder) MaxTurns(n int) *Builder {
	b.cfg.DefaultOptions.SetMaxTurns(n)
	return b
}

// AutoRethink 自动反思开关。
func (b *Builder) AutoRethink(on bool) *Builder {
	b.cfg.DefaultOptions.SetAutoRethink(on)
	return b
}

// SessionWindow 会话回溯窗口（<=0 关闭）。
func (b *Builder) SessionWindow(n int) *Builder {
	b.cfg.DefaultOptions.SetSessionWindowSize(n)
	return b
}

func (b *Builder) OutputKey(key string) *Builder { b.cfg.OutputKey = key; return b }

func (b *Builder) OutputSchema(s string) *Builder {
	b.cfg.DefaultOptions.SetOutputSchema(s)
	return b
}

// OutputSchemaOf[T] 由类型反推输出 schema。
func OutputSchemaOf[T any]() string { return ai.OutputSchemaOf[T]() }

// PlanningMode 规划模式（本卡仅保留选项位）。
func (b *Builder) PlanningMode(on bool) *Builder {
	b.cfg.DefaultOptions.SetPlanningMode(on)
	return b
}

// PlanningInstruction 规划指令（固定文本）。
func (b *Builder) PlanningInstruction(s string) *Builder {
	b.cfg.DefaultOptions.SetPlanningInstructionProvider(func(*Trace) string { return s })
	return b
}

// FeedbackMode 反馈模式开关。
func (b *Builder) FeedbackMode(on bool) *Builder {
	b.cfg.DefaultOptions.SetFeedbackMode(on)
	return b
}

// FeedbackDescription 反馈说明（固定文本）。
func (b *Builder) FeedbackDescription(s string) *Builder {
	b.cfg.DefaultOptions.SetFeedbackDescriptionProvider(func(*Trace) string { return s })
	return b
}

// FeedbackReasonDescription 反馈原因说明（固定文本）。
func (b *Builder) FeedbackReasonDescription(s string) *Builder {
	b.cfg.DefaultOptions.SetFeedbackReasonDescriptionProvider(func(*Trace) string { return s })
	return b
}

// Build 构建智能体。
func (b *Builder) Build() (*Agent, error) { return New(b.cfg) }
