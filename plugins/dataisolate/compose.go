package dataisolate

import (
	"github.com/crazy-airhead/aifei-go/db"
	dbsql "github.com/crazy-airhead/aifei-go/db/sql"
)

// mergeHookKits composes an existing DbHookKit with the dataisolate hookKit. The
// dataisolate hooks always run (they are the point), layered on top of any pre-existing
// hooks: for each Before* the existing hook runs first and its before-state is preserved
// for the matching After*; the dataisolate Before* then runs (its effect is the
// sqlPara/row side effect, not its return value). When existing is nil, the hookKit is
// used directly for all six interfaces.
func mergeHookKits(existing *db.DbHookKit, mine *hookKit) *db.DbHookKit {
	if existing == nil {
		return &db.DbHookKit{
			InsertHook:   mine,
			DeleteHook:   mine,
			UpdateHook:   mine,
			FindHook:     mine,
			QueryHook:    mine,
			PaginateHook: mine,
		}
	}
	m := &mergedHookKit{existing: existing, mine: mine}
	return &db.DbHookKit{
		InsertHook:   m,
		DeleteHook:   m,
		UpdateHook:   m,
		FindHook:     m,
		QueryHook:    m,
		PaginateHook: m,
	}
}

// mergedHookKit dispatches each callback to the existing hook (when present) and to the
// dataisolate hook. It implements all six DbHookKit interfaces.
type mergedHookKit struct {
	existing *db.DbHookKit
	mine     *hookKit
}

// ---- InsertHook ----

func (m *mergedHookKit) BeforeRowInsert(dao *db.Dao, row *db.Row) interface{} {
	var x interface{}
	if m.existing.InsertHook != nil {
		x = m.existing.InsertHook.BeforeRowInsert(dao, row)
	}
	m.mine.BeforeRowInsert(dao, row)
	return x
}
func (m *mergedHookKit) AfterRowInsert(dao *db.Dao, row *db.Row, fromBefore interface{}) {
	if m.existing.InsertHook != nil {
		m.existing.InsertHook.AfterRowInsert(dao, row, fromBefore)
	}
	m.mine.AfterRowInsert(dao, row, nil)
}

// ---- DeleteHook ----

func (m *mergedHookKit) BeforeSqlDelete(dao *db.Dao) interface{} {
	var x interface{}
	if m.existing.DeleteHook != nil {
		x = m.existing.DeleteHook.BeforeSqlDelete(dao)
	}
	m.mine.BeforeSqlDelete(dao)
	return x
}
func (m *mergedHookKit) AfterSqlDelete(dao *db.Dao, ret int64, fromBefore interface{}) {
	if m.existing.DeleteHook != nil {
		m.existing.DeleteHook.AfterSqlDelete(dao, ret, fromBefore)
	}
	m.mine.AfterSqlDelete(dao, ret, nil)
}
func (m *mergedHookKit) BeforeRowDelete(dao *db.Dao, row *db.Row) interface{} {
	var x interface{}
	if m.existing.DeleteHook != nil {
		x = m.existing.DeleteHook.BeforeRowDelete(dao, row)
	}
	m.mine.BeforeRowDelete(dao, row)
	return x
}
func (m *mergedHookKit) AfterRowDelete(dao *db.Dao, row *db.Row, fromBefore interface{}) {
	if m.existing.DeleteHook != nil {
		m.existing.DeleteHook.AfterRowDelete(dao, row, fromBefore)
	}
	m.mine.AfterRowDelete(dao, row, nil)
}

// ---- UpdateHook ----

func (m *mergedHookKit) BeforeSqlUpdate(dao *db.Dao) interface{} {
	var x interface{}
	if m.existing.UpdateHook != nil {
		x = m.existing.UpdateHook.BeforeSqlUpdate(dao)
	}
	m.mine.BeforeSqlUpdate(dao)
	return x
}
func (m *mergedHookKit) AfterSqlUpdate(dao *db.Dao, ret int64, fromBefore interface{}) {
	if m.existing.UpdateHook != nil {
		m.existing.UpdateHook.AfterSqlUpdate(dao, ret, fromBefore)
	}
	m.mine.AfterSqlUpdate(dao, ret, nil)
}
func (m *mergedHookKit) BeforeRowUpdate(dao *db.Dao, row *db.Row) interface{} {
	var x interface{}
	if m.existing.UpdateHook != nil {
		x = m.existing.UpdateHook.BeforeRowUpdate(dao, row)
	}
	m.mine.BeforeRowUpdate(dao, row)
	return x
}
func (m *mergedHookKit) AfterRowUpdate(dao *db.Dao, row *db.Row, fromBefore interface{}) {
	if m.existing.UpdateHook != nil {
		m.existing.UpdateHook.AfterRowUpdate(dao, row, fromBefore)
	}
	m.mine.AfterRowUpdate(dao, row, nil)
}

// ---- FindHook ----

func (m *mergedHookKit) BeforeFind(dao *db.Dao) interface{} {
	var x interface{}
	if m.existing.FindHook != nil {
		x = m.existing.FindHook.BeforeFind(dao)
	}
	m.mine.BeforeFind(dao)
	return x
}
func (m *mergedHookKit) AfterFind(dao *db.Dao, rows []*db.Row, fromBefore interface{}) {
	if m.existing.FindHook != nil {
		m.existing.FindHook.AfterFind(dao, rows, fromBefore)
	}
	m.mine.AfterFind(dao, rows, nil)
}

// ---- QueryHook ----

func (m *mergedHookKit) BeforeQuery(dao *db.Dao) interface{} {
	var x interface{}
	if m.existing.QueryHook != nil {
		x = m.existing.QueryHook.BeforeQuery(dao)
	}
	m.mine.BeforeQuery(dao)
	return x
}
func (m *mergedHookKit) AfterQuery(dao *db.Dao, result interface{}, fromBefore interface{}) {
	if m.existing.QueryHook != nil {
		m.existing.QueryHook.AfterQuery(dao, result, fromBefore)
	}
	m.mine.AfterQuery(dao, result, nil)
}

// ---- PaginateHook ----

func (m *mergedHookKit) BeforeQueryTotalRows(dao *db.Dao, sp *dbsql.SqlPara) interface{} {
	var x interface{}
	if m.existing.PaginateHook != nil {
		x = m.existing.PaginateHook.BeforeQueryTotalRows(dao, sp)
	}
	m.mine.BeforeQueryTotalRows(dao, sp)
	return x
}
func (m *mergedHookKit) AfterQueryTotalRows(dao *db.Dao, ret int64, fromBefore interface{}) {
	if m.existing.PaginateHook != nil {
		m.existing.PaginateHook.AfterQueryTotalRows(dao, ret, fromBefore)
	}
	m.mine.AfterQueryTotalRows(dao, ret, nil)
}
func (m *mergedHookKit) BeforePaginate(dao *db.Dao, pageNum, pageSize int, totalRows int64, sp *dbsql.SqlPara) interface{} {
	var x interface{}
	if m.existing.PaginateHook != nil {
		x = m.existing.PaginateHook.BeforePaginate(dao, pageNum, pageSize, totalRows, sp)
	}
	m.mine.BeforePaginate(dao, pageNum, pageSize, totalRows, sp)
	return x
}
func (m *mergedHookKit) AfterPaginate(dao *db.Dao, page *db.Page, fromBefore interface{}) {
	if m.existing.PaginateHook != nil {
		m.existing.PaginateHook.AfterPaginate(dao, page, fromBefore)
	}
	m.mine.AfterPaginate(dao, page, nil)
}
