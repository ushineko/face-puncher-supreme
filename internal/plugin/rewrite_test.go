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
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"text/html"}},
	}
}
