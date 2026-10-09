package react

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/ai/httpx"
	"github.com/crazy-airhead/aifei-go/flow"
)

var (
	// errLlmNoReturn 模型空返回（对标 LlmNoReturnException；react 重试缺省
	// 放宽——未知错误也重试，仅流已外发与 HTTP 4xx（非 429）例外）。
	errLlmNoReturn = errors.New("react: The LLM did not return")

	// errStreamCancelled 流式消费方取消（takeUntil(cancelled) 的 Go 对应）。
	errStreamCancelled = errors.New("react: stream cancelled by consumer")
)

// thoughtLabel 结构化文本模式的思考标签。
const thoughtLabel = "Thought:"

// ReasonTask ReAct 推理任务（对标 react.task.ReasonTask）：组装上下文发起模型
// 请求，解析意图（Action/Final Answer）并路由分发。
type ReasonTask struct {
	cfg *Config
	ag  *Agent
}

// Name 任务标识。
func (t *ReasonTask) Name() string { return IDReason }

// run 单轮推理（对标 ReasonTask.run，全语义）。
func (t *ReasonTask) run(trace *Trace, ctx flow.Context) error {
	if trace.Route() == IDEnd {
		// 有可能在 action 的拦截里，要求终止
		return nil
	}

	// 回合计数
	currentTurn := trace.NextTurn()
	maxTurns := trace.Options().EffectiveMaxTurns()

	if trace.Options().AutoRethink {
		// [AutoRethink 模式] 达到 80% 回合数时提前介入，留出 buffer 让模型
		// 执行自审和策略调整
		thresholdTurn := maxTurns - 1
		if v := maxTurns * 4 / 5; v > thresholdTurn {
			thresholdTurn = v
		}
		if currentTurn >= thresholdTurn {
			// 自动扩展回合数上限（续航）
			addTurns := 10
			if half := trace.Options().InitialMaxTurns() / 2; half > addTurns {
				addTurns = half
			}
			trace.Options().AddMaxTurns(addTurns)

			rethinkPrompt := "【系统指令：自我反思 (Self-Reflection)】\n" +
				"当前任务已执行至第 " + itoa(currentTurn) + " 回合。为了确保任务准确高效完成，请立即启动自审程序：\n\n" +
				"1. **核心目标检查**：重新审视用户最初提出的核心问题和当前要解决的任务，评估你当前的方向是否偏离了主线？\n" +
				"2. **有效性评估**：检查历史 Observation。如果最近的尝试没有带来有效新线索，说明当前策略已失效，请必须更换思路或换个角度切入。\n" +
				"3. **强制收敛**：若评估判定由于客观限制确实无法达成，请梳理已知线索，并在 Final Answer 中向用户复盘并申请协助。\n\n" +
				"根据新策略决定下一步行动，或输出 Final Answer 结束任务"
			trace.WorkingMemory().AddMessage(ai.OfUser(rethinkPrompt))
		}
	} else {
		// [标准模式] 严格边界判定
		if currentTurn > maxTurns {
			trace.SetRoute(IDEnd)
			trace.SetFinalAnswer("Agent error: Maximum turns reached (" + itoa(maxTurns) + ").")
			return nil
		}
	}

	// 组装「稳定」系统提示词（会话级不变量）：系统角色、输出格式约束及协议指令。
	// 执行计划进度看板属「回合级易变」内容，作为瞬时消息追加在消息列表末尾，
	// 保证 system prompt 作为可缓存前缀块跨回合稳定命中。
	var systemPromptBuf strings.Builder
	if baseSp := t.cfg.SystemPromptFor(trace, ctx); baseSp != "" {
		systemPromptBuf.WriteString(baseSp)
	}

	if trace.Options().OutputSchema != "" {
		t.cfg.ChatModel.Dialect().PrepareOutputSchemaInstruction(
			trace.Options().OutputSchema, &systemPromptBuf)
	}

	if trace.Protocol() != nil {
		trace.Protocol().InjectAgentInstruction(ctx, t.ag, &systemPromptBuf)
	}

	// 开始新建回合标记
	trace.NewCurrentTurnId()

	// 上下文预处理：消息组装前触发（允许拦截器压缩 WorkingMemory）
	for _, ic := range trace.Options().Interceptors {
		if ic != nil && ic.IsEnabled() {
			ic.OnReasonStart(trace, &systemPromptBuf)
		}
	}

	if trace.Route() == IDEnd {
		return nil
	}

	systemPromptStr := systemPromptBuf.String()

	if trace.HasStreamSink() {
		ev := trace.event(agent.KindReasonStart)
		ev.Meta["systemPrompt"] = systemPromptStr
		trace.PushAgentEvent(ev)
	}

	// 模型交互：物理请求 + 推理审计事件
	startMs := time.Now()
	response := t.callWithRetry(trace, systemPromptStr)
	if response == nil || trace.Session().IsPending() {
		trace.SetRoute(IDEnd)
		return nil
	}

	responseMessage := response.Message()

	if response.Usage() != nil {
		trace.Metrics().AddUsage(response.Usage())
	}

	durationMs := time.Since(startMs).Milliseconds()
	// 注意：Java 此处不带 isEnabled 判定（onReasonEnd 恒回调），忠实保留
	for _, ic := range trace.Options().Interceptors {
		if ic != nil {
			ic.OnReasonEnd(trace, response, responseMessage, durationMs)
		}
	}

	if trace.HasStreamSink() {
		ev := trace.event(agent.KindReasonEnd)
		ev.Message = responseMessage
		ev.Text = responseMessage.Text
		ev.Meta["durationMs"] = durationMs
		ev.Meta["response"] = response
		trace.PushAgentEvent(ev)
	}

	if trace.Session().IsPending() {
		return nil
	}

	// 容错处理：正文（trim 判空白）、工具调用与媒体均为空时引导重新生成；
	// 纯生图等 media-only 响应不算空
	if strings.TrimSpace(responseMessage.Text) == "" &&
		len(responseMessage.ToolCalls) == 0 &&
		!hasMediaBlocks(responseMessage.Blocks) {
		trace.emptyRetryCounter++
		if trace.emptyRetryCounter < 3 {
			if responseMessage.HasThinking() {
				// 保留思考上下文，避免下一轮失忆
				trace.WorkingMemory().AddMessage(&ai.Message{Role: ai.RoleAssistant, Content: responseMessage})
				formatFixPrompt := "【系统指令：输出格式修正 (Format Correction)】\n" +
					"您在第 " + itoa(currentTurn) + " 轮中输出了思考内容，但未包含有效的行动（Action）或最终答案（Final Answer）（第 " +
					itoa(trace.emptyRetryCounter) + " 次尝试）。\n\n" +
					"请检查您最近的 Observation 和之前的思考链，确保下一步操作或最终结论已明确给出。"
				trace.WorkingMemory().AddMessage(ai.OfUser(formatFixPrompt))
			} else {
				// 反思机制：完全空响应时引导模型回溯任务目标、审视历史轨迹并重新输出
				reflectPrompt := "【系统指令：自我反思 (Self-Reflection)】\n" +
					"您在第 " + itoa(currentTurn) + " 轮推理中返回了空响应（第 " +
					itoa(trace.emptyRetryCounter) + " 次尝试），请立即启动自我反思：\n\n" +
					"1. **回溯任务目标**：重新审视您最初被分配的任务核心目标，检查是否偏离了主线。\n" +
					"2. **审视历史轨迹**：回顾最近的 Observation 和之前的思考链，定位导致空响应的原因。\n" +
					"3. **策略修正**：若当前路径受阻，请果断切换思路或拆分为更小的子任务推进。\n"
				trace.WorkingMemory().AddMessage(ai.OfUser(reflectPrompt))
			}
			trace.SetRoute(IDReason)
		} else {
			// 软上限变硬：重试耗尽仍空，直接 END，避免 route 保持 REASON 空转到 maxTurns
			trace.SetRoute(IDEnd)
			// 纯思考轮的思考内容已流式输出给前端，再用兜底文案覆盖会割裂；
			// 降级用思考内容作答（非异常结束）
			thinkingFallback := responseMessage.Thinking
			if strings.TrimSpace(thinkingFallback) == "" {
				trace.SetFinalAnswer("抱歉，模型多次未返回有效内容。请稍后重试。")
			} else {
				trace.SetFinalAnswerDetail(strings.TrimSpace(thinkingFallback), false)
			}
		}
		return nil
	}
	trace.emptyRetryCounter = 0

	// 记录最近一次有效推理消息（Action 段解析与终态收口的基础）
	trace.SetLastReasonMessage(responseMessage)

	clearContent := responseMessage.Text

	// 路由分发：基于原生工具调用协议
	if len(responseMessage.ToolCalls) > 0 {
		trace.SetRoute(IDAction)
		return nil
	}

	// 路由判断：文本 ReAct 协议解析
	if t.cfg.Style == StyleNativeTool {
		// 有文本，或仅有媒体（如生图）时，均视为有效最终输出
		if clearContent != "" || hasMediaBlocks(responseMessage.Blocks) {
			trace.SetRoute(IDEnd)
			trace.SetFinalAnswerDetail(clearContent, false)
			return nil
		}
	}

	// 决策流控（基准用 clearContent，不受 <think> 标签内干扰词影响）：
	// 1. 优先 Action：同轮输出 Action + Final Answer 时必须先执行工具
	if i := strings.Index(clearContent, "Action:"); i >= 0 {
		if len(clearContent)-i > 7 {
			trace.SetRoute(IDAction)
			return nil
		}
	}

	// 2/3. 任务结束（Finish）/ 兜底：无工具调用即视为直接回复
	trace.SetRoute(IDEnd)
	trace.SetFinalAnswerDetail(t.extractFinalAnswer(clearContent), false)
	return nil
}

// callWithRetry 带重试的模型请求（对标 ReasonTask.callWithRetry；异常一律
// 收敛为兜底文案而非上抛）。
func (t *ReasonTask) callWithRetry(trace *Trace, systemPrompt string) *ai.Response {
	opts := trace.Options()
	streamEmitted := false

	task := &agent.RetryTask{
		MaxRetries:     opts.MaxRetries,
		InitialDelayMs: opts.RetryDelayMs,
		MaxDelayMs:     30000,
		// 流式已对外输出后重试会造成重复内容；深链判传输类异常，HTTP 4xx
		//（非 429）不重试，其余未知错误默认重试（Java ReAct 缺省，宽于 simple）
		RetryIf: func(e error) bool {
			if streamEmitted {
				return false
			}
			if errors.Is(e, errLlmNoReturn) {
				return true
			}
			var httpErr *httpx.HTTPError
			if errors.As(e, &httpErr) {
				return httpErr.Status == 429 || httpErr.Status >= 500
			}
			return true
		},
		OnRetry: func(attempt int, e error) {
			for _, ic := range trace.Options().Interceptors {
				if ic == nil || !ic.IsEnabled() {
					continue
				}
				// 重建上下文的拦截器异常吞掉，走原始重试流
				_ = ic.OnReasonRetry(trace, e, attempt, systemPrompt)
			}
		},
	}

	resp, err := task.Run(t.goCtx(trace), func() (*ai.Response, error) {
		messages := []*ai.Message{ai.OfSystem(systemPrompt)}
		messages = append(messages, trace.WorkingMemory().Messages()...)

		// 每次物理请求都重新渲染计划状态（重试拦截器可能改过计划）
		if planContext := RenderPlanContext(trace); planContext != "" {
			messages = append(messages, ai.OfUser(planContext))
		}

		chatReq := t.buildRequest(trace, messages)

		var response *ai.Response
		if trace.HasStreamSink() {
			var end *ai.ChatEvent
			err := chatReq.Stream(t.goCtx(trace), ai.ChatSinkFunc(func(ev *ai.ChatEvent) error {
				if trace.IsStreamCancelled() {
					return errStreamCancelled // takeUntil(cancelled)
				}
				if isRetryUnsafeOutput(ev) {
					streamEmitted = true
				}
				switch ev.Type {
				case ai.EventTextStart, ai.EventTextDelta, ai.EventTextEnd,
					ai.EventThinkingStart, ai.EventThinkingDelta, ai.EventThinkingEnd,
					ai.EventMediaDone:
					trace.PushAgentEvent(reasonDeltaOf(trace, ev))
				}
				if ev.Type == ai.EventResponseEnd {
					end = ev // reduce(keep last RESPONSE_END)
				}
				return nil
			}))
			if err != nil {
				if errors.Is(err, errStreamCancelled) ||
					(trace.IsStreamCancelled() && errors.Is(err, context.Canceled)) {
					// 订阅端取消：非模型故障，不重试，由 run() 以 END 收尾
					return nil, nil
				}
				return nil, err
			}
			if end != nil {
				response = end.Response()
			}
		} else {
			r, err := chatReq.Call()
			if err != nil {
				return nil, err
			}
			response = r
		}

		if response == nil {
			return nil, errLlmNoReturn
		}
		if response.IsEmpty() {
			return nil, errLlmNoReturn
		}
		return response, nil
	})
	if err != nil {
		return t.handleLastException(trace, err)
	}
	return resp
}

// isRetryUnsafeOutput 是否已产生不可安全重放的模型输出（TEXT/THINKING/
// TOOL_CALL/SERVER_TOOL/MEDIA/SAFETY 组）。
func isRetryUnsafeOutput(ev *ai.ChatEvent) bool {
	if ev == nil {
		return false
	}
	switch ev.Type {
	case ai.EventTextStart, ai.EventTextDelta, ai.EventTextEnd,
		ai.EventThinkingStart, ai.EventThinkingDelta, ai.EventThinkingEnd,
		ai.EventThinkingSignature, ai.EventThinkingRedacted,
		ai.EventToolCallStart, ai.EventToolCallArgsDelta, ai.EventToolCallEnd,
		ai.EventMediaPartial, ai.EventMediaDone,
		ai.EventRefusalDelta:
		return true
	}
	return false
}

// buildRequest 组装单次模型请求（对标 ReasonTask.buildRequest）。系统提示词
// 与才干指令随消息首条 system 走，故不设 o.SystemPrompt/o.OutputSchema——
// response_format 以临时 options 预演后合并 Raw，避免 prepare 二次注入。
func (t *ReasonTask) buildRequest(trace *Trace, messages []*ai.Message) ai.RequestDesc {
	opts := trace.Options()

	return t.cfg.ChatModel.Prompt(ai.NewPrompt(messages...)).Options(func(o *ai.ChatOptions) {
		if opts.HTTPCustomize != nil {
			o.HTTPCustomize = opts.HTTPCustomize
		}
		falseVal := false
		o.AutoToolCall = &falseVal
		for k, v := range opts.ToolContext {
			if o.ToolContext == nil {
				o.ToolContext = map[string]any{}
			}
			o.ToolContext[k] = v
		}

		if trace.Config().Style == StyleNativeTool {
			o.Tools = append(o.Tools, opts.Tools...)
			o.Tools = append(o.Tools, trace.ProtocolTools()...)
		}

		// ReAct 拦截器同时是 ai.Interceptor（OnPrepare 参与 chat 层组装）
		for _, ic := range opts.Interceptors {
			if ic != nil {
				o.Interceptors = append(o.Interceptors, ic)
			}
		}

		if opts.OutputSchema != "" {
			tmp := &ai.ChatOptions{OutputSchema: opts.OutputSchema}
			t.cfg.ChatModel.Dialect().PrepareOutputFormatOptions(tmp)
			for k, v := range tmp.Raw {
				o.OptionSet(k, v)
			}
		}

		for k, v := range opts.Raw {
			o.OptionSet(k, v)
		}
		o.CacheControl = opts.CacheControl
	})
}

// handleLastException 重试耗尽后的兜底（对标 ReasonTask.handleLastException）：
// 中断信号静默收尾，其余按异常类别提炼友好文案（1 参 setFinalAnswer——异常
// 结束，触发流式补位）。
func (t *ReasonTask) handleLastException(trace *Trace, lastException error) *ai.Response {
	// 1. 中断信号（消费方取消/ctx 取消）
	if errors.Is(lastException, context.Canceled) {
		return nil
	}

	// 2. 终止状态 + 友好提示
	trace.SetRoute(IDEnd)

	var httpRespEx *httpx.HTTPError
	errors.As(lastException, &httpRespEx)

	switch {
	case errors.Is(lastException, errLlmNoReturn):
		trace.SetFinalAnswer("抱歉，模型服务没有内容返回。请稍后重试。")
	case isTimeoutError(lastException):
		trace.SetFinalAnswer("抱歉，模型服务响应超时。请稍后重试。")
	case isConnectError(lastException):
		trace.SetFinalAnswer("抱歉，模型服务连接失败。请稍后重试。")
	case httpRespEx != nil:
		trace.SetFinalAnswer("抱歉，模型服务响应出错（CODE: " + itoa(httpRespEx.Status) + "）。请稍后重试。")
	default:
		// 兜底提示（防止敏感堆栈或 HTML 泄露给终端）
		trace.SetFinalAnswer("抱歉，暂时无法使用模型服务（具体参考日志）。请稍后重试。")
	}
	return nil
}

func isTimeoutError(e error) bool {
	var netErr net.Error
	if errors.As(e, &netErr) && netErr.Timeout() {
		return true
	}
	return e != nil && strings.Contains(strings.ToLower(e.Error()), "timeout")
}

func isConnectError(e error) bool {
	if e == nil {
		return false
	}
	var dnsErr *net.DNSError
	if errors.As(e, &dnsErr) {
		return true
	}
	msg := strings.ToLower(e.Error())
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "no such host") ||
		errors.Is(e, io.EOF)
}

// extractThought 提取思考主体（移除 Thought: 标签；原生工具模式直通正文）。
func (t *ReasonTask) extractThought(trace *Trace, clearContent string) string {
	if clearContent == "" {
		return ""
	}
	if trace.Config().Style == StyleNativeTool {
		return clearContent
	}

	labelIndex := strings.Index(clearContent, thoughtLabel)
	if labelIndex < 0 {
		return ""
	}
	result := strings.TrimSpace(clearContent[labelIndex+len(thoughtLabel):])
	if i := strings.Index(result, "\nAction:"); i > -1 {
		result = strings.TrimSpace(result[:i])
	}
	return result
}

// extractFinalAnswer 从思考片段中提取最终业务答案（finishMarker 缺失时回落
// Action: 标签）。
func (t *ReasonTask) extractFinalAnswer(clearContent string) string {
	if clearContent == "" {
		return ""
	}
	answer := clearContent
	marker := t.cfg.GetFinishMarker()

	markerIndex := strings.Index(answer, marker)
	if markerIndex < 0 {
		marker = "Action:"
		markerIndex = strings.Index(answer, marker)
	}
	if markerIndex < 0 {
		return ""
	}
	return strings.TrimSpace(answer[markerIndex+len(marker):])
}

// goCtx 流式调用的取消源（未绑定时 Background——永不取消）。
func (t *ReasonTask) goCtx(trace *Trace) context.Context {
	if c := trace.Options().goContext(); c != nil {
		return c
	}
	return context.Background()
}

// itoa strconv 别名（包内简写）。
func itoa(n int) string { return strconv.Itoa(n) }
