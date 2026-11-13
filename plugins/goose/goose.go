// Package goose integrates the pressly/goose/v3 migration engine with the
// aifei plugin lifecycle and the db module's connection pool.
//
// The plugin reads the goose.* config section, resolves the target db.Config,
// and on Start (auto: true) converges the schema to the latest version — a
// failed migration aborts startup (fail-fast is the point). Migrations run
// once at startup; there are no background resources to stop.
//
// Configure in app.yml:
//
//	goose:
//	  db: main
//	  dir: migrations
//	  auto: true
//
// or inject embedded migration files with gooseplugin.WithFS(migrationsFS).
// The goose CLI (github.com/pressly/goose/v3/cmd/goose) operates on the same
// migration files and version table, so the two stay fully interoperable.
package goose

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	goosev3 "github.com/pressly/goose/v3"

	"github.com/crazy-airhead/aifei-go/db"
	"github.com/crazy-airhead/aifei-go/log"
)

// StatusRow is a snapshot of a single migration's state — a plugin-owned type
// that keeps underlying library types out of the public API.
type StatusRow struct {
	Version   int64
	FileName  string
	AppliedAt time.Time // zero value = not applied
	Applied   bool
}

// Goose is a migration engine bound to one db.Config, a thin wrapper over the
// underlying goose provider. All methods map one-to-one onto goose CLI
// commands and take a context.
type Goose struct {
	provider *goosev3.Provider
	cfg      *Config
	logger   log.Logger
}

// New builds an engine from cfg:
//  1. db.GetConfig(cfg.DBID) nil → error (fail-fast: db not initialized)
//  2. db.Config.Pool() shares the existing connection pool (never closed here)
//  3. DriverName → goose dialect mapping (unknown driver errors)
//  4. fsys nil → os.DirFS(cfg.Dir); otherwise fs.Sub(fsys, cfg.Dir) unless Dir is "." or empty
//  5. goosev3.NewProvider(dialect, pool, src, table/logger/verbose/... options)
func New(cfg *Config, fsys fs.FS, logger log.Logger) (*Goose, error) {
	if cfg == nil {
		return nil, fmt.Errorf("goose: config is nil")
	}
	if logger == nil {
		logger = log.Default()
	}
	dc := db.GetConfig(cfg.DBID)
	if dc == nil {
		return nil, fmt.Errorf("goose: db config %q not initialized (call db.InitWithID first)", cfg.DBID)
	}
	dialect, err := dialectOf(dc.DriverName)
	if err != nil {
		return nil, err
	}
	warnMySQLDSN(logger, dc.DriverName, dc.DSN)

	pool, err := dc.Pool()
	if err != nil {
		return nil, fmt.Errorf("goose: connect to db %q failed: %w", cfg.DBID, err)
	}

	src := fsys
	if src == nil {
		src = os.DirFS(cfg.Dir)
	} else if cfg.Dir != "" && cfg.Dir != "." {
		sub, err := fs.Sub(src, cfg.Dir)
		if err != nil {
			return nil, fmt.Errorf("goose: resolve dir %q in injected fs failed: %w", cfg.Dir, err)
		}
		src = sub
	}

	opts := []goosev3.ProviderOption{
		goosev3.WithTableName(cfg.Table),
		goosev3.WithVerbose(cfg.Verbose),
		goosev3.WithLogger(gooseLogger{logger}),
	}
	if cfg.AllowMissing {
		opts = append(opts, goosev3.WithAllowOutofOrder(true))
	}
	if cfg.NoVersioning {
		opts = append(opts, goosev3.WithDisableVersioning(true))
	}
	p, err := goosev3.NewProvider(dialect, pool, src, opts...)
	if err != nil {
		return nil, fmt.Errorf("goose: create provider failed: %w", err)
	}
	return &Goose{provider: p, cfg: cfg, logger: logger}, nil
}

// dialectOf maps a database/sql driver name to the goose dialect.
func dialectOf(driverName string) (goosev3.Dialect, error) {
	switch driverName {
	case "mysql":
		return goosev3.DialectMySQL, nil
	case "postgres":
		return goosev3.DialectPostgres, nil
	case "sqlite", "sqlite3":
		return goosev3.DialectSQLite3, nil
	default:
		var zero goosev3.Dialect
		return zero, fmt.Errorf("goose: unsupported driver %q (supported: mysql, postgres, sqlite)", driverName)
	}
}

// warnMySQLDSN warns when a mysql DSN lacks multiStatements: goose requires
// multiStatements=true (multi-statement migration files) and parseTime=true
// (version-table timestamps). Advisory only — parameter naming differs across
// mysql drivers, so the plugin does not block startup on this.
func warnMySQLDSN(logger log.Logger, driverName, dsn string) {
	if driverName != "mysql" {
		return
	}
	if !strings.Contains(dsn, "multiStatements") {
		logger.Warn("goose: mysql DSN lacks multiStatements=true — migration files with multiple statements will fail (goose requires multiStatements=true and parseTime=true)")
	}
}

// gooseLogger adapts log.Logger to goose's Logger interface.
type gooseLogger struct{ l log.Logger }

func (a gooseLogger) Fatalf(format string, v ...any) { a.l.Error(format, v...) }
func (a gooseLogger) Printf(format string, v ...any) { a.l.Info(format, v...) }

// Up applies all pending migrations.
func (g *Goose) Up(ctx context.Context) error {
	_, err := g.provider.Up(ctx)
	return err
}

// UpByOne applies the next pending migration only.
func (g *Goose) UpByOne(ctx context.Context) error {
	_, err := g.provider.UpByOne(ctx)
	return err
}

// UpTo applies pending migrations up to, and including, version.
func (g *Goose) UpTo(ctx context.Context, version int64) error {
	_, err := g.provider.UpTo(ctx, version)
	return err
}

// Down rolls back the most recently applied migration.
func (g *Goose) Down(ctx context.Context) error {
	_, err := g.provider.Down(ctx)
	return err
}

// DownTo rolls back migrations down to, but not including, version.
func (g *Goose) DownTo(ctx context.Context, version int64) error {
	_, err := g.provider.DownTo(ctx, version)
	return err
}

// Reset rolls back all migrations (equivalent to DownTo(0) — the provider has
// no dedicated Reset).
func (g *Goose) Reset(ctx context.Context) error {
	return g.DownTo(ctx, 0)
}

// Status returns the state of every known migration, ordered by version.
func (g *Goose) Status(ctx context.Context) ([]StatusRow, error) {
	rows, err := g.provider.Status(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]StatusRow, 0, len(rows))
	for _, r := range rows {
		row := StatusRow{Applied: r.State == goosev3.StateApplied}
		if r.Source != nil {
			row.Version = r.Source.Version
			row.FileName = r.Source.Path
		}
		if row.Applied {
			row.AppliedAt = r.AppliedAt
		}
		out = append(out, row)
	}
	return out, nil
}

// Version returns the current database version (0 when no migration applied yet).
func (g *Goose) Version(ctx context.Context) (int64, error) {
	return g.provider.GetDBVersion(ctx)
}

// Raw returns the underlying goose provider — the escape hatch for advanced
// usage (session lockers, Go-migration registration, ...). Thin-assembly
// principle: no second abstraction over library capabilities.
func (g *Goose) Raw() *goosev3.Provider {
	return g.provider
}
