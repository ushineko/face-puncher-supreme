# Spec 013: Dashboard UI Improvements

**Status**: COMPLETE
**Priority**: Medium
**Type**: Bugfix + Feature
**Scope**: Frontend only (React/TypeScript in `web/ui/`)

---

## Overview

Three improvements to the management web interface:

1. **Bugfix**: Anchor links in the README (About page) do not work
2. **Feature**: Draggable/repositionable dashboard sections with persistent layout
3. **Feature**: Inline charts — traffic line graph and top-N pie charts

No Go backend changes are required. All data needed for charts is already delivered via the existing WebSocket stats push.

---

## 1. Bugfix: Anchor Links in README

### Problem

The About page renders the project README via `react-markdown`. The table of contents contains anchor links (e.g., `[Features](#features)`), but clicking them either opens a blank tab or does nothing.

**Root causes:**

1. The `<a>` component override in `Markdown.tsx` sets `target="_blank"` on ALL links, including `#hash` anchors. Anchor links should scroll within the page, not open a new tab.
2. Heading components (`h1`, `h2`, `h3`) do not generate `id` attributes. Even if anchor links were handled correctly, there are no scroll targets.

### Fix

- **Headings**: Generate a slug `id` from heading text (lowercase, replace spaces/special chars with hyphens, strip non-alphanumeric). Apply to `h1`, `h2`, `h3`.
- **Links**: Detect `href` starting with `#`. For anchor links, use `onClick` to scroll to the target element within the page (smooth scroll). For external links, keep `target="_blank"` behavior.

### Acceptance Criteria

- [x] `h1`, `h2`, `h3` elements render with a slug-based `id` attribute derived from their text content
- [x] Clicking a `#hash` anchor link scrolls smoothly to the target heading within the About page
- [x] Anchor clicks do NOT open a new tab
- [x] External links (`http://`, `https://`) still open in a new tab with `rel="noopener noreferrer"`
- [x] Relative links without `#` prefix are unaffected (still open in new tab)

---

## 2. Feature: Draggable Dashboard Sections

### Design

Allow users to drag and reposition dashboard cards and top-N tables. Layout persists in `localStorage`.

### Approach

- Use the HTML5 Drag and Drop API (no external library). The dashboard already uses CSS grid; reordering items within a grid is straightforward.
- Each section gets a drag handle (a subtle grip icon in the header area).
- Sections can be reordered within their grid group:
  - **Stat cards** (top grid): Server, Connections, Traffic, Blocking, MITM, Plugins
  - **Top-N tables** (bottom grid): Top Blocked, Top Allowed, Top Requested, Top Clients, Top Intercepted, Top Rules
- Reordering saves a section-order array to `localStorage` keyed by grid group.
- A "Reset Layout" button restores the default order.

### State Model

```typescript
// Persisted to localStorage as JSON
interface DashboardLayout {
  cardOrder: string[];    // e.g., ["server", "connections", "traffic", "blocking", "mitm", "plugins"]
  tableOrder: string[];   // e.g., ["top-blocked", "top-allowed", "top-requested", "top-clients", ...]
}
```

- Default order is the current hardcoded order.
- On load, read from `localStorage("fps-dashboard-layout")`. If missing or malformed, use defaults.
- New sections that appear (e.g., MITM becomes enabled) are appended to the end of the saved order.
- Sections that disappear (e.g., MITM disabled) are hidden but their position is retained for when they reappear.

### Acceptance Criteria

- [x] Each dashboard section (stat cards and top-N tables) can be dragged and dropped to reorder within its grid group
- [x] A visible drag handle (grip icon) appears on each section header on hover
- [x] Drag feedback: visual indicator (opacity change or outline) on the dragged item and drop target
- [x] Reordered layout persists across page refreshes via `localStorage`
- [x] "Reset Layout" button in the dashboard header restores default section order
- [x] Conditional sections (MITM, Plugins, plugin rules) integrate correctly — appearing/disappearing based on data without breaking saved order
- [x] No external drag-and-drop library added (HTML5 DnD API only)

---

## 3. Feature: Inline Charts

### Design

Add lightweight, canvas-based charts alongside existing dashboard sections. Charts are rendered as companion elements that can be toggled visible/hidden.

### Charting Approach

Use `<canvas>` with direct 2D context drawing — no external charting library. The data volumes are small (top-25 lists, rolling time-series of a single metric) and the visual requirements are simple (line graph, pie chart). A lightweight custom renderer keeps the bundle size unchanged and avoids dependency management.

### Charts to Implement

#### A. Traffic Line Graph

- **Location**: Inline within / below the Traffic stat card (toggled via a small chart icon button in the card header)
- **Data**: Req/sec over time, sampled from WebSocket stats pushes (every 3s)
- **Buffer**: Rolling window of the last 60 data points (~3 minutes of history)
- **Rendering**: Simple line graph on `<canvas>`, VSCode dark theme colors
  - X-axis: time (relative, e.g., "-3m" to "now"), subtle grid lines
  - Y-axis: req/sec, auto-scaled
  - Line color: `--color-vsc-accent` (#569cd6)
  - Fill: subtle gradient under the line
  - Grid/axis: `--color-vsc-border` (#3c3c3c)
  - Labels: `--color-vsc-muted` (#808080), small monospace font

#### B. Top Domains Pie Chart

- **Location**: Inline within / beside the Top Blocked Domains and Top Requested Domains tables (toggled via chart icon)
- **Data**: Top 8 entries from the existing top-N lists (remaining entries grouped as "Other")
- **Rendering**: Pie/donut chart on `<canvas>`
  - Color palette: 8 distinct colors derived from the VSCode theme (accent, success, warning, error, plus 4 computed shades)
  - Legend: Small color-keyed legend below/beside the chart
  - Labels: Domain name + percentage on hover (tooltip) or in legend

#### C. Top Clients Pie Chart

- **Location**: Inline with the Top Clients table
- **Data**: Top 8 clients by request count, remaining as "Other"
- **Rendering**: Same pie/donut style as domain charts

### Chart Visibility

- Each chart-capable section gets a small toggle button (chart icon, e.g., a simple bar-chart SVG) in its header.
- Clicking toggles the chart visible/hidden for that section.
- Chart visibility state persists in `localStorage` alongside the dashboard layout.
- Default: charts hidden (text-only view matches current behavior).

### State Model Extension

```typescript
interface DashboardLayout {
  cardOrder: string[];
  tableOrder: string[];
  chartsVisible: Record<string, boolean>;  // e.g., {"traffic": false, "top-blocked": true, ...}
}
```

### Canvas Components

Create reusable chart components:

- `components/LineChart.tsx` — Accepts time-series data array, renders on canvas
- `components/PieChart.tsx` — Accepts labeled-value pairs, renders pie/donut on canvas

Both components should:
- Handle resize (observe parent width, maintain aspect ratio)
- Use theme colors from CSS custom properties
- Render cleanly at 2x DPI (devicePixelRatio scaling)
- Show a "No data" state when the data array is empty

### Acceptance Criteria

- [x] Traffic line graph shows req/sec over a rolling ~3-minute window, updated in real time from WebSocket data
- [x] Top Blocked Domains pie chart shows top 8 domains + "Other" slice
- [x] Top Requested Domains pie chart shows top 8 domains + "Other" slice
- [x] Top Clients pie chart shows top 8 clients + "Other" slice
- [x] Charts use `<canvas>` 2D context rendering, no external charting library
- [x] Each chart has a toggle button in its section header to show/hide
- [x] Chart visibility persists in `localStorage`
- [x] Default state: all charts hidden
- [x] Charts handle HiDPI displays (devicePixelRatio canvas scaling)
- [x] Charts resize correctly when viewport changes
- [x] Chart colors are consistent with the VSCode dark theme
- [x] Line chart has labeled axes, subtle grid lines, and a gradient fill under the line
- [x] Pie charts have a legend with domain/client names and percentages
- [x] Empty data state handled gracefully (no blank canvas, show "No data" text)

---

## Implementation Notes

### File Changes

| File | Change |
|------|--------|
| `web/ui/src/components/Markdown.tsx` | Add heading `id` generation, fix anchor link handling |
| `web/ui/src/pages/Stats.tsx` | Refactor sections to be reorderable, add chart toggles, integrate chart components |
| `web/ui/src/components/LineChart.tsx` | **New** — Canvas line chart component |
| `web/ui/src/components/PieChart.tsx` | **New** — Canvas pie/donut chart component |
| `web/ui/src/components/StatCard.tsx` | Add optional drag handle and chart toggle button props |
| `web/ui/src/components/TopTable.tsx` | Add optional drag handle and chart toggle/slot props |
| `web/ui/src/hooks/useLayout.ts` | **New** — Hook for dashboard layout persistence (localStorage read/write, drag handlers) |

### No Backend Changes

All chart data comes from the existing WebSocket `stats` messages. The line chart samples the req/sec rate that is already computed client-side in `Stats.tsx` (`useRate` hook). Pie chart data comes directly from the `top_blocked`, `top_requested`, and `top_by_requests` arrays already in the stats payload.

### No New Dependencies

- Anchor link fix: Pure React (slug function + onClick handler)
- Drag and drop: HTML5 DnD API (built into browsers)
- Charts: Canvas 2D API (built into browsers)

### Testing Notes

This spec is frontend-only. Verification is manual:
- Build with `make build-ui` and confirm no TypeScript/Vite errors
- Verify anchor links work in the About page
- Verify drag-and-drop reordering in the Stats page
- Verify charts render and toggle correctly
- Confirm `localStorage` persistence across page reloads

Existing Go tests (`make test`) should be unaffected since no backend changes are made.

---

## Out of Scope

- Backend API changes
- New WebSocket message types
- Historical stats (charts only show current session data from the rolling WebSocket feed)
- Chart data export
- Mobile-specific drag-and-drop gestures (touch DnD is a future enhancement)
