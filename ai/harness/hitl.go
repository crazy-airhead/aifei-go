// hitl.go HITL 策略与权限拦截器（对标 harness.hitl 包 + HITLInterceptor 的
// Go 对应物）。Go 侧以 ai.Interceptor 的 InterceptTool 缝实现否决：DENY/ASK
// 不放行工具链（ASK 在无审批回调时视同 DENY——fail-closed）；审批回调可注入，
// 命中后记忆 allow 规则（对标 onApproved）。
package harness

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent/react"
)

// HITLStrategy 单工具策略（对标同名接口）：返回空串 = 放行；非空 = 拦截文案。
type HITLStrategy interface {
	Evaluate(trace *react.Trace, args map[string]any) string
}

// HITLFunc 函数式策略。
type HITLFunc func(trace *react.Trace, args map[string]any) string

// Evaluate 实现 HITLStrategy。
func (f HITLFunc) Evaluate(trace *react.Trace, args map[string]any) string { return f(trace, args) }

// BashToolStrategy bash 命令安全审计（对标同名类）：6 条内置规则（注入/子
// shell/设备重定向、系统特权、路径回溯、敏感文件、只读白名单、不完整命令）
// + PermissionEngine 委托。
type BashToolStrategy struct {
	InternalRulesEnabled bool
	DefaultDecision      PermissionDecision
	PermissionContext    func() *PermissionContext
}

// NewBashToolStrategy 缺省构造（内置规则开、默认 ALLOW）。
func NewBashToolStrategy(ctx func() *PermissionContext) *BashToolStrategy {
	return &BashToolStrategy{InternalRulesEnabled: true, DefaultDecision: BehaviorAllow, PermissionContext: ctx}
}

// Evaluate 审计。
func (s *BashToolStrategy) Evaluate(trace *react.Trace, args map[string]any) string {
	command, _ := args["command"].(string)
	if command == "" {
		return "" // 空命令放行（Java 规则 ①）
	}
	if !s.InternalRulesEnabled {
		return s.engineDecision("bash", args)
	}

	// ② 注入/子 shell/设备重定向
	if strings.ContainsAny(command, "`") || strings.Contains(command, "$(") || strings.Contains(command, "/dev/") {
		return "检测到潜在的命令注入或设备重定向风险。"
	}
	// ③ 系统特权
	if privilegedCommandPattern.MatchString(command) {
		return fmt.Sprintf("检测到系统特权指令 %s。", firstWord(command))
	}
	// ④ 路径回溯
	if strings.Contains(command, "../") || strings.Contains(command, `..\`) {
		return "检测到路径回溯（../），拒绝执行。"
	}
	// ⑤ 敏感文件
	for _, p := range sensitivePathPrefixes {
		if strings.Contains(command, p) {
			return fmt.Sprintf("检测到敏感路径 %s，拒绝执行。", p)
		}
	}
	// ⑥a 只读白名单
	if IsSearchOrReadCommand(command) {
		return ""
	}
	// ⑥b 不完整命令
	if isIncompleteCommand(command) {
		return "命令不完整（尾部悬挂管道/连接符），拒绝执行。"
	}
	return s.engineDecision("bash", args)
}

func (s *BashToolStrategy) engineDecision(tool string, args map[string]any) string {
	ctx := (*PermissionContext)(nil)
	if s.PermissionContext != nil {
		ctx = s.PermissionContext()
	}
	decision := NewPermissionEngine().Evaluate(tool, args, ctx, s.DefaultDecision)
	switch decision {
	case BehaviorDeny:
		return "操作被权限策略拒绝。"
	case BehaviorAsk:
		return "高危操作，需要人工介入确认。"
	}
	return ""
}

var privilegedCommandPattern = regexp.MustCompile(`\b(sudo|su|chown|chmod|chgrp|passwd|visudo)\b`)

var sensitivePathPrefixes = []string{"/etc/", "/var/", "/root/", "~/.ssh/", "~/.bashrc", "~/.zshrc"}

// BashCommandClassifier 三级分类（对标同名类）。
const (
	bashDeny  = 0 // 不完整/空
	bashAllow = 1 // 只读
	bashAsk   = 2 // 其余
)

// searchReadCommands 只读白名单（对标 SEARCH_READ_COMMANDS，含 git 只读子命令）。
var searchReadCommands = map[string]bool{
	"grep": true, "egrep": true, "fgrep": true, "rg": true, "ag": true, "ack": true,
	"find": true, "fd": true, "locate": true,
	"ls": true, "dir": true, "tree": true, "exa": true,
	"cat": true, "bat": true, "less": true, "more": true, "head": true, "tail": true,
	"wc": true, "file": true, "which": true, "whereis": true, "whence": true, "type": true,
	"stat": true, "du": true, "df": true,
	"echo": true, "printf": true,
	"diff": true, "comm": true, "sort": true, "uniq": true, "cut": true, "tr": true,
	"awk": true, "sed": true, "jq": true, "yq": true, "xmllint": true,
	"pwd": true, "env": true, "printenv": true, "id": true, "whoami": true,
	"hostname": true, "uname": true, "date": true, "cal": true,
	"git": true, // 子命令细分在 isGitReadOnly
}

var gitReadOnlySubcommands = map[string]bool{
	"log": true, "show": true, "diff": true, "status": true, "branch": true, "tag": true,
	"remote": true, "rev-parse": true, "ls-files": true, "blame": true, "shortlog": true,
}

var gitWriteSubcommands = map[string]bool{
	"commit": true, "push": true, "pull": true, "merge": true, "rebase": true,
	"reset": true, "checkout": true, "switch": true, "stash": true, "clean": true,
	"rm": true, "mv": true,
}

// IsSearchOrReadCommand 管道/连接符（|、&&、||、;）所有段均须只读且非空
//（输出重申报读、sed -i 报读、git branch -d 报读）。
func IsSearchOrReadCommand(command string) bool {
	for _, seg := range splitCommandSegments(command) {
		if !isSingleReadCommand(strings.TrimSpace(seg)) {
			return false
		}
	}
	return true
}

// splitCommandSegments 按管道与逻辑连接符切段（空段天然不满足只读）。
func splitCommandSegments(command string) []string {
	s := command
	s = strings.ReplaceAll(s, "&&", "\x00")
	s = strings.ReplaceAll(s, "||", "\x00")
	s = strings.ReplaceAll(s, "|", "\x00")
	s = strings.ReplaceAll(s, ";", "\x00")
	return strings.Split(s, "\x00")
}

func isSingleReadCommand(seg string) bool {
	fields := strings.Fields(seg)
	if len(fields) == 0 {
		return false
	}
	name := fields[0]
	if !searchReadCommands[name] {
		return false
	}
	if name == "git" {
		if len(fields) < 2 {
			return true // 裸 git 视为只读
		}
		if !gitReadOnlySubcommands[fields[1]] {
			return false
		}
		// git branch -d/-D/--delete 为写操作
		if fields[1] == "branch" {
			for _, f := range fields[2:] {
				if f == "-d" || f == "-D" || f == "--delete" {
					return false
				}
			}
		}
		return true
	}
	// sed -i/--in-place 不算只读
	if name == "sed" {
		for _, f := range fields[1:] {
			if f == "-i" || f == "--in-place" || strings.HasPrefix(f, "-i") {
				return false
			}
		}
	}
	// 输出重定向（> 或 >>，2> stderr 除外）不算只读
	for i := 0; i < len(seg); i++ {
		if seg[i] == '>' {
			if i > 0 && seg[i-1] == '2' {
				continue // 2> stderr
			}
			if i+1 < len(seg) && seg[i+1] == '>' {
				return false
			}
			return false
		}
	}
	return true
}

// isIncompleteCommand 尾部悬挂 |、&&、||、;。
func isIncompleteCommand(command string) bool {
	trimmed := strings.TrimSpace(command)
	for _, suffix := range []string{"|", "&&", "||", ";"} {
		if strings.HasSuffix(trimmed, suffix) {
			return true
		}
	}
	return false
}

func firstWord(s string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return s
	}
	return fields[0]
}

// WriteToolStrategy write/edit 工具策略（路径回溯 + 敏感文件两条内置 DENY）。
type WriteToolStrategy struct {
	ToolName            string
	InternalRulesEnabled bool
	DefaultDecision     PermissionDecision
	PermissionContext   func() *PermissionContext
}

// NewWriteToolStrategy 构造。
func NewWriteToolStrategy(toolName string, ctx func() *PermissionContext) *WriteToolStrategy {
	return &WriteToolStrategy{ToolName: toolName, InternalRulesEnabled: true, DefaultDecision: BehaviorAllow, PermissionContext: ctx}
}

// Evaluate 审计。
func (s *WriteToolStrategy) Evaluate(trace *react.Trace, args map[string]any) string {
	path, _ := args["file_path"].(string)
	if path != "" && s.InternalRulesEnabled {
		if strings.Contains(path, "../") || strings.Contains(path, `..\`) {
			return "检测到路径回溯（../），拒绝写入。"
		}
		for _, p := range sensitivePathPrefixes {
			if strings.HasPrefix(path, p) || strings.Contains(path, p) {
				return fmt.Sprintf("检测到敏感路径 %s，拒绝写入。", p)
			}
		}
	}
	// 委托规则引擎（无内置命中时）
	ctx := (*PermissionContext)(nil)
	if s.PermissionContext != nil {
		ctx = s.PermissionContext()
	}
	switch NewPermissionEngine().Evaluate(s.ToolName, args, ctx, s.DefaultDecision) {
	case BehaviorDeny:
		return "文件写入操作被权限策略拒绝。"
	case BehaviorAsk:
		return "文件写入需要人工介入确认。"
	}
	return ""
}

// WebToolStrategy webfetch/websearch 高风险域名策略。
type WebToolStrategy struct {
	ToolName          string
	RiskyHosts        map[string]bool
	DefaultDecision   PermissionDecision
	PermissionContext func() *PermissionContext
}

// NewWebToolStrategy 构造（内置六家高风险域名——Java 同款）。
func NewWebToolStrategy(toolName string, ctx func() *PermissionContext) *WebToolStrategy {
	s := &WebToolStrategy{ToolName: toolName, RiskyHosts: map[string]bool{}, DefaultDecision: BehaviorAllow, PermissionContext: ctx}
	s.RiskyHostnames("facebook", "twitter", "x", "tiktok", "reddit", "linkedin")
	return s
}

// RiskyHostnames 追加高风险域名。
func (s *WebToolStrategy) RiskyHostnames(hosts ...string) {
	for _, h := range hosts {
		s.RiskyHosts[strings.ToLower(strings.TrimSpace(h))] = true
	}
}

// Evaluate 审计。
func (s *WebToolStrategy) Evaluate(trace *react.Trace, args map[string]any) string {
	url := ""
	for _, f := range []string{"url", "link", "address"} {
		if v, _ := args[f].(string); v != "" {
			url = v
			break
		}
	}
	if url != "" {
		if host := hostnameOf(url); host != "" && s.isRisky(host) {
			return fmt.Sprintf("检测到高风险域名 %s，拒绝访问。", host)
		}
	}
	ctx := (*PermissionContext)(nil)
	if s.PermissionContext != nil {
		ctx = s.PermissionContext()
	}
	switch NewPermissionEngine().Evaluate(s.ToolName, args, ctx, s.DefaultDecision) {
	case BehaviorDeny:
		return "网络访问操作被权限策略拒绝。"
	case BehaviorAsk:
		return "网络访问需要人工介入确认。"
	}
	return ""
}

func (s *WebToolStrategy) isRisky(host string) bool {
	host = strings.ToLower(strings.TrimPrefix(host, "www."))
	if s.RiskyHosts[host] {
		return true
	}
	for h := range s.RiskyHosts {
		// 域边界匹配：api.twitter.com 对 twitter（含 "twitter."）、facebook.com
		// 对 facebook 同理（Java 实测拦截集合反推的包含边界形态）
		if strings.HasSuffix(host, "."+h) || strings.Contains(host, h+".") {
			return true
		}
	}
	return false
}

func hostnameOf(rawURL string) string {
	s := rawURL
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.LastIndex(s, ":"); i >= 0 {
		s = s[:i]
	}
	return s
}



// PermissionInterceptor 权限拦截器（HITLInterceptor 的 Go 对应物）：InterceptTool
// 缝否决——策略文案直接作为错误结果返回给模型（对标 Java 的拦截消息）。
// Approval 回调命中后记忆 allow 规则（onApproved 对应物）。
type PermissionInterceptor struct {
	react.InterceptorBase
	strategies map[string]HITLStrategy
	approval   func(toolName string, args map[string]any) bool
	memory     *PermissionContext // 审批记忆（allow 规则）
}

// NewPermissionInterceptor 构造。
func NewPermissionInterceptor() *PermissionInterceptor {
	return &PermissionInterceptor{
		strategies: map[string]HITLStrategy{},
		memory:     NewPermissionContext(),
	}
}

// OnTool 注册策略（对标 HITLInterceptor.onTool）。
func (p *PermissionInterceptor) OnTool(toolName string, strategy HITLStrategy) *PermissionInterceptor {
	p.strategies[toolName] = strategy
	return p
}

// OnApproved 审批回调（返回 true = 放行并记忆规则）。
func (p *PermissionInterceptor) OnApproved(fn func(toolName string, args map[string]any) bool) *PermissionInterceptor {
	p.approval = fn
	return p
}

// InterceptTool 权限评估主入口。
func (p *PermissionInterceptor) InterceptTool(req *ai.ToolRequest, chain *ai.ToolChain) *ai.ToolResult {
	name := req.Name()
	strategy, ok := p.strategies[name]
	if !ok {
		return chain.DoIntercept(req) // 无策略直通
	}
	reason := strategy.Evaluate(nil, req.Args())
	if reason == "" {
		return chain.DoIntercept(req)
	}
	// ASK 形态：有审批回调且批准 → 记忆规则并放行
	if p.approval != nil && p.approval(name, req.Args()) {
		p.memory = p.memory.AddRule(approvedRuleOf(name, req.Args()))
		return chain.DoIntercept(req)
	}
	return &ai.ToolResult{IsError: true, Text: reason}
}

// approvedRuleOf 审批记忆规则（优先取 command/file_path 作 pattern——Java 同款）。
func approvedRuleOf(toolName string, args map[string]any) PermissionRule {
	for _, f := range []string{"command", "file_path"} {
		if v, _ := args[f].(string); v != "" {
			return AllowPattern(toolName, v+"*")
		}
	}
	return AllowRule(toolName)
}

// Memory 审批记忆上下文（引擎合并消费）。
func (p *PermissionInterceptor) Memory() *PermissionContext { return p.memory }
