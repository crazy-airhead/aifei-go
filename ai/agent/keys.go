package agent

// 上下文键与常量（对标 Java Agent 的 Context Keys / Node IDs）。
const (
	// KeyCurrentUnitTraceKey 当前单元轨迹键名的存放键。
	KeyCurrentUnitTraceKey = "_current_unit_trace_key_"
	// KeyCurrentTeamTraceKey 当前团队轨迹键名的存放键。
	KeyCurrentTeamTraceKey = "_current_team_trace_key_"
	// KeyPendingReason 挂起原因的存放键。
	KeyPendingReason = "_pending_reason_"
	// KeySession 智能体会话的存放键（AgentBase.Run 经此复用会话）。
	KeySession = "_SESSION_"
	// KeyProtocol 团队协议的存放键。
	KeyProtocol = "_PROTOCOL_"

	// MetaAgent 消息元数据中标注归属智能体的键。
	MetaAgent = "_agent_"

	// MetaRunID 消息元数据中的运行 id 标注（AgentTrace.META_RUN_ID）。
	MetaRunID = "_runId"
	// MetaFirst 「初心」标注：历史窗口/新问题消息进入最终提示词时打标。
	MetaFirst = "_first"

	// IDStart / IDEnd 图内置起止节点 id。
	IDStart = "start"
	IDEnd   = "end"
)
