package ai

import (
	"sort"
	"strings"
	"sync"
)

// Dialect 方言 SPI（决策 D3：事件先行）。请求侧把统一 Message 翻译为厂商协议；
// 响应侧唯一入口 ParseResponseJSON 只允许经 ctx.Emit 出结果（不返回值），
// call/stream、全部厂商、工具递归共用一条事件管线。
type Dialect interface {
	// Name 方言注册名（"openai-completions" / "anthropic" / ...）
	Name() string
	// IsDefault 是否补位方言（openai 为 true：无匹配时兜底）
	IsDefault() bool
	// Matched 按配置判断是否匹配（provider/apiUrl 前缀等）
	Matched(cfg *Config) bool

	// ---- 请求侧 ----

	// BuildRequestJSON 构建请求体与最终 URL（含 /chat/completions 等端点拼接）。
	BuildRequestJSON(cfg *Config, opts *ChatOptions, msgs []*Message, isStream bool) (body []byte, url string, err error)
	// PrepareOutputSchemaInstruction 输出 schema 指令追加到系统提示构建器。
	PrepareOutputSchemaInstruction(schema string, sb *strings.Builder)
	// PrepareOutputFormatOptions 输出格式选项（如 response_format=json_object）。
	PrepareOutputFormatOptions(opts *ChatOptions)
	// BuildAssistantToolCallMessage 流式聚合的工具调用 → assistant 消息
	//（autoToolCall 回放历史用；arguments 出站前净化）。
	BuildAssistantToolCallMessage(acc *Accumulator, builders map[string]*ToolCallBuilder) *Message
	// BuildAssistantMessageByToolMessages returnDirect：工具结果合成最终 assistant 消息。
	BuildAssistantMessageByToolMessages(msg *AssistantMessage, toolMsgs []*Message) *AssistantMessage

	// ---- 响应侧（唯一解析入口）----

	// ParseResponseJSON 解析一帧响应（call 为整体 body；stream 为单个 SSE data）。
	ParseResponseJSON(ctx *StreamContext, body string)
	// ParseAssistantMessage 厂商 message/delta 节点 → 聚合助手消息列表。
	ParseAssistantMessage(acc *Accumulator, node map[string]any) []*AssistantMessage
}

type dialectEntry struct {
	d        Dialect
	priority int
	seq      int // 同优先级按注册序
}

var (
	dialectMu  sync.RWMutex
	dialectReg []dialectEntry
	dialectSeq int
)

// RegisterDialect 方言自注册（各方言包 init() 调用；priority 小者优先，
// 同优先级按注册序）。使用方 import 方言包即触发注册（数据库驱动惯例）。
func RegisterDialect(d Dialect, priority int) {
	dialectMu.Lock()
	defer dialectMu.Unlock()
	dialectSeq++
	dialectReg = append(dialectReg, dialectEntry{d: d, priority: priority, seq: dialectSeq})
}

func sortedDialects() []dialectEntry {
	dialectMu.RLock()
	defer dialectMu.RUnlock()
	out := append([]dialectEntry(nil), dialectReg...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].priority != out[j].priority {
			return out[i].priority < out[j].priority
		}
		return out[i].seq < out[j].seq
	})
	return out
}

// SelectDialect 方言选择（对标 ChatDialectManager.select）：
// ① cfg.Standard 精确匹配注册名 → ② 优先级序第一个 Matched(cfg) → ③ IsDefault 补位。
// 都不中返回 nil（上层报配置错误）。
func SelectDialect(cfg *Config) Dialect {
	entries := sortedDialects()
	if cfg != nil && cfg.Standard != "" {
		for _, e := range entries {
			if e.d.Name() == cfg.Standard {
				return e.d
			}
		}
	}
	for _, e := range entries {
		if e.d.Matched(cfg) {
			return e.d
		}
	}
	for _, e := range entries {
		if e.d.IsDefault() {
			return e.d
		}
	}
	return nil
}

// Dialects 已注册方言名（诊断用）。
func Dialects() []string {
	var out []string
	for _, e := range sortedDialects() {
		out = append(out, e.d.Name())
	}
	return out
}
