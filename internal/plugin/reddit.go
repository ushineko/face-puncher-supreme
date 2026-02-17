package plugin

import (
	"bytes"
	"log/slog"
	"net/http"
	"strings"
)

// redditFilter strips promoted/sponsored content from Reddit's Shreddit UI.
// It operates on server-rendered HTML responses, targeting three ad surfaces:
// feed ads, comment-tree ads, and right-rail promoted posts.
type redditFilter struct {
	name        string
	version     string
	domains     []string
	placeholder string
	logger      *slog.Logger
}

func init() {
	Registry["reddit-promotions"] = func() ContentFilter {
		return &redditFilter{
			name:    "reddit-promotions",
			version: "0.2.0",
			domains: []string{"www.reddit.com"},
		}
	}
}

func (r *redditFilter) Name() string      { return r.name }
func (r *redditFilter) Version() string   { return r.version }
func (r *redditFilter) Domains() []string { return r.domains }

func (r *redditFilter) Init(cfg PluginConfig, logger *slog.Logger) error {
	r.placeholder = cfg.Placeholder
	r.logger = logger
	if len(cfg.Domains) > 0 {
		r.domains = cfg.Domains
	}
	return nil
}

// Filter inspects an HTML response and removes promoted content.
func (r *redditFilter) Filter(req *http.Request, resp *http.Response, body []byte) ([]byte, FilterResult, error) {
	path := req.URL.Path

	// R6: URL-scoped processing.
	if !r.shouldProcess(path) {
		return body, FilterResult{}, nil
	}

	// R5: Quick-skip — if no ad markers present, return immediately.
	if !containsAdMarker(body) {
		return body, FilterResult{}, nil
	}

	ct := resp.Header.Get("Content-Type")
	var totalRemoved int
	var firstRule string
	result := body

	// R1: Feed ads.
	result, n := removeElements(result, "<shreddit-ad-post", "</shreddit-ad-post>",
		Marker(r.placeholder, r.name, "feed-ad", ct))
	if n > 0 {
		totalRemoved += n
		firstRule = "feed-ad"
	}

	// R2: Comment-tree ad containers (remove entire wrapper).
	result, n = removeElements(result, "<shreddit-comment-tree-ads", "</shreddit-comment-tree-ads>",
		Marker(r.placeholder, r.name, "comment-tree-ad", ct))
	if n > 0 {
		totalRemoved += n
		if firstRule == "" {
			firstRule = "comment-tree-ad"
		}
	}

	// R3: Standalone comment-page ads (not inside tree-ads, already removed above).
	result, n = removeElements(result, "<shreddit-comments-page-ad", "</shreddit-comments-page-ad>",
		Marker(r.placeholder, r.name, "comment-page-ad", ct))
	if n > 0 {
		totalRemoved += n
		if firstRule == "" {
			firstRule = "comment-page-ad"
		}
	}

	// R4: Right-rail promoted posts (ad-event-tracker wrappers).
	result, n = removeElements(result, "<ad-event-tracker", "</ad-event-tracker>",
		Marker(r.placeholder, r.name, "right-rail-ad", ct))
	if n > 0 {
		totalRemoved += n
		if firstRule == "" {
			firstRule = "right-rail-ad"
		}
	}

	modified := totalRemoved > 0
	return result, FilterResult{
		Matched:  true, // we passed the containsAdMarker check
		Modified: modified,
		Rule:     firstRule,
		Removed:  totalRemoved,
	}, nil
}

// shouldProcess returns true if the URL path matches a pattern that carries ad content.
func (r *redditFilter) shouldProcess(path string) bool {
	// Homepage.
	if path == "/" {
		return true
	}

	// Feed partials.
	if strings.HasPrefix(path, "/svc/shreddit/feeds/") {
		return true
	}

	// Comment partials.
	if strings.HasPrefix(path, "/svc/shreddit/comments/") {
		return true
	}

	// More-comments partials (can contain injected ads).
	if strings.HasPrefix(path, "/svc/shreddit/more-comments/") {
		return true
	}

	// Right-rail partials.
	if strings.HasPrefix(path, "/svc/shreddit/pdp-right-rail/") {
		return true
	}

	// Subreddit community feed partials.
	if strings.HasPrefix(path, "/svc/shreddit/community-more-posts/") {
		return true
	}

	// Post detail pages: /r/<sub>/comments/<id>/...
	if strings.HasPrefix(path, "/r/") && strings.Contains(path, "/comments/") {
		return true
	}

	// Subreddit listing pages: /r/<sub>/
	if strings.HasPrefix(path, "/r/") {
		return true
	}

	return false
}

// containsAdMarker checks for any of the definitive ad marker strings.
func containsAdMarker(body []byte) bool {
	return bytes.Contains(body, []byte("shreddit-ad-post")) ||
		bytes.Contains(body, []byte("shreddit-comments-page-ad")) ||
		bytes.Contains(body, []byte("<ad-event-tracker"))
}

// removeElements finds and removes all instances of elements delimited by
// openTag (opening tag prefix, e.g. "<shreddit-ad-post") and closeTag
// (full closing tag, e.g. "</shreddit-ad-post>"). Each removed element is
// replaced with the placeholder string. Returns the modified body and the
// count of elements removed.
func removeElements(body []byte, openTag, closeTag, placeholder string) (modified []byte, count int) {
	open := []byte(openTag)
	closeB := []byte(closeTag)
	ph := []byte(placeholder)

	for {
		start := bytes.Index(body, open)
		if start < 0 {
			break
		}
		end := bytes.Index(body[start:], closeB)
		if end < 0 {
			break // malformed HTML, bail out
		}
		end = start + end + len(closeB)

		// Build new body: before + placeholder + after.
		var buf bytes.Buffer
		buf.Grow(len(body) - (end - start) + len(ph))
		buf.Write(body[:start])
		buf.Write(ph)
		buf.Write(body[end:])
		body = buf.Bytes()
		count++
	}

	return body, count
}
