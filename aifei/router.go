package aifei

import (
	"reflect"
	"strings"
)

// Router manages route registration and matching using a radix tree per HTTP method.
type Router struct {
	trees map[string]*node
}

type node struct {
	path      string
	children  []*node
	handlers  []HandlerFunc
	wildChild bool
	param     bool
	catchAll  bool
}

// NewRouter creates a new Router.
func NewRouter() *Router {
	return &Router{
		trees: make(map[string]*node),
	}
}

// GET registers a GET route.
func (r *Router) GET(path string, handlers ...HandlerFunc) {
	r.Handle("GET", path, handlers...)
}

// POST registers a POST route.
func (r *Router) POST(path string, handlers ...HandlerFunc) {
	r.Handle("POST", path, handlers...)
}

// PUT registers a PUT route.
func (r *Router) PUT(path string, handlers ...HandlerFunc) {
	r.Handle("PUT", path, handlers...)
}

// DELETE registers a DELETE route.
func (r *Router) DELETE(path string, handlers ...HandlerFunc) {
	r.Handle("DELETE", path, handlers...)
}

// PATCH registers a PATCH route.
func (r *Router) PATCH(path string, handlers ...HandlerFunc) {
	r.Handle("PATCH", path, handlers...)
}

// HEAD registers a HEAD route.
func (r *Router) HEAD(path string, handlers ...HandlerFunc) {
	r.Handle("HEAD", path, handlers...)
}

// Any registers a route for all HTTP methods.
func (r *Router) Any(path string, handlers ...HandlerFunc) {
	for _, m := range []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"} {
		r.Handle(m, path, handlers...)
	}
}

// Handle registers a route for the given method and path.
func (r *Router) Handle(method, path string, handlers ...HandlerFunc) {
	if path == "" {
		path = "/"
	}
	if path[0] != '/' {
		path = "/" + path
	}

	root := r.trees[method]
	if root == nil {
		root = &node{}
		r.trees[method] = root
	}

	root.add(path, handlers)
}

// Group creates a RouterGroup with the given prefix and optional handlers.
func (r *Router) Group(prefix string, handlers ...Handler) *RouterGroup {
	return &RouterGroup{
		prefix:   prefix,
		handlers: handlers,
		router:   r,
	}
}

// Register registers all public methods of a service struct as routes.
// Method name prefix determines HTTP method:
//   - Get*/List* → GET
//   - Post*/Save*/Create* → POST
//   - Put*/Update* → PUT
//   - Delete*/Remove* → DELETE
//
// "ById" suffix maps to "/:id" path parameter.
func (r *Router) Register(prefix string, service interface{}, handlers ...Handler) {
	t := reflect.TypeOf(service)
	v := reflect.ValueOf(service)
	prefix = strings.TrimRight(prefix, "/")

	// Extract method-level interceptors
	var methodInterceptors map[string][]Interceptor
	if provider, ok := service.(MethodInterceptors); ok {
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
		handler := func(in Input) Output {
			// Build the method invocation chain with interceptors
			invoke := func() Output {
				results := v.MethodByName(method.Name).Call([]reflect.Value{reflect.ValueOf(in)})
				if len(results) == 0 || !results[0].IsValid() {
					return nil
				}
				out, _ := results[0].Interface().(Output)
				return out
			}

			if inters, ok := methodInterceptors[name]; ok {
				for i := len(inters) - 1; i >= 0; i-- {
					ic := inters[i]
					next := invoke
					invoke = func() Output {
						return ic.Intercept(name, in, next)
					}
				}
			}

			return invoke()
		}

		for j := len(m) - 1; j >= 0; j-- {
			handler = m[j](handler)
		}

		r.Handle(httpMethod, path, handler)
	}
}

// Lookup finds the handlers and path parameters for the given method and path.
func (r *Router) Lookup(method, path string) (handlers []HandlerFunc, params map[string]string, found bool) {
	root := r.trees[method]
	if root == nil {
		return nil, nil, false
	}
	return root.find(path)
}

// ---- radix tree implementation ----

func (n *node) add(path string, handlers []HandlerFunc) {
	if n.path == "" && len(n.children) == 0 {
		for i := 0; i < len(path); i++ {
			if path[i] == ':' || path[i] == '*' {
				if i > 0 {
					n.path = path[:i]
					n.children = []*node{{
						path:     path[i:],
						handlers: handlers,
						param:    path[i] == ':',
						catchAll: path[i] == '*',
					}}
					n.wildChild = true
				} else {
					n.path = path
					n.handlers = handlers
					n.param = (path[0] == ':')
					n.catchAll = (path[0] == '*')
				}
				return
			}
		}
		n.path = path
		n.handlers = handlers
		return
	}

walk:
	for {
		i := commonPrefix(n.path, path)
		if i < len(n.path) {
			child := &node{
				path:      n.path[i:],
				handlers:  n.handlers,
				children:  n.children,
				wildChild: n.path[i] == ':' || n.path[i] == '*',
				param:     n.path[i] == ':',
				catchAll:  n.path[i] == '*',
			}
			n.path = path[:i]
			n.handlers = nil
			n.children = []*node{child}
			n.wildChild = false
			n.param = false
			n.catchAll = false
		}

		if i < len(path) {
			path = path[i:]

			// Check literal children first.
			for _, child := range n.children {
				if !child.param && !child.catchAll && len(child.path) > 0 && child.path[0] == path[0] {
					n = child
					continue walk
				}
			}

			// For wild paths, follow existing wild child.
			if path[0] == ':' || path[0] == '*' {
				for _, child := range n.children {
					if (child.param && path[0] == ':') || (child.catchAll && path[0] == '*') {
						n = child
						continue walk
					}
				}
			}

			child := &node{path: path, handlers: handlers}
			if path[0] == ':' || path[0] == '*' {
				n.wildChild = true
				if path[0] == '*' {
					child.catchAll = true
				} else {
					child.param = true
				}
			}
			n.children = append(n.children, child)
			return
		}

		n.handlers = handlers
		return
	}
}

func (n *node) find(path string) (handlers []HandlerFunc, params map[string]string, found bool) {
	if len(path) < len(n.path) || path[:len(n.path)] != n.path {
		return nil, nil, false
	}
	path = path[len(n.path):]

	if len(path) == 0 {
		if n.handlers != nil {
			return n.handlers, nil, true
		}
		return nil, nil, false
	}

	var wildChild *node
	var wildVal string
	var wildPath string

	for _, child := range n.children {
		if child.catchAll {
			if child.handlers != nil {
				params := map[string]string{child.path[1:]: path}
				return child.handlers, params, true
			}
			return nil, nil, false
		}

		if child.param {
			end := strings.Index(path, "/")
			var val, remaining string
			if end == -1 {
				val = path
				remaining = ""
			} else {
				val = path[:end]
				remaining = path[end:]
			}
			wildChild = child
			wildVal = val
			wildPath = remaining
			continue
		}

		if len(child.path) > 0 && path[0] == child.path[0] {
			return child.find(path)
		}
	}

	if wildChild != nil {
		if len(wildPath) == 0 && wildChild.handlers != nil {
			params := map[string]string{wildChild.path[1:]: wildVal}
			return wildChild.handlers, params, true
		}
		if len(wildPath) > 0 {
			h, p, ok := wildChild.find(wildPath)
			if ok {
				if p == nil {
					p = make(map[string]string)
				}
				p[wildChild.path[1:]] = wildVal
				return h, p, true
			}
		}
	}

	return nil, nil, false
}

func commonPrefix(a, b string) int {
	max := len(a)
	if len(b) < max {
		max = len(b)
	}
	i := 0
	for i < max && a[i] == b[i] {
		i++
	}
	return i
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

// ---- RouterGroup ----

// RouterGroup supports grouped routes with shared prefix and handlers.
type RouterGroup struct {
	prefix   string
	handlers []Handler
	router   *Router
}

// GET registers a GET route in the group.
func (g *RouterGroup) GET(path string, handlers ...HandlerFunc) {
	g.Handle("GET", path, handlers...)
}

// POST registers a POST route in the group.
func (g *RouterGroup) POST(path string, handlers ...HandlerFunc) {
	g.Handle("POST", path, handlers...)
}

// PUT registers a PUT route in the group.
func (g *RouterGroup) PUT(path string, handlers ...HandlerFunc) {
	g.Handle("PUT", path, handlers...)
}

// DELETE registers a DELETE route in the group.
func (g *RouterGroup) DELETE(path string, handlers ...HandlerFunc) {
	g.Handle("DELETE", path, handlers...)
}

// Handle registers a route in the group.
func (g *RouterGroup) Handle(method, path string, handlers ...HandlerFunc) {
	fullPath := g.prefix + path
	final := handlers[len(handlers)-1]
	wrapped := final
	for i := len(g.handlers) - 1; i >= 0; i-- {
		wrapped = g.handlers[i](wrapped)
	}
	g.router.Handle(method, fullPath, wrapped)
}

// Group creates a sub-group with extended prefix.
func (g *RouterGroup) Group(prefix string, handlers ...Handler) *RouterGroup {
	return &RouterGroup{
		prefix:   g.prefix + prefix,
		handlers: append(g.handlers, handlers...),
		router:   g.router,
	}
}
