package db

import (
	dbsql "github.com/crazy-airhead/aifei-go/db/sql"
)

// Dao provides chainable database operations.
type Dao struct {
	config    *Config
	sqlStr    string
	sqlArgs   []interface{}
	selFields string
	fromTable string
	sqlPara   *dbsql.SqlPara
}

// ---- Builder methods (set SQL on Dao, no execution) ----

func (d *Dao) SQL(query string, args ...interface{}) *Dao {
	d.sqlStr = query
	d.sqlArgs = args
	return d
}

func (d *Dao) Select(fields string) *Dao {
	d.selFields = fields
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
