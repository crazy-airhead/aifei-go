package ai

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/crazy-airhead/aifei-go/ai/httpx"
)

// ChatModel 聊天模型（config + dialect + httpx）：一次构建重复出请求描述。
type ChatModel struct {
	config  *Config
	dialect Dialect
	client  *httpx.Client
}

// NewChatModel 构建并选定方言。apiURL/model 必填；方言未命中报配置错误。
func NewChatModel(cfg Config) (*ChatModel, error) {
	if cfg.APIURL == "" {
		return nil, fmt.Errorf("ai: the config.apiUrl is required")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("ai: the config.model is required")
	}
	d := SelectDialect(&cfg)
	if d == nil {
		return nil, fmt.Errorf("ai: the dialect(provider) no matched, check config or dependencies")
	}

	var hopts []httpx.Option
	if cfg.Timeout > 0 {
		hopts = append(hopts, httpx.WithTimeout(cfg.Timeout))
	}
	if cfg.Proxy != "" {
		hopts = append(hopts, httpx.WithProxy(cfg.Proxy))
	}
	if cfg.Doer != nil {
		hopts = append(hopts, httpx.WithDoer(cfg.Doer))
	}
	return &ChatModel{config: &cfg, dialect: d, client: httpx.New(hopts...)}, nil
}

// ChatModelBuilder 链式构建（NewChatModelURL(apiURL).APIKey(k).Model(m).Build()）。
type ChatModelBuilder struct {
	cfg Config
}

func NewChatModelURL(apiURL string) *ChatModelBuilder {
	return &ChatModelBuilder{cfg: Config{APIURL: apiURL}}
}

func (b *ChatModelBuilder) APIKey(k string) *ChatModelBuilder   { b.cfg.APIKey = k; return b }
func (b *ChatModelBuilder) Model(m string) *ChatModelBuilder    { b.cfg.Model = m; return b }
func (b *ChatModelBuilder) Provider(p string) *ChatModelBuilder { b.cfg.Provider = p; return b }
func (b *ChatModelBuilder) Standard(s string) *ChatModelBuilder { b.cfg.Standard = s; return b }
func (b *ChatModelBuilder) Doer(d httpx.Doer) *ChatModelBuilder { b.cfg.Doer = d; return b }
func (b *ChatModelBuilder) Name(n string) *ChatModelBuilder     { b.cfg.Name = n; return b }
func (b *ChatModelBuilder) Timeout(t int64) *ChatModelBuilder {
	b.cfg.Timeout = time.Duration(t) * time.Second
	return b
}

// Config 剩余字段的逃生舱配置。
func (b *ChatModelBuilder) Config(fn func(*Config)) *ChatModelBuilder {
	fn(&b.cfg)
	return b
}

func (b *ChatModelBuilder) Build() (*ChatModel, error) { return NewChatModel(b.cfg) }

func (m *ChatModel) Dialect() Dialect { return m.dialect }
func (m *ChatModel) Config() *Config  { return m.config }

// Prompt 创建请求描述：string | *Message | []*Message | *Prompt。
func (m *ChatModel) Prompt(p any) RequestDesc {
	return newRequestDesc(m, promptOf(p))
}

func promptOf(p any) *Prompt {
	switch v := p.(type) {
	case nil:
		return NewPrompt()
	case string:
		return PromptOf(v)
	case *Prompt:
		return v
	case *Message:
		return NewPrompt(v)
	case []*Message:
		return NewPrompt(v...)
	}
	panic("ai: ChatModel.Prompt accepts string | *Message | []*Message | *Prompt")
}

// ---- RequestDesc ----

// RequestDesc 一次聊天请求的描述（对标 ChatRequestDesc）。P0-e 交付 call 路径；
// Stream/StreamChan/CallStream 在 P1-a 落地。
type RequestDesc interface {
	Session(s Session) RequestDesc
	Role(role string) RequestDesc
	Instruction(instruction string) RequestDesc
	SystemPrompt(sp string) RequestDesc
	Options(fn func(*ChatOptions)) RequestDesc
	EventFilter(f EventFilter) RequestDesc

	Call() (*Response, error)
}

type requestDesc struct {
	model          *ChatModel
	originalPrompt *Prompt

	options     *ChatOptions
	eventFilter EventFilter
	systemMsg   *Message
	prepared    bool

	responseID string       // 框架响应 id（Call 期间恒定）
	step       int          // autoToolCall 递归轮次（0 基）
	events     []*ChatEvent // call 路径全轨迹（跨轮累积）
}

func newRequestDesc(m *ChatModel, p *Prompt) *requestDesc {
	opts := &ChatOptions{}
	if m.config.DefaultOptions != nil {
		m.config.DefaultOptions(opts)
	}
	return &requestDesc{model: m, originalPrompt: p, options: opts}
}

func (r *requestDesc) Session(s Session) RequestDesc {
	r.options.Session = s
	return r
}

func (r *requestDesc) Role(role string) RequestDesc {
	r.options.Role = role
	return r
}

func (r *requestDesc) Instruction(instruction string) RequestDesc {
	r.options.Instruction = instruction
	return r
}

func (r *requestDesc) SystemPrompt(sp string) RequestDesc {
	r.options.SystemPrompt = sp
	return r
}

func (r *requestDesc) Options(fn func(*ChatOptions)) RequestDesc {
	if fn != nil {
		fn(r.options)
	}
	return r
}

func (r *requestDesc) EventFilter(f EventFilter) RequestDesc {
	if f != nil {
		r.eventFilter = f
	}
	return r
}

// prepare 一次性组装（对标 Java prepare）：会话兜底 → prompt 入会话（先补
// sessionId）→ toolContext 补 sessionId → 系统提示（SystemPrompt 优先，否则
// Role/Instruction 结构化 → schema 指令+格式选项 → 拦截器 → talent 指令）。
func (r *requestDesc) prepare() {
	if r.prepared {
		return
	}
	r.prepared = true

	if r.options.Session == nil {
		r.options.Session = NewMemorySession()
	}
	if r.originalPrompt != nil {
		if _, ok := r.originalPrompt.Attrs()[AttrSessionID]; !ok {
			r.originalPrompt.Attrs()[AttrSessionID] = r.options.Session.SessionID()
		}
		r.options.Session.AddMessage(r.originalPrompt.Messages()...)
	}
	if r.options.ToolContext == nil {
		r.options.ToolContext = map[string]any{}
	}
	if _, ok := r.options.ToolContext[AttrSessionID]; !ok {
		r.options.ToolContext[AttrSessionID] = r.options.Session.SessionID()
	}

	var ib strings.Builder
	if r.options.SystemPrompt != "" {
		// 有系统提示词（优先用）
		ib.WriteString(r.options.SystemPrompt + "\n\n")
	} else {
		if r.options.Role != "" {
			ib.WriteString("## 你的角色\n" + r.options.Role + "\n\n")
		}
		if r.options.Instruction != "" {
			ib.WriteString("## 执行指令\n" + r.options.Instruction + "\n")
		}
	}

	if r.options.OutputSchema != "" {
		r.model.dialect.PrepareOutputSchemaInstruction(r.options.OutputSchema, &ib)
		r.model.dialect.PrepareOutputFormatOptions(r.options)
	}

	if r.originalPrompt != nil && len(r.options.ToolContext) > 0 {
		for k, v := range r.options.ToolContext {
			r.originalPrompt.Attrs()[k] = v
		}
	}
	for _, ic := range r.options.Interceptors {
		if ic != nil && ic.IsEnabled() {
			ic.OnPrepare(r.options.Session, r.options, r.originalPrompt, &ib)
		}
	}

	var tb strings.Builder
	ActiveTalents(r.options, r.originalPrompt, &tb)
	if tb.Len() > 0 {
		ib.WriteString("\n")
		ib.WriteString(tb.String())
	}

	if ib.Len() > 0 {
		r.systemMsg = OfSystem(ib.String())
	}
}

// Call 非流式调用。与 Java 的差异：全轨迹含 RESPONSE_START/RESPONSE_END 与
// 每轮 STEP_START/STEP_END（Java call 事件仅方言语义事件；Go 让 Call 的
// Events() 与 stream 轨迹同构，见契约不变量 4）。
func (r *requestDesc) Call() (*Response, error) {
	r.prepare()
	r.events = nil
	r.step = 0
	r.responseID = "resp_" + newGUID()

	r.collectEvent(r.lifecycleEvent(EventResponseStart))

	resp, err := r.internalCall()
	if err != nil {
		return nil, err
	}

	end := r.lifecycleEvent(EventResponseEnd)
	end.SetResponse(resp, resp.Message())
	r.collectEvent(end)
	resp.events = r.events // 尾事件在 BuildResponse 之后追加，需回写

	return resp, nil
}

func (r *requestDesc) lifecycleEvent(t EventType) *ChatEvent {
	return &ChatEvent{Type: t, ResponseID: r.responseID, Index: -1}
}

func (r *requestDesc) internalCall() (*Response, error) {
	req := r.newCallRequest(false)
	chain := newCallChain(r.options.Interceptors, r.doCall)
	return chain.DoIntercept(req)
}

func (r *requestDesc) newCallRequest(stream bool) *CallRequest {
	msgs := []*Message{}
	if r.systemMsg != nil {
		msgs = append(msgs, r.systemMsg)
	}
	msgs = append(msgs, r.options.Session.Messages()...)
	return &CallRequest{
		cfg:      r.model.config,
		options:  r.options,
		session:  r.options.Session,
		messages: msgs,
		isStream: stream,
	}
}

// doCall 真实调用（对标 Java doCall）：建请求 → httpx → 方言解析（接 emitter，
// 非流式同样收集 refusal/error 等语义事件）→ 错误检查 → 入会话 → autoToolCall
// 递归（Step+1）或 returnDirect 合成。
func (r *requestDesc) doCall(req *CallRequest) (*Response, error) {
	step := r.step
	r.collectEvent(r.stepEvent(EventStepStart, step, nil, nil))

	acc := NewAccumulator()
	ctx := NewStreamContext(req.cfg, acc, false, step, r.collectEvent)
	ctx.SetResponseID(r.responseID)

	resp, stepAcc, recursed, err := r.roundTrip(req, ctx, acc)
	if err != nil {
		return nil, err
	}
	if recursed {
		return resp, nil // 递归轮已收尾本步
	}

	stepResp := stepAcc.BuildResponse(req.cfg.Model, nil)
	stepEnd := r.stepEvent(EventStepEnd, step, stepResp, stepAcc.Usage())
	r.collectEvent(stepEnd)
	return resp, nil
}

// roundTrip 单轮：请求 → 解析 → 会话写入 → 工具递归决策。
// stepAcc 为本步 STEP_END 聚合快照的来源（returnDirect 时为合成终态载体）；
// recursed=true 表示已进入下一轮 internalCall（本步 STEP_END 已在递归前发出）。
func (r *requestDesc) roundTrip(req *CallRequest, ctx *StreamContext, acc *Accumulator) (resp *Response, stepAcc *Accumulator, recursed bool, err error) {
	m := r.model

	body, url, err := m.dialect.BuildRequestJSON(req.cfg, req.options, req.messages, false)
	if err != nil {
		return nil, nil, false, err
	}

	client := m.client
	if fn := req.options.HTTPCustomize; fn != nil {
		client = httpx.New(httpx.WithDoer(customizeDoer{inner: m.client.Doer(), fn: fn}))
	}
	respBody, _, err := client.PostJSON(context.Background(), url, body, m.requestHeader())
	if err != nil {
		return nil, nil, false, err
	}

	respJSON := strings.TrimSpace(string(respBody))
	// 响应体不是 JSON 时给出指向配置的错误（对称流式守卫）
	if respJSON == "" {
		return nil, nil, false, fmt.Errorf("ai: LLM response is empty. Check the upstream service and apiUrl config")
	}
	if !isModelFrameShape(respJSON) {
		return nil, nil, false, fmt.Errorf("ai: LLM response is unrecognizable: not a json body."+
			" Check the apiUrl and standard/provider config. body: %s", abbreviate(respJSON))
	}

	m.dialect.ParseResponseJSON(ctx, respJSON)
	if acc.Err() != nil {
		return nil, nil, false, acc.Err()
	}

	itemMessage := acc.Message()
	if itemMessage == nil {
		// 解析成功但无消息（如空 choices）：空响应直通，不入会话
		return acc.BuildResponse(req.cfg.Model, r.events), acc, false, nil
	}
	r.options.Session.AddMessage(&Message{Role: RoleAssistant, Content: itemMessage})

	if r.autoToolCall() && len(itemMessage.ToolCalls) > 0 {
		returnDirect, err := r.buildToolMessage(req, itemMessage, nil)
		if err != nil {
			return nil, nil, false, err
		}
		if len(returnDirect) == 0 {
			// 无直接返回消息：本步收尾后递归（Step+1）
			r.closeStep(req, ctx, acc)
			r.step++
			resp, err := r.internalCall()
			return resp, nil, true, err
		}
		// 全部 returnDirect：工具结果合成最终 assistant 消息
		final := m.dialect.BuildAssistantMessageByToolMessages(itemMessage, returnDirect)
		finAcc := NewAccumulator()
		finAcc.SetUsage(acc.Usage())
		finAcc.SetFinishReason("tool")
		finAcc.SetTerminalMessage(final)
		r.options.Session.AddMessage(&Message{Role: RoleAssistant, Content: final})
		return finAcc.BuildResponse(req.cfg.Model, r.events), finAcc, false, nil
	}

	return acc.BuildResponse(req.cfg.Model, r.events), acc, false, nil
}

// closeStep 发出本步 STEP_END（工具递归前；携带本步聚合快照）。
func (r *requestDesc) closeStep(req *CallRequest, ctx *StreamContext, acc *Accumulator) {
	stepResp := acc.BuildResponse(req.cfg.Model, nil)
	r.collectEvent(r.stepEvent(EventStepEnd, r.step, stepResp, acc.Usage()))
}

func (r *requestDesc) stepEvent(t EventType, step int, resp *Response, u *Usage) *ChatEvent {
	ev := &ChatEvent{Type: t, ResponseID: r.responseID, Step: step, Index: -1, Usage: u}
	if resp != nil {
		ev.SetResponse(resp, resp.Message())
	}
	return ev
}

// collectEvent 事件入轨迹（应用过滤器；缺省 DefaultEventFilter 滤除 HEARTBEAT/RAW）。
func (r *requestDesc) collectEvent(ev *ChatEvent) {
	if ev == nil {
		return
	}
	if r.eventFilter != nil {
		if !r.eventFilter.Apply(ev) {
			return
		}
	} else if !DefaultEventFilter(ev) {
		return
	}
	r.events = append(r.events, ev)
}

func (r *requestDesc) autoToolCall() bool {
	if r.options.AutoToolCall != nil {
		return *r.options.AutoToolCall
	}
	return r.model.config.DefaultAutoToolCall
}

// buildToolMessage 顺序执行工具调用并构建 tool 消息（入会话）。返回全部
// returnDirect 的消息；空 = 无直接返回（调用方递归）。observer 为流式路径
// 的 TOOL_RESULT 事件缝（call 路径传 nil）。
func (r *requestDesc) buildToolMessage(req *CallRequest, acm *AssistantMessage,
	observer func(*ToolCall, *Message)) ([]*Message, error) {
	var toolMsgs []*Message
	for i := range acm.ToolCalls {
		call := &acm.ToolCalls[i]
		tool := r.options.Tool(call.Name)
		if tool == nil {
			return nil, fmt.Errorf("ai: tool call not found: %q", call.Name)
		}

		result := r.doToolCall(req, tool, call)
		tm := OfTool(result, call.Name, call.ID, tool.ReturnDirect())
		for k, v := range tool.Meta() {
			tm.AddMetadata(k, v)
		}
		tm.AddMetadata("__tool", tool.Name())

		r.options.Session.AddMessage(tm)
		toolMsgs = append(toolMsgs, tm)

		if observer != nil {
			observer(call, tm)
		}
	}

	for _, tm := range toolMsgs {
		if t, _ := tm.Content.(*ToolMessage); t != nil && !t.ReturnDirect {
			return nil, nil // 存在非直接返回：递归
		}
	}
	return toolMsgs, nil
}

// doToolCall 执行工具（经 ToolInterceptor 链；ctx 携带 toolContext）。
func (r *requestDesc) doToolCall(req *CallRequest, tool FunctionTool, call *ToolCall) *ToolResult {
	treq := &ToolRequest{call: req, toolContext: r.options.ToolContext, args: call.Arguments}
	chain := newToolChain(r.options.Interceptors, func(*ToolRequest) *ToolResult {
		return tool.Call(ContextWithToolContext(context.Background(), treq.toolContext), treq.args)
	})
	return chain.DoIntercept(treq)
}

// requestHeader 鉴权与默认头（Authorization: Bearer <key> 优先级最低可被 Headers 覆盖）。
func (m *ChatModel) requestHeader() map[string]string {
	header := map[string]string{}
	for k, v := range m.config.Headers {
		header[k] = v
	}
	if m.config.APIKey != "" {
		header["Authorization"] = "Bearer " + m.config.APIKey
	}
	if m.config.UserAgent != "" {
		header["User-Agent"] = m.config.UserAgent
	}
	return header
}

// customizeDoer Options.HTTPCustomize 的请求级包装（对标 httpCustomizeAdd）。
type customizeDoer struct {
	inner httpx.Doer
	fn    func(*http.Request)
}

func (d customizeDoer) Do(req *http.Request) (*http.Response, error) {
	d.fn(req)
	return d.inner.Do(req)
}

// isModelFrameShape 纯语法形状判定：模型帧要么 JSON 要么 [DONE] 类标记
// （data: 前缀容忍）。HTML/XML 页面（apiUrl 指错命中网关首页）必然不过。
func isModelFrameShape(data string) bool {
	s := strings.TrimSpace(data)
	if strings.HasPrefix(s, "data:") {
		s = strings.TrimSpace(strings.TrimPrefix(s, "data:"))
	}
	if s == "" {
		return false
	}
	return s[0] == '{' || s[0] == '['
}

// abbreviate 截断过长文本（错误消息用，避免整页 HTML 进异常）。
func abbreviate(text string) string {
	s := strings.TrimSpace(text)
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}
