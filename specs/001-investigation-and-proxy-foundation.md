# Spec 001: Investigation and Proxy Foundation

**Status**: DRAFT
**Created**: 2026-02-15

## Problem Statement

Ad blocking via DNS (e.g., Pi-hole) is ineffective for apps like Apple News where ads are served from the same domains as content. A content-aware HTTPS interception proxy could selectively strip ads based on response body inspection rather than domain/IP blocking.

## Research Findings: Apple Apps and Proxy Feasibility

### Certificate Pinning - The Core Constraint

Apple first-party apps use **certificate pinning** for their own service connections. Apple's enterprise documentation explicitly states:

> "Apple services will fail any connection that uses HTTPS Interception (SSL Inspection). Attempts to perform content inspection on encrypted communications between Apple devices and services will result in a dropped connection."
> -- [Apple Support (101555)](https://support.apple.com/en-us/101555)

This means a standard MITM proxy with a custom CA installed on the device **will not work** for intercepting Apple News API calls. The app will reject the proxy's certificate regardless of trust store configuration.

### Where Interception CAN Work

| Target | Pinning? | Proxy Interception? | Notes |
|--------|----------|---------------------|-------|
| Safari browsing | No | Yes | Respects custom CAs for user-browsed sites |
| Third-party apps (standard NSURLSession) | Varies | Usually yes | Most third-party apps accept trusted custom CAs |
| WKWebView content inside apps | No | Potentially yes | WKWebView does NOT enforce NSPinnedDomains |
| Apple News API calls | Yes | No | Certificate pinning rejects non-Apple certs |
| App Store, iCloud, etc. | Yes | No | Same blanket Apple service pinning |

### The WKWebView Angle (Unconfirmed)

Apple News renders article content - likely using `WKWebView`. Web views do NOT enforce certificate pinning for the content they load. If ads within Apple News are loaded as web content (HTML/JS/images) inside a web view, those specific requests **might** be interceptable even when the app's API calls are not.

**This is unconfirmed and needs hands-on testing** (see Phase 1.5 below).

### Conclusion

- **Direct Apple News HTTPS interception: not viable** for API-level traffic
- **Safari and third-party apps: fully viable** as proxy targets
- **WKWebView-rendered ad content in Apple News: plausible but unproven**
- **A content-aware proxy is still a useful tool** for Safari, third-party apps, and potentially for the web view portions of Apple apps

---

## Phased Approach

### Phase 1: Hello World Proxy (Go)

Build a basic HTTP/HTTPS forward proxy in Go that can:
- Accept HTTP CONNECT requests (for HTTPS tunneling)
- Forward HTTP requests transparently
- Log all requests passing through
- Serve as the foundation for content inspection in later phases

**Acceptance Criteria**:
- [ ] Go module initialized with proxy server implementation
- [ ] Proxy handles HTTP requests (forward proxy mode)
- [ ] Proxy handles HTTPS CONNECT tunneling (passthrough, no inspection yet)
- [ ] Request/response logging with timestamps, method, URL, status code, content-type
- [ ] Configurable listen address and port (flag or env var)
- [ ] Graceful shutdown on SIGINT/SIGTERM
- [ ] Probe/liveness endpoint (see below)
- [ ] Test suite covering: HTTP proxying, HTTPS CONNECT tunneling, concurrent connections, malformed requests, probe endpoint
- [ ] Test client(s) that exercise the proxy against a cross-section of real sites:
  - HTTP-only sites (e.g., httpbin.org)
  - HTTPS sites (e.g., example.com, wikipedia.org)
  - Sites with mixed content
  - Sites that redirect HTTP to HTTPS
  - Sites with large responses (streaming)
- [ ] README with build/run/test instructions

#### Probe Endpoint

The proxy exposes a direct HTTP endpoint for liveness and behavior verification. This is used by the macOS agent test suite to confirm the proxy is reachable and functioning before running traffic tests.

**Request**: A non-proxied HTTP request directly to the proxy's listen address:
```
GET http://PROXY_HOST:PROXY_PORT/fps/probe HTTP/1.1
```

**Response** (200 OK, `application/json`):
```json
{
  "status": "ok",
  "service": "face-puncher-supreme",
  "version": "0.1.0",
  "mode": "passthrough",
  "uptime_seconds": 1234,
  "connections_total": 57,
  "connections_active": 3
}
```

**Behavior**:
- The `/fps/` prefix is reserved for proxy management endpoints. Requests to this path are handled directly by the proxy and never forwarded upstream.
- `mode` reflects the current proxy operating mode (initially just `"passthrough"` - will expand as HTTPS inspection and content filtering are added).
- `connections_total` / `connections_active` provide basic traffic counters so the macOS side can confirm traffic is flowing.
- The probe must be accessible both directly (non-proxied request to the proxy host) and through proxy configuration (browser configured to use the proxy requesting `http://fps.probe/fps/probe` or similar).

**Non-goals for this phase**:
- No HTTPS inspection (no MITM, no custom CA) - just tunneling
- No content filtering or ad blocking
- No persistent configuration

### Phase 1.5: Apple Device Proxy Behavior Probe

Before investing in HTTPS inspection, confirm what traffic from Apple devices actually flows through a configured proxy.

**Methodology**: This phase uses the macOS agent guide (`agents/macos-agent-guide.md`) to coordinate testing between the Linux dev environment (proxy server) and a real macOS/iOS system. The Linux side writes tasks, the macOS Claude executes them on a live system, and results are recorded in the guide. No assumptions about Apple app behavior are made without live system confirmation.

**Acceptance Criteria**:
- [ ] macOS agent guide Task 001 completed (environment discovery)
- [ ] Proxy server (Phase 1) accessible from macOS system on LAN
- [ ] macOS agent guide Task 002 completed (Apple News traffic analysis via proxy)
- [ ] macOS agent guide Task 003 completed if iOS devices are available
- [ ] Traffic logs from the proxy analyzed and categorized:
  - Safari browsing (expected: all traffic visible)
  - Apple News usage (what domains/paths appear vs. what bypasses)
  - Third-party app traffic
- [ ] Findings written up based on actual observed traffic (not web research)
- [ ] Decision gate: based on live findings, determine if proceeding to HTTPS inspection is worthwhile for the Apple News use case, or if the project should pivot to Safari/third-party apps

**This phase is live system testing + documentation, coordinated via `agents/macos-agent-guide.md`.**

### Phase 2: Local Browser Testing (Linux)

Use the Phase 1 proxy with a real browser on Linux to confirm real-world operation.

**Acceptance Criteria**:
- [ ] Configure Firefox or Chrome to use the proxy
- [ ] Verify normal browsing works through the proxy for a cross-section of sites:
  - News sites (CNN, BBC, NYT)
  - Social media (Reddit, Twitter)
  - Video (YouTube - basic page load)
  - E-commerce (Amazon)
  - Web apps (GitHub, Google Docs)
- [ ] Document any sites that break or behave unexpectedly
- [ ] Measure and document latency overhead introduced by the proxy
- [ ] Confirm the proxy handles persistent connections, chunked transfer encoding, and websocket upgrade requests gracefully (or documents known limitations)

### Phase 3: Network Exposure to Apple Devices

Expose the proxy to other devices on the local network and test Apple device behavior.

**Acceptance Criteria**:
- [ ] Proxy binds to LAN-accessible address (0.0.0.0 or specific LAN IP)
- [ ] Configure iOS device to use the proxy (document steps)
- [ ] Configure macOS device to use the proxy (document steps)
- [ ] Test Safari on iOS/macOS through the proxy - verify normal browsing
- [ ] Test Apple News - log what traffic appears (repeat of 1.5 findings, now confirmed with proxy accessible to the device)
- [ ] Test at least 2 third-party iOS apps
- [ ] Document all findings in a test report

---

## Technology Choices

- **Language**: Go
- **Key stdlib packages**: `net/http`, `net`, `crypto/tls`
- **No external dependencies for Phase 1** - use stdlib only
- **Testing**: Go's built-in `testing` package

## Future Phases (Out of Scope)

These are noted for context but are NOT part of this spec:

- HTTPS inspection with custom CA and on-the-fly certificate generation
- Content-aware ad filtering (HTML rewriting, image classification)
- Transparent proxying (no client configuration required)
- GUI-based proxy manager
- Automatic proxy configuration (PAC file serving)
- Certificate pinning bypass research (jailbreak-dependent)

## Open Questions

1. **WKWebView hypothesis**: Does Apple News actually load ad content via web views? Web research suggests this is plausible but **unconfirmed**. Phase 1.5 live testing via the macOS agent guide will answer this definitively.
2. **Scope narrowing**: If Apple News proves completely opaque to proxying, should the project pivot to focus on Safari + third-party app ad blocking (which is confirmed feasible)?
3. **Transparent proxy vs. configured proxy**: Transparent proxying (via iptables/nftables) avoids per-device configuration but adds complexity. When should we introduce this?
4. **Research vs. reality**: The certificate pinning findings above are from web research and enterprise documentation. Until we observe actual traffic from a live Apple device through our proxy (Phase 1.5), these are informed assumptions, not confirmed facts. The macOS agent guide (`agents/macos-agent-guide.md`) is the mechanism for getting ground truth.
