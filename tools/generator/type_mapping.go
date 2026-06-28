package generator

// TypeMapping maps database type names to Go type names.
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

			// JSON
			"JSON":  "string",
			"JSONB": "string",
		},
	}
}

// GetType returns the Go type for a database type name.
func (tm *TypeMapping) GetType(dbType string) string {
	if t, ok := tm.mappings[dbType]; ok {
		return t
	}
	return "string"
}

// AddMapping adds or overrides a type mapping.
func (tm *TypeMapping) AddMapping(dbType, goType string) {
	tm.mappings[dbType] = goType
}

// RemoveMapping removes a type mapping.
func (tm *TypeMapping) RemoveMapping(dbType string) {
	delete(tm.mappings, dbType)
}
