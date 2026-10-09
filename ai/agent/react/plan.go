package react

import "strings"

// RenderPlanContext 渲染执行计划进度看板（对标 PlanContextRenderer.render）：
// 按需生成请求级瞬时上下文，不保存派生快照。未启用计划模式或无计划时空串。
func RenderPlanContext(trace *Trace) string {
	if trace.Options() == nil || !trace.Options().PlanningMode || !trace.HasPlans() {
		return ""
	}

	var buf strings.Builder
	buf.WriteString("[执行计划进度看板]\n")

	plans := trace.Plans()
	total := len(plans)
	currIdx := trace.PlanIndex()
	if currIdx < 0 {
		currIdx = 0
	}
	if currIdx > total {
		currIdx = total
	}

	for i := 0; i < total; i++ {
		status := "[ ] "
		switch {
		case i < currIdx:
			status = "[√] "
		case i == currIdx:
			status = "[●] "
		}
		buf.WriteString(itoa(i+1) + ". " + status + plans[i] + "\n")
	}

	buf.WriteString("\n**计划进度同步协议 (Plan Sync Protocol)：**\n")
	if currIdx < total {
		currentStepNum := currIdx + 1
		nextStepNum := currIdx + 2

		buf.WriteString("- **当前状态**: 你正在执行步骤 [" + itoa(currentStepNum) + "]。\n")
		buf.WriteString("- **正常推进**: 步骤完成后，若结果符合预期，必须调用 `update_plan_progress` 并将 `next_plan_index` 设为 `" + itoa(nextStepNum) + "` ")
		if currIdx == total-1 {
			buf.WriteString("(标志所有计划已达成)。\n")
		} else {
			buf.WriteString("(切换至下一环节)。\n")
		}
		buf.WriteString("- **动态调整**: 若观察结果（Observation）显示原计划已不可行，必须优先调用 `revise_plan` 修正后续步骤，严禁强行进入错误环节。\n")
		buf.WriteString("- **禁止跳步**: 在更新进度前，禁止直接提供最终回答。")
	} else {
		buf.WriteString("- **目标达成**: 计划看板已全部标记为 [√]。请综合上述执行过程中的所有观察结果，直接给出最终的详细回答。")
	}

	return buf.String()
}
