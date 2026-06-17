package db

import (
	dbsql "github.com/crazy-airhead/aifei-go/db/sql"
)

// Dao provides chainable database operations.
type Dao struct {
	config     *Config
	sqlStr     string
	sqlArgs    []interface{}
	selFields  string
	fromTable  string
	sqlPara    *dbsql.SqlPara
	hasGroupBy bool
}

// ---- Builder methods (set SQL on Dao, no execution) ----

func (d *Dao) RawSql(query string, args ...interface{}) *Dao {
	d.sqlStr = query
	d.sqlArgs = args
	return d
}

func (d *Dao) Select(fields string) *Dao {
	d.selFields = fields
	return d
}

func (d *Dao) HasGroupBy(b bool) *Dao {
	d.hasGroupBy = b
	return d
}

func (d *Dao) Sql0(sqlStr string) *Dao {
	d.sqlPara = d.config.GetSqlKit().GetSqlParaWithArgs(sqlStr)
	return d
}

func (d *Dao) Sql(sqlStr string, data map[string]interface{}) *Dao {
	d.sqlPara = d.config.GetSqlKit().GetSqlPara(sqlStr, data)
	return d
}

func (d *Dao) SqlWithArgs(sqlStr string, args ...interface{}) *Dao {
	d.sqlPara = d.config.GetSqlKit().GetSqlParaWithArgs(sqlStr, args...)
	return d
}

func (d *Dao) SqlById0(sqlID string) *Dao {
	d.sqlPara = d.config.GetSqlKit().GetSqlParaByIDWithArgs(sqlID)
	return d
}

func (d *Dao) SqlById(sqlID string, data map[string]interface{}) *Dao {
	d.sqlPara = d.config.GetSqlKit().GetSqlParaByID(sqlID, data)
	return d
}

func (d *Dao) SqlByIdWithArgs(sqlID string, args ...interface{}) *Dao {
	d.sqlPara = d.config.GetSqlKit().GetSqlParaByIDWithArgs(sqlID, args...)
	return d
}

func (d *Dao) SqlPara(sp *dbsql.SqlPara) *Dao {
	d.sqlPara = sp
	return d
}

// ---- Internal helpers ----

func (d *Dao) setSqlPara(sp *dbsql.SqlPara) {
	d.sqlPara = sp
}

func (d *Dao) sqlAndArgs() (string, []interface{}) {
	if d.sqlPara != nil {
		return d.sqlPara.Sql, d.sqlPara.Paras
	}
	return d.sqlStr, d.sqlArgs
}

func (d *Dao) isRawSQL() bool {
	return d.sqlStr != "" || d.sqlPara != nil
}

// ---- Query methods (delegate to executors) ----

func (d *Dao) Find() ([]*Row, error) {
	return execFind(d, d.isRawSQL())
}

func (d *Dao) FindFirst() (*Row, error) {
	results, err := d.Find()
	if err != nil || len(results) == 0 {
		return nil, err
	}
	return results[0], nil
}

func (d *Dao) FindByID(table string, id interface{}) (*Row, error) {
	return execFindById(d, table, "id", id)
}

func (d *Dao) FindByIDWithPK(table, pk string, id interface{}) (*Row, error) {
	return execFindById(d, table, pk, id)
}

func (d *Dao) FindBy(table, whereOrField string, args ...interface{}) ([]*Row, error) {
	return execFindByCondition(d, table, whereOrField, args)
}

func (d *Dao) FindFirstBy(table, whereOrField string, args ...interface{}) (*Row, error) {
	results, err := d.FindBy(table, whereOrField, args...)
	if err != nil || len(results) == 0 {
		return nil, err
	}
	return results[0], nil
}

func (d *Dao) FindAll(table string) ([]*Row, error) {
	return execFindAll(d, table)
}

func (d *Dao) FindIn(table, field string, values ...interface{}) ([]*Row, error) {
	return execFindIn(d, table, field, values)
}

func (d *Dao) Paginate(pageNum, pageSize int) (*Page, error) {
	return execPaginate(d, pageNum, pageSize)
}

func (d *Dao) PaginateWithTotalRows(pageNum, pageSize int, totalRowsFn func(sqlPara *dbsql.SqlPara, defaultQuery func() (int64, error)) (int64, error)) (*Page, error) {
	return execPaginateWithTotalRows(d, pageNum, pageSize, totalRowsFn)
}

// ---- DML methods ----

func (d *Dao) Update() (int64, error) {
	return execSqlUpdate(d)
}

func (d *Dao) InsertRow(row *Row) (*Row, error) {
	return execInsertRow(d, row)
}

func (d *Dao) InsertOrUpdateRow(row *Row) (*Row, error) {
	return execInsertOrUpdateRow(d, row)
}

func (d *Dao) UpdateRow(row *Row) (bool, error) {
	return execUpdateRow(d, row)
}

func (d *Dao) DeleteRow(row *Row) (bool, error) {
	return execDeleteRow(d, row)
}

func (d *Dao) DeleteByID(table string, id interface{}) (bool, error) {
	return execDeleteById(d, table, "id", id)
}

func (d *Dao) DeleteByIDWithPK(table, pk string, id interface{}) (bool, error) {
	return execDeleteById(d, table, pk, id)
}

func (d *Dao) DeleteBy(table, whereOrField string, args ...interface{}) (int64, error) {
	return execDeleteBy(d, table, whereOrField, args)
}

func (d *Dao) DeleteIn(table, field string, values ...interface{}) (int64, error) {
	return execDeleteIn(d, table, field, values)
}

// ---- Aggregation methods ----

func (d *Dao) Count(table string) (int64, error) {
	return execCount(d, table)
}

func (d *Dao) CountBy(table, whereOrField string, args ...interface{}) (int64, error) {
	return execCountBy(d, table, whereOrField, args)
}

// ---- Advanced query methods ----

func (d *Dao) FindOne() (*Row, error) {
	return execFindOne(d, false)
}

func (d *Dao) FindOneOrNull() (*Row, error) {
	return execFindOne(d, true)
}

func (d *Dao) FindExists() (bool, error) {
	return execFindExists(d)
}

func (d *Dao) ForEach(fn func(*Row) bool) error {
	return execForEach(d, fn)
}

// ---- Pagination extensions ----

func (d *Dao) ForEachPage(pageSize int, fn func(*Page) bool) error {
	return execForEachPage(d, pageSize, fn)
}

func (d *Dao) ForEachPageRange(startPageNum, endPageNum, pageSize int, fn func(*Page) bool) error {
	return execForEachPageRange(d, startPageNum, endPageNum, pageSize, fn)
}

// ---- Raw query methods ----

func (d *Dao) Query() ([]interface{}, error) {
	return execQuery(d, false)
}

func (d *Dao) QueryFirst() (interface{}, error) {
	result, err := execQuery(d, true)
	if err != nil || len(result) == 0 {
		return nil, err
	}
	return result[0], nil
}

func (d *Dao) QueryOne() (interface{}, error) {
	return execQueryOne(d, false)
}

func (d *Dao) QueryOneOrNull() (interface{}, error) {
	return execQueryOne(d, true)
}

func (d *Dao) QueryField() (interface{}, error) {
	return execQueryField(d)
}

func (d *Dao) QueryStr() (string, error) {
	v, err := d.QueryField()
	if err != nil || v == nil {
		return "", err
	}
	return ToString(v), nil
}

func (d *Dao) QueryInt() (int, error) {
	v, err := d.QueryField()
	if err != nil || v == nil {
		return 0, err
	}
	return ToInt(v), nil
}

func (d *Dao) QueryInt64() (int64, error) {
	v, err := d.QueryField()
	if err != nil || v == nil {
		return 0, err
	}
	return ToInt64(v), nil
}

func (d *Dao) QueryFloat64() (float64, error) {
	v, err := d.QueryField()
	if err != nil || v == nil {
		return 0, err
	}
	return ToFloat64(v), nil
}

func (d *Dao) QueryTime() (interface{}, error) {
	return execQueryTime(d)
}

func (d *Dao) QueryBytes() ([]byte, error) {
	return execQueryBytes(d)
}

func (d *Dao) QueryBool() (bool, error) {
	v, err := d.QueryField()
	if err != nil || v == nil {
		return false, err
	}
	return ToBool(v), nil
}

// ---- ByID extensions ----

func (d *Dao) DeleteByCompositeId(table, key1, key2 string, id1, id2 interface{}) (bool, error) {
	return execDeleteByCompositeId(d, table, []string{key1, key2}, []interface{}{id1, id2})
}

func (d *Dao) FindByCompositeId(table, key1, key2 string, id1, id2 interface{}) (*Row, error) {
	return execFindByCompositeId(d, table, []string{key1, key2}, []interface{}{id1, id2})
}

func (d *Dao) DeleteInIds(table string, ids ...interface{}) (int64, error) {
	return execDeleteInIds(d, table, ids)
}

func (d *Dao) FindInIds(table string, ids ...interface{}) ([]*Row, error) {
	return execFindInIds(d, table, ids)
}

// ---- Transaction ----

func (d *Dao) Transaction(fn func(*Dao) error) error {
	return execTransaction(d, fn)
}
