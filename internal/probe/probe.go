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

// BlockData holds pre-computed block statistics for the probe response.
// A nil *BlockData means no blocklist is configured (passthrough mode).
type BlockData struct {
	Total   int64
	Size    int
	Sources int
	Top     []TopEntry
}

// TopEntry is a blocked domain with its hit count.
type TopEntry struct {
	Domain string `json:"domain"`
	Count  int64  `json:"count"`
}

// Response is the JSON structure returned by the probe endpoint.
type Response struct {
	Status            string     `json:"status"`
	Service           string     `json:"service"`
	Version           string     `json:"version"`
	Mode              string     `json:"mode"`
	UptimeSeconds     int64      `json:"uptime_seconds"`
	ConnectionsTotal  int64      `json:"connections_total"`
	ConnectionsActive int64      `json:"connections_active"`
	BlocksTotal       int64      `json:"blocks_total"`
	BlocklistSize     int        `json:"blocklist_size"`
	BlocklistSources  int        `json:"blocklist_sources"`
	TopBlocked        []TopEntry `json:"top_blocked"`
}

// Handler returns an http.HandlerFunc that serves the probe response.
// The blockFn callback is called on each request to get current block stats.
// If blockFn is nil, passthrough mode is reported.
func Handler(stats Stats, blockFn func() *BlockData) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mode := "passthrough"
		var blocksTotal int64
		var blocklistSize int
		var blocklistSources int
		var topBlocked []TopEntry

		if blockFn != nil {
			if bd := blockFn(); bd != nil && bd.Size > 0 {
				mode = "blocking"
				blocksTotal = bd.Total
				blocklistSize = bd.Size
				blocklistSources = bd.Sources
				topBlocked = bd.Top
			}
		}

		if topBlocked == nil {
			topBlocked = []TopEntry{}
		}

		resp := Response{
			Status:            "ok",
			Service:           "face-puncher-supreme",
			Version:           version.Short(),
			Mode:              mode,
			UptimeSeconds:     int64(stats.Uptime().Seconds()),
			ConnectionsTotal:  stats.ConnectionsTotal(),
			ConnectionsActive: stats.ConnectionsActive(),
			BlocksTotal:       blocksTotal,
			BlocklistSize:     blocklistSize,
			BlocklistSources:  blocklistSources,
			TopBlocked:        topBlocked,
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp) //nolint:gosec // best-effort response
	}
}
