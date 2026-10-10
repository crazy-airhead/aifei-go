// meta.go 表元数据读取（Java 走 JDBC DatabaseMetaData 的
// getTables/getColumns/getPrimaryKeys/getImportedKeys；Go database/sql 无此
// 面，按方言族自实现：SQLite 走 PRAGMA、MySQL/PostgreSQL 走各自
// information_schema/pg_catalog、其余 Generic 走 information_schema 尽力而
// 为）。列注释/表备注仅 MySQL/PG 可得（SQLite PRAGMA 无注释面，Java JDBC
// 在 SQLite 上同样拿不到）。
package text2sql

import (
	"database/sql"
	"fmt"
	"strings"
)

// columnMeta 列元数据（JDBC getColumns 行的对应物）。
type columnMeta struct {
	name     string // COLUMN_NAME
	typeName string // TYPE_NAME（SQLite 为声明类型，含长度）
	size     int    // COLUMN_SIZE（0 或超 16M 不展示——Java 同款闸）
	nullable bool   // IS_NULLABLE
	remark   string // REMARKS（列注释）
}

// tableMeta 单表元数据：备注 + 列 + 主键 + 外键关系线索。
type tableMeta struct {
	remark    string
	columns   []*columnMeta
	pks       []string
	relations []string // "table.fk -> pkTable.pkCol"（Java getRelation 同款格式）
}

// metaReader 读单表元数据（错误面收口在调用方）。
type metaReader func(pool *sql.DB, table string) (*tableMeta, error)

// metaReaderFor 按方言值类型选读取器（H2 MySQL 模式适配后自然命中 MySQL
// 读取器）。
func metaReaderFor(d SqlDialect) metaReader {
	switch d.(type) {
	case SQLiteDialect:
		return readSQLiteMeta
	case MySQLDialect:
		return readMySQLMeta
	case PostgreDialect:
		return readPostgreMeta
	default:
		return readGenericMeta
	}
}

// quotePragma PRAGMA 的表名参数（双引号转义内嵌引号）。
func quotePragma(table string) string {
	return strings.ReplaceAll(table, `"`, `""`)
}

// readSQLiteMeta PRAGMA table_info + foreign_key_list。
func readSQLiteMeta(pool *sql.DB, table string) (*tableMeta, error) {
	m := &tableMeta{}

	rows, err := pool.Query(fmt.Sprintf(`PRAGMA table_info("%s")`, quotePragma(table)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			return nil, err
		}
		m.columns = append(m.columns, &columnMeta{
			name: name, typeName: ctype, nullable: notNull == 0,
		})
		if pk > 0 {
			m.pks = append(m.pks, name)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	fkRows, err := pool.Query(fmt.Sprintf(`PRAGMA foreign_key_list("%s")`, quotePragma(table)))
	if err != nil {
		return m, nil // 外键探测失败不阻断（尽力而为）
	}
	defer fkRows.Close()
	for fkRows.Next() {
		var id, seq int
		var pkTable, from, to string
		var onUpdate, onDelete, match string
		if err := fkRows.Scan(&id, &seq, &pkTable, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			return m, nil
		}
		m.relations = append(m.relations, table+"."+from+" -> "+pkTable+"."+to)
	}
	return m, fkRows.Err()
}

// readMySQLMeta information_schema 四查询（表注释/列/主键/外键）。
func readMySQLMeta(pool *sql.DB, table string) (*tableMeta, error) {
	m := &tableMeta{}

	if err := pool.QueryRow(
		`SELECT TABLE_COMMENT FROM INFORMATION_SCHEMA.TABLES
		 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?`, table,
	).Scan(&m.remark); err != nil && err != sql.ErrNoRows {
		return nil, err
	}

	rows, err := pool.Query(
		`SELECT COLUMN_NAME, DATA_TYPE, IFNULL(CHARACTER_MAXIMUM_LENGTH, IFNULL(NUMERIC_PRECISION, 0)), IS_NULLABLE, COLUMN_COMMENT
		 FROM INFORMATION_SCHEMA.COLUMNS
		 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?
		 ORDER BY ORDINAL_POSITION`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		c := &columnMeta{}
		var nullable string
		if err := rows.Scan(&c.name, &c.typeName, &c.size, &nullable, &c.remark); err != nil {
			return nil, err
		}
		c.nullable = nullable != "NO"
		m.columns = append(m.columns, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	pkRows, err := pool.Query(
		`SELECT COLUMN_NAME FROM INFORMATION_SCHEMA.STATISTICS
		 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND INDEX_NAME = 'PRIMARY'
		 ORDER BY SEQ_IN_INDEX`, table)
	if err == nil {
		defer pkRows.Close()
		for pkRows.Next() {
			var col string
			if err := pkRows.Scan(&col); err != nil {
				break
			}
			m.pks = append(m.pks, strings.TrimSpace(col))
		}
	}

	fkRows, err := pool.Query(
		`SELECT COLUMN_NAME, REFERENCED_TABLE_NAME, REFERENCED_COLUMN_NAME
		 FROM INFORMATION_SCHEMA.KEY_COLUMN_USAGE
		 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND REFERENCED_TABLE_NAME IS NOT NULL
		 ORDER BY ORDINAL_POSITION`, table)
	if err == nil {
		defer fkRows.Close()
		for fkRows.Next() {
			var fkCol, pkTable, pkCol string
			if err := fkRows.Scan(&fkCol, &pkTable, &pkCol); err != nil {
				break
			}
			m.relations = append(m.relations, table+"."+fkCol+" -> "+pkTable+"."+pkCol)
		}
	}
	return m, nil
}

// readPostgreMeta pg_catalog/information_schema 混合四查询。
func readPostgreMeta(pool *sql.DB, table string) (*tableMeta, error) {
	m := &tableMeta{}

	if err := pool.QueryRow(
		`SELECT obj_description($1::regclass, 'pg_class')`, table,
	).Scan(&m.remark); err != nil && err != sql.ErrNoRows {
		return nil, err
	}

	rows, err := pool.Query(
		`SELECT column_name, data_type, COALESCE(character_maximum_length, numeric_precision, 0), is_nullable,
		        col_description(($1)::regclass, ordinal_position)
		 FROM information_schema.columns
		 WHERE table_schema = current_schema() AND table_name = $1
		 ORDER BY ordinal_position`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		c := &columnMeta{}
		var nullable string
		if err := rows.Scan(&c.name, &c.typeName, &c.size, &nullable, &c.remark); err != nil {
			return nil, err
		}
		c.nullable = nullable != "NO"
		m.columns = append(m.columns, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	pkRows, err := pool.Query(
		`SELECT a.attname
		 FROM pg_index i
		 JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
		 WHERE i.indrelid = $1::regclass AND i.indisprimary
		 ORDER BY array_position(i.indkey, a.attnum)`, table)
	if err == nil {
		defer pkRows.Close()
		for pkRows.Next() {
			var col string
			if err := pkRows.Scan(&col); err != nil {
				break
			}
			m.pks = append(m.pks, strings.TrimSpace(col))
		}
	}

	// 复合外键的 conkey/confkey 逐位配对为 best-effort（ANY 配对无序对齐）
	fkRows, err := pool.Query(
		`SELECT a.attname AS fk_col, cf.relname AS pk_table, af.attname AS pk_col
		 FROM pg_constraint c
		 JOIN pg_class cl ON cl.oid = c.conrelid
		 JOIN pg_namespace ns ON ns.oid = cl.relnamespace
		 JOIN pg_attribute a ON a.attrelid = cl.oid AND a.attnum = ANY(c.conkey)
		 JOIN pg_class cf ON cf.oid = c.confrelid
		 JOIN pg_attribute af ON af.attrelid = cf.oid AND af.attnum = ANY(c.confkey)
		 WHERE c.contype = 'f' AND cl.relname = $1 AND ns.nspname = current_schema()`, table)
	if err == nil {
		defer fkRows.Close()
		for fkRows.Next() {
			var fkCol, pkTable, pkCol string
			if err := fkRows.Scan(&fkCol, &pkTable, &pkCol); err != nil {
				break
			}
			m.relations = append(m.relations, table+"."+fkCol+" -> "+pkTable+"."+pkCol)
		}
	}
	return m, nil
}

// readGenericMeta 未知数据库：information_schema 尽力而为（无表/列注释、无
// 外键；表名字面量内插——占位符风格因库而异，转义单引号保安全）。
func readGenericMeta(pool *sql.DB, table string) (*tableMeta, error) {
	m := &tableMeta{}
	lit := strings.ReplaceAll(table, "'", "''")

	rows, err := pool.Query(
		`SELECT COLUMN_NAME, DATA_TYPE, COALESCE(CHARACTER_MAXIMUM_LENGTH, NUMERIC_PRECISION, 0), IS_NULLABLE
		 FROM INFORMATION_SCHEMA.COLUMNS
		 WHERE TABLE_NAME = '` + lit + `'
		 ORDER BY ORDINAL_POSITION`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		c := &columnMeta{}
		var nullable string
		if err := rows.Scan(&c.name, &c.typeName, &c.size, &nullable); err != nil {
			return nil, err
		}
		c.nullable = nullable != "NO"
		m.columns = append(m.columns, c)
	}
	return m, rows.Err()
}
