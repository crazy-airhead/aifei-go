package aifei

// Config holds framework configuration.
type Config struct {
	Handlers []Handler
	Plugins  []Plugin
	OnStart  func()
	OnStop   func()
}

// Option is a functional option for configuring Aifei.
type Option func(*Aifei)

// WithHandlers adds global handlers.
func WithHandlers(h ...Handler) Option {
	return func(a *Aifei) {
		a.handlers = append(a.handlers, h...)
	}
}

// WithPlugin adds plugins.
func WithPlugin(p ...Plugin) Option {
	return func(a *Aifei) {
		a.plugins = append(a.plugins, p...)
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
