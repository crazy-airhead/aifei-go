package aifei

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

// AutoRegisterServices registers all services from the global registry.
func (a *Aifei) AutoRegisterServices(middlewares ...Middleware) {
	for _, reg := range serviceRegistry {
		a.Register(reg.Prefix, reg.Service, middlewares...)
	}
}
