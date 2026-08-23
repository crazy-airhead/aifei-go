package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	dbsql "github.com/crazy-airhead/aifei-go/db/sql"
)

// Config holds database connection configuration.
type Config struct {
	ID               string
	DriverName       string
	DSN              string
	Dialect          Dialect
	MaxOpen          int
	MaxIdle          int
	MaxLife          time.Duration
	Printer          func(sql string, args ...interface{})
	SqlKit           *dbsql.SqlKit
	HookKit          *DbHookKit
	AutoTableMapping bool // opt-in: auto-parse raw SQL for multi-table column mapping
	pool             *sql.DB
	mu               sync.Mutex // guards pool and SqlKit lazy init; Config must not be copied by value
}

// ConfigOption is a functional option for Config.
type ConfigOption func(*Config)

// WithDialect sets the database dialect.
func WithDialect(d Dialect) ConfigOption {
	return func(c *Config) { c.Dialect = d }
}

// WithMaxOpen sets max open connections.
func WithMaxOpen(n int) ConfigOption {
	return func(c *Config) { c.MaxOpen = n }
}

// WithMaxIdle sets max idle connections.
func WithMaxIdle(n int) ConfigOption {
	return func(c *Config) { c.MaxIdle = n }
}

// WithMaxLife sets connection max lifetime.
func WithMaxLife(d time.Duration) ConfigOption {
	return func(c *Config) { c.MaxLife = d }
}

// WithPrinter sets the SQL log printer.
func WithPrinter(fn func(string, ...interface{})) ConfigOption {
	return func(c *Config) { c.Printer = fn }
}

// WithSqlKit sets a custom SqlKit for Enjoy SQL support.
func WithSqlKit(sk *dbsql.SqlKit) ConfigOption {
	return func(c *Config) { c.SqlKit = sk }
}

// WithHookKit sets the database hook kit.
func WithHookKit(hk *DbHookKit) ConfigOption {
	return func(c *Config) { c.HookKit = hk }
}

// WithAutoTableMapping enables automatic SQL parsing for multi-table column mapping.
func WithAutoTableMapping(b bool) ConfigOption {
	return func(c *Config) { c.AutoTableMapping = b }
}

// GetDbHookKit returns the hook kit, may be nil.
func (c *Config) GetDbHookKit() *DbHookKit {
	return c.HookKit
}

// GetSqlKit returns the SqlKit, creating a default one if nil.
func (c *Config) GetSqlKit() *dbsql.SqlKit {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.SqlKit == nil {
		c.SqlKit = dbsql.NewSqlKit(c.ID)
	}
	return c.SqlKit
}

var (
	configsMu sync.RWMutex
	configs   = map[string]*Config{}
)
var defaultConfigID = "main"

// Init initializes the default database.
func Init(driverName, dsn string, opts ...ConfigOption) error {
	return InitWithID(defaultConfigID, driverName, dsn, opts...)
}

// InitWithID initializes a database with a specific config ID.
func InitWithID(configID, driverName, dsn string, opts ...ConfigOption) error {
	c := &Config{
		ID:         configID,
		DriverName: driverName,
		DSN:        dsn,
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.Dialect == nil {
		c.Dialect = NewDialect(driverName)
	}
	configsMu.Lock()
	configs[configID] = c
	configsMu.Unlock()
	return nil
}

// GetConfig returns a Config by ID (default "main").
func GetConfig(id ...string) *Config {
	key := defaultConfigID
	if len(id) > 0 && id[0] != "" {
		key = id[0]
	}
	configsMu.RLock()
	defer configsMu.RUnlock()
	return configs[key]
}

// Pool returns the sql.DB connection pool (lazy init). Concurrent calls are
// safe: the first caller initializes the pool while others wait, and every
// caller receives the same *sql.DB instance.
func (c *Config) Pool() (*sql.DB, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pool != nil {
		return c.pool, nil
	}
	db, err := sql.Open(c.DriverName, c.DSN)
	if err != nil {
		return nil, fmt.Errorf("db open error: %w", err)
	}
	if c.MaxOpen > 0 {
		db.SetMaxOpenConns(c.MaxOpen)
	}
	if c.MaxIdle > 0 {
		db.SetMaxIdleConns(c.MaxIdle)
	}
	if c.MaxLife > 0 {
		db.SetConnMaxLifetime(c.MaxLife)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("db ping error: %w", err)
	}
	c.pool = db
	return db, nil
}

// runner returns the DBConn that SQL executes on: the *sql.Tx carried by ctx
// when inside a transaction, otherwise the connection pool. A nil ctx safely
// falls back to the pool, so callers that do not propagate a ctx behave exactly
// as before.
func (c *Config) runner(ctx context.Context) (DBConn, error) {
	if tx, ok := txFromContext(ctx); ok {
		return tx, nil
	}
	return c.Pool()
}

// CreateDao creates a new Dao instance.
func (c *Config) CreateDao() *Dao {
	return &Dao{config: c}
}

// GetDialect returns the dialect.
func (c *Config) GetDialect() Dialect {
	return c.Dialect
}

// Close releases the connection pool. It is idempotent and safe for
// concurrent use with Pool.
func (c *Config) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pool != nil {
		c.pool.Close()
		c.pool = nil
	}
}

// ResetConfigs removes all registered configs and closes their pools (for testing).
func ResetConfigs() {
	configsMu.Lock()
	defer configsMu.Unlock()
	for _, c := range configs {
		c.Close()
	}
	configs = map[string]*Config{}
}

// logSQL logs SQL and params if printer is set.
func (c *Config) logSQL(sql string, args ...interface{}) {
	if c.Printer == nil {
		return
	}
	sql = strings.Join(strings.Fields(sql), " ")
	c.Printer(fmt.Sprintf("[SQL]  %s", sql))
	if len(args) > 0 {
		c.Printer(fmt.Sprintf("[PARAM] %v", args))
	}
}
