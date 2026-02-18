## Validation Report: WebSocket Reconnect Auth Revalidation (Spec 016)
**Date**: 2026-02-17 23:50
**Commit**: (pending)
**Status**: PASSED

### Phase 3: Tests
- Test suite: `make test` (`go test -race -short -v ./...`)
- Results: 128 passing, 0 failing
- Status: PASSED

### Phase 4: Code Quality
- Dead code: None found
- Duplication: None found
- Encapsulation: Clean — reconnect listener follows existing `setStatusListener` pattern
- Refactorings: None needed
- Status: PASSED

### Phase 5: Security Review
- Dependencies: No new dependencies
- OWASP Top 10: N/A — uses existing auth check endpoint, no new auth logic
- Anti-patterns: None found
- Status: PASSED

### Phase 5.5: Release Safety
- Change type: Code-only (frontend)
- Rollback plan: `git revert` — changes are isolated to two frontend files
- Status: PASSED

### Lint
- Tool: golangci-lint v2.9.0
- Results: 0 issues
- Status: PASSED

### Changes Summary

- `web/ui/src/ws.ts`: Added `hasConnectedOnce` flag and `onReconnectFn` callback to `FPSSocket`. On `ws.onopen`, fires the reconnect callback only after the first successful connection (distinguishes initial connect from reconnect). Reset on `disconnect()`.
- `web/ui/src/App.tsx`: Registers a reconnect listener that calls `api.authStatus()` to revalidate the session. On 401, the existing `fps:unauthorized` → `useAuth` pipeline redirects to login.

### Overall
- All gates passed: YES
