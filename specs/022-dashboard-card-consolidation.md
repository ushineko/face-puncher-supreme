# Spec 022: Dashboard Card Consolidation

**Status**: COMPLETE
**Created**: 2026-02-23
**Depends on**: Spec 009 (web dashboard), Spec 019 (stats watermarks & resources)

---

## Problem Statement

The dashboard Stats page currently renders seven stat cards in a responsive grid (1-4 columns depending on viewport). Several of these cards contain only 2-5 rows of data, resulting in small cards surrounded by whitespace. The "Connections" card (2 rows) and "MITM Interception" card (2 rows) are particularly sparse, and the "Server" card (7 rows) and "Resources" card (4 rows) are thematically related (both describe the proxy process itself rather than traffic).

This wastes vertical real estate and pushes the more useful Top-N tables further down the page.

---

## Approach

Consolidate the seven stat cards into fewer, denser cards by grouping thematically related data:

1. **Server & Resources** — Merge the "Server" and "Resources" cards into one card. Both describe the proxy process: identity (version, uptime, platform) and runtime health (goroutines, memory, FDs).

2. **Connections, Blocking & MITM** — Merge the "Connections", "Blocking", and "MITM Interception" cards into one card. All three describe request-level filtering activity: how many connections, what got blocked, what got intercepted.

The **Traffic** and **Plugins** cards remain unchanged — Traffic has 8 rows plus the line chart (already dense), and Plugins has dynamic per-plugin sections that grow with the number of active plugins.

### Before (7 cards)

| Server | Connections | Traffic | Blocking |
|--------|-------------|---------|----------|
| MITM   | Plugins     | Resources |        |

### After (4 cards)

| Server & Resources | Connections & Filtering | Traffic | Plugins |

This reduces the stat card grid from 7 cards (often spanning 2 rows) to 4 cards (fitting in a single row on wide viewports), moving the Top-N tables significantly higher on the page.

---

## Scope

### In Scope

- Merge "Server" + "Resources" card renderers into a single "Server" card
- Merge "Connections" + "Blocking" + "MITM Interception" card renderers into a single "Filtering" card
- Use visual separators (section headers or divider lines) within merged cards so the data remains scannable
- Update `useLayout` default card order to reflect the new card IDs
- Handle layout migration: users with saved card orders in localStorage get the new defaults
- MITM section within the Filtering card remains conditionally rendered (hidden when MITM is disabled)

### Out of Scope

- Changes to the Traffic or Plugins cards
- Changes to the Top-N tables section
- Changes to backend data structures or WebSocket messages
- Changes to the StatCard or StatRow components themselves
- Drag-and-drop behavior changes (still works, just fewer cards)

---

## Design

### 1. Server & Resources Card

**Card ID**: `server` (reuses existing ID for layout migration simplicity)
**Title**: "Server"

Combines the current Server and Resources card content with a section divider between them:

```
┌─────────────────────────┐
│ Server                  │
├─────────────────────────┤
│ Version     v1.5.0      │
│ Uptime      3d 14h 22m  │
│ Mode        blocking     │
│ MITM        12 domains   │
│ Plugins     2            │
│ Platform    linux/amd64  │
│ Go          go1.23.6     │
│─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─│
│ Goroutines  42           │
│ Heap        8.3 MB       │
│ Memory (OS) 24.1 MB      │
│ Open FDs    17 / 1024    │
└─────────────────────────┘
```

The divider is a subtle border (same style as the plugin section dividers in the Plugins card: `border-t border-vsc-border mt-2 pt-2`).

### 2. Filtering Card

**Card ID**: `filtering` (new ID, replaces `connections` + `blocking` + `mitm`)
**Title**: "Filtering"

Combines Connections, Blocking, and MITM data with section headers to delineate groups:

```
┌────────────────────────────────┐
│ Filtering                      │
├────────────────────────────────┤
│ Connections                    │
│ Total          14,832          │
│ Active         7               │
│─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─│
│ Blocking                       │
│ Blocked        1,247           │
│ Allowed        13,585          │
│ Blocklist      48,291 domains  │
│ Allowlist      12 entries      │
│ Sources        3               │
│─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─│
│ MITM                           │
│ Intercepts     892             │
│ Domains        12              │
└────────────────────────────────┘
```

The MITM section is conditionally rendered — when MITM is disabled, the card shows only Connections and Blocking sections (no empty section, no divider).

Section headers use the same accent text style as plugin names in the Plugins card (`text-xs text-vsc-accent`).

### 3. Card Order and Layout Changes

**File**: `web/ui/src/hooks/useLayout.ts`

Default card order changes from:
```typescript
["server", "connections", "traffic", "blocking", "mitm", "plugins", "resources"]
```
to:
```typescript
["server", "filtering", "traffic", "plugins"]
```

**Layout migration**: The `getCardOrder` method already handles unknown card IDs gracefully — it filters the saved order against the `available` set and appends any missing cards. When the old IDs (`connections`, `blocking`, `mitm`, `resources`) no longer appear in the available set, they are silently dropped. The new `filtering` card is appended as a new card. This means users with custom saved orders will get `filtering` appended to the end of their existing cards, which is acceptable — they can drag it to their preferred position.

**Chart visibility**: The `traffic` chart key is unchanged. The old `connections`, `blocking`, `mitm`, and `resources` chart keys (none of which had charts) need no migration.

### 4. Changes to Stats.tsx

The `cardRenderers` map changes:
- **Remove**: `connections`, `blocking`, `mitm`, `resources` keys
- **Modify**: `server` renderer to append Resources section
- **Add**: `filtering` renderer combining Connections + Blocking + conditional MITM

The `cardTitles` map changes:
- **Remove**: `connections`, `blocking`, `mitm`, `resources`
- **Add**: `filtering: "Filtering"`
- **Keep**: `server: "Server"`, `traffic: "Traffic"`, `plugins: "Plugins"`

The `availableCards` filtering logic:
- Remove the `mitm` conditional (MITM visibility is now handled inside the `filtering` renderer)
- Remove the separate `resources` entry
- Add `filtering` as always-available

---

## Implementation Plan

### Single Commit: Consolidate dashboard stat cards

**Modify** `web/ui/src/pages/Stats.tsx`:
1. Merge `resources` renderer content into `server` renderer with a divider
2. Create `filtering` renderer combining `connections`, `blocking`, and conditional `mitm` content with section headers
3. Remove `connections`, `blocking`, `mitm`, `resources` from `cardRenderers`
4. Update `cardTitles` map
5. Update `availableCards` array construction

**Modify** `web/ui/src/hooks/useLayout.ts`:
1. Update `DEFAULT_CARD_ORDER` to `["server", "filtering", "traffic", "plugins"]`

No backend changes. No new files. No new dependencies.

---

## Test Strategy

### Automated

No new Go tests required — this is a frontend-only change with no backend modifications.

UI verification via `make build` (Vite build catches TypeScript/import errors).

### Manual Verification

- Dashboard loads with 4 stat cards instead of 7
- "Server" card shows version/uptime/mode/MITM/plugins/platform/Go, then divider, then goroutines/heap/memory/FDs
- "Filtering" card shows Connections section, Blocking section, and MITM section (when enabled)
- MITM section hidden when MITM is disabled in config
- "Traffic" card unchanged (8 rows + line chart)
- "Plugins" card unchanged (per-plugin sections)
- Drag-and-drop reordering still works with the new card set
- Top-N tables appear higher on the page than before
- Users with saved layouts see the new cards after refresh (old IDs dropped, new ones appended)
- Responsive grid: 1 col mobile, 2 cols medium, 3-4 cols wide — all look reasonable with 4 cards

---

## Acceptance Criteria

- [ ] Dashboard shows 4 stat cards: Server, Filtering, Traffic, Plugins
- [ ] Server card contains all previous Server data (version, uptime, mode, MITM status, plugins, platform, Go version) followed by a visual divider and all previous Resources data (goroutines, heap, memory, open FDs)
- [ ] Filtering card contains labeled sections for Connections (total, active), Blocking (blocked, allowed, blocklist, allowlist, sources), and MITM (intercepts, domains)
- [ ] MITM section in Filtering card is hidden when MITM is disabled
- [ ] Section headers within merged cards use accent text styling consistent with plugin name styling
- [ ] Visual dividers between sections use the existing `border-vsc-border` style
- [ ] Default card order is `["server", "filtering", "traffic", "plugins"]`
- [ ] Users with saved custom layouts get new cards appended (no crash, no missing cards)
- [ ] Traffic card is unchanged (same rows, same line chart)
- [ ] Plugins card is unchanged (same per-plugin sections)
- [ ] Drag-and-drop reordering works with the 4-card set
- [ ] `make build` succeeds (Vite + Go build)
- [ ] No lint issues

---

## Non-Goals

- Collapsible sections within cards (sections are always visible)
- Per-section drag reordering within a merged card
- Tabbed views within individual cards
- Responsive breakpoint changes to the grid column count
- Renaming the `server` card ID (keeping it avoids layout migration complexity)
