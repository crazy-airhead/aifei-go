package gohttp

import (
	"context"
	"net/http"
	"time"
)

// Server is the interface for HTTP server backends.
// Implement this to use an alternative server (e.g. fasthttp, fcgi).
type Server interface {
	Start(handler http.Handler) error
	Stop() error
}

// DefaultServer is the default Server implementation using net/http.
type DefaultServer struct {
	addr   string
	server *http.Server
}

// NewDefaultServer creates a DefaultServer listening on addr.
func NewDefaultServer(addr string) *DefaultServer {
	return &DefaultServer{addr: addr}
}

// Start starts the net/http server.
func (s *DefaultServer) Start(handler http.Handler) error {
	s.server = &http.Server{
		Addr:    s.addr,
		Handler: handler,
	}
	return s.server.ListenAndServe()
}

// Stop gracefully shuts down the server with a 5-second timeout.
func (s *DefaultServer) Stop() error {
	if s.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.server.Shutdown(ctx)
}
