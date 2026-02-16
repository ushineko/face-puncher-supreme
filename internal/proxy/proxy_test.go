package proxy_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ushineko/face-puncher-supreme/internal/probe"
	"github.com/ushineko/face-puncher-supreme/internal/proxy"
	"github.com/ushineko/face-puncher-supreme/internal/stats"
)

// _startTestProxy starts a proxy server on a random port and returns
// its URL and a cleanup function.
func _startTestProxy(t *testing.T) (proxyURL string, cleanup func()) {
	t.Helper()

	// Find a free port.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	_ = listener.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	collector := stats.NewCollector()
	srv := proxy.New(&proxy.Config{
		ListenAddr:       addr,
		Logger:           logger,
		HeartbeatHandler: http.NotFound,
		StatsHandler:     http.NotFound,
		OnRequest:        collector.RecordRequest,
		OnTunnelClose:    collector.RecordBytes,
	})
	// Set real handlers now that srv exists.
	srv.SetHandlers(
		probe.HeartbeatHandler(srv, nil, nil),
		probe.StatsHandler(&probe.StatsProvider{
			Info:      srv,
			Collector: collector,
		}),
	)

	go func() { _ = srv.ListenAndServe() }()

	// Wait for the server to be ready.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, dialErr := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	return "http://" + addr, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}

// _proxyClient returns an http.Client configured to use the given proxy.
func _proxyClient(proxyURL string) *http.Client {
	pURL, _ := url.Parse(proxyURL) //nolint:errcheck // test helper, URL always valid
	return &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(pURL),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test client
		},
		Timeout: 10 * time.Second,
	}
}

func TestHeartbeatEndpoint(t *testing.T) {
	proxyURL, cleanup := _startTestProxy(t)
	defer cleanup()

	resp, err := http.Get(proxyURL + "/fps/heartbeat")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var hbResp probe.HeartbeatResponse
	err = json.NewDecoder(resp.Body).Decode(&hbResp)
	require.NoError(t, err)

	assert.Equal(t, "ok", hbResp.Status)
	assert.Equal(t, "face-puncher-supreme", hbResp.Service)
	assert.Equal(t, "passthrough", hbResp.Mode)
	assert.NotEmpty(t, hbResp.Version)
	assert.NotEmpty(t, hbResp.OS)
	assert.NotEmpty(t, hbResp.Arch)
	assert.NotEmpty(t, hbResp.GoVersion)
	assert.NotEmpty(t, hbResp.StartedAt)
}

func TestHeartbeatEndpointViaProxy(t *testing.T) {
	proxyURL, cleanup := _startTestProxy(t)
	defer cleanup()

	// When a client is configured to use the proxy, a request to the proxy's
	// own host on /fps/heartbeat should still work.
	client := _proxyClient(proxyURL)
	resp, err := client.Get(proxyURL + "/fps/heartbeat")
	require.NoError(t, err)
	defer resp.Body.Close()

	var hbResp probe.HeartbeatResponse
	err = json.NewDecoder(resp.Body).Decode(&hbResp)
	require.NoError(t, err)
	assert.Equal(t, "ok", hbResp.Status)
}

func TestManagementUnknownPath(t *testing.T) {
	proxyURL, cleanup := _startTestProxy(t)
	defer cleanup()

	resp, err := http.Get(proxyURL + "/fps/nonexistent")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestHTTPForwardProxy(t *testing.T) {
	// Create a test upstream server.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "hello from upstream")
	}))
	defer upstream.Close()

	proxyURL, cleanup := _startTestProxy(t)
	defer cleanup()

	client := _proxyClient(proxyURL)
	resp, err := client.Get(upstream.URL)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "hello from upstream", string(body))
}

func TestHTTPForwardProxyPreservesHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom-Header", "preserved")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprint(w, `{"ok": true}`)
	}))
	defer upstream.Close()

	proxyURL, cleanup := _startTestProxy(t)
	defer cleanup()

	client := _proxyClient(proxyURL)
	resp, err := client.Get(upstream.URL)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, "preserved", resp.Header.Get("X-Custom-Header"))
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
}

func TestHTTPForwardProxyStripsHopByHopHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Hop-by-hop headers from client should be stripped.
		assert.Empty(t, r.Header.Get("Proxy-Authorization"),
			"proxy-authorization should be stripped before forwarding")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Real-Header", "kept")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	proxyURL, cleanup := _startTestProxy(t)
	defer cleanup()

	client := _proxyClient(proxyURL)
	req, err := http.NewRequest(http.MethodGet, upstream.URL, http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Proxy-Authorization", "Basic secret")

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, "kept", resp.Header.Get("X-Real-Header"))
}

func TestHTTPSConnectTunnel(t *testing.T) {
	// Create an HTTPS test server.
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "hello from tls upstream")
	}))
	defer upstream.Close()

	proxyURL, cleanup := _startTestProxy(t)
	defer cleanup()

	client := _proxyClient(proxyURL)
	// Override the TLS config to trust the test server's cert.
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	upstreamTransport, ok := upstream.Client().Transport.(*http.Transport)
	require.True(t, ok)
	transport.TLSClientConfig = upstreamTransport.TLSClientConfig

	resp, err := client.Get(upstream.URL)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "hello from tls upstream", string(body))
}

func TestConcurrentConnections(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()

	proxyURL, cleanup := _startTestProxy(t)
	defer cleanup()

	const numClients = 20
	var wg sync.WaitGroup
	errors := make(chan error, numClients)

	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := _proxyClient(proxyURL)
			resp, err := client.Get(upstream.URL)
			if err != nil {
				errors <- err
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				errors <- fmt.Errorf("unexpected status: %d", resp.StatusCode)
			}
		}()
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent request failed: %v", err)
	}
}

func TestMalformedRequest(t *testing.T) {
	proxyURL, cleanup := _startTestProxy(t)
	defer cleanup()

	// A GET request without a host (direct to proxy, not a proxy request
	// and not a management endpoint) should return 400.
	req, err := http.NewRequest(http.MethodGet, proxyURL+"/some/path", http.NoBody)
	require.NoError(t, err)
	// Clear the host to simulate a malformed proxy request.
	// When sending directly, the URL host is set to the proxy, which makes
	// this a non-proxy request. The proxy should handle it gracefully.
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// The proxy sees this as a direct request with URL path /some/path
	// and no absolute URL host — should return bad request.
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestStatsConnectionCounters(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	proxyURL, cleanup := _startTestProxy(t)
	defer cleanup()

	// Make a few requests through the proxy.
	client := _proxyClient(proxyURL)
	for i := 0; i < 5; i++ {
		resp, err := client.Get(upstream.URL)
		require.NoError(t, err)
		_ = resp.Body.Close()
	}

	// Check stats counters.
	resp, err := http.Get(proxyURL + "/fps/stats")
	require.NoError(t, err)
	defer resp.Body.Close()

	var statsResp probe.StatsResponse
	err = json.NewDecoder(resp.Body).Decode(&statsResp)
	require.NoError(t, err)

	// connections.total should be > 5 (5 proxied requests + this stats request + possibly earlier requests).
	assert.GreaterOrEqual(t, statsResp.Connections.Total, int64(5),
		"should have counted at least the 5 proxied requests")
	assert.GreaterOrEqual(t, statsResp.Traffic.TotalRequests, int64(5),
		"should have recorded at least the 5 proxied requests in traffic")
}

func TestLargeResponse(t *testing.T) {
	// Generate a 1MB response.
	largeBody := strings.Repeat("x", 1024*1024)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, largeBody)
	}))
	defer upstream.Close()

	proxyURL, cleanup := _startTestProxy(t)
	defer cleanup()

	client := _proxyClient(proxyURL)
	resp, err := client.Get(upstream.URL)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Len(t, body, 1024*1024, "full 1MB body should be relayed")
}

// _mockBlocker is a simple blocker for testing that blocks a fixed set of domains.
type _mockBlocker struct {
	blocked map[string]bool
}

func (m *_mockBlocker) IsBlocked(domain string) bool {
	return m.blocked[strings.ToLower(domain)]
}

// _startTestProxyWithBlocker starts a proxy with a blocker configured.
func _startTestProxyWithBlocker(t *testing.T, blocker proxy.Blocker) (proxyURL string, cleanup func()) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	_ = listener.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	collector := stats.NewCollector()
	srv := proxy.New(&proxy.Config{
		ListenAddr:       addr,
		Logger:           logger,
		Blocker:          blocker,
		HeartbeatHandler: http.NotFound,
		StatsHandler:     http.NotFound,
		OnRequest:        collector.RecordRequest,
		OnTunnelClose:    collector.RecordBytes,
	})
	srv.SetHandlers(
		probe.HeartbeatHandler(srv, nil, nil),
		probe.StatsHandler(&probe.StatsProvider{
			Info:      srv,
			Collector: collector,
		}),
	)

	go func() { _ = srv.ListenAndServe() }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, dialErr := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	return "http://" + addr, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}

func TestHTTPBlockedDomain(t *testing.T) {
	// Create a test upstream that should never be reached.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream should not be reached for blocked domains")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	// Parse upstream URL to get the host.
	upstreamURL, err := url.Parse(upstream.URL)
	require.NoError(t, err)

	blocker := &_mockBlocker{blocked: map[string]bool{
		upstreamURL.Hostname(): true,
	}}

	proxyURL, cleanup := _startTestProxyWithBlocker(t, blocker)
	defer cleanup()

	client := _proxyClient(proxyURL)
	resp, err := client.Get(upstream.URL)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestHTTPAllowedDomain(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "allowed")
	}))
	defer upstream.Close()

	// Blocker that blocks a different domain, not the upstream.
	blocker := &_mockBlocker{blocked: map[string]bool{
		"blocked.example.com": true,
	}}

	proxyURL, cleanup := _startTestProxyWithBlocker(t, blocker)
	defer cleanup()

	client := _proxyClient(proxyURL)
	resp, err := client.Get(upstream.URL)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "allowed", string(body))
}

func TestCONNECTBlockedDomain(t *testing.T) {
	// Create an HTTPS upstream that should never be reached.
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream should not be reached for blocked domains")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	upstreamURL, err := url.Parse(upstream.URL)
	require.NoError(t, err)

	blocker := &_mockBlocker{blocked: map[string]bool{
		upstreamURL.Hostname(): true,
	}}

	proxyURL, cleanup := _startTestProxyWithBlocker(t, blocker)
	defer cleanup()

	client := _proxyClient(proxyURL)
	// Override TLS config to trust test server cert.
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	upstreamTransport, ok := upstream.Client().Transport.(*http.Transport)
	require.True(t, ok)
	transport.TLSClientConfig = upstreamTransport.TLSClientConfig

	// CONNECT to a blocked domain should fail. The HTTP client will get an error
	// because the proxy returns 403 instead of establishing the tunnel.
	_, err = client.Get(upstream.URL)
	assert.Error(t, err, "CONNECT to blocked domain should fail")
}

func TestHeartbeatShowsPassthroughWithNoBlocker(t *testing.T) {
	proxyURL, cleanup := _startTestProxy(t)
	defer cleanup()

	resp, err := http.Get(proxyURL + "/fps/heartbeat")
	require.NoError(t, err)
	defer resp.Body.Close()

	var hbResp probe.HeartbeatResponse
	err = json.NewDecoder(resp.Body).Decode(&hbResp)
	require.NoError(t, err)

	assert.Equal(t, "passthrough", hbResp.Mode)
}

func TestGracefulShutdown(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	_ = listener.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := proxy.New(&proxy.Config{
		ListenAddr:       addr,
		Logger:           logger,
		HeartbeatHandler: http.NotFound,
		StatsHandler:     http.NotFound,
	})

	done := make(chan error, 1)
	go func() {
		done <- srv.ListenAndServe()
	}()

	// Wait for server to start.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, dialErr := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = srv.Shutdown(ctx)
	assert.NoError(t, err)

	// ListenAndServe should return ErrServerClosed.
	err = <-done
	assert.Equal(t, http.ErrServerClosed, err)

	// Subsequent shutdown calls should not panic.
	err = srv.Shutdown(ctx)
	assert.NoError(t, err)
}
