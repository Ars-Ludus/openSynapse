// Package api provides the HTTP REST API for openSynapse. All handlers are
// thin wrappers over service.Service — no business logic lives here.
package api

import (
	"net/http"

	"github.com/Ars-Ludus/openSynapse/internal/service"
)

// Server wraps a service.Service and exposes it over HTTP.
type Server struct {
	svc *service.Service
	mux *http.ServeMux
}

// New creates a Server and registers all routes.
func New(svc *service.Service) *Server {
	s := &Server{svc: svc, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// ListenAndServe starts the HTTP listener on addr (e.g. ":8080").
func (s *Server) ListenAndServe(addr string) error {
	return http.ListenAndServe(addr, s)
}

func (s *Server) routes() {
	// Health
	s.mux.HandleFunc("GET /health", s.health)

	// Files
	s.mux.HandleFunc("GET /files", s.listFiles)
	s.mux.HandleFunc("GET /files/{path...}/snippets", s.getFileSnippets)
	s.mux.HandleFunc("GET /files/{path...}", s.getFile)

	// Search
	s.mux.HandleFunc("POST /search", s.search)

	// Snippets
	s.mux.HandleFunc("GET /snippets/{id}/dependencies", s.getDependencies)
	s.mux.HandleFunc("GET /snippets/{id}/dependents", s.getDependents)
	s.mux.HandleFunc("GET /snippets/{id}", s.getSnippet)

	// Patterns
	s.mux.HandleFunc("GET /patterns", s.listPatterns)

	// Reindex
	s.mux.HandleFunc("POST /reindex", s.reindex)
}
