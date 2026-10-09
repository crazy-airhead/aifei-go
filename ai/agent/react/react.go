// Package react 提供 ReAct 智能体（对标 solon-ai-agent 的 react 包）：
// Reasoning + Acting 循环编排——ReasonTask 组装上下文发起模型请求并解析意图，
// ActionTask 执行工具回填 Observation，Trace 状态机驱动 reason/action/end 路由。
//
// 本卡（P2-b）范围：两 Task + Options + StopLoop/ToolRetry 拦截器 + FeedbackTool
// 与计划状态面（SetPlans/AddPlan/PlanContextRenderer）。计划生成能力包
// （Java PlanTalent/PlanEvent/PlanStage）与上下文压缩、HITL 族拦截器不随本卡
// 迁移，待后续卡补齐；PlanningMode 目前仅保留选项位与轨迹状态。
package react

// 逻辑路由标识（Java ReActAgent.ID_* 与 Agent.ID_END）。
const (
	IDReason = "reason"
	IDAction = "action"
	IDEnd    = "end"
)

// Style ReAct 执行风格（Java ReActStyle）。
type Style int

const (
	// StyleStructuredText 结构化文本模式（论文模式）：LLM 显式输出
	// Thought: / Action: {JSON} 文本标签；适合不支持原生工具协议的底座。
	StyleStructuredText Style = iota

	// StyleNativeTool 原生工具模式（自然模式，缺省）：走厂商标准 tool_calls
	// 协议，不带 ReAct 特定标签。
	StyleNativeTool
)
