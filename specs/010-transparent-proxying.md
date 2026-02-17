# Spec 010: Transparent Proxying

**Status**: COMPLETE
**Created**: 2026-02-16
**Depends on**: Spec 001 (proxy foundation), Spec 002 (domain blocklist), Spec 003 (config file), Spec 006 (MITM TLS interception)

## Problem Statement

Every client device that uses fpsd must be explicitly configured with the proxy address. This creates friction in several ways:

- Each device (iPhone, iPad, Mac) needs individual proxy configuration
- Different OSes and apps have different proxy settings locations
- Some iOS apps ignore the system HTTP proxy setting entirely
- Proxy settings are per-network on iOS — moving between Wi-Fi networks loses the configuration
- macOS agent guide testing (spec 001) identified proxy configuration as a recurring pain point

Transparent proxying eliminates client-side configuration. The Linux machine running fpsd acts as the network's default gateway. iptables redirects HTTP and HTTPS traffic to fpsd automatically. Client devices connect normally — they do not know a proxy exists.

### Why Now

The proxy has matured through 9 specs: foundation, blocklists, config, stats, allowlists, MITM, plugins, content filtering, and dashboard. All core features work in explicit proxy mode. Transparent mode extends the same capabilities to clients that cannot or will not configure a proxy, without changing any existing behavior.

## Approach

### Dual-Mode Architecture

Transparent proxying runs alongside the existing explicit proxy. Both modes share the same blocklist, allowlist, MITM interceptor, plugin pipeline, and stats collector. The explicit proxy listener continues on its configured port (default `:18737`). Two additional listeners handle transparent traffic:

- **Transparent HTTP listener**: Accepts connections redirected from port 80
- **Transparent HTTPS listener**: Accepts connections redirected from port 443

The listeners operate independently. Enabling transparent mode does not affect explicit proxy behavior in any way.

### How Transparent Differs from Explicit

| Aspect | Explicit proxy | Transparent proxy |
| ------ | -------------- | ----------------- |
| Client awareness | Client knows it's using a proxy | Client thinks it's connecting directly |
| HTTP requests | Absolute URI (`GET http://host/path`) | Relative URI (`GET /path`, Host header) |
| HTTPS requests | HTTP CONNECT method, then TLS | Raw TLS ClientHello, no HTTP framing |
| Destination discovery | From request URL or CONNECT host | From Host header (HTTP) or SNI (HTTPS) |
| Management endpoints | Available at `/fps/*` | Not available (traffic goes to real destinations) |

### Transparent HTTP Flow

iptables redirects port 80 traffic from LAN clients to fpsd's transparent HTTP port. The proxy receives a standard HTTP request — identical to what the destination server would see.

```
Client → TCP connect (thinks it's reaching example.com:80)
  → iptables REDIRECT → fpsd transparent HTTP port
    → Read HTTP request (GET /path HTTP/1.1, Host: example.com)
    → Extract domain from Host header
    → Check blocklist → 403 if blocked
    → Dial upstream (example.com:80)
    → Forward request, relay response
```

The request arrives with a relative URI and a Host header. fpsd extracts the domain from the Host header, checks the blocklist and allowlist, then forwards the request to the original destination. If the Host header is missing (HTTP/1.0 without Host), fpsd falls back to `SO_ORIGINAL_DST` to recover the original destination address from the kernel.

Hop-by-hop headers are stripped, same as explicit mode. The response is relayed back to the client.

### Transparent HTTPS Flow

iptables redirects port 443 traffic to fpsd's transparent HTTPS port. The proxy receives raw TCP bytes — the start of a TLS handshake, not an HTTP CONNECT request.

```
Client → TCP connect (thinks it's reaching example.com:443)
  → iptables REDIRECT → fpsd transparent HTTPS port
    → Peek at TLS ClientHello (buffer bytes, do not consume)
    → Extract SNI (Server Name Indication) from ClientHello
    → Check blocklist → close connection if blocked
    → Check MITM domains:
    ├─ NOT in MITM list → tunnel:
    │    → Dial upstream (SNI:443)
    │    → Replay buffered ClientHello to upstream
    │    → Bidirectional byte copy (opaque tunnel)
    └─ IN MITM list → intercept:
         → Wrap connection (prepend buffered bytes)
         → Delegate to existing MITMInterceptor.Handle()
```

**SNI extraction**: fpsd reads the initial TLS record from the client, parses the ClientHello handshake message, and extracts the server_name extension. The buffered bytes are preserved for replay or rewinding.

**Tunneling (non-MITM)**: fpsd dials the upstream server at `SNI:443`, sends the buffered ClientHello bytes first, then performs bidirectional byte copy. The TLS handshake completes between the client and the real server — fpsd never sees plaintext.

**MITM interception**: fpsd wraps the client connection so that reads first return the buffered ClientHello bytes, then proceed from the real socket. This wrapped connection is passed to the existing `MITMInterceptor.Handle()` method — the same handler used in explicit mode. From the MITM handler's perspective, there is no difference between explicit and transparent connections.

**Blocked domains**: Since there is no HTTP layer, fpsd cannot send a 403 response. It closes the TCP connection. The client sees a connection reset or timeout.

**Missing SNI**: If the ClientHello contains no SNI extension (rare in modern clients, but possible), fpsd falls back to `SO_ORIGINAL_DST` to get the original destination IP. Without a domain name, blocklist checks match against the IP (which will not match domain-based rules), and the connection is tunneled. MITM is not possible without a domain name (no cert can be generated).

### SNI Parser

The TLS ClientHello parser extracts the server name from the SNI extension. It operates on buffered bytes without completing a TLS handshake.

**TLS record format parsed**:

```
Record layer:
  Content type: 0x16 (Handshake)
  Version: 2 bytes
  Length: 2 bytes
  Payload:
    Handshake type: 0x01 (ClientHello)
    Length: 3 bytes
    Client version: 2 bytes
    Client random: 32 bytes
    Session ID: length-prefixed
    Cipher suites: length-prefixed
    Compression methods: length-prefixed
    Extensions: length-prefixed
      Each extension:
        Type: 2 bytes (0x0000 = server_name)
        Length: 2 bytes
        Data: server_name_list (for type 0x0000)
          List length: 2 bytes
          Entry type: 1 byte (0x00 = host_name)
          Name length: 2 bytes
          Name: UTF-8 string ← this is the SNI
```

**Read limit**: The parser reads at most 16 KB from the connection. This is far larger than any realistic ClientHello. If parsing fails or the record is not a TLS handshake, the connection is tunneled using `SO_ORIGINAL_DST` as the destination.

### SO_ORIGINAL_DST

When iptables REDIRECT changes a packet's destination, the kernel records the original destination address. fpsd recovers it using the `SO_ORIGINAL_DST` socket option (`getsockopt` with `SOL_IP` / `SO_ORIGINAL_DST`).

This is Linux-specific. The implementation uses build tags:

- `origdst_linux.go`: Real implementation using `syscall.GetsockoptIPv6Mreq` or raw `syscall.Getsockopt`
- `origdst_stub.go`: Stub for non-Linux platforms (returns an error)

`SO_ORIGINAL_DST` is used as a fallback when the primary destination discovery mechanism (Host header for HTTP, SNI for HTTPS) is unavailable. It is also used to determine the original destination port when it might not be the standard port (80/443).

### Connection Wrapper

For MITM in transparent mode, the proxy has already read bytes from the connection (the ClientHello peek). These bytes need to be "unread" so the TLS handshake in `MITMInterceptor.Handle()` sees the complete ClientHello.

```go
// prefixConn wraps a net.Conn, prepending buffered bytes before
// reads from the underlying connection. Used to replay peeked
// TLS ClientHello bytes.
type prefixConn struct {
    net.Conn
    reader io.Reader
}

func newPrefixConn(conn net.Conn, prefix []byte) net.Conn {
    return &prefixConn{
        Conn:   conn,
        reader: io.MultiReader(bytes.NewReader(prefix), conn),
    }
}

func (c *prefixConn) Read(b []byte) (int, error) {
    return c.reader.Read(b)
}
```

This is the same pattern used by HTTP servers that peek at the first bytes to detect TLS vs. plaintext.

### Config

```yaml
transparent:
  enabled: false
  http_addr: ":18780"    # Transparent HTTP listener address
  https_addr: ":18443"   # Transparent HTTPS listener address
```

**Defaults**:

| Field | Default | Description |
| ----- | ------- | ----------- |
| `enabled` | `false` | Transparent mode is off by default |
| `http_addr` | `":18780"` | Listens for redirected port 80 traffic |
| `https_addr` | `":18443"` | Listens for redirected port 443 traffic |

**Port rationale**: 18780 echoes port 80, 18443 echoes port 443. Both are in the unprivileged range and follow the project's 187xx convention.

**Validation**:

- Both addresses must be valid TCP addresses (same validation as `listen`)
- Transparent ports must not conflict with the explicit proxy listen port
- If `transparent.enabled` is true, at least one of `http_addr` or `https_addr` must be non-empty (but either can be set to `""` to disable that protocol)

### iptables Rules

fpsd does not manage iptables rules. The user configures them separately. The documentation provides reference rules for common scenarios.

**Gateway scenario** (fpsd machine is the network's default gateway):

```bash
# Variables — adjust for your environment
PROXY_UID=1000          # UID of the user running fpsd
LAN_IF=eth0             # LAN-facing network interface
THTTP=18780             # fpsd transparent HTTP port
THTTPS=18443            # fpsd transparent HTTPS port

# Redirect LAN clients' HTTP and HTTPS traffic to fpsd
iptables -t nat -A PREROUTING -i $LAN_IF -p tcp --dport 80  -j REDIRECT --to-port $THTTP
iptables -t nat -A PREROUTING -i $LAN_IF -p tcp --dport 443 -j REDIRECT --to-port $THTTPS

# Prevent fpsd's own outbound connections from being redirected (loop prevention)
iptables -t nat -A OUTPUT -m owner --uid-owner $PROXY_UID -p tcp --dport 80  -j RETURN
iptables -t nat -A OUTPUT -m owner --uid-owner $PROXY_UID -p tcp --dport 443 -j RETURN
```

**Requirements for the gateway scenario**:

- IP forwarding enabled: `sysctl net.ipv4.ip_forward=1`
- Client devices use the fpsd machine as their default gateway (DHCP or static route)
- fpsd runs as a known UID (for the `--uid-owner` loop prevention rule)

**Cleanup**:

```bash
iptables -t nat -D PREROUTING -i $LAN_IF -p tcp --dport 80  -j REDIRECT --to-port $THTTP
iptables -t nat -D PREROUTING -i $LAN_IF -p tcp --dport 443 -j REDIRECT --to-port $THTTPS
iptables -t nat -D OUTPUT -m owner --uid-owner $PROXY_UID -p tcp --dport 80  -j RETURN
iptables -t nat -D OUTPUT -m owner --uid-owner $PROXY_UID -p tcp --dport 443 -j RETURN
```

### Stats

Transparent connections feed into the existing stats system. The stats endpoint gains a `transparent` section:

```json
{
  "transparent": {
    "enabled": true,
    "http_requests": 1234,
    "https_tunnels": 5678,
    "https_mitm": 89,
    "blocked": 45,
    "sni_missing": 2
  }
}
```

- `http_requests`: Total HTTP requests handled in transparent mode
- `https_tunnels`: Total HTTPS connections tunneled (non-MITM)
- `https_mitm`: Total HTTPS connections handled via MITM
- `blocked`: Total connections blocked (HTTP + HTTPS)
- `sni_missing`: Total HTTPS connections where SNI was absent (diagnostic counter)

Per-domain stats (requests, blocks, allows, MITM intercepts) use the same counters as explicit mode. A request to `www.reddit.com` through transparent mode increments the same domain counter as a request through the explicit proxy.

### Heartbeat

The heartbeat response gains:

```json
{
  "transparent_enabled": true,
  "transparent_http": ":18780",
  "transparent_https": ":18443"
}
```

### Logging

**Startup** (info level):

- Transparent mode enabled/disabled
- HTTP listener address, HTTPS listener address
- If transparent enabled but MITM not configured: info message noting that HTTPS MITM domains will be tunneled only

**Per-connection** (info level):

- Transparent HTTP: `transparent http domain=example.com method=GET url=/path status=200 remote=192.168.1.5`
- Transparent HTTPS tunnel: `transparent tunnel domain=example.com remote=192.168.1.5`
- Transparent HTTPS MITM: `transparent mitm domain=www.reddit.com remote=192.168.1.5` (then MITM handler logs individual requests as usual)
- Transparent blocked: `transparent blocked domain=ads.example.com remote=192.168.1.5 proto=https`

**SNI parsing** (debug level):

- SNI extracted: `sni extracted domain=example.com bytes_read=245`
- SNI missing: `sni missing, falling back to original destination remote=192.168.1.5 origdst=93.184.216.34:443`
- Parse error: `sni parse failed error=<detail> remote=192.168.1.5`

## File Changes

| File | Change |
| ---- | ------ |
| `internal/transparent/listener.go` | New — Transparent HTTP and HTTPS listeners, connection dispatch, HTTP forwarding |
| `internal/transparent/sni.go` | New — TLS ClientHello parser, SNI extraction |
| `internal/transparent/origdst_linux.go` | New — SO_ORIGINAL_DST implementation (Linux, build-tagged) |
| `internal/transparent/origdst_stub.go` | New — Stub for non-Linux platforms (build-tagged) |
| `internal/transparent/conn.go` | New — prefixConn wrapper for replaying peeked bytes |
| `internal/transparent/transparent_test.go` | New — Tests for SNI parsing, HTTP forwarding, HTTPS tunneling, MITM delegation |
| `internal/config/config.go` | Add `Transparent` struct with `Enabled`, `HTTPAddr`, `HTTPSAddr` fields; validation |
| `internal/config/config_test.go` | Test transparent config parsing and validation |
| `internal/stats/collector.go` | Add transparent mode counters |
| `internal/probe/probe.go` | Add `transparent` section to stats response, heartbeat fields |
| `cmd/fpsd/main.go` | Start transparent listeners when enabled, pass dependencies, graceful shutdown |
| `fpsd.yml` | Add commented `transparent:` section with defaults |

## Acceptance Criteria

- [ ] `transparent` config section parsed from `fpsd.yml` (enabled, http_addr, https_addr)
- [ ] Config validation rejects invalid transparent addresses
- [ ] Config validation rejects transparent ports that conflict with explicit proxy port
- [ ] Transparent mode disabled by default (no behavior change without opt-in)
- [ ] Transparent HTTP listener accepts connections and forwards based on Host header
- [ ] Transparent HTTP: blocklist check works, blocked domains get 403
- [ ] Transparent HTTP: allowlist overrides apply
- [ ] Transparent HTTP: requests forwarded to correct upstream with correct headers
- [ ] Transparent HTTP: hop-by-hop headers stripped
- [ ] Transparent HTTPS listener accepts connections and extracts SNI from ClientHello
- [ ] SNI parser handles standard TLS 1.2 and TLS 1.3 ClientHello messages
- [ ] SNI parser handles ClientHello with no SNI extension (returns empty string)
- [ ] SNI parser handles malformed records without crashing (returns error, connection tunneled)
- [ ] Transparent HTTPS: blocked domains cause connection close
- [ ] Transparent HTTPS non-MITM: connection tunneled to upstream with ClientHello replayed
- [ ] Transparent HTTPS MITM: connection delegated to existing MITMInterceptor.Handle()
- [ ] prefixConn correctly replays buffered bytes before reading from underlying connection
- [ ] SO_ORIGINAL_DST recovers original destination on Linux (build-tagged)
- [ ] SO_ORIGINAL_DST stub returns error on non-Linux (build-tagged)
- [ ] Fallback to SO_ORIGINAL_DST when Host header (HTTP) or SNI (HTTPS) is missing
- [ ] Stats: transparent counters (http_requests, https_tunnels, https_mitm, blocked, sni_missing) tracked
- [ ] Stats: per-domain counters shared with explicit mode (same domain = same counter)
- [ ] Heartbeat shows transparent_enabled, transparent_http, transparent_https
- [ ] Logging: startup, per-connection, SNI extraction at appropriate levels
- [ ] Explicit proxy continues to work unchanged when transparent mode is enabled
- [ ] Management endpoints (`/fps/*`) remain accessible only through explicit proxy
- [ ] All existing tests pass (no regression)
- [ ] New tests: SNI parsing (valid ClientHello, missing SNI, malformed data)
- [ ] New tests: transparent HTTP forwarding (normal request, blocked domain, missing Host)
- [ ] New tests: transparent HTTPS tunneling (non-MITM domain)
- [ ] New tests: transparent HTTPS MITM delegation
- [ ] New tests: prefixConn byte replay
- [ ] Verified locally: transparent HTTP forwarding works with iptables REDIRECT
- [ ] Verified locally: transparent HTTPS tunneling works with iptables REDIRECT
- [ ] fpsd.yml updated with commented transparent section

## Out of Scope

- macOS/pf transparent proxy support (Linux iptables only)
- TPROXY socket option (REDIRECT/NAT only — simpler, sufficient for gateway use case)
- Automatic iptables rule management by fpsd (user configures rules separately)
- IPv6 / ip6tables support (IPv4 only for initial implementation)
- HTTP/2 or QUIC in transparent mode
- Transparent mode on non-Linux platforms (build stubs provided)
- Non-standard destination ports (only port 80 → HTTP, port 443 → HTTPS)
- Handling connections where both SNI and SO_ORIGINAL_DST fail (connection closed with log)
- PAC file generation or WPAD support

## Security Considerations

Transparent proxying introduces additional security surface:

- **Network position**: fpsd must be the network gateway for transparent mode. This means it handles all HTTP/HTTPS traffic for connected devices — a privileged position. Compromise of the fpsd machine compromises all traffic.
- **Loop prevention**: Without proper iptables rules (the `--uid-owner` RETURN rule), fpsd's outbound connections to upstream servers would be redirected back to itself, creating an infinite loop. The documentation emphasizes this.
- **SO_ORIGINAL_DST trust**: The original destination from the kernel is trusted. An attacker who can manipulate iptables rules on the gateway could redirect traffic to arbitrary destinations. This is inherent to the gateway model and not specific to fpsd.
- **SNI privacy**: In transparent HTTPS tunnel mode, fpsd reads the SNI from unencrypted ClientHello. This is the same information visible to any network observer. fpsd does not log SNI at info level for tunneled connections to avoid creating a browsing history log by default (debug level only for SNI extraction details).
- **Missing SNI fallback**: When SNI is absent, the connection is tunneled to the original destination IP without domain-based checks. This is the conservative choice — blocking unknown traffic would break legitimate connections. The `sni_missing` counter provides visibility.
- **MITM scope unchanged**: Transparent mode does not expand which domains are MITM'd. The same `mitm.domains` config controls interception in both modes. A device behind transparent proxy gets the same MITM treatment as one using explicit proxy.
