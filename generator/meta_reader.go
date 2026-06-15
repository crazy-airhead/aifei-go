package generator

import (
	"database/sql"
	"fmt"
	"strings"
)

// MetaReader reads database table metadata.
type MetaReader struct {
	TypeMapping       *TypeMapping
	FieldToAttrFn     func(string) string // snake_case → PascalCase
	ReadView          bool                // whether to read views
	ReadRemarks       bool                // whether to read column comments
	ReadAutoIncrement bool                // whether to read auto-increment flag
	whitelist         map[string]bool     // tables to include
	blacklist         map[string]bool     // tables to exclude
	tableFilter       func(string) bool   // custom include filter
	tableSkip         func(string) bool   // custom exclude filter
}

// NewMetaReader creates a MetaReader with defaults.
func NewMetaReader() *MetaReader {
	return &MetaReader{
		TypeMapping:   NewTypeMapping(),
		FieldToAttrFn: FieldToAttr,
		ReadRemarks:   true,
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
			IsView:     isView,
		}

		// 3. Read field info
		if err := mr.readFieldInfo(pool, dialect, tableInfo); err != nil {
			return nil, fmt.Errorf("read field info for %s: %w", tableName, err)
		}

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
	rows, err := pool.Query("SHOW KEYS FROM `" + table + "` WHERE Key_name = 'PRIMARY' ORDER BY Seq_in_index")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []string
	seen := make(map[string]bool)
	for rows.Next() {
		// SHOW KEYS returns many columns; we scan into a generic holder
		var (
			tableName, nonUnique, keyName, seqInIndex, columnName, collation string
			cardinality                                                      sql.NullString
			subPart, packed, nullVal                                         sql.NullString
			indexType, comment, indexComment                                 sql.NullString
		)
		if err := rows.Scan(
			&tableName, &nonUnique, &keyName, &seqInIndex, &columnName,
			&collation, &cardinality, &subPart, &packed, &nullVal,
			&indexType, &comment, &indexComment,
		); err != nil {
			return nil, err
		}
		col := strings.TrimSpace(columnName)
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
func (mr *MetaReader) readFieldInfo(pool *sql.DB, dialect MetaDialect, tableInfo *TableInfo) error {
	sqlStr := dialect.QueryTableInfo(tableInfo.Name)
	rows, err := pool.Query(sqlStr)
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

		// Handle nullable columns
		nullable, _ := ct.Nullable()
		if nullable {
			scanType := ct.ScanType()
			if scanType != nil {
				goType = mr.resolveNullableType(scanType.String(), goType)
			}
		}

		isAutoIncrement := false
		if mr.ReadAutoIncrement {
			// sql.ColumnType doesn't directly expose auto-increment;
			// for SQLite we check via PRAGMA, for MySQL via extra column info
			// This is a best-effort: check if the type name suggests auto-increment
			isAutoIncrement = strings.Contains(dbType, "AUTO_INCREMENT") ||
				strings.Contains(strings.ToLower(ct.DatabaseTypeName()), "serial")
		}

		attrName := mr.FieldToAttrFn(fieldName)

		fieldInfo := &FieldInfo{
			Name:            fieldName,
			GoType:          goType,
			AttrName:        attrName,
			IsAutoIncrement: isAutoIncrement,
		}
		tableInfo.Fields = append(tableInfo.Fields, fieldInfo)
	}

	return nil
}

// resolveNullableType converts a Go type to its nullable variant when the column is NULL-able.
func (mr *MetaReader) resolveNullableType(scanType, goType string) string {
	// For non-nullable scans, keep as-is
	// For nullable scans with primitive types, use sql.Null* wrappers
	switch goType {
	case "int":
		return "sql.NullInt64"
	case "int64":
		return "sql.NullInt64"
	case "float64":
		return "sql.NullFloat64"
	case "string":
		return "sql.NullString"
	case "bool":
		return "sql.NullBool"
	case "time.Time":
		return "sql.NullTime"
	case "[]byte":
		return "sql.NullString" // bytes stored as string
	default:
		return "sql.NullString"
	}
}
