package db_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dbsql "github.com/crazy-airhead/aifei-go/db/sql"
)

// ISSUE-0014 验收：SqlKit 外部 sql 文件加载与热重载。
//
// 对照 Java aifei-db/sql/SqlKit.java：
//   - addSqlFile / setBaseSqlFilePath / setSqlFileHotReloading / parseSqlFile
//   - 文件内按 #sql 指令分段（与内联 AddSql 同一套语法）
//   - getSqlTemplate devMode 热重载：文件变更自动 reload，保留内联缓存

// i14WriteFile 写临时 sql 文件并返回其路径。
func i14WriteFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

// i14Touch 强制刷新文件 mtime，确保 FileSource.IsModified 能检测到变更
// （规避某些文件系统同纳秒精度的边界情况）。
func i14Touch(t *testing.T, path string) {
	t.Helper()
	now := time.Now()
	if err := os.Chtimes(path, now, now.Add(time.Second)); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

// TestIssue0014ParseSqlFile 验证单文件多 #sql 分段按 sqlID 注册，文件加载与内联 AddSql 等价可用。
func TestIssue0014ParseSqlFile(t *testing.T) {
	sk := dbsql.NewSqlKit("i14-parse")

	dir := t.TempDir()
	i14WriteFile(t, dir, "a.sql", `#sql("findById")
select * from user where id = #para(0)
#end

#sql("findByName")
select * from user where name like #para(0)
#end
`)

	sk.AddSqlFile(filepath.Join(dir, "a.sql"))
	if err := sk.ParseSqlFile(); err != nil {
		t.Fatalf("ParseSqlFile: %v", err)
	}

	sp := sk.GetSqlParaByIDWithArgs("findById", 123)
	if sp == nil {
		t.Fatal("expected SqlPara for findById, got nil")
	}
	if !strings.Contains(sp.Sql, "select * from user where id = ?") {
		t.Fatalf("findById SQL mismatch: %s", sp.Sql)
	}
	if len(sp.Paras) != 1 || sp.Paras[0] != 123 {
		t.Fatalf("findById paras mismatch: %v", sp.Paras)
	}

	sp2 := sk.GetSqlParaByIDWithArgs("findByName", "%bob%")
	if sp2 == nil || !strings.Contains(sp2.Sql, "name like ?") {
		t.Fatalf("findByName SQL mismatch: %v", sp2)
	}

	// 未知 sqlID 返回 nil
	if sk.GetSqlParaByIDWithArgs("nope") != nil {
		t.Fatal("expected nil for unknown sqlID")
	}
}

// TestIssue0014ParseSqlFileConflict 验证文件 sqlID 与已有（内联或文件来源）冲突时报错，
// 对照 Java IllegalArgumentException("sqlId already exists")。
func TestIssue0014ParseSqlFileConflict(t *testing.T) {
	sk := dbsql.NewSqlKit("i14-conflict")

	sk.AddSql("dup", `#sql("dup")
select 1
#end`)

	dir := t.TempDir()
	i14WriteFile(t, dir, "a.sql", `#sql("dup")
select 2
#end
`)
	sk.AddSqlFile(filepath.Join(dir, "a.sql"))

	err := sk.ParseSqlFile()
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
	if !strings.Contains(err.Error(), "sqlId already exists") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestIssue0014AddSqlDir 验证目录批量扫描加载多个 .sql（非递归，忽略非 .sql）。
func TestIssue0014AddSqlDir(t *testing.T) {
	sk := dbsql.NewSqlKit("i14-dir")

	dir := t.TempDir()
	i14WriteFile(t, dir, "one.sql", `#sql("q1")
select * from a where id = #para(0)
#end
`)
	i14WriteFile(t, dir, "two.sql", `#sql("q2")
select * from b where id = #para(0)
#end
`)
	i14WriteFile(t, dir, "ignored.txt", `not a sql file`)

	if err := sk.AddSqlDir(dir); err != nil {
		t.Fatalf("AddSqlDir: %v", err)
	}
	if err := sk.ParseSqlFile(); err != nil {
		t.Fatalf("ParseSqlFile: %v", err)
	}

	if sp := sk.GetSqlParaByIDWithArgs("q1", 1); sp == nil || !strings.Contains(sp.Sql, "from a") {
		t.Fatalf("q1 missing/mismatch: %v", sp)
	}
	if sp := sk.GetSqlParaByIDWithArgs("q2", 2); sp == nil || !strings.Contains(sp.Sql, "from b") {
		t.Fatalf("q2 missing/mismatch: %v", sp)
	}
}

// TestIssue0014BaseSqlFilePath 验证 SetBaseSqlFilePath 下 AddSqlFile 用相对路径解析。
func TestIssue0014BaseSqlFilePath(t *testing.T) {
	sk := dbsql.NewSqlKit("i14-base")

	dir := t.TempDir()
	i14WriteFile(t, dir, "rel.sql", `#sql("rel")
select * from t where id = #para(0)
#end
`)

	sk.SetBaseSqlFilePath(dir)
	sk.AddSqlFile("rel.sql") // 相对路径
	if err := sk.ParseSqlFile(); err != nil {
		t.Fatalf("ParseSqlFile: %v", err)
	}

	sp := sk.GetSqlParaByIDWithArgs("rel", 9)
	if sp == nil || !strings.Contains(sp.Sql, "from t where id = ?") {
		t.Fatalf("rel SQL mismatch: %v", sp)
	}
}

// TestIssue0014HotReload 验证 devMode 下文件内容变更后 GetSqlParaByID 自动反映新内容，
// 且 reload 不清除内联 AddSql 缓存（对照 Java sqlFromSqlFile 精准移除语义）。
func TestIssue0014HotReload(t *testing.T) {
	sk := dbsql.NewSqlKit("i14-hot")
	sk.SetSqlFileHotReloading(true) // = engine devMode

	// 内联 sql：reload 不应清掉它
	sk.AddSql("inline", `#sql("inline")
select 1
#end`)

	dir := t.TempDir()
	path := i14WriteFile(t, dir, "hot.sql", `#sql("hot")
select * from user where id = #para(0)
#end
`)
	sk.AddSqlFile(path)
	if err := sk.ParseSqlFile(); err != nil {
		t.Fatalf("ParseSqlFile: %v", err)
	}

	// 初始：来自 user 表
	sp := sk.GetSqlParaByIDWithArgs("hot", 1)
	if sp == nil || !strings.Contains(sp.Sql, "from user") {
		t.Fatalf("initial hot SQL mismatch: %v", sp)
	}

	// 改文件内容：user → admin
	if err := os.WriteFile(path, []byte(`#sql("hot")
select * from admin where id = #para(0)
#end
`), 0644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	i14Touch(t, path)

	// 热重载应生效
	sp2 := sk.GetSqlParaByIDWithArgs("hot", 1)
	if sp2 == nil || !strings.Contains(sp2.Sql, "from admin") {
		t.Fatalf("after reload hot SQL mismatch: %v", sp2)
	}
	if strings.Contains(sp2.Sql, "from user") {
		t.Fatalf("stale content after reload: %s", sp2.Sql)
	}

	// 内联缓存仍在
	sp3 := sk.GetSqlParaByIDWithArgs("inline")
	if sp3 == nil || !strings.Contains(sp3.Sql, "select 1") {
		t.Fatalf("inline cache should survive file reload: %v", sp3)
	}
}

// TestIssue0014HotReloadOff 验证未开热重载时文件变更不生效（返回旧内容）。
func TestIssue0014HotReloadOff(t *testing.T) {
	sk := dbsql.NewSqlKit("i14-hotoff")
	// 不开 SetSqlFileHotReloading

	dir := t.TempDir()
	path := i14WriteFile(t, dir, "x.sql", `#sql("x")
select * from user where id = #para(0)
#end
`)
	sk.AddSqlFile(path)
	if err := sk.ParseSqlFile(); err != nil {
		t.Fatalf("ParseSqlFile: %v", err)
	}

	_ = os.WriteFile(path, []byte(`#sql("x")
select * from admin where id = #para(0)
#end
`), 0644)
	i14Touch(t, path)

	// 未开热重载：getSqlTemplate cache hit 不检查 isModified，返回旧内容
	sp := sk.GetSqlParaByIDWithArgs("x", 1)
	if sp == nil {
		t.Fatal("expected SqlPara, got nil")
	}
	if !strings.Contains(sp.Sql, "from user") {
		t.Fatalf("expected stale 'from user' without hot reload, got: %s", sp.Sql)
	}
}
