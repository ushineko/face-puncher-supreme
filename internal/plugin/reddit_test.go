package plugin

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loadFixture reads a test fixture from testdata/.
func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "reddit", name))
	require.NoError(t, err, "fixture %q not found", name)
	return data
}

// newRedditFilter creates an initialized redditFilter for testing.
func newRedditFilter(t *testing.T, placeholder string) *redditFilter {
	t.Helper()
	r := &redditFilter{
		name:    "reddit-promotions",
		version: "0.2.0",
		domains: []string{"www.reddit.com"},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	err := r.Init(PluginConfig{
		Enabled:     true,
		Mode:        ModeFilter,
		Placeholder: placeholder,
		Domains:     []string{"www.reddit.com"},
	}, logger)
	require.NoError(t, err)
	return r
}

func makeReq(path string) *http.Request {
	return &http.Request{
		Method: "GET",
		URL:    &url.URL{Path: path},
		Host:   "www.reddit.com",
		Header: http.Header{},
	}
}

func makeResp() *http.Response {
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
	}
}

// --- Registration ---

func TestRedditFilterRegistered(t *testing.T) {
	constructor, ok := Registry["reddit-promotions"]
	require.True(t, ok, "reddit-promotions must be registered")

	p := constructor()
	assert.Equal(t, "reddit-promotions", p.Name())
	assert.Equal(t, "0.3.0", p.Version())
	assert.Contains(t, p.Domains(), "www.reddit.com")
	assert.Contains(t, p.Domains(), "gql-fed.reddit.com")
}

// --- Feed ad removal (R1) ---

func TestFeedAdRemoval(t *testing.T) {
	r := newRedditFilter(t, PlaceholderNone)
	body := loadFixture(t, "feed_with_ad.html")

	out, result, err := r.Filter(makeReq("/"), makeResp(), body)
	require.NoError(t, err)

	assert.True(t, result.Matched)
	assert.True(t, result.Modified)
	assert.Equal(t, "feed-ad", result.Rule)
	assert.Equal(t, 1, result.Removed)

	// Ad element gone.
	assert.NotContains(t, string(out), "shreddit-ad-post")
	assert.NotContains(t, string(out), "promotedlink")
	assert.NotContains(t, string(out), "Sephora")

	// Organic posts preserved.
	assert.Contains(t, string(out), "shreddit-post")
	assert.Contains(t, string(out), "mildlyinfuriating")
	assert.Contains(t, string(out), "interesting")
}

func TestFeedAdRemovalWithVisiblePlaceholder(t *testing.T) {
	r := newRedditFilter(t, PlaceholderVisible)
	body := loadFixture(t, "feed_with_ad.html")

	out, result, err := r.Filter(makeReq("/svc/shreddit/feeds/popular-feed"), makeResp(), body)
	require.NoError(t, err)

	assert.True(t, result.Modified)
	assert.Equal(t, 1, result.Removed)

	// Placeholder inserted.
	assert.Contains(t, string(out), "fps: reddit-promotions/feed-ad")
	assert.Contains(t, string(out), "&#x1f6e1;")

	// Ad gone.
	assert.NotContains(t, string(out), "shreddit-ad-post")
}

func TestFeedAdRemovalWithCommentPlaceholder(t *testing.T) {
	r := newRedditFilter(t, PlaceholderComment)
	body := loadFixture(t, "feed_with_ad.html")

	out, result, err := r.Filter(makeReq("/"), makeResp(), body)
	require.NoError(t, err)

	assert.True(t, result.Modified)
	assert.Contains(t, string(out), "<!-- fps filtered: reddit-promotions/feed-ad -->")
	assert.NotContains(t, string(out), "shreddit-ad-post")
}

func TestFeedNoAdPassthrough(t *testing.T) {
	r := newRedditFilter(t, PlaceholderVisible)
	body := loadFixture(t, "feed_no_ad.html")

	out, result, err := r.Filter(makeReq("/"), makeResp(), body)
	require.NoError(t, err)

	assert.False(t, result.Matched)
	assert.False(t, result.Modified)
	assert.Equal(t, 0, result.Removed)
	assert.Equal(t, body, out, "body must be unchanged when no ads present")
}

// --- Comment-tree ad removal (R2) ---

func TestCommentTreeAdRemoval(t *testing.T) {
	r := newRedditFilter(t, PlaceholderNone)
	body := loadFixture(t, "comments_with_tree_ad.html")

	out, result, err := r.Filter(makeReq("/svc/shreddit/comments/r/interesting/t3_1r6aomv"), makeResp(), body)
	require.NoError(t, err)

	assert.True(t, result.Matched)
	assert.True(t, result.Modified)
	assert.Equal(t, "comment-tree-ad", result.Rule)
	assert.Equal(t, 1, result.Removed) // 1 container removed

	// Entire tree-ads container gone (check actual elements, not fixture comments).
	assert.NotContains(t, string(out), "<shreddit-comment-tree-ads")
	assert.NotContains(t, string(out), "comment-tree-ad_t1_")
	assert.NotContains(t, string(out), "<shreddit-comments-page-ad")
	assert.NotContains(t, string(out), "firehousesubs")
	assert.NotContains(t, string(out), "otherAdvertiser")

	// Surrounding comments preserved.
	assert.Contains(t, string(out), "AutoModerator")
	assert.Contains(t, string(out), "mspaceman")
	assert.Contains(t, string(out), "shreddit-comment-tree-stats")
}

// --- Comment-page ad removal (R3) ---

func TestCommentPageAdRemoval(t *testing.T) {
	r := newRedditFilter(t, PlaceholderNone)
	body := loadFixture(t, "comments_with_page_ad.html")

	out, result, err := r.Filter(makeReq("/r/interesting/comments/1r6aomv/little_chimpanzee/"), makeResp(), body)
	require.NoError(t, err)

	assert.True(t, result.Matched)
	assert.True(t, result.Modified)
	assert.Equal(t, "comment-page-ad", result.Rule)
	assert.Equal(t, 1, result.Removed)

	// Ad element gone (check actual elements, not fixture comments).
	assert.NotContains(t, string(out), "<shreddit-comments-page-ad")
	assert.NotContains(t, string(out), "Pepsi_US")
	assert.NotContains(t, string(out), "promotedlink")
}

// --- Right-rail promoted removal (R4) ---

func TestRightRailPromotedRemoval(t *testing.T) {
	r := newRedditFilter(t, PlaceholderNone)
	body := loadFixture(t, "right_rail_with_promoted.html")

	out, result, err := r.Filter(makeReq("/svc/shreddit/pdp-right-rail/related/interesting/t3_1r6aomv"), makeResp(), body)
	require.NoError(t, err)

	assert.True(t, result.Matched)
	assert.True(t, result.Modified)
	assert.Equal(t, "right-rail-ad", result.Rule)
	assert.Equal(t, 1, result.Removed)

	// Ad-event-tracker wrapper gone.
	assert.NotContains(t, string(out), "<ad-event-tracker")
	assert.NotContains(t, string(out), "</ad-event-tracker>")
	assert.NotContains(t, string(out), "brutusBroth")
	assert.NotContains(t, string(out), "promoted-label")

	// Organic right-rail post preserved.
	assert.Contains(t, string(out), "reddit-pdp-right-rail-post")
	assert.Contains(t, string(out), "MadeMeCry")
	assert.Contains(t, string(out), "Chimpanzee suffering from depression")
}

func TestRightRailNoPromotedPassthrough(t *testing.T) {
	r := newRedditFilter(t, PlaceholderVisible)
	body := loadFixture(t, "right_rail_no_promoted.html")

	out, result, err := r.Filter(makeReq("/svc/shreddit/pdp-right-rail/related/test/t3_abc"), makeResp(), body)
	require.NoError(t, err)

	// The fixture has a comment mentioning "ad-event-tracker" but not the actual element.
	// containsAdMarker checks for "<ad-event-tracker" (with angle bracket).
	assert.False(t, result.Matched)
	assert.False(t, result.Modified)
	assert.Equal(t, body, out)
}

// --- URL scoping (R6) ---

func TestURLScopingSkipsNonAdPaths(t *testing.T) {
	r := newRedditFilter(t, PlaceholderNone)
	// Use a body that DOES contain ad markers — but the URL shouldn't be processed.
	body := loadFixture(t, "feed_with_ad.html")

	nonAdPaths := []string{
		"/svc/shreddit/events",
		"/svc/shreddit/graphql",
		"/svc/shreddit/styling-overrides/",
		"/svc/shreddit/update-recaptcha",
		"/svc/shreddit/perfMetrics",
		"/svc/shreddit/client-errors",
		"/svc/shreddit/data-protection-consent",
	}

	for _, path := range nonAdPaths {
		t.Run(path, func(t *testing.T) {
			out, result, err := r.Filter(makeReq(path), makeResp(), body)
			require.NoError(t, err)
			assert.False(t, result.Matched)
			assert.Equal(t, body, out)
		})
	}
}

func TestURLScopingProcessesAdPaths(t *testing.T) {
	r := newRedditFilter(t, PlaceholderNone)
	body := loadFixture(t, "feed_with_ad.html")

	adPaths := []string{
		"/",
		"/svc/shreddit/feeds/popular-feed?after=abc",
		"/svc/shreddit/comments/r/test/t3_abc",
		"/svc/shreddit/more-comments/r/test/t3_abc",
		"/svc/shreddit/pdp-right-rail/related/test/t3_abc",
		"/svc/shreddit/community-more-posts/best/?after=abc&name=LivestreamFail",
		"/r/interesting/comments/1r6aomv/little_chimpanzee/",
		"/r/LivestreamFail/",
		"/r/funny/",
	}

	for _, path := range adPaths {
		t.Run(path, func(t *testing.T) {
			_, result, err := r.Filter(makeReq(path), makeResp(), body)
			require.NoError(t, err)
			assert.True(t, result.Matched, "path %q should be processed", path)
		})
	}
}

// --- Quick-skip (R5) ---

func TestQuickSkipNoMarkers(t *testing.T) {
	r := newRedditFilter(t, PlaceholderVisible)
	body := []byte("<html><body><shreddit-post>organic content</shreddit-post></body></html>")

	out, result, err := r.Filter(makeReq("/"), makeResp(), body)
	require.NoError(t, err)

	assert.False(t, result.Matched)
	assert.Equal(t, body, out)
}

// --- removeElements unit tests ---

func TestRemoveElementsSingle(t *testing.T) {
	body := []byte("before<shreddit-ad-post class=\"promotedlink\">ad content</shreddit-ad-post>after")
	out, n := removeElements(body, "<shreddit-ad-post", "</shreddit-ad-post>", "[REMOVED]")
	assert.Equal(t, 1, n)
	assert.Equal(t, "before[REMOVED]after", string(out))
}

func TestRemoveElementsMultiple(t *testing.T) {
	body := []byte("A<x>1</x>B<x>2</x>C")
	out, n := removeElements(body, "<x", "</x>", "")
	assert.Equal(t, 2, n)
	assert.Equal(t, "ABC", string(out))
}

func TestRemoveElementsNoMatch(t *testing.T) {
	body := []byte("no elements here")
	out, n := removeElements(body, "<shreddit-ad-post", "</shreddit-ad-post>", "[X]")
	assert.Equal(t, 0, n)
	assert.Equal(t, "no elements here", string(out))
}

func TestRemoveElementsMalformedNoClose(t *testing.T) {
	body := []byte("before<shreddit-ad-post>ad without closing tag")
	out, n := removeElements(body, "<shreddit-ad-post", "</shreddit-ad-post>", "[X]")
	assert.Equal(t, 0, n) // bail out on malformed
	assert.Equal(t, "before<shreddit-ad-post>ad without closing tag", string(out))
}

// --- containsAdMarker ---

func TestContainsAdMarker(t *testing.T) {
	assert.True(t, containsAdMarker([]byte("some <shreddit-ad-post> stuff")))
	assert.True(t, containsAdMarker([]byte("some <shreddit-comments-page-ad> stuff")))
	assert.True(t, containsAdMarker([]byte("some <ad-event-tracker> stuff")))
	assert.False(t, containsAdMarker([]byte("just normal html")))
	assert.False(t, containsAdMarker([]byte("")))
	// The marker check requires the angle bracket for ad-event-tracker.
	assert.False(t, containsAdMarker([]byte("text mentions ad-event-tracker but no angle bracket")))
}

// --- shouldProcess ---

func TestShouldProcess(t *testing.T) {
	r := &redditFilter{}

	tests := []struct {
		path string
		want bool
	}{
		{"/", true},
		{"/svc/shreddit/feeds/popular-feed", true},
		{"/svc/shreddit/comments/r/test/t3_abc", true},
		{"/svc/shreddit/more-comments/r/test/t3_abc", true},
		{"/svc/shreddit/pdp-right-rail/related/test/t3_abc", true},
		{"/svc/shreddit/community-more-posts/best/?after=abc", true},
		{"/r/funny/comments/abc123/some_title/", true},
		{"/r/LivestreamFail/", true},
		{"/r/funny/", true},
		{"/svc/shreddit/events", false},
		{"/svc/shreddit/graphql", false},
		{"/svc/shreddit/styling-overrides/", false},
		{"/svc/shreddit/update-recaptcha", false},
		{"/user/Sephora/", false},
		{"/fps/heartbeat", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := r.shouldProcess(tt.path)
			assert.Equal(t, tt.want, got, "shouldProcess(%q)", tt.path)
		})
	}
}

// --- Multi-rule in single response ---

func TestMultipleRulesInSingleResponse(t *testing.T) {
	r := newRedditFilter(t, PlaceholderNone)

	// Construct a body that has both a feed ad and a comment-page ad.
	body := []byte(
		`<shreddit-post>organic</shreddit-post>` +
			`<shreddit-ad-post class="promotedlink">feed ad</shreddit-ad-post>` +
			`<shreddit-comments-page-ad class="promotedlink">page ad</shreddit-comments-page-ad>`,
	)

	out, result, err := r.Filter(makeReq("/"), makeResp(), body)
	require.NoError(t, err)

	assert.True(t, result.Modified)
	assert.Equal(t, "feed-ad", result.Rule) // first rule wins
	assert.Equal(t, 2, result.Removed)      // both removed

	outStr := string(out)
	assert.Contains(t, outStr, "organic")
	assert.NotContains(t, outStr, "shreddit-ad-post")
	assert.NotContains(t, outStr, "shreddit-comments-page-ad")
}

// --- GraphQL helpers ---

func gqlRequest(opName string) *http.Request {
	r := &http.Request{
		Method: "POST",
		URL:    &url.URL{Scheme: "https", Host: "gql-fed.reddit.com", Path: "/"},
		Host:   "gql-fed.reddit.com",
		Header: http.Header{"Content-Type": []string{"application/json"}},
	}
	if opName != "" {
		r.Header.Set("X-Apollo-Operation-Name", opName)
	}
	return r
}

func jsonResp() *http.Response {
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

// jsonGet navigates a JSON doc and returns the value at the given path.
func jsonGet[T any](t *testing.T, data []byte, keys ...string) T {
	t.Helper()
	var doc map[string]any
	require.NoError(t, json.Unmarshal(data, &doc))
	val, ok := jsonPath[T](doc, keys...)
	require.True(t, ok, "jsonPath %v not found", keys)
	return val
}

// --- HomeFeedSdui tests ---

func TestFilterHomeFeed(t *testing.T) {
	r := newRedditFilter(t, PlaceholderNone)
	body := loadFixture(t, "homefeed_sdui.json")

	out, fr, err := r.Filter(gqlRequest("HomeFeedSdui"), jsonResp(), body)
	require.NoError(t, err)

	assert.True(t, fr.Matched)
	assert.True(t, fr.Modified)
	assert.Equal(t, "feed-sdui-ad", fr.Rule)
	assert.Equal(t, 2, fr.Removed)

	// 3 organic edges remain.
	edges := jsonGet[[]any](t, out, "data", "homeV3", "elements", "edges")
	assert.Len(t, edges, 3)

	for i, edge := range edges {
		em, ok := edge.(map[string]any)
		require.True(t, ok, "edge %d should be a map", i)
		node, ok := em["node"].(map[string]any)
		require.True(t, ok, "edge %d node should be a map", i)
		assert.Nil(t, node["adPayload"], "edge %d should be organic", i)
	}
}

func TestFilterHomeFeedNoAds(t *testing.T) {
	r := newRedditFilter(t, PlaceholderNone)
	body := loadFixture(t, "homefeed_sdui_no_ads.json")

	out, fr, err := r.Filter(gqlRequest("HomeFeedSdui"), jsonResp(), body)
	require.NoError(t, err)

	assert.False(t, fr.Matched)
	assert.False(t, fr.Modified)

	edges := jsonGet[[]any](t, out, "data", "homeV3", "elements", "edges")
	assert.Len(t, edges, 3)
}

func TestFilterHomeFeedLastEdgeIsAd(t *testing.T) {
	r := newRedditFilter(t, PlaceholderNone)
	body := loadFixture(t, "homefeed_sdui_last_ad.json")

	out, fr, err := r.Filter(gqlRequest("HomeFeedSdui"), jsonResp(), body)
	require.NoError(t, err)

	assert.Equal(t, 1, fr.Removed)

	edges := jsonGet[[]any](t, out, "data", "homeV3", "elements", "edges")
	assert.Len(t, edges, 2)

	// endCursor should be updated to last organic edge's groupId.
	cursor := jsonGet[string](t, out, "data", "homeV3", "elements", "pageInfo", "endCursor")
	decoded, err := base64.StdEncoding.DecodeString(cursor)
	require.NoError(t, err)
	assert.Equal(t, "t3_test002", string(decoded))
}

func TestFilterHomeFeedVisiblePlaceholder(t *testing.T) {
	r := newRedditFilter(t, PlaceholderVisible)
	body := loadFixture(t, "homefeed_sdui.json")

	out, fr, err := r.Filter(gqlRequest("HomeFeedSdui"), jsonResp(), body)
	require.NoError(t, err)
	assert.Equal(t, 2, fr.Removed)

	// 3 organic + 2 placeholders = 5 edges.
	edges := jsonGet[[]any](t, out, "data", "homeV3", "elements", "edges")
	assert.Len(t, edges, 5)

	phCount := 0
	for _, edge := range edges {
		if em, ok := edge.(map[string]any); ok {
			if _, has := em["fps_filtered"]; has {
				phCount++
			}
		}
	}
	assert.Equal(t, 2, phCount)
}

// --- FeedPostDetailsByIds tests ---

func TestFilterFeedDetails(t *testing.T) {
	r := newRedditFilter(t, PlaceholderNone)
	body := loadFixture(t, "feed_details.json")

	out, fr, err := r.Filter(gqlRequest("FeedPostDetailsByIds"), jsonResp(), body)
	require.NoError(t, err)

	assert.True(t, fr.Matched)
	assert.True(t, fr.Modified)
	assert.Equal(t, "feed-details-ad", fr.Rule)
	assert.Equal(t, 2, fr.Removed)

	posts := jsonGet[[]any](t, out, "data", "postsInfoByIds")
	assert.Len(t, posts, 3)

	for i, post := range posts {
		pm, ok := post.(map[string]any)
		require.True(t, ok, "post %d should be a map", i)
		assert.Equal(t, "SubredditPost", pm["__typename"], "post %d", i)
	}
}

func TestFilterFeedDetailsNoAds(t *testing.T) {
	r := newRedditFilter(t, PlaceholderNone)
	body := loadFixture(t, "feed_details_no_ads.json")

	out, fr, err := r.Filter(gqlRequest("FeedPostDetailsByIds"), jsonResp(), body)
	require.NoError(t, err)

	assert.False(t, fr.Matched)
	assert.False(t, fr.Modified)

	posts := jsonGet[[]any](t, out, "data", "postsInfoByIds")
	assert.Len(t, posts, 2)
}

func TestFilterFeedDetailsVisiblePlaceholder(t *testing.T) {
	r := newRedditFilter(t, PlaceholderVisible)
	body := loadFixture(t, "feed_details.json")

	out, fr, err := r.Filter(gqlRequest("FeedPostDetailsByIds"), jsonResp(), body)
	require.NoError(t, err)
	assert.Equal(t, 2, fr.Removed)

	// 3 organic + 2 placeholders.
	posts := jsonGet[[]any](t, out, "data", "postsInfoByIds")
	assert.Len(t, posts, 5)
}

// --- PdpCommentsAds tests ---

func TestFilterPdpAds(t *testing.T) {
	r := newRedditFilter(t, PlaceholderNone)
	body := loadFixture(t, "pdp_comments_ads.json")

	out, fr, err := r.Filter(gqlRequest("PdpCommentsAds"), jsonResp(), body)
	require.NoError(t, err)

	assert.True(t, fr.Matched)
	assert.True(t, fr.Modified)
	assert.Equal(t, "pdp-comments-ad", fr.Rule)
	assert.Equal(t, 2, fr.Removed)

	adPosts := jsonGet[[]any](t, out, "data", "postInfoById", "pdpCommentsAds", "adPosts")
	assert.Empty(t, adPosts)
}

func TestFilterPdpAdsEmpty(t *testing.T) {
	r := newRedditFilter(t, PlaceholderNone)
	body := loadFixture(t, "pdp_comments_ads_empty.json")

	_, fr, err := r.Filter(gqlRequest("PdpCommentsAds"), jsonResp(), body)
	require.NoError(t, err)

	assert.False(t, fr.Matched)
	assert.False(t, fr.Modified)
}

func TestFilterPdpAdsVisiblePlaceholder(t *testing.T) {
	r := newRedditFilter(t, PlaceholderVisible)
	body := loadFixture(t, "pdp_comments_ads.json")

	out, fr, err := r.Filter(gqlRequest("PdpCommentsAds"), jsonResp(), body)
	require.NoError(t, err)
	assert.Equal(t, 2, fr.Removed)

	adPosts := jsonGet[[]any](t, out, "data", "postInfoById", "pdpCommentsAds", "adPosts")
	require.Len(t, adPosts, 1)
	ph, ok := adPosts[0].(map[string]any)
	require.True(t, ok, "placeholder should be a map")
	assert.Contains(t, ph, "fps_filtered")
}

// --- GraphQL dispatch and edge case tests ---

func TestFilterJSONPassthroughUnknownOp(t *testing.T) {
	r := newRedditFilter(t, PlaceholderNone)
	body := []byte(`{"data": {"something": "else"}}`)

	out, fr, err := r.Filter(gqlRequest("GetAccount"), jsonResp(), body)
	require.NoError(t, err)
	assert.False(t, fr.Matched)
	assert.Equal(t, string(body), string(out))
}

func TestFilterJSONPassthroughNoOpHeader(t *testing.T) {
	r := newRedditFilter(t, PlaceholderNone)
	body := []byte(`{"data": {"something": "else"}}`)

	out, fr, err := r.Filter(gqlRequest(""), jsonResp(), body)
	require.NoError(t, err)
	assert.False(t, fr.Matched)
	assert.Equal(t, string(body), string(out))
}

func TestFilterJSONMalformedBody(t *testing.T) {
	r := newRedditFilter(t, PlaceholderNone)
	body := []byte(`{not valid json`)

	out, fr, err := r.Filter(gqlRequest("HomeFeedSdui"), jsonResp(), body)
	require.NoError(t, err)
	assert.False(t, fr.Matched)
	assert.Equal(t, string(body), string(out))
}

func TestFilterJSONMissingPath(t *testing.T) {
	r := newRedditFilter(t, PlaceholderNone)
	body := []byte(`{"data": {"unexpected": "structure"}}`)

	out, fr, err := r.Filter(gqlRequest("HomeFeedSdui"), jsonResp(), body)
	require.NoError(t, err)
	assert.False(t, fr.Matched)
	assert.Equal(t, string(body), string(out))
}

func TestFilterCommentPlaceholderMode(t *testing.T) {
	r := newRedditFilter(t, PlaceholderComment)
	body := loadFixture(t, "feed_details.json")

	out, fr, err := r.Filter(gqlRequest("FeedPostDetailsByIds"), jsonResp(), body)
	require.NoError(t, err)
	assert.Equal(t, 2, fr.Removed)

	// Comment mode uses _fps_filtered (underscore prefix).
	posts := jsonGet[[]any](t, out, "data", "postsInfoByIds")
	phCount := 0
	for _, post := range posts {
		if pm, ok := post.(map[string]any); ok {
			if _, has := pm["_fps_filtered"]; has {
				phCount++
			}
		}
	}
	assert.Equal(t, 2, phCount)
}

func TestFilterHTMLStillWorksAfterJSONDispatch(t *testing.T) {
	r := newRedditFilter(t, PlaceholderNone)
	body := loadFixture(t, "feed_with_ad.html")

	// HTML request should still go through HTML filtering path.
	out, fr, err := r.Filter(makeReq("/"), makeResp(), body)
	require.NoError(t, err)

	assert.True(t, fr.Matched)
	assert.True(t, fr.Modified)
	assert.Equal(t, "feed-ad", fr.Rule)
	assert.NotContains(t, string(out), "shreddit-ad-post")
}

// --- Fixture integrity checks ---

func TestFixtureIntegrity(t *testing.T) {
	// Verify that our test fixtures contain the expected marker strings.
	// This catches fixture corruption or accidental edits.
	tests := []struct {
		fixture string
		present []string
		absent  []string
	}{
		{
			"feed_with_ad.html",
			[]string{"shreddit-ad-post", "promotedlink", "shreddit-post", "mildlyinfuriating"},
			[]string{},
		},
		{
			"feed_no_ad.html",
			[]string{"shreddit-post"},
			[]string{"<shreddit-ad-post", "promotedlink", "<ad-event-tracker"},
		},
		{
			"comments_with_tree_ad.html",
			[]string{"<shreddit-comment-tree-ads", "comment-tree-ad_t1_", "<shreddit-comments-page-ad"},
			[]string{},
		},
		{
			"comments_with_page_ad.html",
			[]string{"<shreddit-comments-page-ad", "promotedlink"},
			[]string{"<shreddit-comment-tree-ads"},
		},
		{
			"right_rail_with_promoted.html",
			[]string{"<ad-event-tracker", "reddit-pdp-right-rail-post"},
			[]string{},
		},
		{
			"right_rail_no_promoted.html",
			[]string{"reddit-pdp-right-rail-post"},
			[]string{"<ad-event-tracker"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			data := loadFixture(t, tt.fixture)
			s := string(data)
			for _, p := range tt.present {
				assert.True(t, strings.Contains(s, p),
					"fixture %q must contain %q", tt.fixture, p)
			}
			for _, a := range tt.absent {
				assert.False(t, strings.Contains(s, a),
					"fixture %q must NOT contain %q", tt.fixture, a)
			}
		})
	}
}
