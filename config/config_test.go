package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeYAMLFile writes a YAML file in the given directory.
func writeYAMLFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestLoadStoreBasic(t *testing.T) {
	dir := t.TempDir()
	writeYAMLFile(t, dir, "app.yml", `
server:
  port: 8080
  name: myapp
db:
  driver: sqlite
`)

	props, err := LoadStore([]string{"/path/to/app"}, WithConfigDir(dir))
	if err != nil {
		t.Fatalf("LoadStore failed: %v", err)
	}

	if v := props.GetStr("app.path"); v != "/path/to/app" {
		t.Fatalf("expected '/path/to/app', got '%s'", v)
	}
	if v := props.GetInt("server.port"); v != 8080 {
		t.Fatalf("expected 8080, got %d", v)
	}
	if v := props.GetStr("server.name"); v != "myapp" {
		t.Fatalf("expected 'myapp', got '%s'", v)
	}
	if v := props.GetStr("db.driver"); v != "sqlite" {
		t.Fatalf("expected 'sqlite', got '%s'", v)
	}
}

func TestLoadStoreWithProfile(t *testing.T) {
	dir := t.TempDir()

	writeYAMLFile(t, dir, "app.yml", `
server:
  port: 8080
db:
  driver: sqlite
`)
	writeYAMLFile(t, dir, "app-dev.yml", `
server:
  port: 9090
db:
  dsn: dev.db
`)

	os.Setenv("AIFEI_ENV", "dev")
	defer os.Unsetenv("AIFEI_ENV")

	props, err := LoadStore([]string{"/app"}, WithConfigDir(dir))
	if err != nil {
		t.Fatalf("LoadStore failed: %v", err)
	}

	// Profile overrides port
	if v := props.GetInt("server.port"); v != 9090 {
		t.Fatalf("expected 9090 from profile, got %d", v)
	}
	// Base value preserved
	if v := props.GetStr("db.driver"); v != "sqlite" {
		t.Fatalf("expected 'sqlite' from base, got '%s'", v)
	}
	// Profile adds new key
	if v := props.GetStr("db.dsn"); v != "dev.db" {
		t.Fatalf("expected 'dev.db' from profile, got '%s'", v)
	}
}

func TestLoadStoreWithProfileFromArgs(t *testing.T) {
	dir := t.TempDir()

	writeYAMLFile(t, dir, "app.yml", `
server:
  port: 8080
`)
	writeYAMLFile(t, dir, "app-prod.yml", `
server:
  port: 80
`)

	// --env in CLI args takes priority over env var
	os.Setenv("AIFEI_ENV", "dev")
	defer os.Unsetenv("AIFEI_ENV")

	props, err := LoadStore([]string{"/app", "--env=prod"}, WithConfigDir(dir))
	if err != nil {
		t.Fatalf("LoadStore failed: %v", err)
	}

	if v := props.GetInt("server.port"); v != 80 {
		t.Fatalf("expected 80 from prod profile (via args), got %d", v)
	}
}

func TestLoadStoreWithProfileFromEnv(t *testing.T) {
	dir := t.TempDir()

	writeYAMLFile(t, dir, "app.yml", `
server:
  port: 8080
`)
	writeYAMLFile(t, dir, "app-staging.yml", `
server:
  port: 3000
`)

	os.Setenv("AIFEI_PROFILE", "staging")
	defer os.Unsetenv("AIFEI_PROFILE")

	props, err := LoadStore([]string{"/app"}, WithConfigDir(dir))
	if err != nil {
		t.Fatalf("LoadStore failed: %v", err)
	}

	if v := props.GetInt("server.port"); v != 3000 {
		t.Fatalf("expected 3000 from staging profile, got %d", v)
	}
}

func TestLoadStoreExtensions(t *testing.T) {
	dir := t.TempDir()

	writeYAMLFile(t, dir, "app.yml", `
config:
  include:
    - common/*.yml
server:
  port: 8080
`)

	// Create extension directory
	extDir := filepath.Join(dir, "common")
	if err := os.MkdirAll(extDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeYAMLFile(t, dir, "common/redis.yml", `
redis:
  host: localhost
  port: 6379
`)
	writeYAMLFile(t, dir, "common/email.yml", `
email:
  smtp: smtp.example.com
`)

	props, err := LoadStore([]string{"/app"}, WithConfigDir(dir))
	if err != nil {
		t.Fatalf("LoadStore failed: %v", err)
	}

	if v := props.GetStr("redis.host"); v != "localhost" {
		t.Fatalf("expected 'localhost', got '%s'", v)
	}
	if v := props.GetInt("redis.port"); v != 6379 {
		t.Fatalf("expected 6379, got %d", v)
	}
	if v := props.GetStr("email.smtp"); v != "smtp.example.com" {
		t.Fatalf("expected 'smtp.example.com', got '%s'", v)
	}
}

func TestLoadStoreExtensionsFromEnv(t *testing.T) {
	dir := t.TempDir()

	writeYAMLFile(t, dir, "app.yml", `
server:
  port: 8080
`)

	extDir := filepath.Join(dir, "extra")
	if err := os.MkdirAll(extDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeYAMLFile(t, dir, "extra/logging.yml", `
logging:
  level: debug
`)

	os.Setenv("AIFEI_CONFIG_INCLUDE", "extra/*.yml")
	defer os.Unsetenv("AIFEI_CONFIG_INCLUDE")

	props, err := LoadStore([]string{"/app"}, WithConfigDir(dir))
	if err != nil {
		t.Fatalf("LoadStore failed: %v", err)
	}

	if v := props.GetStr("logging.level"); v != "debug" {
		t.Fatalf("expected 'debug', got '%s'", v)
	}
}

func TestLoadStoreEnvVars(t *testing.T) {
	dir := t.TempDir()

	writeYAMLFile(t, dir, "app.yml", `
server:
  port: 8080
db:
  driver: sqlite
`)

	// Env vars override YAML values
	os.Setenv("AIFEI_SERVER_PORT", "9090")
	os.Setenv("AIFEI_DB__DRIVER", "postgres")
	defer func() {
		os.Unsetenv("AIFEI_SERVER_PORT")
		os.Unsetenv("AIFEI_DB__DRIVER")
	}()

	props, err := LoadStore([]string{"/app"}, WithConfigDir(dir))
	if err != nil {
		t.Fatalf("LoadStore failed: %v", err)
	}

	if v := props.GetStr("server.port"); v != "9090" {
		t.Fatalf("expected '9090' from env, got '%s'", v)
	}
	if v := props.GetStr("db.driver"); v != "postgres" {
		t.Fatalf("expected 'postgres' from env, got '%s'", v)
	}
}

func TestLoadStoreArgs(t *testing.T) {
	dir := t.TempDir()

	writeYAMLFile(t, dir, "app.yml", `
server:
  port: 8080
db:
  driver: sqlite
`)

	// CLI args override env vars and YAML
	os.Setenv("AIFEI_SERVER_PORT", "9090") // env override
	defer os.Unsetenv("AIFEI_SERVER_PORT")

	props, err := LoadStore([]string{"/app", "--server.port=7070", "--db.driver=mysql"}, WithConfigDir(dir))
	if err != nil {
		t.Fatalf("LoadStore failed: %v", err)
	}

	// CLI arg should win over both YAML and env var
	if v := props.GetStr("server.port"); v != "7070" {
		t.Fatalf("expected '7070' from CLI, got '%s'", v)
	}
	if v := props.GetStr("db.driver"); v != "mysql" {
		t.Fatalf("expected 'mysql' from CLI, got '%s'", v)
	}
}

func TestInitFullPipeline(t *testing.T) {
	dir := t.TempDir()

	writeYAMLFile(t, dir, "app.yml", `
server:
  port: 8080
  name: myapp
`)

	// Register a cloud loader that adds a new section
	origLoaders := cloudLoaders
	cloudLoaders = nil // reset
	defer func() { cloudLoaders = origLoaders }()

	RegisterCloudLoader(func(props *Props) ([]byte, error) {
		return []byte(`
nacos:
  enabled: true
  addr: 127.0.0.1:8848
`), nil
	})

	props, err := Init([]string{"/app"}, WithConfigDir(dir))
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// L1 values
	if v := props.GetInt("server.port"); v != 8080 {
		t.Fatalf("expected 8080, got %d", v)
	}

	// L5 cloud loader values
	if v := props.GetBool("nacos.enabled"); !v {
		t.Fatal("expected nacos.enabled=true")
	}
	if v := props.GetStr("nacos.addr"); v != "127.0.0.1:8848" {
		t.Fatalf("expected '127.0.0.1:8848', got '%s'", v)
	}
}

func TestInitCloudLoaderOverrides(t *testing.T) {
	dir := t.TempDir()

	writeYAMLFile(t, dir, "app.yml", `
server:
  port: 8080
`)

	origLoaders := cloudLoaders
	cloudLoaders = nil
	defer func() { cloudLoaders = origLoaders }()

	// Cloud loader overrides L1 value
	RegisterCloudLoader(func(props *Props) ([]byte, error) {
		return []byte(`
server:
  port: 9999
`), nil
	})

	props, err := Init([]string{"/app"}, WithConfigDir(dir))
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if v := props.GetInt("server.port"); v != 9999 {
		t.Fatalf("expected 9999 from cloud loader override, got %d", v)
	}
}

func TestInitCloudLoaderEmptyContent(t *testing.T) {
	dir := t.TempDir()

	writeYAMLFile(t, dir, "app.yml", `
server:
  port: 8080
`)

	origLoaders := cloudLoaders
	cloudLoaders = nil
	defer func() { cloudLoaders = origLoaders }()

	// Cloud loader returns empty content (should be skipped)
	RegisterCloudLoader(func(props *Props) ([]byte, error) {
		return nil, nil
	})

	props, err := Init([]string{"/app"}, WithConfigDir(dir))
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if v := props.GetInt("server.port"); v != 8080 {
		t.Fatalf("expected 8080 unchanged, got %d", v)
	}
}

func TestWithEnvPrefix(t *testing.T) {
	dir := t.TempDir()

	writeYAMLFile(t, dir, "app.yml", `
server:
  port: 8080
`)

	os.Setenv("MYAPP_SERVER_PORT", "5050")
	os.Setenv("AIFEI_SERVER_PORT", "9999") // should be ignored
	defer func() {
		os.Unsetenv("MYAPP_SERVER_PORT")
		os.Unsetenv("AIFEI_SERVER_PORT")
	}()

	props, err := LoadStore([]string{"/app"}, WithConfigDir(dir), WithEnvPrefix("MYAPP"))
	if err != nil {
		t.Fatalf("LoadStore failed: %v", err)
	}

	// Custom prefix env var is loaded
	if v := props.GetStr("server.port"); v != "5050" {
		t.Fatalf("expected '5050' from custom prefix, got '%s'", v)
	}
}

func TestWithEnv(t *testing.T) {
	dir := t.TempDir()

	writeYAMLFile(t, dir, "app.yml", `
server:
  port: 8080
`)
	writeYAMLFile(t, dir, "app-test.yml", `
server:
  port: 7777
`)

	// Force env to "test", ignoring env vars
	os.Setenv("AIFEI_ENV", "dev")
	defer os.Unsetenv("AIFEI_ENV")

	props, err := LoadStore([]string{"/app"}, WithConfigDir(dir), WithEnv("test"))
	if err != nil {
		t.Fatalf("LoadStore failed: %v", err)
	}

	if v := props.GetInt("server.port"); v != 7777 {
		t.Fatalf("expected 7777 from forced test env, got %d", v)
	}
}

func TestWithBaseFiles(t *testing.T) {
	dir := t.TempDir()

	writeYAMLFile(t, dir, "config.yml", `
server:
  port: 3000
`)
	writeYAMLFile(t, dir, "config-dev.yml", `
server:
  port: 4000
`)

	os.Setenv("AIFEI_ENV", "dev")
	defer os.Unsetenv("AIFEI_ENV")

	props, err := LoadStore([]string{"/app"}, WithConfigDir(dir), WithBaseFiles("config.yml"))
	if err != nil {
		t.Fatalf("LoadStore failed: %v", err)
	}

	if v := props.GetInt("server.port"); v != 4000 {
		t.Fatalf("expected 4000 from custom base file + profile, got %d", v)
	}
}

func TestLoadInto(t *testing.T) {
	dir := t.TempDir()

	writeYAMLFile(t, dir, "app.yml", `
server:
  port: 8080
`)
	writeYAMLFile(t, dir, "extra.yml", `
cache:
  ttl: 3600
`)
	writeYAMLFile(t, dir, "more.yml", `
cache:
  maxSize: 1024
`)

	props, err := LoadStore([]string{"/app"}, WithConfigDir(dir))
	if err != nil {
		t.Fatalf("LoadStore failed: %v", err)
	}

	// L4: Load additional files
	if err := LoadInto(props,
		filepath.Join(dir, "extra.yml"),
		filepath.Join(dir, "more.yml"),
	); err != nil {
		t.Fatalf("LoadInto failed: %v", err)
	}

	if v := props.GetInt("server.port"); v != 8080 {
		t.Fatalf("expected 8080, got %d", v)
	}
	if v := props.GetInt("cache.ttl"); v != 3600 {
		t.Fatalf("expected 3600, got %d", v)
	}
	if v := props.GetInt("cache.maxSize"); v != 1024 {
		t.Fatalf("expected 1024, got %d", v)
	}
}

func TestLoadIntoMissingFile(t *testing.T) {
	props := NewProps()
	err := LoadInto(props, "/nonexistent/file.yml")
	if err != nil {
		t.Fatalf("LoadInto should not error on missing file: %v", err)
	}
}

func TestResolveEnv(t *testing.T) {
	cfg := defaultLoaderConfig()

	// No env set
	if env := resolveEnv(nil, cfg); env != "" {
		t.Fatalf("expected empty env, got '%s'", env)
	}

	// From args
	args := []string{"--env=prod", "--other=val"}
	if env := resolveEnv(args, cfg); env != "prod" {
		t.Fatalf("expected 'prod', got '%s'", env)
	}

	// From env var
	os.Setenv("AIFEI_ENV", "staging")
	defer os.Unsetenv("AIFEI_ENV")
	if env := resolveEnv(nil, cfg); env != "staging" {
		t.Fatalf("expected 'staging', got '%s'", env)
	}

	// From profile env var
	os.Unsetenv("AIFEI_ENV")
	os.Setenv("AIFEI_PROFILE", "qa")
	defer os.Unsetenv("AIFEI_PROFILE")
	if env := resolveEnv(nil, cfg); env != "qa" {
		t.Fatalf("expected 'qa', got '%s'", env)
	}
}

func TestEnvFileName(t *testing.T) {
	tests := []struct {
		base     string
		env      string
		expected string
	}{
		{"app.yml", "dev", "app-dev.yml"},
		{"app.yaml", "prod", "app-prod.yaml"},
		{"config/app.yml", "test", "config/app-test.yml"},
		{"settings", "dev", "settings-dev"},
		{"config.yml", "staging", "config-staging.yml"},
	}

	for _, tt := range tests {
		result := envFileName(tt.base, tt.env)
		if result != tt.expected {
			t.Errorf("envFileName(%q, %q) = %q, want %q", tt.base, tt.env, result, tt.expected)
		}
	}
}

func TestCollectExtensionPaths(t *testing.T) {
	// From props
	props := NewProps()
	props.Set("config.include", []interface{}{"common/*.yml", "extra/*.yml"})

	cfg := defaultLoaderConfig()
	paths := collectExtensionPaths(props, cfg)
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths from props, got %d", len(paths))
	}
	if paths[0] != "common/*.yml" || paths[1] != "extra/*.yml" {
		t.Fatalf("unexpected paths: %v", paths)
	}

	// From env var
	store2 := NewProps()
	os.Setenv("AIFEI_CONFIG_INCLUDE", "env/*.yml, other/*.yml")
	defer os.Unsetenv("AIFEI_CONFIG_INCLUDE")
	paths2 := collectExtensionPaths(store2, cfg)
	if len(paths2) != 2 {
		t.Fatalf("expected 2 paths from env, got %d", len(paths2))
	}

	// Both sources combined
	paths3 := collectExtensionPaths(props, cfg)
	if len(paths3) != 4 {
		t.Fatalf("expected 4 paths combined, got %d", len(paths3))
	}
}

func TestSplitComma(t *testing.T) {
	result := splitComma("a, b ,c, ,d,")
	if len(result) != 4 {
		t.Fatalf("expected 4 items, got %d: %v", len(result), result)
	}
	if result[0] != "a" || result[1] != "b" || result[2] != "c" || result[3] != "d" {
		t.Fatalf("unexpected result: %v", result)
	}

	// Empty string
	if len(splitComma("")) != 0 {
		t.Fatal("expected 0 items for empty string")
	}

	// Only whitespace
	if len(splitComma("  ,  ")) != 0 {
		t.Fatal("expected 0 items for whitespace-only")
	}
}

func TestInitEmpty(t *testing.T) {
	dir := t.TempDir()
	// No config files at all

	props, err := Init([]string{"/app"}, WithConfigDir(dir))
	if err != nil {
		t.Fatalf("Init should not error with no config files: %v", err)
	}

	// Should only have app.path
	if v := props.GetStr("app.path"); v != "/app" {
		t.Fatalf("expected '/app', got '%s'", v)
	}
}

func TestYAMLExtensionHandling(t *testing.T) {
	dir := t.TempDir()

	// Test with .yaml extension
	writeYAMLFile(t, dir, "app.yaml", `
server:
  port: 8080
`)
	writeYAMLFile(t, dir, "app-dev.yaml", `
server:
  port: 9090
`)

	os.Setenv("AIFEI_ENV", "dev")
	defer os.Unsetenv("AIFEI_ENV")

	props, err := LoadStore([]string{"/app"}, WithConfigDir(dir), WithBaseFiles("app.yaml"))
	if err != nil {
		t.Fatalf("LoadStore failed: %v", err)
	}

	if v := props.GetInt("server.port"); v != 9090 {
		t.Fatalf("expected 9090, got %d", v)
	}
}

func TestLoadStoreRecheckEnvAfterL3(t *testing.T) {
	dir := t.TempDir()

	writeYAMLFile(t, dir, "app.yml", `
server:
  port: 8080
`)
	writeYAMLFile(t, dir, "app-dynamic.yml", `
server:
  port: 5555
`)

	// --env=dynamic in args but env var says dev
	// The --env flag should be detected by resolveEnv before L1
	// But if it wasn't, the re-check after L3 should catch it
	props, err := LoadStore([]string{"/app", "--env=dynamic"}, WithConfigDir(dir))
	if err != nil {
		t.Fatalf("LoadStore failed: %v", err)
	}

	if v := props.GetInt("server.port"); v != 5555 {
		t.Fatalf("expected 5555 from dynamic profile, got %d", v)
	}
}
