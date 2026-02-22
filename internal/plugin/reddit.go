package plugin

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
)

// redditFilter strips promoted/sponsored content from Reddit responses.
// It handles two content types:
//   - HTML (www.reddit.com): strips Shreddit custom elements for feed ads,
//     comment-tree ads, and right-rail promoted posts.
//   - JSON (gql-fed.reddit.com): strips promoted entries from GraphQL
//     responses used by the Reddit iOS app.
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
			version: "0.3.0",
			domains: []string{"www.reddit.com", "gql-fed.reddit.com"},
		}
	}
}

func (r *redditFilter) Name() string      { return r.name }
func (r *redditFilter) Version() string   { return r.version }
func (r *redditFilter) Domains() []string { return r.domains }

func (r *redditFilter) Init(cfg *PluginConfig, logger *slog.Logger) error {
	r.placeholder = cfg.Placeholder
	r.logger = logger
	if len(cfg.Domains) > 0 {
		r.domains = cfg.Domains
	}
	return nil
}

// Filter inspects a response and removes promoted content. HTML responses
// go through the Shreddit element removal rules; JSON responses go through
// GraphQL operation-specific filters.
func (r *redditFilter) Filter(req *http.Request, resp *http.Response, body []byte) ([]byte, FilterResult, error) {
	ct := resp.Header.Get("Content-Type")

	if isJSONContentType(ct) {
		return r.filterJSON(req, resp, body)
	}

	return r.filterHTML(req, resp, body)
}

// filterHTML handles server-rendered Shreddit HTML responses (www.reddit.com).
func (r *redditFilter) filterHTML(req *http.Request, _ *http.Response, body []byte) ([]byte, FilterResult, error) {
	path := req.URL.Path

	// R6: URL-scoped processing.
	if !r.shouldProcess(path) {
		return body, FilterResult{}, nil
	}

	// R5: Quick-skip — if no ad markers present, return immediately.
	if !containsAdMarker(body) {
		return body, FilterResult{}, nil
	}

	ct := "text/html"
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

// filterJSON handles GraphQL API responses (gql-fed.reddit.com) used by
// the Reddit iOS app. Dispatches by X-Apollo-Operation-Name header.
func (r *redditFilter) filterJSON(req *http.Request, _ *http.Response, body []byte) ([]byte, FilterResult, error) {
	op := req.Header.Get("X-Apollo-Operation-Name")

	switch op {
	case "HomeFeedSdui":
		return r.filterHomeFeed(body)
	case "FeedPostDetailsByIds":
		return r.filterFeedDetails(body)
	case "PdpCommentsAds":
		return r.filterPdpAds(body)
	default:
		return body, FilterResult{}, nil
	}
}

// filterHomeFeed removes ad edges from the SDUI home feed response.
// Detection: edges[i].node.adPayload != null.
func (r *redditFilter) filterHomeFeed(body []byte) ([]byte, FilterResult, error) {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return body, FilterResult{}, nil // fail open
	}

	edges, ok := jsonPath[[]any](doc, "data", "homeV3", "elements", "edges")
	if !ok || len(edges) == 0 {
		return body, FilterResult{}, nil
	}

	// JSON responses always strip without placeholders — structured API
	// consumers (the Reddit iOS app) cannot render arbitrary objects.
	var filtered []any
	var removed int

	for _, edge := range edges {
		em, ok := edge.(map[string]any)
		if !ok {
			filtered = append(filtered, edge)
			continue
		}
		node, _ := em["node"].(map[string]any)
		if node == nil {
			filtered = append(filtered, edge)
			continue
		}
		if node["adPayload"] != nil {
			removed++
			continue
		}
		filtered = append(filtered, edge)
	}

	if removed == 0 {
		return body, FilterResult{}, nil
	}

	// Update the edges array (path existence validated by jsonPath above).
	data := doc["data"].(map[string]any)       //nolint:errcheck // checked
	home := data["homeV3"].(map[string]any)     //nolint:errcheck // checked
	elements := home["elements"].(map[string]any) //nolint:errcheck // checked
	elements["edges"] = filtered

	// Update endCursor if the last original edge was an ad.
	r.updateEndCursor(edges, filtered, elements)

	out, err := json.Marshal(doc)
	if err != nil {
		return body, FilterResult{}, nil // fail open
	}

	return out, FilterResult{
		Matched:  true,
		Modified: true,
		Rule:     "feed-sdui-ad",
		Removed:  removed,
	}, nil
}

// updateEndCursor fixes pageInfo.endCursor when the last original edge was
// removed. The cursor must point to the last remaining organic edge's groupId.
func (r *redditFilter) updateEndCursor(original, filtered []any, elements map[string]any) {
	if len(original) == 0 || len(filtered) == 0 {
		return
	}

	// Check if the original last edge was an ad.
	lastOrig, _ := original[len(original)-1].(map[string]any)
	if lastOrig == nil {
		return
	}
	lastNode, _ := lastOrig["node"].(map[string]any)
	if lastNode == nil || lastNode["adPayload"] == nil {
		return // last edge was organic, cursor is fine
	}

	// Find the last remaining organic edge's groupId.
	for i := len(filtered) - 1; i >= 0; i-- {
		em, _ := filtered[i].(map[string]any)
		if em == nil {
			continue
		}
		node, _ := em["node"].(map[string]any)
		if node == nil {
			continue
		}
		groupID, _ := node["groupId"].(string)
		if groupID == "" {
			continue
		}

		pageInfo, _ := elements["pageInfo"].(map[string]any)
		if pageInfo != nil {
			pageInfo["endCursor"] = base64.StdEncoding.EncodeToString([]byte(groupID))
		}
		return
	}
}

// filterFeedDetails removes ad posts from the FeedPostDetailsByIds response.
// Detection: postsInfoByIds[i].__typename == "ProfilePost".
func (r *redditFilter) filterFeedDetails(body []byte) ([]byte, FilterResult, error) {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return body, FilterResult{}, nil
	}

	posts, ok := jsonPath[[]any](doc, "data", "postsInfoByIds")
	if !ok || len(posts) == 0 {
		return body, FilterResult{}, nil
	}

	var filtered []any
	var removed int

	for _, post := range posts {
		pm, ok := post.(map[string]any)
		if !ok {
			filtered = append(filtered, post)
			continue
		}
		if pm["__typename"] == "ProfilePost" {
			removed++
			continue
		}
		filtered = append(filtered, post)
	}

	if removed == 0 {
		return body, FilterResult{}, nil
	}

	doc["data"].(map[string]any)["postsInfoByIds"] = filtered //nolint:errcheck // path validated by jsonPath above

	out, err := json.Marshal(doc)
	if err != nil {
		return body, FilterResult{}, nil
	}

	return out, FilterResult{
		Matched:  true,
		Modified: true,
		Rule:     "feed-details-ad",
		Removed:  removed,
	}, nil
}

// filterPdpAds empties the adPosts array in PdpCommentsAds responses.
// Detection: data.postInfoById.pdpCommentsAds.adPosts is non-empty.
func (r *redditFilter) filterPdpAds(body []byte) ([]byte, FilterResult, error) {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return body, FilterResult{}, nil
	}

	adPosts, ok := jsonPath[[]any](doc, "data", "postInfoById", "pdpCommentsAds", "adPosts")
	if !ok || len(adPosts) == 0 {
		return body, FilterResult{}, nil
	}

	removed := len(adPosts)

	// Empty the array (path existence validated by jsonPath above).
	data := doc["data"].(map[string]any)              //nolint:errcheck // checked
	post := data["postInfoById"].(map[string]any)     //nolint:errcheck // checked
	pdpAds := post["pdpCommentsAds"].(map[string]any) //nolint:errcheck // checked
	pdpAds["adPosts"] = []any{}

	out, err := json.Marshal(doc)
	if err != nil {
		return body, FilterResult{}, nil
	}

	return out, FilterResult{
		Matched:  true,
		Modified: true,
		Rule:     "pdp-comments-ad",
		Removed:  removed,
	}, nil
}

// jsonPath navigates a nested map structure and returns the value at the
// given key path, type-asserted to T. Returns (zero, false) if any step
// fails.
func jsonPath[T any](doc map[string]any, keys ...string) (T, bool) {
	var zero T
	current := any(doc)
	for i, k := range keys {
		m, ok := current.(map[string]any)
		if !ok {
			return zero, false
		}
		v, ok := m[k]
		if !ok {
			return zero, false
		}
		if i == len(keys)-1 {
			result, ok := v.(T)
			return result, ok
		}
		current = v
	}
	return zero, false
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
