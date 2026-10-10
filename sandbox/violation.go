// violation.go 违规存储（对标 SandboxViolationStore）：按类别归档、
// ignoreViolations 子串过滤。
package sandbox

import (
	"strings"
	"sync"

	"github.com/crazy-airhead/aifei-go/log"
)

// ViolationStore 线程安全的会话违规存储。
type ViolationStore struct {
	mu          sync.RWMutex
	violations  map[string][]string
	ignoreRules map[string][]string
}

// NewViolationStore 构造；ignoreViolations 的键为类别、值为消息子串
// 过滤器（nil 视为空）。
func NewViolationStore(ignoreViolations map[string][]string) *ViolationStore {
	if ignoreViolations == nil {
		ignoreViolations = map[string][]string{}
	}
	return &ViolationStore{
		violations:  map[string][]string{},
		ignoreRules: ignoreViolations,
	}
}

// Record 记录一次违规（命中忽略过滤器则静默丢弃）。
func (s *ViolationStore) Record(category, message string) {
	if s.shouldIgnore(category, message) {
		log.Debug("sandbox: ignoring violation %s: %s", category, message)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.violations[category] = append(s.violations[category], message)
	log.Debug("sandbox: violation recorded %s: %s", category, message)
}

// Violations 取某类别的全部违规消息（无则空切片）。
func (s *ViolationStore) Violations(category string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if list, ok := s.violations[category]; ok {
		return append([]string(nil), list...)
	}
	return nil
}

// Categories 全部违规类别。
func (s *ViolationStore) Categories() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cats := make([]string, 0, len(s.violations))
	for c := range s.violations {
		cats = append(cats, c)
	}
	return cats
}

// HasViolations 是否有违规。
func (s *ViolationStore) HasViolations() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.violations) > 0
}

// Clear 清空。
func (s *ViolationStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.violations = map[string][]string{}
}

func (s *ViolationStore) shouldIgnore(category, message string) bool {
	for _, pattern := range s.ignoreRules[category] {
		if strings.Contains(message, pattern) {
			return true
		}
	}
	return false
}
