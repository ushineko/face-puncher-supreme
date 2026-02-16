# Spec 005: Allowlist and Blocklist Tuning

**Status**: COMPLETE
**Created**: 2026-02-16
**Depends on**: Spec 002 (domain blocklist), Spec 003 (config file)

## Problem Statement

macOS live testing (2026-02-16) confirmed that domain-level proxy blocking works for Apple News ads. However, it also revealed that Pi-hole-style blocklists are too aggressive for general proxy use. Safari browsing through the proxy had a 93.7% block rate, with false positives on content APIs (`registry.api.cnn.io`), A/B testing services (`cdn.optimizely.com`), and other domains that break page rendering.

The proxy needs two mechanisms:

1. **Allowlist** — domains that are never blocked, even if they appear in downloaded blocklists. This lets users subscribe to broad blocklists while carving out exceptions for known false positives.
2. **Inline blocklist** — individual domains specified directly in config, without needing a downloaded blocklist URL. This lets users block specific domains (like `news.iadsdk.apple.com`) without pulling in 376K domains from Pi-hole lists.

## Approach

### Config Changes

Add two new fields to `fpsd.yml`:

```yaml
# Existing field — bulk blocklists downloaded from URLs
blocklist_urls:
  - https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts
  - https://big.oisd.nl/

# NEW — individual domains to block (no download, loaded directly)
blocklist:
  - news.iadsdk.apple.com
  - news-events.apple.com
  - news-app-events.apple.com

# NEW — domains to never block (overrides both blocklist_urls and blocklist)
allowlist:
  - registry.api.cnn.io
  - cdn.optimizely.com
  - "*.cnn.io"
```

**Priority order**: allowlist wins over all block sources. If a domain matches both the allowlist and a blocklist (URL-based or inline), it passes through.

### Allowlist Matching

Two match types:

- **Exact match**: `registry.api.cnn.io` matches only that domain
- **Suffix match**: `*.cnn.io` matches `cnn.io` itself and any subdomain (`registry.api.cnn.io`, `www.cnn.io`, etc.)

Suffix matching is allowlist-only. Blocklists continue to use exact matching (the downloaded lists already enumerate subdomains). This asymmetry is intentional — allowlists are manually curated and it's impractical to enumerate every subdomain of a service you want to un-block.

### Implementation

The allowlist is config-only — no SQLite table, no download mechanism. It is expected to be small (tens to low hundreds of entries). It loads into an in-memory data structure at startup alongside the blocklist cache.

**Data structures**:

```go
type DB struct {
    // existing fields...
    domains   map[string]struct{}   // blocklist (from URLs + inline)
    exact     map[string]struct{}   // allowlist exact matches
    suffixes  []string              // allowlist suffix patterns (sorted, longest first)
}
```

**Lookup flow** (modified `IsBlocked`):

1. Lowercase the domain
2. Check allowlist exact match → if found, return false (not blocked)
3. Check allowlist suffix match → if found, return false (not blocked)
4. Check blocklist map → if found, increment counters, return true (blocked)
5. Return false (not blocked)

The allowlist check happens first so that allowlisted domains never increment block counters.

**Suffix matching algorithm**: For each suffix pattern `*.example.com`, check if the input domain equals `example.com` or ends with `.example.com`. With a small number of suffixes (expected <100), linear scan is sufficient. No need for a trie or other complex structure.

### Inline Blocklist

Inline `blocklist` entries are merged into the same in-memory map as URL-sourced domains. They are loaded from config at startup and added to the `domains` map after URL-sourced domains are loaded.

Inline entries are not stored in `blocklist.db` — they exist only in config and in memory. This means:

- `fpsd update-blocklist` only re-downloads URL sources, it does not affect inline entries
- Inline entries survive a blocklist rebuild (they come from config, not from the DB)
- The `blocklist_size` stat reflects the total (URL-sourced + inline) domain count

### Stats Changes

Add allowlist visibility to the stats endpoint (`/fps/stats`):

```json
{
  "blocking": {
    "blocks_total": 342,
    "allows_total": 58,
    "blocklist_size": 376042,
    "allowlist_size": 12,
    "top_blocked": [...],
    "top_allowed": [
      {"domain": "cdn.optimizely.com", "count": 28},
      {"domain": "registry.api.cnn.io", "count": 18}
    ]
  }
}
```

- `allows_total`: Number of requests that matched the blocklist but were saved by the allowlist
- `allowlist_size`: Number of allowlist entries (exact + suffix patterns)
- `top_allowed`: Top 10 allowlisted domains by count. Helps users see which allowlist entries are actively preventing false positives.

### CLI

No new CLI flags. Allowlist and inline blocklist are config-file-only. The `--blocklist-url` flag continues to work for URL sources.

Rationale: these are tuning knobs that users adjust iteratively based on browsing experience. Config file is the right UX for this — edit, restart, test. CLI flags are better for one-off overrides.

## File Changes

| File | Change |
| ---- | ------ |
| `internal/config/config.go` | Add `Blocklist` and `Allowlist` fields to `Config` struct |
| `internal/config/config_test.go` | Test parsing of new fields |
| `internal/blocklist/blocklist.go` | Add allowlist data structures, modify `IsBlocked` to check allowlist first, add `allows_total` counter, add inline domain loading |
| `internal/blocklist/blocklist_test.go` | Tests for allowlist exact/suffix matching, inline blocklist, priority ordering |
| `internal/stats/collector.go` | Add `allows_total` and per-domain allow counters |
| `internal/stats/db.go` | Persist allow stats alongside block stats |
| `internal/probe/probe.go` | Add `allows_total`, `allowlist_size`, `top_allowed` to stats response |

## Acceptance Criteria

- [x] `allowlist` field in `fpsd.yml` accepts a list of domain patterns
- [x] Exact match allowlist entries prevent blocking of that domain
- [x] Suffix match (`*.example.com`) prevents blocking of the base domain and all subdomains
- [x] Allowlist takes priority over both URL-sourced and inline blocklist entries
- [x] `blocklist` field in `fpsd.yml` accepts a list of individual domains to block
- [x] Inline blocklist entries are merged with URL-sourced domains in the in-memory map
- [x] Inline entries survive `fpsd update-blocklist` (not stored in DB, reloaded from config)
- [x] `blocklist_size` stat reflects total domains (URL-sourced + inline)
- [x] `allows_total` counter increments when a blocklisted domain is allowlisted
- [x] `allowlist_size` stat reflects the number of allowlist entries
- [x] `top_allowed` shows the top 10 allowlisted domains by count
- [x] Stats persistence: allow counters are flushed to `stats.db` alongside block counters
- [x] Startup log shows allowlist size and inline blocklist count
- [x] All existing tests pass (no regression)
- [x] New tests for allowlist matching (exact, suffix, priority over blocklist)
- [x] New tests for inline blocklist loading and merging
- [ ] Verified on macOS: Apple News ads still blocked, Safari false positives resolved (via macOS agent guide Task 005)

## Out of Scope

- Allowlist URLs (downloading allowlists from remote sources)
- Regex matching in allowlist (suffix matching covers the practical cases)
- Per-client allowlist/blocklist (all clients share the same lists)
- Hot-reload of config (restart required to pick up changes)
- Blocklist suffix/wildcard matching (lists already enumerate subdomains)
- Allowlist for the inline blocklist only (allowlist applies uniformly to all block sources)

## Example Configurations

### Minimal: Apple News ads only (no downloaded blocklists)

```yaml
blocklist:
  - news.iadsdk.apple.com
  - news-events.apple.com
  - news-app-events.apple.com
```

3 domains blocked. No Pi-hole lists. No false positives. Safari works normally.

### Balanced: Pi-hole lists with allowlist for known false positives

```yaml
blocklist_urls:
  - https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts
  - https://big.oisd.nl/

allowlist:
  - registry.api.cnn.io
  - cdn.optimizely.com
  - "*.rlcdn.com"
```

Broad ad blocking with surgical exceptions for domains that break pages.

### Combined: Pi-hole lists plus targeted Apple domains

```yaml
blocklist_urls:
  - https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts

blocklist:
  - news.iadsdk.apple.com
  - news-events.apple.com
  - news-app-events.apple.com

allowlist:
  - registry.api.cnn.io
  - cdn.optimizely.com
```

Ensures Apple News ad domains are blocked even if they don't appear in the Pi-hole list, while fixing known false positives.
