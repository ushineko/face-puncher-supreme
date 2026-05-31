# Spec 025: GitHub Actions Node 24 Upgrade

**Status**: DRAFT
**Created**: 2026-05-31
**Depends on**: Spec 012 (GitHub Actions / Arch packaging), Spec 014 (AUR publish automation)

> **Note**: This work has no associated issue tracker ticket. Consider creating one for traceability.

---

## Problem Statement

The v1.5.2 release run surfaced GitHub Actions deprecation annotations: several actions still run on the Node.js 20 runtime. Per the GitHub announcement (`github.blog/changelog/2025-09-19-deprecation-of-node-20-on-github-actions-runners`):

- **2026-06-16**: actions are forced to run on Node.js 24 by default.
- **2026-09-16**: Node.js 20 is removed from the runner entirely.

After the June date, actions pinned to Node-20 major versions may misbehave; after September they will fail outright. The release pipeline (build → GitHub release → AUR publish) and the spec-tracking automation both depend on these actions, so a broken pipeline would block future releases and issue sync.

This spec is a deferred reminder to bump the affected actions to majors that run on Node.js 24, ahead of the June 16 forced-migration date.

---

## Affected Actions

| Action | Used in | Current | Target (Node 24) |
|--------|---------|---------|------------------|
| `actions/checkout` | release.yml:28, release.yml:84, ci.yml:28, spec-tracking.yml:31 | `@v4` | `@v5` |
| `actions/upload-artifact` | release.yml:51 | `@v4` | latest major on Node 24 (verify current) |
| `actions/download-artifact` | release.yml:65 | `@v4` | latest major on Node 24 (verify current) |
| `softprops/action-gh-release` | release.yml:71 | `@v1` | `@v2` |

Exact target majors must be verified against each action's releases at implementation time — the table records the current runtime gap, not a pinned answer.

---

## Approach

Bump each action's major version to the current release that runs on Node.js 24. Prefer pinning to a major tag (e.g. `@v5`) consistent with the existing style in these workflows, rather than full commit SHAs (the repo does not currently SHA-pin).

`actions/upload-artifact` and `download-artifact` must move in lockstep — v4 artifacts are not cross-compatible with earlier majors, so both the producer (release.yml:51) and consumer (release.yml:65) must use the same major.

The `FORCE_JAVASCRIPT_ACTIONS_TO_NODE24=true` env-var opt-in is explicitly **not** the chosen path — it is a temporary runner override, not a fix, and would still leave the workflows on stale action majors.

---

## Scope

### In Scope

- Version bumps for the four actions above across all three workflow files.
- A verification run (push or tag) confirming the release and CI pipelines still pass with no Node-20 deprecation annotations.

### Out of Scope

- SHA-pinning actions for supply-chain hardening (separate concern; note it as a future option).
- Restructuring the release/CI/spec-tracking workflows.
- Changing the AUR publish logic or `.SRCINFO` generation.

---

## Risks & Assumptions

- **Rollback**: Revert the workflow commit. Workflows are declarative and take effect on the next trigger; no runtime state to migrate.
- **Verification risk**: A version bump can introduce input/output schema changes (notably `upload-artifact`/`download-artifact` between majors, and `action-gh-release` v1→v2). Implementation must read each action's migration notes and confirm the release run produces the GitHub release assets and AUR push as before.
- **Assumption**: The target majors run on Node.js 24 at implementation time — verify against each action's release notes, as versions move.
- **Timing**: Should land before 2026-06-16 to avoid forced-migration surprises, and must land before 2026-09-16 to avoid hard failure.
- **Security**: CI configuration only; no application code, secrets, or dependency changes. AUR SSH key handling in `publish-aur` is unchanged.

---

## Acceptance Criteria

- [ ] `actions/checkout` bumped to a Node-24 major in all four usages (release.yml ×2, ci.yml, spec-tracking.yml).
- [ ] `actions/upload-artifact` and `actions/download-artifact` bumped in lockstep to a Node-24 major.
- [ ] `softprops/action-gh-release` bumped to a Node-24 major.
- [ ] A release or CI run completes successfully with no Node.js 20 deprecation annotations.
- [ ] The GitHub release still receives the `.pkg.tar.zst` assets and the AUR push still updates `.SRCINFO` (verified on a real tag, or reasoned + spot-checked if deferring an actual release).
