package react

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/flow"
)

// ErrInvalidArguments 工具参数类错误哨兵：handler 返回 %w 本哨兵的错误时按
// 「参数自愈」类处理（对标 Java IllegalArgumentException/StatusException 的
// 参数错误分支——Go 无受检异常类别，以哨兵显式声明）。
var ErrInvalidArguments = errors.New("react: invalid tool arguments")

// 工具错误类别标注（chain 终点写进 ToolResult.Meta；ToolRetry/executeTool 消费）。
const (
	metaErrClass = "_react_err_class_"
	metaErrTool  = "_react_err_tool_"

	errClassArgs     = "args"
	errClassPhysical = "physical"
)

// ToolExchanger Action 工具执行交换器（对标 react.task.ToolExchanger）：承载
// 工具参数与完整 ToolResult（文本/media/isError/metas），避免 returnDirect 与
// observation 路径把富结果压成纯字符串。
type ToolExchanger struct {
	callID   string
	toolName string
	args     map[string]any

	returnDirect bool

	toolCall   *ai.ToolCall
	toolResult *ai.ToolResult
}

// NewToolExchanger 构造交换器。
func NewToolExchanger(callID, toolName string, args map[string]any) *ToolExchanger {
	return &ToolExchanger{callID: callID, toolName: toolName, args: args}
}

// CallId 调用标识。
func (e *ToolExchanger) CallId() string { return e.callID }

// ToolName 工具名。
func (e *ToolExchanger) ToolName() string { return e.toolName }

// Args 调用参数（拷贝自模型原始参数，HITL 改参不污染 ToolCall）。
func (e *ToolExchanger) Args() map[string]any { return e.args }

// IsReturnDirect 是否可直接返回给调用者（仅真实成功后标记）。
func (e *ToolExchanger) IsReturnDirect() bool { return e.returnDirect }

// SetReturnDirect 标记直返。
func (e *ToolExchanger) SetReturnDirect(b bool) { e.returnDirect = b }

// ToolCall 关联的原生工具调用（文本模式为 nil）。
func (e *ToolExchanger) ToolCall() *ai.ToolCall { return e.toolCall }

func (e *ToolExchanger) setToolCall(call *ai.ToolCall) { e.toolCall = call }

// Result 文本投影（兼容 HITL / 拦截器既有 API）。
func (e *ToolExchanger) Result() string {
	if e.toolResult == nil {
		return ""
	}
	return e.toolResult.ContentText()
}

// ToolResult 完整工具结果（含 blocks/isError/metas）。
func (e *ToolExchanger) ToolResult() *ai.ToolResult { return e.toolResult }

// SetToolResult 写入完整工具结果（执行成功/失败/HITL 预填后统一入口）。
func (e *ToolExchanger) SetToolResult(r *ai.ToolResult) { e.toolResult = r }

// SetResult 按文本写入或改写结果（HITL 预填、批准 Note 等场景）。已有富结果时
// 仅替换文本投影，保留非文本 media blocks 与 isError/metas（避免 Note 冲掉图）。
func (e *ToolExchanger) SetResult(result string) {
	if e.toolResult == nil {
		e.toolResult = ai.ToolResultOf(result)
		return
	}
	rebuilt := &ai.ToolResult{Text: result, IsError: e.toolResult.IsError}
	for k, v := range e.toolResult.Meta {
		if rebuilt.Meta == nil {
			rebuilt.Meta = map[string]any{}
		}
		rebuilt.Meta[k] = v
	}
	for _, block := range e.toolResult.Blocks {
		if _, ok := block.(ai.TextBlock); !ok && block != nil {
			rebuilt.Blocks = append(rebuilt.Blocks, block)
		}
	}
	e.toolResult = rebuilt
}

// ActionTask ReAct 动作执行任务（对标 react.task.ActionTask）：解析 Reason 阶段
// 的指令，调用业务工具，并将 Observation 回填至上下文。
type ActionTask struct {
	cfg *Config
}

// Name 任务标识。
func (t *ActionTask) Name() string { return IDAction }

// run 单轮动作执行（对标 ActionTask.run，全语义）。
func (t *ActionTask) run(trace *Trace, context flow.Context) error {
	// 重置默认路由
	trace.SetRoute(IDReason)

	lastReason := trace.LastReasonMessage()
	if lastReason == nil {
		return nil
	}

	var exchangers []*ToolExchanger

	defer func() {
		// finally：不挂起时才推 ActionEnd（避免前端误判本轮 Action 已执行完毕）
		if !trace.Session().IsPending() {
			for _, ic := range trace.Options().Interceptors {
				if ic != nil && ic.IsEnabled() {
					ic.OnActionEnd(trace, exchangers)
				}
			}
			if trace.HasStreamSink() {
				trace.PushAgentEvent(trace.event(agent.KindActionEnd))
			}
		}
		// 刷新快照
		trace.Session().UpdateSnapshot()
	}()

	if len(lastReason.ToolCalls) > 0 {
		// 1. 优先处理原生工具调用（Native Tool Calls）
		t.processNativeToolCall(lastReason, trace, &exchangers)
	} else {
		// 2. 文本模式：解析模型输出中的 Action 块
		t.processTextModeAction(lastReason, trace, &exchangers)
	}
	return nil
}

// processNativeToolCall 处理标准 ToolCall 协议调用。
func (t *ActionTask) processNativeToolCall(lastReason *ai.AssistantMessage, trace *Trace, exchangers *[]*ToolExchanger) {
	for i := range lastReason.ToolCalls {
		call := &lastReason.ToolCalls[i]
		// 拷贝参数，避免 HITL 改参加污染 ToolCall.arguments（会话/审计看到的是模型原始参数）
		args := map[string]any{}
		for k, v := range call.Arguments {
			args[k] = v
		}
		exchanger := NewToolExchanger(toolCallUuid(call), call.Name, args)
		exchanger.setToolCall(call)
		*exchangers = append(*exchangers, exchanger)
	}

	for _, ic := range trace.Options().Interceptors {
		if ic != nil && ic.IsEnabled() {
			ic.OnActionStart(trace, *exchangers)
		}
	}

	if trace.Session().IsPending() || trace.Route() == IDEnd {
		return
	}

	if trace.HasStreamSink() {
		trace.PushAgentEvent(actionStartEvent(trace, *exchangers))
	}

	var toolResults []*ai.Message
	for _, exchanger := range *exchangers {
		result := t.doAction(trace, exchanger, &toolResults)
		if result == nil {
			// pending / critical：不写不完整的成套 WM，交给上层
			return
		}
		// Feedback 等已终止路径：停止后续工具，但仍要落库已执行部分
		if trace.Route() == IDEnd {
			break
		}
	}

	if len(toolResults) > 0 {
		// 确保「成套」出现，避免错位
		trace.WorkingMemory().AddMessage(&ai.Message{Role: ai.RoleAssistant, Content: lastReason})
		trace.WorkingMemory().AddMessage(toolResults...)
	}

	// 本轮全部 returnDirect 且成功时直接结束（对齐 Chat 层）
	applyReturnDirectIfEligible(trace, *exchangers)
}

// processTextModeAction 解析并执行文本模式下的 Action 指令（逐个执行并即时
// 回填与反馈）。
func (t *ActionTask) processTextModeAction(lastReason *ai.AssistantMessage, trace *Trace, exchangers *[]*ToolExchanger) {
	lastContent := lastReason.Text
	if lastContent == "" {
		return
	}

	var toolResults []*ai.Message
	index := 0
	actionLabelIndex := strings.Index(lastContent, "Action:")

	if actionLabelIndex >= 0 {
		// 尝试寻找 JSON 起始位置
		jsonStart := strings.IndexByte(lastContent[actionLabelIndex+7:], '{')
		if jsonStart >= 0 {
			// 情况 A：JSON 模式流式解析（successive objects: {name, arguments}）
			decoder := json.NewDecoder(strings.NewReader(lastContent[actionLabelIndex+7+jsonStart:]))
			for {
				var node map[string]any
				if err := decoder.Decode(&node); err != nil {
					if err != io.EOF {
						// 解析异常回传
						toolResults = append(toolResults,
							ai.OfUser("Observation: Error parsing Action JSON: "+err.Error()))
					}
					break
				}
				toolName, _ := node["name"].(string)
				args := map[string]any{}
				if rawArgs, ok := node["arguments"].(map[string]any); ok {
					for k, v := range rawArgs {
						args[k] = v
					}
				}
				callID := toolName + "-" + itoa(index)
				index++
				*exchangers = append(*exchangers, NewToolExchanger(callID, toolName, args))
			}
		} else {
			// 情况 B：纯文本模式 Action: toolName
			toolName := strings.TrimSpace(lastContent[actionLabelIndex+7:])
			if trace.Options().Tool(toolName) != nil || toolName == FeedbackToolName {
				callID := toolName + "-" + itoa(index)
				index++
				*exchangers = append(*exchangers, NewToolExchanger(callID, toolName, map[string]any{}))
			}
		}
	}

	if len(*exchangers) == 0 {
		// 模型声明了 Action 但未解析成功，或无 Action 声明
		if actionLabelIndex >= 0 {
			toolResults = append(toolResults, ai.OfUser(
				"Observation: No valid Action format detected. Use JSON: {\"name\": \"...\", \"arguments\": {}}"))
		}
		if len(toolResults) > 0 {
			trace.WorkingMemory().AddMessage(&ai.Message{Role: ai.RoleAssistant, Content: lastReason})
			trace.WorkingMemory().AddMessage(toolResults...)
		}
		return
	}

	for _, ic := range trace.Options().Interceptors {
		if ic != nil && ic.IsEnabled() {
			ic.OnActionStart(trace, *exchangers)
		}
	}

	if trace.Session().IsPending() || trace.Route() == IDEnd {
		return
	}

	if trace.HasStreamSink() {
		trace.PushAgentEvent(actionStartEvent(trace, *exchangers))
	}

	for _, exchanger := range *exchangers {
		result := t.doAction(trace, exchanger, &toolResults)
		if result == nil {
			return
		}
		if trace.Route() == IDEnd {
			break
		}
	}

	if len(toolResults) > 0 {
		// 确保「成套」出现，避免错位
		trace.WorkingMemory().AddMessage(&ai.Message{Role: ai.RoleAssistant, Content: lastReason})
		trace.WorkingMemory().AddMessage(toolResults...)
	}

	applyReturnDirectIfEligible(trace, *exchangers)
}

// doAction 执行单个工具调用（100% 强物理闭环——对标 ActionTask.doAction）。
func (t *ActionTask) doAction(trace *Trace, exchanger *ToolExchanger, toolResults *[]*ai.Message) *ai.ToolResult {
	// 1. 触发前置生命周期
	for _, ic := range trace.Options().Interceptors {
		if ic != nil && ic.IsEnabled() {
			ic.OnToolCallStart(trace, exchanger)
		}
	}

	// 2. 前置拦截器直接挂起或截断路由时立刻退出（交给外层 defer 闭环）
	if trace.Session().IsPending() || trace.Route() == IDEnd {
		return nil
	}

	// 3. 推送流式动作片
	if trace.HasStreamSink() {
		trace.PushAgentEvent(toolCallStartEvent(trace, exchanger))
	}

	startMs := time.Now()
	var thrown error

	// 与 Java 的差异：Java 把拦截器异常 rethrow 上抛中断整个 agent 调用；Go 以
	// recover 收敛为 critical error 观测（避免 panic 打穿进程），其余语义一致。
	func() {
		defer func() {
			if r := recover(); r != nil {
				thrown = fmt.Errorf("%v", r)
			}
		}()
		// 4. 执行工具调用（HITL reject/skip 等会预填 exchanger.toolResult；
		//    有预填则跳过真实执行）
		var result *ai.ToolResult
		if exchanger.ToolResult() == nil {
			result = t.executeTool(trace, exchanger)
		} else {
			result = exchanger.ToolResult()
		}

		if result != nil && !trace.Session().IsPending() {
			// 保留完整 ToolResult（blocks/isError/metas）
			exchanger.SetToolResult(result)
		}
	}()

	// ================== finally：统一清理与下发 ==================
	durationMs := time.Since(startMs).Milliseconds()

	// Fallback 单工具挂起：不进执行、不推假 ToolCallEnd；仅调拦截器清理
	pendingWithoutResult := thrown == nil &&
		exchanger.ToolResult() == nil &&
		trace.Session() != nil && trace.Session().IsPending()

	if pendingWithoutResult {
		for _, ic := range trace.Options().Interceptors {
			if ic != nil && ic.IsEnabled() {
				ic.OnToolCallEnd(trace, exchanger, nil, nil, durationMs)
			}
		}
	} else {
		var observationMessage *ai.Message
		call := exchanger.ToolCall()

		switch {
		case thrown != nil:
			if call == nil {
				observationMessage = ai.OfUser("Observation: Execution critical error: " + thrown.Error())
			} else {
				observationMessage = ai.OfTool(
					ai.ToolResultErr("Execution critical error: "+thrown.Error()),
					call.Name, call.ID, false)
			}
		case exchanger.ToolResult() != nil:
			observationMessage = buildObservationMessage(call, exchanger)
		}

		// 无论正常结束还是 critical error，走统一清理与下发逻辑
		handleSingleObservation(trace, exchanger, observationMessage, durationMs, thrown, toolResults)
	}

	// 最终返回当前轮次处理后的最新观测值（完整结果，供 process* 判空）
	if thrown != nil {
		return nil
	}
	return exchanger.ToolResult()
}

// buildObservationMessage 用完整 ToolResult 构造 observation（保留
// media/isError/metas）。
func buildObservationMessage(call *ai.ToolCall, exchanger *ToolExchanger) *ai.Message {
	toolResult := exchanger.ToolResult()
	if call == nil {
		// 文本模式：无 ToolCall id，退化为 User observation（文本投影）
		text := ""
		if toolResult != nil {
			text = toolResult.ContentText()
			if text == "" && len(toolResult.Blocks) > 0 {
				if data, err := json.Marshal(toolResult.Blocks); err == nil {
					text = string(data)
				}
			}
		}
		return ai.OfUser("Observation: " + text)
	}
	return ai.OfTool(toolResult, call.Name, call.ID, exchanger.IsReturnDirect())
}

// handleSingleObservation 统一 Observation 落地逻辑（对标 ActionTask.
// handleSingleObservation）：拦截器 → 按最终结果重建 → WM/事件下发。
func handleSingleObservation(trace *Trace, toolExchanger *ToolExchanger,
	observationMessage *ai.Message, durationMs int64, err error, toolResults *[]*ai.Message) {

	// 先走拦截器（HITL 可在 OnToolCallEnd 注入批准 Note 到 exchanger.result）
	for _, ic := range trace.Options().Interceptors {
		if ic != nil && ic.IsEnabled() {
			ic.OnToolCallEnd(trace, toolExchanger, observationMessage, err, durationMs)
		}
	}

	// 拦截器可能改写了 result（如批准 Note）：按最终 toolResult 重建 observation
	if err == nil && toolExchanger.ToolResult() != nil {
		observationMessage = rebuildObservationIfNeeded(observationMessage, toolExchanger)
	}

	if observationMessage == nil {
		if err == nil {
			err = errors.New("The tool task has been interrupted or pending.")
		}
		observationMessage = ai.OfAssistant("")
	} else if toolResults != nil {
		*toolResults = append(*toolResults, observationMessage)
	}

	// 流式客户端通知闭环（使用最终 observation，保留 ToolResult.error 语义）
	if trace.HasStreamSink() {
		toolResult := toolExchanger.ToolResult()
		resultError := toolResult != nil && toolResult.IsError
		trace.PushAgentEvent(toolCallEndEvent(trace, toolExchanger, observationMessage, resultError, err, durationMs))
	}
}

// rebuildObservationIfNeeded 若拦截器改写了 exchanger 文本，则按最终 toolResult
// 重建 observation（保留 media/isError）。
func rebuildObservationIfNeeded(observationMessage *ai.Message, toolExchanger *ToolExchanger) *ai.Message {
	toolResult := toolExchanger.ToolResult()
	if toolResult == nil {
		return observationMessage
	}

	finalContent := toolResult.ContentText()
	textMode := observationMessage == nil
	if s, ok := observationMessage.Content.(string); ok && strings.HasPrefix(s, "Observation: ") {
		textMode = true
	}

	if textMode {
		projected := finalContent
		if projected == "" && len(toolResult.Blocks) > 0 {
			if data, err := json.Marshal(toolResult.Blocks); err == nil {
				projected = string(data)
			}
		}
		expected := "Observation: " + projected
		if observationMessage != nil {
			if s, ok := observationMessage.Content.(string); ok && expected == s {
				return observationMessage
			}
		}
		return ai.OfUser(expected)
	}

	// native ToolMessage：content 与 returnDirect 均未变则复用（拦截器未改写）；
	// 任一变化则按最终 toolResult 重建，保留 media/isError
	if tm, ok := observationMessage.Content.(*ai.ToolMessage); ok && tm != nil {
		content := ""
		if tm.Result != nil {
			content = tm.Result.ContentText()
		}
		if content == finalContent && tm.ReturnDirect == toolExchanger.IsReturnDirect() {
			return observationMessage
		}
		return ai.OfTool(toolResult, toolExchanger.ToolName(), tm.CallID, toolExchanger.IsReturnDirect())
	}

	return ai.OfTool(toolResult, toolExchanger.ToolName(), "", toolExchanger.IsReturnDirect())
}

// executeTool 查找并执行工具（对标 ActionTask.executeTool）。returnDirect 仅在
// 真实成功后标记到 exchanger；循环收口由 applyReturnDirectIfEligible 统一判定。
func (t *ActionTask) executeTool(trace *Trace, exchanger *ToolExchanger) *ai.ToolResult {
	if exchanger.ToolName() == FeedbackToolName {
		// Feedback：保留独立结束语义（interrupt + abnormal FinalAnswer），不走通用 returnDirect 汇总
		reason, _ := exchanger.Args()["reason"].(string)
		trace.SetRoute(IDEnd)
		trace.SetFinalAnswer(reason)
		if ctx := trace.Context(); ctx != nil {
			ctx.Interrupt()
		}
		return ai.ToolResultOf(reason)
	}

	opts := trace.Options()
	tool := opts.Tool(exchanger.ToolName())
	if tool == nil {
		tool = trace.ProtocolTool(exchanger.ToolName())
	}

	if tool != nil {
		// 合并工具上下文和参数，形成请求
		toolReq := ai.NewToolRequest(opts.ToolContext, exchanger.Args())

		var result *ai.ToolResult
		if len(opts.Interceptors) == 0 {
			result = t.callToolDirect(tool, toolReq)
		} else {
			ics := make([]ai.Interceptor, 0, len(opts.Interceptors))
			for _, ic := range opts.Interceptors {
				ics = append(ics, ic)
			}
			chain := ai.NewToolChain(ics, func(req *ai.ToolRequest) *ai.ToolResult {
				return t.callToolDirect(tool, req)
			})
			result = chain.DoIntercept(toolReq)
		}
		trace.IncrementToolCallCount()

		// 无人接手的类别标注错误 → Java catch 同款文案（ToolRetry 已处理时
		// 结果非 error、无标注，不会进此分支）
		if result != nil && result.IsError {
			if class, _ := result.Meta[metaErrClass].(string); class != "" {
				msg := result.ContentText()
				if class == errClassArgs {
					result = ai.ToolResultErr("Invalid arguments for [" + exchanger.ToolName() +
						"]. Expected Schema: " + tool.InputSchema() + ". Error: " + msg)
				} else {
					result = ai.ToolResultErr("Execution error in tool [" + exchanger.ToolName() + "]: " + msg)
				}
			}
		}

		// 仅「真实成功」标记：声明 returnDirect、非 error、且有可交付内容
		//（文本非空或含 media）；空串无 media 不标记
		if tool.ReturnDirect() && isSuccessfulDeliverable(result) {
			exchanger.SetReturnDirect(true)
		}

		return result
	}

	return ai.ToolResultErr("Tool [" + exchanger.ToolName() + "] not found.")
}

// callToolDirect chain 终点：直调 tool.Handle（不经 tool.Call 的 "Error: " 前缀
// 转换，保留错误类别判定；panic recover——对标 Java call 的Throwable 上抛由
// chain/executeTool 分类收口）。与 tool.Call 的差异：ToolDesc.ResultConverter
// 不参与（结果统一 resultConvert 序列化）。
func (t *ActionTask) callToolDirect(tool ai.FunctionTool, req *ai.ToolRequest) *ai.ToolResult {
	ctx := ai.ContextWithToolContext(context.Background(), req.ToolContext())

	var result any
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panic: %v", r)
			}
		}()
		result, err = tool.Handle(ctx, req.Args())
	}()
	if err != nil {
		class := errClassPhysical
		if errors.Is(err, ErrInvalidArguments) {
			class = errClassArgs
		}
		return &ai.ToolResult{
			Text:    err.Error(),
			IsError: true,
			Meta:    map[string]any{metaErrClass: class, metaErrTool: tool.Name()},
		}
	}
	return ai.ToolResultOf(resultConvert(result))
}

// resultConvert 工具结果字符串化（同 ai 内部规则：nil→空、string 直出、其余
// JSON 序列化）。
func resultConvert(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(data)
}

// toolCallUuid ToolCall 的调用标识（id 优先，流式聚合键兜底）。
func toolCallUuid(call *ai.ToolCall) string {
	if call.ID != "" {
		return call.ID
	}
	return call.Index
}

// isSuccessfulDeliverable 结果是否「真实成功且可交付」：非 nil、非 error，且
// （文本非空 或 含非文本 media）。纯 media（生图）允许直返。
func isSuccessfulDeliverable(result *ai.ToolResult) bool {
	if result == nil || result.IsError {
		return false
	}
	if result.ContentText() != "" {
		return true
	}
	return hasMediaBlocks(result.Blocks)
}

// applyReturnDirectIfEligible 本轮工具全部 returnDirect 且均成功时，将结果拼接
// 为 FinalAnswer 并 END（对齐 Chat 层 buildToolMessage 的全员直返判定）。
func applyReturnDirectIfEligible(trace *Trace, exchangers []*ToolExchanger) {
	if trace.Session().IsPending() || trace.Route() == IDEnd {
		return
	}
	if len(exchangers) == 0 {
		return
	}

	var joined strings.Builder
	var mediaBlocks []ai.ContentBlock

	for _, exchanger := range exchangers {
		// isReturnDirect=false 覆盖：未声明、执行异常、ToolResult.error、空串、
		// HITL 预填跳过执行等
		if !exchanger.IsReturnDirect() {
			return
		}
		tr := exchanger.ToolResult()
		if tr == nil || !isSuccessfulDeliverable(tr) {
			return
		}

		if content := tr.ContentText(); content != "" {
			if joined.Len() > 0 {
				joined.WriteByte('\n')
			}
			joined.WriteString(content)
		}

		// 收集非文本 media，供终态 AssistantMessage 保留
		for _, block := range tr.Blocks {
			if block == nil {
				continue
			}
			if _, ok := block.(ai.TextBlock); !ok {
				mediaBlocks = append(mediaBlocks, block)
			}
		}
	}

	// 纯 media 时 finalAnswer 可为 ""，终态收口靠 finalMediaBlocks 合并
	trace.SetFinalAnswer(joined.String())
	trace.SetRoute(IDEnd)

	if len(mediaBlocks) > 0 {
		// 记到 trace 专用字段（不挂回 lastReason——那条是带 tool_call 的推理
		// 消息且已入 WorkingMemory，挂 media 会语义错位 + WM 重复落库）
		trace.SetFinalMediaBlocks(mediaBlocks)
	}
}

// ---- 事件构造 ----

func actionStartEvent(trace *Trace, exchangers []*ToolExchanger) *agent.Event {
	ev := trace.event(agent.KindActionStart)
	ev.Meta["toolCalls"] = exchangers
	return ev
}

func toolCallStartEvent(trace *Trace, exchanger *ToolExchanger) *agent.Event {
	ev := trace.event(agent.KindToolCallStart)
	ev.Meta["callId"] = exchanger.CallId()
	ev.Meta["toolName"] = exchanger.ToolName()
	ev.Meta["args"] = exchanger.Args()
	return ev
}

func toolCallEndEvent(trace *Trace, exchanger *ToolExchanger, observation *ai.Message,
	resultError bool, err error, durationMs int64) *agent.Event {
	ev := trace.event(agent.KindToolCallEnd)
	if observation != nil {
		ev.Text = observationText(observation)
		ev.Meta["observation"] = observation
	}
	ev.Meta["callId"] = exchanger.CallId()
	ev.Meta["toolName"] = exchanger.ToolName()
	ev.Meta["args"] = exchanger.Args()
	ev.Meta["durationMs"] = durationMs
	ev.Meta["resultError"] = resultError
	if err != nil {
		ev.Meta["error"] = err.Error()
	}
	return ev
}

// observationText 观测消息文本投影（Java ToolCallEndEvent.getText）。
func observationText(m *ai.Message) string {
	switch c := m.Content.(type) {
	case string:
		return c
	case *ai.ToolMessage:
		return c.ResultText()
	case *ai.AssistantMessage:
		return c.Text
	}
	return ""
}
