# Spec 024: Transparent CONNECT Handling and Blocked-Dial Noise Reduction

**Status**: COMPLETE
**Created**: 2026-05-29
**Depends on**: Spec 001 (proxy foundation), Spec 010 (transparent proxying)

> **Note**: This work has no associated issue tracker ticket. Consider creating one for traceability.

---

## Problem Statement

Log review of the running `fpsd` service (5 days uptime, no crashes) surfaced two recurring error patterns in the request-handling paths. Both originate inside `fpsd`, not the network.

### 1. Transparent HTTP listener drops the CONNECT method (267 occurrences)

The dominant error is `transparent http response read failed` for `proxy-safebrowsing.googleapis.com` with `unexpected EOF`, appearing in regular bursts. Tracing it:

A client (Apple Safe Browsing) sends an HTTP `CONNECT proxy-safebrowsing.googleapis.com:443` request that iptables redirects to the transparent HTTP (port 80) listener. `handleHTTP` has no branch for the CONNECT method, so it treats the request as an ordinary forward:

1. `host := req.Host` → `proxy-safebrowsing.googleapis.com:443`, logged domain `proxy-safebrowsing.googleapis.com`.
2. The authority already contains a port, so no `:80` is appended — the handler dials `:443`.
3. `req.Write(upConn)` writes the plaintext `CONNECT …` request line into a TLS endpoint.
4. The TLS server cannot parse plaintext and closes the socket, so `http.ReadResponse` returns `unexpected EOF`.
5. `fpsd` never returns `200 Connection Established` and never establishes a tunnel.

The smoking gun is in the logs: each failure is followed ~80 ms later by a successful direct HTTPS tunnel to `safebrowsing.googleapis.com` — the client gives up on the proxy CONNECT and falls back to a direct connection. The explicit-proxy path (`internal/proxy/proxy.go` `handleConnect`) already handles CONNECT correctly (dial authority, write `200 Connection Established`, relay). The transparent path is simply missing the equivalent branch.

### 2. Dials to unspecified addresses are logged at ERROR (46 occurrences)

The host runs a blocking resolver that returns `0.0.0.0` (and `::` for IPv6) for ad/telemetry domains. When a client requests such a domain, `fpsd` resolves it, dials `0.0.0.0:443` / `[::]:443`, and the connection is refused. The outcome is benign — the domain is already blocked at the resolver — but `fpsd` logs the inevitable refusal at ERROR (`connect tunnel failed`, `transparent tunnel dial failed`) and wastes a dial attempt on a meaningless address.

---

## Approach

### Fix 1: Handle CONNECT in the transparent HTTP listener

Add a CONNECT branch to `handleHTTP`. When `req.Method == http.MethodConnect`, delegate to a new `handleConnect(conn, req, clientIP)` method that mirrors the existing transparent HTTPS tunnel and the explicit-proxy `handleConnect`:

1. Derive `domain` from `req.Host` (authority form). Default to port `443` if the authority carries no port.
2. Blocklist check — on block, write an HTTP `403` to the client, fire `OnRequest(blocked=true)` and `OnTransparentBlock`, and close.
3. Dial the upstream authority.
4. On dial failure, write an HTTP `502` and return (with the blocked-dial downgrade from Fix 2 applied).
5. Write `HTTP/1.1 200 Connection Established\r\n\r\n` to the client.
6. Relay bytes bidirectionally using the same `io.Copy` + `CloseWrite` pattern as `handleHTTPS`, tracking byte counts.
7. Account via `OnRequest(clientIP, domain, false, 0, 0)` and `OnTunnelClose(clientIP, up, down)` — identical accounting to the explicit-proxy CONNECT path. No new stat counters are introduced.

### Fix 2: Short-circuit dials to unspecified addresses

Add a shared helper `internal/netutil.IsUnspecifiedDialError(err) bool` that unwraps a dial error to a `*net.OpError` and reports whether its address is an unspecified IP (`0.0.0.0` or `::`, via `net.IP.IsUnspecified`). At every upstream dial-failure site (`proxy.handleConnect`, transparent `handleHTTP`, transparent `handleHTTPS`, and the new transparent `handleConnect`), when the helper matches, log at DEBUG with a "DNS-blocked" message instead of ERROR. All other dial failures continue to log at ERROR unchanged.

This is evaluated only on the dial-error path (rare), so it adds no DNS lookups to the success path. `IsUnspecified` covers both IPv4 `0.0.0.0` and IPv6 `::`.

### Fix 3: Extract a shared bidirectional relay helper

Fix 1's transparent `handleConnect` reuses the same bidirectional byte-relay pattern (`io.Copy` pair + `CloseWrite` half-close + byte counters) already present in transparent `handleHTTPS` and the explicit proxy `handleConnect` — three copies. Rather than leave the duplication, extract `internal/relay.Bidirectional(client, upstream) (fromClient, fromUpstream int64)` and adopt it at all three sites. The helper half-closes each destination's write side on completion (via `CloseWrite` when supported), blocks until both directions finish, returns the byte counts, and does not close either connection (callers own lifetime via `defer`). The explicit proxy retains its non-blocking behaviour by calling the helper inside the existing tunnel goroutine. This is a Phase 4 (code quality) deduplication, prioritised over deferral so the duplication does not persist.

---

## Scope

### In Scope

- CONNECT method handling in the transparent HTTP listener (`internal/transparent`).
- `internal/netutil` package with `IsUnspecifiedDialError`.
- DEBUG-level downgrade of unspecified-address dial failures at all four upstream dial sites.
- `internal/relay` package with `Bidirectional`, adopted by transparent `handleConnect`, transparent `handleHTTPS`, and explicit proxy `handleConnect` (deduplication).
- Unit tests for the new CONNECT path, the dial-error helper, and the relay helper.

### Out of Scope

- Any change to the explicit-proxy CONNECT logic beyond adopting the dial-error downgrade.
- New stat counters or dashboard changes (CONNECT tunnels reuse existing `OnRequest`/`OnTunnelClose` accounting).
- MITM of transparently-CONNECTed traffic (tunnels remain opaque byte streams).
- Changing the host's resolver behavior or `fpsd`'s own blocklist contents.
- The benign MITM TLS-handshake-EOF and STUN-over-TCP timeout log lines (client-side / upstream behavior, not `fpsd` bugs).

---

## Design

### Package Structure

| File | Change |
|------|--------|
| `internal/netutil/netutil.go` | New — `IsUnspecifiedDialError(err error) bool` |
| `internal/netutil/netutil_test.go` | New — unit tests for the helper |
| `internal/relay/relay.go` | New — `Bidirectional(client, upstream) (fromClient, fromUpstream int64)` |
| `internal/relay/relay_test.go` | New — unit tests for the relay helper |
| `internal/transparent/listener.go` | Add CONNECT branch + `handleConnect`; downgrade at two dial sites; adopt `relay.Bidirectional` in `handleConnect` and `handleHTTPS` |
| `internal/transparent/connect_test.go` | New — CONNECT tunnel and blocked-CONNECT tests |
| `internal/proxy/proxy.go` | Downgrade at the `handleConnect` dial site; adopt `relay.Bidirectional` |

### `handleConnect` (transparent)

```go
func (l *Listener) handleConnect(conn net.Conn, req *http.Request, clientIP string) {
    host := req.Host
    if !strings.Contains(host, ":") {
        host += ":443"
    }
    domain := stripPort(host)

    if l.cfg.Blocker != nil && l.cfg.Blocker.IsBlocked(domain) {
        writeHTTPError(conn, http.StatusForbidden, "blocked by proxy")
        // OnRequest(blocked) + OnTransparentBlock, log, return
    }

    upConn, err := net.DialTimeout("tcp", host, l.cfg.ConnectTimeout)
    if err != nil {
        if netutil.IsUnspecifiedDialError(err) { /* Debug */ } else { /* Error */ }
        writeHTTPError(conn, http.StatusBadGateway, "upstream connection failed")
        return
    }
    defer upConn.Close()

    _, _ = conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
    // OnRequest(allowed); bidirectional relay (mirror handleHTTPS); OnTunnelClose
}
```

`handleHTTP` gains, immediately after parsing the request:

```go
if req.Method == http.MethodConnect {
    l.handleConnect(conn, req, clientIP)
    return
}
```

### `IsUnspecifiedDialError`

```go
// internal/netutil/netutil.go
func IsUnspecifiedDialError(err error) bool {
    var opErr *net.OpError
    if errors.As(err, &opErr) {
        if tcpAddr, ok := opErr.Addr.(*net.TCPAddr); ok {
            return tcpAddr.IP.IsUnspecified()
        }
    }
    return false
}
```

---

## Test Strategy

### Automated (Go tests)

- **`IsUnspecifiedDialError`**: table-driven — synthetic `*net.OpError` with `0.0.0.0` and `::` TCP addrs (true), with a routable addr (false), a non-OpError (false), and a wrapped OpError via `fmt.Errorf("%w")` (true).
- **Transparent CONNECT success**: start a real `127.0.0.1` echo listener; drive `handleHTTP` over a `net.Pipe`; send `CONNECT 127.0.0.1:<port>`; assert `200 Connection Established`, then assert bytes echo through the tunnel and `OnTunnelClose` reports non-zero counts.
- **Transparent CONNECT blocked**: configure a `Blocker` that blocks the target; assert a `403` reply, `OnRequest(blocked=true)` fired, and no tunnel established.

These exercise the real socket path (a live loopback listener as the downstream), satisfying the integration-boundary expectation for the new connection-handling code rather than relying on mocks alone.

### Manual Verification

- Tail `journalctl --user -u fpsd`; confirm `transparent http response read failed` for `proxy-safebrowsing.googleapis.com` no longer appears and is replaced by successful `connect`/tunnel log lines.
- Confirm `0.0.0.0` / `[::]` dial failures no longer appear at ERROR level.

---

## Acceptance Criteria

- [x] `handleHTTP` detects `req.Method == http.MethodConnect` and delegates to `handleConnect`. (listener.go:181)
- [x] Transparent `handleConnect` returns `HTTP/1.1 200 Connection Established` and relays bytes bidirectionally for an allowed destination. (listener.go:289, reply at :325, relay at :339-360; test `TestHandleConnect_AllowedTunnel`)
- [x] Transparent `handleConnect` returns HTTP `403` and fires `OnRequest(blocked=true)` + `OnTransparentBlock` for a blocked destination, with no tunnel established. (listener.go:297-308; test `TestHandleConnect_BlockedDomain`)
- [x] Transparent CONNECT tunnels are accounted via `OnRequest` and `OnTunnelClose` (no new stat counters). (listener.go:334, :363)
- [x] `internal/netutil.IsUnspecifiedDialError` returns true for `0.0.0.0` and `::` dial `*net.OpError`s (including wrapped) and false otherwise. (netutil.go:17; test `TestIsUnspecifiedDialError`)
- [x] Unspecified-address dial failures are logged at DEBUG (not ERROR) at all four upstream dial sites (proxy `handleConnect`, transparent `handleHTTP`, transparent `handleHTTPS`, transparent `handleConnect`). (proxy.go:329, listener.go:228, :453, :313)
- [x] New tests cover the CONNECT success path, the blocked-CONNECT path, and the helper; the success/blocked tests drive a real loopback listener. (connect_test.go `startEchoServer` + two tests; netutil_test.go)
- [x] `internal/relay.Bidirectional` extracted and adopted by transparent `handleConnect`, transparent `handleHTTPS`, and explicit proxy `handleConnect`; covered by `relay_test.go` (byte counts both directions + empty tunnel). (relay.go:35; listener.go:337,:454; proxy.go:379)
- [x] `make test` passes.
- [x] `make lint` passes.
- [x] `go vet ./...` passes.

---

## Risks & Assumptions

- **Rollback**: Pure additive change (new package, new method, new branch, log-level adjustments). Revert the commit to restore prior behavior in minutes. No data migration, no config change, no on-disk format change.
- **Assumption**: Clients sending transparent CONNECT expect standard `HTTP/1.1 200 Connection Established` tunnel semantics (RFC 7231 §4.3.6) — the same contract the explicit proxy already serves.
- **Assumption**: `net.DialTimeout` surfaces unspecified-address failures as `*net.OpError` with a `*net.TCPAddr` address; verified against the observed `dial tcp 0.0.0.0:443: connect: connection refused` errors.
- **Behavioral claim caveat**: The Apple Safe Browsing CONNECT root cause is inferred from log correlation, not a packet capture. The fix is protocol-correct regardless of the specific client; manual verification confirms the error class disappears.
- **Security**: Connection-level tunneling only; no new TLS interception, no new secret handling, no new logging of request bodies or credentials. CONNECT targets are subject to the same blocklist check as every other path.

---

## Alternatives Considered

- **Pre-resolve every host and short-circuit unspecified IPs before dialing** — rejected; it adds a DNS lookup to every connection's success path to handle a rare, already-benign case. Inspecting the dial error instead costs nothing on the hot path.
- **Duplicate the unspecified-address helper in both packages** — rejected in favor of a shared `internal/netutil` package; both `proxy` and `transparent` need it, and duplication would drift.
- **Defer the relay-helper extraction to spec 023 (SOCKS5/FTP)** — rejected. Spec 023 is on hold, so deferral would leave three copies of the relay loop persisting indefinitely. The duplication is extracted now (Fix 3) rather than carried; spec 023 can adopt the same `internal/relay` helper when revived.
