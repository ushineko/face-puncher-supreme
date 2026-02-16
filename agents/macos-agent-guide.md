# macOS Agent Guide

This document is a communication channel between the Linux development environment (where the proxy server is built) and a macOS system (where Apple-specific testing and configuration happens).

**How this works**: The Linux Claude writes tasks in the `## Current Tasks` section. The macOS Claude reads this file, executes the tasks, and writes results in the `## Results` section. The Linux Claude reads the results and updates the project accordingly.

---

## Proxy Status

| Field | Value |
| ----- | ----- |
| Version | 0.5.0 |
| Binary | `fpsd` (Go, Linux amd64) |
| Default listen | `:18737` |
| Config file | `fpsd.yml` (auto-discovered in working directory) |
| Mode | Domain blocking (Pi-hole compatible blocklists) |
| Blocklist domains | ~376,000 (5 sources) |
| Heartbeat endpoint | `http://njv-cachyos.local:18737/fps/heartbeat` |
| Stats endpoint | `http://njv-cachyos.local:18737/fps/stats` |

### Verified Working (Linux + Chromium 145)

- HTTP forward proxy: passthrough for non-blocked domains
- HTTPS CONNECT tunnel: passthrough for non-blocked domains
- Domain blocking: 403 for blocked domains (both HTTP and CONNECT)
- Heartbeat endpoint: returns JSON with status, version, mode, uptime
- Stats endpoint: returns JSON with connections, blocking, domains, clients, traffic
- Tested: ~400 connections, ~317 blocked (78% block rate on ad-heavy sites)
- Apple.com browsed successfully through proxy (no false positives)

### Starting the Proxy

```bash
# From the project root on the Linux machine:
make build

# Start with config file (fpsd.yml has all blocklist URLs pre-configured):
./fpsd

# With LAN-accessible binding (for macOS/iOS testing):
./fpsd --addr 0.0.0.0:18737

# Update blocklists without starting proxy:
./fpsd update-blocklist
```

All blocklist URLs are configured in `fpsd.yml`. First run downloads lists and builds `blocklist.db` (~4 seconds). Subsequent runs load from the existing DB instantly.

---

## System Info

Fill this in on first run so the Linux side knows what we're working with.

```text
macOS version: 26.3 (Build 25D125)
Hardware: MacBook Pro (MacBookPro18,2), Apple M1 Max, 10 cores, 32 GB RAM
Network interface (Wi-Fi): en0 (Wi-Fi)
Network interface (Ethernet): USB 10/100/1000 LAN (available, not primary)
Local IP address: 192.168.86.27
Apple News installed: yes
Apple News version: 11.3
iOS devices available for testing: None connected (iPhone USB service listed but no device attached)
Browsers installed: Safari
Proxy tools installed: None (no mitmproxy, Charles, or Proxyman)
Custom CA certificates: None (no mitmproxy or Charles CAs in System keychain)
Current proxy config: None active (all proxies disabled)
```

---

## Current Tasks

### Task 005: Allowlist Verification (Post Spec 005)

**Status**: PENDING (waiting for spec 005 implementation on Linux side)
**Priority**: High
**Context**: macOS testing revealed that Pi-hole blocklists cause over-blocking in Safari (93.7% block rate, broken pages). Spec 005 adds an allowlist mechanism to the proxy. This task verifies the allowlist works on macOS.

**Prerequisites**: Spec 005 must be implemented and the proxy restarted on the Linux side with allowlist entries configured.

**Instructions**:

1. Verify the proxy is reachable and check the version:

   ```bash
   curl -s http://njv-cachyos.local:18737/fps/heartbeat | python3 -m json.tool
   ```

2. Identify the active network interface before configuring the proxy:

   ```bash
   # IMPORTANT: must configure proxy on the ACTIVE interface
   # Check which interface has an IP and default route
   networksetup -listallnetworkservices
   route -n get default 2>/dev/null | grep interface
   ifconfig en0 | grep "inet "    # Wi-Fi
   ifconfig en6 | grep "inet "    # USB Ethernet (if present)
   ```

3. Configure the proxy on the **active** interface (substitute the correct interface name):

   ```bash
   # If Wi-Fi is active:
   networksetup -setwebproxy Wi-Fi njv-cachyos.local 18737
   networksetup -setsecurewebproxy Wi-Fi njv-cachyos.local 18737

   # If USB Ethernet is active (replace with actual service name):
   # networksetup -setwebproxy "USB 10/100/1000 LAN" njv-cachyos.local 18737
   # networksetup -setsecurewebproxy "USB 10/100/1000 LAN" njv-cachyos.local 18737
   ```

4. Verify with `scutil` that proxy settings are actually active:

   ```bash
   scutil --proxy
   # Must show HTTPEnable: 1 and HTTPSEnable: 1 with the proxy host/port
   ```

5. **Test 1: Apple News** — Browse for 2-3 minutes. Confirm ads are still suppressed (should be identical to Task 002 results since `news.iadsdk.apple.com` is not allowlisted).

6. **Test 2: Safari on previously broken sites** — Browse the sites that broke in Task 002:
   - CNN (was broken due to `registry.api.cnn.io` being blocked)
   - Any other news site that had missing content
   - Note whether the allowlist fixed the breakage

7. **Test 3: General Safari browsing** — Browse 5-6 sites (mix of news, social, shopping) for 5 minutes. Note any remaining breakage.

8. Check stats:

   ```bash
   curl -s http://njv-cachyos.local:18737/fps/stats | python3 -m json.tool
   ```

   Report: block rate (should be lower than 93.7%), any remaining false positives.

9. Disable the proxy:

   ```bash
   networksetup -setwebproxystate Wi-Fi off
   networksetup -setsecurewebproxystate Wi-Fi off
   # Or for USB Ethernet:
   # networksetup -setwebproxystate "USB 10/100/1000 LAN" off
   # networksetup -setsecurewebproxystate "USB 10/100/1000 LAN" off
   ```

10. Report findings in Results. Key questions:
    - Are Apple News ads still blocked?
    - Did the allowlist fix Safari breakage on CNN and other affected sites?
    - What is the new block rate? Is it more reasonable for general browsing?
    - Any remaining false positives to add to the allowlist?

---

### Task 003: iOS Device Proxy Configuration

**Status**: BLOCKED (no iOS device connected)
**Priority**: Medium
**Context**: If iOS devices are available, we want to test Apple News on iOS through the proxy. iOS Apple News may behave differently from macOS regarding proxy obedience. The proxy is running and reachable at `njv-cachyos.local:18737`.

1. From the iOS device's browser, verify the proxy is reachable:
   - Open Safari and navigate to `http://njv-cachyos.local:18737/fps/heartbeat`
   - Expected: JSON with `"status": "ok"`, `"mode": "blocking"`.

2. Check `http://njv-cachyos.local:18737/fps/stats` and note `connections.total` and `blocking.blocks_total` (baseline).

3. Configure the iOS device to use the proxy:
   - Settings > Wi-Fi > (i) on connected network > Configure Proxy > Manual
   - Server: `njv-cachyos.local`, Port: `18737`

4. Run the same tests as Task 005 (Apple News, Safari on news sites, general browsing).

5. Check the stats again from Safari (`http://njv-cachyos.local:18737/fps/stats`) and note the new `connections.total` and `blocking.blocks_total`.

6. Disable the proxy on the iOS device:
   - Settings > Wi-Fi > (i) on connected network > Configure Proxy > Off

7. Document findings in Results. Key questions:
   - Does iOS Apple News route traffic through the configured proxy?
   - Are ad domains blocked on iOS?
   - Does Apple News still function with blocking active?
   - Are ads visibly reduced?

---

## Results

*(Results for Tasks 005 and 003 will be recorded here as testing is completed.)*

---

## Completed Tasks Archive

### Task 001: Environment Discovery (COMPLETE, 2026-02-16)

- macOS 26.3 on M1 Max MacBook Pro (32 GB)
- Connected via Wi-Fi on 192.168.86.27 (USB Ethernet also available at 192.168.86.38)
- Apple News 11.3 installed (system app), Safari only browser
- No proxy tools, no custom CAs, clean baseline
- Proxy at njv-cachyos.local:18737 reachable — heartbeat confirmed

### Task 002: Apple News Traffic Analysis (COMPLETE, 2026-02-16)

Combined results with Task 004. Key findings:

**Proxy connectivity**: Active interface was USB Ethernet (192.168.86.38), not Wi-Fi. Initial proxy setup targeted Wi-Fi and had no effect until corrected. Always check the active interface with `route -n get default`.

**Traffic stats**: 2105 connections, 1963 blocked (93.7% block rate across Safari + News).

**Apple News ad blocking — WORKS**:
- User confirmed: "News seems to not be showing ads anymore"
- `news.iadsdk.apple.com` — 108 blocks (Apple's iAd SDK, serves News ads)
- `news-events.apple.com` — 424 blocks (News telemetry/analytics)
- `news-app-events.apple.com` — 116 blocks (News app events)
- News continued to function normally with ads removed

**Safari — over-blocking causes breakage**:
- User reported: "very slow and lots of stuff on pages are missing"
- Legitimate ad blocks: doubleclick.net (84), ad-delivery.net (80), amazon-adsystem.com (64)
- False positives: `registry.api.cnn.io` (66, CNN content API), `cdn.optimizely.com` (72, A/B testing), `api.rlcdn.com` (64)
- 93.7% block rate is too aggressive for general browsing

### Task 003: iOS Device Proxy (SKIPPED, 2026-02-16)

No iOS device connected during testing. Task carried forward.

### Task 004: Apple News Internal Behavior (COMPLETE, 2026-02-16)

**Baseline capture (no proxy)**:
- News downloads ~32 MiB, uploads ~516 KiB in 5 min session
- Protocol: QUIC (HTTP/3) for nearly all connections (UDP-based)
- DNS: Encrypted (DoH/DoT), zero port-53 queries for News domains
- Processes: News, NewsTag, NewsToday2
- Content domains: `c.apple.news`, `news-assets.apple.com`, `gateway.icloud.com`, `news-edge.apple.com`
- Third-party ad domains: NONE — all traffic to Apple-owned domains
- Ad delivery: via `news.iadsdk.apple.com` (Apple's iAd SDK)

**Proxy capture**:
- News routed ad/telemetry through proxy: `news.iadsdk.apple.com` (108), `news-events.apple.com` (424), `news-app-events.apple.com` (116)
- Content traffic used QUIC directly, bypassing proxy

**Feasibility**: Domain-level proxy blocking VIABLE for News ads. HTTPS inspection NOT needed. DNS blocking NOT viable (encrypted DNS). Blocklist tuning required for Safari.

---

## Notes

### Proxy Info

- The proxy is built and verified on Linux with Chromium and macOS with Safari + Apple News (v0.5.0).
- Proxy address: `njv-cachyos.local:18737` (must be started with `--addr 0.0.0.0:18737` for LAN access).
- Both machines must be on the same network. Check firewall rules if the heartbeat endpoint isn't reachable.
- The `blocklist.db` file is created on first run. Subsequent starts reuse it. Use `fpsd update-blocklist` to refresh.

### Lessons Learned from Testing (2026-02-16)

- **Network interface matters**: `networksetup -setwebproxy` must target the active interface. The Mac had both Wi-Fi and USB Ethernet; configuring Wi-Fi had no effect when Ethernet was active. Always check with `route -n get default` first, and verify with `scutil --proxy`.
- **Apple News ad blocking works at the domain level**: No MITM or content inspection needed. The iAd SDK (`news.iadsdk.apple.com`) is a separate domain from content.
- **Pi-hole blocklists are too aggressive for proxy use**: 93.7% block rate in Safari. DNS blocklists assume they're blocking at the network edge where false positives just mean a page element doesn't load. At the proxy level, false positives break entire page loads.
- **Apple News uses QUIC for content**: Content traffic bypasses the TCP proxy entirely. This is fine — we only need to block the ad SDK domain, which routes through the proxy as a standard HTTPS CONNECT.
- **Apple News uses encrypted DNS**: Zero port-53 queries. Pi-hole/DNS-based blocking cannot reach News. This validates the proxy approach.
- **Do NOT install custom CA certificates** until explicitly instructed. Domain blocking works at the CONNECT level — we see the domain name and block before the TLS handshake.

### MITM Case Study: Reddit Native Ads

**Problem**: Reddit serves ads as "promoted" posts from its own domain (`www.reddit.com`). These are indistinguishable from regular content at the domain level. Domain-level blocking cannot filter them without blocking Reddit entirely.

**Why Reddit is a good MITM test case**:
- Ads and content share the same origin domain — the exact scenario domain blocking can't handle
- Promoted posts have structural markers in the HTML/JSON (e.g., `promoted`, `is_promoted`, ad labels) that content inspection could target
- Reddit is not an Apple app, so no certificate pinning concerns — standard MITM with a custom CA should work
- Reddit's ad volume is high enough to validate detection but low enough (relative to content) to measure precision
- Unlike Apple News (which uses QUIC and encrypted DNS), Reddit in Safari uses standard HTTPS through the proxy, making it the path of least resistance for MITM development

**What MITM would need to do**:
1. Terminate the TLS connection from the client using a custom CA certificate
2. Inspect the HTTP response body from `www.reddit.com`
3. Identify promoted/ad content in the response (JSON API responses or HTML)
4. Strip or modify the ad content before forwarding to the client
5. Re-encrypt and forward the modified response

**Prerequisites before testing**:
- Custom CA certificate generated on the proxy (Linux side)
- CA certificate installed and trusted on macOS (System Keychain + full trust)
- Proxy updated to support TLS interception for selected domains
- Allowlist approach: only MITM domains that need content inspection (e.g., `www.reddit.com`), passthrough everything else

**Testing approach**:
1. Browse Reddit without MITM — capture baseline of promoted posts seen
2. Enable MITM for `www.reddit.com` only — verify pages still load correctly
3. Add content filtering rules targeting promoted posts
4. Browse again — compare promoted post visibility
5. Verify no breakage on non-MITM sites (CNN, Yahoo, Apple News should be unaffected)
