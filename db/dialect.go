package db

import (
	"fmt"
	"strings"
)

// Dialect is the interface for database-specific SQL generation.
type Dialect interface {
	Name() string
	DefaultPrimaryKeys() []string

	// Core per-dialect methods (quoting varies)
	ForFindByID(table string, pks []string) string
	ForDeleteByID(table string, pks []string) string
	ForInsert(table string, fields []string) string
	ForUpdate(table string, fields []string, pks []string) string
	ForInsertOrUpdate(table string, fields []string, pks []string) string
	ForCountSubquery(query string) string
	ForPaginate(query string, pageNum, pageSize int) string

	// Higher-level builders (shared impl, quoting-aware)
	ForSelect(table, fields string) string
	ForDeleteBy(table, whereOrField string) string
	ForDeleteIn(table, field string, valueCount int) string
	ForFindBy(table, whereOrField string) string
	ForFindIn(table, field string, valueCount int) string
	ForCount(table string) string
	ForCountBy(table, whereOrField string) string

	// Internal quoting
	quote(name string) string
}

// NewDialect creates a Dialect based on driver name.
func NewDialect(driverName string) Dialect {
	switch driverName {
	case "mysql":
		return &MySQLDialect{}
	case "postgres", "pgx":
		return &PostgresDialect{}
	case "sqlite", "sqlite3":
		return &SQLiteDialect{}
	default:
		return &SQLiteDialect{}
	}
}

// ---- Shared helpers ----

func buildForSelect(table, fields string, quote func(string) string) string {
	return fmt.Sprintf("SELECT %s FROM %s", fields, quote(table))
}

func buildForDeleteBy(table, whereOrField string, quote func(string) string) string {
	var cond string
	if strings.Contains(whereOrField, " ") {
		cond = whereOrField
	} else {
		cond = quote(whereOrField) + " = ?"
	}
	return fmt.Sprintf("DELETE FROM %s WHERE %s", quote(table), cond)
}

func buildForDeleteIn(table, field string, valueCount int, quote func(string) string) string {
	return fmt.Sprintf("DELETE FROM %s WHERE %s IN (%s)", quote(table), quote(field), makePlaceholders(valueCount))
}

func buildForFindBy(table, whereOrField string, quote func(string) string) string {
	var cond string
	if strings.Contains(whereOrField, " ") {
		cond = whereOrField
	} else {
		cond = quote(whereOrField) + " = ?"
	}
	return fmt.Sprintf("SELECT * FROM %s WHERE %s", quote(table), cond)
}

func buildForFindIn(table, field string, valueCount int, quote func(string) string) string {
	return fmt.Sprintf("SELECT * FROM %s WHERE %s IN (%s)", quote(table), quote(field), makePlaceholders(valueCount))
}

func buildForCount(table string, quote func(string) string) string {
	return fmt.Sprintf("SELECT COUNT(*) FROM %s", quote(table))
}

func buildForCountBy(table, whereOrField string, quote func(string) string) string {
	var cond string
	if strings.Contains(whereOrField, " ") {
		cond = whereOrField
	} else {
		cond = quote(whereOrField) + " = ?"
	}
	return fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s", quote(table), cond)
}

// ---- MySQL ----

type MySQLDialect struct{}

func (d *MySQLDialect) Name() string                 { return "mysql" }
func (d *MySQLDialect) DefaultPrimaryKeys() []string { return []string{"id"} }
func (d *MySQLDialect) quote(name string) string     { return "`" + name + "`" }

func (d *MySQLDialect) ForSelect(table, fields string) string {
	return buildForSelect(table, fields, d.quote)
}
func (d *MySQLDialect) ForDeleteBy(table, whereOrField string) string {
	return buildForDeleteBy(table, whereOrField, d.quote)
}
func (d *MySQLDialect) ForDeleteIn(table, field string, valueCount int) string {
	return buildForDeleteIn(table, field, valueCount, d.quote)
}
func (d *MySQLDialect) ForFindBy(table, whereOrField string) string {
	return buildForFindBy(table, whereOrField, d.quote)
}
func (d *MySQLDialect) ForFindIn(table, field string, valueCount int) string {
	return buildForFindIn(table, field, valueCount, d.quote)
}
func (d *MySQLDialect) ForCount(table string) string {
	return buildForCount(table, d.quote)
}
func (d *MySQLDialect) ForCountBy(table, whereOrField string) string {
	return buildForCountBy(table, whereOrField, d.quote)
}
func (d *MySQLDialect) ForFindByID(table string, pks []string) string {
	where := buildPKWhere(pks, "`")
	return fmt.Sprintf("SELECT * FROM `%s` WHERE %s", table, where)
}
func (d *MySQLDialect) ForDeleteByID(table string, pks []string) string {
	where := buildPKWhere(pks, "`")
	return fmt.Sprintf("DELETE FROM `%s` WHERE %s", table, where)
}
func (d *MySQLDialect) ForInsert(table string, fields []string) string {
	quoted := quoteFields(fields, "`")
	placeholders := makePlaceholders(len(fields))
	return fmt.Sprintf("INSERT INTO `%s` (%s) VALUES (%s)", table, stringsJoin(quoted, ", "), placeholders)
}
func (d *MySQLDialect) ForUpdate(table string, fields []string, pks []string) string {
	setParts := make([]string, len(fields))
	for i, f := range fields {
		setParts[i] = fmt.Sprintf("`%s` = ?", f)
	}
	where := buildPKWhere(pks, "`")
	return fmt.Sprintf("UPDATE `%s` SET %s WHERE %s", table, stringsJoin(setParts, ", "), where)
}
func (d *MySQLDialect) ForInsertOrUpdate(table string, fields []string, pks []string) string {
	quoted := quoteFields(fields, "`")
	placeholders := makePlaceholders(len(fields))
	setParts := make([]string, 0, len(fields)-len(pks))
	pkSet := make(map[string]bool)
	for _, pk := range pks {
		pkSet[pk] = true
	}
	for _, f := range fields {
		if !pkSet[f] {
			setParts = append(setParts, fmt.Sprintf("`%s` = VALUES(`%s`)", f, f))
		}
	}
	sql := fmt.Sprintf("INSERT INTO `%s` (%s) VALUES (%s)", table, stringsJoin(quoted, ", "), placeholders)
	if len(setParts) > 0 {
		sql += " ON DUPLICATE KEY UPDATE " + stringsJoin(setParts, ", ")
	}
	return sql
}
func (d *MySQLDialect) ForCountSubquery(query string) string {
	return "SELECT COUNT(*) FROM (" + query + ") AS _cnt"
}
func (d *MySQLDialect) ForPaginate(query string, pageNum, pageSize int) string {
	offset := (pageNum - 1) * pageSize
	return fmt.Sprintf("%s LIMIT %d OFFSET %d", query, pageSize, offset)
}

// ---- PostgreSQL ----

type PostgresDialect struct{}

func (d *PostgresDialect) Name() string                 { return "postgres" }
func (d *PostgresDialect) DefaultPrimaryKeys() []string { return []string{"id"} }
func (d *PostgresDialect) quote(name string) string     { return `"` + name + `"` }

func (d *PostgresDialect) ForSelect(table, fields string) string {
	return buildForSelect(table, fields, d.quote)
}
func (d *PostgresDialect) ForDeleteBy(table, whereOrField string) string {
	return buildForDeleteBy(table, whereOrField, d.quote)
}
func (d *PostgresDialect) ForDeleteIn(table, field string, valueCount int) string {
	return buildForDeleteIn(table, field, valueCount, d.quote)
}
func (d *PostgresDialect) ForFindBy(table, whereOrField string) string {
	return buildForFindBy(table, whereOrField, d.quote)
}
func (d *PostgresDialect) ForFindIn(table, field string, valueCount int) string {
	return buildForFindIn(table, field, valueCount, d.quote)
}
func (d *PostgresDialect) ForCount(table string) string {
	return buildForCount(table, d.quote)
}
func (d *PostgresDialect) ForCountBy(table, whereOrField string) string {
	return buildForCountBy(table, whereOrField, d.quote)
}
func (d *PostgresDialect) ForFindByID(table string, pks []string) string {
	where := buildPKWhere(pks, `"`)
	return fmt.Sprintf(`SELECT * FROM "%s" WHERE %s`, table, where)
}
func (d *PostgresDialect) ForDeleteByID(table string, pks []string) string {
	where := buildPKWhere(pks, `"`)
	return fmt.Sprintf(`DELETE FROM "%s" WHERE %s`, table, where)
}
func (d *PostgresDialect) ForInsert(table string, fields []string) string {
	quoted := quoteFields(fields, `"`)
	placeholders := makePlaceholders(len(fields))
	return fmt.Sprintf(`INSERT INTO "%s" (%s) VALUES (%s)`, table, stringsJoin(quoted, ", "), placeholders)
}
func (d *PostgresDialect) ForUpdate(table string, fields []string, pks []string) string {
	setParts := make([]string, len(fields))
	for i, f := range fields {
		setParts[i] = fmt.Sprintf(`"%s" = ?`, f)
	}
	where := buildPKWhere(pks, `"`)
	return fmt.Sprintf(`UPDATE "%s" SET %s WHERE %s`, table, stringsJoin(setParts, ", "), where)
}
func (d *PostgresDialect) ForInsertOrUpdate(table string, fields []string, pks []string) string {
	quoted := quoteFields(fields, `"`)
	placeholders := makePlaceholders(len(fields))
	setParts := make([]string, 0)
	pkSet := make(map[string]bool)
	for _, pk := range pks {
		pkSet[pk] = true
	}
	for _, f := range fields {
		if !pkSet[f] {
			setParts = append(setParts, fmt.Sprintf(`"%s" = EXCLUDED."%s"`, f, f))
		}
	}
	pkWhere := quoteFields(pks, `"`)
	sql := fmt.Sprintf(`INSERT INTO "%s" (%s) VALUES (%s)`, table, stringsJoin(quoted, ", "), placeholders)
	if len(setParts) > 0 {
		sql += fmt.Sprintf(" ON CONFLICT (%s) DO UPDATE SET %s", stringsJoin(pkWhere, ", "), stringsJoin(setParts, ", "))
	}
	return sql
}
func (d *PostgresDialect) ForCountSubquery(query string) string {
	return "SELECT COUNT(*) FROM (" + query + ") AS _cnt"
}
func (d *PostgresDialect) ForPaginate(query string, pageNum, pageSize int) string {
	offset := (pageNum - 1) * pageSize
	return fmt.Sprintf("%s LIMIT %d OFFSET %d", query, pageSize, offset)
}

// ---- SQLite ----

type SQLiteDialect struct{}

func (d *SQLiteDialect) Name() string                 { return "sqlite" }
func (d *SQLiteDialect) DefaultPrimaryKeys() []string { return []string{"id"} }
func (d *SQLiteDialect) quote(name string) string     { return name }

func (d *SQLiteDialect) ForSelect(table, fields string) string {
	return buildForSelect(table, fields, d.quote)
}
func (d *SQLiteDialect) ForDeleteBy(table, whereOrField string) string {
	return buildForDeleteBy(table, whereOrField, d.quote)
}
func (d *SQLiteDialect) ForDeleteIn(table, field string, valueCount int) string {
	return buildForDeleteIn(table, field, valueCount, d.quote)
}
func (d *SQLiteDialect) ForFindBy(table, whereOrField string) string {
	return buildForFindBy(table, whereOrField, d.quote)
}
func (d *SQLiteDialect) ForFindIn(table, field string, valueCount int) string {
	return buildForFindIn(table, field, valueCount, d.quote)
}
func (d *SQLiteDialect) ForCount(table string) string {
	return buildForCount(table, d.quote)
}
func (d *SQLiteDialect) ForCountBy(table, whereOrField string) string {
	return buildForCountBy(table, whereOrField, d.quote)
}
func (d *SQLiteDialect) ForFindByID(table string, pks []string) string {
	where := buildPKWhere(pks, "")
	return fmt.Sprintf("SELECT * FROM %s WHERE %s", table, where)
}
func (d *SQLiteDialect) ForDeleteByID(table string, pks []string) string {
	where := buildPKWhere(pks, "")
	return fmt.Sprintf("DELETE FROM %s WHERE %s", table, where)
}
func (d *SQLiteDialect) ForInsert(table string, fields []string) string {
	placeholders := makePlaceholders(len(fields))
	return fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, stringsJoin(fields, ", "), placeholders)
}
func (d *SQLiteDialect) ForUpdate(table string, fields []string, pks []string) string {
	setParts := make([]string, len(fields))
	for i, f := range fields {
		setParts[i] = fmt.Sprintf("%s = ?", f)
	}
	where := buildPKWhere(pks, "")
	return fmt.Sprintf("UPDATE %s SET %s WHERE %s", table, stringsJoin(setParts, ", "), where)
}
func (d *SQLiteDialect) ForInsertOrUpdate(table string, fields []string, pks []string) string {
	placeholders := makePlaceholders(len(fields))
	return fmt.Sprintf("INSERT OR REPLACE INTO %s (%s) VALUES (%s)", table, stringsJoin(fields, ", "), placeholders)
}
func (d *SQLiteDialect) ForCountSubquery(query string) string {
	return "SELECT COUNT(*) FROM (" + query + ")"
}
func (d *SQLiteDialect) ForPaginate(query string, pageNum, pageSize int) string {
	offset := (pageNum - 1) * pageSize
	return fmt.Sprintf("%s LIMIT %d OFFSET %d", query, pageSize, offset)
}

// ---- helpers ----

func buildPKWhere(pks []string, quote string) string {
	parts := make([]string, len(pks))
	for i, pk := range pks {
		parts[i] = fmt.Sprintf("%s%s%s = ?", quote, pk, quote)
	}
	return stringsJoin(parts, " AND ")
}

func quoteFields(fields []string, quote string) []string {
	if quote == "" {
		return fields
	}
	result := make([]string, len(fields))
	for i, f := range fields {
		result[i] = quote + f + quote
	}
	return result
}

func makePlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	s := "?"
	for i := 1; i < n; i++ {
		s += ", ?"
	}
	return s
}

func stringsJoin(parts []string, sep string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += sep
		}
		result += p
	}
	return result
}
