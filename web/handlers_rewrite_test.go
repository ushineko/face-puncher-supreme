package web

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ushineko/face-puncher-supreme/internal/plugin"
)

func testDashboard(t *testing.T) *DashboardServer {
	t.Helper()
	store, err := plugin.OpenRewriteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	return &DashboardServer{
		prefix:          "/fps",
		rewriteStore:    store,
		rewriteReloadFn: func() error { return nil },
		logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestHandleRewriteListEmpty(t *testing.T) {
	s := testDashboard(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/fps/api/rewrite/rules", http.NoBody)
	s.handleRewriteList(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var rules []plugin.RewriteRule
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rules))
	assert.Empty(t, rules)
}

func TestHandleRewriteCRUD(t *testing.T) {
	s := testDashboard(t)

	// Create.
	body := `{"name":"test","pattern":"foo","replacement":"bar","enabled":true}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/fps/api/rewrite/rules", bytes.NewBufferString(body))
	s.handleRewriteCreate(w, r)
	assert.Equal(t, http.StatusCreated, w.Code)

	var created plugin.RewriteRule
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	assert.Equal(t, "test", created.Name)
	assert.NotEmpty(t, created.ID)

	// Get.
	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/fps/api/rewrite/rules/"+created.ID, http.NoBody)
	r.SetPathValue("id", created.ID)
	s.handleRewriteGet(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	var got plugin.RewriteRule
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, created.ID, got.ID)

	// Update.
	updateBody := `{"name":"updated","pattern":"baz","replacement":"qux","enabled":true}`
	w = httptest.NewRecorder()
	r = httptest.NewRequest("PUT", "/fps/api/rewrite/rules/"+created.ID, bytes.NewBufferString(updateBody))
	r.SetPathValue("id", created.ID)
	s.handleRewriteUpdate(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	var updated plugin.RewriteRule
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &updated))
	assert.Equal(t, "updated", updated.Name)

	// Toggle.
	w = httptest.NewRecorder()
	r = httptest.NewRequest("PATCH", "/fps/api/rewrite/rules/"+created.ID+"/toggle", http.NoBody)
	r.SetPathValue("id", created.ID)
	s.handleRewriteToggle(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	var toggled plugin.RewriteRule
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &toggled))
	assert.False(t, toggled.Enabled)

	// Delete.
	w = httptest.NewRecorder()
	r = httptest.NewRequest("DELETE", "/fps/api/rewrite/rules/"+created.ID, http.NoBody)
	r.SetPathValue("id", created.ID)
	s.handleRewriteDelete(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	// Verify deleted.
	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/fps/api/rewrite/rules/"+created.ID, http.NoBody)
	r.SetPathValue("id", created.ID)
	s.handleRewriteGet(w, r)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleRewriteCreateValidation(t *testing.T) {
	s := testDashboard(t)

	// Missing name.
	body := `{"pattern":"foo"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/fps/api/rewrite/rules", bytes.NewBufferString(body))
	s.handleRewriteCreate(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// Invalid regex.
	body = `{"name":"test","pattern":"[bad","is_regex":true}`
	w = httptest.NewRecorder()
	r = httptest.NewRequest("POST", "/fps/api/rewrite/rules", bytes.NewBufferString(body))
	s.handleRewriteCreate(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleRewriteTest(t *testing.T) {
	s := testDashboard(t)

	// Literal test.
	body := `{"pattern":"foo","replacement":"bar","sample":"foo baz foo"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/fps/api/rewrite/test", bytes.NewBufferString(body))
	s.handleRewriteTest(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp rewriteTestResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "bar baz bar", resp.Result)
	assert.Equal(t, 2, resp.MatchCount)
	assert.True(t, resp.Valid)

	// Regex test.
	body = `{"pattern":"\\bfoo\\b","replacement":"bar","is_regex":true,"sample":"foo foobar foo"}`
	w = httptest.NewRecorder()
	r = httptest.NewRequest("POST", "/fps/api/rewrite/test", bytes.NewBufferString(body))
	s.handleRewriteTest(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "bar foobar bar", resp.Result)
	assert.Equal(t, 2, resp.MatchCount)
	assert.True(t, resp.Valid)

	// Invalid regex.
	body = `{"pattern":"[bad","is_regex":true,"sample":"test"}`
	w = httptest.NewRecorder()
	r = httptest.NewRequest("POST", "/fps/api/rewrite/test", bytes.NewBufferString(body))
	s.handleRewriteTest(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.Valid)
	assert.NotEmpty(t, resp.Error)
}

func TestHandleRestartNoSystemd(t *testing.T) {
	s := testDashboard(t)

	// Without INVOCATION_ID, should return 503.
	t.Setenv("INVOCATION_ID", "")
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/fps/api/restart", http.NoBody)
	s.handleRestart(w, r)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandleRewriteGetNotFound(t *testing.T) {
	s := testDashboard(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/fps/api/rewrite/rules/nonexistent", http.NoBody)
	r.SetPathValue("id", "nonexistent")
	s.handleRewriteGet(w, r)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleRewriteDeleteNotFound(t *testing.T) {
	s := testDashboard(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("DELETE", "/fps/api/rewrite/rules/nonexistent", http.NoBody)
	r.SetPathValue("id", "nonexistent")
	s.handleRewriteDelete(w, r)
	assert.Equal(t, http.StatusNotFound, w.Code)
}
