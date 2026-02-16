# macOS Agent Guide

This document is a communication channel between the Linux development environment (where the proxy server is built) and a macOS system (where Apple-specific testing and configuration happens).

**How this works**: The Linux Claude writes tasks in the `## Current Tasks` section. The macOS Claude reads this file, executes the tasks, and writes results in the `## Results` section. The Linux Claude reads the results and updates the project accordingly.

---

## System Info

Fill this in on first run so the Linux side knows what we're working with.

```
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

### Task 002: Apple News Traffic Analysis (Passive)

**Status**: BLOCKED (waiting on Task 001 + proxy server from Linux side)
**Priority**: High
**Context**: We need to know what traffic Apple News generates and whether it flows through a configured HTTP proxy. The Linux side will provide a proxy server address to point to.

**When the Linux side provides a proxy address** (`PROXY_HOST:PROXY_PORT`):

1. Verify the proxy is reachable by hitting the probe endpoint:
   ```bash
   curl -s http://PROXY_HOST:PROXY_PORT/fps/probe | python3 -m json.tool
   ```
   Expected: JSON response with `"status": "ok"` and `"service": "face-puncher-supreme"`. If this fails, stop and report the error - do not proceed to proxy configuration.

2. Note the `connections_total` value from the probe response (baseline).

3. Configure macOS to use the proxy:
   ```bash
   networksetup -setwebproxy Wi-Fi PROXY_HOST PROXY_PORT
   networksetup -setsecurewebproxy Wi-Fi PROXY_HOST PROXY_PORT
   ```

4. Verify proxy is working by hitting the probe again, this time through proxy config:
   ```bash
   curl -s --proxy http://PROXY_HOST:PROXY_PORT http://example.com -o /dev/null -w '%{http_code}'
   ```
   Expected: `200`. This confirms traffic is actually routing through the proxy.

5. Open Apple News and browse for 2-3 minutes. Interact with:
   - The Today feed
   - At least 3 full articles
   - Any visible ads (note their appearance)
   - The News+ tab if available

6. Open Safari and browse to 2-3 news sites for comparison.

7. Hit the probe endpoint again and note `connections_total`:
   ```bash
   curl -s http://PROXY_HOST:PROXY_PORT/fps/probe | python3 -m json.tool
   ```
   The difference from the baseline (step 2) tells us how many connections flowed through the proxy during testing.

8. Disable the proxy when done:
   ```bash
   networksetup -setwebproxystate Wi-Fi off
   networksetup -setsecurewebproxystate Wi-Fi off
   ```

9. Report what you observed in Results (the Linux side will have the actual traffic logs).

---

### Task 003: iOS Device Proxy Configuration

**Status**: BLOCKED (waiting on Task 001 + proxy server)
**Priority**: Medium
**Context**: If iOS devices are available, we want to test the same traffic analysis from iOS.

**When the Linux side provides a proxy address** (`PROXY_HOST:PROXY_PORT`):

1. From the iOS device's browser, verify the proxy is reachable:
   - Open Safari and navigate to `http://PROXY_HOST:PROXY_PORT/fps/probe`
   - Expected: JSON response with `"status": "ok"`. If this fails, the device can't reach the proxy - check network/firewall.

2. Note the `connections_total` value from the probe response (baseline).

3. Configure the iOS device to use the proxy:
   - Settings > Wi-Fi > (i) on connected network > Configure Proxy > Manual
   - Server: `PROXY_HOST`, Port: `PROXY_PORT`

4. Run the same Apple News + Safari test as Task 002 (steps 5-6).

5. Check the probe again from Safari (`http://PROXY_HOST:PROXY_PORT/fps/probe`) and note the new `connections_total` for comparison with baseline.

6. Disable the proxy on the iOS device:
   - Settings > Wi-Fi > (i) on connected network > Configure Proxy > Off

7. Document findings in Results.

---

## Results

### Result for Task 001

```
Status: NOT STARTED
Date:
Findings:
```

### Result for Task 002

```
Status: NOT STARTED
Date:
Findings:
```

### Result for Task 003

```
Status: NOT STARTED
Date:
Findings:
```

---

## Completed Tasks Archive

(Move completed task/result pairs here to keep the active sections clean.)

---

## Notes

- The Linux side proxy address will be provided once Phase 1 of the proxy server is built. The macOS side does not need to install anything initially - just fill in environment info (Task 001).
- If the proxy is on a different machine on the LAN, both machines need to be on the same network and the proxy needs to bind to 0.0.0.0 or the LAN IP.
- Do NOT install custom CA certificates until explicitly instructed. Phase 1 is passive observation only (HTTPS CONNECT tunneling - we see the domain but not the content).
