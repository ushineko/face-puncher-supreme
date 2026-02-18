# Spec 016: WebSocket Reconnect Auth Revalidation

**Status**: COMPLETE
**Priority**: Medium
**Type**: UX improvement
**Scope**: Frontend only (React/TypeScript in `web/ui/`)

---

## Problem

When the fpsd server restarts (e.g., during an upgrade), the WebSocket connection drops and auto-reconnects. The `ReconnectBanner` component already shows "Reconnecting to server..." during the disconnection. However, sessions are stored in-memory (`web/auth.go` sessionStore) and are lost on restart. After the WebSocket reconnects:

- The frontend still considers itself authenticated (React state `authenticated = true`)
- The dashboard shows stale/empty data with no indication that re-login is needed
- The user must manually navigate or trigger an API call to discover the session is gone

## Solution

When the WebSocket transitions from disconnected to connected (reconnect), call `authStatus()` to verify the session. If the session is invalid (server returns 401), the existing `fps:unauthorized` event mechanism triggers logout and shows the login dialog. If the session is still valid, the dashboard resumes seamlessly.

This approach:
- No jarring immediate logout on WebSocket disconnect (banner handles UX during outage)
- Auth check only on successful reconnect (not on every disconnect)
- Uses the existing `fps:unauthorized` → `useAuth` → login redirect pipeline

## Design

### Change 1: WebSocket reconnect callback

Add an `onReconnect` callback to `FPSSocket` that fires when the connection transitions from closed → open (not on the initial connect). This distinguishes first connect from reconnect.

### Change 2: Wire auth revalidation in App.tsx

In `App.tsx`, register an `onReconnect` handler that calls `authStatus()`. The existing `apiFetch` wrapper already dispatches `fps:unauthorized` on 401, and `useAuth` already listens for that event.

### Files Changed

| File | Change |
|------|--------|
| `web/ui/src/ws.ts` | Add `onReconnect` callback, track first-connect vs reconnect |
| `web/ui/src/App.tsx` | Register reconnect handler that calls `authStatus()` |

### No Backend Changes

The backend `/fps/api/auth/status` endpoint already returns the correct auth state. No changes needed.

---

## Acceptance Criteria

- [x] WebSocket reconnect (after a disconnect) triggers an auth status check
- [x] If the session is invalid after reconnect, the user sees the login dialog (not a broken dashboard)
- [x] If the session is still valid after reconnect, the dashboard resumes seamlessly with no interruption
- [x] The initial WebSocket connect (on first login) does NOT trigger the auth revalidation
- [x] The "Reconnecting to server..." banner still appears during disconnection (no regression)
- [x] Existing Go tests pass (`make test`)
- [x] UI builds without errors (`make build-ui`)

---

## Out of Scope

- Session persistence across server restarts (that would be a separate feature)
- Reconnect overlay/dimming of the dashboard content area
- WebSocket auth (currently unauthenticated WebSocket; auth is cookie-based HTTP only)
