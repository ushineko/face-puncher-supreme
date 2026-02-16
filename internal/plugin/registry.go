package plugin

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/ushineko/face-puncher-supreme/internal/mitm"
)

// Registry maps plugin names to constructor functions.
// Adding a new plugin means adding its constructor here and rebuilding.
var Registry = map[string]func() ContentFilter{}

// PluginStats tracks per-plugin filter statistics reported by the response modifier.
type PluginStats struct {
	Name      string
	Version   string
	Mode      string
	Domains   []string
	Inspected int64
	Matched   int64
	Modified  int64
	RuleCounts map[string]int64
}

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

	// Track domain claims across plugins.
	domainOwner := make(map[string]string)
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

		// Create plugin instance.
		p := constructor()

		// Use config domains if specified, otherwise use plugin's built-in domains.
		domains := cfg.Domains
		if len(domains) == 0 {
			domains = p.Domains()
		}

		// Validate domains are in MITM list.
		for _, d := range domains {
			dl := strings.ToLower(d)
			if _, ok := mitmSet[dl]; !ok {
				return nil, fmt.Errorf("plugin %q: domain %q is not in mitm.domains (plugin cannot fire for non-intercepted domains)", name, d)
			}
			if owner, claimed := domainOwner[dl]; claimed {
				return nil, fmt.Errorf("plugin %q: domain %q is already claimed by plugin %q", name, d, owner)
			}
			domainOwner[dl] = name
		}

		// Override config domains with the resolved list.
		cfg.Domains = domains

		// Initialize the plugin.
		if err := p.Init(cfg, logger.With("plugin", name)); err != nil {
			return nil, fmt.Errorf("plugin %q: init failed: %w", name, err)
		}

		logger.Info("plugin loaded",
			"name", p.Name(),
			"version", p.Version(),
			"mode", cfg.Mode,
			"placeholder", cfg.Placeholder,
			"domains", domains,
		)

		results = append(results, InitResult{Plugin: p, Config: cfg})
	}

	return results, nil
}

// BuildResponseModifier creates a ResponseModifier that dispatches to the
// correct plugin based on domain. Only plugins in "filter" mode are wired
// into the modifier; "intercept" mode plugins handle their own capture in
// their Filter() method.
func BuildResponseModifier(
	results []InitResult,
	onMatch OnFilterMatch,
	logger *slog.Logger,
) mitm.ResponseModifier {
	if len(results) == 0 {
		return nil
	}

	type entry struct {
		plugin ContentFilter
		cfg    PluginConfig
	}

	lookup := map[string]entry{}
	for _, r := range results {
		for _, d := range r.Config.Domains {
			lookup[strings.ToLower(d)] = entry{plugin: r.Plugin, cfg: r.Config}
		}
	}

	if len(lookup) == 0 {
		return nil
	}

	return func(domain string, req *http.Request, resp *http.Response, body []byte) ([]byte, error) {
		e, ok := lookup[strings.ToLower(domain)]
		if !ok {
			return body, nil
		}

		modified, result, err := e.plugin.Filter(req, resp, body)
		if err != nil {
			return nil, fmt.Errorf("plugin %s: %w", e.plugin.Name(), err)
		}

		if result.Matched && onMatch != nil {
			onMatch(e.plugin.Name(), result.Rule, result.Modified, result.Removed)
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
				"body_delta", len(modified)-len(body),
				"placeholder", e.cfg.Placeholder,
				"removed", result.Removed,
			)
		}

		return modified, nil
	}
}
