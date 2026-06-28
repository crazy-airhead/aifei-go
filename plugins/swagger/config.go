package swagger

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/crazy-airhead/aifei-go/config"
)

// Config holds knife4j-vue3 UI and plugin settings. It is read from the
// global config under the "swagger" prefix.
//
// YAML — single group (legacy):
//
//	swagger:
//	  enabled: true
//	  basePath: /swagger
//	  groupName: AdminApi
//
// YAML — multiple groups. The full OpenAPI document is generated from a single
// swag scan; each group exposes a path-filtered slice of it (springdoc/yudao
// "GroupedOpenApi" style), so there is no per-package doc list to maintain:
//
//	swagger:
//	  enabled: true
//	  basePath: /swagger
//	  groups:
//	    - name: AdminApi      # display name in knife4j
//	      path: admin          # spec served at /swagger/admin/swagger.json
//	      filter: ^/oa/admin-api   # path-key regex; keeps only matching operations
//	    - name: AppApi
//	      path: app
//	      filter: ^/oa/app-api
type Config struct {
	// Enabled gates whether the plugin serves the knife4j UI. When false,
	// Start is a no-op so callers can keep the plugin registered and toggle
	// it from configuration.
	Enabled bool `yaml:"enabled"`

	// BasePath is the URL prefix where the knife4j UI (doc.html and its
	// webjars assets) and the OpenAPI specs are served.
	// Note: the compiled knife4j-vue3 frontend always requests services.json
	// from the server root, so /services.json is served at root regardless.
	// Default: "/swagger".
	BasePath string `yaml:"basePath"`

	// GroupName is the display name of the single default group. It is used
	// only when Groups is empty (legacy single-group mode). Default: "API Docs".
	GroupName string `yaml:"groupName"`

	// Groups lists multiple OpenAPI documents, each shown as a separate doc
	// group in the knife4j selector. When empty, a single default group
	// (named GroupName, serving the full doc) is used so existing single-doc
	// setups keep working unchanged.
	Groups []Group `yaml:"groups"`
}

// Group is one doc group served to knife4j. All groups read the same, single
// OpenAPI document (produced by one `swag init` scan); Filter reduces it to
// the operations whose @Router path matches.
type Group struct {
	// Name is the group's display name in the knife4j doc selector — the
	// services.json / swagger-config "name" field.
	Name string `yaml:"name"`

	// Path is the URL slug where the group's spec is served. The spec URL
	// becomes {basePath}/{path}/swagger.json. When empty, it falls back to
	// {basePath}/swagger.json.
	Path string `yaml:"path"`

	// Filter is a regular expression matched against the spec's path keys
	// (the @Router values, e.g. /oa/admin-api/...). Only matching operations
	// are kept in the group's spec. When empty, the full doc is served.
	Filter string `yaml:"filter"`
}

// LoadConfig reads swagger configuration from the global config under the
// "swagger" prefix. Scalar fields fall back to sensible defaults; the Groups
// slice is bound via YAML round-trip (a no-op when swagger.groups is absent).
// Group filters are validated here so a bad regex fails loudly at startup.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		Enabled:   config.GetBool("swagger.enabled", true),
		BasePath:  normalizeBasePath(config.GetStr("swagger.basePath", "/swagger")),
		GroupName: config.GetStr("swagger.groupName", "API Docs"),
	}

	// Bind the optional groups subtree ([]Group). SubBind leaves Groups nil
	// when swagger.groups is absent, so the legacy single-group path applies.
	var withGroups struct {
		Groups []Group `yaml:"groups"`
	}
	if err := config.SubBind("swagger", &withGroups); err != nil {
		return nil, err
	}
	cfg.Groups = withGroups.Groups

	// Validate filters up front: a malformed regex is a config error, not a
	// silent "serve everything" footgun.
	for _, g := range cfg.Groups {
		if g.Filter == "" {
			continue
		}
		if _, err := regexp.Compile(g.Filter); err != nil {
			return nil, fmt.Errorf("swagger group %q filter %q: %w", g.Name, g.Filter, err)
		}
	}

	return cfg, nil
}

// normalizeBasePath ensures bp starts with "/" and has no trailing slash,
// defaulting to "/swagger" when empty.
func normalizeBasePath(bp string) string {
	if bp == "" {
		bp = "/swagger"
	}
	if !strings.HasPrefix(bp, "/") {
		bp = "/" + bp
	}
	return strings.TrimRight(bp, "/")
}
