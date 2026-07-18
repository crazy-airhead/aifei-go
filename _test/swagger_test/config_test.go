package swagger_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/crazy-airhead/aifei-go/config"
	"github.com/crazy-airhead/aifei-go/plugins/swagger"
)

// TestLoadConfigBindsGroups verifies LoadConfig binds the swagger.groups
// subtree (including each group's filter) from YAML via config.SubBind, and
// applies the scalar defaults. It is the config-level complement to the
// buildHandler/filter tests.
func TestLoadConfigBindsGroups(t *testing.T) {
	dir := t.TempDir()
	yml := `
swagger:
  enabled: true
  basePath: /swagger
  groups:
    - name: AdminApi
      path: admin
      filter: ^/oa/admin-api
    - name: AppApi
      path: app
      filter: ^/oa/app-api
`
	path := filepath.Join(dir, "app.yml")
	if err := os.WriteFile(path, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}

	// Load this YAML into a fresh Props and make it the global for LoadConfig,
	// then restore the (initially nil) global afterwards.
	props := config.NewProps()
	if err := props.LoadYAML(path); err != nil {
		t.Fatal(err)
	}
	config.SetProps(props)
	defer config.SetProps(nil)

	cfg, err := swagger.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.Enabled || cfg.BasePath != "/swagger" {
		t.Errorf("scalars wrong: enabled=%v basePath=%q", cfg.Enabled, cfg.BasePath)
	}
	if len(cfg.Groups) != 2 {
		t.Fatalf("groups = %d, want 2: %+v", len(cfg.Groups), cfg.Groups)
	}
	want := map[string]swagger.Group{
		"AdminApi": {Name: "AdminApi", Path: "admin", Filter: "^/oa/admin-api"},
		"AppApi":   {Name: "AppApi", Path: "app", Filter: "^/oa/app-api"},
	}
	for _, g := range cfg.Groups {
		w, ok := want[g.Name]
		if !ok {
			t.Errorf("unexpected group %q", g.Name)
			continue
		}
		if g.Path != w.Path || g.Filter != w.Filter {
			t.Errorf("group %q = %+v, want %+v", g.Name, g, w)
		}
	}
}

// TestLoadConfigRejectsBadFilter verifies a malformed regex fails LoadConfig
// loudly rather than silently serving the full doc.
func TestLoadConfigRejectsBadFilter(t *testing.T) {
	dir := t.TempDir()
	yml := `
swagger:
  enabled: true
  groups:
    - name: Bad
      filter: "[unterminated"
`
	path := filepath.Join(dir, "app.yml")
	if err := os.WriteFile(path, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}

	props := config.NewProps()
	if err := props.LoadYAML(path); err != nil {
		t.Fatal(err)
	}
	config.SetProps(props)
	defer config.SetProps(nil)

	if _, err := swagger.LoadConfig(); err == nil {
		t.Fatal("LoadConfig succeeded for a malformed filter; want error")
	}
}
