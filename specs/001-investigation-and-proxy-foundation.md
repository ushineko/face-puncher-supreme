# Spec 001: Investigation and Proxy Foundation

**Status**: COMPLETE
**Created**: 2026-02-15

## Problem Statement

Ad blocking via DNS (e.g., Pi-hole) is ineffective for apps like Apple News where ads are served from the same domains as content. A content-aware HTTPS interception proxy could selectively strip ads based on response body inspection rather than domain/IP blocking.

## Research Findings: Apple Apps and Proxy Feasibility

### Certificate Pinning — Pre-Testing Research

Apple first-party apps use **certificate pinning** for their own service connections. Apple's enterprise documentation explicitly states:

> "Apple services will fail any connection that uses HTTPS Interception (SSL Inspection). Attempts to perform content inspection on encrypted communications between Apple devices and services will result in a dropped connection."
> -- [Apple Support (101555)](https://support.apple.com/en-us/101555)

This led to the initial assumption that a MITM proxy would not work for Apple News API calls.

### Live Testing Results (2026-02-16)

**Phase 1.5 testing on macOS 26.3, Apple News 11.3, M1 Max MacBook Pro. See `agents/macos-agent-guide.md` for full trace data.**

The reality is more nuanced than the research predicted. Apple News splits its traffic into two categories with different proxy behavior:

| Traffic Type | Domains | Proxy Behavior | Protocol |
|-------------|---------|----------------|----------|
| Ad SDK | `news.iadsdk.apple.com` | Routes through system proxy | HTTPS via CONNECT |
| Telemetry | `news-events.apple.com`, `news-app-events.apple.com` | Routes through system proxy | HTTPS via CONNECT |
| Content/API | `c.apple.news`, `news-assets.apple.com`, `gateway.icloud.com` | Bypasses proxy (QUIC/HTTP3) | UDP-based QUIC |
| Infrastructure | `news-edge.apple.com`, `bag.itunes.apple.com`, `fpinit.itunes.apple.com` | Bypasses proxy | QUIC |
| Safari browsing | All domains | Routes through system proxy | HTTPS via CONNECT |

**Key finding**: Domain-level blocking of `news.iadsdk.apple.com` suppresses Apple News ads. User confirmed: "News seems to not be showing ads anymore." News continued to function normally with ads removed — no crashes, no content loading failures.

**DNS behavior**: Apple News uses encrypted DNS (DoH/DoT). Zero DNS queries on port 53 for News domains during the baseline capture. This confirms Pi-hole-style DNS blocking cannot reach these connections.

### Where the Research Was Wrong

The pre-testing research assumed certificate pinning would prevent **all** proxy interaction with Apple News. In practice:

- **Content traffic** does bypass the proxy — it uses QUIC (HTTP/3, UDP-based), which a TCP HTTP CONNECT proxy cannot intercept regardless of pinning
- **Ad SDK and telemetry traffic** respects the system HTTP proxy and routes through it as standard HTTPS CONNECT tunnels — domain-level blocking works
- **HTTPS content inspection (MITM) is not needed** for the primary use case. The ad SDK uses a separate domain from content, so domain-level blocking is sufficient
- **The WKWebView hypothesis was irrelevant** — ads come through the iAd SDK domain, not via web view content loading

### Revised Conclusion

- **Apple News ad blocking via domain proxy: viable and confirmed working**
- **The critical domain is `news.iadsdk.apple.com`** (Apple's iAd SDK)
- **HTTPS content inspection: not needed** for News ad suppression
- **DNS-level blocking: not viable** for News (encrypted DNS bypasses Pi-hole)
- **Safari through proxy: works, but Pi-hole blocklists are too aggressive** (93.7% block rate causes page breakage — see spec 002 notes)
- **Proxy approach validated** — this project addresses a gap that DNS blocking cannot fill

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
- [x] macOS agent guide Task 001 completed (environment discovery)
- [x] Proxy server (Phase 1) accessible from macOS system on LAN
- [x] macOS agent guide Task 002 completed (Apple News traffic analysis via proxy)
- [x] macOS agent guide Task 003 — SKIPPED, no iOS device connected during testing
- [x] Traffic logs from the proxy analyzed and categorized:
  - Safari browsing: all traffic routes through proxy; Pi-hole lists cause over-blocking
  - Apple News: ad SDK and telemetry route through proxy; content uses QUIC, bypasses proxy
  - Third-party app traffic: not tested (only Safari and News tested)
- [x] Findings written up based on actual observed traffic (not web research)
- [x] Decision gate: **HTTPS inspection not needed.** Domain-level blocking of `news.iadsdk.apple.com` suppresses News ads. Project proceeds with targeted blocklist refinement (see spec 005).

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

## Open Questions (Resolved)

1. **WKWebView hypothesis**: RESOLVED — irrelevant. Ads are delivered via the iAd SDK (`news.iadsdk.apple.com`), not via web view content. Domain-level blocking is sufficient.
2. **Scope narrowing**: RESOLVED — no pivot needed. Apple News ad blocking works via the proxy. The project continues on its original path.
3. **Transparent proxy vs. configured proxy**: Still open. macOS testing revealed a practical friction point — the proxy must be configured on the correct network interface (the Mac was on USB Ethernet, not Wi-Fi). Transparent proxying would eliminate this class of issue.
4. **Research vs. reality**: RESOLVED — live testing completed. See "Live Testing Results" section above. The research was partially wrong: Apple News does route ad/telemetry traffic through the system proxy, despite certificate pinning on content traffic.

## Open Questions (New)

1. **Blocklist tuning**: Pi-hole lists (376K domains) cause Safari breakage. The project needs an allowlist mechanism or more selective blocklists for general browser use (see spec 005).
2. **iOS testing**: Not yet tested. Task 003 was skipped due to no device. iOS Apple News may behave differently from macOS regarding proxy obedience.
3. **QUIC blocking**: Apple News content uses QUIC (HTTP/3, UDP). If future phases need to inspect content traffic, QUIC would need to be blocked to force fallback to TCP HTTPS. This is out of scope for now since domain-level ad blocking is sufficient.
