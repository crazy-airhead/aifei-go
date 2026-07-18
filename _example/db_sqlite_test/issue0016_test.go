package db_sqlite_test

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/crazy-airhead/aifei-go/db"
)

// ---- 复合主键（任意列数）----

func setupCompositeTable(t *testing.T, cols string) {
	t.Helper()
	setupTestDB(t)
	stmt := "CREATE TABLE IF NOT EXISTS comp (" + cols + ")"
	if _, err := db.Use().RawSql(stmt).Update(); err != nil {
		t.Fatal(err)
	}
	db.Use().RawSql("DELETE FROM comp").Update()
}

func TestIssue0016CompositeIdArbitraryArity(t *testing.T) {
	setupCompositeTable(t, "k1 TEXT, k2 TEXT, k3 TEXT, val TEXT, PRIMARY KEY (k1, k2, k3)")

	row := db.NewRow("comp").
		Set("k1", "a").Set("k2", "b").Set("k3", "c").Set("val", "v1")
	if _, err := db.Insert(row); err != nil {
		t.Fatal(err)
	}

	// 3 列复合主键查询
	got, err := db.FindByCompositeIds("comp", []string{"k1", "k2", "k3"}, "a", "b", "c")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.GetStr("val") != "v1" {
		t.Fatalf("expected val=v1, got %+v", got)
	}

	// 列数不匹配应返回明确错误（3 keys vs 2 values）
	if _, err := db.FindByCompositeIds("comp", []string{"k1", "k2", "k3"}, "a", "b"); err == nil {
		t.Fatal("expected error for mismatched key/value counts")
	}

	// 3 列复合主键删除
	ok, err := db.DeleteByCompositeIds("comp", []string{"k1", "k2", "k3"}, "a", "b", "c")
	if err != nil || !ok {
		t.Fatalf("delete failed: ok=%v err=%v", ok, err)
	}
	if got, _ := db.FindByCompositeIds("comp", []string{"k1", "k2", "k3"}, "a", "b", "c"); got != nil {
		t.Fatal("expected row deleted")
	}
}

// ---- findOne 自定义错误回调 ----

func TestIssue0016FindOneWithMsg(t *testing.T) {
	setupTestDB(t)
	for _, n := range []string{"u1", "u2"} {
		db.Insert(db.NewRow("user").Set("name", n).Set("age", 20))
	}

	// 命中多条 → 用回调生成的消息报错
	_, err := db.Use().RawSql("select * from user").FindOneWithMsg(func(n int) string {
		return "用户数必须为 1，实际 " + strconv.Itoa(n)
	})
	if err == nil || !strings.Contains(err.Error(), "实际 2") {
		t.Fatalf("expected custom message with count 2, got: %v", err)
	}

	// 命中 0 条 → 回调收到 0
	_, err = db.Use().RawSql("select * from user where id = -1").FindOneWithMsg(func(n int) string {
		return "count=" + strconv.Itoa(n)
	})
	if err == nil || !strings.Contains(err.Error(), "count=0") {
		t.Fatalf("expected count=0 message, got: %v", err)
	}

	// 恰好 1 条 → 正常返回
	row, err := db.Use().RawSql("select * from user where name = 'u1'").FindOneWithMsg(func(n int) string { return "x" })
	if err != nil || row == nil {
		t.Fatalf("expected one row, got err=%v", err)
	}
}

// ---- queryField 默认值重载 ----

func TestIssue0016QueryFieldOr(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "n1").Set("age", 20))

	// 有值
	v, err := db.Use().RawSql("select age from user where name = 'n1'").QueryFieldOr(99)
	if err != nil || v == nil {
		t.Fatalf("expected age value, got %v err=%v", v, err)
	}

	// 无值 → 默认
	v, err = db.Use().RawSql("select age from user where name = 'nope'").QueryFieldOr(99)
	if err != nil || v != 99 {
		t.Fatalf("expected default 99, got %v err=%v", v, err)
	}
}

// ---- 泛型事务返回值 + 成功路径主动回滚 ----

func TestIssue0016TransactionOfCommit(t *testing.T) {
	setupTestDB(t)
	type Result struct{ Msg string }
	res, err := db.TransactionOf(func(ctx context.Context, tx *db.Tx) (Result, error) {
		if _, err := db.WithCtx(ctx).InsertRow(db.NewRow("user").Set("name", "committed").Set("age", 1)); err != nil {
			return Result{}, err
		}
		return Result{Msg: "ok"}, nil
	})
	if err != nil || res.Msg != "ok" {
		t.Fatalf("expected committed result, got %+v err=%v", res, err)
	}
	// 提交后数据应存在
	if n, _ := db.Use().CountBy("user", "name = ?", "committed"); n != 1 {
		t.Fatalf("expected row committed, count=%d", n)
	}
}

func TestIssue0016TransactionOfActiveRollback(t *testing.T) {
	setupTestDB(t)
	_, err := db.TransactionOf(func(ctx context.Context, tx *db.Tx) (string, error) {
		if _, err := db.WithCtx(ctx).InsertRow(db.NewRow("user").Set("name", "rolled").Set("age", 1)); err != nil {
			return "", err
		}
		tx.Rollback() // 成功路径主动回滚
		return "done", nil
	})
	if !errors.Is(err, db.ErrRollback) {
		t.Fatalf("expected ErrRollback, got %v", err)
	}
	// 回滚后数据不应存在
	if n, _ := db.Use().CountBy("user", "name = ?", "rolled"); n != 0 {
		t.Fatalf("expected rollback, count=%d", n)
	}
}

type rollbackResult struct {
	msg      string
	rollback bool
}

func (r rollbackResult) ShouldRollback() bool { return r.rollback }

func TestIssue0016TransactionOfRollbackDecision(t *testing.T) {
	setupTestDB(t)
	_, err := db.TransactionOf(func(ctx context.Context, tx *db.Tx) (rollbackResult, error) {
		if _, err := db.WithCtx(ctx).InsertRow(db.NewRow("user").Set("name", "decided").Set("age", 1)); err != nil {
			return rollbackResult{}, err
		}
		return rollbackResult{msg: "fail", rollback: true}, nil // 结果驱动回滚
	})
	if !errors.Is(err, db.ErrRollback) {
		t.Fatalf("expected ErrRollback, got %v", err)
	}
	if n, _ := db.Use().CountBy("user", "name = ?", "decided"); n != 0 {
		t.Fatalf("expected rollback, count=%d", n)
	}
}

// ---- Batch: UpdateCounts / GeneratedKeys ----

func TestIssue0016BatchCountsAndGeneratedKeys(t *testing.T) {
	setupTestDB(t)
	rows := []*db.Row{
		db.NewRow("user").Set("name", "b1").Set("age", 1),
		db.NewRow("user").Set("name", "b2").Set("age", 2),
		db.NewRow("user").Set("name", "b3").Set("age", 3),
	}
	res, err := db.NewBatch().GetGeneratedKeys(true).Insert(rows)
	if err != nil {
		t.Fatal(err)
	}
	if res.RowsAffected != 3 {
		t.Fatalf("expected 3 rows affected, got %d", res.RowsAffected)
	}
	if len(res.UpdateCounts) != 3 || res.UpdateCounts[0] != 1 {
		t.Fatalf("expected UpdateCounts [1,1,1], got %v", res.UpdateCounts)
	}
	if len(res.GeneratedKeys) != 3 {
		t.Fatalf("expected 3 generated keys, got %v", res.GeneratedKeys)
	}
}

// ---- Batch: 异构批（多表/混列）----

func TestIssue0016BatchInsertGroup(t *testing.T) {
	setupTestDB(t)
	setupCompositeTable(t, "k1 TEXT, k2 TEXT, val TEXT, PRIMARY KEY (k1, k2)")

	mixed := []*db.Row{
		db.NewRow("user").Set("name", "g1").Set("age", 1),
		db.NewRow("comp").Set("k1", "x").Set("k2", "y").Set("val", "z"),
		db.NewRow("user").Set("name", "g2").Set("age", 2).Set("email", "e@e"), // 不同字段集
	}
	res, err := db.NewBatch().InsertGroup(mixed)
	if err != nil {
		t.Fatal(err)
	}
	if res.RowsAffected != 3 {
		t.Fatalf("expected 3 rows affected, got %d", res.RowsAffected)
	}
	if n, _ := db.Use().Count("user"); n != 2 {
		t.Fatalf("expected 2 user rows, got %d", n)
	}
	if got, _ := db.FindByCompositeIds("comp", []string{"k1", "k2"}, "x", "y"); got == nil || got.GetStr("val") != "z" {
		t.Fatalf("expected comp row val=z, got %+v", got)
	}
}

// ---- Batch: 分块提交（commitOnBatchSize）----

func TestIssue0016BatchChunkedCommit(t *testing.T) {
	setupTestDB(t)
	rows := make([]*db.Row, 5)
	for i := range rows {
		rows[i] = db.NewRow("user").Set("name", "c"+strconv.Itoa(i)).Set("age", i)
	}
	// commitOnBatchSize 自管事务：batch 内部按 batchSize 提交，末尾块一并提交。
	// 不应外包 TransactionCtx（batch 接管事务生命周期）。
	res, err := db.NewBatch().BatchSize(2).CommitOnBatchSize(true).Insert(rows)
	if err != nil {
		t.Fatal(err)
	}
	if res.RowsAffected != 5 {
		t.Fatalf("expected 5 rows affected, got %d", res.RowsAffected)
	}
	// 分块提交后全部应持久化
	if n, _ := db.Use().Count("user"); n != 5 {
		t.Fatalf("expected 5 committed rows, got %d", n)
	}
}

// ---- Kv 有序 fluent 参数 Map ----

func TestIssue0016Kv(t *testing.T) {
	setupTestDB(t)
	db.Insert(db.NewRow("user").Set("name", "alice").Set("age", 30))

	kv := db.NewKv().Set("name", "alice").Set("age", 30)
	if kv.Len() != 2 || !kv.Has("name") || !kv.Has("age") {
		t.Fatalf("expected keys {name, age}, got %v", kv.Keys())
	}

	// Kv 直接传 SqlKit（Kv 即 map[string]interface{}，无需 .Map()）
	rows, err := db.Sql("select * from user where name = #para(name)", kv).Find()
	if err != nil || len(rows) != 1 || rows[0].GetStr("name") != "alice" {
		t.Fatalf("kv direct bridge failed: rows=%v err=%v", rows, err)
	}

	// fluent + typed getters + Unset（Go map 无序，仅校验成员）
	kv.Set("extra", "x").Unset("age")
	if kv.Has("age") {
		t.Fatal("Unset should remove age")
	}
	if kv.Len() != 2 || !kv.Has("name") || !kv.Has("extra") {
		t.Fatalf("expected keys {name, extra} after unset, got %v", kv.Keys())
	}
	if kv.GetInt("age") != 0 { // absent → zero
		t.Fatal("absent key should return zero")
	}

	// KvAs 转换器
	upper := db.KvAs(kv, "name", func(v interface{}) string {
		return strings.ToUpper(db.ToString(v))
	})
	if upper != "ALICE" {
		t.Fatalf("expected ALICE, got %q", upper)
	}

	// SortedKeys / SortedKeysBy（kv 现含 {name, extra}）
	if got := kv.SortedKeys(); len(got) != 2 || got[0] != "extra" || got[1] != "name" {
		t.Fatalf("expected sorted [extra name], got %v", got)
	}
	if got := kv.SortedKeysBy(func(a, b string) bool { return a > b }); len(got) != 2 || got[0] != "name" || got[1] != "extra" {
		t.Fatalf("expected reverse [name extra], got %v", got)
	}
	if !sort.StringsAreSorted(kv.SortedKeys()) {
		t.Fatal("SortedKeys should be alphabetically sorted")
	}
}

// ---- Row 小缺口：SetOrPut / Data / SetData / RowAs ----

func TestIssue0016RowGaps(t *testing.T) {
	r := db.NewRow("user").Set("name", "alice") // name 已存在
	r.SetOrPut("name", "bob")                   // 已存在 → Set（跟踪变更）
	r.SetOrPut("nick", "al")                    // 不存在 → Put（不跟踪变更）

	if r.GetStr("name") != "bob" {
		t.Fatalf("expected name=bob, got %q", r.GetStr("name"))
	}
	changed := map[string]bool{}
	for _, f := range r.ChangedFields() {
		changed[f] = true
	}
	if !changed["name"] {
		t.Error("name should be tracked as changed")
	}
	if changed["nick"] {
		t.Error("nick should NOT be tracked as changed (Put)")
	}

	// Data / SetData 批量读写
	d := r.Data()
	if d["name"] != "bob" {
		t.Fatalf("Data() should expose name=bob, got %v", d["name"])
	}
	r2 := db.NewRow("user")
	r2.SetData(map[string]interface{}{"name": "carol", "age": 25})
	if r2.GetStr("name") != "carol" || r2.GetInt("age") != 25 {
		t.Fatalf("SetData failed: %+v", r2.Data())
	}

	// RowAs 函数式转换器
	age := db.RowAs(r2, "age", func(v interface{}) int { return db.ToInt(v) * 2 })
	if age != 50 {
		t.Fatalf("expected 50, got %d", age)
	}
	// RowAs 对 nil 字段不调用 fn
	got := db.RowAs(r2, "missing", func(v interface{}) int { return 999 })
	if got != 0 {
		t.Fatalf("expected zero for missing field, got %d", got)
	}
}
