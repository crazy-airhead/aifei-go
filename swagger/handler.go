package swagger

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
)

// webFS holds the compiled knife4j-vue3 frontend (doc.html + webjars assets).
//
//go:embed web
var webFS embed.FS

// swaggerResource mirrors knife4j's group config entry (Springfox
// SwaggerResource). knife4j-vue3 first requests services.json (the array of
// these entries), then fetches the OpenAPI doc from the url/location field.
type swaggerResource struct {
	Name           string `json:"name"`
	URL            string `json:"url"`
	SwaggerVersion string `json:"swaggerVersion"`
	Location       string `json:"location"`
}

// readDocFunc returns the OpenAPI document string. Matches swag.ReadDoc
// (which reads the spec registered by the user's generated docs package);
// overridable for testing.
type readDocFunc func(optionalName ...string) (string, error)

// buildHandler builds the http.Handler serving the knife4j-vue3 UI, the
// generated services.json group config, and the OpenAPI spec.
//
// Routes (basePath defaults to "/swagger"):
//   - /services.json          — group config (knife4j-vue3 hardcodes this at root)
//   - {basePath}/swagger.json — OpenAPI spec via readDoc
//   - {basePath}              — redirect to {basePath}/doc.html
//   - {basePath}/doc.html     — knife4j entry page
//   - {basePath}/webjars/...  — static assets (JS/CSS/img)
func buildHandler(cfg *Config, readDoc readDocFunc) http.Handler {
	rootFS, err := fs.Sub(webFS, "web")
	if err != nil {
		// web is compiled into the binary; fs.Sub("web") cannot fail.
		panic(err)
	}

	bp := cfg.BasePath
	specURL := bp + "/swagger.json"

	services := []swaggerResource{{
		Name:           cfg.GroupName,
		URL:            specURL,
		SwaggerVersion: "2.0",
		Location:       specURL,
	}}
	servicesBytes, _ := json.Marshal(services)

	fileServer := http.FileServer(http.FS(rootFS))
	uiFileServer := http.StripPrefix(bp, fileServer)

	mux := http.NewServeMux()

	// services.json — knife4j-vue3 requests this from the server root.
	mux.HandleFunc("/services.json", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, servicesBytes)
	})

	// springdoc swagger-config — knife4j-vue3 (springdoc mode) requests
	// {basePath}/v3/api-docs/swagger-config, whose urls[0] points the UI at the
	// OpenAPI spec. It lives under basePath/, so matches() admits it via the
	// basePath/ prefix (no special case needed).
	swaggerConfigBytes, _ := json.Marshal(map[string]any{
		"configUrl": "",
		"urls":      []map[string]any{{"url": specURL, "name": cfg.GroupName}},
	})
	mux.HandleFunc(bp+"/v3/api-docs/swagger-config", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, swaggerConfigBytes)
	})

	// OpenAPI spec. readDoc applies the runtime SwaggerInfo overrides
	// (host, basePath, etc.) the user may have set on docs.SwaggerInfo.
	mux.HandleFunc(specURL, func(w http.ResponseWriter, r *http.Request) {
		doc, err := readDoc()
		if err != nil {
			http.Error(w, "swagger doc not registered: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(doc))
	})

	// {basePath} → redirect to the entry page.
	mux.HandleFunc(bp, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, bp+"/doc.html", http.StatusFound)
	})

	// {basePath}/ subtree: doc.html and all webjars assets.
	mux.HandleFunc(bp+"/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == bp+"/" {
			http.Redirect(w, r, bp+"/doc.html", http.StatusFound)
			return
		}
		uiFileServer.ServeHTTP(w, r)
	})

	return mux
}

func writeJSON(w http.ResponseWriter, b []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(b)
}

// matches reports whether path should be served by the knife4j handler.
func (p *Plugin) matches(path string) bool {
	bp := p.cfg.BasePath
	if path == "/services.json" {
		return true
	}
	if path == bp || strings.HasPrefix(path, bp+"/") {
		return true
	}
	return false
}
