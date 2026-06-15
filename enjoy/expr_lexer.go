package enjoy

import "strings"

// ETok represents expression token types.
type ETok int

const (
	ETokEOF ETok = iota
	ETokID
	ETokStr
	ETokInt
	ETokFloat
	ETokBool
	ETokNull
	ETokAdd
	ETokSub
	ETokMul
	ETokDiv
	ETokMod
	ETokEq
	ETokNe
	ETokLt
	ETokLe
	ETokGt
	ETokGe
	ETokAnd
	ETokOr
	ETokNot
	ETokQuestion
	ETokColon
	ETokAssign
	ETokLParen
	ETokRParen
	ETokLBrack
	ETokRBrack
	ETokLBrace
	ETokRBrace
	ETokDot
	ETokComma
	ETokNullSafe
	ETokNullCoalesce
	ETokRange
	ETokInc
	ETokDec
	ETokStatic
)

// ExprLexer tokenizes expression strings.
type ExprLexer struct {
	input  string
	pos    int
	length int
}

// NewExprLexer creates a new ExprLexer.
func NewExprLexer(input string) *ExprLexer {
	return &ExprLexer{input: strings.TrimSpace(input), length: len(input)}
}

// Scan returns the next expression token.
func (l *ExprLexer) Scan() (ETok, string) {
	l.skipWS()
	if l.pos >= l.length {
		return ETokEOF, ""
	}
	ch := l.input[l.pos]

	switch {
	case eIsIDStart(ch):
		return l.scanEID()
	case ch == '"' || ch == '\'':
		return l.scanEString()
	case eIsDigit(ch):
		return l.scanENumber()
	}

	switch ch {
	case '+':
		if l.pos+1 < l.length && l.input[l.pos+1] == '+' {
			l.pos += 2
			return ETokInc, "++"
		}
		l.pos++
		return ETokAdd, "+"
	case '-':
		if l.pos+1 < l.length && l.input[l.pos+1] == '-' {
			l.pos += 2
			return ETokDec, "--"
		}
		l.pos++
		return ETokSub, "-"
	case '*':
		l.pos++
		return ETokMul, "*"
	case '/':
		l.pos++
		return ETokDiv, "/"
	case '%':
		l.pos++
		return ETokMod, "%"
	case '=':
		if l.pos+1 < l.length && l.input[l.pos+1] == '=' {
			l.pos += 2
			return ETokEq, "=="
		}
		l.pos++
		return ETokAssign, "="
	case '!':
		if l.pos+1 < l.length && l.input[l.pos+1] == '=' {
			l.pos += 2
			return ETokNe, "!="
		}
		l.pos++
		return ETokNot, "!"
	case '<':
		if l.pos+1 < l.length && l.input[l.pos+1] == '=' {
			l.pos += 2
			return ETokLe, "<="
		}
		l.pos++
		return ETokLt, "<"
	case '>':
		if l.pos+1 < l.length && l.input[l.pos+1] == '=' {
			l.pos += 2
			return ETokGe, ">="
		}
		l.pos++
		return ETokGt, ">"
	case '&':
		if l.pos+1 < l.length && l.input[l.pos+1] == '&' {
			l.pos += 2
			return ETokAnd, "&&"
		}
		l.pos++
		return ETokEOF, ""
	case '|':
		if l.pos+1 < l.length && l.input[l.pos+1] == '|' {
			l.pos += 2
			return ETokOr, "||"
		}
		l.pos++
		return ETokEOF, ""
	case '?':
		if l.pos+1 < l.length && l.input[l.pos+1] == '.' {
			l.pos += 2
			return ETokNullSafe, "?."
		}
		if l.pos+1 < l.length && l.input[l.pos+1] == '?' {
			l.pos += 2
			return ETokNullCoalesce, "??"
		}
		l.pos++
		return ETokQuestion, "?"
	case ':':
		if l.pos+1 < l.length && l.input[l.pos+1] == ':' {
			l.pos += 2
			return ETokStatic, "::"
		}
		l.pos++
		return ETokColon, ":"
	case '.':
		if l.pos+1 < l.length && l.input[l.pos+1] == '.' {
			l.pos += 2
			return ETokRange, ".."
		}
		l.pos++
		return ETokDot, "."
	case '(':
		l.pos++
		return ETokLParen, "("
	case ')':
		l.pos++
		return ETokRParen, ")"
	case '[':
		l.pos++
		return ETokLBrack, "["
	case ']':
		l.pos++
		return ETokRBrack, "]"
	case '{':
		l.pos++
		return ETokLBrace, "{"
	case '}':
		l.pos++
		return ETokRBrace, "}"
	case ',':
		l.pos++
		return ETokComma, ","
	}
	l.pos++
	return ETokEOF, string(ch)
}

func (l *ExprLexer) skipWS() {
	for l.pos < l.length && (l.input[l.pos] == ' ' || l.input[l.pos] == '\t' || l.input[l.pos] == '\n' || l.input[l.pos] == '\r') {
		l.pos++
	}
}

func (l *ExprLexer) scanEID() (ETok, string) {
	start := l.pos
	for l.pos < l.length && eIsIDPart(l.input[l.pos]) {
		l.pos++
	}
	val := l.input[start:l.pos]
	switch val {
	case "true", "TRUE":
		return ETokBool, "true"
	case "false", "FALSE":
		return ETokBool, "false"
	case "null", "nil":
		return ETokNull, "null"
	}
	return ETokID, val
}

func (l *ExprLexer) scanEString() (ETok, string) {
	quote := l.input[l.pos]
	l.pos++
	start := l.pos
	for l.pos < l.length && l.input[l.pos] != quote {
		if l.input[l.pos] == '\\' {
			l.pos++
		}
		l.pos++
	}
	val := l.input[start:l.pos]
	if l.pos < l.length {
		l.pos++
	}
	return ETokStr, val
}

func (l *ExprLexer) scanENumber() (ETok, string) {
	start := l.pos
	isFloat := false
	for l.pos < l.length && (eIsDigit(l.input[l.pos]) || l.input[l.pos] == '.') {
		if l.input[l.pos] == '.' {
			if l.pos+1 < l.length && l.input[l.pos+1] == '.' {
				break
			}
			isFloat = true
		}
		l.pos++
	}
	val := l.input[start:l.pos]
	if isFloat {
		return ETokFloat, val
	}
	return ETokInt, val
}

func eIsIDStart(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_' || ch == '$'
}

func eIsIDPart(ch byte) bool { return eIsIDStart(ch) || eIsDigit(ch) }
func eIsDigit(ch byte) bool  { return ch >= '0' && ch <= '9' }
