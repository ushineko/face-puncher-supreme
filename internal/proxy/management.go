package proxy

import (
	"net/http"
)

// handleManagement routes requests under the management prefix to the
// appropriate endpoint.
func (s *Server) handleManagement(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case s.managementPrefix + "/heartbeat":
		s.heartbeatHandler(w, r)
	case s.managementPrefix + "/stats":
		s.statsHandler(w, r)
	case s.managementPrefix + "/ca.pem":
		if s.caPEMHandler != nil {
			s.caPEMHandler(w, r)
		} else {
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}
