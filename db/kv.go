package db

import "sort"

// Kv is a fluent, named map[string]interface{} — the Go counterpart of Java
// aifei-db util/Kv. It embeds a plain map directly, so it can be passed straight
// to the SqlKit without conversion:
//
//	kv := db.NewKv().Set("name", "aifei").Set("age", 18)
//	rows, _ := db.Sql("select * from user where name = #para(name)", kv)
//
// 与 Java 的差异：键为 string（Java 为 Object）；Go map 本身无序，故不保插入序、不排序。
type Kv map[string]interface{}

// NewKv creates an empty Kv (a ready-to-write map).
func NewKv() Kv { return Kv{} }

// OfKv creates a Kv seeded with one entry.
func OfKv(key string, value interface{}) Kv {
	return NewKv().Set(key, value)
}

// Set sets a value and returns k for chaining. The underlying map is mutated in place.
func (k Kv) Set(key string, value interface{}) Kv {
	k[key] = value
	return k
}

// SetMap merges a map into k.
func (k Kv) SetMap(data map[string]interface{}) Kv {
	for key, v := range data {
		k[key] = v
	}
	return k
}

// Unset removes a key.
func (k Kv) Unset(key string) Kv {
	delete(k, key)
	return k
}

// SetIfNotBlank sets a string value only when non-empty.
func (k Kv) SetIfNotBlank(key, value string) Kv {
	if value != "" {
		k[key] = value
	}
	return k
}

// SetIfNotNull sets a value only when non-nil.
func (k Kv) SetIfNotNull(key string, value interface{}) Kv {
	if value != nil {
		k[key] = value
	}
	return k
}

// Get returns a value (nil if absent).
func (k Kv) Get(key string) interface{} { return k[key] }

// GetDefault returns a value, or def when absent or nil.
func (k Kv) GetDefault(key string, def interface{}) interface{} {
	if v, ok := k[key]; ok && v != nil {
		return v
	}
	return def
}

// GetStr returns a value as string.
func (k Kv) GetStr(key string) string { return ToString(k[key]) }

// GetStrDefault returns a value as string, or def when absent or nil.
func (k Kv) GetStrDefault(key, def string) string {
	if v, ok := k[key]; ok && v != nil {
		return ToString(v)
	}
	return def
}

// GetInt returns a value as int.
func (k Kv) GetInt(key string) int { return ToInt(k[key]) }

// GetIntDefault returns a value as int, or def when absent or nil.
func (k Kv) GetIntDefault(key string, def int) int {
	if v, ok := k[key]; ok && v != nil {
		return ToInt(v)
	}
	return def
}

// GetInt64 returns a value as int64.
func (k Kv) GetInt64(key string) int64 { return ToInt64(k[key]) }

// GetInt64Default returns a value as int64, or def when absent or nil.
func (k Kv) GetInt64Default(key string, def int64) int64 {
	if v, ok := k[key]; ok && v != nil {
		return ToInt64(v)
	}
	return def
}

// GetFloat64 returns a value as float64.
func (k Kv) GetFloat64(key string) float64 { return ToFloat64(k[key]) }

// GetFloat64Default returns a value as float64, or def when absent or nil.
func (k Kv) GetFloat64Default(key string, def float64) float64 {
	if v, ok := k[key]; ok && v != nil {
		return ToFloat64(v)
	}
	return def
}

// GetBool returns a value as bool.
func (k Kv) GetBool(key string) bool { return ToBool(k[key]) }

// GetBoolDefault returns a value as bool, or def when absent or nil.
func (k Kv) GetBoolDefault(key string, def bool) bool {
	if v, ok := k[key]; ok && v != nil {
		return ToBool(v)
	}
	return def
}

// Has reports whether a key exists.
func (k Kv) Has(key string) bool { _, ok := k[key]; return ok }

// NotNull reports whether a key exists with a non-nil value.
func (k Kv) NotNull(key string) bool { v, ok := k[key]; return ok && v != nil }

// IsNull reports whether a key is absent or nil.
func (k Kv) IsNull(key string) bool { v, ok := k[key]; return !ok || v == nil }

// Len returns the number of entries.
func (k Kv) Len() int { return len(k) }

// Keys returns the keys (unordered, like any Go map).
func (k Kv) Keys() []string {
	out := make([]string, 0, len(k))
	for key := range k {
		out = append(out, key)
	}
	return out
}

// SortedKeys returns the keys sorted alphabetically — on-demand deterministic
// ordering for stable SQL field lists, cache keys, or logging. Kv itself stays
// unordered; only this call sorts. Use SortedKeysBy for a custom comparator.
func (k Kv) SortedKeys() []string {
	out := k.Keys()
	sort.Strings(out)
	return out
}

// SortedKeysBy returns the keys ordered by less. Nil falls back to alphabetical.
func (k Kv) SortedKeysBy(less func(a, b string) bool) []string {
	out := k.Keys()
	if less == nil {
		sort.Strings(out)
	} else {
		sort.SliceStable(out, func(i, j int) bool { return less(out[i], out[j]) })
	}
	return out
}

// ForEach iterates entries (unordered).
func (k Kv) ForEach(fn func(key string, value interface{})) {
	for key, v := range k {
		fn(key, v)
	}
}

// Keep retains only the given keys, dropping the rest.
func (k Kv) Keep(keys ...string) Kv {
	keep := make(map[string]bool, len(keys))
	for _, kk := range keys {
		keep[kk] = true
	}
	for key := range k {
		if !keep[key] {
			delete(k, key)
		}
	}
	return k
}

// Map returns k as a plain map[string]interface{} (a view, not a copy). Kv is
// already directly assignable to map[string]interface{}, so this is only for
// callers that want the conversion to be explicit.
func (k Kv) Map() map[string]interface{} { return map[string]interface{}(k) }

// KvAs returns a value converted by fn (nil-safe: fn is NOT called when the
// value is absent or nil — the zero T is returned). Mirrors Java Kv.getAs.
func KvAs[T any](k Kv, key string, fn func(interface{}) T) T {
	v, ok := k[key]
	if !ok || v == nil {
		var zero T
		return zero
	}
	return fn(v)
}
