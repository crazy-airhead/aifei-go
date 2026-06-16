package db

import (
	"database/sql"
	"fmt"

	dbsql "github.com/crazy-airhead/aifei-go/db/sql"
)

// ---- Row-based executors ----

// execInsertRow handles INSERT for a Row, including InsertHook callbacks.
func execInsertRow(dao *Dao, row *Row) (*Row, error) {
	config := dao.config
	dialect := config.Dialect
	hk := config.GetDbHookKit()

	fields := row.FieldNames()
	values := row.FieldValues()
	sqlStr := dialect.ForInsert(row.table, fields)
	dao.setSqlPara(&dbsql.SqlPara{Sql: sqlStr, Paras: values})

	var toAfter interface{}
	if hk != nil && hk.InsertHook != nil {
		toAfter = hk.InsertHook.BeforeRowInsert(dao, row)
		if sp := dao.sqlPara; sp != nil {
			sqlStr = sp.Sql
			values = sp.Paras
		}
	}

	pool, err := config.Pool()
	if err != nil {
		return nil, err
	}
	config.logSQL(sqlStr, values...)
	result, err := pool.Exec(sqlStr, values...)
	if err != nil {
		return nil, err
	}

	n, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n != 1 {
		return nil, fmt.Errorf("insert error: expected exactly 1 affected row, got %d", n)
	}

	id, _ := result.LastInsertId()
	if id > 0 && len(row.primaryKeys) > 0 {
		row.Set(row.primaryKeys[0], id)
	}

	if hk != nil && hk.InsertHook != nil {
		hk.InsertHook.AfterRowInsert(dao, row, toAfter)
	}
	row.ClearChange()
	return row, nil
}

// execUpdateRow handles UPDATE for a Row, including UpdateHook callbacks
// (both Row and Sql variants, matching Java's two-level hook design).
func execUpdateRow(dao *Dao, row *Row) (bool, error) {
	config := dao.config
	dialect := config.Dialect
	hk := config.GetDbHookKit()

	changedFields := row.ChangedFields()
	if len(changedFields) == 0 {
		return false, nil
	}

	for _, pk := range row.primaryKeys {
		if v := row.Get(pk); v == nil {
			return false, fmt.Errorf("update operation cannot proceed without primary key value: %q can not be null", pk)
		}
	}

	args := make([]interface{}, 0, len(changedFields)+len(row.primaryKeys))
	for _, f := range changedFields {
		args = append(args, row.data[f])
	}
	for _, pk := range row.primaryKeys {
		args = append(args, row.data[pk])
	}
	sqlStr := dialect.ForUpdate(row.table, changedFields, row.primaryKeys)
	dao.setSqlPara(&dbsql.SqlPara{Sql: sqlStr, Paras: args})

	if len(args) <= len(row.primaryKeys) {
		return false, nil // only PK fields, nothing to update
	}

	var toAfterRow interface{}
	if hk != nil && hk.UpdateHook != nil {
		toAfterRow = hk.UpdateHook.BeforeRowUpdate(dao, row)
		if sp := dao.sqlPara; sp != nil {
			sqlStr = sp.Sql
			args = sp.Paras
		}
	}

	affected, err := execSqlUpdate(dao)
	if err != nil {
		return false, err
	}

	switch {
	case affected == 1:
		if hk != nil && hk.UpdateHook != nil {
			hk.UpdateHook.AfterRowUpdate(dao, row, toAfterRow)
		}
		row.ClearChange()
		return true, nil
	case affected == 0:
		return false, nil
	default:
		return false, fmt.Errorf("the number of rows updated by the primary key cannot be greater than 1")
	}
}

// execDeleteRow handles DELETE for a Row, including DeleteHook callbacks
// (both Row and Sql variants).
func execDeleteRow(dao *Dao, row *Row) (bool, error) {
	config := dao.config
	dialect := config.Dialect
	hk := config.GetDbHookKit()

	sqlStr := dialect.ForDeleteByID(row.table, row.primaryKeys)
	args := make([]interface{}, len(row.primaryKeys))
	for i, pk := range row.primaryKeys {
		args[i] = row.data[pk]
	}
	dao.setSqlPara(&dbsql.SqlPara{Sql: sqlStr, Paras: args})

	var toAfterRow interface{}
	if hk != nil && hk.DeleteHook != nil {
		toAfterRow = hk.DeleteHook.BeforeRowDelete(dao, row)
		if sp := dao.sqlPara; sp != nil {
			sqlStr = sp.Sql
			args = sp.Paras
		}
	}

	affected, err := execSqlDelete(dao)
	if err != nil {
		return false, err
	}

	switch {
	case affected == 1:
		if hk != nil && hk.DeleteHook != nil {
			hk.DeleteHook.AfterRowDelete(dao, row, toAfterRow)
		}
		row.ClearChange()
		return true, nil
	case affected == 0:
		return false, nil
	default:
		return false, fmt.Errorf("the number of rows deleted by the primary key cannot be greater than 1")
	}
}

// ---- SQL-based DML executors ----

// execSqlUpdate executes a DML statement, firing UpdateHook.Sql callbacks.
// Used by Dao.Update() for raw DML and internally by execUpdateRow.
func execSqlUpdate(dao *Dao) (int64, error) {
	config := dao.config
	hk := config.GetDbHookKit()

	query, args := dao.sqlAndArgs()
	dao.setSqlPara(&dbsql.SqlPara{Sql: query, Paras: args})

	var toAfter interface{}
	if hk != nil && hk.UpdateHook != nil {
		toAfter = hk.UpdateHook.BeforeSqlUpdate(dao)
		if sp := dao.sqlPara; sp != nil {
			query = sp.Sql
			args = sp.Paras
		}
	}

	pool, err := config.Pool()
	if err != nil {
		return 0, err
	}
	config.logSQL(query, args...)
	result, err := pool.Exec(query, args...)
	if err != nil {
		return 0, err
	}
	ret, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}

	if hk != nil && hk.UpdateHook != nil {
		hk.UpdateHook.AfterSqlUpdate(dao, ret, toAfter)
	}
	return ret, nil
}

// execSqlDelete executes a DELETE statement, firing DeleteHook.Sql callbacks.
// Used by Dao.DeleteBy/DeleteIn and internally by execDeleteRow.
func execSqlDelete(dao *Dao) (int64, error) {
	config := dao.config
	hk := config.GetDbHookKit()

	query, args := dao.sqlAndArgs()
	dao.setSqlPara(&dbsql.SqlPara{Sql: query, Paras: args})

	var toAfter interface{}
	if hk != nil && hk.DeleteHook != nil {
		toAfter = hk.DeleteHook.BeforeSqlDelete(dao)
		if sp := dao.sqlPara; sp != nil {
			query = sp.Sql
			args = sp.Paras
		}
	}

	pool, err := config.Pool()
	if err != nil {
		return 0, err
	}
	config.logSQL(query, args...)
	result, err := pool.Exec(query, args...)
	if err != nil {
		return 0, err
	}
	ret, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}

	if hk != nil && hk.DeleteHook != nil {
		hk.DeleteHook.AfterSqlDelete(dao, ret, toAfter)
	}
	return ret, nil
}

// ---- Read executors ----

// execFind executes a SELECT query and returns Rows.
// If isRawSQL is true, QueryHook also fires (in addition to FindHook).
func execFind(dao *Dao, isRawSQL bool) ([]*Row, error) {
	config := dao.config
	hk := config.GetDbHookKit()

	query, args := dao.sqlAndArgs()
	if dao.selFields != "" && dao.fromTable != "" {
		query = dao.config.Dialect.ForSelect(dao.fromTable, dao.selFields)
	}
	dao.setSqlPara(&dbsql.SqlPara{Sql: query, Paras: args})

	var toAfterFind, toAfterQuery interface{}
	if hk != nil && hk.QueryHook != nil && isRawSQL {
		toAfterQuery = hk.QueryHook.BeforeQuery(dao)
	}
	if hk != nil && hk.FindHook != nil {
		toAfterFind = hk.FindHook.BeforeFind(dao)
	}
	if sp := dao.sqlPara; sp != nil {
		query = sp.Sql
		args = sp.Paras
	}

	pool, err := config.Pool()
	if err != nil {
		return nil, err
	}
	config.logSQL(query, args...)
	rows, err := pool.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result, err := scanRows(rows)
	if err != nil {
		return nil, err
	}

	if hk != nil && hk.FindHook != nil {
		hk.FindHook.AfterFind(dao, result, toAfterFind)
	}
	if hk != nil && hk.QueryHook != nil && isRawSQL {
		hk.QueryHook.AfterQuery(dao, result, toAfterQuery)
	}
	return result, nil
}

// execFindBy executes a builder-based SELECT query (FindBy, FindByID, FindAll, FindIn).
// Only FindHook fires (not QueryHook).
func execFindBy(dao *Dao) ([]*Row, error) {
	config := dao.config
	hk := config.GetDbHookKit()

	query, args := dao.sqlAndArgs()
	dao.setSqlPara(&dbsql.SqlPara{Sql: query, Paras: args})

	var toAfter interface{}
	if hk != nil && hk.FindHook != nil {
		toAfter = hk.FindHook.BeforeFind(dao)
		if sp := dao.sqlPara; sp != nil {
			query = sp.Sql
			args = sp.Paras
		}
	}

	pool, err := config.Pool()
	if err != nil {
		return nil, err
	}
	config.logSQL(query, args...)
	rows, err := pool.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result, err := scanRows(rows)
	if err != nil {
		return nil, err
	}

	if hk != nil && hk.FindHook != nil {
		hk.FindHook.AfterFind(dao, result, toAfter)
	}
	return result, nil
}

// execPaginate executes a paginated query with PaginateHook callbacks.
func execPaginate(dao *Dao, pageNum, pageSize int) (*Page, error) {
	if pageNum < 1 || pageSize < 1 {
		return nil, fmt.Errorf("pageNum and pageSize must be greater than 0")
	}

	config := dao.config
	dialect := config.Dialect
	hk := config.GetDbHookKit()
	query, args := dao.sqlAndArgs()

	// ---- COUNT query ----
	countSQL := dialect.ForCountSubquery(query)
	countSP := &dbsql.SqlPara{Sql: countSQL, Paras: args}
	dao.setSqlPara(countSP)

	var toAfterCount interface{}
	if hk != nil && hk.PaginateHook != nil {
		toAfterCount = hk.PaginateHook.BeforeQueryTotalRows(dao, countSP)
		if sp := dao.sqlPara; sp != nil {
			countSQL = sp.Sql
			args = sp.Paras
		}
	}

	pool, err := config.Pool()
	if err != nil {
		return nil, err
	}
	config.logSQL(countSQL, args...)
	var totalRows int64
	if err := pool.QueryRow(countSQL, args...).Scan(&totalRows); err != nil {
		return nil, err
	}

	if hk != nil && hk.PaginateHook != nil {
		hk.PaginateHook.AfterQueryTotalRows(dao, totalRows, toAfterCount)
	}

	if totalRows == 0 {
		return NewPage(pageNum, pageSize, 0, nil), nil
	}

	// ---- Data query ----
	paginateSQL := dialect.ForPaginate(query, pageNum, pageSize)
	dataSP := &dbsql.SqlPara{Sql: paginateSQL, Paras: args}
	dao.setSqlPara(dataSP)

	var toAfterPage interface{}
	if hk != nil && hk.PaginateHook != nil {
		toAfterPage = hk.PaginateHook.BeforePaginate(dao, pageNum, pageSize, totalRows, dataSP)
		if sp := dao.sqlPara; sp != nil {
			paginateSQL = sp.Sql
			args = sp.Paras
		}
	}

	config.logSQL(paginateSQL, args...)
	rows, err := pool.Query(paginateSQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result, err := scanRows(rows)
	if err != nil {
		return nil, err
	}

	page := NewPage(pageNum, pageSize, totalRows, result)
	if hk != nil && hk.PaginateHook != nil {
		hk.PaginateHook.AfterPaginate(dao, page, toAfterPage)
	}
	return page, nil
}

// ---- Higher-level executors (SQL building + delegation) ----

// execInsertOrUpdateRow handles INSERT OR UPDATE for a Row with InsertHook.
func execInsertOrUpdateRow(dao *Dao, row *Row) (*Row, error) {
	config := dao.config
	hk := config.GetDbHookKit()

	fields := row.FieldNames()
	values := row.FieldValues()
	sqlStr := config.Dialect.ForInsertOrUpdate(row.table, fields, row.primaryKeys)
	dao.setSqlPara(&dbsql.SqlPara{Sql: sqlStr, Paras: values})

	var toAfter interface{}
	if hk != nil && hk.InsertHook != nil {
		toAfter = hk.InsertHook.BeforeRowInsert(dao, row)
		if sp := dao.sqlPara; sp != nil {
			sqlStr = sp.Sql
			values = sp.Paras
		}
	}

	pool, err := config.Pool()
	if err != nil {
		return nil, err
	}
	config.logSQL(sqlStr, values...)
	if _, err := pool.Exec(sqlStr, values...); err != nil {
		return nil, err
	}

	if hk != nil && hk.InsertHook != nil {
		hk.InsertHook.AfterRowInsert(dao, row, toAfter)
	}
	row.ClearChange()
	return row, nil
}

// execDeleteById deletes by table and primary key value.
// Creates a Row internally so both Row and Sql DeleteHook callbacks fire.
func execDeleteById(dao *Dao, table, pk string, id interface{}) (bool, error) {
	row := NewRowWithPK(table, pk)
	row.Put(pk, id)
	return execDeleteRow(dao, row)
}

// execDeleteBy deletes rows by table and condition.
func execDeleteBy(dao *Dao, table, whereOrField string, args []interface{}) (int64, error) {
	dao.setSqlPara(&dbsql.SqlPara{Sql: dao.config.Dialect.ForDeleteBy(table, whereOrField), Paras: args})
	return execSqlDelete(dao)
}

// execDeleteIn deletes rows where field IN values.
func execDeleteIn(dao *Dao, table, field string, values []interface{}) (int64, error) {
	dao.setSqlPara(&dbsql.SqlPara{Sql: dao.config.Dialect.ForDeleteIn(table, field, len(values)), Paras: values})
	return execSqlDelete(dao)
}

// execFindById finds a row by table and primary key value.
func execFindById(dao *Dao, table, pk string, id interface{}) (*Row, error) {
	sqlStr := dao.config.Dialect.ForFindByID(table, []string{pk})
	dao.setSqlPara(&dbsql.SqlPara{Sql: sqlStr, Paras: []interface{}{id}})
	rows, err := execFindBy(dao)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// execFindByCondition finds rows by table and condition.
func execFindByCondition(dao *Dao, table, whereOrField string, args []interface{}) ([]*Row, error) {
	dao.setSqlPara(&dbsql.SqlPara{Sql: dao.config.Dialect.ForFindBy(table, whereOrField), Paras: args})
	return execFindBy(dao)
}

// execFindAll finds all rows in a table.
func execFindAll(dao *Dao, table string) ([]*Row, error) {
	return execFindByCondition(dao, table, "1=1", nil)
}

// execFindIn finds rows where field IN values.
func execFindIn(dao *Dao, table, field string, values []interface{}) ([]*Row, error) {
	dao.setSqlPara(&dbsql.SqlPara{Sql: dao.config.Dialect.ForFindIn(table, field, len(values)), Paras: values})
	return execFindBy(dao)
}

// execCount counts all rows in a table.
func execCount(dao *Dao, table string) (int64, error) {
	dao.setSqlPara(&dbsql.SqlPara{Sql: dao.config.Dialect.ForCount(table)})
	rows, err := execFindBy(dao)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return ToInt64(rows[0].Get("COUNT(*)")), nil
}

// execCountBy counts rows by condition.
func execCountBy(dao *Dao, table, whereOrField string, args []interface{}) (int64, error) {
	dao.setSqlPara(&dbsql.SqlPara{Sql: dao.config.Dialect.ForCountBy(table, whereOrField), Paras: args})
	rows, err := execFindBy(dao)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return ToInt64(rows[0].Get("COUNT(*)")), nil
}

// ---- Internal helpers ----

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
