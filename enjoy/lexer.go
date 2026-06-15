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
		l.pos += 3
		for l.pos < l.length && l.input[l.pos] != '\n' {
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
	l.pos++

	if l.pos < l.length && l.input[l.pos] == '@' {
		l.pos++
		return Token{TokCall, trimRight(l.input[l.pos:]), ""}
	}

	start := l.pos
	for l.pos < l.length && isDirectiveChar(l.input[l.pos]) {
		l.pos++
	}
	name := l.input[start:l.pos]

	para := ""
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

	tokType := l.mapDirective(name)
	return Token{tokType, para, name}
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
	case "call":
		return TokCall
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

func trimRight(s string) string {
	i := len(s) - 1
	for i >= 0 && (s[i] == ' ' || s[i] == '\t' || s[i] == '\r' || s[i] == '\n') {
		i--
	}
	return s[:i+1]
}
