package goose

import (
	"time"

	"github.com/crazy-airhead/aifei-go/config"
)

// DefaultPrefix is the config section read by the Plugin when no
// WithPrefix option overrides it.
const DefaultPrefix = "goose"

// Config is the goose plugin configuration, loaded from config.Props by
// LoadConfig. All fields are flat scalars — see docs/arch/goose-plugin.md §7.
type Config struct {
	Enabled      bool          // false = skip everything (same as not installing the plugin)
	DBID         string        // target db.Config ID (db.InitWithID id)
	Dir          string        // migration directory; a subdirectory inside the WithFS fs when one is injected
	Table        string        // schema version table name
	Auto         bool          // run Up at Start (forward only; never Down)
	Timeout      time.Duration // startup migration timeout; 0 = unbounded
	Verbose      bool          // goose verbose (print each SQL statement)
	AllowMissing bool          // apply out-of-order migrations (dev-branch parallel commits)
	NoVersioning bool          // do not record applied versions (rarely useful)
}

// LoadConfig reads the prefix config section ("" defaults to "goose") from the
// global config with defaults expressed in place. timeout parses via
// time.ParseDuration; an empty or invalid value means no timeout.
func LoadConfig(prefix string) *Config {
	if prefix == "" {
		prefix = DefaultPrefix
	}
	cfg := &Config{
		Enabled:      config.GetBool(prefix+".enabled", true),
		DBID:         config.GetStr(prefix+".db", "main"),
		Dir:          config.GetStr(prefix+".dir", "migrations"),
		Table:        config.GetStr(prefix+".table", "goose_db_version"),
		Auto:         config.GetBool(prefix+".auto", true),
		Verbose:      config.GetBool(prefix+".verbose", false),
		AllowMissing: config.GetBool(prefix+".allowMissing", false),
		NoVersioning: config.GetBool(prefix+".noVersioning", false),
	}
	if s := config.GetStr(prefix + ".timeout"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			cfg.Timeout = d
		}
	}
	return cfg
}
