package db

import (
	"context"

	dbsql "github.com/crazy-airhead/aifei-go/db/sql"
)

// Dao provides chainable database operations.
type Dao struct {
	config     *Config
	ctx        context.Context // propagated tx context; nil → pool (default)
	sqlStr     string
	sqlArgs    []interface{}
	selFields  string
	fromTable  string
	sqlPara    *dbsql.SqlPara
	hasGroupBy bool
	table      string     // single-table hint (existing)
	multi      []TableRef // multi-table explicit hint (new)
	autoTables bool       // auto-parse switch (new)
	failErr    error      // hook veto: when non-nil, runner() returns it, aborting the statement
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

// Table declares the table that raw-SQL result rows belong to. When set, result
// rows carry table/primary-key metadata and declared JSON columns (see
// Table.FieldTypes) are decoded into their Go types — so typed accessors and the
// wire format match the model DAO without per-call decoding at the call site.
// Builder queries (FindBy, FindByID, FindAll, FindIn, ...) set this automatically
// from their table argument. No-op for unregistered tables.
func (d *Dao) Table(name string) *Dao {
	d.table = name
	return d
}

// Tables declares the tables involved in a multi-table query, with optional aliases.
// The first element is the primary table (determines row.Table() and write path).
// Overrides Table(). No-op for unregistered tables.
func (d *Dao) Tables(refs ...TableRef) *Dao {
	d.multi = append(d.multi[:0], refs...)
	return d
}

// AutoTables enables automatic SQL parsing for multi-table mapping on this Dao.
// When set, the Dao parses the rendered SQL at execution time to discover table
// references and build column-to-table mappings. Requires tables to be registered.
func (d *Dao) AutoTables() *Dao {
	d.autoTables = true
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

// Ctx binds a context to the Dao so its executors participate in any
// transaction carried by ctx (see Transaction / WithTx). A Dao without a ctx
// (the default) always uses the connection pool, matching pre-existing behavior.
func (d *Dao) Ctx(ctx context.Context) *Dao {
	d.ctx = ctx
	return d
}

// runner returns the DBConn this Dao executes on: the *sql.Tx from ctx when
// inside a transaction, otherwise the pool. It honors a hook veto: when Fail
// has been called, it returns the veto error instead of a connection, so every
// executor (which already checks runner's error) aborts the statement.
func (d *Dao) runner() (DBConn, error) {
	if d.failErr != nil {
		return nil, d.failErr
	}
	return d.config.runner(d.ctx)
}

// Context returns the context bound to this Dao (set by Dao.Ctx / db.WithCtx;
// carries *sql.Tx when inside a transaction). Hooks read the Principal from it.
// Returns nil when unbound.
func (d *Dao) Context() context.Context { return d.ctx }

// SqlAndArgs returns the currently staged SQL and args that will be dispatched
// (the exported counterpart of the internal sqlAndArgs). Hooks inspect/rewrite
// the statement before execution, then write the result back via Dao.SqlPara.
func (d *Dao) SqlAndArgs() (string, []interface{}) {
	return d.sqlAndArgs()
}

// Fail marks this Dao's statement to be aborted. A Before hook calls it when it
// cannot safely rewrite the statement (fail-closed); the next runner() returns
// the error and the executor propagates it to the caller. Only the first error
// is kept.
func (d *Dao) Fail(err error) {
	if d.failErr == nil {
		d.failErr = err
	}
}

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
	return execFindOne(d, false, nil)
}

// FindOneWithMsg is like FindOne but builds the not-one error message from msgFn,
// invoked with the actual result count (对照 Java Dao.findOne(Function)).
//
//	Db.sql("select * from orders where id = ?", id).FindOneWithMsg(func(n int) string {
//	    return fmt.Sprintf("订单数必须为 1，不能为 %d", n)
//	})
func (d *Dao) FindOneWithMsg(msgFn func(int) string) (*Row, error) {
	return execFindOne(d, false, msgFn)
}

func (d *Dao) FindOneOrNull() (*Row, error) {
	return execFindOne(d, true, nil)
}

// FindOneOrNullWithMsg is like FindOneOrNull but builds the too-many error message
// from msgFn (invoked with the count). Zero results still return (nil, nil).
func (d *Dao) FindOneOrNullWithMsg(msgFn func(int) string) (*Row, error) {
	return execFindOne(d, true, msgFn)
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

// QueryFieldOr is like QueryField but returns def when the query yields no value
// (对照 Java Dao.queryField(T defaultValue)).
func (d *Dao) QueryFieldOr(def interface{}) (interface{}, error) {
	v, err := d.QueryField()
	if err != nil || v == nil {
		return def, err
	}
	return v, nil
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

// FindByCompositeIds finds a row by a composite primary key of arbitrary arity
// (对照 Java Dao.findByCompositeId(String, String[], Object[])). keys and ids must
// have equal, non-zero length.
func (d *Dao) FindByCompositeIds(table string, keys []string, ids ...interface{}) (*Row, error) {
	return execFindByCompositeId(d, table, keys, ids)
}

// DeleteByCompositeIds deletes by a composite primary key of arbitrary arity.
func (d *Dao) DeleteByCompositeIds(table string, keys []string, ids ...interface{}) (bool, error) {
	return execDeleteByCompositeId(d, table, keys, ids)
}

func (d *Dao) DeleteInIds(table string, ids ...interface{}) (int64, error) {
	return execDeleteInIds(d, table, ids)
}

func (d *Dao) FindInIds(table string, ids ...interface{}) ([]*Row, error) {
	return execFindInIds(d, table, ids)
}

// ---- Transaction ----

// Transaction executes fn in a transaction on this Dao's config, propagating
// the *sql.Tx to fn via context so every db call inside fn that uses the
// ctx-aware dao d (or db.WithCtx(ctx)) participates in the same transaction.
// Nested calls join the outer transaction. See the package-level Transaction
// for full semantics.
func (d *Dao) Transaction(fn func(ctx context.Context, d *Dao) error) error {
	return execTransaction(d, fn)
}

// DaoTransactionOf is the generic counterpart of Dao.Transaction: it returns the
// atom's typed business result and honors active rollback (tx.Rollback() or a
// RollbackDecision result). Because Go does not permit generic methods, it is a
// free function taking the Dao. For most call sites prefer the package-level
// db.TransactionOf with db.WithCtx(ctx).
func DaoTransactionOf[R any](d *Dao, fn func(ctx context.Context, d *Dao, tx *Tx) (R, error)) (R, error) {
	return execTransactionOf(d, fn)
}
