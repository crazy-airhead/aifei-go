package aifei

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// Version is the framework version.
const Version = "1.0.0"

// Aifei is the core framework instance.
type Aifei struct {
	config      *Config
	router      *Router
	middlewares []Middleware
	plugins     []Plugin
	server      *http.Server
}

// New creates a new Aifei instance.
func New(opts ...Option) *Aifei {
	a := &Aifei{
		config: &Config{},
		router: NewRouter(),
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Use adds global middlewares.
func (a *Aifei) Use(m ...Middleware) {
	a.middlewares = append(a.middlewares, m...)
}

// GET registers a GET route.
func (a *Aifei) GET(path string, handlers ...HandlerFunc) {
	a.router.GET(path, handlers...)
}

// POST registers a POST route.
func (a *Aifei) POST(path string, handlers ...HandlerFunc) {
	a.router.POST(path, handlers...)
}

// PUT registers a PUT route.
func (a *Aifei) PUT(path string, handlers ...HandlerFunc) {
	a.router.PUT(path, handlers...)
}

// DELETE registers a DELETE route.
func (a *Aifei) DELETE(path string, handlers ...HandlerFunc) {
	a.router.DELETE(path, handlers...)
}

// PATCH registers a PATCH route.
func (a *Aifei) PATCH(path string, handlers ...HandlerFunc) {
	a.router.PATCH(path, handlers...)
}

// Any registers a route for all HTTP methods.
func (a *Aifei) Any(path string, handlers ...HandlerFunc) {
	a.router.Any(path, handlers...)
}

// Handle registers a route for the given method and path.
func (a *Aifei) Handle(method, path string, handlers ...HandlerFunc) {
	a.router.Handle(method, path, handlers...)
}

// Group creates a RouterGroup with shared prefix and middlewares.
func (a *Aifei) Group(prefix string, middlewares ...Middleware) *RouterGroup {
	return a.router.Group(prefix, middlewares...)
}

// Register registers all public methods of a service struct as routes.
func (a *Aifei) Register(prefix string, service interface{}, middlewares ...Middleware) {
	a.router.Register(prefix, service, middlewares...)
}

// Static registers a static file server.
func (a *Aifei) Static(prefix, root string) {
	handler := Static(prefix, root)
	path := strings.TrimRight(prefix, "/") + "/*filepath"
	a.router.GET(path, handler)
	a.router.HEAD(path, handler)
}

// ServeHTTP implements http.Handler interface.
func (a *Aifei) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c := newContext(w, r)

	handlers, params, found := a.router.Lookup(r.Method, r.URL.Path)
	if !found {
		c.Status(404).Json(map[string]interface{}{
			"code": 404,
			"msg":  "Not Found",
		})
		return
	}

	c.params = params

	// Build the full handler chain: global middlewares wrapping route handlers
	allHandlers := handlers
	wrapped := func(c *Context) {
		for _, h := range allHandlers {
			h(c)
		}
	}
	for i := len(a.middlewares) - 1; i >= 0; i-- {
		wrapped = a.middlewares[i](wrapped)
	}

	c.handlers = []HandlerFunc{wrapped}
	c.index = -1
	c.Next()
}

// Start starts the HTTP server.
func (a *Aifei) Start(addr string) error {
	for _, p := range a.plugins {
		if err := p.Start(); err != nil {
			return fmt.Errorf("plugin start error: %w", err)
		}
	}

	if a.config.OnStart != nil {
		a.config.OnStart()
	}

	a.server = &http.Server{
		Addr:    addr,
		Handler: a,
	}

	fmt.Printf("[AIFEI] Server starting on %s (version %s)\n", addr, Version)
	return a.server.ListenAndServe()
}

// Stop gracefully shuts down the server.
func (a *Aifei) Stop() error {
	if a.server == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := a.server.Shutdown(ctx); err != nil {
		return err
	}

	if a.config.OnStop != nil {
		a.config.OnStop()
	}

	for _, p := range a.plugins {
		_ = p.Stop()
	}

	fmt.Println("[AIFEI] Server stopped")
	return nil
}

// Run starts the server and blocks until a shutdown signal is received.
func (a *Aifei) Run(addr string) {
	go func() {
		if err := a.Start(addr); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[AIFEI] Server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	if err := a.Stop(); err != nil {
		log.Printf("[AIFEI] Shutdown error: %v", err)
	}
}
