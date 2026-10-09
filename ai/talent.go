package ai

import "strings"

// Talent 能力包 SPI（对标 solon-ai Talent）：工具包 + 系统指令注入 + 准入控制
// 三合一。能力包矩阵（file/sys/diff/web 等 18 项）按 06-talents 分期落地，
// 本接口先行固化注入契约。
type Talent interface {
	IsEnabled() bool
	Name() string // 指令块标题用；TalentBase 缺省空（纯工具集形态）
	Description() string
	IsSupported(p *Prompt) bool // 准入：按 Prompt 决定本次是否激活
	OnAttach(p *Prompt)         // 激活副作用（写 attrs 等）
	Instruction(p *Prompt) string
	Tools(p *Prompt) []FunctionTool
}

// TalentBase 默认实现（恒启用、无准入限制、无副作用、无指令、无工具），
// 嵌入者按需覆写。
type TalentBase struct{}

func (TalentBase) IsEnabled() bool              { return true }
func (TalentBase) Name() string                 { return "" }
func (TalentBase) Description() string          { return "" }
func (TalentBase) IsSupported(*Prompt) bool     { return true }
func (TalentBase) OnAttach(*Prompt)             {}
func (TalentBase) Instruction(*Prompt) string   { return "" }
func (TalentBase) Tools(*Prompt) []FunctionTool { return nil }

// ActiveTalents 激活能力包（对标 TalentUtil.activeTalents）：准入 → onAttach →
// 指令块聚合 → 工具部署（meta 染色 talent=<name>）。工具同时进入 opts.Tools。
func ActiveTalents(opts *ChatOptions, p *Prompt, sb *strings.Builder) *strings.Builder {
	if opts == nil {
		return sb
	}
	for _, t := range opts.Talents {
		if t == nil || !t.IsEnabled() {
			continue
		}
		if p != nil && !t.IsSupported(p) {
			continue
		}
		if p != nil {
			t.OnAttach(p)
		}
		injectTalentInstruction(t, p, sb)
		for _, tool := range t.Tools(p) {
			if tool == nil {
				continue
			}
			if name := t.Name(); name != "" {
				metaPut(tool, "talent", name)
			}
			opts.Tools = append(opts.Tools, tool)
		}
	}
	return sb
}

// metaPut 工具 meta 染色（接口面只有 Meta() 读取；可变实现走 MetaSet 鸭子缝）。
func metaPut(tool FunctionTool, k string, v any) {
	if mp, ok := tool.(interface{ MetaSet(string, any) }); ok {
		mp.MetaSet(k, v)
		return
	}
	if m := tool.Meta(); m != nil {
		m[k] = v
	}
}

// injectTalentInstruction 指令块（Java TalentUtil 逐字对标形态）：
// 分割线开启独立空间 + 标题行 + 包裹标签 + 指令 + 工具作用域声明。
func injectTalentInstruction(t Talent, p *Prompt, sb *strings.Builder) {
	ins := t.Instruction(p)
	if ins == "" {
		// 无指令不展示（只作工具集用）
		return
	}
	tools := t.Tools(p)
	name := t.Name()
	if name == "" {
		name = "talent"
	}

	sb.WriteString("\n---\n")
	sb.WriteString("# [Talent: " + name + "]")
	if t.Description() != "" {
		sb.WriteString(" - " + t.Description())
	}
	sb.WriteString("\n<Talent:" + name + ">\n\n")

	sb.WriteString(strings.TrimSpace(ins) + "\n")

	if len(tools) > 0 {
		names := make([]string, 0, len(tools))
		for _, tl := range tools {
			if tl != nil {
				names = append(names, "`"+tl.Name()+"`")
			}
		}
		sb.WriteString("\n> **工具作用域**: 此工具包指令适用于以下工具的调用: " +
			strings.Join(names, ", ") + "\n")
	}

	sb.WriteString("\n\n</Talent:" + name + ">\n")
	sb.WriteString("---\n")
}
