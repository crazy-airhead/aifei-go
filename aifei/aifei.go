package aifei

// Version is the framework version.
const Version = "1.0.0"

// Aifei is the core framework instance.
// Server startup is handled by the go-http module (or other server adapters).
type Aifei struct {
	config      *Config
	router      *Router
	middlewares []Middleware
	plugins     []Plugin
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

// Middlewares returns the global middlewares.
func (a *Aifei) Middlewares() []Middleware { return a.middlewares }

// Plugins returns the registered plugins.
func (a *Aifei) Plugins() []Plugin { return a.plugins }

// OnStartFunc returns the OnStart callback.
func (a *Aifei) OnStartFunc() func() { return a.config.OnStart }

// OnStopFunc returns the OnStop callback.
func (a *Aifei) OnStopFunc() func() { return a.config.OnStop }

// ---- Middleware ----

// Use adds global middlewares.
func (a *Aifei) Use(m ...Middleware) {
	a.middlewares = append(a.middlewares, m...)
}

// ---- Route Registration ----

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
