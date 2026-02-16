package proxy_test

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Integration tests that hit real external sites through the proxy.
// These require network access and are skipped in short mode.
//
// Run with: go test -v -run TestIntegration ./internal/proxy/
// Skip with: go test -short ./...

// Use a realistic browser User-Agent so external sites don't reject us
// based on Go's default UA. This also validates that the proxy faithfully
// forwards the header, matching real-world browser usage.
const testUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// _uaTransport wraps an http.RoundTripper and injects a User-Agent header.
type _uaTransport struct {
	base http.RoundTripper
	ua   string
}

func (t *_uaTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("User-Agent", t.ua)
	return t.base.RoundTrip(req)
}

// _integrationProxyClient returns an HTTP client configured to route through
// the test proxy, with TLS verification disabled and a realistic User-Agent.
func _integrationProxyClient(t *testing.T, proxyURL string) *http.Client {
	t.Helper()
	pURL, err := url.Parse(proxyURL)
	require.NoError(t, err)
	return &http.Client{
		Transport: &_uaTransport{
			base: &http.Transport{
				Proxy:           http.ProxyURL(pURL),
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // integration test
			},
			ua: testUserAgent,
		},
		// Do not follow redirects automatically so we can test redirect behavior.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// _integrationFollowRedirectsClient is like _integrationProxyClient but follows redirects.
func _integrationFollowRedirectsClient(t *testing.T, proxyURL string) *http.Client {
	t.Helper()
	pURL, err := url.Parse(proxyURL)
	require.NoError(t, err)
	return &http.Client{
		Transport: &_uaTransport{
			base: &http.Transport{
				Proxy:           http.ProxyURL(pURL),
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // integration test
			},
			ua: testUserAgent,
		},
	}
}

func TestIntegrationHTTPPlain(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	proxyURL, cleanup := _startTestProxy(t)
	defer cleanup()
	client := _integrationProxyClient(t, proxyURL)

	tests := []struct {
		name       string
		url        string
		wantStatus int
		minSize    int
		checkBody  func(t *testing.T, body []byte)
	}{
		{
			name:       "httpbin GET returns JSON",
			url:        "http://httpbin.org/get",
			wantStatus: http.StatusOK,
			minSize:    50,
			checkBody: func(t *testing.T, body []byte) {
				assert.Contains(t, string(body), `"url"`)
			},
		},
		{
			name:       "httpbin preserves status codes (418)",
			url:        "http://httpbin.org/status/418",
			wantStatus: http.StatusTeapot,
		},
		{
			name:       "httpbin binary data (100KB)",
			url:        "http://httpbin.org/bytes/102400",
			wantStatus: http.StatusOK,
			minSize:    102400,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := client.Get(tt.url)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, tt.wantStatus, resp.StatusCode)

			if tt.minSize > 0 || tt.checkBody != nil {
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				if tt.minSize > 0 {
					assert.GreaterOrEqual(t, len(body), tt.minSize,
						"response body should be at least %d bytes", tt.minSize)
				}
				if tt.checkBody != nil {
					tt.checkBody(t, body)
				}
			}
		})
	}
}

func TestIntegrationHTTPRedirect(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	proxyURL, cleanup := _startTestProxy(t)
	defer cleanup()

	t.Run("httpbin redirect chain (3 hops)", func(t *testing.T) {
		client := _integrationFollowRedirectsClient(t, proxyURL)
		resp, err := client.Get("http://httpbin.org/redirect/3")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode,
			"should follow 3 redirects and land on 200")
	})

	t.Run("HTTP to HTTPS redirect (github.com)", func(t *testing.T) {
		client := _integrationFollowRedirectsClient(t, proxyURL)
		resp, err := client.Get("http://github.com")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode,
			"should follow HTTP->HTTPS redirect")
		body, _ := io.ReadAll(resp.Body)
		assert.Greater(t, len(body), 1000, "github.com should return substantial HTML")
	})
}

func TestIntegrationHTTPSConnect(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	proxyURL, cleanup := _startTestProxy(t)
	defer cleanup()
	client := _integrationProxyClient(t, proxyURL)

	tests := []struct {
		name       string
		url        string
		wantStatus int
		minSize    int
	}{
		{
			name:       "example.com (simple HTTPS)",
			url:        "https://example.com",
			wantStatus: http.StatusOK,
			minSize:    500,
		},
		{
			name:       "wikipedia main page (large HTTPS)",
			url:        "https://en.wikipedia.org/wiki/Main_Page",
			wantStatus: http.StatusOK,
			minSize:    50000,
		},
		{
			name:       "httpbin HTTPS API",
			url:        "https://httpbin.org/get",
			wantStatus: http.StatusOK,
			minSize:    50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := client.Get(tt.url)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, tt.wantStatus, resp.StatusCode)

			if tt.minSize > 0 {
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				assert.GreaterOrEqual(t, len(body), tt.minSize)
			}
		})
	}
}

func TestIntegrationNewsSites(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	proxyURL, cleanup := _startTestProxy(t)
	defer cleanup()
	client := _integrationProxyClient(t, proxyURL)

	// These are the kinds of sites the proxy will target for ad blocking.
	// For now we just verify they load through the proxy.
	tests := []struct {
		name    string
		url     string
		minSize int
	}{
		{
			name:    "BBC News",
			url:     "https://www.bbc.com",
			minSize: 10000,
		},
		{
			name:    "CNN",
			url:     "https://www.cnn.com",
			minSize: 10000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := client.Get(tt.url)
			require.NoError(t, err)
			defer resp.Body.Close()

			// News sites may return 200 or 30x. Either is acceptable
			// as long as we get a response through the tunnel.
			assert.Less(t, resp.StatusCode, 500,
				"should not get server error through proxy")

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			assert.GreaterOrEqual(t, len(body), tt.minSize,
				"news site should return substantial content")
		})
	}
}

func TestIntegrationProbeCountersIncrement(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	proxyURL, cleanup := _startTestProxy(t)
	defer cleanup()
	client := _integrationProxyClient(t, proxyURL)

	// Get baseline.
	baseline := _getProbeTotal(t, proxyURL)

	// Make a few proxied requests.
	for _, site := range []string{
		"http://httpbin.org/get",
		"http://httpbin.org/status/200",
		"http://httpbin.org/status/201",
	} {
		resp, err := client.Get(site)
		require.NoError(t, err)
		resp.Body.Close()
	}

	// Verify counters increased.
	after := _getProbeTotal(t, proxyURL)
	assert.GreaterOrEqual(t, after-baseline, int64(3),
		"connections_total should have increased by at least 3")
}

// _getProbeTotal hits the probe endpoint directly and returns connections_total.
func _getProbeTotal(t *testing.T, proxyURL string) int64 {
	t.Helper()
	resp, err := http.Get(proxyURL + "/fps/probe")
	require.NoError(t, err)
	defer resp.Body.Close()

	var probeResp struct {
		ConnectionsTotal int64 `json:"connections_total"`
	}
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	err = json.Unmarshal(body, &probeResp)
	require.NoError(t, err)
	return probeResp.ConnectionsTotal
}
