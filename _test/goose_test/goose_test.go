package goose_test

import (
	"context"
	"database/sql"
	"embed"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	_ "modernc.org/sqlite" // register the sqlite driver

	"github.com/crazy-airhead/aifei-go/config"
	"github.com/crazy-airhead/aifei-go/db"
	"github.com/crazy-airhead/aifei-go/log"
	goose "github.com/crazy-airhead/aifei-go/plugins/goose"
)

// ---- fixtures ----
//
// Migrations are real .sql files under testdata/ (the production layout).
// Tests copy them into a per-test temp dir via migrDir, so every test
// exercises the os.DirFS disk-reading path and tests can grow the file set
// mid-run (TestUpIncremental):
//
//	testdata/migrations/  00001_create_users.sql, 00002_create_posts.sql,
//	                      00003_add_users_email.sql
//	testdata/notx/        00001_create_notx.sql   (NO TRANSACTION)
//	testdata/broken/      00001_broken.sql        (invalid SQL → fail-fast)
//	testdata/seed/        00001_init_dict.sql     (DDL + INSERT DML)
//	testdata/embed/       00001_create_embedtable.sql (go:embed path)

const (
	migUsers = "00001_create_users.sql"
	migPosts = "00002_create_posts.sql"
	migEmail = "00003_add_users_email.sql"
)

// migrDir copies the selected .sql files (all of them when names is empty)
// from testdata/<src> into a fresh temp dir and returns its path — the
// production os.DirFS layout, so every test runs against real files.
func migrDir(t *testing.T, src string, names ...string) string {
	t.Helper()
	dst := t.TempDir()
	entries, err := os.ReadDir(filepath.Join("testdata", src))
	if err != nil {
		t.Fatalf("read testdata/%s: %v", src, err)
	}
	for _, e := range entries {
		if e.IsDir() || (len(names) > 0 && !slices.Contains(names, e.Name())) {
			continue
		}
		copyMigr(t, src, dst, e.Name())
	}
	return dst
}

// copyMigr copies one migration file from testdata/<src> into dstDir — used
// on its own to grow a dir's migration set mid-test (TestUpIncremental).
func copyMigr(t *testing.T, src, dstDir, name string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", src, name))
	if err != nil {
		t.Fatalf("read testdata/%s/%s: %v", src, name, err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, name), data, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// initialDir returns a temp migrations dir holding the first two files
// (users + posts); the email migration stays out until a test copies it in.
func initialDir(t *testing.T) string {
	return migrDir(t, "migrations", migUsers, migPosts)
}

// newDB initializes the "main" db.Config on a fresh sqlite file and returns
// its DSN (for direct sqlite_master probes).
func newDB(t *testing.T) string {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "migrate.db")
	if err := db.InitWithID("main", "sqlite", dsn); err != nil {
		t.Fatalf("db.InitWithID: %v", err)
	}
	t.Cleanup(db.ResetConfigs)
	return dsn
}

// newGoose builds a Goose over a real migrations dir (fsys nil → os.DirFS
// path, same as the plugin default) with sqlite defaults.
func newGoose(t *testing.T, dir string, mutate func(*goose.Config)) *goose.Goose {
	t.Helper()
	cfg := &goose.Config{
		DBID:  "main",
		Dir:   dir,
		Table: "goose_db_version",
	}
	if mutate != nil {
		mutate(cfg)
	}
	g, err := goose.New(cfg, nil, nil)
	if err != nil {
		t.Fatalf("goose.New: %v", err)
	}
	return g
}

func tableExists(t *testing.T, dsn, name string) bool {
	t.Helper()
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer conn.Close()
	var n int
	if err := conn.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name,
	).Scan(&n); err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	return n > 0
}

func versionRows(t *testing.T, dsn, table string) int {
	t.Helper()
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer conn.Close()
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("count rows in %s: %v", table, err)
	}
	return n
}

// setGlobalConfig installs a fresh global Props with the given keys. Tests
// never call config.Init, so plain config.Set would silently no-op; SetProps
// makes the values visible to LoadConfig. Restored to nil (defaults) on
// cleanup so tests stay independent.
func setGlobalConfig(t *testing.T, kv map[string]interface{}) {
	t.Helper()
	p := config.NewProps()
	for k, v := range kv {
		p.Set(k, v)
	}
	config.SetProps(p)
	t.Cleanup(func() { config.SetProps(nil) })
}

// recordingLogger captures Warn/Error calls for assertions.
type recordingLogger struct {
	mu     sync.Mutex
	warns  []string
	errors []string
}

func (l *recordingLogger) record(target *[]string, msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	*target = append(*target, msg)
}

func (l *recordingLogger) Trace(msg string, _ ...interface{}) {}
func (l *recordingLogger) Debug(msg string, _ ...interface{}) {}
func (l *recordingLogger) Info(msg string, _ ...interface{})  {}
func (l *recordingLogger) Warn(msg string, _ ...interface{}) {
	l.record(&l.warns, msg)
}
func (l *recordingLogger) Error(msg string, _ ...interface{}) {
	l.record(&l.errors, msg)
}
func (l *recordingLogger) IsTraceEnabled() bool { return false }
func (l *recordingLogger) IsDebugEnabled() bool { return false }
func (l *recordingLogger) IsInfoEnabled() bool  { return false }
func (l *recordingLogger) IsWarnEnabled() bool  { return true }
func (l *recordingLogger) IsErrorEnabled() bool { return true }

var _ log.Logger = (*recordingLogger)(nil)

func (l *recordingLogger) warnCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.warns)
}

// ---- package-level API: not started ----

// TestPackageLevelNotStarted must run before any test that calls Plugin.Start:
// tests in a single file execute in source order, and this one asserts the
// package-level default instance is absent (Use() nil, helpers error).
func TestPackageLevelNotStarted(t *testing.T) {
	if g := goose.Use(); g != nil {
		t.Fatalf("Use() = %v, want nil before any Start", g)
	}
	ctx := context.Background()
	if err := goose.Up(ctx); err == nil {
		t.Error("Up() without a started plugin should error")
	}
	if err := goose.UpByOne(ctx); err == nil {
		t.Error("UpByOne() without a started plugin should error")
	}
	if err := goose.Down(ctx); err == nil {
		t.Error("Down() without a started plugin should error")
	}
	if rows, err := goose.Status(ctx); err == nil || rows != nil {
		t.Errorf("Status() = %v, %v; want nil, error", rows, err)
	}
	if v, err := goose.Version(ctx); err == nil || v != 0 {
		t.Errorf("Version() = %d, %v; want 0, error", v, err)
	}
}

// ---- New construction ----

func TestNewMissingDBConfig(t *testing.T) {
	db.ResetConfigs()
	cfg := &goose.Config{DBID: "main", Dir: "."}
	if _, err := goose.New(cfg, nil, nil); err == nil {
		t.Fatal("New without db.InitWithID should fail-fast, got nil error")
	}
}

func TestNewDialectMapping(t *testing.T) {
	// sqlite maps and connects end-to-end.
	dsn := newDB(t)
	g := newGoose(t, initialDir(t), nil)
	if g == nil || g.Raw() == nil {
		t.Fatal("driver sqlite: New should succeed with a non-nil Raw()")
	}

	// Unknown driver → unsupported-driver error (before any connection attempt).
	if err := db.InitWithID("main", "oracle", dsn); err != nil {
		t.Fatalf("InitWithID(oracle): %v", err)
	}
	_, err := goose.New(&goose.Config{DBID: "main", Dir: ".", Table: "goose_db_version"}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported driver") {
		t.Errorf("unknown driver error = %v, want contains %q", err, "unsupported driver")
	}

	// sqlite3/mysql/postgres map past the dialect check; they fail later at
	// driver registration/connection (modernc registers only "sqlite") —
	// proving the mapping, not the connection (contract §11 note).
	for _, driver := range []string{"sqlite3", "mysql", "postgres"} {
		if err := db.InitWithID("main", driver, "user:pass@127.0.0.1/db?multiStatements=true"); err != nil {
			t.Fatalf("InitWithID(%s): %v", driver, err)
		}
		_, err := goose.New(&goose.Config{DBID: "main", Dir: ".", Table: "goose_db_version"}, nil, nil)
		if err == nil {
			t.Errorf("driver %s: expected a driver-layer error, got nil", driver)
		} else if strings.Contains(err.Error(), "unsupported driver") {
			t.Errorf("driver %s should map to a goose dialect, got: %v", driver, err)
		}
	}
}

func TestMySQLDSNWarn(t *testing.T) {
	// mysql DSN without multiStatements → New warns (then fails on the
	// unregistered driver); the warn is what this test pins.
	if err := db.InitWithID("main", "mysql", "user:pass@127.0.0.1:3306/app"); err != nil {
		t.Fatalf("InitWithID: %v", err)
	}
	t.Cleanup(db.ResetConfigs)
	logger := &recordingLogger{}
	_, _ = goose.New(&goose.Config{DBID: "main", Dir: ".", Table: "goose_db_version"}, nil, logger)
	if logger.warnCount() == 0 {
		t.Error("expected a Warn about multiStatements for a mysql DSN lacking it")
	}
}

// ---- migration operations ----

func TestUpInitial(t *testing.T) {
	dsn := newDB(t)
	g := newGoose(t, initialDir(t), nil)
	ctx := context.Background()
	if err := g.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if v, err := g.Version(ctx); err != nil || v != 2 {
		t.Errorf("Version = %d, %v; want 2", v, err)
	}
	if !tableExists(t, dsn, "users") || !tableExists(t, dsn, "posts") {
		t.Error("expected users and posts tables after Up")
	}
	// goose seeds the version table with a version-0 row on creation, hence
	// 2 applied migrations → 3 rows.
	if n := versionRows(t, dsn, "goose_db_version"); n != 3 {
		t.Errorf("version table rows = %d, want 3", n)
	}
}

func TestUpIncremental(t *testing.T) {
	dsn := newDB(t)
	ctx := context.Background()

	dir := initialDir(t)
	g1 := newGoose(t, dir, nil)
	if err := g1.Up(ctx); err != nil {
		t.Fatalf("first Up: %v", err)
	}

	// A third migration file appears on disk; a fresh Goose over the same db
	// applies only the new file.
	copyMigr(t, "migrations", dir, migEmail)
	g2 := newGoose(t, dir, nil)
	if err := g2.Up(ctx); err != nil {
		t.Fatalf("second Up: %v", err)
	}
	if v, err := g2.Version(ctx); err != nil || v != 3 {
		t.Errorf("Version = %d, %v; want 3", v, err)
	}
	// zero seed row + 3 applied migrations.
	if n := versionRows(t, dsn, "goose_db_version"); n != 4 {
		t.Errorf("version table rows = %d, want 4", n)
	}
	// Re-running Up is a no-op.
	if err := g2.Up(ctx); err != nil {
		t.Fatalf("idempotent Up: %v", err)
	}
	if n := versionRows(t, dsn, "goose_db_version"); n != 4 {
		t.Errorf("version table rows after repeat Up = %d, want 4", n)
	}
}

func TestUpByOne(t *testing.T) {
	newDB(t)
	ctx := context.Background()
	g := newGoose(t, migrDir(t, "migrations"), nil)

	for want := int64(1); want <= 3; want++ {
		if err := g.UpByOne(ctx); err != nil {
			t.Fatalf("UpByOne step %d: %v", want, err)
		}
		if v, err := g.Version(ctx); err != nil || v != want {
			t.Fatalf("after step %d: Version = %d, %v", want, v, err)
		}
	}
}

func TestDown(t *testing.T) {
	dsn := newDB(t)
	g := newGoose(t, initialDir(t), nil)
	ctx := context.Background()
	if err := g.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}

	if err := g.Down(ctx); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if v, err := g.Version(ctx); err != nil || v != 1 {
		t.Errorf("Version after Down = %d, %v; want 1", v, err)
	}
	if tableExists(t, dsn, "posts") {
		t.Error("posts table should be gone after Down")
	}
	if !tableExists(t, dsn, "users") {
		t.Error("users table should remain after Down")
	}
	// zero seed row + the remaining users migration.
	if n := versionRows(t, dsn, "goose_db_version"); n != 2 {
		t.Errorf("version table rows = %d, want 2", n)
	}
}

func TestReset(t *testing.T) {
	dsn := newDB(t)
	ctx := context.Background()
	g := newGoose(t, migrDir(t, "migrations"), nil)
	if err := g.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}

	if err := g.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if v, err := g.Version(ctx); err != nil || v != 0 {
		t.Errorf("Version after Reset = %d, %v; want 0", v, err)
	}
	if tableExists(t, dsn, "users") || tableExists(t, dsn, "posts") {
		t.Error("users/posts should be gone after Reset")
	}
	// DownTo(0) removes every applied version but keeps the zero seed row.
	if n := versionRows(t, dsn, "goose_db_version"); n != 1 {
		t.Errorf("version table rows after Reset = %d, want 1 (zero seed row)", n)
	}
}

func TestStatus(t *testing.T) {
	newDB(t)
	g := newGoose(t, initialDir(t), nil)
	ctx := context.Background()
	if err := g.UpByOne(ctx); err != nil {
		t.Fatalf("UpByOne: %v", err)
	}

	rows, err := g.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("Status rows = %d, want 2", len(rows))
	}
	applied := rows[0]
	if !applied.Applied || applied.AppliedAt.IsZero() {
		t.Errorf("row 0 = %+v; want Applied with a timestamp", applied)
	}
	if applied.Version != 1 || !strings.Contains(applied.FileName, "00001") {
		t.Errorf("row 0 version/name = %d, %q; want 1, contains 00001", applied.Version, applied.FileName)
	}
	pending := rows[1]
	if pending.Applied || !pending.AppliedAt.IsZero() {
		t.Errorf("row 1 = %+v; want not applied with zero timestamp", pending)
	}
	if pending.Version != 2 {
		t.Errorf("row 1 version = %d, want 2", pending.Version)
	}
}

func TestNoTransaction(t *testing.T) {
	dsn := newDB(t)
	g := newGoose(t, migrDir(t, "notx"), nil)
	ctx := context.Background()
	if err := g.Up(ctx); err != nil {
		t.Fatalf("Up with NO TRANSACTION: %v", err)
	}
	if !tableExists(t, dsn, "notx") {
		t.Error("notx table should exist after a NO TRANSACTION migration")
	}
	if v, err := g.Version(ctx); err != nil || v != 1 {
		t.Errorf("Version = %d, %v; want 1 (NO TRANSACTION still records the version)", v, err)
	}
}

// TestDataMigration: a migration file mixing DDL and DML executes for real —
// the inserted rows are queryable after Up and the table is gone after Down.
func TestDataMigration(t *testing.T) {
	dsn := newDB(t)
	g := newGoose(t, migrDir(t, "seed"), nil)
	ctx := context.Background()
	if err := g.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}

	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer conn.Close()

	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM dict`).Scan(&n); err != nil {
		t.Fatalf("count dict rows: %v", err)
	}
	if n != 2 {
		t.Errorf("dict rows = %d, want 2", n)
	}
	var name string
	if err := conn.QueryRow(`SELECT name FROM dict WHERE id = 2`).Scan(&name); err != nil {
		t.Fatalf("query dict row: %v", err)
	}
	if name != "beta" {
		t.Errorf("dict[2].name = %q, want beta", name)
	}

	// Down rolls the file back — the table (and its rows) disappear.
	if err := g.Down(ctx); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if tableExists(t, dsn, "dict") {
		t.Error("dict table should be gone after Down")
	}
}

//go:embed testdata/embed/*.sql
var embedMigrations embed.FS

// TestWithFS_Embed: an embed.FS plus a dir subpath behaves like a disk
// directory (same fs.Sub path the WithFS injection takes).
func TestWithFS_Embed(t *testing.T) {
	dsn := newDB(t)
	p, err := goose.NewPlugin(nil, goose.WithFS(embedMigrations))
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}
	// Config defaults read the global config; dir must point at the embedded
	// subdirectory, auto migrates at Start.
	setGlobalConfig(t, map[string]interface{}{
		"goose.enabled": true,
		"goose.db":      "main",
		"goose.dir":     "testdata/embed",
		"goose.table":   "goose_db_version",
		"goose.auto":    true,
	})
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !tableExists(t, dsn, "embed_table") {
		t.Error("embed_table should exist after Start with embedded migrations")
	}
	if v, err := p.Goose().Version(context.Background()); err != nil || v != 1 {
		t.Errorf("Version = %d, %v; want 1", v, err)
	}
}

// ---- plugin lifecycle ----

func setPluginConfig(t *testing.T, dbID, dir string, enabled, auto bool) {
	t.Helper()
	setGlobalConfig(t, map[string]interface{}{
		"goose.enabled": enabled,
		"goose.db":      dbID,
		"goose.dir":     dir,
		"goose.table":   "goose_db_version",
		"goose.auto":    auto,
	})
}

func TestPluginStart_Auto(t *testing.T) {
	dsn := newDB(t)
	setPluginConfig(t, "main", initialDir(t), true, true)
	p, err := goose.NewPlugin(nil)
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !tableExists(t, dsn, "users") || !tableExists(t, dsn, "posts") {
		t.Error("tables should exist right after Start with auto: true")
	}
	if p.Goose() == nil {
		t.Error("Goose() should return the Start-built instance")
	}
}

func TestPluginStart_AutoFalse(t *testing.T) {
	dsn := newDB(t)
	setPluginConfig(t, "main", initialDir(t), true, false)
	p, err := goose.NewPlugin(nil)
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if tableExists(t, dsn, "users") || tableExists(t, dsn, "goose_db_version") {
		t.Error("no schema changes expected after Start with auto: false")
	}
	ctx := context.Background()
	if err := goose.Up(ctx); err != nil {
		t.Fatalf("package-level Up: %v", err)
	}
	if !tableExists(t, dsn, "users") {
		t.Error("users table should exist after explicit Up")
	}
}

func TestPluginStart_Disabled(t *testing.T) {
	dsn := newDB(t)
	setPluginConfig(t, "main", initialDir(t), false, true)
	p, err := goose.NewPlugin(nil)
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}
	if err := p.Start(); err != nil {
		t.Fatalf("Start with enabled: false should be a no-op, got: %v", err)
	}
	if p.Goose() != nil {
		t.Error("Goose() should stay nil when disabled")
	}
	if tableExists(t, dsn, "goose_db_version") {
		t.Error("disabled plugin must not touch the database")
	}
}

func TestPluginStart_FailFast(t *testing.T) {
	newDB(t)
	setPluginConfig(t, "main", migrDir(t, "broken"), true, true)
	p, err := goose.NewPlugin(nil)
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}
	if err := p.Start(); err == nil {
		t.Fatal("Start with a broken migration must fail (fail-fast), got nil")
	}
}

// TestPackageLevelAPI: after a Start (auto: false here), the package-level
// helpers delegate to the installed default instance.
func TestPackageLevelAPI(t *testing.T) {
	dsn := newDB(t)
	setPluginConfig(t, "main", initialDir(t), true, false)
	p, err := goose.NewPlugin(nil)
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if goose.Use() != p.Goose() {
		t.Error("Use() should return the instance installed by Start")
	}

	ctx := context.Background()
	if err := goose.Up(ctx); err != nil {
		t.Fatalf("package-level Up: %v", err)
	}
	if v, err := goose.Version(ctx); err != nil || v != 2 {
		t.Errorf("package-level Version = %d, %v; want 2", v, err)
	}
	rows, err := goose.Status(ctx)
	if err != nil || len(rows) != 2 {
		t.Errorf("package-level Status = %d rows, %v; want 2 rows, nil error", len(rows), err)
	}
	if !tableExists(t, dsn, "users") {
		t.Error("users table should exist after package-level Up")
	}
}
