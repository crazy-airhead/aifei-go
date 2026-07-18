package enjoy

import (
	"fmt"
	"strconv"
)

// ParseExpr parses an expression string into an Expr AST.
// 独立解析（无 EngineConfig）：`::` 静态访问按伪实现处理（向后兼容 db SqlKit 等）。
func ParseExpr(input string) (Expr, error) {
	return parseExprWithConfig(input, nil)
}

// parseExprWithConfig 解析表达式；模板路径传入 EngineConfig 以启用 `::` 默认禁用检查
// （对照 Java ExprParser 构造时持有 engineConfig）。config==nil 时跳过该检查。
func parseExprWithConfig(input string, cfg *EngineConfig) (Expr, error) {
	// `::` 静态访问：Java 用 Class.forName 反射、默认关闭；Go 无运行时按全限定名反射。
	// 模板路径（cfg != nil）下默认禁用——预扫描整个表达式是否含 `::` token
	// （词法器正确跳过字符串字面量内的 "::"），覆盖 a::b 与 a.b.c::d 两种形式，
	// 消除旧伪实现（:: 当 IDExpr 的 field/method）静默失效或误命中同名变量。
	// config==nil（独立 ParseExpr）保持伪实现以向后兼容。
	if cfg != nil && !cfg.IsStaticMethodExpressionEnabled() && !cfg.IsStaticFieldExpressionEnabled() && exprContainsStatic(input) {
		return nil, fmt.Errorf("static method/field expression is not enabled (Go lacks runtime Class.forName; use AddSharedMethod instead)")
	}
	lexer := NewExprLexer(input)
	p := &exprParser{lexer: lexer}
	p.next()
	return p.parseAssign()
}

// exprContainsStatic 用 ExprLexer 扫描表达式，报告是否含 `::`(ETokStatic) token。
// 词法器会跳过字符串字面量，故 "#('a::b')" 中的 :: 不会误判。
func exprContainsStatic(input string) bool {
	l := NewExprLexer(input)
	for {
		tok, _ := l.Scan()
		if tok == ETokEOF {
			return false
		}
		if tok == ETokStatic {
			return true
		}
	}
}

type exprParser struct {
	lexer *ExprLexer
	tok   ETok
	val   string
}

func (p *exprParser) next() {
	p.tok, p.val = p.lexer.Scan()
}

func (p *exprParser) expect(tok ETok) error {
	if p.tok != tok {
		return fmt.Errorf("expected token %d, got %d (%s)", tok, p.tok, p.val)
	}
	p.next()
	return nil
}

func (p *exprParser) parseAssign() (Expr, error) {
	left, err := p.parseTernary()
	if err != nil {
		return nil, err
	}
	if p.tok == ETokAssign {
		p.next()
		// 右结合递归，支持无限连写：id = a[i=0] = a[1] = 123（对照 Java Assign）。
		right, err := p.parseAssign()
		if err != nil {
			return nil, err
		}
		switch t := left.(type) {
		case *IDExpr:
			// 普通赋值：ID = expr
			return &AssignExpr{Name: t.Name, Value: right}, nil
		case *IndexExpr:
			// 索引赋值：container[index] = expr（map[key] / list[i] / array[i]）
			return &AssignExpr{Target: t, Value: right}, nil
		default:
			return nil, fmt.Errorf("left side of assignment must be an identifier or index expression")
		}
	}
	return left, nil
}

func (p *exprParser) parseTernary() (Expr, error) {
	cond, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.tok == ETokQuestion {
		p.next()
		then, err := p.parseAssign()
		if err != nil {
			return nil, err
		}
		if err := p.expect(ETokColon); err != nil {
			return nil, err
		}
		else_, err := p.parseAssign()
		if err != nil {
			return nil, err
		}
		return &TernaryExpr{Cond: cond, Then: then, Else: else_}, nil
	}
	return cond, nil
}

func (p *exprParser) parseOr() (Expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.tok == ETokOr {
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &LogicExpr{Op: "||", Left: left, Right: right}
	}
	return left, nil
}

func (p *exprParser) parseAnd() (Expr, error) {
	left, err := p.parseEquality()
	if err != nil {
		return nil, err
	}
	for p.tok == ETokAnd {
		p.next()
		right, err := p.parseEquality()
		if err != nil {
			return nil, err
		}
		left = &LogicExpr{Op: "&&", Left: left, Right: right}
	}
	return left, nil
}

func (p *exprParser) parseEquality() (Expr, error) {
	left, err := p.parseCompare()
	if err != nil {
		return nil, err
	}
	for p.tok == ETokEq || p.tok == ETokNe {
		op := p.val
		p.next()
		right, err := p.parseCompare()
		if err != nil {
			return nil, err
		}
		left = &CompareExpr{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *exprParser) parseCompare() (Expr, error) {
	left, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	for p.tok == ETokLt || p.tok == ETokLe || p.tok == ETokGt || p.tok == ETokGe {
		op := p.val
		p.next()
		right, err := p.parseAdd()
		if err != nil {
			return nil, err
		}
		left = &CompareExpr{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *exprParser) parseAdd() (Expr, error) {
	left, err := p.parseMul()
	if err != nil {
		return nil, err
	}
	for p.tok == ETokAdd || p.tok == ETokSub {
		op := p.val
		p.next()
		right, err := p.parseMul()
		if err != nil {
			return nil, err
		}
		left = &ArithExpr{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *exprParser) parseMul() (Expr, error) {
	left, err := p.parseNullSafe()
	if err != nil {
		return nil, err
	}
	for p.tok == ETokMul || p.tok == ETokDiv || p.tok == ETokMod {
		op := p.val
		p.next()
		right, err := p.parseNullSafe()
		if err != nil {
			return nil, err
		}
		left = &ArithExpr{Op: op, Left: left, Right: right}
	}
	return left, nil
}

// parseNullSafe 解析 null 合并 `??`（对照 Java ExprParser.nullSafe）。
// 优先级位于 mulDivMod(* / %) 与 unary 之间，for 循环左结合，支持链式 a ?? b ?? c → (a??b)??c。
// 旧实现把 ?? 放在 parsePostfix（与 . / [] / () 同层、优先级过高）属语义错误，已对齐 Java。
func (p *exprParser) parseNullSafe() (Expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.tok == ETokNullCoalesce {
		p.next()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = &NullCoalesceExpr{Left: left, Right: right}
	}
	return left, nil
}

func (p *exprParser) parseUnary() (Expr, error) {
	switch p.tok {
	case ETokNot:
		p.next()
		expr, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &LogicExpr{Op: "!", Left: expr}, nil
	case ETokSub:
		p.next()
		expr, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &ArithExpr{Op: "neg", Left: expr}, nil
	case ETokInc:
		p.next()
		expr, err := p.parsePostfix()
		if err != nil {
			return nil, err
		}
		return &IncDecExpr{Name: exprName(expr), Op: "++"}, nil
	case ETokDec:
		p.next()
		expr, err := p.parsePostfix()
		if err != nil {
			return nil, err
		}
		return &IncDecExpr{Name: exprName(expr), Op: "--"}, nil
	}
	return p.parsePostfix()
}

func (p *exprParser) parsePostfix() (Expr, error) {
	expr, err := p.parseAtom()
	if err != nil {
		return nil, err
	}

	for {
		switch p.tok {
		case ETokDot:
			p.next()
			if p.tok != ETokID {
				return nil, fmt.Errorf("expected field name after '.'")
			}
			name := p.val
			p.next()
			if p.tok == ETokLParen {
				args, err := p.parseCallArgs()
				if err != nil {
					return nil, err
				}
				expr = &MethodExpr{Obj: expr, Name: name, Args: args}
			} else {
				expr = &FieldExpr{Obj: expr, Name: name}
			}
		case ETokNullSafe:
			p.next()
			if p.tok != ETokID {
				return nil, fmt.Errorf("expected field name after '?.'")
			}
			name := p.val
			p.next()
			if p.tok == ETokLParen {
				args, err := p.parseCallArgs()
				if err != nil {
					return nil, err
				}
				expr = &NullSafeExpr{Inner: &MethodExpr{Obj: expr, Name: name, Args: args}}
			} else {
				expr = &NullSafeExpr{Inner: &FieldExpr{Obj: expr, Name: name}}
			}
		case ETokLBrack:
			p.next()
			index, err := p.parseAssign()
			if err != nil {
				return nil, err
			}
			if err := p.expect(ETokRBrack); err != nil {
				return nil, err
			}
			expr = &IndexExpr{Obj: expr, Index: index}
		case ETokLParen:
			args, err := p.parseCallArgs()
			if err != nil {
				return nil, err
			}
			if id, ok := expr.(*IDExpr); ok {
				expr = &MethodExpr{Obj: nil, Name: id.Name, Args: args}
			} else {
				expr = &MethodExpr{Obj: expr, Name: "", Args: args}
			}
		case ETokInc:
			p.next()
			return &IncDecExpr{Name: exprName(expr), Op: "++"}, nil
		case ETokDec:
			p.next()
			return &IncDecExpr{Name: exprName(expr), Op: "--"}, nil
		default:
			return expr, nil
		}
	}
}

func (p *exprParser) parseAtom() (Expr, error) {
	switch p.tok {
	case ETokID:
		name := p.val
		p.next()
		if p.tok == ETokStatic {
			// `::` 静态调用（Cls::method / Cls::field）。模板路径下默认禁用的拦截已在
			// parseExprWithConfig 预扫描统一处理；此处生成 StaticMethodExpr——开启后查进程级
			// staticMethodKit 注册的全局函数（Java 静态方法的 Go 等价，不依赖实例）。
			// 有 `(` 为 method 形式带参；无 `(` 为 field 形式（无参调用注册的函数）。
			p.next()
			if p.tok != ETokID {
				return nil, fmt.Errorf("expected name after '::'")
			}
			member := p.val
			p.next()
			var args []Expr
			if p.tok == ETokLParen {
				var err error
				args, err = p.parseCallArgs()
				if err != nil {
					return nil, err
				}
			}
			return &StaticMethodExpr{Cls: name, Name: member, Args: args}, nil
		}
		return &IDExpr{Name: name}, nil
	case ETokStr:
		val := p.val
		p.next()
		return &ConstExpr{Type: "string", Value: val}, nil
	case ETokInt:
		val, _ := strconv.ParseInt(p.val, 10, 64)
		p.next()
		return &ConstExpr{Type: "int", Value: val}, nil
	case ETokFloat:
		val, _ := strconv.ParseFloat(p.val, 64)
		p.next()
		return &ConstExpr{Type: "float", Value: val}, nil
	case ETokBool:
		val := p.val == "true"
		p.next()
		return &ConstExpr{Type: "bool", Value: val}, nil
	case ETokNull:
		p.next()
		return &ConstExpr{Type: "null"}, nil
	case ETokLParen:
		p.next()
		expr, err := p.parseAssign()
		if err != nil {
			return nil, err
		}
		if err := p.expect(ETokRParen); err != nil {
			return nil, err
		}
		return expr, nil
	case ETokLBrack:
		return p.parseArrayOrRange()
	case ETokLBrace:
		return p.parseMap()
	default:
		return nil, fmt.Errorf("unexpected token: %d (%s)", p.tok, p.val)
	}
}

func (p *exprParser) parseArrayOrRange() (Expr, error) {
	p.next()
	if p.tok == ETokRBrack {
		p.next()
		return &ArrayExpr{}, nil
	}
	first, err := p.parseAssign()
	if err != nil {
		return nil, err
	}
	if p.tok == ETokRange {
		p.next()
		end, err := p.parseAssign()
		if err != nil {
			return nil, err
		}
		if err := p.expect(ETokRBrack); err != nil {
			return nil, err
		}
		return &RangeExpr{Start: first, End: end}, nil
	}
	elements := []Expr{first}
	for p.tok == ETokComma {
		p.next()
		e, err := p.parseAssign()
		if err != nil {
			return nil, err
		}
		elements = append(elements, e)
	}
	if err := p.expect(ETokRBrack); err != nil {
		return nil, err
	}
	return &ArrayExpr{Elements: elements}, nil
}

func (p *exprParser) parseMap() (Expr, error) {
	p.next()
	var pairs []MapPair
	for p.tok != ETokRBrace && p.tok != ETokEOF {
		var key Expr
		if p.tok == ETokID {
			key = &ConstExpr{Type: "string", Value: p.val}
			p.next()
		} else if p.tok == ETokStr {
			key = &ConstExpr{Type: "string", Value: p.val}
			p.next()
		} else {
			return nil, fmt.Errorf("expected map key")
		}
		if err := p.expect(ETokColon); err != nil {
			return nil, err
		}
		val, err := p.parseAssign()
		if err != nil {
			return nil, err
		}
		pairs = append(pairs, MapPair{Key: key, Value: val})
		if p.tok == ETokComma {
			p.next()
		}
	}
	if err := p.expect(ETokRBrace); err != nil {
		return nil, err
	}
	return &MapExpr{Pairs: pairs}, nil
}

func (p *exprParser) parseCallArgs() ([]Expr, error) {
	if err := p.expect(ETokLParen); err != nil {
		return nil, err
	}
	var args []Expr
	for p.tok != ETokRParen && p.tok != ETokEOF {
		arg, err := p.parseAssign()
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
		if p.tok == ETokComma {
			p.next()
		}
	}
	if err := p.expect(ETokRParen); err != nil {
		return nil, err
	}
	return args, nil
}

func exprName(e Expr) string {
	if id, ok := e.(*IDExpr); ok {
		return id.Name
	}
	return ""
}
