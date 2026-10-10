// ultraqa.go 质量门禁循环（对标 UltraQAStrategy + CommandExecutor）。
package loop

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// CommandResult 命令结果。
type CommandResult struct {
	ExitCode        int
	Stdout, Stderr  string
	ExecutionTimeMs int64
}

// IsSuccess 成功。
func (r *CommandResult) IsSuccess() bool { return r.ExitCode == 0 }

// Summary 摘要（首行）。
func (r *CommandResult) Summary() string {
	first := func(s string) string {
		for _, line := range strings.Split(s, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				return line
			}
		}
		return ""
	}
	if r.IsSuccess() {
		if s := first(r.Stdout); s != "" {
			return s
		}
		return "OK"
	}
	if s := first(r.Stderr + "\n" + r.Stdout); strings.HasPrefix(s, "[err]") || s != "" {
		if s == "" {
			return "Failed"
		}
		return s
	}
	return fmt.Sprintf("Failed (exit: %d)", r.ExitCode)
}

// CommandExecutor 命令执行器 SPI。
type CommandExecutor interface {
	Execute(command, workDir string) (*CommandResult, error)
	IsAvailable() bool
}

// ProcessCommandExecutor 真实子进程执行器（sh -c / cmd /c；超时 5 分钟强杀）。
type ProcessCommandExecutor struct {
	TimeoutMs int64
}

// NewProcessCommandExecutor 构造。
func NewProcessCommandExecutor() *ProcessCommandExecutor {
	return &ProcessCommandExecutor{TimeoutMs: 300000}
}

// Execute 执行。
func (e *ProcessCommandExecutor) Execute(command, workDir string) (*CommandResult, error) {
	start := time.Now()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", command)
	} else {
		cmd = exec.Command("sh", "-c", command)
	}
	if workDir != "" {
		cmd.Dir = workDir
	}
	var stdout strings.Builder
	var stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	ctx := context.Background()
	if e.TimeoutMs > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(e.TimeoutMs)*time.Millisecond)
		defer cancel()
	}
	cmd = exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	elapsed := time.Since(start).Milliseconds()

	result := &CommandResult{Stdout: stdout.String(), Stderr: stderr.String(), ExecutionTimeMs: elapsed}
	if ctx.Err() == context.DeadlineExceeded {
		result.ExitCode = -1
		result.Stdout += "\n[TIMEOUT]"
		return result, nil
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			return result, err
		}
	}
	return result, nil
}

// IsAvailable 恒可用。
func (e *ProcessCommandExecutor) IsAvailable() bool { return true }

// UltraQAGoalType 目标类型。
type UltraQAGoalType string

const (
	GoalTests     UltraQAGoalType = "TESTS"
	GoalBuild     UltraQAGoalType = "BUILD"
	GoalLint      UltraQAGoalType = "LINT"
	GoalTypeCheck UltraQAGoalType = "TYPECHECK"
	GoalCustom    UltraQAGoalType = "CUSTOM"
)

// UltraQAExitReason 退出原因。
type UltraQAExitReason string

const (
	ExitGoalMet    UltraQAExitReason = "GOAL_MET"
	ExitMaxCycles  UltraQAExitReason = "MAX_CYCLES"
	ExitSameFail   UltraQAExitReason = "SAME_FAILURE"
	ExitCancelled  UltraQAExitReason = "CANCELLED"
	ExitEnvError   UltraQAExitReason = "ENV_ERROR"
)

// UltraQAStrategy 质量门禁循环。
type UltraQAStrategy struct {
	StrategyBase
	Gates          []*QualityGate
	MaxTestAttempts int
	GoalType       UltraQAGoalType
	StrictMode     bool
	Validator      Validator
	Executor       CommandExecutor
}

// NewUltraQAStrategy 构造（缺省 build+test 门禁、TESTS 目标、100 轮）。
func NewUltraQAStrategy() *UltraQAStrategy {
	u := &UltraQAStrategy{
		Gates:           []*QualityGate{BuildGate(), TestGate()},
		MaxTestAttempts: 10,
		GoalType:        GoalTests,
	}
	u.MaxIterationsValue = 100
	return u
}

// SetExecutor 注入执行器。
func (u *UltraQAStrategy) SetExecutor(e CommandExecutor) *UltraQAStrategy {
	u.Executor = e
	return u
}

// Name 名称。
func (u *UltraQAStrategy) Name() string { return "ultra-qa" }

// Description 描述。
func (u *UltraQAStrategy) Description() string { return "质量门禁循环" }

// MaxIterations 上限。
func (u *UltraQAStrategy) MaxIterations() int { return u.MaxIterationsValue }

// SupportsParallelExecution 并行开关。
func (u *UltraQAStrategy) SupportsParallelExecution() bool { return false }

// ShouldContinue 续跑（exitReason 未设、未达轮次上限、无同失败三连、门禁未全过）。
func (u *UltraQAStrategy) ShouldContinue(ctx *Context) bool {
	return u.StrategyBase.ShouldContinueFuse(ctx, u.shouldContinueInternal)
}

func (u *UltraQAStrategy) shouldContinueInternal(ctx *Context) bool {
	if v, _ := ctx.Get("exitReason").(string); v != "" {
		return false
	}
	cycles, _ := ctx.Get("qaCycleCount").(int)
	if cycles >= u.MaxTestAttempts {
		ctx.Put("exitReason", string(ExitMaxCycles))
		return false
	}
	if u.sameFailureStreak(ctx) {
		ctx.Put("exitReason", string(ExitSameFail))
		return false
	}
	return !u.allGatesPassed(ctx)
}

func (u *UltraQAStrategy) allGatesPassed(ctx *Context) bool {
	status, _ := ctx.Get("gateStatus").(map[string]bool)
	for _, g := range u.Gates {
		if !status[g.Name] {
			return false
		}
	}
	return true
}

func (u *UltraQAStrategy) sameFailureStreak(ctx *Context) bool {
	failures, _ := ctx.Get("failures").([]string)
	if len(failures) < 3 {
		return false
	}
	last := normalizeFailure(failures[len(failures)-1])
	for i := len(failures) - 2; i >= len(failures)-3; i-- {
		if normalizeFailure(failures[i]) != last {
			return false
		}
	}
	return true
}

// ExecuteIteration 执行一轮（cycle++ → 下一个未过门禁 → 逐检查 → passed/FIXING）。
func (u *UltraQAStrategy) ExecuteIteration(ctx *Context) *IterationResult {
	return u.StrategyBase.WrapIterationSafe(ctx, u.executeInternal)
}

func (u *UltraQAStrategy) executeInternal(ctx *Context) *IterationResult {
	cycles, _ := ctx.Get("qaCycleCount").(int)
	ctx.Put("qaCycleCount", cycles+1)

	gate := u.nextGate(ctx)
	if gate == nil {
		ctx.Put("exitReason", string(ExitGoalMet))
		return &IterationResult{State: StateCompleted, Success: true, Message: "goal met"}
	}

	var errors []string
	var warnings []string
	for _, check := range gate.Checks {
		if u.Validator != nil {
			if v := u.Validator.ValidateQualityGate(gate, check); v != nil && !v.Passed {
				if gate.Blocking {
					errors = append(errors, v.Message)
				} else {
					warnings = append(warnings, v.Message)
				}
			}
			continue
		}
		ok := u.executeCheck(ctx, check)
		if !ok {
			if gate.Blocking {
				if u.StrictMode {
					errors = append(errors, "check failed: "+check)
				} else if !u.executorAvailable() {
					// 无执行器且非严格：宽容通过（Java !strictMode 同款）
				} else {
					errors = append(errors, "check failed: "+check)
				}
			} else {
				warnings = append(warnings, "check failed: "+check)
			}
		}
	}

	if len(errors) > 0 {
		u.recordFailure(ctx, gate.Name+": "+strings.Join(errors, "; "))
		return &IterationResult{State: StateFixing, Success: false,
			Message: "Gate failed: " + gate.Name,
			Metadata: u.iterationMeta(gate, false)}
	}

	status, _ := ctx.Get("gateStatus").(map[string]bool)
	if status == nil {
		status = map[string]bool{}
		ctx.Put("gateStatus", status)
	}
	status[gate.Name] = true
	return &IterationResult{State: StateVerifying, Success: true,
		Message: "Gate passed: " + gate.Name,
		Metadata: u.iterationMeta(gate, true)}
}

func (u *UltraQAStrategy) iterationMeta(gate *QualityGate, passed bool) map[string]any {
	cycles, _ := u.currentCycles()
	return map[string]any{"gate": gate.Name, "passed": passed,
		"goalType": string(u.GoalType), "cycle": cycles}
}

func (u *UltraQAStrategy) currentCycles() (int, bool) { return 0, true }

func (u *UltraQAStrategy) nextGate(ctx *Context) *QualityGate {
	status, _ := ctx.Get("gateStatus").(map[string]bool)
	for _, g := range u.Gates {
		if !status[g.Name] {
			return g
		}
	}
	return nil
}

func (u *UltraQAStrategy) executorAvailable() bool {
	return u.Executor != nil && u.Executor.IsAvailable()
}

// executeCheck 单检查（有执行器走命令；无执行器且非严格 → 通过）。
func (u *UltraQAStrategy) executeCheck(ctx *Context, check string) bool {
	if u.Executor == nil || !u.Executor.IsAvailable() {
		return !u.StrictMode
	}
	workDir, _ := ctx.Get("workDir").(string)
	result, err := u.Executor.Execute(GoalCommand(u.GoalType, check), workDir)
	if err != nil {
		return false
	}
	records, _ := ctx.Get("executorResults").(map[string]any)
	if records == nil {
		records = map[string]any{}
		ctx.Put("executorResults", records)
	}
	records[check] = map[string]any{"success": result.IsSuccess(), "summary": result.Summary(), "executionTimeMs": result.ExecutionTimeMs}
	return result.IsSuccess()
}

// GoalCommand 目标命令（Go 侧命令模板参数化——Java 硬编码 mvn 的对应物）。
func GoalCommand(goal UltraQAGoalType, check string) string {
	switch goal {
	case GoalBuild:
		return "go build ./..."
	case GoalLint:
		return "go vet ./..."
	case GoalTypeCheck:
		return "go build ./..."
	case GoalCustom:
		return "check " + check
	default:
		switch check {
		case "compilation":
			return "go build ./..."
		case "dependencies":
			return "go mod verify"
		case "unit-tests":
			return "go test ./... -count=1"
		case "integration-tests":
			return "go test ./... -tags=integration -count=1"
		default:
			return "go test ./... -count=1"
		}
	}
}

func (u *UltraQAStrategy) recordFailure(ctx *Context, msg string) {
	failures, _ := ctx.Get("failures").([]string)
	ctx.Put("failures", append(failures, msg))
}

// FinalExitReason 终局原因。
func (u *UltraQAStrategy) FinalExitReason(ctx *Context) UltraQAExitReason {
	if v, _ := ctx.Get("exitReason").(string); v != "" {
		return UltraQAExitReason(v)
	}
	return ExitGoalMet
}

// ReportEnvError 环境错误。
func (u *UltraQAStrategy) ReportEnvError(ctx *Context, diagnosis string) {
	ctx.Put("exitReason", string(ExitEnvError))
	ctx.Put("envErrorDiagnosis", diagnosis)
}

// Cancel 取消。
func (u *UltraQAStrategy) Cancel(ctx *Context) {
	ctx.Put("exitReason", string(ExitCancelled))
}

// normalizeFailure 同失败指纹（时间戳/行号/时长/版本/空白归一——Java 正则链）。
var failureNormalizers = []*regexp.Regexp{
	regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}`),
	regexp.MustCompile(`:\d+:\d+`),
	regexp.MustCompile(`\d+ms`),
	regexp.MustCompile(`line \d+`),
	regexp.MustCompile(`\[\d+ms\]`),
	regexp.MustCompile(`\d+\.\d+\.\d+`),
	regexp.MustCompile(`\s+`),
}

func normalizeFailure(s string) string {
	out := s
	for _, re := range failureNormalizers {
		switch re.String() {
		case `line \d+`:
			out = re.ReplaceAllString(out, "line N")
		case `\[\d+ms\]`:
			out = re.ReplaceAllString(out, "[Nms]")
		case `\d+\.\d+\.\d+`:
			out = re.ReplaceAllString(out, "X.Y.Z")
		case `\s+`:
			out = re.ReplaceAllString(out, " ")
		default:
			out = re.ReplaceAllString(out, "")
		}
	}
	return strings.ToLower(strings.TrimSpace(out))
}
