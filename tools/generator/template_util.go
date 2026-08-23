package generator

import "strings"

// TemplateUtil provides helper methods available in Enjoy code-generation templates.
// Registered via engine.AddSharedObject("u", &TemplateUtil{}).
type TemplateUtil struct{}

// RowGetter returns the db.Row getter method name for a Go type.
// e.g. "int" → "GetInt", "string" → "GetStr", "time.Time" → "GetTime"
func (tu *TemplateUtil) RowGetter(goType string) string {
	switch goType {
	case "int":
		return "GetInt"
	case "int64":
		return "GetInt64"
	case "float64":
		return "GetFloat64"
	case "string":
		return "GetStr"
	case "bool":
		return "GetBool"
	case "time.Time":
		// Lenient accessor keeps generated getter signatures single-value;
		// strict error handling lives in Row.GetTime for hand-written code.
		return "GetTimeOrZero"
	case "[]byte":
		return "GetBytes"
	default:
		return "Get"
	}
}

// ZeroValue returns the zero-value expression for a Go type, used in
// reflect.TypeOf() calls.
func (tu *TemplateUtil) ZeroValue(goType string) string {
	switch goType {
	case "int":
		return "int(0)"
	case "int64":
		return "int64(0)"
	case "float64":
		return "float64(0)"
	case "string":
		return "\"\""
	case "bool":
		return "false"
	case "time.Time":
		return "time.Time{}"
	case "[]byte":
		return "[]byte{}"
	default:
		return "nil"
	}
}

// ImportPath returns the import path required for a Go type, or empty string.
func (tu *TemplateUtil) ImportPath(goType string) string {
	if goType == "time.Time" {
		return "time"
	}
	if goType == "[]byte" {
		return ""
	}
	return ""
}

// PkgName converts a table name to a Go package name.
// Removes underscores for direct concatenation: "sys_user" → "sysuser".
func (tu *TemplateUtil) PkgName(tableName string) string {
	return strings.ReplaceAll(tableName, "_", "")
}

// StructName converts a table name to a PascalCase struct name.
func (tu *TemplateUtil) StructName(tableName string) string {
	return FieldToAttr(tableName)
}

// BaseName returns the base struct name for a given struct name.
func (tu *TemplateUtil) BaseName(structName string) string {
	return "Base" + structName
}

// EscapeKeyword suffixed the string with "_" if it is a Go keyword.
func (tu *TemplateUtil) EscapeKeyword(s string) string {
	return EscapeKeyword(s)
}

// JoinNames joins string slices with a separator.
func (tu *TemplateUtil) JoinNames(names []string, sep string) string {
	result := ""
	for i, n := range names {
		if i > 0 {
			result += sep
		}
		result += n
	}
	return result
}

// Quote wraps each string in quotes and joins with a separator.
func (tu *TemplateUtil) Quote(names []string, sep string) string {
	result := ""
	for i, n := range names {
		if i > 0 {
			result += sep
		}
		result += "\"" + n + "\""
	}
	return result
}
