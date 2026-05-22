package aifei

// Config holds framework configuration.
type Config struct {
	Middlewares []Middleware
	Plugins     []Plugin
	Server      Server
	OnStart     func()
	OnStop      func()
}

// Option is a functional option for configuring Aifei.
type Option func(*Aifei)

// WithMiddleware adds global middlewares.
func WithMiddleware(m ...Middleware) Option {
	return func(a *Aifei) {
		a.middlewares = append(a.middlewares, m...)
	}
}

// WithPlugin adds plugins.
func WithPlugin(p ...Plugin) Option {
	return func(a *Aifei) {
		a.plugins = append(a.plugins, p...)
	}
}

// WithServer sets a custom HTTP server backend.
func WithServer(s Server) Option {
	return func(a *Aifei) {
		a.server = s
	}
}

// WithOnStart sets the start callback.
func WithOnStart(fn func()) Option {
	return func(a *Aifei) {
		a.config.OnStart = fn
	}
}

// WithOnStop sets the stop callback.
func WithOnStop(fn func()) Option {
	return func(a *Aifei) {
		a.config.OnStop = fn
	}
}
