## Validation Report: Dashboard Stats Bugfixes (Spec 015)
**Date**: 2026-02-17 23:00
**Commit**: (pending)
**Status**: PASSED

### Phase 3: Tests
- Test suite: `make test` (`go test -race -short -v ./...`)
- Results: 128 passing, 0 failing
- Status: PASSED

### Phase 4: Code Quality
- Dead code: None found (removed dead `useRef` import use after conversion to `useState`)
- Duplication: None found
- Encapsulation: Clean — `OnPluginInspect` callback type follows existing `OnFilterMatch` pattern
- Refactorings: None needed
- Status: PASSED

### Phase 5: Security Review
- Dependencies: No new dependencies added
- OWASP Top 10: N/A — changes are frontend state management and backend callback wiring
- Anti-patterns: None found
- Status: PASSED

### Phase 5.5: Release Safety
- Change type: Code-only (frontend + backend)
- Rollback plan: `git revert` the commit; both fixes are isolated and additive
- Status: PASSED

### Lint
- Tool: golangci-lint v2.9.0
- Results: 0 issues
- Status: PASSED

### Go Vet
- Results: 0 issues
- Status: PASSED

### Changes Summary

**Bug 1 — Traffic graph frozen on first load:**
- `web/ui/src/pages/Stats.tsx`: Replaced `useRef<TimePoint[]>` with `useState<TimePoint[]>` + `useEffect` so `LineChart` receives a new array reference on each WebSocket update, triggering its canvas draw effect correctly.

**Bug 2 — Plugin stats always zero:**
- `internal/plugin/registry.go`: Added `OnPluginInspect` callback type; updated `BuildResponseModifier` to accept and call `onInspect` before dispatching to `Filter()`.
- `cmd/fpsd/main.go`: Wired `collector.RecordPluginInspected` as the new `onInspect` callback.
- `internal/plugin/plugin_test.go`: Updated all three `BuildResponseModifier` test call sites for new signature; added `onInspect` assertions.

### Overall
- All gates passed: YES
