// Package mount 统一资源挂载系统（对标 solon-ai-talent-mount：Mount/MountSource/
// MountManager/SkillCatalog/AgentCatalog——逻辑路径 @alias/sub 解析、技能
// SKILL.md 扫描与渲染、代理 AGENT.md 目录；Go 首卡落文件系统 MountSource）。
package mount

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MountType 挂载类型。
type MountType uint8

const (
	// TypeFiles 文件挂载。
	TypeFiles MountType = iota
	// TypeSkills 技能挂载。
	TypeSkills
	// TypeAgents 代理挂载。
	TypeAgents
)

// Mount 挂载点（不可变值对象）。
type Mount struct {
	alias       string
	description string
	mountType   MountType
	primary     bool
	enabled     bool
	writeable   bool
	visible     bool
	source      MountSource
}

// MountBuilder 构建。
type MountBuilder struct{ m Mount }

// NewMountBuilder 起步。
func NewMountBuilder() *MountBuilder {
	return &MountBuilder{m: Mount{enabled: true, visible: true}}
}

// Alias 别名（自动补 @ 前缀；禁 /\ 与空白与 @workspace）。
func (b *MountBuilder) Alias(alias string) *MountBuilder {
	alias = strings.TrimSpace(alias)
	if alias != "" && !strings.HasPrefix(alias, "@") {
		alias = "@" + alias
	}
	b.m.alias = alias
	return b
}

// Description 描述。
func (b *MountBuilder) Description(d string) *MountBuilder { b.m.description = d; return b }

// Type 类型。
func (b *MountBuilder) Type(t MountType) *MountBuilder { b.m.mountType = t; return b }

// Primary 主挂载。
func (b *MountBuilder) Primary(v bool) *MountBuilder { b.m.primary = v; return b }

// Enabled 启用。
func (b *MountBuilder) Enabled(v bool) *MountBuilder { b.m.enabled = v; return b }

// Writeable 可写。
func (b *MountBuilder) Writeable(v bool) *MountBuilder { b.m.writeable = v; return b }

// Source 来源。
func (b *MountBuilder) Source(s MountSource) *MountBuilder { b.m.source = s; return b }

// Build 校验产出。
func (b *MountBuilder) Build() (*Mount, error) {
	if b.m.alias == "" {
		return nil, errf("mount: alias is required")
	}
	if strings.ContainsAny(b.m.alias[1:], "/\\") || strings.Contains(b.m.alias, " ") {
		return nil, errf("mount: alias %q contains illegal characters", b.m.alias)
	}
	if b.m.source == nil {
		return nil, errf("mount: source is required")
	}
	m := b.m
	return &m, nil
}

// Alias 等 getter。
func (m *Mount) Alias() string       { return m.alias }
func (m *Mount) Description() string { return m.description }
func (m *Mount) Type() MountType     { return m.mountType }
func (m *Mount) Primary() bool       { return m.primary }
func (m *Mount) Enabled() bool       { return m.enabled }
func (m *Mount) Writeable() bool     { return m.writeable }
func (m *Mount) Visible() bool       { return m.visible }
func (m *Mount) Source() MountSource { return m.source }

// MountSource 虚拟文件来源接口（对标同名——Go 首卡文件系统实现）。
type MountSource interface {
	// Exists 是否存在。
	Exists(relPath string) bool
	// List 目录列表。
	List(relPath string) ([]string, error)
	// Read 文件读取。
	Read(relPath string) ([]byte, error)
	// LocalRoot 本地根（shell 可达性判定）。
	LocalRoot() string
}

// FileMountSource 本地文件系统来源。
type FileMountSource struct{ root string }

// NewFileMountSource 构造（root 绝对化）。
func NewFileMountSource(root string) (*FileMountSource, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &FileMountSource{root: abs}, nil
}

func (s *FileMountSource) resolve(rel string) (string, error) {
	abs := filepath.Join(s.root, filepath.FromSlash(rel))
	if !strings.HasPrefix(abs, s.root+string(filepath.Separator)) && abs != s.root {
		return "", errf("mount: path escape: %s", rel)
	}
	return abs, nil
}

// Exists 存在性。
func (s *FileMountSource) Exists(rel string) bool {
	abs, err := s.resolve(rel)
	return err == nil && fileExists(abs)
}

// List 列表。
func (s *FileMountSource) List(rel string) ([]string, error) {
	abs, err := s.resolve(rel)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out, nil
}

// Read 读取。
func (s *FileMountSource) Read(rel string) ([]byte, error) {
	abs, err := s.resolve(rel)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(abs)
}

// LocalRoot 本地根。
func (s *FileMountSource) LocalRoot() string { return s.root }

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// MountManager 挂载管理器。
type MountManager struct {
	workDir  string
	mounts   map[string]*Mount
	aliases  []string
	skills   *SkillCatalog
	agents   *AgentCatalog
}

// NewMountManager 构造（内置 @workspace 文件挂载）。
func NewMountManager(workDir string) (*MountManager, error) {
	src, err := NewFileMountSource(workDir)
	if err != nil {
		return nil, err
	}
	ws, err := NewMountBuilder().Alias("@workspace").Description("工作区").Type(TypeFiles).Source(src).Build()
	if err != nil {
		return nil, err
	}
	m := &MountManager{workDir: workDir, mounts: map[string]*Mount{"@workspace": ws}, aliases: []string{"@workspace"}}
	m.skills = newSkillCatalog(m)
	m.agents = newAgentCatalog(m)
	return m, nil
}

// Register 注册/替换（刷新目录；@workspace 保留名拒绝）。
func (m *MountManager) Register(mount *Mount) error {
	if mount == nil || mount.alias == "" {
		return errf("mount: invalid mount")
	}
	if mount.alias == "@workspace" {
		return errf("mount: @workspace is reserved")
	}
	if _, exists := m.mounts[mount.alias]; !exists {
		m.aliases = append(m.aliases, mount.alias)
	}
	m.mounts[mount.alias] = mount
	m.skills.refresh()
	m.agents.refresh()
	return nil
}

// Remove 移除。
func (m *MountManager) Remove(alias string) {
	if alias == "@workspace" {
		return
	}
	if _, ok := m.mounts[alias]; ok {
		delete(m.mounts, alias)
		for i, a := range m.aliases {
			if a == alias {
				m.aliases = append(m.aliases[:i], m.aliases[i+1:]...)
				break
			}
		}
	}
	m.skills.refresh()
	m.agents.refresh()
}

// Get 按别名取。
func (m *MountManager) Get(alias string) *Mount { return m.mounts[alias] }

// Mounts 全部（注册序）。
func (m *MountManager) Mounts() []*Mount {
	out := make([]*Mount, 0, len(m.aliases))
	for _, a := range m.aliases {
		if mt := m.mounts[a]; mt != nil {
			out = append(out, mt)
		}
	}
	return out
}

// ResolveResource 逻辑路径解析（@alias/sub → 挂载 + 源相对路径）。
type ResolvedResource struct {
	LogicalPath string
	MountAlias  string
	Mount       *Mount
	SourcePath  string
}

// ResolveResource 解析（@alias/… 或相对工作区）。
func (m *MountManager) ResolveResource(path string) (*ResolvedResource, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errf("mount: empty path")
	}
	if strings.HasPrefix(path, "@") {
		idx := strings.Index(path, "/")
		alias, sub := path, ""
		if idx >= 0 {
			alias, sub = path[:idx], path[idx+1:]
		}
		mt := m.mounts[alias]
		if mt == nil {
			return nil, errf("mount: unknown alias %s", alias)
		}
		return &ResolvedResource{LogicalPath: path, MountAlias: alias, Mount: mt, SourcePath: sub}, nil
	}
	ws := m.mounts["@workspace"]
	return &ResolvedResource{LogicalPath: "@workspace/" + path, MountAlias: "@workspace",
		Mount: ws, SourcePath: path}, nil
}

// Skills 技能目录。
func (m *MountManager) Skills() *SkillCatalog { return m.skills }

// Agents 代理目录。
func (m *MountManager) Agents() *AgentCatalog { return m.agents }

// SkillDescriptor 技能描述。
type SkillDescriptor struct {
	ID          string
	Name        string
	Description string
	Version     string
	Dir         string // 挂载内相对目录
	MountAlias  string
}

// SkillCatalog 技能目录（扫描 SKILL.md）。
type SkillCatalog struct {
	manager *MountManager
	skills  []*SkillDescriptor
	byID    map[string]*SkillDescriptor
}

func newSkillCatalog(m *MountManager) *SkillCatalog {
	return &SkillCatalog{manager: m, byID: map[string]*SkillDescriptor{}}
}

// Refresh 重扫。
func (c *SkillCatalog) refresh() {
	c.skills = nil
	c.byID = map[string]*SkillDescriptor{}
	for _, mt := range c.manager.Mounts() {
		if !mt.Enabled() || mt.Type() != TypeSkills {
			continue
		}
		c.scanMount(mt, "SKILL.md")
	}
}

// scanMount 递归扫描标记文件（深度 ≤3）。
func (c *SkillCatalog) scanMount(mt *Mount, marker string) {
	root := mt.Source()
	walkDepth(root, "", 3, func(dir string, entries []string) {
		for _, e := range entries {
			if e != marker && !strings.EqualFold(e, marker) {
				continue
			}
			rel := dir
			if rel == "" {
				rel = "."
			}
			id := mt.Alias()
			if rel != "." {
				id = id + "/" + rel
			}
			name := filepath.Base(rel)
			if rel == "." {
				name = strings.TrimPrefix(mt.Alias(), "@")
			}
			sd := &SkillDescriptor{ID: id, Name: name, Dir: rel, MountAlias: mt.Alias()}
			if data, err := root.Read(filepath.ToSlash(filepath.Join(filepath.FromSlash(rel), marker))); err == nil {
				sd.Description, sd.Version = parseFrontMatterMeta(string(data))
			}
			c.skills = append(c.skills, sd)
			c.byID[id] = sd
		}
	})
}

// walkDepth 限深遍历回调（dir 相对根）。
func walkDepth(src MountSource, dir string, depth int, fn func(dir string, entries []string)) {
	if depth < 0 {
		return
	}
	entries, err := src.List(dir)
	if err != nil {
		return
	}
	fn(dir, entries)
	if dir == "" && depth == 0 {
		return
	}
	for _, e := range entries {
		if strings.HasPrefix(e, ".") {
			continue
		}
		sub := e
		if dir != "" {
			sub = dir + "/" + e
		}
		if l, err := src.List(sub); err == nil && len(l) >= 0 {
			// 是目录（List 成功）→ 递归
			walkDepth(src, sub, depth-1, fn)
		}
	}
}

// parseFrontMatterMeta 提取 description/version（--- YAML 头）。
func parseFrontMatterMeta(content string) (desc, version string) {
	if !strings.HasPrefix(content, "---") {
		return
	}
	rest := content[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return
	}
	for _, line := range strings.Split(rest[:end], "\n") {
		kv := strings.SplitN(line, ":", 2)
		if len(kv) != 2 {
			continue
		}
		k := strings.TrimSpace(kv[0])
		v := strings.Trim(strings.TrimSpace(kv[1]), `"'`)
		switch k {
		case "description":
			desc = v
		case "version":
			version = v
		}
	}
	return
}

// All 全部技能。
func (c *SkillCatalog) All() []*SkillDescriptor { return c.skills }

// Find 按名/ID 找。
func (c *SkillCatalog) Find(name string) *SkillDescriptor {
	if sd := c.byID[name]; sd != nil {
		return sd
	}
	if sd := c.byID["@"+name]; sd != nil {
		return sd
	}
	for _, sd := range c.skills {
		if sd.Name == name {
			return sd
		}
	}
	return nil
}

// AgentDescriptor 代理描述。
type AgentDescriptor struct {
	ID          string
	Name        string
	Description string
	Dir         string
	MountAlias  string
}

// AgentCatalog 代理目录（AGENT.md）。
type AgentCatalog struct {
	manager *MountManager
	agents  []*AgentDescriptor
	byID    map[string]*AgentDescriptor
}

func newAgentCatalog(m *MountManager) *AgentCatalog {
	return &AgentCatalog{manager: m, byID: map[string]*AgentDescriptor{}}
}

func (c *AgentCatalog) refresh() {
	c.agents = nil
	c.byID = map[string]*AgentDescriptor{}
	for _, mt := range c.manager.Mounts() {
		if !mt.Enabled() || mt.Type() != TypeAgents {
			continue
		}
		c.scanMount(mt, "AGENT.md")
	}
}

func (c *AgentCatalog) scanMount(mt *Mount, marker string) {
	root := mt.Source()
	walkDepth(root, "", 3, func(dir string, entries []string) {
		for _, e := range entries {
			if e != marker && !strings.EqualFold(e, marker) {
				continue
			}
			rel := dir
			if rel == "" {
				rel = "."
			}
			id := mt.Alias()
			if rel != "." {
				id = id + "/" + rel
			}
			name := filepath.Base(rel)
			if rel == "." {
				name = strings.TrimPrefix(mt.Alias(), "@")
			}
			ad := &AgentDescriptor{ID: id, Name: name, Dir: rel, MountAlias: mt.Alias()}
			if data, err := root.Read(filepath.ToSlash(filepath.Join(filepath.FromSlash(rel), marker))); err == nil {
				ad.Description, _ = parseFrontMatterMeta(string(data))
			}
			c.agents = append(c.agents, ad)
			c.byID[id] = ad
		}
	})
}

// All 全部代理。
func (c *AgentCatalog) All() []*AgentDescriptor { return c.agents }

// Find 按名/ID 找。
func (c *AgentCatalog) Find(name string) *AgentDescriptor {
	if ad := c.byID[name]; ad != nil {
		return ad
	}
	if ad := c.byID["@"+name]; ad != nil {
		return ad
	}
	for _, ad := range c.agents {
		if ad.Name == name {
			return ad
		}
	}
	return nil
}

// errf 错误便捷。
func errf(format string, args ...any) error {
	return &mountError{msg: fmt.Sprintf(format, args...)}
}

type mountError struct{ msg string }

func (e *mountError) Error() string { return e.msg }
