package swagger

import (
	"net/http"

	"github.com/crazy-airhead/aifei-go/aifei"
	"github.com/crazy-airhead/aifei-go/log"
	"github.com/swaggo/swag"
)

var _ aifei.Plugin = (*Plugin)(nil)

// Plugin serves the knife4j-vue3 UI and OpenAPI spec. It implements
// aifei.Plugin.
//
// The plugin reads configuration from the "swagger" subtree of the global
// config at Start time. When swagger.enabled is false, Start is a no-op.
//
// Users must wire the plugin's Handler() middleware into the HTTP chain via
// server.WithHTTPHandler so the knife4j requests bypass the aifei pipeline
// (raw HTML/JSON/CSS/JS, not the {code, msg, data} envelope):
//
//	swagPlugin := swagger.NewPlugin(nil)
//	app := aifei.New(aifei.WithPlugin(swagPlugin))
//	server.Run(app, ":8080", server.WithHTTPHandler(swagPlugin.Handler()))
//
// Then open http://localhost:8080/swagger/doc.html.
type Plugin struct {
	cfg     *Config
	logger  log.Logger
	started bool
	handler http.Handler
}

// NewPlugin creates a Swagger Plugin. A nil logger falls back to log.Default().
// Configuration is read from the global config on Start.
func NewPlugin(logger log.Logger) *Plugin {
	if logger == nil {
		logger = log.Default()
	}
	return &Plugin{logger: logger}
}

// Start loads configuration and builds the knife4j-vue3 handler. When
// swagger.enabled is false, this is a no-op.
func (p *Plugin) Start() error {
	if p.cfg == nil {
		cfg, err := LoadConfig()
		if err != nil {
			return err
		}
		p.cfg = cfg
	}

	if !p.cfg.Enabled {
		p.logger.Info("swagger plugin disabled, skipping start")
		return nil
	}

	p.handler = buildHandler(p.cfg, swag.ReadDoc)
	p.started = true
	p.logger.Info("swagger plugin started at %s", p.cfg.BasePath)
	return nil
}

// Stop marks the plugin as stopped.
func (p *Plugin) Stop() error {
	p.started = false
	p.handler = nil
	p.logger.Info("swagger plugin stopped")
	return nil
}

// Handler returns an HTTP middleware that intercepts knife4j requests
// (services.json, the spec, doc.html, and webjars assets) and serves them
// directly, bypassing the aifei Input/Output pipeline. Users wire this into
// the server via:
//
//	server.Run(app, ":8080", server.WithHTTPHandler(swagPlugin.Handler()))
func (p *Plugin) Handler() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if p.handler != nil && p.matches(r.URL.Path) {
				p.handler.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
