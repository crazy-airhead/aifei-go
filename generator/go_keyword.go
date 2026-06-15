package generator

// IsGoKeyword checks if a string is a Go reserved keyword.
func IsGoKeyword(s string) bool {
	return goKeywords[s]
}

// EscapeKeyword returns the string with a trailing underscore if it is a Go keyword.
func EscapeKeyword(s string) string {
	if IsGoKeyword(s) {
		return s + "_"
	}
	return s
}

var goKeywords = map[string]bool{
	"break": true, "default": true, "func": true, "interface": true, "select": true,
	"case": true, "defer": true, "go": true, "map": true, "struct": true,
	"chan": true, "else": true, "goto": true, "package": true, "switch": true,
	"const": true, "fallthrough": true, "if": true, "range": true, "type": true,
	"continue": true, "for": true, "import": true, "return": true, "var": true,
}
