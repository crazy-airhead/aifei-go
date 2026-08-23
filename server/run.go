package server

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/crazy-airhead/aifei-go/aifei"
	aifeihttp "github.com/crazy-airhead/aifei-go/http"
)

// Option configures the Run function.
type Option func(*options)

type options struct {
	httpHandlers   []func(http.Handler) http.Handler
	rootWrapper    func(http.Handler) http.Handler
	ioOptions      []IoOption
	maxHeaderBytes int
}

// WithCORS adds CORS handler to the HTTP handler chain.
func WithCORS(origin string) Option {
	return func(o *options) {
		o.httpHandlers = append(o.httpHandlers, CORS(origin))
	}
}

// WithBasicAuth adds Basic Auth handler to the HTTP handler chain.
func WithBasicAuth(check func(user, pass string) bool) Option {
	return func(o *options) {
		o.httpHandlers = append(o.httpHandlers, BasicAuth(check))
	}
}

// WithRequestID adds Request ID handler to the HTTP handler chain.
func WithRequestID() Option {
	return func(o *options) {
		o.httpHandlers = append(o.httpHandlers, RequestID())
	}
}

// WithHTTPHandler adds a custom HTTP handler wrapper to the chain.
func WithHTTPHandler(m func(http.Handler) http.Handler) Option {
	return func(o *options) {
		o.httpHandlers = append(o.httpHandlers, m)
	}
}

// WithRootHandler wraps the core aifei handler (the innermost handler) before
// the httpHandler middleware chain is applied. Use it to short-circuit specific
// paths (e.g. raw file endpoints that write binary/302 responses) ahead of the
// aifei JSON router.
func WithRootHandler(wrap func(http.Handler) http.Handler) Option {
	return func(o *options) { o.rootWrapper = wrap }
}

// WithIoOptions configures the IoHandler that renders responses (view engine,
// download base, dev mode, ...).
func WithIoOptions(opts ...IoOption) Option {
	return func(o *options) { o.ioOptions = append(o.ioOptions, opts...) }
}

// WithMaxHeaderBytes limits accepted request header size in bytes (对照 Java
// undertow.maxHeaderSize). 0 keeps the net/http default (1MB); a typical
// tightening is 16 << 10.
func WithMaxHeaderBytes(n int) Option {
	return func(o *options) { o.maxHeaderBytes = n }
}

// Run starts the server and blocks until a shutdown signal is received.
func Run(app *aifei.Aifei, addr string, opts ...Option) {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}

	// Build the HTTP handler chain. The core aifei handler may be wrapped
	// (e.g. to serve raw file endpoints) before the middleware chain applies.
	core := http.Handler(NewIoHandler(app, o.ioOptions...))
	if o.rootWrapper != nil {
		core = o.rootWrapper(core)
	}
	var h http.Handler = core
	for i := len(o.httpHandlers) - 1; i >= 0; i-- {
		h = o.httpHandlers[i](h)
	}

	srv := aifeihttp.NewDefaultServer(addr)
	srv.MaxHeaderBytes = o.maxHeaderBytes

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

	// Stop plugins in reverse start order (对照 Java 29a46c6): a plugin
	// started later may depend on earlier ones, so it must stop first.
	plugins := app.Plugins()
	for i := len(plugins) - 1; i >= 0; i-- {
		_ = plugins[i].Stop()
	}

	fmt.Println("[AIFEI] Server stopped")
}
