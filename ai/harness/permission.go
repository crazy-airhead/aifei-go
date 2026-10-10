// permission.go 权限引擎（对标 harness.permission 包）：不可变规则 +
// 评估器（priority 降序、同序 DENY>ALLOW>ASK、首条匹配即返回）+ glob 模式。
package harness

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// PermissionBehavior 规则行为。
type PermissionBehavior uint8

const (
	BehaviorAllow PermissionBehavior = iota
	BehaviorDeny
	BehaviorAsk
)

// String 行为名（小写——frontmatter 形态）。
func (b PermissionBehavior) String() string {
	switch b {
	case BehaviorDeny:
		return "deny"
	case BehaviorAsk:
		return "ask"
	default:
		return "allow"
	}
}

// ParseBehavior 字符串解析（大小写不敏感；非法回落 Allow）。
func ParseBehavior(s string) PermissionBehavior {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "deny":
		return BehaviorDeny
	case "ask":
		return BehaviorAsk
	default:
		return BehaviorAllow
	}
}

// UnmarshalYAML frontmatter 小写行为名解析。
func (b *PermissionBehavior) UnmarshalYAML(value *yaml.Node) error {
	*b = ParseBehavior(value.Value)
	return nil
}

// PermissionMatcher 自定义匹配器（优先于 glob；Go 侧以函数位表达 Java 的
// BiPredicate）。
type PermissionMatcher func(toolName string, args map[string]any) bool

// PermissionRule 不可变规则（对标同名类）。
type PermissionRule struct {
	ToolName string            `yaml:"toolName"` // 支持 "*" 通配（equalsIgnoreCase）
	Behavior PermissionBehavior `yaml:"behavior"`
	Pattern  string            `yaml:"pattern"` // glob（如 "git *"、"rm -rf *"）；空 = 匹配全部
	Matcher PermissionMatcher  `yaml:"-"`       // 与 Pattern 互斥、优先
	Prompt  string             `yaml:"prompt"`  // 拦截提示（空 = 策略动态生成）
	Priority int               `yaml:"priority"`
}

// AllowRule 工具级放行。
func AllowRule(toolName string) PermissionRule {
	return PermissionRule{ToolName: toolName, Behavior: BehaviorAllow}
}

// DenyRule 工具级拒绝。
func DenyRule(toolName string) PermissionRule {
	return PermissionRule{ToolName: toolName, Behavior: BehaviorDeny}
}

// AskRule 工具级询问。
func AskRule(toolName string) PermissionRule {
	return PermissionRule{ToolName: toolName, Behavior: BehaviorAsk}
}

// AllowPattern glob 放行。
func AllowPattern(toolName, pattern string) PermissionRule {
	return PermissionRule{ToolName: toolName, Behavior: BehaviorAllow, Pattern: pattern}
}

// DenyPattern glob 拒绝。
func DenyPattern(toolName, pattern string) PermissionRule {
	return PermissionRule{ToolName: toolName, Behavior: BehaviorDeny, Pattern: pattern}
}

// WithPriority 优先级副本（priority 越大越优先）。
func (r PermissionRule) WithPriority(p int) PermissionRule {
	r.Priority = p
	return r
}

// WithPrompt 提示副本。
func (r PermissionRule) WithPrompt(p string) PermissionRule {
	r.Prompt = p
	return r
}

// PermissionContext 不可变上下文（workingDirectory + rules + additionalDirs）。
type PermissionContext struct {
	WorkingDirectory string
	Rules            []PermissionRule
	AdditionalDirs   []string
}

// NewPermissionContext 起步上下文。
func NewPermissionContext() *PermissionContext {
	return &PermissionContext{WorkingDirectory: "."}
}

// AddRule 追加规则（新实例）。
func (c *PermissionContext) AddRule(rules ...PermissionRule) *PermissionContext {
	out := c.copy()
	out.Rules = append(out.Rules, rules...)
	return out
}

// WithWorkingDirectory 换目录（新实例）。
func (c *PermissionContext) WithWorkingDirectory(dir string) *PermissionContext {
	out := c.copy()
	out.WorkingDirectory = dir
	return out
}

func (c *PermissionContext) copy() *PermissionContext {
	return &PermissionContext{
		WorkingDirectory: c.WorkingDirectory,
		Rules:            append([]PermissionRule(nil), c.Rules...),
		AdditionalDirs:   append([]string(nil), c.AdditionalDirs...),
	}
}

// PermissionDecision 评估输出（与 Behavior 同形）。
type PermissionDecision = PermissionBehavior

// PermissionEngine 评估器（无状态，可复用）。
type PermissionEngine struct{}

// NewPermissionEngine 构造。
func NewPermissionEngine() *PermissionEngine { return &PermissionEngine{} }

// Evaluate 评估：规则排序（priority 降序 → DENY>ALLOW>ASK）→ 首条匹配即返回；
// 无匹配 → defaultDecision。
func (e *PermissionEngine) Evaluate(toolName string, args map[string]any, ctx *PermissionContext, defaultDecision PermissionDecision) PermissionDecision {
	if ctx == nil || len(ctx.Rules) == 0 {
		return defaultDecision
	}
	rules := append([]PermissionRule(nil), ctx.Rules...)
	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].Priority != rules[j].Priority {
			return rules[i].Priority > rules[j].Priority
		}
		return behaviorWeight(rules[i].Behavior) > behaviorWeight(rules[j].Behavior)
	})

	for _, r := range rules {
		if !matchesToolName(r.ToolName, toolName) {
			continue
		}
		if !e.matchesPattern(r, args) {
			continue
		}
		return r.Behavior
	}
	return defaultDecision
}

func behaviorWeight(b PermissionBehavior) int {
	switch b {
	case BehaviorDeny:
		return 3
	case BehaviorAllow:
		return 2
	default:
		return 1
	}
}

func matchesToolName(pattern, name string) bool {
	if pattern == "*" {
		return true
	}
	return strings.EqualFold(pattern, name)
}

// matchesPattern 模式判定：matcher 优先；无 pattern 恒真；有 pattern 对提取
// 文本做大小写不敏感全串 glob 匹配。
func (e *PermissionEngine) matchesPattern(r PermissionRule, args map[string]any) bool {
	if r.Matcher != nil {
		return r.Matcher(r.ToolName, args)
	}
	if r.Pattern == "" {
		return true
	}
	text := extractArgText(args)
	if text == "" {
		return false
	}
	re, err := globToRegex(r.Pattern)
	if err != nil {
		return false
	}
	return re.MatchString(text)
}

// argTextFields 参数文本提取的字段序（Java 同序）。
var argTextFields = []string{"command", "file_path", "path", "content", "url", "link"}

// extractArgText 参数字段提取（首个字符串值；都没有取整体串；空参数 → 空）。
func extractArgText(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	for _, f := range argTextFields {
		if s, ok := args[f].(string); ok && s != "" {
			return s
		}
	}
	var sb strings.Builder
	sb.WriteString(mapKeySortedString(args))
	return sb.String()
}

// mapKeySortedString 参数整体串（键序稳定）。
func mapKeySortedString(args map[string]any) string {
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString("{")
	for i, k := range keys {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(k)
		sb.WriteString("=")
		sb.WriteString(stringifyArg(args[k]))
	}
	sb.WriteString("}")
	return sb.String()
}

func stringifyArg(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// globToRegex glob → 正则（* → .*、? → .、元字符转义；大小写不敏感全串）。
func globToRegex(pattern string) (*regexp.Regexp, error) {
	var sb strings.Builder
	sb.WriteString("(?i)^")
	for _, c := range pattern {
		switch c {
		case '*':
			sb.WriteString(".*")
		case '?':
			sb.WriteString(".")
		default:
			if strings.IndexByte(`\.+()[]{}|^$`, byte(c)) >= 0 {
				sb.WriteByte('\\')
			}
			sb.WriteRune(c)
		}
	}
	sb.WriteString("$")
	return regexp.Compile(sb.String())
}
