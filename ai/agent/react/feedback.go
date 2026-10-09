package react

import (
	"context"
	"encoding/json"
	"strings"

	ai "github.com/crazy-airhead/aifei-go/ai"
)

// FeedbackToolName 流程挂起工具名（主要为 TeamAgent 传递 ctx.stop 信号）。
const FeedbackToolName = "__ask_for_feedback"

// 反馈工具缺省文案（对标 FeedbackTool.TOOL_*_DEF）。
const (
	feedbackDescriptionDef = "当任务缺失必要参数、条件或外部反馈而无法继续时，必须调用此工具反馈，并向用户说明原因。"
	feedbackReasonDescDef  = "向用户说明原因"
	suspendedStatusMark    = `"status":"suspended"`
)

// FeedbackTool 构造挂起工具（对标 FeedbackTool.getTool）：缺失关键信息时主动
// 挂起当前推理，等待外部输入后恢复。
func FeedbackTool(toolDescription, toolReasonDescription string) ai.FunctionTool {
	if toolDescription == "" {
		toolDescription = feedbackDescriptionDef
	}
	if toolReasonDescription == "" {
		toolReasonDescription = feedbackReasonDescDef
	}

	return ai.NewTool(FeedbackToolName).
		ReturnDirect(true).
		Desc(toolDescription).
		StringParam("reason", toolReasonDescription, false).
		Handler(func(ctx context.Context, args map[string]any) (any, error) {
			node := map[string]any{}
			for k, v := range args {
				node[k] = v
			}
			node["status"] = "suspended"
			data, err := json.Marshal(node)
			if err != nil {
				return "", err
			}
			return string(data), nil
		}).
		Build()
}

// IsSuspend 文本是否为挂起决策（status=suspended）。
func IsSuspend(text string) bool {
	return text != "" && strings.Contains(text, suspendedStatusMark)
}

// AsSuspendDecision 构造可被 IsSuspend 识别的挂起决策文本。
func AsSuspendDecision(reason string) string {
	node := map[string]any{"reason": reason, "status": "suspended"}
	data, err := json.Marshal(node)
	if err != nil {
		return `{"reason":"","status":"suspended"}`
	}
	return string(data)
}
