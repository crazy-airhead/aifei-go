package db

import dbsql "github.com/crazy-airhead/aifei-go/db/sql"

// InsertHook intercepts Row-based insert operations.
//
// Use cases: auto-fill created_time/updated_time, post-insert cache/async actions.
type InsertHook interface {
	BeforeRowInsert(dao *Dao, row *Row) interface{}
	AfterRowInsert(dao *Dao, row *Row, fromBefore interface{})
}

// DeleteHook intercepts delete operations in two modes:
//   - Sql-based: for DeleteBy, DeleteIn, DeleteByID (raw SQL delete)
//   - Row-based: for DeleteRow (Row instance delete)
//
// Use cases: soft delete, archive deleted data, prevent missing WHERE clause.
type DeleteHook interface {
	BeforeSqlDelete(dao *Dao) interface{}
	AfterSqlDelete(dao *Dao, ret int64, fromBefore interface{})
	BeforeRowDelete(dao *Dao, row *Row) interface{}
	AfterRowDelete(dao *Dao, row *Row, fromBefore interface{})
}

// UpdateHook intercepts update operations in two modes:
//   - Sql-based: for Dao.Update() (raw DML execution)
//   - Row-based: for UpdateRow (Row instance update)
//
// Use cases: auto-fill updated_time, cache eviction, change logging.
type UpdateHook interface {
	BeforeSqlUpdate(dao *Dao) interface{}
	AfterSqlUpdate(dao *Dao, ret int64, fromBefore interface{})
	BeforeRowUpdate(dao *Dao, row *Row) interface{}
	AfterRowUpdate(dao *Dao, row *Row, fromBefore interface{})
}

// FindHook fires for ALL read operations: Find, FindBy, FindByID, FindAll,
// FindIn, and their First variants.
//
// Use cases: block SELECT *, measure slow queries.
type FindHook interface {
	BeforeFind(dao *Dao) interface{}
	AfterFind(dao *Dao, rows []*Row, fromBefore interface{})
}

// QueryHook fires from Find() only when the user explicitly set raw SQL
// (via Dao.RawSql() or Dao.Sql()), and from Query() methods.
// The result parameter is interface{} because Query returns raw []interface{}
// while Find returns []*Row.
type QueryHook interface {
	BeforeQuery(dao *Dao) interface{}
	AfterQuery(dao *Dao, result interface{}, fromBefore interface{})
}

// PaginateHook intercepts paginated queries.
//
// Use cases: cache totalRows, modify pagination SQL, log slow pagination queries.
type PaginateHook interface {
	BeforeQueryTotalRows(dao *Dao, sqlPara *dbsql.SqlPara) interface{}
	AfterQueryTotalRows(dao *Dao, ret int64, fromBefore interface{})
	BeforePaginate(dao *Dao, pageNum, pageSize int, totalRows int64, sqlPara *dbsql.SqlPara) interface{}
	AfterPaginate(dao *Dao, page *Page, fromBefore interface{})
}

// DbHookKit holds all hook implementations for a Config.
// All fields default to nil (no-op). Set individual hooks as needed.
type DbHookKit struct {
	InsertHook   InsertHook
	DeleteHook   DeleteHook
	UpdateHook   UpdateHook
	FindHook     FindHook
	QueryHook    QueryHook
	PaginateHook PaginateHook
}
