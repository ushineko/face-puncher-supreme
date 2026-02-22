# Spec 017: Reddit iOS App GraphQL Ad Filtering

**Status**: DRAFT

---

## Background

The existing `reddit-promotions` plugin (spec 007) filters ads from Reddit's
web interface by stripping HTML elements (`<shreddit-ad-post>`, etc.) from
server-rendered responses on `www.reddit.com`. This works for Safari and
desktop browsers but has no effect on the Reddit iOS app, which uses a
completely separate API.

The iOS app communicates via GraphQL to `gql-fed.reddit.com` (Apollo
Federation gateway). Traffic analysis on 2026-02-21 confirmed:

1. **No certificate pinning** -- the app accepts the fpsd CA cert via the
   transparent proxy. MITM interception succeeds with `application/json`
   responses fully visible.
2. **Ads are structurally distinct** -- promoted content uses different
   GraphQL types and carries ad-exclusive fields, making detection reliable.
3. **Ads are inline** -- in the home feed, ads are interleaved with organic
   posts in the same response arrays (no separate ad query).

## Objective

Extend fpsd to filter promoted content from the Reddit iOS app's GraphQL
API responses, removing ads from the home feed and post detail pages.

---

## Architecture: Server-Driven UI (SDUI)

The Reddit iOS app uses a two-query pattern for feeds:

### Query 1: `HomeFeedSdui` (layout)

Returns a Server-Driven UI specification. The server sends the exact cells
and layout the client should render. The client is a thin renderer.

```
data.homeV3.elements.edges[]  (FeedElementEdge)
  node: CellGroup
    id: string           (base64-encoded)
    groupId: string      (Reddit post ID, e.g., "t3_1r9bkjp")
    adPayload: null | AdPayload   <-- PRIMARY AD SIGNAL
    cells: [MetadataCell | AdMetadataCell | TitleCell | ...]
```

Organic edges have `adPayload: null`. Ad edges have a populated `AdPayload`
object containing ~33 tracking event URLs, campaign metadata, and encrypted
tracking payloads. Each ad edge averages 20.7KB vs 2.5KB for organic (8.2x
larger due to tracking overhead).

Ad-exclusive cell types: `AdMetadataCell`, `CallToActionCell`.

### Query 2: `FeedPostDetailsByIds` (data)

Returns post data for every item in the SDUI layout, in a flat array:

```
data.postsInfoByIds[]
  __typename: "SubredditPost" | "ProfilePost"
```

Organic posts are `SubredditPost`. Ad posts are `ProfilePost` with
`isCreatedFromAdsUi: true`. There is a 1:1 correspondence between
SDUI `groupId` values and entries in this array.

### Post Detail Pages: `PdpCommentsAds`

On post detail pages, ads arrive via a separate query (`PdpCommentsAds`)
that returns `AdPost` objects nested under
`data.postInfoById.pdpCommentsAds.adPosts[]`.

---

## Detection Signals

Ordered by reliability:

### Home Feed (HomeFeedSdui)

| Priority | Signal | Location | Values |
|----------|--------|----------|--------|
| P1 | `adPayload != null` | `edges[].node.adPayload` | null (organic) vs AdPayload object (ad) |
| P2 | Cell type | `edges[].node.cells[].typename` | `AdMetadataCell`, `CallToActionCell` (ad-only) |

### Feed Details (FeedPostDetailsByIds)

| Priority | Signal | Location | Values |
|----------|--------|----------|--------|
| P1 | `__typename` | `postsInfoByIds[].__typename` | `SubredditPost` (organic) vs `ProfilePost` (ad) |
| P2 | `isCreatedFromAdsUi` | `postsInfoByIds[].isCreatedFromAdsUi` | `false` (organic) vs `true` (ad) |

### Post Detail Ads (PdpCommentsAds)

| Priority | Signal | Location | Values |
|----------|--------|----------|--------|
| P1 | `__typename` | `pdpCommentsAds.adPosts[].__typename` | `AdPost` |
| P2 | Operation name | `X-Apollo-Operation-Name` header | `PdpCommentsAds` |

---

## Requirements

### R1: Home Feed SDUI Filtering

When the `X-Apollo-Operation-Name` header is `HomeFeedSdui`:

- Parse the JSON response
- Remove edges from `data.homeV3.elements.edges[]` where
  `edge.node.adPayload != null`
- If the last remaining edge's `groupId` differs from the original last
  edge, update `data.homeV3.elements.pageInfo.endCursor` to the base64
  encoding of the last remaining edge's `groupId`
- Re-serialize and return the modified JSON

### R2: Feed Post Details Filtering

When the `X-Apollo-Operation-Name` header is `FeedPostDetailsByIds`:

- Parse the JSON response
- Remove entries from `data.postsInfoByIds[]` where `__typename` is
  `ProfilePost`
- Re-serialize and return the modified JSON

### R3: PDP Comment Ads Filtering

When the `X-Apollo-Operation-Name` header is `PdpCommentsAds`:

- Parse the JSON response
- Empty the `data.postInfoById.pdpCommentsAds.adPosts` array (set to `[]`)
- Re-serialize and return the modified JSON

### R4: Passthrough for Other Operations

All other GraphQL operations (e.g., `PostInfoById`, `BadgeCounts`,
`GetAccount`, `DynamicConfigsByNames`) pass through unmodified.

### R5: Operation Name Detection

Use the `X-Apollo-Operation-Name` HTTP request header to identify which
response handler to invoke. If the header is absent, pass through
unmodified.

### R6: Plugin Domain Registration

Register `gql-fed.reddit.com` as a new plugin domain. This domain must be:
- Added to `mitm.domains` in config
- Claimed by a plugin (either the existing `reddit-promotions` or a new
  plugin)

### R7: Consistent ID Removal

When filtering `HomeFeedSdui`, collect the `groupId` values of removed
edges. When filtering the corresponding `FeedPostDetailsByIds` response,
remove posts whose `id` (t3_xxx) matches any removed `groupId`.

Note: R7 requires cross-request state within a MITM session. If this adds
unacceptable complexity, R1 and R2 can operate independently using their
own detection signals (adPayload and __typename respectively), which are
individually sufficient.

---

## Design Decisions

### Single Plugin vs. Separate Plugin

**Option A**: Extend `reddit-promotions` to handle both HTML (www.reddit.com)
and JSON (gql-fed.reddit.com), dispatching by Content-Type.

**Option B**: Create a new `reddit-graphql` plugin for `gql-fed.reddit.com`.

**Recommendation**: Option A. The domain ownership model (one plugin per
domain) already separates dispatch. A single plugin simplifies config and
keeps all Reddit ad-filtering logic together. The `Filter()` method
dispatches by Content-Type: `text/html` -> existing HTML rules,
`application/json` -> new GraphQL rules.

### JSON Parsing Strategy

**Option A**: Full `json.Unmarshal` into `map[string]any`, modify, re-marshal.

**Option B**: Use a streaming/path-based JSON library (e.g., `gjson`/`sjson`)
to modify specific paths without full parse.

**Recommendation**: Option A for correctness and simplicity. Feed responses
are typically 100-400KB, well within the existing 10MB MITM buffer limit.
Full parse ensures structural validity of the output. The performance cost
of marshal/unmarshal at these sizes is negligible compared to network
latency.

### Placeholder Strategy

For JSON responses, use the existing `Marker()` function's JSON mode:
- `visible`: `{"fps_filtered":"reddit-promotions/feed-ad"}`
- `comment`: `{"_fps_filtered":"reddit-promotions/feed-ad"}`
- `none`: element removed entirely (no placeholder)

In `none` and `comment` mode, ad entries are simply removed from their
arrays. In `visible` mode, ad entries are replaced with the placeholder
object to make filtering visible during development.

---

## Config Changes

```yaml
mitm:
  domains:
    - www.reddit.com
    - old.reddit.com
    - gql-fed.reddit.com      # NEW

plugins:
  reddit-promotions:
    enabled: true
    mode: "filter"
    placeholder: "visible"
    domains:
      - www.reddit.com
      - gql-fed.reddit.com    # NEW
    options:
      log_matches: true
```

---

## Acceptance Criteria

- [ ] `HomeFeedSdui` responses have ad edges removed (adPayload != null)
- [ ] `FeedPostDetailsByIds` responses have ProfilePost entries removed
- [ ] `PdpCommentsAds` responses have adPosts array emptied
- [ ] Other GraphQL operations pass through unmodified
- [ ] Existing HTML filtering on www.reddit.com continues to work
- [ ] FilterResult reports accurate Matched/Modified/Removed counts
- [ ] Plugin stats track GraphQL filter matches separately from HTML matches
  (distinct rule names, e.g., `feed-sdui-ad`, `feed-details-ad`,
  `pdp-comments-ad`)
- [ ] Reddit iOS app renders feeds without errors after filtering
- [ ] Pagination continues to work (endCursor preserved correctly)
- [ ] Unit tests cover all three filter rules with representative payloads
- [ ] `make test` and `make lint` pass

---

## Risks

### Client-Side Integrity Checks

The Reddit app could detect missing ads (it knows how many it requested).
No evidence of such checks exists in the captured traffic, but if Reddit
adds them, the app may degrade or show error states. Mitigation: monitor
app behavior after enabling the filter.

### Schema Changes

Reddit's GraphQL schema is private and can change without notice. Field
names (`adPayload`, `ProfilePost`, etc.) could be renamed. Mitigation:
the filter should fail open -- if expected fields are missing, return the
response unmodified rather than erroring.

### Performance

Full JSON parse/marshal of 400KB responses adds latency. At typical sizes
this is <1ms on modern hardware. The existing 10MB buffer limit already
handles responses of this size for HTML filtering.

### Response Compression

The MITM handler already strips `Accept-Encoding` when a ResponseModifier
is set, forcing uncompressed upstream responses. No additional handling
needed.

---

## Test Data

### Reference Captures (not committed)

Raw traffic captures from 2026-02-21 are on disk for reference during
development:
```
~/.local/share/fpsd/intercepts/traffic-capture/2026-02-21T20-34-40/
```
These contain real user data and must NOT be committed to the repository.

### Synthetic Test Fixtures (committed)

Build minimal synthetic JSON fixtures that exercise each filter rule. These
contain only the structural fields the filter inspects -- no real usernames,
tokens, tracking IDs, or post content.

Each fixture should include a mix of organic and ad entries to verify that
ads are removed and organic content is preserved.

**HomeFeedSdui fixture** (~2-3KB):

- 3 organic edges (`adPayload: null`, cells with `MetadataCell`)
- 2 ad edges (`adPayload: {...}`, cells with `AdMetadataCell` +
  `CallToActionCell`)
- Valid `pageInfo` with `endCursor` (test cursor preservation when last
  edge is organic vs. ad)

**FeedPostDetailsByIds fixture** (~1-2KB):

- 3 `SubredditPost` entries (organic)
- 2 `ProfilePost` entries (`isCreatedFromAdsUi: true`)
- Post IDs matching the SDUI fixture's `groupId` values

**PdpCommentsAds fixture** (~1KB):

- `data.postInfoById.pdpCommentsAds.adPosts` with 2 `AdPost` entries
- Minimal ad fields (just `__typename`, `id`, `adEvents: []`)

**Edge cases to cover**:

- Response with zero ads (passthrough, no modification)
- Response where the last edge is an ad (endCursor must update)
- Malformed/unexpected JSON (fail open, return unmodified)
- Missing `X-Apollo-Operation-Name` header (passthrough)
