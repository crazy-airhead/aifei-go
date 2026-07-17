package generator

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/crazy-airhead/aifei-go/db"
)

// MetaDialect extends db.Dialect with metadata query methods needed by the generator.
// Defined in the generator package so db.Dialect stays generator-agnostic.
type MetaDialect interface {
	db.Dialect
	QueryTableNames(pool *sql.DB) ([]string, error)
	QueryTableInfo(table string) string
}

// ColumnMeta is column metadata read directly from the database information
// schema. Unlike database/sql driver reflection, it exposes data the driver
// cannot: column comments and generated/auto-increment flags.
type ColumnMeta struct {
	Name          string // column name
	DataType      string // database type name, upper-cased (e.g. "VARCHAR", "INT")
	Comment       string // column comment, collapsed to a single line
	Nullable      bool   // whether the column allows NULL
	Generated     bool   // whether the column is a generated/computed column
	AutoIncrement bool   // whether the column is auto-increment
}

// ColumnMetaReader is an optional capability of a MetaDialect: reading column
// metadata from the information schema. When implemented, the MetaReader uses
// it instead of database/sql driver reflection, gaining access to comments and
// generated/auto-increment flags.
type ColumnMetaReader interface {
	ReadColumns(pool *sql.DB, table string) ([]ColumnMeta, error)
}

// TableMetaReader is an optional capability: reading table comments.
type TableMetaReader interface {
	ReadTableRemarks(pool *sql.DB) (map[string]string, error)
}

// ---- MySQL ----

// MySQLMetaDialect wraps db.MySQLDialect with generator-specific metadata queries.
type MySQLMetaDialect struct {
	db.MySQLDialect
}

func (d *MySQLMetaDialect) QueryTableNames(pool *sql.DB) ([]string, error) {
	rows, err := pool.Query("SHOW TABLES")
	if err != nil {
		return nil, fmt.Errorf("mysql show tables: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, rows.Err()
}

func (d *MySQLMetaDialect) QueryTableInfo(table string) string {
	return fmt.Sprintf("SELECT * FROM `%s` LIMIT 0", table)
}

// ReadColumns reads column metadata from INFORMATION_SCHEMA in a single query.
func (d *MySQLMetaDialect) ReadColumns(pool *sql.DB, table string) ([]ColumnMeta, error) {
	const q = `SELECT COLUMN_NAME, DATA_TYPE, IS_NULLABLE, COLUMN_COMMENT, EXTRA
		FROM INFORMATION_SCHEMA.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?
		ORDER BY ORDINAL_POSITION`
	rows, err := pool.Query(q, table)
	if err != nil {
		return nil, fmt.Errorf("mysql read columns: %w", err)
	}
	defer rows.Close()

	var cols []ColumnMeta
	for rows.Next() {
		var name, dataType, isNullable, comment, extra string
		if err := rows.Scan(&name, &dataType, &isNullable, &comment, &extra); err != nil {
			return nil, err
		}
		extraUp := strings.ToUpper(extra)
		cols = append(cols, ColumnMeta{
			Name:     name,
			DataType: strings.ToUpper(dataType),
			Comment:  sanitizeComment(comment),
			Nullable: strings.EqualFold(isNullable, "YES"),
			// Only VIRTUAL/STORED generated columns are truly generated. MySQL also
			// tags DEFAULT-expression columns (e.g. DEFAULT CURRENT_TIMESTAMP, which
			// is common for gmt_create) with "DEFAULT_GENERATED" — those must NOT be
			// excluded from INSERT/UPDATE.
			Generated:     strings.Contains(extraUp, "VIRTUAL GENERATED") || strings.Contains(extraUp, "STORED GENERATED"),
			AutoIncrement: strings.Contains(extraUp, "AUTO_INCREMENT"),
		})
	}
	return cols, rows.Err()
}

// ReadTableRemarks reads TABLE_COMMENT for all base tables.
func (d *MySQLMetaDialect) ReadTableRemarks(pool *sql.DB) (map[string]string, error) {
	rows, err := pool.Query(`SELECT TABLE_NAME, TABLE_COMMENT FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_TYPE = 'BASE TABLE'`)
	if err != nil {
		return nil, fmt.Errorf("mysql read table remarks: %w", err)
	}
	defer rows.Close()

	m := make(map[string]string)
	for rows.Next() {
		var name, comment string
		if err := rows.Scan(&name, &comment); err != nil {
			return nil, err
		}
		m[name] = sanitizeComment(comment)
	}
	return m, rows.Err()
}

// ---- PostgreSQL ----

// PostgresMetaDialect wraps db.PostgresDialect.
type PostgresMetaDialect struct {
	db.PostgresDialect
}

func (d *PostgresMetaDialect) QueryTableNames(pool *sql.DB) ([]string, error) {
	rows, err := pool.Query("SELECT tablename FROM pg_catalog.pg_tables WHERE schemaname = 'public' ORDER BY tablename")
	if err != nil {
		return nil, fmt.Errorf("postgres query tables: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func (d *PostgresMetaDialect) QueryTableInfo(table string) string {
	return fmt.Sprintf(`SELECT * FROM "%s" LIMIT 0`, table)
}

// ReadColumns reads column metadata from information_schema (PostgreSQL).
// NOTE: written against the information_schema spec; not exercised against a live PG in CI.
func (d *PostgresMetaDialect) ReadColumns(pool *sql.DB, table string) ([]ColumnMeta, error) {
	const q = `SELECT c.column_name,
			c.data_type,
			c.is_nullable,
			COALESCE(col_description((c.table_schema||'.'||c.table_name)::regclass, c.ordinal_position), ''),
			c.is_generated,
			COALESCE(c.identity_generation, '')
		FROM information_schema.columns c
		WHERE c.table_schema = current_schema() AND c.table_name = $1
		ORDER BY c.ordinal_position`
	rows, err := pool.Query(q, table)
	if err != nil {
		return nil, fmt.Errorf("postgres read columns: %w", err)
	}
	defer rows.Close()

	var cols []ColumnMeta
	for rows.Next() {
		var name, dataType, isNullable, comment, isGenerated, identity string
		if err := rows.Scan(&name, &dataType, &isNullable, &comment, &isGenerated, &identity); err != nil {
			return nil, err
		}
		cols = append(cols, ColumnMeta{
			Name:          name,
			DataType:      strings.ToUpper(dataType),
			Comment:       sanitizeComment(comment),
			Nullable:      strings.EqualFold(isNullable, "YES"),
			Generated:     strings.EqualFold(isGenerated, "ALWAYS"),
			AutoIncrement: identity != "", // IDENTITY columns (plain SERIAL has none here)
		})
	}
	return cols, rows.Err()
}

// ReadTableRemarks reads table comments via obj_description (PostgreSQL).
func (d *PostgresMetaDialect) ReadTableRemarks(pool *sql.DB) (map[string]string, error) {
	rows, err := pool.Query(`SELECT c.relname, COALESCE(obj_description(c.oid), '') FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = current_schema() AND c.relkind = 'r'`)
	if err != nil {
		return nil, fmt.Errorf("postgres read table remarks: %w", err)
	}
	defer rows.Close()

	m := make(map[string]string)
	for rows.Next() {
		var name, comment string
		if err := rows.Scan(&name, &comment); err != nil {
			return nil, err
		}
		m[name] = sanitizeComment(comment)
	}
	return m, rows.Err()
}

// ---- SQLite ----

// SQLiteMetaDialect wraps db.SQLiteDialect.
type SQLiteMetaDialect struct {
	db.SQLiteDialect
}

func (d *SQLiteMetaDialect) QueryTableNames(pool *sql.DB) ([]string, error) {
	rows, err := pool.Query("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("sqlite query tables: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func (d *SQLiteMetaDialect) QueryTableInfo(table string) string {
	return fmt.Sprintf("SELECT * FROM %s LIMIT 0", table)
}

// NewMetaDialect creates the appropriate MetaDialect from a db.Dialect.
func NewMetaDialect(d db.Dialect) MetaDialect {
	switch d.Name() {
	case "mysql":
		return &MySQLMetaDialect{}
	case "postgres":
		return &PostgresMetaDialect{}
	case "sqlite":
		return &SQLiteMetaDialect{}
	default:
		return &MySQLMetaDialect{}
	}
}

// DialectName returns a human-readable name for a db.Dialect.
func DialectName(d db.Dialect) string {
	parts := strings.Split(fmt.Sprintf("%T", d), ".")
	return parts[len(parts)-1]
}

// sanitizeComment collapses a database comment into a single line so it is safe
// to emit as a Go line comment.
func sanitizeComment(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}
