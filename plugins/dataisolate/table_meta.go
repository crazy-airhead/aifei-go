package dataisolate

import (
	"sync"

	"github.com/crazy-airhead/aifei-go/db"
)

// TableMeta names the identity columns of a table (tenant / creator / dept / region).
// It is stable and schema-bound: register once at startup via RegisterTableMeta, or let
// TableMetaOf discover the columns by convention against the registered db.Table.
type TableMeta struct {
	TenantCol  string
	CreatorCol string
	DeptCol    string
	RegionCol  string
}

var (
	tableMetaMu       sync.RWMutex
	tableMetaRegistry = map[string]TableMeta{}
)

// RegisterTableMeta registers the identity columns for a table.
func RegisterTableMeta(table string, m TableMeta) {
	tableMetaMu.Lock()
	defer tableMetaMu.Unlock()
	tableMetaRegistry[table] = m
}

// TableMetaOf resolves a table's identity columns: explicit registration first, then
// convention matching against db.Table.FieldTypes, else an empty TableMeta.
func TableMetaOf(table string) TableMeta {
	tableMetaMu.RLock()
	if m, ok := tableMetaRegistry[table]; ok {
		tableMetaMu.RUnlock()
		return m
	}
	tableMetaMu.RUnlock()
	return conventionMeta(table)
}

// conventionMeta infers identity columns from the registered table's FieldTypes when no
// explicit TableMeta was registered. Non-conventional column names require
// RegisterTableMeta.
func conventionMeta(table string) TableMeta {
	t := db.GetTableByName(table)
	if t == nil || t.FieldTypes == nil {
		return TableMeta{}
	}
	has := func(names ...string) string {
		for _, n := range names {
			if _, ok := t.FieldTypes[n]; ok {
				return n
			}
		}
		return ""
	}
	return TableMeta{
		TenantCol:  has("tenant_id", "tenant_code", "tid"),
		CreatorCol: has("creator_id", "created_by", "create_by", "owner_id", "creator"),
		DeptCol:    has("dept_id", "dept_code", "department_id", "org_id"),
		RegionCol:  has("region_id", "region_code", "area_id"),
	}
}

// tableHasColumn reports whether col is a registered column of table.
func tableHasColumn(table, col string) bool {
	if table == "" || col == "" {
		return false
	}
	t := db.GetTableByName(table)
	if t == nil || t.FieldTypes == nil {
		return false
	}
	_, ok := t.FieldTypes[col]
	return ok
}
