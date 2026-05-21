package db

import (
	"database/sql"
	"fmt"
	"strings"
)

// Dao provides chainable database operations.
type Dao struct {
	config    *Config
	sqlStr    string
	sqlArgs   []interface{}
	selFields string
	fromTable string
}

// SQL sets the SQL query and args.
func (d *Dao) SQL(query string, args ...interface{}) *Dao {
	d.sqlStr = query
	d.sqlArgs = args
	return d
}

// Select sets the fields to select.
func (d *Dao) Select(fields string) *Dao {
	d.selFields = fields
	return d
}

// Find executes the query and returns multiple rows.
func (d *Dao) Find() ([]*Row, error) {
	pool, err := d.config.Pool()
	if err != nil {
		return nil, err
	}
	query := d.sqlStr
	if d.selFields != "" && d.fromTable != "" {
		query = "SELECT " + d.selFields + " FROM " + d.fromTable
	}
	d.config.logSQL(query, d.sqlArgs...)
	rows, err := pool.Query(query, d.sqlArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRows(rows)
}

// FindFirst executes the query and returns the first row.
func (d *Dao) FindFirst() (*Row, error) {
	results, err := d.Find()
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, nil
	}
	return results[0], nil
}

// Paginate executes a paginated query.
func (d *Dao) Paginate(pageNum, pageSize int) (*Page, error) {
	pool, err := d.config.Pool()
	if err != nil {
		return nil, err
	}

	query := d.sqlStr

	// COUNT query - wrap as subquery
	countSQL := "SELECT COUNT(*) FROM (" + query + ")"
	d.config.logSQL(countSQL, d.sqlArgs...)
	var totalRows int64
	err = pool.QueryRow(countSQL, d.sqlArgs...).Scan(&totalRows)
	if err != nil {
		return nil, err
	}

	// Data query with LIMIT/OFFSET
	offset := (pageNum - 1) * pageSize
	paginateSQL := fmt.Sprintf("%s LIMIT %d OFFSET %d", query, pageSize, offset)
	d.config.logSQL(paginateSQL, d.sqlArgs...)
	rows, err := pool.Query(paginateSQL, d.sqlArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result, err := scanRows(rows)
	if err != nil {
		return nil, err
	}

	return NewPage(pageNum, pageSize, totalRows, result), nil
}

// Update executes an update/delete/insert and returns affected rows.
func (d *Dao) Update() (int64, error) {
	pool, err := d.config.Pool()
	if err != nil {
		return 0, err
	}
	d.config.logSQL(d.sqlStr, d.sqlArgs...)
	result, err := pool.Exec(d.sqlStr, d.sqlArgs...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// InsertRow inserts a row.
func (d *Dao) InsertRow(row *Row) (*Row, error) {
	pool, err := d.config.Pool()
	if err != nil {
		return nil, err
	}

	fields := row.FieldNames()
	values := row.FieldValues()
	sqlStr := d.config.Dialect.ForInsert(row.table, fields)
	args := values

	d.config.logSQL(sqlStr, args...)
	result, err := pool.Exec(sqlStr, args...)
	if err != nil {
		return nil, err
	}

	id, _ := result.LastInsertId()
	if id > 0 && len(row.primaryKeys) > 0 {
		row.Set(row.primaryKeys[0], id)
	}
	return row, nil
}

// InsertOrUpdateRow inserts or updates a row.
func (d *Dao) InsertOrUpdateRow(row *Row) (*Row, error) {
	pool, err := d.config.Pool()
	if err != nil {
		return nil, err
	}

	fields := row.FieldNames()
	values := row.FieldValues()
	sqlStr := d.config.Dialect.ForInsertOrUpdate(row.table, fields, row.primaryKeys)

	d.config.logSQL(sqlStr, values...)
	_, err = pool.Exec(sqlStr, values...)
	if err != nil {
		return nil, err
	}
	return row, nil
}

// UpdateRow updates a row using the change set.
func (d *Dao) UpdateRow(row *Row) (bool, error) {
	pool, err := d.config.Pool()
	if err != nil {
		return false, err
	}

	changedFields := row.ChangedFields()
	if len(changedFields) == 0 {
		return false, nil
	}

	args := make([]interface{}, 0, len(changedFields)+len(row.primaryKeys))
	for _, f := range changedFields {
		args = append(args, row.data[f])
	}
	for _, pk := range row.primaryKeys {
		args = append(args, row.data[pk])
	}

	sqlStr := d.config.Dialect.ForUpdate(row.table, changedFields, row.primaryKeys)
	d.config.logSQL(sqlStr, args...)
	result, err := pool.Exec(sqlStr, args...)
	if err != nil {
		return false, err
	}
	n, _ := result.RowsAffected()
	return n > 0, nil
}

// DeleteRow deletes a row by primary key.
func (d *Dao) DeleteRow(row *Row) (bool, error) {
	pool, err := d.config.Pool()
	if err != nil {
		return false, err
	}

	sqlStr := d.config.Dialect.ForDeleteByID(row.table, row.primaryKeys)
	args := make([]interface{}, len(row.primaryKeys))
	for i, pk := range row.primaryKeys {
		args[i] = row.data[pk]
	}

	d.config.logSQL(sqlStr, args...)
	result, err := pool.Exec(sqlStr, args...)
	if err != nil {
		return false, err
	}
	n, _ := result.RowsAffected()
	return n > 0, nil
}

// FindByID finds a row by table and ID.
func (d *Dao) FindByID(table string, id interface{}) (*Row, error) {
	return d.FindByIDWithPK(table, "id", id)
}

// FindByIDWithPK finds a row by table, PK name, and ID.
func (d *Dao) FindByIDWithPK(table, pk string, id interface{}) (*Row, error) {
	pool, err := d.config.Pool()
	if err != nil {
		return nil, err
	}
	sqlStr := d.config.Dialect.ForFindByID(table, []string{pk})
	d.config.logSQL(sqlStr, id)
	rows, err := pool.Query(sqlStr, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result, err := scanRows(rows)
	if err != nil || len(result) == 0 {
		return nil, err
	}
	return result[0], nil
}

// FindBy finds rows by table and condition.
func (d *Dao) FindBy(table, whereOrField string, args ...interface{}) ([]*Row, error) {
	query := buildSelectWhere(table, whereOrField)
	pool, err := d.config.Pool()
	if err != nil {
		return nil, err
	}
	d.config.logSQL(query, args...)
	rows, err := pool.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRows(rows)
}

// FindFirstBy finds the first row by condition.
func (d *Dao) FindFirstBy(table, whereOrField string, args ...interface{}) (*Row, error) {
	results, err := d.FindBy(table, whereOrField, args...)
	if err != nil || len(results) == 0 {
		return nil, err
	}
	return results[0], nil
}

// FindAll finds all rows in a table.
func (d *Dao) FindAll(table string) ([]*Row, error) {
	return d.FindBy(table, "1=1")
}

// DeleteByID deletes a row by table and ID.
func (d *Dao) DeleteByID(table string, id interface{}) (bool, error) {
	return d.DeleteByIDWithPK(table, "id", id)
}

// DeleteByIDWithPK deletes by table, PK name, and ID.
func (d *Dao) DeleteByIDWithPK(table, pk string, id interface{}) (bool, error) {
	pool, err := d.config.Pool()
	if err != nil {
		return false, err
	}
	sqlStr := d.config.Dialect.ForDeleteByID(table, []string{pk})
	d.config.logSQL(sqlStr, id)
	result, err := pool.Exec(sqlStr, id)
	if err != nil {
		return false, err
	}
	n, _ := result.RowsAffected()
	return n > 0, nil
}

// DeleteBy deletes rows by condition.
func (d *Dao) DeleteBy(table, whereOrField string, args ...interface{}) (int64, error) {
	var query string
	var execArgs []interface{}
	if strings.Contains(whereOrField, " ") {
		query = fmt.Sprintf("DELETE FROM %s WHERE %s", table, whereOrField)
		execArgs = args
	} else {
		query = fmt.Sprintf("DELETE FROM %s WHERE %s = ?", table, whereOrField)
		execArgs = args
	}
	pool, err := d.config.Pool()
	if err != nil {
		return 0, err
	}
	d.config.logSQL(query, execArgs...)
	result, err := pool.Exec(query, execArgs...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// Count counts all rows in a table.
func (d *Dao) Count(table string) (int64, error) {
	pool, err := d.config.Pool()
	if err != nil {
		return 0, err
	}
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s", table)
	d.config.logSQL(query)
	var count int64
	err = pool.QueryRow(query).Scan(&count)
	return count, err
}

// CountBy counts rows by condition.
func (d *Dao) CountBy(table, whereOrField string, args ...interface{}) (int64, error) {
	var query string
	var queryArgs []interface{}
	if strings.Contains(whereOrField, " ") {
		query = fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s", table, whereOrField)
		queryArgs = args
	} else {
		query = fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s = ?", table, whereOrField)
		queryArgs = args
	}
	pool, err := d.config.Pool()
	if err != nil {
		return 0, err
	}
	d.config.logSQL(query, queryArgs...)
	var count int64
	err = pool.QueryRow(query, queryArgs...).Scan(&count)
	return count, err
}

// FindIn finds rows where field IN values.
func (d *Dao) FindIn(table, field string, values ...interface{}) ([]*Row, error) {
	placeholders := strings.Repeat("?, ", len(values)-1) + "?"
	query := fmt.Sprintf("SELECT * FROM %s WHERE %s IN (%s)", table, field, placeholders)
	pool, err := d.config.Pool()
	if err != nil {
		return nil, err
	}
	d.config.logSQL(query, values...)
	rows, err := pool.Query(query, values...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRows(rows)
}

// DeleteIn deletes rows where field IN values.
func (d *Dao) DeleteIn(table, field string, values ...interface{}) (int64, error) {
	placeholders := strings.Repeat("?, ", len(values)-1) + "?"
	query := fmt.Sprintf("DELETE FROM %s WHERE %s IN (%s)", table, field, placeholders)
	pool, err := d.config.Pool()
	if err != nil {
		return 0, err
	}
	d.config.logSQL(query, values...)
	result, err := pool.Exec(query, values...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// ---- helpers ----

func scanRows(rows *sql.Rows) ([]*Row, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var results []*Row
	for rows.Next() {
		values := make([]interface{}, len(cols))
		for i := range values {
			values[i] = new(interface{})
		}
		if err := rows.Scan(values...); err != nil {
			return nil, err
		}
		data := make(map[string]interface{})
		for i, col := range cols {
			val := values[i]
			if p, ok := val.(*interface{}); ok {
				data[col] = *p
			} else {
				data[col] = val
			}
		}
		row := &Row{data: data}
		results = append(results, row)
	}
	return results, nil
}

func buildSelectWhere(table, whereOrField string) string {
	if strings.Contains(whereOrField, " ") {
		return fmt.Sprintf("SELECT * FROM %s WHERE %s", table, whereOrField)
	}
	return fmt.Sprintf("SELECT * FROM %s WHERE %s = ?", table, whereOrField)
}

func extractCountSQL(sql string) string {
	// Try to extract FROM clause and build COUNT
	s := strings.TrimSpace(sql)
	upper := strings.ToUpper(s)

	fromIdx := strings.Index(upper, "FROM ")
	if fromIdx == -1 {
		return "SELECT COUNT(*) FROM (" + sql + ")"
	}

	fromClause := s[fromIdx:]
	// Remove ORDER BY
	if idx := strings.Index(strings.ToUpper(fromClause), " ORDER BY "); idx != -1 {
		fromClause = fromClause[:idx]
	}
	return "SELECT COUNT(*) " + fromClause
}
