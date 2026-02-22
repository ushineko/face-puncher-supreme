# Spec 020: Spec-Driven Issue Tracking via GitHub Actions

**Status**: PENDING
**Created**: 2026-02-22
**Depends on**: Spec 012 (GitHub Actions CI)

## Problem Statement

fpsd uses spec-driven development: numbered specs in `specs/` define work items, and
`validation-reports/` provide evidence that the work is complete. This workflow is
effective locally but invisible to anyone looking at the GitHub repository — the
Issues tab is empty and gives no indication that 19 specs have been implemented.

Reflecting spec lifecycle in GitHub Issues would:
- Give external users/contributors visibility into the project's development history
- Provide a natural place to discuss spec-related work alongside user-opened issues
- Allow the existing GitHub issue tracker to serve as a unified view of both
  spec-driven work and community-reported bugs/features

## Approach

### Issue Lifecycle

```
spec committed ──> GH issue opened (labeled, links to spec)
                        │
validation committed ──>│──> matching validation found
                        │         │
                        │    status = PASSED ──> issue closed with comment
                        │    status != PASSED ──> comment added, issue stays open
                        │
spec status = COMPLETE ─┘──> issue closed (fallback for specs without validation)
```

### Issue Format

**Title**: `[spec-NNN] <title from spec first heading>`

**Labels**:
- `spec-tracking` — primary marker distinguishing these from user-opened issues
- `backfill` — added only during backfill to flag that the issue was created retroactively

**Body** (created from template):
```markdown
## Spec: <title>

**Spec file**: [`specs/NNN-name.md`](link to file on main branch)
**Status**: <status from spec front matter>
**Created**: <date from spec front matter, or git first-commit date>
**Depends on**: <dependencies from spec, if any>

---

> This issue tracks the lifecycle of a development spec.
> It was created automatically by the spec-tracking workflow.
> See the linked spec file for full requirements and acceptance criteria.
```

**Closing comment** (when validation matches):
```markdown
Validated and closing.

**Validation report**: [`validation-reports/<filename>`](link)
**Validation date**: <date from report>
**Validation status**: PASSED

All acceptance criteria met per the linked validation report.
```

### Distinguishing Spec Issues from User Issues

The `spec-tracking` label is the primary discriminator. The `[spec-NNN]` title prefix
provides visual distinction in issue lists. The body includes a boilerplate notice
that the issue was auto-generated.

Users can still open issues freely — they won't have the `spec-tracking` label or
the `[spec-NNN]` prefix.

### GitHub Actions Workflow

New file: `.github/workflows/spec-tracking.yml`

**Trigger 1 — Push to main** (ongoing):
```yaml
on:
  push:
    branches: [main]
    paths:
      - 'specs/**'
      - 'validation-reports/**'
```

Logic:
1. Use `git diff --name-only $BEFORE..$AFTER` to find changed files in `specs/` and
   `validation-reports/`
2. For each new/modified spec: check if a `[spec-NNN]` issue exists (search by title).
   If not, create one. If it exists, update the body if spec status changed.
3. For each new validation report: match it to a spec (see matching logic below).
   If the matched spec has an open issue and the validation status is PASSED, close
   the issue with a comment linking the report.

**Trigger 2 — workflow_dispatch** (backfill):
```yaml
on:
  workflow_dispatch:
    inputs:
      dry_run:
        description: 'Preview what would be created/closed without making changes'
        type: boolean
        default: true
```

Logic:
1. Scan all files in `specs/` and build a list of spec numbers + metadata
2. Scan all files in `validation-reports/` and match each to a spec
3. For each spec: if no `[spec-NNN]` issue exists, create one
4. For each spec with a matching PASSED validation: close the issue
5. In dry-run mode, output the plan without creating/closing anything

### Validation-to-Spec Matching

Matching is attempted in priority order. First match wins.

**Priority 1 — Explicit spec number in validation filename**:
Files like `spec-018-refactor-runproxy.md` → match to spec 018.
Pattern: filename contains `spec-(\d{3})`.

**Priority 2 — Explicit spec reference in validation content**:
Front matter like `**Spec**: 010` or `**Specs**: 010, 011` → match to those specs.
Pattern: scan first 10 lines for `Spec[s]?.*?(\d{3})`.

**Priority 3 — Topic keyword matching**:
Compare the slugified validation filename against spec filenames. Example:
`domain-blocklist` in validation matches `002-domain-blocklist` in specs.
Algorithm: longest common substring of hyphen-delimited tokens between the
validation filename (after stripping the date prefix) and each spec filename
(after stripping the number prefix). Match requires >= 2 common tokens.

**Priority 4 — Fallback: no match**:
If no spec matches, the validation report is attached as a comment to the most
recently created (highest-numbered) spec issue that already has a validation
report, on the assumption that standalone validations (e.g., lint hardening
passes) relate to the most recent completed work. If no spec has a validation
yet, attach to spec 001.

Validation reports that match multiple specs (e.g., `transparent-proxy-installer`
covers specs 010 and 011) create closing comments on all matched issues.

### Backfill Plan

The `workflow_dispatch` trigger handles backfilling. When run against the current
repository state (19 specs, 19 validation reports), it should produce the following:

#### Expected Spec-to-Validation Mapping

| Spec | Title | Validation Report(s) | Match Method |
|------|-------|---------------------|--------------|
| 001 | Investigation and Proxy Foundation | `2026-02-16-0050-phase1-proxy-foundation` | topic: "proxy-foundation" |
| 002 | Domain-Based Ad Blocking | `2026-02-16-0300-domain-blocklist` | topic: "domain-blocklist" |
| 003 | YAML Configuration File | `2026-02-16-1840-yaml-config` | topic: "config" |
| 004 | Database-Backed Statistics | `2026-02-16-2200-database-stats` | topic: "database-stats" |
| 005 | Allowlist and Blocklist Tuning | `2026-02-16-2330-allowlist-blocklist-tuning` | topic: "allowlist-blocklist" |
| 006 | Per-Domain TLS Interception | `2026-02-17-0100-mitm-tls-interception` | topic: "mitm-tls-interception" |
| 007 | Content Filter Plugin Architecture | `2026-02-17-0200-content-filter-plugins` | topic: "content-filter" |
| 008 | Reddit Promotions Filter Plugin | `2026-02-17-0300-reddit-promotions-filter` | topic: "reddit-promotions-filter" |
| 009 | Web Dashboard | `2026-02-16-2400-web-dashboard` | topic: "web-dashboard" |
| 010 | Transparent Proxying | `2026-02-17-0400-transparent-proxy-installer` | content: "Specs: 010, 011" |
| 011 | Installer / Uninstaller | `2026-02-17-0400-transparent-proxy-installer` | content: "Specs: 010, 011" |
| 012 | GitHub Actions CI + Arch Packaging | `2026-02-17-2140-spec-012-github-actions-arch-packaging` | filename: "spec-012" |
| 013 | Dashboard UI Improvements | `2026-02-17-0300-dashboard-ui-improvements` | topic: "dashboard-ui-improvements" |
| 014 | AUR Publish Automation | *(none — closed via spec status COMPLETE)* | — |
| 015 | Dashboard Stats Bugfixes | `2026-02-17-2300-dashboard-stats-bugfixes` | topic: "dashboard-stats-bugfixes" |
| 016 | WebSocket Reconnect Auth | `2026-02-17-2350-websocket-reconnect-auth`, `2026-02-18-0610-websocket-reconnect-auth-fix` | topic: "websocket-reconnect-auth" |
| 017 | Reddit iOS GraphQL Ad Filtering | `2026-02-21-2200-reddit-ios-graphql-ad-filtering` | topic: "reddit-ios-graphql-ad-filtering" |
| 018 | Refactor runProxy() God Function | `2026-02-22-0710-spec-018-refactor-runproxy` | filename: "spec-018" |
| 019 | Stats Watermarks, Resources, UI Fixes | `2026-02-22-1900-spec-019-stats-watermarks-resources-ui-fixes` | filename: "spec-019" |

**Unmatched validation report**: `2026-02-16-0130-lint-integration` — no spec
reference, topic "lint-integration" doesn't match any spec filename. Falls back to
spec 001 (most recent spec with a validation at that point in time). Added as a
supplementary comment, does not affect issue close/open state.

#### Backfill Issue Behavior

All backfilled issues will:
- Include the `backfill` label in addition to `spec-tracking`
- Note in the body that the issue was retroactively created:
  ```
  > **Backfill notice**: This issue was created retroactively by the spec-tracking
  > workflow. The spec was originally authored on <date>. The issue creation date
  > does not reflect when the work was done.
  ```
- Be created in spec-number order (001 first, 019 last)
- Be immediately closed with the validation comment if a matching PASSED report exists
- Specs with status COMPLETE but no validation report (spec 014) are closed with a
  comment noting completion per spec status

#### Backfill Idempotency

The backfill job searches for existing `[spec-NNN]` issues before creating. Running
it multiple times produces the same result. This also means the backfill and ongoing
workflows are safe to run concurrently — the ongoing workflow uses the same "check
before create" logic.

### Workflow Implementation Details

**Permissions**: The workflow needs `issues: write` and `contents: read`.

**Shell approach**: The workflow will use `gh` CLI commands (available in GitHub-hosted
runners) rather than the GitHub API directly. This keeps the implementation readable
and maintainable as a shell script within the workflow YAML.

**Key `gh` commands**:
- `gh issue list --label spec-tracking --json number,title` — find existing spec issues
- `gh issue create --title "..." --body "..." --label spec-tracking` — create issue
- `gh issue close N --comment "..."` — close with validation comment
- `gh label create spec-tracking --description "..." --color "..."` — ensure label exists

**Label colors**:
- `spec-tracking`: `#0E8A16` (green) — matches the spec-driven development theme
- `backfill`: `#D4C5F9` (light purple) — subtle indicator of retroactive creation

**Error handling**: If a `gh` command fails (e.g., rate limit), the workflow should
log the error and continue processing remaining specs. Partial runs are safe because
of the idempotency guarantee.

**Concurrency**: The workflow should use a concurrency group to prevent parallel runs
from creating duplicate issues:
```yaml
concurrency:
  group: spec-tracking
  cancel-in-progress: false
```

## Acceptance Criteria

- [ ] `.github/workflows/spec-tracking.yml` exists with both push and workflow_dispatch triggers
- [ ] `spec-tracking` and `backfill` labels are created by the workflow if they don't exist
- [ ] Committing a new spec to `specs/` on main creates a GitHub issue with `[spec-NNN]` title and `spec-tracking` label
- [ ] Committing a validation report that matches a spec closes the corresponding issue with a comment linking the report
- [ ] Updating a spec (e.g., status change) updates the existing issue body
- [ ] The workflow_dispatch backfill (dry_run=false) creates issues for all 19 existing specs
- [ ] All specs with PASSED validation reports or COMPLETE status have their issues closed after backfill
- [ ] Backfilled issues include the `backfill` label and retroactive creation notice with original spec date
- [ ] The `lint-integration` validation report is attached as a comment to spec 001's issue
- [ ] The `transparent-proxy-installer` validation report closes both spec 010 and spec 011 issues
- [ ] Spec 016 issue receives comments for both the original and fix validation reports
- [ ] Spec 014 (no validation report) is closed based on its COMPLETE status
- [ ] Spec 017 issue is closed (has PASSED validation report)
- [ ] Running the backfill twice produces no duplicate issues (idempotent)
- [ ] Concurrent workflow runs do not create duplicate issues (concurrency group)

## Out of Scope

- Syncing issue comments back to spec files
- Creating specs from user-opened issues
- PR integration (linking PRs to spec issues)
- Milestone or project board integration
- Modifying the existing CI or release workflows
