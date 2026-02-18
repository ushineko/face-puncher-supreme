## Validation Report: WebSocket Reconnect Auth Fix (Spec 016 bugfix)
**Date**: 2026-02-18 06:10
**Commit**: (pending)
**Status**: PASSED

### Phase 3: Tests
- Test suite: `make test` (`go test -race -short -v ./...`)
- Results: 177 passing, 0 failing
- Status: PASSED

### Phase 4: Code Quality
- Dead code: None found
- Duplication: None found
- Encapsulation: Clean — changes confined to two frontend files
- Refactorings: None needed
- Status: PASSED

### Phase 5: Security Review
- Dependencies: No new dependencies
- OWASP Top 10: N/A — uses existing auth check endpoint, no new auth logic
- Anti-patterns: None found
- Status: PASSED

### Phase 5.5: Release Safety
- Change type: Code-only (frontend)
- Rollback plan: `git revert` — changes isolated to two frontend files
- Status: PASSED

### Lint
- Tool: golangci-lint v2.9.0
- Results: 0 issues
- Status: PASSED

### Root Cause Analysis

Two bugs prevented spec 016 from working:

1. **`ws.onclose` never fired the reconnect callback**: The original code only called `onReconnectFn` in `ws.onopen`. After a server restart, the WS upgrade request is rejected by `requireAuth` middleware (401), so `onopen` never fires. Fix: also fire the callback in `ws.onclose` when `hasConnectedOnce && shouldConnect`.

2. **`authStatus()` response was discarded**: `handleAuthStatus` returns HTTP 200 with `{"authenticated":false}` (not 401), so `apiFetch` doesn't dispatch `fps:unauthorized`. The App.tsx code was `void api.authStatus().catch(() => {})` — catching network errors but ignoring the successful response. Fix: check `res.authenticated` and dispatch `fps:unauthorized` manually when false.

### Changes Summary

- `web/ui/src/ws.ts`: Added `onReconnectFn` call in `ws.onclose` handler when reconnecting after a previous connection, so auth is checked even when WS handshake fails (401 from middleware).
- `web/ui/src/App.tsx`: Changed reconnect callback to check `authStatus()` response — dispatches `fps:unauthorized` when `authenticated` is false instead of discarding the result.

### Overall
- All gates passed: YES
- Live-tested: server bounce correctly redirects to login screen
