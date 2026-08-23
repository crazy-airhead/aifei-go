package config_test

import (
	"os"
	"testing"

	"github.com/crazy-airhead/aifei-go/config"
)

// TestExtensionConfigEnvVariant verifies config.include extension files get
// the same -{env} variant propagation as L1 base files.
func TestExtensionConfigEnvVariant(t *testing.T) {
	defer config.SetProps(nil)

	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/extra", 0755); err != nil {
		t.Fatal(err)
	}
	writeYAMLFile(t, dir, "app.yml", `
config:
  include:
    - extra/db.yml
`)
	writeYAMLFile(t, dir, "extra/db.yml", `
db:
  pool: 10
  dsn: base.db
`)
	writeYAMLFile(t, dir, "extra/db-dev.yml", `
db:
  pool: 100
`)

	os.Setenv("AIFEI_ENV", "dev")
	defer os.Unsetenv("AIFEI_ENV")

	if err := config.Load([]string{"/app"}, config.WithConfigDir(dir)); err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if v := config.GetInt("db.pool"); v != 100 {
		t.Errorf("expected 100 from extension env variant, got %d", v)
	}
	if v := config.GetStr("db.dsn"); v != "base.db" {
		t.Errorf("expected base value preserved, got %q", v)
	}
}

// TestExtensionConfigGlobEnvVariant covers a glob include: each matched file
// gets its own env variant; files without a variant load unchanged.
func TestExtensionConfigGlobEnvVariant(t *testing.T) {
	defer config.SetProps(nil)

	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/extra", 0755); err != nil {
		t.Fatal(err)
	}
	writeYAMLFile(t, dir, "app.yml", `
config:
  include:
    - extra/*.yml
`)
	writeYAMLFile(t, dir, "extra/db.yml", "db:\n  pool: 10\n")
	writeYAMLFile(t, dir, "extra/db-dev.yml", "db:\n  pool: 100\n")
	writeYAMLFile(t, dir, "extra/cache.yml", "cache:\n  ttl: 60\n")

	os.Setenv("AIFEI_ENV", "dev")
	defer os.Unsetenv("AIFEI_ENV")

	if err := config.Load([]string{"/app"}, config.WithConfigDir(dir)); err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if v := config.GetInt("db.pool"); v != 100 {
		t.Errorf("db.pool: expected 100 from dev variant, got %d", v)
	}
	if v := config.GetInt("cache.ttl"); v != 60 {
		t.Errorf("cache.ttl: expected 60 (no variant file), got %d", v)
	}
}

// TestExtensionConfigLateEnv verifies extension variants are applied when the
// env is only resolved after L3 (e.g. --env passed as a CLI arg).
func TestExtensionConfigLateEnv(t *testing.T) {
	defer config.SetProps(nil)

	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/extra", 0755); err != nil {
		t.Fatal(err)
	}
	writeYAMLFile(t, dir, "app.yml", `
config:
  include:
    - extra/db.yml
`)
	writeYAMLFile(t, dir, "extra/db.yml", "db:\n  pool: 10\n")
	writeYAMLFile(t, dir, "extra/db-prod.yml", "db:\n  pool: 200\n")

	if err := config.Load([]string{"/app", "--env=prod"}, config.WithConfigDir(dir)); err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if v := config.GetInt("db.pool"); v != 200 {
		t.Errorf("expected 200 from prod variant (late env), got %d", v)
	}
}
