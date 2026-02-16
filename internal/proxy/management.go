package proxy

import (
	"net/http"

	"github.com/ushineko/face-puncher-supreme/internal/probe"
)

// handleManagement routes requests under the management prefix to the
// appropriate endpoint.
func (s *Server) handleManagement(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case s.managementPrefix + "/probe":
		probe.Handler(s, s.blockDataFn)(w, r)
	default:
		http.NotFound(w, r)
	}
}
