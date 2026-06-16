package server

import (
	"reflect"
	"strings"

	"github.com/crazy-airhead/aifei-go"
)

// Register registers all public methods of a service struct as routes.
// Method name prefix determines HTTP method:
//   - Get*/List*/Paginate* → GET
//   - Post*/Save*/Create* → POST
//   - Put*/Update* → PUT
//   - Delete*/Remove* → DELETE
//
// "ById" suffix maps to "/:id" path parameter.
func Register(router *aifei.Router, prefix string, service interface{}, handlers ...aifei.Handler) {
	t := reflect.TypeOf(service)
	v := reflect.ValueOf(service)
	prefix = strings.TrimRight(prefix, "/")

	var methodInterceptors map[string][]aifei.Interceptor
	if provider, ok := service.(aifei.MethodInterceptors); ok {
		methodInterceptors = provider.MethodInterceptors()
	}

	for i := 0; i < t.NumMethod(); i++ {
		method := t.Method(i)
		name := method.Name

		httpMethod := "POST"
		pathSuffix := camelToPath(name)

		switch {
		case strings.HasPrefix(name, "List"):
			httpMethod = "GET"
		case strings.HasPrefix(name, "Paginate"):
			httpMethod = "GET"
			pathSuffix = camelToPath(name[8:])
			if pathSuffix == "" {
				pathSuffix = "paginate"
			}
		case strings.HasPrefix(name, "Get"):
			httpMethod = "GET"
			pathSuffix = camelToPath(name[3:])
		case strings.HasPrefix(name, "Delete"):
			httpMethod = "DELETE"
			pathSuffix = camelToPath(name[6:])
		case strings.HasPrefix(name, "Remove"):
			httpMethod = "DELETE"
			pathSuffix = camelToPath(name[6:])
		case strings.HasPrefix(name, "Update"):
			httpMethod = "PUT"
			pathSuffix = camelToPath(name[6:])
		case strings.HasPrefix(name, "Put"):
			httpMethod = "PUT"
			pathSuffix = camelToPath(name[3:])
		case strings.HasPrefix(name, "Post"):
			pathSuffix = camelToPath(name[4:])
		case strings.HasPrefix(name, "Save"):
			pathSuffix = camelToPath(name[4:])
		case strings.HasPrefix(name, "Create"):
			pathSuffix = camelToPath(name[6:])
		}

		pathSuffix = strings.Replace(pathSuffix, "by-id", ":id", 1)

		path := prefix
		if pathSuffix != "" {
			path = prefix + "/" + pathSuffix
		}

		m := handlers
		handler := func(in aifei.Input) aifei.Output {
			invoke := func() aifei.Output {
				results := v.MethodByName(method.Name).Call([]reflect.Value{reflect.ValueOf(in)})
				if len(results) == 0 || !results[0].IsValid() {
					return nil
				}
				out, _ := results[0].Interface().(aifei.Output)
				return out
			}

			if inters, ok := methodInterceptors[name]; ok {
				for i := len(inters) - 1; i >= 0; i-- {
					ic := inters[i]
					next := invoke
					invoke = func() aifei.Output {
						return ic.Intercept(name, in, next)
					}
				}
			}

			return invoke()
		}

		for j := len(m) - 1; j >= 0; j-- {
			handler = m[j](handler)
		}

		router.Handle(httpMethod, path, handler)
	}
}

// camelToPath converts CamelCase to kebab-case path segment.
func camelToPath(s string) string {
	var result []byte
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				result = append(result, '-')
			}
			result = append(result, byte(r+32))
		} else {
			result = append(result, byte(r))
		}
	}
	return string(result)
}
