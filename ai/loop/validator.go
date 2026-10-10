// validator.go 验证器 SPI 与质量门禁（对标 validator 包）。
package loop

import "time"

// Validator 验证器 SPI。
type Validator interface {
	// Validate 结果验证（引擎主循环消费）。
	Validate(result any, criteria *ValidationCriteria) *ValidationResult
	// ValidateQualityGate 质量门禁验证（UltraQA 消费；不支持返回 nil）。
	ValidateQualityGate(gate *QualityGate, result any) *ValidationResult
	// ValidateIteration 单迭代验证（可选；nil = 未实现）。
	ValidateIteration(result *IterationResult, ctx *ValidationContext) *ValidationResult
}

// ValidationCriteria 验证标准。
type ValidationCriteria struct {
	Name         string
	Description  string
	Requirements []string
	Parameters   map[string]any
	Strict       bool
}

// SimpleCriteria 简单标准。
func SimpleCriteria(name string, requirements ...string) *ValidationCriteria {
	return &ValidationCriteria{Name: name, Requirements: requirements}
}

// StrictCriteria 严格标准。
func StrictCriteria(name, desc string, requirements ...string) *ValidationCriteria {
	return &ValidationCriteria{Name: name, Description: desc, Requirements: requirements, Strict: true}
}

// ValidationResult 验证结果（needsFix=true 时引擎进 FIXING 继续修复轮）。
type ValidationResult struct {
	Passed    bool
	Message   string
	Details   string
	Timestamp time.Time
	Metadata  map[string]any
	Errors    []string
	Warnings  []string
	NeedsFix  bool
}

// PassedResult 通过。
func PassedResult(msg string) *ValidationResult {
	return &ValidationResult{Passed: true, Message: msg, Timestamp: time.Now()}
}

// FailedResult 失败（needsFix=true——Java failed 工厂同款）。
func FailedResult(msg, details string) *ValidationResult {
	return &ValidationResult{Passed: false, Message: msg, Details: details, NeedsFix: true, Timestamp: time.Now()}
}

// NeedsFixResult 需修复。
func NeedsFixResult(msg string, errors ...string) *ValidationResult {
	return &ValidationResult{Passed: false, Message: msg, Errors: errors, NeedsFix: true, Timestamp: time.Now()}
}

// ValidationContext 验证上下文。
type ValidationContext struct {
	SessionID        string
	IterationNumber  int
	TaskDescription  string
	ContextData      map[string]any
	Metadata         map[string]any
}

// QualityGate 质量门禁（对标同名；checks 为检查名列表）。
type QualityGate struct {
	Name        string
	Description string
	Checks      []string
	Parameters  map[string]any
	Blocking    bool
}

// BuildGate 构建门禁（compilation + dependencies，blocking）。
func BuildGate() *QualityGate {
	return &QualityGate{Name: "build", Checks: []string{"compilation", "dependencies"}, Blocking: true}
}

// TestGate 测试门禁（unit-tests + integration-tests，blocking）。
func TestGate() *QualityGate {
	return &QualityGate{Name: "test", Checks: []string{"unit-tests", "integration-tests"}, Blocking: true}
}

// LintGate 风格门禁（style + complexity + duplication，非 blocking）。
func LintGate() *QualityGate {
	return &QualityGate{Name: "lint", Checks: []string{"style", "complexity", "duplication"}}
}

// AcceptAllValidator 恒过验证器（null validator 的显式形态）。
type AcceptAllValidator struct{}

// Validate 恒过。
func (AcceptAllValidator) Validate(any, *ValidationCriteria) *ValidationResult {
	return PassedResult("No validator configured")
}

// ValidateQualityGate 恒过。
func (AcceptAllValidator) ValidateQualityGate(*QualityGate, any) *ValidationResult {
	return PassedResult("gate auto-passed")
}

// ValidateIteration 恒过。
func (AcceptAllValidator) ValidateIteration(*IterationResult, *ValidationContext) *ValidationResult {
	return PassedResult("iteration auto-passed")
}
