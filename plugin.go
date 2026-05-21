package aifei

// Plugin is the interface for framework extensions.
type Plugin interface {
	Start() error
	Stop() error
}
