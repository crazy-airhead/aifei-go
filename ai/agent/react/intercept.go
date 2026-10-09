package react

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	ai "github.com/crazy-airhead/aifei-go/ai"
)

// StopLoop 死循环检测拦截器缺省名下 extras 键。
const (
	extrasStopLoopHistory = "stoploop_history"
	extrasStopLoopPending = "stoploop_pending_prompt"
	extrasStopLoopTurn    = "stoploop_pending_turn"
)

// StopLoopInterceptor 避免逻辑死循环拦截器（Loop Breaker，对标
// StopLoopInterceptor）：监控动作意图的滑动窗口频率，防止智能体陷入连续重复
// 或 A-B-A-B 型往复迭代。
type StopLoopInterceptor struct {
	InterceptorBase

	maxRepeatCount int
	windowSize     int
}

// NewStopLoopInterceptor 缺省（3 次重复 / 8 窗口——稍微放大窗口，增加容错）。
func NewStopLoopInterceptor() *StopLoopInterceptor {
	return NewStopLoopInterceptorOf(3, 8)
}

// NewStopLoopInterceptorOf 自定义阈值（maxRepeatCount>=2；
// windowSize>=max(4, windowSize, maxRepeatCount)——Java 构造钳制同款）。
func NewStopLoopInterceptorOf(maxRepeatCount, windowSize int) *StopLoopInterceptor {
	if maxRepeatCount < 2 {
		maxRepeatCount = 2
	}
	if windowSize < 4 {
		windowSize = 4
	}
	if windowSize < maxRepeatCount {
		windowSize = maxRepeatCount
	}
	return &StopLoopInterceptor{maxRepeatCount: maxRepeatCount, windowSize: windowSize}
}

// OnReasonEnd 动作指纹入窗统计；超阈值时挂起自纠提示（由 OnActionEnd 在本轮
// Observation 落库后注入）。注意：ReasonTask 的 OnReasonEnd 回调不带 isEnabled
// 判定（Java 同款），故此处自检。
func (i *StopLoopInterceptor) OnReasonEnd(trace *Trace, _ *ai.Response, assistantMessage *ai.AssistantMessage, _ int64) {
	if !i.IsEnabled() || assistantMessage == nil {
		return
	}

	fingerprint := i.generateNormalizedFingerprint(assistantMessage)
	if fingerprint == "" {
		return
	}

	var history []string
	if h, ok := trace.Extra(extrasStopLoopHistory).([]string); ok {
		history = h
	}
	history = append(history, fingerprint)
	if overflow := len(history) - i.windowSize; overflow > 0 {
		history = history[overflow:]
	}
	trace.SetExtra(extrasStopLoopHistory, history)

	count := 0
	for _, fp := range history {
		if fp == fingerprint {
			count++
		}
	}

	if count >= i.maxRepeatCount {
		breakMsg := fmt.Sprintf(
			"【系统提示：检测到潜在循环 (Loop Detected)】\n"+
				"系统检测到最近 %d 个动作的窗口内，当前操作已出现 %d 次，可能存在\"原地打转\"的情况。\n\n"+
				"请立即暂停当前操作思路，审视历史 Observation 并执行以下步骤：\n"+
				"1. **有效性评估**：如果最近的尝试没有带来有效新线索，说明当前策略已失效，请必须更换思路或换个角度切入。\n"+
				"2. **强制收敛**：若已有足够信息供用户参考，请直接输出 Final Answer 结束任务。\n"+
				"3. **避免重复**：不要重复执行已经执行过且未产生新信息的操作。",
			i.windowSize, count)

		// Action 尚未执行；绑定当前轮次并延后注入，避免陈旧提醒被后续 Action 消费。
		trace.SetExtra(extrasStopLoopPending, breakMsg)
		trace.SetExtra(extrasStopLoopTurn, trace.TurnCount())

		// 清除计数，避免连续重复注入提醒
		trace.SetExtra(extrasStopLoopHistory, []string{})
	}
}

// OnActionEnd 本轮 Observation 已写入后注入挂起的自纠提示（作为下一轮最新
// 指令；仅同轮次且未 END 时）。
func (i *StopLoopInterceptor) OnActionEnd(trace *Trace, _ []*ToolExchanger) {
	breakMsg, _ := trace.Extra(extrasStopLoopPending).(string)
	pendingTurn, _ := trace.Extra(extrasStopLoopTurn).(int)
	i.clearPendingPrompt(trace)

	if breakMsg != "" && pendingTurn == trace.TurnCount() && trace.Route() != IDEnd {
		trace.WorkingMemory().AddMessage(ai.OfUser(breakMsg))
	}
}

// OnAgentEnd 任务结束，清掉未消费的挂起提示。
func (i *StopLoopInterceptor) OnAgentEnd(trace *Trace) {
	i.clearPendingPrompt(trace)
}

func (i *StopLoopInterceptor) clearPendingPrompt(trace *Trace) {
	delete(trace.Extras(), extrasStopLoopPending)
	delete(trace.Extras(), extrasStopLoopTurn)
}

// generateNormalizedFingerprint 动作意图规范化指纹：工具名+规范化参数定义动作
// 身份（调用 id 等协议字段不参与）；文本模式只跟踪 Action（普通文本直接结束
// 任务，不构成动作循环）。
func (i *StopLoopInterceptor) generateNormalizedFingerprint(message *ai.AssistantMessage) string {
	if len(message.ToolCalls) > 0 {
		parts := make([]string, 0, len(message.ToolCalls))
		for _, call := range message.ToolCalls {
			parts = append(parts, "tool:"+call.Name+"|args:"+i.canonicalArguments(&call))
		}
		return strings.Join(parts, "|")
	}
	if message.Text != "" {
		if actionIdx := strings.Index(message.Text, "Action:"); actionIdx >= 0 {
			return i.canonicalTextAction(strings.TrimSpace(message.Text[actionIdx:]))
		}
	}
	return ""
}

// canonicalArguments 工具调用参数规范化：ArgumentsStr 单根 JSON 对象 →
// canonical JSON（键排序）；非法、流式截断或多根参数按原文区分；空串回落
// Arguments map 的 canonical 形式。
func (i *StopLoopInterceptor) canonicalArguments(call *ai.ToolCall) string {
	if raw := strings.TrimSpace(call.ArgumentsStr); raw != "" {
		var node map[string]any
		if err := json.Unmarshal([]byte(raw), &node); err == nil {
			return canonicalJSON(node)
		}
		return "raw:" + raw
	}
	if call.Arguments == nil {
		return canonicalJSON(map[string]any{})
	}
	return canonicalJSON(call.Arguments)
}

// canonicalTextAction 文本 Action 指纹：与 ActionTask 保持同一语义，只采用每个
// 对象中的 name 与 arguments；无可解析对象时退回原文。
func (i *StopLoopInterceptor) canonicalTextAction(action string) string {
	braceIdx := strings.IndexByte(action, '{')
	if braceIdx < 0 {
		return action
	}

	decoder := json.NewDecoder(strings.NewReader(action[braceIdx:]))
	var parts []string
	for {
		var node map[string]any
		if err := decoder.Decode(&node); err != nil {
			break
		}
		args, ok := node["arguments"].(map[string]any)
		if !ok {
			args = map[string]any{}
		}
		parts = append(parts, "tool:"+jsonScalarString(node["name"])+"|args:"+canonicalJSON(args))
	}
	if len(parts) == 0 {
		return action
	}
	return strings.Join(parts, "|")
}

// canonicalJSON 规范化 JSON 文本：encoding/json 对 map 按键排序序列化，天然
// 满足 Java 手写排序的 canonical 语义。
func canonicalJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(data)
}

// jsonScalarString 非字符串标量的字符串投影（对标 ONode.getString 宽松转换）。
func jsonScalarString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// ToolRetryInterceptor 工具执行重试拦截器（Tool Resilience，对标
// ToolRetryInterceptor）：物理错误线性退避重试 + 参数错误自愈反馈。实现
// ai.ToolInterceptor，经 ai.ToolChain 参与工具执行（react 拦截器面同时保留）。
type ToolRetryInterceptor struct {
	InterceptorBase

	maxRetries   int
	retryDelayMs int64
}

// NewToolRetryInterceptor 缺省（3 次 / 1000ms）。
func NewToolRetryInterceptor() *ToolRetryInterceptor {
	return NewToolRetryInterceptorOf(3, 1000)
}

// NewToolRetryInterceptorOf 自定义参数（maxRetries>=1、retryDelayMs>=500——
// Java 构造钳制同款）。
func NewToolRetryInterceptorOf(maxRetries int, retryDelayMs int64) *ToolRetryInterceptor {
	if maxRetries < 1 {
		maxRetries = 1
	}
	if retryDelayMs < 500 {
		retryDelayMs = 500
	}
	return &ToolRetryInterceptor{maxRetries: maxRetries, retryDelayMs: retryDelayMs}
}

// InterceptTool 工具执行拦截。Go 版错误类别由 chain 终点标注在
// ToolResult.Meta（args/physical，对标 Java 的异常类型分派）：
//   - 参数类错误（ErrInvalidArguments）：重试物理链路无意义，直接回馈修正
//     提示（非 error 结果）；
//   - 物理错误：线性退避重试（delay*(i+1)），末次尝试的异常文案直接返回
//     （非 error 结果，喂给模型自纠或终止）。
func (i *ToolRetryInterceptor) InterceptTool(req *ai.ToolRequest, chain *ai.ToolChain) *ai.ToolResult {
	toolName := ""
	for attempt := 0; attempt < i.maxRetries; attempt++ {
		result := chain.DoIntercept(req)
		if result == nil || !result.IsError {
			return result
		}

		class, _ := result.Meta[metaErrClass].(string)
		toolName, _ = result.Meta[metaErrTool].(string)
		msg := result.ContentText()

		if class == errClassArgs {
			// 参数自愈：逻辑错误直接回馈给模型，提示其修正参数
			return ai.ToolResultOf("Invalid arguments for tool [" + toolName + "]: " + msg +
				". Please fix the arguments and try again.")
		}
		if class != errClassPhysical {
			// 非 chain 终点标注的错误（工具自身/其它拦截器构造的 error 结果）：
			// Java 中不会以异常形式抛出，原样放行
			return result
		}

		if attempt == i.maxRetries-1 {
			return ai.ToolResultOf("Execution error in tool [" + toolName + "]: " + msg)
		}

		// 线性退避，与 ReasonTask 保持一致
		time.Sleep(time.Duration(i.retryDelayMs*(int64(attempt)+1)) * time.Millisecond)
	}

	return ai.ToolResultOf("Tool [" + toolName + "] failed with unknown error.")
}
