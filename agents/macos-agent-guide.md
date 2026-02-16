# macOS Agent Guide

This document is a communication channel between the Linux development environment (where the proxy server is built) and a macOS system (where Apple-specific testing and configuration happens).

**How this works**: The Linux Claude writes tasks in the `## Current Tasks` section. The macOS Claude reads this file, executes the tasks, and writes results in the `## Results` section. The Linux Claude reads the results and updates the project accordingly.

---

## Proxy Status

| Field | Value |
| ----- | ----- |
| Version | 0.4.0 |
| Binary | `fpsd` (Go, Linux amd64) |
| Default listen | `:8080` |
| Config file | `fpsd.yml` (auto-discovered in working directory) |
| Mode | Domain blocking (Pi-hole compatible blocklists) |
| Blocklist domains | ~376,000 (5 sources) |
| Probe endpoint | `http://PROXY_HOST:PROXY_PORT/fps/probe` |

### Verified Working (Linux + Chromium 145)

- HTTP forward proxy: passthrough for non-blocked domains
- HTTPS CONNECT tunnel: passthrough for non-blocked domains
- Domain blocking: 403 for blocked domains (both HTTP and CONNECT)
- Probe endpoint: returns JSON with status, block stats, top blocked domains
- Tested: ~400 connections, ~317 blocked (78% block rate on ad-heavy sites)
- Apple.com browsed successfully through proxy (no false positives)

### Starting the Proxy

```bash
# From the project root on the Linux machine:
make build

# Start with config file (fpsd.yml has all blocklist URLs pre-configured):
./fpsd

# With LAN-accessible binding (for macOS/iOS testing):
./fpsd --addr 0.0.0.0:8080

# Update blocklists without starting proxy:
./fpsd update-blocklist
```

All blocklist URLs are configured in `fpsd.yml`. First run downloads lists and builds `blocklist.db` (~4 seconds). Subsequent runs load from the existing DB instantly.

---

## System Info

Fill this in on first run so the Linux side knows what we're working with.

```text
macOS version:
Hardware:
Network interface (Wi-Fi):
Network interface (Ethernet):
Local IP address:
Apple News installed: yes/no
Apple News version:
iOS devices available for testing: (list devices + iOS versions)
Browsers installed:
```

---

## Current Tasks

### Task 001: Environment Discovery

**Status**: PENDING
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

**Status**: BLOCKED (waiting on Task 001 + proxy server running on Linux side)
**Priority**: High
**Context**: The proxy is built and verified working on Linux with Chromium. We now need to test it with macOS and Apple devices to see (a) whether Apple News traffic flows through the proxy, and (b) whether domain blocking affects ad delivery.

**When the Linux side provides a proxy address** (`PROXY_HOST:PROXY_PORT`):

1. Verify the proxy is reachable by hitting the probe endpoint:

   ```bash
   curl -s http://PROXY_HOST:PROXY_PORT/fps/probe | python3 -m json.tool
   ```

   Expected: JSON with `"status": "ok"`, `"mode": "blocking"`, and `"blocklist_size"` > 0. If `blocklist_size` is 0, the proxy started without blocklists — report this to the Linux side.

2. Note the `connections_total` and `blocks_total` values (baseline).

3. Configure macOS to use the proxy:

   ```bash
   networksetup -setwebproxy Wi-Fi PROXY_HOST PROXY_PORT
   networksetup -setsecurewebproxy Wi-Fi PROXY_HOST PROXY_PORT
   ```

4. Verify proxy is working:

   ```bash
   curl -s --proxy http://PROXY_HOST:PROXY_PORT http://example.com -o /dev/null -w '%{http_code}'
   ```

   Expected: `200`. This confirms traffic is routing through the proxy.

5. **Test 1: Safari on ad-heavy sites** — Browse 2-3 news sites (CNN, BBC, etc.) for 2 minutes. Note whether ads are visibly reduced compared to direct browsing.

6. **Test 2: Apple News** — Browse for 2-3 minutes. Interact with:
   - The Today feed
   - At least 3 full articles
   - Any visible ads (note their appearance — are they reduced? unchanged?)
   - The News+ tab if available

7. **Test 3: Apple.com** — Browse apple.com to verify no false positives (site should work normally).

8. Check probe for block stats:

   ```bash
   curl -s http://PROXY_HOST:PROXY_PORT/fps/probe | python3 -m json.tool
   ```

   Report: new `connections_total`, `blocks_total`, `top_blocked` list. Compare with baseline from step 2.

9. Disable the proxy when done:

   ```bash
   networksetup -setwebproxystate Wi-Fi off
   networksetup -setsecurewebproxystate Wi-Fi off
   ```

10. Report findings in Results, specifically:
    - Did Apple News traffic flow through the proxy? (connections_total should increase)
    - Were any ad domains blocked? (blocks_total should increase)
    - Did Apple News still function correctly with blocking active?
    - Were ads visibly reduced in Apple News?
    - Any broken functionality or errors?

---

### Task 003: iOS Device Proxy Configuration

**Status**: BLOCKED (waiting on Task 001 + proxy server)
**Priority**: Medium
**Context**: If iOS devices are available, we want to test Apple News on iOS through the proxy. This is the primary target — Apple News ads on iOS are the whole reason for the project.

**When the Linux side provides a proxy address** (`PROXY_HOST:PROXY_PORT`):

1. From the iOS device's browser, verify the proxy is reachable:
   - Open Safari and navigate to `http://PROXY_HOST:PROXY_PORT/fps/probe`
   - Expected: JSON with `"status": "ok"`, `"mode": "blocking"`, `"blocklist_size"` > 0.

2. Note `connections_total` and `blocks_total` (baseline).

3. Configure the iOS device to use the proxy:
   - Settings > Wi-Fi > (i) on connected network > Configure Proxy > Manual
   - Server: `PROXY_HOST`, Port: `PROXY_PORT`

4. Run the same tests as Task 002 (Safari on news sites, Apple News browsing, apple.com).

5. Check the probe again from Safari (`http://PROXY_HOST:PROXY_PORT/fps/probe`) and note the new `connections_total` and `blocks_total`.

6. Disable the proxy on the iOS device:
   - Settings > Wi-Fi > (i) on connected network > Configure Proxy > Off

7. Document findings in Results. Key questions:
   - Does iOS Apple News route traffic through the configured proxy?
   - Are ad domains blocked on iOS?
   - Does Apple News still function with blocking active?
   - Are ads visibly reduced?

---

### Task 004: Apple News Internal Behavior Investigation (Tracing)

**Status**: PENDING (can run independently of Tasks 002/003)
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

1. Start the proxy on the Linux side:

   ```bash
   # On Linux:
   ./fpsd --addr 0.0.0.0:8080
   ```

2. On macOS, verify the proxy is reachable:

   ```bash
   curl -s http://PROXY_HOST:PROXY_PORT/fps/probe | python3 -m json.tool
   ```

3. Configure the system proxy:

   ```bash
   networksetup -setwebproxy Wi-Fi PROXY_HOST PROXY_PORT
   networksetup -setsecurewebproxy Wi-Fi PROXY_HOST PROXY_PORT
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
   curl -s http://PROXY_HOST:PROXY_PORT/fps/probe | python3 -m json.tool
   ```

2. Report findings in the Results section. Key questions:
    - **Proxy obedience**: Did Apple News route any traffic through the system proxy? (Compare `connections_total` before and after the News session.)
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
Status: NOT STARTED
Date:
Findings:
```

### Result for Task 002

```text
Status: NOT STARTED
Date:
Findings:
```

### Result for Task 003

```text
Status: NOT STARTED
Date:
Findings:
```

### Result for Task 004

```text
Status: NOT STARTED
Date:
Findings:
```

---

## Completed Tasks Archive

(Move completed task/result pairs here to keep the active sections clean.)

---

## Notes

- The proxy is built and verified on Linux with Chromium (v0.4.0). The macOS side needs to fill in environment info (Task 001) and then test with the proxy running on the Linux machine.
- The proxy must bind to `0.0.0.0` (not localhost) for LAN access: `./fpsd --addr 0.0.0.0:8080`
- Both machines must be on the same network. Check firewall rules if the probe endpoint isn't reachable.
- Do NOT install custom CA certificates until explicitly instructed. Domain blocking works at the CONNECT level — we see the domain name and block before the TLS handshake, but cannot inspect encrypted content.
- Current blocking is domain-level only. Ads served from the same domain as content (e.g., Apple's own ad infrastructure) will NOT be blocked by this approach. That's the next phase (content inspection with MITM).
- The `blocklist.db` file is created on first run. Subsequent starts reuse it without re-downloading. Use `fpsd update-blocklist` to refresh.
