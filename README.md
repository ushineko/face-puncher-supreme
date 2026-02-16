# face-puncher-supreme

Content-aware ad-blocking proxy. Targets apps where ads are served from the same domain as content, making DNS-based blocking ineffective.

## Table of Contents

- [Build](#build)
- [Configuration](#configuration)
- [Run](#run)
- [CLI Flags](#cli-flags)
- [Domain Blocking](#domain-blocking)
- [Allowlist and Inline Blocklist](#allowlist-and-inline-blocklist)
- [MITM TLS Interception](#mitm-tls-interception)
- [Content Filter Plugins](#content-filter-plugins)
- [Management Endpoints](#management-endpoints)
- [Logging](#logging)
- [Test](#test)
- [Lint](#lint)
- [Project Structure](#project-structure)
- [Changelog](#changelog)

## Build

```bash
make build
```

Produces the `fpsd` binary in the project root with version info baked in via ldflags.

## Configuration

The proxy reads configuration from a YAML file. It searches for `fpsd.yml` (or `fpsd.yaml`) in the working directory, or you can specify a path with `--config`/`-c`.

A reference config file (`fpsd.yml`) is included in the repo with all defaults and 5 Pi-hole compatible blocklist URLs.

```bash
# Use the included config (auto-discovered in working directory)
./fpsd

# Use a specific config file
./fpsd -c /etc/fpsd/fpsd.yml

# Inspect the resolved configuration
fpsd config dump

# Validate a config file
fpsd config validate -c /path/to/fpsd.yml
```

CLI flags override config file values. If no config file exists, the proxy starts with built-in defaults (same as before).

## Run

```bash
# With config file (auto-discovered fpsd.yml has blocklist URLs pre-configured)
./fpsd

# Override listen address via CLI flag
./fpsd --addr 0.0.0.0:9090

# Without config file, pass blocklist URLs as flags
./fpsd \
  --blocklist-url https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts \
  --blocklist-url https://cdn.jsdelivr.net/gh/hagezi/dns-blocklists@latest/adblock/pro.txt
```

Configure your browser or system to use `http://<host>:<port>` as an HTTP/HTTPS proxy. For Chromium:

```bash
chromium --proxy-server="http://127.0.0.1:18737"
```

## CLI Flags

| Flag | Short | Default | Description |
| ---- | ----- | ------- | ----------- |
| `--config` | `-c` | (auto-discover) | Config file path (`fpsd.yml` / `fpsd.yaml` in CWD) |
| `--addr` | `-a` | `:18737` | Listen address (host:port) |
| `--log-dir` | | `logs` | Directory for log files (empty to disable) |
| `--verbose` | `-v` | `false` | Enable DEBUG-level logging with full request/response detail |
| `--blocklist-url` | | | Blocklist URL (repeatable, same format as Pi-hole adlists) |
| `--data-dir` | | `.` | Directory for `blocklist.db` |

CLI flags override config file values when both are specified.

Subcommands:

- `fpsd version` — Print version string and exit
- `fpsd update-blocklist` — Re-download all blocklist URLs, rebuild the database, and exit
- `fpsd generate-ca` — Generate CA certificate and private key for MITM (`--force` to overwrite)
- `fpsd config dump` — Print the resolved configuration as YAML
- `fpsd config validate` — Validate configuration and exit with 0 (ok) or 1 (error)

## Domain Blocking

The proxy blocks requests to domains on known ad/tracking blocklists. This complements DNS-based blocking (Pi-hole) at the proxy layer.

```bash
# First run: downloads lists and builds blocklist.db
./fpsd --blocklist-url https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts

# Subsequent runs: loads from existing blocklist.db (no re-download)
./fpsd --blocklist-url https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts

# Update lists without starting the proxy
fpsd update-blocklist \
  --blocklist-url https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts
```

Supported list formats: hosts (`0.0.0.0 domain`), adblock (`||domain^`), and domain-only. Matching is exact and case-insensitive. Blocked requests receive `403 Forbidden`.

With no blocklist URLs (neither in config file nor via `--blocklist-url` flags), the proxy runs in passthrough mode (no blocking).

## Allowlist and Inline Blocklist

Beyond URL-sourced blocklists, the config file supports two additional mechanisms for tuning:

**Inline blocklist** — block individual domains without downloading a full list:

```yaml
blocklist:
  - news.iadsdk.apple.com
  - news-events.apple.com
  - news-app-events.apple.com
```

**Allowlist** — domains that are never blocked, even if they appear in blocklists. Supports exact match and suffix patterns (`*.example.com` matches the base domain and all subdomains):

```yaml
allowlist:
  - registry.api.cnn.io
  - cdn.optimizely.com
  - "*.cnn.io"
```

Allowlist takes priority over all block sources (URL-sourced and inline). Inline blocklist entries are merged into the in-memory cache at startup and are not stored in `blocklist.db` — they survive `fpsd update-blocklist` since they come from config.

## MITM TLS Interception

For sites that serve ads from the same domain as content (e.g., Reddit promoted posts from `www.reddit.com`), domain blocking is insufficient. MITM TLS interception lets the proxy inspect HTTP traffic for configured domains.

**Setup**:

```bash
# 1. Generate CA certificate and private key
fpsd generate-ca

# 2. Install CA in Chromium's trust store (Linux)
curl -o fps-ca.pem http://localhost:18737/fps/ca.pem
mkdir -p ~/.pki/nssdb
certutil -d sql:$HOME/.pki/nssdb -A -t "C,," -n "Face Puncher Supreme CA" -i fps-ca.pem

# 3. Configure domains in fpsd.yml
```

```yaml
mitm:
  domains:
    - www.reddit.com
    - old.reddit.com
```

Only explicitly listed domains are intercepted. All other HTTPS traffic remains in opaque tunnels. The blocklist check still happens first — blocked domains get 403 regardless of MITM config.

MITM is HTTP/1.1 only. The proxy generates short-lived leaf certificates (24h) per domain, signed by the CA, cached in memory.

**Subcommands**:

- `fpsd generate-ca` — Create CA cert and key (refuses to overwrite; use `--force` to regenerate)

## Content Filter Plugins

Plugins are site-specific content filters that inspect and modify MITM'd HTTP responses. Each plugin targets a set of domains and operates in one of two modes:

- **intercept** — captures request/response pairs to disk for offline analysis (development tool)
- **filter** — applies content filtering rules, replacing matched ad content with placeholder markers

```yaml
plugins:
  reddit-promotions:
    enabled: true
    mode: "filter"           # "intercept" or "filter"
    placeholder: "visible"   # "visible", "comment", or "none"
    domains:
      - www.reddit.com
    options:
      log_matches: true
```

Plugin domains must be a subset of `mitm.domains`. Placeholder markers indicate what was filtered: `visible` shows a styled HTML element, `comment` inserts an HTML comment, `none` removes content silently.

Plugins are compiled statically into the binary. Adding a new plugin means registering its constructor in `internal/plugin/registry.go` and rebuilding. Plugin stats appear in `/fps/stats` and `/fps/heartbeat`.

## Management Endpoints

### `/fps/heartbeat` — Health Check

Lightweight health check for monitoring. No database queries or sorting.

```bash
curl -s http://localhost:18737/fps/heartbeat | python3 -m json.tool
```

Returns JSON with status, version, mode, MITM status, uptime, OS info, and startup timestamp.

### `/fps/stats` — Full Statistics

Detailed traffic, blocking, domain, and client statistics.

```bash
# All stats, top 10, all time
curl -s http://localhost:18737/fps/stats | python3 -m json.tool

# Top 5 over the last 24 hours
curl -s 'http://localhost:18737/fps/stats?n=5&period=24h' | python3 -m json.tool
```

Returns connections, blocking stats (with top blocked and top allowed domains), MITM interception stats (total intercepts, top intercepted domains), top requested domains, top clients by request count, and aggregate traffic totals.

Query parameters: `n` (top-N size, default 10), `period` (`1h`, `24h`, `7d`, or omit for all time).

Stats are persisted to `stats.db` via periodic flush (default 60s) and survive restarts. Disable with `stats.enabled: false` in config (returns 501).

### `/fps/ca.pem` — CA Certificate Download

Download the MITM CA certificate for client installation. Returns 404 when MITM is not configured.

## Logging

Logs are written to both stderr (text format) and a rotated JSON log file:

- **File**: `<log-dir>/fpsd.log`
- **Rotation**: 10MB per file, 3 backups, 7-day retention, gzip compressed
- **Verbose mode** (`--verbose`): Logs full request/response headers, User-Agent, body sizes, and byte counts for CONNECT tunnels

## Test

```bash
# Unit tests only (fast)
make test

# Include integration tests against real sites
go test -race -v ./...
```

Integration tests are skipped with `-short` (which `make test` uses via `-race -v`). To run them, omit the `-short` flag or run directly with `go test -race -v ./internal/proxy/`.

## Lint

```bash
make lint
```

Uses golangci-lint v2 with a versioned binary (auto-installed on first run). Configuration is in `.golangci.yml`. Enabled linters: errcheck, gocognit, gocritic, gocyclo, govet, lll, unparam, unused, cyclop, gosec.

## Project Structure

```
cmd/fpsd/           Daemon entrypoint (Cobra CLI)
internal/config/    YAML config loading, validation, CLI merge
internal/proxy/     Proxy server (HTTP forward, HTTPS CONNECT tunnel, domain blocking)
internal/blocklist/ Domain blocklist (SQLite DB, parser, fetcher, in-memory cache)
internal/mitm/      Per-domain TLS interception (CA, leaf certs, HTTP proxy loop)
internal/plugin/    Content filter plugin architecture (registry, interception, markers)
internal/probe/     Management endpoints (heartbeat + stats)
internal/stats/     In-memory counters and SQLite stats persistence
internal/logging/   Structured logging with file rotation
internal/version/   Build-time version info
specs/              Project specifications
agents/             Cross-system testing guides
fpsd.yml            Reference configuration with defaults and blocklist URLs
```

## Changelog

### v0.9.0 — 2026-02-17

- Reddit promotions filter: strips promoted/sponsored content from Reddit's Shreddit UI (spec 008)
- Feed ads (`<shreddit-ad-post>`), comment-tree ads, comment-page ads, and right-rail promoted posts removed
- Byte-level HTML element removal using Reddit's unique custom element names (no full HTML parser needed)
- URL-scoped processing: only homepage, feed, comment, and right-rail paths are inspected
- Quick-skip optimization: responses without ad marker strings bypass element scanning
- Placeholder insertion per configured mode (`visible`, `comment`, `none`)
- FilterResult reporting: accurate match/modify/rule/count data for stats integration
- 6 HTML test fixtures extracted from real interception captures for regression testing
- 25 new tests for filter rules, URL scoping, quick-skip, element removal, and fixture integrity
- 167 total tests (all passing)

### v0.8.0 — 2026-02-16

- Content filter plugin architecture for site-specific MITM response modification (spec 007)
- `ContentFilter` interface: `Name()`, `Version()`, `Domains()`, `Init()`, `Filter()`
- Plugin registry with compile-time constructors; adding a plugin is one line of code
- Interception mode: captures MITM traffic to disk (`intercepts/<plugin>/`) for offline analysis
- Placeholder markers: `visible` (styled HTML), `comment` (HTML comment), `none` (clean removal)
- Shared `Marker()` helper generates format-appropriate placeholders for HTML and JSON
- Response buffering in MITM proxyLoop for text Content-Types (text/*, application/json, etc.)
- Binary responses (images, video, fonts) bypass buffering; 10MB body size cap
- Modified responses update `Content-Length` header automatically
- Plugin config section in `fpsd.yml`: `enabled`, `mode`, `placeholder`, `domains`, `options`
- Config validation: registry existence, mode/placeholder values, domain subset of `mitm.domains`
- Plugin stats in `/fps/stats`: per-plugin inspected/matched/modified counts, top rules
- Heartbeat shows `plugins_active` count and plugin `name@version` list
- Reddit promotions stub plugin registered (interception mode for traffic analysis)
- 26 new tests: marker generation, content-type filtering, registry dispatch, interception capture
- 142 total tests (all passing)

### v0.7.0 — 2026-02-16

- Per-domain MITM TLS interception for content-level ad blocking (spec 006)
- `fpsd generate-ca` subcommand creates CA certificate and private key (ECDSA P-256, 10-year validity)
- `mitm.domains` config: explicit list of domains to intercept (exact match, case-insensitive)
- Dynamic leaf certificate generation per domain (24h validity, in-memory cache)
- HTTP/1.1 proxy loop through intercepted TLS connections with hop-by-hop header stripping
- `ResponseModifier` hook for future content filtering (nil/passthrough by default)
- `/fps/ca.pem` endpoint serves CA certificate for client installation
- MITM stats in `/fps/stats`: `intercepts_total`, `domains_configured`, `top_intercepted`
- Heartbeat shows `mitm_enabled` and `mitm_domains` fields
- Startup warnings: domain in both MITM and blocklist, CA expiry within 30 days
- Config validation for MITM domain entries (no wildcards, paths, or spaces)
- 16 new tests: CA generation, leaf cert caching, end-to-end MITM proxy loop
- 116 total tests (all passing)

### v0.6.0 — 2026-02-16

- Allowlist: domains that are never blocked, with exact match and suffix pattern (`*.example.com`) support
- Inline blocklist: block individual domains via config without downloading full lists
- Allowlist takes priority over all block sources (URL-sourced and inline)
- Allow counters: `allows_total`, `allowlist_size`, `top_allowed` in `/fps/stats`
- Allow stats persisted to `stats.db` alongside block stats (delta-based flush)
- Config validation for `blocklist` (no wildcards) and `allowlist` (exact or `*.domain`) entries
- Startup log shows allowlist size and inline blocklist count
- 13 new tests for allowlist matching, inline blocklist, and allow counters
- 90 total tests (all passing)

### v0.5.0 — 2026-02-16

- Database-backed statistics with SQLite persistence (`stats.db`)
- `/fps/heartbeat` lightweight health check (replaces `/fps/probe`)
- `/fps/stats` full statistics endpoint with connections, blocking, domains, clients, and traffic data
- Top-N query support (`?n=25`) and time-bounded queries (`?period=24h`)
- In-memory counters with delta-based periodic flush (default 60s, configurable)
- Per-client byte tracking for HTTP and CONNECT requests
- Final flush on graceful shutdown (no data loss)
- Stats survive proxy restarts (merge DB + in-memory unflushed data)
- `stats.enabled: false` disables collection; `/fps/stats` returns 501
- Config: `stats.enabled` and `stats.flush_interval` settings
- 16 new unit tests for stats collector, DB operations, flush logic, and merged queries
- 77 total tests (all passing)

### v0.4.0 — 2026-02-16

- YAML configuration file (`fpsd.yml`) with auto-discovery and `--config`/`-c` flag
- Reference config checked into repo with all 5 Pi-hole blocklist URLs
- CLI flags override config file values; unset fields use built-in defaults
- Configurable timeouts: shutdown, upstream connect, request header read
- Configurable management endpoint path prefix
- `fpsd config dump` and `fpsd config validate` subcommands
- `update-blocklist` reads blocklist URLs from config file (no more repeating `--blocklist-url` flags)
- Custom `Duration` YAML type for human-readable timeout strings (`"5s"`, `"1m"`)
- 20 new unit tests for config loading, merging, validation, and Duration type
- 60 total tests (40 unit + 5 integration, all passing)

### v0.3.0 — 2026-02-16

- Domain-based ad blocking via `--blocklist-url` flags (Pi-hole compatible)
- SQLite-backed blocklist with in-memory cache for O(1) domain lookup
- Hosts format (`0.0.0.0 domain`), adblock format (`||domain^`), and domain-only parsing
- `fpsd update-blocklist` subcommand to re-download and rebuild the database
- Block statistics in `/fps/probe`: `blocks_total`, `blocklist_size`, `blocklist_sources`, `top_blocked`
- Probe mode switches between `"passthrough"` and `"blocking"` based on loaded domains
- 40 unit tests + 5 integration tests

### v0.2.0 — 2026-02-16

- golangci-lint v2 integration with versioned binary and auto-install via `make lint`
- Lint fixes: error handling, Slowloris protection (`ReadHeaderTimeout`), tighter directory permissions
- Spec 002 draft: domain-based ad blocking with SQLite blocklist
- Quality gates documented in project config

### v0.1.0 — 2026-02-16

- HTTP forward proxy with hop-by-hop header stripping
- HTTPS CONNECT tunnel with bidirectional streaming
- `/fps/probe` liveness endpoint with connection counters and uptime
- Structured logging: stderr (text) + rotated JSON file via lumberjack
- Verbose mode for debugging real browser traffic (headers, sizes, timing)
- Cobra CLI with `--addr`, `--log-dir`, `--verbose` flags and `version` subcommand
- Build-time version injection via ldflags
- 15 unit tests + 13 integration tests (real sites via httpbin, Wikipedia, BBC, CNN)
- Verified working with Chromium browser traffic in passthrough mode
