# Spec 002: Domain-Based Ad Blocking

**Status**: COMPLETE
**Created**: 2026-02-16
**Depends on**: Spec 001 (proxy foundation)

## Problem Statement

The proxy currently passes all traffic through without filtering. The first layer of ad blocking is domain-level blocking — refusing to connect to known ad/tracking domains. This is the same approach Pi-hole uses at the DNS level, but applied at the proxy layer where it can complement DNS blocking for devices that route through the proxy.

## Approach

Reuse the same blocklist URLs that Pi-hole subscribes to. The server configuration declares blocklist URLs (same format as Pi-hole's adlist). On first run (or on-demand rescan), the server downloads these lists and populates a local SQLite database. At runtime, domain lookups hit the SQLite DB for O(1) matching.

### SQLite Bindings

Use `zombiezen.com/go/sqlite` (pure Go, backed by `modernc.org/sqlite`). No CGO required.

### Blocklist Sources

The server configuration includes blocklist URLs. These are the same URLs Pi-hole subscribes to:

```text
https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts
https://raw.githubusercontent.com/StevenBlack/hosts/master/alternates/fakenews-gambling-social/hosts
https://cdn.jsdelivr.net/gh/hagezi/dns-blocklists@latest/adblock/pro.txt
https://urlhaus.abuse.ch/downloads/hostfile/
https://big.oisd.nl/
```

These lists come in two common formats:

- **Hosts format**: `0.0.0.0 ad.example.com` or `127.0.0.1 ad.example.com` — extract the domain (second field)
- **Domain-only/adblock format**: `ad.example.com` or `||ad.example.com^` — extract the domain

The parser handles both formats, ignoring comments (`#`, `!`) and blank lines.

### Database Schema

SQLite database stored at `<data-dir>/blocklist.db` (default data-dir: current working directory):

```sql
CREATE TABLE IF NOT EXISTS domains (
    domain TEXT NOT NULL PRIMARY KEY
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS sources (
    url      TEXT NOT NULL PRIMARY KEY,
    fetched  TEXT NOT NULL,  -- ISO 8601 timestamp of last fetch
    count    INTEGER NOT NULL -- number of domains from this source
) WITHOUT ROWID;
```

The `domains` table stores deduplicated domains across all sources. The `sources` table tracks fetch metadata. The `WITHOUT ROWID` optimization is ideal for these text-keyed lookup tables.

### Lifecycle

1. **First run**: No `blocklist.db` exists. If `--blocklist-url` flags are provided, the server downloads all lists and builds the database before starting to serve traffic.
2. **Subsequent runs**: The existing `blocklist.db` is loaded. The server starts serving immediately with the cached data.
3. **Rescan**: `fpsd update-blocklist` subcommand (or `--update-blocklist` flag on startup) re-downloads all configured URLs and rebuilds the database. This drops and recreates the `domains` table in a transaction.
4. **No URLs configured**: No blocking — passthrough mode, same as current behavior.

### CLI

```bash
fpsd --blocklist-url URL [--blocklist-url URL ...] [--data-dir DIR]
fpsd update-blocklist --blocklist-url URL [--blocklist-url URL ...] [--data-dir DIR]
```

- `--blocklist-url`: Repeatable flag, each specifying a blocklist URL to subscribe to
- `--data-dir`: Directory for `blocklist.db` (default: `.`)
- `update-blocklist`: Subcommand that downloads all URLs, rebuilds the DB, and exits

### Blocking Behavior

- **CONNECT requests**: If the target host (sans port) matches a domain in the DB, reject with `403 Forbidden` instead of establishing the tunnel
- **HTTP requests**: If the target URL host matches a domain in the DB, reject with `403 Forbidden` instead of forwarding
- Matching is exact (no wildcards, no subdomain matching beyond what's already in the lists)
- Matching is case-insensitive (domains stored lowercase in DB)

### In-Memory Cache

At startup, all domains from the SQLite DB are loaded into a `map[string]struct{}` for O(1) in-memory lookup. The SQLite DB is the persistent store; the map is the runtime lookup. With 450K domains this is roughly 30-40MB of memory — acceptable.

### Block Statistics

Add block counters to the existing probe/management endpoint:

```json
{
  "status": "ok",
  "service": "face-puncher-supreme",
  "version": "0.1.0",
  "mode": "blocking",
  "uptime_seconds": 3600,
  "connections_total": 1500,
  "connections_active": 12,
  "blocks_total": 342,
  "blocklist_size": 454636,
  "blocklist_sources": 5,
  "top_blocked": [
    {"domain": "googleads.g.doubleclick.net", "count": 87},
    {"domain": "pagead2.googlesyndication.com", "count": 54},
    {"domain": "securepubads.g.doubleclick.net", "count": 31}
  ]
}
```

- `blocks_total`: Total number of blocked requests since startup
- `blocklist_size`: Number of unique domains in the loaded blocklist (0 if none)
- `blocklist_sources`: Number of configured blocklist URLs
- `top_blocked`: Top 10 blocked domains by count
- `mode`: `"blocking"` when domains are loaded, `"passthrough"` when not

### Management Endpoints

No new endpoints needed. The existing `/fps/probe` endpoint gains the block stats fields above.

## File Changes

| File | Change |
| ---- | ------ |
| `internal/blocklist/blocklist.go` | New — SQLite DB management, domain lookup, in-memory cache |
| `internal/blocklist/parser.go` | New — hosts/adblock format parsing |
| `internal/blocklist/fetcher.go` | New — HTTP download of blocklist URLs |
| `internal/blocklist/blocklist_test.go` | New — unit tests for DB, parser, lookup |
| `internal/proxy/proxy.go` | Add blocking check before forwarding/tunneling |
| `internal/proxy/proxy.go` | Add block counters and top-blocked tracking |
| `internal/probe/probe.go` | Add block stats to probe response |
| `cmd/fpsd/main.go` | Add `--blocklist-url`, `--data-dir` flags, `update-blocklist` subcommand |
| `internal/proxy/proxy_test.go` | Add tests for blocked requests |

## Acceptance Criteria

- [x] `--blocklist-url` flags configure blocklist sources
- [x] First run with URLs downloads lists and builds `blocklist.db`
- [x] `fpsd update-blocklist` re-downloads and rebuilds the DB
- [x] Subsequent runs load from existing DB without re-downloading
- [x] Hosts format (`0.0.0.0 domain`) parsed correctly
- [x] Adblock/domain-only format (`||domain^` or bare `domain`) parsed correctly
- [x] CONNECT requests to blocked domains return 403
- [x] HTTP requests to blocked domains return 403
- [x] Non-blocked requests pass through unchanged (no regression)
- [x] Block counter increments on each blocked request
- [x] `/fps/probe` shows `blocks_total`, `blocklist_size`, `blocklist_sources`, and `top_blocked`
- [x] `/fps/probe` mode is `"blocking"` when domains loaded, `"passthrough"` when not
- [x] Matching is case-insensitive
- [x] Comments and blank lines in list files are ignored
- [x] Startup log shows number of domains loaded and sources configured
- [x] All existing tests pass (no regression)
- [x] New unit tests for DB operations, parser, and blocking behavior
- [x] Verified with real browser: ad domains blocked (macOS Safari + Apple News, 2026-02-16). **Note**: Pi-hole blocklists cause over-blocking in Safari (93.7% block rate, false positives on content APIs like `registry.api.cnn.io` and `cdn.optimizely.com`). Apple News ad blocking works correctly — `news.iadsdk.apple.com` blocked, ads suppressed, app functions normally. Blocklist tuning addressed in spec 005.

## Out of Scope

- Wildcard or regex matching
- Subdomain matching (the lists already include subdomains)
- Hot-reloading / runtime list updates (restart or run `update-blocklist`)
- List management UI
- Allowlist / exceptions
- Custom response body for blocked requests (plain 403 is fine for now)
- Scheduled/automatic list updates

## Reference: Pi-hole Sources

Current Pi-hole configuration (5 sources, 454K unique domains):

| Source | URL |
| ------ | --- |
| StevenBlack hosts | `https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts` |
| StevenBlack fakenews+gambling+social | `https://raw.githubusercontent.com/StevenBlack/hosts/master/alternates/fakenews-gambling-social/hosts` |
| Hagezi Pro | `https://cdn.jsdelivr.net/gh/hagezi/dns-blocklists@latest/adblock/pro.txt` |
| URLhaus | `https://urlhaus.abuse.ch/downloads/hostfile/` |
| OISD Big | `https://big.oisd.nl/` |
