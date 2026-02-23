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
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Pattern      string   `json:"pattern"`
	Replacement  string   `json:"replacement"`
	IsRegex      bool     `json:"is_regex"`
	Domains      []string `json:"domains"`
	URLPatterns  []string `json:"url_patterns"`
	ContentTypes []string `json:"content_types"`
	Enabled      bool     `json:"enabled"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
}

// defaultSafeContentTypes is the set of content types that are safe
// for text replacement. Used when a rule has no explicit ContentTypes.
var defaultSafeContentTypes = map[string]struct{}{
	"text/html":  {},
	"text/plain": {},
}

// compiledRule is a pre-compiled version of a RewriteRule for fast matching.
type compiledRule struct {
	RewriteRule
	re           *regexp.Regexp        // nil for literal rules
	contentTypes map[string]struct{}   // resolved from ContentTypes or defaults
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
		if len(r.ContentTypes) > 0 {
			cr.contentTypes = make(map[string]struct{}, len(r.ContentTypes))
			for _, ct := range r.ContentTypes {
				cr.contentTypes[strings.ToLower(strings.TrimSpace(ct))] = struct{}{}
			}
		} else {
			cr.contentTypes = defaultSafeContentTypes
		}
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
func (f *rewriteFilter) Filter(req *http.Request, resp *http.Response, body []byte) ([]byte, FilterResult, error) {
	f.mu.RLock()
	rules := f.compiledRules
	f.mu.RUnlock()

	if len(rules) == 0 {
		return body, FilterResult{}, nil
	}

	ct := normalizeContentType(resp.Header.Get("Content-Type"))
	isHTML := ct == "text/html"
	domain := strings.ToLower(req.Host)
	urlPath := req.URL.Path

	current := body
	var protected []protectedRange
	if isHTML {
		protected = findProtectedRanges(current)
	}

	var firstRule string
	var totalCount int
	var matched bool
	var ruleMatches []RuleMatch

	for i := range rules {
		r := &rules[i]
		if !matchesContentType(r.contentTypes, ct) {
			continue
		}
		if !matchesDomain(r.Domains, domain) || !matchesURL(r.URLPatterns, urlPath) {
			continue
		}

		var replaced []byte
		var count int

		if isHTML && len(protected) > 0 {
			if r.re != nil {
				replaced, count = htmlSafeRegexReplace(r.re, r.Replacement, current, protected)
			} else {
				replaced, count = htmlSafeLiteralReplace(r.Pattern, r.Replacement, current, protected)
			}
		} else {
			if r.re != nil {
				replaced, count = regexReplace(r.re, r.Replacement, current)
			} else {
				replaced, count = literalReplace(r.Pattern, r.Replacement, current)
			}
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
			if isHTML {
				protected = findProtectedRanges(current)
			}
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

// normalizeContentType extracts the media type from a Content-Type header,
// stripping parameters like charset.
func normalizeContentType(ct string) string {
	if idx := strings.IndexByte(ct, ';'); idx >= 0 {
		ct = ct[:idx]
	}
	return strings.ToLower(strings.TrimSpace(ct))
}

// matchesContentType checks whether the response content type is in the
// rule's allowed set.
func matchesContentType(allowed map[string]struct{}, ct string) bool {
	_, ok := allowed[ct]
	return ok
}

// protectedRange represents a byte range that should not be modified
// (e.g., <script>...</script> or <style>...</style> blocks in HTML).
type protectedRange struct {
	start, end int
}

// findProtectedRanges finds all <script>...</script> and <style>...</style>
// byte ranges in the body.
func findProtectedRanges(body []byte) []protectedRange {
	var ranges []protectedRange
	lower := bytes.ToLower(body)
	ranges = appendTagRanges(ranges, lower, body, []byte("<script"), []byte("</script"))
	ranges = appendTagRanges(ranges, lower, body, []byte("<style"), []byte("</style"))
	return ranges
}

// appendTagRanges scans lowered for open/close tag pairs and appends the
// byte ranges (using original body offsets) to ranges.
func appendTagRanges(ranges []protectedRange, lowered, _, openTag, closeTag []byte) []protectedRange {
	search := lowered
	offset := 0
	for {
		idx := bytes.Index(search, openTag)
		if idx < 0 {
			break
		}
		// Verify the character after the tag name is '>' or whitespace (not a
		// prefix match like "<scripted").
		tagEnd := idx + len(openTag)
		if tagEnd < len(search) {
			ch := search[tagEnd]
			if ch != '>' && ch != ' ' && ch != '\t' && ch != '\n' && ch != '\r' && ch != '/' {
				search = search[tagEnd:]
				offset += tagEnd
				continue
			}
		}

		closeIdx := bytes.Index(search[tagEnd:], closeTag)
		if closeIdx < 0 {
			// Unclosed tag — protect the rest of the body.
			ranges = append(ranges, protectedRange{start: offset + idx, end: offset + len(search)})
			break
		}
		// Find the '>' that closes the </script> or </style> tag.
		closeStart := tagEnd + closeIdx
		closeEnd := closeStart + len(closeTag)
		gt := bytes.IndexByte(search[closeEnd:], '>')
		if gt >= 0 {
			closeEnd += gt + 1
		} else {
			closeEnd = len(search)
		}
		ranges = append(ranges, protectedRange{start: offset + idx, end: offset + closeEnd})
		search = search[closeEnd:]
		offset += closeEnd
	}
	return ranges
}

// isInProtectedRange checks whether a byte offset falls inside any
// protected range.
func isInProtectedRange(pos int, protected []protectedRange) bool {
	for i := range protected {
		if pos >= protected[i].start && pos < protected[i].end {
			return true
		}
		if pos < protected[i].start {
			break // ranges are ordered
		}
	}
	return false
}

// htmlSafeLiteralReplace performs literal replacement, skipping matches
// that fall inside protected ranges.
func htmlSafeLiteralReplace(pattern, replacement string, body []byte, protected []protectedRange) (result []byte, count int) {
	old := []byte(pattern)
	repl := []byte(replacement)
	var buf bytes.Buffer
	remaining := body
	offset := 0

	for {
		idx := bytes.Index(remaining, old)
		if idx < 0 {
			break
		}
		absPos := offset + idx
		if isInProtectedRange(absPos, protected) {
			// Skip this match — write up to and including the match unchanged.
			buf.Write(remaining[:idx+len(old)])
			remaining = remaining[idx+len(old):]
			offset = absPos + len(old)
			continue
		}
		buf.Write(remaining[:idx])
		buf.Write(repl)
		count++
		remaining = remaining[idx+len(old):]
		offset = absPos + len(old)
	}

	if count == 0 {
		return body, 0
	}
	buf.Write(remaining)
	return buf.Bytes(), count
}

// htmlSafeRegexReplace performs regex replacement, skipping matches
// that fall inside protected ranges.
func htmlSafeRegexReplace(re *regexp.Regexp, replacement string, body []byte, protected []protectedRange) (result []byte, count int) {
	matches := re.FindAllIndex(body, -1)
	if len(matches) == 0 {
		return body, 0
	}

	repl := []byte(replacement)
	var buf bytes.Buffer
	prev := 0

	for _, m := range matches {
		if isInProtectedRange(m[0], protected) {
			continue
		}
		buf.Write(body[prev:m[0]])
		// Expand capture group references ($1, $2, etc.) in the replacement.
		buf.Write(re.Expand(nil, repl, body, m))
		count++
		prev = m[1]
	}

	if count == 0 {
		return body, 0
	}
	buf.Write(body[prev:])
	return buf.Bytes(), count
}
