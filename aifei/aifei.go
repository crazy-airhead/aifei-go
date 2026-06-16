package aifei

// Version is the framework version.
const Version = "1.0.0"

// Aifei is the core framework instance.
// Server startup is handled by the go-http module (or other server adapters).
type Aifei struct {
	config   *Config
	router   *Router
	handlers []Handler
	plugins  []Plugin
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

// ---- Accessors (used by server adapters) ----

// Router returns the internal Router.
func (a *Aifei) Router() *Router { return a.router }

// Handlers returns the global handlers.
func (a *Aifei) Handlers() []Handler { return a.handlers }

// Plugins returns the registered plugins.
func (a *Aifei) Plugins() []Plugin { return a.plugins }

// OnStartFunc returns the OnStart callback.
func (a *Aifei) OnStartFunc() func() { return a.config.OnStart }

// OnStopFunc returns the OnStop callback.
func (a *Aifei) OnStopFunc() func() { return a.config.OnStop }

// Use adds global handlers.
func (a *Aifei) Use(h ...Handler) {
	a.handlers = append(a.handlers, h...)
}

// ---- Route Registration ----

// GET registers a GET route.
func (a *Aifei) GET(path string, handlerFuncs ...HandlerFunc) {
	a.router.GET(path, handlerFuncs...)
}

// POST registers a POST route.
func (a *Aifei) POST(path string, handlerFuncs ...HandlerFunc) {
	a.router.POST(path, handlerFuncs...)
}

// PUT registers a PUT route.
func (a *Aifei) PUT(path string, handlerFuncs ...HandlerFunc) {
	a.router.PUT(path, handlerFuncs...)
}

// DELETE registers a DELETE route.
func (a *Aifei) DELETE(path string, handlerFuncs ...HandlerFunc) {
	a.router.DELETE(path, handlerFuncs...)
}

// PATCH registers a PATCH route.
func (a *Aifei) PATCH(path string, handlerFuncs ...HandlerFunc) {
	a.router.PATCH(path, handlerFuncs...)
}

// Any registers a route for all HTTP methods.
func (a *Aifei) Any(path string, handlerFuncs ...HandlerFunc) {
	a.router.Any(path, handlerFuncs...)
}

// Handle registers a route for the given method and path.
func (a *Aifei) Handle(method, path string, handlerFuncs ...HandlerFunc) {
	a.router.Handle(method, path, handlerFuncs...)
}

// Group creates a RouterGroup with shared prefix and handlers.
func (a *Aifei) Group(prefix string, handlers ...Handler) *RouterGroup {
	return a.router.Group(prefix, handlers...)
}

// Register registers all public methods of a service struct as routes.
func (a *Aifei) Register(prefix string, service interface{}, handlers ...Handler) {
	a.router.Register(prefix, service, handlers...)
}
