package probe_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ushineko/face-puncher-supreme/internal/probe"
)

type _mockStats struct {
	total  int64
	active int64
	uptime time.Duration
}

func (m *_mockStats) ConnectionsTotal() int64  { return m.total }
func (m *_mockStats) ConnectionsActive() int64 { return m.active }
func (m *_mockStats) Uptime() time.Duration    { return m.uptime }

func TestProbeHandler(t *testing.T) {
	tests := []struct {
		name   string
		stats  *_mockStats
		checks func(t *testing.T, resp probe.Response)
	}{
		{
			name:  "returns ok status and service name",
			stats: &_mockStats{total: 0, active: 0, uptime: 0},
			checks: func(t *testing.T, resp probe.Response) {
				assert.Equal(t, "ok", resp.Status)
				assert.Equal(t, "face-puncher-supreme", resp.Service)
				assert.Equal(t, "passthrough", resp.Mode)
			},
		},
		{
			name:  "returns version",
			stats: &_mockStats{},
			checks: func(t *testing.T, resp probe.Response) {
				assert.NotEmpty(t, resp.Version)
			},
		},
		{
			name:  "returns connection counters",
			stats: &_mockStats{total: 42, active: 3, uptime: 90 * time.Second},
			checks: func(t *testing.T, resp probe.Response) {
				assert.Equal(t, int64(42), resp.ConnectionsTotal)
				assert.Equal(t, int64(3), resp.ConnectionsActive)
				assert.Equal(t, int64(90), resp.UptimeSeconds)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := probe.Handler(tt.stats, nil)
			req := httptest.NewRequest(http.MethodGet, "/fps/probe", http.NoBody)
			rec := httptest.NewRecorder()

			handler(rec, req)

			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

			var resp probe.Response
			err := json.Unmarshal(rec.Body.Bytes(), &resp)
			require.NoError(t, err, "response should be valid JSON")

			tt.checks(t, resp)
		})
	}
}

func TestProbeHandlerPassthroughDefaults(t *testing.T) {
	handler := probe.Handler(&_mockStats{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/fps/probe", http.NoBody)
	rec := httptest.NewRecorder()

	handler(rec, req)

	var resp probe.Response
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, "passthrough", resp.Mode)
	assert.Equal(t, int64(0), resp.BlocksTotal)
	assert.Equal(t, 0, resp.BlocklistSize)
	assert.Equal(t, 0, resp.BlocklistSources)
	assert.Empty(t, resp.TopBlocked)
	assert.NotNil(t, resp.TopBlocked, "top_blocked should be [] not null")
}

func TestProbeHandlerBlockingMode(t *testing.T) {
	blockFn := func() *probe.BlockData {
		return &probe.BlockData{
			Total:   42,
			Size:    1000,
			Sources: 3,
			Top: []probe.TopEntry{
				{Domain: "ads.example.com", Count: 20},
				{Domain: "tracker.example.org", Count: 15},
			},
		}
	}

	handler := probe.Handler(&_mockStats{total: 100}, blockFn)
	req := httptest.NewRequest(http.MethodGet, "/fps/probe", http.NoBody)
	rec := httptest.NewRecorder()

	handler(rec, req)

	var resp probe.Response
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, "blocking", resp.Mode)
	assert.Equal(t, int64(42), resp.BlocksTotal)
	assert.Equal(t, 1000, resp.BlocklistSize)
	assert.Equal(t, 3, resp.BlocklistSources)
	require.Len(t, resp.TopBlocked, 2)
	assert.Equal(t, "ads.example.com", resp.TopBlocked[0].Domain)
	assert.Equal(t, int64(20), resp.TopBlocked[0].Count)
}
