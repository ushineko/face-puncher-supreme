package proxy

import (
	"net/http"
	"strings"
)

// handleManagement routes requests under the management prefix to the
// appropriate endpoint.
func (s *Server) handleManagement(w http.ResponseWriter, r *http.Request) {
	// Exact-match endpoints first (monitoring/automation — no auth).
	switch r.URL.Path {
	case s.managementPrefix + "/heartbeat":
		s.heartbeatHandler(w, r)
		return
	case s.managementPrefix + "/stats":
		s.statsHandler(w, r)
		return
	case s.managementPrefix + "/ca.pem":
		if s.caPEMHandler != nil {
			s.caPEMHandler(w, r)
		} else {
			http.NotFound(w, r)
		}
		return
	}

	// Dashboard routes: /fps/dashboard* and /fps/api/*
	path := r.URL.Path
	prefix := s.managementPrefix
	if strings.HasPrefix(path, prefix+"/dashboard") || strings.HasPrefix(path, prefix+"/api/") {
		if s.dashboardHandler != nil {
			s.dashboardHandler.ServeHTTP(w, r)
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			//nolint:errcheck // best-effort response
			_, _ = w.Write([]byte(`{"error":"dashboard not configured (set --dashboard-user and --dashboard-pass)"}`))
		}
		return
	}

	http.NotFound(w, r)
}
