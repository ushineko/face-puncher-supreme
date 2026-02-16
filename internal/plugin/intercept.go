package plugin

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// InterceptionFilter captures MITM traffic to disk without modification.
// It implements ContentFilter with a no-op filter that saves request/response
// pairs for offline analysis.
type InterceptionFilter struct {
	name      string
	version   string
	domains   []string
	outputDir string
	logger    *slog.Logger
	sequence  atomic.Int64
	sessionID string
}

// NewInterceptionFilter creates a new interception filter. The name, version,
// and domains define the plugin identity; actual output directory is set during Init.
func NewInterceptionFilter(name, version string, domains []string) *InterceptionFilter {
	return &InterceptionFilter{
		name:    name,
		version: version,
		domains: domains,
	}
}

func (f *InterceptionFilter) Name() string      { return f.name }
func (f *InterceptionFilter) Version() string    { return f.version }
func (f *InterceptionFilter) Domains() []string  { return f.domains }

// Init sets up the interception output directory. The data_dir is read from
// Options["data_dir"] (set by main during plugin init).
func (f *InterceptionFilter) Init(cfg PluginConfig, logger *slog.Logger) error {
	f.logger = logger

	dataDir := "."
	if v, ok := cfg.Options["data_dir"]; ok {
		if s, ok := v.(string); ok && s != "" {
			dataDir = s
		}
	}

	// Session ID for this run — all captures go into this subdirectory.
	f.sessionID = time.Now().Format("2006-01-02T15-04-05")
	f.outputDir = filepath.Join(dataDir, "intercepts", f.name, f.sessionID)

	if err := os.MkdirAll(f.outputDir, 0700); err != nil {
		return fmt.Errorf("create intercept output dir: %w", err)
	}

	logger.Info("interception mode active",
		"output_dir", f.outputDir,
	)

	return nil
}

// Filter captures the request/response pair to disk and returns the body
// unchanged (interception mode does not modify responses).
//nolint:unparam // FilterResult intentionally zero — interception never modifies
func (f *InterceptionFilter) Filter(
	req *http.Request, resp *http.Response, body []byte,
) ([]byte, FilterResult, error) {
	seq := f.sequence.Add(1)

	// Save request metadata.
	reqData := map[string]any{
		"method":  req.Method,
		"url":     req.URL.String(),
		"host":    req.Host,
		"headers": flattenHeaders(req.Header),
	}

	// Save response metadata.
	respData := map[string]any{
		"status":       resp.StatusCode,
		"content_type": resp.Header.Get("Content-Type"),
		"headers":      flattenHeaders(resp.Header),
	}

	prefix := fmt.Sprintf("%03d", seq)

	if err := writeJSON(filepath.Join(f.outputDir, prefix+"-req.json"), reqData); err != nil {
		f.logger.Warn("intercept save failed", "file", prefix+"-req.json", "error", err)
	}
	if err := writeJSON(filepath.Join(f.outputDir, prefix+"-resp.json"), respData); err != nil {
		f.logger.Warn("intercept save failed", "file", prefix+"-resp.json", "error", err)
	}

	// Determine body extension from content type.
	ext := bodyExtension(resp.Header.Get("Content-Type"))
	bodyFile := prefix + "-body" + ext
	if err := os.WriteFile(filepath.Join(f.outputDir, bodyFile), body, 0600); err != nil {
		f.logger.Warn("intercept save failed", "file", bodyFile, "error", err)
	}

	f.logger.Debug("intercept saved",
		"url", req.URL.String(),
		"content_type", resp.Header.Get("Content-Type"),
		"body_bytes", len(body),
		"path", filepath.Join(f.outputDir, bodyFile),
	)

	return body, FilterResult{}, nil
}

// flattenHeaders converts http.Header to a simple map for JSON serialization.
func flattenHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[k] = strings.Join(v, ", ")
	}
	return out
}

// writeJSON marshals data to a JSON file with indentation.
func writeJSON(path string, data any) error {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0600)
}

// bodyExtension returns a file extension based on Content-Type.
func bodyExtension(ct string) string {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if idx := strings.IndexByte(ct, ';'); idx >= 0 {
		ct = strings.TrimSpace(ct[:idx])
	}

	switch {
	case ct == "application/json":
		return ".json"
	case ct == "application/javascript":
		return ".js"
	case ct == "application/xml", ct == "text/xml":
		return ".xml"
	case strings.HasPrefix(ct, "text/html"), strings.Contains(ct, "html"):
		return ".html"
	case strings.HasPrefix(ct, "text/"):
		return ".txt"
	default:
		return ".bin"
	}
}
