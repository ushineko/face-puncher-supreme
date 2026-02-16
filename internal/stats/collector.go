/*
Package stats provides in-memory counters and SQLite persistence for
proxy traffic statistics.

The Collector accumulates per-client and per-domain counters in memory
using atomic operations for lock-free increments. A background flush loop
periodically writes deltas to a SQLite database for persistence across
restarts.
*/
package stats

import (
	"sync"
	"sync/atomic"
)

// clientStats holds per-client-IP counters (all atomic for lock-free access).
type clientStats struct {
	Requests atomic.Int64
	Blocked  atomic.Int64
	BytesIn  atomic.Int64
	BytesOut atomic.Int64
}

// Collector accumulates in-memory traffic statistics.
type Collector struct {
	// Per-client-IP stats.
	clients sync.Map // string -> *clientStats

	// Per-domain total request counts (all traffic, not just blocked).
	domainRequests sync.Map // string -> *atomic.Int64

	// Per-domain block counts.
	domainBlocks sync.Map // string -> *atomic.Int64
}

// NewCollector creates a new in-memory stats collector.
func NewCollector() *Collector {
	return &Collector{}
}

// RecordRequest records a request from a client to a domain.
func (c *Collector) RecordRequest(clientIP, domain string, blocked bool, bytesIn, bytesOut int64) {
	// Per-client stats.
	val, _ := c.clients.LoadOrStore(clientIP, &clientStats{})
	cs, _ := val.(*clientStats) //nolint:errcheck // type is guaranteed by LoadOrStore
	cs.Requests.Add(1)
	cs.BytesIn.Add(bytesIn)
	cs.BytesOut.Add(bytesOut)
	if blocked {
		cs.Blocked.Add(1)
	}

	// Per-domain request count.
	dv, _ := c.domainRequests.LoadOrStore(domain, &atomic.Int64{})
	dv.(*atomic.Int64).Add(1) //nolint:errcheck // type is guaranteed by LoadOrStore

	// Per-domain block count.
	if blocked {
		bv, _ := c.domainBlocks.LoadOrStore(domain, &atomic.Int64{})
		bv.(*atomic.Int64).Add(1) //nolint:errcheck // type is guaranteed by LoadOrStore
	}
}

// RecordBytes adds byte counts to an existing client entry (for CONNECT tunnels
// where final byte counts are known after the tunnel closes).
func (c *Collector) RecordBytes(clientIP string, bytesIn, bytesOut int64) {
	val, _ := c.clients.LoadOrStore(clientIP, &clientStats{})
	cs, _ := val.(*clientStats) //nolint:errcheck // type is guaranteed by LoadOrStore
	cs.BytesIn.Add(bytesIn)
	cs.BytesOut.Add(bytesOut)
}

// ClientSnapshot captures a point-in-time view of per-client counters.
type ClientSnapshot struct {
	IP       string
	Requests int64
	Blocked  int64
	BytesIn  int64
	BytesOut int64
}

// DomainCount holds a domain and its counter value.
type DomainCount struct {
	Domain string
	Count  int64
}

// SnapshotClients returns current per-client stats.
func (c *Collector) SnapshotClients() []ClientSnapshot {
	var out []ClientSnapshot
	c.clients.Range(func(key, value any) bool {
		cs, _ := value.(*clientStats) //nolint:errcheck // type is guaranteed
		ip, _ := key.(string)         //nolint:errcheck // type is guaranteed
		out = append(out, ClientSnapshot{
			IP:       ip,
			Requests: cs.Requests.Load(),
			Blocked:  cs.Blocked.Load(),
			BytesIn:  cs.BytesIn.Load(),
			BytesOut: cs.BytesOut.Load(),
		})
		return true
	})
	return out
}

// SnapshotDomainRequests returns current per-domain request counts.
func (c *Collector) SnapshotDomainRequests() []DomainCount {
	var out []DomainCount
	c.domainRequests.Range(func(key, value any) bool {
		domain, _ := key.(string)         //nolint:errcheck // type is guaranteed
		counter, _ := value.(*atomic.Int64) //nolint:errcheck // type is guaranteed
		out = append(out, DomainCount{Domain: domain, Count: counter.Load()})
		return true
	})
	return out
}

// SnapshotDomainBlocks returns current per-domain block counts.
func (c *Collector) SnapshotDomainBlocks() []DomainCount {
	var out []DomainCount
	c.domainBlocks.Range(func(key, value any) bool {
		domain, _ := key.(string)         //nolint:errcheck // type is guaranteed
		counter, _ := value.(*atomic.Int64) //nolint:errcheck // type is guaranteed
		out = append(out, DomainCount{Domain: domain, Count: counter.Load()})
		return true
	})
	return out
}

// TotalRequests returns the sum of all client request counts.
func (c *Collector) TotalRequests() int64 {
	var total int64
	c.clients.Range(func(_, value any) bool {
		cs, _ := value.(*clientStats) //nolint:errcheck // type is guaranteed
		total += cs.Requests.Load()
		return true
	})
	return total
}

// TotalBlocked returns the sum of all client blocked counts.
func (c *Collector) TotalBlocked() int64 {
	var total int64
	c.clients.Range(func(_, value any) bool {
		cs, _ := value.(*clientStats) //nolint:errcheck // type is guaranteed
		total += cs.Blocked.Load()
		return true
	})
	return total
}

// TotalBytesIn returns the sum of all client bytes-in counts.
func (c *Collector) TotalBytesIn() int64 {
	var total int64
	c.clients.Range(func(_, value any) bool {
		cs, _ := value.(*clientStats) //nolint:errcheck // type is guaranteed
		total += cs.BytesIn.Load()
		return true
	})
	return total
}

// TotalBytesOut returns the sum of all client bytes-out counts.
func (c *Collector) TotalBytesOut() int64 {
	var total int64
	c.clients.Range(func(_, value any) bool {
		cs, _ := value.(*clientStats) //nolint:errcheck // type is guaranteed
		total += cs.BytesOut.Load()
		return true
	})
	return total
}
