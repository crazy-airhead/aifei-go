package db

// This file hosts the Go 1.27 generic-method query terminals (see
// docs/arch/generic-methods.md). Before Go allowed type parameters on
// methods, typed query results required a per-table generated wrapper that
// re-declared the chainable methods just to narrow the return type. With
// FindAs/FindFirstAs/FindOneAs/PaginateAs the chain stays on *Dao and the
// terminal wraps rows into typed models directly.

// RowEntity is the constraint satisfied by the generated Base types (and any
// hand-written model): InitRow adopts a queried *db.Row — stamping table and
// primary-key metadata and decoding JSON columns — so the generic terminals
// can construct the model from a row.
type RowEntity interface {
	InitRow(row *Row)
}

// wrapRows adapts queried rows to typed models via the T/PT idiom: T is the
// model struct, PT (inferred as *T) carries the InitRow constraint.
func wrapRows[T any, PT interface {
	*T
	RowEntity
}](rows []*Row) []PT {
	out := make([]PT, len(rows))
	for i, row := range rows {
		if row == nil {
			continue
		}
		var t T
		pt := PT(&t)
		pt.InitRow(row)
		out[i] = pt
	}
	return out
}

// FindAs executes the query and wraps all matching rows into typed models.
//
//	db.Use().Table("user").Where("age > ?", 18) — chain as usual on *db.Dao,
//	then terminate typed:
//
//	users, err := dao.FindAs[User]() // []*User (PT inferred as *User)
//
// The model must satisfy RowEntity; the generated Base types do via InitRow.
func (d *Dao) FindAs[T any, PT interface {
	*T
	RowEntity
}]() ([]PT, error) {
	rows, err := d.Find()
	if err != nil {
		return nil, err
	}
	return wrapRows[T, PT](rows), nil
}

// FindFirstAs executes the query and wraps the first matching row into a
// typed model. No match yields (nil, nil), matching FindFirst.
func (d *Dao) FindFirstAs[T any, PT interface {
	*T
	RowEntity
}]() (PT, error) {
	row, err := d.FindFirst()
	if err != nil || row == nil {
		var zero PT
		return zero, err
	}
	var t T
	pt := PT(&t)
	pt.InitRow(row)
	return pt, nil
}

// FindOneAs executes the query and wraps the single matching row into a typed
// model; a result count other than one is an error (same contract as
// FindOne).
func (d *Dao) FindOneAs[T any, PT interface {
	*T
	RowEntity
}]() (PT, error) {
	row, err := d.FindOne()
	if err != nil || row == nil {
		var zero PT
		return zero, err
	}
	var t T
	pt := PT(&t)
	pt.InitRow(row)
	return pt, nil
}

// PaginateAs executes the query and returns a typed page. The returned
// *PageAs[PT] carries []*-models in Rows, consistent with FindAs ([]PT).
//
//	page, err := dao.PaginateAs[User](1, 10) // *PageAs[*User]
func (d *Dao) PaginateAs[T any, PT interface {
	*T
	RowEntity
}](pageNum, pageSize int) (*PageAs[PT], error) {
	page, err := d.Paginate(pageNum, pageSize)
	if err != nil {
		return nil, err
	}
	out := &PageAs[PT]{
		PageNum:    page.PageNum,
		PageSize:   page.PageSize,
		TotalRows:  page.TotalRows,
		TotalPages: page.TotalPages,
		Rows:      make([]PT, 0, len(page.Rows)),
	}
	for _, row := range page.Rows {
		if row == nil {
			continue
		}
		var t T
		pt := PT(&t)
		pt.InitRow(row)
		out.Rows = append(out.Rows, pt)
	}
	return out, nil
}

// FindByAs queries by a where clause or a single field (with arguments) and
// wraps all matching rows into typed models — the typed counterpart of
// FindBy, shaped for the generated table packages.
func (d *Dao) FindByAs[T any, PT interface {
	*T
	RowEntity
}](table, whereOrField string, args ...interface{}) ([]PT, error) {
	rows, err := d.FindBy(table, whereOrField, args...)
	if err != nil {
		return nil, err
	}
	return wrapRows[T, PT](rows), nil
}

// FindFirstByAs queries the first row by a where clause or a single field and
// wraps it into a typed model. No match yields (nil, nil), matching
// FindFirstBy.
func (d *Dao) FindFirstByAs[T any, PT interface {
	*T
	RowEntity
}](table, whereOrField string, args ...interface{}) (PT, error) {
	row, err := d.FindFirstBy(table, whereOrField, args...)
	if err != nil || row == nil {
		var zero PT
		return zero, err
	}
	var t T
	pt := PT(&t)
	pt.InitRow(row)
	return pt, nil
}

// FindByIDAs loads a single row by its primary key (column "id"; use the
// plain FindByIDWithPK for a custom pk column) and wraps it into a typed
// model. No match yields (nil, nil).
func (d *Dao) FindByIDAs[T any, PT interface {
	*T
	RowEntity
}](table string, id interface{}) (PT, error) {
	row, err := d.FindByID(table, id)
	if err != nil || row == nil {
		var zero PT
		return zero, err
	}
	var t T
	pt := PT(&t)
	pt.InitRow(row)
	return pt, nil
}
