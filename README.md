# face-puncher-supreme

Content-aware HTTPS interception proxy for ad blocking. Operates at the HTTP layer to filter ads that DNS-based blockers cannot reach — specifically ads served from the same domain as content (e.g., Reddit promoted posts from `www.reddit.com`).

**How it works**: fpsd sits between clients and the internet as an HTTP/HTTPS proxy. It combines three layers of filtering:

1. **Domain blocking** — 376K+ domains from Pi-hole-compatible blocklists, checked on every request
2. **MITM TLS interception** — decrypts HTTPS for configured domains to inspect response bodies
3. **Content filter plugins** — site-specific rules that strip ad elements from HTML/JSON responses

Runs as a single Go binary with an embedded React dashboard. Supports both explicit proxy configuration and transparent mode (iptables redirect, no client config needed). Deployed as a systemd user service or Arch Linux package.

**Tested on**: Chromium, Safari (macOS), iOS/iPadOS (transparent mode), Windows (transparent mode).

## Table of Contents

- [Build](#build)
- [Configuration](#configuration)
- [Run](#run)
- [CLI Flags](#cli-flags)
- [Domain Blocking](#domain-blocking)
- [Allowlist and Inline Blocklist](#allowlist-and-inline-blocklist)
- [MITM TLS Interception](#mitm-tls-interception)
- [Content Filter Plugins](#content-filter-plugins)
- [Web Dashboard](#web-dashboard)
- [Transparent Proxying](#transparent-proxying)
- [Management Endpoints](#management-endpoints)
- [Logging](#logging)
- [Install / Uninstall](#install--uninstall)
- [Arch Linux Package](#arch-linux-package)
- [Test](#test)
- [Lint](#lint)
- [Project Structure](#project-structure)
- [License](#license)
- [Changelog](#changelog)

## Build

```bash
make build
```

Produces the `fpsd` binary in the project root with version info baked in via ldflags.

## Configuration

The proxy reads configuration from a YAML file. It searches for `fpsd.yml` (or `fpsd.yaml`) in the working directory, or you can specify a path with `--config`/`-c`.

A reference config file (`fpsd.yml`) is included in the repo with all defaults and 5 Pi-hole compatible blocklist URLs.

```bash
# Use the included config (auto-discovered in working directory)
./fpsd

# Use a specific config file
./fpsd -c /etc/fpsd/fpsd.yml

# Inspect the resolved configuration
fpsd config dump

# Validate a config file
fpsd config validate -c /path/to/fpsd.yml
```

CLI flags override config file values. If no config file exists, the proxy starts with built-in defaults (same as before).

## Run

```bash
# With config file (auto-discovered fpsd.yml has blocklist URLs pre-configured)
./fpsd

# Override listen address via CLI flag
./fpsd --addr 0.0.0.0:9090

# Without config file, pass blocklist URLs as flags
./fpsd \
  --blocklist-url https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts \
  --blocklist-url https://cdn.jsdelivr.net/gh/hagezi/dns-blocklists@latest/adblock/pro.txt
```

Configure your browser or system to use `http://<host>:<port>` as an HTTP/HTTPS proxy. For Chromium:

```bash
chromium --proxy-server="http://127.0.0.1:18737"
```

## CLI Flags

| Flag | Short | Default | Description |
| ---- | ----- | ------- | ----------- |
| `--config` | `-c` | (auto-discover) | Config file path (`fpsd.yml` / `fpsd.yaml` in CWD) |
| `--addr` | `-a` | `:18737` | Listen address (host:port) |
| `--log-dir` | | `logs` | Directory for log files (empty to disable) |
| `--verbose` | `-v` | `false` | Enable DEBUG-level logging with full request/response detail |
| `--blocklist-url` | | | Blocklist URL (repeatable, same format as Pi-hole adlists) |
| `--data-dir` | | `.` | Directory for `blocklist.db` |
| `--dashboard-user` | | | Dashboard login username (enables dashboard) |
| `--dashboard-pass` | | | Dashboard login password (enables dashboard) |
| `--dashboard-dev` | | `false` | Serve dashboard from filesystem (development mode) |

CLI flags override config file values when both are specified.

Subcommands:

- `fpsd version` — Print version string and exit
- `fpsd update-blocklist` — Re-download all blocklist URLs, rebuild the database, and exit
- `fpsd generate-ca` — Generate CA certificate and private key for MITM (`--force` to overwrite)
- `fpsd config dump` — Print the resolved configuration as YAML
- `fpsd config validate` — Validate configuration and exit with 0 (ok) or 1 (error)

## Domain Blocking

The proxy blocks requests to domains on known ad/tracking blocklists. This complements DNS-based blocking (Pi-hole) at the proxy layer.

```bash
# First run: downloads lists and builds blocklist.db
./fpsd --blocklist-url https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts

# Subsequent runs: loads from existing blocklist.db (no re-download)
./fpsd --blocklist-url https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts

# Update lists without starting the proxy
fpsd update-blocklist \
  --blocklist-url https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts
```

Supported list formats: hosts (`0.0.0.0 domain`), adblock (`||domain^`), and domain-only. Matching is exact and case-insensitive. Blocked requests receive `403 Forbidden`.

With no blocklist URLs (neither in config file nor via `--blocklist-url` flags), the proxy runs in passthrough mode (no blocking).

## Allowlist and Inline Blocklist

Beyond URL-sourced blocklists, the config file supports two additional mechanisms for tuning:

**Inline blocklist** — block individual domains without downloading a full list:

```yaml
blocklist:
  - news.iadsdk.apple.com
  - news-events.apple.com
  - news-app-events.apple.com
```

**Allowlist** — domains that are never blocked, even if they appear in blocklists. Supports exact match and suffix patterns (`*.example.com` matches the base domain and all subdomains):

```yaml
allowlist:
  - registry.api.cnn.io
  - cdn.optimizely.com
  - "*.cnn.io"
```

Allowlist takes priority over all block sources (URL-sourced and inline). Inline blocklist entries are merged into the in-memory cache at startup and are not stored in `blocklist.db` — they survive `fpsd update-blocklist` since they come from config.

## MITM TLS Interception

For sites that serve ads from the same domain as content (e.g., Reddit promoted posts from `www.reddit.com`), domain blocking is insufficient. MITM TLS interception lets the proxy inspect HTTP traffic for configured domains.

**Setup**:

```bash
# 1. Generate CA certificate and private key
fpsd generate-ca

# 2. Install CA in Chromium's trust store (Linux)
curl -o fps-ca.pem http://localhost:18737/fps/ca.pem
mkdir -p ~/.pki/nssdb
certutil -d sql:$HOME/.pki/nssdb -A -t "C,," -n "Face Puncher Supreme CA" -i fps-ca.pem

# 3. Configure domains in fpsd.yml
```

```yaml
mitm:
  domains:
    - www.reddit.com
    - old.reddit.com
```

Only explicitly listed domains are intercepted. All other HTTPS traffic remains in opaque tunnels. The blocklist check still happens first — blocked domains get 403 regardless of MITM config.

MITM is HTTP/1.1 only. The proxy generates short-lived leaf certificates (24h) per domain, signed by the CA, cached in memory.

**Subcommands**:

- `fpsd generate-ca` — Create CA cert and key (refuses to overwrite; use `--force` to regenerate)

## Content Filter Plugins

Plugins are site-specific content filters that inspect and modify MITM'd HTTP responses. Each plugin targets a set of domains and operates in one of two modes:

- **intercept** — captures request/response pairs to disk for offline analysis (development tool)
- **filter** — applies content filtering rules, replacing matched ad content with placeholder markers

```yaml
plugins:
  reddit-promotions:
    enabled: true
    mode: "filter"           # "intercept" or "filter"
    placeholder: "visible"   # "visible", "comment", or "none"
    domains:
      - www.reddit.com
    options:
      log_matches: true
```

Plugin domains must be a subset of `mitm.domains`. Placeholder markers indicate what was filtered: `visible` shows a styled HTML element, `comment` inserts an HTML comment, `none` removes content silently.

Plugins are compiled statically into the binary. Adding a new plugin means registering its constructor in `internal/plugin/registry.go` and rebuilding. Plugin stats appear in `/fps/stats` and `/fps/heartbeat`.

## Web Dashboard

A built-in web dashboard for real-time proxy monitoring and management. Served at `/fps/dashboard` from assets embedded in the binary — no external files needed.

**Enable the dashboard** by setting credentials via CLI flags or config file:

```bash
# Via CLI flags
./fpsd --dashboard-user admin --dashboard-pass secret

# Via config file (fpsd.yml)
```

```yaml
dashboard:
  username: admin
  password: changeme
```

Without credentials, the dashboard is disabled and `/fps/dashboard` returns 503.

**Features**:

- **Stats dashboard**: live connections, traffic, blocking, MITM, and plugin stats updated via WebSocket (no polling)
- **Top-N tables**: top 25 blocked, allowed, requested domains, and clients
- **Live log viewer**: real-time log tail with level filtering and text search, backed by a 1000-entry circular buffer
- **Config view**: resolved proxy configuration (passwords redacted) with hot-reload button
- **About page**: embedded README rendered as styled markdown
- **VSCode Dark theme**: monospace font stack, dark color scheme

**Real-time updates** use a single multiplexed WebSocket connection per client:

| Data | Push interval |
| ---- | ------------- |
| Stats | 3 seconds |
| Heartbeat | 5 seconds |
| Log entries | Immediate |

The frontend auto-reconnects on WebSocket disconnect with exponential backoff (1s to 30s).

**Build chain**:

```bash
make build-ui    # Compile React app (npm ci + vite build)
make build       # Full build: UI + Go binary with embedded assets
make build-go    # Go-only rebuild (reuses existing UI dist, re-copies README)
```

The React frontend is built with Vite + TypeScript + Tailwind CSS v4. Production bundle is ~97 KB gzipped.

## Transparent Proxying

Transparent proxying accepts connections redirected by iptables without any client-side proxy configuration. Devices on the LAN send traffic to their default gateway; iptables REDIRECT rules send port 80/443 to fpsd's transparent listeners.

**Enable in config** (`fpsd.yml`):

```yaml
transparent:
  enabled: true
  http_addr: ":18780"    # Receives redirected port 80 traffic
  https_addr: ":18443"   # Receives redirected port 443 traffic
```

**How it works**:

- **HTTP** (port 80): fpsd reads the HTTP request, extracts the `Host` header (or falls back to `SO_ORIGINAL_DST`), checks the blocklist, and forwards to the upstream server.
- **HTTPS** (port 443): fpsd peeks at the TLS ClientHello to extract the SNI server name. Blocked domains get a TCP close. MITM-configured domains are intercepted. All other traffic is tunneled with the ClientHello replayed to upstream.

**iptables rules** (applied by `fps-ctl install --transparent`):

```bash
# Redirect LAN traffic to fpsd transparent ports
iptables -t nat -A PREROUTING -i eth0 -p tcp --dport 80  -j REDIRECT --to-port 18780
iptables -t nat -A PREROUTING -i eth0 -p tcp --dport 443 -j REDIRECT --to-port 18443

# Loop prevention — skip fpsd's own outbound traffic
iptables -t nat -A OUTPUT -m owner --uid-owner $(id -u) -p tcp --dport 80  -j RETURN
iptables -t nat -A OUTPUT -m owner --uid-owner $(id -u) -p tcp --dport 443 -j RETURN

# Enable IP forwarding
sysctl -w net.ipv4.ip_forward=1
```

Replace `eth0` with your LAN interface. The `fps-ctl` installer handles all of this automatically.

## Management Endpoints

### `/fps/heartbeat` — Health Check

Lightweight health check for monitoring. No database queries or sorting.

```bash
curl -s http://localhost:18737/fps/heartbeat | python3 -m json.tool
```

Returns JSON with status, version, mode, MITM status, uptime, OS info, and startup timestamp.

### `/fps/stats` — Full Statistics

Detailed traffic, blocking, domain, and client statistics.

```bash
# All stats, top 10, all time
curl -s http://localhost:18737/fps/stats | python3 -m json.tool

# Top 5 over the last 24 hours
curl -s 'http://localhost:18737/fps/stats?n=5&period=24h' | python3 -m json.tool
```

Returns connections, blocking stats (with top blocked and top allowed domains), MITM interception stats (total intercepts, top intercepted domains), top requested domains, top clients by request count, and aggregate traffic totals.

Query parameters: `n` (top-N size, default 10), `period` (`1h`, `24h`, `7d`, or omit for all time).

Stats are persisted to `stats.db` via periodic flush (default 60s) and survive restarts. Disable with `stats.enabled: false` in config (returns 501).

### `/fps/ca.pem` — CA Certificate Download

Download the MITM CA certificate for client installation. Returns 404 when MITM is not configured.

```bash
curl -O http://<proxy-host>:18737/fps/ca.pem
```

Or open the URL in a browser on the client device.

#### macOS

```bash
sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain ca.pem
```

Or: double-click `ca.pem` → Keychain Access opens → select **System** keychain → enter password → double-click the certificate → expand **Trust** → set **When using this certificate** to **Always Trust**.

#### iOS / iPadOS

1. In Safari, navigate to `http://<proxy-host>:18737/fps/ca.pem`
2. Tap **Allow** when prompted to download the profile
3. Go to **Settings → General → VPN & Device Management** → tap the downloaded profile → **Install**
4. Go to **Settings → General → About → Certificate Trust Settings** → enable full trust for **Face Puncher Supreme CA**

#### Windows

**GUI**: Download `ca.pem` from the browser → double-click → **Install Certificate** → **Local Machine** → **Place all certificates in the following store** → browse to **Trusted Root Certification Authorities** → Finish → accept the security warning.

**PowerShell (Admin)**:

```powershell
Invoke-WebRequest -Uri http://<proxy-host>:18737/fps/ca.pem -OutFile $env:TEMP\fps-ca.pem
Import-Certificate -FilePath $env:TEMP\fps-ca.pem -CertStoreLocation Cert:\LocalMachine\Root
```

#### Linux (Debian/Ubuntu)

```bash
sudo cp ca.pem /usr/local/share/ca-certificates/fps-ca.crt
sudo update-ca-certificates
```

#### Linux (Arch/Fedora)

```bash
sudo trust anchor --store ca.pem
```

## Logging

Logs are written to both stderr (text format) and a rotated JSON log file:

- **File**: `<log-dir>/fpsd.log`
- **Rotation**: 10MB per file, 3 backups, 7-day retention, gzip compressed
- **Verbose mode** (`--verbose`): Logs full request/response headers, User-Agent, body sizes, and byte counts for CONNECT tunnels

## Install / Uninstall

`fps-ctl` installs fpsd as a systemd user service. The proxy runs as the current user with no root privileges. Transparent proxy iptables rules are optional and require sudo.

```bash
# Build and install
make install

# Or step by step:
make build
./scripts/fps-ctl install

# With transparent proxy on a specific interface
./scripts/fps-ctl install --transparent --interface eth0

# Multiple interfaces (e.g. LAN + VM bridge)
./scripts/fps-ctl install --transparent --interface eth0,virbr0

# Check status
./scripts/fps-ctl status

# Uninstall (keeps config and data by default)
./scripts/fps-ctl uninstall

# Uninstall everything including config and data
./scripts/fps-ctl uninstall --purge
```

**Installed layout** (XDG Base Directory):

| Path | Purpose |
| ---- | ------- |
| `~/.local/bin/fpsd` | Binary |
| `~/.config/fpsd/fpsd.yml` | Configuration |
| `~/.local/share/fpsd/` | Data (blocklist.db, stats.db, CA certs) |
| `~/.local/share/fpsd/logs/` | Log files |
| `~/.config/systemd/user/fpsd.service` | Systemd user service |
| `/etc/systemd/system/fpsd-tproxy.service` | iptables rules (if transparent proxy enabled) |

**Upgrade**: Running `fps-ctl install` on an existing installation updates the binary and service unit without overwriting the config file.

**Service management**:

```bash
systemctl --user status fpsd         # Check service
systemctl --user restart fpsd        # Restart after config change
journalctl --user -u fpsd -f         # Follow logs
sudo systemctl stop fpsd-tproxy      # Remove iptables rules temporarily
sudo systemctl start fpsd-tproxy     # Re-apply iptables rules
```

## Arch Linux Package

An AUR package (`fpsd-git`) is available for Arch Linux and derivatives (CachyOS, EndeavourOS, etc.).

```bash
# Install from AUR (using an AUR helper)
yay -S fpsd-git

# Or build manually
git clone https://aur.archlinux.org/fpsd-git.git
cd fpsd-git
makepkg -si

# First-time setup after install
mkdir -p ~/.config/fpsd ~/.local/share/fpsd/logs
cp /usr/share/doc/fpsd-git/fpsd.yml.example ~/.config/fpsd/fpsd.yml
# Edit ~/.config/fpsd/fpsd.yml — set data_dir to ~/.local/share/fpsd

# Enable the service
systemctl --user enable --now fpsd

# Transparent proxy (still uses fps-ctl)
sudo fps-ctl install --transparent --interface eth0
```

**Package layout**:

| Path | Purpose |
| ---- | ------- |
| `/usr/bin/fpsd` | Binary |
| `/usr/bin/fps-ctl` | Transparent proxy management |
| `/usr/lib/systemd/user/fpsd.service` | Systemd user service |
| `/usr/share/doc/fpsd-git/fpsd.yml.example` | Reference config |
| `~/.config/fpsd/fpsd.yml` | User config (copy from example) |
| `~/.local/share/fpsd/` | User data (databases, CA certs, logs) |

`fps-ctl` detects the package-managed binary and skips binary/service management, only handling iptables rules for transparent proxying.

## Test

```bash
# Unit tests only (fast)
make test

# Include integration tests against real sites
go test -race -v ./...
```

Integration tests are skipped with `-short` (which `make test` uses via `-race -v`). To run them, omit the `-short` flag or run directly with `go test -race -v ./internal/proxy/`.

## Lint

```bash
make lint
```

Uses golangci-lint v2 with a versioned binary (auto-installed on first run). Configuration is in `.golangci.yml`. Enabled linters: errcheck, gocognit, gocritic, gocyclo, govet, lll, unparam, unused, cyclop, gosec.

## Project Structure

```
cmd/fpsd/              Daemon entrypoint (Cobra CLI)
internal/config/       YAML config loading, validation, CLI merge
internal/proxy/        Proxy server (HTTP forward, HTTPS CONNECT tunnel, domain blocking)
internal/transparent/  Transparent proxy (iptables REDIRECT, SNI extraction, SO_ORIGINAL_DST)
internal/blocklist/    Domain blocklist (SQLite DB, parser, fetcher, in-memory cache)
internal/mitm/         Per-domain TLS interception (CA, leaf certs, HTTP proxy loop)
internal/plugin/       Content filter plugin architecture (registry, interception, markers)
internal/probe/        Management endpoints (heartbeat + stats)
internal/stats/        In-memory counters and SQLite stats persistence
internal/logging/      Structured logging with file rotation
internal/logbuf/       Circular buffer slog.Handler for dashboard log viewer
internal/version/      Build-time version info
web/                   Dashboard HTTP server, auth, WebSocket hub, SPA handler
web/ui/                React frontend (Vite + TypeScript + Tailwind CSS)
scripts/               Installer/uninstaller (fps-ctl)
specs/                 Project specifications
agents/                Cross-system testing guides
fpsd.yml               Reference configuration with defaults and blocklist URLs
```

## License

MIT License — (c)2026 ushineko — [github.com/ushineko/face-puncher-supreme](https://github.com/ushineko/face-puncher-supreme)

See [LICENSE](LICENSE) for the full text.

## Changelog

### v1.3.2 — 2026-02-17

- ci: split workflows into ci.yml (branch/PR) and release.yml (tags) to fix GitHub Actions deduplication

### v1.3.1 — 2026-02-17

- ci: automated AUR publishing on version tags (spec 014)

### v1.3.0 — 2026-02-17

- fix: anchor links in README (About page) now scroll to target headings instead of opening blank tabs
- feat: draggable dashboard sections — reorder stat cards and top-N tables via drag-and-drop, layout persisted to localStorage
- feat: inline charts — traffic req/sec line graph (rolling 3-minute window) and top-N pie charts for blocked domains, requested domains, and clients
- charts toggle per-section with visibility persisted alongside layout; default hidden to preserve existing text-only view

### v1.2.3 — 2026-02-17

- docs: README intro rewritten to describe the three-layer filtering architecture, deployment modes, and tested platforms

### v1.2.2 — 2026-02-17

- fix: dashboard styles missing — add explicit `@source` directive for Tailwind v4.1.18 content detection

### v1.2.1 — 2026-02-17

- fps-ctl: automatic migration from fps-ctl to package-managed service — cleans up stale unit file and binary that shadow the package install
- fps-ctl status: warns when fps-ctl unit is shadowing a package-installed unit, with fix instructions
- CI: build UI assets before running tests (fixes `go:embed` missing `web/ui/dist` in clean CI environment)

### v1.2.0 — 2026-02-17

- GitHub Actions CI: builds Arch Linux package on push/PR to main (spec 012)
- `PKGBUILD` for `fpsd-git` AUR package (x86_64, pure Go binary, ~5 MB compressed)
- `fpsd.install` pacman hooks: first-time setup instructions, service restart on upgrade, pre-remove stop/disable
- `pkgver()` reads VERSION from Makefile + git metadata for Arch-compatible version strings
- Systemd user service unit: `/usr/bin/fpsd` with hardening (NoNewPrivileges, ProtectSystem, PrivateTmp)
- Release job: creates GitHub Release with `.pkg.tar.zst` attached on `v*` tag push
- `scripts/update_aur.sh`: manual AUR publish script (clone, copy, generate .SRCINFO, confirm, push)
- fps-ctl package awareness: detects pacman-managed binary at `/usr/bin/fpsd`, skips binary/service management
- fps-ctl status shows package name and version when package-managed
- Reference config shipped as `/usr/share/doc/fpsd-git/fpsd.yml.example` (not /etc, for credential safety)
- Full dogfood lifecycle verified: fps-ctl uninstall, makepkg, pacman -U, heartbeat OK, pacman -R, fps-ctl restore
- 128 tests passing, 0 lint issues

### v1.1.0 — 2026-02-17

- Transparent proxying: accepts iptables-redirected HTTP/HTTPS traffic without client proxy config (spec 010)
- Dual transparent listeners: HTTP (:18780) and HTTPS (:18443), independent of the explicit proxy
- TLS ClientHello SNI extraction for HTTPS destination routing without decryption
- `SO_ORIGINAL_DST` fallback for connections without SNI (Linux-specific, build-tagged)
- `prefixConn` wrapper replays peeked ClientHello bytes to MITM handler or upstream
- Transparent stats: `transparent_http`, `transparent_tls`, `transparent_mitm`, `transparent_block`, `sni_missing` counters
- Heartbeat shows `transparent_enabled`, `transparent_http`, `transparent_https` fields
- Config validation: port conflict detection, address format checks, http/https addr uniqueness
- `fps-ctl` installer/uninstaller script for systemd user service deployment (spec 011)
- `fps-ctl install`: creates XDG directory layout, copies binary/config/CA certs, writes systemd unit, enables linger
- `fps-ctl install --transparent --interface IF[,IF2,...]`: writes system service for iptables rules with loop prevention (comma-separated for multiple interfaces)
- `fps-ctl uninstall`: stops services, removes iptables rules, cleans up files (`--purge` for full removal)
- `fps-ctl status`: shows binary, service, transparent proxy, and connectivity status
- Systemd hardening: `ProtectHome=tmpfs`, `ProtectSystem=strict`, `NoNewPrivileges`, bind-mounted config/data
- Idempotent install: upgrade path preserves config, updates binary and service unit
- `make install` and `make uninstall` Makefile targets
- Multi-interface support: `--interface eno2,virbr0` applies iptables rules to multiple interfaces (LAN + VM bridges)
- CA certificate installation docs for macOS, iOS/iPadOS, Windows, and Linux
- 12 new transparent proxy tests (SNI parsing, prefixConn, real TLS ClientHello validation)
- 128 total tests (all passing), 0 lint issues
- Verified on: iPhone 17 Pro Max (iOS 26.2.1), iPad Pro 13" M5 (iPadOS 26.2), Windows 11 Pro (Vivaldi, transparent mode), macOS 26.3 (Safari)

### v1.1.2 — 2026-02-17

- MIT license and copyright notice added to project
- Copyright footer in dashboard layout

### v1.1.1 — 2026-02-17

- Fix: dashboard About page tables not rendering (added remark-gfm plugin for GFM table support)

### v1.0.0 — 2026-02-16

- Web dashboard: real-time proxy monitoring and management at `/fps/dashboard` (spec 009)
- React SPA embedded in Go binary via `go:embed` — single-binary deployment preserved
- Vite + TypeScript + Tailwind CSS v4 frontend with VSCode Dark theme
- Session-based authentication: HttpOnly + SameSite=Strict cookies, 24h expiry, crypto-random tokens
- Single multiplexed WebSocket per client: stats (3s), heartbeat (5s), logs (immediate)
- Auto-reconnect with exponential backoff (1s to 30s) and UI reconnection indicator
- Stats dashboard: connections, traffic, blocking, MITM, plugins, top-25 tables
- Client-side rate computation (req/sec, bytes/sec) from consecutive stats deltas
- Live log viewer with server-side level filtering and client-side text search
- Circular log buffer (`internal/logbuf`): 1000-entry slog.Handler with subscriber fan-out
- REST log backfill on page load, then WebSocket stream for new entries
- About page: embedded README rendered as styled markdown via react-markdown
- Config view: resolved config (passwords redacted) with hot-reload button
- Hot-reload via WebSocket: re-reads fpsd.yml, updates allowlist, blocklist, verbose mode
- Dashboard disabled with 503 when no credentials configured
- `--dashboard-user`, `--dashboard-pass`, `--dashboard-dev` CLI flags
- `dashboard.username` and `dashboard.password` config file fields
- Dev mode (`--dashboard-dev`): serves from filesystem for frontend iteration
- Build chain: `make build-ui` (npm ci + vite build), `make build` (UI + Go), `make build-go` (Go only)
- Runtime log level changes via `slog.LevelVar` (verbose toggle on reload)
- Extracted `probe.BuildHeartbeat()` and `probe.BuildStats()` for WebSocket hub reuse
- Production bundle: ~97 KB gzipped, ~1.5 MB increase to binary size
- 0 lint issues, all existing tests passing

### v0.9.0 — 2026-02-17

- Reddit promotions filter: strips promoted/sponsored content from Reddit's Shreddit UI (spec 008)
- Feed ads (`<shreddit-ad-post>`), comment-tree ads, comment-page ads, and right-rail promoted posts removed
- Byte-level HTML element removal using Reddit's unique custom element names (no full HTML parser needed)
- URL-scoped processing: homepage, subreddit listings, feed, comment, right-rail, and scroll-load partials
- Quick-skip optimization: responses without ad marker strings bypass element scanning
- Placeholder insertion per configured mode (`visible`, `comment`, `none`)
- FilterResult reporting: accurate match/modify/rule/count data for stats integration
- MITM handler strips `Accept-Encoding` when ResponseModifier is active (ensures uncompressed responses for filtering)
- Visible placeholder CSS: nowrap with ellipsis for narrow containers (right-rail)
- 6 HTML test fixtures extracted from real interception captures for regression testing
- 25 new tests for filter rules, URL scoping, quick-skip, element removal, and fixture integrity
- 167 total tests (all passing), live-verified across multiple subreddits

### v0.8.0 — 2026-02-16

- Content filter plugin architecture for site-specific MITM response modification (spec 007)
- `ContentFilter` interface: `Name()`, `Version()`, `Domains()`, `Init()`, `Filter()`
- Plugin registry with compile-time constructors; adding a plugin is one line of code
- Interception mode: captures MITM traffic to disk (`intercepts/<plugin>/`) for offline analysis
- Placeholder markers: `visible` (styled HTML), `comment` (HTML comment), `none` (clean removal)
- Shared `Marker()` helper generates format-appropriate placeholders for HTML and JSON
- Response buffering in MITM proxyLoop for text Content-Types (text/*, application/json, etc.)
- Binary responses (images, video, fonts) bypass buffering; 10MB body size cap
- Modified responses update `Content-Length` header automatically
- Plugin config section in `fpsd.yml`: `enabled`, `mode`, `placeholder`, `domains`, `options`
- Config validation: registry existence, mode/placeholder values, domain subset of `mitm.domains`
- Plugin stats in `/fps/stats`: per-plugin inspected/matched/modified counts, top rules
- Heartbeat shows `plugins_active` count and plugin `name@version` list
- Reddit promotions stub plugin registered (interception mode for traffic analysis)
- 26 new tests: marker generation, content-type filtering, registry dispatch, interception capture
- 142 total tests (all passing)

### v0.7.0 — 2026-02-16

- Per-domain MITM TLS interception for content-level ad blocking (spec 006)
- `fpsd generate-ca` subcommand creates CA certificate and private key (ECDSA P-256, 10-year validity)
- `mitm.domains` config: explicit list of domains to intercept (exact match, case-insensitive)
- Dynamic leaf certificate generation per domain (24h validity, in-memory cache)
- HTTP/1.1 proxy loop through intercepted TLS connections with hop-by-hop header stripping
- `ResponseModifier` hook for future content filtering (nil/passthrough by default)
- `/fps/ca.pem` endpoint serves CA certificate for client installation
- MITM stats in `/fps/stats`: `intercepts_total`, `domains_configured`, `top_intercepted`
- Heartbeat shows `mitm_enabled` and `mitm_domains` fields
- Startup warnings: domain in both MITM and blocklist, CA expiry within 30 days
- Config validation for MITM domain entries (no wildcards, paths, or spaces)
- 16 new tests: CA generation, leaf cert caching, end-to-end MITM proxy loop
- 116 total tests (all passing)

### v0.6.0 — 2026-02-16

- Allowlist: domains that are never blocked, with exact match and suffix pattern (`*.example.com`) support
- Inline blocklist: block individual domains via config without downloading full lists
- Allowlist takes priority over all block sources (URL-sourced and inline)
- Allow counters: `allows_total`, `allowlist_size`, `top_allowed` in `/fps/stats`
- Allow stats persisted to `stats.db` alongside block stats (delta-based flush)
- Config validation for `blocklist` (no wildcards) and `allowlist` (exact or `*.domain`) entries
- Startup log shows allowlist size and inline blocklist count
- 13 new tests for allowlist matching, inline blocklist, and allow counters
- 90 total tests (all passing)

### v0.5.0 — 2026-02-16

- Database-backed statistics with SQLite persistence (`stats.db`)
- `/fps/heartbeat` lightweight health check (replaces `/fps/probe`)
- `/fps/stats` full statistics endpoint with connections, blocking, domains, clients, and traffic data
- Top-N query support (`?n=25`) and time-bounded queries (`?period=24h`)
- In-memory counters with delta-based periodic flush (default 60s, configurable)
- Per-client byte tracking for HTTP and CONNECT requests
- Final flush on graceful shutdown (no data loss)
- Stats survive proxy restarts (merge DB + in-memory unflushed data)
- `stats.enabled: false` disables collection; `/fps/stats` returns 501
- Config: `stats.enabled` and `stats.flush_interval` settings
- 16 new unit tests for stats collector, DB operations, flush logic, and merged queries
- 77 total tests (all passing)

### v0.4.0 — 2026-02-16

- YAML configuration file (`fpsd.yml`) with auto-discovery and `--config`/`-c` flag
- Reference config checked into repo with all 5 Pi-hole blocklist URLs
- CLI flags override config file values; unset fields use built-in defaults
- Configurable timeouts: shutdown, upstream connect, request header read
- Configurable management endpoint path prefix
- `fpsd config dump` and `fpsd config validate` subcommands
- `update-blocklist` reads blocklist URLs from config file (no more repeating `--blocklist-url` flags)
- Custom `Duration` YAML type for human-readable timeout strings (`"5s"`, `"1m"`)
- 20 new unit tests for config loading, merging, validation, and Duration type
- 60 total tests (40 unit + 5 integration, all passing)

### v0.3.0 — 2026-02-16

- Domain-based ad blocking via `--blocklist-url` flags (Pi-hole compatible)
- SQLite-backed blocklist with in-memory cache for O(1) domain lookup
- Hosts format (`0.0.0.0 domain`), adblock format (`||domain^`), and domain-only parsing
- `fpsd update-blocklist` subcommand to re-download and rebuild the database
- Block statistics in `/fps/probe`: `blocks_total`, `blocklist_size`, `blocklist_sources`, `top_blocked`
- Probe mode switches between `"passthrough"` and `"blocking"` based on loaded domains
- 40 unit tests + 5 integration tests

### v0.2.0 — 2026-02-16

- golangci-lint v2 integration with versioned binary and auto-install via `make lint`
- Lint fixes: error handling, Slowloris protection (`ReadHeaderTimeout`), tighter directory permissions
- Spec 002 draft: domain-based ad blocking with SQLite blocklist
- Quality gates documented in project config

### v0.1.0 — 2026-02-16

- HTTP forward proxy with hop-by-hop header stripping
- HTTPS CONNECT tunnel with bidirectional streaming
- `/fps/probe` liveness endpoint with connection counters and uptime
- Structured logging: stderr (text) + rotated JSON file via lumberjack
- Verbose mode for debugging real browser traffic (headers, sizes, timing)
- Cobra CLI with `--addr`, `--log-dir`, `--verbose` flags and `version` subcommand
- Build-time version injection via ldflags
- 15 unit tests + 13 integration tests (real sites via httpbin, Wikipedia, BBC, CNN)
- Verified working with Chromium browser traffic in passthrough mode
