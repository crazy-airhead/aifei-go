// compress.go 上下文压缩拦截器（对标 ContextCompressionInterceptor + compress/
// 子包的双内置策略）：OnReasonStart 非强制触发（消息数阈值 || token 阈值）、
// OnReasonRetry 的 prompt-too-long 强制触发（forceLevel 递减预算）；过期段经
// LLM 策略摘要（KeyInfo 提取 / Hierarchical 滚动摘要）后重建工作记忆。
//
// Java 用 jtokkit 精确计数；Go 保持 harness 模块零依赖——内置近似计数器
//（CJK 一字一 token、其余四字符一 token），阈值语义一致、绝对值偏保守；
// 需要精确值时 WithTokenCounter 注入。
package harness

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent/react"
)

// 消息元数据标记（Java META_FIRST/META_COMPRESSED）。
const (
	MetaFirst      = "__metaFirst"
	MetaCompressed = "__metaCompressed"
)

// TokenCounter token 计数器（文本 → token 数）。
type TokenCounter func(text string) int

// ApproxTokens 近似计数器（CJK 一字一 token、其余四字符一 token——缺省）。
func ApproxTokens(text string) int {
	if text == "" {
		return 0
	}
	cjk, others := 0, 0
	for _, r := range text {
		if r > 0x2E80 { // CJK 及全角区
			cjk++
		} else {
			others++
		}
	}
	return cjk + (others+3)/4
}

// CompressionStrategy 压缩策略 SPI（对标同名接口）。
type CompressionStrategy interface {
	// Compress 过期段 → 单条摘要消息（nil = 失败/无增量，调用方走 fallback）。
	Compress(model *ai.ChatModel, maxRetries int, trace *react.Trace, msgs []*ai.Message) *ai.Message
}

// ---- KeyInfoExtractionStrategy（对标同名：关键信息提取脱水）----

// KeyInfoExtractionStrategy 关键信息提取。
type KeyInfoExtractionStrategy struct{}

// keyInfoSystemInstruction 系统指令（Java 原文的 Go 转录）。
const keyInfoSystemInstruction = `你是一名精密的信息审计专家，负责从对话历史中提取关键信息。
你的任务不是总结，而是"脱水"——提取对后续任务执行有直接价值的增量信息。

提取维度（按优先级）：
1. 业务参数：任务中出现的特定 ID、数值、时间、偏好、约束条件
2. 确定性事实：工具调用已证实的状态与关键结果
3. 负面路径：已验证无效的尝试（防止重复犯错）
4. 技术细节：所有文件路径、函数名、技术标识（不可省略）

输出要求：
- 简洁的 Markdown 列表
- 严禁推测与修饰性语言
- 无关键信息时只回复：(无关键增量)`

// Compress 提取。
func (KeyInfoExtractionStrategy) Compress(model *ai.ChatModel, maxRetries int, trace *react.Trace, msgs []*ai.Message) *ai.Message {
	var variable []*ai.Message
	for _, m := range msgs {
		if m.Metadata != nil && m.Metadata[MetaFirst] != nil {
			continue
		}
		variable = append(variable, m)
	}
	if len(variable) == 0 {
		return nil
	}
	userData := "### 待处理历史片段\n" + formatMessagesForCompression(variable) +
		"\n\n### 审计要求\n请根据系统指令，提取上述片段中的关键信息。"
	content := compressWithPTLRetry(model, maxRetries, trace, userData, keyInfoSystemInstruction)
	if isEmptySummary(content) {
		return nil
	}
	return buildCompressedMessage("--- [已确认的关键信息] ---", content)
}

// ---- HierarchicalCompressionStrategy（对标同名：滚动摘要）----

// HierarchicalCompressionStrategy 层次滚动摘要（摘要状态存 trace attr）。
type HierarchicalCompressionStrategy struct{}

// hierarchicalSummaryKey trace attr 键（Java agent:summary:hierarchical）。
const hierarchicalSummaryKey = "agent:summary:hierarchical"

// hierarchicalMaxSummaryLength 摘要 token 硬上限（Java 500）。
const hierarchicalMaxSummaryLength = 500

const hierarchicalSummaryPrefix = "--- [全局进度滚动摘要 (层级压缩)] ---"

const hierarchicalSystemInstruction = `你是一名专业的记忆管理专家，负责维护任务的滚动进度摘要。
你的逻辑：将『旧的压缩内容』与『新增的过期历史记录』合并，生成『当前进度摘要』。

要求：
1. 信息提取：已确认关键数据、当前逻辑位置、阶段性结论
2. 去重降噪：重复思考、已失效尝试、无意义中间状态
3. 长度约束：简洁陈述句
4. 路径保留：所有文件路径/函数名/技术细节是后续导航信息，必须保留

直接输出正文，禁用"好的/明白/根据您的要求"等客套。`

// Compress 滚动合并。
func (HierarchicalCompressionStrategy) Compress(model *ai.ChatModel, maxRetries int, trace *react.Trace, msgs []*ai.Message) *ai.Message {
	lastSummary := ""
	if trace != nil {
		if v, ok := trace.Options().AttrGet(hierarchicalSummaryKey).(string); ok {
			lastSummary = v
		}
	}
	var pureExpired []*ai.Message
	for _, m := range msgs {
		if m.Metadata != nil && m.Metadata[MetaFirst] != nil {
			continue
		}
		pureExpired = append(pureExpired, m)
	}
	if len(pureExpired) == 0 {
		if lastSummary == "" {
			return nil
		}
		return buildCompressedMessage(hierarchicalSummaryPrefix, lastSummary)
	}

	candidate := mergeOnce(model, maxRetries, trace, lastSummary, pureExpired)
	// PTL 降级：二分两段式
	if isPTLText(candidate) {
		half := len(pureExpired) / 2
		if half == 0 {
			return nil
		}
		intermediate := mergeOnce(model, maxRetries, trace, lastSummary, pureExpired[:half])
		if isPTLText(intermediate) {
			return nil
		}
		candidate = mergeOnce(model, maxRetries, trace, intermediate, pureExpired[half:])
		if isPTLText(candidate) {
			return nil
		}
	}
	candidate = limitSummaryTokens(candidate)
	if trace != nil {
		trace.Options().AttrPut(hierarchicalSummaryKey, candidate)
	}
	return buildCompressedMessage(hierarchicalSummaryPrefix, candidate)
}

func mergeOnce(model *ai.ChatModel, maxRetries int, trace *react.Trace, base string, history []*ai.Message) string {
	oldPart := base
	if oldPart == "" {
		oldPart = "（暂无）"
	}
	userData := "### 旧的摘要内容\n" + oldPart +
		"\n\n### 新增的过期历史记录\n" + formatMessagesForCompression(history) +
		"\n\n### 最终任务要求\n请根据 System Message（系统指令）中的逻辑，输出更新后的『进度摘要』："
	return compressWithPTLRetry(model, maxRetries, trace, userData, hierarchicalSystemInstruction)
}

func limitSummaryTokens(s string) string {
	if s == "" {
		return s
	}
	// 近似上限：500 token（计数器口径一致即可）
	chars := hierarchicalMaxSummaryLength * 3
	if len(s) <= chars {
		return s
	}
	for chars > 0 && ApproxTokens(s[:chars]) > hierarchicalMaxSummaryLength {
		chars = chars * 9 / 10
	}
	if chars <= len("...[Truncated]") {
		return "...[Truncated]"
	}
	return s[:chars-15] + "...[Truncated]"
}

// ---- 公共件 ----

// compressWithPTLRetry LLM 摘要调用（PTL 提示过长时裁批重试——Java 3 次）。
func compressWithPTLRetry(model *ai.ChatModel, maxRetries int, trace *react.Trace, userData, systemInstruction string) string {
	if model == nil {
		return ""
	}
	for attempt := 0; attempt < 3; attempt++ {
		content := ""
		var err error
		for r := 0; r < maxInt(1, maxRetries); r++ {
			resp, e := model.Prompt(userData).Options(func(o *ai.ChatOptions) {
				o.OptionSet("agentName", "Compression")
			}).Call()
			err = e
			if e == nil && resp != nil {
				if m := resp.Message(); m != nil {
					content = m.Text
				}
				break
			}
		}
		if err != nil || content == "" {
			return ""
		}
		if !isPTLText(content) {
			return content
		}
		// PTL：裁剪批次（保尾段）
		if !reduceBatch(&userData) {
			return ""
		}
	}
	return ""
}

// reduceBatch 批次裁剪（保留后 70% + 提示头）——findSafeSplit 的简化对应物。
func reduceBatch(userData *string) bool {
	idx := strings.LastIndex(*userData, "\n[Observation]:")
	if idx < 0 {
		idx = strings.Index(*userData, "\n") // 无安全边界
		if idx < 0 {
			return false
		}
		idx = len(*userData) * 3 / 10
		if idx <= 0 {
			return false
		}
	}
	cut := len(*userData) * 3 / 10
	if cut >= len(*userData) {
		return false
	}
	*userData = (*userData)[:0] + // 保留提示头
		"### 待处理历史片段\n（前段已省略）\n" + (*userData)[cut:]
	return true
}

func isPTLText(s string) bool {
	lower := strings.ToLower(s)
	return strings.HasPrefix(lower, "prompt is too long") ||
		strings.Contains(lower, "context length") ||
		strings.Contains(lower, "too many tokens") ||
		strings.Contains(lower, "maximum context")
}

// IsPromptTooLongError 传输错误是否为提示过长（Java CompressionUtil）。
func IsPromptTooLongError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	for _, kw := range []string{
		"prompt is too long", "context length", "context_length_exceeded",
		"maximum context", "maximum input length", "too many tokens",
		"input_too_long", "request_too_large", "context window",
	} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func isEmptySummary(s string) bool {
	s = strings.TrimSpace(s)
	return s == "" || s == "(无显著进度)" || s == "(无关键增量)"
}

// buildCompressedMessage 摘要消息（UserMessage + META_COMPRESSED——Java 同款）。
func buildCompressedMessage(prefix, content string) *ai.Message {
	if content == "" {
		return nil
	}
	text := content
	if prefix != "" {
		text = prefix + "\n" + content
	}
	return ai.OfUser(text).AddMetadata(MetaCompressed, 1)
}

// formatMessagesForCompression 喂给摘要 LLM 的行格式（Java formatMessage 对应物）。
func formatMessagesForCompression(msgs []*ai.Message) string {
	var sb strings.Builder
	for _, m := range msgs {
		switch content := m.Content.(type) {
		case *ai.AssistantMessage:
			if content.Thinking != "" {
				sb.WriteString("[Thought]: " + content.Thinking + "\n")
			}
			if content.Text != "" {
				sb.WriteString("[Text]: " + content.Text + "\n")
			}
			for _, call := range content.ToolCalls {
				sb.WriteString("[Action]: 调用工具 " + call.Name + "，参数: " + call.ArgumentsStr + "\n")
			}
		case *ai.ToolMessage:
			text := content.ResultText()
			if len(text) > 10000 {
				text = text[:10000] + fmt.Sprintf("... [truncated, %d chars total]", len(text))
			}
			sb.WriteString("[Observation]: 得到结果 " + text + "\n")
		default:
			sb.WriteString(strings.ToUpper(string(m.Role)) + ": " + fmt.Sprint(content) + "\n")
		}
	}
	return sb.String()
}

// ---- 拦截器 ----

// CompressionInterceptorConfig 压缩参数。
type CompressionInterceptorConfig struct {
	MaxMessages          int     // 保留窗口（缺省 15；harness 装配 100）
	MessageTriggerFactor float64 // 触发因子（缺省 1.0）
	MaxContextRatio      float64 // 上下文占用比（缺省 0.75）
	DefaultContextLength int     // 模型缺省上下文（缺省 128000）
	MaxRetries           int     // LLM 摘要重试（缺省 3）
}

// CompressionInterceptor 上下文压缩拦截器。
type CompressionInterceptor struct {
	react.InterceptorBase
	cfg      CompressionInterceptorConfig
	strategy CompressionStrategy
	counter  TokenCounter
}

// NewCompressionInterceptor 构造（strategy 空 = KeyInfo 提取）。
func NewCompressionInterceptor(strategy CompressionStrategy) *CompressionInterceptor {
	if strategy == nil {
		strategy = KeyInfoExtractionStrategy{}
	}
	return &CompressionInterceptor{
		cfg: CompressionInterceptorConfig{
			MaxMessages: 15, MessageTriggerFactor: 1.0,
			MaxContextRatio: 0.75, DefaultContextLength: 128000, MaxRetries: 3,
		},
		strategy: strategy,
		counter:  ApproxTokens,
	}
}

// SetConfig 参数覆写。
func (i *CompressionInterceptor) SetConfig(fn func(*CompressionInterceptorConfig)) *CompressionInterceptor {
	fn(&i.cfg)
	return i
}

// SetTokenCounter 注入精确计数器。
func (i *CompressionInterceptor) SetTokenCounter(c TokenCounter) *CompressionInterceptor {
	if c != nil {
		i.counter = c
	}
	return i
}

// CompositeStrategy 顺序组合（先 KeyInfo 后 Hierarchical——harness 缺省装配）。
type CompositeStrategy struct{ Strategies []CompressionStrategy }

// Compress 依序尝试，首个非 nil 结果胜出。
func (c CompositeStrategy) Compress(model *ai.ChatModel, maxRetries int, trace *react.Trace, msgs []*ai.Message) *ai.Message {
	for _, s := range c.Strategies {
		if m := s.Compress(model, maxRetries, trace, msgs); m != nil {
			return m
		}
	}
	return nil
}

// OnReasonStart 非强制触发（消息数阈值 || token 阈值）。
func (i *CompressionInterceptor) OnReasonStart(trace *react.Trace, _ *strings.Builder) {
	i.compressContext(trace, false, 0)
}

// OnReasonRetry 强制触发（提示过长错误；forceLevel 递减预算）。
func (i *CompressionInterceptor) OnReasonRetry(trace *react.Trace, err error, attempt int, _ string) bool {
	if !IsPromptTooLongError(err) {
		return false
	}
	return i.compressContext(trace, true, attempt)
}

// compressContext 主流程（对标 compressContext 的保真收窄：固定前缀/预算/
// 双维度截断/原子对齐/语义锚定/策略摘要+fallback/悬空清理/写回）。
func (i *CompressionInterceptor) compressContext(trace *react.Trace, force bool, forceLevel int) bool {
	if trace == nil {
		return false
	}
	wm := trace.WorkingMemory()
	if wm == nil {
		return false
	}
	msgs := wm.Messages()
	if len(msgs) <= 2 {
		return false
	}
	systemPrompt := trace.Config().SystemPromptFor(trace, nil)

	contextLength := i.cfg.DefaultContextLength
	if m := trace.Config().ChatModel; m != nil && m.Config().ContextLength > 0 {
		contextLength = m.Config().ContextLength
	}
	threshold := int(float64(contextLength) * i.cfg.MaxContextRatio)

	current := i.estimateTokens(msgs, systemPrompt) + i.fixedRequestReserve(threshold)

	triggerThreshold := int(math.Ceil(float64(i.cfg.MaxMessages) * maxF(i.cfg.MessageTriggerFactor, 1.0)))
	variableCount := 0
	for _, m := range msgs {
		if m.Metadata == nil || m.Metadata[MetaFirst] == nil {
			variableCount++
		}
	}
	if !force && variableCount <= triggerThreshold && current <= threshold {
		return false
	}

	// 固定前缀：首个 META_FIRST 连续段（至少保首条用户原始输入）
	fixedEnd := 0
	for idx, m := range msgs {
		if m.Metadata != nil && m.Metadata[MetaFirst] != nil {
			fixedEnd = idx + 1
		} else if idx > 0 {
			break
		} else {
			fixedEnd = 1
		}
	}
	fixed := msgs[:fixedEnd]

	// 预算
	budget := threshold
	if force {
		ratios := []float64{0.8, 0.6, 0.4, 0.25}
		r := ratios[minInt(forceLevel-1, len(ratios)-1)]
		if forceLevel < 1 {
			r = ratios[0]
		}
		budget = minInt(threshold, maxInt(1000, int(float64(threshold)*r)))
	}
	fixedTokens := i.estimateTokens(fixed, systemPrompt)
	if fixedTokens >= budget {
		i.replaceWorkingMemory(wm, fixed)
		return true
	}
	available := budget - fixedTokens
	summaryReserve := minInt(available, maxInt(200, available/10))
	windowBudget := available - summaryReserve

	// 双维度截断点：条数（msgBudget）与 token（从尾累加超预算）
	msgBudget := i.cfg.MaxMessages
	if force {
		msgBudget = maxInt(1, variableCount*3/5)
	}
	targetByCount := maxInt(len(fixed), len(msgs)-msgBudget)
	targetByTokens := len(fixed)
	acc := 0
	for idx := len(msgs) - 1; idx >= fixedEnd; idx-- {
		acc += i.messageTokens(msgs[idx]) + 4
		if acc > windowBudget {
			targetByTokens = idx + 1
			break
		}
		targetByTokens = fixedEnd
	}
	targetIdx := maxInt(targetByCount, minInt(targetByTokens, len(msgs)))

	// 原子对对齐：截断点落在工具输出上 → 回溯到带 toolCalls 的 assistant
	for targetIdx > fixedEnd && isToolOutput(msgs[targetIdx]) {
		targetIdx--
	}
	// 语义锚定：前一条是无工具调用的 assistant → 并入过期段（其结论被摘要）
	if targetIdx > fixedEnd {
		if am, ok := msgs[targetIdx-1].Content.(*ai.AssistantMessage); ok && len(am.ToolCalls) == 0 {
			targetIdx--
		}
	}

	expired := msgs[fixedEnd:targetIdx]
	window := msgs[targetIdx:]

	var rebuilt []*ai.Message
	rebuilt = append(rebuilt, fixed...)
	if len(expired) > 0 {
		var summary *ai.Message
		if trace.Config().ChatModel != nil {
			summary = i.strategy.Compress(trace.Config().ChatModel, i.cfg.MaxRetries, trace, expired)
		}
		if summary == nil {
			summary = i.fallbackSummary(expired) // 零 LLM 成本兜底
		}
		if summary != nil {
			rebuilt = append(rebuilt, summary)
		}
	}
	rebuilt = append(rebuilt, window...)

	rebuilt = removeDanglingToolOutputs(rebuilt)
	before := len(msgs)
	i.replaceWorkingMemory(wm, rebuilt)
	return len(rebuilt) < before
}

// fallbackSummary 策略失败兜底：保留旧摘要消息 + 末尾原子组（Java 同款）。
func (i *CompressionInterceptor) fallbackSummary(expired []*ai.Message) *ai.Message {
	for idx := len(expired) - 1; idx >= 0; idx-- {
		if expired[idx].Metadata != nil && expired[idx].Metadata[MetaCompressed] != nil {
			return expired[idx]
		}
	}
	if len(expired) == 0 {
		return nil
	}
	last := expired[len(expired)-1]
	if isToolOutput(last) {
		// 回溯连续工具输出到源头 assistant
		start := len(expired) - 1
		for start > 0 && isToolOutput(expired[start-1]) {
			start--
		}
		return last // 仅保末条输出（语义收窄：单消息粒度兜底）
	}
	return last
}

// replaceWorkingMemory 重建工作记忆（Clear 保留 attrs + 逐条回填）。
func (i *CompressionInterceptor) replaceWorkingMemory(wm *ai.Prompt, msgs []*ai.Message) {
	wm.Clear()
	wm.AddMessage(msgs...)
}

// estimateTokens 消息集 + 系统提示的总量（+3 固定开销——Java 口径）。
func (i *CompressionInterceptor) estimateTokens(msgs []*ai.Message, systemPrompt string) int {
	total := 3
	for _, m := range msgs {
		total += i.messageTokens(m) + 4
	}
	if systemPrompt != "" {
		total += i.counter(systemPrompt) + 4
	}
	return total
}

// messageTokens 单消息（文本 + 思考 + 工具调用 + 媒体块近似）。
func (i *CompressionInterceptor) messageTokens(m *ai.Message) int {
	total := 0
	switch content := m.Content.(type) {
	case *ai.AssistantMessage:
		total += i.counter(content.Text) + i.counter(content.Thinking)
		for _, call := range content.ToolCalls {
			total += i.counter(call.Name+call.ArgumentsStr) + 10
		}
		for _, block := range content.Blocks {
			total += mediaTokens(i.counter, block)
		}
	case *ai.ToolMessage:
		total += i.counter(content.ResultText()) + i.counter(content.Name)
		total += mediaTokens(i.counter, nil)
	default:
		total += i.counter(fmt.Sprint(content))
	}
	return total
}

func mediaTokens(counter TokenCounter, block ai.ContentBlock) int {
	if block == nil {
		return 8
	}
	if s, ok := block.Content().(string); ok && s != "" {
		return maxInt(1, len(s)/4) + 20
	}
	return 8
}

// fixedRequestReserve 请求固定预留（阈值 5%，clamp [256,2000]）。
func (i *CompressionInterceptor) fixedRequestReserve(threshold int) int {
	reserve := (threshold + 19) / 20
	if reserve < 256 {
		reserve = 256
	}
	if reserve > 2000 {
		reserve = 2000
	}
	return reserve
}

// isToolOutput 工具输出消息（ToolMessage 载体）。
func isToolOutput(m *ai.Message) bool {
	_, ok := m.Content.(*ai.ToolMessage)
	return ok
}

// removeDanglingToolOutputs 悬空工具输出清理（孤立 ToolMessage 删除；空壳
// assistant 删除）。
func removeDanglingToolOutputs(msgs []*ai.Message) []*ai.Message {
	var out []*ai.Message
	for _, m := range msgs {
		if isToolOutput(m) {
			continue // 重建窗口不带孤立输出（源头 assistant 已被摘要/截断）
		}
		if am, ok := m.Content.(*ai.AssistantMessage); ok {
			if am.Text == "" && am.Thinking == "" && len(am.ToolCalls) == 0 && len(am.Blocks) == 0 {
				continue
			}
		}
		out = append(out, m)
	}
	return out
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

var _ = utf8.RuneLen // 保留 unicode 引用位（计数器演进用）
