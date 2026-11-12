## Validation Report: Spec 028 — CI, Release and Supply-Chain Standardization
**Date**: 2026-11-12 08:38
**Commit**: pre-commit
**Specs**: 028
**Status**: PASSED

### Phase 3: Tests
- Test suite: `make test` (system Go 1.27.3), all packages passing including the new `tests/structure`
- New tests:
  - `TestEveryActionIsPinned` over the real `.github` tree; before the workflow changes it failed on all three workflows (`actions/checkout@v4`, `archlinux:latest`, `softprops/action-gh-release@v1`, ...), after them it passes
  - `TestAnUnpinnedActionIsFound`: 7 pinned forms accepted, 22 tag/evasion forms (flow maps, anchors, block scalars, second documents, container tags) rejected
  - `TestPKGBUILDVersionMatchesVERSION` over the real files; `TestPKGBUILDVersionMismatchIsFound` covers a stale bump, an unmapped pre-release hyphen and a missing static line
- Additional gates: `make lint` → 0 issues; `go vet ./...` → clean; `make vulncheck` → No vulnerabilities found; `make check-no-binaries` → none tracked
- Workflow YAML parses; GitHub-side behavior is verified by the E2E plan (PR, main, pre-release tag), not locally
- Status: PASSED

### Phase 4: Code Quality
- Duplication: the package build used to be inlined in both workflows; it is now `scripts/makepkg-local.sh`, shared by CI and Release (shellcheck clean)
- `actions_pinned_test.go` is hayami's, with the YAML import switched to the module this repository already uses and two lint fixes
- Status: PASSED

### Phase 5: Security Review
- Phase A — Dependency CVE scan: govulncheck v1.8.0 → No vulnerabilities found. No new Go modules
- Phase B — OWASP / CI review (AI-assisted, best-effort): workflow permissions default to none, per-job grants; Actions pinned by verified commit SHA (each SHA checked against its upstream tag via the GitHub API); container pinned by digest; `persist-credentials: false` everywhere (no job pushes with the checkout token); `AUR_SSH_KEY` and the tag name reach scripts through `env:` rather than `${{ }}` interpolation in `run:`; release publishes only from a tag that matches `VERSION` and, for releases, is on main; provenance attested before publishing
- Phase C — Secrets scan: inline grep of the diff → 0 findings; gitleaks runs in CI from now on
- Status: PASSED

### Phase 5.5: Release Safety
- Change type: CI/release tooling and repository settings
- Rollback: revert the merge; repository settings can be switched off individually
- Status: PASSED

### Overall
- All local gates passed: YES
- Notes: spec 028 stays INCOMPLETE until the PR, main and pre-release workflow runs and the repository settings are verified.
