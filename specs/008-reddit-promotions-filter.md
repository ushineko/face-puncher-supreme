# Spec 008: Reddit Promotions Filter Plugin

**Status**: COMPLETE
**Depends on**: Spec 007 (content filter plugin architecture)
**Commit**: (see git log for spec 008 commits)

---

## Overview

Implement the `reddit-promotions` content filter plugin to strip promoted/sponsored content from Reddit's new Shreddit UI. The plugin operates on MITM'd HTML responses from `www.reddit.com`, identifying and removing ad elements based on Reddit's custom web component naming conventions.

Reddit serves all ad content as server-rendered HTML fragments — no GraphQL interception is needed.

---

## Analysis Summary

Traffic captured via spec 007 interception mode (100 requests, 2026-02-16) reveals three distinct ad surfaces:

| Surface | Element | URL Pattern | Frequency |
|---------|---------|-------------|-----------|
| Feed ads | `<shreddit-ad-post>` | `/`, `/svc/shreddit/feeds/*` | 1-2 per page load |
| Comment-tree ads | `<shreddit-comments-page-ad>` inside `<template>` | `/svc/shreddit/comments/*` | 1-3 per comment page |
| Right-rail promoted | `<reddit-pdp-right-rail-post>` with `"promoted":true` | `/svc/shreddit/pdp-right-rail/*` | 0-1 per sidebar |

Detection is unambiguous: ad elements use distinct custom element names (`shreddit-ad-post`, `shreddit-comments-page-ad`) and CSS classes (`promotedlink`). There is no obfuscation.

---

## Requirements

### R1: Feed Ad Removal

Remove `<shreddit-ad-post ...>...</shreddit-ad-post>` elements from HTML responses. These are self-contained blocks with matching open/close tags. Present in:
- Full-page SSR responses (`GET /`)
- Feed partial responses (`GET /svc/shreddit/feeds/*`)

### R2: Comment-Tree Ad Removal

Remove `<shreddit-comment-tree-ads>...</shreddit-comment-tree-ads>` container elements from comment responses. This container wraps `<template id="comment-tree-ad_*">` blocks containing `<shreddit-comments-page-ad>` elements. Present in:
- Comment partial responses (`GET /svc/shreddit/comments/*`)

### R3: Comment-Page Top Ad Removal

Remove standalone `<shreddit-comments-page-ad ...>...</shreddit-comments-page-ad>` elements (those NOT inside a `<shreddit-comment-tree-ads>` wrapper) from comment responses. These appear at the top of the comments section with `placement="comments_page"`.

### R4: Right-Rail Promoted Post Removal

In right-rail responses (`GET /svc/shreddit/pdp-right-rail/*`), remove `<ad-event-tracker ...>...</ad-event-tracker>` wrapper elements that contain promoted posts. The `<ad-event-tracker>` element is the outermost wrapper for right-rail ads and contains `<reddit-pdp-right-rail-post>` children with `"promoted":true` in their tracking context.

### R5: Quick-Skip Optimization

Before attempting element removal, check if the response body contains any ad marker strings. If none are present, return the body unmodified to avoid unnecessary processing. Marker strings:
- `shreddit-ad-post`
- `shreddit-comments-page-ad`
- `ad-event-tracker`

### R6: URL-Scoped Processing

Only apply filter rules to responses matching these URL path patterns:
- `/` (homepage SSR)
- `/r/*` (subreddit listing pages)
- `/r/*/comments/*` (post detail SSR)
- `/svc/shreddit/feeds/*` (feed partials)
- `/svc/shreddit/comments/*` (comment partials)
- `/svc/shreddit/more-comments/*` (more-comments partials)
- `/svc/shreddit/pdp-right-rail/*` (sidebar partials)
- `/svc/shreddit/community-more-posts/*` (subreddit scroll-load partials)

All other URLs (events, GraphQL, styling, recaptcha, etc.) are passed through unmodified.

### R7: FilterResult Reporting

Return accurate `FilterResult` metadata:
- `Matched`: true if any ad markers were found in the body
- `Modified`: true if any elements were actually removed
- `Rule`: name of the rule that matched (e.g., `"feed-ad"`, `"comment-tree-ad"`, `"right-rail-ad"`)
- `Removed`: count of ad elements removed

When multiple rules match the same response, report the first matching rule name and the total count of all removed elements.

### R8: Placeholder Insertion

When removing ad elements, replace them with the placeholder from `Marker()` (configured via `placeholder` in fpsd.yml). The placeholder mode (`visible`, `comment`, or `none`) determines what replaces the removed content. Use the response's Content-Type when calling `Marker()`.

---

## Implementation Approach

### HTML Element Removal Strategy

Use byte-level string matching rather than a full HTML parser. Reddit's ad elements use unique custom element names that don't appear in any other context, making regex/string search safe:

1. Find opening tag `<shreddit-ad-post` (or other element name)
2. Find the corresponding closing tag `</shreddit-ad-post>`
3. Replace the entire span (inclusive) with the placeholder string

For nested containers like `<shreddit-comment-tree-ads>`, remove the entire container including children.

For `<ad-event-tracker>`, find `<ad-event-tracker` and its closing `</ad-event-tracker>`.

### File Structure

Modify `internal/plugin/reddit.go`:
- Replace the `InterceptionFilter` usage with a direct `ContentFilter` implementation
- Implement `Filter()` with the rules above
- Keep `Name()`, `Version()`, `Domains()`, `Init()` signatures the same

### Config Change

Switch mode from `"intercept"` to `"filter"` in `fpsd.yml`:

```yaml
plugins:
  reddit-promotions:
    enabled: true
    mode: "filter"
    placeholder: "visible"
    domains:
      - www.reddit.com
```

---

## Acceptance Criteria

- [x] Feed ads (`<shreddit-ad-post>`) are removed from homepage and feed partial responses
- [x] Comment-tree ads (`<shreddit-comment-tree-ads>` container) are removed from comment responses
- [x] Standalone comment-page ads (`<shreddit-comments-page-ad>`) are removed
- [x] Right-rail promoted posts (`<ad-event-tracker>` wrappers) are removed
- [x] Quick-skip: responses without ad markers pass through with zero string replacement
- [x] URL scoping: only matching URL patterns are processed
- [x] FilterResult reports accurate match/modify/rule/count data
- [x] Placeholder markers inserted per configured mode
- [x] Unit tests for each rule using captured HTML samples
- [x] Plugin switches from intercept to filter mode in fpsd.yml
- [x] Proxy builds, 0 lint issues, all tests pass
- [x] Live verification: browsing Reddit through the proxy shows no promoted posts

---

## Non-Goals

- Old Reddit (`old.reddit.com`) — different HTML structure, separate iteration
- Blocking `alb.reddit.com` tracking domain — domain-level concern, not plugin scope
- Modifying event tracking payloads — unnecessary for visual ad removal
- Full HTML parsing (e.g., `golang.org/x/net/html`) — overkill given unambiguous element names
