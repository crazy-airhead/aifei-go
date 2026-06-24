package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"time"

	"github.com/crazy-airhead/aifei-go/aifei"
	gohttp "github.com/crazy-airhead/aifei-go/go-http"
)

// ---- Handler-level middleware (Input → Output) ----

// Logger returns a middleware that logs request method, path, code, and duration.
func Logger() aifei.Handler {
	return func(next aifei.HandlerFunc) aifei.HandlerFunc {
		return func(in aifei.Input) aifei.Output {
			start := time.Now()
			out := next(in)
			// Method is HTTP-specific, so it lives on gohttp.HTTPMeta, not core Input.
			method := "-"
			if h, ok := in.(gohttp.HTTPMeta); ok {
				method = h.Method()
			}
			fmt.Printf("[AIFEI] %s %s %d %s\n", method, in.Path(), out.Code(), time.Since(start).Round(time.Microsecond))
			return out
		}
	}
}

// Recover returns a middleware that recovers from panics and returns an error Output.
func Recover() aifei.Handler {
	return func(next aifei.HandlerFunc) aifei.HandlerFunc {
		return func(in aifei.Input) (out aifei.Output) {
			defer func() {
				if err := recover(); err != nil {
					buf := make([]byte, 4096)
					n := runtime.Stack(buf, false)
					fmt.Printf("[AIFEI] panic recovered: %v\n%s\n", err, buf[:n])
					out = Fail("Internal Server Error")
				}
			}()
			return next(in)
		}
	}
}

// Timeout returns a middleware that cancels the handler after the given duration.
func Timeout(d time.Duration) aifei.Handler {
	return func(next aifei.HandlerFunc) aifei.HandlerFunc {
		return func(in aifei.Input) aifei.Output {
			type result struct {
				out aifei.Output
			}
			ch := make(chan result, 1)
			go func() {
				ch <- result{next(in)}
			}()
			select {
			case r := <-ch:
				return r.out
			case <-time.After(d):
				return Fail("Gateway Timeout")
			}
		}
	}
}

// ---- HTTP-level middleware (func(http.Handler) http.Handler) ----

// CORS returns HTTP middleware that sets CORS headers and handles preflight.
func CORS(origin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Max-Age", "86400")

			if r.Method == "OPTIONS" {
				w.WriteHeader(204)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// BasicAuth returns HTTP middleware that enforces HTTP Basic Authentication.
func BasicAuth(check func(user, pass string) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			if !ok || !check(user, pass) {
				w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(200)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"code": CodeFail,
					"msg":  "Unauthorized",
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequestID returns HTTP middleware that generates a unique X-Request-ID header.
func RequestID() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := generateID()
			w.Header().Set("X-Request-ID", id)
			next.ServeHTTP(w, r)
		})
	}
}

func generateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// StaticFile returns an http.Handler that serves files from the given directory.
func StaticFile(prefix, root string) http.Handler {
	return http.StripPrefix(prefix, http.FileServer(http.Dir(root)))
}
