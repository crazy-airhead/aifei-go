// manager.go 代理定义注册表（对标 harness.agent.AgentManager）：内存 map +
// 目录回源（AgentCatalog），hidden 过滤，内置挂载合并。
package harness

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/crazy-airhead/aifei-go/log"
)

// AgentManager 代理注册表。
type AgentManager struct {
	mu         sync.RWMutex
	definitions map[string]*AgentDefinition
	catalogDir string // 自定义代理目录（可空）
}

// NewAgentManager 构造（内置代理预载）。
func NewAgentManager(catalogDir string) (*AgentManager, error) {
	m := &AgentManager{definitions: map[string]*AgentDefinition{}, catalogDir: catalogDir}
	builtins, err := BuiltinAgents()
	if err != nil {
		return nil, err
	}
	for _, def := range builtins {
		m.definitions[def.Name()] = def
	}
	return m, nil
}

// AddAgentIfAbsent 注册（已存在返回既有定义不覆盖——缓存优先于目录）。
func (m *AgentManager) AddAgentIfAbsent(def *AgentDefinition) *AgentDefinition {
	if def == nil || def.Name() == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.definitions[def.Name()]; ok {
		return old
	}
	m.definitions[def.Name()] = def
	return def
}

// Get 按名取定义（内存 → 目录回源）。
func (m *AgentManager) Get(name string) *AgentDefinition {
	m.mu.RLock()
	def, ok := m.definitions[name]
	m.mu.RUnlock()
	if ok {
		return def
	}
	if m.catalogDir == "" || name == "" {
		return nil
	}
	// 目录回源：{dir}/{name}.md
	path := filepath.Join(m.catalogDir, filepath.FromSlash(name)+".md")
	if !strings.HasPrefix(filepath.Clean(path), filepath.Clean(m.catalogDir)) {
		return nil // 路径逃逸防护
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	parsed, err := ParseAgentMarkdown(string(data))
	if err != nil {
		log.Warn("harness: agent %s parse failed: %v", name, err)
		return nil
	}
	if parsed.Name() != name {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.definitions[name] = parsed
	return parsed
}

// Agents 可用代理（hidden 过滤，按名排序）。
func (m *AgentManager) Agents() []*AgentDefinition {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []*AgentDefinition
	for _, def := range m.definitions {
		if def.IsHidden() {
			continue
		}
		out = append(out, def)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// InvalidateCache 清缓存（mount 刷新对应物；内置不清）。
func (m *AgentManager) InvalidateCache() {
	builtins, _ := BuiltinAgents()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.definitions = map[string]*AgentDefinition{}
	for _, def := range builtins {
		m.definitions[def.Name()] = def
	}
}
