package db

// Use returns the default Dao.
func Use() *Dao {
	return GetConfig().CreateDao()
}

// UseWithID returns a Dao for the given config ID.
func UseWithID(configID string) *Dao {
	return GetConfig(configID).CreateDao()
}

// RawSql creates a Dao with the given raw SQL query.
func RawSql(query string, args ...interface{}) *Dao {
	return Use().RawSql(query, args...)
}

// Sql creates a Dao with an Enjoy SQL template and named parameters.
func Sql(sqlStr string, data map[string]interface{}) *Dao {
	return Use().Sql(sqlStr, data)
}

// SqlWithArgs creates a Dao with an Enjoy SQL template and positional arguments.
func SqlWithArgs(sqlStr string, args ...interface{}) *Dao {
	return Use().SqlWithArgs(sqlStr, args...)
}

// SqlById creates a Dao with a cached SQL template by ID and named parameters.
func SqlById(sqlID string, data map[string]interface{}) *Dao {
	return Use().SqlById(sqlID, data)
}

// SqlByIdWithArgs creates a Dao with a cached SQL template by ID and positional arguments.
func SqlByIdWithArgs(sqlID string, args ...interface{}) *Dao {
	return Use().SqlByIdWithArgs(sqlID, args...)
}

// AddSql adds a named SQL template to the default config's SqlKit.
func AddSql(sqlID, sql string) {
	GetConfig().GetSqlKit().AddSql(sqlID, sql)
}

// AddSqlWithID adds a named SQL template to a specific config's SqlKit.
func AddSqlWithID(configID, sqlID, sql string) {
	GetConfig(configID).GetSqlKit().AddSql(sqlID, sql)
}

// Select creates a Dao with the given select fields.
func Select(fields string) *Dao {
	return Use().Select(fields)
}

// Insert inserts a row.
func Insert(row *Row) (*Row, error) {
	return Use().InsertRow(row)
}

// InsertOrUpdate inserts or updates a row.
func InsertOrUpdate(row *Row) (*Row, error) {
	return Use().InsertOrUpdateRow(row)
}

// Update updates a row.
func Update(row *Row) (bool, error) {
	return Use().UpdateRow(row)
}

// Delete deletes a row.
func Delete(row *Row) (bool, error) {
	return Use().DeleteRow(row)
}

// DeleteByID deletes a row by table and ID.
func DeleteByID(table string, id interface{}) (bool, error) {
	return Use().DeleteByID(table, id)
}

// DeleteByIDWithPK deletes by table, PK name, and ID.
func DeleteByIDWithPK(table, pk string, id interface{}) (bool, error) {
	return Use().DeleteByIDWithPK(table, pk, id)
}

// DeleteBy deletes rows by condition.
func DeleteBy(table, whereOrField string, args ...interface{}) (int64, error) {
	return Use().DeleteBy(table, whereOrField, args...)
}

// FindByID finds a row by table and ID.
func FindByID(table string, id interface{}) (*Row, error) {
	return Use().FindByID(table, id)
}

// FindByIDWithPK finds by table, PK name, and ID.
func FindByIDWithPK(table, pk string, id interface{}) (*Row, error) {
	return Use().FindByIDWithPK(table, pk, id)
}

// FindBy finds rows by condition.
func FindBy(table, whereOrField string, args ...interface{}) ([]*Row, error) {
	return Use().FindBy(table, whereOrField, args...)
}

// FindFirstBy finds the first row by condition.
func FindFirstBy(table, whereOrField string, args ...interface{}) (*Row, error) {
	return Use().FindFirstBy(table, whereOrField, args...)
}

// FindIn finds rows where field IN values.
func FindIn(table, field string, values ...interface{}) ([]*Row, error) {
	return Use().FindIn(table, field, values...)
}

// Count counts all rows.
func Count(table string) (int64, error) {
	return Use().Count(table)
}

// CountBy counts rows by condition.
func CountBy(table, whereOrField string, args ...interface{}) (int64, error) {
	return Use().CountBy(table, whereOrField, args...)
}

// NewBatch returns a Batch instance.
func NewBatch() *Batch {
	return &Batch{config: GetConfig()}
}

// Transaction executes a function in a transaction.
func Transaction(fn func() error) error {
	return TransactionWithID(defaultConfigID, fn)
}
