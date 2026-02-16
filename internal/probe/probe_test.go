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
	"github.com/ushineko/face-puncher-supreme/internal/stats"
)

type _mockServerInfo struct {
	total     int64
	active    int64
	uptime    time.Duration
	startedAt time.Time
}

func (m *_mockServerInfo) ConnectionsTotal() int64  { return m.total }
func (m *_mockServerInfo) ConnectionsActive() int64 { return m.active }
func (m *_mockServerInfo) Uptime() time.Duration    { return m.uptime }
func (m *_mockServerInfo) StartedAt() time.Time     { return m.startedAt }

func TestHeartbeatHandler(t *testing.T) {
	tests := []struct {
		name   string
		info   *_mockServerInfo
		checks func(t *testing.T, resp probe.HeartbeatResponse)
	}{
		{
			name: "returns ok status and service name",
			info: &_mockServerInfo{startedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
			checks: func(t *testing.T, resp probe.HeartbeatResponse) {
				assert.Equal(t, "ok", resp.Status)
				assert.Equal(t, "face-puncher-supreme", resp.Service)
				assert.Equal(t, "passthrough", resp.Mode)
			},
		},
		{
			name: "returns version",
			info: &_mockServerInfo{startedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
			checks: func(t *testing.T, resp probe.HeartbeatResponse) {
				assert.NotEmpty(t, resp.Version)
			},
		},
		{
			name: "returns uptime",
			info: &_mockServerInfo{uptime: 90 * time.Second, startedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
			checks: func(t *testing.T, resp probe.HeartbeatResponse) {
				assert.Equal(t, int64(90), resp.UptimeSeconds)
			},
		},
		{
			name: "returns OS and architecture",
			info: &_mockServerInfo{startedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
			checks: func(t *testing.T, resp probe.HeartbeatResponse) {
				assert.NotEmpty(t, resp.OS)
				assert.NotEmpty(t, resp.Arch)
				assert.NotEmpty(t, resp.GoVersion)
				assert.NotEmpty(t, resp.StartedAt)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := probe.HeartbeatHandler(tt.info, nil, nil, nil)
			req := httptest.NewRequest(http.MethodGet, "/fps/heartbeat", http.NoBody)
			rec := httptest.NewRecorder()

			handler(rec, req)

			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

			var resp probe.HeartbeatResponse
			err := json.Unmarshal(rec.Body.Bytes(), &resp)
			require.NoError(t, err, "response should be valid JSON")

			tt.checks(t, resp)
		})
	}
}

func TestHeartbeatHandlerPassthroughDefaults(t *testing.T) {
	info := &_mockServerInfo{startedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	handler := probe.HeartbeatHandler(info, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/fps/heartbeat", http.NoBody)
	rec := httptest.NewRecorder()

	handler(rec, req)

	var resp probe.HeartbeatResponse
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, "passthrough", resp.Mode)
}

func TestHeartbeatHandlerBlockingMode(t *testing.T) {
	blockFn := func() *probe.BlockData {
		return &probe.BlockData{
			Total:   42,
			Size:    1000,
			Sources: 3,
		}
	}

	info := &_mockServerInfo{total: 100, startedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	handler := probe.HeartbeatHandler(info, blockFn, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/fps/heartbeat", http.NoBody)
	rec := httptest.NewRecorder()

	handler(rec, req)

	var resp probe.HeartbeatResponse
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, "blocking", resp.Mode)
}

func TestStatsHandler(t *testing.T) {
	collector := stats.NewCollector()
	collector.RecordRequest("192.168.1.42", "www.example.com", false, 100, 5000)
	collector.RecordRequest("192.168.1.42", "ads.example.com", true, 0, 0)
	collector.RecordRequest("192.168.1.15", "www.example.com", false, 200, 3000)

	info := &_mockServerInfo{total: 50, active: 2, startedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	blockFn := func() *probe.BlockData {
		return &probe.BlockData{Total: 1, Size: 500, Sources: 2}
	}

	sp := &probe.StatsProvider{
		Info:      info,
		BlockFn:   blockFn,
		Collector: collector,
	}

	handler := probe.StatsHandler(sp)
	req := httptest.NewRequest(http.MethodGet, "/fps/stats", http.NoBody)
	rec := httptest.NewRecorder()

	handler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var resp probe.StatsResponse
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, int64(50), resp.Connections.Total)
	assert.Equal(t, int64(2), resp.Connections.Active)
	assert.Equal(t, int64(1), resp.Blocking.BlocksTotal)
	assert.Equal(t, 500, resp.Blocking.BlocklistSize)
	assert.Equal(t, 2, resp.Blocking.BlocklistSources)
	assert.Equal(t, int64(3), resp.Traffic.TotalRequests)
	assert.Equal(t, int64(1), resp.Traffic.TotalBlocked)
	assert.Equal(t, int64(300), resp.Traffic.TotalBytesIn)
	assert.Equal(t, int64(8000), resp.Traffic.TotalBytesOut)

	// top_requested should have www.example.com (2 requests) first.
	require.NotEmpty(t, resp.Domains.TopRequested)
	assert.Equal(t, "www.example.com", resp.Domains.TopRequested[0].Domain)
	assert.Equal(t, int64(2), resp.Domains.TopRequested[0].Count)

	// top_blocked should have ads.example.com.
	require.NotEmpty(t, resp.Blocking.TopBlocked)
	assert.Equal(t, "ads.example.com", resp.Blocking.TopBlocked[0].Domain)

	// top_by_requests should have 192.168.1.42 first (2 requests).
	require.NotEmpty(t, resp.Clients.TopByRequests)
	assert.Equal(t, "192.168.1.42", resp.Clients.TopByRequests[0].ClientIP)
	assert.Equal(t, int64(2), resp.Clients.TopByRequests[0].Requests)
}

func TestStatsHandlerTopN(t *testing.T) {
	collector := stats.NewCollector()
	for i := 0; i < 20; i++ {
		domain := "domain" + string(rune('a'+i)) + ".com"
		collector.RecordRequest("10.0.0.1", domain, false, 0, 0)
	}

	info := &_mockServerInfo{startedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	sp := &probe.StatsProvider{Info: info, Collector: collector}

	handler := probe.StatsHandler(sp)
	req := httptest.NewRequest(http.MethodGet, "/fps/stats?n=5", http.NoBody)
	rec := httptest.NewRecorder()

	handler(rec, req)

	var resp probe.StatsResponse
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Len(t, resp.Domains.TopRequested, 5, "n=5 should limit top_requested to 5")
}

func TestStatsDisabledHandler(t *testing.T) {
	handler := probe.StatsDisabledHandler()
	req := httptest.NewRequest(http.MethodGet, "/fps/stats", http.NoBody)
	rec := httptest.NewRecorder()

	handler(rec, req)

	assert.Equal(t, http.StatusNotImplemented, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
}

func TestHeartbeatNoDBQueries(t *testing.T) {
	// Heartbeat should work with no StatsDB — it only reads atomics.
	info := &_mockServerInfo{
		total:     100,
		active:    5,
		uptime:    60 * time.Second,
		startedAt: time.Date(2026, 2, 16, 10, 0, 0, 0, time.UTC),
	}

	handler := probe.HeartbeatHandler(info, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/fps/heartbeat", http.NoBody)
	rec := httptest.NewRecorder()

	handler(rec, req)

	var resp probe.HeartbeatResponse
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, "ok", resp.Status)
	assert.Equal(t, int64(60), resp.UptimeSeconds)
	assert.Equal(t, "2026-02-16T10:00:00Z", resp.StartedAt)
}
