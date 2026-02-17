package plugin

import (
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Marker tests ---

func TestMarkerVisibleHTML(t *testing.T) {
	m := Marker(PlaceholderVisible, "reddit-promotions", "promoted-post-html", "text/html")
	assert.Contains(t, m, "fps: reddit-promotions/promoted-post-html")
	assert.Contains(t, m, "<div")
	assert.Contains(t, m, "&#x1f6e1;")
}

func TestMarkerCommentHTML(t *testing.T) {
	m := Marker(PlaceholderComment, "reddit-promotions", "promoted-post-html", "text/html")
	assert.Equal(t, "<!-- fps filtered: reddit-promotions/promoted-post-html -->", m)
}

func TestMarkerNone(t *testing.T) {
	m := Marker(PlaceholderNone, "reddit-promotions", "promoted-post-html", "text/html")
	assert.Empty(t, m)
}

func TestMarkerVisibleJSON(t *testing.T) {
	m := Marker(PlaceholderVisible, "reddit-promotions", "promoted-post-json", "application/json")
	assert.Equal(t, `{"fps_filtered":"reddit-promotions/promoted-post-json"}`, m)
}

func TestMarkerCommentJSON(t *testing.T) {
	m := Marker(PlaceholderComment, "reddit-promotions", "promoted-post-json", "application/json")
	assert.Equal(t, `{"_fps_filtered":"reddit-promotions/promoted-post-json"}`, m)
}

func TestMarkerNoneJSON(t *testing.T) {
	m := Marker(PlaceholderNone, "reddit-promotions", "promoted-post-json", "application/json")
	assert.Empty(t, m)
}

func TestMarkerJSONWithCharset(t *testing.T) {
	m := Marker(PlaceholderVisible, "test", "rule1", "application/json; charset=utf-8")
	assert.Contains(t, m, "fps_filtered")
}

// --- IsTextContentType tests ---

func TestIsTextContentType(t *testing.T) {
	tests := []struct {
		ct   string
		want bool
	}{
		{"text/html", true},
		{"text/html; charset=utf-8", true},
		{"text/plain", true},
		{"text/css", true},
		{"text/vnd.reddit.partial+html", true},
		{"application/json", true},
		{"application/javascript", true},
		{"application/xml", true},
		{"application/json; charset=utf-8", true},
		{"image/png", false},
		{"image/jpeg", false},
		{"video/mp4", false},
		{"application/octet-stream", false},
		{"font/woff2", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.ct, func(t *testing.T) {
			assert.Equal(t, tt.want, IsTextContentType(tt.ct))
		})
	}
}

// --- Registry and InitPlugins tests ---

// mockFilter is a test ContentFilter that records calls.
type mockFilter struct {
	name     string
	version  string
	domains  []string
	initCfg  PluginConfig
	initErr  error
	filterFn func(*http.Request, *http.Response, []byte) ([]byte, FilterResult, error)
}

func (m *mockFilter) Name() string      { return m.name }
func (m *mockFilter) Version() string    { return m.version }
func (m *mockFilter) Domains() []string  { return m.domains }
func (m *mockFilter) Init(cfg PluginConfig, _ *slog.Logger) error {
	m.initCfg = cfg
	return m.initErr
}
func (m *mockFilter) Filter(req *http.Request, resp *http.Response, body []byte) ([]byte, FilterResult, error) {
	if m.filterFn != nil {
		return m.filterFn(req, resp, body)
	}
	return body, FilterResult{}, nil
}

func TestInitPluginsBasic(t *testing.T) {
	// Register a test plugin.
	mock := &mockFilter{name: "test-plugin", version: "1.0.0", domains: []string{"example.com"}}
	Registry["test-plugin"] = func() ContentFilter { return mock }
	defer delete(Registry, "test-plugin")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	configs := map[string]PluginConfig{
		"test-plugin": {
			Enabled: true,
			Mode:    ModeFilter,
			Options: map[string]any{"log_matches": true},
		},
	}

	results, err := InitPlugins(configs, []string{"example.com"}, logger)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "test-plugin", results[0].Plugin.Name())
	assert.Equal(t, ModeFilter, results[0].Config.Mode)
	assert.Equal(t, PlaceholderVisible, results[0].Config.Placeholder) // default
}

func TestInitPluginsDisabledSkipped(t *testing.T) {
	Registry["disabled-plugin"] = func() ContentFilter {
		return &mockFilter{name: "disabled-plugin", domains: []string{"disabled.com"}}
	}
	defer delete(Registry, "disabled-plugin")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	configs := map[string]PluginConfig{
		"disabled-plugin": {Enabled: false},
	}

	results, err := InitPlugins(configs, []string{"disabled.com"}, logger)
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestInitPluginsUnknownRegistry(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	configs := map[string]PluginConfig{
		"nonexistent": {Enabled: true},
	}

	_, err := InitPlugins(configs, []string{}, logger)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found in registry")
}

func TestInitPluginsInvalidMode(t *testing.T) {
	Registry["mode-test"] = func() ContentFilter {
		return &mockFilter{name: "mode-test", domains: []string{"example.com"}}
	}
	defer delete(Registry, "mode-test")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	configs := map[string]PluginConfig{
		"mode-test": {Enabled: true, Mode: "bogus"},
	}

	_, err := InitPlugins(configs, []string{"example.com"}, logger)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mode must be")
}

func TestInitPluginsInvalidPlaceholder(t *testing.T) {
	Registry["ph-test"] = func() ContentFilter {
		return &mockFilter{name: "ph-test", domains: []string{"example.com"}}
	}
	defer delete(Registry, "ph-test")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	configs := map[string]PluginConfig{
		"ph-test": {Enabled: true, Placeholder: "bogus"},
	}

	_, err := InitPlugins(configs, []string{"example.com"}, logger)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "placeholder must be")
}

func TestInitPluginsDomainNotInMITM(t *testing.T) {
	Registry["domain-test"] = func() ContentFilter {
		return &mockFilter{name: "domain-test", domains: []string{"other.com"}}
	}
	defer delete(Registry, "domain-test")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	configs := map[string]PluginConfig{
		"domain-test": {Enabled: true},
	}

	_, err := InitPlugins(configs, []string{"example.com"}, logger)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not in mitm.domains")
}

func TestInitPluginsDuplicateDomain(t *testing.T) {
	Registry["dup-a"] = func() ContentFilter {
		return &mockFilter{name: "dup-a", domains: []string{"shared.com"}}
	}
	Registry["dup-b"] = func() ContentFilter {
		return &mockFilter{name: "dup-b", domains: []string{"shared.com"}}
	}
	defer delete(Registry, "dup-a")
	defer delete(Registry, "dup-b")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	configs := map[string]PluginConfig{
		"dup-a": {Enabled: true},
		"dup-b": {Enabled: true},
	}

	_, err := InitPlugins(configs, []string{"shared.com"}, logger)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already claimed")
}

func TestInitPluginsConfigDomainOverride(t *testing.T) {
	Registry["override-test"] = func() ContentFilter {
		return &mockFilter{name: "override-test", domains: []string{"builtin.com"}}
	}
	defer delete(Registry, "override-test")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	configs := map[string]PluginConfig{
		"override-test": {
			Enabled: true,
			Domains: []string{"custom.com"},
		},
	}

	results, err := InitPlugins(configs, []string{"custom.com"}, logger)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, []string{"custom.com"}, results[0].Config.Domains)
}

// --- BuildResponseModifier tests ---

func TestBuildResponseModifierEmpty(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mod := BuildResponseModifier(nil, nil, nil, logger)
	assert.Nil(t, mod)
}

func TestBuildResponseModifierDispatch(t *testing.T) {
	mock := &mockFilter{
		name:    "test",
		version: "1.0",
		domains: []string{"example.com"},
		filterFn: func(_ *http.Request, _ *http.Response, body []byte) ([]byte, FilterResult, error) {
			return []byte("modified"), FilterResult{Matched: true, Modified: true, Rule: "test-rule", Removed: 1}, nil
		},
	}

	results := []InitResult{{
		Plugin: mock,
		Config: PluginConfig{
			Enabled:     true,
			Mode:        ModeFilter,
			Placeholder: PlaceholderVisible,
			Domains:     []string{"example.com"},
			Options:     map[string]any{},
		},
	}}

	var inspectCalled bool
	onInspect := func(name string) {
		inspectCalled = true
		assert.Equal(t, "test", name)
	}

	var matchCalled bool
	onMatch := func(name, rule string, modified bool, removed int) {
		matchCalled = true
		assert.Equal(t, "test", name)
		assert.Equal(t, "test-rule", rule)
		assert.True(t, modified)
		assert.Equal(t, 1, removed)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mod := BuildResponseModifier(results, onInspect, onMatch, logger)
	require.NotNil(t, mod)

	req := &http.Request{URL: &url.URL{Path: "/test"}, Method: "GET"}
	resp := &http.Response{StatusCode: 200, Header: http.Header{}}
	body, err := mod("example.com", req, resp, []byte("original"))
	require.NoError(t, err)
	assert.Equal(t, "modified", string(body))
	assert.True(t, inspectCalled)
	assert.True(t, matchCalled)
}

func TestBuildResponseModifierNoMatchPassthrough(t *testing.T) {
	mock := &mockFilter{
		name:    "test",
		version: "1.0",
		domains: []string{"example.com"},
	}

	results := []InitResult{{
		Plugin: mock,
		Config: PluginConfig{
			Enabled: true,
			Mode:    ModeFilter,
			Domains: []string{"example.com"},
			Options: map[string]any{},
		},
	}}

	var inspectCalled bool
	onInspect := func(_ string) {
		inspectCalled = true
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mod := BuildResponseModifier(results, onInspect, nil, logger)
	require.NotNil(t, mod)

	req := &http.Request{URL: &url.URL{Path: "/test"}, Method: "GET"}
	resp := &http.Response{StatusCode: 200, Header: http.Header{}}

	// Unknown domain: passthrough — onInspect should NOT be called.
	body, err := mod("other.com", req, resp, []byte("original"))
	require.NoError(t, err)
	assert.Equal(t, "original", string(body))
	assert.False(t, inspectCalled, "onInspect should not be called for unknown domains")
}

// --- Interception filter tests ---

func TestInterceptionFilterCapture(t *testing.T) {
	tmpDir := t.TempDir()

	f := NewInterceptionFilter("test-intercept", "0.1.0", []string{"example.com"})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	err := f.Init(PluginConfig{
		Enabled: true,
		Mode:    ModeIntercept,
		Options: map[string]any{"data_dir": tmpDir},
	}, logger)
	require.NoError(t, err)

	req := &http.Request{
		Method: "GET",
		URL:    &url.URL{Scheme: "https", Host: "example.com", Path: "/page"},
		Host:   "example.com",
		Header: http.Header{"Accept": []string{"text/html"}},
	}
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
	}
	body := []byte("<html><body>Hello</body></html>")

	out, result, filterErr := f.Filter(req, resp, body)
	require.NoError(t, filterErr)

	// Body unchanged (interception mode).
	assert.Equal(t, body, out)
	assert.False(t, result.Matched)
	assert.False(t, result.Modified)

	// Files written.
	entries, err := os.ReadDir(f.outputDir)
	require.NoError(t, err)
	assert.Len(t, entries, 3) // 001-req.json, 001-resp.json, 001-body.html

	// Check body file content.
	bodyData, err := os.ReadFile(filepath.Join(f.outputDir, "001-body.html"))
	require.NoError(t, err)
	assert.Equal(t, "<html><body>Hello</body></html>", string(bodyData))
}

func TestInterceptionFilterSequencing(t *testing.T) {
	tmpDir := t.TempDir()

	f := NewInterceptionFilter("test-seq", "0.1.0", []string{"example.com"})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	err := f.Init(PluginConfig{
		Enabled: true,
		Mode:    ModeIntercept,
		Options: map[string]any{"data_dir": tmpDir},
	}, logger)
	require.NoError(t, err)

	req := &http.Request{
		Method: "GET",
		URL:    &url.URL{Path: "/page1"},
		Host:   "example.com",
		Header: http.Header{},
	}
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}

	// Two captures.
	_, _, _ = f.Filter(req, resp, []byte(`{"a":1}`))
	_, _, _ = f.Filter(req, resp, []byte(`{"b":2}`))

	entries, err := os.ReadDir(f.outputDir)
	require.NoError(t, err)
	assert.Len(t, entries, 6) // 001-* (3 files) + 002-* (3 files)

	// Second body file should be JSON.
	bodyData, err := os.ReadFile(filepath.Join(f.outputDir, "002-body.json"))
	require.NoError(t, err)
	assert.Equal(t, `{"b":2}`, string(bodyData))
}

func TestInterceptionFilterOutputDirPermissions(t *testing.T) {
	tmpDir := t.TempDir()

	f := NewInterceptionFilter("perm-test", "0.1.0", []string{"example.com"})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	err := f.Init(PluginConfig{
		Enabled: true,
		Mode:    ModeIntercept,
		Options: map[string]any{"data_dir": tmpDir},
	}, logger)
	require.NoError(t, err)

	// Check that the intercepts directory has restricted permissions.
	interceptsDir := filepath.Join(tmpDir, "intercepts", "perm-test")
	info, err := os.Stat(interceptsDir)
	require.NoError(t, err)
	// The directory should have 0700 permissions (owner only).
	assert.Equal(t, os.FileMode(0700), info.Mode().Perm())
}

// --- Body extension tests ---

func TestBodyExtension(t *testing.T) {
	tests := []struct {
		ct  string
		ext string
	}{
		{"text/html; charset=utf-8", ".html"},
		{"text/html", ".html"},
		{"text/vnd.reddit.partial+html", ".html"},
		{"application/json", ".json"},
		{"application/javascript", ".js"},
		{"application/xml", ".xml"},
		{"text/xml", ".xml"},
		{"text/plain", ".txt"},
		{"text/css", ".txt"},
		{"image/png", ".bin"},
		{"", ".bin"},
	}

	for _, tt := range tests {
		t.Run(tt.ct, func(t *testing.T) {
			assert.Equal(t, tt.ext, bodyExtension(tt.ct))
		})
	}
}

// Reddit registration and filter tests are in reddit_test.go.
