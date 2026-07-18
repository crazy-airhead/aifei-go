package enjoy

import (
	"fmt"
	"strings"
)

// StatList is a list of statements.
type StatList struct {
	Stats []Stat
}

func (s *StatList) Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {
	for _, stat := range s.Stats {
		if ctrl.Return || ctrl.Break || ctrl.Continue {
			return
		}
		stat.Exec(env, scope, writer, ctrl)
	}
}

// Text outputs raw text.
type Text struct {
	Content string
}

func (s *Text) Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {
	writer.WriteString(s.Content)
}

// Output evaluates and outputs an expression: #(expr).
type Output struct {
	Expr Expr
}

func (s *Output) Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {
	v := s.Expr.Eval(scope, ctrl)
	if v != nil {
		writer.WriteString(fmt.Sprintf("%v", v))
	}
}

// If represents #if / #elseif / #else / #end.
type IfStat struct {
	Cond     Expr
	Then     Stat
	ElseIfs  []ElseIfStat
	ElseStat Stat
}

type ElseIfStat struct {
	Cond Expr
	Then Stat
}

func (s *IfStat) Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {
	if isTruthy(s.Cond.Eval(scope, ctrl)) {
		s.Then.Exec(env, scope, writer, ctrl)
		return
	}
	for _, ei := range s.ElseIfs {
		if isTruthy(ei.Cond.Eval(scope, ctrl)) {
			ei.Then.Exec(env, scope, writer, ctrl)
			return
		}
	}
	if s.ElseStat != nil {
		s.ElseStat.Exec(env, scope, writer, ctrl)
	}
}

// ForStat represents the iterator form #for(id : expr) ... #else ... #end
// （对照 Java For 的 forIterator 分支）。Go 版本仅支持迭代型 for —— 不支持 Java 的
// C 风格 for(init; cond; update)（forLoop），以收敛 for 的语义。
//
// 循环状态聚合为作用域内的 `for` 对象，模板用 for.index/count/first/last/odd/even/size/outer
// 对象式访问（对照 Java ForIteratorStatus）。#else 体在循环一次未执行（空集合）时运行。
type ForStat struct {
	VarName  string
	IterExpr Expr
	Body     Stat
	Else     Stat
}

// Exec 执行迭代型 #for(id : expr)。跳转语义对照 Java For.forIterator：
// #return 透传不在此复位；#break 跳出循环；#continue 跳过本次后续并进入下一轮。
// break/continue 复位（setJumpNone），return 保留。
func (s *ForStat) Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {
	val := s.IterExpr.Eval(scope, ctrl)
	items := toSlice(val)
	size := len(items)
	// 捕获外层循环状态，供内层 for.outer 访问（对照 Java Object outer = scope.get("for")）。
	outer := scope.Get("for")
	ran := false
	for i, item := range items {
		if ctrl.Return {
			return
		}
		ran = true
		child := scope.NewChild()
		child.Set(s.VarName, item)
		child.Set("for", forIteratorStatus(outer, i, size))
		s.Body.Exec(env, child, writer, ctrl)
		if ctrl.Return {
			return
		}
		if ctrl.Break {
			ctrl.Break = false
			break
		}
		if ctrl.Continue {
			ctrl.Continue = false
			continue
		}
	}
	if !ran && s.Else != nil {
		s.Else.Exec(env, scope, writer, ctrl)
	}
}

// SetStat represents #set, #setLocal, #setGlobal.
type SetStat struct {
	Name     string
	Expr     Expr
	ScopeTyp string
}

func (s *SetStat) Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {
	v := s.Expr.Eval(scope, ctrl)
	switch s.ScopeTyp {
	case "local":
		scope.SetLocal(s.Name, v)
	case "global":
		scope.SetGlobal(s.Name, v)
	default:
		scope.Set(s.Name, v)
	}
}

// DefineStat represents #define.
type DefineStat struct {
	Name   string
	Params []string
	Body   Stat
}

// Exec 在运行期把 #define 注册到当前 env。注意：与 Java（Define.exec 为空、仅 parse 期
// 注册）不同，Go 的 #include 走「子模板独立编译 + 在父 env 中执行」路径，被 include 模板
// 里的 define 需在此处（exec 期、父 env）再次注册才能渗入父模板，故保留运行期注册。
// 同模板内的前向引用由 parse 期注册（registerDefine）保证。
func (s *DefineStat) Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {
	env.AddFunction(s.Name, s)
}

// CallStat is the #@name(args) static call sugar (对照 Java Lexer state 20 →
// Symbol.CALL / CALL_IF_DEFINED)。函数名 name 由词法器解析为字面标识符；
// nullSafe 对应 #@name?(args) 的 callIfDefined 形态。动态调用 #call(funcName, args)
// 由 CallDirective 指令处理（对照 Java CallDirective）。
type CallStat struct {
	FuncName string
	NullSafe bool
	Args     []Expr
}

func (s *CallStat) Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {
	def := env.GetFunction(s.FuncName)
	if def == nil {
		return // Go 版本宽松：函数不存在则跳过（Java 非_nullSafe 时会抛异常）
	}
	callDefine(env, def, s.Args, scope, writer, ctrl)
}

// callDefine binds evaluated args to the function's params in a child scope and
// executes the function body (shared by #@name(args) 与 #call(...) 动态调用)。
// 对照 Java Define.call：子作用域以 caller scope 为 parent（new Scope(scope)），
// 故函数体内可见外层变量；body 执行后消化其内部 #return/#break/#continue（setJumpNone），
// 使函数体内的跳转不外泄到调用方。
func callDefine(env *Env, def *DefineStat, argExprs []Expr, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {
	args := make([]interface{}, len(argExprs))
	for i, a := range argExprs {
		args[i] = a.Eval(scope, ctrl)
	}
	// 以 caller scope 为 parent 构造子作用域：参数局部绑定，同时可见外层变量
	// （对照 Java scope = new Scope(scope)）。
	childScope := scope.NewChild()
	for i, name := range def.Params {
		if i < len(args) {
			childScope.Set(name, args[i])
		}
	}
	def.Body.Exec(env, childScope, writer, ctrl)
	// 函数体内的 #return/#break/#continue 在此消化，不外泄（对照 Java setJumpNone）。
	ctrl.Reset()
}

// BreakStat represents #break.
type BreakStat struct{}

func (s *BreakStat) Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {
	ctrl.Break = true
}

// ContinueStat represents #continue.
type ContinueStat struct{}

func (s *ContinueStat) Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {
	ctrl.Continue = true
}

// ReturnStat represents #return.
type ReturnStat struct {
	Expr Expr
}

func (s *ReturnStat) Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {
	if s.Expr != nil {
		ctrl.Attachment = s.Expr.Eval(scope, ctrl)
	}
	ctrl.Return = true
}

// ReturnIfStat represents #returnIf(cond): returns from the current
// template / define only when cond evaluates to true (对照 Java ReturnIf.java)。
// cond 是「返回条件」而非返回值，且不写入 ctrl.Attachment。
type ReturnIfStat struct {
	Cond Expr
}

func (s *ReturnIfStat) Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {
	if s.Cond != nil && isTruthy(s.Cond.Eval(scope, ctrl)) {
		ctrl.Return = true
	}
}

// NullStat is an empty statement.
type NullStat struct{}

func (s *NullStat) Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {}

// IncludeStat represents #include(path, arg1=val1, ...).
type IncludeStat struct {
	SubStat Stat
	assigns []AssignExpr
}

func (s *IncludeStat) Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {
	child := NewScope(make(map[string]interface{}))
	child.parent = scope
	child.global = scope.global
	for _, a := range s.assigns {
		child.Set(a.Name, a.Value.Eval(scope, ctrl))
	}
	s.SubStat.Exec(env, child, writer, ctrl)
}

// SwitchStat represents #switch(expr).
type SwitchStat struct {
	Expr      Expr
	FirstCase *CaseStat
	Default   *DefaultStat
}

func (s *SwitchStat) Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {
	switchValue := s.Expr.Eval(scope, ctrl)
	for c := s.FirstCase; c != nil; c = c.NextCase {
		if c.execIfMatch(switchValue, env, scope, writer, ctrl) {
			return
		}
	}
	if s.Default != nil {
		s.Default.Exec(env, scope, writer, ctrl)
	}
}

// CaseStat represents #case(v1, v2, ...).
type CaseStat struct {
	Exprs    []Expr
	Body     Stat
	NextCase *CaseStat
}

func (s *CaseStat) Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {}

func (s *CaseStat) execIfMatch(switchValue interface{}, env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) bool {
	for _, ex := range s.Exprs {
		v := ex.Eval(scope, ctrl)
		if valuesEqual(switchValue, v) {
			s.Body.Exec(env, scope, writer, ctrl)
			return true
		}
	}
	if s.NextCase != nil {
		return s.NextCase.execIfMatch(switchValue, env, scope, writer, ctrl)
	}
	return false
}

// DefaultStat represents #default.
type DefaultStat struct {
	Body Stat
}

func (s *DefaultStat) Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {
	s.Body.Exec(env, scope, writer, ctrl)
}

func valuesEqual(a, b interface{}) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

// ---- Template Parser ----

// ParseTemplate parses template tokens into a Stat AST.
func ParseTemplate(lexer *Lexer, env *Env) (Stat, error) {
	return parseStatList(lexer, env, TokEOF)
}

func parseStatList(lexer *Lexer, env *Env, endTok TokType) (Stat, error) {
	var stats []Stat
	for {
		tok := lexer.Scan()
		if tok.Type == TokEOF || tok.Type == endTok {
			break
		}
		stat, err := parseOneStat(tok, lexer, env)
		if err != nil {
			return nil, err
		}
		stat = registerDefine(stat, env)
		if stat != nil {
			stats = append(stats, stat)
		}
	}
	if len(stats) == 0 {
		return &NullStat{}, nil
	}
	if len(stats) == 1 {
		return stats[0], nil
	}
	return &StatList{Stats: stats}, nil
}

// registerDefine 在 parse 阶段把 #define 注册到 env，使模板支持前向引用（文档顺序靠后
// 的 define 也能被前面的 call 调用，对照 Java Parser.statList: env.addFunction）。返回
// stat 本身——与 Java 不同，Go 不把 DefineStat 从 stat 列表中剔除：#include 走「子模板
// 独立编译 + 在父 env 中执行」路径，DefineStat.Exec 会在父 env 中再次注册，使被 include
// 模板里的 define 能渗入父模板（覆盖 include 场景）；本处 parse 期注册覆盖同 env 前向引用。
func registerDefine(stat Stat, env *Env) Stat {
	if def, ok := stat.(*DefineStat); ok {
		env.AddFunction(def.Name, def)
	}
	return stat
}

func parseOneStat(tok Token, lexer *Lexer, env *Env) (Stat, error) {
	switch tok.Type {
	case TokText:
		return &Text{Content: tok.Val}, nil
	case TokOutput:
		ex, err := ParseExpr(tok.Val)
		if err != nil {
			return nil, fmt.Errorf("output expression error: %w", err)
		}
		return &Output{Expr: ex}, nil
	case TokIf:
		return parseIfStat(tok.Val, lexer, env)
	case TokFor:
		return parseForStat(tok.Val, lexer, env)
	case TokSet:
		return parseSetStat(tok.Val, "")
	case TokSetLocal:
		return parseSetStat(tok.Val, "local")
	case TokSetGlobal:
		return parseSetStat(tok.Val, "global")
	case TokDefine:
		return parseDefineStat(tok.Val, lexer, env)
	case TokCall, TokCallIfDefined:
		return parseCallStat(tok)
	case TokBreak:
		return &BreakStat{}, nil
	case TokContinue:
		return &ContinueStat{}, nil
	case TokInclude:
		return parseIncludeStat(tok.Val, env)
	case TokSwitch:
		return parseSwitchStat(tok.Val, lexer, env)
	case TokCase, TokDefault:
		return nil, nil
	case TokReturn:
		return parseReturnStat(tok.Val)
	case TokReturnIf:
		return parseReturnIfStat(tok.Val)
	case TokID:
		return parseDirectiveStat(tok, lexer, env)
	default:
		return &NullStat{}, nil
	}
}

// DirectiveStat wraps a custom directive for execution.
type DirectiveStat struct {
	Directive Directive
	Body      Stat
}

func (s *DirectiveStat) Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {
	s.Directive.Exec(env, scope, writer, ctrl)
}

func parseDirectiveStat(tok Token, lexer *Lexer, env *Env) (Stat, error) {
	config := env.GetEngineConfig()
	if config == nil || config.directiveMap == nil {
		return &NullStat{}, nil
	}

	factory, ok := config.directiveMap[tok.Name]
	if !ok {
		return &NullStat{}, nil
	}

	directive := factory()

	// Parse parameters into ExprList
	exprList := NewExprList()
	if tok.Val != "" {
		exprs, err := parseExprList(tok.Val)
		if err != nil {
			return nil, fmt.Errorf("directive #%s parameter error: %w", tok.Name, err)
		}
		exprList = exprs
	}

	directive.SetExprList(exprList)

	if !directive.HasEnd() {
		return &DirectiveStat{Directive: directive}, nil
	}

	// Collect body until #end
	body, _, err := collectUntil(lexer, env, TokEnd)
	if err != nil {
		return nil, err
	}
	directive.SetStat(body)
	return &DirectiveStat{Directive: directive, Body: body}, nil
}

// parseExprList parses a comma-separated list of expressions.
func parseExprList(s string) (*ExprList, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return NewExprList(), nil
	}

	parts := splitByComma(s)
	exprs := make([]Expr, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		ex, err := ParseExpr(part)
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, ex)
	}
	return NewExprList(exprs...), nil
}

// splitByComma splits a string by commas, respecting nested parentheses and quotes.
func splitByComma(s string) []string {
	var result []string
	depth := 0
	inQuote := byte(0)
	start := 0
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inQuote != 0 {
			if ch == '\\' {
				i++
			} else if ch == inQuote {
				inQuote = 0
			}
			continue
		}
		if ch == '"' || ch == '\'' {
			inQuote = ch
			continue
		}
		if ch == '(' {
			depth++
		} else if ch == ')' {
			depth--
		} else if ch == ',' && depth == 0 {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	result = append(result, s[start:])
	return result
}

func parseIfStat(condStr string, lexer *Lexer, env *Env) (Stat, error) {
	cond, err := ParseExpr(condStr)
	if err != nil {
		return nil, err
	}

	thenBody, thenTokens, err := collectUntil(lexer, env, TokElseIf, TokElse, TokEnd)
	if err != nil {
		return nil, err
	}

	var elseIfs []ElseIfStat
	var elseStat Stat

	for _, t := range thenTokens {
		if t.Type == TokElseIf {
			eiCond, _ := ParseExpr(t.Val)
			eiBody, eiTokens, err := collectUntil(lexer, env, TokElseIf, TokElse, TokEnd)
			if err != nil {
				return nil, err
			}
			elseIfs = append(elseIfs, ElseIfStat{Cond: eiCond, Then: eiBody})
			thenTokens = eiTokens
			continue
		}
		if t.Type == TokElse {
			elseBody, _, err := collectUntil(lexer, env, TokEnd)
			if err != nil {
				return nil, err
			}
			elseStat = elseBody
			break
		}
		if t.Type == TokEnd {
			break
		}
	}

	return &IfStat{Cond: cond, Then: thenBody, ElseIfs: elseIfs, ElseStat: elseStat}, nil
}

func collectUntil(lexer *Lexer, env *Env, stopToks ...TokType) (Stat, []Token, error) {
	var stats []Stat

	for {
		tok := lexer.Scan()
		if tok.Type == TokEOF {
			break
		}

		for _, st := range stopToks {
			if tok.Type == st {
				return mergeStats(stats), []Token{tok}, nil
			}
		}

		// A bare #end not in stopToks acts as an implicit stop.
		if tok.Type == TokEnd {
			return mergeStats(stats), []Token{tok}, nil
		}

		stat, err := parseOneStat(tok, lexer, env)
		if err != nil {
			return nil, nil, err
		}
		// #define 在 parse 期注册并剔除（同 parseStatList，对照 Java 递归 statList）。
		stat = registerDefine(stat, env)
		if stat == nil {
			continue
		}
		if _, ok := stat.(*NullStat); !ok {
			stats = append(stats, stat)
		}
	}

	return mergeStats(stats), nil, nil
}

// mergeStats collapses a stat slice into a single stat.
func mergeStats(stats []Stat) Stat {
	if len(stats) == 0 {
		return &NullStat{}
	}
	if len(stats) == 1 {
		return stats[0]
	}
	return &StatList{Stats: stats}
}

func parseForStat(header string, lexer *Lexer, env *Env) (Stat, error) {
	// 收集循环体，遇到 #else 或 #end 停止；命中 #else 则继续收集 else 体（对照 Java For 的 _else）。
	body, stopToks, err := collectUntil(lexer, env, TokElse, TokEnd)
	if err != nil {
		return nil, err
	}
	var elseStat Stat
	if len(stopToks) > 0 && stopToks[0].Type == TokElse {
		elseStat, _, err = collectUntil(lexer, env, TokEnd)
		if err != nil {
			return nil, err
		}
	}

	header = strings.TrimSpace(header)

	// 仅支持迭代型 #for(id : expr) / #for(id in expr)（对照 Java For 的 forIterator）。
	// 不支持 C 风格 for(init; cond; update) —— header 不匹配迭代型语法时报语法错误，
	// 经 errorStat 在渲染期输出错误标记，而非静默忽略（避免循环体被悄悄丢弃）。
	var varName, iterStr string
	if idx := strings.Index(header, " : "); idx != -1 {
		varName = strings.TrimSpace(header[:idx])
		iterStr = strings.TrimSpace(header[idx+3:])
	} else if idx := strings.Index(header, " in "); idx != -1 {
		varName = strings.TrimSpace(header[:idx])
		iterStr = strings.TrimSpace(header[idx+4:])
	} else {
		return nil, fmt.Errorf("#for syntax error: only iterator form '#for(id : expr)' or '#for(id in expr)' is supported, C-style 'for(init; cond; update)' is not: %q", header)
	}
	iterExpr, err := ParseExpr(iterStr)
	if err != nil {
		return nil, err
	}
	return &ForStat{VarName: varName, IterExpr: iterExpr, Body: body, Else: elseStat}, nil
}

func parseSetStat(val string, scopeType string) (Stat, error) {
	idx := strings.Index(val, "=")
	if idx == -1 {
		return &NullStat{}, nil
	}
	name := strings.TrimSpace(val[:idx])
	exprStr := strings.TrimSpace(val[idx+1:])
	ex, err := ParseExpr(exprStr)
	if err != nil {
		return nil, err
	}
	return &SetStat{Name: name, Expr: ex, ScopeTyp: scopeType}, nil
}

func parseDefineStat(header string, lexer *Lexer, env *Env) (Stat, error) {
	header = strings.TrimSpace(header)
	parenIdx := strings.Index(header, "(")
	name := header
	var params []string
	if parenIdx != -1 {
		name = strings.TrimSpace(header[:parenIdx])
		if len(header) > parenIdx+1 {
			paramStr := header[parenIdx+1:]
			if idx := strings.Index(paramStr, ")"); idx != -1 {
				paramStr = paramStr[:idx]
			}
			for _, p := range strings.Split(paramStr, ",") {
				p = strings.TrimSpace(p)
				if p != "" {
					params = append(params, p)
				}
			}
		}
	}
	body, _, err := collectUntil(lexer, env, TokEnd)
	if err != nil {
		return nil, err
	}
	return &DefineStat{Name: name, Params: params, Body: body}, nil
}

// parseCallStat parses the #@name(args) static call sugar. The function name is
// the literal identifier captured by the lexer (tok.Name)，无需启发式判定。
func parseCallStat(tok Token) (Stat, error) {
	val := strings.TrimSpace(tok.Val)
	var args []Expr
	if val != "" {
		exprs, err := parseExprList(val)
		if err != nil {
			return nil, fmt.Errorf("#@%s parameter error: %w", tok.Name, err)
		}
		for i := 0; i < exprs.Length(); i++ {
			args = append(args, exprs.GetExpr(i))
		}
	}
	return &CallStat{FuncName: tok.Name, NullSafe: tok.Type == TokCallIfDefined, Args: args}, nil
}

func parseReturnStat(val string) (Stat, error) {
	val = strings.TrimSpace(val)
	if val == "" {
		return &ReturnStat{}, nil
	}
	ex, err := ParseExpr(val)
	if err != nil {
		return nil, err
	}
	return &ReturnStat{Expr: ex}, nil
}

// parseReturnIfStat parses #returnIf(cond): cond is the return condition
// (对照 Java ReturnIf.java，空参数报错，expr 作为条件而非返回值)。
func parseReturnIfStat(val string) (Stat, error) {
	val = strings.TrimSpace(val)
	if val == "" {
		return nil, fmt.Errorf("the parameter of #returnIf directive can not be blank")
	}
	ex, err := ParseExpr(val)
	if err != nil {
		return nil, err
	}
	return &ReturnIfStat{Cond: ex}, nil
}

func parseIncludeStat(para string, env *Env) (Stat, error) {
	exprs, err := parseExprList(para)
	if err != nil {
		return nil, fmt.Errorf("#include parameter error: %w", err)
	}
	if exprs == nil || exprs.Length() == 0 {
		return nil, fmt.Errorf("#include requires a path argument")
	}

	first := exprs.GetExpr(0)
	constExpr, ok := first.(*ConstExpr)
	if !ok || constExpr.Type != "string" {
		return nil, fmt.Errorf("#include path must be a string literal")
	}

	subPath, _ := constExpr.Value.(string)
	engine := env.GetEngine()
	if engine == nil {
		return &NullStat{}, nil
	}

	if !strings.HasPrefix(subPath, "/") {
		basePath := ""
		if engine.config != nil {
			basePath = engine.config.baseTemplatePath
		}
		if basePath != "" {
			subPath = basePath + "/" + subPath
		}
	}

	subTpl := engine.GetTemplate(subPath)
	subStat := subTpl.ast
	if subStat == nil {
		return &NullStat{}, nil
	}

	var assigns []AssignExpr
	for i := 1; i < exprs.Length(); i++ {
		e := exprs.GetExpr(i)
		if ae, ok := e.(*AssignExpr); ok {
			assigns = append(assigns, *ae)
		}
	}

	return &IncludeStat{SubStat: subStat, assigns: assigns}, nil
}

func parseSwitchStat(para string, lexer *Lexer, env *Env) (Stat, error) {
	exprs, err := parseExprList(para)
	if err != nil {
		return nil, fmt.Errorf("#switch parameter error: %w", err)
	}
	if exprs == nil || exprs.Length() == 0 {
		return nil, fmt.Errorf("#switch requires a condition expression")
	}

	sw := &SwitchStat{Expr: exprs.GetExpr(0)}
	var lastCase *CaseStat

	// Read first token inside the switch block.
	tok := lexer.Scan()
	for {
		if tok.Type == TokEOF || tok.Type == TokEnd {
			break
		}

		if tok.Type == TokCase {
			caseExprs, err := parseExprList(tok.Val)
			if err != nil {
				return nil, fmt.Errorf("#case parameter error: %w", err)
			}
			body, stopToks, err := collectUntil(lexer, env, TokCase, TokDefault, TokEnd)
			if err != nil {
				return nil, err
			}

			exprSlice := make([]Expr, 0, caseExprs.Length())
			for i := 0; i < caseExprs.Length(); i++ {
				exprSlice = append(exprSlice, caseExprs.GetExpr(i))
			}
			c := &CaseStat{Exprs: exprSlice, Body: body}
			if lastCase == nil {
				sw.FirstCase = c
			} else {
				lastCase.NextCase = c
			}
			lastCase = c

			// Use the stop token returned by collectUntil as the next token.
			if len(stopToks) > 0 {
				tok = stopToks[0]
			} else {
				tok = lexer.Scan()
			}
			continue
		}

		if tok.Type == TokDefault {
			body, _, err := collectUntil(lexer, env, TokEnd)
			if err != nil {
				return nil, err
			}
			sw.Default = &DefaultStat{Body: body}
			break
		}

		tok = lexer.Scan()
	}

	return sw, nil
}
