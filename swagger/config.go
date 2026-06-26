package swagger

import (
	"strings"

	"github.com/crazy-airhead/aifei-go/config"
)

// Config holds knife4j-vue3 UI and plugin settings. It is read from the
// global config under the "swagger" prefix.
//
// YAML example:
//
//	swagger:
//	  enabled: true
//	  basePath: /swagger
//	  groupName: API Docs
type Config struct {
	// Enabled gates whether the plugin serves the knife4j UI. When false,
	// Start is a no-op so callers can keep the plugin registered and toggle
	// it from configuration.
	Enabled bool `yaml:"enabled"`

	// BasePath is the URL prefix where the knife4j UI (doc.html and its
	// webjars assets) and the OpenAPI spec are served.
	// Note: the compiled knife4j-vue3 frontend always requests services.json
	// from the server root, so /services.json is served at root regardless.
	// Default: "/swagger".
	BasePath string `yaml:"basePath"`

	// GroupName is the display name of the API group shown in the knife4j
	// doc selector. Default: "API Docs".
	GroupName string `yaml:"groupName"`
}

// LoadConfig reads swagger configuration from the global config under the
// "swagger" prefix. All fields have sensible defaults when not configured,
// and BasePath is normalized to a leading slash with no trailing slash.
func LoadConfig() (*Config, error) {
	bp := config.GetStr("swagger.basePath", "/swagger")
	if bp == "" {
		bp = "/swagger"
	}
	if !strings.HasPrefix(bp, "/") {
		bp = "/" + bp
	}
	bp = strings.TrimRight(bp, "/")

	cfg := &Config{
		Enabled:   config.GetBool("swagger.enabled", true),
		BasePath:  bp,
		GroupName: config.GetStr("swagger.groupName", "API Docs"),
	}
	return cfg, nil
}
