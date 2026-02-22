/*
Package web implements the dashboard HTTP server, SPA handler, and WebSocket hub.

The dashboard is served at /fps/dashboard from embedded static assets built
by Vite + React + TypeScript. All API endpoints live under /fps/api/.
*/
package web

import (
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/ushineko/face-puncher-supreme/internal/logbuf"
	"github.com/ushineko/face-puncher-supreme/internal/plugin"
)

// DashboardConfig holds all dependencies for the dashboard server.
type DashboardConfig struct {
	// PathPrefix is the management endpoint prefix (e.g., "/fps").
	PathPrefix string
	// Username and Password are the dashboard credentials.
	Username string
	Password string
	// DevMode serves from filesystem instead of embedded FS.
	DevMode bool
	// LogBuffer is the circular log buffer for the live log viewer.
	LogBuffer *logbuf.Buffer
	// HeartbeatJSON returns the heartbeat response as JSON bytes.
	HeartbeatJSON func() ([]byte, error)
	// StatsJSON returns the stats response as JSON bytes.
	StatsJSON func() ([]byte, error)
	// ConfigJSON returns the redacted config as JSON bytes.
	ConfigJSON func() ([]byte, error)
	// ReloadFn reloads the proxy configuration.
	ReloadFn func() error
	// RewriteStore is the rewrite rule persistence store (nil if plugin disabled).
	RewriteStore *plugin.RewriteStore
	// RewriteReloadFn reloads compiled rewrite rules from the store.
	RewriteReloadFn func() error
	// Logger is the structured logger.
	Logger *slog.Logger
}

// DashboardServer handles all dashboard HTTP requests.
type DashboardServer struct {
	prefix          string
	username        string
	password        string
	devMode         bool
	sessions        *sessionStore
	hub             *Hub
	logBuffer       *logbuf.Buffer
	configFn        func() ([]byte, error)
	reloadFn        func() error
	rewriteStore    *plugin.RewriteStore
	rewriteReloadFn func() error
	logger          *slog.Logger
	mux             *http.ServeMux
}

// NewDashboard creates a new dashboard server.
func NewDashboard(cfg *DashboardConfig) *DashboardServer {
	s := &DashboardServer{
		prefix:          cfg.PathPrefix,
		username:        cfg.Username,
		password:        cfg.Password,
		devMode:         cfg.DevMode,
		sessions:        newSessionStore(),
		logBuffer:       cfg.LogBuffer,
		configFn:        cfg.ConfigJSON,
		reloadFn:        cfg.ReloadFn,
		rewriteStore:    cfg.RewriteStore,
		rewriteReloadFn: cfg.RewriteReloadFn,
		logger:          cfg.Logger,
	}

	s.hub = newHub(cfg.HeartbeatJSON, cfg.StatsJSON, cfg.ReloadFn, cfg.LogBuffer, cfg.Logger)
	s.mux = s.buildMux()
	return s
}

// Start begins the WebSocket hub goroutine.
func (s *DashboardServer) Start() {
	go s.hub.run()
}

// Stop shuts down the WebSocket hub.
func (s *DashboardServer) Stop() {
	s.hub.stop()
}

// ServeHTTP implements http.Handler.
func (s *DashboardServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *DashboardServer) buildMux() *http.ServeMux {
	mux := http.NewServeMux()
	p := s.prefix

	// Auth endpoints (login and status are public).
	mux.HandleFunc("POST "+p+"/api/auth/login", s.handleLogin)
	mux.HandleFunc("POST "+p+"/api/auth/logout", s.requireAuth(s.handleLogout))
	mux.HandleFunc("GET "+p+"/api/auth/status", s.handleAuthStatus)

	// Protected API endpoints.
	mux.HandleFunc("GET "+p+"/api/readme", s.requireAuth(s.handleReadme))
	mux.HandleFunc("GET "+p+"/api/config", s.requireAuth(s.handleConfig))
	mux.HandleFunc("GET "+p+"/api/logs", s.requireAuth(s.handleLogs))
	mux.HandleFunc(p+"/api/ws", s.requireAuth(s.handleWebSocket))

	// Rewrite rules CRUD (only if rewrite plugin is active).
	if s.rewriteStore != nil {
		mux.HandleFunc("GET "+p+"/api/rewrite/rules", s.requireAuth(s.handleRewriteList))
		mux.HandleFunc("POST "+p+"/api/rewrite/rules", s.requireAuth(s.handleRewriteCreate))
		mux.HandleFunc("GET "+p+"/api/rewrite/rules/{id}", s.requireAuth(s.handleRewriteGet))
		mux.HandleFunc("PUT "+p+"/api/rewrite/rules/{id}", s.requireAuth(s.handleRewriteUpdate))
		mux.HandleFunc("DELETE "+p+"/api/rewrite/rules/{id}", s.requireAuth(s.handleRewriteDelete))
		mux.HandleFunc("PATCH "+p+"/api/rewrite/rules/{id}/toggle", s.requireAuth(s.handleRewriteToggle))
		mux.HandleFunc("POST "+p+"/api/rewrite/test", s.requireAuth(s.handleRewriteTest))
	}

	// Proxy restart.
	mux.HandleFunc("POST "+p+"/api/restart", s.requireAuth(s.handleRestart))

	// SPA handler — serves static files with index.html fallback.
	mux.Handle(p+"/dashboard/", s.spaHandler())
	mux.HandleFunc(p+"/dashboard", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, p+"/dashboard/", http.StatusMovedPermanently)
	})

	return mux
}

// spaHandler returns an http.Handler that serves the React SPA.
// Static files are served directly; all other paths fall back to index.html
// for client-side routing.
func (s *DashboardServer) spaHandler() http.Handler {
	var fsys fs.FS
	if s.devMode {
		fsys = os.DirFS("web/ui/dist")
	} else {
		sub, err := fs.Sub(staticFS, "ui/dist")
		if err != nil {
			s.logger.Error("failed to create sub-filesystem for SPA", "error", err)
			return http.NotFoundHandler()
		}
		fsys = sub
	}

	prefix := s.prefix + "/dashboard/"

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Strip the prefix to get the relative file path.
		path := strings.TrimPrefix(r.URL.Path, prefix)
		if path == "" {
			path = "index.html"
		}

		// Try to serve the static file.
		f, err := fsys.Open(path)
		if err == nil {
			fi, statErr := f.Stat()
			_ = f.Close()
			if statErr == nil && !fi.IsDir() {
				http.ServeFileFS(w, r, fsys, path)
				return
			}
		}

		// SPA fallback: serve index.html for client-side routing.
		http.ServeFileFS(w, r, fsys, "index.html")
	})
}
