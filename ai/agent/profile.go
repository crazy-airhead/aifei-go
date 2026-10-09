package agent

import (
	"sort"
	"strings"
)

// Profile 智能体档案（对标 AgentProfile）：能力画像与交互契约。
// 能力/约束保序去重；缺省 inputModes/outputModes 均为 ["text"] 且预置
// "assistant" 能力（与 Java 缺省一致）。
type Profile struct {
	capabilities []string
	constraints  []string
	style        string
	inputModes   []string
	outputModes  []string
	metadata     map[string]any
}

// NewProfile 空档案（缺省档位：text/text + assistant 能力）。
func NewProfile() *Profile {
	return &Profile{
		capabilities: []string{"assistant"},
		inputModes:   []string{"text"},
		outputModes:  []string{"text"},
	}
}

func (p *Profile) appendUnique(list []string, v string) []string {
	for _, has := range list {
		if has == v {
			return list
		}
	}
	return append(list, v)
}

// CapabilityAdd 追加能力（去重）。
func (p *Profile) CapabilityAdd(capability string) *Profile {
	p.capabilities = p.appendUnique(p.capabilities, capability)
	return p
}

// ConstraintAdd 追加约束（去重）。
func (p *Profile) ConstraintAdd(constraint string) *Profile {
	p.constraints = p.appendUnique(p.constraints, constraint)
	return p
}

// SetStyle 设置风格。
func (p *Profile) SetStyle(style string) *Profile { p.style = style; return p }

// Style 风格。
func (p *Profile) Style() string { return p.style }

// ModeAdd 同时追加输入与输出模式（对齐 Java mode()）。
func (p *Profile) ModeAdd(mode string) *Profile {
	p.inputModes = p.appendUnique(p.inputModes, mode)
	p.outputModes = p.appendUnique(p.outputModes, mode)
	return p
}

// MetaPut 写元数据。
func (p *Profile) MetaPut(key string, value any) *Profile {
	if p.metadata == nil {
		p.metadata = map[string]any{}
	}
	p.metadata[key] = value
	return p
}

// MetaGet 读元数据（缺省 def）。
func (p *Profile) MetaGet(key string, def any) any {
	if p.metadata == nil {
		return def
	}
	if v, ok := p.metadata[key]; ok {
		return v
	}
	return def
}

// Supports 是否支持给定输入/输出模式。
func (p *Profile) Supports(in, out string) bool {
	return p.contains(p.inputModes, in) && p.contains(p.outputModes, out)
}

func (p *Profile) contains(list []string, v string) bool {
	for _, has := range list {
		if has == v {
			return true
		}
	}
	return false
}

// Capabilities 能力快照（拷贝）。
func (p *Profile) Capabilities() []string { return append([]string(nil), p.capabilities...) }

// Constraints 约束快照（拷贝）。
func (p *Profile) Constraints() []string { return append([]string(nil), p.constraints...) }

// InputModes / OutputModes 交互模式快照（拷贝）。
func (p *Profile) InputModes() []string  { return append([]string(nil), p.inputModes...) }
func (p *Profile) OutputModes() []string { return append([]string(nil), p.outputModes...) }

// Metadata 元数据快照（拷贝；无则 nil）。
func (p *Profile) Metadata() map[string]any {
	if p.metadata == nil {
		return nil
	}
	out := make(map[string]any, len(p.metadata))
	for k, v := range p.metadata {
		out[k] = v
	}
	return out
}

// FormatString 档案摘要（对标 getFormatString；locale 取 "zh"/"en"，缺省 zh）。
func (p *Profile) FormatString(locale string) string {
	var segs []string
	if locale == "en" {
		if caps := p.Capabilities(); len(caps) > 0 {
			segs = append(segs, "capabilities: "+strings.Join(caps, ", "))
		}
		if cons := p.Constraints(); len(cons) > 0 {
			segs = append(segs, "constraints: "+strings.Join(cons, ", "))
		}
		if p.style != "" {
			segs = append(segs, "style: "+p.style)
		}
		if ims := p.InputModes(); len(ims) > 0 {
			segs = append(segs, "input modes: "+strings.Join(ims, ", "))
		}
		if oms := p.OutputModes(); len(oms) > 0 {
			segs = append(segs, "output modes: "+strings.Join(oms, ", "))
		}
		return strings.Join(segs, " | ")
	}
	if caps := p.Capabilities(); len(caps) > 0 {
		segs = append(segs, "能力: "+strings.Join(caps, "、"))
	}
	if cons := p.Constraints(); len(cons) > 0 {
		segs = append(segs, "约束: "+strings.Join(cons, "；"))
	}
	if p.style != "" {
		segs = append(segs, "风格: "+p.style)
	}
	if ims := p.InputModes(); len(ims) > 0 {
		segs = append(segs, "输入模式: "+strings.Join(ims, "、"))
	}
	if oms := p.OutputModes(); len(oms) > 0 {
		segs = append(segs, "输出模式: "+strings.Join(oms, "、"))
	}
	return strings.Join(segs, " | ")
}

// sortedKeys 用于确定性行为的小工具（当前档案 API 保序，保留备用）。
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
