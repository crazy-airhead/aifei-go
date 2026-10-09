package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai/httpx"
)

// ChatSink 流式事件消费方（决策 D1 主契约）：OnEvent 串行回调；返回 error 即
// 消费方中止——不发 ERROR 事件、不发 RESPONSE_END，直接透传该 error。
type ChatSink interface {
	OnEvent(ev *ChatEvent) error
}

// ChatSinkFunc 函数适配器。
type ChatSinkFunc func(ev *ChatEvent) error

func (f ChatSinkFunc) OnEvent(ev *ChatEvent) error { return f(ev) }

// sinkCancel 消费方中止标记（内部）：把 sink 错误与上游错误区分开——前者无终止事件。
type sinkCancel struct{ err error }

func (e *sinkCancel) Error() string { return e.err.Error() }
func (e *sinkCancel) Unwrap() error { return e.err }

// streamSession 一次 Stream 订阅的跨轮状态（对标 ChatStreamSession）：responseID
// 全流一致；step 每轮模型调用递增、不因工具递归重置；usage 步间累加（各步是
// 独立模型调用，相加才是本次 stream 的真实消耗）。
type streamSession struct {
	responseID string
	step       int // -1 起；nextStep 先递增后返回（首轮 0）
	totalUsage *Usage
	usageSrcs  []json.RawMessage
}

func (s *streamSession) nextStep() int {
	s.step++
	return s.step
}

func (s *streamSession) currentStep() int {
	if s.step < 0 {
		return 0
	}
	return s.step
}

// accumulateUsage 每步恰好一次（STEP_END 发射处）；克隆后再累加，避免改写
// 已随 STEP_END 事件发出的分步快照。档位/区域是标签不是计数，取首个非空值。
func (s *streamSession) accumulateUsage(u *Usage) {
	if u == nil {
		return
	}
	if len(u.Source) > 0 {
		s.usageSrcs = append(s.usageSrcs, u.Source)
	}
	if s.totalUsage == nil {
		c := *u
		s.totalUsage = &c
		return
	}
	t := *s.totalUsage
	t.PromptTokens += u.PromptTokens
	t.ThinkTokens += u.ThinkTokens
	t.CompletionTokens += u.CompletionTokens
	t.TotalTokens += u.TotalTokens
	t.CacheCreationInputTokens += u.CacheCreationInputTokens
	t.CacheReadInputTokens += u.CacheReadInputTokens
	t.CacheCreation5mInputTokens += u.CacheCreation5mInputTokens
	t.CacheCreation1hInputTokens += u.CacheCreation1hInputTokens
	t.WebSearchRequests += u.WebSearchRequests
	t.WebFetchRequests += u.WebFetchRequests
	if t.ServiceTier == "" {
		t.ServiceTier = u.ServiceTier
	}
	if t.InferenceGeo == "" {
		t.InferenceGeo = u.InferenceGeo
	}
	t.Source = s.aggregateUsageSource()
	s.totalUsage = &t
}

// aggregateUsageSource 单步保留原始节点；多步以 steps 数组完整保留每轮的
// provider-specific 字段（不做 max、不静默覆盖）。
func (s *streamSession) aggregateUsageSource() json.RawMessage {
	switch len(s.usageSrcs) {
	case 0:
		return nil
	case 1:
		return s.usageSrcs[0]
	}
	var sb strings.Builder
	sb.WriteString(`{"steps":[`)
	for i, src := range s.usageSrcs {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.Write(src)
	}
	sb.WriteString("]}")
	return json.RawMessage(sb.String())
}

// streamState 一次 Stream 订阅的运行时状态（对标 Java stream() Flux.defer 内的
// 局部态；Stream 可重复调用，状态不落在 requestDesc 上）。
type streamState struct {
	r    *requestDesc
	sink ChatSink
	ctx  context.Context
	ss   *streamSession

	lastResp *Response      // 最近一次分步快照（失败收尾的 RESPONSE_END/ERROR 载荷）
	curAcc   *Accumulator   // 当前轮聚合（轮收尾后置 nil）
	curCtx   *StreamContext // 当前轮解析上下文
	errEvent *ChatEvent     // 方言 ERROR 原始事件（收尾重建终态 ERROR 用）
	abort    error          // 消费方取消或方言 ERROR：停止一切投递
}

// Stream 流式调用（决策 D1 主契约）。返回 nil = 按不变量正常终结；
// sink 返回 error 立即中止（不发 ERROR/RESPONSE_END，透传该 error）；
// 上游失败发终态 ERROR 事件（携带可打捞的部分聚合）并返回该 error。
func (r *requestDesc) Stream(ctx context.Context, sink ChatSink) error {
	r.prepare()

	st := &streamState{
		r:    r,
		sink: sink,
		ctx:  ctx,
		ss:   &streamSession{responseID: "resp_" + newGUID(), step: -1},
	}

	if err := st.dispatch(&ChatEvent{
		Type: EventResponseStart, ResponseID: st.ss.responseID, Index: -1,
	}); err != nil {
		return err.(*sinkCancel).err
	}

	if err := st.internalStream(ctx); err != nil {
		var sc *sinkCancel
		if errors.As(err, &sc) {
			return sc.err // 消费方中止：无终止事件（契约不变量 3）
		}
		return st.finishFailure(err)
	}
	return st.finishSuccess()
}

// dispatch 事件出站：过滤器 → sink；sink 错误包装为 sinkCancel。
func (st *streamState) dispatch(ev *ChatEvent) error {
	if ev == nil || !st.r.passEvent(ev) {
		return nil
	}
	if err := st.sink.OnEvent(ev); err != nil {
		return &sinkCancel{err: err}
	}
	return nil
}

// emit 模型层语义事件投递：ERROR 暂存转终态（全流恰好一个终止事件）；abort 后静默。
// 方言事件不经此函数——它们走 ctx.Emit（先归并再投递到 emitTo=本状态）。
func (st *streamState) emit(ev *ChatEvent) {
	if ev == nil {
		return
	}
	if ev.Type == EventError {
		if st.errEvent == nil {
			st.errEvent = ev
		}
		if st.abort == nil {
			if ev.Err != nil {
				st.abort = ev.Err
			} else {
				st.abort = fmt.Errorf("ai: LLM stream emitted an ERROR event without an error payload")
			}
		}
		return
	}
	if st.abort != nil {
		return
	}
	if err := st.dispatch(ev); err != nil {
		st.abort = err
	}
}

// internalStream 一轮流式调用（对标 Java internalStream）：StreamInterceptor 链
// → doStream；cfg.Timeout 作为本轮整体时限（ctx 表达，流式不能用客户端总超时）。
func (st *streamState) internalStream(ctx context.Context) error {
	req := st.r.newCallRequest(true)
	chain := newStreamChain(st.r.options.Interceptors, st.doStream)
	return chain.DoIntercept(ctx, req)
}

// doStream 单轮流式请求（对标 doStream + parseResp + onEventStream + onEventEnd）。
func (st *streamState) doStream(ctx context.Context, req *CallRequest) error {
	r := st.r
	m := r.model

	body, url, err := m.dialect.BuildRequestJSON(req.cfg, req.options, req.messages, true)
	if err != nil {
		return err
	}
	client := m.client
	if fn := req.options.HTTPCustomize; fn != nil {
		client = httpx.New(httpx.WithDoer(customizeDoer{inner: m.client.Doer(), fn: fn}))
	}
	if req.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.cfg.Timeout)
		defer cancel()
	}

	step := st.ss.nextStep()
	acc := NewAccumulator()
	acc.SetStream(true)
	acc.SetModel(req.cfg.Model)
	st.curAcc = acc

	sctx := NewStreamContext(req.cfg, acc, true, step, st.emit)
	sctx.SetResponseID(st.ss.responseID)
	sctx.SetOptions(req.options)
	st.curCtx = sctx

	st.emit(sctx.Event(EventStepStart))
	if st.abort != nil {
		return st.abort
	}

	frameCount, modelFrameCount := 0, 0
	lastFrame := ""
	err = client.PostJSONStream(ctx, url, body, m.requestHeader(req.options, true), func(_, data string) error {
		if st.abort != nil {
			return st.abort
		}
		if data == "" {
			return nil
		}
		frameCount++
		if isModelFrameShape(data) {
			modelFrameCount++
		}
		lastFrame = data

		prevUsage := acc.Usage()
		m.dialect.ParseResponseJSON(sctx, data)
		if st.abort != nil {
			return st.abort
		}
		if acc.Err() != nil {
			return acc.Err()
		}
		if u := acc.Usage(); u != nil && u != prevUsage {
			// usage 与内容正交：只在方言于当前帧提交新快照时发 USAGE，
			// 不把跨帧累计状态当成每帧新事件
			ev := sctx.Event(EventUsage)
			ev.Usage = u
			ev.SetResponse(acc.BuildResponse(req.cfg.Model, nil), nil)
			st.emit(ev)
		}
		return nil
	})
	if err != nil {
		return err
	}

	// 守卫（响应体边界）：空流 / 无模型帧 / 无完成信号都不能当正常空流静默完成。
	// （HTTP 边界的 Content-Type 守卫不单独做：httpx 不外露响应头，且 HTML 页面
	// 经 SSE 解析后必然落入前两条守卫。）
	if frameCount == 0 {
		return fmt.Errorf("ai: LLM stream response is empty. Check the upstream service and apiUrl config")
	}
	if modelFrameCount == 0 {
		return fmt.Errorf("ai: LLM stream response is unrecognizable: no model frame in %d frame(s)."+
			" Check the apiUrl and standard/provider config. last frame: %s", frameCount, abbreviate(lastFrame))
	}
	if !acc.IsFinished() {
		return fmt.Errorf("ai: LLM stream response ended before a completion signal."+
			" The response may be truncated. last frame: %s", abbreviate(lastFrame))
	}

	return st.onEventEnd(sctx, req)
}

// onEventEnd 一轮流结束（对标 Java onEventEnd）：工具组装 / 记忆写入 / STEP_END /
// 递归子流。子流在同一管线续播且 Step 递增（契约不变量 2）。
func (st *streamState) onEventEnd(ctx *StreamContext, req *CallRequest) error {
	acc := ctx.Acc()

	// 流结束时思考仍未闭合（模型整轮只吐 reasoning）：只关解析状态
	if acc.InThinking() {
		acc.SetInThinking(false)
	}

	memoryWritten := false
	if len(acc.ToolCallBuilders()) > 0 {
		outcome, err := st.buildStreamToolCallMessage(ctx, req)
		if err != nil {
			return err
		}
		if outcome == streamOutcomeRecursed {
			return nil // 递归轮已接管后续收尾
		}
		memoryWritten = outcome == streamOutcomeMemoryWritten
	}

	if !memoryWritten {
		if msg := acc.Message(); msg != nil {
			st.r.options.Session.AddMessage(&Message{Role: RoleAssistant, Content: msg})
		}
	}

	if err := st.emitStepEnd(ctx); err != nil {
		return err
	}
	st.curAcc = nil
	return nil
}

// streamToolOutcome 工具调用组装的处理结果（对标 Java ToolCallOutcome）。
type streamToolOutcome int

const (
	streamOutcomeComplete      streamToolOutcome = iota // 外层正常收尾（含写聚合消息）
	streamOutcomeRecursed                               // 已递归子流：外层不收尾
	streamOutcomeMemoryWritten                          // 记忆已写：外层收尾但不重写
)

// buildStreamToolCallMessage 流式工具调用组装（对标 Java 同名方法）：终态载体写入
// 聚合 → TOOL_CALL_END → 工具执行（TOOL_RESULT 流内可见）→ 递归或 returnDirect 合成。
func (st *streamState) buildStreamToolCallMessage(ctx *StreamContext, req *CallRequest) (streamToolOutcome, error) {
	r := st.r
	m := r.model
	acc := ctx.Acc()

	msg := m.dialect.BuildAssistantToolCallMessage(acc, acc.ToolCallBuilders())
	if msg == nil {
		// 工具调用解析为空：按普通完成收尾
		return streamOutcomeComplete, nil
	}
	am, _ := msg.Content.(*AssistantMessage)
	if am != nil {
		// 方言从 builder 只组装 ArgumentsStr（Java 在 ToolCall 构造时即填
		// arguments）：终态调用在此统一补解析——END 事件载荷、记忆消息与
		// 工具执行参数（buildToolMessage 取 call.Arguments）共享同一份
		am.ToolCalls = parseToolCallArgs(am.ToolCalls)
	}
	r.options.Session.AddMessage(msg)
	if am == nil || len(am.ToolCalls) == 0 {
		return streamOutcomeComplete, nil
	}

	// 参数拼接已完成：每个真实工具调用发一个完成信号（在执行之前，不携带快照）
	for i := range am.ToolCalls {
		call := &am.ToolCalls[i]
		ev := ctx.Event(EventToolCallEnd)
		ev.ToolCall = call
		ev.ToolCallID = call.ID
		ev.Text = call.ArgumentsStr
		ctx.Emit(ev)
	}
	if st.abort != nil {
		return streamOutcomeRecursed, st.abort
	}

	if r.autoToolCall() {
		returnDirect, err := r.buildToolMessage(req, am, func(call *ToolCall, tm *Message) {
			t, _ := tm.Content.(*ToolMessage)
			ev := ctx.Event(EventToolResult)
			ev.ToolCallID = call.ID
			ev.ToolCall = call
			if t != nil {
				ev.Text = t.ResultText()
			}
			ctx.Emit(ev)
		})
		if err != nil {
			return streamOutcomeComplete, err
		}
		if len(returnDirect) == 0 {
			// 本步收尾（终态载体写入聚合，供 STEP_END 快照与历史回放）→ 子流续播
			acc.SetError(nil)
			acc.SetFinishReason("tool")
			acc.MergeTerminalMessage(am)
			if err := st.emitStepEnd(ctx); err != nil {
				return streamOutcomeRecursed, err
			}
			st.curAcc = nil
			if err := st.internalStream(st.ctx); err != nil {
				return streamOutcomeRecursed, err
			}
			return streamOutcomeRecursed, nil
		}
		// 全部 returnDirect：工具结果合成终答（记忆交外层收尾写）
		final := m.dialect.BuildAssistantMessageByToolMessages(am, returnDirect)
		acc.SetError(nil)
		acc.SetFinishReason("tool")
		acc.SetTerminalMessage(final)
		st.emitSyntheticMessage(ctx, final)
		return streamOutcomeComplete, nil
	}

	// 关闭 autoToolCall：工具调用交回调用方（完成信号已发；调用经 STEP_END/RESPONSE_END 交付）
	acc.SetError(nil)
	acc.SetFinishReason("tool")
	acc.MergeTerminalMessage(am)
	return streamOutcomeMemoryWritten, nil
}

// emitStepEnd 发射本步结束事件（终态形态快照 + 用量；对标 Java emitStepEnd）。
func (st *streamState) emitStepEnd(ctx *StreamContext) error {
	acc := ctx.Acc()
	stepResp := acc.BuildResponse(ctx.Config().Model, nil)
	st.lastResp = stepResp
	st.ss.accumulateUsage(acc.Usage())

	ev := ctx.Event(EventStepEnd)
	ev.Usage = acc.Usage()
	ev.SetResponse(stepResp, stepResp.Message())
	st.emit(ev)
	if st.abort != nil {
		return st.abort
	}
	return nil
}

// emitSyntheticMessage returnDirect 合成消息投影为事件（对标 Java
// emitSyntheticMessageEvents；经 ctx.Emit 归并，与方言分片同管线）。
func (st *streamState) emitSyntheticMessage(ctx *StreamContext, acm *AssistantMessage) {
	if acm == nil {
		return
	}
	if len(acm.ToolCalls) > 0 {
		started, _ := ctx.AttrIfAbsent("__startedToolCalls", func() any {
			return map[string]struct{}{}
		}).(map[string]struct{})
		for i := range acm.ToolCalls {
			call := &acm.ToolCalls[i]
			key := toolKey(call)
			if key == "" || func() bool {
				if _, ok := started[key]; ok {
					return false
				}
				started[key] = struct{}{}
				return true
			}() {
				ev := ctx.Event(EventToolCallStart)
				ev.ToolCall = call
				ev.ToolCallID = call.ID
				ctx.Emit(ev)
			}
			if call.ArgumentsStr != "" {
				ev := ctx.Event(EventToolCallArgsDelta)
				ev.ToolCall = call
				ev.ToolCallID = call.ID
				ev.Text = call.ArgumentsStr
				ctx.Emit(ev)
			}
		}
	}
	for _, b := range acm.Blocks {
		if _, isText := b.(TextBlock); isText {
			continue
		}
		ev := ctx.Event(EventMediaDone)
		ev.Block = b
		ctx.Emit(ev)
	}
	if acm.Thinking != "" {
		ev := ctx.Event(EventThinkingDelta)
		ev.Text = acm.Thinking
		ctx.Emit(ev)
	}
	if acm.Text != "" {
		ev := ctx.Event(EventTextDelta)
		ev.Text = acm.Text
		ctx.Emit(ev)
	}
}

// finishSuccess 流收尾（正常完成）：RESPONSE_END 恰一次，携带全流聚合与累加用量。
// 终止事件的投递错误不再上抛——流已按契约完成，消费方在最后一刻取消不再有意义。
func (st *streamState) finishSuccess() error {
	var resp *Response
	if st.curAcc != nil {
		resp = st.curAcc.BuildResponse(st.r.model.config.Model, nil)
	} else if st.lastResp != nil {
		resp = st.lastResp
	}
	total := st.ss.totalUsage
	if resp != nil {
		// 克隆后替换用量：不改写已随 STEP_END 发出的分步快照
		c := *resp
		c.usage = total
		resp = &c
	}

	ev := &ChatEvent{
		Type:       EventResponseEnd,
		ResponseID: st.ss.responseID,
		Step:       st.ss.currentStep(),
		Index:      -1,
		Usage:      total,
	}
	if resp != nil {
		ev.SetResponse(resp, resp.Message())
	}
	_ = st.dispatch(ev)
	return nil
}

// finishFailure 流收尾（失败）：先配平 STEP_END（携带可打捞的部分聚合），再发
// 终态 ERROR（沿用方言原始 ERROR 事件的协议字段）。全流恰好一个终止事件，
// 不补 RESPONSE_END——那会让「收到 RESPONSE_END 即成功」的订阅方静默误判。
func (st *streamState) finishFailure(err error) error {
	if st.curAcc != nil && st.curCtx != nil {
		acc := st.curAcc
		partial := acc.BuildResponse(st.r.model.config.Model, nil)
		st.lastResp = partial
		st.ss.accumulateUsage(acc.Usage())

		ev := st.curCtx.Event(EventStepEnd)
		ev.Usage = acc.Usage()
		ev.SetResponse(partial, partial.Message())
		_ = st.dispatch(ev)
	}

	ev := &ChatEvent{
		Type:       EventError,
		ResponseID: st.ss.responseID,
		Step:       st.ss.currentStep(),
		Index:      -1,
		Err:        err,
		Usage:      st.ss.totalUsage,
	}
	if src := st.errEvent; src != nil {
		ev.RawType, ev.SubType = src.RawType, src.SubType
		ev.ProviderResponseID = src.ProviderResponseID
		ev.ItemID, ev.ToolCallID, ev.Index = src.ItemID, src.ToolCallID, src.Index
		ev.Text, ev.ToolCall, ev.Block = src.Text, src.ToolCall, src.Block
		ev.Raw, ev.Attrs = src.Raw, src.Attrs
	}
	if st.lastResp != nil {
		ev.SetResponse(st.lastResp, st.lastResp.Message())
	}
	_ = st.dispatch(ev)
	return err
}

// ---- channel 适配与聚合帮助（决策 D1）----

// StreamChan channel 适配（人体工学）：缓冲 64，满则阻塞生产者（天然背压，
// 不丢事件）；ctx 取消即中止并关闭。失败经终态 ERROR 事件表达后关闭——
// error 返回值恒 nil（契约签名对齐；流内错误不走同步返回，订阅方已离开）。
func StreamChan(ctx context.Context, req RequestDesc) (<-chan *ChatEvent, error) {
	ch := make(chan *ChatEvent, 64)
	go func() {
		defer close(ch)
		_ = req.Stream(ctx, ChatSinkFunc(func(ev *ChatEvent) error {
			select {
			case ch <- ev:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}))
	}()
	return ch, nil
}

// CallStream 流式请求的聚合形态（= stream().reduce）：事件全轨迹进
// Response.Events()，最终聚合取 RESPONSE_END 携带（含跨步累加用量）。
func CallStream(ctx context.Context, req RequestDesc) (*Response, error) {
	var events []*ChatEvent
	var end *ChatEvent
	err := req.Stream(ctx, ChatSinkFunc(func(ev *ChatEvent) error {
		events = append(events, ev)
		if ev.Type == EventResponseEnd {
			end = ev
		}
		return nil
	}))
	if err != nil {
		return nil, err
	}
	if end == nil || end.Response() == nil {
		return nil, fmt.Errorf("ai: stream completed without a RESPONSE_END event")
	}
	resp := end.Response()
	resp.events = events
	return resp, nil
}
