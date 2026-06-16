package aifei

import "strings"

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
			firstCh := path[0]
			if firstCh == '/' && len(path) > 1 {
				firstCh = path[1]
			}
			if firstCh == ':' || firstCh == '*' {
				for _, child := range n.children {
					if (child.param && firstCh == ':') || (child.catchAll && firstCh == '*') {
						n = child
						continue walk
					}
				}
			}

			// Scan for wild char in the remaining path.
			wildIdx := -1
			for j := 0; j < len(path); j++ {
				if path[j] == ':' || path[j] == '*' {
					wildIdx = j
					break
				}
			}
			if wildIdx > 0 {
				prefix := path[:wildIdx]
				wild := path[wildIdx:]
				n.children = append(n.children, &node{
					path:      prefix,
					children:  []*node{{path: wild, handlers: handlers, param: wild[0] == ':', catchAll: wild[0] == '*'}},
					wildChild: true,
				})
				return
			}
			child := &node{path: path, handlers: handlers}
			if wildIdx == 0 {
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
				params := map[string]string{paramName(child.path): path}
				return child.handlers, params, true
			}
			return nil, nil, false
		}

		if child.param {
			// Skip optional leading "/" in remaining path.
			search := path
			if len(search) > 0 && search[0] == '/' {
				search = search[1:]
			}
			end := strings.Index(search, "/")
			var val, remaining string
			if end == -1 {
				val = search
				remaining = ""
			} else {
				val = search[:end]
				remaining = search[end:]
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
			params := map[string]string{paramName(wildChild.path): wildVal}
			return wildChild.handlers, params, true
		}
		if len(wildPath) > 0 {
			h, p, ok := wildChild.find(wildPath)
			if ok {
				if p == nil {
					p = make(map[string]string)
				}
				p[paramName(wildChild.path)] = wildVal
				return h, p, true
			}
		}
	}

	return nil, nil, false
}

// paramName extracts the parameter name from a wild child path.
// Handles both ":id" and "/:id" formats.
func paramName(p string) string {
	for i, c := range p {
		if c == ':' {
			return p[i+1:]
		}
	}
	return p
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
