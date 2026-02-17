## Validation Report: Transparent Proxying + Installer/Uninstaller
**Date**: 2026-02-17 04:04
**Specs**: 010 (transparent proxying), 011 (installer/uninstaller)
**Status**: PASSED

### Phase 3: Tests
- Test suite: `make test` (`go test -race -short -v ./...`)
- Results: 128 passing, 0 failing
- New tests: 12 (transparent package: SNI parsing, prefixConn, peekClientHello, real TLS ClientHello)
- Status: PASSED

### Phase 4: Code Quality
- Dead code: None found
- Duplication: `stripPort` and `removeHopByHopHeaders` are duplicated between proxy and transparent packages — acceptable for package independence (no shared internal util package needed for 2 functions)
- Encapsulation: Clean package boundaries — transparent package has its own `Blocker` and `MITMInterceptor` interfaces
- Refactorings: None needed
- Status: PASSED

### Phase 5: Security Review
- Dependencies: No new dependencies added
- OWASP: iptables rules use `--uid-owner` for loop prevention; systemd unit uses `ProtectHome=tmpfs`, `ProtectSystem=strict`, `NoNewPrivileges`, `PrivateTmp`
- `SO_ORIGINAL_DST` uses `unsafe.Pointer` (required for syscall, nolint'd with justification)
- TLS ClientHello parsing has bounds checking on all slice accesses (gosec nolint'd after verification)
- `fps-ctl` uses `set -euo pipefail`, validates all inputs, shows rules before applying
- No secrets in code; CA certs are user-generated
- Status: PASSED

### Phase 5.5: Release Safety
- Change type: Code + script + config
- Rollback plan: `fps-ctl uninstall --purge` removes all installed files, services, and iptables rules. `git revert` for code changes. Transparent proxy is additive — disabling `transparent.enabled` in config returns to explicit-only mode.
- Status: PASSED

### Dogfood Results
- Fresh install: directories created, binary copied, config adjusted, service started, heartbeat OK
- Upgrade install: existing config preserved, binary updated, service restarted
- Transparent proxy install (single): iptables rules applied on eno2, PREROUTING REDIRECT verified, loop prevention rules verified, IP forwarding enabled
- Multi-interface install: `--interface eno2,virbr0` applied 4 PREROUTING rules (2 per interface), status shows both interfaces
- Multi-interface uninstall: all 4 PREROUTING rules removed cleanly (0 remaining)
- Status check: all components reported correctly (binary, service, tproxy, heartbeat)
- Full uninstall: service stopped, iptables rules removed, unit files deleted, data purged
- Clean validation: all files removed, no iptables rules remaining, no running processes
- Double uninstall: graceful "not installed" message

### Device Testing (Transparent Mode)

- iPhone 17 Pro Max (iOS 26.2.1): domain blocking, MITM, content filtering, Safari — all working
- iPad Pro 13" M5 (iPadOS 26.2): same results as iPhone
- Windows 11 Pro (Vivaldi, via virbr0 NAT bridge): MITM TLS interception working
- macOS 26.3 (Safari): domain blocking, allowlist, MITM — all working

### Overall
- All gates passed: YES
- Notes: `ProtectHome=tmpfs` required adding `BindReadOnlyPaths` for the binary path. CA cert paths kept relative (data_dir prefix applied by application). Multi-interface support via comma-separated `--interface` flag. Pi-hole DNS blocklists can conflict with fpsd MITM domains — whitelist overlapping domains in Pi-hole.
