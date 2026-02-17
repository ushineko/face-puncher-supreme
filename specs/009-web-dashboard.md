# Spec 009: Web Dashboard

**Status**: COMPLETE
**Depends on**: Spec 004 (database stats), Spec 007 (plugin architecture)

---

## Overview

Add a React-based web dashboard bundled into the fpsd binary, served at `/fps/dashboard`. The dashboard provides real-time visibility into proxy operations: live stats, heartbeat information, recent logs, project documentation, and proxy configuration management. Authentication is required via login/password configured through CLI flags or config file.

The frontend is built with Vite + React + TypeScript + Tailwind CSS, compiled to static assets, and embedded into the Go binary via `go:embed`. No external files needed at runtime — single-binary deployment is preserved.

All real-time updates are pushed via WebSocket. The server maintains a single multiplexed WebSocket connection per client that streams stats, heartbeat, and log data. No polling — the server pushes updates on its own schedule.

---

## Requirements

### D1: Embedded React Frontend

Build a React SPA using Vite + TypeScript + Tailwind CSS. The compiled output (`web/ui/dist/`) is embedded into the Go binary via `//go:embed`. The dashboard is served at `/fps/dashboard` and all sub-routes (e.g., `/fps/dashboard/logs`) resolve to the SPA's `index.html` for client-side routing.

**Build chain**:

1. `make build-ui` — copies README.md to `web/readme.md`, installs npm deps, runs `vite build`, outputs to `web/ui/dist/`
2. `make build` — depends on `build-ui`, then runs `go build` which embeds `web/ui/dist/*` and `web/readme.md`

Tailwind CSS is compiled at build time via Vite plugin — zero runtime CSS, only the classes actually used are included in the output. This keeps the bundle minimal.

**Dev mode**: When `--dashboard-dev` flag is set, serve from filesystem (`web/ui/dist/`) instead of embedded FS. This allows iterating on the frontend without recompiling Go.

### D2: Authentication

Dashboard access requires login. Credentials are configured via:

- **CLI flags**: `--dashboard-user` and `--dashboard-pass`
- **Config file**: `dashboard.username` and `dashboard.password`

Only one user account is supported. If no credentials are configured, the dashboard is disabled and `/fps/dashboard` returns 503 with a message indicating dashboard credentials are not configured.

**Session management**:

- POST `/fps/api/auth/login` — validates credentials, returns session cookie
- POST `/fps/api/auth/logout` — invalidates session
- GET `/fps/api/auth/status` — returns current auth state

Sessions use a cryptographically random token stored in an HttpOnly, SameSite=Strict cookie. Sessions are stored in-memory (no persistence across restarts). Session lifetime: 24 hours.

All `/fps/api/*` endpoints (except `/fps/api/auth/login` and `/fps/api/auth/status`) require a valid session. Unauthenticated requests receive 401.

### D3: WebSocket Push Architecture

A single multiplexed WebSocket connection per client handles all real-time updates. No polling — the server pushes data on its own schedule.

**Connection**: WebSocket `/fps/api/ws` (authenticated via session cookie on upgrade)

**Message format** (server → client):

```json
{"type": "heartbeat", "data": { ... }}
{"type": "stats", "data": { ... }}
{"type": "log", "data": { "timestamp": "...", "level": "INFO", "msg": "...", "attrs": {} }}
{"type": "reload_result", "data": { "success": true, "message": "..." }}
```

**Push schedule** (server-driven):

| Message type    | Interval  | Trigger                                  |
| --------------- | --------- | ---------------------------------------- |
| `heartbeat`     | 5s        | Timer                                    |
| `stats`         | 3s        | Timer                                    |
| `log`           | Immediate | New log entry written to circular buffer |
| `reload_result` | Immediate | Reload completes (success or failure)    |

**Client → server messages**:

```json
{"type": "reload"}
{"type": "set_log_level", "data": {"min_level": "INFO"}}
```

The `set_log_level` message sets a per-connection filter — the server only pushes log entries at or above the requested level. Default: INFO. This filtering happens server-side to avoid wasting bandwidth on DEBUG entries the client doesn't want.

**Reconnection**: The frontend auto-reconnects on WebSocket close/error with exponential backoff (1s, 2s, 4s, 8s, max 30s). During disconnection, the UI shows a "reconnecting..." indicator.

### D4: Stats Dashboard (Main View)

The primary dashboard view shows live proxy statistics pushed via WebSocket (D3). No client-side polling.

**Panels**:

| Panel         | WebSocket message | Content                                                                          |
| ------------- | ----------------- | -------------------------------------------------------------------------------- |
| Heartbeat     | `heartbeat`       | Version, uptime, mode, MITM status, active plugins, OS/arch, Go version, started-at |
| Connections   | `stats`           | Total and active connections                                                     |
| Traffic       | `stats`           | Total requests, blocked, bytes in/out with human-readable formatting (KB/MB/GB)  |
| Blocking      | `stats`           | Blocks total, allows total, blocklist size, allowlist size, sources              |
| Top Blocked   | `stats`           | Top 25 blocked domains                                                          |
| Top Allowed   | `stats`           | Top 25 allowed domains                                                          |
| Top Requested | `stats`           | Top 25 requested domains                                                        |
| Top Clients   | `stats`           | Top 25 clients by request count                                                 |
| MITM          | `stats`           | Enabled, intercepts total, top intercepted domains                               |
| Plugins       | `stats`           | Active count, per-plugin stats (inspected/matched/modified), top rules           |

**Rates**: The frontend computes request/sec and bytes/sec client-side by tracking deltas between consecutive `stats` messages.

### D5: About / README View

The dashboard embeds the project README so users can read about features, changelog, and configuration without leaving the UI.

**Embedding**: The build chain copies `README.md` to `web/readme.md` before `go build`. The Go binary embeds it via `//go:embed readme.md`. The API serves it as raw markdown.

**API**:

- GET `/fps/api/readme` — returns the raw README markdown text

**Frontend**:

- Rendered as styled HTML using a lightweight markdown renderer (`react-markdown`)
- Headings, code blocks, tables, and inline code styled to match the VSCode Dark theme
- Fetched once on page mount (no need for live updates — README changes only on rebuild)

### D6: Proxy Configuration View

A read-only view of the current proxy configuration, plus controls to bounce the proxy.

**Configuration display**:

- GET `/fps/api/config` — returns the resolved (merged) config as JSON (passwords redacted)
- Displayed as a formatted, syntax-highlighted YAML/JSON block

**Proxy reload**:

- Client sends `{"type": "reload"}` over the WebSocket
- Server re-reads fpsd.yml, validates, and hot-reloads all subsystems
- Server pushes `{"type": "reload_result", "data": {"success": true/false, "message": "..."}}` when complete
- After successful reload, the config view auto-refreshes via a GET `/fps/api/config` fetch

**Hot-reload scope** — what gets reloaded without restarting the process:

- Blocklist URLs and inline blocklist (re-download, rebuild in-memory cache)
- Allowlist entries
- MITM domain list and plugin configuration
- Stats flush interval
- Log buffer size (resize the circular buffer)
- Timeouts (applied to new connections)
- Verbose mode toggle

**What does NOT reload** (requires process restart):

- Listen address (TCP listener is bound at startup)
- Dashboard credentials (security: prevent locked-out state)
- Data directory path

The reload does NOT restart the TCP listener — the dashboard WebSocket connection stays alive throughout.

If reload fails (bad config, fetch error), the old config stays active and the error is returned to the dashboard.

### D7: Live Log Viewer

A real-time log tail view. Log entries are pushed via WebSocket (D3) as they happen — no polling.

**Circular buffer (Go side)**:

- New `internal/logbuf` package: a fixed-size circular buffer holding the most recent N log entries (default: 1000, hot-reloadable)
- Implements `slog.Handler` so it plugs into the existing slog handler chain as an additional sink
- Each entry stores: timestamp, level, message, and structured attributes (as JSON)
- Thread-safe (sync.Mutex around the ring)
- Notifies registered WebSocket subscribers on each new entry (fan-out via channel)

**REST fallback**:

- GET `/fps/api/logs?n=100&level=info` — returns the most recent N entries (default 100, max 1000) filtered by minimum level. Used for initial page load (backfill) and as a fallback if WebSocket is unavailable.

**Frontend**:

- On mount: fetches recent logs via GET, then switches to WebSocket stream for new entries
- Log entries displayed in a scrollable, monospace-font container
- Color-coded by level (DEBUG=gray, INFO=blue, WARN=yellow, ERROR=red)
- Auto-scroll to bottom with a "pause" toggle to freeze scrolling for inspection
- Level filter dropdown (DEBUG, INFO, WARN, ERROR) — sends `set_log_level` to server to filter at source
- Text search filter (client-side, filters visible entries in the current buffer)

### D8: Visual Theme

VSCode Dark theme aesthetic, implemented via Tailwind CSS custom theme:

| Element      | Color     | Tailwind Token      |
| ------------ | --------- | ------------------- |
| Background   | `#1e1e1e` | `bg-vsc-bg`         |
| Surface/card | `#252526` | `bg-vsc-surface`    |
| Border       | `#3c3c3c` | `border-vsc-border` |
| Text primary | `#d4d4d4` | `text-vsc-text`     |
| Text secondary | `#808080` | `text-vsc-muted`  |
| Accent/link  | `#569cd6` | `text-vsc-accent`   |
| Success      | `#4ec9b0` | `text-vsc-success`  |
| Warning      | `#dcdcaa` | `text-vsc-warning`  |
| Error        | `#f44747` | `text-vsc-error`    |
| Header/nav   | `#333333` | `bg-vsc-header`     |

Font: system monospace stack (`"Cascadia Code", "Fira Code", "JetBrains Mono", "Consolas", monospace`).

Tailwind purges unused classes at build time — only the CSS actually referenced in components ships in the bundle.

### D9: API Routing

All dashboard API endpoints live under `/fps/api/` to avoid collision with existing management endpoints:

| Endpoint               | Method    | Auth | Purpose                              |
| ---------------------- | --------- | ---- | ------------------------------------ |
| `/fps/api/auth/login`  | POST      | No   | Login                                |
| `/fps/api/auth/logout` | POST      | Yes  | Logout                               |
| `/fps/api/auth/status` | GET       | No   | Session check                        |
| `/fps/api/readme`      | GET       | Yes  | Project README (raw markdown)        |
| `/fps/api/config`      | GET       | Yes  | Resolved config (redacted)           |
| `/fps/api/logs`        | GET       | Yes  | Recent log entries (backfill/fallback) |
| `/fps/api/ws`          | WebSocket | Yes  | Multiplexed real-time stream         |

The existing `/fps/heartbeat` and `/fps/stats` endpoints remain unchanged and unauthenticated (they're used for monitoring/automation). The WebSocket stream reuses the same data sources internally.

---

## Implementation Approach

### File Structure

```
web/
  embed.go              # //go:embed ui/dist/* and readme.md
  readme.md             # Copied from project root at build time
  server.go             # HTTP server, SPA handler, dev-mode toggle
  auth.go               # Session management, login/logout handlers
  handlers.go           # REST API handlers (config, logs backfill, readme)
  websocket.go          # WebSocket hub: fan-out, per-client state, message routing
  middleware.go          # Auth middleware (HTTP + WebSocket upgrade)
web/ui/
  package.json          # React, TypeScript, Vite, Tailwind
  vite.config.ts        # Build config, base path, dev proxy
  tailwind.config.ts    # VSCode Dark theme tokens
  tsconfig.json
  src/
    main.tsx            # React mount
    App.tsx             # Router, layout, auth gate
    api.ts              # REST fetch wrapper with auth handling
    ws.ts               # WebSocket client: connect, reconnect, message dispatch
    pages/
      Stats.tsx         # D4: main stats dashboard
      About.tsx         # D5: README viewer
      Config.tsx        # D6: config view + reload
      Logs.tsx          # D7: live log viewer
      Login.tsx         # D2: login form
    components/
      Layout.tsx        # Nav bar, sidebar
      StatCard.tsx      # Reusable stat display card
      TopTable.tsx      # Reusable top-N table
      LogEntry.tsx      # Single log line
      Markdown.tsx      # Themed markdown renderer wrapper
      ReconnectBanner.tsx  # WebSocket disconnection indicator
    hooks/
      useAuth.ts        # Login/logout/status
      useSocket.ts      # Subscribe to specific WebSocket message types
    theme.css           # Tailwind @layer base overrides (scrollbar, selection)
internal/
  logbuf/
    logbuf.go           # Circular buffer slog.Handler + subscriber fan-out
    logbuf_test.go
```

### Build Integration

Update `Makefile`:

```makefile
copy-readme:
	cp README.md web/readme.md

build-ui: copy-readme
	cd web/ui && npm ci && npx vite build

build: build-ui
	go build -ldflags "$(LDFLAGS)" -o fpsd ./cmd/fpsd

# Go-only rebuild (reuses existing dist/, re-copies README)
build-go: copy-readme
	go build -ldflags "$(LDFLAGS)" -o fpsd ./cmd/fpsd
```

### Config Extension

Add to `Config`:

```go
type Dashboard struct {
    Username string `yaml:"username"`
    Password string `yaml:"password"`
}
```

Add CLI flags: `--dashboard-user`, `--dashboard-pass`, `--dashboard-dev`.

### Embedding

```go
// web/embed.go
package web

import "embed"

//go:embed ui/dist/*
var StaticFS embed.FS

//go:embed readme.md
var ReadmeContent string
```

### SPA Routing

The Go server handles `/fps/dashboard*` routes:

1. Try to serve as a static file from `ui/dist/` (JS, CSS, images)
2. If not found, serve `ui/dist/index.html` (SPA client-side routing)

Vite's `base` config is set to `/fps/dashboard/` so asset paths are correct.

### WebSocket Hub

The hub maintains a set of connected clients. Each client has:

- A send channel (buffered, dropped on overflow to prevent slow clients from blocking)
- A per-connection log level filter
- A goroutine pair (read pump + write pump) per connection

Data sources push to the hub:

- Stats ticker (3s) → marshals stats JSON, broadcasts to all clients
- Heartbeat ticker (5s) → marshals heartbeat JSON, broadcasts
- Log buffer subscriber → fans out new entries to clients whose level filter matches

### Graceful Reload

The reload mechanism requires the proxy server to expose a `Reload() error` method:

1. Re-reads `fpsd.yml` from disk
2. Validates the new config
3. Rebuilds blocklist (if URLs changed)
4. Rebuilds MITM interceptor (if domains changed)
5. Reinitializes plugins (if plugin config changed)
6. Updates stats flush interval, log buffer size, timeouts, verbose mode
7. Does NOT restart the TCP listener (avoids dropping connections)

If reload fails, old config stays active and the error is pushed to the requesting client.

---

## Dependencies

### Go

- `nhooyr.io/websocket` — WebSocket support (single dependency, maintained, supports context cancellation)

### Frontend (npm)

- `react` + `react-dom` (18.x)
- `react-router-dom` (6.x) — client-side routing
- `react-markdown` — markdown rendering for README view
- `typescript`
- `vite` — build tool
- `tailwindcss` + `@tailwindcss/vite` — utility CSS (compiled at build, tree-shaken)

No additional UI component libraries. Tailwind utility classes keep the bundle small.

---

## Acceptance Criteria

- [ ] Dashboard served at `/fps/dashboard` from embedded assets in the fpsd binary
- [ ] `make build-ui` compiles React app; `make build` embeds it into Go binary
- [ ] Dev mode (`--dashboard-dev`) serves from filesystem for frontend iteration
- [ ] Login/password authentication via `--dashboard-user`/`--dashboard-pass` flags or config
- [ ] Dashboard disabled with 503 when no credentials configured
- [ ] Session cookie: HttpOnly, SameSite=Strict, 24h expiry, crypto-random token
- [ ] Single multiplexed WebSocket streams stats (3s), heartbeat (5s), and logs (immediate)
- [ ] WebSocket auto-reconnects with exponential backoff; UI shows reconnection indicator
- [ ] Stats panels update from WebSocket push — no polling
- [ ] Heartbeat panel shows version, uptime, mode, MITM status, plugins, OS info
- [ ] Traffic panel shows live counters with human-readable byte formatting and computed rates
- [ ] Top-N tables (n=25) for blocked, allowed, requested domains, and clients
- [ ] About page renders embedded README as styled markdown matching VSCode Dark theme
- [ ] Config view shows resolved config (passwords redacted)
- [ ] Reload via WebSocket hot-reloads blocklist, allowlist, MITM, plugins, stats interval, log buffer size, timeouts, verbose mode
- [ ] Reload does not restart TCP listener or drop WebSocket connections
- [ ] Circular log buffer (1000 entries, hot-reloadable size) as additional slog handler
- [ ] Log viewer backfills via REST on mount, then streams via WebSocket
- [ ] Server-side log level filtering per WebSocket connection
- [ ] Client-side text search on visible log entries
- [ ] VSCode Dark theme via Tailwind custom tokens, monospace font stack
- [ ] Tailwind tree-shaking: only used classes in production bundle
- [ ] Existing `/fps/heartbeat` and `/fps/stats` endpoints unchanged and unauthenticated
- [ ] All `/fps/api/*` endpoints require authentication (except login/status)
- [ ] Unit tests for: auth session management, log buffer, WebSocket hub, config redaction
- [ ] Frontend builds with zero warnings
- [ ] Binary size increase documented (expected: 1-3 MB for embedded frontend)
- [ ] 0 lint issues, all existing tests pass

---

## Non-Goals

- Multi-user accounts or role-based access control — single user is sufficient
- HTTPS for the dashboard — the proxy already runs behind a trusted network boundary
- Persistent sessions across restarts — in-memory is fine for a local tool
- Editing individual config fields via the dashboard — config file is the source of truth; reload picks up changes
- Mobile-responsive layout — desktop-only is acceptable
- Internationalization — English only
- Restarting the TCP listener or changing listen address via reload — requires process restart
