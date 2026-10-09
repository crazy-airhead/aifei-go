package agent

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/flow"
)

// Session 智能体会话（对标 AgentSession = ChatSession + 执行流快照）：
// ai.Session 的消息记忆 + 流上下文快照 + 挂起（HITL）状态。
type Session interface {
	ai.Session

	// UpdateSnapshot 同步/更新执行快照（内存会话为 no-op；文件会话落盘）。
	UpdateSnapshot()
	// Context 会话上下文（快照本体）。
	Context() flow.Context
	// Pending 挂起/恢复：pending=true 停止流程并标记；false 清除挂起标记。
	// reason 非空写入（空则移除）；与 Java 的差异：Go 以空串表达 null reason。
	Pending(pending bool, reason string)
	// IsPending 是否已挂起。
	IsPending() bool
	// PendingReason 挂起原因（无则空串）。
	PendingReason() string
}

// stoppedSetter 由 *flow.flowContext 实现（SetStopped 导出具体方法，经类型
// 断言使用——与 GoContext 同款缝）。
type stoppedSetter interface {
	SetStopped(bool)
}

// MemorySession 内存智能体会话（对标 InMemoryAgentSession）。
// AddMessage 过滤 system 角色消息（会话只保留对话流，系统提示随请求组装）；
// 构造时把 KeySession → 自身写入快照（Java KEY_SESSION 同款）。
type MemorySession struct {
	chat     *ai.MemorySession
	snapshot flow.Context
}

// NewMemorySession 内存会话（id 缺省随机；容量上限 50）。
func NewMemorySession(id ...string) *MemorySession {
	s := &MemorySession{chat: ai.NewMemorySession(id...), snapshot: flow.NewContext()}
	s.snapshot.Put(KeySession, s)
	return s
}

// NewMemorySessionMax 指定容量上限（<=0 不限）。
func NewMemorySessionMax(maxMessages int, id ...string) *MemorySession {
	s := &MemorySession{chat: ai.NewMemorySessionMax(maxMessages, id...), snapshot: flow.NewContext()}
	s.snapshot.Put(KeySession, s)
	return s
}

// NewMemorySessionOfContext 绑定既有流上下文（快照共享、容量不限——对标
// Java InMemoryAgentSession.of(FlowContext)）。
func NewMemorySessionOfContext(ctx flow.Context) *MemorySession {
	if ctx == nil {
		ctx = flow.NewContext()
	}
	s := &MemorySession{chat: ai.NewMemorySessionMax(0, newSessionID()), snapshot: ctx}
	s.snapshot.Put(KeySession, s)
	return s
}

func newSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "sess"
	}
	return hex.EncodeToString(b)
}

func (s *MemorySession) SessionID() string       { return s.chat.SessionID() }
func (s *MemorySession) Messages() []*ai.Message { return s.chat.Messages() }
func (s *MemorySession) LatestMessages(n int) []*ai.Message {
	return s.chat.LatestMessages(n)
}
func (s *MemorySession) RemoveLatestMessage(n int) { s.chat.RemoveLatestMessage(n) }

// AddMessage 追加消息（过滤 system 角色，再走基类裁剪）。
func (s *MemorySession) AddMessage(msgs ...*ai.Message) {
	var kept []*ai.Message
	for _, m := range msgs {
		if m != nil && m.Role == ai.RoleSystem {
			continue
		}
		kept = append(kept, m)
	}
	if len(kept) > 0 {
		s.chat.AddMessage(kept...)
	}
}

func (s *MemorySession) IsEmpty() bool         { return s.chat.IsEmpty() }
func (s *MemorySession) Clear()                { s.chat.Clear() }
func (s *MemorySession) Attrs() map[string]any { return s.chat.Attrs() }
func (s *MemorySession) UpdateSnapshot()       {} // 内存会话：快照即本体
func (s *MemorySession) Context() flow.Context { return s.snapshot }

// Pending 挂起/恢复（对标 AgentSession.pending 默认实现）：pending=true 时
// Stop()（经交换器停整流程）+ 置跨引擎 stopped 标记；false 只清标记（交换器
// 级停止属整次运行，不由此清除——Java stopped(false) 同款语义）。
func (s *MemorySession) Pending(pending bool, reason string) {
	ctx := s.snapshot
	if pending {
		ctx.Stop()
		if ss, ok := ctx.(stoppedSetter); ok {
			ss.SetStopped(true)
		}
	} else {
		if ss, ok := ctx.(stoppedSetter); ok {
			ss.SetStopped(false)
		}
	}
	if reason == "" {
		ctx.Remove(KeyPendingReason)
	} else {
		ctx.Put(KeyPendingReason, reason)
	}
}

func (s *MemorySession) IsPending() bool { return s.snapshot.IsStopped() }
func (s *MemorySession) PendingReason() string {
	v, _ := s.snapshot.Get(KeyPendingReason).(string)
	return v
}
