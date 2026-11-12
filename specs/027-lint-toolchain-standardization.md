# Spec 027: Lint and Go Toolchain Standardization

**Status**: INCOMPLETE
**Created**: 2026-11-12
**Depends on**: none

> **Note**: This work has no associated issue tracker ticket. Consider creating one for traceability.

---

## Context

`make lint` fails on any machine whose Go is 1.27, including the CI container (`archlinux:latest`, pacman's `go`). The pinned golangci-lint v2.9.0 binary was built with Go 1.26 and type-checks against the system standard library: it panics with `file requires newer Go version go1.27`. The last CI run (2026-09-19) failed this way, so a release tag would fail its gate.

Rebuilding v2.9.0 with Go 1.27 does not help: its bundled `golang.org/x/tools` cannot read Go 1.27 export data.

The other Go projects in this workspace (hayami, nmsbonker, fynstall and others) solved this with a common pattern: golangci-lint v2.12.2, installed from the release tarball with a pinned checksum, run under `GOTOOLCHAIN=go1.26.0` (the Go that linter was built with), a versioned config file, and a per-checkout lint cache. `go.mod` states only the minimum Go, with no `toolchain` line.

## Requirements

1. Adopt the shared lint block: `LINT_VERSION ?= v2.12.2`, checksum-verified tarball install (pinned `LINT_CHECKSUMS_SHA256`), `LINT_GO_TOOLCHAIN ?= go1.26.0` exported as `GOTOOLCHAIN` for `make lint`, and `GOLANGCI_LINT_CACHE` under the checkout's `.cache/` (ignored).
2. Move `.golangci.yml` to `config/.golangci-v2.12.2.yml`. fpsd keeps its own rule set; only the location changes.
3. Update module dependencies to current releases; `go.mod`'s `go` line is the minimum those require (1.26.0), with no `toolchain` line.
4. Triage findings that the newer linter reports in existing code. Each is fixed or suppressed with a `//nolint:<linter> // <rule>: <reason>` comment.
5. README Lint section describes the new arrangement.
6. Fix the pre-commit spec check (`scripts/pre-commit-spec-check.sh`): it zero-pads spec numbers with `printf "%03d"`, which reads `027` as octal 23 and errors on `008`/`009`. Parse them as base 10.

## Acceptance Criteria

- [x] `make lint` passes on this machine (system Go 1.27.3) and reports `go version go1.26.0` for the lint run.
- [x] The lint install downloads the release tarball and verifies both the checksums file and the tarball; the old `install.sh` pipe is gone.
- [x] `config/.golangci-v2.12.2.yml` holds the previous rule set unchanged; `.golangci.yml` no longer exists.
- [x] `go.mod` has `go 1.26.0`, no `toolchain` line, and updated dependencies; `go mod tidy` is a no-op.
- [x] Every new linter finding is resolved; suppressions carry the rule ID and a reason.
- [x] `make test` and `go vet ./...` pass.
- [x] The pre-commit spec check maps a report for spec 027 to `specs/027-*` (not 023) and accepts `008`/`009`.
- [ ] CI's `make lint` step passes on main and in the release workflow.

## Findings Triage

| Finding | Location | Disposition |
|---|---|---|
| G124 cookie without `Secure` | `web/auth.go` set/clear session cookie | Suppressed: the dashboard is served over plain HTTP on the LAN; `Secure` would make the browser drop the cookie. `HttpOnly` and `SameSite=Strict` are already set. |
| G703 path traversal | `web/server.go` `ServeFileFS` | Suppressed: `fsys` is the embedded UI; `fs.FS` rejects `..` and absolute paths. |
| G704 SSRF | `internal/transparent/listener.go` upstream dial | Suppressed: dialing the client's original destination is the transparent proxy's function. |
| G115 int→byte (5) | `internal/transparent/transparent_test.go` | Suppressed: test-only ClientHello construction with lengths far below 64 KiB. |

## Risks & Assumptions

- **Rollback**: revert the commit; the previous Makefile and config return.
- **Toolchain download**: `make lint` fetches Go 1.26.0 through `GOTOOLCHAIN` when it is not cached. CI has network access; the download is checksum-verified by the Go command against the Go checksum database.
- **Dependency updates** (`modernc.org/sqlite` 1.37→1.62, `golang.org/x/sys`, `spf13/pflag`, `testify` and others) are exercised by the full test suite, including the SQLite-backed stats and blocklist tests.
- **Assumption**: the checksum pinned in the other projects' Makefiles for v2.12.2 matches the release (verified by a successful `make lint` install here).

## E2E Test Plan

| Step | Environment | Action | Expected | AC |
|---|---|---|---|---|
| 1 | njv-cachyos, Go 1.27.3 | `make lint` in a fresh worktree | Installs v2.12.2 with checksum checks, prints `go1.26.0`, `0 issues` | 1, 2, 5 |
| 2 | GitHub Actions, release tag | CI and release workflows | `make lint` step green | 7 |

### Prod visibility

Not applicable: build tooling only.

## Alternatives Considered

- Considered golangci-lint v2.15.0 (built with Go 1.27); rejected to stay on the version, checksum and toolchain pin shared with the other projects.
- Considered a `toolchain` line in `go.mod`; rejected because the linter refuses a module that names a newer Go than it was built with.

