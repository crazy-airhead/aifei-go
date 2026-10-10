// Package harness Claude-Code 式编码代理容器（对标 solon-ai-harness，契约
// docs/arch/ai/06-talents-and-extended.md §4）。Go 首卡的域收窄（06 优先级
// 矩阵的 talents 只落地了 file/sys/diff/web/text2sql 五件）：AgentDefinition
// 的工具名映射到 Go 已有 talent 工具集；harness 专属的 terminal/code/memory/
// skill/lsp 族 talent 留缝（ToolMapper 可注入），随后续 talents 解锁补齐。
//
// 组成：Engine（装配 + 懒加载主代理 + 运行时热更新）、AgentDefinition
//（YAML frontmatter + 内置 markdown 代理 embed）、AgentManager（注册表 +
// 目录回源）、AgentFactory（definition → react.Builder）、PermissionEngine
//（规则评估 + HITL 策略对接）、CommandRegistry（斜杠命令 + markdown 自定义
// 命令）、TaskTalent/GenerateTalent（子代理调度与动态创建）。
package harness

import (
	"bytes"
	"embed"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed agents/*.md
var builtinAgentsFS embed.FS

// 内置代理（classpath META-INF/.../agents/ 的 Go embed 对应物）。
const (
	AgentMain   = "main"
	AgentGeneral = "general"
	builtinAgentDir = "agents"
)

// AgentMainName 主代理名常量（Java AGENT_MAIN）。
const AgentMainName = AgentMain

// Metadata frontmatter 字段全集（对标 AgentDefinition.Metadata；@Deprecated
// 字段解析保留、不消费）。
type Metadata struct {
	Enabled         bool     `yaml:"enabled"`
	Hidden          bool     `yaml:"hidden"`
	Primary         bool     `yaml:"primary"`
	Name            string   `yaml:"name"`
	Description     string   `yaml:"description"`
	Model           string   `yaml:"model"`
	Tools           []string `yaml:"tools"`
	DisallowedTools []string `yaml:"disallowedTools"`
	PermissionRules []PermissionRule `yaml:"permissionRules"`
	Skills          []string `yaml:"skills"`     // Deprecated
	McpServers      []string `yaml:"mcpServers"` // Deprecated
	Hooks           any      `yaml:"hooks"`      // Deprecated（保留字段不解析）
	Memory          string   `yaml:"memory"`     // Deprecated
	Background      *bool    `yaml:"background"` // Deprecated
	Isolation       string   `yaml:"isolation"`  // Deprecated
	TeamName        string   `yaml:"teamName"`   // Deprecated
}

// AgentDefinition 代理定义（frontmatter + systemPrompt 正文）。
type AgentDefinition struct {
	Metadata     *Metadata
	SystemPrompt string
	MountAlias   string
}

// Name 名称。
func (d *AgentDefinition) Name() string { return d.Metadata.Name }

// IsHidden 隐藏（不进可用子代理清单）。
func (d *AgentDefinition) IsHidden() bool { return d.Metadata.Hidden }

// Copy 深拷贝（permissionRules 手工复制——yaml 序列化不保身份）。
func (d *AgentDefinition) Copy() *AgentDefinition {
	m := *d.Metadata
	m.Tools = append([]string(nil), d.Metadata.Tools...)
	m.DisallowedTools = append([]string(nil), d.Metadata.DisallowedTools...)
	m.PermissionRules = append([]PermissionRule(nil), d.Metadata.PermissionRules...)
	return &AgentDefinition{Metadata: &m, SystemPrompt: d.SystemPrompt, MountAlias: d.MountAlias}
}

// DefinitionBuilder 链式构建（对标 AgentDefinition.Builder）。
type DefinitionBuilder struct {
	m           *Metadata
	promptParts []string
}

// NewDefinitionBuilder 起步（name 必填）。
func NewDefinitionBuilder(name string) *DefinitionBuilder {
	return &DefinitionBuilder{m: &Metadata{Name: name, Enabled: true}}
}

// Description 描述。
func (b *DefinitionBuilder) Description(s string) *DefinitionBuilder { b.m.Description = s; return b }

// SystemPrompt 附加正文（多段 \n\n 连接）。
func (b *DefinitionBuilder) SystemPrompt(s string) *DefinitionBuilder {
	b.promptParts = append(b.promptParts, s)
	return b
}

// ToolsAdd 工具追加。
func (b *DefinitionBuilder) ToolsAdd(tools ...string) *DefinitionBuilder {
	b.m.Tools = append(b.m.Tools, tools...)
	return b
}

// Hidden 隐藏标记。
func (b *DefinitionBuilder) Hidden(v bool) *DefinitionBuilder { b.m.Hidden = v; return b }

// Primary 主代理标记。
func (b *DefinitionBuilder) Primary(v bool) *DefinitionBuilder { b.m.Primary = v; return b }

// PermissionRulesAdd 权限规则追加。
func (b *DefinitionBuilder) PermissionRulesAdd(rules ...PermissionRule) *DefinitionBuilder {
	b.m.PermissionRules = append(b.m.PermissionRules, rules...)
	return b
}

// Build 产出定义。
func (b *DefinitionBuilder) Build() *AgentDefinition {
	return &AgentDefinition{Metadata: b.m, SystemPrompt: strings.Join(b.promptParts, "\n\n")}
}

// ParseAgentMarkdown markdown（frontmatter + 正文）→ 定义（对标
// fromParsedMarkdown）。frontmatter 缺失/空/畸形按空 Metadata 容忍。
func ParseAgentMarkdown(content string) (*AgentDefinition, error) {
	meta := &Metadata{}
	body := content

	if strings.HasPrefix(content, "---\n") || strings.HasPrefix(content, "---\r\n") {
		rest := content[4:]
		if end := strings.Index(rest, "\n---"); end >= 0 {
			fm := rest[:end]
			after := rest[end+4:]
			after = strings.TrimLeft(after, "\r\n")
			if err := yaml.Unmarshal([]byte(fm), meta); err != nil {
				return nil, fmt.Errorf("harness: agent frontmatter parse: %w", err)
			}
			body = after
		}
	}
	return &AgentDefinition{Metadata: meta, SystemPrompt: body}, nil
}

// ToMarkdown 反向序列化（frontmatter + 正文；对标 toMarkdown）。
func (d *AgentDefinition) ToMarkdown() string {
	m := d.Metadata
	var fm bytes.Buffer
	fm.WriteString("name: " + m.Name + "\n")
	if m.Description != "" {
		fm.WriteString("description: " + m.Description + "\n")
	}
	if m.Hidden {
		fm.WriteString("hidden: true\n")
	}
	if m.Primary {
		fm.WriteString("primary: true\n")
	}
	if !m.Enabled {
		fm.WriteString("enabled: false\n")
	}
	if m.Model != "" {
		fm.WriteString("model: " + m.Model + "\n")
	}
	if len(m.Tools) > 0 {
		fm.WriteString("tools:\n")
		for _, t := range m.Tools {
			fm.WriteString("  - " + t + "\n")
		}
	}
	if len(m.DisallowedTools) > 0 {
		fm.WriteString("disallowedTools:\n")
		for _, t := range m.DisallowedTools {
			fm.WriteString("  - " + t + "\n")
		}
	}
	if len(m.PermissionRules) > 0 {
		fm.WriteString("permissionRules:\n")
		for _, r := range m.PermissionRules {
			fm.WriteString("  - toolName: \"" + r.ToolName + "\"\n")
			fm.WriteString("    behavior: " + strings.ToLower(r.Behavior.String()) + "\n")
			if r.Pattern != "" {
				fm.WriteString("    pattern: \"" + r.Pattern + "\"\n")
			}
			if r.Priority != 0 {
				fm.WriteString("    priority: " + fmt.Sprint(r.Priority) + "\n")
			}
		}
	}

	out := "---\n" + fm.String() + "---\n\n" + d.SystemPrompt
	return out
}

// BuiltinAgents 内置代理定义（explore/general/plan/bash/git-summary）。
func BuiltinAgents() ([]*AgentDefinition, error) {
	entries, err := builtinAgentsFS.ReadDir(builtinAgentDir)
	if err != nil {
		return nil, err
	}
	var out []*AgentDefinition
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := builtinAgentsFS.ReadFile(builtinAgentDir + "/" + e.Name())
		if err != nil {
			return nil, err
		}
		def, err := ParseAgentMarkdown(string(data))
		if err != nil {
			return nil, fmt.Errorf("harness: builtin agent %s: %w", e.Name(), err)
		}
		def.MountAlias = BuiltinAgentMount
		out = append(out, def)
	}
	return out, nil
}

// BuiltinAgentMount 内置挂载别名（Java @harness-agents）。
const BuiltinAgentMount = "@harness-agents"
