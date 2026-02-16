/*
Package blocklist manages a domain blocklist backed by SQLite with an
in-memory cache for O(1) runtime lookups.

The SQLite database is the persistent store. At startup, all domains are
loaded into a map[string]struct{} for fast matching. The database is
rebuilt when blocklist URLs are fetched via Update.
*/
package blocklist

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// BlockedEntry tracks per-domain block counts.
type BlockedEntry struct {
	Domain string `json:"domain"`
	Count  int64  `json:"count"`
}

// sourceInfo tracks metadata about a single blocklist source.
type sourceInfo struct {
	url   string
	count int
}

// DB manages the blocklist database and in-memory cache.
type DB struct {
	conn   *sqlite.Conn
	logger *slog.Logger

	mu      sync.RWMutex
	domains map[string]struct{}

	// Block statistics.
	blocksTotal atomic.Int64
	blockCounts sync.Map // domain -> *atomic.Int64

	sourceCount int
}

// Open opens or creates a blocklist database at the given path and loads
// all domains into memory. Pass ":memory:" for a transient in-memory DB.
func Open(dbPath string, logger *slog.Logger) (*DB, error) {
	conn, err := sqlite.OpenConn(dbPath, sqlite.OpenReadWrite|sqlite.OpenCreate)
	if err != nil {
		return nil, fmt.Errorf("open blocklist db: %w", err)
	}

	db := &DB{
		conn:    conn,
		logger:  logger,
		domains: make(map[string]struct{}),
	}

	if err := db.ensureSchema(); err != nil {
		_ = conn.Close()
		return nil, err
	}

	if err := db.loadCache(); err != nil {
		_ = conn.Close()
		return nil, err
	}

	return db, nil
}

// Close closes the underlying database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}

// IsBlocked returns true if the domain (case-insensitive) is in the blocklist.
// It also increments block counters.
func (db *DB) IsBlocked(domain string) bool {
	domain = strings.ToLower(domain)

	db.mu.RLock()
	_, blocked := db.domains[domain]
	db.mu.RUnlock()

	if blocked {
		db.blocksTotal.Add(1)
		val, _ := db.blockCounts.LoadOrStore(domain, &atomic.Int64{})
		if counter, ok := val.(*atomic.Int64); ok {
			counter.Add(1)
		}
	}

	return blocked
}

// BlocksTotal returns the total number of blocked requests since startup.
func (db *DB) BlocksTotal() int64 {
	return db.blocksTotal.Load()
}

// Size returns the number of domains in the blocklist.
func (db *DB) Size() int {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return len(db.domains)
}

// SourceCount returns the number of configured blocklist sources.
func (db *DB) SourceCount() int {
	return db.sourceCount
}

// TopBlocked returns the top n blocked domains by count.
func (db *DB) TopBlocked(n int) []BlockedEntry {
	var entries []BlockedEntry

	db.blockCounts.Range(func(key, value any) bool {
		domain, ok := key.(string)
		if !ok {
			return true
		}
		counter, ok := value.(*atomic.Int64)
		if !ok {
			return true
		}
		entries = append(entries, BlockedEntry{
			Domain: domain,
			Count:  counter.Load(),
		})
		return true
	})

	// Sort descending by count (insertion sort is fine for small n).
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j].Count > entries[j-1].Count; j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}

	if len(entries) > n {
		entries = entries[:n]
	}

	return entries
}

// Update downloads blocklists from the given URLs, parses them, and
// rebuilds the database. This replaces all existing domain data.
func (db *DB) Update(urls []string, fetchFn FetchFunc) error {
	var allDomains []string
	var sources []sourceInfo

	for _, u := range urls {
		db.logger.Info("fetching blocklist", "url", u)

		domains, err := fetchFn(u)
		if err != nil {
			db.logger.Error("failed to fetch blocklist", "url", u, "error", err)
			continue
		}

		db.logger.Info("parsed blocklist", "url", u, "domains", len(domains))
		sources = append(sources, sourceInfo{url: u, count: len(domains)})
		allDomains = append(allDomains, domains...)
	}

	if err := db.rebuildDB(allDomains, sources); err != nil {
		return fmt.Errorf("rebuild blocklist db: %w", err)
	}

	if err := db.loadCache(); err != nil {
		return fmt.Errorf("reload cache: %w", err)
	}

	db.sourceCount = len(sources)
	db.logger.Info("blocklist updated",
		"domains", db.Size(),
		"sources", len(sources),
	)

	return nil
}

// ensureSchema creates the database tables if they don't exist.
func (db *DB) ensureSchema() error {
	return sqlitex.ExecuteScript(db.conn, `
		CREATE TABLE IF NOT EXISTS domains (
			domain TEXT NOT NULL PRIMARY KEY
		) WITHOUT ROWID;

		CREATE TABLE IF NOT EXISTS sources (
			url     TEXT NOT NULL PRIMARY KEY,
			fetched TEXT NOT NULL,
			count   INTEGER NOT NULL
		) WITHOUT ROWID;
	`, nil)
}

// loadCache reads all domains from SQLite into the in-memory map.
func (db *DB) loadCache() error {
	newDomains := make(map[string]struct{})

	err := sqlitex.Execute(db.conn, "SELECT domain FROM domains", &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			newDomains[stmt.ColumnText(0)] = struct{}{}
			return nil
		},
	})
	if err != nil {
		return fmt.Errorf("load domains from db: %w", err)
	}

	// Count sources.
	var sourceCount int
	err = sqlitex.Execute(db.conn, "SELECT COUNT(*) FROM sources", &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			sourceCount = stmt.ColumnInt(0)
			return nil
		},
	})
	if err != nil {
		return fmt.Errorf("count sources: %w", err)
	}

	db.mu.Lock()
	db.domains = newDomains
	db.mu.Unlock()
	db.sourceCount = sourceCount

	return nil
}

// rebuildDB replaces the domains table contents in a transaction.
func (db *DB) rebuildDB(domains []string, sources []sourceInfo) (err error) {
	defer sqlitex.Save(db.conn)(&err)

	// Clear existing data. Assignments use named return err for deferred Save.
	if err = sqlitex.Execute(db.conn, "DELETE FROM domains", nil); err != nil { //nolint:gocritic // named return for sqlitex.Save
		return err
	}
	if err = sqlitex.Execute(db.conn, "DELETE FROM sources", nil); err != nil { //nolint:gocritic // named return for sqlitex.Save
		return err
	}

	// Deduplicate and insert domains.
	seen := make(map[string]struct{}, len(domains))
	for _, d := range domains {
		d = strings.ToLower(d)
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}

		err = sqlitex.Execute(db.conn,
			"INSERT INTO domains (domain) VALUES (?)",
			&sqlitex.ExecOptions{
				Args: []any{d},
			})
		if err != nil {
			return fmt.Errorf("insert domain %q: %w", d, err)
		}
	}

	// Insert source metadata.
	for _, s := range sources {
		err = sqlitex.Execute(db.conn,
			"INSERT OR REPLACE INTO sources (url, fetched, count) VALUES (?, datetime('now'), ?)",
			&sqlitex.ExecOptions{
				Args: []any{s.url, s.count},
			})
		if err != nil {
			return fmt.Errorf("insert source %q: %w", s.url, err)
		}
	}

	return nil
}
