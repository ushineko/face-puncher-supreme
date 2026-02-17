# Validation Report: Reddit Promotions Filter Plugin (Spec 008)

**Date**: 2026-02-17 03:00 (initial), 2026-02-17 04:30 (iteration 1 fixes)
**Spec**: specs/008-reddit-promotions-filter.md
**Status**: PASSED (all acceptance criteria met, live-verified)

## Phase 3: Tests

- Test suite: `make test` (go test -race -short -v ./...)
- Results: 167 passing, 0 failing
- Package breakdown:
  - `internal/plugin`: 51 tests (26 existing + 25 new for reddit filter)
  - All other packages: unchanged, all passing
- Status: PASSED

### New Tests (25)

| Test | Rule | What It Verifies |
|------|------|------------------|
| TestRedditFilterRegistered | — | Plugin in registry with correct name/version/domains |
| TestFeedAdRemoval | R1 | `<shreddit-ad-post>` removed, organic posts preserved |
| TestFeedAdRemovalWithVisiblePlaceholder | R1, R8 | Visible placeholder inserted on feed ads |
| TestFeedAdRemovalWithCommentPlaceholder | R1, R8 | Comment placeholder inserted on feed ads |
| TestFeedNoAdPassthrough | R1, R5 | No-ad feed fixture passes through unmodified |
| TestCommentTreeAdRemoval | R2 | `<shreddit-comment-tree-ads>` container removed, surrounding comments preserved |
| TestCommentPageAdRemoval | R3 | Standalone `<shreddit-comments-page-ad>` removed |
| TestRightRailPromotedRemoval | R4 | `<ad-event-tracker>` wrapper removed, organic posts preserved |
| TestRightRailNoPromotedPassthrough | R4, R5 | No-promoted right-rail fixture passes through unmodified |
| TestURLScopingSkipsNonAdPaths | R6 | 7 non-ad paths bypass filter |
| TestURLScopingProcessesAdPaths | R6 | 9 ad paths are processed (updated with iteration 1 additions) |
| TestQuickSkipNoMarkers | R5 | Body without ad markers skips element scanning |
| TestRemoveElementsSingle | — | Single element removal with placeholder |
| TestRemoveElementsMultiple | — | Multiple elements removed in one pass |
| TestRemoveElementsNoMatch | — | No-match returns body unchanged |
| TestRemoveElementsMalformedNoClose | — | Missing close tag bails out safely |
| TestContainsAdMarker | R5 | All 3 marker strings detected; angle bracket required for ad-event-tracker |
| TestShouldProcess | R6 | 15 path patterns tested (9 true, 6 false) |
| TestMultipleRulesInSingleResponse | R7 | Multi-rule body: first rule name, total count |
| TestFixtureIntegrity (x6) | — | 6 fixtures verified for expected marker presence/absence |

### Test Fixtures (6)

All extracted from real interception captures (2026-02-16T14-56-29 session):

| Fixture | Source | Content |
|---------|--------|---------|
| feed_with_ad.html | Homepage SSR | 1 organic + 1 `<shreddit-ad-post>` + 1 organic |
| feed_no_ad.html | Feed partial | 3 organic posts, no ads |
| comments_with_tree_ad.html | Comment partial | `<shreddit-comment-tree-ads>` with 2 template blocks |
| comments_with_page_ad.html | Comment page | Standalone `<shreddit-comments-page-ad>` |
| right_rail_with_promoted.html | Right-rail partial | 1 organic + 1 `<ad-event-tracker>` promoted |
| right_rail_no_promoted.html | Right-rail partial | 3 organic posts, no ads |

## Phase 4: Code Quality

- Dead code: None found
- Duplication: None — `removeElements` is a shared helper for all 4 rules
- Encapsulation: Filter logic isolated in `reddit.go`, test helpers in `reddit_test.go`
- Line lengths: All within 150-char limit
- Status: PASSED

## Phase 5: Security Review

- Dependencies: No new dependencies added (stdlib only: bytes, strings, net/http)
- Input validation: URL path checked before processing; malformed HTML (missing close tag) handled gracefully
- No user input flows into file operations, SQL, or shell commands
- Interception data (HTML fixtures) contain only truncated tracking payloads — no real credentials or PII
- Accept-Encoding stripping: only applied when ResponseModifier is active, does not affect non-MITM traffic
- Status: PASSED

## Phase 5.5: Release Safety

- Change type: Code-only (new filter logic, MITM header fix, placeholder CSS, test fixtures, config mode change)
- Rollback plan: Revert commit, or change `mode: "filter"` back to `mode: "intercept"` in fpsd.yml
- Breaking changes: None — plugin architecture unchanged, new filter replaces stub
- Status: PASSED

## Iteration 1: Live Testing Fixes

Three issues discovered during live verification and fixed:

### Fix 1: Accept-Encoding stripping in MITM handler

**Problem**: Browser sends `Accept-Encoding: gzip, deflate, br` to upstream via the MITM proxy. Upstream returns compressed responses. The filter tries to match string patterns against compressed binary data — zero matches.

**Fix**: Strip `Accept-Encoding` from requests forwarded to upstream when `ResponseModifier != nil` (`internal/mitm/handler.go`). The proxy serves uncompressed responses to the browser with an accurate `Content-Length`.

### Fix 2: Visible placeholder CSS for narrow containers

**Problem**: Right-rail placeholder text wraps character-by-character in narrow sidebar columns, rendering vertically.

**Fix**: Added `white-space:nowrap;overflow:hidden;text-overflow:ellipsis` to visible mode placeholder CSS. Shortened label text from "fps filtered:" to "fps:" and reduced font/padding (`internal/plugin/plugin.go`).

### Fix 3: Expanded URL scoping for subreddit pages

**Problem**: Subreddit listing pages (`/r/LivestreamFail/`) and community scroll-load partials (`/svc/shreddit/community-more-posts/*`) not matched by `shouldProcess()`.

**Fix**: Added two new URL prefix checks to `shouldProcess()` in `internal/plugin/reddit.go`. Updated test tables in `reddit_test.go`.

## Acceptance Criteria

- [x] Feed ads (`<shreddit-ad-post>`) removed — TestFeedAdRemoval
- [x] Comment-tree ads (`<shreddit-comment-tree-ads>`) removed — TestCommentTreeAdRemoval
- [x] Standalone comment-page ads removed — TestCommentPageAdRemoval
- [x] Right-rail promoted posts removed — TestRightRailPromotedRemoval
- [x] Quick-skip for no-marker responses — TestQuickSkipNoMarkers, TestFeedNoAdPassthrough
- [x] URL scoping — TestURLScopingSkipsNonAdPaths, TestURLScopingProcessesAdPaths
- [x] FilterResult reporting — TestMultipleRulesInSingleResponse, all rule tests
- [x] Placeholder insertion — TestFeedAdRemovalWithVisiblePlaceholder, TestFeedAdRemovalWithCommentPlaceholder
- [x] Unit tests using captured HTML — 6 fixtures, 25 tests
- [x] Config switched to filter mode — fpsd.yml updated
- [x] Build, lint, vet all pass — 167 tests, 0 lint issues, 0 vet warnings
- [x] Live verification — browsing Reddit through proxy shows no promoted posts (confirmed across multiple subreddits)

## Overall

- All gates passed: YES
- Live verification: PASSED (homepage, feed partials, comment pages, right-rail, subreddit listings)
- Lint: golangci-lint v2.9.0 — 0 issues
- Vet: 0 warnings
