package mitm

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Interceptor handles MITM TLS interception for configured domains.
type Interceptor struct {
	certCache      *CertCache
	domains        map[string]struct{}
	logger         *slog.Logger
	verbose        bool
	connectTimeout time.Duration

	// OnMITMRequest is called for each HTTP request-response cycle through
	// a MITM session. Parameters: clientIP, domain.
	OnMITMRequest func(clientIP, domain string)

	// InterceptsTotal tracks the total number of MITM'd HTTP requests.
	InterceptsTotal atomic.Int64

	// ResponseModifier is called for each MITM'd response if non-nil.
	// When nil (default), all responses stream through without buffering.
	ResponseModifier ResponseModifier
}

// ResponseModifier may inspect or modify an HTTP response body during MITM.
// It is called only for text-based Content-Types (text/*, application/json,
// application/javascript). Binary responses stream through unmodified.
//
// If nil, all responses stream through without buffering.
type ResponseModifier func(domain string, req *http.Request, resp *http.Response, body []byte) ([]byte, error)

// InterceptorConfig holds configuration for creating an Interceptor.
type InterceptorConfig struct {
	CA             *CA
	Domains        []string
	Logger         *slog.Logger
	Verbose        bool
	ConnectTimeout time.Duration
	OnMITMRequest  func(clientIP, domain string)
}

// NewInterceptor creates a MITM interceptor for the given domains.
func NewInterceptor(cfg *InterceptorConfig) *Interceptor {
	domains := make(map[string]struct{}, len(cfg.Domains))
	for _, d := range cfg.Domains {
		domains[strings.ToLower(d)] = struct{}{}
	}

	return &Interceptor{
		certCache:      NewCertCache(cfg.CA),
		domains:        domains,
		logger:         cfg.Logger,
		verbose:        cfg.Verbose,
		connectTimeout: cfg.ConnectTimeout,
		OnMITMRequest:  cfg.OnMITMRequest,
	}
}

// IsMITMDomain returns true if the domain is configured for MITM interception.
func (i *Interceptor) IsMITMDomain(domain string) bool {
	_, ok := i.domains[strings.ToLower(domain)]
	return ok
}

// Domains returns the number of configured MITM domains.
func (i *Interceptor) Domains() int {
	return len(i.domains)
}

// Handle runs a MITM session on an already-hijacked client connection.
// It terminates TLS with the client using a generated certificate, connects
// to the upstream server, and proxies HTTP request-response cycles between them.
//
// This method takes ownership of clientConn and closes it when done.
// host is the original CONNECT target (e.g., "www.reddit.com:443").
func (i *Interceptor) Handle(clientConn net.Conn, domain, host, clientIP string) {
	defer func() { _ = clientConn.Close() }()

	start := time.Now()
	i.logger.Info("mitm session start",
		"domain", domain,
		"client", clientIP,
	)

	// Generate or retrieve a cached leaf certificate for this domain.
	leafCert, certErr := i.certCache.GetCert(domain)
	if certErr != nil {
		i.logger.Error("mitm leaf cert generation failed",
			"domain", domain,
			"client", clientIP,
			"error", certErr,
		)
		return
	}

	// TLS handshake with the client (proxy acts as the domain).
	clientTLSConfig := &tls.Config{
		Certificates: []tls.Certificate{*leafCert},
		MinVersion:   tls.VersionTLS12,
	}
	clientTLS := tls.Server(clientConn, clientTLSConfig)
	clientHSCtx, clientHSCancel := timeoutCtx(5 * time.Second)
	defer clientHSCancel()
	if hsErr := clientTLS.HandshakeContext(clientHSCtx); hsErr != nil {
		i.logger.Warn("mitm client TLS handshake failed",
			"domain", domain,
			"client", clientIP,
			"error", hsErr,
		)
		return
	}
	defer func() { _ = clientTLS.Close() }()

	// Connect to the real upstream server.
	upstreamConn, dialErr := net.DialTimeout("tcp", host, i.connectTimeout)
	if dialErr != nil {
		i.logger.Error("mitm upstream dial failed",
			"domain", domain,
			"client", clientIP,
			"upstream", host,
			"timeout", i.connectTimeout,
			"error", dialErr,
		)
		return
	}
	defer func() { _ = upstreamConn.Close() }()

	// TLS handshake with the upstream server (proxy acts as a client).
	upstreamTLSConfig := &tls.Config{
		ServerName: domain,
		NextProtos: []string{"http/1.1"},
		MinVersion: tls.VersionTLS12,
	}
	upstreamTLS := tls.Client(upstreamConn, upstreamTLSConfig)
	upHSCtx, upHSCancel := timeoutCtx(5 * time.Second)
	defer upHSCancel()
	if err := upstreamTLS.HandshakeContext(upHSCtx); err != nil {
		i.logger.Error("mitm upstream TLS handshake failed",
			"domain", domain,
			"client", clientIP,
			"error", err,
		)
		return
	}
	defer func() { _ = upstreamTLS.Close() }()

	// HTTP proxy loop.
	requests := i.proxyLoop(clientTLS, upstreamTLS, domain, clientIP)

	duration := time.Since(start)
	i.logger.Info("mitm session end",
		"domain", domain,
		"client", clientIP,
		"requests", requests,
		"duration_ms", duration.Milliseconds(),
	)
}

// proxyLoop reads HTTP requests from the client and forwards them to the
// upstream server, then reads responses and forwards them back. Returns
// the number of request-response cycles completed.
func (i *Interceptor) proxyLoop(clientTLS, upstreamTLS *tls.Conn, domain, clientIP string) int {
	clientReader := bufio.NewReader(clientTLS)
	upstreamReader := bufio.NewReader(upstreamTLS)
	requests := 0

	for {
		// Read request from client.
		req, err := http.ReadRequest(clientReader)
		if err != nil {
			if err != io.EOF && !isClosedConnErr(err) {
				i.logger.Debug("mitm client request read failed",
					"domain", domain,
					"client", clientIP,
					"error", err,
					"requests_completed", requests,
				)
			}
			break
		}

		reqStart := time.Now()

		// Strip hop-by-hop headers from client request.
		removeHopByHopHeaders(req.Header)

		// Ensure Host header is set correctly.
		if req.Host == "" {
			req.Host = domain
		}

		// Forward request to upstream.
		if writeErr := req.Write(upstreamTLS); writeErr != nil {
			i.logger.Error("mitm upstream request write failed",
				"domain", domain,
				"client", clientIP,
				"method", req.Method,
				"url", req.URL.String(),
				"error", writeErr,
			)
			break
		}

		// Read response from upstream.
		resp, err := http.ReadResponse(upstreamReader, req)
		if err != nil {
			i.logger.Error("mitm upstream response read failed",
				"domain", domain,
				"client", clientIP,
				"method", req.Method,
				"url", req.URL.String(),
				"error", err,
			)
			break
		}

		// Strip hop-by-hop headers from upstream response.
		removeHopByHopHeaders(resp.Header)

		// If ResponseModifier is set and content is text-based, buffer and modify.
		if i.ResponseModifier != nil && isTextContent(resp.Header.Get("Content-Type")) {
			body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBufferSize+1))
			_ = resp.Body.Close()

			if readErr != nil {
				i.logger.Error("mitm response body read failed",
					"domain", domain,
					"url", req.URL.String(),
					"error", readErr,
				)
				break
			}

			// Only modify if within size limit.
			if int64(len(body)) <= maxBufferSize {
				modified, modErr := i.ResponseModifier(domain, req, resp, body)
				if modErr != nil {
					i.logger.Error("mitm response modifier failed",
						"domain", domain,
						"url", req.URL.String(),
						"error", modErr,
					)
					break
				}
				body = modified
			}

			// Write modified response with updated Content-Length.
			resp.Body = io.NopCloser(bytes.NewReader(body))
			resp.ContentLength = int64(len(body))
			resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
			resp.Header.Del("Transfer-Encoding")

			if writeErr := resp.Write(clientTLS); writeErr != nil {
				if !isClosedConnErr(writeErr) {
					i.logger.Warn("mitm client response write failed",
						"domain", domain,
						"client", clientIP,
						"method", req.Method,
						"url", req.URL.String(),
						"error", writeErr,
					)
				}
				break
			}
		} else {
			// Stream through unmodified (binary content or no modifier).
			if writeErr := resp.Write(clientTLS); writeErr != nil {
				_ = resp.Body.Close()
				if !isClosedConnErr(writeErr) {
					i.logger.Warn("mitm client response write failed",
						"domain", domain,
						"client", clientIP,
						"method", req.Method,
						"url", req.URL.String(),
						"error", writeErr,
					)
				}
				break
			}
			_ = resp.Body.Close()
		}

		requests++
		i.InterceptsTotal.Add(1)
		if i.OnMITMRequest != nil {
			i.OnMITMRequest(clientIP, domain)
		}

		if i.verbose {
			i.logger.Debug("mitm request",
				"domain", domain,
				"method", req.Method,
				"url", req.URL.String(),
				"status", resp.StatusCode,
				"content_type", resp.Header.Get("Content-Type"),
				"content_length", resp.ContentLength,
				"duration_ms", time.Since(reqStart).Milliseconds(),
			)
		}

		// Check if either side wants to close the connection.
		if resp.Close || req.Close {
			break
		}
	}

	return requests
}

// hopByHopHeaders are headers that apply to a single transport-level
// connection and must not be forwarded by proxies.
var hopByHopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Proxy-Connection",
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

// timeoutCtx returns a context with the given timeout and its cancel function.
// The caller should defer cancel() to release resources promptly.
func timeoutCtx(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// maxBufferSize is the maximum response body size that will be buffered
// for plugin inspection. Responses larger than this stream through unmodified.
const maxBufferSize = 10 * 1024 * 1024 // 10MB

// isTextContent returns true if the Content-Type is text-based and should
// be buffered for plugin inspection.
func isTextContent(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if idx := strings.IndexByte(ct, ';'); idx >= 0 {
		ct = strings.TrimSpace(ct[:idx])
	}
	if strings.HasPrefix(ct, "text/") {
		return true
	}
	switch ct {
	case "application/json", "application/javascript", "application/xml":
		return true
	}
	return false
}

// isClosedConnErr returns true if the error indicates a closed connection,
// which is expected behavior (client navigated away, tab closed, etc.).
func isClosedConnErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "use of closed network connection") ||
		strings.Contains(s, "connection reset by peer") ||
		strings.Contains(s, "broken pipe")
}
