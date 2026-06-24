package server

import "github.com/crazy-airhead/aifei-go/aifei"

// ServiceRegistration holds a service instance and its route prefix.
type ServiceRegistration struct {
	Prefix  string
	Service interface{}
}

var serviceRegistry []ServiceRegistration

// RegisterService registers a service into the global registry.
// Generated service.go files call this in their init() function.
func RegisterService(prefix string, svc interface{}) {
	serviceRegistry = append(serviceRegistry, ServiceRegistration{Prefix: prefix, Service: svc})
}

// ServiceRegistrations returns all registered service registrations.
func ServiceRegistrations() []ServiceRegistration {
	return serviceRegistry
}

// AutoRegisterServices registers all services from the global registry into the app.
func AutoRegisterServices(app *aifei.Aifei, handlers ...aifei.Handler) {
	for _, reg := range serviceRegistry {
		Register(app.Router(), reg.Prefix, reg.Service, handlers...)
	}
}
