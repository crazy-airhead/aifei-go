package swagger

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"regexp"
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

// readDocFunc returns the full OpenAPI document string. Matches swag.ReadDoc,
// which reads the single registered doc (default instance). Overridable for
// testing.
type readDocFunc func(optionalName ...string) (string, error)

// resolvedGroup is a group resolved to its concrete spec URL and path filter.
type resolvedGroup struct {
	name    string         // display name
	specURL string         // full path to the group's OpenAPI spec
	filter  *regexp.Regexp // nil → serve the full doc verbatim
}

// resolveGroups turns the Config into concrete groups. When no groups are
// configured, a single legacy group (named GroupName, serving the full doc at
// {basePath}/swagger.json) is returned so existing single-doc setups keep
// working unchanged. Groups with an empty or duplicate Name are skipped; if
// every configured group is invalid, the legacy single group is used.
func resolveGroups(cfg *Config) []resolvedGroup {
	bp := cfg.BasePath

	legacy := func() []resolvedGroup {
		return []resolvedGroup{{name: cfg.GroupName, specURL: bp + "/swagger.json"}}
	}

	if len(cfg.Groups) == 0 {
		return legacy()
	}

	out := make([]resolvedGroup, 0, len(cfg.Groups))
	seen := make(map[string]bool, len(cfg.Groups))
	for _, g := range cfg.Groups {
		if g.Name == "" || seen[g.Name] {
			continue
		}
		seen[g.Name] = true

		var specURL string
		if g.Path == "" {
			specURL = bp + "/swagger.json"
		} else {
			specURL = bp + "/" + g.Path + "/swagger.json"
		}

		// Filters are validated by LoadConfig; compile defensively and treat
		// any compile failure as "no filter" (full doc) rather than crashing.
		var re *regexp.Regexp
		if g.Filter != "" {
			if compiled, err := regexp.Compile(g.Filter); err == nil {
				re = compiled
			}
		}
		out = append(out, resolvedGroup{name: g.Name, specURL: specURL, filter: re})
	}

	if len(out) == 0 {
		return legacy()
	}
	return out
}

// buildHandler builds the http.Handler serving the knife4j-vue3 UI, the
// generated services.json / swagger-config group listings, and one OpenAPI
// spec per group.
//
// The full document is read once at build time; each group's spec is a
// path-filtered slice of it, precomputed and cached so a spec request is a
// single Write. Routes (basePath defaults to "/swagger"):
//
//   - /services.json                        — group config (knife4j-vue3 hardcodes this at root)
//   - {basePath}/v3/api-docs/swagger-config — springdoc-style group config (urls[])
//   - {basePath}/swagger.json               — spec for the legacy/default group (no filter)
//   - {basePath}/{path}/swagger.json        — spec for each named group (filtered)
//   - {basePath}                            — redirect to {basePath}/doc.html
//   - {basePath}/doc.html                   — knife4j entry page
//   - {basePath}/webjars/...                — static assets (JS/CSS/img)
func buildHandler(cfg *Config, readDoc readDocFunc) http.Handler {
	rootFS, err := fs.Sub(webFS, "web")
	if err != nil {
		// web is compiled into the binary; fs.Sub("web") cannot fail.
		panic(err)
	}

	groups := resolveGroups(cfg)

	// Read the full OpenAPI document once; cache a filtered slice per group so
	// spec requests are constant-time.
	type cachedSpec struct {
		bytes []byte
		err   error
	}
	specs := make([]cachedSpec, len(groups))
	fullDoc, readErr := readDoc()
	for i, g := range groups {
		if readErr != nil {
			specs[i] = cachedSpec{err: readErr}
			continue
		}
		filtered, ferr := filterDocByPath(fullDoc, g.filter)
		if ferr != nil {
			specs[i] = cachedSpec{err: ferr}
		} else {
			specs[i] = cachedSpec{bytes: []byte(filtered)}
		}
	}

	// services.json + swagger-config list every group.
	resources := make([]swaggerResource, 0, len(groups))
	urls := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		resources = append(resources, swaggerResource{
			Name:           g.name,
			URL:            g.specURL,
			SwaggerVersion: "2.0",
			Location:       g.specURL,
		})
		urls = append(urls, map[string]any{"url": g.specURL, "name": g.name})
	}
	servicesBytes, _ := json.Marshal(resources)
	swaggerConfigBytes, _ := json.Marshal(map[string]any{
		"configUrl": "",
		"urls":      urls,
	})

	fileServer := http.FileServer(http.FS(rootFS))
	uiFileServer := http.StripPrefix(cfg.BasePath, fileServer)

	mux := http.NewServeMux()

	// services.json — knife4j-vue3 requests this from the server root.
	mux.HandleFunc("/services.json", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, servicesBytes)
	})

	// springdoc swagger-config — knife4j-vue3 (springdoc mode) requests
	// {basePath}/v3/api-docs/swagger-config, whose urls[] points the UI at
	// each group's OpenAPI spec.
	mux.HandleFunc(cfg.BasePath+"/v3/api-docs/swagger-config", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, swaggerConfigBytes)
	})

	// One (cached) OpenAPI spec per group.
	for i, g := range groups {
		specURL := g.specURL
		i := i
		mux.HandleFunc(specURL, func(w http.ResponseWriter, r *http.Request) {
			cs := specs[i]
			if cs.err != nil {
				http.Error(w, "swagger doc not available: "+cs.err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write(cs.bytes)
		})
	}

	// {basePath} → redirect to the entry page.
	mux.HandleFunc(cfg.BasePath, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, cfg.BasePath+"/doc.html", http.StatusFound)
	})

	// {basePath}/ subtree: doc.html and all webjars assets.
	mux.HandleFunc(cfg.BasePath+"/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == cfg.BasePath+"/" {
			http.Redirect(w, r, cfg.BasePath+"/doc.html", http.StatusFound)
			return
		}
		uiFileServer.ServeHTTP(w, r)
	})

	return mux
}

// filterDocByPath returns docJSON with its "paths" reduced to those whose key
// matches filter, and the top-level "tags" trimmed to the ones still used.
// When filter is nil the document is returned verbatim — no parsing, exact
// bytes preserved — which is both the fast path and keeps the legacy/no-group
// spec byte-identical to the source.
func filterDocByPath(docJSON string, filter *regexp.Regexp) (string, error) {
	if filter == nil {
		return docJSON, nil
	}

	var spec map[string]any
	if err := json.Unmarshal([]byte(docJSON), &spec); err != nil {
		return "", fmt.Errorf("parse swagger doc: %w", err)
	}

	paths, _ := spec["paths"].(map[string]any)
	kept := make(map[string]any, len(paths))
	usedTags := make(map[string]bool)
	for p, op := range paths {
		if !filter.MatchString(p) {
			continue
		}
		kept[p] = op
		for _, tag := range opTags(op) {
			usedTags[tag] = true
		}
	}
	spec["paths"] = kept

	// Drop top-level tags no longer referenced by any kept operation.
	if oldTags, ok := spec["tags"].([]any); ok {
		filtered := make([]any, 0, len(oldTags))
		for _, t := range oldTags {
			if usedTags[tagName(t)] {
				filtered = append(filtered, t)
			}
		}
		spec["tags"] = filtered
	}

	b, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// opTags returns the operation tags under a path object. A path object maps
// HTTP methods to operations; each operation may carry a "tags" array.
func opTags(pathObj any) []string {
	methods, ok := pathObj.(map[string]any)
	if !ok {
		return nil
	}
	var tags []string
	for _, m := range methods {
		op, ok := m.(map[string]any)
		if !ok {
			continue
		}
		arr, ok := op["tags"].([]any)
		if !ok {
			continue
		}
		for _, t := range arr {
			if s, ok := t.(string); ok {
				tags = append(tags, s)
			}
		}
	}
	return tags
}

// tagName returns the "name" field of a tag object, or "" if absent.
func tagName(t any) string {
	m, _ := t.(map[string]any)
	s, _ := m["name"].(string)
	return s
}

func writeJSON(w http.ResponseWriter, b []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(b)
}

// matches reports whether path should be served by the knife4j handler. All
// group specs live under basePath/, so multi-group paths are admitted by the
// basePath/ prefix without special handling.
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
