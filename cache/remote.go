package cache

import (
	"fmt"

	"github.com/mgtv-tech/jetcache-go/remote"
	"github.com/redis/go-redis/v9"
)

// buildRedisClient builds a redis.Cmdable from cfg. A non-empty Addrs map yields
// a Ring; otherwise Addr yields a single-node Client. Both empty is an error.
func buildRedisClient(cfg RedisConfig) (redis.Cmdable, error) {
	if len(cfg.Addrs) > 0 {
		return redis.NewRing(&redis.RingOptions{
			Addrs:    cfg.Addrs,
			Username: cfg.Username,
			Password: cfg.Password,
			DB:       cfg.DB,
			PoolSize: cfg.PoolSize,
		}), nil
	}
	if cfg.Addr != "" {
		return redis.NewClient(&redis.Options{
			Addr:     cfg.Addr,
			Username: cfg.Username,
			Password: cfg.Password,
			DB:       cfg.DB,
			PoolSize: cfg.PoolSize,
		}), nil
	}
	return nil, fmt.Errorf("redis addr(s) required")
}

// buildRemoteCache wraps a redis client as a jetcache Remote.
func buildRemoteCache(cli redis.Cmdable) remote.Remote {
	return remote.NewGoRedisV9Adapter(cli)
}
