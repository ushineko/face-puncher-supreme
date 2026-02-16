package proxy

import (
	"net/http"

	"github.com/ushineko/face-puncher-supreme/internal/probe"
)

// handleManagement routes requests under the /fps/ prefix to the
// appropriate management endpoint.
func (s *Server) handleManagement(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/fps/probe":
		probe.Handler(s)(w, r)
	default:
		http.NotFound(w, r)
	}
}
