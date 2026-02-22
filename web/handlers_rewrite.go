package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"time"

	"github.com/ushineko/face-puncher-supreme/internal/plugin"
)

// handleRewriteList returns all rewrite rules.
func (s *DashboardServer) handleRewriteList(w http.ResponseWriter, _ *http.Request) {
	rules, err := s.rewriteStore.List()
	if err != nil {
		s.logger.Error("rewrite list failed", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rules) //nolint:errcheck // best-effort response
}

// handleRewriteCreate creates a new rewrite rule.
func (s *DashboardServer) handleRewriteCreate(w http.ResponseWriter, r *http.Request) {
	var rule plugin.RewriteRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	created, err := s.rewriteStore.Add(rule)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}

	s.reloadRewriteRules()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(created) //nolint:errcheck // best-effort response
}

// handleRewriteGet returns a single rule by ID.
func (s *DashboardServer) handleRewriteGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rule, err := s.rewriteStore.Get(id)
	if err != nil {
		if isNotFound(err) {
			http.Error(w, `{"error":"rule not found"}`, http.StatusNotFound)
		} else {
			s.logger.Error("rewrite get failed", "error", err)
			http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rule) //nolint:errcheck // best-effort response
}

// handleRewriteUpdate replaces a rule's fields.
func (s *DashboardServer) handleRewriteUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var rule plugin.RewriteRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	updated, err := s.rewriteStore.Update(id, rule)
	if err != nil {
		if isNotFound(err) {
			http.Error(w, `{"error":"rule not found"}`, http.StatusNotFound)
		} else {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		}
		return
	}

	s.reloadRewriteRules()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(updated) //nolint:errcheck // best-effort response
}

// handleRewriteDelete removes a rule.
func (s *DashboardServer) handleRewriteDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.rewriteStore.Delete(id); err != nil {
		if isNotFound(err) {
			http.Error(w, `{"error":"rule not found"}`, http.StatusNotFound)
		} else {
			s.logger.Error("rewrite delete failed", "error", err)
			http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		}
		return
	}

	s.reloadRewriteRules()

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck // best-effort response
}

// handleRewriteToggle flips the enabled state of a rule.
func (s *DashboardServer) handleRewriteToggle(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	toggled, err := s.rewriteStore.Toggle(id)
	if err != nil {
		if isNotFound(err) {
			http.Error(w, `{"error":"rule not found"}`, http.StatusNotFound)
		} else {
			s.logger.Error("rewrite toggle failed", "error", err)
			http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		}
		return
	}

	s.reloadRewriteRules()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(toggled) //nolint:errcheck // best-effort response
}

// rewriteTestRequest is the request body for the test endpoint.
type rewriteTestRequest struct {
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
	IsRegex     bool   `json:"is_regex"`
	Sample      string `json:"sample"`
}

// rewriteTestResponse is the response body for the test endpoint.
type rewriteTestResponse struct {
	Result     string `json:"result"`
	MatchCount int    `json:"match_count"`
	Valid      bool   `json:"valid"`
	Error      string `json:"error,omitempty"`
}

// handleRewriteTest tests a pattern against sample text without persisting.
func (s *DashboardServer) handleRewriteTest(w http.ResponseWriter, r *http.Request) {
	var req rewriteTestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	resp := rewriteTestResponse{Valid: true}

	if req.IsRegex {
		re, err := regexp.Compile(req.Pattern)
		if err != nil {
			resp.Valid = false
			resp.Error = err.Error()
			resp.Result = req.Sample
		} else {
			sample := []byte(req.Sample)
			matches := re.FindAllIndex(sample, -1)
			resp.MatchCount = len(matches)
			resp.Result = string(re.ReplaceAll(sample, []byte(req.Replacement)))
		}
	} else {
		sample := []byte(req.Sample)
		pattern := []byte(req.Pattern)
		resp.MatchCount = bytes.Count(sample, pattern)
		resp.Result = string(bytes.ReplaceAll(sample, pattern, []byte(req.Replacement)))
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp) //nolint:errcheck // best-effort response
}

// handleRestart restarts the proxy via systemd if running as a managed service.
func (s *DashboardServer) handleRestart(w http.ResponseWriter, _ *http.Request) {
	if os.Getenv("INVOCATION_ID") == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		//nolint:errcheck // best-effort response
		_, _ = w.Write([]byte(`{"status":"error",` +
			`"message":"Restart is only available when running as a systemd service."}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	//nolint:errcheck // best-effort response
	_, _ = w.Write([]byte(`{"status":"restarting",` +
		`"message":"Proxy is restarting via systemd. You will need to log in again."}`))


	// Flush response, then restart after a short delay.
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	go func() {
		time.Sleep(100 * time.Millisecond)
		cmd := exec.Command("systemctl", "--user", "restart", "fpsd") //nolint:gosec // intentional restart command
		if err := cmd.Start(); err != nil {
			s.logger.Error("failed to restart via systemd", "error", err)
		}
	}()
}

// reloadRewriteRules calls the reload function and logs any errors.
func (s *DashboardServer) reloadRewriteRules() {
	if s.rewriteReloadFn == nil {
		return
	}
	if err := s.rewriteReloadFn(); err != nil {
		s.logger.Error("failed to reload rewrite rules", "error", err)
	}
}

// isNotFound checks if an error message indicates a "not found" condition.
func isNotFound(err error) bool {
	return err != nil && bytes.Contains([]byte(err.Error()), []byte("not found"))
}
