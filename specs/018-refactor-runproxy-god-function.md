# Spec 018: Refactor `runProxy()` God Function

**Status**: COMPLETE

---

## Background

`runProxy()` in `cmd/fpsd/main.go` (lines 175-509) is the main entry point
that initializes all proxy subsystems. At 334 lines it handles 14 sequential
responsibilities: config loading, logging, blocklist, MITM, plugins, stats,
dashboard, transparent proxy, and graceful shutdown. The function has explicit
`//nolint:gocognit,gocyclo,cyclop` suppressions because it exceeds all three
complexity thresholds.

Two helper functions have already been extracted (`initPlugins`,
`makeBlockDataFn`), establishing the pattern for this refactoring. The
remaining subsystem init logic is inline.

A secondary target exists in `internal/stats/db.go` where `Flush()` (lines
113-221) contains four structurally identical delta-compute-then-upsert blocks
that differ only in table name and snapshot source.

## Objective

Break `runProxy()` into named initialization phases so the function reads as
a table of contents (~50 lines). Extract the repeated pattern in `Flush()`.
No behavioral changes.

---

## Scope

### In scope

- `cmd/fpsd/main.go` — extract `runProxy()` inline logic into named helpers
- `internal/stats/db.go` — deduplicate `Flush()` domain-level blocks

### Out of scope

- Bidirectional copy pattern in `proxy.handleConnect()` and
  `transparent.handleHTTPS()` — looks similar but differs structurally
  (fire-and-forget vs WaitGroup, no half-close vs CloseWrite). Extracting a
  shared helper would parameterize the concurrency model, adding more
  complexity than the duplication it eliminates.
- `proxyLoop()` in `internal/mitm/handler.go` — complex but cohesive; its
  concerns (read request, forward, read response, modify, write back) are
  tightly coupled steps in a single protocol loop.
- No new packages. All helpers stay in their respective files.
- No new tests for wiring helpers (see rationale below).

---

## Part 1: `runProxy()` Extraction

### Result Structs

Two small structs group multi-value returns from init functions. Defined in
`main.go` near existing types:

```go
// blocklistResult holds initialized blocklist resources.
type blocklistResult struct {
    bl          *blocklist.DB
    blocker     proxy.Blocker           // nil if no entries
    blockDataFn func() *probe.BlockData // nil if no entries
}

// mitmResult holds initialized MITM resources. Zero-valued when disabled.
type mitmResult struct {
    interceptor  *mitm.Interceptor
    caPEMHandler http.HandlerFunc
    dataFn       func() *probe.MITMData
}
```

The caller still owns lifecycle (`defer blRes.bl.Close()`). No hidden cleanup.

### Extracted Functions

| Function | Current lines | Responsibility | Returns |
|---|---|---|---|
| `initLogging(cfg)` | 181-190 | Log buffer + structured logging setup | `(*logbuf.Buffer, logging.Result)` |
| `initBlocklist(cfg, logger)` | 192-230 | DB open, first-run fetch, allowlist, inline domains | `(*blocklistResult, error)` |
| `initMITM(cfg, bl, logger, collector)` | 240-301 | CA load, interceptor, PEM handler, expiry warning | `(mitmResult, error)` |
| `initStatsDB(cfg, collector, bl, logger)` | 309-326 | Conditional stats DB open, allow source wiring | `(*stats.DB, error)` |
| `makeTransparentDataFn(cfg, mitmEnabled, logger)` | 328-341 | Transparent data callback for probe | `func() *probe.TransparentData` |
| `initHandlers(cfg, srv, collector, statsDB, blockDataFn, mitmDataFn, transparentDataFn, pluginsDataFn, logger)` | 360-378 | Build heartbeat/stats handlers, wire into server | `*probe.StatsProvider` |
| `initDashboard(cfg, srv, statsProvider, blockDataFn, mitmDataFn, transparentDataFn, pluginsDataFn, bl, logBuf, logResult, logger)` | 380-415 | Dashboard with JSON callbacks | `func()` (cleanup fn) |
| `initTransparentListener(cfg, blocker, mitmInterceptor, collector, logger)` | 422-456 | Transparent listener with 5 stat callbacks | `*transparent.Listener` |
| `runServers(cfg, srv, tpListener, bl, logger)` | 458-508 | Start listeners, signal wait, ordered shutdown | `error` |

### What Stays Inline

- `stats.NewCollector()` — one-liner, no benefit to extracting
- `proxy.New(&proxy.Config{...})` — struct literal initialization, not logic
- `statsDB.Start()` — one-liner

### Resulting `runProxy()`

```go
func runProxy(cmd *cobra.Command, _ []string) error {
    cfg, err := loadConfig(cmd)
    if err != nil {
        return err
    }

    logBuf, logResult := initLogging(cfg)
    defer logResult.Cleanup()
    logger := logResult.Logger

    blRes, err := initBlocklist(cfg, logger)
    if err != nil {
        return err
    }
    defer blRes.bl.Close()

    collector := stats.NewCollector()

    mr, err := initMITM(cfg, blRes.bl, logger, collector)
    if err != nil {
        return err
    }

    pluginsDataFn, err := initPlugins(&cfg, mr.interceptor, collector, logger)
    if err != nil {
        return err
    }

    statsDB, err := initStatsDB(cfg, collector, blRes.bl, logger)
    if err != nil {
        return err
    }
    if statsDB != nil {
        defer statsDB.Close()
    }

    transparentDataFn := makeTransparentDataFn(cfg, mr.interceptor != nil, logger)

    srv := proxy.New(&proxy.Config{
        ListenAddr:        cfg.Listen,
        Logger:            logger,
        Verbose:           cfg.Verbose,
        Blocker:           blRes.blocker,
        MITMInterceptor:   mr.interceptor,
        ConnectTimeout:    cfg.Timeouts.Connect.Duration,
        ReadHeaderTimeout: cfg.Timeouts.ReadHeader.Duration,
        ManagementPrefix:  cfg.Management.PathPrefix,
        HeartbeatHandler:  http.NotFound,
        StatsHandler:      http.NotFound,
        CAPEMHandler:      mr.caPEMHandler,
        OnRequest:         collector.RecordRequest,
        OnTunnelClose:     collector.RecordBytes,
    })

    statsProvider := initHandlers(cfg, srv, collector, statsDB,
        blRes.blockDataFn, mr.dataFn, transparentDataFn, pluginsDataFn, logger)

    defer initDashboard(cfg, srv, statsProvider,
        blRes.blockDataFn, mr.dataFn, transparentDataFn, pluginsDataFn,
        blRes.bl, logBuf, logResult, logger)()

    if statsDB != nil {
        statsDB.Start()
    }

    tpListener := initTransparentListener(
        cfg, blRes.blocker, mr.interceptor, collector, logger)

    return runServers(cfg, srv, tpListener, blRes.bl, logger)
}
```

The `//nolint:gocognit,gocyclo,cyclop` suppression is removed.

### Defer Ordering

The current defer execution order must be preserved:

1. `logResult.Cleanup()` (last to run — logging stays active during shutdown)
2. `blRes.bl.Close()`
3. `statsDB.Close()` (includes final flush)
4. `dashboard.Stop()` (via the cleanup function returned by `initDashboard`)

The refactored code maintains this ordering because defers execute LIFO and
the calls appear in the same sequence as the original.

---

## Part 2: `Flush()` Deduplication

### File: `internal/stats/db.go`

The three domain-level flush blocks (blocked_domains at lines 150-171,
domain_requests at lines 173-194, allowed_domains at lines 196-218) follow
an identical pattern:

1. Snapshot current counts into `map[string]int64`
2. Compute delta vs. previous snapshot
3. Skip if delta is zero
4. `INSERT ... ON CONFLICT DO UPDATE SET count = count + excluded.count`
5. Update the `last*` map

Extract into:

```go
// flushDomainDeltas upserts delta counts for a single domain-counter table.
func (db *DB) flushDomainDeltas(table string, current, last map[string]int64) error {
    for domain, count := range current {
        delta := count - last[domain]
        if delta == 0 {
            continue
        }
        err := sqlitex.Execute(db.conn, fmt.Sprintf(`
            INSERT INTO %s (domain, count) VALUES (?, ?)
            ON CONFLICT (domain) DO UPDATE SET count = count + excluded.count
        `, table), &sqlitex.ExecOptions{Args: []any{domain, delta}})
        if err != nil {
            return fmt.Errorf("upsert %s: %w", table, err)
        }
    }
    return nil
}
```

Also extract a small `snapshotToMap` helper since `SnapshotDomainBlocks()`
and `SnapshotDomainRequests()` return `[]DomainCount`, not `map[string]int64`:

```go
// snapshotToMap converts a DomainCount slice to a domain->count map.
func snapshotToMap(counts []DomainCount) map[string]int64 {
    m := make(map[string]int64, len(counts))
    for _, dc := range counts {
        m[dc.Domain] = dc.Count
    }
    return m
}
```

The per-client block (traffic_hourly) stays as-is — different schema
(multi-column with hour key), different snapshot type (`ClientSnapshot`).

**SQL injection note**: Table names are hardcoded string literals from our own
code (`"blocked_domains"`, `"domain_requests"`, `"allowed_domains"`), not
user input. The `fmt.Sprintf` for table name is safe.

---

## Test Strategy

No new tests. Rationale:

- The extracted functions are wiring code — they call already-tested package
  APIs and return the results. Testing them would require mocking every
  dependency (blocklist DB, MITM CA, stats collector, etc.) to verify that
  `New()` was called with the right args. This is the "mock the universe"
  anti-pattern that encodes implementation details rather than behavioral
  contracts.
- The existing 128 tests cover the behavioral contracts of every subsystem.
- The `Flush()` deduplication is covered by existing `stats` tests.
- Correctness is verified by: all tests pass, lint passes, the service starts
  and serves traffic.

---

## Implementation Plan

### Commit 1: Refactor `runProxy()` into named init phases

1. Add `blocklistResult` and `mitmResult` structs
2. Extract functions bottom-up (from end of `runProxy` backward to avoid
   line-number drift): `runServers`, `initTransparentListener`,
   `initDashboard`, `initHandlers`, `makeTransparentDataFn`, `initStatsDB`,
   `initMITM`, `initBlocklist`, `initLogging`
3. Rewrite `runProxy()` body as table-of-contents calls
4. Remove `//nolint:gocognit,gocyclo,cyclop` from `runProxy` signature

### Commit 2: Deduplicate `Flush()` domain-level blocks

1. Add `flushDomainDeltas` and `snapshotToMap` methods
2. Replace three inline blocks in `Flush()` with calls

### Validation (both commits)

- [ ] `make test` — 128 tests pass
- [ ] `make lint` — 0 warnings (gocognit nolint removed)
- [ ] `go vet ./...` — clean
- [ ] `make build` — binary builds
- [ ] Service starts and responds to `/fps/heartbeat`

---

## Acceptance Criteria

- [ ] `runProxy()` is under 60 lines (excluding the inline `proxy.New` struct literal)
- [ ] `//nolint:gocognit,gocyclo,cyclop` suppression removed from `runProxy`
- [ ] Each extracted function has a doc comment stating its responsibility
- [ ] No behavioral changes — identical startup log output, identical
      heartbeat/stats responses, identical shutdown sequence
- [ ] `Flush()` domain-level blocks replaced by `flushDomainDeltas` calls
- [ ] All quality gates pass (tests, lint, vet)
