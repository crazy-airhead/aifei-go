// supervisor.go 团队协作指挥任务（对标 team.task.SupervisorTask）：协调中枢，
// 基于 LLM 推理结果进行任务分发、成员调度及协作终止判定。决策文本解析为物理
// 路由的状态机收口（FINISH 守卫/回派/极端终止）全部保留。
package team

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/ai/agent/react"
	"github.com/crazy-airhead/aifei-go/flow"
)

var (
	// errLlmNoReturn 模型空返回（对标 LlmNoReturnException；触发调度重试）。
	errLlmNoReturn = errors.New("team: The LLM did not return")

	// errStreamCancelled 流式消费方取消（takeUntil(cancelled) 的 Go 对应）。
	errStreamCancelled = errors.New("team: stream cancelled by consumer")
)

// assignVerbRe 显式指派句式动词（指派/派发/先派/再派/派/next/route to）。
var assignVerbRe = regexp.MustCompile(`(?i)(指派(?:专家)?|派发|先派|再派|派|next(?:\s+agent)?|route(?:\s+to)?)`)

// SupervisorTask 团队协作指挥任务。
type SupervisorTask struct {
	config *Config
}

// NewSupervisorTask 构建指挥任务。
func NewSupervisorTask(config *Config) *SupervisorTask { return &SupervisorTask{config: config} }

// Name 任务标识（supervisor）。
func (t *SupervisorTask) Name() string { return IDSupervisor }

// Title 同名。
func (t *SupervisorTask) Title() string { return IDSupervisor }

// Run flow 节点运行实现。
func (t *SupervisorTask) Run(ctx flow.Context, node *flow.Node) error {
	trace := Current(ctx)
	if trace == nil {
		return nil // Java: LOG.error + return
	}

	err := t.run(trace, ctx, node)
	if err != nil {
		t.handleError(ctx, err)
	}
	return nil // 异常已收敛（Java catch Throwable → handleError）
}

func (t *SupervisorTask) run(trace *Trace, ctx flow.Context, node *flow.Node) error {
	// 1. 拦截器准入检查
	for _, ic := range trace.Options().Interceptors {
		if ic == nil || !ic.IsEnabled() {
			continue
		}
		if !ic.ShouldSupervisorContinue(trace) {
			trace.AddRecord(ai.RoleSystem, IDSupervisor, "[Skipped] Intercepted by "+ic.Name(), 0)
			if trace.Route() == IDSupervisor {
				t.routeTo(ctx, trace, agent.IDEnd)
			}
			return nil
		}
	}

	// 2. 协作熔断检查：达到最大迭代次数或已标记结束则退出
	if trace.Route() == agent.IDEnd || trace.TurnCount() >= trace.Options().MaxTurns {
		trace.AddRecord(ai.RoleSystem, IDSystem, "[Terminated] Max turns reached", 0)
		t.routeTo(ctx, trace, agent.IDEnd)
		return nil
	}

	// 3. 协议逻辑检查：由特定协议决定是否继续执行 Supervisor
	if !t.config.ProtocolWide().ShouldSupervisorExecute(ctx, trace) {
		if trace.Route() == IDSupervisor {
			t.routeTo(ctx, trace, agent.IDEnd)
		}
		return nil
	}

	return t.dispatch(node, ctx, trace)
}

// dispatch 构建 Prompt 并调用模型进行调度决策。
func (t *SupervisorTask) dispatch(node *flow.Node, ctx flow.Context, trace *Trace) error {
	// 组装系统提示词（基础模版 + 协议扩展）
	var protocolExt strings.Builder
	t.config.ProtocolWide().PrepareSupervisorInstruction(ctx, trace, &protocolExt)
	basePrompt := t.config.SystemPromptFor(trace, ctx)
	finalSystemPrompt := basePrompt
	if protocolExt.Len() > 0 {
		finalSystemPrompt = basePrompt + "\n\n" + protocolExt.String()
	}

	// 组装用户输入（协作历史、当前轮次、候选成员）
	var userContent strings.Builder
	t.config.ProtocolWide().PrepareSupervisorContext(ctx, trace, &userContent)
	windowSize := trace.Options().RecordWindowSize

	userContent.WriteString("## 协作进度 (最近 " + itoa(windowSize) + " 轮历史)\n")
	userContent.WriteString(trace.FormattedHistoryOf(windowSize))
	userContent.WriteString("\n\n---\n")
	userContent.WriteString("当前迭代轮次: " + itoa(trace.NextTurn()) + "\n")
	userContent.WriteString("指令：请指派下一位执行者。已完成则输出 " + t.config.FinishMarker())

	// 过滤已参与成员，提示待命专家
	participated := map[string]bool{}
	for _, r := range trace.Records() {
		if r.IsAgent() {
			participated[strings.ToLower(r.Source())] = true
		}
	}
	var remaining []string
	for _, name := range t.config.AgentNames() {
		if !participated[strings.ToLower(name)] {
			remaining = append(remaining, name)
		}
	}
	if len(remaining) > 0 {
		userContent.WriteString("\n> **待命专家**：[" + strings.Join(remaining, ", ") + "].")
	}

	messages := []*ai.Message{ai.OfSystem(finalSystemPrompt)}
	messages = append(messages, trace.WorkingMemory().Messages()...)
	messages = append(messages, ai.OfUser(userContent.String()))

	response, err := t.callWithRetry(node, trace, messages)
	if err != nil {
		return err // 上抛交 Run → handleError（Java catch Throwable 同款）
	}
	if response == nil || trace.Session().IsPending() {
		return nil
	}

	responseMessage := response.Message()

	if response.Usage() != nil {
		trace.Metrics().AddUsage(response.Usage())
	}

	for _, ic := range trace.Options().Interceptors {
		if ic != nil && ic.IsEnabled() {
			ic.OnModelEnd(trace, response)
		}
	}

	if trace.Session().IsPending() {
		return nil
	}
	if responseMessage == nil {
		t.handleUnresolvableDecision(ctx, trace, "", "empty supervisor response")
		return nil
	}

	// 优先从正文提取决策；若模型走 feedback 工具，仅在正文存在「显式指派成员名」
	// 时抢路由
	clearContent := responseMessage.Text
	decision := strings.TrimSpace(clearContent)

	for i := range responseMessage.ToolCalls {
		call := &responseMessage.ToolCalls[i]
		if call.Name != react.FeedbackToolName {
			continue
		}
		reason := ""
		if call.Arguments != nil {
			if v, ok := call.Arguments["reason"]; ok && v != nil {
				reason = stringValue(v)
			}
		}

		// 模型常一边写「指派/派 天气专家」一边误调 feedback。
		// 正文或 reason 中若存在显式指派句式，优先继续协作。
		assigned := t.extractAssignedAgentName(decision)
		if assigned == "" {
			assigned = t.extractAssignedAgentName(reason)
		}
		if assigned != "" {
			t.routeTo(ctx, trace, assigned)
			return nil
		}

		// 真正需要外部反馈：挂起
		decision = react.AsSuspendDecision(reason)
		break
	}

	trace.SetLastDecision(decision)

	for _, ic := range trace.Options().Interceptors {
		if ic != nil && ic.IsEnabled() {
			ic.OnSupervisorDecision(trace, decision)
		}
	}

	if trace.Session().IsPending() {
		return nil
	}

	t.commitRoute(trace, decision, ctx)
	return nil
}

// commitRoute 将决策文本解析为物理路由目标（状态机收口）：
//   - FINISH 且 shouldSupervisorRoute=true → END
//   - FINISH 但被协议拒绝 → 禁止 fall-through 误入 END；尝试回派成员继续协作
//   - 无法解析的 decision → 不静默 END；优先回派可执行成员以重新进入循环
func (t *SupervisorTask) commitRoute(trace *Trace, decision string, ctx flow.Context) {
	if react.IsSuspend(decision) {
		var payload struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal([]byte(decision), &payload)
		reason := payload.Reason

		// 仅当 reason 是「显式指派成员名」时才抢路由；
		// 普通「请用户补充目的地」里顺带提到专家名不算路由。
		assigned := t.extractAssignedAgentName(decision)
		if assigned == "" && reason != "" {
			assigned = t.extractAssignedAgentName(reason)
		}
		if assigned != "" {
			t.routeTo(ctx, trace, assigned)
			return
		}

		// 真正需要外部反馈：挂起流程并记录原因
		trace.SetFinalAnswer(reason)
		if trace.Session() != nil {
			trace.Session().Pending(true, reason)
		}
		// Java 原版此处 setRoute(ID_END)；但挂起 ≠ 结束——route=END 会在恢复续跑时
		// 命中 run() 的「已标记结束」熔断检查，导致恢复后 supervisor 直接终止、
		// 永不重新决策。挂起保持 route=supervisor（与 Sequential 成员挂起回写
		// agent 名同构）：恢复后 supervisor 重入决策循环；若停控失效继续走图，
		// exclusive 无命中链路也会落到 END 兜底。
		trace.SetRoute(IDSupervisor)
		if trace.Context() != nil {
			trace.Context().Interrupt()
		}
		return
	}

	if decision == "" {
		t.handleUnresolvableDecision(ctx, trace, decision, "empty supervisor decision")
		return
	}

	// 1. 优先尝试协议自定义路由解析（可含 FINISH 阻断时的硬回派）
	protoRoute := t.config.ProtocolWide().ResolveSupervisorRoute(ctx, trace, decision)
	if protoRoute != "" {
		t.routeTo(ctx, trace, protoRoute)
		return
	}

	// 2. FINISH 分支：允许结束则 END；拒绝则必须 return，禁止 fall-through
	finishMarker := t.config.FinishMarker()
	if strings.Contains(decision, finishMarker) {
		if t.config.ProtocolWide().ShouldSupervisorRoute(ctx, trace, decision) {
			finishRe := regexp.MustCompile(`(?s).*?` + regexp.QuoteMeta(finishMarker) + `[:\s]*(.*)`)
			finalAnswer := ""
			if m := finishRe.FindStringSubmatch(decision); m != nil {
				finalAnswer = strings.TrimSpace(m[1])
			}
			if finalAnswer == "" {
				finalAnswer = trace.LastAgentContent()
			}
			trace.SetFinalAnswer(finalAnswer)
			t.routeTo(ctx, trace, agent.IDEnd)
			return
		}

		// FINISH 被协议拒绝：不得误入 END
		if t.matchAgentRoute(ctx, trace, decision) {
			return
		}
		fallback := t.resolveContinueFallback(trace)
		if fallback != "" {
			trace.AddRecord(ai.RoleSystem, IDSystem,
				"[Routing] FINISH rejected by protocol; re-assign to ["+fallback+"]", 0)
			t.routeTo(ctx, trace, fallback)
			return
		}

		// 极端：无可回派成员（空团队）才允许 END，且必须留下原因
		if !trace.HasFinalAnswer() {
			trace.SetFinalAnswer(trace.LastAgentContent())
		}
		trace.AddRecord(ai.RoleSystem, IDSystem,
			"[Terminated] FINISH rejected and no agent available to continue", 0)
		t.routeTo(ctx, trace, agent.IDEnd)
		return
	}

	// 3. 匹配成员 Agent 路由
	if t.matchAgentRoute(ctx, trace, decision) {
		return
	}

	// 4. 无法解析：禁止静默 END
	t.handleUnresolvableDecision(ctx, trace, decision, "unable to extract agent name")
}

// handleUnresolvableDecision 解析失败/空决策时的统一处置：优先回派可执行成员
// 以重新进入协作循环，避免 exclusive 默认边误入 END。仅在确实无法继续时带原因
// 结束。
func (t *SupervisorTask) handleUnresolvableDecision(ctx flow.Context, trace *Trace, decision, reason string) {
	fallback := t.resolveContinueFallback(trace)
	if fallback != "" && trace.TurnCount() < trace.Options().MaxTurns {
		trace.AddRecord(ai.RoleSystem, IDSystem,
			"[Routing] "+reason+"; retry via ["+fallback+"]", 0)
		t.routeTo(ctx, trace, fallback)
		return
	}

	trace.AddRecord(ai.RoleSystem, IDSystem,
		"[Terminated] "+reason+": "+truncate(decision, 200), 0)
	if !trace.HasFinalAnswer() {
		last := trace.LastAgentContent()
		if last == "" {
			last = "Supervisor decision unresolved: " + reason
		}
		trace.SetFinalAnswer(last)
	}
	t.routeTo(ctx, trace, agent.IDEnd)
}

// resolveContinueFallback FINISH 被拒/解析失败时的继续执行回退：
// 上一位合法专家 → 队伍中第一位专家。
func (t *SupervisorTask) resolveContinueFallback(trace *Trace) string {
	last := trace.LastAgentName()
	if last != "" && t.config.HasAgent(last) {
		return t.config.NormalizeAgentName(last)
	}
	if names := t.config.AgentNames(); len(names) > 0 {
		return names[0]
	}
	return ""
}

func truncate(text string, max int) string {
	t := strings.TrimSpace(text)
	if len(t) <= max {
		return t
	}
	return t[:max] + "..."
}

// matchAgentRoute 模糊匹配文本中的 Agent 名称。
// 优先级：全文恰为成员名 → 显式指派句式 → 正文中最后出现的成员名（仅 agent
// name，不做宽泛角色匹配——主管决策/feedback 文案常复述成员角色，会误路由）。
func (t *SupervisorTask) matchAgentRoute(ctx flow.Context, trace *Trace, text string) bool {
	if text == "" {
		return false
	}

	// 移除 Markdown 格式字符（加粗/斜体的 * 和代码的 `），保留下划线
	cleanText := stripMarkdownMarks(text)
	if cleanText == "" {
		return false
	}

	// 1. 全文恰为成员名
	if t.config.HasAgent(cleanText) {
		t.routeTo(ctx, trace, t.config.NormalizeAgentName(cleanText))
		return true
	}

	// 2. 显式指派句式中的成员名
	if assigned := t.extractAssignedAgentName(cleanText); assigned != "" {
		t.routeTo(ctx, trace, assigned)
		return true
	}

	// 3. 成员名（agent name）最后一次出现
	if byName := t.findLastAgentName(cleanText); byName != "" {
		t.routeTo(ctx, trace, byName)
		return true
	}

	return false
}

// extractAssignedAgentName 从「指派/派发/派/next/route」类句式中提取目标 Agent
// 的规范名（支持成员 name 与角色 role——role 仅在显式指派句式中）。
// Java 版经 lookahead 表达词边界；RE2 无 lookahead，改为动词定位 + 手工
// 定界校验，语义等价（最近一次指派命中优先）。
func (t *SupervisorTask) extractAssignedAgentName(text string) string {
	if text == "" {
		return ""
	}
	cleanText := stripMarkdownMarks(text)

	// 1) agent name：指派动词 + name（避免中文角色后吞掉整句）
	assignedByName := ""
	assignedByNameIdx := -1
	for _, name := range t.config.AgentNames() {
		for _, at := range assignedAgentAfter(cleanText, name) {
			if at > assignedByNameIdx {
				assignedByNameIdx = at
				assignedByName = name
			}
		}
	}
	if assignedByName != "" {
		return t.config.NormalizeAgentName(assignedByName)
	}

	// 2) role：指派动词 + role（仅在显式指派句式中）
	assignedByRole := ""
	assignedByRoleIdx := -1
	for _, name := range t.config.AgentNames() {
		ag := t.config.Agent(name)
		if ag == nil {
			continue
		}
		role := ag.Role()
		if role == "" || utf8.RuneCountInString(role) < 2 {
			continue
		}
		for _, at := range assignedAgentAfter(cleanText, role) {
			if at > assignedByRoleIdx {
				assignedByRoleIdx = at
				assignedByRole = name
			}
		}
	}
	return assignedByRole
}

// assignedAgentAfter 返回 text 中「指派动词 + 分隔 + target + 词边界」命中的
// 动词起始位置列表（RE2 无 lookahead 的手工实现）。
func assignedAgentAfter(text, target string) []int {
	if target == "" {
		return nil
	}
	var out []int
	lowerText := strings.ToLower(text)
	lowerTarget := strings.ToLower(target)
	for _, loc := range assignVerbRe.FindAllStringIndex(text, -1) {
		verbStart, after := loc[0], loc[1]

		// 跳过动词后的分隔符：\s*[:：]?\s*（：为多字节 rune，前缀比对）
		for after < len(text) && isSpaceByte(text[after]) {
			after++
		}
		if after < len(text) && text[after] == ':' {
			after++
			for after < len(text) && isSpaceByte(text[after]) {
				after++
			}
		} else if strings.HasPrefix(text[after:], "：") {
			after += len("：")
			for after < len(text) && isSpaceByte(text[after]) {
				after++
			}
		}

		// 目标名匹配（大小写不敏感）+ 词边界（其后不得是 [a-zA-Z0-9_]）
		if after+len(target) > len(text) {
			continue
		}
		if !strings.EqualFold(lowerText[after:after+len(target)], lowerTarget) {
			continue
		}
		if end := after + len(target); end < len(text) && isWordByte(text[end]) {
			continue
		}
		out = append(out, verbStart)
	}
	return out
}

// findLastAgentName 正文中最后出现的成员名（词边界约束，大小写不敏感）。
func (t *SupervisorTask) findLastAgentName(cleanText string) string {
	if cleanText == "" {
		return ""
	}
	lower := strings.ToLower(cleanText)
	lastFound := ""
	lastIndex := -1

	for _, name := range t.config.AgentNames() {
		ln := strings.ToLower(name)
		if ln == "" {
			continue
		}
		for from := 0; from < len(lower); {
			i := strings.Index(lower[from:], ln)
			if i < 0 {
				break
			}
			pos := from + i
			if isWordBounded(cleanText, pos, pos+len(ln)) && pos > lastIndex {
				lastIndex = pos
				lastFound = name
			}
			from = pos + 1
		}
	}
	return lastFound
}

// callWithRetry 带重试机制的模型调用（Java 重试缺省宽松：任意异常均重试；
// 耗尽后返回错误，由 Run 收敛为 handleError）。
func (t *SupervisorTask) callWithRetry(node *flow.Node, trace *Trace, messages []*ai.Message) (*ai.Response, error) {
	opts := trace.Options()

	chatReq := t.config.ChatModel().Prompt(ai.NewPrompt(messages...)).Options(func(o *ai.ChatOptions) {
		if opts.HTTPCustomize != nil {
			o.HTTPCustomize = opts.HTTPCustomize
		}

		// Supervisor 自行解析 tool_calls（尤其是 feedback），避免 returnDirect
		// 抹掉正文中的路由意图
		falseVal := false
		o.AutoToolCall = &falseVal

		if opts.FeedbackMode {
			o.Tools = append(o.Tools, react.FeedbackTool(
				opts.FeedbackDescription(trace), opts.FeedbackReasonDescription(trace)))
		}
		o.Tools = append(o.Tools, opts.Tools...)
		t.config.ProtocolWide().InjectSupervisorTools(trace.Context(), func(tool ai.FunctionTool) {
			o.Tools = append(o.Tools, tool)
		})

		if o.ToolContext == nil {
			o.ToolContext = map[string]any{}
		}
		for k, v := range opts.ToolContext {
			o.ToolContext[k] = v
		}

		// 团队拦截器同时是 ai.Interceptor（chat 层组装 + 启用控制）
		for _, ic := range opts.Interceptors {
			if ic != nil {
				o.Interceptors = append(o.Interceptors, ic)
			}
		}

		for k, v := range opts.Raw {
			o.OptionSet(k, v)
		}
		// 覆盖 optionSet 可能带回的 autoToolCall=true
		o.AutoToolCall = &falseVal

		o.CacheControl = opts.CacheControl
	})

	for _, ic := range opts.Interceptors {
		if ic != nil && ic.IsEnabled() {
			ic.OnModelStart(trace, chatReq)
		}
	}

	if trace.Session().IsPending() {
		return nil, nil
	}

	task := &agent.RetryTask{
		MaxRetries:     opts.MaxRetries,
		InitialDelayMs: opts.RetryDelayMs,
		MaxDelayMs:     30000,
	}

	resp, err := task.Run(t.goCtx(trace), func() (*ai.Response, error) {
		var response *ai.Response

		if trace.HasStreamSink() {
			var end *ai.ChatEvent
			err := chatReq.Stream(t.goCtx(trace), ai.ChatSinkFunc(func(ev *ai.ChatEvent) error {
				if trace.IsStreamCancelled() {
					return errStreamCancelled // takeUntil(cancelled)
				}
				switch ev.Type {
				case ai.EventTextStart, ai.EventTextDelta, ai.EventTextEnd,
					ai.EventThinkingStart, ai.EventThinkingDelta, ai.EventThinkingEnd,
					ai.EventMediaDone:
					trace.PushAgentEvent(supervisorDeltaOf(node, trace, ev))
				}
				if ev.Type == ai.EventResponseEnd {
					end = ev // reduce(keep last RESPONSE_END)
				}
				return nil
			}))
			if err != nil {
				if errors.Is(err, errStreamCancelled) ||
					(trace.IsStreamCancelled() && errors.Is(err, context.Canceled)) {
					// 订阅端取消：非模型故障，不重试，由 dispatch 以挂起收尾
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

		if response == nil || response.IsEmpty() {
			// 触发重试
			return nil, errLlmNoReturn
		}
		return response, nil
	})
	if err != nil {
		// Java：InterruptedException 静默返回 null，其余上抛交 run 的 handleError
		if errors.Is(err, context.Canceled) {
			return nil, nil
		}
		return nil, err
	}
	return resp, nil
}

// routeTo 物理路由（记录 + 协议路由后置回调）。
func (t *SupervisorTask) routeTo(ctx flow.Context, trace *Trace, targetName string) {
	trace.SetRoute(targetName)
	t.config.ProtocolWide().OnSupervisorRouting(ctx, trace, targetName)
}

// handleError 运行时异常收敛：路由 END + 系统足迹。
func (t *SupervisorTask) handleError(ctx flow.Context, e error) {
	trace := Current(ctx)
	if trace != nil {
		trace.SetRoute(agent.IDEnd)
		trace.AddRecord(ai.RoleSystem, IDSupervisor, "Runtime Error: "+e.Error(), 0)
	}
}

// goCtx 流式调用的取消源（未绑定时 Background——永不取消）。
func (t *SupervisorTask) goCtx(trace *Trace) context.Context {
	if c := trace.Options().goContext(); c != nil {
		return c
	}
	return context.Background()
}

// supervisorDeltaOf Supervisor 增量事件（转发模型 ChatEvent）。
func supervisorDeltaOf(node *flow.Node, trace *Trace, ev *ai.ChatEvent) *agent.Event {
	e := trace.event(agent.KindSupervisorDelta)
	e.ChatEvent = ev
	e.Text = ev.Text
	if node != nil {
		e.Meta["nodeId"] = node.ID()
		e.Meta["nodeTitle"] = node.Title()
	}
	return e
}

// ---- 文本工具 ----

func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// stripMarkdownMarks 移除加粗/斜体 * 与代码 ` 标记。
var markdownMarks = strings.NewReplacer("*", "", "`", "")

func stripMarkdownMarks(text string) string {
	return strings.TrimSpace(markdownMarks.Replace(text))
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f'
}

// isWordBounded [s:e) 区间前后均非词字节（或抵达文本边界）。
func isWordBounded(s string, start, end int) bool {
	if start > 0 && isWordByte(s[start-1]) {
		return false
	}
	if end < len(s) && isWordByte(s[end]) {
		return false
	}
	return true
}
