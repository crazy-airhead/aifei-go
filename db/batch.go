package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	dbsql "github.com/crazy-airhead/aifei-go/db/sql"
)

// BatchResult holds batch operation results.
type BatchResult struct {
	RowsAffected int64

	// UpdateCounts holds the per-statement affected row count, in execution
	// order (对照 Java BatchResult.updateCounts). For heterogeneous batches the
	// order follows group execution, not the original input order.
	UpdateCounts []int64

	// GeneratedKeys holds auto-generated primary key values collected from
	// inserts when Batch.GetGeneratedKeys(true) is set (对照 Java
	// BatchResult.generatedKeys). Driver-dependent: relies on sql.Result.
	// LastInsertId(), which MySQL and SQLite support.
	GeneratedKeys []interface{}

	Error error
}

// Batch provides batch database operations.
type Batch struct {
	config *Config
	ctx    context.Context

	batchSize         int  // chunk size for commitOnBatchSize; 0 means no chunking
	commitOnBatchSize bool // commit + rebegin between chunks (transaction mode only)
	getGeneratedKeys  bool // collect LastInsertId from inserts
}

// Ctx binds a context so the Batch participates in any transaction carried by
// ctx (see Transaction / WithTx). A Batch without a ctx always uses the pool.
func (b *Batch) Ctx(ctx context.Context) *Batch {
	b.ctx = ctx
	return b
}

// BatchSize sets the chunk size used when CommitOnBatchSize is enabled (对照
// Java Batch.batchSize). Has no observable effect unless CommitOnBatchSize(true)
// is also set. n must be >= 1.
func (b *Batch) BatchSize(n int) *Batch {
	if n < 1 {
		panic("The batchSize must be greater than 0.")
	}
	b.batchSize = n
	return b
}

// CommitOnBatchSize enables chunked commits: every BatchSize rows the active
// transaction is committed and a fresh one begun, so a huge batch does not grow
// a single transaction without bound (对照 Java Batch.commitOnBatchSize).
//
// 慎用：设置为 true 时，已提交的数据无法在后续异常时回滚。仅在 Batch 运行于事务
// 上下文（Ctx 携带 *sql.Tx）时生效；否则每条语句本就各自自动提交。
func (b *Batch) CommitOnBatchSize(enable bool) *Batch {
	b.commitOnBatchSize = enable
	return b
}

// GetGeneratedKeys enables collecting auto-generated primary keys from inserts
// into BatchResult.GeneratedKeys (对照 Java Batch.getGeneratedKeys). Only insert
// statements produce keys.
func (b *Batch) GetGeneratedKeys(enable bool) *Batch {
	b.getGeneratedKeys = enable
	return b
}

// runner returns the DBConn this Batch executes on (tx from ctx, or the pool).
func (b *Batch) runner() (DBConn, error) {
	return b.config.runner(b.ctx)
}

// sqlKind classifies a SQL statement by its leading keyword (lowercased). Used only to
// route batch statements to the matching Before* hook; tolerant of leading parentheses.
func sqlKind(sql string) string {
	s := strings.TrimSpace(sql)
	for len(s) > 0 && s[0] == '(' {
		s = strings.TrimSpace(s[1:])
	}
	s = strings.ToLower(s)
	switch {
	case strings.HasPrefix(s, "select"), strings.HasPrefix(s, "with"):
		return "select"
	case strings.HasPrefix(s, "insert"), strings.HasPrefix(s, "replace"):
		return "insert"
	case strings.HasPrefix(s, "update"):
		return "update"
	case strings.HasPrefix(s, "delete"):
		return "delete"
	default:
		return "other"
	}
}

// fireBeforeRowInserts fires InsertHook.BeforeRowInsert for each row when a hook is
// installed, so row-stamping hooks (tenant/creator/dept) apply before the rows are
// grouped and inserted. Returns a veto error if any hook calls Dao.Fail. No-op without
// a hook. (See §13: batch inserts consume only the row-mutation semantics of the hook.)
func (b *Batch) fireBeforeRowInserts(rows []*Row) error {
	hk := b.config.GetDbHookKit()
	if hk == nil || hk.InsertHook == nil {
		return nil
	}
	for _, row := range rows {
		synth := &Dao{config: b.config, ctx: b.ctx}
		hk.InsertHook.BeforeRowInsert(synth, row)
		if synth.failErr != nil {
			return synth.failErr
		}
	}
	return nil
}

// applyHook fires the statement-kind-appropriate Before* hook on a synthetic Dao
// carrying sql+sampleArgs, then returns the rewritten SQL together with the TRAILING
// args injected by the hook (constant across rows). The original placeholders in sql
// are per-row template slots filled by the caller; only the trailing injected params
// are returned here. Returns sql unchanged (no trailing) when no hook is installed.
// A hook veto (Dao.Fail) is surfaced as err so the batch aborts (fail-closed).
func (b *Batch) applyHook(sql string, sampleArgs []interface{}) (string, []interface{}, error) {
	hk := b.config.GetDbHookKit()
	if hk == nil {
		return sql, nil, nil
	}
	synth := &Dao{config: b.config, ctx: b.ctx}
	synth.setSqlPara(&dbsql.SqlPara{Sql: sql, Paras: sampleArgs})
	switch sqlKind(sql) {
	case "select":
		if hk.FindHook != nil {
			hk.FindHook.BeforeFind(synth)
		} else if hk.QueryHook != nil {
			hk.QueryHook.BeforeQuery(synth)
		}
	case "update":
		if hk.UpdateHook != nil {
			hk.UpdateHook.BeforeSqlUpdate(synth)
		}
	case "delete":
		if hk.DeleteHook != nil {
			hk.DeleteHook.BeforeSqlDelete(synth)
		}
	case "insert":
		// No row is available at this layer, so row-stamping cannot apply; the
		// documented caveat is to use the row-based Batch.Insert for inserts.
	}
	if synth.failErr != nil {
		return "", nil, synth.failErr
	}
	outSQL, outArgs := synth.SqlAndArgs()
	if len(outArgs) <= len(sampleArgs) {
		return outSQL, nil, nil
	}
	return outSQL, append([]interface{}(nil), outArgs[len(sampleArgs):]...), nil
}

// updateRowArgs builds the per-row argument slice for a batch UPDATE group: the
// normalized values of the changed fields followed by the primary-key values, matching
// the placeholder order produced by Dialect.ForUpdate.
func updateRowArgs(row *Row, changedFields, pks []string) []interface{} {
	args := make([]interface{}, 0, len(changedFields)+len(pks))
	for _, f := range changedFields {
		args = append(args, normalizeSQLValue(row.data[f]))
	}
	for _, pk := range pks {
		args = append(args, row.data[pk])
	}
	return args
}

// commitChunk is the chunked-commit hook: when CommitOnBatchSize is on, it
// commits the current *sql.Tx and begins a fresh one, rebinding b.ctx. It returns
// the new DBConn (the caller must re-Prepare any statement). It is a no-op when
// CommitOnBatchSize is off or BatchSize is 0.
func (b *Batch) commitChunk() (DBConn, error) {
	if !b.commitOnBatchSize || b.batchSize == 0 {
		return nil, nil
	}
	tx, ok := txFromContext(b.ctx)
	if !ok {
		return nil, nil // not in a tx: each Exec autocommits already
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	pool, err := b.config.Pool()
	if err != nil {
		return nil, err
	}
	newTx, err := pool.Begin()
	if err != nil {
		return nil, err
	}
	b.ctx = withTx(b.ctx, newTx)
	return newTx, nil
}

// beginForChunking begins a transaction owned by this batch when chunked commit
// is enabled and the batch is not already running inside one. Pair with
// commitTail. This makes CommitOnBatchSize self-contained: callers do NOT need to
// wrap the batch in TransactionCtx (and should not — the batch takes ownership of
// the transaction for its duration).
func (b *Batch) beginForChunking() error {
	if !b.commitOnBatchSize || b.batchSize == 0 {
		return nil
	}
	if _, ok := txFromContext(b.ctx); ok {
		return nil // already in a tx (e.g. Ctx set explicitly)
	}
	pool, err := b.config.Pool()
	if err != nil {
		return err
	}
	tx, err := pool.Begin()
	if err != nil {
		return err
	}
	b.ctx = withTx(b.ctx, tx)
	return nil
}

// commitTail commits the transaction owned by this batch (the trailing partial
// chunk) when chunked commit is enabled. No-op otherwise.
func (b *Batch) commitTail() error {
	if !b.commitOnBatchSize || b.batchSize == 0 {
		return nil
	}
	tx, ok := txFromContext(b.ctx)
	if !ok {
		return nil
	}
	return tx.Commit()
}

// recordResult accumulates one statement's outcome into res.
func (res *BatchResult) recordResult(n int64, result interface{}) {
	res.UpdateCounts = append(res.UpdateCounts, n)
	res.RowsAffected += n
	if result != nil {
		if keyed, ok := result.(interface{ LastInsertId() (int64, error) }); ok {
			if id, err := keyed.LastInsertId(); err == nil {
				res.GeneratedKeys = append(res.GeneratedKeys, id)
			}
		}
	}
}

// Insert batch-inserts rows.
func (b *Batch) Insert(rows []*Row) (*BatchResult, error) {
	if len(rows) == 0 {
		return &BatchResult{}, nil
	}
	return b.InsertWithTable(rows[0].table, rows)
}

// InsertWithTable batch-inserts rows for a specific table.
func (b *Batch) InsertWithTable(table string, rows []*Row) (*BatchResult, error) {
	if len(rows) == 0 {
		return &BatchResult{}, nil
	}
	if err := b.fireBeforeRowInserts(rows); err != nil {
		res := &BatchResult{Error: err}
		return res, err
	}
	groups := groupRowsForInsert(table, rows)
	return b.execInsertGroups(groups)
}

// InsertGroup batch-inserts a HETEROGENEOUS set of rows: rows may span multiple
// tables or carry different field sets (对照 Java BatchGroup — 多表/混列批). Rows
// are grouped by (table, field set) and each group runs its own prepared
// statement. The row's own table takes precedence; rows lacking a table use the
// empty string and are skipped with an error.
func (b *Batch) InsertGroup(rows []*Row) (*BatchResult, error) {
	if len(rows) == 0 {
		return &BatchResult{}, nil
	}
	for _, r := range rows {
		if r.table == "" {
			return &BatchResult{}, fmt.Errorf("batch InsertGroup requires every row to carry a table; use InsertWithTable for a fixed table")
		}
	}
	if err := b.fireBeforeRowInserts(rows); err != nil {
		res := &BatchResult{Error: err}
		return res, err
	}
	groups := groupRowsForInsert("", rows)
	return b.execInsertGroups(groups)
}

// rowGroup is a homogeneous bucket of rows sharing a table and field set.
type rowGroup struct {
	table  string
	fields []string
	rows   []*Row
}

// groupRowsForInsert buckets rows by (table, filtered field tuple). When table
// is non-empty it overrides each row's table (homogeneous InsertWithTable path).
func groupRowsForInsert(table string, rows []*Row) []rowGroup {
	groups := map[string]*rowGroup{}
	order := []string{}
	for _, row := range rows {
		t := table
		if t == "" {
			t = row.table
		}
		fields := filterTableFields(t, row.FieldNames())
		key := t + "\x00" + strings.Join(fields, ",")
		g, ok := groups[key]
		if !ok {
			g = &rowGroup{table: t, fields: fields}
			groups[key] = g
			order = append(order, key)
		}
		g.rows = append(g.rows, row)
	}
	out := make([]rowGroup, 0, len(order))
	for _, k := range order {
		out = append(out, *groups[k])
	}
	return out
}

// execInsertGroups runs each group with its own prepared statement, applying
// chunked commit and collecting update counts / generated keys.
func (b *Batch) execInsertGroups(groups []rowGroup) (*BatchResult, error) {
	res := &BatchResult{}
	if err := b.beginForChunking(); err != nil {
		res.Error = err
		return res, err
	}
	for _, g := range groups {
		if len(g.fields) == 0 {
			continue
		}
		r, err := b.runner()
		if err != nil {
			res.Error = err
			return res, err
		}
		sqlStr := b.config.Dialect.ForInsert(g.table, g.fields)
		stmt, err := r.Prepare(sqlStr)
		if err != nil {
			res.Error = err
			return res, err
		}
		var execResult interface{}
		for i, row := range g.rows {
			args := make([]interface{}, len(g.fields))
			for j, f := range g.fields {
				args[j] = normalizeSQLValue(row.data[f])
			}
			result, err := stmt.Exec(args...)
			if err != nil {
				stmt.Close()
				res.Error = err
				return res, err
			}
			n, _ := result.RowsAffected()
			if b.getGeneratedKeys {
				execResult = result
			}
			res.recordResult(n, execResult)
			execResult = nil
			if b.batchSize > 0 && (i+1)%b.batchSize == 0 && i+1 < len(g.rows) {
				stmt.Close()
				if nr, cerr := b.commitChunk(); cerr != nil {
					res.Error = cerr
					return res, cerr
				} else if nr != nil {
					r = nr
				}
				stmt, err = r.Prepare(sqlStr)
				if err != nil {
					res.Error = err
					return res, err
				}
			}
		}
		stmt.Close()
	}
	if err := b.commitTail(); err != nil {
		res.Error = err
		return res, err
	}
	return res, nil
}

// Update batch-updates rows.
func (b *Batch) Update(rows []*Row) (*BatchResult, error) {
	if len(rows) == 0 {
		return &BatchResult{}, nil
	}
	return b.UpdateWithTable(rows[0].table, rows)
}

// UpdateWithTable batch-updates rows for a specific table.
func (b *Batch) UpdateWithTable(table string, rows []*Row) (*BatchResult, error) {
	if len(rows) == 0 {
		return &BatchResult{}, nil
	}
	return b.execUpdateGroups(groupRowsForUpdate(table, rows))
}

// UpdateGroup batch-updates a heterogeneous set of rows grouped by
// (table, changed-field tuple, primary-key tuple). Each row is updated using its
// own changed fields and primary keys.
func (b *Batch) UpdateGroup(rows []*Row) (*BatchResult, error) {
	if len(rows) == 0 {
		return &BatchResult{}, nil
	}
	for _, r := range rows {
		if r.table == "" {
			return &BatchResult{}, fmt.Errorf("batch UpdateGroup requires every row to carry a table")
		}
	}
	return b.execUpdateGroups(groupRowsForUpdate("", rows))
}

// updateGroup buckets rows sharing a table, changed-field tuple, and primary-key
// tuple, so one prepared UPDATE statement serves the whole bucket.
type updateGroup struct {
	table         string
	changedFields []string
	pks           []string
	rows          []*Row
}

func groupRowsForUpdate(table string, rows []*Row) []updateGroup {
	groups := map[string]*updateGroup{}
	order := []string{}
	for _, row := range rows {
		t := table
		if t == "" {
			t = row.table
		}
		changed := filterTableFields(t, row.ChangedFields())
		pks := row.primaryKeys
		key := fmt.Sprintf("%s\x00%s\x00%s", t, strings.Join(changed, ","), strings.Join(pks, ","))
		g, ok := groups[key]
		if !ok {
			g = &updateGroup{table: t, changedFields: changed, pks: pks}
			groups[key] = g
			order = append(order, key)
		}
		g.rows = append(g.rows, row)
	}
	out := make([]updateGroup, 0, len(order))
	for _, k := range order {
		out = append(out, *groups[k])
	}
	return out
}

func (b *Batch) execUpdateGroups(groups []updateGroup) (*BatchResult, error) {
	res := &BatchResult{}
	if err := b.beginForChunking(); err != nil {
		res.Error = err
		return res, err
	}
	for _, g := range groups {
		if len(g.changedFields) == 0 {
			continue
		}
		r, err := b.runner()
		if err != nil {
			res.Error = err
			return res, err
		}
		sqlStr := b.config.Dialect.ForUpdate(g.table, g.changedFields, g.pks)
		// Route the base UPDATE through any installed hook so isolation predicates
		// (e.g. AND tenant_id=?) are injected once per group; the rewritten SQL is the
		// prepared template and the trailing injected params are appended per row.
		sample := updateRowArgs(g.rows[0], g.changedFields, g.pks)
		sqlStr, trailing, err := b.applyHook(sqlStr, sample)
		if err != nil {
			res.Error = err
			return res, err
		}
		stmt, err := r.Prepare(sqlStr)
		if err != nil {
			res.Error = err
			return res, err
		}
		for i, row := range g.rows {
			args := updateRowArgs(row, g.changedFields, g.pks)
			if len(trailing) > 0 {
				args = append(args, trailing...)
			}
			result, err := stmt.Exec(args...)
			if err != nil {
				stmt.Close()
				res.Error = err
				return res, err
			}
			n, _ := result.RowsAffected()
			res.recordResult(n, nil)
			if b.batchSize > 0 && (i+1)%b.batchSize == 0 && i+1 < len(g.rows) {
				stmt.Close()
				if nr, cerr := b.commitChunk(); cerr != nil {
					res.Error = cerr
					return res, cerr
				} else if nr != nil {
					r = nr
				}
				stmt, err = r.Prepare(sqlStr)
				if err != nil {
					res.Error = err
					return res, err
				}
			}
		}
		stmt.Close()
	}
	if err := b.commitTail(); err != nil {
		res.Error = err
		return res, err
	}
	return res, nil
}

// Execute batch-executes the same SQL with different args.
func (b *Batch) Execute(sql string, argsList [][]interface{}) (*BatchResult, error) {
	res := &BatchResult{}
	if err := b.beginForChunking(); err != nil {
		res.Error = err
		return res, err
	}
	r, err := b.runner()
	if err != nil {
		res.Error = err
		return res, err
	}
	// Route the template SQL through any installed hook once; the rewritten SQL is the
	// prepared template and the trailing injected params are appended to each arg row.
	execSQL := sql
	var trailing []interface{}
	if len(argsList) > 0 {
		execSQL, trailing, err = b.applyHook(sql, argsList[0])
		if err != nil {
			res.Error = err
			return res, err
		}
	}
	stmt, err := r.Prepare(execSQL)
	if err != nil {
		res.Error = err
		return res, err
	}
	defer stmt.Close()

	for i, args := range argsList {
		full := args
		if len(trailing) > 0 {
			full = make([]interface{}, 0, len(args)+len(trailing))
			full = append(full, args...)
			full = append(full, trailing...)
		}
		result, err := stmt.Exec(full...)
		if err != nil {
			res.Error = err
			return res, err
		}
		n, _ := result.RowsAffected()
		res.recordResult(n, nil)
		if b.batchSize > 0 && (i+1)%b.batchSize == 0 && i+1 < len(argsList) {
			if nr, cerr := b.commitChunk(); cerr != nil {
				res.Error = cerr
				return res, cerr
			} else if nr != nil {
				r = nr
				stmt.Close()
				stmt, err = r.Prepare(execSQL)
				if err != nil {
					res.Error = err
					return res, err
				}
			}
		}
	}
	if err := b.commitTail(); err != nil {
		res.Error = err
		return res, err
	}
	return res, nil
}

// ExecuteSQLs batch-executes multiple SQL statements.
func (b *Batch) ExecuteSQLs(sqls []string) (*BatchResult, error) {
	res := &BatchResult{}
	if err := b.beginForChunking(); err != nil {
		res.Error = err
		return res, err
	}
	r, err := b.runner()
	if err != nil {
		res.Error = err
		return res, err
	}
	for i, q := range sqls {
		execSQL, trailing, err := b.applyHook(q, nil)
		if err != nil {
			res.Error = err
			return res, fmt.Errorf("sql error: %w", err)
		}
		var result sql.Result
		if len(trailing) > 0 {
			result, err = r.Exec(execSQL, trailing...)
		} else {
			result, err = r.Exec(execSQL)
		}
		if err != nil {
			res.Error = err
			return res, fmt.Errorf("sql error: %w", err)
		}
		n, _ := result.RowsAffected()
		res.recordResult(n, nil)
		if b.batchSize > 0 && (i+1)%b.batchSize == 0 && i+1 < len(sqls) {
			if nr, cerr := b.commitChunk(); cerr != nil {
				res.Error = cerr
				return res, cerr
			} else if nr != nil {
				r = nr
			}
		}
	}
	if err := b.commitTail(); err != nil {
		res.Error = err
		return res, err
	}
	return res, nil
}
