package server

import (
	"reflect"
	"strings"

	"github.com/crazy-airhead/aifei-go"
)

// defaultMethodMap defines exact-match method names and their HTTP methods.
// These are the default RESTful endpoints mapped to the service prefix directly.
var defaultMethodMap = map[string]string{
	"Paginate": "GET",
	"List":     "GET",
	"Create":   "POST",
}

// httpMethodPrefixes maps method name prefixes to HTTP methods.
// Methods like GetById / UpdateById / DeleteById match here and produce
// path suffixes like "/:id" via the ById → :id conversion.
var httpMethodPrefixes = []struct {
	prefix string
	method string
}{
	{"Get", "GET"},
	{"Post", "POST"},
	{"Put", "PUT"},
	{"Delete", "DELETE"},
	{"Update", "PUT"},
}

// Register registers public methods of a service struct as routes.
//
// Registration rules:
//   - Exact match in defaultMethodMap → registered at the service prefix (no path suffix).
//   - Method name starts with Get/Post/Put/Delete/Update → prefix stripped
//     to form the path suffix (e.g. GetById → GET /prefix/:id).
//   - Everything else is skipped (not added to the router).
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

		httpMethod, pathSuffix, ok := resolveRoute(name)
		if !ok {
			continue
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
				for j := len(inters) - 1; j >= 0; j-- {
					ic := inters[j]
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

// resolveRoute determines the HTTP method and path suffix for a method name.
// Returns ok=false if the method should not be registered.
func resolveRoute(name string) (httpMethod, pathSuffix string, ok bool) {
	// 1. Exact match in the default method map.
	if m, found := defaultMethodMap[name]; found {
		if name == "List" {
			return m, camelToPath(name), true
		}

		return m, "", true
	}

	// 2. Prefix match: Get*/Post*/Put*/Delete*.
	for _, p := range httpMethodPrefixes {
		if strings.HasPrefix(name, p.prefix) && name != p.prefix {
			return p.method, camelToPath(name[len(p.prefix):]), true
		}
	}

	// 3. Does not match any rule — skip.
	return "", "", false
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
