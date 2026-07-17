package generator

// FieldInfo holds metadata for a database column.
type FieldInfo struct {
	Name            string // database column name (snake_case)
	GoType          string // Go type, e.g. "int", "string", "time.Time"
	AttrName        string // Go exported field name (CamelCase)
	Remarks         string // column comment
	IsAutoIncrement bool   // whether the column is auto-increment
	IsJSON          bool   // whether the column is a JSON/JSONB type
	IsGenerated     bool   // whether the column is a generated/computed column

	// Derived fields, filled by EnrichFields after metadata is read.
	JSONName  string // json tag value, follows KeyFormat (camelCase by default)
	RowGetter string // db.Row getter method, e.g. "GetInt"
	Zero      string // zero-value expression, e.g. "int(0)"
	ShortName string // AttrName + "_" when it collides with a Go keyword (for short setters)
}

// TableInfo holds metadata for a database table.
type TableInfo struct {
	Name       string       // table name
	PrimaryKey []string     // primary key column names
	Remarks    string       // table comment
	IsView     bool         // whether this is a view
	Fields     []*FieldInfo // column list

	// Assigned during generation
	PkgName    string // package name, e.g. "user"
	StructName string // struct name, e.g. "User"
	BaseName   string // base struct name, e.g. "BaseUser"
}
