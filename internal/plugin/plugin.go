/*
Package plugin defines the content filter plugin architecture for MITM response
modification.

Plugins are site-specific filters compiled statically into the proxy binary.
Each plugin targets a set of domains and implements the ContentFilter interface
to inspect and optionally modify HTTP responses during MITM interception.

Plugins operate in two modes:
  - "intercept": captures request/response data to disk without modification
  - "filter": applies content filtering rules, replacing matched content
    with placeholder markers
*/
package plugin

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

// ContentFilter inspects and optionally modifies HTTP responses during MITM.
// Each plugin targets a specific site or set of domains.
type ContentFilter interface {
	// Name returns the plugin identifier (e.g., "reddit-promotions").
	// Must be unique across all registered plugins.
	Name() string

	// Version returns the plugin version (e.g., "0.1.0").
	// Reported in stats and heartbeat.
	Version() string

	// Domains returns the set of domains this plugin handles.
	// The plugin is only invoked for responses from these domains.
	Domains() []string

	// Init is called once at startup with the plugin's config and a logger.
	// Returns an error if the plugin cannot start (missing config, etc.).
	Init(cfg PluginConfig, logger *slog.Logger) error

	// Filter inspects an HTTP response and returns the (possibly modified) body.
	// Called only for text-based Content-Types from matching domains.
	//
	// Parameters:
	//   - req: the client's HTTP request (URL, headers, method)
	//   - resp: the upstream's HTTP response (status, headers — body already read)
	//   - body: the full response body bytes
	//
	// Returns:
	//   - modified body (or input unchanged for no-op)
	//   - FilterResult with match metadata
	//   - error (breaks the MITM session if non-nil)
	Filter(req *http.Request, resp *http.Response, body []byte) ([]byte, FilterResult, error)
}

// FilterResult reports what the plugin did with a response.
type FilterResult struct {
	Matched  bool   // true if the response contained filterable content
	Modified bool   // true if the body was actually changed
	Rule     string // which rule matched (for stats/logging), empty if no match
	Removed  int    // number of content elements removed in this response
}

// PluginConfig holds per-plugin configuration from fpsd.yml.
type PluginConfig struct {
	Enabled     bool
	Mode        string         // "intercept" or "filter"
	Placeholder string         // placeholder mode: "visible", "comment", "none"
	Domains     []string       // domains this plugin handles (overrides built-in)
	Options     map[string]any // plugin-specific key-value pairs
}

// Placeholder mode constants.
const (
	PlaceholderVisible = "visible"
	PlaceholderComment = "comment"
	PlaceholderNone    = "none"
)

// Mode constants.
const (
	ModeIntercept = "intercept"
	ModeFilter    = "filter"
)

// Marker generates a placeholder string for filtered content.
// contentType should be the response Content-Type (used to pick HTML vs JSON format).
// Returns empty string when mode is "none".
func Marker(mode, pluginName, ruleName, contentType string) string {
	if mode == PlaceholderNone {
		return ""
	}

	label := fmt.Sprintf("%s/%s", pluginName, ruleName)

	if isJSONContentType(contentType) {
		if mode == PlaceholderComment {
			// JSON has no comment syntax; use a minimal object.
			return `{"_fps_filtered":"` + label + `"}`
		}
		return `{"fps_filtered":"` + label + `"}`
	}

	// HTML/text content.
	if mode == PlaceholderComment {
		return fmt.Sprintf("<!-- fps filtered: %s -->", label)
	}

	// Visible mode: styled HTML div.
	return fmt.Sprintf(
		`<div style="background:#1a1a2e;color:#e0e0e0;padding:8px 12px;margin:4px 0;`+
			`border-left:3px solid #e94560;font:12px/1.4 monospace;border-radius:3px">`+
			`&#x1f6e1; fps filtered: %s</div>`,
		label,
	)
}

// IsTextContentType returns true if the Content-Type is text-based and should
// be buffered for plugin inspection.
func IsTextContentType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	// Strip parameters (e.g., "text/html; charset=utf-8" -> "text/html").
	if idx := strings.IndexByte(ct, ';'); idx >= 0 {
		ct = strings.TrimSpace(ct[:idx])
	}

	if strings.HasPrefix(ct, "text/") {
		return true
	}

	switch ct {
	case "application/json", "application/javascript", "application/xml":
		return true
	}
	return false
}

// MaxBufferSize is the maximum response body size that will be buffered
// for plugin inspection. Responses larger than this stream through unmodified.
const MaxBufferSize = 10 * 1024 * 1024 // 10MB

// isJSONContentType returns true if the content type indicates JSON.
func isJSONContentType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if idx := strings.IndexByte(ct, ';'); idx >= 0 {
		ct = strings.TrimSpace(ct[:idx])
	}
	return ct == "application/json"
}
