package cache

import "strings"

// defaultCacheName is the instance name used when none is configured, mirroring
// storage.defaultBucketName.
const defaultCacheName = "cache"

// keySeparator joins the instance prefix and the user key.
const keySeparator = ":"

// CacheType identifies a cache topology.
type CacheType string

const (
	// CacheLocal is the in-process (L1) topology.
	CacheLocal CacheType = "local"
	// CacheRemote is the distributed (L2, Redis) topology.
	CacheRemote CacheType = "remote"
	// CacheBoth is the two-level (L1 + L2) topology.
	CacheBoth CacheType = "both"
)

// LocalDriver selects the L1 implementation.
type LocalDriver string

const (
	// LocalFreeCache is the sharded, byte-sized FreeCache backend (default).
	LocalFreeCache LocalDriver = "freecache"
	// LocalTinyLFU is the per-instance TinyLFU (ristretto) backend; size is an
	// entry count. Prefer it when multiple instances need real isolation.
	LocalTinyLFU LocalDriver = "tinylfu"
)

// cacheTypeOf infers the cache topology. An explicit type wins; otherwise both
// configured levels -> both, only remote -> remote, otherwise -> local.
func cacheTypeOf(typ string, hasLocal, hasRemote bool) CacheType {
	switch strings.ToLower(typ) {
	case "local":
		return CacheLocal
	case "remote":
		return CacheRemote
	case "both":
		return CacheBoth
	}
	// type empty or unknown: infer from which levels are configured.
	if hasRemote && hasLocal {
		return CacheBoth
	}
	if hasRemote {
		return CacheRemote
	}
	return CacheLocal
}
