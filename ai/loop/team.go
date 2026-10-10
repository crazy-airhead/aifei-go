// team.go 阶段管道策略（对标 TeamPipelineStrategy：PLAN→PRD→EXEC→VERIFY→FIX
// 状态全存 contextData；取消/保留恢复；非法转移守卫）+ 进度记忆。
package loop

import (
	"fmt"
	"strings"
	"time"
)

// TeamPhase 管道阶段（8 值）。
type TeamPhase string

const (
	PhasePlan      TeamPhase = "PLAN"
	PhasePrd       TeamPhase = "PRD"
	PhaseExec      TeamPhase = "EXEC"
	PhaseVerify    TeamPhase = "VERIFY"
	PhaseFix       TeamPhase = "FIX"
	PhaseCompleted TeamPhase = "COMPLETED"
	PhaseFailed    TeamPhase = "FAILED"
	PhaseCancelled TeamPhase = "CANCELLED"
)

// teamTransitions 非法转换表（对照 Java isValidTransition）。
var teamTransitions = map[TeamPhase][]TeamPhase{
	PhasePlan:      {PhasePrd, PhaseCancelled, PhaseFailed},
	PhasePrd:       {PhaseExec, PhaseCancelled, PhaseFailed},
	PhaseExec:      {PhaseVerify, PhaseCancelled, PhaseFailed},
	PhaseVerify:    {PhaseFix, PhaseCompleted, PhaseCancelled, PhaseFailed},
	PhaseFix:       {PhaseExec, PhaseVerify, PhaseCompleted, PhaseFailed, PhaseCancelled},
	PhaseCompleted: {},
	PhaseFailed:    {},
	PhaseCancelled: {},
}

// IsValidTeamTransition 阶段转移合法性。
func IsValidTeamTransition(from, to TeamPhase) bool {
	for _, t := range teamTransitions[from] {
		if t == to {
			return true
		}
	}
	return false
}

// TeamStrategy 阶段管道策略。
type TeamStrategy struct {
	StrategyBase
	MaxFixAttemptsValue int
}

// NewTeamStrategy 构造（缺省 100 轮、maxFix 3）。
func NewTeamStrategy() *TeamStrategy {
	t := &TeamStrategy{MaxFixAttemptsValue: 3}
	t.MaxIterationsValue = 100
	return t
}

// Name 名称。
func (t *TeamStrategy) Name() string { return "team-pipeline" }

// Description 描述。
func (t *TeamStrategy) Description() string { return "PLAN→PRD→EXEC→VERIFY→FIX 阶段管道" }

// MaxIterations 上限。
func (t *TeamStrategy) MaxIterations() int { return t.MaxIterationsValue }

// SupportsParallelExecution 支持。
func (t *TeamStrategy) SupportsParallelExecution() bool { return true }

// GetPhase 当前阶段。
func GetPhase(ctx *Context) TeamPhase {
	s, _ := ctx.Get("currentPhase").(string)
	if s == "" {
		return PhasePlan
	}
	return TeamPhase(s)
}

func appendPhaseHistory(ctx *Context, p TeamPhase, reason string, success bool, from string) {
	hist, _ := ctx.Get("phaseHistory").([]map[string]any)
	ctx.Put("phaseHistory", append(hist, map[string]any{
		"phase": string(p), "enteredAt": time.Now().Format(time.RFC3339),
		"reason": reason, "success": success, "from": from,
	}))
}

// SetPhaseDirect 直写阶段（测试/外部推进用；不经守卫）。
func SetPhaseDirect(ctx *Context, p TeamPhase, reason string) {
	ctx.Put("currentPhase", string(p))
	appendPhaseHistory(ctx, p, reason, true, "")
}

// MarkPhase 阶段推进（非法报错；记历史——对标 markPhase）。
func MarkPhase(ctx *Context, next TeamPhase, reason string) error {
	from := GetPhase(ctx)
	if !IsValidTeamTransition(from, next) {
		return fmt.Errorf("loop: invalid team phase transition %s → %s", from, next)
	}
	ctx.Put("currentPhase", string(next))
	appendPhaseHistory(ctx, next, reason, true, string(from))
	if next == PhaseExec {
		ctx.Put("fixAttempts", 0)
	}
	return nil
}

// RequestCancel 请求取消。
func RequestCancel(ctx *Context, preserveForResume bool) {
	ctx.Put("cancelRequested", true)
	ctx.Put("cancelRequestedAt", time.Now().Format(time.RFC3339))
	ctx.Put("preserveForResume", preserveForResume)
	ctx.Put("phaseBeforeCancel", string(GetPhase(ctx)))
}

// ResumeFromCancel 取消恢复（须 cancelRequested && preserve）。
func ResumeFromCancel(ctx *Context) error {
	cancelled, _ := ctx.Get("cancelRequested").(bool)
	preserve, _ := ctx.Get("preserveForResume").(bool)
	if !cancelled || !preserve {
		return fmt.Errorf("loop: resume requires cancelled and preserveForResume")
	}
	before, _ := ctx.Get("phaseBeforeCancel").(string)
	if before == "" {
		before = string(PhaseExec)
	}
	ctx.Put("currentPhase", before)
	ctx.Put("cancelRequested", false)
	ctx.Put("preserveForResume", false)
	return nil
}

// SetExecutionData 执行数据。
func SetExecutionData(ctx *Context, workersTotal, tasksTotal int) {
	ctx.Put("execution", map[string]any{
		"workersTotal": workersTotal, "workersActive": workersTotal,
		"tasksTotal": tasksTotal, "tasksCompleted": 0, "tasksFailed": 0, "initialized": true,
	})
}

// MarkTaskCompleted 任务完成计数。
func MarkTaskCompleted(ctx *Context) {
	exec, _ := ctx.Get("execution").(map[string]any)
	if exec == nil {
		return
	}
	if n, ok := exec["tasksCompleted"].(int); ok {
		exec["tasksCompleted"] = n + 1
	}
}

// MarkPhaseQuiet 无守卫阶段写入（终态）。
func MarkPhaseQuiet(ctx *Context, p TeamPhase) {
	ctx.Put("currentPhase", string(p))
	appendPhaseHistory(ctx, p, "", false, string(GetPhase(ctx)))
}

// ShouldContinue 续跑（未取消且非三终态）。
func (t *TeamStrategy) ShouldContinue(ctx *Context) bool {
	return t.StrategyBase.ShouldContinueFuse(ctx, func(c *Context) bool {
		if cancelled, _ := c.Get("cancelRequested").(bool); cancelled {
			return false
		}
		switch GetPhase(c) {
		case PhaseCompleted, PhaseCancelled, PhaseFailed:
			return false
		}
		return true
	})
}

// ExecuteIteration 执行当前一个阶段步骤。
func (t *TeamStrategy) ExecuteIteration(ctx *Context) *IterationResult {
	return t.StrategyBase.WrapIterationSafe(ctx, t.executeInternal)
}

func (t *TeamStrategy) executeInternal(ctx *Context) *IterationResult {
	if cancelled, _ := ctx.Get("cancelRequested").(bool); cancelled {
		preserve, _ := ctx.Get("preserveForResume").(bool)
		MarkPhaseQuiet(ctx, PhaseCancelled)
		if preserve {
			return &IterationResult{State: StatePaused, Success: true, Message: "cancelled (preserved)"}
		}
		return &IterationResult{State: StateCompleted, Success: true, Message: "cancelled"}
	}

	phase := GetPhase(ctx)
	var passed bool
	var result any
	switch phase {
	case PhasePlan:
		ctx.Put("planGenerated", true)
		ctx.Put("planResult", "plan-ok")
		passed, result = true, "plan"
	case PhasePrd:
		ctx.Put("prdGenerated", true)
		ctx.Put("prdResult", "prd-ok")
		passed, result = true, "prd"
	case PhaseExec:
		exec, _ := ctx.Get("execution").(map[string]any)
		tasksTotal, tasksCompleted := 0, 0
		if exec != nil {
			tasksTotal, _ = exec["tasksTotal"].(int)
			tasksCompleted, _ = exec["tasksCompleted"].(int)
		}
		MarkTaskCompleted(ctx)
		passed, result = true, fmt.Sprintf("exec %d/%d", tasksCompleted+1, tasksTotal)
	case PhaseVerify:
		exec, _ := ctx.Get("execution").(map[string]any)
		tasksTotal, tasksCompleted := 0, 0
		if exec != nil {
			tasksTotal, _ = exec["tasksTotal"].(int)
			tasksCompleted, _ = exec["tasksCompleted"].(int)
		}
		if tasksTotal == 0 {
			passed, result = true, "no tasks"
		} else {
			passed = tasksCompleted >= tasksTotal
			result = fmt.Sprintf("verify %d/%d", tasksCompleted, tasksTotal)
			if !passed {
				ctx.Put("lastFailureReason", fmt.Sprintf("tasks %d/%d incomplete", tasksCompleted, tasksTotal))
			}
		}
	case PhaseFix:
		attempts, _ := ctx.Get("fixAttempts").(int)
		if attempts >= t.MaxFixAttemptsValue {
			reason, _ := ctx.Get("lastFailureReason").(string)
			MarkPhaseQuiet(ctx, PhaseFailed)
			return &IterationResult{State: StateFailed, Success: false,
				Message: fmt.Sprintf("fix attempts exhausted: %s", reason)}
		}
		ctx.Put("fixAttempts", attempts+1)
		ctx.Put("fixResult", "fixed")
		ctx.Put("lastFailureReason", "")
		passed, result = true, "fix"
	default:
		return &IterationResult{State: StateCompleted, Success: true, Message: "terminal phase"}
	}

	next := t.nextPhase(ctx, phase, passed)
	_ = MarkPhase(ctx, next, phaseReason(passed)) // 守卫拒绝时保持原阶段
	return &IterationResult{
		State: phaseResultState(next), Success: true, Result: result,
		Message: fmt.Sprintf("phase %s → %s", phase, next),
		Metadata: map[string]any{"phase": string(phase), "nextPhase": string(next), "passed": passed},
	}
}

// nextPhase 转移规则（cancelled→CANCELLED；VERIFY+success→COMPLETED；VERIFY+
// fail→FIX；FIX→fixAttempts 溢出 FAILED 否则 EXEC；否则序列下一项）。
func (t *TeamStrategy) nextPhase(ctx *Context, current TeamPhase, passed bool) TeamPhase {
	switch current {
	case PhaseVerify:
		if passed {
			return PhaseCompleted
		}
		return PhaseFix
	case PhaseFix:
		attempts, _ := ctx.Get("fixAttempts").(int)
		if attempts >= t.MaxFixAttemptsValue {
			return PhaseFailed
		}
		return PhaseExec
	}
	sequence := []TeamPhase{PhasePlan, PhasePrd, PhaseExec, PhaseVerify, PhaseFix}
	for i, p := range sequence {
		if p == current && i+1 < len(sequence) {
			return sequence[i+1]
		}
	}
	return PhaseCompleted
}

func phaseReason(passed bool) string {
	if passed {
		return "success"
	}
	return "failure"
}

func phaseResultState(p TeamPhase) State {
	switch p {
	case PhaseCompleted:
		return StateCompleted
	case PhaseFailed:
		return StateFailed
	case PhaseFix:
		return StateFixing
	default:
		return StateExecuting
	}
}

// ---- 进度记忆（progress 包）----

// ProgressEntry 单次迭代进度条目。
type ProgressEntry struct {
	Iteration int    `json:"iteration"`
	Timestamp string `json:"timestamp"`
	Status    string `json:"status"`
	Summary   string `json:"summary"`
	Learning  string `json:"learning,omitempty"`
}

// NewProgressEntry 构造。
func NewProgressEntry(iteration int, status, summary string) *ProgressEntry {
	return &ProgressEntry{Iteration: iteration, Timestamp: time.Now().Format(time.RFC3339),
		Status: status, Summary: summary}
}

// AddLearning 学习记录。
func (e *ProgressEntry) AddLearning(s string) *ProgressEntry { e.Learning = s; return e }

// FormatXML XML 形态。
func (e *ProgressEntry) FormatXML() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("  <entry>\n    <iteration>%d</iteration>\n", e.Iteration))
	sb.WriteString(fmt.Sprintf("    <timestamp>%s</timestamp>\n", e.Timestamp))
	sb.WriteString(fmt.Sprintf("    <status>%s</status>\n", e.Status))
	sb.WriteString(fmt.Sprintf("    <summary>%s</summary>\n", e.Summary))
	if e.Learning != "" {
		sb.WriteString(fmt.Sprintf("    <learning>%s</learning>\n", e.Learning))
	}
	sb.WriteString("  </entry>")
	return sb.String()
}

// ProgressLog 进度日志。
type ProgressLog struct {
	Patterns []map[string]string `json:"patterns"`
	Entries  []*ProgressEntry    `json:"entries"`
}

// AddPattern 模式记录（占位符 {status} 替换）。
func (l *ProgressLog) AddPattern(pattern, guidance string) {
	l.Patterns = append(l.Patterns, map[string]string{
		"pattern": strings.ReplaceAll(pattern, "{status}", ".*"), "guidance": guidance,
	})
}

// AddEntry 条目追加。
func (l *ProgressLog) AddEntry(e *ProgressEntry) { l.Entries = append(l.Entries, e) }

// RecentLearnings 近 n 条学习。
func (l *ProgressLog) RecentLearnings(n int) []string {
	var out []string
	for i := len(l.Entries) - 1; i >= 0 && len(out) < n; i-- {
		if l.Entries[i].Learning != "" {
			out = append(out, l.Entries[i].Learning)
		}
	}
	return out
}

// FormatForContext 上下文格式化。
func (l *ProgressLog) FormatForContext() string {
	if len(l.Entries) == 0 {
		return "No progress log available."
	}
	var sb strings.Builder
	sb.WriteString("进度日志：\n")
	for _, p := range l.Patterns {
		sb.WriteString(fmt.Sprintf("- 模式 %s → %s\n", p["pattern"], p["guidance"]))
	}
	for _, e := range l.Entries {
		sb.WriteString(fmt.Sprintf("- #%d [%s] %s\n", e.Iteration, e.Status, e.Summary))
		if e.Learning != "" {
			sb.WriteString(fmt.Sprintf("  学习：%s\n", e.Learning))
		}
	}
	return sb.String()
}
