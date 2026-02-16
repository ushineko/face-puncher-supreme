## Validation Report: Spec 004 — Database-Backed Statistics
**Date**: 2026-02-16 22:00
**Commit**: (pending)
**Status**: PASSED

### Phase 3: Tests
- Test suite: `make test` (`go test -race -short -v ./...`)
- Results: 77 passing, 0 failing (5 integration tests skipped in short mode)
- New tests: 16 (15 stats + 1 probe)
- Test breakdown by package:
  - `internal/stats`: 15 tests (5 collector, 10 DB: flush, top-N, merged, idempotent, limits, time-bounded)
  - `internal/probe`: 7 tests (4 heartbeat, 2 stats handler, 1 disabled)
  - `internal/proxy`: 16 tests (heartbeat endpoint, stats counters, blocking, tunnel, concurrent)
  - `internal/config`: 20 tests (defaults, duration, loading, merge, validation, dump)
  - `internal/blocklist`: 19 tests
- Status: PASSED

### Phase 4: Code Quality
- Dead code: `TopBlockedSince` stub removed (was unused wrapper)
- Duplication: 3 similar UPSERT patterns in Flush() — acceptable given distinct table schemas
- Encapsulation: Stats collector (in-memory), DB (persistence), probe (HTTP handlers) cleanly separated
- Delta-based flush: Fixed double-counting bug where cumulative snapshots were being re-added on each flush
- Merged queries: Fixed `TopBlocked(0)` / `TopRequested(0)` which passed `LIMIT 0` to SQLite (returned 0 rows)
- Refactorings: Converted if-else chain to switch (gocritic), inverted nesting in test (gocritic)
- Status: PASSED

### Phase 5: Security Review
- SQL injection: All queries use parameterized execution via `sqlitex.Execute` with `Args` option
- Path traversal: `filepath.Join(cfg.DataDir, "stats.db")` — DataDir comes from config, not user HTTP input
- Secrets: No hardcoded credentials
- Race conditions: `sync.Map` + `atomic.Int64` for lock-free counters; delta tracking uses snapshot-then-compare pattern (safe)
- Resource leaks: DB connection closed via deferred `statsDB.Close()` with final flush; flush loop goroutine exits on context cancel
- Input validation: `n` query param parsed with `strconv.Atoi`, only accepted if positive; `period` validated against allowlist
- Dependencies: `zombiezen.com/go/sqlite` (pure Go, no CGO) — same as blocklist, no new dependencies added
- Status: PASSED

### Phase 5.5: Release Safety
- Change type: Code + schema (new SQLite tables) + config extension + endpoint rename
- Rollback plan: `git revert` removes all changes; stats.db is a new file (delete it); /fps/probe callers need to update to /fps/heartbeat
- Breaking change: `/fps/probe` endpoint removed, replaced by `/fps/heartbeat` and `/fps/stats`. macOS agent guide updated.
- Status: PASSED

### Overall
- All gates passed: YES
- Notes:
  - Pre-existing medium issues noted (silent DB read errors, CONNECT goroutine coordination) — not introduced by this spec, deferred to future work
  - Flush uses delta-based tracking to prevent double-counting across periodic flushes
  - Merged queries (DB + unflushed in-memory deltas) provide accurate all-time totals without double-counting
