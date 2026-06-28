package cache

import (
	"github.com/mgtv-tech/jetcache-go/local"
)

// defaultLocalSizeBytes is the freecache size used when Size is unset, matching
// jetcache's own fallback.
const defaultLocalSizeBytes = 256 * local.MB

// buildLocalCache builds a jetcache Local from cfg. A nil cfg returns nil.
//
// Note: jetcache's FreeCache shares one process-global backing store across all
// instances (isolated only by key). This module isolates instances by key
// prefix (see JetCache.fullKey), so differently-sized FreeCache instances still
// share memory; use TinyLFU when instances need a separate memory budget.
func buildLocalCache(cfg *LocalConfig) local.Local {
	if cfg == nil {
		return nil
	}
	ttl := ttlSeconds(cfg.TTL)
	switch LocalDriver(cfg.Driver) {
	case LocalTinyLFU:
		size := cfg.Size
		if size <= 0 {
			size = 10000
		}
		return local.NewTinyLFU(size, ttl)
	default: // freecache (default)
		size := local.Size(cfg.Size)
		if size <= 0 {
			size = defaultLocalSizeBytes
		}
		return local.NewFreeCache(size, ttl)
	}
}
