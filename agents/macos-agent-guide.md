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

### Task 001: Environment Discovery

**Status**: COMPLETE
**Priority**: High
**Context**: We're building a content-aware ad-blocking proxy (Go, running on Linux). Before investing in HTTPS inspection, we need to understand what Apple devices actually send through a configured HTTP proxy.

**Instructions**:

1. Fill in the System Info section above.

2. Check what proxy-related tools are available:

   ```bash
   which mitmproxy
   which charles
   which proxyman
   # check if any proxy tools are installed
   brew list | grep -i proxy
   ```

3. Report current network proxy configuration:

   ```bash
   networksetup -getwebproxy Wi-Fi
   networksetup -getsecurewebproxy Wi-Fi
   networksetup -listallnetworkservices
   scutil --proxy
   ```

4. Check if there's an existing custom CA in the keychain:

   ```bash
   security find-certificate -a -c "mitmproxy" /Library/Keychains/System.keychain 2>/dev/null
   security find-certificate -a -c "Charles" /Library/Keychains/System.keychain 2>/dev/null
   ```

5. Report findings in the Results section below.

---

### Task 002: Apple News Traffic Analysis (Passive + Blocking)

**Status**: COMPLETE
**Priority**: High
**Context**: The proxy is built and verified working on Linux with Chromium. We now need to test it with macOS and Apple devices to see (a) whether Apple News traffic flows through the proxy, and (b) whether domain blocking affects ad delivery. The proxy is running and reachable at `njv-cachyos.local:18737`.

**Proxy address**: `njv-cachyos.local:18737`

1. Verify the proxy is reachable by hitting the heartbeat endpoint:

   ```bash
   curl -s http://njv-cachyos.local:18737/fps/heartbeat | python3 -m json.tool
   ```

   Expected: JSON with `"status": "ok"` and `"mode": "blocking"`.

2. Get baseline stats:

   ```bash
   curl -s http://njv-cachyos.local:18737/fps/stats | python3 -m json.tool
   ```

   Note the `connections.total` and `blocking.blocks_total` values (baseline).

3. Configure macOS to use the proxy:

   ```bash
   networksetup -setwebproxy Wi-Fi njv-cachyos.local 18737
   networksetup -setsecurewebproxy Wi-Fi njv-cachyos.local 18737
   ```

4. Verify proxy is working:

   ```bash
   curl -s --proxy http://njv-cachyos.local:18737 http://example.com -o /dev/null -w '%{http_code}'
   ```

   Expected: `200`. This confirms traffic is routing through the proxy.

5. **Test 1: Safari on ad-heavy sites** — Browse 2-3 news sites (CNN, BBC, etc.) for 2 minutes. Note whether ads are visibly reduced compared to direct browsing.

6. **Test 2: Apple News** — Browse for 2-3 minutes. Interact with:
   - The Today feed
   - At least 3 full articles
   - Any visible ads (note their appearance — are they reduced? unchanged?)
   - The News+ tab if available

7. **Test 3: Apple.com** — Browse apple.com to verify no false positives (site should work normally).

8. Check stats for block data:

   ```bash
   curl -s http://njv-cachyos.local:18737/fps/stats | python3 -m json.tool
   ```

   Report: new `connections.total`, `blocking.blocks_total`, `blocking.top_blocked` list. Compare with baseline from step 2.

9. Disable the proxy when done:

   ```bash
   networksetup -setwebproxystate Wi-Fi off
   networksetup -setsecurewebproxystate Wi-Fi off
   ```

10. Report findings in Results, specifically:
    - Did Apple News traffic flow through the proxy? (connections.total should increase)
    - Were any ad domains blocked? (blocking.blocks_total should increase)
    - Did Apple News still function correctly with blocking active?
    - Were ads visibly reduced in Apple News?
    - Any broken functionality or errors?

---

### Task 003: iOS Device Proxy Configuration

**Status**: BLOCKED (no iOS device connected)
**Priority**: Medium
**Context**: If iOS devices are available, we want to test Apple News on iOS through the proxy. This is the primary target — Apple News ads on iOS are the whole reason for the project. The proxy is running and reachable at `njv-cachyos.local:18737`.

1. From the iOS device's browser, verify the proxy is reachable:
   - Open Safari and navigate to `http://njv-cachyos.local:18737/fps/heartbeat`
   - Expected: JSON with `"status": "ok"`, `"mode": "blocking"`.

2. Check `http://njv-cachyos.local:18737/fps/stats` and note `connections.total` and `blocking.blocks_total` (baseline).

3. Configure the iOS device to use the proxy:
   - Settings > Wi-Fi > (i) on connected network > Configure Proxy > Manual
   - Server: `njv-cachyos.local`, Port: `18737`

4. Run the same tests as Task 002 (Safari on news sites, Apple News browsing, apple.com).

5. Check the stats again from Safari (`http://njv-cachyos.local:18737/fps/stats`) and note the new `connections.total` and `blocking.blocks_total`.

6. Disable the proxy on the iOS device:
   - Settings > Wi-Fi > (i) on connected network > Configure Proxy > Off

7. Document findings in Results. Key questions:
   - Does iOS Apple News route traffic through the configured proxy?
   - Are ad domains blocked on iOS?
   - Does Apple News still function with blocking active?
   - Are ads visibly reduced?

---

### Task 004: Apple News Internal Behavior Investigation (Tracing)

**Status**: COMPLETE (can run independently of Tasks 002/003)
**Priority**: High
**Context**: Before investing further in proxy-based ad blocking, we need to understand how Apple News actually behaves internally on macOS. Does it use the system HTTP proxy? Does it pin certificates? Does it use a custom transport? What domains does it contact and for what purpose? This task uses macOS tracing tools to answer these questions by capturing real network activity while a user interacts with the app.

**Goal**: Capture a detailed trace of Apple News network behavior during normal use. The user will generate activity (browsing feeds, reading articles, encountering ads), and the trace will show exactly what connections News makes, to which hosts, using which protocols. This tells us whether the proxy approach is feasible for macOS before we invest in HTTPS inspection.

**Instructions**:

#### Part A: Capture with no proxy configured (baseline)

1. Ensure no proxy is configured on macOS:

   ```bash
   networksetup -setwebproxystate Wi-Fi off
   networksetup -setsecurewebproxystate Wi-Fi off
   scutil --proxy  # confirm all proxies are off
   ```

2. Close Apple News completely (force quit if needed):

   ```bash
   killall News 2>/dev/null
   ```

3. Start a network trace using `nettop` in a terminal. This captures per-process network activity:

   ```bash
   # Run nettop filtered to the News process, log to file
   # -P = show only processes with network activity
   # -J bytes_in,bytes_out = columns to show
   # -t wifi = filter to Wi-Fi interface
   nettop -P -J bytes_in,bytes_out -t wifi -n -l 1 > ~/news_nettop_baseline.txt 2>&1 &
   NETTOP_PID=$!
   echo "nettop running as PID $NETTOP_PID"
   ```

4. In a separate terminal, start a DNS query log:

   ```bash
   sudo tcpdump -i any -n port 53 -l 2>/dev/null | tee ~/news_dns_baseline.txt &
   TCPDUMP_PID=$!
   echo "tcpdump running as PID $TCPDUMP_PID"
   ```

5. In a third terminal, capture the unified log for News network activity:

   ```bash
   log stream --predicate '(process == "News" || process == "nsurlsessiond") && (category == "networking" || category == "default")' --style compact > ~/news_log_baseline.txt 2>&1 &
   LOG_PID=$!
   echo "log stream running as PID $LOG_PID"
   ```

6. **User activity** — Open Apple News and use it normally for 5 minutes:
   - Scroll through the Today feed
   - Open and read at least 5 full articles (mix of free and News+ if available)
   - Note any ads you see (banner, interstitial, inline) and where they appear
   - Visit the Following tab
   - Search for a topic and open a result
   - Return to the Today feed and scroll more

7. Stop all captures:

   ```bash
   kill $NETTOP_PID $TCPDUMP_PID $LOG_PID 2>/dev/null
   sudo killall tcpdump 2>/dev/null
   ```

8. Extract the domains News contacted:

   ```bash
   # From DNS log — domains resolved during the session
   grep -oP '(?<=A\? )\S+' ~/news_dns_baseline.txt | sort -u > ~/news_domains_baseline.txt

   # Count total unique domains
   wc -l ~/news_domains_baseline.txt
   ```

#### Part B: Capture with system proxy configured

1. The proxy is already running on the Linux side at `njv-cachyos.local:18737`.

2. On macOS, verify the proxy is reachable:

   ```bash
   curl -s http://njv-cachyos.local:18737/fps/heartbeat | python3 -m json.tool
   ```

3. Configure the system proxy:

   ```bash
   networksetup -setwebproxy Wi-Fi njv-cachyos.local 18737
   networksetup -setsecurewebproxy Wi-Fi njv-cachyos.local 18737
   ```

4. Force quit and relaunch Apple News:

   ```bash
   killall News 2>/dev/null
   sleep 2
   open -a News
   ```

5. Repeat the same nettop + DNS + log stream captures (Part A steps 3-5), saving to `*_proxy.txt` filenames instead.

6. Repeat the same user activity (Part A step 6) for 5 minutes.

7. Stop captures and disable the proxy:

   ```bash
   kill $NETTOP_PID $TCPDUMP_PID $LOG_PID 2>/dev/null
   sudo killall tcpdump 2>/dev/null
   networksetup -setwebproxystate Wi-Fi off
   networksetup -setsecurewebproxystate Wi-Fi off
   ```

#### Part C: Analysis

1. Compare the two captures and report:

   ```bash
   # Domains contacted without proxy vs with proxy
   diff ~/news_domains_baseline.txt ~/news_domains_proxy.txt

   # Check proxy stats
   curl -s http://njv-cachyos.local:18737/fps/stats | python3 -m json.tool
   ```

2. Report findings in the Results section. Key questions:
    - **Proxy obedience**: Did Apple News route any traffic through the system proxy? (Compare `connections.total` before and after the News session.)
    - **Direct connections**: Did News bypass the proxy for some or all connections? (Compare DNS logs — if News resolved the same domains in both runs, it may be ignoring the proxy.)
    - **Certificate pinning**: Did News fail to load content when the proxy was configured? (This would suggest it detects the proxy or pins certificates.)
    - **Domain inventory**: List all unique domains News contacted. Categorize as: content, ads/tracking, Apple infrastructure, CDN, other.
    - **Protocol breakdown**: What protocols did News use? (HTTP/1.1, HTTP/2, HTTP/3/QUIC, custom?)
    - **Ad delivery pattern**: Are ads served from the same domains as content, or from distinct ad-serving domains?
    - **Feasibility assessment**: Based on the trace, is domain-level proxy blocking viable for News ads? Would HTTPS content inspection be needed? Or does News bypass the proxy entirely?

#### Optional: Instruments Network Profiler

If Xcode is installed, an Instruments trace provides deeper insight:

```bash
# Check if Instruments is available
which instruments || xcrun xctrace list devices
```

If available, consider running a Network profiler trace on the News process. This shows HTTP request/response pairs, connection reuse, and protocol details that nettop and tcpdump cannot capture. Document the steps taken and attach or describe the Instruments output.

---

## Results

### Result for Task 001

```text
Status: COMPLETE
Date: 2026-02-16

Findings:
- macOS 26.3 on M1 Max MacBook Pro (32 GB)
- Connected via Wi-Fi on 192.168.86.27
- Apple News 11.3 installed (system app)
- Safari is the only browser installed
- No proxy tools installed (no mitmproxy, Charles, Proxyman)
- No custom CA certificates in System keychain
- No proxies currently configured (clean baseline)
- Proxy at njv-cachyos.local:18737 is reachable — heartbeat confirmed (v0.5.0, mode: blocking)
- No iOS devices currently connected (iPhone USB service present but no device attached)
```

### Result for Task 002

```text
Status: COMPLETE
Date: 2026-02-16

Findings (combined with Task 004):

PROXY CONNECTIVITY
- Active interface: USB 10/100/1000 LAN (192.168.86.38), NOT Wi-Fi
- Initial setup used Wi-Fi — proxy settings had no effect until corrected
- networksetup vs scutil: must target the active interface or scutil --proxy shows nothing
- Proxy heartbeat and blocking confirmed working before test (ads.google.com → 403)

TRAFFIC STATS (proxy session)
- Total connections: 2105
- Total requests: 2095
- Total blocked: 1963 (93.7% block rate)
- Total bytes out (to client): ~80 MB
- Client 192.168.86.38 (Ethernet): 1114 requests, 983 blocked
- Client 192.168.86.27 (Wi-Fi background): 981 requests, 980 blocked

APPLE NEWS AD BLOCKING — WORKS
- User confirmed: "News seems to not be showing ads anymore"
- news.iadsdk.apple.com — 108 blocks (Apple's iAd SDK, serves News ads)
- news-events.apple.com — 424 blocks (News telemetry/analytics)
- news-app-events.apple.com — 116 blocks (News app events)
- News continued to function normally with ads removed
- No crashes or content loading failures in News

SAFARI — OVER-BLOCKING CAUSES BREAKAGE
- User reported: "very slow and lots of stuff on pages are missing"
- Legitimate ad blocks: doubleclick.net (84), ad-delivery.net (80),
  amazon-adsystem.com (64), googlesyndication.com (40), adnxs.com (40)
- False positive blocks causing breakage:
  - registry.api.cnn.io (66) — CNN's content API, not ads
  - cdn.optimizely.com (72) — A/B testing, some sites need it for rendering
  - api.rlcdn.com (64) — LiveRamp tracking (may affect content loading)
- 93.7% block rate is far too aggressive for general browsing

KEY CONCLUSIONS
1. Apple News DOES route traffic through the system HTTP proxy
2. Domain-level blocking of news.iadsdk.apple.com suppresses News ads
3. HTTPS content inspection is NOT needed for basic News ad blocking
4. Pi-hole-style blocklists are too aggressive for browser proxy use
5. News uses QUIC (HTTP/3) for content but still routes ad SDK via proxy
6. The proxy needs a whitelist or more selective blocklist for Safari use
```

### Result for Task 003

```text
Status: SKIPPED
Date: 2026-02-16
Findings: No iOS device was connected during testing. Task remains available for
future testing when an iPhone/iPad is available.
```

### Result for Task 004

```text
Status: COMPLETE
Date: 2026-02-16

Findings (combined with Task 002):

BASELINE CAPTURE (no proxy, ~5 min Apple News browsing)

Traffic volume: News downloaded ~32 MiB, uploaded ~516 KiB

Protocol: Apple News uses QUIC (HTTP/3) for nearly all connections.
Log shows "quic-connection" throughout. This is UDP-based, meaning a
traditional TCP HTTP CONNECT proxy cannot intercept QUIC content traffic.

DNS: Almost zero DNS queries captured on port 53 (only 2 unrelated domains).
Apple News uses encrypted DNS (DoH/DoT), bypassing standard DNS interception.

Processes observed: News (main), NewsTag, NewsToday2

Domains contacted (from unified log URL extraction):
  Apple infrastructure:
  - news-edge.apple.com — News API/config endpoint
  - news-todayconfig-edge.apple.com — Today feed config
  - news-assets.apple.com — Static assets (layouts, themes, images, data feeds)
  - c.apple.news — Article content and images (bulk of traffic)
  - gateway.icloud.com — CloudKit database queries (articles, for-you, magazines)
  - bag.itunes.apple.com — iTunes bag config
  - s.mzstatic.com — Apple static content (cert setup)
  - fpinit.itunes.apple.com — Fingerprint/security initialization

  Third-party ad domains: NONE visible in baseline
  All traffic goes to Apple-owned domains.

Ad delivery pattern: Ads in Apple News appear to be served through
news.iadsdk.apple.com (Apple's own iAd SDK), which is an Apple domain.
No third-party ad networks observed in the baseline capture.

PROXY CAPTURE (with proxy active, ~5 min Apple News + Safari)

Apple News routed ad/telemetry traffic through the proxy:
  - news.iadsdk.apple.com (108 requests, all blocked)
  - news-events.apple.com (424 requests, all blocked)
  - news-app-events.apple.com (116 requests, all blocked)
Content traffic (c.apple.news, news-assets.apple.com) likely still
used QUIC directly, bypassing the proxy.

Safari routed all traffic through the proxy, including ad domains:
  - securepubads.g.doubleclick.net, ad-delivery.net,
    aax.amazon-adsystem.com, pagead2.googlesyndication.com, ib.adnxs.com

FEASIBILITY ASSESSMENT
- Domain-level proxy blocking: VIABLE for Apple News ads
- The key domain is news.iadsdk.apple.com
- HTTPS content inspection: NOT needed for News ad suppression
- DNS-level blocking: NOT viable (News uses encrypted DNS)
- QUIC interception: Would be needed to inspect content traffic,
  but is unnecessary since ad SDK is a separate domain
- Blocklist tuning: Required — Pi-hole lists are too broad for
  proxy use with Safari (false positives on content APIs)
```

---

## Completed Tasks Archive

(Move completed task/result pairs here to keep the active sections clean.)

---

## Notes

- The proxy is built and verified on Linux with Chromium (v0.5.0). **Proxy is currently running at `njv-cachyos.local:18737`**. The macOS side needs to fill in environment info (Task 001) and then proceed with testing.
- Both machines must be on the same network. Check firewall rules if the heartbeat endpoint isn't reachable.
- Do NOT install custom CA certificates until explicitly instructed. Domain blocking works at the CONNECT level — we see the domain name and block before the TLS handshake, but cannot inspect encrypted content.
- Current blocking is domain-level only. Ads served from the same domain as content (e.g., Apple's own ad infrastructure) will NOT be blocked by this approach. That's the next phase (content inspection with MITM).
- The `blocklist.db` file is created on first run. Subsequent starts reuse it without re-downloading. Use `fpsd update-blocklist` to refresh.
