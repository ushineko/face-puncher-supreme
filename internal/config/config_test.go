package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestDefault(t *testing.T) {
	cfg := Default()

	assert.Equal(t, ":18737", cfg.Listen)
	assert.Equal(t, "logs", cfg.LogDir)
	assert.False(t, cfg.Verbose)
	assert.Equal(t, ".", cfg.DataDir)
	assert.Empty(t, cfg.BlocklistURLs)
	assert.Empty(t, cfg.Blocklist)
	assert.Empty(t, cfg.Allowlist)
	assert.Equal(t, 5*time.Second, cfg.Timeouts.Shutdown.Duration)
	assert.Equal(t, 10*time.Second, cfg.Timeouts.Connect.Duration)
	assert.Equal(t, 10*time.Second, cfg.Timeouts.ReadHeader.Duration)
	assert.Equal(t, "/fps", cfg.Management.PathPrefix)
}

func TestDuration_UnmarshalYAML(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr bool
	}{
		{name: "seconds", input: `"5s"`, want: 5 * time.Second},
		{name: "minutes", input: `"1m"`, want: time.Minute},
		{name: "compound", input: `"2m30s"`, want: 2*time.Minute + 30*time.Second},
		{name: "milliseconds", input: `"500ms"`, want: 500 * time.Millisecond},
		{name: "invalid", input: `"bogus"`, wantErr: true},
		{name: "number", input: `42`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d Duration
			err := yaml.Unmarshal([]byte(tt.input), &d)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, d.Duration)
		})
	}
}

func TestDuration_MarshalYAML(t *testing.T) {
	d := Duration{5 * time.Second}
	out, err := yaml.Marshal(d)
	require.NoError(t, err)
	assert.Equal(t, "5s\n", string(out))
}

func TestLoad_ExplicitPath(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "test.yml")
	content := `
listen: ":9090"
verbose: true
data_dir: "/tmp/data"
blocklist_urls:
  - https://example.com/hosts
timeouts:
  shutdown: "10s"
  connect: "30s"
  read_header: "5s"
management:
  path_prefix: "/mgmt"
`
	require.NoError(t, os.WriteFile(cfgPath, []byte(content), 0o600))

	cfg, loaded, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, cfgPath, loaded)

	assert.Equal(t, ":9090", cfg.Listen)
	assert.True(t, cfg.Verbose)
	assert.Equal(t, "/tmp/data", cfg.DataDir)
	assert.Equal(t, []string{"https://example.com/hosts"}, cfg.BlocklistURLs)
	assert.Equal(t, 10*time.Second, cfg.Timeouts.Shutdown.Duration)
	assert.Equal(t, 30*time.Second, cfg.Timeouts.Connect.Duration)
	assert.Equal(t, 5*time.Second, cfg.Timeouts.ReadHeader.Duration)
	assert.Equal(t, "/mgmt", cfg.Management.PathPrefix)
}

func TestLoad_PartialOverride(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "partial.yml")
	content := `
listen: ":3000"
verbose: true
`
	require.NoError(t, os.WriteFile(cfgPath, []byte(content), 0o600))

	cfg, _, err := Load(cfgPath)
	require.NoError(t, err)

	// Overridden values.
	assert.Equal(t, ":3000", cfg.Listen)
	assert.True(t, cfg.Verbose)

	// Defaults preserved for unspecified fields.
	assert.Equal(t, "logs", cfg.LogDir)
	assert.Equal(t, ".", cfg.DataDir)
	assert.Equal(t, 5*time.Second, cfg.Timeouts.Shutdown.Duration)
	assert.Equal(t, "/fps", cfg.Management.PathPrefix)
}

func TestLoad_AutoDiscover(t *testing.T) {
	dir := t.TempDir()
	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	require.NoError(t, os.Chdir(dir))

	// Write fpsd.yml in the temp directory.
	content := `listen: ":4000"`
	require.NoError(t, os.WriteFile("fpsd.yml", []byte(content), 0o600))

	cfg, loaded, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, "fpsd.yml", loaded)
	assert.Equal(t, ":4000", cfg.Listen)
}

func TestLoad_AutoDiscoverYAMLExtension(t *testing.T) {
	dir := t.TempDir()
	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	require.NoError(t, os.Chdir(dir))

	content := `listen: ":5000"`
	require.NoError(t, os.WriteFile("fpsd.yaml", []byte(content), 0o600))

	cfg, loaded, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, "fpsd.yaml", loaded)
	assert.Equal(t, ":5000", cfg.Listen)
}

func TestLoad_NoConfigFile(t *testing.T) {
	dir := t.TempDir()
	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	require.NoError(t, os.Chdir(dir))

	cfg, loaded, err := Load("")
	require.NoError(t, err)
	assert.Empty(t, loaded)
	assert.Equal(t, Default(), cfg)
}

func TestLoad_MissingExplicitPath(t *testing.T) {
	_, _, err := Load("/nonexistent/fpsd.yml")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "read config")
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "bad.yml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("listen: [invalid"), 0o600))

	_, _, err := Load(cfgPath)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse config")
}

func TestMerge(t *testing.T) {
	cfg := Default()

	addr := ":9999"
	verbose := true

	cfg.Merge(CLIOverrides{
		Addr:          &addr,
		Verbose:       &verbose,
		BlocklistURLs: []string{"https://example.com/list"},
	})

	assert.Equal(t, ":9999", cfg.Listen)
	assert.True(t, cfg.Verbose)
	assert.Equal(t, []string{"https://example.com/list"}, cfg.BlocklistURLs)

	// Unset overrides should not change anything.
	assert.Equal(t, "logs", cfg.LogDir)
	assert.Equal(t, ".", cfg.DataDir)
}

func TestMerge_EmptyOverrides(t *testing.T) {
	cfg := Default()
	original := Default()
	cfg.Merge(CLIOverrides{})
	assert.Equal(t, original, cfg)
}

func TestValidate_Valid(t *testing.T) {
	cfg := Default()
	assert.NoError(t, cfg.Validate())
}

func TestValidate_ValidWithURLs(t *testing.T) {
	cfg := Default()
	cfg.BlocklistURLs = []string{
		"https://example.com/hosts",
		"http://example.com/list.txt",
	}
	assert.NoError(t, cfg.Validate())
}

func TestValidate_InvalidListen(t *testing.T) {
	cfg := Default()
	cfg.Listen = "not-a-valid-address"
	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "listen:")
}

func TestValidate_InvalidURL(t *testing.T) {
	cfg := Default()
	cfg.BlocklistURLs = []string{"ftp://nope.com/list"}
	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "scheme must be http or https")
}

func TestValidate_NegativeDuration(t *testing.T) {
	cfg := Default()
	cfg.Timeouts.Shutdown = Duration{-1 * time.Second}
	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timeouts.shutdown:")
}

func TestValidate_ZeroDuration(t *testing.T) {
	cfg := Default()
	cfg.Timeouts.Connect = Duration{0}
	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timeouts.connect:")
}

func TestValidate_BadPathPrefix(t *testing.T) {
	cfg := Default()
	cfg.Management.PathPrefix = "no-slash"
	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "path_prefix:")
}

func TestValidate_MultipleErrors(t *testing.T) {
	cfg := Default()
	cfg.Listen = "bad"
	cfg.Timeouts.Shutdown = Duration{-1 * time.Second}
	cfg.Management.PathPrefix = "bad"
	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "listen:")
	assert.Contains(t, err.Error(), "timeouts.shutdown:")
	assert.Contains(t, err.Error(), "path_prefix:")
}

func TestLoad_BlocklistAndAllowlist(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "test.yml")
	content := `
blocklist:
  - news.iadsdk.apple.com
  - news-events.apple.com
allowlist:
  - registry.api.cnn.io
  - "*.optimizely.com"
`
	require.NoError(t, os.WriteFile(cfgPath, []byte(content), 0o600))

	cfg, _, err := Load(cfgPath)
	require.NoError(t, err)

	assert.Equal(t, []string{"news.iadsdk.apple.com", "news-events.apple.com"}, cfg.Blocklist)
	assert.Equal(t, []string{"registry.api.cnn.io", "*.optimizely.com"}, cfg.Allowlist)
}

func TestValidate_ValidBlocklistAndAllowlist(t *testing.T) {
	cfg := Default()
	cfg.Blocklist = []string{"ad.example.com", "tracker.example.org"}
	cfg.Allowlist = []string{"safe.example.com", "*.cnn.io"}
	assert.NoError(t, cfg.Validate())
}

func TestValidate_InvalidBlocklistEntry(t *testing.T) {
	cfg := Default()
	cfg.Blocklist = []string{"*.wildcard.com"}
	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "blocklist[0]")
}

func TestValidate_InvalidBlocklistEmpty(t *testing.T) {
	cfg := Default()
	cfg.Blocklist = []string{""}
	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "blocklist[0]")
}

func TestValidate_InvalidAllowlistSuffix(t *testing.T) {
	cfg := Default()
	cfg.Allowlist = []string{"*."}
	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "allowlist[0]")
}

func TestValidate_InvalidAllowlistMidWildcard(t *testing.T) {
	cfg := Default()
	cfg.Allowlist = []string{"foo.*.com"}
	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "wildcard must be prefix")
}

func TestDump(t *testing.T) {
	cfg := Default()
	cfg.BlocklistURLs = []string{"https://example.com/hosts"}

	out, err := cfg.Dump()
	require.NoError(t, err)

	// Round-trip: the dumped YAML should parse back to the same config.
	var parsed Config
	require.NoError(t, yaml.Unmarshal(out, &parsed))
	assert.Equal(t, cfg.Listen, parsed.Listen)
	assert.Equal(t, cfg.BlocklistURLs, parsed.BlocklistURLs)
	assert.Equal(t, cfg.Timeouts.Shutdown.Duration, parsed.Timeouts.Shutdown.Duration)
}
