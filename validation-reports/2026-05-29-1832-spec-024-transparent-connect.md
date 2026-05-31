## Validation Report: Spec 024 — Transparent CONNECT Handling and Blocked-Dial Noise Reduction
**Date**: 2026-05-29 18:32
**Commit**: pre-commit
**Specs**: 024
**Status**: PASSED

### Phase 3: Tests
- Test suite: `make test` (`go test -race -short ./...`)
- New tests: `TestIsUnspecifiedDialError` (netutil); `TestHandleConnect_AllowedTunnel`, `TestHandleConnect_BlockedDomain` (transparent — both drive a real loopback echo listener); `TestBidirectional_CopiesBothDirectionsAndCounts`, `TestBidirectional_EmptyTunnel` (relay)
- Results: all packages passing, 0 failing (incl. pre-existing `internal/proxy` CONNECT tunnel test, unchanged after adopting the shared relay helper)
- Additional gates: `make lint` (golangci-lint v2.9.0) → 0 issues; `go vet ./...` → clean
- Status: PASSED

### Phase 4: Code Quality
- Dead code: None found
- Duplication: RESOLVED — the bidirectional relay block (`io.Copy` pair + half-close + byte counters) was extracted into `internal/relay.Bidirectional` and adopted at all three tunnel sites (`proxy.handleConnect`, transparent `handleHTTPS`, transparent `handleConnect`). Per the project rule of thumb, duplication is refactored rather than carried; spec 023 (DRAFT, on hold) can adopt the same helper when revived.
- Encapsulation: `handleConnect` and the two refactored tunnel paths are now small; relay concurrency lives in one tested helper.
- Refactorings: Extracted `internal/relay.Bidirectional`; removed three inline relay copies. The explicit-proxy CONNECT path keeps its non-blocking handler by calling the helper inside the existing tunnel goroutine (behaviour preserved; gains graceful half-close).
- Pre-existing note: `gofmt -l` flags a trailing blank line at `listener.go` EOF, present in HEAD and unrelated to this change; left untouched per surgical-changes rule (golangci-lint tolerates it).
- Status: PASSED

### Phase 5: Security Review (via /ralph-security-review)
- Verdict: PASS
- Quoted summary from /ralph-security-review:
  - Phase A — Dependency CVE Scan: `govulncheck` → 0 vulnerabilities affecting code. 1 pre-existing vuln in a required-but-uncalled module; no new dependencies added (stdlib only).
  - Phase B — OWASP Top 10 (AI-assisted, best-effort — not compliance evidence): clean. Access control improved — the new transparent CONNECT path now applies the domain blocklist before establishing a tunnel. SSRF surface unchanged from the existing proxy/HTTPS handlers (same blocklist gate). No secrets logged.
  - Phase C — Secrets & Credential Scan (inline AI scan): 0 findings.
- Status: PASSED

### Phase 5.5: Release Safety
- Change type: Code-only (new `internal/netutil` package, new transparent `handleConnect` method + CONNECT dispatch branch, log-level adjustments at four dial sites). No schema, no config, no on-disk format change, no new dependency.
- Rollback plan: Revert the single commit. Behavior returns to prior state in minutes; additive-only, no data migration. The pre-fix behavior (CONNECT mishandled, ERROR-level 0.0.0.0 noise) is benign to restore.
- Status: PASSED

### Overall
- All gates passed: YES
- Notes: Fix addresses two recurring runtime errors found in fpsd logs — (1) 267 `transparent http response read failed` errors from unhandled transparent CONNECT (Apple Safe Browsing), and (2) 46 ERROR-level dial failures to DNS-blocked `0.0.0.0`/`::` addresses. Root cause for (1) inferred from log correlation, not packet capture; fix is protocol-correct regardless of client. Manual verification step: confirm both error classes disappear from `journalctl --user -u fpsd` after deploy.
