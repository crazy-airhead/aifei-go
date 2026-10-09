package ai

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
)

// AttrSessionID 会话 id 在 prompt attrs / toolContext 中的注入键。
const AttrSessionID = "sessionId"

// Session 会话（对标 ChatSession）：跨请求的消息记忆 + 会话级 attrs。
type Session interface {
	SessionID() string
	Messages() []*Message
	LatestMessages(window int) []*Message
	RemoveLatestMessage(n int)
	AddMessage(msgs ...*Message)
	IsEmpty() bool
	Clear()
	Attrs() map[string]any
}

// MemorySession 内存会话（对标 InMemoryChatSession）。超过 maxMessages 时从头
// 移除非 system 消息；带 toolCalls 的 assistant 连带其紧随的 tool 消息成组移除，
// 保证剩余历史里 tool_calls 与 tool 结果不脱节。
type MemorySession struct {
	mu          sync.RWMutex
	sessionID   string
	messages    []*Message
	maxMessages int
	attrs       map[string]any
}

// NewMemorySession 内存会话（id 缺省随机；容量上限 50，对标 Java 默认）。
func NewMemorySession(id ...string) *MemorySession {
	sid := ""
	if len(id) > 0 {
		sid = id[0]
	}
	if sid == "" {
		sid = newGUID()
	}
	return &MemorySession{sessionID: sid, maxMessages: 50}
}

// NewMemorySessionMax 指定容量上限（<=0 不限）。
func NewMemorySessionMax(maxMessages int, id ...string) *MemorySession {
	s := NewMemorySession(id...)
	s.maxMessages = maxMessages
	return s
}

func (s *MemorySession) SessionID() string { return s.sessionID }

// Messages 快照拷贝（线程安全；Java 返回活列表，Go 惯用拷贝防外部改写）。
func (s *MemorySession) Messages() []*Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]*Message(nil), s.messages...)
}

// LatestMessages 最近 window 条（向前对齐到 user 消息，保证对话逻辑完整、
// 绝不出现 tool/assistant 错位；window<=0 返回全部）。
func (s *MemorySession) LatestMessages(window int) []*Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	all := s.messages
	if len(all) == 0 {
		return nil
	}
	if len(all) <= window || window <= 0 {
		return append([]*Message(nil), all...)
	}
	start := len(all) - window
	found := false
	for i := start; i >= 0; i-- {
		if all[i].Role == RoleUser {
			start, found = i, true
			break
		}
	}
	if !found {
		// 兜底：起点前无 user（全是 tool/assistant），向后找第一个 user
		for i := start; i < len(all); i++ {
			if all[i].Role == RoleUser {
				start = i
				break
			}
		}
	}
	return append([]*Message(nil), all[start:]...)
}

// RemoveLatestMessage 从末尾安全删除 n 条：带 toolCalls 的 assistant 连带其
// 后续 tool 消息；tool 消息被删后孤立的 toolCalls assistant 一并删除。
func (s *MemorySession) RemoveLatestMessage(n int) {
	if n <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := 0; i < n && len(s.messages) > 0; i++ {
		last := s.messages[len(s.messages)-1]
		switch {
		case last.Role == RoleAssistant && len(assistantToolCalls(last)) > 0:
			s.messages = s.messages[:len(s.messages)-1]
			for len(s.messages) > 0 && s.messages[len(s.messages)-1].Role == RoleTool {
				s.messages = s.messages[:len(s.messages)-1]
			}
		case last.Role == RoleTool:
			s.messages = s.messages[:len(s.messages)-1]
			for len(s.messages) > 0 && s.messages[len(s.messages)-1].Role == RoleTool {
				s.messages = s.messages[:len(s.messages)-1]
			}
			if len(s.messages) > 0 && s.messages[len(s.messages)-1].Role == RoleAssistant &&
				len(assistantToolCalls(s.messages[len(s.messages)-1])) > 0 {
				s.messages = s.messages[:len(s.messages)-1]
			}
		default:
			s.messages = s.messages[:len(s.messages)-1]
		}
	}
}

func assistantToolCalls(m *Message) []ToolCall {
	if am, ok := m.Content.(*AssistantMessage); ok {
		return am.ToolCalls
	}
	return nil
}

func (s *MemorySession) AddMessage(msgs ...*Message) {
	if len(msgs) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, msgs...)
	if s.maxMessages > 0 && len(s.messages) > s.maxMessages {
		s.removeNonSystemMessages(len(msgs))
	}
}

// removeNonSystemMessages 从头移除最多 size 条非 system 消息（toolCalls 级联
// 可能超出 size；system 消息保留原位——不能随游标一并截掉）。
func (s *MemorySession) removeNonSystemMessages(size int) {
	out := s.messages[:0]
	removed := 0
	for i := 0; i < len(s.messages); i++ {
		m := s.messages[i]
		if m.Role == RoleSystem || removed >= size {
			out = append(out, m)
			continue
		}
		removed++
		if len(assistantToolCalls(m)) > 0 {
			// 连带其后紧邻的 tool 消息
			for i+1 < len(s.messages) && s.messages[i+1].Role == RoleTool {
				i++
				removed++
			}
		}
	}
	s.messages = out
}

func (s *MemorySession) IsEmpty() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.messages) == 0
}

func (s *MemorySession) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = nil
}

// Attrs 懒初始化的会话级标注袋。
func (s *MemorySession) Attrs() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attrs == nil {
		s.attrs = map[string]any{}
	}
	return s.attrs
}

// newGUID 32 位十六进制随机 id（Java Utils.guid 对应物，零依赖实现）。
func newGUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
