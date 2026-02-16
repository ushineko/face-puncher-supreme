/*
Package proxy implements an HTTP/HTTPS forward proxy server.

The proxy handles HTTP requests by forwarding them to the destination
and HTTPS requests via the CONNECT method by establishing a TCP tunnel.
Requests to the /fps/ path prefix are intercepted and handled as
management endpoints (e.g., the probe/liveness endpoint).
*/
package proxy

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ushineko/face-puncher-supreme/internal/probe"
)

// Blocker checks whether a domain should be blocked.
type Blocker interface {
	IsBlocked(domain string) bool
}

// Server is an HTTP/HTTPS forward proxy.
type Server struct {
	httpServer  *http.Server
	logger      *slog.Logger
	verbose     bool
	startTime   time.Time
	blocker     Blocker
	blockDataFn func() *probe.BlockData

	// Connection counters.
	connectionsTotal  atomic.Int64
	connectionsActive atomic.Int64

	// shutdownOnce ensures graceful shutdown runs once.
	shutdownOnce sync.Once
}

// Config holds proxy server configuration.
type Config struct {
	// ListenAddr is the address to listen on (e.g., ":8080" or "0.0.0.0:8080").
	ListenAddr string
	// Logger is the structured logger to use. If nil, a default is created.
	Logger *slog.Logger
	// Verbose enables detailed request/response logging (headers, sizes, timing).
	Verbose bool
	// Blocker checks domains against a blocklist. If nil, no blocking is performed.
	Blocker Blocker
	// BlockDataFn returns current block statistics for the probe endpoint.
	// If nil, no block stats are reported.
	BlockDataFn func() *probe.BlockData
}

// New creates a new proxy server with the given configuration.
func New(cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	s := &Server{
		logger:      cfg.Logger,
		verbose:     cfg.Verbose,
		startTime:   time.Now(),
		blocker:     cfg.Blocker,
		blockDataFn: cfg.BlockDataFn,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/fps/", s.handleManagement)

	s.httpServer = &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           s, // Server itself implements http.Handler
		ReadHeaderTimeout: 10 * time.Second,
	}

	return s
}

// ServeHTTP dispatches incoming requests to either the management handler,
// the CONNECT tunnel handler, or the HTTP forward proxy handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.connectionsTotal.Add(1)
	s.connectionsActive.Add(1)
	defer s.connectionsActive.Add(-1)

	// Management endpoints are handled directly regardless of request method.
	if len(r.URL.Path) >= 5 && r.URL.Path[:5] == "/fps/" {
		s.handleManagement(w, r)
		return
	}

	if r.Method == http.MethodConnect {
		s.handleConnect(w, r)
		return
	}

	s.handleHTTP(w, r)
}

// handleHTTP forwards an HTTP request to the destination server and relays
// the response back to the client.
func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Host == "" {
		http.Error(w, "missing host in request", http.StatusBadRequest)
		s.logger.Warn("bad request: missing host",
			"method", r.Method,
			"url", r.URL.String(),
			"remote", r.RemoteAddr,
		)
		return
	}

	// Check blocklist before forwarding.
	if s.blocker != nil && s.blocker.IsBlocked(stripPort(r.URL.Host)) {
		http.Error(w, "blocked by proxy", http.StatusForbidden)
		s.logger.Info("blocked",
			"method", r.Method,
			"host", r.URL.Host,
			"remote", r.RemoteAddr,
		)
		return
	}

	start := time.Now()

	if s.verbose {
		s.logger.Debug("http request",
			"method", r.Method,
			"url", r.URL.String(),
			"remote", r.RemoteAddr,
			"user_agent", r.Header.Get("User-Agent"),
			"content_length", r.ContentLength,
			"headers", flattenHeaders(r.Header),
		)
	}

	// Create the outbound request. We must not reuse the incoming request
	// directly because the proxy hop headers need to be stripped.
	outReq := r.Clone(r.Context())
	outReq.RequestURI = "" // Required for client requests.
	removeHopByHopHeaders(outReq.Header)

	resp, err := http.DefaultTransport.RoundTrip(outReq)
	if err != nil {
		http.Error(w, fmt.Sprintf("proxy error: %v", err), http.StatusBadGateway)
		s.logger.Error("upstream request failed",
			"method", r.Method,
			"url", r.URL.String(),
			"error", err,
			"duration_ms", time.Since(start).Milliseconds(),
		)
		return
	}
	defer resp.Body.Close() //nolint:errcheck // response body close in defer

	removeHopByHopHeaders(resp.Header)

	// Copy response headers.
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	written, _ := io.Copy(w, resp.Body) //nolint:errcheck // best-effort streaming

	duration := time.Since(start)

	s.logger.Info("http",
		"method", r.Method,
		"url", r.URL.String(),
		"status", resp.StatusCode,
		"content_type", resp.Header.Get("Content-Type"),
		"duration_ms", duration.Milliseconds(),
		"remote", r.RemoteAddr,
	)

	if s.verbose {
		s.logger.Debug("http response",
			"method", r.Method,
			"url", r.URL.String(),
			"status", resp.StatusCode,
			"response_bytes", written,
			"content_length", resp.ContentLength,
			"duration_ms", duration.Milliseconds(),
			"headers", flattenHeaders(resp.Header),
		)
	}
}

// handleConnect establishes a TCP tunnel for HTTPS CONNECT requests.
func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	// Check blocklist before establishing tunnel.
	if s.blocker != nil && s.blocker.IsBlocked(stripPort(r.Host)) {
		http.Error(w, "blocked by proxy", http.StatusForbidden)
		s.logger.Info("blocked",
			"method", "CONNECT",
			"host", r.Host,
			"remote", r.RemoteAddr,
		)
		return
	}

	start := time.Now()

	if s.verbose {
		s.logger.Debug("connect request",
			"host", r.Host,
			"remote", r.RemoteAddr,
			"user_agent", r.Header.Get("User-Agent"),
			"headers", flattenHeaders(r.Header),
		)
	}

	destConn, err := net.DialTimeout("tcp", r.Host, 10*time.Second)
	if err != nil {
		http.Error(w, fmt.Sprintf("tunnel error: %v", err), http.StatusBadGateway)
		s.logger.Error("connect tunnel failed",
			"host", r.Host,
			"error", err,
			"duration_ms", time.Since(start).Milliseconds(),
		)
		return
	}

	// Hijack the client connection to get the raw TCP socket.
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		_ = destConn.Close()
		return
	}

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, fmt.Sprintf("hijack error: %v", err), http.StatusInternalServerError)
		_ = destConn.Close()
		return
	}

	// Send 200 Connection Established to the client.
	_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")) //nolint:gosec // best-effort

	s.logger.Info("connect",
		"host", r.Host,
		"remote", r.RemoteAddr,
	)

	// Bidirectional copy — track bytes for verbose logging.
	var uploadBytes, downloadBytes atomic.Int64
	go func() {
		defer func() { _ = destConn.Close() }()
		defer func() { _ = clientConn.Close() }()
		n, _ := io.Copy(destConn, clientConn) //nolint:errcheck // tunnel streaming
		uploadBytes.Store(n)
	}()
	go func() {
		defer func() { _ = destConn.Close() }()
		defer func() { _ = clientConn.Close() }()
		n, _ := io.Copy(clientConn, destConn) //nolint:errcheck // tunnel streaming
		downloadBytes.Store(n)

		duration := time.Since(start)
		if s.verbose {
			s.logger.Debug("connect closed",
				"host", r.Host,
				"duration_ms", duration.Milliseconds(),
				"upload_bytes", uploadBytes.Load(),
				"download_bytes", downloadBytes.Load(),
			)
		} else {
			s.logger.Debug("connect closed",
				"host", r.Host,
				"duration_ms", duration.Milliseconds(),
			)
		}
	}()
}

// ListenAndServe starts the proxy server.
func (s *Server) ListenAndServe() error {
	s.logger.Info("proxy starting",
		"addr", s.httpServer.Addr,
	)
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully shuts down the proxy server.
func (s *Server) Shutdown(ctx context.Context) error {
	var err error
	s.shutdownOnce.Do(func() {
		s.logger.Info("proxy shutting down")
		err = s.httpServer.Shutdown(ctx)
	})
	return err
}

// ConnectionsTotal returns the total number of connections handled.
func (s *Server) ConnectionsTotal() int64 {
	return s.connectionsTotal.Load()
}

// ConnectionsActive returns the number of currently active connections.
func (s *Server) ConnectionsActive() int64 {
	return s.connectionsActive.Load()
}

// Uptime returns the duration since the server was created.
func (s *Server) Uptime() time.Duration {
	return time.Since(s.startTime)
}

// hopByHopHeaders are headers that apply to a single transport-level
// connection and must not be forwarded by proxies.
var hopByHopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"TE",
	"Trailers",
	"Transfer-Encoding",
	"Upgrade",
}

// removeHopByHopHeaders strips hop-by-hop headers from an HTTP header set.
func removeHopByHopHeaders(h http.Header) {
	for _, hdr := range hopByHopHeaders {
		h.Del(hdr)
	}
}

// flattenHeaders converts HTTP headers to a flat key=value slice for structured logging.
func flattenHeaders(h http.Header) []string {
	var out []string
	for k, vv := range h {
		for _, v := range vv {
			out = append(out, k+"="+v)
		}
	}
	return out
}

// stripPort removes the port from a host:port string.
// If there is no port, the host is returned as-is.
func stripPort(hostport string) string {
	if idx := strings.LastIndex(hostport, ":"); idx >= 0 {
		return hostport[:idx]
	}
	return hostport
}
