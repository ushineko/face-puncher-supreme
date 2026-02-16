# Spec 006: Per-Domain TLS Interception (MITM)

**Status**: COMPLETE
**Created**: 2026-02-16
**Depends on**: Spec 001 (proxy foundation), Spec 003 (config file)

## Problem Statement

Domain-level blocking handles ads served from dedicated ad domains (e.g., `news.iadsdk.apple.com` for Apple News). However, some sites serve ads from the same domain as content. Reddit serves promoted posts from `www.reddit.com` — indistinguishable from regular content at the domain level. Blocking the domain blocks everything; allowing it allows ads.

To filter same-domain ads, the proxy needs to inspect HTTP response content. This requires TLS interception (MITM): terminating the client's TLS connection with a proxy-generated certificate, inspecting the plaintext HTTP traffic, and forwarding to the upstream server over a separate TLS connection.

MITM is expensive and invasive. It must be configured explicitly per-domain. Non-MITM domains continue to use opaque TCP tunnels with zero overhead. The proxy never intercepts a domain unless the user has explicitly listed it.

### Why Reddit Is the First Target

From macOS agent guide testing (2026-02-16):

- Reddit ads and content share the same origin domain — the exact scenario domain blocking cannot handle
- Promoted posts have structural markers in HTML/JSON (`promoted`, `is_promoted`, ad labels) that content inspection could target
- Reddit is not an Apple app — no certificate pinning concerns
- Reddit in Safari uses standard HTTPS through the proxy (unlike Apple News which uses QUIC), making it the path of least resistance for MITM development

## Approach

### CA Certificate Management

The proxy uses a self-signed Certificate Authority (CA) to sign leaf certificates on the fly. Users must install and trust this CA on their devices for MITM to work.

**Generation**: `fpsd generate-ca` subcommand creates the CA certificate and private key.

```bash
fpsd generate-ca [--data-dir DIR]

# Creates:
#   <data-dir>/ca-cert.pem  — CA certificate (distribute to clients)
#   <data-dir>/ca-key.pem   — CA private key (keep secret)
```

**CA certificate attributes**:

| Attribute | Value |
| --------- | ----- |
| Key algorithm | ECDSA P-256 |
| Subject | CN=Face Puncher Supreme CA |
| Validity | 10 years |
| Basic Constraints | CA:TRUE, pathlen:0 |
| Key Usage | Certificate Sign |
| Serial | Random 128-bit |

**Overwrite protection**: If CA files already exist, `generate-ca` refuses and prints a message. Use `--force` to overwrite. This prevents accidental regeneration, which would invalidate all client-installed CAs.

**Distribution**: The CA certificate is served at `/fps/ca.pem` so devices on the network can download and install it without file transfer. Returns PEM-encoded certificate with `Content-Type: application/x-pem-file`.

### Config

```yaml
mitm:
  ca_cert: "ca-cert.pem"     # Path to CA certificate (relative to data_dir)
  ca_key: "ca-key.pem"       # Path to CA private key (relative to data_dir)
  domains:                    # Domains to intercept (exact match only)
    - www.reddit.com
    - old.reddit.com
```

**Defaults**: `ca_cert` and `ca_key` default to `ca-cert.pem` and `ca-key.pem` in `data_dir`. If `mitm.domains` is empty or absent, MITM is completely disabled — current behavior preserved, CA files not loaded.

**Startup validation**:

- `mitm.domains` non-empty but CA files missing → fatal error with message to run `fpsd generate-ca`
- Domain appears in both `mitm.domains` and the blocklist → warning at startup (blocked domains are never reached for MITM; remove from blocklist if MITM is intended)
- Domain entries validated: no wildcards, no paths, no spaces (same rules as inline blocklist)
- Domains stored lowercase

### Dynamic Leaf Certificate Generation

When the proxy intercepts a CONNECT request for a MITM domain, it generates a TLS certificate for that domain on the fly, signed by the CA.

**Leaf certificate attributes**:

| Attribute | Value |
| --------- | ----- |
| Key algorithm | ECDSA P-256 (new key per domain) |
| Subject | CN=\<target domain\> |
| SAN (DNS) | \<target domain\> |
| Validity | 24 hours |
| Signed by | The proxy CA |
| Key Usage | Digital Signature |
| Extended Key Usage | Server Authentication |
| Serial | Random 128-bit |

**Caching**: Leaf certificates are cached in memory keyed by domain (`map[string]*tls.Certificate`). Before returning a cached cert, check expiry — if less than 1 hour remains, regenerate. With a small number of MITM domains (expected <20), cache size is trivial.

### CONNECT Flow Change

Current flow (all CONNECT requests):

```
Client CONNECT host:443 → Extract domain
  → Check blocklist → 403 if blocked
  → Dial upstream TCP
  → Hijack client connection
  → Send "200 Connection Established"
  → Bidirectional byte copy (opaque tunnel)
```

New flow:

```
Client CONNECT host:443 → Extract domain
  → Check blocklist → 403 if blocked
  → Check mitm.domains
  ├─ NOT in MITM list → existing tunnel (dial, hijack, byte copy)
  └─ IN MITM list → MITM interception:
       → Hijack client connection
       → Send "200 Connection Established"
       → Generate/cache leaf cert for domain
       → TLS handshake with client (proxy presents leaf cert)
       → Dial upstream, TLS handshake with real server
       → HTTP proxy loop (request → forward → response → forward)
       → Close both sides when done
```

The blocklist check still happens first. A domain in the blocklist is blocked regardless of MITM config.

### HTTP Proxy Loop

Once TLS is established on both sides, the proxy has two plaintext HTTP streams:

```
Client ←TLS→ Proxy ←TLS→ Upstream
       HTTP/1.1      HTTP/1.1
```

The proxy loop:

1. Read HTTP request from client (via `http.ReadRequest`)
2. Strip hop-by-hop headers (`Connection`, `Proxy-Connection`, `Keep-Alive`, `Transfer-Encoding`, `TE`, `Trailer`, `Upgrade`)
3. Forward request to upstream (via `Request.Write`)
4. Read HTTP response from upstream (via `http.ReadResponse`)
5. If a `ResponseModifier` is registered and the response Content-Type is text-based, buffer the body and call the modifier
6. Strip hop-by-hop headers from response
7. Forward response to client (via `Response.Write`)
8. Repeat until either side closes the connection or sends `Connection: close`

**Protocol**: HTTP/1.1 only. The TLS handshake with the client sets no ALPN protocols, forcing HTTP/1.1 negotiation. HTTP/2 support is out of scope.

**Buffered readers**: The `bufio.Reader` for each side is created once per MITM session and reused across request-response cycles.

### Response Modifier Hook

This spec establishes the MITM infrastructure. Content filtering is a follow-up spec. The MITM handler accepts an optional modifier:

```go
// ResponseModifier may inspect or modify an HTTP response body during MITM.
// It is called only for text-based Content-Types (text/*, application/json,
// application/javascript). Binary responses (images, video, fonts) stream
// through unmodified.
//
// The modifier receives the domain, the original request (for URL/header
// context), the response (for status/header context), and the response body.
// It returns a (possibly modified) body. Returning the input body unchanged
// is a no-op passthrough.
//
// If nil, all responses stream through without buffering.
type ResponseModifier func(domain string, req *http.Request, resp *http.Response, body []byte) ([]byte, error)
```

**Buffering behavior**:

- When `ResponseModifier` is nil (this spec): all responses stream through unbuffered via `io.Copy`. No memory overhead.
- When `ResponseModifier` is set (future spec): text-based responses are buffered into memory, passed to the modifier, then written to the client. Binary responses still stream through.

**Why define the hook now**: So the MITM handler architecture supports filtering from the start. Adding filtering later is a matter of registering a modifier function, not restructuring the handler.

### Stats

Add MITM visibility to the stats endpoint (`/fps/stats`):

```json
{
  "mitm": {
    "enabled": true,
    "intercepts_total": 42,
    "domains_configured": 2,
    "top_intercepted": [
      {"domain": "www.reddit.com", "count": 38},
      {"domain": "old.reddit.com", "count": 4}
    ]
  }
}
```

- `enabled`: Whether MITM is configured
- `intercepts_total`: Total MITM'd HTTP request-response cycles (not CONNECT count — one CONNECT may carry many HTTP requests)
- `domains_configured`: Number of entries in `mitm.domains`
- `top_intercepted`: Top 10 MITM'd domains by HTTP request count

The stats collector gains new MITM counters, persisted to `stats.db` alongside existing block/allow stats.

### Management Endpoints

New endpoint:

| Endpoint | Method | Description |
| -------- | ------ | ----------- |
| `/fps/ca.pem` | GET | Download CA certificate (PEM format) |

Returns 404 if MITM is not configured (no CA loaded).

The heartbeat response gains:

```json
{
  "mitm_enabled": true,
  "mitm_domains": 2
}
```

### Logging

MITM adds complexity that is invisible at the network level. Logging must provide enough context to diagnose every failure mode without requiring a debugger.

**Startup** (info level):

- MITM enabled/disabled, number of configured domains, list of domains
- CA certificate loaded: path, SHA-256 fingerprint, expiry date
- If CA expires within 30 days: warning with expiry date

**Per-MITM session** (info level):

- Session start: `mitm session start domain=www.reddit.com client=192.168.1.5`
- Session end: `mitm session end domain=www.reddit.com requests=12 duration_ms=34521 bytes_in=4820 bytes_out=287331`

**Per-MITM request** (verbose/debug level):

- Request forwarded: `mitm request domain=www.reddit.com method=GET url=/r/all status=200 content_type=text/html body_bytes=84210 duration_ms=245`
- This logs after the full request-response cycle completes, with upstream response status

**Error paths** — every error logs the domain, client IP, and what phase failed:

| Error | Log level | Context fields |
| ----- | --------- | -------------- |
| Leaf cert generation failed | Error | domain, error detail |
| Client TLS handshake failed | Warn | domain, client IP, error (likely: CA not trusted) |
| Upstream TCP dial failed | Error | domain, client IP, upstream host:port, error, timeout used |
| Upstream TLS handshake failed | Error | domain, client IP, error (likely: cert validation failure) |
| HTTP request read failed from client | Debug | domain, client IP, error, requests completed so far |
| HTTP request write to upstream failed | Error | domain, client IP, method, URL, error |
| HTTP response read failed from upstream | Error | domain, client IP, method, URL, error |
| HTTP response write to client failed | Warn | domain, client IP, method, URL, error (client may have disconnected) |

**Rationale for log levels**:

- Client TLS handshake failure is `Warn` not `Error` — the most common cause is the user hasn't installed the CA yet. This is expected during setup and shouldn't look like a proxy bug.
- Client-side read errors and response write failures are lower severity because the client disconnecting mid-session is normal browser behavior (tab closed, navigation).
- Upstream failures are `Error` because they indicate a real connectivity or TLS problem that needs investigation.

### Upstream TLS

The proxy connects to the real upstream server using standard TLS:

- Verify upstream certificate against system root CAs (standard `tls.Config` defaults)
- SNI set to the target domain
- ALPN: negotiate `http/1.1` only (matching our client-side constraint)
- Use the proxy's existing `connectTimeout` for the TCP dial

## File Changes

| File | Change |
| ---- | ------ |
| `internal/mitm/ca.go` | New — CA generation (`GenerateCA`), loading (`LoadCA`), PEM read/write |
| `internal/mitm/cert.go` | New — Dynamic leaf cert generation with in-memory cache |
| `internal/mitm/handler.go` | New — MITM session handler (TLS setup, HTTP proxy loop, response modifier hook) |
| `internal/mitm/mitm_test.go` | New — Tests for CA gen, leaf cert gen, MITM proxy flow |
| `internal/config/config.go` | Add `MITM` struct with `CACert`, `CAKey`, `Domains` fields |
| `internal/config/config_test.go` | Test MITM config parsing and validation |
| `internal/proxy/proxy.go` | Add MITM domain check in `handleConnect`, delegate to MITM handler |
| `internal/stats/collector.go` | Add MITM intercept counters (total + per-domain) |
| `internal/stats/db.go` | Persist MITM intercept stats |
| `internal/probe/probe.go` | Add MITM section to stats response, `mitm_enabled`/`mitm_domains` to heartbeat, `/fps/ca.pem` handler |
| `cmd/fpsd/main.go` | Add `generate-ca` subcommand, MITM handler initialization, CA loading |

## Acceptance Criteria

- [ ] `fpsd generate-ca` creates `ca-cert.pem` and `ca-key.pem` in data directory
- [ ] `generate-ca` refuses to overwrite existing files unless `--force` is passed
- [ ] Generated CA has correct attributes (ECDSA P-256, CA:TRUE, pathlen:0, 10yr validity)
- [ ] `mitm.domains` in `fpsd.yml` specifies which domains to intercept (exact match, lowercase)
- [ ] Config validation rejects invalid MITM domain entries (wildcards, paths, spaces)
- [ ] Proxy errors on startup if MITM domains configured but CA files are missing
- [ ] Warning logged at startup if a domain is in both `mitm.domains` and the blocklist
- [ ] Non-MITM CONNECT requests use existing opaque tunnel (no regression)
- [ ] MITM CONNECT requests: client receives `200 Connection Established`, then TLS handshake with proxy-generated cert
- [ ] Leaf certs are signed by the CA, have correct SAN, valid for 24 hours
- [ ] Leaf certs are cached in memory and reused across connections to the same domain
- [ ] Expired/near-expiry cached certs are regenerated
- [ ] HTTP requests forwarded correctly through MITM connection (method, URL, headers, body)
- [ ] HTTP responses forwarded correctly through MITM connection (status, headers, body)
- [ ] Multiple request-response cycles work on a single MITM connection (HTTP keep-alive)
- [ ] Hop-by-hop headers stripped in both directions
- [ ] HTTP/1.1 forced (no ALPN negotiation for HTTP/2)
- [ ] Upstream TLS validates server certificate against system roots
- [ ] `/fps/ca.pem` serves the CA certificate for download (404 when MITM disabled)
- [ ] Heartbeat shows `mitm_enabled` and `mitm_domains` fields
- [ ] Stats show `mitm.intercepts_total`, `mitm.domains_configured`, `mitm.top_intercepted`
- [ ] MITM intercept stats persisted to `stats.db`
- [ ] `ResponseModifier` hook exists and is nil by default (pure passthrough)
- [ ] When `ResponseModifier` is nil, responses stream through without buffering
- [ ] Blocked domains still return 403 regardless of MITM config
- [ ] All existing tests pass (no regression)
- [ ] New tests: CA generation, leaf cert generation, MITM proxy end-to-end flow
- [ ] Verified locally: Reddit pages load correctly through MITM proxy in Chromium with CA trusted
- [ ] Verified locally: non-MITM HTTPS sites load normally through proxy (no interception)

## Out of Scope

- Content filtering/modification rules (follow-up spec — this spec provides the infrastructure and hook)
- HTTP/2 support in MITM connections
- Certificate pinning bypass
- Auto-detection of which domains need MITM
- Wildcard/suffix matching for MITM domains (explicit per-domain only)
- Client certificate authentication to upstream
- MITM for plain HTTP requests (already inspectable without interception)
- Upstream certificate validation customization (use system defaults)
- CA certificate revocation (CRL/OCSP)
- Hot-reload of MITM domain list (restart required)

## Security Considerations

MITM interception is inherently security-sensitive. Mitigations:

- **Explicit opt-in**: MITM only activates when the user configures `mitm.domains` AND installs the CA on their device. No implicit interception.
- **CA key protection**: `ca-key.pem` file permissions should be 0600. The `generate-ca` command sets this. If compromised, an attacker could generate certs trusted by the user's device.
- **Scope minimization**: Only configured domains are intercepted. All other traffic remains in opaque tunnels.
- **Upstream validation**: The proxy validates the real upstream server's TLS certificate. If the upstream cert is invalid, the MITM session fails (no silent downgrade).
- **No credential logging**: The proxy does not log request/response bodies at info level. Verbose mode logs metadata (URL, status, content-type) but not bodies.

## Client CA Trust Setup

### Linux (Chromium — primary test environment)

Chromium uses the NSS certificate database. Install the CA with `certutil`:

```bash
# Download CA from proxy
curl -o fps-ca.pem http://localhost:18737/fps/ca.pem

# Install into NSS DB for Chromium (create DB if it doesn't exist)
mkdir -p ~/.pki/nssdb
certutil -d sql:$HOME/.pki/nssdb -A -t "C,," -n "Face Puncher Supreme CA" -i fps-ca.pem

# Verify it was installed
certutil -d sql:$HOME/.pki/nssdb -L

# To remove later:
# certutil -d sql:$HOME/.pki/nssdb -D -n "Face Puncher Supreme CA"
```

Restart Chromium after installing the CA. Launch Chromium with the proxy:

```bash
chromium --proxy-server=http://localhost:18737
```

### macOS (Safari — via agent guide)

After implementation, add a task to the macOS agent guide:

1. Download CA cert: `curl -o fps-ca.pem http://njv-cachyos.local:18737/fps/ca.pem`
2. Install in System Keychain: `sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain fps-ca.pem`
3. Browse Reddit through the proxy — verify pages load correctly (TLS interception working)
4. Verify non-MITM sites are unaffected (Apple News, CNN, etc.)
5. Check stats for MITM intercept counts

## Example Configurations

### Reddit MITM only (no blocklists)

```yaml
mitm:
  domains:
    - www.reddit.com
    - old.reddit.com
```

CA files default to `ca-cert.pem`/`ca-key.pem` in data directory. No domain blocking.

### Combined: blocking + MITM

```yaml
blocklist:
  - news.iadsdk.apple.com
  - news-events.apple.com
  - news-app-events.apple.com

allowlist:
  - registry.api.cnn.io
  - "*.cnn.io"

mitm:
  domains:
    - www.reddit.com
```

Apple News ads blocked at domain level. Reddit ads require MITM for content-level filtering (filtering rules in follow-up spec). CNN content allowed through.
