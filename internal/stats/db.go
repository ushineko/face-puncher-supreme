package stats

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// DB manages the stats SQLite database and periodic flushing.
type DB struct {
	mu        sync.Mutex
	conn      *sqlite.Conn
	collector *Collector
	logger    *slog.Logger
	interval  time.Duration
	cancel    context.CancelFunc
	done      chan struct{}

	// lastClients / lastDomainReqs / lastDomainBlocks store the cumulative
	// snapshot from the previous flush so we can compute deltas.
	lastClients      map[string]ClientSnapshot
	lastDomainReqs   map[string]int64
	lastDomainBlks   map[string]int64
	lastDomainAllows map[string]int64

	// allowSnapshotFn is an optional callback that returns per-domain allow
	// counts from the blocklist package. Set via SetAllowStatsSource to
	// avoid an import cycle between stats and blocklist.
	allowSnapshotFn func() map[string]int64
}

// Open opens or creates a stats database at the given path.
func Open(dbPath string, collector *Collector, logger *slog.Logger, flushInterval time.Duration) (*DB, error) {
	conn, err := sqlite.OpenConn(dbPath, sqlite.OpenReadWrite|sqlite.OpenCreate)
	if err != nil {
		return nil, fmt.Errorf("open stats db: %w", err)
	}

	db := &DB{
		conn:             conn,
		collector:        collector,
		logger:           logger,
		interval:         flushInterval,
		done:             make(chan struct{}),
		lastClients:      make(map[string]ClientSnapshot),
		lastDomainReqs:   make(map[string]int64),
		lastDomainBlks:   make(map[string]int64),
		lastDomainAllows: make(map[string]int64),
	}

	if err := db.ensureSchema(); err != nil {
		_ = conn.Close()
		return nil, err
	}

	return db, nil
}

// SetAllowStatsSource sets the callback used to snapshot per-domain allow
// counts from the blocklist. This avoids an import cycle between packages.
func (db *DB) SetAllowStatsSource(fn func() map[string]int64) {
	db.allowSnapshotFn = fn
}

// Start begins the background flush loop.
func (db *DB) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	db.cancel = cancel

	go db.flushLoop(ctx)
}

// Close stops the flush loop, performs a final flush, and closes the database.
func (db *DB) Close() error {
	if db.cancel != nil {
		db.cancel()
		<-db.done
	}

	// Final flush.
	if err := db.Flush(); err != nil {
		db.logger.Error("final stats flush failed", "error", err)
	}

	return db.conn.Close()
}

// flushLoop runs periodic flushes until the context is cancelled.
func (db *DB) flushLoop(ctx context.Context) {
	defer close(db.done)

	ticker := time.NewTicker(db.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := db.Flush(); err != nil {
				db.logger.Error("stats flush failed", "error", err)
			}
		}
	}
}

// Flush computes deltas since the last flush and writes them to SQLite.
func (db *DB) Flush() (err error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	hour := time.Now().UTC().Truncate(time.Hour).Format("2006-01-02T15")

	defer sqlitex.Save(db.conn)(&err)

	// Flush per-client stats (delta since last flush) to traffic_hourly.
	currentClients := make(map[string]ClientSnapshot)
	for _, cs := range db.collector.SnapshotClients() {
		currentClients[cs.IP] = cs
		prev := db.lastClients[cs.IP]
		dReqs := cs.Requests - prev.Requests
		dBlocked := cs.Blocked - prev.Blocked
		dIn := cs.BytesIn - prev.BytesIn
		dOut := cs.BytesOut - prev.BytesOut
		if dReqs == 0 && dBlocked == 0 && dIn == 0 && dOut == 0 {
			continue
		}
		err = sqlitex.Execute(db.conn, `
			INSERT INTO traffic_hourly (hour, client_ip, requests, blocked, bytes_in, bytes_out)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (hour, client_ip) DO UPDATE SET
				requests  = requests  + excluded.requests,
				blocked   = blocked   + excluded.blocked,
				bytes_in  = bytes_in  + excluded.bytes_in,
				bytes_out = bytes_out + excluded.bytes_out
		`, &sqlitex.ExecOptions{
			Args: []any{hour, cs.IP, dReqs, dBlocked, dIn, dOut},
		})
		if err != nil {
			return fmt.Errorf("upsert traffic_hourly: %w", err)
		}
	}
	db.lastClients = currentClients

	// Flush per-domain block count deltas.
	currentBlks := make(map[string]int64)
	for _, dc := range db.collector.SnapshotDomainBlocks() {
		currentBlks[dc.Domain] = dc.Count
		prev := db.lastDomainBlks[dc.Domain]
		delta := dc.Count - prev
		if delta == 0 {
			continue
		}
		err = sqlitex.Execute(db.conn, `
			INSERT INTO blocked_domains (domain, count)
			VALUES (?, ?)
			ON CONFLICT (domain) DO UPDATE SET
				count = count + excluded.count
		`, &sqlitex.ExecOptions{
			Args: []any{dc.Domain, delta},
		})
		if err != nil {
			return fmt.Errorf("upsert blocked_domains: %w", err)
		}
	}
	db.lastDomainBlks = currentBlks

	// Flush per-domain request count deltas.
	currentReqs := make(map[string]int64)
	for _, dc := range db.collector.SnapshotDomainRequests() {
		currentReqs[dc.Domain] = dc.Count
		prev := db.lastDomainReqs[dc.Domain]
		delta := dc.Count - prev
		if delta == 0 {
			continue
		}
		err = sqlitex.Execute(db.conn, `
			INSERT INTO domain_requests (domain, count)
			VALUES (?, ?)
			ON CONFLICT (domain) DO UPDATE SET
				count = count + excluded.count
		`, &sqlitex.ExecOptions{
			Args: []any{dc.Domain, delta},
		})
		if err != nil {
			return fmt.Errorf("upsert domain_requests: %w", err)
		}
	}
	db.lastDomainReqs = currentReqs

	// Flush per-domain allow count deltas (if source is configured).
	if db.allowSnapshotFn != nil {
		currentAllows := db.allowSnapshotFn()
		for domain, count := range currentAllows {
			prev := db.lastDomainAllows[domain]
			delta := count - prev
			if delta == 0 {
				continue
			}
			err = sqlitex.Execute(db.conn, `
				INSERT INTO allowed_domains (domain, count)
				VALUES (?, ?)
				ON CONFLICT (domain) DO UPDATE SET
					count = count + excluded.count
			`, &sqlitex.ExecOptions{
				Args: []any{domain, delta},
			})
			if err != nil {
				return fmt.Errorf("upsert allowed_domains: %w", err)
			}
		}
		db.lastDomainAllows = currentAllows
	}

	return nil
}

// TopBlocked returns the top n blocked domains from the database.
func (db *DB) TopBlocked(n int) []DomainCount {
	db.mu.Lock()
	defer db.mu.Unlock()
	var out []DomainCount
	_ = sqlitex.Execute(db.conn, `
		SELECT domain, count FROM blocked_domains
		ORDER BY count DESC LIMIT ?
	`, &sqlitex.ExecOptions{
		Args: []any{n},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			out = append(out, DomainCount{
				Domain: stmt.ColumnText(0),
				Count:  stmt.ColumnInt64(1),
			})
			return nil
		},
	})
	return out
}

// TopRequested returns the top n most-requested domains from the database.
func (db *DB) TopRequested(n int) []DomainCount {
	db.mu.Lock()
	defer db.mu.Unlock()
	var out []DomainCount
	_ = sqlitex.Execute(db.conn, `
		SELECT domain, count FROM domain_requests
		ORDER BY count DESC LIMIT ?
	`, &sqlitex.ExecOptions{
		Args: []any{n},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			out = append(out, DomainCount{
				Domain: stmt.ColumnText(0),
				Count:  stmt.ColumnInt64(1),
			})
			return nil
		},
	})
	return out
}

// TopClients returns the top n clients by request count from the database.
func (db *DB) TopClients(n int) []ClientSnapshot {
	db.mu.Lock()
	defer db.mu.Unlock()
	var out []ClientSnapshot
	_ = sqlitex.Execute(db.conn, `
		SELECT client_ip,
			SUM(requests) as total_requests,
			SUM(blocked) as total_blocked,
			SUM(bytes_in) as total_bytes_in,
			SUM(bytes_out) as total_bytes_out
		FROM traffic_hourly
		GROUP BY client_ip
		ORDER BY total_requests DESC LIMIT ?
	`, &sqlitex.ExecOptions{
		Args: []any{n},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			out = append(out, ClientSnapshot{
				IP:       stmt.ColumnText(0),
				Requests: stmt.ColumnInt64(1),
				Blocked:  stmt.ColumnInt64(2),
				BytesIn:  stmt.ColumnInt64(3),
				BytesOut: stmt.ColumnInt64(4),
			})
			return nil
		},
	})
	return out
}

// TopClientsSince returns the top n clients within a time window.
func (db *DB) TopClientsSince(n int, since time.Time) []ClientSnapshot {
	db.mu.Lock()
	defer db.mu.Unlock()
	sinceHour := since.UTC().Truncate(time.Hour).Format("2006-01-02T15")
	var out []ClientSnapshot
	_ = sqlitex.Execute(db.conn, `
		SELECT client_ip,
			SUM(requests) as total_requests,
			SUM(blocked) as total_blocked,
			SUM(bytes_in) as total_bytes_in,
			SUM(bytes_out) as total_bytes_out
		FROM traffic_hourly
		WHERE hour >= ?
		GROUP BY client_ip
		ORDER BY total_requests DESC LIMIT ?
	`, &sqlitex.ExecOptions{
		Args: []any{sinceHour, n},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			out = append(out, ClientSnapshot{
				IP:       stmt.ColumnText(0),
				Requests: stmt.ColumnInt64(1),
				Blocked:  stmt.ColumnInt64(2),
				BytesIn:  stmt.ColumnInt64(3),
				BytesOut: stmt.ColumnInt64(4),
			})
			return nil
		},
	})
	return out
}

// TrafficTotalsSince returns aggregate traffic stats within a time window.
func (db *DB) TrafficTotalsSince(since time.Time) (requests, blocked, bytesIn, bytesOut int64) {
	db.mu.Lock()
	defer db.mu.Unlock()
	sinceHour := since.UTC().Truncate(time.Hour).Format("2006-01-02T15")
	_ = sqlitex.Execute(db.conn, `
		SELECT COALESCE(SUM(requests), 0),
			COALESCE(SUM(blocked), 0),
			COALESCE(SUM(bytes_in), 0),
			COALESCE(SUM(bytes_out), 0)
		FROM traffic_hourly
		WHERE hour >= ?
	`, &sqlitex.ExecOptions{
		Args: []any{sinceHour},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			requests = stmt.ColumnInt64(0)
			blocked = stmt.ColumnInt64(1)
			bytesIn = stmt.ColumnInt64(2)
			bytesOut = stmt.ColumnInt64(3)
			return nil
		},
	})
	return
}

// MergedTopBlocked returns the top n blocked domains by merging DB totals
// with unflushed in-memory deltas.
func (db *DB) MergedTopBlocked(n int) []DomainCount {
	db.mu.Lock()
	defer db.mu.Unlock()
	merged := make(map[string]int64)

	// DB cumulative totals (all rows, no limit).
	for _, dc := range db.allBlockedDomains() {
		merged[dc.Domain] = dc.Count
	}

	// Add only the unflushed delta from in-memory.
	for _, dc := range db.collector.SnapshotDomainBlocks() {
		delta := dc.Count - db.lastDomainBlks[dc.Domain]
		if delta > 0 {
			merged[dc.Domain] += delta
		}
	}

	return topNFromMap(merged, n)
}

// MergedTopRequested returns the top n requested domains by merging DB totals
// with unflushed in-memory deltas.
func (db *DB) MergedTopRequested(n int) []DomainCount {
	db.mu.Lock()
	defer db.mu.Unlock()
	merged := make(map[string]int64)

	// DB cumulative totals (all rows, no limit).
	for _, dc := range db.allDomainRequests() {
		merged[dc.Domain] = dc.Count
	}

	// Add only the unflushed delta from in-memory.
	for _, dc := range db.collector.SnapshotDomainRequests() {
		delta := dc.Count - db.lastDomainReqs[dc.Domain]
		if delta > 0 {
			merged[dc.Domain] += delta
		}
	}

	return topNFromMap(merged, n)
}

// MergedTopClients returns the top n clients by merging DB totals
// with unflushed in-memory deltas.
func (db *DB) MergedTopClients(n int) []ClientSnapshot {
	db.mu.Lock()
	defer db.mu.Unlock()
	merged := make(map[string]*ClientSnapshot)

	// DB cumulative totals.
	_ = sqlitex.Execute(db.conn, `
		SELECT client_ip,
			SUM(requests), SUM(blocked), SUM(bytes_in), SUM(bytes_out)
		FROM traffic_hourly
		GROUP BY client_ip
	`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			cs := ClientSnapshot{
				IP:       stmt.ColumnText(0),
				Requests: stmt.ColumnInt64(1),
				Blocked:  stmt.ColumnInt64(2),
				BytesIn:  stmt.ColumnInt64(3),
				BytesOut: stmt.ColumnInt64(4),
			}
			merged[cs.IP] = &cs
			return nil
		},
	})

	// Add only the unflushed deltas from in-memory.
	for _, cs := range db.collector.SnapshotClients() {
		prev := db.lastClients[cs.IP]
		dReqs := cs.Requests - prev.Requests
		dBlocked := cs.Blocked - prev.Blocked
		dIn := cs.BytesIn - prev.BytesIn
		dOut := cs.BytesOut - prev.BytesOut
		if existing, ok := merged[cs.IP]; ok {
			existing.Requests += dReqs
			existing.Blocked += dBlocked
			existing.BytesIn += dIn
			existing.BytesOut += dOut
		} else if dReqs > 0 || dIn > 0 || dOut > 0 {
			merged[cs.IP] = &ClientSnapshot{
				IP:       cs.IP,
				Requests: dReqs,
				Blocked:  dBlocked,
				BytesIn:  dIn,
				BytesOut: dOut,
			}
		}
	}

	// Sort by requests descending.
	var result []ClientSnapshot
	for _, cs := range merged {
		result = append(result, *cs)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Requests > result[j].Requests
	})

	if n > 0 && len(result) > n {
		result = result[:n]
	}

	return result
}

// TopAllowed returns the top n allowed domains from the database.
func (db *DB) TopAllowed(n int) []DomainCount {
	db.mu.Lock()
	defer db.mu.Unlock()
	var out []DomainCount
	_ = sqlitex.Execute(db.conn, `
		SELECT domain, count FROM allowed_domains
		ORDER BY count DESC LIMIT ?
	`, &sqlitex.ExecOptions{
		Args: []any{n},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			out = append(out, DomainCount{
				Domain: stmt.ColumnText(0),
				Count:  stmt.ColumnInt64(1),
			})
			return nil
		},
	})
	return out
}

// MergedTopAllowed returns the top n allowed domains by merging DB totals
// with unflushed in-memory deltas.
func (db *DB) MergedTopAllowed(n int) []DomainCount {
	db.mu.Lock()
	defer db.mu.Unlock()
	merged := make(map[string]int64)

	// DB cumulative totals.
	for _, dc := range db.allAllowedDomains() {
		merged[dc.Domain] = dc.Count
	}

	// Add only the unflushed delta from in-memory.
	if db.allowSnapshotFn != nil {
		for domain, count := range db.allowSnapshotFn() {
			delta := count - db.lastDomainAllows[domain]
			if delta > 0 {
				merged[domain] += delta
			}
		}
	}

	return topNFromMap(merged, n)
}

// allAllowedDomains returns all allowed domain counts (no limit).
func (db *DB) allAllowedDomains() []DomainCount {
	var out []DomainCount
	_ = sqlitex.Execute(db.conn, `
		SELECT domain, count FROM allowed_domains ORDER BY count DESC
	`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			out = append(out, DomainCount{
				Domain: stmt.ColumnText(0),
				Count:  stmt.ColumnInt64(1),
			})
			return nil
		},
	})
	return out
}

// topNFromMap extracts the top n entries from a domain->count map.
func topNFromMap(m map[string]int64, n int) []DomainCount {
	out := make([]DomainCount, 0, len(m))
	for domain, count := range m {
		out = append(out, DomainCount{Domain: domain, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Count > out[j].Count
	})
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// ensureSchema creates the stats tables.
func (db *DB) ensureSchema() error {
	return sqlitex.ExecuteScript(db.conn, `
		CREATE TABLE IF NOT EXISTS traffic_hourly (
			hour      TEXT NOT NULL,
			client_ip TEXT NOT NULL,
			requests  INTEGER NOT NULL DEFAULT 0,
			blocked   INTEGER NOT NULL DEFAULT 0,
			bytes_in  INTEGER NOT NULL DEFAULT 0,
			bytes_out INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (hour, client_ip)
		) WITHOUT ROWID;

		CREATE TABLE IF NOT EXISTS blocked_domains (
			domain TEXT NOT NULL PRIMARY KEY,
			count  INTEGER NOT NULL DEFAULT 0
		) WITHOUT ROWID;

		CREATE TABLE IF NOT EXISTS domain_requests (
			domain TEXT NOT NULL PRIMARY KEY,
			count  INTEGER NOT NULL DEFAULT 0
		) WITHOUT ROWID;

		CREATE TABLE IF NOT EXISTS allowed_domains (
			domain TEXT NOT NULL PRIMARY KEY,
			count  INTEGER NOT NULL DEFAULT 0
		) WITHOUT ROWID;

		CREATE INDEX IF NOT EXISTS idx_traffic_hourly_hour ON traffic_hourly(hour);
		CREATE INDEX IF NOT EXISTS idx_traffic_hourly_client ON traffic_hourly(client_ip);
	`, nil)
}

// allBlockedDomains returns all blocked domain counts (no limit).
func (db *DB) allBlockedDomains() []DomainCount {
	var out []DomainCount
	_ = sqlitex.Execute(db.conn, `
		SELECT domain, count FROM blocked_domains ORDER BY count DESC
	`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			out = append(out, DomainCount{
				Domain: stmt.ColumnText(0),
				Count:  stmt.ColumnInt64(1),
			})
			return nil
		},
	})
	return out
}

// allDomainRequests returns all domain request counts.
func (db *DB) allDomainRequests() []DomainCount {
	var out []DomainCount
	_ = sqlitex.Execute(db.conn, `
		SELECT domain, count FROM domain_requests ORDER BY count DESC
	`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			out = append(out, DomainCount{
				Domain: stmt.ColumnText(0),
				Count:  stmt.ColumnInt64(1),
			})
			return nil
		},
	})
	return out
}
