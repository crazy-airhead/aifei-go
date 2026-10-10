// ralph.go PRD 驱动持久循环（对标 RalphLoopStrategy + prd 包 + verify 包）。
package loop

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// UserStory 用户故事。
type UserStory struct {
	ID               string   `json:"id"`
	Title            string   `json:"title"`
	Description      string   `json:"description"`
	AcceptanceCriteria []string `json:"acceptanceCriteria"`
	Priority         int      `json:"priority"`
	Passes           bool     `json:"passes"`
	ArchitectVerified bool    `json:"architectVerified"`
	Notes            string   `json:"notes"`
}

// PRDDocument PRD 文档。
type PRDDocument struct {
	Project     string       `json:"project"`
	BranchName  string       `json:"branchName"`
	Description string      `json:"description"`
	UserStories []*UserStory `json:"userStories"`
}

// AllStoriesCompleted 全部故事完成（passes）。
func (p *PRDDocument) AllStoriesCompleted() bool {
	if p == nil || len(p.UserStories) == 0 {
		return false
	}
	for _, s := range p.UserStories {
		if !s.Passes {
			return false
		}
	}
	return true
}

// AllStoriesFullyCompleted 全部故事完成且架构师验证。
func (p *PRDDocument) AllStoriesFullyCompleted() bool {
	if !p.AllStoriesCompleted() {
		return false
	}
	for _, s := range p.UserStories {
		if !s.ArchitectVerified {
			return false
		}
	}
	return true
}

// NextIncompleteStory 下一个未完成故事（优先级降序）。
func (p *PRDDocument) NextIncompleteStory() *UserStory {
	stories := append([]*UserStory(nil), p.UserStories...)
	sort.SliceStable(stories, func(i, j int) bool {
		if stories[i].Priority != stories[j].Priority {
			return stories[i].Priority > stories[j].Priority
		}
		return stories[i].ID < stories[j].ID
	})
	for _, s := range stories {
		if !s.Passes {
			return s
		}
	}
	return nil
}

// PRDStatus 完成度统计。
type PRDStatus struct {
	TotalStories    int     `json:"totalStories"`
	CompletedStories int    `json:"completedStories"`
	FullyCompleted  int     `json:"fullyCompleted"`
	ProgressPercent float64 `json:"progressPercent"`
}

// CalculatePRDStatus 统计。
func CalculatePRDStatus(p *PRDDocument) *PRDStatus {
	if p == nil || len(p.UserStories) == 0 {
		return &PRDStatus{}
	}
	st := &PRDStatus{TotalStories: len(p.UserStories)}
	for _, s := range p.UserStories {
		if s.Passes {
			st.CompletedStories++
			if s.ArchitectVerified {
				st.FullyCompleted++
			}
		}
	}
	st.ProgressPercent = float64(st.CompletedStories) / float64(st.TotalStories) * 100
	return st
}

// FormatPRDForContext LLM 上下文格式化。
func FormatPRDForContext(p *PRDDocument) string {
	if p == nil {
		return "No PRD available."
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("PRD: %s (branch: %s)\n%s\n\n用户故事：\n", p.Project, p.BranchName, p.Description))
	for _, s := range p.UserStories {
		mark := "[ ]"
		if s.Passes {
			mark = "[x]"
			if s.ArchitectVerified {
				mark = "[X]"
			}
		}
		sb.WriteString(fmt.Sprintf("%s #%s %s（优先级 %d）\n", mark, s.ID, s.Title, s.Priority))
	}
	return sb.String()
}

// ---- 验证器（verify 包）----

// ArchitectVerifier 架构师验证：验收条件逐条包含判定。
type ArchitectVerifier struct{}

// Verify 验证（story 未完成或实现不含任一验收条件 → 未过）。
func (ArchitectVerifier) Verify(story *UserStory, implementation string) *VerificationResult {
	if story == nil || !story.Passes {
		return &VerificationResult{Approved: false, Feedback: "story not implemented"}
	}
	impl := strings.ToLower(implementation)
	for _, c := range story.AcceptanceCriteria {
		if !strings.Contains(impl, strings.ToLower(c)) {
			return &VerificationResult{Approved: false,
				Feedback: fmt.Sprintf("acceptance criteria not met: %s", c)}
		}
	}
	return &VerificationResult{Approved: true, Feedback: "architect approved"}
}

// StoryComplete 故事完成且架构师验证。
func (ArchitectVerifier) StoryComplete(s *UserStory) bool {
	return s != nil && s.Passes && s.ArchitectVerified
}

// CriticMode 二线评审模式。
type CriticMode string

const (
	CriticArchitect CriticMode = "architect"
	CriticCritic    CriticMode = "critic"
	CriticCodex     CriticMode = "codex"
)

// CriticVerifier 二线评审。
type CriticVerifier struct{ Mode CriticMode }

// NewCriticVerifier 构造。
func NewCriticVerifier(mode CriticMode) *CriticVerifier {
	if mode == "" {
		mode = CriticArchitect
	}
	return &CriticVerifier{Mode: mode}
}

// Review 评审（实现文本按模式判定）。
func (v *CriticVerifier) Review(implementation string) *VerificationResult {
	switch v.Mode {
	case CriticArchitect:
		if implementation == "" {
			return &VerificationResult{Approved: false, Feedback: "no implementation"}
		}
		lower := strings.ToLower(implementation)
		for _, kw := range []string{"architecture", "design", "pattern"} {
			if strings.Contains(lower, kw) {
				return &VerificationResult{Approved: true, Feedback: "approved with architecture notes"}
			}
		}
		return &VerificationResult{Approved: true, Feedback: "approved"}
	case CriticCodex:
		for _, w := range strings.Fields(implementation) {
			if strings.EqualFold(w, "test") {
				return &VerificationResult{Approved: true, Feedback: "codex approved"}
			}
		}
		return &VerificationResult{Approved: false, Feedback: "codex rejected: no tests mentioned"}
	default: // critic
		return &VerificationResult{Approved: implementation != "", Feedback: "reviewed"}
	}
}

// VerificationResult 验证结果（verify 包内部类）。
type VerificationResult struct {
	Approved  bool
	Feedback  string
}

// VerificationState 单次验证状态机（PENDING→AWAITING_REVIEW→ARCHITECT_APPROVED/
// CRITIC_APPROVED/FAILED；maxAttempts=3 达上限强判完成）。
type VerificationState struct {
	Pending              bool
	CompletionClaim      string
	VerificationAttempts int
	MaxAttempts          int
	ArchitectFeedback    string
	ArchitectApproved    bool
	RequestedAt          time.Time
	OriginalTask         string
	StoryID              string
	CriticMode           CriticMode
	RequestID            string
	CurrentState         string
}

// NewVerificationState 构造（maxAttempts 缺省 3）。
func NewVerificationState() *VerificationState {
	return &VerificationState{Pending: true, MaxAttempts: 3, CurrentState: "PENDING"}
}

// StartVerification 重置并进入 AWAITING_REVIEW。
func (v *VerificationState) StartVerification(task, storyID string, mode CriticMode) {
	v.VerificationAttempts = 0
	v.RequestedAt = time.Now()
	v.RequestID = NewUUID()
	v.OriginalTask = task
	v.StoryID = storyID
	v.CriticMode = mode
	v.CurrentState = "AWAITING_REVIEW"
	v.Pending = true
}

// RecordArchitectFeedback 记录架构师反馈（true = 验证完成可裁决）。
func (v *VerificationState) RecordArchitectFeedback(approved bool, feedback string) bool {
	v.VerificationAttempts++
	v.ArchitectFeedback = feedback
	if approved {
		v.ArchitectApproved = true
		v.CurrentState = "ARCHITECT_APPROVED"
		v.Pending = false
		return true
	}
	if v.VerificationAttempts >= v.MaxAttempts {
		v.CurrentState = "FAILED"
		v.Pending = false
		return true // 达上限视为验证完成（Ralph 强制判负）
	}
	v.CurrentState = "AWAITING_REVIEW"
	return false
}

// StoryImplementor 故事实现器（Java BiFunction 的 Go 形态）。
type StoryImplementor func(storyDesc string, ctx *Context) (any, error)

// StoryValidator 故事验证器。
type StoryValidator func(storyDesc string, result any, ctx *Context) bool

// RalphStrategy PRD 驱动策略。
type RalphStrategy struct {
	StrategyBase
	VerificationRequired bool
	CriticModeValue      CriticMode

	prd      *PRDDocument
	disk     *DiskStateManager
	architect ArchitectVerifier
	critic    *CriticVerifier
	verify   *VerificationState

	implementor StoryImplementor
	validator   StoryValidator
}

// NewRalphStrategy 构造（缺省 50 轮、verification=true、architect 模式）。
func NewRalphStrategy() *RalphStrategy {
	r := &RalphStrategy{
		VerificationRequired: true,
		CriticModeValue:      CriticArchitect,
		verify:               NewVerificationState(),
		architect:            ArchitectVerifier{},
	}
	r.MaxIterationsValue = 50
	r.critic = NewCriticVerifier(CriticArchitect)
	return r
}

// SetImplementor 注入实现器。
func (r *RalphStrategy) SetImplementor(fn StoryImplementor) *RalphStrategy {
	r.implementor = fn
	return r
}

// SetStoryValidator 注入故事验证器。
func (r *RalphStrategy) SetStoryValidator(fn StoryValidator) *RalphStrategy {
	r.validator = fn
	return r
}

// InjectPrdComponents PRD 组件注入（引擎 Start 时）。
func (r *RalphStrategy) InjectPrdComponents(prd *PRDDocument, disk *DiskStateManager) {
	r.prd = prd
	r.disk = disk
	r.verify = NewVerificationState()
}

// SetCriticMode 评审模式。
func (r *RalphStrategy) SetCriticMode(m CriticMode) *RalphStrategy {
	r.CriticModeValue = m
	r.critic = NewCriticVerifier(m)
	return r
}

// SetVerificationRequired 验证开关。
func (r *RalphStrategy) SetVerificationRequired(b bool) *RalphStrategy {
	r.VerificationRequired = b
	return r
}

// Name 名称。
func (r *RalphStrategy) Name() string { return "ralph" }

// Description 描述。
func (r *RalphStrategy) Description() string { return "PRD 驱动持久循环" }

// MaxIterations 上限。
func (r *RalphStrategy) MaxIterations() int { return r.MaxIterationsValue }

// SupportsParallelExecution 不支持。
func (r *RalphStrategy) SupportsParallelExecution() bool { return false }

// ShouldContinue 续跑判定。
func (r *RalphStrategy) ShouldContinue(ctx *Context) bool {
	return r.StrategyBase.ShouldContinueFuse(ctx, r.shouldContinueInternal)
}

func (r *RalphStrategy) shouldContinueInternal(ctx *Context) bool {
	if r.prd == nil {
		return false
	}
	if r.VerificationRequired {
		return !r.prd.AllStoriesFullyCompleted()
	}
	return !r.prd.AllStoriesCompleted()
}

// ExecuteIteration 执行一轮（读故事 → 实现 → 验证 → 标记 → 状态回写）。
func (r *RalphStrategy) ExecuteIteration(ctx *Context) *IterationResult {
	return r.StrategyBase.WrapIterationSafe(ctx, r.executeInternal)
}

func (r *RalphStrategy) executeInternal(ctx *Context) *IterationResult {
	if r.prd == nil {
		return &IterationResult{State: StateFailed, Success: false, Message: "No PRD found"}
	}
	story := r.prd.NextIncompleteStory()
	if story == nil {
		return &IterationResult{State: StateCompleted, Success: true, Message: "All stories completed"}
	}

	// 实现（缺省实现器：占位文本）
	var implementation any
	if r.implementor != nil {
		out, err := r.implementor(story.Title+"\n"+story.Description, ctx)
		if err != nil {
			return &IterationResult{State: StateFailed, Success: false, Message: err.Error()}
		}
		implementation = out
	} else {
		implementation = fmt.Sprintf("Implemented: %s %s", story.ID, story.Title)
	}

	// 实现即声明完成（Java 实现器的 completion claim 语义——Architect 验证以
	// 此为前提），验证不过则回退
	story.Passes = true
	passed := true
	if r.VerificationRequired {
		if r.validator != nil {
			passed = r.validator(story.Title, implementation, ctx)
		} else {
			passed = r.validateStory(story, fmt.Sprint(implementation), ctx)
		}
	}

	if passed {
		story.ArchitectVerified = true
	} else {
		story.Passes = false
	}

	allDone := !r.shouldContinueInternal(ctx)
	status := CalculatePRDStatus(r.prd)
	ctx.Put("prdStatus", status.ProgressPercent)
	ctx.Put("prdTotalStories", status.TotalStories)
	ctx.Put("prdCompletedStories", status.CompletedStories)

	return &IterationResult{
		State:   stateOf(allDone, passed),
		Success: true,
		Result:  implementation,
		Message: fmt.Sprintf("story %s: %v", story.ID, map[bool]string{true: "passed", false: "needs fix"}[passed]),
		Metadata: map[string]any{
			"criticMode": string(r.CriticModeValue), "verificationRequired": r.VerificationRequired,
			"hasResult": implementation != nil,
		},
	}
}

func stateOf(allDone, passed bool) State {
	switch {
	case allDone:
		return StateCompleted
	case passed:
		return StateExecuting
	default:
		return StateFixing
	}
}

// validateStory Architect→VerificationState→Critic 双层验证。
func (r *RalphStrategy) validateStory(story *UserStory, implementation string, ctx *Context) bool {
	r.verify.StartVerification("Verify story: "+story.ID, story.ID, r.CriticModeValue)
	r.verify.CompletionClaim = implementation

	arch := r.architect.Verify(story, implementation)
	if !r.verify.RecordArchitectFeedback(arch.Approved, arch.Feedback) {
		ctx.Put("verificationState", r.verify.CurrentState)
		ctx.Put("verificationAttempts", r.verify.VerificationAttempts)
		return false // 未达上限：继续重试
	}
	if !arch.Approved {
		return false // 达上限强判负
	}
	critic := r.critic.Review(implementation)
	if critic.Approved {
		r.verify.CurrentState = "CRITIC_APPROVED"
	}
	return critic.Approved
}

// marshalJSON/unmarshalJSON 内部便捷。
func marshalJSON(v any) ([]byte, error) { return json.Marshal(v) }
func unmarshalJSON(b []byte, v any) error { return json.Unmarshal(b, v) }
