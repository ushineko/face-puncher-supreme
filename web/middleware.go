package web

import "net/http"

// requireAuth wraps an http.HandlerFunc, returning 401 if no valid session exists.
func (s *DashboardServer) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := getSessionToken(r)
		if !s.sessions.validate(token) {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}
