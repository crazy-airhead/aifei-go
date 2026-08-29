package cache

import (
	"context"
	"time"
)

// This file hosts the typed cache entries (see docs/arch/generic-methods.md).
// Cache is an interface, and Go still forbids type parameters on interface
// methods, so the generic forms are package-level helpers over the default
// instance — value-returning counterparts of the dest-pointer forms, with a
// typed loader for GetOrStore.

// GetAs fetches a value from the default instance into a fresh T.
// found is false on a miss (nil error); dest-pointer Get callers migrate as:
//
//	var u User
//	found, err := cache.Get(ctx, key, &u)
//
// becomes:
//
//	u, found, err := cache.GetAs[User](ctx, key)
func GetAs[T any](ctx context.Context, key string) (T, bool, error) {
	var t T
	found, err := Get(ctx, key, &t)
	return t, found, err
}

// GetOrStoreAs gets-or-loads a value via the default instance, returning a
// fresh T. The loader returns T directly — the typed counterpart of the
// any-returning Loader that the non-generic interface requires.
//
//	u, err := cache.GetOrStoreAs[User](ctx, key, func(ctx context.Context) (User, error) {
//		return loadUser(ctx, id)
//	})
func GetOrStoreAs[T any](ctx context.Context, key string, loader func(context.Context) (T, error), ttl ...time.Duration) (T, error) {
	var t T
	err := GetOrStore(ctx, key, &t, func(ctx context.Context) (any, error) {
		return loader(ctx)
	}, ttl...)
	return t, err
}
