/*
Package probe implements the /fps/probe liveness endpoint for the proxy.

The probe returns JSON with server status, version, mode, uptime, and
connection counters. It is used by remote test clients (e.g., the macOS
agent) to confirm the proxy is reachable and functioning.
*/
package probe

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/ushineko/face-puncher-supreme/internal/version"
)

// Stats provides an interface for the probe to read server metrics.
type Stats interface {
	ConnectionsTotal() int64
	ConnectionsActive() int64
	Uptime() time.Duration
}

// Response is the JSON structure returned by the probe endpoint.
type Response struct {
	Status            string `json:"status"`
	Service           string `json:"service"`
	Version           string `json:"version"`
	Mode              string `json:"mode"`
	UptimeSeconds     int64  `json:"uptime_seconds"`
	ConnectionsTotal  int64  `json:"connections_total"`
	ConnectionsActive int64  `json:"connections_active"`
}

// Handler returns an http.HandlerFunc that serves the probe response.
func Handler(stats Stats) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := Response{
			Status:            "ok",
			Service:           "face-puncher-supreme",
			Version:           version.Short(),
			Mode:              "passthrough",
			UptimeSeconds:     int64(stats.Uptime().Seconds()),
			ConnectionsTotal:  stats.ConnectionsTotal(),
			ConnectionsActive: stats.ConnectionsActive(),
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp) //nolint:gosec // best-effort response
	}
}
