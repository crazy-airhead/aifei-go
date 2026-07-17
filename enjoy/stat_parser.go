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

// ForStat represents #for.
type ForStat struct {
	VarName  string
	IterExpr Expr
	Body     Stat
	Init     Stat
	Cond     Expr
	Update   Stat
	IsRange  bool
}

func (s *ForStat) Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {
	if s.IsRange {
		s.execRange(env, scope, writer, ctrl)
	} else {
		s.execTrad(env, scope, writer, ctrl)
	}
}

func (s *ForStat) execRange(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {
	val := s.IterExpr.Eval(scope, ctrl)
	items := toSlice(val)
	for i, item := range items {
		if ctrl.Return {
			return
		}
		child := scope.NewChild()
		child.Set(s.VarName, item)
		child.Set("index", i)
		child.Set("size", len(items))
		child.Set("first", i == 0)
		child.Set("last", i == len(items)-1)
		ctrl.Reset()
		s.Body.Exec(env, child, writer, ctrl)
	}
	ctrl.Reset()
}

func (s *ForStat) execTrad(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {
	if s.Init != nil {
		s.Init.Exec(env, scope, writer, ctrl)
	}
	for {
		if ctrl.Return {
			return
		}
		if s.Cond != nil && !isTruthy(s.Cond.Eval(scope, ctrl)) {
			break
		}
		ctrl.Reset()
		s.Body.Exec(env, scope, writer, ctrl)
		if ctrl.Break {
			break
		}
		if s.Update != nil {
			s.Update.Exec(env, scope, writer, ctrl)
		}
	}
	ctrl.Reset()
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

func (s *DefineStat) Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {
	env.AddFunction(s.Name, s)
}

// CallStat represents #call or #@name.
type CallStat struct {
	FuncName string
	Args     []Expr
}

func (s *CallStat) Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {
	def := env.GetFunction(s.FuncName)
	if def == nil {
		return
	}
	args := make([]interface{}, len(s.Args))
	for i, a := range s.Args {
		args[i] = a.Eval(scope, ctrl)
	}
	childScope := NewScope(make(map[string]interface{}))
	for i, name := range def.Params {
		if i < len(args) {
			childScope.Set(name, args[i])
		}
	}
	def.Body.Exec(env, childScope, writer, ctrl)
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

// SetAsStat wraps an Expr as a Stat.
type SetAsStat struct {
	Expr Expr
}

func (s *SetAsStat) Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {
	if s.Expr != nil {
		s.Expr.Eval(scope, ctrl)
	}
}

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
	case TokCall:
		return parseCallStat(tok.Val)
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
	body, _, err := collectUntil(lexer, env, TokEnd)
	if err != nil {
		return nil, err
	}

	header = strings.TrimSpace(header)

	if idx := strings.Index(header, " : "); idx != -1 {
		varName := strings.TrimSpace(header[:idx])
		iterStr := strings.TrimSpace(header[idx+3:])
		iterExpr, err := ParseExpr(iterStr)
		if err != nil {
			return nil, err
		}
		return &ForStat{VarName: varName, IterExpr: iterExpr, Body: body, IsRange: true}, nil
	}
	if idx := strings.Index(header, " in "); idx != -1 {
		varName := strings.TrimSpace(header[:idx])
		iterStr := strings.TrimSpace(header[idx+4:])
		iterExpr, err := ParseExpr(iterStr)
		if err != nil {
			return nil, err
		}
		return &ForStat{VarName: varName, IterExpr: iterExpr, Body: body, IsRange: true}, nil
	}

	parts := strings.Split(header, ";")
	if len(parts) >= 3 {
		initExpr, _ := ParseExpr(strings.TrimSpace(parts[0]))
		condExpr, _ := ParseExpr(strings.TrimSpace(parts[1]))
		updateExpr, _ := ParseExpr(strings.TrimSpace(parts[2]))
		return &ForStat{
			Body:    body,
			IsRange: false,
			Init:    &SetAsStat{Expr: initExpr},
			Cond:    condExpr,
			Update:  &SetAsStat{Expr: updateExpr},
		}, nil
	}

	return &NullStat{}, nil
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

func parseCallStat(val string) (Stat, error) {
	val = strings.TrimSpace(val)
	parenIdx := strings.Index(val, "(")
	name := val
	var args []Expr
	if parenIdx != -1 {
		name = strings.TrimSpace(val[:parenIdx])
		if len(val) > parenIdx+1 {
			argStr := val[parenIdx+1:]
			if idx := strings.Index(argStr, ")"); idx != -1 {
				argStr = argStr[:idx]
			}
			for _, a := range strings.Split(argStr, ",") {
				a = strings.TrimSpace(a)
				if a != "" {
					ex, err := ParseExpr(a)
					if err != nil {
						continue
					}
					args = append(args, ex)
				}
			}
		}
	}
	return &CallStat{FuncName: name, Args: args}, nil
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
