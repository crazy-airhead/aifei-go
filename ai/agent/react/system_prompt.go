package react

import (
	"strings"

	ai "github.com/crazy-airhead/aifei-go/ai"
)

// SystemPrompt ReAct 系统提示词提供者（对标 ReActSystemPrompt：「协议+业务」
// 增量构建——核心协议由内部强制注入，业务指令增量追加）。
type SystemPrompt interface {
	// Locale 协议指令语言（"zh"/"en"）。
	Locale() string
	// GetSystemPrompt 渲染系统提示词。
	GetSystemPrompt(trace *Trace) string
}

// systemPromptCN 中文版提示词（对标 ReActSystemPromptCn）。
type systemPromptCN struct {
	roleDesc            string
	instructionProvider func(*Trace) string
}

// DefaultSystemPromptCN 缺省中文提示词（无角色/指令覆写）。
func DefaultSystemPromptCN() SystemPrompt {
	return &systemPromptCN{}
}

func (p *systemPromptCN) Locale() string { return "zh" }

func (p *systemPromptCN) GetSystemPrompt(trace *Trace) string {
	var sb strings.Builder

	// 1. 角色定义与 ReAct 范式宣告
	sb.WriteString("## 你的角色\n")
	sb.WriteString(p.role(trace) + "。")
	if trace.Config().Style == StyleStructuredText {
		sb.WriteString("你必须使用 ReAct 模式解决问题，每一轮推理必须包含显式的标签：" +
			"Thought（思考） -> Action（行动） -> Observation（观察）。\n\n")
	} else {
		sb.WriteString("你遵循严谨的 ReAct (Reasoning and Acting) 逻辑。\n\n")
	}

	// 2. 注入指令集（含格式、准则、示例）
	sb.WriteString(p.instruction(trace))

	return sb.String()
}

func (p *systemPromptCN) role(trace *Trace) string {
	if p.roleDesc != "" {
		return p.roleDesc
	}
	if trace.Config().Role != "" {
		return trace.Config().Role
	}
	return "具备自主行动能力的专业任务解决专家"
}

func (p *systemPromptCN) instruction(trace *Trace) string {
	if trace.Config().Style == StyleNativeTool {
		return p.naturalInstruction(trace)
	}
	return p.classicInstruction(trace)
}

// naturalInstruction 原生工具模式指令。
func (p *systemPromptCN) naturalInstruction(trace *Trace) string {
	var sb strings.Builder

	sb.WriteString("## 核心规则\n")
	sb.WriteString("1. **工具调用**：如果需要调用工具，请【直接】触发函数调用（Function Calling）。\n")
	sb.WriteString("2. **结果导向**：所有结论必须基于工具返回的真实数据（Observation）。\n")
	sb.WriteString("3. **严禁伪造**：禁止在回复中模拟或伪造工具的执行过程。\n")
	sb.WriteString("4. **自然回复**：任务完成后，请以自然语言直接回复，无需输出 `Final Answer:` 等标签。\n\n")

	appendBusinessInstructions(&sb, p.instructionProvider, trace)

	if len(trace.Options().Tools) == 0 {
		sb.WriteString("\n> 注意：当前环境无可用工具，请根据已有信息直接回复。\n")
	}

	return sb.String()
}

// classicInstruction 结构化文本模式指令（论文模式）。
func (p *systemPromptCN) classicInstruction(trace *Trace) string {
	cfg := trace.Config()
	marker := cfg.GetFinishMarker()
	var sb strings.Builder

	// A. 格式约束：ReAct 循环的语法基石
	sb.WriteString("## 输出格式（必须遵守）\n")
	sb.WriteString("Thought: 简要解释你的思考过程（1-2句话）。\n")
	sb.WriteString("Action: 如果需要调用工具，必须输出 JSON 对象：{\"name\": \"工具名\", \"arguments\": {...}}。不要使用代码块，不要有额外文本。\n")
	sb.WriteString("Final Answer: 任务完成后，以 " + marker + " 开头给出回答。\n\n")

	// B. 结束规格：确保任务能被系统正确识别截断
	sb.WriteString("## 最终答案要求\n")
	sb.WriteString("1. 当你获得结论或信息已足够时，必须给出最终答案。\n")
	sb.WriteString("2. 最终答案**必须**以 " + marker + " 开头。\n")
	sb.WriteString("3. 在 " + marker + " 之后直接提供完整回答，不要换行，不要输出标注标签。\n\n")

	// C. 行为准则：防止模型出现伪造 Observation 或死循环
	sb.WriteString("## 核心规则\n")
	sb.WriteString("1. 每次仅输出一个 Action，输出后立即停止等待 Observation。\n")
	sb.WriteString("2. 严禁伪造 Observation，严禁调用‘可用工具’之外的工具。\n")
	sb.WriteString("3. 最终回答未带上 " + marker + " 将被视为无效。\n\n")

	// D. 业务指令注入
	appendBusinessInstructions(&sb, p.instructionProvider, trace)

	// E. 少样本引导 (Few-shot)
	sb.WriteString("## 示例\n")
	sb.WriteString("用户: 北京天气怎么样？\n")
	sb.WriteString("Thought: 我需要查询北京当前的实时天气信息。\n")
	sb.WriteString("Action: {\"name\": \"get_weather\", \"arguments\": {\"location\": \"北京\"}}\n")
	sb.WriteString("Thought: 根据观察结果，北京天气良好。\n")
	sb.WriteString("Final Answer: " + marker + "北京目前天气晴间多云，气温约 25°C。\n")

	// F. 工具集动态注入
	if len(trace.Options().Tools) == 0 {
		sb.WriteString("\n注意：当前没有可用工具。请直接给出 Final Answer。\n")
	} else {
		sb.WriteString("\n## 可用工具\n")
		sb.WriteString("你可以通过 Action 字段调用以下工具：\n")
		for _, t := range trace.Options().Tools {
			sb.WriteString("- " + t.Name() + ": " + ai.DescriptionAndMeta(t))
			if s := t.InputSchema(); s != "" {
				sb.WriteString(" 参数定义: " + s)
			}
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

func appendBusinessInstructions(sb *strings.Builder, provider func(*Trace) string, trace *Trace) {
	switch {
	case provider != nil:
		sb.WriteString("## 核心任务指令\n")
		sb.WriteString(provider(trace) + "\n")
		sb.WriteString("\n")
	case trace.Config().Instruction != "":
		sb.WriteString("## 核心任务指令\n")
		sb.WriteString(trace.Config().Instruction + "\n")
		sb.WriteString("\n")
	}
}

// PromptBuilder 系统提示词构建器（对标 ReActSystemPromptCn.Builder）。
type PromptBuilder struct {
	roleDesc            string
	instructionProvider func(*Trace) string
}

// NewPromptBuilder 空构建器。
func NewPromptBuilder() *PromptBuilder { return &PromptBuilder{} }

// Role 覆写角色描述。
func (b *PromptBuilder) Role(role string) *PromptBuilder {
	b.roleDesc = role
	return b
}

// Instruction 固定业务指令。
func (b *PromptBuilder) Instruction(s string) *PromptBuilder {
	b.instructionProvider = func(*Trace) string { return s }
	return b
}

// InstructionFn 动态业务指令（每轮以 Trace 求值）。
func (b *PromptBuilder) InstructionFn(fn func(*Trace) string) *PromptBuilder {
	b.instructionProvider = fn
	return b
}

// Build 生成中文系统提示词。
func (b *PromptBuilder) Build() SystemPrompt {
	return &systemPromptCN{roleDesc: b.roleDesc, instructionProvider: b.instructionProvider}
}
