# Face Puncher Supreme: Project Retrospective

**Date**: 2026-02-17
**Version**: 1.0.0
**Duration**: Single development session (2026-02-16)
**Commits**: 23

---

## Project Summary

Face Puncher Supreme is a content-aware HTTPS interception proxy built to block ads in contexts where DNS-based blocking fails — specifically, Apple News and Reddit, where ad content is served from the same domains as editorial content.

The project went from first commit to v1.0.0 (web dashboard, 9 specs complete, 242 tests passing) in a single day. Development used the Ralph Wiggum methodology: spec-driven, iterative, with validation gates at every commit.

### Final Metrics

| Metric | Value |
|--------|-------|
| Go code | 9,749 lines (3,756 in tests) |
| Frontend (TS/TSX/CSS) | 1,212 lines |
| Test count | 242 passing |
| Specs completed | 9 of 9 |
| Validation reports | 10 (all PASSED) |
| Binary size | 17 MB |
| Blocklist domains | 416,476 |
| External Go deps | 5 runtime (cobra, lumberjack, yaml.v3, zombiezen/sqlite, nhooyr/websocket) |
| npm audit | 0 vulnerabilities |
| Lint issues | 0 (golangci-lint v2.9.0) |

---

## Spec-by-Spec Learnings

### Spec 001: Proxy Foundation

**What went well**: stdlib-only HTTP/HTTPS proxy worked on first attempt. Test suite covered HTTP forwarding, CONNECT tunnels, redirects, and streaming. Graceful shutdown pattern established early.

**Challenge**: The initial lint pass (integrated post-001) surfaced 31 issues. Adding `ReadHeaderTimeout` for Slowloris protection was the only functional fix — the rest were style/hygiene.

**Lesson**: Integrate the linter from the start, not after the first feature. Retrofitting lint compliance is more work than writing lint-clean code from the beginning.

### Spec 002: Domain Blocklist

**What went well**: Pi-hole hosts format and adblock/domain-only format parsers worked correctly. SQLite via zombiezen.com/go/sqlite (pure Go, no CGO) eliminated build complexity. 376K domains loaded and queryable.

**Challenge**: Live testing on macOS revealed a 93.7% block rate in Safari — the Pi-hole lists are tuned for DNS resolvers where blocking a tracker domain doesn't break the page. In a forward proxy, blocking those same domains returns 403s that break page loads. Content APIs (`registry.api.cnn.io`, `cdn.optimizely.com`) were false positives.

**Lesson**: Blocklists are context-dependent. A list that works for DNS blocking may be too aggressive for a forward proxy. This directly motivated Spec 005 (allowlist).

### Spec 003: YAML Config

**What went well**: Clean separation between config file, CLI flags, and defaults. The `Merge` function with `CLIOverrides` struct made the precedence chain explicit and testable.

**Challenge**: Custom `Duration` type for YAML marshaling required careful handling — Go's `time.Duration` doesn't implement `yaml.Unmarshaler` natively.

**Lesson**: Minor friction, well-handled. Config validation at startup (rather than at point-of-use) catches misconfigurations early.

### Spec 004: Database Statistics

**What went well**: Delta-based flush strategy was the right call. By tracking `lastClients`/`lastDomainReqs`/`lastDomainBlks` snapshots, each flush writes only the increment since the previous flush. No double-counting even across restarts (since unflushed data is merged with DB totals for queries).

**Challenge**: The `MergedTop*` query pattern (DB cumulative + unflushed in-memory delta) required careful reasoning about snapshot consistency. Getting it wrong would show incorrect counts.

**Critical bug found later (Spec 009)**: The single `*sqlite.Conn` had no mutex protection. When the WebSocket hub (Spec 009) started querying stats every 3 seconds while the flush loop writes every 60 seconds, concurrent access caused a SIGSEGV in the SQLite C library. Fixed by adding `sync.Mutex` to the `DB` struct.

**Lesson**: Single-connection SQLite is fine for single-goroutine access patterns. The moment a second goroutine needs the same connection, you need a mutex or connection pool. This wasn't caught during Spec 004 testing because there was only one reader (the HTTP handler, which runs in the request goroutine) and one writer (the flush loop). The dashboard introduced a persistent second reader.

### Spec 005: Allowlist and Blocklist Tuning

**What went well**: Suffix matching (`*.cnn.io`) solved the false positive problem cleanly. Allowlist priority over all block sources was the right design — no ambiguity about what wins.

**Challenge**: Testing on macOS was essential. The false positive list couldn't have been derived from code inspection alone — it required live browsing with Safari and observing which sites broke.

**Lesson**: Cross-system testing (Linux dev → macOS test) added overhead but was necessary. The macOS agent guide pattern (structured tasks with acceptance criteria) worked well for coordinating between environments.

### Spec 006: MITM TLS Interception

**What went well**: The architecture cleanly separated MITM from non-MITM flows. Non-MITM CONNECT requests use opaque TCP tunnels (zero overhead). MITM only activates for explicitly configured domains. Dynamic leaf cert generation with in-memory caching avoided disk I/O per connection.

**Challenge**: CA certificate installation on macOS required manual System Keychain steps. The `/fps/ca.pem` endpoint simplified distribution but trust still required user action.

**Lesson**: MITM infrastructure is the foundation — keep it generic. The `ResponseModifier` hook was nil by default (passthrough), making it safe to ship MITM support before any filtering logic existed.

### Spec 007: Content Filter Plugin Architecture

**What went well**: The `ContentFilter` interface (`Name`, `Version`, `Domains`, `Init`, `Filter`) was minimal and sufficient. Static compilation (no runtime plugin loading) kept the binary self-contained. Interception mode (capture to disk) enabled data-driven filter development.

**Challenge**: Import cycle between `plugin` and `stats` packages. Resolved by duplicating a small struct rather than introducing a shared package for a single type.

**Lesson**: Plugin architecture should be established before implementing any specific plugin. The interface was validated by the interception-mode stub before the Reddit filter was written.

### Spec 008: Reddit Promotions Filter

**What went well**: The filter successfully strips promoted posts, comment ads, and ad trackers from Reddit's Shreddit UI. Quick-skip optimization (check for ad markers before processing) avoids unnecessary regex/parsing on non-ad responses. URL scoping limits processing to relevant paths.

**Challenge**: This spec required **3 live-testing iterations** after the initial implementation:

1. **Accept-Encoding**: The proxy wasn't stripping `Accept-Encoding` from the upstream request, so Reddit responded with compressed content that the filter couldn't parse. Fix: strip the header so responses arrive uncompressed.
2. **Placeholder CSS**: The visible placeholder had no `max-width`, causing layout overflow on narrow screens. Fix: add `max-width: 100%; box-sizing: border-box` to the placeholder div.
3. **URL scoping**: The filter was processing all responses from `www.reddit.com`, including API calls and static assets. Fix: restrict to paths that serve HTML feeds (subreddit, user, search, home).

**Lesson**: Content filtering can't be fully validated without live traffic. Unit tests with captured fixtures verify the parsing logic, but integration issues (encoding, layout, path matching) only surface in real browsers. The interception → analysis → spec → implement → test loop is the right workflow for site-specific filters.

### Spec 009: Web Dashboard

**What went well**: The embedded SPA approach (Vite build → `go:embed` → single binary) eliminated deployment complexity. WebSocket multiplexing (stats/heartbeat/logs on one connection) kept the client simple. The `logbuf` package (circular buffer `slog.Handler` with subscriber fan-out) integrated naturally with Go's structured logging.

**Challenges**:

1. **SQLite concurrency crash (SIGSEGV)**: See Spec 004 section above. The dashboard's 3-second stats ticker introduced a concurrent reader that the stats DB wasn't designed for.
2. **Probe refactoring**: `BuildHeartbeat()` and `BuildStats()` had to be extracted from the HTTP handlers so the WebSocket hub could reuse them. This was a design oversight — the original probe handlers built JSON directly in the HTTP handler, coupling data construction to HTTP response writing.
3. **`logging.Setup` return type**: Changed from a tuple `(logger, cleanup)` to a `Result` struct with `LevelVar` for runtime verbose toggling. This was a breaking API change to an internal package, but necessary for the dashboard's log-level control.

**Lesson**: Features that add persistent background goroutines (WebSocket hub) change the concurrency model of existing subsystems. The stats DB worked fine with request-scoped goroutines but failed with a persistent polling goroutine. Always review mutex requirements when adding new concurrent consumers.

---

## Cross-Cutting Challenges

### 1. Live Testing Friction

The proxy runs on Linux but the primary test targets (Apple News, Safari) are on macOS. This required:
- A structured macOS agent guide with discrete tasks
- Manual proxy configuration on the Mac
- CA certificate installation in System Keychain
- Visual verification of ad blocking (no automation possible for Apple News)

**What would help**: A test harness that replays captured traffic through the proxy, allowing regression testing without live devices. The interception-mode captures from Spec 007 could serve as the basis for this.

### 2. Blocklist Tuning Is Empirical

There's no way to compute the right allowlist from first principles. It requires:
- Live browsing with the proxy active
- Observing breakage
- Adding allowlist entries
- Repeating

The current allowlist (3 entries + 1 wildcard) was derived from manual testing. As more users and sites are tested, the allowlist will grow. A mechanism to report false positives from the dashboard could streamline this.

### 3. SQLite Single-Connection Limitation

The pure-Go SQLite binding (`zombiezen.com/go/sqlite`) works well but a single `*sqlite.Conn` is not safe for concurrent use. The project hit this as a SIGSEGV when the dashboard introduced a second reader goroutine.

The fix (mutex around all DB methods) is correct but serializes all database access. For the current workload (reads every 3s, writes every 60s) this is fine. If query latency becomes a concern, switching to `sqlitex.Pool` would allow concurrent readers with exclusive writers.

### 4. Content Filtering Is Iterative

The Reddit filter required 3 fix iterations after initial implementation. Each iteration followed the same pattern: deploy → browse → observe failure → diagnose → fix → redeploy. This is inherent to content filtering — the target site's HTML structure, encoding, and path routing are implementation details that change without notice.

**Mitigation**: Capture real responses as test fixtures during each iteration. The project now has 6 Reddit HTML fixtures that serve as regression tests.

---

## Process Observations

### What Worked

- **Spec-driven development**: Each spec had clear acceptance criteria. Implementation was focused and verifiable. No scope creep within specs.
- **Validation gates at every commit**: Tests, lint, vet, code quality, security review, and release safety checked before every commit. Caught real issues (31 lint problems in Spec 001, SIGSEGV in Spec 009).
- **Sequential spec ordering**: Lower-numbered specs are foundations for higher-numbered ones (proxy → blocklist → config → stats → allowlist → MITM → plugins → filter → dashboard). No spec required rework of a previous spec's design.
- **Interception-before-filtering**: Capturing live traffic (Spec 007 interception mode) before writing filter rules (Spec 008) produced data-driven filters rather than speculative ones.
- **Cross-system testing via agent guide**: The macOS agent guide gave structure to what would otherwise be ad-hoc manual testing.

### Process Gaps

1. **No concurrency testing**: The `-race` flag was present in test commands, but no test exercised concurrent access to the stats DB from multiple goroutines. A test that runs `Flush()` and `MergedTopBlocked()` concurrently would have caught the SIGSEGV before the dashboard was built.

2. **No integration test for the dashboard WebSocket**: The dashboard has no Go-side tests (the `web` package has `[no test files]`). The auth, WebSocket, and SPA serving logic are only verified manually. Adding tests for the session store, auth middleware, and WebSocket message encoding would improve confidence.

3. **No automated cross-platform testing**: macOS testing is entirely manual. While full automation isn't feasible for Apple News, a traffic-replay test harness could validate the proxy's behavior against captured sessions.

4. **Validation report not updated for mid-spec fixes**: The Spec 008 validation report covers the initial implementation, but the 3 fix iterations (Accept-Encoding, placeholder CSS, URL scoping) were committed as a separate fix without their own validation report. The process requires a report per commit, but fix commits sometimes skip this.

5. **No load testing**: The proxy handles real household traffic (~100 req/min) without issues, but there's no benchmark for throughput limits, memory growth under sustained load, or behavior under connection storms.

---

## Architecture Decisions Worth Revisiting

### 1. Single Binary vs. Separate Frontend

The embedded SPA adds 2 MB to the binary and requires `npm ci && npx vite build` in the build chain. For a personal project this is fine. For broader distribution, consider whether the dashboard should be optional (build tag) or a separate process.

### 2. Stats DB Schema

The hourly rollup (`traffic_hourly`) works for dashboard display but loses granularity. If per-minute or per-request analytics are needed later, the schema would need extension. The current design prioritizes disk efficiency over query flexibility.

### 3. Plugin Registration

Plugins are statically compiled into the binary. Adding a new plugin requires modifying `internal/plugin/registry.go` and rebuilding. For a personal project with one plugin this is fine. If more plugins are added, a more dynamic registration pattern (build tags, or runtime loading via Go plugins) might be worth considering.

---

## Summary

The project achieved its goals: Apple News ads are blocked at the domain level, Reddit promoted posts are stripped via MITM content filtering, and the whole system is observable via a live dashboard. The spec-driven approach kept each feature focused, and validation gates caught real bugs before they shipped to production (with the notable exception of the SQLite concurrency issue, which was caught during live dashboard testing).

The main areas for improvement are test coverage for concurrent access patterns, automated regression testing against captured traffic, and integration tests for the web dashboard package.
