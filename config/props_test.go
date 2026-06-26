package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreSetGet(t *testing.T) {
	s := NewProps()

	// Simple key
	s.Set("name", "test")
	if v := s.Get("name"); v != "test" {
		t.Fatalf("expected 'test', got %v", v)
	}

	// Dot-separated key creates intermediate maps
	s.Set("server.port", 8080)
	if v := s.Get("server.port"); v != 8080 {
		t.Fatalf("expected 8080, got %v", v)
	}

	// Intermediate map exists
	server := s.Get("server")
	serverMap, ok := server.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map for 'server', got %T", server)
	}
	if serverMap["port"] != 8080 {
		t.Fatalf("expected 8080 in server map, got %v", serverMap["port"])
	}

	// Missing key
	if v := s.Get("nonexistent"); v != nil {
		t.Fatalf("expected nil for nonexistent key, got %v", v)
	}

	// Missing intermediate path
	if v := s.Get("server.host.name"); v != nil {
		t.Fatalf("expected nil, got %v", v)
	}

	// Empty key
	if v := s.Get(""); v != nil {
		t.Fatalf("expected nil for empty key, got %v", v)
	}
}

func TestStoreSetOverwrite(t *testing.T) {
	s := NewProps()

	s.Set("a.b.c", 1)
	s.Set("a.b.c", 2)
	if v := s.GetInt("a.b.c"); v != 2 {
		t.Fatalf("expected 2 after overwrite, got %d", v)
	}

	// Overwrite a map node with a scalar
	s.Set("a.b", "scalar")
	if v := s.GetStr("a.b"); v != "scalar" {
		t.Fatalf("expected 'scalar', got %s", v)
	}
	// Old nested path should be gone
	if v := s.Get("a.b.c"); v != nil {
		t.Fatalf("expected nil after overwrite, got %v", v)
	}
}

func TestStoreGetStr(t *testing.T) {
	s := NewProps()

	// Existing string
	s.Set("name", "hello")
	if v := s.GetStr("name"); v != "hello" {
		t.Fatalf("expected 'hello', got '%s'", v)
	}

	// Missing key
	if v := s.GetStr("missing"); v != "" {
		t.Fatalf("expected empty string, got '%s'", v)
	}

	// Non-string value
	s.Set("port", 8080)
	if v := s.GetStr("port"); v != "" {
		t.Fatalf("expected empty string for non-string, got '%s'", v)
	}
}

func TestStoreGetBool(t *testing.T) {
	s := NewProps()

	s.Set("enabled", true)
	if v := s.GetBool("enabled"); !v {
		t.Fatal("expected true")
	}

	s.Set("disabled", false)
	if v := s.GetBool("disabled"); v {
		t.Fatal("expected false")
	}

	// Missing key
	if v := s.GetBool("missing"); v {
		t.Fatal("expected false for missing key")
	}

	// Non-bool value
	s.Set("name", "true")
	if v := s.GetBool("name"); v {
		t.Fatal("expected false for non-bool")
	}
}

func TestStoreGetInt(t *testing.T) {
	s := NewProps()

	// int
	s.Set("a", 42)
	if v := s.GetInt("a"); v != 42 {
		t.Fatalf("expected 42, got %d", v)
	}

	// int64
	s.Set("b", int64(100))
	if v := s.GetInt("b"); v != 100 {
		t.Fatalf("expected 100, got %d", v)
	}

	// float64 (from YAML parsing)
	s.Set("c", float64(3.14))
	if v := s.GetInt("c"); v != 3 {
		t.Fatalf("expected 3, got %d", v)
	}

	// Missing key
	if v := s.GetInt("missing"); v != 0 {
		t.Fatalf("expected 0, got %d", v)
	}

	// Non-numeric value
	s.Set("name", "hello")
	if v := s.GetInt("name"); v != 0 {
		t.Fatalf("expected 0 for string, got %d", v)
	}
}

func TestStoreGetInt64(t *testing.T) {
	s := NewProps()

	s.Set("a", int64(9999999999))
	if v := s.GetInt64("a"); v != 9999999999 {
		t.Fatalf("expected 9999999999, got %d", v)
	}

	s.Set("b", 42)
	if v := s.GetInt64("b"); v != 42 {
		t.Fatalf("expected 42, got %d", v)
	}

	s.Set("c", float64(3.14))
	if v := s.GetInt64("c"); v != 3 {
		t.Fatalf("expected 3, got %d", v)
	}

	if v := s.GetInt64("missing"); v != 0 {
		t.Fatalf("expected 0, got %d", v)
	}
}

func TestStoreGetFloat64(t *testing.T) {
	s := NewProps()

	s.Set("a", float64(3.14))
	if v := s.GetFloat64("a"); v != 3.14 {
		t.Fatalf("expected 3.14, got %f", v)
	}

	s.Set("b", 42)
	if v := s.GetFloat64("b"); v != 42.0 {
		t.Fatalf("expected 42.0, got %f", v)
	}

	s.Set("c", int64(100))
	if v := s.GetFloat64("c"); v != 100.0 {
		t.Fatalf("expected 100.0, got %f", v)
	}

	if v := s.GetFloat64("missing"); v != 0 {
		t.Fatalf("expected 0, got %f", v)
	}
}

func TestStoreHas(t *testing.T) {
	s := NewProps()

	s.Set("name", "test")
	if !s.Has("name") {
		t.Fatal("expected Has('name') to be true")
	}

	if s.Has("missing") {
		t.Fatal("expected Has('missing') to be false")
	}

	s.Set("nested.key", "value")
	if !s.Has("nested.key") {
		t.Fatal("expected Has('nested.key') to be true")
	}
	if s.Has("nested.missing") {
		t.Fatal("expected Has('nested.missing') to be false")
	}
}

func TestStoreKeys(t *testing.T) {
	s := NewProps()

	keys := s.Keys()
	if len(keys) != 0 {
		t.Fatalf("expected 0 keys, got %d", len(keys))
	}

	s.Set("a", 1)
	s.Set("b", 2)
	s.Set("c.d", 3)

	keys = s.Keys()
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(keys))
	}

	keySet := make(map[string]bool)
	for _, k := range keys {
		keySet[k] = true
	}
	for _, expected := range []string{"a", "b", "c"} {
		if !keySet[expected] {
			t.Fatalf("expected key '%s' not found in %v", expected, keys)
		}
	}
}

func TestStoreSub(t *testing.T) {
	s := NewProps()
	s.Set("db.driver", "mysql")
	s.Set("db.port", 3306)
	s.Set("db.pool.max", 10)
	s.Set("server.port", 8080)

	// Sub for existing prefix
	dbProps := s.Sub("db")
	if v := dbProps.GetStr("driver"); v != "mysql" {
		t.Fatalf("expected 'mysql', got '%s'", v)
	}
	if v := dbProps.GetInt("port"); v != 3306 {
		t.Fatalf("expected 3306, got %d", v)
	}
	if v := dbProps.GetInt("pool.max"); v != 10 {
		t.Fatalf("expected 10, got %d", v)
	}

	// Sub for nested prefix returns empty store
	poolStore := dbProps.Sub("pool")
	if v := poolStore.GetInt("max"); v != 10 {
		t.Fatalf("expected 10, got %d", v)
	}

	// Sub for missing prefix returns empty store
	emptyStore := s.Sub("nonexistent")
	if len(emptyStore.Keys()) != 0 {
		t.Fatalf("expected 0 keys, got %d", len(emptyStore.Keys()))
	}

	// Sub for non-map value returns empty store
	s.Set("scalar", "value")
	emptyStore2 := s.Sub("scalar")
	if len(emptyStore2.Keys()) != 0 {
		t.Fatalf("expected 0 keys for scalar sub, got %d", len(emptyStore2.Keys()))
	}

	// sub-props independence: mutate sub, parent unchanged
	dbProps.Set("driver", "postgres")
	if v := s.GetStr("db.driver"); v != "mysql" {
		t.Fatalf("parent should still be 'mysql', got '%s'", v)
	}
}

func TestStoreSubAndBind(t *testing.T) {
	s := NewProps()
	s.Set("db.driver", "sqlite")
	s.Set("db.dsn", "test.db")
	s.Set("server.port", 8080)

	type DBConf struct {
		Driver string `yaml:"driver"`
		DSN    string `yaml:"dsn"`
	}

	dbProps := s.Sub("db")
	var db DBConf
	if err := dbProps.Bind(&db); err != nil {
		t.Fatalf("Bind failed: %v", err)
	}
	if db.Driver != "sqlite" {
		t.Fatalf("expected 'sqlite', got '%s'", db.Driver)
	}
	if db.DSN != "test.db" {
		t.Fatalf("expected 'test.db', got '%s'", db.DSN)
	}
}

func TestStoreSubBind(t *testing.T) {
	s := NewProps()
	s.Set("db.driver", "sqlite")
	s.Set("db.dsn", "test.db")
	s.Set("server.port", 8080)

	type DBConf struct {
		Driver string `yaml:"driver"`
		DSN    string `yaml:"dsn"`
	}

	var db DBConf
	if err := s.SubBind("db", &db); err != nil {
		t.Fatalf("SubBind failed: %v", err)
	}
	if db.Driver != "sqlite" {
		t.Fatalf("expected 'sqlite', got '%s'", db.Driver)
	}
	if db.DSN != "test.db" {
		t.Fatalf("expected 'test.db', got '%s'", db.DSN)
	}
}

func TestStoreSubBindMissingPrefix(t *testing.T) {
	s := NewProps()

	type DBConf struct {
		Driver string `yaml:"driver"`
	}
	var db DBConf
	db.Driver = "default"

	// Binding a missing prefix should leave v unchanged
	if err := s.SubBind("nonexistent", &db); err != nil {
		t.Fatalf("SubBind should not error on missing prefix: %v", err)
	}
	if db.Driver != "default" {
		t.Fatalf("expected 'default' unchanged, got '%s'", db.Driver)
	}
}

func TestStoreSubBindNonMap(t *testing.T) {
	s := NewProps()
	s.Set("scalar", "just a string")

	type DBConf struct {
		Driver string `yaml:"driver"`
	}
	var db DBConf
	if err := s.SubBind("scalar", &db); err != nil {
		t.Fatalf("SubBind should not error on non-map value: %v", err)
	}
	// v should be unchanged
	if db.Driver != "" {
		t.Fatalf("expected zero value, got '%s'", db.Driver)
	}
}

func TestDeepMerge(t *testing.T) {
	dst := map[string]interface{}{
		"a": 1,
		"b": map[string]interface{}{
			"x": "old",
			"y": 2,
		},
		"c": "keep",
	}

	src := map[string]interface{}{
		"a": 999, // overwrite scalar
		"b": map[string]interface{}{
			"x": "new", // overwrite nested scalar
			"z": 3,     // add new nested key
		},
		"d": "added", // add new top-level key
	}

	deepMerge(dst, src)

	if v := dst["a"]; v != 999 {
		t.Fatalf("expected a=999, got %v", v)
	}
	if v := dst["c"]; v != "keep" {
		t.Fatalf("expected c='keep', got %v", v)
	}
	if v := dst["d"]; v != "added" {
		t.Fatalf("expected d='added', got %v", v)
	}

	b := dst["b"].(map[string]interface{})
	if v := b["x"]; v != "new" {
		t.Fatalf("expected b.x='new', got %v", v)
	}
	if v := b["y"]; v != 2 {
		t.Fatalf("expected b.y=2, got %v", v)
	}
	if v := b["z"]; v != 3 {
		t.Fatalf("expected b.z=3, got %v", v)
	}
}

func TestStoreLoadYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yml")
	content := `
server:
  port: 9090
  name: test-server
db:
  driver: mysql
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	s := NewProps()
	if err := s.LoadYAML(path); err != nil {
		t.Fatalf("LoadYAML failed: %v", err)
	}

	if v := s.GetInt("server.port"); v != 9090 {
		t.Fatalf("expected 9090, got %d", v)
	}
	if v := s.GetStr("server.name"); v != "test-server" {
		t.Fatalf("expected 'test-server', got '%s'", v)
	}
	if v := s.GetStr("db.driver"); v != "mysql" {
		t.Fatalf("expected 'mysql', got '%s'", v)
	}
}

func TestStoreLoadYAMLNotExist(t *testing.T) {
	s := NewProps()
	err := s.LoadYAML("/nonexistent/path/config.yml")
	if err != nil {
		t.Fatalf("LoadYAML should return nil for missing file, got: %v", err)
	}
}

func TestStoreLoadYAMLInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yml")
	if err := os.WriteFile(path, []byte("invalid: [yaml: {{"), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	s := NewProps()
	err := s.LoadYAML(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestStoreLoadYAMLBytes(t *testing.T) {
	s := NewProps()
	data := []byte(`
app:
  name: myapp
  version: "1.0"
`)
	if err := s.LoadYAMLBytes(data); err != nil {
		t.Fatalf("LoadYAMLBytes failed: %v", err)
	}

	if v := s.GetStr("app.name"); v != "myapp" {
		t.Fatalf("expected 'myapp', got '%s'", v)
	}
	if v := s.GetStr("app.version"); v != "1.0" {
		t.Fatalf("expected '1.0', got '%s'", v)
	}
}

func TestStoreLoadYAMLBytesEmpty(t *testing.T) {
	s := NewProps()
	if err := s.LoadYAMLBytes([]byte("")); err != nil {
		t.Fatalf("LoadYAMLBytes with empty content should not error: %v", err)
	}
	if len(s.Keys()) != 0 {
		t.Fatalf("expected 0 keys for empty YAML, got %d", len(s.Keys()))
	}
}

func TestStoreLoadYAMLPattern(t *testing.T) {
	dir := t.TempDir()

	writeYAML := func(name, content string) {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	writeYAML("common.yml", "common: loaded")
	writeYAML("extra.yml", "extra: loaded")
	writeYAML("ignore.txt", "not yaml")

	s := NewProps()
	pattern := filepath.Join(dir, "*.yml")
	if err := s.LoadYAMLPattern(pattern); err != nil {
		t.Fatalf("LoadYAMLPattern failed: %v", err)
	}

	if v := s.GetStr("common"); v != "loaded" {
		t.Fatalf("expected 'loaded', got '%s'", v)
	}
	if v := s.GetStr("extra"); v != "loaded" {
		t.Fatalf("expected 'loaded', got '%s'", v)
	}
	// txt file should be ignored
	if s.Has("ignore") {
		t.Fatal("expected ignore.txt to not be loaded")
	}
}

func TestStoreMergeYAML(t *testing.T) {
	s := NewProps()
	s.Set("server.port", 8080)
	s.Set("server.host", "localhost")

	// Merge overrides server.port and adds db section
	content := []byte(`
server:
  port: 9090
db:
  driver: postgres
`)
	if err := s.MergeYAML(content); err != nil {
		t.Fatalf("MergeYAML failed: %v", err)
	}

	if v := s.GetInt("server.port"); v != 9090 {
		t.Fatalf("expected 9090, got %d", v)
	}
	if v := s.GetStr("server.host"); v != "localhost" {
		t.Fatalf("expected 'localhost', got '%s'", v)
	}
	if v := s.GetStr("db.driver"); v != "postgres" {
		t.Fatalf("expected 'postgres', got '%s'", v)
	}
}

func TestStoreLoadEnv(t *testing.T) {
	// Set test env vars
	os.Setenv("TESTCFG_SERVER_PORT", "9090")
	os.Setenv("TESTCFG_SERVER__HOST", "myhost")
	os.Setenv("TESTCFG_DB__DRIVER", "mysql")
	os.Setenv("TESTCFG_DEBUG", "true")
	os.Setenv("OTHER_VAR", "ignored")
	defer func() {
		os.Unsetenv("TESTCFG_SERVER_PORT")
		os.Unsetenv("TESTCFG_SERVER__HOST")
		os.Unsetenv("TESTCFG_DB__DRIVER")
		os.Unsetenv("TESTCFG_DEBUG")
		os.Unsetenv("OTHER_VAR")
	}()

	s := NewProps()
	s.LoadEnv("TESTCFG")

	// Single underscore -> dot
	if v := s.GetStr("server.port"); v != "9090" {
		t.Fatalf("expected '9090', got '%s'", v)
	}

	// Double underscore -> single dot (nesting boundary)
	if v := s.GetStr("server.host"); v != "myhost" {
		t.Fatalf("expected 'myhost', got '%s'", v)
	}

	// db.driver via double underscore
	if v := s.GetStr("db.driver"); v != "mysql" {
		t.Fatalf("expected 'mysql', got '%s'", v)
	}

	// Simple key
	if v := s.GetStr("debug"); v != "true" {
		t.Fatalf("expected 'true', got '%s'", v)
	}

	// Non-matching prefix should be ignored
	if s.Has("other.var") {
		t.Fatal("OTHER_VAR should not be loaded")
	}
}

func TestStoreLoadEnvCaseInsensitive(t *testing.T) {
	os.Setenv("TESTCFG_CASE_CHECK", "found")
	defer os.Unsetenv("TESTCFG_CASE_CHECK")

	s := NewProps()
	s.LoadEnv("testcfg") // lowercase prefix
	if v := s.GetStr("case.check"); v != "found" {
		t.Fatalf("expected 'found', got '%s'", v)
	}
}

func TestStoreLoadArgs(t *testing.T) {
	s := NewProps()
	args := []string{
		"--server.port=9090",
		"-db.driver=postgres",
		"--debug=true",
		"positional-arg",
		"---weird",
	}

	s.LoadArgs(args)

	if v := s.GetStr("server.port"); v != "9090" {
		t.Fatalf("expected '9090', got '%s'", v)
	}
	if v := s.GetStr("db.driver"); v != "postgres" {
		t.Fatalf("expected 'postgres', got '%s'", v)
	}
	if v := s.GetStr("debug"); v != "true" {
		t.Fatalf("expected 'true', got '%s'", v)
	}

	// Positional args should be ignored
	if s.Has("positional-arg") {
		t.Fatal("positional arg should be ignored")
	}
}

func TestStoreLoadArgsEmpty(t *testing.T) {
	s := NewProps()
	s.LoadArgs([]string{})
	if len(s.Keys()) != 0 {
		t.Fatalf("expected 0 keys, got %d", len(s.Keys()))
	}
}

func TestStoreBind(t *testing.T) {
	s := NewProps()
	s.Set("server.port", 8080)
	s.Set("server.name", "myapp")
	s.Set("db.driver", "sqlite")
	s.Set("db.dsn", "test.db")
	s.Set("debug", true)
	s.Set("tags", []string{"dev", "test"})

	type ServerConf struct {
		Port int    `yaml:"port"`
		Name string `yaml:"name"`
	}
	type DBConf struct {
		Driver string `yaml:"driver"`
		DSN    string `yaml:"dsn"`
	}
	type AppConf struct {
		Server ServerConf `yaml:"server"`
		DB     DBConf     `yaml:"db"`
		Debug  bool       `yaml:"debug"`
		Tags   []string   `yaml:"tags"`
	}

	var cfg AppConf
	if err := s.Bind(&cfg); err != nil {
		t.Fatalf("Bind failed: %v", err)
	}

	if cfg.Server.Port != 8080 {
		t.Fatalf("expected 8080, got %d", cfg.Server.Port)
	}
	if cfg.Server.Name != "myapp" {
		t.Fatalf("expected 'myapp', got '%s'", cfg.Server.Name)
	}
	if cfg.DB.Driver != "sqlite" {
		t.Fatalf("expected 'sqlite', got '%s'", cfg.DB.Driver)
	}
	if cfg.DB.DSN != "test.db" {
		t.Fatalf("expected 'test.db', got '%s'", cfg.DB.DSN)
	}
	if !cfg.Debug {
		t.Fatal("expected debug=true")
	}
	if len(cfg.Tags) != 2 || cfg.Tags[0] != "dev" || cfg.Tags[1] != "test" {
		t.Fatalf("expected [dev test], got %v", cfg.Tags)
	}
}

func TestStoreData(t *testing.T) {
	s := NewProps()
	s.Set("a", 1)
	s.Set("b.c", 2)

	data := s.Data()
	if v := data["a"]; v != 1 {
		t.Fatalf("expected 1, got %v", v)
	}

	// Mutating returned map should not affect store
	data["a"] = 999
	if v := s.GetInt("a"); v != 1 {
		t.Fatalf("expected 1 after mutating Data() result, got %d", v)
	}

	// Nested mutation via Data() affects the props because Data()
	// returns a shallow copy — nested maps are shared references.
	bMap, ok := data["b"].(map[string]interface{})
	if !ok {
		t.Fatal("expected map for 'b'")
	}
	bMap["c"] = 888
	if v := s.GetInt("b.c"); v != 888 {
		t.Fatalf("expected 888 after mutating nested Data() result (shallow copy), got %d", v)
	}
}

func TestStoreSetEmptyKey(t *testing.T) {
	s := NewProps()
	s.Set("", "value")
	if len(s.Keys()) != 0 {
		t.Fatalf("expected 0 keys for empty key Set, got %d", len(s.Keys()))
	}
}

func TestNewProps(t *testing.T) {
	p := NewProps()
	if p == nil {
		t.Fatal("NewProps returned nil")
	}
	if p.data == nil {
		t.Fatal("props.data is nil")
	}
	if len(p.Keys()) != 0 {
		t.Fatalf("new props should have 0 keys, got %d", len(p.Keys()))
	}
}

func TestStoreGetWithDefault(t *testing.T) {
	s := NewProps()
	s.Set("name", "hello")
	if v := s.Get("name", "world"); v != "hello" {
		t.Fatalf("expected 'hello', got %v", v)
	}
	if v := s.Get("missing", "default"); v != "default" {
		t.Fatalf("expected 'default', got %v", v)
	}
}

func TestStoreGetStrWithDefault(t *testing.T) {
	s := NewProps()
	s.Set("host", "example.com")
	if v := s.GetStr("host", "localhost"); v != "example.com" {
		t.Fatalf("expected 'example.com', got '%s'", v)
	}
	// empty string triggers default
	s.Set("empty", "")
	if v := s.GetStr("empty", "fallback"); v != "fallback" {
		t.Fatalf("expected 'fallback' for empty, got '%s'", v)
	}
	// missing key triggers default
	if v := s.GetStr("missing", "default"); v != "default" {
		t.Fatalf("expected 'default', got '%s'", v)
	}
	// no default, existing behavior
	if v := s.GetStr("host"); v != "example.com" {
		t.Fatalf("expected 'example.com', got '%s'", v)
	}
}

func TestStoreGetBoolWithDefault(t *testing.T) {
	s := NewProps()
	s.Set("enabled", true)
	if v := s.GetBool("enabled", false); !v {
		t.Fatal("expected true")
	}
	if v := s.GetBool("missing", true); !v {
		t.Fatal("expected default true")
	}
	// non-bool triggers default
	s.Set("name", "hello")
	if v := s.GetBool("name", true); !v {
		t.Fatal("expected default true for non-bool")
	}
	// no default, existing behavior
	if v := s.GetBool("missing"); v {
		t.Fatal("expected false without default")
	}
}

func TestStoreGetIntWithDefault(t *testing.T) {
	s := NewProps()
	s.Set("port", 8080)
	if v := s.GetInt("port", 3000); v != 8080 {
		t.Fatalf("expected 8080, got %d", v)
	}
	if v := s.GetInt("missing", 3000); v != 3000 {
		t.Fatalf("expected 3000, got %d", v)
	}
	// non-numeric triggers default
	s.Set("name", "hello")
	if v := s.GetInt("name", 9999); v != 9999 {
		t.Fatalf("expected 9999, got %d", v)
	}
}

func TestStoreGetInt64WithDefault(t *testing.T) {
	s := NewProps()
	s.Set("max", int64(9999999999))
	if v := s.GetInt64("max", 0); v != 9999999999 {
		t.Fatalf("expected 9999999999, got %d", v)
	}
	if v := s.GetInt64("missing", int64(100)); v != 100 {
		t.Fatalf("expected 100, got %d", v)
	}
}

func TestStoreGetFloat64WithDefault(t *testing.T) {
	s := NewProps()
	s.Set("rate", 0.75)
	if v := s.GetFloat64("rate", 0.5); v != 0.75 {
		t.Fatalf("expected 0.75, got %f", v)
	}
	if v := s.GetFloat64("missing", 3.14); v != 3.14 {
		t.Fatalf("expected 3.14, got %f", v)
	}
}

// =============================================================================
// Global Props tests
// =============================================================================

func TestGlobalGetSet(t *testing.T) {
	// Save and restore global
	old := globalProps
	defer setProps(old)

	p := NewProps()
	p.Set("name", "test")
	p.Set("server.port", 8080)
	setProps(p)

	// Get
	if v := Get("name"); v != "test" {
		t.Fatalf("expected 'test', got %v", v)
	}

	// GetStr
	if v := GetStr("name"); v != "test" {
		t.Fatalf("expected 'test', got '%s'", v)
	}

	// GetInt
	if v := GetInt("server.port"); v != 8080 {
		t.Fatalf("expected 8080, got %d", v)
	}

	// GetInt64
	if v := GetInt64("server.port"); v != 8080 {
		t.Fatalf("expected 8080, got %d", v)
	}

	// GetFloat64
	if v := GetFloat64("server.port"); v != 8080.0 {
		t.Fatalf("expected 8080.0, got %f", v)
	}

	// GetBool
	p.Set("enabled", true)
	if v := GetBool("enabled"); !v {
		t.Fatal("expected true")
	}

	// Has
	if !Has("name") {
		t.Fatal("expected Has('name') to be true")
	}
	if Has("missing") {
		t.Fatal("expected Has('missing') to be false")
	}

	// Keys
	keys := Keys()
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(keys))
	}

	// Set via global
	Set("newkey", "newvalue")
	if v := p.GetStr("newkey"); v != "newvalue" {
		t.Fatalf("expected 'newvalue', got '%s'", v)
	}
}

func TestGlobalNilProps(t *testing.T) {
	// Save and restore global
	old := globalProps
	defer setProps(old)

	globalProps = nil

	// All getters should return zero values / defaults
	if v := Get("key"); v != nil {
		t.Fatalf("expected nil, got %v", v)
	}
	if v := Get("key", "default"); v != "default" {
		t.Fatalf("expected 'default', got %v", v)
	}
	if v := GetStr("key"); v != "" {
		t.Fatalf("expected empty string, got '%s'", v)
	}
	if v := GetStr("key", "fallback"); v != "fallback" {
		t.Fatalf("expected 'fallback', got '%s'", v)
	}
	if v := GetBool("key"); v {
		t.Fatal("expected false")
	}
	if v := GetBool("key", true); !v {
		t.Fatal("expected default true")
	}
	if v := GetInt("key"); v != 0 {
		t.Fatalf("expected 0, got %d", v)
	}
	if v := GetInt("key", 42); v != 42 {
		t.Fatalf("expected 42, got %d", v)
	}
	if v := GetInt64("key"); v != 0 {
		t.Fatalf("expected 0, got %d", v)
	}
	if v := GetFloat64("key"); v != 0 {
		t.Fatalf("expected 0, got %f", v)
	}
	if Has("key") {
		t.Fatal("expected false")
	}
	if keys := Keys(); keys != nil {
		t.Fatalf("expected nil keys, got %v", keys)
	}

	// Set should be a no-op (doesn't panic)
	Set("key", "value")

	// Sub returns empty Props
	sub := Sub("prefix")
	if sub == nil {
		t.Fatal("Sub should return non-nil empty Props")
	}
	if len(sub.Keys()) != 0 {
		t.Fatalf("expected 0 keys, got %d", len(sub.Keys()))
	}

	// SubBind returns nil
	type DBConf struct {
		Driver string `yaml:"driver"`
	}
	var db DBConf
	if err := SubBind("db", &db); err != nil {
		t.Fatalf("SubBind should not error on nil global: %v", err)
	}

	// Bind returns nil
	var cfg map[string]interface{}
	if err := Bind(&cfg); err != nil {
		t.Fatalf("Bind should not error on nil global: %v", err)
	}
}

func TestGlobalSubAndSubBind(t *testing.T) {
	old := globalProps
	defer setProps(old)

	p := NewProps()
	p.Set("db.driver", "mysql")
	p.Set("db.port", 3306)
	setProps(p)

	// Sub
	dbProps := Sub("db")
	if v := dbProps.GetStr("driver"); v != "mysql" {
		t.Fatalf("expected 'mysql', got '%s'", v)
	}
	if v := dbProps.GetInt("port"); v != 3306 {
		t.Fatalf("expected 3306, got %d", v)
	}

	// SubBind
	type DBConf struct {
		Driver string `yaml:"driver"`
		Port   int    `yaml:"port"`
	}
	var db DBConf
	if err := SubBind("db", &db); err != nil {
		t.Fatalf("SubBind failed: %v", err)
	}
	if db.Driver != "mysql" {
		t.Fatalf("expected 'mysql', got '%s'", db.Driver)
	}
	if db.Port != 3306 {
		t.Fatalf("expected 3306, got %d", db.Port)
	}
}

func TestGlobalBind(t *testing.T) {
	old := globalProps
	defer setProps(old)

	p := NewProps()
	p.Set("server.port", 9090)
	p.Set("server.name", "myapp")
	setProps(p)

	type ServerConf struct {
		Port int    `yaml:"port"`
		Name string `yaml:"name"`
	}
	type AppConf struct {
		Server ServerConf `yaml:"server"`
	}
	var cfg AppConf
	if err := Bind(&cfg); err != nil {
		t.Fatalf("Bind failed: %v", err)
	}
	if cfg.Server.Port != 9090 {
		t.Fatalf("expected 9090, got %d", cfg.Server.Port)
	}
	if cfg.Server.Name != "myapp" {
		t.Fatalf("expected 'myapp', got '%s'", cfg.Server.Name)
	}
}

func TestGlobalGetDefaults(t *testing.T) {
	old := globalProps
	defer setProps(old)

	p := NewProps()
	setProps(p)

	// Get with default
	if v := Get("missing", "default"); v != "default" {
		t.Fatalf("expected 'default', got %v", v)
	}

	// GetStr with default
	if v := GetStr("missing", "fallback"); v != "fallback" {
		t.Fatalf("expected 'fallback', got '%s'", v)
	}
	// empty string triggers default
	p.Set("empty", "")
	if v := GetStr("empty", "nonempty"); v != "nonempty" {
		t.Fatalf("expected 'nonempty', got '%s'", v)
	}

	// GetBool with default
	if v := GetBool("missing", true); !v {
		t.Fatal("expected default true")
	}
	// non-bool triggers default
	p.Set("name", "hello")
	if v := GetBool("name", true); !v {
		t.Fatal("expected default true for non-bool")
	}

	// GetInt with default
	if v := GetInt("missing", 3000); v != 3000 {
		t.Fatalf("expected 3000, got %d", v)
	}

	// GetInt64 with default
	if v := GetInt64("missing", 100); v != 100 {
		t.Fatalf("expected 100, got %d", v)
	}

	// GetFloat64 with default
	if v := GetFloat64("missing", 3.14); v != 3.14 {
		t.Fatalf("expected 3.14, got %f", v)
	}
}

func TestSetPropsFunc(t *testing.T) {
	old := globalProps
	defer setProps(old)

	p := NewProps()
	p.Set("key", "value")
	setProps(p)

	if v := GetStr("key"); v != "value" {
		t.Fatalf("expected 'value', got '%s'", v)
	}

	// setProps with nil
	setProps(nil)
	if globalProps != nil {
		t.Fatal("expected Props to be nil after setProps(nil)")
	}
}

// =============================================================================
// Key normalization tests
// =============================================================================

func TestNormalizeSegment(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// Simple
		{"port", "port"},
		{"server", "server"},
		{"", ""},

		// kebab-case
		{"max-connections", "maxConnections"},
		{"max-Connections", "maxConnections"},
		{"MAX-CONNECTIONS", "maxConnections"},

		// snake_case
		{"max_connections", "maxConnections"},
		{"MAX_CONNECTIONS", "maxConnections"},
		{"Max_Connections", "maxConnections"},

		// CamelCase
		{"maxConnections", "maxConnections"},
		{"MaxConnections", "maxConnections"},
		{"MAXConnections", "maxConnections"},

		// Mixed
		{"max-Connections_Host", "maxConnectionsHost"},
		{"DB_max-connections", "dbMaxConnections"},

		// Acronym handling
		{"XMLParser", "xmlParser"},
		{"parseXML", "parseXml"},
		{"userID", "userId"},
		{"getHTTPResponse", "getHttpResponse"},
	}

	for _, tt := range tests {
		result := normalizeSegment(tt.input)
		if result != tt.expected {
			t.Errorf("normalizeSegment(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestNormalizeKey(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// Simple dot-separated
		{"server.port", "server.port"},
		{"Server.Port", "server.port"},
		{"SERVER.PORT", "server.port"},

		// Mixed segments
		{"db.max-connections", "db.maxConnections"},
		{"DB.MAX_CONNECTIONS", "db.maxConnections"},
		{"db.max_connections", "db.maxConnections"},
		{"Db.MaxConnections", "db.maxConnections"},

		// Deep nesting
		{"spring.datasource.max-active", "spring.datasource.maxActive"},
		{"Spring.DataSource.MaxActive", "spring.dataSource.maxActive"},

		// Empty parts are preserved
		{".", "."},
		{"a..b", "a..b"},
	}

	for _, tt := range tests {
		result := normalizeKey(tt.input)
		if result != tt.expected {
			t.Errorf("normalizeKey(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestPropsNormalizedGetSet(t *testing.T) {
	p := NewProps()

	// Set with camelCase, get with snake_case
	p.Set("server.maxConnections", 100)
	if v := p.GetInt("server.max_connections"); v != 100 {
		t.Fatalf("expected 100 via snake_case, got %d", v)
	}
	if v := p.GetInt("server.max-connections"); v != 100 {
		t.Fatalf("expected 100 via kebab-case, got %d", v)
	}
	if v := p.GetInt("SERVER.MAX_CONNECTIONS"); v != 100 {
		t.Fatalf("expected 100 via UPPER_CASE, got %d", v)
	}

	// Set with kebab-case, get with CamelCase
	p.Set("server.max-connections", 200)
	if v := p.GetInt("server.MaxConnections"); v != 200 {
		t.Fatalf("expected 200 via CamelCase, got %d", v)
	}
	if v := p.GetInt("server.max_connections"); v != 200 {
		t.Fatalf("expected 200 via snake_case, got %d", v)
	}

	// Hash should work with any format
	p.Set("db.max-idle", 10)
	if !p.Has("db.max_idle") {
		t.Fatal("expected Has('db.max_idle') to be true")
	}
	if !p.Has("db.maxIdle") {
		t.Fatal("expected Has('db.maxIdle') to be true")
	}
}

func TestPropsNormalizedLoadYAML(t *testing.T) {
	p := NewProps()

	// YAML with kebab-case keys
	if err := p.LoadYAMLBytes([]byte(`
db:
  max-connections: 50
  connection-timeout: 30s
`)); err != nil {
		t.Fatalf("LoadYAMLBytes: %v", err)
	}

	// Access via any format
	if v := p.GetInt("db.max-connections"); v != 50 {
		t.Fatalf("expected 50 via kebab-case, got %d", v)
	}
	if v := p.GetInt("db.maxConnections"); v != 50 {
		t.Fatalf("expected 50 via camelCase, got %d", v)
	}
	if v := p.GetInt("db.max_connections"); v != 50 {
		t.Fatalf("expected 50 via snake_case, got %d", v)
	}
	if v := p.GetStr("db.connection-timeout"); v != "30s" {
		t.Fatalf("expected 30s, got %s", v)
	}
	if v := p.GetStr("db.connectionTimeout"); v != "30s" {
		t.Fatalf("expected 30s via camelCase, got %s", v)
	}
}

func TestPropsNormalizedSubAndSubBind(t *testing.T) {
	p := NewProps()
	p.Set("db.maxIdleConnections", 20)
	p.Set("db.connectionTimeout", "60s")

	// Sub with any key format
	db := p.Sub("db")
	if v := db.GetInt("max-idle-connections"); v != 20 {
		t.Fatalf("Sub: expected 20 via kebab, got %d", v)
	}
	if v := db.GetStr("connection_timeout"); v != "60s" {
		t.Fatalf("Sub: expected 60s via snake, got %s", v)
	}

	// SubBind with normalized YAML tags
	type DBConf struct {
		MaxIdleConnections int    `yaml:"maxIdleConnections"`
		ConnectionTimeout  string `yaml:"connectionTimeout"`
	}
	var conf DBConf
	if err := p.SubBind("db", &conf); err != nil {
		t.Fatalf("SubBind: %v", err)
	}
	if conf.MaxIdleConnections != 20 {
		t.Fatalf("SubBind: expected 20, got %d", conf.MaxIdleConnections)
	}
	if conf.ConnectionTimeout != "60s" {
		t.Fatalf("SubBind: expected 60s, got %s", conf.ConnectionTimeout)
	}
}

func TestPropsNormalizedMergeYAML(t *testing.T) {
	p := NewProps()

	// First YAML with snake_case
	p.MergeYAML([]byte(`
server:
  max_connections: 100
`))

	// Second YAML with kebab-case — should override the same key
	p.MergeYAML([]byte(`
server:
  max-connections: 200
`))

	if v := p.GetInt("server.maxConnections"); v != 200 {
		t.Fatalf("expected 200 after merge (kebab overrides snake), got %d", v)
	}
}
