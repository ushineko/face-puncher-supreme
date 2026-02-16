# Spec 004: Database-Backed Statistics

**Status**: DRAFT
**Created**: 2026-02-16
**Depends on**: Spec 001 (proxy foundation), Spec 002 (domain blocklist)

## Problem Statement

Traffic and blocking statistics are currently held in memory (atomics and `sync.Map`). They reset on every restart and cannot be queried historically. A database-backed stats system would persist data across restarts, enable trend analysis, and support richer queries via the management endpoints.

The current `/fps/probe` endpoint also serves two distinct purposes: lightweight health checking (is the proxy alive?) and detailed statistics reporting. These should be split so that monitoring systems can poll a fast heartbeat without the overhead of computing top-N rankings on every request.

## Approach

### Stats Database

A separate SQLite database (`stats.db` in `data_dir`, alongside `blocklist.db`) records traffic events. In-memory counters remain the fast path for runtime decisions; the database receives periodic flushes for persistence and queryability.

#### Schema

```sql
-- Hourly traffic rollups (one row per hour per client IP).
CREATE TABLE IF NOT EXISTS traffic_hourly (
    hour      TEXT NOT NULL,   -- ISO 8601 hour truncated: "2026-02-16T14"
    client_ip TEXT NOT NULL,
    requests  INTEGER NOT NULL DEFAULT 0,
    blocked   INTEGER NOT NULL DEFAULT 0,
    bytes_in  INTEGER NOT NULL DEFAULT 0,
    bytes_out INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (hour, client_ip)
) WITHOUT ROWID;

-- Per-domain block counts (cumulative, all time).
CREATE TABLE IF NOT EXISTS blocked_domains (
    domain TEXT NOT NULL PRIMARY KEY,
    count  INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;

-- Per-domain request counts (cumulative, all time — all requests, not just blocked).
CREATE TABLE IF NOT EXISTS domain_requests (
    domain TEXT NOT NULL PRIMARY KEY,
    count  INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;

CREATE INDEX IF NOT EXISTS idx_traffic_hourly_hour ON traffic_hourly(hour);
CREATE INDEX IF NOT EXISTS idx_traffic_hourly_client ON traffic_hourly(client_ip);
```

#### Flush Strategy

In-memory counters accumulate during normal operation. A background goroutine flushes to SQLite on a configurable interval (default: 60 seconds). Flush uses `INSERT ... ON CONFLICT ... DO UPDATE` (upsert) to merge increments into existing rows.

On graceful shutdown, a final flush ensures no data is lost.

The flush interval is tunable in the config file (spec 003):

```yaml
stats:
  enabled: true
  flush_interval: "60s"
```

If `stats.enabled` is `false`, no stats database is created and the stats endpoint returns `501 Not Implemented`. The heartbeat endpoint always works regardless.

### In-Memory Counters

The existing `atomic.Int64` and `sync.Map` counters in `blocklist.DB` and `proxy.Server` remain the runtime fast path. The stats package reads from these during flush, computes deltas since the last flush, and writes the deltas to SQLite.

New per-request tracking (added to `proxy.Server`):

| Counter | Type | Description |
| ------- | ---- | ----------- |
| Client request counts | `sync.Map[string]*clientStats` | Per-client-IP: total requests, blocked requests, bytes in/out |
| Domain request counts | `sync.Map[string]*atomic.Int64` | Per-domain: total requests (all traffic, not just blocked) |

`clientStats` is a small struct with atomic fields for lock-free increment.

### Management Endpoint Split

The current `/fps/probe` is replaced by two endpoints:

#### `/fps/heartbeat` — Lightweight Health Check

Fast, minimal computation. Suitable for monitoring systems polling every few seconds.

**Response** (200 OK, `application/json`):

```json
{
  "status": "ok",
  "service": "face-puncher-supreme",
  "version": "0.3.0",
  "mode": "blocking",
  "uptime_seconds": 86400,
  "os": "linux",
  "arch": "amd64",
  "go_version": "go1.25.6",
  "started_at": "2026-02-15T10:00:00Z"
}
```

Fields:

| Field | Description |
| ----- | ----------- |
| `status` | Always `"ok"` if the proxy is responding |
| `service` | Service name (`"face-puncher-supreme"`) |
| `version` | Build version |
| `mode` | `"passthrough"` or `"blocking"` |
| `uptime_seconds` | Seconds since startup |
| `os` | Runtime OS (`runtime.GOOS`) |
| `arch` | Runtime architecture (`runtime.GOARCH`) |
| `go_version` | Go version (`runtime.Version()`) |
| `started_at` | ISO 8601 startup timestamp |

No database queries. No sorting. No aggregation. Just read a few atomics and static values.

#### `/fps/stats` — Full Statistics

The detailed endpoint for dashboards, debugging, and analysis.

**Response** (200 OK, `application/json`):

```json
{
  "connections": {
    "total": 15234,
    "active": 7
  },
  "blocking": {
    "blocks_total": 3421,
    "blocklist_size": 376000,
    "blocklist_sources": 5,
    "top_blocked": [
      {"domain": "googleads.g.doubleclick.net", "count": 870},
      {"domain": "pagead2.googlesyndication.com", "count": 540},
      {"domain": "securepubads.g.doubleclick.net", "count": 310}
    ]
  },
  "domains": {
    "top_requested": [
      {"domain": "www.reddit.com", "count": 1200},
      {"domain": "i.redd.it", "count": 980},
      {"domain": "www.google.com", "count": 750}
    ]
  },
  "clients": {
    "top_by_requests": [
      {"client_ip": "192.168.1.42", "requests": 8700, "blocked": 1200, "bytes_in": 524288, "bytes_out": 10485760},
      {"client_ip": "192.168.1.15", "requests": 4300, "blocked": 890, "bytes_in": 262144, "bytes_out": 5242880}
    ]
  },
  "traffic": {
    "total_requests": 15234,
    "total_blocked": 3421,
    "total_bytes_in": 1048576,
    "total_bytes_out": 20971520
  }
}
```

Sections:

| Section | Description |
| ------- | ----------- |
| `connections` | Real-time connection counters (from atomics) |
| `blocking` | Block stats and top-N blocked domains |
| `blocking.top_blocked` | Top 10 blocked domains by hit count |
| `domains.top_requested` | Top 10 most-requested domains (all traffic) |
| `clients.top_by_requests` | Top 10 clients by total request count, with per-client breakdown |
| `traffic` | Aggregate traffic totals |

Query parameters:

| Param | Default | Description |
| ----- | ------- | ----------- |
| `n` | `10` | Number of entries in top-N lists (applies to all: blocked, domains, clients) |
| `period` | (all time) | Time window for top-N: `1h`, `24h`, `7d`, or omit for cumulative |

Examples:
- `/fps/stats` — all stats, top 10, all time
- `/fps/stats?n=25` — top 25 in all lists
- `/fps/stats?n=5&period=24h` — top 5 over the last 24 hours

When `period` is specified, the stats endpoint queries `traffic_hourly` for time-bounded results. When omitted, it reads from the cumulative in-memory counters (current session) merged with cumulative DB totals.

#### `/fps/probe` — Removed

The `/fps/probe` endpoint is removed. `/fps/heartbeat` replaces it. The macOS agent guide and any other references to `/fps/probe` will be updated as part of this spec.

### Byte Tracking

To populate `bytes_in` / `bytes_out` per client, the proxy needs to track transfer sizes:

- **HTTP requests**: `bytes_in` = request body size, `bytes_out` = response body size (already available from `io.Copy` return value)
- **CONNECT tunnels**: `bytes_in` = upload bytes, `bytes_out` = download bytes (already tracked in verbose mode via `atomic.Int64` in `handleConnect`)

The existing byte tracking in `handleConnect` needs to be promoted from verbose-only to always-on, and aggregated into per-client stats.

### Config Extension (Spec 003)

This spec adds a `stats` section to the config file:

```yaml
stats:
  enabled: true
  flush_interval: "60s"
```

| Field | Type | Default | Description |
| ----- | ---- | ------- | ----------- |
| `stats.enabled` | bool | `true` | Enable stats collection and persistence |
| `stats.flush_interval` | duration | `"60s"` | How often in-memory counters are flushed to SQLite |

## File Changes

| File | Change |
| ---- | ------ |
| `internal/stats/stats.go` | New — stats database, flush loop, query methods |
| `internal/stats/stats_test.go` | New — unit tests for flush, queries, top-N |
| `internal/stats/collector.go` | New — in-memory counters for per-client and per-domain tracking |
| `internal/probe/probe.go` | Replace probe with heartbeat response; add stats handler |
| `internal/probe/probe_test.go` | Replace probe tests with heartbeat + stats tests |
| `internal/proxy/management.go` | Remove `/fps/probe`, add `/fps/heartbeat` and `/fps/stats` routes |
| `agents/macos-agent-guide.md` | Update `/fps/probe` references to `/fps/heartbeat` |
| `internal/proxy/proxy.go` | Wire stats collector into request handlers; promote byte tracking |
| `cmd/fpsd/main.go` | Initialize stats DB, pass to proxy, start flush loop, flush on shutdown |

## Acceptance Criteria

- [ ] `stats.db` created in `data_dir` on startup (when `stats.enabled` is true)
- [ ] In-memory counters flush to SQLite on configured interval (default 60s)
- [ ] Final flush on graceful shutdown (no data loss)
- [ ] `/fps/heartbeat` returns lightweight health check with OS info, uptime, version, started_at
- [ ] `/fps/heartbeat` involves no database queries or sorting
- [ ] `/fps/stats` returns full statistics: connections, blocking, domains, clients, traffic
- [ ] `/fps/stats` `top_blocked` shows top N blocked domains by count
- [ ] `/fps/stats` `top_requested` shows top N requested domains (all traffic)
- [ ] `/fps/stats` `top_by_requests` shows top N clients with request/blocked/bytes breakdown
- [ ] `/fps/stats?n=N` controls the size of all top-N lists
- [ ] `/fps/stats?period=24h` returns time-bounded results from hourly rollups
- [ ] `/fps/probe` removed; references in codebase and macOS agent guide updated
- [ ] Stats survive proxy restarts (read from DB on startup, merge with current session)
- [ ] Per-client byte tracking works for both HTTP and CONNECT requests
- [ ] `stats.enabled: false` disables stats collection; `/fps/stats` returns 501
- [ ] `traffic_hourly` table enables historical trend queries
- [ ] All existing tests pass (no regression)
- [ ] New unit tests for: stats DB operations, flush logic, top-N queries, period filtering, heartbeat response, stats response

## Out of Scope

- Graphing / visualization (stats are JSON — dashboarding is a client concern)
- Stats API authentication (management endpoints are for local/LAN use)
- Retention policy / automatic pruning of old hourly data (add later if DB grows)
- Per-URL path tracking (domain-level granularity only)
- Real-time WebSocket stats streaming
- Export to Prometheus / StatsD / other telemetry systems
