package plugin

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/google/uuid"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// RewriteStore manages rewrite rule persistence in SQLite.
type RewriteStore struct {
	mu   sync.Mutex
	conn *sqlite.Conn
}

// OpenRewriteStore opens or creates the rewrite rules database.
func OpenRewriteStore(dataDir string) (*RewriteStore, error) {
	dbPath := dataDir + "/rewrite.db"
	conn, err := sqlite.OpenConn(dbPath, sqlite.OpenReadWrite|sqlite.OpenCreate)
	if err != nil {
		return nil, fmt.Errorf("open rewrite db: %w", err)
	}

	// Enable WAL mode for concurrent read access during writes.
	if err := sqlitex.ExecuteTransient(conn, "PRAGMA journal_mode=WAL", nil); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}

	s := &RewriteStore{conn: conn}
	if err := s.ensureSchema(); err != nil {
		_ = conn.Close()
		return nil, err
	}

	return s, nil
}

// Close closes the database connection.
func (s *RewriteStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.Close()
}

func (s *RewriteStore) ensureSchema() error {
	return sqlitex.ExecuteScript(s.conn, `
		CREATE TABLE IF NOT EXISTS rewrite_rules (
			id           TEXT PRIMARY KEY,
			name         TEXT NOT NULL,
			pattern      TEXT NOT NULL,
			replacement  TEXT NOT NULL DEFAULT '',
			is_regex     INTEGER NOT NULL DEFAULT 0,
			domains      TEXT NOT NULL DEFAULT '[]',
			url_patterns TEXT NOT NULL DEFAULT '[]',
			enabled      INTEGER NOT NULL DEFAULT 1,
			created_at   TEXT NOT NULL,
			updated_at   TEXT NOT NULL
		);
	`, nil)
}

// List returns all rewrite rules ordered by creation time.
func (s *RewriteStore) List() ([]RewriteRule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var rules []RewriteRule
	err := sqlitex.Execute(s.conn, `
		SELECT id, name, pattern, replacement, is_regex, domains, url_patterns, enabled, created_at, updated_at
		FROM rewrite_rules ORDER BY created_at ASC
	`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			r, scanErr := scanRule(stmt)
			if scanErr != nil {
				return scanErr
			}
			rules = append(rules, r)
			return nil
		},
	})
	if err != nil {
		return nil, fmt.Errorf("list rules: %w", err)
	}
	if rules == nil {
		rules = []RewriteRule{}
	}
	return rules, nil
}

// Get returns a single rule by ID.
func (s *RewriteStore) Get(id string) (RewriteRule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var rule RewriteRule
	var found bool
	err := sqlitex.Execute(s.conn, `
		SELECT id, name, pattern, replacement, is_regex, domains, url_patterns, enabled, created_at, updated_at
		FROM rewrite_rules WHERE id = ?
	`, &sqlitex.ExecOptions{
		Args: []any{id},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			r, scanErr := scanRule(stmt)
			if scanErr != nil {
				return scanErr
			}
			rule = r
			found = true
			return nil
		},
	})
	if err != nil {
		return RewriteRule{}, fmt.Errorf("get rule: %w", err)
	}
	if !found {
		return RewriteRule{}, fmt.Errorf("rule %q not found", id)
	}
	return rule, nil
}

// Add creates a new rule. Validates the pattern and returns the created rule.
//nolint:gocritic // hugeParam: value copy intentional — we mutate ID/timestamps before returning
func (s *RewriteStore) Add(rule RewriteRule) (RewriteRule, error) {
	if err := validateRule(&rule); err != nil {
		return RewriteRule{}, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	rule.ID = uuid.New().String()
	rule.CreatedAt = now
	rule.UpdatedAt = now

	domainsJSON, _ := json.Marshal(rule.Domains)       //nolint:errcheck // string slice always marshals
	urlPatternsJSON, _ := json.Marshal(rule.URLPatterns) //nolint:errcheck // string slice always marshals

	s.mu.Lock()
	defer s.mu.Unlock()

	err := sqlitex.Execute(s.conn, `
		INSERT INTO rewrite_rules (id, name, pattern, replacement, is_regex, domains, url_patterns, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, &sqlitex.ExecOptions{
		Args: []any{
			rule.ID, rule.Name, rule.Pattern, rule.Replacement,
			boolToInt(rule.IsRegex), string(domainsJSON), string(urlPatternsJSON),
			boolToInt(rule.Enabled), rule.CreatedAt, rule.UpdatedAt,
		},
	})
	if err != nil {
		return RewriteRule{}, fmt.Errorf("add rule: %w", err)
	}
	return rule, nil
}

// Update replaces a rule's fields. Validates the pattern and returns the updated rule.
//nolint:gocritic // hugeParam: value copy intentional — we mutate timestamps before returning
func (s *RewriteStore) Update(id string, rule RewriteRule) (RewriteRule, error) {
	if err := validateRule(&rule); err != nil {
		return RewriteRule{}, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	domainsJSON, _ := json.Marshal(rule.Domains)       //nolint:errcheck // string slice always marshals
	urlPatternsJSON, _ := json.Marshal(rule.URLPatterns) //nolint:errcheck // string slice always marshals

	s.mu.Lock()
	defer s.mu.Unlock()

	err := sqlitex.Execute(s.conn, `
		UPDATE rewrite_rules SET name=?, pattern=?, replacement=?, is_regex=?, domains=?, url_patterns=?, enabled=?, updated_at=?
		WHERE id=?
	`, &sqlitex.ExecOptions{
		Args: []any{
			rule.Name, rule.Pattern, rule.Replacement,
			boolToInt(rule.IsRegex), string(domainsJSON), string(urlPatternsJSON),
			boolToInt(rule.Enabled), now, id,
		},
	})
	if err != nil {
		return RewriteRule{}, fmt.Errorf("update rule: %w", err)
	}

	if s.conn.Changes() == 0 {
		return RewriteRule{}, fmt.Errorf("rule %q not found", id)
	}

	rule.ID = id
	rule.UpdatedAt = now
	return rule, nil
}

// Delete removes a rule by ID.
func (s *RewriteStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	err := sqlitex.Execute(s.conn, `DELETE FROM rewrite_rules WHERE id=?`, &sqlitex.ExecOptions{
		Args: []any{id},
	})
	if err != nil {
		return fmt.Errorf("delete rule: %w", err)
	}
	if s.conn.Changes() == 0 {
		return fmt.Errorf("rule %q not found", id)
	}
	return nil
}

// Toggle flips the enabled state of a rule and returns the updated rule.
func (s *RewriteStore) Toggle(id string) (RewriteRule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	err := sqlitex.Execute(s.conn, `
		UPDATE rewrite_rules SET enabled = 1 - enabled, updated_at = ? WHERE id = ?
	`, &sqlitex.ExecOptions{
		Args: []any{time.Now().UTC().Format(time.RFC3339), id},
	})
	if err != nil {
		return RewriteRule{}, fmt.Errorf("toggle rule: %w", err)
	}
	if s.conn.Changes() == 0 {
		return RewriteRule{}, fmt.Errorf("rule %q not found", id)
	}

	// Read back the toggled rule.
	var rule RewriteRule
	err = sqlitex.Execute(s.conn, `
		SELECT id, name, pattern, replacement, is_regex, domains, url_patterns, enabled, created_at, updated_at
		FROM rewrite_rules WHERE id = ?
	`, &sqlitex.ExecOptions{
		Args: []any{id},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			r, scanErr := scanRule(stmt)
			if scanErr != nil {
				return scanErr
			}
			rule = r
			return nil
		},
	})
	if err != nil {
		return RewriteRule{}, fmt.Errorf("read toggled rule: %w", err)
	}
	return rule, nil
}

// scanRule reads a rule from a query result row.
func scanRule(stmt *sqlite.Stmt) (RewriteRule, error) {
	var domains, urlPatterns []string
	if err := json.Unmarshal([]byte(stmt.ColumnText(5)), &domains); err != nil {
		return RewriteRule{}, fmt.Errorf("parse domains: %w", err)
	}
	if err := json.Unmarshal([]byte(stmt.ColumnText(6)), &urlPatterns); err != nil {
		return RewriteRule{}, fmt.Errorf("parse url_patterns: %w", err)
	}
	if domains == nil {
		domains = []string{}
	}
	if urlPatterns == nil {
		urlPatterns = []string{}
	}
	return RewriteRule{
		ID:          stmt.ColumnText(0),
		Name:        stmt.ColumnText(1),
		Pattern:     stmt.ColumnText(2),
		Replacement: stmt.ColumnText(3),
		IsRegex:     stmt.ColumnInt64(4) != 0,
		Domains:     domains,
		URLPatterns: urlPatterns,
		Enabled:     stmt.ColumnInt64(7) != 0,
		CreatedAt:   stmt.ColumnText(8),
		UpdatedAt:   stmt.ColumnText(9),
	}, nil
}

// validateRule checks required fields and pattern validity.
func validateRule(r *RewriteRule) error {
	if r.Name == "" {
		return fmt.Errorf("name is required")
	}
	if len(r.Name) > 200 {
		return fmt.Errorf("name must be 200 characters or fewer")
	}
	if r.Pattern == "" {
		return fmt.Errorf("pattern is required")
	}
	if r.IsRegex {
		if _, err := regexp.Compile(r.Pattern); err != nil {
			return fmt.Errorf("invalid regex: %w", err)
		}
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
