# macOS Agent Guide

This document is a communication channel between the Linux development environment (where the proxy server is built) and a macOS system (where Apple-specific testing and configuration happens).

**How this works**: The Linux Claude writes tasks in the `## Current Tasks` section. The macOS Claude reads this file, executes the tasks, and writes results in the `## Results` section. The Linux Claude reads the results and updates the project accordingly.

---

## Proxy Status

| Field | Value |
| ----- | ----- |
| Version | 0.9.0 |
| Binary | `fpsd` (Go, Linux amd64) |
| Default listen | `:18737` |
| Config file | `fpsd.yml` (auto-discovered in working directory) |
| Mode | Domain blocking + MITM content filtering |
| Blocklist domains | ~376,000 (5 sources) |
| MITM domains | `www.reddit.com`, `old.reddit.com` |
| Active plugins | `reddit-promotions@0.2.0` |
| CA cert endpoint | `http://njv-cachyos.local:18737/fps/ca.pem` |
| Heartbeat endpoint | `http://njv-cachyos.local:18737/fps/heartbeat` |
| Stats endpoint | `http://njv-cachyos.local:18737/fps/stats` |

### Verified Working (Linux + Chromium 145)

- HTTP forward proxy: passthrough for non-blocked domains
- HTTPS CONNECT tunnel: passthrough for non-blocked domains
- Domain blocking: 403 for blocked domains (both HTTP and CONNECT)
- Heartbeat endpoint: returns JSON with status, version, mode, uptime
- Stats endpoint: returns JSON with connections, blocking, domains, clients, traffic
- MITM TLS interception for configured domains (Reddit)
- Reddit promotions filter plugin active

### Verified Working (iPhone 17 Pro Max, iOS 26.2.1)

- Apple News ad blocking: no ads visible
- Safari browsing: YouTube and other sites functional, no breakage
- MITM TLS interception: Reddit loads without errors after CA trust
- Reddit promotions filter: active and working
- .local hostname resolution: works (no raw IP needed)
- Setup friction: iOS cert trust flow is multi-step (download, install profile, enable trust separately)

### Verified Working (macOS 26.3 + Safari)

- Domain blocking: Apple News ads suppressed (`news.iadsdk.apple.com` blocked)
- Allowlist: CNN content API, Optimizely allowed through (v0.6.0+)
- Safari browsing: CNN, Apple, Reuters, Yahoo, YouTube all functional
- Yahoo ad blocking confirmed (empty "advertisement" frames, no ad content)
- MITM: Reddit loads correctly through TLS interception with CA trusted
- Reddit promotions filter: active and working (v0.9.0)
- Non-MITM sites unaffected by MITM configuration
- CA cert: Face Puncher Supreme CA installed in System Keychain (SHA-256: 3F:53:FA:E4:AE:9B:4B:AA:EF:34:CB:C1:0D:C1:9E:FE:FD:AF:68:F6:9A:96:48:35:22:A6:5E:E2:AF:31:86:25)

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
Custom CA certificates: Face Puncher Supreme CA (installed 2026-02-16, trusted root, System Keychain)
Current proxy config: HTTP+HTTPS proxy on USB 10/100/1000 LAN → njv-cachyos.local:18737
```

---

## Current Tasks

### Task 005: Allowlist Verification (Post Spec 005)

**Status**: COMPLETE
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

### Task 003: iPhone Testing (Full Stack)

**Status**: COMPLETE
**Priority**: High
**Context**: The proxy is fully verified on macOS (domain blocking, allowlist, MITM, content filtering). This task validates the full stack on an iPhone. iOS Apple News may behave differently from macOS regarding proxy obedience, QUIC fallback, and certificate trust. The proxy must be running on the Linux machine at `njv-cachyos.local:18737` with `--addr 0.0.0.0:18737`.

#### Prerequisites

- Proxy running on Linux with LAN binding (`./fpsd --addr 0.0.0.0:18737`)
- iPhone on the same Wi-Fi network as the proxy host
- iPhone unlocked with passcode available (needed for certificate trust)

#### Part 1: Connectivity Check (Before Proxy)

1. **Confirm network**: On the iPhone, go to Settings > Wi-Fi. Note the connected network name. It must be the same LAN as `njv-cachyos.local`.

2. **Test reachability**: Open Safari on the iPhone and navigate to:
   ```
   http://njv-cachyos.local:18737/fps/heartbeat
   ```
   Expected: JSON with `"status": "ok"`. If this fails, the proxy is not reachable — check firewall rules on the Linux host and confirm both devices are on the same subnet.

3. **Record baseline stats**: Navigate to:
   ```
   http://njv-cachyos.local:18737/fps/stats
   ```
   Note `connections.total` and `blocking.blocks_total` for comparison later.

#### Part 2: CA Certificate Installation

The CA certificate is required for MITM interception (Reddit). Domain-level blocking works without it, so if certificate installation is skipped, test only the domain blocking sections.

4. **Download the CA cert**: In Safari on the iPhone, navigate to:
   ```
   http://njv-cachyos.local:18737/fps/ca.pem
   ```
   Safari will prompt: "This website is trying to download a configuration profile. Do you want to allow this?" Tap **Allow**.

5. **Install the profile**:
   - Go to Settings > General > VPN & Device Management (or "Profiles & Device Management" on older iOS)
   - The "Face Puncher Supreme CA" profile should appear under "Downloaded Profile"
   - Tap it, then tap **Install** (top right)
   - Enter the device passcode when prompted
   - Tap **Install** again on the warning screen, then **Done**

6. **Enable full trust for the CA**:
   - Go to Settings > General > About > Certificate Trust Settings
   - Under "Enable full trust for root certificates", toggle ON **Face Puncher Supreme CA**
   - Tap **Continue** on the warning dialog

7. **Verify**: The certificate should now show as trusted. If this step is skipped, MITM connections will fail with TLS errors (expected — iOS enforces certificate validation strictly).

#### Part 3: Proxy Configuration

8. **Configure the proxy**:
   - Settings > Wi-Fi > tap the (i) icon on the connected network
   - Scroll down to "HTTP Proxy" (or "Configure Proxy")
   - Select **Manual**
   - Server: `njv-cachyos.local`
   - Port: `18737`
   - Authentication: OFF
   - Tap **Save** (top right)

9. **Verify proxy is active**: Open Safari and navigate to:
   ```
   http://njv-cachyos.local:18737/fps/heartbeat
   ```
   This should still work. If it fails, the proxy configuration may be incorrect.

#### Part 4: Domain Blocking Tests

10. **Test A — Apple News ad blocking**:
    - Open the Apple News app
    - Browse articles from several publishers for 2-3 minutes
    - Scroll through the Today feed
    - **Key question**: Are ads visible? On macOS, `news.iadsdk.apple.com` gets blocked and ads disappear entirely
    - Note: iOS News may use QUIC/HTTP3 for some traffic (bypasses TCP proxy). If ads still appear, this is a significant finding — it means iOS News routes ad traffic differently than macOS News

11. **Test B — Safari browsing (news sites)**:
    - Visit: CNN, Reuters, Yahoo News, Apple.com
    - Pages should load correctly (allowlist covers these)
    - Note any missing content, broken layouts, or slow loading
    - Compare experience to normal browsing — allowlist should prevent over-blocking

12. **Test C — Safari browsing (other sites)**:
    - Visit: YouTube, Amazon, Wikipedia, GitHub
    - Note any breakage or missing content
    - These sites should work normally through the proxy

13. **Test D — Blocked domain verification**:
    - Navigate to a known blocked domain in Safari (e.g., `http://doubleclick.net`)
    - Expected: connection refused or proxy error page (403)
    - This confirms domain blocking is active

#### Part 5: MITM + Content Filtering Tests

These tests require the CA certificate to be installed and trusted (Part 2).

14. **Test E — Reddit through MITM**:
    - Open Safari and navigate to `https://www.reddit.com`
    - The page should load without TLS errors (green lock / no warnings)
    - Browse the front page and a few subreddits
    - **Key question**: Do promoted posts appear? On macOS, the reddit-promotions plugin filters them out
    - Try `https://old.reddit.com` as well

15. **Test F — MITM scope verification**:
    - Visit `https://www.apple.com` (not in MITM domain list)
    - Check the certificate: tap the lock icon in Safari's address bar
    - The certificate should be from Apple (Digicert or similar), NOT Face Puncher Supreme CA
    - This confirms MITM is scoped to configured domains only

#### Part 6: Stats and Cleanup

16. **Check final stats**: In Safari, navigate to:
    ```
    http://njv-cachyos.local:18737/fps/stats
    ```
    Record:
    - `connections.total` (compare to baseline from step 3)
    - `blocking.blocks_total`
    - `blocking.domains` — top blocked domains (look for `news.iadsdk.apple.com`)
    - `clients` — the iPhone should appear as a client entry

17. **Check the dashboard** (optional):
    ```
    http://njv-cachyos.local:18737/fps/dashboard/
    ```
    Verify the iPhone's traffic appears in the live view.

18. **Disable the proxy**:
    - Settings > Wi-Fi > tap (i) on the connected network
    - HTTP Proxy > select **Off**
    - Tap **Save**

19. **Optionally remove the CA cert** (if not needed for future testing):
    - Settings > General > VPN & Device Management
    - Tap "Face Puncher Supreme CA" > **Remove Profile**
    - Or: Settings > General > About > Certificate Trust Settings > toggle OFF

#### Part 7: Report

Document findings in the Results section. Key questions to answer:

| Question | Expected | Notes |
|----------|----------|-------|
| Does iOS Apple News route ad traffic through the proxy? | Yes (matches macOS) | If no, QUIC/HTTP3 may bypass the proxy |
| Are `news.iadsdk.apple.com` blocks visible in stats? | Yes | Compare to macOS (108 blocks in 5 min) |
| Are ads visibly reduced in Apple News? | Yes | User perception matters |
| Does Apple News still function with blocking active? | Yes | Content should be unaffected |
| Does Safari work on allowlisted sites? | Yes | CNN, Reuters, Yahoo, YouTube |
| Does Reddit load through MITM without TLS errors? | Yes | Requires CA trust |
| Are Reddit promoted posts filtered? | Yes | Content filtering plugin |
| Is MITM scoped correctly (non-MITM sites show original certs)? | Yes | Check apple.com cert |
| Any iOS-specific breakage not seen on macOS? | Document | Certificate pinning, HTTP/3, etc. |
| Does the iPhone appear in proxy stats as a client? | Yes | Check `/fps/stats` |

Also note:
- iPhone model and iOS version
- Any apps that stop working with the proxy enabled (certificate pinning)
- Whether `.local` hostname resolution works or if a raw IP is needed
- Approximate block rate compared to macOS testing

---

## Results

### Result for Task 005: Allowlist Verification

```text
Status: COMPLETE
Date: 2026-02-16

v0.6.0 Testing (blocklist tuning + allowlist):
- Apple News: ads still blocked (news.iadsdk.apple.com, news-events, news-app-events)
- Safari much improved vs v0.5.0 — CNN, Apple, Reuters, Yahoo, YouTube all functional
- Allowlist confirmed working: registry.api.cnn.io and cdn.optimizely.com in top_allowed
- Yahoo ad blocking confirmed: empty "advertisement" frames, no ad content loaded
  (bats.video.yahoo.com 296 blocks, geo.yahoo.com 176, noa.yahoo.com 96)
- www.reddit.com was a false positive (blocklist), fixed by removing overly strict
  social/gambling content blocklist
- Block rate still high but Safari functional — allowlist approach works

v0.9.0 Testing (MITM + Reddit promotions filter):
- CA certificate installed: Face Puncher Supreme CA, trusted root, System Keychain
  Downloaded from http://njv-cachyos.local:18737/fps/ca.pem
  Installed via: sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain fps-ca.pem
  SHA-256: 3F:53:FA:E4:AE:9B:4B:AA:EF:34:CB:C1:0D:C1:9E:FE:FD:AF:68:F6:9A:96:48:35:22:A6:5E:E2:AF:31:86:25
- MITM enabled: 2 domains (www.reddit.com, old.reddit.com)
- Reddit promotions plugin (v0.2.0) active
- Reddit pages load correctly through MITM — no TLS errors, no broken pages
- Reddit promoted posts filtered by content inspection plugin
- Non-MITM sites unaffected (Apple News, CNN, Yahoo continue working normally)
- Full stack verified: domain blocking + allowlist + MITM + content filtering
```

### Result for Task 003: iPhone Testing (Full Stack)

```text
Status: COMPLETE
Date: 2026-02-16
Device: iPhone 17 Pro Max, iOS 26.2.1

Apple News ad blocking: WORKS — no ads visible during browsing
Reddit MITM: WORKS — no TLS errors after CA cert installed and trusted, ad blocking active
Safari browsing: No breakage observed (YouTube and other sites tested)
Certificate setup: Painful (download profile, install profile, enable trust separately) but functional
.local hostname: Resolved successfully (no raw IP needed)
Overall: Full stack verified on iOS — domain blocking, MITM, content filtering all working
```

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

### Task 003: iPhone Testing (Full Stack) (COMPLETE, 2026-02-16)

iPhone 17 Pro Max, iOS 26.2.1. Full stack verified: domain blocking (Apple News ads removed), MITM (Reddit, no TLS errors), content filtering (promotions filtered), Safari browsing (no breakage). Setup painful due to iOS cert trust flow but functional. mDNS .local resolution worked.

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

- The proxy is built and verified on Linux with Chromium and macOS with Safari + Apple News (v0.9.0).
- Proxy address: `njv-cachyos.local:18737` (must be started with `--addr 0.0.0.0:18737` for LAN access).
- Both machines must be on the same network. Check firewall rules if the heartbeat endpoint isn't reachable.
- The `blocklist.db` file is created on first run. Subsequent starts reuse it. Use `fpsd update-blocklist` to refresh.

### Lessons Learned from Testing (2026-02-16)

- **Network interface matters**: `networksetup -setwebproxy` must target the active interface. The Mac had both Wi-Fi and USB Ethernet; configuring Wi-Fi had no effect when Ethernet was active. Always check with `route -n get default` first, and verify with `scutil --proxy`.
- **Apple News ad blocking works at the domain level**: No MITM or content inspection needed. The iAd SDK (`news.iadsdk.apple.com`) is a separate domain from content.
- **Pi-hole blocklists are too aggressive for proxy use**: 93.7% block rate in Safari. DNS blocklists assume they're blocking at the network edge where false positives just mean a page element doesn't load. At the proxy level, false positives break entire page loads.
- **Apple News uses QUIC for content**: Content traffic bypasses the TCP proxy entirely. This is fine — we only need to block the ad SDK domain, which routes through the proxy as a standard HTTPS CONNECT.
- **Apple News uses encrypted DNS**: Zero port-53 queries. Pi-hole/DNS-based blocking cannot reach News. This validates the proxy approach.
- **CA certificate installed**: Face Puncher Supreme CA is trusted in System Keychain. This enables MITM for configured domains (currently Reddit). To remove: `sudo security delete-certificate -c "Face Puncher Supreme CA" /Library/Keychains/System.keychain`

### MITM: Reddit Native Ads (IMPLEMENTED AND VERIFIED)

**Problem**: Reddit serves ads as "promoted" posts from its own domain (`www.reddit.com`). Indistinguishable from regular content at the domain level.

**Solution implemented (v0.9.0)**:
- MITM TLS interception for `www.reddit.com` and `old.reddit.com`
- `reddit-promotions@0.2.0` plugin filters promoted posts via content inspection
- CA certificate (Face Puncher Supreme CA) installed on macOS System Keychain
- Non-MITM sites continue using opaque tunnels (zero overhead)

**Verified on macOS (2026-02-16)**:
- Reddit pages load correctly through MITM — no TLS errors
- Promoted posts filtered by the content inspection plugin
- Non-MITM sites (Apple News, CNN, Yahoo, YouTube) completely unaffected
- Full pipeline: domain blocking + allowlist + MITM + content filtering all working together
