## Validation Report: Spec 026 — Browser-Shaped Upstream TLS ClientHello for MITM Sessions
**Date**: 2026-11-12 08:21
**Commit**: pre-commit
**Specs**: 026
**Status**: PASSED

### Phase 3: Tests
- Test suite: `make test` (`go test -race -short ./...`), system Go 1.27.3
- New tests (`internal/mitm/upstream_test.go`), all against loopback TLS servers, no mocks:
  - `TestNewUpstreamTLS_ClientHelloShape`: the upstream server records the ClientHello; `safari` carries a GREASE cipher suite and ALPN exactly `http/1.1`, `go` carries no GREASE
  - `TestInterceptor_HandleUpstreamFingerprints`: a full `Handle` session (client TLS → fpsd leaf cert → upstream) for both fingerprints, upstream verification on
  - `TestInterceptor_HandleSafariVerifiesUpstreamCert`: an untrusted upstream cert fails the handshake, nothing is proxied, the log names `upstream_tls=safari`. Mutation check: forcing `InsecureSkipVerify` makes this test fail
  - `TestNewUpstreamTLS_UnknownFingerprint`, `TestNewInterceptor_UpstreamTLSDefault`
- New tests (`internal/config`): default `safari`; validation accepts `safari`/`go`/empty, rejects others; YAML override
- Results: all packages passing, 0 failing
- Additional gates: `make lint` (v2.12.2 under Go 1.26.0) → 0 issues; `go vet ./...` → clean
- Contract quality: tests assert what the upstream observes (ClientHello contents, delivered response, refused handshake), not uTLS internals
- Status: PASSED

### Phase 4: Code Quality
- Dead code: none; the previous inline `tls.Config` construction moved into `newUpstreamTLS`
- Duplication: none
- Encapsulation: fingerprint selection isolated in `internal/mitm/upstream.go`; `proxyLoop` now takes `net.Conn` for the upstream side
- Status: PASSED

### Phase 5: Security Review
- Phase A — Dependency CVE scan: govulncheck v1.8.0 (DB vuln.go.dev, Go 1.27.3) → No vulnerabilities found. New modules: `refraction-networking/utls` v1.8.2, `golang.org/x/crypto` v0.59.0, `klauspost/compress` v1.20.4, `andybalholm/brotli` v1.3.0 (uTLS certificate compression); `xyproto/randomstring` appears in the graph only as a brotli test dependency and is not compiled in
- Phase B — OWASP review (AI-assisted, best-effort): A02/A07 — upstream certificate verification stays on (ServerName = intercepted domain, system roots), enforced by test and mutation check. The Safari preset permits TLS 1.0–1.2 negotiation where the Go path required 1.2+ (recorded in spec risks). No credentials or bodies are logged; the new log field is the fingerprint name only. Config input validated against a fixed set
- Phase C — Secrets scan (inline grep of staged diff): 0 findings
- Status: PASSED

### Phase 5.5: Release Safety
- Change type: behavior change on intercepted upstream connections, behind a config flag
- Rollback: `mitm.upstream_tls: "go"` + restart (previous behavior), or reinstall v1.5.2
- Status: PASSED

### Overall
- All gates passed: YES
- Live E2E on njv-cachyos with the Reddit iOS app: feed loads, 31 MITM sessions without upstream errors, ad filter matches recorded (spec 026 E2E results)
