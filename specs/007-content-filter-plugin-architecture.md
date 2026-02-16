# Spec 007: Content Filter Plugin Architecture

**Status**: COMPLETE
**Created**: 2026-02-16
**Depends on**: Spec 006 (MITM TLS interception)

## Problem Statement

Spec 006 established the MITM infrastructure: TLS interception, HTTP proxy loop, and a `ResponseModifier` hook. The hook exists but is nil — responses stream through unmodified. To actually filter same-domain ads (e.g., Reddit promoted posts), the proxy needs content-aware filtering logic.

Content filtering is inherently site-specific. Reddit serves promoted posts as HTML fragments via `/svc/shreddit/` endpoints. Another site would use completely different ad delivery mechanisms. A single generic filter would either be too broad (breaking content) or too narrow (missing ads). Each site needs its own filter built from direct observation of its traffic patterns.

This spec defines the **plugin architecture** that hosts these site-specific filters, the **development workflow** for creating new filters through traffic interception and analysis, and the **runtime integration** that wires plugins into the MITM handler.

## Architecture

### Plugin Interface

A plugin is a Go module that implements the `ContentFilter` interface. Plugins are compiled statically into the proxy binary — no runtime loading.

```go
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
    Placeholder string         // placeholder mode: "visible", "comment", "none"
    Options     map[string]any // plugin-specific key-value pairs
}
```

### Plugin Registry

The registry is a compile-time list of available plugins. Adding a new plugin means adding its constructor to the registry and rebuilding.

```go
// Registry maps plugin names to constructor functions.
var Registry = map[string]func() ContentFilter{
    "reddit-promotions": func() ContentFilter { return reddit.NewPromotionsFilter() },
}
```

At startup, the proxy:

1. Reads the `plugins` config section
2. For each enabled plugin, looks up its constructor in the registry
3. Calls `Init()` with the plugin's config and logger
4. Validates that the plugin's domains are a subset of `mitm.domains` (warning if not)
5. Builds a domain-to-plugin lookup map for the response modifier

A domain can have at most one plugin. If multiple plugins claim the same domain, startup fails with an error.

### Response Modifier Integration

The plugin registry produces a single `ResponseModifier` function that dispatches to the correct plugin based on domain:

```go
// buildResponseModifier creates a ResponseModifier that dispatches to plugins.
func buildResponseModifier(plugins []ContentFilter) mitm.ResponseModifier {
    lookup := map[string]ContentFilter{}
    for _, p := range plugins {
        for _, d := range p.Domains() {
            lookup[strings.ToLower(d)] = p
        }
    }
    return func(domain string, req *http.Request, resp *http.Response, body []byte) ([]byte, error) {
        plugin, ok := lookup[strings.ToLower(domain)]
        if !ok {
            return body, nil // no plugin for this domain, passthrough
        }
        modified, result, err := plugin.Filter(req, resp, body)
        // ... record stats, log result ...
        return modified, err
    }
}
```

This function is assigned to `mitmInterceptor.ResponseModifier` at startup.

### Placeholder Markers

When a plugin removes content (e.g., stripping a promoted post from an HTML fragment), the removed content is replaced with a **placeholder marker** instead of being silently deleted. This serves two purposes: during development it makes filtered elements visible so testers can confirm the right content was removed, and in regular use it provides a clear signal that the proxy is working.

**Placeholder modes** (configured per-plugin via `placeholder` in config):

| Mode | Behavior | Use case |
| ---- | -------- | -------- |
| `visible` (default) | Inserts a visible HTML/text marker at the removal site | Development, testing, verification |
| `comment` | Inserts an HTML comment (`<!-- fps: ... -->`) that is invisible to the user but visible in page source | Production use where you still want traceability |
| `none` | Removes content with no marker — clean deletion | Production use, minimal footprint |

**Marker format**: The placeholder includes the plugin name and the rule that matched, so it is clear what was filtered and why.

Visible mode example (HTML context):

```html
<div style="background:#1a1a2e;color:#e0e0e0;padding:8px 12px;margin:4px 0;
     border-left:3px solid #e94560;font:12px/1.4 monospace;border-radius:3px">
  &#x1f6e1; fps filtered: reddit-promotions/promoted-post-html
</div>
```

Comment mode example:

```html
<!-- fps filtered: reddit-promotions/promoted-post-html -->
```

For JSON responses, the marker adapts to the content type:

```json
{"fps_filtered": "reddit-promotions/promoted-post-json"}
```

**Plugin responsibility**: Plugins receive the placeholder mode via `PluginConfig.Placeholder` during `Init()`. The `Filter()` method is responsible for inserting the appropriate marker when it removes content. A shared helper function (`plugin.Marker(mode, pluginName, ruleName, contentType)`) generates the correct marker string for the given mode and content type, so individual plugins don't need to implement marker formatting.

```go
// Marker generates a placeholder string for filtered content.
// contentType should be the response Content-Type (used to pick HTML vs JSON format).
// Returns empty string when mode is "none".
func Marker(mode, pluginName, ruleName, contentType string) string
```

### Activating Response Buffering in proxyLoop

The proxyLoop in `handler.go` currently streams all responses unbuffered. When `ResponseModifier` is non-nil, responses with text-based Content-Types must be buffered so the modifier can inspect them.

**Content-Type check**: Buffer only when Content-Type starts with `text/`, or is `application/json`, `application/javascript`, `application/xml`. All other types (images, video, fonts, binary downloads) stream through unbuffered.

**Body size limit**: Responses larger than 10MB are not buffered (stream through). This prevents memory issues from abnormally large responses.

**Chunked encoding**: After modification, if the original response used `Content-Length`, update the header to reflect the new body size. If chunked, write the modified body as a single chunk.

### Interception Mode

Before writing a filter, you need to understand the site's traffic patterns. Interception mode captures MITM session data for offline analysis.

An interception plugin is a `ContentFilter` implementation that **does not modify responses** but saves request/response pairs to disk for later analysis.

```go
// InterceptionFilter captures MITM traffic to disk without modification.
type InterceptionFilter struct {
    name     string
    version  string
    domains  []string
    outputDir string
    logger   *slog.Logger
}
```

**What it captures** (per request-response cycle):

- Request: method, URL, headers
- Response: status code, headers, Content-Type
- Response body (text-based only, same Content-Type filter as regular plugins)
- Timestamp

**Storage format**: Each captured session is a directory under `<data-dir>/intercepts/<plugin-name>/`. Each request-response pair is saved as a numbered pair of files:

```
intercepts/reddit-promotions/
  2026-02-16T14:05:40/
    001-req.json      # {method, url, headers}
    001-resp.json     # {status, headers, content_type}
    001-body.html     # response body (text content only)
    002-req.json
    002-resp.json
    002-body.json
    ...
```

**Activation**: Set the plugin mode to `intercept` in config:

```yaml
plugins:
  reddit-promotions:
    enabled: true
    mode: "intercept"    # "intercept" captures traffic; "filter" applies rules
    domains:
      - www.reddit.com
```

When mode is `intercept`, the plugin saves data but returns the body unchanged. When mode is `filter`, the plugin applies its filtering rules. This way the same plugin supports both the investigation and filtering phases.

### Development Workflow

Creating a new content filter follows this process:

1. **Configure interception**: Add the plugin to `fpsd.yml` with `mode: intercept`. Add the target domains to `mitm.domains` if not already there.

2. **Browse the site**: Use the proxy normally. The interception plugin captures all text-based request/response pairs to disk.

3. **Analyze captures**: Examine the saved data for ad markers. For Reddit, this means looking for `promoted`, `is_promoted`, ad labels in HTML fragments, and ad-related JSON fields in API responses. AI-assisted analysis compares ad-containing responses to clean content responses.

4. **Write a plugin spec**: Based on the analysis, write a spec (e.g., `specs/007a-reddit-promotions-filter.md`) describing the exact filtering rules: which URLs to target, what patterns to match, how to modify the response.

5. **Implement the filter**: Code the plugin's `Filter()` method according to the spec. Switch config to `mode: filter`.

6. **Test iteratively**: Browse the site with filtering active. If ads are missed or content is broken, go back to step 2 — interception mode is always available for re-analysis.

This is an iterative process. Plugin specs are versioned alongside the code. Each iteration refines the filter rules based on observed traffic.

### Config

```yaml
# Plugins — site-specific content filters for MITM'd domains.
plugins:
  reddit-promotions:
    enabled: true
    mode: "filter"       # "intercept" or "filter"
    placeholder: "visible" # "visible", "comment", or "none"
    domains:             # must be subset of mitm.domains
      - www.reddit.com
    options:             # plugin-specific (passed to Init)
      log_matches: true
```

**Defaults**: All plugins disabled by default. Mode defaults to `filter` if omitted. Placeholder defaults to `visible` if omitted. Domains default to the plugin's built-in domain list if omitted.

**Validation**:

- Plugin name must exist in the registry
- Plugin domains must be a subset of `mitm.domains` (error if domain not in MITM list — the MITM handler won't intercept it, so the plugin can never fire)
- Mode must be `intercept` or `filter`
- Placeholder must be `visible`, `comment`, or `none`
- No two plugins can claim the same domain

### Stats

The stats endpoint (`/fps/stats`) gains a new `plugins` section:

```json
{
  "plugins": {
    "active": 1,
    "filters": [
      {
        "name": "reddit-promotions",
        "version": "0.1.0",
        "mode": "filter",
        "domains": ["www.reddit.com"],
        "responses_inspected": 842,
        "responses_matched": 23,
        "responses_modified": 23,
        "top_rules": [
          {"rule": "promoted-post-html", "count": 18},
          {"rule": "promoted-post-json", "count": 5}
        ]
      }
    ]
  }
}
```

The heartbeat gains:

```json
{
  "plugins_active": 1,
  "plugins": ["reddit-promotions@0.1.0"]
}
```

### Logging

**Startup** (info level):

- Plugin loaded: name, version, mode, domains
- If interception mode: output directory path
- Domain overlap warnings (plugin domain not in MITM list)

**Per-filter match** (info level when `log_matches: true`, debug otherwise):

- `plugin filter match name=reddit-promotions rule=promoted-post-html url=/svc/shreddit/feeds/popular-feed method=GET status=200 body_delta=-2841 placeholder=visible removed=1`
- `body_delta` shows bytes removed (negative) or added (positive)

**Interception capture** (debug level):

- `plugin intercept saved name=reddit-promotions url=/svc/shreddit/feeds/popular-feed content_type=text/vnd.reddit.partial+html body_bytes=84210 path=intercepts/reddit-promotions/2026-02-16T14:05:40/042-body.html`

## File Changes

| File | Change |
| ---- | ------ |
| `internal/plugin/plugin.go` | New — `ContentFilter` interface, `FilterResult`, `PluginConfig`, `Registry`, `Marker()` helper |
| `internal/plugin/registry.go` | New — Plugin registry, domain-to-plugin lookup builder, response modifier composer |
| `internal/plugin/intercept.go` | New — Generic interception plugin (captures traffic to disk) |
| `internal/plugin/plugin_test.go` | New — Tests for registry, dispatch, interception capture |
| `internal/mitm/handler.go` | Activate `ResponseModifier` in proxyLoop: Content-Type check, body buffering, modifier call |
| `internal/config/config.go` | Add `Plugins` config section |
| `internal/stats/collector.go` | Add plugin filter counters (inspected, matched, modified, per-rule) |
| `internal/probe/probe.go` | Add `PluginsBlock` to stats response, `plugins_active` to heartbeat |
| `cmd/fpsd/main.go` | Plugin initialization: registry lookup, Init(), domain validation, wire ResponseModifier |

## Acceptance Criteria

- [ ] `ContentFilter` interface defined with `Name()`, `Version()`, `Domains()`, `Init()`, `Filter()`
- [ ] `FilterResult` struct captures match metadata (matched, modified, rule name)
- [ ] Plugin registry maps names to constructors; adding a plugin is one line of code
- [ ] Registry rejects duplicate domain claims across plugins at startup
- [ ] Plugin domains validated as subset of `mitm.domains` at startup
- [ ] `plugins` config section parsed from `fpsd.yml` with `enabled`, `mode`, `domains`, `options`
- [ ] Config validation: plugin name exists in registry, mode is `intercept` or `filter`, placeholder is `visible`/`comment`/`none`, domains valid
- [ ] `ResponseModifier` activated in proxyLoop when non-nil: body buffered for text Content-Types, modifier called
- [ ] Binary responses (images, video, fonts) bypass buffering even when modifier is set
- [ ] Responses over 10MB body size bypass buffering (stream through)
- [ ] Modified responses update `Content-Length` header to match new body size
- [ ] Interception mode: saves request metadata + response body to `intercepts/<plugin>/` directory
- [ ] Interception mode returns body unchanged (no modification)
- [ ] Filter mode: calls plugin's `Filter()` method, uses returned body
- [ ] Placeholder markers: `visible` mode inserts styled HTML div (or JSON object) at removal site
- [ ] Placeholder markers: `comment` mode inserts HTML comment at removal site
- [ ] Placeholder markers: `none` mode removes content with no marker
- [ ] Placeholder defaults to `visible` when not specified in config
- [ ] Shared `Marker()` helper generates correct format for HTML and JSON content types
- [ ] Plugin stats in `/fps/stats`: per-plugin inspected/matched/modified counts, top rules
- [ ] Heartbeat shows `plugins_active` count and plugin name@version list
- [ ] Startup logs plugin name, version, mode, and domains
- [ ] `log_matches: true` option logs filter matches at info level
- [ ] All existing tests pass (no regression)
- [ ] New tests: registry dispatch, Content-Type filtering, interception capture, stats recording, placeholder marker generation (all three modes, HTML and JSON)
- [ ] Verified locally: interception mode captures Reddit traffic to disk
- [ ] Verified locally: with no plugins enabled, MITM passthrough behavior unchanged

## Out of Scope

- Actual filtering rules for any specific site (follow-up plugin specs)
- Runtime plugin loading (Go doesn't support this cleanly; static compilation)
- Plugin hot-reload (restart required to change plugin config)
- Multiple plugins per domain
- Plugin-to-plugin chaining (single plugin per domain)
- Response body decompression (assume upstream sends uncompressed when proxy doesn't advertise Accept-Encoding)
- WebSocket interception
- HTTP/2 support in MITM connections

## Security Considerations

- **Interception data on disk**: Captured traffic may contain session tokens, cookies, and personal content. The `intercepts/` directory should have restricted permissions (0700). Interception mode is a development tool, not a production feature.
- **Body buffering memory**: Buffering responses consumes memory proportional to response size. The 10MB cap prevents unbounded growth. With a small number of MITM domains and typical web page sizes (100KB-1MB), memory usage is bounded.
- **Plugin trust**: Plugins are compiled into the binary. There is no untrusted code execution. A malicious plugin would require modifying the source code and rebuilding.
- **Modified content integrity**: Filters that remove content (e.g., stripping promoted posts) change what the user sees. This is the intended behavior. Placeholder markers are the only content the proxy injects, and they contain only the plugin name and rule name — no user data, no tracking, no external resources. The `none` placeholder mode eliminates injected content entirely.
