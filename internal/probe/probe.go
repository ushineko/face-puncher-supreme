/*
Package probe implements the management endpoint handlers for the proxy:
/fps/heartbeat (lightweight health check) and /fps/stats (full statistics).
*/
package probe

import (
	"encoding/json"
	"net/http"
	"runtime"
	"strconv"
	"time"

	"github.com/ushineko/face-puncher-supreme/internal/stats"
	"github.com/ushineko/face-puncher-supreme/internal/version"
)

// ServerInfo provides lightweight server metrics for the heartbeat.
type ServerInfo interface {
	Uptime() time.Duration
	StartedAt() time.Time
	ConnectionsTotal() int64
	ConnectionsActive() int64
}

// BlockData holds blocklist metadata for the stats response.
type BlockData struct {
	Total         int64
	AllowsTotal   int64
	Size          int
	AllowlistSize int
	Sources       int
}

// MITMData holds MITM interception metadata for responses.
type MITMData struct {
	Enabled           bool
	InterceptsTotal   int64
	DomainsConfigured int
}

// TopEntry is a domain with a counter value.
type TopEntry struct {
	Domain string `json:"domain"`
	Count  int64  `json:"count"`
}

// PluginInfo holds per-plugin metadata for heartbeat/stats.
type PluginInfo struct {
	Name    string
	Version string
	Mode    string
	Domains []string
}

// PluginsData holds plugin metadata for responses.
type PluginsData struct {
	Active  int
	Plugins []PluginInfo
}

// HeartbeatResponse is the JSON structure returned by /fps/heartbeat.
type HeartbeatResponse struct {
	Status        string   `json:"status"`
	Service       string   `json:"service"`
	Version       string   `json:"version"`
	Mode          string   `json:"mode"`
	MITMEnabled   bool     `json:"mitm_enabled"`
	MITMDomains   int      `json:"mitm_domains"`
	PluginsActive int      `json:"plugins_active"`
	Plugins       []string `json:"plugins"`
	UptimeSeconds int64    `json:"uptime_seconds"`
	OS            string   `json:"os"`
	Arch          string   `json:"arch"`
	GoVersion     string   `json:"go_version"`
	StartedAt     string   `json:"started_at"`
}

// StatsResponse is the JSON structure returned by /fps/stats.
type StatsResponse struct {
	Connections ConnectionsBlock `json:"connections"`
	Blocking    BlockingBlock    `json:"blocking"`
	MITM        MITMBlock        `json:"mitm"`
	Plugins     PluginsBlock     `json:"plugins"`
	Domains     DomainsBlock     `json:"domains"`
	Clients     ClientsBlock     `json:"clients"`
	Traffic     TrafficBlock     `json:"traffic"`
}

// PluginsBlock holds plugin filter statistics.
type PluginsBlock struct {
	Active  int                 `json:"active"`
	Filters []PluginFilterEntry `json:"filters"`
}

// PluginFilterEntry holds per-plugin stats for the stats response.
type PluginFilterEntry struct {
	Name               string          `json:"name"`
	Version            string          `json:"version"`
	Mode               string          `json:"mode"`
	Domains            []string        `json:"domains"`
	ResponsesInspected int64           `json:"responses_inspected"`
	ResponsesMatched   int64           `json:"responses_matched"`
	ResponsesModified  int64           `json:"responses_modified"`
	TopRules           []RuleCountJSON `json:"top_rules"`
}

// RuleCountJSON is the JSON-friendly version of a rule count.
type RuleCountJSON struct {
	Rule  string `json:"rule"`
	Count int64  `json:"count"`
}

// MITMBlock holds MITM interception statistics.
type MITMBlock struct {
	Enabled           bool       `json:"enabled"`
	InterceptsTotal   int64      `json:"intercepts_total"`
	DomainsConfigured int        `json:"domains_configured"`
	TopIntercepted    []TopEntry `json:"top_intercepted"`
}

// ConnectionsBlock holds real-time connection counters.
type ConnectionsBlock struct {
	Total  int64 `json:"total"`
	Active int64 `json:"active"`
}

// BlockingBlock holds block statistics.
type BlockingBlock struct {
	BlocksTotal      int64      `json:"blocks_total"`
	AllowsTotal      int64      `json:"allows_total"`
	BlocklistSize    int        `json:"blocklist_size"`
	AllowlistSize    int        `json:"allowlist_size"`
	BlocklistSources int        `json:"blocklist_sources"`
	TopBlocked       []TopEntry `json:"top_blocked"`
	TopAllowed       []TopEntry `json:"top_allowed"`
}

// DomainsBlock holds domain request statistics.
type DomainsBlock struct {
	TopRequested []TopEntry `json:"top_requested"`
}

// ClientEntry holds per-client stats for the response.
type ClientEntry struct {
	ClientIP string `json:"client_ip"`
	Requests int64  `json:"requests"`
	Blocked  int64  `json:"blocked"`
	BytesIn  int64  `json:"bytes_in"`
	BytesOut int64  `json:"bytes_out"`
}

// ClientsBlock holds client statistics.
type ClientsBlock struct {
	TopByRequests []ClientEntry `json:"top_by_requests"`
}

// TrafficBlock holds aggregate traffic totals.
type TrafficBlock struct {
	TotalRequests int64 `json:"total_requests"`
	TotalBlocked  int64 `json:"total_blocked"`
	TotalBytesIn  int64 `json:"total_bytes_in"`
	TotalBytesOut int64 `json:"total_bytes_out"`
}

// HeartbeatHandler returns an http.HandlerFunc for the heartbeat endpoint.
// No database queries, no sorting — just reads atomics and static values.
func HeartbeatHandler(info ServerInfo, blockFn func() *BlockData, mitmFn func() *MITMData, pluginsFn func() *PluginsData) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		mode := "passthrough"
		if blockFn != nil {
			if bd := blockFn(); bd != nil && bd.Size > 0 {
				mode = "blocking"
			}
		}

		var mitmEnabled bool
		var mitmDomains int
		if mitmFn != nil {
			if md := mitmFn(); md != nil {
				mitmEnabled = md.Enabled
				mitmDomains = md.DomainsConfigured
			}
		}

		var pluginsActive int
		var pluginList []string
		if pluginsFn != nil {
			if pd := pluginsFn(); pd != nil {
				pluginsActive = pd.Active
				for _, p := range pd.Plugins {
					pluginList = append(pluginList, p.Name+"@"+p.Version)
				}
			}
		}
		if pluginList == nil {
			pluginList = []string{}
		}

		resp := HeartbeatResponse{
			Status:        "ok",
			Service:       "face-puncher-supreme",
			Version:       version.Short(),
			Mode:          mode,
			MITMEnabled:   mitmEnabled,
			MITMDomains:   mitmDomains,
			PluginsActive: pluginsActive,
			Plugins:       pluginList,
			UptimeSeconds: int64(info.Uptime().Seconds()),
			OS:            runtime.GOOS,
			Arch:          runtime.GOARCH,
			GoVersion:     runtime.Version(),
			StartedAt:     info.StartedAt().UTC().Format(time.RFC3339),
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp) //nolint:gosec // best-effort response
	}
}

// StatsProvider supplies data for the full stats response.
type StatsProvider struct {
	Info      ServerInfo
	BlockFn   func() *BlockData
	MITMFn    func() *MITMData
	PluginsFn func() *PluginsData
	StatsDB   *stats.DB
	Collector *stats.Collector
}

// StatsHandler returns an http.HandlerFunc for the full stats endpoint.
// Supports query parameters: n (top-N size), period (time window).
func StatsHandler(sp *StatsProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n := 10
		if nStr := r.URL.Query().Get("n"); nStr != "" {
			if parsed, err := strconv.Atoi(nStr); err == nil && parsed > 0 {
				n = parsed
			}
		}

		var periodSince *time.Time
		if period := r.URL.Query().Get("period"); period != "" {
			var d time.Duration
			switch period {
			case "1h":
				d = time.Hour
			case "24h":
				d = 24 * time.Hour
			case "7d":
				d = 7 * 24 * time.Hour
			}
			if d > 0 {
				t := time.Now().Add(-d)
				periodSince = &t
			}
		}

		// Block stats from blocklist DB.
		var blocksTotal int64
		var allowsTotal int64
		var blocklistSize int
		var allowlistSize int
		var blocklistSources int
		if sp.BlockFn != nil {
			if bd := sp.BlockFn(); bd != nil {
				blocksTotal = bd.Total
				allowsTotal = bd.AllowsTotal
				blocklistSize = bd.Size
				allowlistSize = bd.AllowlistSize
				blocklistSources = bd.Sources
			}
		}

		var topBlocked []TopEntry
		var topAllowed []TopEntry
		var topRequested []TopEntry
		var topClients []ClientEntry
		var totalReqs, totalBlocked, totalBytesIn, totalBytesOut int64

		switch {
		case periodSince != nil && sp.StatsDB != nil:
			// Time-bounded queries from hourly rollups.
			topBlocked = domainCountsToEntries(sp.StatsDB.TopBlocked(n))
			topAllowed = domainCountsToEntries(sp.StatsDB.TopAllowed(n))
			topRequested = domainCountsToEntries(sp.StatsDB.TopRequested(n))
			clients := sp.StatsDB.TopClientsSince(n, *periodSince)
			topClients = clientSnapsToEntries(clients)
			totalReqs, totalBlocked, totalBytesIn, totalBytesOut = sp.StatsDB.TrafficTotalsSince(*periodSince)
		case sp.StatsDB != nil:
			// All-time: merge in-memory + DB.
			topBlocked = domainCountsToEntries(sp.StatsDB.MergedTopBlocked(n))
			topAllowed = domainCountsToEntries(sp.StatsDB.MergedTopAllowed(n))
			topRequested = domainCountsToEntries(sp.StatsDB.MergedTopRequested(n))
			topClients = clientSnapsToEntries(sp.StatsDB.MergedTopClients(n))
			totalReqs = sp.Collector.TotalRequests()
			totalBlocked = sp.Collector.TotalBlocked()
			totalBytesIn = sp.Collector.TotalBytesIn()
			totalBytesOut = sp.Collector.TotalBytesOut()
		default:
			// No DB — just return in-memory data (no allow data without DB).
			topBlocked = domainCountsToEntries(topN(sp.Collector.SnapshotDomainBlocks(), n))
			topRequested = domainCountsToEntries(topN(sp.Collector.SnapshotDomainRequests(), n))
			topClients = clientSnapsToEntries(topNClients(sp.Collector.SnapshotClients(), n))
			totalReqs = sp.Collector.TotalRequests()
			totalBlocked = sp.Collector.TotalBlocked()
			totalBytesIn = sp.Collector.TotalBytesIn()
			totalBytesOut = sp.Collector.TotalBytesOut()
		}

		if topBlocked == nil {
			topBlocked = []TopEntry{}
		}
		if topAllowed == nil {
			topAllowed = []TopEntry{}
		}
		if topRequested == nil {
			topRequested = []TopEntry{}
		}
		if topClients == nil {
			topClients = []ClientEntry{}
		}

		// MITM stats (always from in-memory — no DB persistence for MITM yet).
		mitmBlock := MITMBlock{}
		if sp.MITMFn != nil {
			if md := sp.MITMFn(); md != nil {
				mitmBlock.Enabled = md.Enabled
				mitmBlock.InterceptsTotal = md.InterceptsTotal
				mitmBlock.DomainsConfigured = md.DomainsConfigured
			}
		}
		topMITM := domainCountsToEntries(topN(sp.Collector.SnapshotMITMIntercepts(), n))
		if topMITM == nil {
			topMITM = []TopEntry{}
		}
		mitmBlock.TopIntercepted = topMITM

		pluginsBlock := buildPluginsBlock(sp, n)

		resp := StatsResponse{
			Connections: ConnectionsBlock{
				Total:  sp.Info.ConnectionsTotal(),
				Active: sp.Info.ConnectionsActive(),
			},
			Blocking: BlockingBlock{
				BlocksTotal:      blocksTotal,
				AllowsTotal:      allowsTotal,
				BlocklistSize:    blocklistSize,
				AllowlistSize:    allowlistSize,
				BlocklistSources: blocklistSources,
				TopBlocked:       topBlocked,
				TopAllowed:       topAllowed,
			},
			MITM:    mitmBlock,
			Plugins: pluginsBlock,
			Domains: DomainsBlock{
				TopRequested: topRequested,
			},
			Clients: ClientsBlock{
				TopByRequests: topClients,
			},
			Traffic: TrafficBlock{
				TotalRequests: totalReqs,
				TotalBlocked:  totalBlocked,
				TotalBytesIn:  totalBytesIn,
				TotalBytesOut: totalBytesOut,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp) //nolint:gosec // best-effort response
	}
}

// StatsDisabledHandler returns 501 Not Implemented when stats are disabled.
func StatsDisabledHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{ //nolint:gosec // best-effort response
			"error": "stats collection is disabled",
		})
	}
}

// buildPluginsBlock constructs the plugins section for the stats response.
func buildPluginsBlock(sp *StatsProvider, n int) PluginsBlock {
	block := PluginsBlock{Filters: []PluginFilterEntry{}}
	if sp.PluginsFn == nil {
		return block
	}
	pd := sp.PluginsFn()
	if pd == nil {
		return block
	}
	block.Active = pd.Active
	snaps := sp.Collector.SnapshotPlugins()
	for _, pi := range pd.Plugins {
		entry := PluginFilterEntry{
			Name:    pi.Name,
			Version: pi.Version,
			Mode:    pi.Mode,
			Domains: pi.Domains,
		}
		for _, s := range snaps {
			if s.Name == pi.Name {
				entry.ResponsesInspected = s.Inspected
				entry.ResponsesMatched = s.Matched
				entry.ResponsesModified = s.Modified
				break
			}
		}
		rules := sp.Collector.SnapshotPluginRules(pi.Name, n)
		topRules := make([]RuleCountJSON, len(rules))
		for j, r := range rules {
			topRules[j] = RuleCountJSON{Rule: r.Rule, Count: r.Count}
		}
		entry.TopRules = topRules
		if entry.Domains == nil {
			entry.Domains = []string{}
		}
		block.Filters = append(block.Filters, entry)
	}
	return block
}

// domainCountsToEntries converts stats.DomainCount slice to TopEntry slice.
func domainCountsToEntries(dcs []stats.DomainCount) []TopEntry {
	out := make([]TopEntry, len(dcs))
	for i, dc := range dcs {
		out[i] = TopEntry{Domain: dc.Domain, Count: dc.Count}
	}
	return out
}

// clientSnapsToEntries converts stats.ClientSnapshot slice to ClientEntry slice.
func clientSnapsToEntries(snaps []stats.ClientSnapshot) []ClientEntry {
	out := make([]ClientEntry, len(snaps))
	for i, cs := range snaps {
		out[i] = ClientEntry{
			ClientIP: cs.IP,
			Requests: cs.Requests,
			Blocked:  cs.Blocked,
			BytesIn:  cs.BytesIn,
			BytesOut: cs.BytesOut,
		}
	}
	return out
}

// topN returns the top n entries from a DomainCount slice (sorts in-place).
func topN(dcs []stats.DomainCount, n int) []stats.DomainCount {
	for i := 1; i < len(dcs); i++ {
		for j := i; j > 0 && dcs[j].Count > dcs[j-1].Count; j-- {
			dcs[j], dcs[j-1] = dcs[j-1], dcs[j]
		}
	}
	if len(dcs) > n {
		dcs = dcs[:n]
	}
	return dcs
}

// topNClients returns the top n clients by request count.
func topNClients(snaps []stats.ClientSnapshot, n int) []stats.ClientSnapshot {
	for i := 1; i < len(snaps); i++ {
		for j := i; j > 0 && snaps[j].Requests > snaps[j-1].Requests; j-- {
			snaps[j], snaps[j-1] = snaps[j-1], snaps[j]
		}
	}
	if len(snaps) > n {
		snaps = snaps[:n]
	}
	return snaps
}
