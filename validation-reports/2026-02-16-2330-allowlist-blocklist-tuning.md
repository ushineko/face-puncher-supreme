# Validation Report: Allowlist and Blocklist Tuning (Spec 005)

**Date**: 2026-02-16 23:30
**Commit**: 28a8d7c
**Status**: PASSED

## Phase 3: Tests

- Test suite: `make test` (`go test -race -short -v ./...`)
- Results: 90 passing, 0 failing (77 existing + 13 new)
- New tests:
  - `TestAllowlistExactMatch` — exact domain match prevents blocking
  - `TestAllowlistSuffixMatch` — `*.example.com` matches base and subdomains
  - `TestAllowlistCaseInsensitive` — case-insensitive matching
  - `TestAllowlistCounters` — allow counters increment correctly
  - `TestAllowlistSize` — exact + suffix count
  - `TestAllowlistNotInBlocklist` — allowlist-only domain not counted
  - `TestSnapshotAllowCounts` — snapshot returns correct per-domain counts
  - `TestAddInlineDomains` — inline domains merge into blocklist
  - `TestAddInlineDomainsWithURLDomains` — inline + URL domains coexist
  - `TestAddInlineDomainsCaseInsensitive` — lowercased on merge
  - `TestAddInlineDomainsEmpty` — no-op for empty list
  - `TestInlineDomainsWithAllowlist` — allowlist overrides inline blocklist
  - Config tests: `TestLoad_BlocklistAndAllowlist`, `TestValidate_ValidBlocklistAndAllowlist`, `TestValidate_InvalidBlocklistEntry`, `TestValidate_InvalidBlocklistEmpty`, `TestValidate_InvalidAllowlistSuffix`, `TestValidate_InvalidAllowlistMidWildcard`
- Status: PASSED

## Phase 4: Code Quality

- Dead code: None found
- Duplication: Extracted `validateBlocklistURLs`, `validateBlocklist`, `validateAllowlist` from `Validate()` to reduce cyclomatic complexity (was 26, now within limit)
- Encapsulation: Allowlist logic contained within `blocklist.DB`; callback function avoids import cycle between blocklist and stats packages
- Status: PASSED

## Phase 5: Security Review

- Dependencies: No new dependencies added
- OWASP Top 10: No injection vectors (allowlist/blocklist entries are config-only strings, not user input at runtime)
- Input validation: Config `Validate()` rejects malformed entries (empty, slashes, spaces, invalid wildcards)
- No hardcoded secrets, no file path traversal vectors
- Status: PASSED

## Phase 5.5: Release Safety

- Change type: Code-only (no schema migrations for existing DBs; `allowed_domains` table created via `IF NOT EXISTS`)
- Rollback plan: Revert commit. The `allowed_domains` table is additive (SQLite ignores unknown tables). Existing `stats.db` and `blocklist.db` are unchanged.
- Backwards compatible: New config fields (`blocklist`, `allowlist`) are optional; existing configs work unchanged
- Status: PASSED

## Overall

- All gates passed: YES
- Files modified: 8 (config.go, config_test.go, blocklist.go, blocklist_test.go, stats/db.go, probe.go, main.go, fpsd.yml)
- Files updated: 3 (README.md, Makefile, spec 005)
