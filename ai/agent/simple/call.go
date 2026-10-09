package simple

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"time"

	"github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/ai/httpx"
	"github.com/crazy-airhead/aifei-go/flow"
)

var (
	// errLlmNoReturn 模型空返回（对标 LlmNoReturnException；属传输类，触发重试）。
	errLlmNoReturn = errors.New("simple: The LLM did not return")

	// errStreamCancelled 流式消费方取消（takeUntil(cancelled) 的 Go 对应：
	// sink 中止消费；上层视同 Java InterruptedException → 返回 nil 消息）。
	errStreamCancelled = errors.New("simple: stream cancelled by consumer")

	// errAgentRequiresModelOrHandler ChatModel 与 Handler 必须提供其一。
	errAgentRequiresModelOrHandler = errors.New("simple: Handler or ChatModel must be provided")
)

// isRetryableError 重试谓词（P2-a 卡：retry 仅传输错误重试；业务 ERROR 不重试）：
// 空返回哨兵 ∪ HTTP 5xx/429 ∪ net.Error（超时/连接类）∪ EOF（连接中断）。
// 业务错误（方言 acc.Err 载荷、HTTP 4xx）不重试。
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errLlmNoReturn) {
		return true
	}
	var httpErr *httpx.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Status >= 500 || httpErr.Status == 429
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

// call 主执行流（对标 SimpleAgent.call，全语义）。
func (a *Agent) call(prompt *ai.Prompt, session agent.Session, options *Options) (*ai.AssistantMessage, error) {
	ctx := session.Context()
	protocol := agent.ProtocolOf(ctx) // 从上下文获取协议
	parentTeamTrace := agent.CurrentTeamTrace(ctx)

	// 初始化或恢复推理痕迹 (Trace)
	trace := a.getTrace(ctx)

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

	if prompt == nil || len(prompt.Messages()) == 0 {
		// 可能是旧问题（之前中断的）
		prompt = trace.OriginalPrompt()
		if prompt == nil || len(prompt.Messages()) == 0 {
			return &ai.AssistantMessage{}, nil // Java: warn + ofAssistant("")
		}
	} else {
		trace.reset(prompt)
		if _, ok := prompt.Attrs()[ai.AttrSessionID]; !ok {
			prompt.Attrs()[ai.AttrSessionID] = session.SessionID()
		}
		// 更新下快照（记录上面的数据）
		session.UpdateSnapshot()
	}

	// 拦截器：任务开始
	for _, ic := range options.SimpleInterceptors {
		if ic != nil && ic.IsEnabled() {
			ic.OnSimpleStart(trace)
		}
	}

	if trace.HasStreamSink() {
		trace.PushAgentEvent(&agent.Event{
			Kind: agent.KindSimpleStart, RunID: trace.RunID(), AgentName: a.cfg.Name,
			Session: session, Trace: trace,
		})
	}

	// 1. 构建请求消息
	finalPrompt := a.prepareAgentPrompt(trace, parentTeamTrace, session, prompt)

	// 2. 物理调用：带重试的 LLM 请求；耗时与团队用量增量在 finally 语义位结算
	startTime := time.Now()
	metricsBaseline := trace.Metrics().Snapshot()
	assistantMessage, callErr := a.callWithRetry(trace, session, finalPrompt, options)
	trace.Metrics().AddTotalDuration(time.Since(startTime).Milliseconds())
	if parentTeamTrace != nil {
		// 只汇总本次执行增量，避免重复执行时累计历史用量
		parentTeamTrace.Metrics().AddMetrics(trace.Metrics().DeltaSince(metricsBaseline))
	}
	if callErr != nil {
		return nil, callErr
	}

	if assistantMessage == nil {
		assistantMessage = &ai.AssistantMessage{}
	}

	// 3. 状态回填：输出结果映射到会话上下文（session.Context()，非图 ctx）
	if a.cfg.OutputKey != "" {
		ctx.Put(a.cfg.OutputKey, assistantMessage.Text)
	}

	// 4. 更新会话状态与快照
	assistantMessage.AddMetadata(agent.MetaRunID, trace.RunID())
	if (assistantMessage.Text != "" || len(assistantMessage.Blocks) > 0) &&
		len(assistantMessage.ToolCalls) == 0 && parentTeamTrace == nil {
		session.AddMessage(&ai.Message{Role: ai.RoleAssistant, Content: assistantMessage})
	}
	session.UpdateSnapshot()

	// 拦截器：任务结束
	for _, ic := range options.SimpleInterceptors {
		if ic != nil && ic.IsEnabled() {
			ic.OnSimpleEnd(trace)
		}
	}
	return assistantMessage, nil
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

// prepareAgentPrompt 组装完整 Prompt（系统提示 + 协议断面 + schema 指令 +
// 历史窗口 + 新问题；非团队场景同步归档到会话）。
func (a *Agent) prepareAgentPrompt(trace *Trace, parentTeamTrace agent.TeamTrace,
	session agent.Session, originalPrompt *ai.Prompt) *ai.Prompt {
	var spBuf strings.Builder
	if baseSp := a.cfg.SystemPromptFor(trace, session.Context()); baseSp != "" {
		spBuf.WriteString(baseSp)
	}
	// 协议指令注入（断面数据：如 Coder 写的代码就在这里）
	if trace.Protocol() != nil {
		trace.Protocol().InjectAgentInstruction(session.Context(), a, &spBuf)
	}
	// JSON Schema 指令
	if a.cfg.OutputSchema != "" && a.cfg.ChatModel != nil {
		a.cfg.ChatModel.Dialect().PrepareOutputSchemaInstruction(a.cfg.OutputSchema, &spBuf)
	}

	var messages []*ai.Message
	if spBuf.Len() > 0 {
		messages = append(messages, ai.OfSystem(strings.TrimSpace(spBuf.String())))
	}

	// 限定窗口的历史记录（初心标注；团队场景由协议组织，不走会话窗口）
	if parentTeamTrace == nil && a.cfg.SessionWindowSize > 0 {
		for _, m := range session.LatestMessages(a.cfg.SessionWindowSize) {
			m.AddMetadata(agent.MetaFirst, 1)
			messages = append(messages, m)
		}
	}

	// 新问题
	for _, m := range originalPrompt.Messages() {
		m.AddMetadata(agent.MetaFirst, 1)
		m.AddMetadata(agent.MetaRunID, trace.RunID())
		messages = append(messages, m)
	}

	// 消息归档：同步当前用户请求到会话历史
	if parentTeamTrace == nil && len(originalPrompt.Messages()) > 0 {
		for _, m := range originalPrompt.Messages() {
			m.AddMetadata(agent.MetaRunID, trace.RunID())
			session.AddMessage(m)
		}
	}

	p := ai.NewPrompt(messages...)
	for k, v := range originalPrompt.Attrs() {
		p.SetAttr(k, v)
	}
	return p
}

// callWithRetry 指数延迟重试调用（对标 callWithRetry）。模型请求绑定全新
// MemorySession（隔离原始轮次），成功后整体归档进工作记忆。
func (a *Agent) callWithRetry(trace *Trace, session agent.Session,
	finalPrompt *ai.Prompt, options *Options) (*ai.AssistantMessage, error) {
	var chatReq ai.RequestDesc
	var chatSession *ai.MemorySession

	if a.cfg.ChatModel != nil {
		chatSession = ai.NewMemorySession()
		opts := options
		chatReq = a.cfg.ChatModel.Prompt(finalPrompt).Session(chatSession).Options(func(o *ai.ChatOptions) {
			if opts.HTTPCustomize != nil {
				o.HTTPCustomize = opts.HTTPCustomize
			}
			for k, v := range opts.ToolContext {
				if o.ToolContext == nil {
					o.ToolContext = map[string]any{}
				}
				o.ToolContext[k] = v
			}
			// 配置工具
			o.Tools = append(o.Tools, opts.Tools...)
			// 协议工具
			if trace.Protocol() != nil {
				trace.Protocol().InjectAgentTools(session.Context(), a,
					func(t ai.FunctionTool) { o.Tools = append(o.Tools, t) })
			}
			o.Talents = append(o.Talents, opts.Talents...)
			// Simple 拦截器同时是 ChatInterceptor（OnPrepare 参与）
			for _, ic := range opts.SimpleInterceptors {
				o.Interceptors = append(o.Interceptors, ic)
			}
			if a.cfg.OutputSchema != "" {
				o.OutputSchema = a.cfg.OutputSchema
				a.cfg.ChatModel.Dialect().PrepareOutputFormatOptions(o)
			}
			if opts.AutoToolCall != nil {
				o.AutoToolCall = opts.AutoToolCall
			}
			for k, v := range opts.Raw {
				o.OptionSet(k, v)
			}
			o.CacheControl = opts.CacheControl
		})
	}

	task := &agent.RetryTask{
		MaxRetries:     a.cfg.MaxRetries,
		InitialDelayMs: a.cfg.RetryDelayMs,
		MaxDelayMs:     30000,
		RetryIf:        isRetryableError,
	}
	msg, err := task.Run(options.goContext(), func() (*ai.AssistantMessage, error) {
		return a.doCall(trace, session, finalPrompt, chatReq)
	})
	if err != nil {
		return nil, err
	}
	if chatSession != nil {
		trace.WorkingMemory().AddMessage(chatSession.Messages()...)
	}
	return msg, nil
}

// doCall 底层物理调用（流式转发或非流式），含空返回/纯思考/source 投影处理。
func (a *Agent) doCall(trace *Trace, session agent.Session,
	finalPrompt *ai.Prompt, chatReq ai.RequestDesc) (*ai.AssistantMessage, error) {
	if chatReq == nil {
		// fallback 到自定义处理器
		return a.cfg.Handler(finalPrompt, session)
	}

	var resp *ai.Response
	if trace.HasStreamSink() {
		var end *ai.ChatEvent
		err := chatReq.Stream(optionsGoCtx(trace), ai.ChatSinkFunc(func(ev *ai.ChatEvent) error {
			if trace.IsStreamCancelled() {
				return errStreamCancelled // takeUntil(cancelled)
			}
			switch ev.Type {
			case ai.EventTextStart, ai.EventTextDelta, ai.EventTextEnd,
				ai.EventThinkingStart, ai.EventThinkingDelta, ai.EventThinkingEnd,
				ai.EventMediaDone:
				trace.PushAgentEvent(&agent.Event{
					Kind: agent.KindSimpleDelta, RunID: trace.RunID(), AgentName: a.cfg.Name,
					Session: session, Trace: trace, ChatEvent: ev,
				})
			}
			if ev.Type == ai.EventResponseEnd {
				end = ev // reduce(keep last RESPONSE_END)
			}
			return nil
		}))
		if err != nil {
			// 消费方取消的两种到达路径：sink 哨兵（帧间检查）与传输层
			// context.Canceled（goCtx 即 HTTP ctx，取消先中断 body 读取）。
			// 两者都对应 Java InterruptedException → null 消息。
			if errors.Is(err, errStreamCancelled) ||
				(trace.IsStreamCancelled() && errors.Is(err, context.Canceled)) {
				return nil, nil
			}
			return nil, err
		}
		if end != nil {
			resp = end.Response()
		}
	} else {
		r, err := chatReq.Call()
		if err != nil {
			return nil, err
		}
		resp = r
	}

	if resp == nil || resp.IsEmpty() {
		// 触发重试
		return nil, errLlmNoReturn
	}

	responseMessage := resp.Message()
	if resp.Usage() != nil {
		trace.Metrics().AddUsage(resp.Usage())
	}

	// 纯 thinking 降级为普通最终答案（正文=思考摘要）；协议回放状态因语义
	// 变化失效不复用，但通用语义的媒体/引用与 metadata 保留。
	if responseMessage.ThinkingOnly() {
		return &ai.AssistantMessage{
			Text:          strings.TrimSpace(responseMessage.Thinking),
			SearchResults: responseMessage.SearchResults,
			Citations:     responseMessage.Citations,
			Metadata:      responseMessage.Metadata,
		}, nil
	}

	// source 投影（协议把通用正文改写为投影时，旧 raw/协议状态失效）
	if responseMessage.Text != "" {
		if _, has := responseMessage.Metadata[agent.MetaAgent]; has {
			if source, _ := responseMessage.Metadata["source"].(string); source != "" && source != responseMessage.Text {
				return &ai.AssistantMessage{
					Text:          source,
					Thinking:      responseMessage.Thinking,
					ToolCalls:     responseMessage.ToolCalls,
					Blocks:        projectBlocks(source, responseMessage.Blocks),
					SearchResults: responseMessage.SearchResults,
					Citations:     responseMessage.Citations,
					Metadata:      responseMessage.Metadata,
				}, nil
			}
		}
	}
	return responseMessage, nil
}

// optionsGoCtx 流式调用的取消源（未绑定时 Background——永不取消）。
func optionsGoCtx(trace *Trace) context.Context {
	if trace.options != nil && trace.options.goCtx != nil {
		return trace.options.goCtx
	}
	return context.Background()
}

// projectBlocks source 投影后的块列表：首个 TextBlock 替换为 source 文本
// （仅一次），无文本块且结果非空时前置补位；非文本块原样保留。
func projectBlocks(text string, sourceBlocks []ai.ContentBlock) []ai.ContentBlock {
	if len(sourceBlocks) == 0 {
		return sourceBlocks
	}
	projected := make([]ai.ContentBlock, 0, len(sourceBlocks))
	textWritten := false
	for _, block := range sourceBlocks {
		if _, ok := block.(ai.TextBlock); ok {
			if !textWritten && text != "" {
				projected = append(projected, ai.TextBlock{Text: text})
				textWritten = true
			}
			continue
		}
		if block != nil {
			projected = append(projected, block)
		}
	}
	if !textWritten && text != "" && len(projected) > 0 {
		projected = append([]ai.ContentBlock{ai.TextBlock{Text: text}}, projected...)
	}
	return projected
}
