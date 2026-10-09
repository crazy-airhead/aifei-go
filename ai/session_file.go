package ai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// FileSession 文件会话（对标 FileChatSession）：MemorySession 缓存层 + NDJSON
// 持久化（<id>.messages.ndjson，一行一条消息 JSON）。system 消息不落盘（重建
// 会话时由调用方重新注入，与 Java 一致）；attrs 不持久化。
//
// 与 Java 的差异：持久化统一走原子重写（临时文件 + fsync + rename），拒绝
// 追加模式可能留下的半行损坏；Session 接口无错误通道，落盘失败记入
// LastError() 且内存层照常工作（Java 仅打日志吞掉）。
type FileSession struct {
	mu      sync.Mutex
	file    string
	dir     string
	cache   *MemorySession
	lastErr error
}

// NewFileSession 文件会话（dir 为会话文件目录，对标 Java FileChatSession(sessionId, dir)
// 的参数反転——Go 以可缺省的 id 收尾；id 缺省随机）。已有文件自动装载。
func NewFileSession(dir string, id ...string) *FileSession {
	sid := ""
	if len(id) > 0 {
		sid = id[0]
	}
	if sid == "" {
		sid = newGUID()
	}
	s := &FileSession{
		file:  filepath.Join(dir, sid+".messages.ndjson"),
		dir:   dir,
		cache: NewMemorySession(sid),
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.lastErr = err
		return s
	}
	s.load()
	return s
}

// load 装载既有 NDJSON（坏行跳过并记入 lastErr，不阻断可用行——Java 解析失败
// 会炸构造器，Go 宽容装载）。
func (s *FileSession) load() {
	f, err := os.Open(s.file)
	if err != nil {
		return // 无文件 = 空会话
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // 大块消息（base64 图片等）
	var msgs []*Message
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		msg := &Message{}
		if err := json.Unmarshal(line, msg); err != nil {
			if s.lastErr == nil {
				s.lastErr = err
			}
			continue
		}
		msgs = append(msgs, msg)
	}
	if err := scanner.Err(); err != nil && s.lastErr == nil {
		s.lastErr = err
	}
	if len(msgs) > 0 {
		s.cache.AddMessage(msgs...)
	}
}

func (s *FileSession) SessionID() string { return s.cache.SessionID() }
func (s *FileSession) Messages() []*Message {
	return s.cache.Messages()
}
func (s *FileSession) LatestMessages(window int) []*Message {
	return s.cache.LatestMessages(window)
}
func (s *FileSession) IsEmpty() bool { return s.cache.IsEmpty() }
func (s *FileSession) Attrs() map[string]any {
	return s.cache.Attrs()
}

func (s *FileSession) AddMessage(msgs ...*Message) {
	if len(msgs) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache.AddMessage(msgs...)
	s.persist()
}

// RemoveLatestMessage 末尾安全删除 n 条（toolCalls/tool 分组级联与 MemorySession
// 一致）后同步重写磁盘全量。
func (s *FileSession) RemoveLatestMessage(n int) {
	if n <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache.RemoveLatestMessage(n)
	s.persist()
}

// Clear 清空内存并删除会话文件。
func (s *FileSession) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache.Clear()
	if err := os.Remove(s.file); err != nil && !os.IsNotExist(err) && s.lastErr == nil {
		s.lastErr = err
	}
}

// LastError 最近一次装载/落盘错误（Session 接口无错误通道的逃生舱；nil = 健康）。
func (s *FileSession) LastError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

// persist 原子重写全量非 system 消息：同目录临时文件 → fsync → rename。
func (s *FileSession) persist() {
	tmp, err := os.CreateTemp(s.dir, ".aifei-session-*")
	if err != nil {
		s.recordErr(err)
		return
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmp.Name())
		}
	}()

	w := bufio.NewWriter(tmp)
	enc := json.NewEncoder(w)
	for _, msg := range s.cache.Messages() {
		if msg.Role == RoleSystem {
			continue
		}
		if err := enc.Encode(msg); err != nil { // Encoder 自带换行 = NDJSON
			s.recordErr(err)
			return
		}
	}
	if err := w.Flush(); err != nil {
		s.recordErr(err)
		return
	}
	if err := tmp.Sync(); err != nil {
		s.recordErr(err)
		return
	}
	if err := tmp.Close(); err != nil {
		s.recordErr(err)
		return
	}
	if err := os.Rename(tmp.Name(), s.file); err != nil {
		s.recordErr(err)
		return
	}
	ok = true
}

func (s *FileSession) recordErr(err error) {
	if s.lastErr == nil {
		s.lastErr = err
	}
}
