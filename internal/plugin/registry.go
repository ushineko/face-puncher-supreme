package plugin

import (
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/ushineko/face-puncher-supreme/internal/mitm"
)

// Registry maps plugin names to constructor functions.
// Adding a new plugin means adding its constructor here and rebuilding.
var Registry = map[string]func() ContentFilter{}

// DefaultPriority is the default priority for plugins that don't specify one.
const DefaultPriority = 100

// PluginStats tracks per-plugin filter statistics reported by the response modifier.
type PluginStats struct {
	Name       string
	Version    string
	Mode       string
	Domains    []string
	Inspected  int64
	Matched    int64
	Modified   int64
	RuleCounts map[string]int64
}

// OnPluginInspect is called by the response modifier when a response is
// dispatched to a plugin for inspection (before the filter runs).
type OnPluginInspect func(pluginName string)

// OnFilterMatch is called by the response modifier when a plugin matches.
// Parameters: pluginName, rule, modified (whether body was changed), removed count.
type OnFilterMatch func(pluginName, rule string, modified bool, removed int)

// InitResult holds an initialized plugin and its resolved configuration.
type InitResult struct {
	Plugin ContentFilter
	Config PluginConfig
}

// InitPlugins initializes all enabled plugins from config, validates domain
// assignments, and returns the initialized plugins.
func InitPlugins(
	configs map[string]PluginConfig,
	mitmDomains []string,
	logger *slog.Logger,
) ([]InitResult, error) {
	// Build MITM domain set for validation.
	mitmSet := make(map[string]struct{}, len(mitmDomains))
	for _, d := range mitmDomains {
		mitmSet[strings.ToLower(d)] = struct{}{}
	}

	// Track domain+priority to detect conflicts.
	// Key: "domain:priority", value: plugin name.
	domainPriority := make(map[string]string)
	var results []InitResult

	for name, cfg := range configs {
		if !cfg.Enabled {
			logger.Debug("plugin disabled", "name", name)
			continue
		}

		// Look up constructor in registry.
		constructor, ok := Registry[name]
		if !ok {
			return nil, fmt.Errorf("plugin %q: not found in registry", name)
		}

		// Validate mode.
		if cfg.Mode == "" {
			cfg.Mode = ModeFilter
		}
		if cfg.Mode != ModeIntercept && cfg.Mode != ModeFilter {
			return nil, fmt.Errorf("plugin %q: mode must be %q or %q, got %q",
				name, ModeIntercept, ModeFilter, cfg.Mode)
		}

		// Validate placeholder.
		if cfg.Placeholder == "" {
			cfg.Placeholder = PlaceholderVisible
		}
		if cfg.Placeholder != PlaceholderVisible &&
			cfg.Placeholder != PlaceholderComment &&
			cfg.Placeholder != PlaceholderNone {
			return nil, fmt.Errorf("plugin %q: placeholder must be %q, %q, or %q, got %q",
				name, PlaceholderVisible, PlaceholderComment, PlaceholderNone, cfg.Placeholder)
		}

		// Default priority.
		if cfg.Priority == 0 {
			cfg.Priority = DefaultPriority
		}

		// Create plugin instance.
		p := constructor()

		// Use config domains if specified, otherwise use plugin's built-in domains.
		domains := cfg.Domains
		if len(domains) == 0 {
			domains = p.Domains()
		}

		// Validate domains are in MITM list and no duplicate priorities per domain.
		for _, d := range domains {
			dl := strings.ToLower(d)
			if _, ok := mitmSet[dl]; !ok {
				return nil, fmt.Errorf("plugin %q: domain %q is not in mitm.domains (plugin cannot fire for non-intercepted domains)", name, d)
			}
			key := fmt.Sprintf("%s:%d", dl, cfg.Priority)
			if owner, exists := domainPriority[key]; exists {
				return nil, fmt.Errorf("plugin %q: priority %d on domain %q conflicts with plugin %q", name, cfg.Priority, d, owner)
			}
			domainPriority[key] = name
		}

		// Override config domains with the resolved list.
		cfg.Domains = domains

		// Initialize the plugin.
		if err := p.Init(&cfg, logger.With("plugin", name)); err != nil {
			return nil, fmt.Errorf("plugin %q: init failed: %w", name, err)
		}

		logger.Info("plugin loaded",
			"name", p.Name(),
			"version", p.Version(),
			"mode", cfg.Mode,
			"placeholder", cfg.Placeholder,
			"priority", cfg.Priority,
			"domains", domains,
		)

		results = append(results, InitResult{Plugin: p, Config: cfg})
	}

	return results, nil
}

// BuildResponseModifier creates a ResponseModifier that dispatches to
// plugins based on domain. Multiple plugins can handle the same domain,
// executing in priority order (lower number first). Each plugin receives
// the output of the previous one.
func BuildResponseModifier(
	results []InitResult,
	onInspect OnPluginInspect,
	onMatch OnFilterMatch,
	logger *slog.Logger,
) mitm.ResponseModifier {
	if len(results) == 0 {
		return nil
	}

	type entry struct {
		plugin   ContentFilter
		cfg      PluginConfig
		priority int
	}

	// Build domain → sorted list of entries.
	lookup := map[string][]entry{}
	for _, r := range results {
		e := entry{plugin: r.Plugin, cfg: r.Config, priority: r.Config.Priority}
		for _, d := range r.Config.Domains {
			dl := strings.ToLower(d)
			lookup[dl] = append(lookup[dl], e)
		}
	}

	// Sort each domain's entries by priority (ascending = lower runs first).
	for d := range lookup {
		sort.Slice(lookup[d], func(i, j int) bool {
			return lookup[d][i].priority < lookup[d][j].priority
		})
	}

	if len(lookup) == 0 {
		return nil
	}

	return func(domain string, req *http.Request, resp *http.Response, body []byte) ([]byte, error) {
		entries, ok := lookup[strings.ToLower(domain)]
		if !ok {
			return body, nil
		}

		current := body
		for _, e := range entries {
			if onInspect != nil {
				onInspect(e.plugin.Name())
			}

			modified, result, err := e.plugin.Filter(req, resp, current)
			if err != nil {
				return nil, fmt.Errorf("plugin %s: %w", e.plugin.Name(), err)
			}

			// Report matches via callback.
			if result.Matched && onMatch != nil {
				if len(result.Rules) > 0 {
					// Multi-rule plugin: report each rule individually.
					for _, rm := range result.Rules {
						onMatch(e.plugin.Name(), rm.Rule, rm.Modified, rm.Count)
					}
				} else {
					// Single-rule plugin: report aggregate.
					onMatch(e.plugin.Name(), result.Rule, result.Modified, result.Removed)
				}
			}

			logMatches := false
			if v, ok := e.cfg.Options["log_matches"]; ok {
				if b, ok := v.(bool); ok {
					logMatches = b
				}
			}

			if result.Matched {
				lvl := slog.LevelDebug
				if logMatches {
					lvl = slog.LevelInfo
				}
				logger.Log(nil, lvl, "plugin filter match", //nolint:staticcheck // nil context is fine for slog
					"name", e.plugin.Name(),
					"rule", result.Rule,
					"url", req.URL.String(),
					"method", req.Method,
					"status", resp.StatusCode,
					"body_delta", len(modified)-len(current),
					"placeholder", e.cfg.Placeholder,
					"removed", result.Removed,
				)
			}

			current = modified
		}

		return current, nil
	}
}
