package db

import (
	"database/sql"
	"fmt"
	"strings"

	dbsql "github.com/crazy-airhead/aifei-go/db/sql"
)

// ---- Row-based executors ----

// execInsertRow handles INSERT for a Row, including InsertHook callbacks.
func execInsertRow(dao *Dao, row *Row) (*Row, error) {
	config := dao.config
	dialect := config.Dialect
	hk := config.GetDbHookKit()

	fields := filterTableFields(row.table, row.FieldNames())
	values := make([]interface{}, len(fields))
	for i, f := range fields {
		values[i] = normalizeSQLValue(row.data[f])
	}
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

	changedFields := filterTableFields(row.table, row.ChangedFields())
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
		args = append(args, normalizeSQLValue(row.data[f]))
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
	decodeRows(result, dao.table)
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
	decodeRows(result, dao.table)
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
	decodeRows(result, dao.table)

	page := NewPage(pageNum, pageSize, totalRows, result)
	if hk != nil && hk.PaginateHook != nil {
		hk.PaginateHook.AfterPaginate(dao, page, toAfterPage)
	}
	return page, nil
}

// execPaginateWithTotalRows executes a paginated query with a custom totalRows function.
// The totalRowsFn receives the count SqlPara and a defaultQuery closure for the standard COUNT.
// Use cases: cache totalRows in Redis, or provide custom COUNT SQL for complex ORDER BY.
func execPaginateWithTotalRows(dao *Dao, pageNum, pageSize int, totalRowsFn func(sqlPara *dbsql.SqlPara, defaultQuery func() (int64, error)) (int64, error)) (*Page, error) {
	if pageNum < 1 || pageSize < 1 {
		return nil, fmt.Errorf("pageNum and pageSize must be greater than 0")
	}

	config := dao.config
	dialect := config.Dialect
	hk := config.GetDbHookKit()
	query, args := dao.sqlAndArgs()

	countSQL := dialect.ForCountSubquery(query)
	countSP := &dbsql.SqlPara{Sql: countSQL, Paras: args}

	defaultQuery := func() (int64, error) {
		pool, err := config.Pool()
		if err != nil {
			return 0, err
		}
		config.logSQL(countSQL, args...)
		var total int64
		if err := pool.QueryRow(countSQL, args...).Scan(&total); err != nil {
			return 0, err
		}
		return total, nil
	}

	var toAfterCount interface{}
	if hk != nil && hk.PaginateHook != nil {
		toAfterCount = hk.PaginateHook.BeforeQueryTotalRows(dao, countSP)
	}

	totalRows, err := totalRowsFn(countSP, defaultQuery)
	if err != nil {
		return nil, err
	}

	if hk != nil && hk.PaginateHook != nil {
		hk.PaginateHook.AfterQueryTotalRows(dao, totalRows, toAfterCount)
	}

	if totalRows == 0 {
		return NewPage(pageNum, pageSize, 0, nil), nil
	}

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

	pool, err := config.Pool()
	if err != nil {
		return nil, err
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
	decodeRows(result, dao.table)

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

	fields := filterTableFields(row.table, row.FieldNames())
	values := make([]interface{}, len(fields))
	for i, f := range fields {
		values[i] = normalizeSQLValue(row.data[f])
	}
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
	dao.table = table
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
	dao.table = table
	dao.setSqlPara(&dbsql.SqlPara{Sql: dao.config.Dialect.ForFindBy(table, whereOrField), Paras: args})
	return execFindBy(dao)
}

// execFindAll finds all rows in a table.
func execFindAll(dao *Dao, table string) ([]*Row, error) {
	return execFindByCondition(dao, table, "1=1", nil)
}

// execFindIn finds rows where field IN values.
func execFindIn(dao *Dao, table, field string, values []interface{}) ([]*Row, error) {
	dao.table = table
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

// ---- Advanced query executors ----

// execFindOne returns exactly one row, or an error.
func execFindOne(dao *Dao, allowNull bool) (*Row, error) {
	rows, err := execFind(dao, dao.isRawSQL())
	if err != nil {
		return nil, err
	}
	switch len(rows) {
	case 1:
		return rows[0], nil
	case 0:
		if allowNull {
			return nil, nil
		}
		return nil, fmt.Errorf("expected exactly one result, but found 0")
	default:
		return nil, fmt.Errorf("expected exactly one result, but found %d. Consider using FindFirst instead", len(rows))
	}
}

// execFindExists returns true if the query returns at least one row.
func execFindExists(dao *Dao) (bool, error) {
	config := dao.config
	hk := config.GetDbHookKit()

	query, args := dao.sqlAndArgs()
	if dao.selFields != "" && dao.fromTable != "" {
		query = config.Dialect.ForSelect(dao.fromTable, dao.selFields)
	}
	dao.setSqlPara(&dbsql.SqlPara{Sql: query, Paras: args})

	var toAfterFind, toAfterQuery interface{}
	if hk != nil && hk.QueryHook != nil && dao.isRawSQL() {
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
		return false, err
	}
	config.logSQL(query, args...)
	rows, err := pool.Query(query, args...)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	exists := rows.Next()

	if hk != nil && hk.FindHook != nil {
		hk.FindHook.AfterFind(dao, nil, toAfterFind)
	}
	if hk != nil && hk.QueryHook != nil && dao.isRawSQL() {
		hk.QueryHook.AfterQuery(dao, nil, toAfterQuery)
	}
	return exists, nil
}

// execForEach iterates over query results with a callback.
// The callback receives each *Row; return false to stop iteration early.
func execForEach(dao *Dao, fn func(*Row) bool) error {
	config := dao.config
	hk := config.GetDbHookKit()

	query, args := dao.sqlAndArgs()
	if dao.selFields != "" && dao.fromTable != "" {
		query = config.Dialect.ForSelect(dao.fromTable, dao.selFields)
	}
	dao.setSqlPara(&dbsql.SqlPara{Sql: query, Paras: args})

	var toAfterFind, toAfterQuery interface{}
	if hk != nil && hk.QueryHook != nil && dao.isRawSQL() {
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
		return err
	}
	config.logSQL(query, args...)
	rows, err := pool.Query(query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	colTypes, _ := rows.ColumnTypes()
	isBinary := make([]bool, len(colTypes))
	for i, ct := range colTypes {
		isBinary[i] = isBinaryColumnType(ct.DatabaseTypeName())
	}
	decodeTable := tableFor(dao.table)
	var rowList []*Row
	for rows.Next() {
		values := make([]interface{}, len(cols))
		for i := range values {
			values[i] = new(interface{})
		}
		if err := rows.Scan(values...); err != nil {
			return err
		}
		data := make(map[string]interface{})
		for i, col := range cols {
			val := values[i]
			if p, ok := val.(*interface{}); ok {
				if isBinary[i] {
					data[col] = *p
				} else {
					data[col] = bytesToStr(*p)
				}
			} else {
				data[col] = val
			}
		}
		row := &Row{data: data}
		if decodeTable != nil {
			decodeRow(row, decodeTable)
		}
		rowList = append(rowList, row)
		if !fn(row) {
			break
		}
	}

	if hk != nil && hk.FindHook != nil {
		hk.FindHook.AfterFind(dao, rowList, toAfterFind)
	}
	if hk != nil && hk.QueryHook != nil && dao.isRawSQL() {
		hk.QueryHook.AfterQuery(dao, rowList, toAfterQuery)
	}
	return nil
}

// ---- Pagination extensions ----

// execForEachPage iterates over all pages.
func execForEachPage(dao *Dao, pageSize int, fn func(*Page) bool) error {
	for pageNum := 1; ; pageNum++ {
		page, err := execPaginate(dao, pageNum, pageSize)
		if err != nil {
			return err
		}
		if len(page.Rows) == 0 || !fn(page) {
			return nil
		}
	}
}

// execForEachPageRange iterates from startPageNum to endPageNum.
func execForEachPageRange(dao *Dao, startPageNum, endPageNum, pageSize int, fn func(*Page) bool) error {
	for pageNum := startPageNum; pageNum <= endPageNum; pageNum++ {
		page, err := execPaginate(dao, pageNum, pageSize)
		if err != nil {
			return err
		}
		if len(page.Rows) == 0 || !fn(page) {
			return nil
		}
	}
	return nil
}

// ---- Query executors (raw results, not wrapped in Row) ----

// execQuery executes raw SQL and returns results as []interface{}.
// For single-column results, each element is a scalar.
// For multi-column results, each element is []interface{}.
func execQuery(dao *Dao, returnFirst bool) ([]interface{}, error) {
	config := dao.config
	hk := config.GetDbHookKit()

	query, args := dao.sqlAndArgs()
	dao.setSqlPara(&dbsql.SqlPara{Sql: query, Paras: args})

	var toAfter interface{}
	if hk != nil && hk.QueryHook != nil {
		toAfter = hk.QueryHook.BeforeQuery(dao)
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

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var result []interface{}
	if len(cols) > 1 {
		for rows.Next() {
			columnArray := make([]interface{}, len(cols))
			scanArgs := make([]interface{}, len(cols))
			for i := range scanArgs {
				scanArgs[i] = new(interface{})
			}
			if err := rows.Scan(scanArgs...); err != nil {
				return nil, err
			}
			for i := range columnArray {
				if p, ok := scanArgs[i].(*interface{}); ok {
					columnArray[i] = *p
				}
			}
			result = append(result, columnArray)
			if returnFirst {
				break
			}
		}
	} else {
		for rows.Next() {
			var val interface{}
			if err := rows.Scan(&val); err != nil {
				return nil, err
			}
			result = append(result, val)
			if returnFirst {
				break
			}
		}
	}

	if hk != nil && hk.QueryHook != nil {
		hk.QueryHook.AfterQuery(dao, result, toAfter)
	}
	return result, nil
}

// execQueryField returns the first field of the first row.
func execQueryField(dao *Dao) (interface{}, error) {
	result, err := execQuery(dao, true)
	if err != nil {
		return nil, err
	}
	if len(result) > 0 {
		if _, ok := result[0].([]interface{}); ok {
			return nil, fmt.Errorf("the queryField method allows querying only a single field")
		}
		return result[0], nil
	}
	return nil, nil
}

// execQueryOne returns exactly one raw result.
func execQueryOne(dao *Dao, allowNull bool) (interface{}, error) {
	result, err := execQuery(dao, false)
	if err != nil {
		return nil, err
	}
	switch len(result) {
	case 1:
		return result[0], nil
	case 0:
		if allowNull {
			return nil, nil
		}
		return nil, fmt.Errorf("expected one or zero result, but found 0")
	default:
		return nil, fmt.Errorf("expected one or zero result, but found %d. Consider using QueryFirst instead", len(result))
	}
}

// execQueryTime returns the first field as time.Time.
func execQueryTime(dao *Dao) (interface{}, error) {
	return execQueryField(dao)
}

// execQueryBytes returns the first field as []byte.
func execQueryBytes(dao *Dao) ([]byte, error) {
	v, err := execQueryField(dao)
	if err != nil || v == nil {
		return nil, err
	}
	if b, ok := v.([]byte); ok {
		return b, nil
	}
	return nil, fmt.Errorf("expected []byte, got %T", v)
}

// ---- ByID extensions ----

// execDeleteByCompositeId deletes by composite primary keys.
func execDeleteByCompositeId(dao *Dao, table string, pks []string, idValues []interface{}) (bool, error) {
	row := NewRowWithCompositePK(table, pks[0], pks[1])
	for i, pk := range pks {
		row.Put(pk, idValues[i])
	}
	return execDeleteRow(dao, row)
}

// execFindByCompositeId finds by composite primary keys.
func execFindByCompositeId(dao *Dao, table string, pks []string, idValues []interface{}) (*Row, error) {
	dao.table = table
	sqlStr := dao.config.Dialect.ForFindByID(table, pks)
	dao.setSqlPara(&dbsql.SqlPara{Sql: sqlStr, Paras: idValues})
	rows, err := execFindBy(dao)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// execDeleteInIds deletes by primary key IN values.
func execDeleteInIds(dao *Dao, table string, ids []interface{}) (int64, error) {
	pk := dao.config.Dialect.DefaultPrimaryKeys()[0]
	return execDeleteIn(dao, table, pk, ids)
}

// execFindInIds finds by primary key IN values.
func execFindInIds(dao *Dao, table string, ids []interface{}) ([]*Row, error) {
	pk := dao.config.Dialect.DefaultPrimaryKeys()[0]
	return execFindIn(dao, table, pk, ids)
}

// ---- Transaction on Dao ----

// execTransaction executes fn within a transaction for the Dao's config.
func execTransaction(dao *Dao, fn func(*Dao) error) error {
	config := dao.config
	pool, err := config.Pool()
	if err != nil {
		return err
	}
	tx, err := pool.Begin()
	if err != nil {
		return err
	}
	if err := fn(dao); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// ---- Internal helpers ----

func scanRows(rows *sql.Rows) ([]*Row, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	colTypes, _ := rows.ColumnTypes()
	isBinary := make([]bool, len(colTypes))
	for i, ct := range colTypes {
		isBinary[i] = isBinaryColumnType(ct.DatabaseTypeName())
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
				if isBinary[i] {
					data[col] = *p
				} else {
					data[col] = bytesToStr(*p)
				}
			} else {
				data[col] = val
			}
		}
		row := &Row{data: data}
		results = append(results, row)
	}
	return results, nil
}

// decodeRows binds table/primary-key metadata to result rows and decodes declared
// JSON columns into their Go types. It is a no-op when table is empty or not
// registered, so raw queries without a Table() hint (and unregistered tables)
// stay exactly as before. Idempotent: re-decoding an already-typed row is a no-op.
func decodeRows(rows []*Row, table string) {
	t := tableFor(table)
	if t == nil {
		return
	}
	for _, r := range rows {
		decodeRow(r, t)
	}
}

// tableFor returns the registered Table for name, or nil when name is empty or
// unregistered.
func tableFor(table string) *Table {
	if table == "" {
		return nil
	}
	return GetTableByName(table)
}

// decodeRow binds table/primary-key metadata to r and decodes its JSON columns.
func decodeRow(r *Row, t *Table) {
	r.SetTable(t.Name)
	if len(t.PrimaryKeys) > 0 {
		r.SetPrimaryKeys(t.PrimaryKeys...)
	}
	DecodeJSONFields(r)
}

// bytesToStr converts []byte to string to avoid Base64 encoding in JSON serialization.
// MySQL driver returns []byte for string columns when scanning into interface{}.
func bytesToStr(v interface{}) interface{} {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return v
}

// isBinaryColumnType returns true for database column types that store binary data.
// BLOBs and BINARY types should remain as []byte, not be converted to string.
func isBinaryColumnType(dbType string) bool {
	switch strings.ToUpper(dbType) {
	case "BLOB", "TINYBLOB", "MEDIUMBLOB", "LONGBLOB",
		"BINARY", "VARBINARY", "BIT",
		"BYTEA":
		return true
	}
	return false
}
