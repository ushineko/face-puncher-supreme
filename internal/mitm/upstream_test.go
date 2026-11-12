package mitm

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// isGREASE reports whether v is a GREASE value (RFC 8701): 0x?a?a with
// both bytes equal.
func isGREASE(v uint16) bool {
	return v&0x0f0f == 0x0a0a && v>>8 == v&0xff
}

// startHelloRecorder starts a loopback TLS server that records the
// ClientHello of each connection and answers HTTP requests with "ok".
func startHelloRecorder(t *testing.T) (srv *httptest.Server, lastHello func() *tls.ClientHelloInfo) {
	t.Helper()
	var (
		mu    sync.Mutex
		hello *tls.ClientHelloInfo
	)
	srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "ok")
	}))
	srv.TLS = &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetConfigForClient: func(chi *tls.ClientHelloInfo) (*tls.Config, error) {
			mu.Lock()
			hello = chi
			mu.Unlock()
			return nil, nil
		},
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, func() *tls.ClientHelloInfo {
		mu.Lock()
		defer mu.Unlock()
		return hello
	}
}

func serverRoots(srv *httptest.Server) *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return pool
}

func TestNewUpstreamTLS_ClientHelloShape(t *testing.T) {
	tests := []struct {
		fingerprint string
		wantGREASE  bool
	}{
		{UpstreamTLSSafari, true},
		{UpstreamTLSGo, false},
	}

	for _, tt := range tests {
		t.Run(tt.fingerprint, func(t *testing.T) {
			srv, lastHello := startHelloRecorder(t)

			conn, err := net.Dial("tcp", srv.Listener.Addr().String())
			require.NoError(t, err)
			defer conn.Close() //nolint:errcheck // test cleanup

			up, err := newUpstreamTLS(conn, "example.com", tt.fingerprint, serverRoots(srv))
			require.NoError(t, err)
			ctx, cancel := timeoutCtx(5 * time.Second)
			defer cancel()
			require.NoError(t, up.HandshakeContext(ctx))

			hello := lastHello()
			require.NotNil(t, hello)
			assert.Equal(t, []string{"http/1.1"}, hello.SupportedProtos)

			hasGREASE := false
			for _, cs := range hello.CipherSuites {
				if isGREASE(cs) {
					hasGREASE = true
				}
			}
			assert.Equal(t, tt.wantGREASE, hasGREASE, "GREASE cipher suite presence")
		})
	}
}

func TestNewUpstreamTLS_UnknownFingerprint(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close() //nolint:errcheck // test cleanup
	defer server.Close() //nolint:errcheck // test cleanup

	_, err := newUpstreamTLS(client, "example.com", "chrome", nil)
	assert.Error(t, err)
}

// handleResult is what the client side of a runHandle session observed.
type handleResult struct {
	body string
	err  error // request write or response read error
	logs string
}

// runHandle runs Interceptor.Handle for domain example.com against the
// upstream server, sends one GET through the MITM tunnel, and returns the
// response body (or the I/O error) plus the captured log output.
func runHandle(t *testing.T, srv *httptest.Server, fingerprint string, roots *x509.CertPool) handleResult {
	t.Helper()
	ca := generateTestCA(t)
	var logBuf bytes.Buffer
	var logMu sync.Mutex
	logger := slog.New(slog.NewTextHandler(&lockedWriter{w: &logBuf, mu: &logMu}, nil))

	i := NewInterceptor(&InterceptorConfig{
		CA:             ca,
		Domains:        []string{"example.com"},
		Logger:         logger,
		ConnectTimeout: 5 * time.Second,
		UpstreamTLS:    fingerprint,
	})
	i.upstreamRootCAs = roots

	// Loopback TCP rather than net.Pipe: a pipe is unbuffered, so TLS
	// close_notify writes stall until their 5s deadline.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close() //nolint:errcheck // test cleanup
	done := make(chan struct{})
	go func() {
		defer close(done)
		proxySide, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		i.Handle(proxySide, "example.com", srv.Listener.Addr().String(), "127.0.0.1")
	}()
	clientSide, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)

	caPool := x509.NewCertPool()
	caPool.AddCert(ca.Cert)
	clientTLS := tls.Client(clientSide, &tls.Config{
		RootCAs:    caPool,
		ServerName: "example.com",
		MinVersion: tls.VersionTLS12,
	})
	require.NoError(t, clientTLS.Handshake())

	req, _ := http.NewRequest(http.MethodGet, "http://example.com/", http.NoBody)
	req.Close = true
	var body string
	var resp *http.Response
	readErr := req.Write(clientTLS)
	if readErr == nil {
		resp, readErr = http.ReadResponse(bufio.NewReader(clientTLS), req)
	}
	if readErr == nil {
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		body = string(b)
	}
	_ = clientTLS.Close()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Handle did not return")
	}

	logMu.Lock()
	defer logMu.Unlock()
	return handleResult{body: body, err: readErr, logs: logBuf.String()}
}

func TestInterceptor_HandleUpstreamFingerprints(t *testing.T) {
	for _, fp := range []string{UpstreamTLSSafari, UpstreamTLSGo} {
		t.Run(fp, func(t *testing.T) {
			srv, lastHello := startHelloRecorder(t)

			res := runHandle(t, srv, fp, serverRoots(srv))
			require.NoError(t, res.err)
			assert.Equal(t, "ok", res.body)
			require.NotNil(t, lastHello())
			assert.Equal(t, "example.com", lastHello().ServerName)
		})
	}
}

func TestInterceptor_HandleSafariVerifiesUpstreamCert(t *testing.T) {
	srv, _ := startHelloRecorder(t)

	// Empty root pool: the upstream's self-signed cert must not verify.
	res := runHandle(t, srv, UpstreamTLSSafari, x509.NewCertPool())
	assert.Error(t, res.err, "no response may be proxied when upstream verification fails")
	assert.Contains(t, res.logs, "mitm upstream TLS handshake failed")
	assert.Contains(t, res.logs, "upstream_tls=safari")
}

func TestNewInterceptor_UpstreamTLSDefault(t *testing.T) {
	i := NewInterceptor(&InterceptorConfig{CA: generateTestCA(t), Logger: slog.Default()})
	assert.Equal(t, UpstreamTLSSafari, i.UpstreamTLS())
}

// lockedWriter serializes writes from the Handle goroutine and the test.
type lockedWriter struct {
	w  io.Writer
	mu *sync.Mutex
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
