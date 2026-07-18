package enjoy

import (
	"bytes"
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"time"
)

// This file implements the builtin directives registered by NewEngineConfig
// (对照 Java aifei-enjoy/ext/directive/{Date,Escape,Number,Random,Render,String,Call}Directive.java)。
// #call(...) 作为指令注册（动态函数名）；#@name(args) 静态糖在词法器/parseCallStat 中处理。

// ---- #date -----------------------------------------------------------------

// DateDirective formats a date value (对照 Java DateDirective)。
//
//	#date(createAt)                       用默认 datePattern
//	#date(createAt, "yyyy-MM-dd HH:mm:ss") 用指定 pattern
//	#date()                               输出当前时间（默认 pattern）
type DateDirective struct {
	dateExpr    Expr
	patternExpr Expr
}

func (d *DateDirective) SetExprList(exprList *ExprList) {
	switch exprList.Length() {
	case 0:
		d.dateExpr = nil
		d.patternExpr = nil
	case 1:
		d.dateExpr = exprList.GetExpr(0)
		d.patternExpr = nil
	case 2:
		d.dateExpr = exprList.GetExpr(0)
		d.patternExpr = exprList.GetExpr(1)
	default:
		panic("Wrong number parameter of #date directive, two parameters allowed at most")
	}
}

func (d *DateDirective) SetStat(Stat) {}

func (d *DateDirective) HasEnd() bool { return false }

func (d *DateDirective) Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {
	var dateVal interface{}
	if d.dateExpr != nil {
		dateVal = d.dateExpr.Eval(scope, ctrl)
	} else {
		dateVal = time.Now()
	}

	pattern := ""
	if env != nil {
		if cfg := env.GetEngineConfig(); cfg != nil {
			pattern = cfg.GetDatePattern()
		}
	}
	if d.patternExpr != nil {
		if s, ok := d.patternExpr.Eval(scope, ctrl).(string); ok {
			pattern = s
		}
	}

	out := formatDate(dateVal, pattern)
	if out != "" {
		writer.WriteString(out)
	}
}

// formatDate renders a date value using a Java-style pattern. Only time.Time is
// formatted (对照 Java 仅支持 Date / Temporal)；nil 输出空串，其余类型退化为 toStr。
func formatDate(value interface{}, pattern string) string {
	if value == nil {
		return ""
	}
	t, ok := value.(time.Time)
	if !ok {
		return toStr(value)
	}
	return t.Format(javaDatePatternToGo(pattern))
}

// javaDatePatternToGo translates a Java (SimpleDateFormat) date pattern into a
// Go time layout (对照 Java pattern 语义)。覆盖常见 token：y/M/d/H/h/m/s/S/a/E。
func javaDatePatternToGo(pattern string) string {
	if pattern == "" {
		pattern = defaultDatePattern
	}
	// 长 token 在前，保证贪婪匹配（yyyy 先于 yy，SSS 先于 S，EEEE 先于 E …）。
	tokens := []struct{ java, goLayout string }{
		{"yyyy", "2006"},
		{"yyy", "2006"},
		{"yy", "06"},
		{"y", "2006"},
		{"SSS", "000"},
		{"SS", "000"},
		{"S", "000"},
		{"EEEE", "Monday"},
		{"EEE", "Mon"},
		{"EE", "Mon"},
		{"E", "Mon"},
		{"HH", "15"},
		{"H", "15"},
		{"hh", "03"},
		{"h", "3"},
		{"mm", "04"},
		{"m", "4"},
		{"ss", "05"},
		{"s", "5"},
		{"MM", "01"},
		{"M", "1"},
		{"dd", "02"},
		{"d", "2"},
		{"a", "PM"},
	}
	var b strings.Builder
	i := 0
	for i < len(pattern) {
		matched := false
		for _, tk := range tokens {
			if strings.HasPrefix(pattern[i:], tk.java) {
				b.WriteString(tk.goLayout)
				i += len(tk.java)
				matched = true
				break
			}
		}
		if !matched {
			b.WriteByte(pattern[i])
			i++
		}
	}
	return b.String()
}

// ---- #escape ---------------------------------------------------------------

// EscapeDirective HTML-escapes its argument: < > " ' & (对照 Java EscapeDirective)。
//
//	#escape(value)
type EscapeDirective struct {
	exprList *ExprList
}

func (d *EscapeDirective) SetExprList(exprList *ExprList) { d.exprList = exprList }

func (d *EscapeDirective) SetStat(Stat) {}

func (d *EscapeDirective) HasEnd() bool { return false }

func (d *EscapeDirective) Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {
	if d.exprList == nil || d.exprList.Length() == 0 {
		return
	}
	// 对照 Java exprList.eval(scope) 取最后一个表达式的值。
	value := d.exprList.GetExpr(d.exprList.Length()-1).Eval(scope, ctrl)
	if value == nil {
		return
	}
	switch v := value.(type) {
	case string:
		writer.WriteString(htmlEscape(v))
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		writer.WriteString(toStr(v)) // 数字原样输出（对照 Java Number 分支）
	default:
		writer.WriteString(htmlEscape(toStr(v)))
	}
}

// htmlEscape replaces < > " ' & with HTML entities, char-by-char to avoid
// double-escaping the ampersand (对照 Java EscapeDirective.escape)。
func htmlEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&#39;") // IE 不支持 &apos;，对照 Java 用 &#39;
		case '&':
			b.WriteString("&amp;")
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// ---- #number ---------------------------------------------------------------

// NumberDirective formats a number using a Java DecimalFormat-style pattern
// (对照 Java NumberDirective)。实现了 DecimalFormat 的常用子集：小数位 (#/0)、
// 千分位分组 (,)、百分号 (%)、字面前后缀。
//
//	#number(n)
//	#number(n, "#.##")
//	#number(0.9518, "#.##%")
//	#number(300000, "光速为每秒 ,### 公里。")
type NumberDirective struct {
	valueExpr   Expr
	patternExpr Expr
}

func (d *NumberDirective) SetExprList(exprList *ExprList) {
	if exprList.Length() == 0 {
		panic("The parameter of #number directive can not be blank")
	}
	if exprList.Length() > 2 {
		panic("Wrong number parameter of #number directive, two parameters allowed at most")
	}
	d.valueExpr = exprList.GetExpr(0)
	if exprList.Length() == 2 {
		d.patternExpr = exprList.GetExpr(1)
	}
}

func (d *NumberDirective) SetStat(Stat) {}

func (d *NumberDirective) HasEnd() bool { return false }

func (d *NumberDirective) Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {
	value := d.valueExpr.Eval(scope, ctrl)
	if value == nil {
		return
	}
	pattern := ""
	if d.patternExpr != nil {
		if s, ok := d.patternExpr.Eval(scope, ctrl).(string); ok {
			pattern = s
		}
	}
	roundingMode := RoundingModeHalfEven
	if cfg := env.GetEngineConfig(); cfg != nil {
		roundingMode = cfg.GetRoundingMode()
	}
	writer.WriteString(formatNumber(value, pattern, roundingMode))
}

// formatNumber formats a numeric value with a Java DecimalFormat-style pattern
// (subset)。roundingMode 控制舍入：HALF_EVEN（Go FormatFloat 默认，对照 Java DecimalFormat
// 默认）/ HALF_UP（math.Round 远离 0 四舍五入）；其余模式回退 HALF_EVEN。
func formatNumber(value interface{}, pattern string, roundingMode string) string {
	f := toFloat64(value)
	if pattern == "" {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}

	isPercent := strings.Contains(pattern, "%")
	if isPercent {
		f = f * 100
	}

	// 定位数字核心（由 # 0 , . 组成的连续片段），其前后为字面前/后缀。
	coreStart, coreEnd := -1, -1
	for i := 0; i < len(pattern); i++ {
		if isNumberPatternChar(pattern[i]) {
			if coreStart == -1 {
				coreStart = i
			}
			coreEnd = i + 1
		}
	}
	if coreStart == -1 {
		return pattern // 纯字面 pattern，原样返回
	}
	prefix := pattern[:coreStart]
	core := pattern[coreStart:coreEnd]
	suffix := pattern[coreEnd:]

	intPat, fracPat := core, ""
	if i := strings.Index(core, "."); i >= 0 {
		intPat = core[:i]
		fracPat = core[i+1:]
	}
	grouping := strings.Contains(intPat, ",")
	intPatClean := strings.ReplaceAll(intPat, ",", "")

	maxFrac := 0
	minFrac := 0
	for _, c := range fracPat {
		if c == '#' || c == '0' {
			maxFrac++
		}
		if c == '0' {
			minFrac++
		}
	}
	minInt := strings.Count(intPatClean, "0")

	neg := f < 0
	af := f
	if neg {
		af = -af
	}

	// HALF_UP：先按 maxFrac 位四舍五入（math.Round 远离 0，对正数即 HALF_UP），
	// 再交给 FormatFloat 定位；HALF_EVEN（默认）与其他模式直接用 FormatFloat 的 round-half-even。
	if roundingMode == RoundingModeHalfUp && maxFrac >= 0 {
		pow := math.Pow(10, float64(maxFrac))
		af = math.Round(af*pow) / pow
	}

	// FormatFloat 以 maxFrac 固定小数位（含四舍五入），再按 # 的可选语义裁剪尾零。
	full := strconv.FormatFloat(af, 'f', maxFrac, 64)
	intStr, fracStr := full, ""
	if dot := strings.Index(full, "."); dot >= 0 {
		intStr = full[:dot]
		fracStr = full[dot+1:]
	}
	for len(intStr) < minInt {
		intStr = "0" + intStr
	}
	if grouping {
		intStr = groupBy3(intStr, ',')
	}
	for len(fracStr) > minFrac && strings.HasSuffix(fracStr, "0") {
		fracStr = fracStr[:len(fracStr)-1]
	}

	var b strings.Builder
	b.WriteString(prefix)
	if neg {
		b.WriteByte('-')
	}
	b.WriteString(intStr)
	if len(fracStr) > 0 {
		b.WriteByte('.')
		b.WriteString(fracStr)
	}
	b.WriteString(suffix)
	return b.String()
}

func isNumberPatternChar(c byte) bool {
	return c == '#' || c == '0' || c == ',' || c == '.'
}

// groupBy3 inserts sep every 3 digits from the right (对照 DecimalFormat 分组)。
func groupBy3(s string, sep byte) string {
	n := len(s)
	if n <= 3 {
		return s
	}
	first := n % 3
	var b strings.Builder
	if first > 0 {
		b.WriteString(s[:first])
		b.WriteByte(sep)
	}
	for i := first; i < n; i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < n {
			b.WriteByte(sep)
		}
	}
	return b.String()
}

// ---- #random ---------------------------------------------------------------

// RandomDirective outputs a random int (对照 Java RandomDirective，使用 ThreadLocalRandom)。
//
//	#random
type RandomDirective struct{}

func (d *RandomDirective) SetExprList(*ExprList) {}

func (d *RandomDirective) SetStat(Stat) {}

func (d *RandomDirective) HasEnd() bool { return false }

func (d *RandomDirective) Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {
	writer.WriteString(strconv.Itoa(rand.Int()))
}

// ---- #render ---------------------------------------------------------------

// RenderDirective dynamically renders a sub-template by name (expression),
// as a complement to #include whose path must be a string literal
// (对照 Java RenderDirective)。额外参数为赋值表达式，仅在子模板作用域生效。
//
//	#render("_hot.html")
//	#render(subFile)
//	#render("_hot.html", title = "热门新闻", list = newsList)
type RenderDirective struct {
	exprList *ExprList
}

func (d *RenderDirective) SetExprList(exprList *ExprList) {
	if exprList.Length() == 0 {
		panic("The parameter of #render directive can not be blank")
	}
	for i := 1; i < exprList.Length(); i++ {
		if _, ok := exprList.GetExpr(i).(*AssignExpr); !ok {
			panic("The parameter of #render directive must be an assignment expression")
		}
	}
	d.exprList = exprList
}

func (d *RenderDirective) SetStat(Stat) {}

func (d *RenderDirective) HasEnd() bool { return false }

func (d *RenderDirective) Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {
	if d.exprList == nil || d.exprList.Length() == 0 {
		return
	}
	nameVal := d.exprList.GetExpr(0).Eval(scope, ctrl)
	fileName, ok := nameVal.(string)
	if !ok {
		return // 对照 Java：参数值必须为 String；Go 版本宽松跳过
	}

	engine := env.GetEngine()
	if engine == nil {
		return
	}
	if !strings.HasPrefix(fileName, "/") {
		basePath := engine.config.GetBaseTemplatePath()
		if basePath != "" {
			fileName = basePath + "/" + fileName
		}
	}
	subTpl := engine.GetTemplate(fileName)
	if subTpl == nil || subTpl.ast == nil {
		return
	}

	// 赋值参数绑定到子作用域本地，不污染父作用域（对照 Java new Scope(scope) + setLocalAssignment）。
	child := NewScope(make(map[string]interface{}))
	child.parent = scope
	child.global = scope.global
	for i := 1; i < d.exprList.Length(); i++ {
		if ae, ok := d.exprList.GetExpr(i).(*AssignExpr); ok {
			child.SetLocal(ae.Name, ae.Value.Eval(scope, ctrl))
		}
	}
	subTpl.ast.Exec(env, child, writer, ctrl)
}

// ---- #string ---------------------------------------------------------------

// StringDirective captures its body into a multiline string variable
// (对照 Java StringDirective)。
//
//	#string(name)
//	   大量多行文本
//	#end
//
//	#(name)
type StringDirective struct {
	name    string
	isLocal bool
	body    Stat
}

func (d *StringDirective) SetExprList(exprList *ExprList) {
	if exprList.Length() == 0 {
		panic("#string directive parameter can not be null")
	}
	if exprList.Length() > 2 {
		panic("wrong number of #string directive parameter, two parameters allowed at most")
	}
	id, ok := exprList.GetExpr(0).(*IDExpr)
	if !ok {
		panic("#string first parameter must be identifier")
	}
	d.name = id.Name
	if exprList.Length() == 2 {
		if c, ok := exprList.GetExpr(1).(*ConstExpr); ok && c.Type == "bool" {
			d.isLocal, _ = c.Value.(bool)
		}
	}
}

func (d *StringDirective) SetStat(stat Stat) { d.body = stat }

func (d *StringDirective) HasEnd() bool { return true }

func (d *StringDirective) Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {
	var buf bytes.Buffer
	d.body.Exec(env, scope, &IOAdapter{w: &buf}, ctrl)
	s := buf.String()
	if d.isLocal {
		scope.SetLocal(d.name, s)
	} else {
		scope.Set(d.name, s)
	}
}

// ---- #call -----------------------------------------------------------------

// CallDirective dynamically calls a template function whose name is an
// expression (对照 Java CallDirective)。区别于 #@name(args) 静态糖（函数名为字面量）。
//
//	#call(funcName, p1, p2, ..., pn)
//	#call(true, funcName, p1, ..., pn)   // 首参为 true：函数不存在时跳过（nullSafe）
type CallDirective struct {
	funcExpr Expr
	nullSafe bool
	args     []Expr
}

func (d *CallDirective) SetExprList(exprList *ExprList) {
	if exprList.Length() == 0 {
		panic("Template function name required")
	}
	idx := 0
	if c, ok := exprList.GetExpr(0).(*ConstExpr); ok && c.Type == "bool" {
		if exprList.Length() == 1 {
			panic("Template function name required")
		}
		d.nullSafe, _ = c.Value.(bool)
		idx++
	}
	d.funcExpr = exprList.GetExpr(idx)
	idx++
	for ; idx < exprList.Length(); idx++ {
		d.args = append(d.args, exprList.GetExpr(idx))
	}
}

func (d *CallDirective) SetStat(Stat) {}

func (d *CallDirective) HasEnd() bool { return false }

func (d *CallDirective) Exec(env *Env, scope *Scope, writer *IOAdapter, ctrl *Ctrl) {
	name, _ := d.funcExpr.Eval(scope, ctrl).(string)
	def := env.GetFunction(name) // name 为 "" 时返回 nil
	if def == nil {
		if d.nullSafe {
			return // nullSafe（首参 true）：函数不存在跳过
		}
		// 非 nullSafe：函数不存在抛异常（对照 Java CallDirective）。
		panic(fmt.Sprintf("template function not defined: %s", name))
	}
	callDefine(env, def, d.args, scope, writer, ctrl)
}
