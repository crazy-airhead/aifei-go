package enjoy

// Lexer tokenizes template content.
type Lexer struct {
	input  string
	pos    int
	length int
}

// NewLexer creates a new template lexer.
func NewLexer(input string) *Lexer {
	return &Lexer{input: input, length: len(input)}
}

// Scan returns the next token.
func (l *Lexer) Scan() Token {
	if l.pos >= l.length {
		return Token{TokEOF, "", ""}
	}

	if l.pos < l.length && l.input[l.pos] != '#' {
		return l.scanText()
	}

	if l.pos >= l.length {
		return Token{TokEOF, "", ""}
	}

	// Comment: #-- ... --#
	if l.pos+3 < l.length && l.input[l.pos:l.pos+3] == "#--" {
		l.pos += 3
		for l.pos+2 < l.length {
			if l.input[l.pos:l.pos+3] == "--#" {
				l.pos += 3
				return l.Scan()
			}
			l.pos++
		}
		return Token{TokEOF, "", ""}
	}

	// Raw block: #[[ ... ]]#
	if l.pos+2 < l.length && l.input[l.pos:l.pos+3] == "#[[" {
		l.pos += 3
		start := l.pos
		for l.pos+2 < l.length {
			if l.input[l.pos:l.pos+3] == "]]#" {
				val := l.input[start:l.pos]
				l.pos += 3
				return Token{TokText, val, ""}
			}
			l.pos++
		}
		return Token{TokText, l.input[start:], ""}
	}

	// Single-line comment: ###
	if l.pos+2 < l.length && l.input[l.pos:l.pos+3] == "###" {
		commentPos := l.pos
		l.pos += 3
		for l.pos < l.length && l.input[l.pos] != '\n' {
			l.pos++
		}
		if l.pos < l.length && l.isAtLineStart(commentPos) {
			l.pos++
		}
		return l.Scan()
	}

	// Output expression: #(expr)
	if l.pos+1 < l.length && l.input[l.pos+1] == '(' {
		return l.scanOutput()
	}

	return l.scanDirective()
}

func (l *Lexer) scanText() Token {
	start := l.pos
	for l.pos < l.length {
		if l.input[l.pos] == '#' && l.pos+1 < l.length {
			next := l.input[l.pos+1]
			if next == '(' || next == '-' || next == '[' || next == '#' ||
				(next >= 'a' && next <= 'z') || (next >= 'A' && next <= 'Z') || next == '_' || next == '@' {
				break
			}
		}
		l.pos++
	}
	return Token{TokText, l.input[start:l.pos], ""}
}

func (l *Lexer) scanOutput() Token {
	l.pos += 2
	depth := 1
	start := l.pos
	for l.pos < l.length && depth > 0 {
		ch := l.input[l.pos]
		if ch == '(' {
			depth++
		} else if ch == ')' {
			depth--
			if depth == 0 {
				break
			}
		} else if ch == '"' || ch == '\'' {
			quote := ch
			l.pos++
			for l.pos < l.length && l.input[l.pos] != quote {
				if l.input[l.pos] == '\\' {
					l.pos++
				}
				l.pos++
			}
		}
		l.pos++
	}
	val := l.input[start:l.pos]
	if l.pos < l.length {
		l.pos++
	}
	return Token{TokOutput, val, ""}
}

func (l *Lexer) scanDirective() Token {
	hashPos := l.pos
	l.pos++

	if l.pos < l.length && l.input[l.pos] == '@' {
		return l.scanAtCall(hashPos)
	}

	start := l.pos
	for l.pos < l.length && isDirectiveChar(l.input[l.pos]) {
		l.pos++
	}
	name := l.input[start:l.pos]
	tokType := l.mapDirective(name)

	para := ""
	if isParameterLessDirective(tokType) {
		// 无参指令（#else/#end/#break/#continue/#default）：不把尾部文本当作参数消费
		// （解析器从不读取其 para，原先会吞掉 #else<文本> 的尾部文本，导致分支体丢失）。
		// 行首（独占一行）时顺带吃掉尾随水平空白与换行，避免输出多余空行；
		// 行内时保留尾部文本，交由后续 TokText 输出。
		if l.isAtLineStart(hashPos) {
			for l.pos < l.length && (l.input[l.pos] == ' ' || l.input[l.pos] == '\t') {
				l.pos++
			}
			if l.pos < l.length && l.input[l.pos] == '\n' {
				l.pos++
			} else if l.pos+1 < l.length && l.input[l.pos] == '\r' && l.input[l.pos+1] == '\n' {
				l.pos += 2
			}
		}
	} else {
		// Skip whitespace between directive name and parameter.
		// e.g. #for (f : fields) → skip the space, then enter paren path.
		for l.pos < l.length && l.input[l.pos] == ' ' {
			l.pos++
		}
		if l.pos < l.length && l.input[l.pos] == '(' {
			l.pos++
			depth := 1
			pstart := l.pos
			for l.pos < l.length && depth > 0 {
				if l.input[l.pos] == '(' {
					depth++
				} else if l.input[l.pos] == ')' {
					depth--
					if depth == 0 {
						break
					}
				}
				l.pos++
			}
			para = l.input[pstart:l.pos]
			if l.pos < l.length {
				l.pos++
			}
		} else {
			pstart := l.pos
			for l.pos < l.length && l.input[l.pos] != '\n' && l.input[l.pos] != '#' {
				l.pos++
			}
			para = trimRight(l.input[pstart:l.pos])
		}
	}

	// Consume trailing newline if the directive starts at the beginning of its line
	// (only whitespace between the last \n and #). This prevents blank lines in output.
	if l.pos < l.length && l.isAtLineStart(hashPos) {
		if l.input[l.pos] == '\n' {
			l.pos++
		} else if l.pos+1 < l.length && l.input[l.pos] == '\r' && l.input[l.pos+1] == '\n' {
			l.pos += 2
		}
	}

	return Token{tokType, para, name}
}

// isParameterLessDirective 报告该指令是否不接受任何参数。
// 这类指令（#else/#end/#break/#continue/#default）不得消费其后的行内文本，
// 否则会把 #else<文本> 的尾部文本吞进 para（解析器从不读取它）从而丢失分支体。
func isParameterLessDirective(t TokType) bool {
	switch t {
	case TokElse, TokEnd, TokBreak, TokContinue, TokDefault:
		return true
	}
	return false
}

// isAtLineStart checks if position pos is at the start of a line
// (only whitespace between the last \n and pos, or pos is at start of input).
func (l *Lexer) isAtLineStart(pos int) bool {
	for i := pos - 1; i >= 0; i-- {
		ch := l.input[i]
		if ch == '\n' {
			return true
		}
		if ch != ' ' && ch != '\t' {
			return false
		}
	}
	return true
}

func (l *Lexer) mapDirective(name string) TokType {
	switch name {
	case "if":
		return TokIf
	case "elseif", "elif":
		return TokElseIf
	case "else":
		return TokElse
	case "end":
		return TokEnd
	case "for":
		return TokFor
	case "set":
		return TokSet
	case "setLocal":
		return TokSetLocal
	case "setGlobal":
		return TokSetGlobal
	case "define":
		return TokDefine
	case "include":
		return TokInclude
	case "switch":
		return TokSwitch
	case "case":
		return TokCase
	case "default":
		return TokDefault
	case "break":
		return TokBreak
	case "continue":
		return TokContinue
	case "return":
		return TokReturn
	case "returnIf":
		return TokReturnIf
	default:
		return TokID
	}
}

func isDirectiveChar(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
}

// scanAtCall scans the #@id(p) / #@id?(p) static call sugar (对照 Java Lexer
// state 20)。它推进 l.pos 越过 '@'、函数名、可选 '?' 与参数括号，避免被当作文本重扫
// （修复旧实现只前进一步导致 #@ 内容被重扫的缺陷）。函数名通过 Token.Name 传递。
func (l *Lexer) scanAtCall(hashPos int) Token {
	l.pos++ // past '@'
	l.skipBlanks()

	idStart := l.pos
	for l.pos < l.length && isIdentChar(l.input[l.pos]) {
		l.pos++
	}
	id := l.input[idStart:l.pos]

	l.skipBlanks()
	tokType := TokCall
	if l.pos < l.length && l.input[l.pos] == '?' { // #@id?(p) callIfDefined
		tokType = TokCallIfDefined
		l.pos++
		l.skipBlanks()
	}

	para := ""
	if l.pos < l.length && l.input[l.pos] == '(' {
		l.pos++ // past '('
		depth := 1
		pstart := l.pos
		for l.pos < l.length && depth > 0 {
			if l.input[l.pos] == '(' {
				depth++
			} else if l.input[l.pos] == ')' {
				depth--
				if depth == 0 {
					break
				}
			}
			l.pos++
		}
		para = l.input[pstart:l.pos]
		if l.pos < l.length { // skip closing ')'
			l.pos++
		}
	}

	// Consume trailing newline when the directive starts its line.
	if l.pos < l.length && l.isAtLineStart(hashPos) {
		if l.input[l.pos] == '\n' {
			l.pos++
		} else if l.pos+1 < l.length && l.input[l.pos] == '\r' && l.input[l.pos+1] == '\n' {
			l.pos += 2
		}
	}

	return Token{tokType, para, id}
}

func (l *Lexer) skipBlanks() {
	for l.pos < l.length && (l.input[l.pos] == ' ' || l.input[l.pos] == '\t') {
		l.pos++
	}
}

func isIdentStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func isIdentChar(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

func trimRight(s string) string {
	i := len(s) - 1
	for i >= 0 && (s[i] == ' ' || s[i] == '\t' || s[i] == '\r' || s[i] == '\n') {
		i--
	}
	return s[:i+1]
}
