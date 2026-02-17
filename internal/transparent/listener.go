package transparent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Blocker checks whether a domain should be blocked.
type Blocker interface {
	IsBlocked(domain string) bool
}

// MITMInterceptor checks whether a domain should be MITM'd and handles
// the interception session.
type MITMInterceptor interface {
	IsMITMDomain(domain string) bool
	Handle(clientConn net.Conn, domain, host, clientIP string)
}

// Config holds transparent listener configuration.
type Config struct {
	HTTPAddr  string
	HTTPSAddr string
	Logger    *slog.Logger
	Verbose   bool

	Blocker         Blocker
	MITMInterceptor MITMInterceptor
	ConnectTimeout  time.Duration

	// Stats callbacks — same interface as the explicit proxy.
	OnRequest     func(clientIP, domain string, blocked bool, bytesIn, bytesOut int64)
	OnTunnelClose func(clientIP string, bytesIn, bytesOut int64)

	// Transparent-specific stats.
	OnTransparentHTTP  func()
	OnTransparentTLS   func()
	OnTransparentMITM  func()
	OnTransparentBlock func()
	OnSNIMissing       func()
}

// Listener manages transparent HTTP and HTTPS listeners.
type Listener struct {
	httpListener  net.Listener
	httpsListener net.Listener
	logger        *slog.Logger
	verbose       bool
	cfg           *Config

	wg sync.WaitGroup
}

// New creates a new transparent proxy Listener.
func New(cfg *Config) *Listener {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 10 * time.Second
	}
	return &Listener{
		logger:  cfg.Logger,
		verbose: cfg.Verbose,
		cfg:     cfg,
	}
}

// ListenAndServe starts the transparent HTTP and/or HTTPS listeners.
// Blocks until both listeners are closed.
func (l *Listener) ListenAndServe() error {
	var errs []error

	if l.cfg.HTTPAddr != "" {
		ln, err := net.Listen("tcp", l.cfg.HTTPAddr)
		if err != nil {
			return fmt.Errorf("transparent http listen: %w", err)
		}
		l.httpListener = ln
		l.logger.Info("transparent http listener started", "addr", l.cfg.HTTPAddr)

		l.wg.Add(1)
		go func() {
			defer l.wg.Done()
			l.acceptHTTP(ln)
		}()
	}

	if l.cfg.HTTPSAddr != "" {
		ln, err := net.Listen("tcp", l.cfg.HTTPSAddr)
		if err != nil {
			if l.httpListener != nil {
				_ = l.httpListener.Close()
			}
			return fmt.Errorf("transparent https listen: %w", err)
		}
		l.httpsListener = ln
		l.logger.Info("transparent https listener started", "addr", l.cfg.HTTPSAddr)

		l.wg.Add(1)
		go func() {
			defer l.wg.Done()
			l.acceptHTTPS(ln)
		}()
	}

	l.wg.Wait()

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// Shutdown gracefully stops both transparent listeners.
func (l *Listener) Shutdown(_ context.Context) {
	if l.httpListener != nil {
		_ = l.httpListener.Close()
	}
	if l.httpsListener != nil {
		_ = l.httpsListener.Close()
	}
}

// acceptHTTP accepts connections on the transparent HTTP listener.
func (l *Listener) acceptHTTP(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				l.logger.Error("transparent http accept", "error", err)
			}
			return
		}
		go l.handleHTTP(conn)
	}
}

// acceptHTTPS accepts connections on the transparent HTTPS listener.
func (l *Listener) acceptHTTPS(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				l.logger.Error("transparent https accept", "error", err)
			}
			return
		}
		go l.handleHTTPS(conn)
	}
}

// handleHTTP handles a transparent HTTP connection.
func (l *Listener) handleHTTP(conn net.Conn) {
	defer conn.Close() //nolint:errcheck // best-effort close

	clientIP := stripPort(conn.RemoteAddr().String())

	// Read the HTTP request. In transparent mode, it arrives with a relative
	// URI (e.g., GET /path HTTP/1.1) and a Host header.
	req, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		l.logger.Debug("transparent http read request failed", "remote", clientIP, "error", err)
		return
	}

	// Determine destination from Host header.
	host := req.Host
	if host == "" {
		// Fallback to SO_ORIGINAL_DST.
		origAddr, origErr := getOriginalDst(conn)
		if origErr != nil {
			l.logger.Warn("transparent http: no Host header and SO_ORIGINAL_DST failed",
				"remote", clientIP, "error", origErr)
			return
		}
		host = origAddr.String()
	}

	domain := stripPort(host)

	// Blocklist check.
	if l.cfg.Blocker != nil && l.cfg.Blocker.IsBlocked(domain) {
		writeHTTPError(conn, http.StatusForbidden, "blocked by proxy")
		l.logger.Info("transparent blocked", "domain", domain, "remote", clientIP, "proto", "http")
		if l.cfg.OnRequest != nil {
			l.cfg.OnRequest(clientIP, domain, true, 0, 0)
		}
		if l.cfg.OnTransparentBlock != nil {
			l.cfg.OnTransparentBlock()
		}
		return
	}

	if l.cfg.OnTransparentHTTP != nil {
		l.cfg.OnTransparentHTTP()
	}

	// Determine upstream address. Use original port 80 by default.
	upstream := host
	if !strings.Contains(upstream, ":") {
		upstream += ":80"
	}

	// Dial upstream.
	upConn, err := net.DialTimeout("tcp", upstream, l.cfg.ConnectTimeout)
	if err != nil {
		writeHTTPError(conn, http.StatusBadGateway, "upstream connection failed")
		l.logger.Error("transparent http dial failed",
			"domain", domain, "upstream", upstream, "remote", clientIP, "error", err)
		return
	}
	defer upConn.Close() //nolint:errcheck // best-effort close

	// Forward the request.
	removeHopByHopHeaders(req.Header)
	if writeErr := req.Write(upConn); writeErr != nil {
		l.logger.Error("transparent http request write failed",
			"domain", domain, "remote", clientIP, "error", writeErr)
		return
	}

	// Read response.
	resp, err := http.ReadResponse(bufio.NewReader(upConn), req)
	if err != nil {
		l.logger.Error("transparent http response read failed",
			"domain", domain, "remote", clientIP, "error", err)
		return
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close

	removeHopByHopHeaders(resp.Header)

	// Write response to client.
	if writeErr := resp.Write(conn); writeErr != nil {
		l.logger.Debug("transparent http response write failed",
			"domain", domain, "remote", clientIP, "error", writeErr)
	}

	var respSize int64
	if resp.ContentLength > 0 {
		respSize = resp.ContentLength
	}
	var reqSize int64
	if req.ContentLength > 0 {
		reqSize = req.ContentLength
	}

	if l.cfg.OnRequest != nil {
		l.cfg.OnRequest(clientIP, domain, false, reqSize, respSize)
	}

	l.logger.Info("transparent http",
		"domain", domain,
		"method", req.Method,
		"url", req.URL.String(),
		"status", resp.StatusCode,
		"remote", clientIP,
	)
}

// handleHTTPS handles a transparent HTTPS connection.
func (l *Listener) handleHTTPS(conn net.Conn) {
	defer conn.Close() //nolint:errcheck // best-effort close

	clientIP := stripPort(conn.RemoteAddr().String())

	// Peek at the TLS ClientHello to extract SNI.
	serverName, peeked, err := peekClientHello(conn)
	if err != nil && !errors.Is(err, errNoSNI) {
		l.logger.Debug("transparent https: SNI parse failed",
			"remote", clientIP, "error", err)
	}

	if l.verbose && serverName != "" {
		l.logger.Debug("sni extracted", "domain", serverName, "bytes_read", len(peeked))
	}

	// Determine destination.
	var domain string
	var upstreamHost string

	if serverName != "" {
		domain = serverName
		upstreamHost = serverName + ":443"
	} else {
		// Fallback to SO_ORIGINAL_DST.
		if l.cfg.OnSNIMissing != nil {
			l.cfg.OnSNIMissing()
		}
		origAddr, origErr := getOriginalDst(conn)
		if origErr != nil {
			l.logger.Warn("transparent https: no SNI and SO_ORIGINAL_DST failed",
				"remote", clientIP, "error", origErr)
			return
		}
		upstreamHost = origAddr.String()
		domain = stripPort(upstreamHost)
		l.logger.Debug("sni missing, falling back to original destination",
			"remote", clientIP, "origdst", upstreamHost)
	}

	// Blocklist check.
	if l.cfg.Blocker != nil && l.cfg.Blocker.IsBlocked(domain) {
		// No HTTP layer — just close the connection.
		l.logger.Info("transparent blocked", "domain", domain, "remote", clientIP, "proto", "https")
		if l.cfg.OnRequest != nil {
			l.cfg.OnRequest(clientIP, domain, true, 0, 0)
		}
		if l.cfg.OnTransparentBlock != nil {
			l.cfg.OnTransparentBlock()
		}
		return
	}

	// MITM interception.
	if serverName != "" && l.cfg.MITMInterceptor != nil && l.cfg.MITMInterceptor.IsMITMDomain(domain) {
		if l.cfg.OnTransparentMITM != nil {
			l.cfg.OnTransparentMITM()
		}
		l.logger.Info("transparent mitm", "domain", domain, "remote", clientIP)

		// Wrap conn to replay the peeked ClientHello bytes.
		wrapped := newPrefixConn(conn, peeked)

		// Delegate to the MITM handler. It takes ownership and closes the conn.
		// We must not close conn ourselves after this (defer close is harmless
		// on an already-closed conn).
		l.cfg.MITMInterceptor.Handle(wrapped, domain, upstreamHost, clientIP)
		return
	}

	// Tunnel: connect upstream and relay bytes.
	if l.cfg.OnTransparentTLS != nil {
		l.cfg.OnTransparentTLS()
	}

	upConn, err := net.DialTimeout("tcp", upstreamHost, l.cfg.ConnectTimeout)
	if err != nil {
		l.logger.Error("transparent tunnel dial failed",
			"domain", domain, "upstream", upstreamHost, "remote", clientIP, "error", err)
		return
	}
	defer upConn.Close() //nolint:errcheck // best-effort close

	// Replay peeked ClientHello to upstream.
	if _, err := upConn.Write(peeked); err != nil {
		l.logger.Error("transparent tunnel replay failed",
			"domain", domain, "remote", clientIP, "error", err)
		return
	}

	if l.cfg.OnRequest != nil {
		l.cfg.OnRequest(clientIP, domain, false, 0, 0)
	}

	l.logger.Info("transparent tunnel", "domain", domain, "remote", clientIP)

	// Bidirectional byte copy.
	var uploadBytes, downloadBytes atomic.Int64
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		n, _ := io.Copy(upConn, conn) //nolint:errcheck // tunnel streaming
		uploadBytes.Store(n)
		// Signal upstream we're done sending.
		if tc, ok := upConn.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}()

	go func() {
		defer wg.Done()
		n, _ := io.Copy(conn, upConn) //nolint:errcheck // tunnel streaming
		downloadBytes.Store(n)
		if tc, ok := conn.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}()

	wg.Wait()

	if l.cfg.OnTunnelClose != nil {
		l.cfg.OnTunnelClose(clientIP, uploadBytes.Load(), downloadBytes.Load())
	}

	if l.verbose {
		l.logger.Debug("transparent tunnel closed",
			"domain", domain,
			"upload_bytes", uploadBytes.Load(),
			"download_bytes", downloadBytes.Load(),
		)
	}
}

// writeHTTPError writes a simple HTTP error response to a raw connection.
func writeHTTPError(conn net.Conn, statusCode int, msg string) {
	resp := fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		statusCode, http.StatusText(statusCode), len(msg), msg)
	_, _ = conn.Write([]byte(resp)) //nolint:gosec // best-effort error response
}

// hopByHopHeaders are headers that must not be forwarded by proxies.
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

// stripPort removes the port from a host:port string.
func stripPort(hostport string) string {
	if idx := strings.LastIndex(hostport, ":"); idx >= 0 {
		return hostport[:idx]
	}
	return hostport
}

