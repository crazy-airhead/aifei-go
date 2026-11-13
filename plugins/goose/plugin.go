package goose

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sync"

	"github.com/crazy-airhead/aifei-go/aifei"
	"github.com/crazy-airhead/aifei-go/log"
)

// Compile-time assertion: Plugin participates in the aifei plugin lifecycle.
var _ aifei.Plugin = (*Plugin)(nil)

// errNotStarted is returned by package-level helpers when no default instance
// has been installed (plugin not started, or disabled in config).
var errNotStarted = errors.New("goose: plugin not started (no default instance installed)")

// Plugin wires the migration capability into the aifei lifecycle.
//
// Start: LoadConfig → enabled? → New → auto? Up with timeout → install the
// package-level default instance.
// Stop: no-op — migrations are a one-shot startup action with no background
// resources; the connection pool belongs to db.Config.
type Plugin struct {
	prefix string
	log    log.Logger
	fsys   fs.FS // injected via WithFS; nil = read Dir from disk
	g      *Goose
}

// Option configures the Plugin.
type Option func(*Plugin)

// WithFS injects a migration source filesystem (typically an embed.FS — the
// recommended setup, see docs/arch/goose-plugin.md §12).
func WithFS(fsys fs.FS) Option {
	return func(p *Plugin) { p.fsys = fsys }
}

// WithPrefix overrides the config section prefix ("" defaults to "goose").
func WithPrefix(prefix string) Option {
	return func(p *Plugin) { p.prefix = prefix }
}

// NewPlugin builds the plugin. A nil logger falls back to log.Default().
func NewPlugin(logger log.Logger, opts ...Option) (*Plugin, error) {
	if logger == nil {
		logger = log.Default()
	}
	p := &Plugin{prefix: DefaultPrefix, log: logger}
	for _, opt := range opts {
		opt(p)
	}
	return p, nil
}

// Start loads the config section, resolves the db.Config, and — when auto is
// enabled — converges the schema. It returns an error when: db is not
// initialized, the migration source is missing/unreadable, the driver is
// unsupported, or a migration fails. Any of these makes server.Run
// log.Fatalf — refusing to boot on a broken schema is a feature, not a bug
// (docs/arch/goose-plugin.md §9.5).
func (p *Plugin) Start() error {
	cfg := LoadConfig(p.prefix)
	if !cfg.Enabled {
		return nil
	}
	g, err := New(cfg, p.fsys, p.log)
	if err != nil {
		return err
	}
	p.g = g

	if cfg.Auto {
		ctx := context.Background()
		if cfg.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
			defer cancel()
		}
		if err := g.Up(ctx); err != nil {
			return fmt.Errorf("goose: auto up failed: %w", err)
		}
		p.log.Info("[goose] migrations applied (db=%s table=%s)", cfg.DBID, cfg.Table)
	}

	defaultMu.Lock()
	defaultGoose = g
	defaultMu.Unlock()
	return nil
}

// Stop is a no-op: nothing to release — the shared pool stays with db.Config.
func (p *Plugin) Stop() error { return nil }

// Goose returns the instance built during Start (use it for manual
// Up/Down when auto: false). Nil before a successful Start.
func (p *Plugin) Goose() *Goose { return p.g }

var (
	defaultMu    sync.RWMutex
	defaultGoose *Goose
)

// Use returns the default instance installed by Plugin.Start; nil when the
// plugin has not started (or is disabled).
func Use() *Goose {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultGoose
}

// Up applies all pending migrations on the default instance.
func Up(ctx context.Context) error {
	g := Use()
	if g == nil {
		return errNotStarted
	}
	return g.Up(ctx)
}

// UpByOne applies the next pending migration on the default instance.
func UpByOne(ctx context.Context) error {
	g := Use()
	if g == nil {
		return errNotStarted
	}
	return g.UpByOne(ctx)
}

// Down rolls back the latest migration on the default instance.
func Down(ctx context.Context) error {
	g := Use()
	if g == nil {
		return errNotStarted
	}
	return g.Down(ctx)
}

// Status returns migration states from the default instance.
func Status(ctx context.Context) ([]StatusRow, error) {
	g := Use()
	if g == nil {
		return nil, errNotStarted
	}
	return g.Status(ctx)
}

// Version returns the current database version from the default instance.
func Version(ctx context.Context) (int64, error) {
	g := Use()
	if g == nil {
		return 0, errNotStarted
	}
	return g.Version(ctx)
}
