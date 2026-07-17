package generator

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/crazy-airhead/aifei-go/db"
)

// MetaReader reads database table metadata.
type MetaReader struct {
	TypeMapping       *TypeMapping
	FieldToAttrFn     func(string) string // snake_case → PascalCase
	ReadView          bool                // whether to read views
	ReadRemarks       bool                // whether to read column/table comments
	ReadAutoIncrement bool                // whether to read auto-increment flag
	// ResolveNullable, when true, maps NULL-able columns to sql.Null* Go types.
	// Defaults to false: plain Go types are emitted and NULLs are handled by
	// db.Row at access time. Enable when generated code must distinguish NULL
	// from the zero value at the type level.
	ResolveNullable bool
	// KeyFormat controls the derived JSONName: KeyFormatCamel (default) emits
	// camelCase json tags; KeyFormatSnake emits the raw column name. Match it to
	// the runtime db.DefaultKeyFormat so generated tags agree with db.Row JSON
	// serialization.
	KeyFormat   db.KeyFormat
	whitelist   map[string]bool   // tables to include
	blacklist   map[string]bool   // tables to exclude
	tableFilter func(string) bool // custom include filter
	tableSkip   func(string) bool // custom exclude filter
}

// NewMetaReader creates a MetaReader with defaults.
func NewMetaReader() *MetaReader {
	return &MetaReader{
		TypeMapping:   NewTypeMapping(),
		FieldToAttrFn: FieldToAttr,
		ReadRemarks:   true,
		KeyFormat:     db.KeyFormatCamel,
	}
}

// AddWhitelist adds tables to the whitelist. If whitelist is non-empty,
// only whitelisted tables are processed.
func (mr *MetaReader) AddWhitelist(tables ...string) *MetaReader {
	if mr.whitelist == nil {
		mr.whitelist = make(map[string]bool)
	}
	for _, t := range tables {
		t = strings.TrimSpace(t)
		if mr.blacklist != nil && mr.blacklist[t] {
			panic("table in blacklist cannot be added to whitelist: " + t)
		}
		mr.whitelist[strings.ToLower(t)] = true
	}
	return mr
}

// AddBlacklist adds tables to exclude from generation.
func (mr *MetaReader) AddBlacklist(tables ...string) *MetaReader {
	if mr.blacklist == nil {
		mr.blacklist = make(map[string]bool)
	}
	for _, t := range tables {
		t = strings.TrimSpace(t)
		if mr.whitelist != nil && mr.whitelist[t] {
			panic("table in whitelist cannot be added to blacklist: " + t)
		}
		mr.blacklist[strings.ToLower(t)] = true
	}
	return mr
}

// SetFilter sets a custom table filter. Only tables for which fn returns true are processed.
func (mr *MetaReader) SetFilter(fn func(string) bool) *MetaReader {
	mr.tableFilter = fn
	return mr
}

// SetSkip sets a custom table skip function. Tables for which fn returns true are skipped.
func (mr *MetaReader) SetSkip(fn func(string) bool) *MetaReader {
	mr.tableSkip = fn
	return mr
}

// shouldProcess determines if a table should be processed based on
// whitelist > filter > blacklist > skip priority.
func (mr *MetaReader) shouldProcess(table string) bool {
	lower := strings.ToLower(table)

	if len(mr.whitelist) > 0 {
		return mr.whitelist[lower]
	}
	if mr.tableFilter != nil {
		return mr.tableFilter(table)
	}
	if mr.blacklist != nil && mr.blacklist[lower] {
		return false
	}
	if mr.tableSkip != nil && mr.tableSkip(table) {
		return false
	}
	return true
}

// Read reads metadata for all tables in the database.
func (mr *MetaReader) Read(pool *sql.DB, dialect MetaDialect) ([]*TableInfo, error) {
	// 1. Get table names
	tableNames, err := dialect.QueryTableNames(pool)
	if err != nil {
		return nil, fmt.Errorf("query table names: %w", err)
	}

	// Prefetch table comments when the dialect exposes them.
	tableRemarks := map[string]string{}
	if mr.ReadRemarks {
		if tr, ok := dialect.(TableMetaReader); ok {
			tableRemarks, err = tr.ReadTableRemarks(pool)
			if err != nil {
				return nil, fmt.Errorf("read table remarks: %w", err)
			}
		}
	}

	var tableInfos []*TableInfo

	for _, tableName := range tableNames {
		if !mr.shouldProcess(tableName) {
			fmt.Printf("[aifei-gen] Skip table: %s\n", tableName)
			continue
		}

		// 2. Read primary keys
		primaryKeys, err := mr.readPrimaryKeys(pool, dialect, tableName)
		if err != nil {
			return nil, fmt.Errorf("read primary keys for %s: %w", tableName, err)
		}

		// Skip tables without primary keys (views can use fake_id)
		if len(primaryKeys) == 0 {
			if mr.ReadView {
				primaryKeys = []string{"fake_id"}
				fmt.Printf("[aifei-gen] Set primaryKey \"fake_id\" for view %s\n", tableName)
			} else {
				fmt.Printf("[aifei-gen] Skip table %s: no primary key\n", tableName)
				continue
			}
		}

		isView := strings.EqualFold(tableName, "view") // simplified; real detection needs dialect-specific logic

		tableInfo := &TableInfo{
			Name:       tableName,
			PrimaryKey: primaryKeys,
			Remarks:    tableRemarks[tableName],
			IsView:     isView,
		}

		// 3. Read field info
		if err := mr.readFieldInfo(pool, dialect, tableInfo); err != nil {
			return nil, fmt.Errorf("read field info for %s: %w", tableName, err)
		}
		// 4. Enrich derived fields (json tag, row getter, zero value).
		mr.enrich(tableInfo)

		tableInfos = append(tableInfos, tableInfo)
	}

	return tableInfos, nil
}

// readPrimaryKeys reads the primary key columns for a table.
func (mr *MetaReader) readPrimaryKeys(pool *sql.DB, dialect MetaDialect, table string) ([]string, error) {
	switch dialect.Name() {
	case "mysql":
		return mr.readPrimaryKeysMySQL(pool, table)
	case "postgres":
		return mr.readPrimaryKeysPostgres(pool, table)
	case "sqlite":
		return mr.readPrimaryKeysSQLite(pool, table)
	default:
		return mr.readPrimaryKeysSQLite(pool, table)
	}
}

func (mr *MetaReader) readPrimaryKeysMySQL(pool *sql.DB, table string) ([]string, error) {
	// INFORMATION_SCHEMA.STATISTICS is preferred over "SHOW KEYS": SHOW does not
	// accept ORDER BY (it raises ER_PARSE_ERROR), and STATISTICS gives a stable,
	// ordered result for composite primary keys.
	rows, err := pool.Query(`SELECT COLUMN_NAME FROM INFORMATION_SCHEMA.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND INDEX_NAME = 'PRIMARY' ORDER BY SEQ_IN_INDEX`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []string
	seen := make(map[string]bool)
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			return nil, err
		}
		col = strings.TrimSpace(col)
		if col != "" && !seen[col] {
			keys = append(keys, col)
			seen[col] = true
		}
	}
	return keys, rows.Err()
}

func (mr *MetaReader) readPrimaryKeysPostgres(pool *sql.DB, table string) ([]string, error) {
	query := `
		SELECT a.attname
		FROM pg_index i
		JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
		WHERE i.indrelid = $1::regclass
		AND i.indisprimary
		ORDER BY array_position(i.indkey, a.attnum)
	`
	rows, err := pool.Query(query, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			return nil, err
		}
		keys = append(keys, strings.TrimSpace(col))
	}
	return keys, rows.Err()
}

func (mr *MetaReader) readPrimaryKeysSQLite(pool *sql.DB, table string) ([]string, error) {
	rows, err := pool.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var defaultVal sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultVal, &pk); err != nil {
			return nil, err
		}
		if pk > 0 {
			keys = append(keys, strings.TrimSpace(name))
		}
	}
	return keys, rows.Err()
}

// readFieldInfo reads column metadata for a table.
//
// Dialects implementing ColumnMetaReader (MySQL, PostgreSQL) supply comments,
// generated/auto-increment flags, nullability and type names directly from the
// information schema. Others (e.g. SQLite) fall back to database/sql driver
// reflection, which cannot expose comments or generated flags.
func (mr *MetaReader) readFieldInfo(pool *sql.DB, dialect MetaDialect, tableInfo *TableInfo) error {
	if cr, ok := dialect.(ColumnMetaReader); ok {
		return mr.readFieldsFromMeta(pool, cr, tableInfo)
	}
	return mr.readFieldsFromDriver(pool, dialect, tableInfo)
}

// readFieldsFromMeta reads columns via the dialect's information-schema query,
// which exposes comments and generated/auto-increment flags the driver cannot.
func (mr *MetaReader) readFieldsFromMeta(pool *sql.DB, cr ColumnMetaReader, tableInfo *TableInfo) error {
	cols, err := cr.ReadColumns(pool, tableInfo.Name)
	if err != nil {
		return fmt.Errorf("read columns for %s: %w", tableInfo.Name, err)
	}
	for _, c := range cols {
		goType := mr.TypeMapping.GetType(c.DataType)
		if c.Nullable && mr.ResolveNullable {
			goType = mr.resolveNullableType(goType)
		}
		remarks := c.Comment
		if !mr.ReadRemarks {
			remarks = ""
		}
		tableInfo.Fields = append(tableInfo.Fields, &FieldInfo{
			Name:            c.Name,
			GoType:          goType,
			AttrName:        mr.FieldToAttrFn(c.Name),
			Remarks:         remarks,
			IsAutoIncrement: c.AutoIncrement,
			IsJSON:          c.DataType == "JSON" || c.DataType == "JSONB",
			IsGenerated:     c.Generated,
		})
	}
	return nil
}

// readFieldsFromDriver falls back to database/sql driver reflection (SQLite and
// any dialect without a ColumnMetaReader). Comments and generated flags are not
// available through the driver.
func (mr *MetaReader) readFieldsFromDriver(pool *sql.DB, dialect MetaDialect, tableInfo *TableInfo) error {
	rows, err := pool.Query(dialect.QueryTableInfo(tableInfo.Name))
	if err != nil {
		return fmt.Errorf("query table info: %w", err)
	}
	defer rows.Close()

	columnTypes, err := rows.ColumnTypes()
	if err != nil {
		return fmt.Errorf("column types: %w", err)
	}

	for _, ct := range columnTypes {
		fieldName := ct.Name()
		dbType := strings.ToUpper(ct.DatabaseTypeName())
		goType := mr.TypeMapping.GetType(dbType)

		// NULL-able columns map to sql.Null* only when explicitly enabled.
		if mr.ResolveNullable {
			if nullable, _ := ct.Nullable(); nullable {
				goType = mr.resolveNullableType(goType)
			}
		}

		// sql.ColumnType does not expose auto-increment; this is a best-effort
		// heuristic on the type name (e.g. PostgreSQL SERIAL).
		isAutoIncrement := false
		if mr.ReadAutoIncrement {
			lower := strings.ToLower(ct.DatabaseTypeName())
			isAutoIncrement = strings.Contains(lower, "auto_increment") ||
				strings.Contains(lower, "serial")
		}

		tableInfo.Fields = append(tableInfo.Fields, &FieldInfo{
			Name:            fieldName,
			GoType:          goType,
			AttrName:        mr.FieldToAttrFn(fieldName),
			IsAutoIncrement: isAutoIncrement,
			IsJSON:          dbType == "JSON" || dbType == "JSONB",
		})
	}
	return nil
}

// resolveNullableType maps a Go type to its sql.Null* variant for NULL-able
// columns when ResolveNullable is enabled.
func (mr *MetaReader) resolveNullableType(goType string) string {
	switch goType {
	case "int", "int64":
		return "sql.NullInt64"
	case "float64":
		return "sql.NullFloat64"
	case "string", "[]byte":
		return "sql.NullString"
	case "bool":
		return "sql.NullBool"
	case "time.Time":
		return "sql.NullTime"
	default:
		return "sql.NullString"
	}
}

// enrich fills the derived fields on each FieldInfo using the MetaReader's
// KeyFormat and the shared TemplateUtil.
func (mr *MetaReader) enrich(t *TableInfo) {
	EnrichFields(t.Fields, &TemplateUtil{}, mr.KeyFormat)
}

// EnrichFields populates the derived fields (JSONName, RowGetter, Zero) on each
// FieldInfo from its metadata. The MetaReader calls it automatically after
// reading; call it by hand when constructing FieldInfo directly.
func EnrichFields(fields []*FieldInfo, util *TemplateUtil, kf db.KeyFormat) {
	for _, f := range fields {
		f.RowGetter = util.RowGetter(f.GoType)
		f.Zero = util.ZeroValue(f.GoType)
		f.ShortName = EscapeKeyword(f.AttrName)
		switch kf {
		case db.KeyFormatSnake:
			f.JSONName = f.Name
		default: // KeyFormatCamel
			f.JSONName = ToCamelCase(f.AttrName)
		}
	}
}
