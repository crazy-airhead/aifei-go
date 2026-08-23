package generator

import "strings"

// TypeMapping maps database type names to Go type names. Lookups are
// normalized first (see NormalizeDataType), so aliases like
// "character varying(255)" hit the VARCHAR entry and user-added mappings
// should use the canonical upper-case name.
type TypeMapping struct {
	mappings map[string]string
}

// NewTypeMapping creates a TypeMapping with default mappings.
func NewTypeMapping() *TypeMapping {
	return &TypeMapping{
		mappings: map[string]string{
			// Integers
			"INT":       "int",
			"INTEGER":   "int",
			"TINYINT":   "int",
			"SMALLINT":  "int",
			"MEDIUMINT": "int",
			"BIGINT":    "int64",
			"SERIAL":    "int64",
			"BIGSERIAL": "int64",

			// Floats
			"FLOAT":  "float64",
			"DOUBLE": "float64",
			"REAL":   "float64",

			// Decimal
			"DECIMAL": "string",
			"NUMERIC": "string",

			// Strings
			"VARCHAR":    "string",
			"CHAR":       "string",
			"TEXT":       "string",
			"LONGTEXT":   "string",
			"MEDIUMTEXT": "string",
			"TINYTEXT":   "string",
			"ENUM":       "string",
			"SET":        "string",
			"UUID":       "string",
			"CLOB":       "string",
			"NCLOB":      "string",

			// Time
			"DATE":      "time.Time",
			"DATETIME":  "time.Time",
			"TIMESTAMP": "time.Time",
			"TIME":      "time.Time",

			// Boolean
			"BOOL":    "bool",
			"BOOLEAN": "bool",
			"BIT":     "bool",

			// Binary
			"BLOB":       "[]byte",
			"LONGBLOB":   "[]byte",
			"MEDIUMBLOB": "[]byte",
			"TINYBLOB":   "[]byte",
			"VARBINARY":  "[]byte",
			"BINARY":     "[]byte",
			"BYTEA":      "[]byte",

			// JSON
			"JSON":  "string",
			"JSONB": "string",
		},
	}
}

// NormalizeDataType canonicalizes a database type name: strips the
// length/precision suffix ("VARCHAR(255)"→"VARCHAR"), upper-cases, and folds
// vendor/spelled-out aliases to their canonical SQL name ("character varying"
// →"VARCHAR", "timestamp with time zone"→"TIMESTAMP", PG "int4"→"INTEGER").
func NormalizeDataType(dbType string) string {
	t := strings.TrimSpace(dbType)
	if i := strings.IndexByte(t, '('); i >= 0 {
		t = t[:i]
	}
	t = strings.ToUpper(strings.TrimSpace(t))
	switch t {
	case "CHARACTER VARYING", "VARCHAR2", "NVARCHAR", "NVARCHAR2":
		return "VARCHAR"
	case "CHARACTER", "NCHAR", "BPCHAR":
		return "CHAR"
	case "DOUBLE PRECISION", "FLOAT8":
		return "DOUBLE"
	case "FLOAT4":
		return "FLOAT"
	case "TIMESTAMP WITH TIME ZONE", "TIMESTAMP WITHOUT TIME ZONE", "TIMESTAMPTZ":
		return "TIMESTAMP"
	case "TIME WITH TIME ZONE", "TIME WITHOUT TIME ZONE", "TIMETZ":
		return "TIME"
	case "INT2":
		return "SMALLINT"
	case "INT4":
		return "INTEGER"
	case "INT8":
		return "BIGINT"
	}
	return t
}

// GetType returns the Go type for a database type name, falling back to
// "string" when the (normalized) name has no mapping.
func (tm *TypeMapping) GetType(dbType string) string {
	if t, ok := tm.Lookup(dbType); ok {
		return t
	}
	return "string"
}

// Lookup returns the Go type for a database type name and reports whether a
// mapping matched (after normalization).
func (tm *TypeMapping) Lookup(dbType string) (string, bool) {
	t, ok := tm.mappings[NormalizeDataType(dbType)]
	return t, ok
}

// AddMapping adds or overrides a type mapping. Use the canonical upper-case
// name (see NormalizeDataType) so lookups with aliases and length suffixes
// still hit the entry.
func (tm *TypeMapping) AddMapping(dbType, goType string) {
	tm.mappings[NormalizeDataType(dbType)] = goType
}

// RemoveMapping removes a type mapping.
func (tm *TypeMapping) RemoveMapping(dbType string) {
	delete(tm.mappings, NormalizeDataType(dbType))
}
