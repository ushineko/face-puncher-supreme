# face-puncher-supreme

Content-aware ad-blocking proxy. Targets apps where ads are served from the same domain as content, making DNS-based blocking ineffective.

## Table of Contents

- [Build](#build)
- [Run](#run)
- [CLI Flags](#cli-flags)
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

## Run

```bash
# Default: listen on :8080, logs to ./logs/
./fpsd

# Custom address with verbose logging
./fpsd --addr 0.0.0.0:9090 --verbose
```

Configure your browser or system to use `http://<host>:<port>` as an HTTP/HTTPS proxy. For Chromium:

```bash
chromium --proxy-server="http://127.0.0.1:8080"
```

## CLI Flags

| Flag | Short | Default | Description |
| ---- | ----- | ------- | ----------- |
| `--addr` | `-a` | `:8080` | Listen address (host:port) |
| `--log-dir` | | `logs` | Directory for log files (empty to disable) |
| `--verbose` | `-v` | `false` | Enable DEBUG-level logging with full request/response detail |

Subcommands:

- `fpsd version` — Print version string and exit

## Probe Endpoint

Verify the proxy is running:

```bash
curl -s http://localhost:8080/fps/probe | python3 -m json.tool
```

Returns JSON with status, version, mode, uptime, and connection counters.

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
internal/proxy/     Proxy server (HTTP forward, HTTPS CONNECT tunnel)
internal/probe/     Liveness/probe endpoint
internal/logging/   Structured logging with file rotation
internal/version/   Build-time version info
specs/              Project specifications
agents/             Cross-system testing guides
```

## Changelog

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
