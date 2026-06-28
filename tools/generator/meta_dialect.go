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
