/*
Package config handles YAML configuration loading, validation, and
CLI flag merging for fpsd.

Configuration is resolved in this order (highest priority first):
  1. CLI flags (explicitly passed)
  2. Config file values
  3. Built-in defaults
*/
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration for fpsd.
type Config struct {
	Listen        string     `yaml:"listen"`
	LogDir        string     `yaml:"log_dir"`
	Verbose       bool       `yaml:"verbose"`
	DataDir       string     `yaml:"data_dir"`
	BlocklistURLs []string   `yaml:"blocklist_urls"`
	Blocklist     []string   `yaml:"blocklist"`
	Allowlist     []string   `yaml:"allowlist"`
	Timeouts      Timeouts   `yaml:"timeouts"`
	Management    Management `yaml:"management"`
	Stats         Stats      `yaml:"stats"`
}

// Timeouts holds proxy timeout configuration.
type Timeouts struct {
	Shutdown   Duration `yaml:"shutdown"`
	Connect    Duration `yaml:"connect"`
	ReadHeader Duration `yaml:"read_header"`
}

// Management holds management endpoint configuration.
type Management struct {
	PathPrefix string `yaml:"path_prefix"`
}

// Stats holds statistics collection configuration.
type Stats struct {
	Enabled       bool     `yaml:"enabled"`
	FlushInterval Duration `yaml:"flush_interval"`
}

// Default returns a Config populated with built-in defaults.
func Default() Config {
	return Config{
		Listen:  ":18737",
		LogDir:  "logs",
		Verbose: false,
		DataDir: ".",
		Timeouts: Timeouts{
			Shutdown:   Duration{5 * time.Second},
			Connect:    Duration{10 * time.Second},
			ReadHeader: Duration{10 * time.Second},
		},
		Management: Management{
			PathPrefix: "/fps",
		},
		Stats: Stats{
			Enabled:       true,
			FlushInterval: Duration{60 * time.Second},
		},
	}
}

// Load reads a config file from disk and parses it. If path is empty,
// it searches for fpsd.yml or fpsd.yaml in the working directory.
// Returns the parsed config and the path that was loaded (empty if none found).
func Load(path string) (Config, string, error) {
	cfg := Default()

	if path == "" {
		path = discover()
		if path == "" {
			return cfg, "", nil
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, path, fmt.Errorf("read config %s: %w", path, err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, path, fmt.Errorf("parse config %s: %w", path, err)
	}

	return cfg, path, nil
}

// discover searches for a config file in the working directory.
func discover() string {
	for _, name := range []string{"fpsd.yml", "fpsd.yaml"} {
		if _, err := os.Stat(name); err == nil {
			return name
		}
	}
	return ""
}

// CLIOverrides holds values from CLI flags that should override config file values.
// A nil/zero value means the flag was not explicitly set.
type CLIOverrides struct {
	Addr          *string
	LogDir        *string
	Verbose       *bool
	DataDir       *string
	BlocklistURLs []string
}

// Merge applies CLI flag overrides to a loaded config. Only explicitly-set
// flags override config file values.
func (c *Config) Merge(o CLIOverrides) {
	if o.Addr != nil {
		c.Listen = *o.Addr
	}
	if o.LogDir != nil {
		c.LogDir = *o.LogDir
	}
	if o.Verbose != nil {
		c.Verbose = *o.Verbose
	}
	if o.DataDir != nil {
		c.DataDir = *o.DataDir
	}
	if len(o.BlocklistURLs) > 0 {
		c.BlocklistURLs = o.BlocklistURLs
	}
}

// Validate checks the config for invalid values and returns an error
// describing all problems found.
func (c *Config) Validate() error {
	var errs []string

	// Listen address.
	if _, err := net.ResolveTCPAddr("tcp", c.Listen); err != nil {
		errs = append(errs, fmt.Sprintf("listen: invalid address %q: %v", c.Listen, err))
	}

	errs = append(errs, validateBlocklistURLs(c.BlocklistURLs)...)
	errs = append(errs, validateBlocklist(c.Blocklist)...)
	errs = append(errs, validateAllowlist(c.Allowlist)...)

	// Durations must be positive.
	if c.Timeouts.Shutdown.Duration <= 0 {
		errs = append(errs, fmt.Sprintf("timeouts.shutdown: must be positive, got %s", c.Timeouts.Shutdown))
	}
	if c.Timeouts.Connect.Duration <= 0 {
		errs = append(errs, fmt.Sprintf("timeouts.connect: must be positive, got %s", c.Timeouts.Connect))
	}
	if c.Timeouts.ReadHeader.Duration <= 0 {
		errs = append(errs, fmt.Sprintf("timeouts.read_header: must be positive, got %s", c.Timeouts.ReadHeader))
	}

	// Stats flush interval must be positive when enabled.
	if c.Stats.Enabled && c.Stats.FlushInterval.Duration <= 0 {
		errs = append(errs, fmt.Sprintf("stats.flush_interval: must be positive, got %s", c.Stats.FlushInterval))
	}

	// Management path prefix.
	if !strings.HasPrefix(c.Management.PathPrefix, "/") {
		errs = append(errs, fmt.Sprintf("management.path_prefix: must start with /, got %q", c.Management.PathPrefix))
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed:\n  %s", strings.Join(errs, "\n  "))
	}

	return nil
}

// validateBlocklistURLs checks that all blocklist URLs are valid HTTP(S) URLs.
func validateBlocklistURLs(urls []string) []string {
	var errs []string
	for i, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil {
			errs = append(errs, fmt.Sprintf("blocklist_urls[%d]: invalid URL %q: %v", i, raw, err))
			continue
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			errs = append(errs, fmt.Sprintf("blocklist_urls[%d]: scheme must be http or https, got %q", i, u.Scheme))
		}
	}
	return errs
}

// validateBlocklist checks that inline blocklist entries are valid domain names.
func validateBlocklist(domains []string) []string {
	var errs []string
	for i, d := range domains {
		if d == "" || strings.Contains(d, "*") || strings.Contains(d, "/") || strings.Contains(d, " ") {
			errs = append(errs, fmt.Sprintf("blocklist[%d]: invalid domain %q", i, d))
		}
	}
	return errs
}

// validateAllowlist checks that allowlist entries are valid exact domains or
// *.domain suffix patterns.
func validateAllowlist(entries []string) []string {
	var errs []string
	for i, entry := range entries {
		switch {
		case entry == "" || strings.Contains(entry, "/") || strings.Contains(entry, " "):
			errs = append(errs, fmt.Sprintf("allowlist[%d]: invalid entry %q", i, entry))
		case strings.HasPrefix(entry, "*."):
			domain := entry[2:]
			if domain == "" || strings.Contains(domain, "*") {
				errs = append(errs, fmt.Sprintf("allowlist[%d]: invalid suffix pattern %q", i, entry))
			}
		case strings.Contains(entry, "*"):
			errs = append(errs, fmt.Sprintf("allowlist[%d]: wildcard must be prefix *.domain, got %q", i, entry))
		}
	}
	return errs
}

// Dump serializes the config to YAML.
func (c *Config) Dump() ([]byte, error) {
	return yaml.Marshal(c)
}
