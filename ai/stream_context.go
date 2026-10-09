package ai

// StreamContext 方言解析上下文（对标 ChatStreamContext，决策 D3）：
// 方言只允许经 ctx.Emit 表达结果——Emit 先归并进 Accumulator，再投递下游
// 管线（emitTo；nil 时仅聚合）。attrs 为方言私有状态袋（流式快照归一等）。
type StreamContext struct {
	cfg      *Config
	acc      *Accumulator
	isStream bool
	step     int

	responseID         string // 框架响应 id（一次请求内恒定）
	providerResponseID string // 厂商原始 id（chatcmpl-xxx 等）

	attrs  map[string]any
	emitTo func(*ChatEvent)
}

// NewStreamContext 构建解析上下文。emitTo 为事件下游（模型层管线），可为 nil。
func NewStreamContext(cfg *Config, acc *Accumulator, isStream bool, step int, emitTo func(*ChatEvent)) *StreamContext {
	return &StreamContext{cfg: cfg, acc: acc, isStream: isStream, step: step, emitTo: emitTo}
}

func (c *StreamContext) Config() *Config   { return c.cfg }
func (c *StreamContext) Acc() *Accumulator { return c.acc }
func (c *StreamContext) IsStream() bool    { return c.isStream }
func (c *StreamContext) Step() int         { return c.step }

// SetResponseID 记录框架响应 id（空忽略）。
func (c *StreamContext) SetResponseID(id string) {
	if id != "" {
		c.responseID = id
	}
}

func (c *StreamContext) ResponseID() string { return c.responseID }

// SetProviderResponseID 记录供应商响应 id（空忽略；之后 Event 自动预填）。
func (c *StreamContext) SetProviderResponseID(id string) {
	if id != "" {
		c.providerResponseID = id
	}
}

func (c *StreamContext) ProviderResponseID() string { return c.providerResponseID }

// Event 创建预填 responseID/providerResponseID/step 的事件（Index 默认 -1）。
func (c *StreamContext) Event(t EventType) *ChatEvent {
	return &ChatEvent{
		Type:               t,
		ResponseID:         c.responseID,
		ProviderResponseID: c.providerResponseID,
		Step:               c.step,
		Index:              -1,
	}
}

// Emit 归并 + 投递：预填空缺身份字段 → Accumulator.Merge → 下游管线。
// 方言不得在 Emit 后再写同一份聚合状态（对标 Java ChatStreamContext.emit 契约）。
func (c *StreamContext) Emit(ev *ChatEvent) {
	if ev == nil {
		return
	}
	if ev.ResponseID == "" {
		ev.ResponseID = c.responseID
	}
	if ev.ProviderResponseID == "" {
		ev.ProviderResponseID = c.providerResponseID
	}
	if ev.Step == 0 {
		ev.Step = c.step // 首步即 0，赋值无歧义
	}
	c.acc.Merge(ev)
	if c.emitTo != nil {
		c.emitTo(ev)
	}
}

// ---- 方言私有状态袋 ----

// AttrIfAbsent 取方言私有状态；不存在则以 mk 构建并写入。
func (c *StreamContext) AttrIfAbsent(name string, mk func() any) any {
	if v, ok := c.attrs[name]; ok {
		return v
	}
	if mk == nil {
		return nil
	}
	v := mk()
	if c.attrs == nil {
		c.attrs = map[string]any{}
	}
	c.attrs[name] = v
	return v
}

// AttrPut 写方言私有状态。
func (c *StreamContext) AttrPut(name string, v any) {
	if c.attrs == nil {
		c.attrs = map[string]any{}
	}
	c.attrs[name] = v
}

// AttrRemove 取出并移除方言私有状态。
func (c *StreamContext) AttrRemove(name string) any {
	v, ok := c.attrs[name]
	if ok {
		delete(c.attrs, name)
	}
	return v
}
