# Spec 028: CI, Release and Supply-Chain Standardization

**Status**: COMPLETE
**Created**: 2026-11-12
**Depends on**: Spec 012 (GitHub Actions / Arch packaging), Spec 014 (AUR publish), Spec 027 (lint toolchain)
**Supersedes**: Spec 025 (GitHub Actions Node 24 upgrade)

> **Note**: This work has no associated issue tracker ticket. Consider creating one for traceability.

---

## Context

fpsd's CI predates the build and release conventions the newer Go projects in this workspace follow (hayami is the reference). Gaps found on 2026-11-12:

- Actions are referenced by mutable tag (`@v4`, `@v1`), several on the deprecated Node.js 20 runtime (the spec 025 reminder). No job declares least-privilege `permissions`.
- Test and lint run inside `archlinux:latest` with pacman's Go, so the Go that builds a release is whatever Arch ships that day. The image is referenced by mutable tag.
- No secret scanning, OpenSSF Scorecard, Dependabot, or vulnerability scan in CI. No `SECURITY.md`.
- The version of record is a `VERSION :=` line in the Makefile, and `PKGBUILD`'s static `pkgver=` must be bumped separately; a missed bump has shipped a stale AUR version before (v1.3.3). Any build reports the release version, so a working-tree build cannot be told from a release.
- The release job publishes whatever `makepkg` builds, and `makepkg` clones `main` HEAD from GitHub, not the tagged commit. There are no release notes, checksums or provenance; the AUR publish depends only on the package build, so it can run when the GitHub release fails.
- Repository settings differ from hayami: SHA pinning not required, Dependabot security updates off, immutable releases off, private vulnerability reporting off.

## Requirements

### R1 Pinned, least-privilege workflows

1. Every `uses:` names a full commit SHA with the version in a comment, at majors that run on Node.js 24. Container images are pinned by digest.
2. Each workflow sets `permissions: {}`; each job grants only what it uses. `actions/checkout` uses `persist-credentials: false` in jobs that do not push.
3. A Go test in `tests/structure` fails when any workflow names an Action or image by tag (adapted from hayami's `actions_pinned_test.go`), with a negative test showing it detects tag references.

### R2 CI build and gates

1. Test, lint and build run on `ubuntu-latest` with `actions/setup-go` (`GO_VERSION: "1.27.x"`, `check-latest`) and `actions/setup-node` for the UI build.
2. New CI steps: `make check-no-binaries` (no compiled binary tracked in git) and `make vulncheck` (govulncheck).
3. The Arch package is still built with `makepkg` in an Arch container (digest-pinned) to prove the PKGBUILD builds.

### R3 Security workflow and policy

1. `.github/workflows/security.yml`: gitleaks over the full history on push to main, pull requests, weekly and on demand, with `.gitleaks.toml` (default rules, allow-list by pattern/path only); OpenSSF Scorecard weekly and on main, SARIF uploaded to code scanning.
2. `.github/dependabot.yml`: weekly grouped updates for `github-actions`, `gomod` and `npm` (`/web/ui`), 7-day cooldown.
3. `SECURITY.md`: private reporting through GitHub, scope, response time.

### R4 Version of record

1. A `VERSION` file holds the version. The Makefile reads it; `PKGBUILD` `pkgver()` reads it.
2. A build that is not exactly the clean tagged commit reports `<VERSION>-<commit>-dev`.
3. pacman forbids `-` in `pkgver`, so the package version is `VERSION` with `-` replaced by `_` (`1.5.4-rc.1` → `1.5.4_rc.1`), both in `pkgver()` and in the static `pkgver=` line. A Go test fails when the static line differs from that mapping of `VERSION`.

### R5 Release workflow

On a `v*` tag:

1. The tag must match `VERSION`, and (for non-pre-release tags) name a commit on `main`.
2. Gates run as in CI; the package is built by `makepkg` from the tagged commit (local checkout), not from GitHub's `main`.
3. The built binary reports exactly `VERSION`.
4. Release notes come from the tag's README changelog entry; the job fails when the entry is missing.
5. `SHA256SUMS` and build provenance attestations (`actions/attest-build-provenance`) cover every published file.
6. `gh release create --verify-tag` publishes (compatible with immutable releases). A tag with a suffix (`v1.5.4-rc.1`) is a pre-release, not `latest`.
7. AUR publish runs only after the GitHub release succeeds, and never for a pre-release.

### R6 Repository settings

Enable: required SHA pinning for Actions, Dependabot security updates, immutable releases, private vulnerability reporting.

## Acceptance Criteria

- [x] All workflow `uses:` and container references are SHA/digest-pinned; `TestEveryActionIsPinned` passes and `TestAnUnpinnedActionIsFound` shows tag references are caught.
- [x] Every workflow has `permissions: {}` at the top and per-job grants.
- [x] CI runs on a pull request: test, lint, build, check-no-binaries, vulncheck, structure tests, Arch package build all green.
- [x] Security workflow runs gitleaks green on the pull request; Scorecard runs green on main after merge.
- [x] `dependabot.yml` is accepted by GitHub (no configuration error on the repository's Dependabot page) after merge.
- [x] `VERSION` exists; `make build` from a clean tagged commit reports the plain version, from a modified tree reports `-dev`; `TestPKGBUILDVersionMatchesVERSION` passes and fails when they differ.
- [x] A pre-release tag run (`v1.5.4-rc.1`) passes the release workflow: tag/VERSION check, package from the tagged commit, version self-check, notes, SHA256SUMS, attestations, published as pre-release; AUR publish skipped.
- [x] `gh attestation verify` succeeds for a file from that pre-release.
- [x] Repository settings in R6 are enabled.
- [x] Spec 025 marked SUPERSEDED by this spec.
- [x] `make test`, `make lint`, `go vet ./...` pass.

## Test Strategy

- **Structure tests** (`tests/structure`, Go): pinned-Action detection over the real workflow files plus synthetic positive/negative cases; `VERSION` vs `PKGBUILD` agreement over the real files.
- **Integration boundary**: GitHub Actions itself is the downstream system. Verified by real workflow runs on the pull request, on `main`, and on a pre-release tag (E2E Test Plan). No mocks of GitHub.

## E2E Test Plan

| Step | Environment | Action | Expected | AC |
|---|---|---|---|---|
| 1 | GitHub, PR from `ci/supply-chain-standardization` | Open PR | CI and Security (gitleaks) workflows green | 3, 4 |
| 2 | GitHub, main after merge | Merge | CI green on main; Scorecard job green; Dependabot page shows no config error | 4, 5 |
| 3 | GitHub, tag `v1.5.4-rc.1` on a side branch `rc/v1.5.4-rc.1` (one commit on top of the merged main: `VERSION`, `PKGBUILD` and a changelog heading for the rc) | Push branch and tag | Release workflow green; pre-release with package, SHA256SUMS, notes; AUR job skipped | 7 |
| 4 | Local | `gh attestation verify <pkg> -R ushineko/face-puncher-supreme` | Verification succeeds | 8 |

### E2E results (2026-11-12)

| Step | Result |
|---|---|
| 1 | PR #33: Test and lint (Go 1.27.4; lint under go1.26.0, 0 issues; structure tests; vulncheck; check-no-binaries; build reported `1.5.3-2909c24-dev`), Arch package (makepkg from the PR commit), gitleaks: all pass. PASS |
| 2 | Main `add6a20`: CI, Security (gitleaks + Scorecard), Spec Tracking pass; Dependabot ran all three ecosystems without configuration errors and opened PRs #35 (actions) and #36 (ui). PASS |
| 3 | Tag `v1.5.4-rc.1` (`e3cf16f`, side branch): Gates, package, publish pass, AUR skipped. Release is a pre-release, immutable, not latest (latest stays v1.5.3); notes are the changelog entry; assets are the package, debug package and SHA256SUMS; the packaged binary reports `fpsd 1.5.4-rc.1`. PASS |
| 4 | `sha256sum -c SHA256SUMS` OK; `gh attestation verify` exits 0 for the package and SHA256SUMS, signer `release.yml@refs/tags/v1.5.4-rc.1`; a tampered SHA256SUMS fails (exit 1). PASS |

Repository settings (R6) verified via the API: `sha_pinning_required` true, vulnerability alerts and Dependabot security updates enabled, immutable releases enabled, private vulnerability reporting enabled.

### Prod visibility

Not applicable: CI and release tooling. Observation is through the Actions run history and the Releases page.

## Risks & Assumptions

- **Rollback**: revert the merge commit; repository settings can be switched back individually. A pre-release can be deleted (immutable releases prevent changing assets, not deleting the release).
- **Pre-release version**: the rc run needs `VERSION` = `1.5.4-rc.1` on the tagged commit. That commit lives only on the side branch `rc/v1.5.4-rc.1` (pre-releases are exempt from the on-main check), so `main` keeps `1.5.3` until the next real release.
- **Digest-pinned Arch image** goes stale; the job runs `pacman -Syu` so packages are current, and the digest is bumped by hand (Dependabot does not track workflow `container:` images).
- **SHA pinning enforcement** fails any workflow that names an Action by tag, including the spec-tracking workflow; all are converted in the same change.
- **Scorecard** publishes results publicly (the repository is public).
- **AUR package source**: `fpsd-git` still builds from GitHub's default branch for AUR users; only the release job's package is built from the tag.

## Executive Summary

fpsd's CI and releases now follow the workspace's shared conventions: SHA-pinned least-privilege workflows enforced by a test, gitleaks, Scorecard and Dependabot, a `VERSION` file with `-dev` builds, and releases built from the tag with checksums, provenance attestations and pre-release support. Verified end to end with PR #33, main, and the `v1.5.4-rc.1` pre-release. Review first: `release.yml` and `scripts/makepkg-local.sh`.

## Alternatives Considered

- Considered keeping test/lint in the Arch container; rejected because the Go version would track Arch rather than the newest patch of the supported line.
- Considered goreleaser; rejected as a new tool for a single Arch package release.
