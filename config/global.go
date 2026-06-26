package config

// globalProps is the package-level configuration instance.
// It is automatically set by Init(). Callers may also set it explicitly via
// setProps() before any concurrent reads.
//
// The package-level functions (Get, GetStr, GetBool, GetInt, GetInt64,
// GetFloat64, Has, Keys, Set, Sub, SubBind, Bind) operate on this instance.
// When globalProps is nil, all getters return their zero value (or the
// provided default), and Set is a no-op.
var globalProps *Props

// setProps sets the global configuration instance.
// It is safe to call before any concurrent reads.
func setProps(p *Props) {
	globalProps = p
}

// Get retrieves a value from the global Props by dot-separated key.
// Returns def[0] if the global is nil, the key is not found, or the value is nil.
func Get(key string, def ...interface{}) interface{} {
	if globalProps == nil {
		if len(def) > 0 {
			return def[0]
		}
		return nil
	}
	return globalProps.Get(key, def...)
}

// GetStr retrieves a string value from the global Props.
// Returns def[0] if the global is nil, the key is not found, the value is nil,
// or the value is an empty string.
func GetStr(key string, def ...string) string {
	if globalProps == nil {
		if len(def) > 0 {
			return def[0]
		}
		return ""
	}
	return globalProps.GetStr(key, def...)
}

// GetBool retrieves a boolean value from the global Props.
// Returns def[0] if the global is nil, the key is not found, or the value is non-bool.
func GetBool(key string, def ...bool) bool {
	if globalProps == nil {
		if len(def) > 0 {
			return def[0]
		}
		return false
	}
	return globalProps.GetBool(key, def...)
}

// GetInt retrieves an int value from the global Props.
// Returns def[0] if the global is nil, the key is not found, or the value is non-numeric.
func GetInt(key string, def ...int) int {
	if globalProps == nil {
		if len(def) > 0 {
			return def[0]
		}
		return 0
	}
	return globalProps.GetInt(key, def...)
}

// GetInt64 retrieves an int64 value from the global Props.
// Returns def[0] if the global is nil, the key is not found, or the value is non-numeric.
func GetInt64(key string, def ...int64) int64 {
	if globalProps == nil {
		if len(def) > 0 {
			return def[0]
		}
		return 0
	}
	return globalProps.GetInt64(key, def...)
}

// GetFloat64 retrieves a float64 value from the global Props.
// Returns def[0] if the global is nil, the key is not found, or the value is non-numeric.
func GetFloat64(key string, def ...float64) float64 {
	if globalProps == nil {
		if len(def) > 0 {
			return def[0]
		}
		return 0
	}
	return globalProps.GetFloat64(key, def...)
}

// Has reports whether a key exists in the global Props.
// Returns false if the global is nil.
func Has(key string) bool {
	if globalProps == nil {
		return false
	}
	return globalProps.Has(key)
}

// Keys returns all top-level keys in the global Props.
// Returns nil if the global is nil.
func Keys() []string {
	if globalProps == nil {
		return nil
	}
	return globalProps.Keys()
}

// Set stores a value at the given dot-separated key in the global Props.
// No-op if the global is nil.
func Set(key string, value interface{}) {
	if globalProps == nil {
		return
	}
	globalProps.Set(key, value)
}

// Sub returns a new Props scoped to the given prefix from the global Props.
// Returns an empty Props if the global is nil.
func Sub(prefix string) *Props {
	if globalProps == nil {
		return NewProps()
	}
	return globalProps.Sub(prefix)
}

// SubBind binds the subtree at prefix to v from the global Props via YAML round-trip.
// Returns nil if the global is nil or the prefix does not exist.
func SubBind(prefix string, v interface{}) error {
	if globalProps == nil {
		return nil
	}
	return globalProps.SubBind(prefix, v)
}

// Bind unmarshals the entire global Props into a typed struct via YAML round-trip.
// Returns nil if the global is nil.
func Bind(v interface{}) error {
	if globalProps == nil {
		return nil
	}
	return globalProps.Bind(v)
}
