package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/crazy-airhead/aifei-go/config"
)

// writeYAMLFile writes a YAML file in the given directory.
func writeYAMLFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestLoadBasic(t *testing.T) {
	defer config.SetProps(nil)

	dir := t.TempDir()
	writeYAMLFile(t, dir, "app.yml", `
server:
  port: 8080
  name: myapp
db:
  driver: sqlite
`)

	err := config.Load([]string{"/path/to/app"}, config.WithConfigDir(dir))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if v := config.GetStr("app.path"); v != "/path/to/app" {
		t.Fatalf("expected '/path/to/app', got '%s'", v)
	}
	if v := config.GetInt("server.port"); v != 8080 {
		t.Fatalf("expected 8080, got %d", v)
	}
	if v := config.GetStr("server.name"); v != "myapp" {
		t.Fatalf("expected 'myapp', got '%s'", v)
	}
	if v := config.GetStr("db.driver"); v != "sqlite" {
		t.Fatalf("expected 'sqlite', got '%s'", v)
	}
}

func TestLoadWithProfile(t *testing.T) {
	defer config.SetProps(nil)

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

	err := config.Load([]string{"/app"}, config.WithConfigDir(dir))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Profile overrides port
	if v := config.GetInt("server.port"); v != 9090 {
		t.Fatalf("expected 9090 from profile, got %d", v)
	}
	// Base value preserved
	if v := config.GetStr("db.driver"); v != "sqlite" {
		t.Fatalf("expected 'sqlite' from base, got '%s'", v)
	}
	// Profile adds new key
	if v := config.GetStr("db.dsn"); v != "dev.db" {
		t.Fatalf("expected 'dev.db' from profile, got '%s'", v)
	}
}

func TestLoadWithProfileFromArgs(t *testing.T) {
	defer config.SetProps(nil)

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

	err := config.Load([]string{"/app", "--env=prod"}, config.WithConfigDir(dir))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if v := config.GetInt("server.port"); v != 80 {
		t.Fatalf("expected 80 from prod profile (via args), got %d", v)
	}
}

func TestLoadWithProfileFromEnv(t *testing.T) {
	defer config.SetProps(nil)

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

	err := config.Load([]string{"/app"}, config.WithConfigDir(dir))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if v := config.GetInt("server.port"); v != 3000 {
		t.Fatalf("expected 3000 from staging profile, got %d", v)
	}
}

func TestLoadExtensions(t *testing.T) {
	defer config.SetProps(nil)

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

	err := config.Load([]string{"/app"}, config.WithConfigDir(dir))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if v := config.GetStr("redis.host"); v != "localhost" {
		t.Fatalf("expected 'localhost', got '%s'", v)
	}
	if v := config.GetInt("redis.port"); v != 6379 {
		t.Fatalf("expected 6379, got %d", v)
	}
	if v := config.GetStr("email.smtp"); v != "smtp.example.com" {
		t.Fatalf("expected 'smtp.example.com', got '%s'", v)
	}
}

func TestLoadExtensionsFromEnv(t *testing.T) {
	defer config.SetProps(nil)

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

	err := config.Load([]string{"/app"}, config.WithConfigDir(dir))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if v := config.GetStr("logging.level"); v != "debug" {
		t.Fatalf("expected 'debug', got '%s'", v)
	}
}

func TestLoadEnvVars(t *testing.T) {
	defer config.SetProps(nil)

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

	err := config.Load([]string{"/app"}, config.WithConfigDir(dir))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if v := config.GetStr("server.port"); v != "9090" {
		t.Fatalf("expected '9090' from env, got '%s'", v)
	}
	if v := config.GetStr("db.driver"); v != "postgres" {
		t.Fatalf("expected 'postgres' from env, got '%s'", v)
	}
}

func TestLoadArgs(t *testing.T) {
	defer config.SetProps(nil)

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

	err := config.Load([]string{"/app", "--server.port=7070", "--db.driver=mysql"}, config.WithConfigDir(dir))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// CLI arg should win over both YAML and env var
	if v := config.GetStr("server.port"); v != "7070" {
		t.Fatalf("expected '7070' from CLI, got '%s'", v)
	}
	if v := config.GetStr("db.driver"); v != "mysql" {
		t.Fatalf("expected 'mysql' from CLI, got '%s'", v)
	}
}

func TestWithEnvPrefix(t *testing.T) {
	defer config.SetProps(nil)

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

	err := config.Load([]string{"/app"}, config.WithConfigDir(dir), config.WithEnvPrefix("MYAPP"))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Custom prefix env var is loaded
	if v := config.GetStr("server.port"); v != "5050" {
		t.Fatalf("expected '5050' from custom prefix, got '%s'", v)
	}
}

func TestWithEnv(t *testing.T) {
	defer config.SetProps(nil)

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

	err := config.Load([]string{"/app"}, config.WithConfigDir(dir), config.WithEnv("test"))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if v := config.GetInt("server.port"); v != 7777 {
		t.Fatalf("expected 7777 from forced test env, got %d", v)
	}
}

func TestWithBaseFiles(t *testing.T) {
	defer config.SetProps(nil)

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

	err := config.Load([]string{"/app"}, config.WithConfigDir(dir), config.WithBaseFiles("config.yml"))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if v := config.GetInt("server.port"); v != 4000 {
		t.Fatalf("expected 4000 from custom base file + profile, got %d", v)
	}
}

func TestLoadFiles(t *testing.T) {
	defer config.SetProps(nil)

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

	err := config.Load([]string{"/app"}, config.WithConfigDir(dir))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// L4: Load additional files into the global
	if err := config.LoadFiles(
		filepath.Join(dir, "extra.yml"),
		filepath.Join(dir, "more.yml"),
	); err != nil {
		t.Fatalf("LoadFiles failed: %v", err)
	}

	if v := config.GetInt("server.port"); v != 8080 {
		t.Fatalf("expected 8080, got %d", v)
	}
	if v := config.GetInt("cache.ttl"); v != 3600 {
		t.Fatalf("expected 3600, got %d", v)
	}
	if v := config.GetInt("cache.maxSize"); v != 1024 {
		t.Fatalf("expected 1024, got %d", v)
	}
}

func TestLoadFilesMissingFile(t *testing.T) {
	defer config.SetProps(nil)

	config.SetProps(config.NewProps())
	err := config.LoadFiles("/nonexistent/file.yml")
	if err != nil {
		t.Fatalf("LoadFiles should not error on missing file: %v", err)
	}
}

func TestInitEmpty(t *testing.T) {
	defer config.SetProps(nil)

	dir := t.TempDir()
	// No config files at all

	err := config.Init([]string{"/app"}, config.WithConfigDir(dir))
	if err != nil {
		t.Fatalf("Init should not error with no config files: %v", err)
	}

	// Should only have app.path
	if v := config.GetStr("app.path"); v != "/app" {
		t.Fatalf("expected '/app', got '%s'", v)
	}
}

func TestYAMLExtensionHandling(t *testing.T) {
	defer config.SetProps(nil)

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

	err := config.Load([]string{"/app"}, config.WithConfigDir(dir), config.WithBaseFiles("app.yaml"))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if v := config.GetInt("server.port"); v != 9090 {
		t.Fatalf("expected 9090, got %d", v)
	}
}

func TestLoadRecheckEnvAfterL3(t *testing.T) {
	defer config.SetProps(nil)

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
	err := config.Load([]string{"/app", "--env=dynamic"}, config.WithConfigDir(dir))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if v := config.GetInt("server.port"); v != 5555 {
		t.Fatalf("expected 5555 from dynamic profile, got %d", v)
	}
}
