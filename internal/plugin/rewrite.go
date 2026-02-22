package plugin

import (
	"bytes"
	"log/slog"
	"net/http"
	"path"
	"regexp"
	"strings"
	"sync"
)

// RewriteRule defines a content rewrite rule.
type RewriteRule struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Pattern     string   `json:"pattern"`
	Replacement string   `json:"replacement"`
	IsRegex     bool     `json:"is_regex"`
	Domains     []string `json:"domains"`
	URLPatterns []string `json:"url_patterns"`
	Enabled     bool     `json:"enabled"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

// compiledRule is a pre-compiled version of a RewriteRule for fast matching.
type compiledRule struct {
	RewriteRule
	re *regexp.Regexp // nil for literal rules
}

// rewriteFilter implements ContentFilter with API-managed rewrite rules.
type rewriteFilter struct {
	name    string
	version string
	logger  *slog.Logger

	mu            sync.RWMutex
	compiledRules []compiledRule
	store         *RewriteStore
}

func init() {
	Registry["rewrite"] = func() ContentFilter {
		return &rewriteFilter{
			name:    "rewrite",
			version: "0.1.0",
		}
	}
}

func (f *rewriteFilter) Name() string    { return f.name }
func (f *rewriteFilter) Version() string { return f.version }

// Domains returns an empty list; the rewrite plugin gets its domains from
// config (defaults to all mitm.domains if not specified).
func (f *rewriteFilter) Domains() []string { return nil }

// Init opens the rule store and loads compiled rules into memory.
func (f *rewriteFilter) Init(cfg *PluginConfig, logger *slog.Logger) error {
	f.logger = logger

	dataDir, _ := cfg.Options["data_dir"].(string) //nolint:errcheck // optional
	if dataDir == "" {
		dataDir = "."
	}

	store, err := OpenRewriteStore(dataDir)
	if err != nil {
		return err
	}
	f.store = store

	return f.ReloadRules()
}

// Store returns the underlying RewriteStore for API handlers.
func (f *rewriteFilter) Store() *RewriteStore {
	return f.store
}

// ReloadRules queries the DB for all enabled rules, compiles patterns,
// and swaps the in-memory rule set under a write lock.
func (f *rewriteFilter) ReloadRules() error {
	rules, err := f.store.List()
	if err != nil {
		return err
	}

	var compiled []compiledRule
	for i := range rules {
		r := &rules[i]
		if !r.Enabled {
			continue
		}
		cr := compiledRule{RewriteRule: *r}
		if r.IsRegex {
			re, compileErr := regexp.Compile(r.Pattern)
			if compileErr != nil {
				f.logger.Warn("skipping rule with invalid regex",
					"rule", r.Name, "pattern", r.Pattern, "error", compileErr)
				continue
			}
			cr.re = re
		}
		compiled = append(compiled, cr)
	}

	f.mu.Lock()
	f.compiledRules = compiled
	f.mu.Unlock()

	f.logger.Debug("rewrite rules reloaded", "active_rules", len(compiled))
	return nil
}

// Close closes the underlying store.
func (f *rewriteFilter) Close() error {
	if f.store != nil {
		return f.store.Close()
	}
	return nil
}

// Filter applies rewrite rules to the response body.
func (f *rewriteFilter) Filter(req *http.Request, _ *http.Response, body []byte) ([]byte, FilterResult, error) {
	f.mu.RLock()
	rules := f.compiledRules
	f.mu.RUnlock()

	if len(rules) == 0 {
		return body, FilterResult{}, nil
	}

	domain := strings.ToLower(req.Host)
	urlPath := req.URL.Path

	current := body
	var firstRule string
	var totalCount int
	var matched bool
	var ruleMatches []RuleMatch

	for i := range rules {
		r := &rules[i]
		if !matchesDomain(r.Domains, domain) || !matchesURL(r.URLPatterns, urlPath) {
			continue
		}

		var replaced []byte
		var count int

		if r.re != nil {
			replaced, count = regexReplace(r.re, r.Replacement, current)
		} else {
			replaced, count = literalReplace(r.Pattern, r.Replacement, current)
		}

		if count > 0 {
			matched = true
			totalCount += count
			if firstRule == "" {
				firstRule = r.Name
			}
			ruleMatches = append(ruleMatches, RuleMatch{
				Rule:     r.Name,
				Count:    count,
				Modified: true,
			})
			current = replaced
		}
	}

	return current, FilterResult{
		Matched:  matched,
		Modified: !bytes.Equal(current, body),
		Rule:     firstRule,
		Removed:  totalCount,
		Rules:    ruleMatches,
	}, nil
}

// matchesDomain returns true if the domain matches the rule's domain list.
// Empty domain list matches all domains.
func matchesDomain(ruleDomains []string, domain string) bool {
	if len(ruleDomains) == 0 {
		return true
	}
	for _, d := range ruleDomains {
		if strings.EqualFold(d, domain) {
			return true
		}
	}
	return false
}

// matchesURL returns true if the URL path matches any of the rule's URL patterns.
// Empty pattern list matches all paths.
func matchesURL(patterns []string, urlPath string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, p := range patterns {
		if ok, _ := path.Match(p, urlPath); ok {
			return true
		}
	}
	return false
}

// literalReplace performs case-sensitive literal string replacement.
func literalReplace(pattern, replacement string, body []byte) (result []byte, count int) {
	old := []byte(pattern)
	repl := []byte(replacement)
	count = bytes.Count(body, old)
	if count == 0 {
		return body, 0
	}
	return bytes.ReplaceAll(body, old, repl), count
}

// regexReplace performs regex replacement on the body.
func regexReplace(re *regexp.Regexp, replacement string, body []byte) (result []byte, count int) {
	matches := re.FindAllIndex(body, -1)
	if len(matches) == 0 {
		return body, 0
	}
	result = re.ReplaceAll(body, []byte(replacement))
	return result, len(matches)
}
