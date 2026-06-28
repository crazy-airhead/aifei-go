package generator

// FieldInfo holds metadata for a database column.
type FieldInfo struct {
	Name            string // database column name (snake_case)
	GoType          string // Go type, e.g. "int", "string", "time.Time"
	AttrName        string // Go exported field name (CamelCase)
	Remarks         string // column comment
	IsAutoIncrement bool   // whether the column is auto-increment
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
