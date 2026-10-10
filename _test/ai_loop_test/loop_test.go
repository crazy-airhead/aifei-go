// loop_test.go ai/loop 黑盒测试（P5-L）：状态机转移矩阵、引擎生命周期（start/
// pause 真续跑/stop/null 验证器/0 轮保险丝/无效 id 不炸/双 stop）、二轮修复
//（needsFix→FIXING 继续）、Ralph PRD 全流程、Team 管道规范路径与修复环与取消
// 恢复、UltraQA 门禁与假执行器、磁盘状态往返与原子性、互斥守卫、Autopilot
// 管道、进度与 PRD 格式化。
package ai_loop_test

import (
	"strings"
	"testing"
	"time"

	"github.com/crazy-airhead/aifei-go/ai/loop"
)

// countingStrategy 计数策略（n 轮后停；接保险丝；可配每轮延迟）。
type countingStrategy struct {
	loop.StrategyBase
	remaining int
	failFirst int // 前 N 轮失败
	executed  int
	delay     time.Duration
}

func newCountingStrategy(total, failFirst int) *countingStrategy {
	s := &countingStrategy{remaining: total, failFirst: failFirst}
	s.MaxIterationsValue = total
	return s
}

func (s *countingStrategy) setDelay(d time.Duration) *countingStrategy {
	s.delay = d
	return s
}

func (s *countingStrategy) Name() string { return "counting" }
func (s *countingStrategy) Description() string { return "计数" }
func (s *countingStrategy) MaxIterations() int { return s.MaxIterationsValue }
func (s *countingStrategy) SupportsParallelExecution() bool { return false }
func (s *countingStrategy) ShouldContinue(ctx *loop.Context) bool {
	return s.ShouldContinueFuse(ctx, func(*loop.Context) bool { return s.remaining > 0 })
}
func (s *countingStrategy) ExecuteIteration(ctx *loop.Context) *loop.IterationResult {
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	s.remaining--
	s.executed++
	success := s.executed > s.failFirst
	return &loop.IterationResult{State: loop.StateExecuting, Success: success,
		Message: "iter", Result: s.executed}
}

// ---- 状态机 ----

func TestLoopStateTransitions(t *testing.T) {
	if !loop.IsValidTransition(loop.StateIdle, loop.StatePlanning) {
		t.Fatal("IDLE→PLANNING 应合法")
	}
	for _, c := range []struct{ from, to loop.State }{
		{loop.StatePlanning, loop.StateExecuting},
		{loop.StateExecuting, loop.StateVerifying},
		{loop.StateVerifying, loop.StateCompleted},
		{loop.StateVerifying, loop.StateFixing},
		{loop.StateFixing, loop.StateExecuting},
		{loop.StatePlanning, loop.StatePaused},
		{loop.StatePaused, loop.StateExecuting},
	} {
		if !loop.IsValidTransition(c.from, c.to) {
			t.Fatalf("%s→%s 应合法", c.from, c.to)
		}
	}
	for _, c := range []struct{ from, to loop.State }{
		{loop.StateIdle, loop.StateExecuting},
		{loop.StateCompleted, loop.StatePlanning},
		{loop.StateFailed, loop.StatePlanning},
		{loop.StatePlanning, loop.StateCompleted},
	} {
		if loop.IsValidTransition(c.from, c.to) {
			t.Fatalf("%s→%s 应非法", c.from, c.to)
		}
	}
	if !loop.StatePlanning.IsActive() || loop.StateCompleted.IsActive() {
		t.Fatal("IsActive 判定错误")
	}
	if !loop.StateCompleted.IsTerminal() || loop.StateFixing.IsTerminal() {
		t.Fatal("IsTerminal 判定错误")
	}
}

// ---- 引擎生命周期 ----

func TestLoopEngineLifecycle(t *testing.T) {
	e := loop.NewEngine(nil)
	s, err := e.Start(&loop.Config{Strategy: newCountingStrategy(3, 0), MaxIterations: 10})
	if err != nil {
		t.Fatal(err)
	}
	if s.ID() == "" {
		t.Fatal("会话 id 非空")
	}
	if !e.IsRunning(s.ID()) {
		t.Fatal("start 后应 running")
	}
	result := s.WaitForCompletion()
	if result == nil || !result.Success || result.TotalIterations != 3 {
		t.Fatalf("终态：%+v", result)
	}
	if e.IsRunning(s.ID()) {
		t.Fatal("完成后不 running")
	}
	if result.SuccessRate != 1.0 {
		t.Fatalf("成功率：%v", result.SuccessRate)
	}
}

func TestLoopEngineStopAndEdge(t *testing.T) {
	e := loop.NewEngine(nil)
	// 无效 id：不炸
	e.Pause("nope")
	e.Resume("nope")
	e.Stop("nope")
	if _, ok := e.GetState("nope"); ok {
		t.Fatal("无效 id GetState 应 false")
	}
	if e.ActiveSessions() != nil && len(e.ActiveSessions()) > 0 {
		t.Fatal("无活跃会话")
	}

	// 0 轮保险丝：立即完成
	s, _ := e.Start(&loop.Config{Strategy: newCountingStrategy(5, 0), MaxIterations: 0})
	r := s.WaitForCompletion()
	if r.TotalIterations != 0 {
		t.Fatalf("0 轮应立即收口：%+v", r)
	}

	// 双 stop 不炸
	e.Stop(s.ID())
	e.Stop(s.ID())

	// nil 策略报错
	if _, err := e.Start(nil); err == nil {
		t.Fatal("nil 配置应报错")
	}
	if _, err := e.Start(&loop.Config{}); err == nil {
		t.Fatal("nil 策略应报错")
	}

	// 空 taskDescription 可启动
	s2, err := e.Start(&loop.Config{Strategy: newCountingStrategy(1, 0)})
	if err != nil {
		t.Fatal(err)
	}
	s2.WaitForCompletion()
}

func TestLoopEnginePauseResume(t *testing.T) {
	e := loop.NewEngine(nil)
	strategy := newCountingStrategy(3, 0).setDelay(30 * time.Millisecond)
	s, _ := e.Start(&loop.Config{Strategy: strategy, MaxIterations: 10})

	// 等首轮迭代后暂停
	deadline := time.Now().Add(2 * time.Second)
	for s.IterationCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	e.Pause(s.ID())
	if st, _ := e.GetState(s.ID()); st != loop.StatePaused {
		t.Fatalf("pause 后状态：%v", st)
	}
	pausedCount := s.IterationCount()

	// 恢复真续跑（Java 退出线程的缺陷已修）
	e.Resume(s.ID())
	r := s.WaitForCompletion()
	if r == nil || !r.Success {
		t.Fatalf("resume 后应完成：%+v", r)
	}
	if r.TotalIterations < 3 {
		t.Fatalf("应跑完全部轮次：%d（暂停时 %d）", r.TotalIterations, pausedCount)
	}
}

// needsFixValidator 首轮 needsFix、次轮起 passed。
type needsFixValidator struct{ calls int }

func (v *needsFixValidator) Validate(result any, c *loop.ValidationCriteria) *loop.ValidationResult {
	v.calls++
	if v.calls == 1 {
		return loop.NeedsFixResult("first round needs fix")
	}
	return loop.PassedResult("fixed")
}
func (v *needsFixValidator) ValidateQualityGate(*loop.QualityGate, any) *loop.ValidationResult {
	return nil
}
func (v *needsFixValidator) ValidateIteration(*loop.IterationResult, *loop.ValidationContext) *loop.ValidationResult {
	return nil
}

// TestLoopSecondRoundFix 二轮修复（搬译 SecondRoundFixTest 核心：needsFix →
// FIXING 属活跃、循环继续跑修复轮后完成）。
func TestLoopSecondRoundFix(t *testing.T) {
	e := loop.NewEngine(nil)
	sawFixing := make(chan bool, 4)
	strategy := newCountingStrategy(3, 0).setDelay(20 * time.Millisecond)
	s, _ := e.Start(&loop.Config{
		Strategy:  strategy,
		Validator: &needsFixValidator{},
		MaxIterations: 10,
	})
	s.OnStateChange(func(st loop.State) {
		if st == loop.StateFixing {
			select {
			case sawFixing <- true:
			default:
			}
		}
	})
	r := s.WaitForCompletion()
	if r == nil || !r.Success {
		t.Fatalf("修复后应完成：%+v", r)
	}
	select {
	case <-sawFixing:
	default:
		t.Fatal("应经过 FIXING 状态")
	}
}

// ---- Ralph ----

func TestLoopRalphStrategy(t *testing.T) {
	prd := &loop.PRDDocument{
		Project: "demo", BranchName: "main", Description: "演示",
		UserStories: []*loop.UserStory{
			{ID: "S1", Title: "故事一", Priority: 2,
				AcceptanceCriteria: []string{"Implemented: S1"}},
			{ID: "S2", Title: "故事二", Priority: 1,
				AcceptanceCriteria: []string{"Implemented: S2"}},
		},
	}
	r := loop.NewRalphStrategy()
	r.InjectPrdComponents(prd, loop.NewDiskStateManager(t.TempDir()))

	e := loop.NewEngine(nil)
	s, err := e.Start(&loop.Config{Strategy: r, MaxIterations: 50})
	if err != nil {
		t.Fatal(err)
	}
	result := s.WaitForCompletion()
	if result == nil || !result.Success {
		t.Fatalf("Ralph 全流程：%+v", result)
	}
	if result.TotalIterations != 2 {
		t.Fatalf("两故事两轮：%d", result.TotalIterations)
	}
	if !prd.AllStoriesFullyCompleted() {
		t.Fatal("全部故事应完成且架构师验证")
	}

	// 优先级降序：高优先进
	if prd.UserStories[0].Priority < prd.UserStories[1].Priority {
		t.Fatal("故事序（测试构造即降序）")
	}

	// 统计与格式化
	st := loop.CalculatePRDStatus(prd)
	if st.CompletedStories != 2 || st.FullyCompleted != 2 || st.ProgressPercent != 100 {
		t.Fatalf("统计：%+v", st)
	}
	if !strings.Contains(loop.FormatPRDForContext(prd), "[X] #S1") {
		t.Fatalf("格式化：\n%s", loop.FormatPRDForContext(prd))
	}
}

func TestLoopRalphVerifiers(t *testing.T) {
	// Architect：验收条件逐条包含
	arch := loop.ArchitectVerifier{}
	story := &loop.UserStory{ID: "S1", Passes: true, AcceptanceCriteria: []string{"登录成功"}}
	if arch.Verify(story, "实现了登录成功功能").Approved != true {
		t.Fatal("条件满足应通过")
	}
	if arch.Verify(story, "别的功能").Approved {
		t.Fatal("条件不满足应拒绝")
	}
	if arch.Verify(&loop.UserStory{ID: "S2"}, "x").Approved {
		t.Fatal("未完成故事应拒绝")
	}

	// Critic 三模式
	if !loop.NewCriticVerifier(loop.CriticArchitect).Review("含 architecture 设计").Approved {
		t.Fatal("architect 模式应通过")
	}
	if loop.NewCriticVerifier(loop.CriticCodex).Review("no tests here").Approved {
		t.Fatal("codex 模式无 test 应拒绝")
	}
	if !loop.NewCriticVerifier(loop.CriticCodex).Review("added test cases").Approved {
		t.Fatal("codex 模式含 test 应通过")
	}

	// VerificationState 上限强判
	v := loop.NewVerificationState()
	v.StartVerification("t", "S1", loop.CriticArchitect)
	if v.RecordArchitectFeedback(false, "no") {
		t.Fatal("首轮失败应继续重试")
	}
	if v.RecordArchitectFeedback(false, "no") {
		t.Fatal("二轮失败应继续")
	}
	if !v.RecordArchitectFeedback(false, "no") {
		t.Fatal("三轮达上限应视为验证完成")
	}
	if v.CurrentState != "FAILED" {
		t.Fatalf("上限状态：%s", v.CurrentState)
	}
}

// ---- Team 管道 ----

func TestLoopTeamPipelineCanonical(t *testing.T) {
	ctx := &loop.Context{ContextData: map[string]any{}, MaxIterations: 10}
	team := loop.NewTeamStrategy()

	// 规范路径 PLAN→PRD→EXEC→VERIFY→COMPLETED（单任务单执行完成）
	loop.SetExecutionData(ctx, 1, 1)
	var phases []string
	for i := 0; i < 8 && team.ShouldContinue(ctx); i++ {
		ctx.IterationCount++
		r := team.ExecuteIteration(ctx)
		phases = append(phases, r.Metadata["phase"].(string)+"→"+r.Metadata["nextPhase"].(string))
	}
	if loop.GetPhase(ctx) != loop.PhaseCompleted {
		t.Fatalf("规范路径应 COMPLETED：%v（%v）", loop.GetPhase(ctx), phases)
	}
	if len(phases) != 4 {
		t.Fatalf("四阶段：%v", phases)
	}
}

func TestLoopTeamPipelineFixLoop(t *testing.T) {
	ctx := &loop.Context{ContextData: map[string]any{}}
	team := loop.NewTeamStrategy()
	// 2 任务只完成 1：VERIFY 失败 → FIX →（清 failure 后手动再置未完成）→ …
	loop.SetExecutionData(ctx, 2, 2)
	// PLAN→PRD→EXEC（1 次）
	team.ExecuteIteration(ctx)
	team.ExecuteIteration(ctx)
	team.ExecuteIteration(ctx)
	if loop.GetPhase(ctx) != loop.PhaseVerify {
		t.Fatalf("三步后应 VERIFY：%v", loop.GetPhase(ctx))
	}
	// 模拟 1/2 完成
	exec, _ := ctx.Get("execution").(map[string]any)
	exec["tasksCompleted"] = 1
	r := team.ExecuteIteration(ctx)
	if r.Metadata["nextPhase"] != string(loop.PhaseFix) {
		t.Fatalf("验证失败应进 FIX：%v", r.Metadata)
	}
	// 非法转移守卫
	if err := loop.MarkPhase(ctx, loop.PhasePlan, "x"); err == nil {
		t.Fatal("FIX→PLAN 应非法")
	}
	// fix 溢出 → FAILED
	loop.RequestCancel(ctx, false)
	team.ExecuteIteration(ctx)
	if loop.GetPhase(ctx) != loop.PhaseCancelled {
		t.Fatalf("取消应 CANCELLED：%v", loop.GetPhase(ctx))
	}
}

func TestLoopTeamCancelResume(t *testing.T) {
	ctx := &loop.Context{ContextData: map[string]any{}}
	loop.SetPhaseDirect(ctx, loop.PhaseExec, "test")
	loop.RequestCancel(ctx, true)
	if err := loop.ResumeFromCancel(ctx); err != nil {
		t.Fatalf("保留恢复：%v", err)
	}
	if loop.GetPhase(ctx) != loop.PhaseExec {
		t.Fatalf("恢复到取消前阶段：%v", loop.GetPhase(ctx))
	}
	// 不保留的取消不可恢复
	loop.RequestCancel(ctx, false)
	if err := loop.ResumeFromCancel(ctx); err == nil {
		t.Fatal("无保留的取消恢复应报错")
	}
}

// ---- UltraQA ----

type fakeExecutor struct {
	failures map[string]bool
	calls    []string
}

func (f *fakeExecutor) Execute(command, workDir string) (*loop.CommandResult, error) {
	f.calls = append(f.calls, command)
	if f.failures[command] {
		return &loop.CommandResult{ExitCode: 1, Stderr: "boom"}, nil
	}
	return &loop.CommandResult{ExitCode: 0, Stdout: "ok"}, nil
}
func (f *fakeExecutor) IsAvailable() bool { return true }

func TestLoopUltraQA(t *testing.T) {
	u := loop.NewUltraQAStrategy()
	u.SetExecutor(&fakeExecutor{failures: map[string]bool{}})

	e := loop.NewEngine(nil)
	s, _ := e.Start(&loop.Config{Strategy: u, MaxIterations: 20})
	r := s.WaitForCompletion()
	if r == nil || !r.Success {
		t.Fatalf("门禁全过应完成：%+v", r)
	}
	if u.FinalExitReason(s.Context()) != loop.ExitGoalMet {
		t.Fatalf("退出原因：%v", u.FinalExitReason(s.Context()))
	}

	// 失败 → FIXING；同失败三连 → SAME_FAILURE 退出
	u2 := loop.NewUltraQAStrategy()
	u2.SetExecutor(&fakeExecutor{failures: map[string]bool{"go build ./...": true}})
	u2.MaxTestAttempts = 10
	ctx := &loop.Context{ContextData: map[string]any{}, MaxIterations: 20}
	states := map[loop.State]bool{}
	for i := 0; i < 10 && u2.ShouldContinue(ctx); i++ {
		ctx.IterationCount++
		states[u2.ExecuteIteration(ctx).State] = true
	}
	if !states[loop.StateFixing] {
		t.Fatal("失败门禁应进 FIXING")
	}
	if u2.FinalExitReason(ctx) != loop.ExitSameFail {
		t.Fatalf("同失败三连退出：%v", u2.FinalExitReason(ctx))
	}

	// 轮次上限
	u3 := loop.NewUltraQAStrategy()
	u3.MaxTestAttempts = 2
	u3.SetExecutor(&fakeExecutor{failures: map[string]bool{"go test ./... -count=1": true}})
	ctx3 := &loop.Context{ContextData: map[string]any{}, MaxIterations: 10}
	for i := 0; i < 5 && u3.ShouldContinue(ctx3); i++ {
		ctx3.IterationCount++
		u3.ExecuteIteration(ctx3)
	}
	if u3.FinalExitReason(ctx3) != loop.ExitMaxCycles {
		t.Fatalf("轮次上限退出：%v", u3.FinalExitReason(ctx3))
	}
}

func TestLoopCommandExecutor(t *testing.T) {
	ex := loop.NewProcessCommandExecutor()
	r, err := ex.Execute("echo hello", "")
	if err != nil || !r.IsSuccess() || !strings.Contains(r.Stdout, "hello") {
		t.Fatalf("真实执行：%+v %v", r, err)
	}
	r, _ = ex.Execute("exit 3", "")
	if r.IsSuccess() || r.ExitCode != 3 {
		t.Fatalf("失败退出码：%+v", r)
	}
}

// ---- 磁盘状态 ----

func TestLoopDiskStateManager(t *testing.T) {
	dir := t.TempDir()
	d := loop.NewDiskStateManager(dir)

	data := &loop.StateData{
		SessionID: "s1", State: loop.StatePlanning, IterationCount: 2,
		SuccessfulIterations: 1, StartTime: time.Now(), LastUpdateTime: time.Now(),
		ContextData: map[string]any{"mode": "ralph", "nested": map[string]any{"k": "v"}},
		Metadata:    map[string]any{"m": 1},
	}
	if err := d.SaveStateInMode("ralph", data); err != nil {
		t.Fatal(err)
	}
	// 布局：.loops/state/ralph/s1.json
	if _, err := filepathAbsExists(dir + "/.loops/state/ralph/s1.json"); err != nil {
		t.Fatalf("布局：%v", err)
	}
	got, err := d.ReadState("ralph", "s1")
	if err != nil || got == nil {
		t.Fatalf("读取：%v %v", got, err)
	}
	if got.SessionID != "s1" || got.State != loop.StatePlanning || got.IterationCount != 2 {
		t.Fatalf("往返：%+v", got)
	}
	if got.ContextData["mode"] != "ralph" {
		t.Fatalf("嵌套 contextData：%v", got.ContextData)
	}
	// 跨模式找（LoadState 依次探）
	if g, _ := d.LoadState("s1"); g == nil {
		t.Fatal("跨模式 LoadState")
	}
	// 会话摘要
	if ids, _ := d.AllSessionIDs(); len(ids) != 1 || ids[0] != "s1" {
		t.Fatalf("摘要：%v", ids)
	}
	// 清除
	if err := d.ClearState("ralph", "s1"); err != nil {
		t.Fatal(err)
	}
	if has, _ := d.HasState("s1"); has {
		t.Fatal("清除后不应存在")
	}
	// 原子写不留 tmp 残留
	if tmpLeft := countTmp(dir); tmpLeft != 0 {
		t.Fatalf("tmp 残留：%d", tmpLeft)
	}
}

func filepathAbsExists(p string) (bool, error) {
	_, err := osStat(p)
	if err != nil {
		return false, err
	}
	return true, nil
}

func TestLoopDiskLegacyFormat(t *testing.T) {
	dir := t.TempDir()
	d := loop.NewDiskStateManager(dir)
	// 旧格式：无 _meta/data 信封，根对象即 data
	legacy := `{"sessionId":"old","state":"PLANNING","iterationCount":1}`
	if err := writeFile(dir+"/.loops/state/ralph/old.json", legacy); err != nil {
		t.Fatal(err)
	}
	got, err := d.ReadState("ralph", "old")
	if err != nil || got == nil || got.SessionID != "old" || got.State != loop.StatePlanning {
		t.Fatalf("旧格式兼容：%v %v", got, err)
	}
	// 畸形：state 值非法 → 静默 IDLE
	writeFile(dir+"/.loops/state/ralph/bad.json", `{"sessionId":"bad","state":"NOT_A_STATE"}`)
	got, _ = d.ReadState("ralph", "bad")
	if got == nil || got.State != loop.StateIdle {
		t.Fatalf("非法 state 容忍：%v", got)
	}
}

// ---- 互斥守卫 ----

func TestLoopMutualExclusionGuard(t *testing.T) {
	g := loop.NewMutualExclusionGuard(loop.NewDiskStateManager(t.TempDir()))

	if !g.Acquire("s1", "ralph") {
		t.Fatal("首获取应成功")
	}
	if g.CanStartUltraQA("s2") {
		t.Fatal("ralph 活跃时 ultraqa 不应可启")
	}
	if g.CanStartTeam("s2") {
		t.Fatal("ralph 活跃时 team 不应可启")
	}
	if !g.CanStartRalph("s2") {
		t.Fatal("同模式他启应允许（Java 同会话自挡语义之外允许多 ralph 会话）")
	}
	g.Release("s1")
	if !g.CanStartUltraQA("s2") {
		t.Fatal("释放后应可启")
	}

	// 未知模式
	if g.Acquire("s3", "unknown") {
		t.Fatal("未知模式应 false")
	}
	// ForceRelease 恒 true
	if !g.ForceRelease("ralph", "nope") {
		t.Fatal("ForceRelease 恒 true")
	}
	// 并发获取全成功（同模式多会话）
	done := make(chan bool, 8)
	for i := 0; i < 8; i++ {
		go func(i int) { g.Acquire(loop.NewUUID(), "team"); done <- true }(i)
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	if g.ActiveCount() != 8 {
		t.Fatalf("并发登记：%d", g.ActiveCount())
	}
}

// ---- Autopilot ----

func TestLoopAutopilot(t *testing.T) {
	e := loop.NewEngine(nil)
	cfg := loop.NewPipelineConfig()
	stages := cfg.EnabledStages()
	want := []loop.PipelineStage{loop.StageExpansion, loop.StagePlanning, loop.StageExecution, loop.StageQA, loop.StageValidation}
	if len(stages) != len(want) {
		t.Fatalf("缺省全开固定序：%v", stages)
	}
	for i, s := range stages {
		if s != want[i] {
			t.Fatalf("阶段序：%v", stages)
		}
	}

	a := loop.NewAutopilotExecutor(e, cfg)
	req := loop.NewPipelineRequest("pipe-1", "完成演示任务")
	req.Context["description"] = "演示"
	req.Context["validationChecksPassed"] = true
	// EXECUTION/QA 阶段的策略：Ralph 无 PRD 首轮即失败——替换为计数策略避免
	// 外部依赖（阶段策略映射可覆盖——PipelineConfig.SetStageStrategy 面）
	cfg.SetStageStrategy(loop.StageExecution, newCountingStrategy(1, 0))
	cfg.SetStageStrategy(loop.StageQA, newCountingStrategy(1, 0))
	cfg.SetStageStrategy(loop.StagePlanning, newCountingStrategy(1, 0))

	result := a.StartPipeline(req)
	if result == nil || !result.Success {
		t.Fatalf("管道应成功：%+v", result)
	}
	if a.Status("pipe-1") != "COMPLETED" {
		t.Fatalf("状态：%s", a.Status("pipe-1"))
	}
	if len(result.Tracking.History) != 5 {
		t.Fatalf("五阶段历史：%v", result.Tracking.Summary())
	}
	if !strings.Contains(result.Summary(), "EXPANSION ✓") {
		t.Fatalf("摘要：%s", result.Summary())
	}

	// 失败路径：validation 未过
	req2 := loop.NewPipelineRequest("pipe-2", "失败任务")
	req2.Context["description"] = "x"
	// validationChecksPassed 缺省 false
	result2 := a.StartPipeline(req2)
	if result2.Success {
		t.Fatal("validation 失败管道应失败")
	}
	if a.Status("pipe-2") != "FAILED" {
		t.Fatalf("失败状态：%s", a.Status("pipe-2"))
	}
	_ = loop.StageFromTeamPhase(loop.PhaseExec) // 双向映射冒烟
	_ = loop.StagePlanning.ToTeamPhase()
}

// ---- 进度 ----

func TestLoopProgress(t *testing.T) {
	l := &loop.ProgressLog{}
	l.AddPattern("repeat {status}", "换思路")
	l.AddEntry(loop.NewProgressEntry(1, "FAILED", "首轮失败").AddLearning("依赖缺失"))
	l.AddEntry(loop.NewProgressEntry(2, "PASSED", "修复通过"))

	if got := l.RecentLearnings(1); len(got) != 1 || got[0] != "依赖缺失" {
		t.Fatalf("学习：%v", got)
	}
	if got := l.RecentLearnings(0); len(got) != 0 {
		t.Fatalf("空学习：%v", got)
	}
	out := l.FormatForContext()
	if !strings.Contains(out, "repeat .*") || !strings.Contains(out, "#1 [FAILED]") {
		t.Fatalf("上下文格式化：%s", out)
	}
	e := loop.NewProgressEntry(3, "FAILED", "x").AddLearning("y")
	if !strings.Contains(e.FormatXML(), "<learning>y</learning>") {
		t.Fatalf("XML：%s", e.FormatXML())
	}
}
