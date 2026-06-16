package server

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/crazy-airhead/aifei-go"
	gohttp "github.com/crazy-airhead/aifei-go/go-http"
)

// Option configures the Run function.
type Option func(*options)

type options struct {
	httpMiddlewares []func(http.Handler) http.Handler
}

// WithCORS adds CORS middleware to the HTTP handler chain.
func WithCORS(origin string) Option {
	return func(o *options) {
		o.httpMiddlewares = append(o.httpMiddlewares, CORS(origin))
	}
}

// WithBasicAuth adds Basic Auth middleware to the HTTP handler chain.
func WithBasicAuth(check func(user, pass string) bool) Option {
	return func(o *options) {
		o.httpMiddlewares = append(o.httpMiddlewares, BasicAuth(check))
	}
}

// WithRequestID adds Request ID middleware to the HTTP handler chain.
func WithRequestID() Option {
	return func(o *options) {
		o.httpMiddlewares = append(o.httpMiddlewares, RequestID())
	}
}

// WithHTTPMiddleware adds a custom HTTP middleware to the handler chain.
func WithHTTPMiddleware(m func(http.Handler) http.Handler) Option {
	return func(o *options) {
		o.httpMiddlewares = append(o.httpMiddlewares, m)
	}
}

// Run starts the server and blocks until a shutdown signal is received.
func Run(app *aifei.Aifei, addr string, opts ...Option) {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}

	// Build the HTTP handler chain
	var h http.Handler = gohttp.NewHttpHandler(app)
	for i := len(o.httpMiddlewares) - 1; i >= 0; i-- {
		h = o.httpMiddlewares[i](h)
	}

	srv := gohttp.NewDefaultServer(addr)

	for _, p := range app.Plugins() {
		if err := p.Start(); err != nil {
			log.Fatalf("[AIFEI] Plugin start error: %v", err)
		}
	}

	if f := app.OnStartFunc(); f != nil {
		f()
	}

	go func() {
		fmt.Printf("[AIFEI] Server starting on %s (version %s)\n", addr, aifei.Version)
		if err := srv.Start(h); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[AIFEI] Server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	if err := srv.Stop(); err != nil {
		log.Printf("[AIFEI] Shutdown error: %v", err)
	}

	if f := app.OnStopFunc(); f != nil {
		f()
	}

	for _, p := range app.Plugins() {
		_ = p.Stop()
	}

	fmt.Println("[AIFEI] Server stopped")
}
