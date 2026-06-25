// Package config provides a layered configuration loading system.
// Configuration is stored in a generic key-value Store that can be populated
// from YAML files, environment variables, CLI arguments, and cloud config sources,
// then bound to any user-defined struct.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Props holds configuration as a nested key-value map.
// Keys use dot-separated paths (e.g., "server.port").
// It supports deep merging from YAML files, environment variables,
// and CLI arguments.
//
// Props is safe for concurrent use. Read methods acquire RLock;
// write methods (LoadYAML*, Set, MergeYAML, etc.) acquire a full Lock
// so dynamic config updates from CloudLoaders or external watchers
// (e.g., Nacos) do not race with in-flight requests.
type Props struct {
	mu   sync.RWMutex
	data map[string]interface{}
}

// NewProps creates an empty Props.
func NewProps() *Props {
	return &Props{data: make(map[string]interface{})}
}

// Get retrieves a value by dot-separated key.
// If the key is not found and def is provided, returns def[0].
func (p *Props) Get(key string, def ...interface{}) interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()
	v := p.get(key)
	if v == nil && len(def) > 0 {
		return def[0]
	}
	return v
}

// get retrieves a value without locking — caller must hold p.mu (read or write).
func (p *Props) get(key string) interface{} {
	if key == "" {
		return nil
	}
	parts := strings.Split(key, ".")
	var current interface{} = p.data
	for _, part := range parts {
		m, ok := current.(map[string]interface{})
		if !ok {
			return nil
		}
		current = m[part]
		if current == nil {
			return nil
		}
	}
	return current
}

// GetStr retrieves a string value by dot-separated key.
// Returns def[0] if the key is not found, the value is nil, or the value is an empty string.
func (p *Props) GetStr(key string, def ...string) string {
	v := p.Get(key)
	if v == nil {
		if len(def) > 0 {
			return def[0]
		}
		return ""
	}
	str, ok := v.(string)
	if !ok || str == "" {
		if len(def) > 0 {
			return def[0]
		}
		return str
	}
	return str
}

// GetBool retrieves a boolean value.
// Returns def[0] if the key is not found or the value is non-bool.
func (p *Props) GetBool(key string, def ...bool) bool {
	v := p.Get(key)
	if v == nil {
		if len(def) > 0 {
			return def[0]
		}
		return false
	}
	b, ok := v.(bool)
	if !ok {
		if len(def) > 0 {
			return def[0]
		}
		return false
	}
	return b
}

// GetInt retrieves an int value. Handles int, int64, and float64 (from YAML parsing).
// Returns def[0] if the key is not found or the value is non-numeric.
func (p *Props) GetInt(key string, def ...int) int {
	v := p.Get(key)
	if v == nil {
		if len(def) > 0 {
			return def[0]
		}
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		if len(def) > 0 {
			return def[0]
		}
		return 0
	}
}

// GetInt64 retrieves an int64 value. Handles int, int64, and float64 (from YAML parsing).
// Returns def[0] if the key is not found or the value is non-numeric.
func (p *Props) GetInt64(key string, def ...int64) int64 {
	v := p.Get(key)
	if v == nil {
		if len(def) > 0 {
			return def[0]
		}
		return 0
	}
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	default:
		if len(def) > 0 {
			return def[0]
		}
		return 0
	}
}

// GetFloat64 retrieves a float64 value. Handles int, int64, and float64.
// Returns def[0] if the key is not found or the value is non-numeric.
func (p *Props) GetFloat64(key string, def ...float64) float64 {
	v := p.Get(key)
	if v == nil {
		if len(def) > 0 {
			return def[0]
		}
		return 0
	}
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		if len(def) > 0 {
			return def[0]
		}
		return 0
	}
}

// Has reports whether a key exists in the props.
func (p *Props) Has(key string) bool {
	return p.Get(key) != nil
}

// Keys returns all top-level keys in the props.
func (p *Props) Keys() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	keys := make([]string, 0, len(p.data))
	for k := range p.data {
		keys = append(keys, k)
	}
	return keys
}

// Set stores a value at the given dot-separated key, creating intermediate
// maps as needed.
func (p *Props) Set(key string, value interface{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.set(key, value)
}

// set stores a value without locking — caller must hold p.mu.
func (p *Props) set(key string, value interface{}) {
	if key == "" {
		return
	}
	parts := strings.Split(key, ".")
	current := p.data
	for i := 0; i < len(parts)-1; i++ {
		part := parts[i]
		next, ok := current[part]
		if !ok {
			newMap := make(map[string]interface{})
			current[part] = newMap
			current = newMap
		} else {
			m, ok := next.(map[string]interface{})
			if !ok {
				// Overwrite non-map with a new map
				newMap := make(map[string]interface{})
				current[part] = newMap
				current = newMap
			} else {
				current = m
			}
		}
	}
	current[parts[len(parts)-1]] = value
}

// Sub returns a new Props scoped to the given prefix.
// Keys in the returned Props are relative to the prefix.
// The sub-props is independent from the parent; mutations to one
// do not affect the other.
//
// Example:
//
//	dbProps := props.Sub("db")
//	driver := dbProps.GetStr("driver")  // equivalent to props.GetStr("db.driver")
//	dbProps.Bind(&dbConfig)             // binds just the "db" subtree
//
// Returns an empty Props if the prefix does not exist.
func (p *Props) Sub(prefix string) *Props {
	p.mu.RLock()
	defer p.mu.RUnlock()
	v := p.get(prefix)
	if v == nil {
		return NewProps()
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		return NewProps()
	}
	// Deep copy the map so sub-props is independent
	sub := NewProps()
	for k, val := range m {
		sub.data[k] = copyValue(val)
	}
	return sub
}

// SubBind binds the subtree at prefix to v via YAML round-trip.
// It is a convenience shorthand for props.Sub(prefix).Bind(v).
//
// Example:
//
//	type DBConf struct {
//	    Driver string `yaml:"driver"`
//	    DSN    string `yaml:"dsn"`
//	}
//	var db DBConf
//	if err := props.SubBind("db", &db); err != nil { ... }
func (p *Props) SubBind(prefix string, v interface{}) error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	val := p.get(prefix)
	if val == nil {
		return nil // nothing to bind, leave v unchanged
	}
	m, ok := val.(map[string]interface{})
	if !ok {
		return nil
	}
	// YAML round-trip just the subtree
	b, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", prefix, err)
	}
	return yaml.Unmarshal(b, v)
}

// copyValue returns a deep copy of the value to ensure store independence.
func copyValue(v interface{}) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		copied := make(map[string]interface{})
		for k, nested := range val {
			copied[k] = copyValue(nested)
		}
		return copied
	case []interface{}:
		copied := make([]interface{}, len(val))
		for i, item := range val {
			copied[i] = copyValue(item)
		}
		return copied
	default:
		return v
	}
}

// Bind unmarshals the entire props into a typed struct via YAML round-trip.
// The caller defines the struct type with yaml tags.
//
// Example:
//
//	type MyConfig struct {
//	    Server ServerConf `yaml:"server"`
//	    DB     DBConf     `yaml:"db"`
//	}
//	var cfg MyConfig
//	if err := props.Bind(&cfg); err != nil { ... }
func (p *Props) Bind(v interface{}) error {
	p.mu.RLock()
	b, err := yaml.Marshal(p.data)
	p.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return yaml.Unmarshal(b, v)
}

// Data returns a shallow copy of the props's internal map.
func (p *Props) Data() map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make(map[string]interface{}, len(p.data))
	for k, v := range p.data {
		result[k] = v
	}
	return result
}

// LoadYAML reads a YAML file and deep-merges it into the props.
// Returns nil if the file does not exist (graceful skip).
func (p *Props) LoadYAML(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read config file %s: %w", path, err)
	}
	return p.mergeYAMLLocked(data)
}

// LoadYAMLBytes deep-merges raw YAML bytes into the props.
func (p *Props) LoadYAMLBytes(data []byte) error {
	return p.mergeYAMLLocked(data)
}

// LoadYAMLPattern loads all YAML files matching a glob pattern.
func (p *Props) LoadYAMLPattern(pattern string) error {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("glob pattern %s: %w", pattern, err)
	}
	for _, match := range matches {
		if err := p.LoadYAML(match); err != nil {
			return err
		}
	}
	return nil
}

// MergeYAML deep-merges raw YAML content into the props.
// Useful for cloud config (e.g., Nacos) where configuration arrives as a YAML string.
func (p *Props) MergeYAML(content []byte) error {
	return p.mergeYAMLLocked(content)
}

// mergeYAMLLocked parses YAML bytes and deep-merges the result into p.data
// under the write lock.
func (p *Props) mergeYAMLLocked(data []byte) error {
	var m map[string]interface{}
	if err := yaml.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("parse YAML: %w", err)
	}
	p.mu.Lock()
	if m != nil {
		deepMerge(p.data, m)
	}
	p.mu.Unlock()
	return nil
}

// LoadEnv loads environment variables with the given prefix into the props.
// The prefix is matched case-insensitively. Single underscores become dots;
// double underscores denote nesting boundaries (prevent dot-splitting).
//
// Examples with prefix "AIFEI":
//
//	AIFEI_SERVER_PORT=9090   sets "server.port" = "9090"
//	AIFEI_DB__DRIVER=mysql    sets "db.driver" = "mysql"
//	AIFEI_DEBUG=true          sets "debug" = "true"
func (p *Props) LoadEnv(prefix string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	prefix = strings.ToUpper(prefix) + "_"
	for _, kv := range os.Environ() {
		eqIdx := strings.IndexByte(kv, '=')
		if eqIdx < 0 {
			continue
		}
		key := kv[:eqIdx]
		value := kv[eqIdx+1:]

		if !strings.HasPrefix(strings.ToUpper(key), prefix) {
			continue
		}

		// Strip prefix
		key = key[len(prefix):]

		// Replace __ with a placeholder, then _ with ., then restore __ as .
		// This preserves double-underscore as a single dot (nesting boundary)
		// while converting single underscore to dot for the same nesting level.
		key = strings.ReplaceAll(key, "__", "\x00")
		key = strings.ReplaceAll(key, "_", ".")
		key = strings.ReplaceAll(key, "\x00", ".")

		key = strings.ToLower(key)
		p.set(key, value)
	}
}

// LoadArgs parses command-line arguments in --key=value and -key=value format.
// Dot-separated keys create nested paths.
// Positional arguments (without =) are silently ignored.
//
// Example:
//
//	--server.port=9090  sets "server.port" = "9090"
func (p *Props) LoadArgs(args []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, arg := range args {
		// Strip leading dashes
		trimmed := strings.TrimLeft(arg, "-")
		if trimmed == arg || trimmed == "" {
			// Not a --key=value or -key=value arg
			continue
		}

		eqIdx := strings.IndexByte(trimmed, '=')
		if eqIdx < 0 {
			// Positional argument, skip
			continue
		}

		key := trimmed[:eqIdx]
		value := trimmed[eqIdx+1:]

		if key == "" {
			continue
		}

		key = strings.ToLower(key)
		p.set(key, value)
	}
}

// deepMerge merges src into dst recursively.
// Nested maps are merged; scalar values are overwritten.
func deepMerge(dst, src map[string]interface{}) {
	for k, srcVal := range src {
		if dstVal, ok := dst[k]; ok {
			dstMap, dstOk := dstVal.(map[string]interface{})
			srcMap, srcOk := srcVal.(map[string]interface{})
			if dstOk && srcOk {
				deepMerge(dstMap, srcMap)
				continue
			}
		}
		// Overwrite or add new key (deep copy to avoid shared references)
		dst[k] = copyValue(srcVal)
	}
}
