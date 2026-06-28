package dami

// defaultBus is the package-level singleton, mirroring Java's Dami.bus().
var defaultBus = New()

// Default returns the package-level default bus.
func Default() *Bus { return defaultBus }

// Configure rebuilds the default bus with the given options (e.g. to switch the
// router to Path/Tag). Call at startup, before any Listen/Send, since listeners
// already registered belong to the previous instance. Mirrors Java
// DamiConfig.configure.
func Configure(opts ...Option) {
	defaultBus = New(opts...)
}

// Send broadcasts on the default bus — the common case, mirroring
// Dami.bus().send(topic, payload, fallback).
func Send[P any](topic string, payload P, fallback ...func(P)) (*Event[P], error) {
	return SendOn(defaultBus, topic, payload, fallback...)
}

// Listen registers a typed listener on the default bus; returns an unlisten func.
func Listen[P any](topic string, listener Listener[P], index ...int) (unlisten func()) {
	return ListenOn(defaultBus, topic, listener, index...)
}

// Intercept registers an interceptor on the default bus.
func Intercept(index int, it Interceptor) { defaultBus.Intercept(index, it) }

// UnlistenAll removes every listener under a topic on the default bus.
func UnlistenAll(topic string) { defaultBus.UnlistenAll(topic) }

// DefaultRouter returns the default bus's router.
func DefaultRouter() Router { return defaultBus.Router() }
