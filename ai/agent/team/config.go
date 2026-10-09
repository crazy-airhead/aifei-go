// config.go 团队协作配置（对标 team.TeamAgentConfig）：组织架构、决策大脑、
// 协作协议与治理策略。成员名录保持注册序 + 大小写不敏感（Java IgnoreCaseMap
// = LinkedHashMap 语义）。
package team

import (
	"encoding/json"
	"strings"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
	"github.com/crazy-airhead/aifei-go/flow"
)

// SystemPrompt 主管系统提示词提供者（对标 AgentSystemPrompt<TeamTrace>）。
type SystemPrompt interface {
	SystemPrompt(trace *Trace) string
}

// SystemPromptOf 函数适配。
func SystemPromptOf(fn func(*Trace) string) SystemPrompt { return systemPromptFn(fn) }

type systemPromptFn func(*Trace) string

func (f systemPromptFn) SystemPrompt(trace *Trace) string { return f(trace) }

// cnSystemPrompt 中文版主管系统提示词（对标 TeamSystemPromptCn.getDefault）。
type cnSystemPrompt struct {
	roleDesc            string
	instructionProvider func(*Trace) string
}

// DefaultSystemPrompt 缺省中文主管提示词。
func DefaultSystemPrompt() SystemPrompt { return &cnSystemPrompt{} }

// SystemPromptBuilder 提示词构建器（role / instruction 两维定制）。
type SystemPromptBuilder struct {
	roleDesc            string
	instructionProvider func(*Trace) string
}

// NewSystemPromptBuilder 构建器。
func NewSystemPromptBuilder() *SystemPromptBuilder { return &SystemPromptBuilder{} }

// Role 角色描述（固定文本）。
func (b *SystemPromptBuilder) Role(role string) *SystemPromptBuilder {
	b.roleDesc = role
	return b
}

// Instruction 增量指令（固定文本）。
func (b *SystemPromptBuilder) Instruction(instruction string) *SystemPromptBuilder {
	b.instructionProvider = func(*Trace) string { return instruction }
	return b
}

// InstructionFn 增量指令（每轮以 Trace 求值）。
func (b *SystemPromptBuilder) InstructionFn(fn func(*Trace) string) *SystemPromptBuilder {
	b.instructionProvider = fn
	return b
}

// Build 构建提示词提供者。
func (b *SystemPromptBuilder) Build() SystemPrompt {
	return &cnSystemPrompt{roleDesc: b.roleDesc, instructionProvider: b.instructionProvider}
}

func (p *cnSystemPrompt) SystemPrompt(trace *Trace) string {
	var sb strings.Builder
	sb.WriteString("## 你的角色\n")
	sb.WriteString(p.roleOf(trace))
	sb.WriteString("\n\n")
	sb.WriteString(p.instructionOf(trace))
	return sb.String()
}

func (p *cnSystemPrompt) roleOf(trace *Trace) string {
	if p.roleDesc != "" {
		return p.roleDesc
	}
	if trace != nil && trace.Config() != nil && trace.Config().Role() != "" {
		return trace.Config().Role()
	}
	return "团队协作主管 (Supervisor)，负责协调成员完成任务"
}

func (p *cnSystemPrompt) instructionOf(trace *Trace) string {
	config := trace.Config()
	var sb strings.Builder

	// A. 团队成员名录
	sb.WriteString("## 团队成员\n```json\n")
	agentMetas := make([]map[string]any, 0, len(config.agentNames))
	ctx := trace.Context()
	for _, name := range config.agentNames {
		agentMetas = append(agentMetas, config.agents[name].Metadata(ctx))
	}
	if data, err := json.Marshal(agentMetas); err == nil {
		sb.Write(data)
	}
	sb.WriteString("\n```\n")

	// B. 任务背景：用户原始 Prompt
	sb.WriteString("\n## 当前任务\n")
	sb.WriteString(promptUserContent(trace.OriginalPrompt()))
	sb.WriteString("\n")

	// C. 协议规则
	config.ProtocolWide().InjectSupervisorInstruction(&sb)
	sb.WriteString("\n")

	// D. 输出规范
	sb.WriteString("\n## 输出规范\n")
	sb.WriteString("1. **任务终止**：如果任务已完成，必须输出: " + config.FinishMarker() +
		" 并在其后提供最终答案。若直接回复用户，**禁止**包括分析过程。\n")
	sb.WriteString("2. **继续执行**：若任务未完成，请**仅输出**下一个要执行的 Agent 名字，不要有额外文本。\n")

	// E. 治理准则
	sb.WriteString("\n## 历史分析与准则\n")
	sb.WriteString("- 参考协作历史，避免重复执行相同逻辑。\n")
	sb.WriteString("- 任务完成信号：" + config.FinishMarker() + "。\n")
	sb.WriteString("- 注意：严禁过早结束，确保专家意见已被充分获取。\n")

	// F. 增量指令
	if p.instructionProvider != nil {
		sb.WriteString("\n## 核心任务指令\n")
		sb.WriteString(p.instructionProvider(trace))
		sb.WriteString("\n\n")
	} else if config.Instruction() != "" {
		sb.WriteString("\n## 核心任务指令\n")
		sb.WriteString(config.Instruction())
		sb.WriteString("\n\n")
	}

	return sb.String()
}

// promptUserContent 提示词中最后一条 user 消息的文本（Java Prompt.getUserContent）。
func promptUserContent(p *ai.Prompt) string {
	if p == nil {
		return ""
	}
	msgs := p.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i] != nil && msgs[i].Role == ai.RoleUser {
			return messageTextOf(msgs[i])
		}
	}
	return ""
}

// Config 团队配置。
type Config struct {
	name        string
	role        string
	instruction string
	profile     *agent.Profile

	chatModel *ai.ChatModel

	agentNames []string              // 注册序
	agents     map[string]agent.Agent // 规范名 → agent
	lowerIndex map[string]string     // 小写名 → 规范名

	protocol      Protocol // 缺省 Hierarchical
	graphAdjuster func(*flow.GraphSpec)

	finishMarker string
	outputKey    string

	systemPrompt    SystemPrompt
	defaultOptions  *Options
}

// NewConfig 基于调度模型的团队配置（其余走缺省）。
func NewConfig(chatModel *ai.ChatModel) *Config {
	c := &Config{
		name:          "team_agent",
		chatModel:     chatModel,
		agents:        map[string]agent.Agent{},
		lowerIndex:    map[string]string{},
		systemPrompt:  DefaultSystemPrompt(),
		defaultOptions: NewOptions(),
	}
	c.protocol = NewHierarchicalProtocol(c)
	return c
}

// AddAgent 注册团队成员（专家；name/role 必填——Java Objects.requireNonNull）。
func (c *Config) AddAgent(ag agent.Agent) *Config {
	if ag == nil || ag.Name() == "" {
		panic("team: agent.name is required")
	}
	if ag.Role() == "" {
		panic("team: agent.role is required")
	}
	name := ag.Name()
	if _, exists := c.agents[name]; !exists {
		c.agentNames = append(c.agentNames, name)
	}
	c.agents[name] = ag
	c.lowerIndex[strings.ToLower(name)] = name
	return c
}

// SetProtocol 经工厂切换协议（生成执行图逻辑骨架）。
func (c *Config) SetProtocol(factory func(*Config) Protocol) *Config {
	if factory == nil {
		panic("team: protocolFactory is null")
	}
	c.protocol = factory(c)
	return c
}

// ---- 属性获取 ----

// DefaultOptions 运行时缺省选项。
func (c *Config) DefaultOptions() *Options { return c.defaultOptions }

// Name 团队唯一标识。
func (c *Config) Name() string { return c.name }

// TraceKey 轨迹存储键（缺省双下划线前缀隔离）。
func (c *Config) TraceKey() string {
	return "__" + c.name
}

// Role 角色描述。
func (c *Config) Role() string { return c.role }

// Instruction 指令描述。
func (c *Config) Instruction() string { return c.instruction }

// Profile 档案（惰性缺省）。
func (c *Config) Profile() *agent.Profile {
	if c.profile == nil {
		c.profile = agent.NewProfile()
	}
	return c.profile
}

// ChatModel 调度中心模型（Supervisor）。
func (c *Config) ChatModel() *ai.ChatModel { return c.chatModel }

// AgentNames 成员名（注册序）。
func (c *Config) AgentNames() []string {
	return append([]string(nil), c.agentNames...)
}

// Agents 成员列表（注册序对齐）。
func (c *Config) Agents() []agent.Agent {
	out := make([]agent.Agent, 0, len(c.agentNames))
	for _, name := range c.agentNames {
		out = append(out, c.agents[name])
	}
	return out
}

// Agent 按名取成员（大小写不敏感；无则 nil）。
func (c *Config) Agent(name string) agent.Agent {
	if name == "" {
		return nil
	}
	if ag, ok := c.agents[name]; ok {
		return ag
	}
	if canonical, ok := c.lowerIndex[strings.ToLower(name)]; ok {
		return c.agents[canonical]
	}
	return nil
}

// HasAgent 是否存在成员（大小写不敏感）。
func (c *Config) HasAgent(name string) bool { return c.Agent(name) != nil }

// NormalizeAgentName 归一为注册表规范名（未注册原样返回）。
func (c *Config) NormalizeAgentName(name string) string {
	if canonical, ok := c.lowerIndex[strings.ToLower(name)]; ok {
		return canonical
	}
	return name
}

// ProtocolWide 协议宽面（团队内部构图/治理用）。
func (c *Config) ProtocolWide() Protocol { return c.protocol }

// protocolNarrow 协议窄面（写进上下文 KeyProtocol 供成员智能体消费）。
func (c *Config) protocolNarrow() agent.TeamProtocol { return protocolNarrow{p: c.protocol} }

// GraphAdjuster 图微调钩子。
func (c *Config) GraphAdjuster() func(*flow.GraphSpec) { return c.graphAdjuster }

// FinishMarker 终结标识符（兜底生成 "[TEAM_FINISH]" 形态）。
func (c *Config) FinishMarker() string {
	if c.finishMarker == "" {
		name := c.name
		if name == "" {
			name = "TEAM"
		}
		c.finishMarker = "[" + strings.ToUpper(name) + "_FINISH]"
	}
	return c.finishMarker
}

// OutputKey 结果回填 Key。
func (c *Config) OutputKey() string { return c.outputKey }

// SystemPromptFor 渲染主管系统提示词（模板 #{var} 渲染 + 才能指令追加强化）。
func (c *Config) SystemPromptFor(trace *Trace, ctx flow.Context) string {
	prompt := c.systemPrompt.SystemPrompt(trace)
	if prompt == "" {
		return prompt
	}
	if ctx != nil {
		prompt = agent.RenderVars(prompt, ctx)
	}
	if ti := trace.Options().talentInstruction; ti != "" {
		prompt = prompt + "\n" + ti + "\n"
	}
	return prompt
}
