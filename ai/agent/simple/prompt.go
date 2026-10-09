package simple

// SystemPrompt 简单系统提示词提供者（对标 SimpleSystemPrompt）：
// 「## 你的角色\n{role}。\n\n## 你的执行指令\n{instruction}」结构化布局。
type SystemPrompt struct {
	roleDesc            string
	instructionProvider func(trace *Trace) string
}

// DefaultSystemPrompt 空提示词（角色/指令回落 Config 字段）。
func DefaultSystemPrompt() *SystemPrompt { return &SystemPrompt{} }

// PromptBuilder 构建器。
type PromptBuilder struct {
	sp *SystemPrompt
}

// NewPromptBuilder 系统提示词构建器。
func NewPromptBuilder() *PromptBuilder { return &PromptBuilder{sp: &SystemPrompt{}} }

// Role 静态角色文本（覆盖 Config.Role）。
func (b *PromptBuilder) Role(role string) *PromptBuilder { b.sp.roleDesc = role; return b }

// Instruction 动态指令（每轮以 Trace 求值；覆盖 Config.Instruction）。
func (b *PromptBuilder) Instruction(fn func(trace *Trace) string) *PromptBuilder {
	b.sp.instructionProvider = fn
	return b
}

// InstructionStr 静态指令。
func (b *PromptBuilder) InstructionStr(s string) *PromptBuilder {
	return b.Instruction(func(*Trace) string { return s })
}

// Build 构建系统提示词。
func (b *PromptBuilder) Build() *SystemPrompt { return b.sp }

// GetSystemPrompt 组合角色与指令文本（可空串）。
func (s *SystemPrompt) GetSystemPrompt(trace *Trace) string {
	role, instruction := s.role(trace), s.instruction(trace)
	out := ""
	if role != "" {
		out += "## 你的角色\n" + role + "。\n\n"
	}
	if instruction != "" {
		out += "## 执行指令\n" + instruction
	}
	return out
}

func (s *SystemPrompt) role(trace *Trace) string {
	if s.roleDesc != "" {
		return s.roleDesc
	}
	if trace != nil && trace.Config() != nil && trace.Config().Role != "" {
		return trace.Config().Role
	}
	return ""
}

func (s *SystemPrompt) instruction(trace *Trace) string {
	if s.instructionProvider != nil {
		return s.instructionProvider(trace)
	}
	if trace != nil && trace.Config() != nil {
		return trace.Config().Instruction
	}
	return ""
}
