package cache

import (
	"fmt"
	"time"

	"github.com/crazy-airhead/aifei-go/config"
)

// Config holds cache configuration: a default instance name and the
// per-instance settings.
type Config struct {
	Default   string                    `yaml:"default"`
	Instances map[string]InstanceConfig `yaml:"instances"`
}

// InstanceConfig describes a single named cache instance, which may be L1, L2,
// or both.
type InstanceConfig struct {
	// Type selects the topology: "local", "remote", "both". When empty it is
	// inferred from which of Local/Remote is configured.
	Type string `yaml:"type"`
	// TTL is the default remote (L2) expiry in seconds. 0 means the library
	// default (1 hour). Redis uses SETEX, so true "never expire" is not
	// supported for remote levels; remote values must have a positive TTL.
	TTL int `yaml:"ttl"`
	// Codec selects the serialization: "msgpack" (default), "json", or "sonic".
	// json/sonic must be imported by the application to register them.
	Codec string `yaml:"codec"`
	// KeyPrefix is an optional extra key prefix applied before the instance
	// name, e.g. "app" yields keys "app:<instance>:<key>".
	KeyPrefix string `yaml:"keyPrefix"`
	// Local is the L1 configuration; non-nil means L1 is configured.
	Local *LocalConfig `yaml:"local"`
	// Remote is the L2 configuration; non-nil means L2 is configured.
	Remote *RemoteConfig `yaml:"remote"`
	// Refresh enables optional background refresh.
	Refresh *RefreshConfig `yaml:"refresh"`
	// SyncLocal enables local-cache invalidation events for multi-instance L1
	// coherence (Both topology only). Wiring pub/sub broadcast is left to the
	// caller via the JetCache() escape hatch in v1.
	SyncLocal bool `yaml:"syncLocal"`
}

// LocalConfig holds the L1 (in-process) settings.
type LocalConfig struct {
	// Driver selects the L1 backend: "freecache" (default) or "tinylfu".
	Driver string `yaml:"driver"`
	// Size is the cache size. For freecache it is bytes (values outside
	// [512KB, 8GB] reset to 256MB); for tinylfu it is an entry count.
	Size int `yaml:"size"`
	// TTL is the local expiry in seconds; 0 means no expiry at the L1 level.
	TTL int `yaml:"ttl"`
}

// RemoteConfig holds the L2 (distributed) settings.
type RemoteConfig struct {
	// TTL is the remote expiry in seconds, falling back to the instance TTL;
	// a non-positive result means the library default (1 hour).
	TTL int `yaml:"ttl"`
	// Redis is the Redis connection.
	Redis RedisConfig `yaml:"redis"`
}

// RedisConfig configures the Redis client. Set Addr for a single node, or Addrs
// for a ring of shards; Addrs takes precedence when non-empty.
type RedisConfig struct {
	Addr     string            `yaml:"addr"`
	Addrs    map[string]string `yaml:"addrs"`
	Username string            `yaml:"username"`
	Password string            `yaml:"password"`
	DB       int               `yaml:"db"`
	PoolSize int               `yaml:"poolSize"`
}

// RefreshConfig holds optional background-refresh settings.
type RefreshConfig struct {
	// Duration is the refresh interval in seconds; <=0 disables refresh.
	Duration int `yaml:"duration"`
	// Concurrency is the max concurrent refreshes (default 4).
	Concurrency int `yaml:"concurrency"`
	// StopAfter stops refreshing a key this many seconds after its last access.
	StopAfter int `yaml:"stopAfter"`
}

// remoteTTL returns the effective remote (L2) expiry. Remote.TTL takes
// precedence over the instance TTL; a zero result means "use the library
// default".
func (c InstanceConfig) remoteTTL() time.Duration {
	if c.Remote != nil && c.Remote.TTL > 0 {
		return time.Duration(c.Remote.TTL) * time.Second
	}
	if c.TTL > 0 {
		return time.Duration(c.TTL) * time.Second
	}
	return 0
}

// prefixedName combines the optional KeyPrefix with the instance name to form
// the key prefix that isolates instances sharing one Redis.
func (c InstanceConfig) prefixedName(name string) string {
	if c.KeyPrefix != "" {
		return c.KeyPrefix + keySeparator + name
	}
	return name
}

// ttlSeconds converts seconds to a duration; non-positive becomes 0 (no
// expiry), which is meaningful for the L1 local level.
func ttlSeconds(sec int) time.Duration {
	if sec <= 0 {
		return 0
	}
	return time.Duration(sec) * time.Second
}

// LoadConfig reads cache configuration from the global config under prefix
// (empty defaults to "cache"). The instances subtree is bound via YAML
// round-trip.
func LoadConfig(prefix string) (*Config, error) {
	if prefix == "" {
		prefix = "cache"
	}
	cfg := &Config{Default: config.GetStr(prefix + ".default")}
	if err := config.SubBind(prefix+".instances", &cfg.Instances); err != nil {
		return nil, fmt.Errorf("cache: bind instances: %w", err)
	}
	return cfg, nil
}
