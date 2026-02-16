# face-puncher-supreme

Content-aware ad-blocking proxy. Targets apps where ads are served from the same domain as content, making DNS-based blocking ineffective.

## Table of Contents

- [Build](#build)
- [Configuration](#configuration)
- [Run](#run)
- [CLI Flags](#cli-flags)
- [Domain Blocking](#domain-blocking)
- [Probe Endpoint](#probe-endpoint)
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
chromium --proxy-server="http://127.0.0.1:8080"
```

## CLI Flags

| Flag | Short | Default | Description |
| ---- | ----- | ------- | ----------- |
| `--config` | `-c` | (auto-discover) | Config file path (`fpsd.yml` / `fpsd.yaml` in CWD) |
| `--addr` | `-a` | `:8080` | Listen address (host:port) |
| `--log-dir` | | `logs` | Directory for log files (empty to disable) |
| `--verbose` | `-v` | `false` | Enable DEBUG-level logging with full request/response detail |
| `--blocklist-url` | | | Blocklist URL (repeatable, same format as Pi-hole adlists) |
| `--data-dir` | | `.` | Directory for `blocklist.db` |

CLI flags override config file values when both are specified.

Subcommands:

- `fpsd version` — Print version string and exit
- `fpsd update-blocklist` — Re-download all blocklist URLs, rebuild the database, and exit
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

## Probe Endpoint

Verify the proxy is running:

```bash
curl -s http://localhost:8080/fps/probe | python3 -m json.tool
```

Returns JSON with status, version, mode, uptime, connection counters, and block statistics (`blocks_total`, `blocklist_size`, `blocklist_sources`, `top_blocked`).

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
internal/probe/     Liveness/probe endpoint with block statistics
internal/logging/   Structured logging with file rotation
internal/version/   Build-time version info
specs/              Project specifications
agents/             Cross-system testing guides
fpsd.yml            Reference configuration with defaults and blocklist URLs
```

## Changelog

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
