# Spec 003: YAML Configuration File

**Status**: COMPLETE
**Created**: 2026-02-16
**Depends on**: Spec 001 (proxy foundation), Spec 002 (domain blocklist)

## Problem Statement

All proxy settings are currently passed as CLI flags. This works for simple cases but becomes unwieldy as configuration grows — a typical invocation with 5 blocklist URLs already spans multiple lines. A config file provides a single source of truth, is easy to version-control, and eliminates repetitive flag typing.

## Approach

Add support for a YAML configuration file that consolidates all proxy settings. CLI flags continue to work and override config file values where both are specified.

### Config File Location

The proxy searches for configuration in this order:

1. Path specified by `--config` / `-c` flag (explicit)
2. `./fpsd.yml` in the current working directory
3. `./fpsd.yaml` (alternate extension)

If no config file is found and no `--config` flag was given, the proxy starts with defaults (current behavior — all settings via flags or built-in defaults).

### File Format

```yaml
# fpsd.yml — Face Puncher Supreme configuration

# Network
listen: "0.0.0.0:8080"

# Logging
log_dir: "logs"
verbose: false

# Blocklist
data_dir: "/var/lib/fpsd"
blocklist_urls:
  - https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts
  - https://raw.githubusercontent.com/StevenBlack/hosts/master/alternates/fakenews-gambling-social/hosts
  - https://cdn.jsdelivr.net/gh/hagezi/dns-blocklists@latest/adblock/pro.txt
  - https://urlhaus.abuse.ch/downloads/hostfile/
  - https://big.oisd.nl/

# Timeouts
timeouts:
  shutdown: "5s"        # graceful shutdown deadline
  connect: "10s"        # upstream TCP dial timeout
  read_header: "10s"    # client request header read timeout

# Probe/management
management:
  path_prefix: "/fps"   # management endpoint prefix (read-only, informational)
```

### Field Definitions

| Field | Type | Default | Description |
| ----- | ---- | ------- | ----------- |
| `listen` | string | `":8080"` | Listen address in host:port format |
| `log_dir` | string | `"logs"` | Directory for rotated log files (empty string disables file logging) |
| `verbose` | bool | `false` | Enable DEBUG-level logging |
| `data_dir` | string | `"."` | Directory for persistent data (blocklist.db, stats.db) |
| `blocklist_urls` | []string | `[]` | List of blocklist URLs (Pi-hole compatible) |
| `timeouts.shutdown` | duration | `"5s"` | Graceful shutdown deadline |
| `timeouts.connect` | duration | `"10s"` | Upstream TCP connection timeout |
| `timeouts.read_header` | duration | `"10s"` | Client request header read timeout |
| `management.path_prefix` | string | `"/fps"` | URL prefix for management endpoints (informational only — changing this is cosmetic, not a security boundary) |

Duration strings use Go `time.ParseDuration` format: `"5s"`, `"30s"`, `"1m"`, `"2m30s"`.

### Precedence Rules

CLI flags override config file values. Unset fields fall back to defaults.

Resolution order (highest priority first):
1. CLI flag (explicitly passed)
2. Config file value
3. Built-in default

Example: `fpsd --addr :9090 -c fpsd.yml` uses port 9090 even if the config file says `listen: ":8080"`.

### Flag Changes

| Current Flag | Config Key | Notes |
| ------------ | ---------- | ----- |
| `--addr` / `-a` | `listen` | Name change: "listen" is clearer in config context |
| `--log-dir` | `log_dir` | Direct mapping |
| `--verbose` / `-v` | `verbose` | Direct mapping |
| `--data-dir` | `data_dir` | Direct mapping |
| `--blocklist-url` | `blocklist_urls` | Repeatable flag maps to YAML list |
| (new) `--config` / `-c` | N/A | Specifies config file path |

All existing flags continue to work unchanged. `--config` is the only new flag.

### Implementation

#### Config Struct

```go
// internal/config/config.go
type Config struct {
    Listen       string        `yaml:"listen"`
    LogDir       string        `yaml:"log_dir"`
    Verbose      bool          `yaml:"verbose"`
    DataDir      string        `yaml:"data_dir"`
    BlocklistURLs []string     `yaml:"blocklist_urls"`
    Timeouts     Timeouts      `yaml:"timeouts"`
    Management   Management    `yaml:"management"`
}

type Timeouts struct {
    Shutdown    Duration `yaml:"shutdown"`
    Connect     Duration `yaml:"connect"`
    ReadHeader  Duration `yaml:"read_header"`
}

type Management struct {
    PathPrefix string `yaml:"path_prefix"`
}
```

A custom `Duration` type wraps `time.Duration` with YAML marshal/unmarshal that accepts human-readable strings (`"5s"`, `"1m"`).

#### Loading

1. Parse CLI flags (Cobra, as today)
2. If `--config` specified or default config file found, read and parse YAML
3. Merge: CLI flags override config file values, unset fields get defaults
4. Validate: listen address parseable, durations positive, URLs well-formed
5. Pass the merged `Config` into the proxy server, blocklist, and logging setup

The `gopkg.in/yaml.v3` package is already an indirect dependency (via cobra/testify). This spec promotes it to a direct dependency.

#### Validation

The config loader validates at startup and fails fast with clear error messages:

- `listen`: must be parseable by `net.ResolveTCPAddr`
- `blocklist_urls`: each entry must be a valid URL with http or https scheme
- Duration fields: must be positive (> 0)
- `data_dir` / `log_dir`: parent directory must exist (created if needed, as today)
- `management.path_prefix`: must start with `/`

#### Subcommand Support

The `update-blocklist` subcommand also reads the config file, so blocklist URLs can come from config rather than requiring `--blocklist-url` flags on every invocation.

New subcommand:

```bash
fpsd config dump           # Print the resolved config (merged flags + file + defaults) as YAML
fpsd config validate       # Validate a config file and exit
```

These aid debugging and setup.

## File Changes

| File | Change |
| ---- | ------ |
| `internal/config/config.go` | New — config struct, YAML loading, validation, merge logic |
| `internal/config/config_test.go` | New — unit tests for loading, merging, validation |
| `internal/config/duration.go` | New — custom Duration YAML type |
| `cmd/fpsd/main.go` | Refactor to load config first, then derive all settings from it |
| `internal/proxy/proxy.go` | Accept timeouts from config (connect, read_header) |
| `go.mod` | Promote `gopkg.in/yaml.v3` to direct dependency |

## Acceptance Criteria

- [x] `fpsd.yml` / `fpsd.yaml` auto-discovered in working directory
- [x] `--config` / `-c` flag specifies explicit config file path
- [x] All current CLI flags continue to work unchanged
- [x] CLI flags override config file values
- [x] Unset fields fall back to documented defaults
- [x] YAML parsing errors produce clear error messages with line/column info
- [x] Validation catches: invalid listen address, bad URLs, negative durations, missing `/` prefix
- [x] `blocklist_urls` in config used by both `fpsd` and `fpsd update-blocklist`
- [x] `fpsd config dump` prints resolved configuration as YAML
- [x] `fpsd config validate` validates config and exits with 0 (ok) or 1 (error)
- [x] Timeout values from config applied to proxy server (connect, read_header, shutdown)
- [x] Duration fields accept Go duration strings (`"5s"`, `"1m"`, `"2m30s"`)
- [x] Proxy starts with sensible defaults when no config file exists and no flags given
- [x] Reference `fpsd.yml` checked into repo root with documented defaults and all 5 blocklist URLs
- [x] All existing tests pass (no regression)
- [x] New unit tests for config loading, merging, validation, and Duration type

## Out of Scope

- Environment variable configuration (may add later via a `FPSD_` prefix convention)
- Config hot-reloading (restart required for changes)
- Config file generation wizard
- Encryption of config values
- Multiple config file includes/imports
- TOML or JSON config format alternatives
