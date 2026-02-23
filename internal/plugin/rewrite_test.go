package plugin

import (
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// --- RewriteStore tests ---

func TestStoreAddAndList(t *testing.T) {
	store := openTestStore(t)

	rule, err := store.Add(RewriteRule{
		Name:    "test rule",
		Pattern: "foo",
		Enabled: true,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, rule.ID)
	assert.Equal(t, "test rule", rule.Name)
	assert.NotEmpty(t, rule.CreatedAt)

	rules, err := store.List()
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, rule.ID, rules[0].ID)
}

func TestStoreGet(t *testing.T) {
	store := openTestStore(t)
	created, err := store.Add(RewriteRule{Name: "get test", Pattern: "bar", Enabled: true})
	require.NoError(t, err)

	got, err := store.Get(created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, "get test", got.Name)
}

func TestStoreGetNotFound(t *testing.T) {
	store := openTestStore(t)
	_, err := store.Get("nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestStoreUpdate(t *testing.T) {
	store := openTestStore(t)
	created, err := store.Add(RewriteRule{Name: "original", Pattern: "a", Enabled: true})
	require.NoError(t, err)

	updated, err := store.Update(created.ID, RewriteRule{
		Name:        "updated",
		Pattern:     "b",
		Replacement: "c",
		Enabled:     true,
	})
	require.NoError(t, err)
	assert.Equal(t, "updated", updated.Name)
	assert.Equal(t, "b", updated.Pattern)
	assert.Equal(t, created.ID, updated.ID)

	// Verify created_at was preserved via Get.
	got, err := store.Get(created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.CreatedAt, got.CreatedAt)
}

func TestStoreUpdateNotFound(t *testing.T) {
	store := openTestStore(t)
	_, err := store.Update("nonexistent", RewriteRule{Name: "x", Pattern: "y"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestStoreDelete(t *testing.T) {
	store := openTestStore(t)
	created, err := store.Add(RewriteRule{Name: "to delete", Pattern: "x", Enabled: true})
	require.NoError(t, err)

	err = store.Delete(created.ID)
	require.NoError(t, err)

	rules, err := store.List()
	require.NoError(t, err)
	assert.Empty(t, rules)
}

func TestStoreDeleteNotFound(t *testing.T) {
	store := openTestStore(t)
	err := store.Delete("nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestStoreToggle(t *testing.T) {
	store := openTestStore(t)
	created, err := store.Add(RewriteRule{Name: "toggle me", Pattern: "x", Enabled: true})
	require.NoError(t, err)
	assert.True(t, created.Enabled)

	toggled, err := store.Toggle(created.ID)
	require.NoError(t, err)
	assert.False(t, toggled.Enabled)

	toggled2, err := store.Toggle(created.ID)
	require.NoError(t, err)
	assert.True(t, toggled2.Enabled)
}

func TestStoreToggleNotFound(t *testing.T) {
	store := openTestStore(t)
	_, err := store.Toggle("nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestStoreValidation(t *testing.T) {
	store := openTestStore(t)

	// Missing name.
	_, err := store.Add(RewriteRule{Pattern: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name is required")

	// Missing pattern.
	_, err = store.Add(RewriteRule{Name: "test"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pattern is required")

	// Invalid regex.
	_, err = store.Add(RewriteRule{Name: "test", Pattern: "[invalid", IsRegex: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid regex")
}

func TestStoreDomainAndURLPatterns(t *testing.T) {
	store := openTestStore(t)
	created, err := store.Add(RewriteRule{
		Name:        "scoped",
		Pattern:     "x",
		Domains:     []string{"example.com", "test.com"},
		URLPatterns: []string{"/blog/*", "/api/*"},
		Enabled:     true,
	})
	require.NoError(t, err)

	got, err := store.Get(created.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"example.com", "test.com"}, got.Domains)
	assert.Equal(t, []string{"/blog/*", "/api/*"}, got.URLPatterns)
}

// --- Rewrite filter tests ---

func TestRewriteLiteralReplacement(t *testing.T) {
	f := setupFilter(t, RewriteRule{
		Name: "literal", Pattern: "foo", Replacement: "bar", Enabled: true,
	})

	body, result, err := f.Filter(rewriteReq("example.com", "/"), rewriteResp(), []byte("foo baz foo"))
	require.NoError(t, err)
	assert.Equal(t, "bar baz bar", string(body))
	assert.True(t, result.Matched)
	assert.True(t, result.Modified)
	assert.Equal(t, "literal", result.Rule)
	assert.Equal(t, 2, result.Removed)
}

func TestRewriteRegexReplacement(t *testing.T) {
	f := setupFilter(t, RewriteRule{
		Name: "regex", Pattern: `\bfoo\b`, Replacement: "bar", IsRegex: true, Enabled: true,
	})

	body, result, err := f.Filter(rewriteReq("example.com", "/"), rewriteResp(), []byte("foo foobar foo"))
	require.NoError(t, err)
	assert.Equal(t, "bar foobar bar", string(body))
	assert.True(t, result.Matched)
	assert.Equal(t, 2, result.Removed)
}

func TestRewriteRegexCaptureGroups(t *testing.T) {
	f := setupFilter(t, RewriteRule{
		Name: "capture", Pattern: `(\w+)@(\w+)`, Replacement: "$2/$1", IsRegex: true, Enabled: true,
	})

	body, _, err := f.Filter(rewriteReq("example.com", "/"), rewriteResp(), []byte("user@host"))
	require.NoError(t, err)
	assert.Equal(t, "host/user", string(body))
}

func TestRewriteDeletion(t *testing.T) {
	f := setupFilter(t, RewriteRule{
		Name: "delete", Pattern: "unwanted", Replacement: "", Enabled: true,
	})

	body, result, err := f.Filter(rewriteReq("example.com", "/"), rewriteResp(), []byte("keep unwanted keep"))
	require.NoError(t, err)
	assert.Equal(t, "keep  keep", string(body))
	assert.True(t, result.Modified)
}

func TestRewriteNoMatch(t *testing.T) {
	f := setupFilter(t, RewriteRule{
		Name: "no match", Pattern: "xyz", Replacement: "abc", Enabled: true,
	})

	body, result, err := f.Filter(rewriteReq("example.com", "/"), rewriteResp(), []byte("hello"))
	require.NoError(t, err)
	assert.Equal(t, "hello", string(body))
	assert.False(t, result.Matched)
	assert.False(t, result.Modified)
}

func TestRewriteEmptyRuleSet(t *testing.T) {
	f := setupFilter(t)

	body, result, err := f.Filter(rewriteReq("example.com", "/"), rewriteResp(), []byte("hello"))
	require.NoError(t, err)
	assert.Equal(t, "hello", string(body))
	assert.False(t, result.Matched)
}

func TestRewriteDomainScoping(t *testing.T) {
	f := setupFilter(t, RewriteRule{
		Name: "scoped", Pattern: "foo", Replacement: "bar",
		Domains: []string{"target.com"}, Enabled: true,
	})

	// Matching domain: should rewrite.
	body, result, err := f.Filter(rewriteReq("target.com", "/"), rewriteResp(), []byte("foo"))
	require.NoError(t, err)
	assert.Equal(t, "bar", string(body))
	assert.True(t, result.Matched)

	// Non-matching domain: passthrough.
	body, result, err = f.Filter(rewriteReq("other.com", "/"), rewriteResp(), []byte("foo"))
	require.NoError(t, err)
	assert.Equal(t, "foo", string(body))
	assert.False(t, result.Matched)
}

func TestRewriteURLScoping(t *testing.T) {
	f := setupFilter(t, RewriteRule{
		Name: "url-scoped", Pattern: "foo", Replacement: "bar",
		URLPatterns: []string{"/blog/*"}, Enabled: true,
	})

	// Matching path: should rewrite.
	body, result, err := f.Filter(rewriteReq("example.com", "/blog/post"), rewriteResp(), []byte("foo"))
	require.NoError(t, err)
	assert.Equal(t, "bar", string(body))
	assert.True(t, result.Matched)

	// Non-matching path: passthrough.
	body, result, err = f.Filter(rewriteReq("example.com", "/api/data"), rewriteResp(), []byte("foo"))
	require.NoError(t, err)
	assert.Equal(t, "foo", string(body))
	assert.False(t, result.Matched)
}

func TestRewriteMultipleRules(t *testing.T) {
	f := setupFilter(t,
		RewriteRule{Name: "r1", Pattern: "aaa", Replacement: "bbb", Enabled: true},
		RewriteRule{Name: "r2", Pattern: "bbb", Replacement: "ccc", Enabled: true},
	)

	// r1 replaces aaa→bbb, then r2 replaces bbb→ccc.
	body, result, err := f.Filter(rewriteReq("example.com", "/"), rewriteResp(), []byte("aaa"))
	require.NoError(t, err)
	assert.Equal(t, "ccc", string(body))
	assert.True(t, result.Matched)
	assert.Equal(t, "r1", result.Rule) // first matching rule
	require.Len(t, result.Rules, 2)
	assert.Equal(t, "r1", result.Rules[0].Rule)
	assert.Equal(t, "r2", result.Rules[1].Rule)
}

func TestRewriteDisabledRuleSkipped(t *testing.T) {
	f := setupFilter(t,
		RewriteRule{Name: "disabled", Pattern: "foo", Replacement: "bar", Enabled: false},
	)

	body, result, err := f.Filter(rewriteReq("example.com", "/"), rewriteResp(), []byte("foo"))
	require.NoError(t, err)
	assert.Equal(t, "foo", string(body))
	assert.False(t, result.Matched)
}

func TestRewriteHotReload(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := OpenRewriteStore(tmpDir)
	require.NoError(t, err)
	defer store.Close() //nolint:errcheck // best-effort close in test

	f := &rewriteFilter{name: "rewrite", version: "0.1.0", logger: testLogger(), store: store}
	require.NoError(t, f.ReloadRules())

	// No rules: passthrough.
	body, result, _ := f.Filter(rewriteReq("example.com", "/"), rewriteResp(), []byte("foo"))
	assert.Equal(t, "foo", string(body))
	assert.False(t, result.Matched)

	// Add a rule and reload.
	_, err = store.Add(RewriteRule{Name: "hot", Pattern: "foo", Replacement: "bar", Enabled: true})
	require.NoError(t, err)
	require.NoError(t, f.ReloadRules())

	body, result, _ = f.Filter(rewriteReq("example.com", "/"), rewriteResp(), []byte("foo"))
	assert.Equal(t, "bar", string(body))
	assert.True(t, result.Matched)
}

// --- Helper functions ---

func openTestStore(t *testing.T) *RewriteStore {
	t.Helper()
	store, err := OpenRewriteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func setupFilter(t *testing.T, rules ...RewriteRule) *rewriteFilter {
	t.Helper()
	tmpDir := t.TempDir()
	store, err := OpenRewriteStore(tmpDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	for i := range rules {
		_, err := store.Add(rules[i])
		require.NoError(t, err)
	}

	f := &rewriteFilter{name: "rewrite", version: "0.1.0", logger: testLogger(), store: store}
	require.NoError(t, f.ReloadRules())
	return f
}

func rewriteReq(host, path string) *http.Request {
	return &http.Request{
		Host:   host,
		URL:    &url.URL{Path: path},
		Method: "GET",
	}
}

func rewriteResp() *http.Response {
	return rewriteRespWithCT("text/html")
}

func rewriteRespWithCT(ct string) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{ct}},
	}
}

// --- Content-type scoping tests ---

func TestRewriteSkipsJSON(t *testing.T) {
	f := setupFilter(t, RewriteRule{
		Name: "literal", Pattern: "foo", Replacement: "bar", Enabled: true,
	})

	body, result, err := f.Filter(rewriteReq("example.com", "/"), rewriteRespWithCT("application/json"), []byte(`{"key":"foo"}`))
	require.NoError(t, err)
	assert.Equal(t, `{"key":"foo"}`, string(body))
	assert.False(t, result.Matched)
}

func TestRewriteSkipsJavaScript(t *testing.T) {
	f := setupFilter(t, RewriteRule{
		Name: "literal", Pattern: "foo", Replacement: "bar", Enabled: true,
	})

	body, result, err := f.Filter(rewriteReq("example.com", "/"), rewriteRespWithCT("application/javascript"), []byte("var x = 'foo';"))
	require.NoError(t, err)
	assert.Equal(t, "var x = 'foo';", string(body))
	assert.False(t, result.Matched)
}

func TestRewriteSkipsJSONWithCharset(t *testing.T) {
	f := setupFilter(t, RewriteRule{
		Name: "literal", Pattern: "foo", Replacement: "bar", Enabled: true,
	})

	body, result, err := f.Filter(rewriteReq("example.com", "/"), rewriteRespWithCT("application/json; charset=utf-8"), []byte(`{"key":"foo"}`))
	require.NoError(t, err)
	assert.Equal(t, `{"key":"foo"}`, string(body))
	assert.False(t, result.Matched)
}

func TestRewriteExplicitContentType(t *testing.T) {
	f := setupFilter(t, RewriteRule{
		Name: "json-rule", Pattern: "foo", Replacement: "bar",
		ContentTypes: []string{"application/json"}, Enabled: true,
	})

	// Should apply to JSON.
	body, result, err := f.Filter(rewriteReq("example.com", "/"), rewriteRespWithCT("application/json"), []byte(`{"key":"foo"}`))
	require.NoError(t, err)
	assert.Equal(t, `{"key":"bar"}`, string(body))
	assert.True(t, result.Matched)

	// Should NOT apply to HTML (not in the rule's content types).
	body, result, err = f.Filter(rewriteReq("example.com", "/"), rewriteResp(), []byte("foo"))
	require.NoError(t, err)
	assert.Equal(t, "foo", string(body))
	assert.False(t, result.Matched)
}

func TestRewriteHTMLWithCharset(t *testing.T) {
	f := setupFilter(t, RewriteRule{
		Name: "literal", Pattern: "foo", Replacement: "bar", Enabled: true,
	})

	body, result, err := f.Filter(rewriteReq("example.com", "/"), rewriteRespWithCT("text/html; charset=utf-8"), []byte("<p>foo</p>"))
	require.NoError(t, err)
	assert.Equal(t, "<p>bar</p>", string(body))
	assert.True(t, result.Matched)
}

// --- HTML-safe replacement tests ---

func TestRewriteHTMLSkipsScript(t *testing.T) {
	f := setupFilter(t, RewriteRule{
		Name: "replace", Pattern: "Trump", Replacement: "Chump", Enabled: true,
	})

	html := `<p>Trump said</p><script>var data = {"name": "Trump"};</script><p>Trump again</p>`
	body, result, err := f.Filter(rewriteReq("example.com", "/"), rewriteResp(), []byte(html))
	require.NoError(t, err)
	assert.Equal(t, `<p>Chump said</p><script>var data = {"name": "Trump"};</script><p>Chump again</p>`, string(body))
	assert.True(t, result.Matched)
	assert.Equal(t, 2, result.Removed)
}

func TestRewriteHTMLSkipsStyle(t *testing.T) {
	f := setupFilter(t, RewriteRule{
		Name: "replace", Pattern: "red", Replacement: "blue", Enabled: true,
	})

	html := `<p>red text</p><style>.red { color: red; }</style><p>more red</p>`
	body, result, err := f.Filter(rewriteReq("example.com", "/"), rewriteResp(), []byte(html))
	require.NoError(t, err)
	assert.Equal(t, `<p>blue text</p><style>.red { color: red; }</style><p>more blue</p>`, string(body))
	assert.True(t, result.Matched)
	assert.Equal(t, 2, result.Removed)
}

func TestRewriteHTMLMultipleProtectedBlocks(t *testing.T) {
	f := setupFilter(t, RewriteRule{
		Name: "replace", Pattern: "word", Replacement: "WORD", Enabled: true,
	})

	html := `<p>word</p><script>var word = 1;</script><p>word</p><style>.word{}</style><p>word</p>`
	body, result, err := f.Filter(rewriteReq("example.com", "/"), rewriteResp(), []byte(html))
	require.NoError(t, err)
	assert.Equal(t, `<p>WORD</p><script>var word = 1;</script><p>WORD</p><style>.word{}</style><p>WORD</p>`, string(body))
	assert.Equal(t, 3, result.Removed)
}

func TestRewriteHTMLRegexSkipsScript(t *testing.T) {
	f := setupFilter(t, RewriteRule{
		Name: "regex", Pattern: `\bTrump\b`, Replacement: "Chump", IsRegex: true, Enabled: true,
	})

	html := `<p>Trump here</p><script>{"name":"Trump"}</script><p>Trump there</p>`
	body, result, err := f.Filter(rewriteReq("example.com", "/"), rewriteResp(), []byte(html))
	require.NoError(t, err)
	assert.Equal(t, `<p>Chump here</p><script>{"name":"Trump"}</script><p>Chump there</p>`, string(body))
	assert.Equal(t, 2, result.Removed)
}

func TestRewriteHTMLProtectedRangesRecomputed(t *testing.T) {
	// First rule replaces text outside scripts (changing body length).
	// Second rule should still correctly skip scripts after offset shift.
	f := setupFilter(t,
		RewriteRule{Name: "r1", Pattern: "short", Replacement: "much-longer-text", Enabled: true},
		RewriteRule{Name: "r2", Pattern: "secret", Replacement: "REDACTED", Enabled: true},
	)

	html := `<p>short secret</p><script>var secret = "secret";</script><p>secret</p>`
	body, _, err := f.Filter(rewriteReq("example.com", "/"), rewriteResp(), []byte(html))
	require.NoError(t, err)
	assert.Equal(t, `<p>much-longer-text REDACTED</p><script>var secret = "secret";</script><p>REDACTED</p>`, string(body))
}

func TestRewriteHTMLNoScriptTags(t *testing.T) {
	f := setupFilter(t, RewriteRule{
		Name: "replace", Pattern: "foo", Replacement: "bar", Enabled: true,
	})

	html := `<p>foo</p><div>foo</div>`
	body, result, err := f.Filter(rewriteReq("example.com", "/"), rewriteResp(), []byte(html))
	require.NoError(t, err)
	assert.Equal(t, `<p>bar</p><div>bar</div>`, string(body))
	assert.Equal(t, 2, result.Removed)
}

func TestRewriteHTMLCaseInsensitiveTags(t *testing.T) {
	f := setupFilter(t, RewriteRule{
		Name: "replace", Pattern: "word", Replacement: "WORD", Enabled: true,
	})

	html := `<p>word</p><SCRIPT>var word = 1;</SCRIPT><p>word</p>`
	body, _, err := f.Filter(rewriteReq("example.com", "/"), rewriteResp(), []byte(html))
	require.NoError(t, err)
	assert.Equal(t, `<p>WORD</p><SCRIPT>var word = 1;</SCRIPT><p>WORD</p>`, string(body))
}

func TestRewritePlainTextNoProtection(t *testing.T) {
	// text/plain should NOT have script/style protection.
	f := setupFilter(t, RewriteRule{
		Name: "replace", Pattern: "word", Replacement: "WORD", Enabled: true,
	})

	text := `word <script>word</script> word`
	body, result, err := f.Filter(rewriteReq("example.com", "/"), rewriteRespWithCT("text/plain"), []byte(text))
	require.NoError(t, err)
	assert.Equal(t, `WORD <script>WORD</script> WORD`, string(body))
	assert.Equal(t, 3, result.Removed)
}

// --- Store content_types tests ---

func TestStoreContentTypes(t *testing.T) {
	store := openTestStore(t)
	created, err := store.Add(RewriteRule{
		Name:         "typed",
		Pattern:      "x",
		ContentTypes: []string{"text/html", "application/json"},
		Enabled:      true,
	})
	require.NoError(t, err)

	got, err := store.Get(created.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"text/html", "application/json"}, got.ContentTypes)
}

func TestStoreContentTypesDefaultEmpty(t *testing.T) {
	store := openTestStore(t)
	created, err := store.Add(RewriteRule{
		Name:    "no-types",
		Pattern: "x",
		Enabled: true,
	})
	require.NoError(t, err)

	got, err := store.Get(created.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{}, got.ContentTypes)
}

// --- Protected range unit tests ---

func TestFindProtectedRanges(t *testing.T) {
	body := []byte(`<p>text</p><script>js code</script><p>more</p><style>css</style><p>end</p>`)
	ranges := findProtectedRanges(body)
	require.Len(t, ranges, 2)
	// Verify the script block is captured.
	assert.Equal(t, "<script>js code</script>", string(body[ranges[0].start:ranges[0].end]))
	// Verify the style block is captured.
	assert.Equal(t, "<style>css</style>", string(body[ranges[1].start:ranges[1].end]))
}

func TestFindProtectedRangesWithAttributes(t *testing.T) {
	body := []byte(`<script type="text/javascript">code</script>`)
	ranges := findProtectedRanges(body)
	require.Len(t, ranges, 1)
	assert.Equal(t, string(body), string(body[ranges[0].start:ranges[0].end]))
}

func TestFindProtectedRangesNoTags(t *testing.T) {
	body := []byte(`<p>hello</p><div>world</div>`)
	ranges := findProtectedRanges(body)
	assert.Empty(t, ranges)
}

func TestFindProtectedRangesDoesNotMatchScripted(t *testing.T) {
	body := []byte(`<scripted>not a script tag</scripted>`)
	ranges := findProtectedRanges(body)
	assert.Empty(t, ranges)
}
