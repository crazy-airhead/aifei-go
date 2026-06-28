package generator

import "unicode"

// FieldToAttr converts a database column name to a Go exported field name.
// Default implementation: snake_case → PascalCase.
func FieldToAttr(fieldName string) string {
	return toPascalCase(fieldName)
}

// ToCamelCase converts a PascalCase string to camelCase by lowercasing the first rune.
func ToCamelCase(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

// toPascalCase converts snake_case to PascalCase (exported Go identifier).
func toPascalCase(s string) string {
	result := make([]rune, 0, len(s))
	nextUpper := true

	for _, r := range s {
		if r == '_' {
			nextUpper = true
			continue
		}
		if nextUpper {
			result = append(result, unicode.ToUpper(r))
			nextUpper = false
		} else {
			result = append(result, r)
		}
	}

	if len(result) == 0 {
		return s
	}
	return string(result)
}
