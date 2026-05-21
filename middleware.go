package aifei

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"runtime"
	"time"
)

// Logger returns a middleware that logs request method, path, status, and duration.
func Logger() Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) {
			start := time.Now()
			next(c)
			duration := time.Since(start)
			status := c.status
			if status == 0 {
				status = 200
			}
			fmt.Printf("[AIFEI] %s %s %d %s\n", c.Method(), c.Path(), status, duration.Round(time.Microsecond))
		}
	}
}

// Recover returns a middleware that recovers from panics and returns 500.
func Recover() Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) {
			defer func() {
				if err := recover(); err != nil {
					buf := make([]byte, 4096)
					n := runtime.Stack(buf, false)
					fmt.Printf("[AIFEI] panic recovered: %v\n%s\n", err, buf[:n])
					c.Status(500).Json(map[string]interface{}{
						"code": 500,
						"msg":  "Internal Server Error",
					})
				}
			}()
			next(c)
		}
	}
}

// CORS returns a middleware that sets CORS headers.
func CORS(origin string) Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Max-Age", "86400")

			if c.Method() == "OPTIONS" {
				c.Status(204)
				c.written = true
				c.Writer.WriteHeader(204)
				return
			}
			next(c)
		}
	}
}

// BasicAuth returns a middleware that enforces HTTP Basic Authentication.
func BasicAuth(check func(user, pass string) bool) Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) {
			user, pass, ok := c.Request.BasicAuth()
			if !ok || !check(user, pass) {
				c.Header("WWW-Authenticate", `Basic realm="Restricted"`)
				c.Status(401).Json(map[string]interface{}{
					"code": 401,
					"msg":  "Unauthorized",
				})
				return
			}
			next(c)
		}
	}
}

// RequestID returns a middleware that generates a unique request ID.
func RequestID() Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) {
			id := generateID()
			c.Header("X-Request-ID", id)
			c.Request.Header.Set("X-Request-ID", id)
			next(c)
		}
	}
}

func generateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Timeout returns a middleware that sets a request deadline.
func Timeout(duration time.Duration) Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) {
			done := make(chan struct{})
			go func() {
				next(c)
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(duration):
				c.Status(504).Json(map[string]interface{}{
					"code": 504,
					"msg":  "Gateway Timeout",
				})
			}
		}
	}
}

// Static serves files from a directory.
func Static(prefix, root string) HandlerFunc {
	fs := http.StripPrefix(prefix, http.FileServer(http.Dir(root)))
	return func(c *Context) {
		fs.ServeHTTP(c.Writer, c.Request)
	}
}
