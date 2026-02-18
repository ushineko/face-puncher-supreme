package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
)

// handleLogin validates credentials and creates a session.
func (s *DashboardServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if req.Username != s.username || req.Password != s.password {
		http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
		return
	}

	token, err := s.sessions.create()
	if err != nil {
		s.logger.Error("failed to create session", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	setSessionCookie(w, token)
	w.Header().Set("Content-Type", "application/json")
	resp, _ := json.Marshal(map[string]string{"status": "ok", "token": token}) //nolint:errcheck // static map always marshals
	_, _ = w.Write(resp)                                                        //nolint:errcheck // best-effort response
}

// handleLogout invalidates the current session.
func (s *DashboardServer) handleLogout(w http.ResponseWriter, r *http.Request) {
	token := getSessionToken(r)
	if token != "" {
		s.sessions.revoke(token)
	}
	clearSessionCookie(w)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck // best-effort response
}

// handleAuthStatus returns the current session state.
func (s *DashboardServer) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	token := getSessionToken(r)
	authed := s.sessions.validate(token)
	w.Header().Set("Content-Type", "application/json")
	if authed {
		_, _ = w.Write([]byte(`{"authenticated":true}`)) //nolint:errcheck // best-effort response
	} else {
		_, _ = w.Write([]byte(`{"authenticated":false}`)) //nolint:errcheck // best-effort response
	}
}

// handleReadme returns the embedded README content as plain text.
func (s *DashboardServer) handleReadme(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(readmeContent)) //nolint:errcheck // best-effort response
}

// handleConfig returns the resolved (redacted) configuration as JSON.
func (s *DashboardServer) handleConfig(w http.ResponseWriter, _ *http.Request) {
	data, err := s.configFn()
	if err != nil {
		s.logger.Error("failed to get config", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data) //nolint:errcheck // best-effort response
}

// handleLogs returns recent log entries from the circular buffer.
// Query params: n (max entries, default 100, max 1000), level (min level, default INFO).
func (s *DashboardServer) handleLogs(w http.ResponseWriter, r *http.Request) {
	n := 100
	if nStr := r.URL.Query().Get("n"); nStr != "" {
		if parsed, parseErr := strconv.Atoi(nStr); parseErr == nil && parsed > 0 {
			n = parsed
		}
	}
	if n > 1000 {
		n = 1000
	}

	minLevel := slog.LevelInfo
	if lvl := r.URL.Query().Get("level"); lvl != "" {
		switch lvl {
		case "debug", "DEBUG":
			minLevel = slog.LevelDebug
		case "warn", "WARN":
			minLevel = slog.LevelWarn
		case "error", "ERROR":
			minLevel = slog.LevelError
		}
	}

	entries := s.logBuffer.Recent(n, minLevel)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(entries) //nolint:errcheck // best-effort response
}
