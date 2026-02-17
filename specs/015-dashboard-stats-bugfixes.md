# Spec 015: Dashboard Stats Bugfixes

**Status**: COMPLETE
**Priority**: High
**Type**: Bugfix
**Scope**: Frontend (React/TypeScript) + Backend (Go stats collection)

---

## Overview

Two bugs in the dashboard stats pipeline:

1. **Traffic graph frozen on first load** — When chart visibility is restored from `localStorage` as visible, the line graph shows "Collecting data..." indefinitely. The user must toggle it off then on again to see live data.
2. **Plugin stats always zero** — Plugin filter counters (inspected, matched, modified, top rules) are always 0 in the dashboard, even when the plugin is active and handling traffic. Confirmed via live testing with the reddit-promotions filter.

---

## Bug 1: Traffic Graph Frozen on Restored Layout

### Root Cause

The traffic history is stored in a `useRef<TimePoint[]>([])` ([Stats.tsx:122](web/ui/src/pages/Stats.tsx#L122)). On each stats WebSocket push, the array is mutated in-place via `push()` and `splice()` ([Stats.tsx:137-138](web/ui/src/pages/Stats.tsx#L137-L138)).

The `LineChart` component receives `data={trafficHistory.current}` ([Stats.tsx:306](web/ui/src/pages/Stats.tsx#L306)). Its canvas draw effect depends on `[data, label, accentColor]` ([LineChart.tsx:164](web/ui/src/components/LineChart.tsx#L164)). Since `trafficHistory.current` is always the **same array reference** (mutated, not replaced), React's effect dependency check sees no change. The draw effect never re-fires after the initial mount.

**Why toggling fixes it:** When the user hides then shows the chart, the `LineChart` component unmounts and remounts. The fresh mount triggers the useEffect, which now sees the populated array and renders correctly.

**Why it only manifests on restored layouts:** The default `chartsVisible` is `{}` (all charts hidden). On a fresh session, the user manually toggles the chart on, which mounts `LineChart` fresh — by that time, a few data points have accumulated, and subsequent ref mutations happen to coincide with other state changes that trigger parent re-renders. When `chartsVisible: { traffic: true }` is restored from localStorage, the chart mounts immediately at page load with an empty array, shows "Collecting data...", and never updates because no dependency changes.

### Fix

Replace the `useRef` approach with a pattern that gives `LineChart` a new array reference on each update, so the effect dependency triggers correctly.

**Option A (state-based):** Convert `trafficHistory` from `useRef` to `useState`. Each stats push creates a new array via spread or slice, triggering a re-render of the `LineChart`.

**Option B (ref + counter):** Keep the ref for the array but add a `useState` counter that increments on each push, passed as a key or dependency to force re-renders.

**Recommended: Option A.** It is the simplest and most idiomatic React pattern. The array is small (max 60 elements) and updates every 3 seconds, so the re-render cost is negligible.

```typescript
// Before (broken):
const trafficHistory = useRef<TimePoint[]>([]);
if (stats) {
  const hist = trafficHistory.current;
  hist.push({ time: now, value: rate });
  if (hist.length > MAX_HISTORY) hist.splice(0, hist.length - MAX_HISTORY);
}
// data={trafficHistory.current}  <-- same reference every time

// After (fixed):
const [trafficHistory, setTrafficHistory] = useState<TimePoint[]>([]);
useEffect(() => {
  if (!stats) return;
  setTrafficHistory(prev => {
    const next = [...prev, { time: Date.now(), value: parseFloat(reqRate) }];
    return next.length > MAX_HISTORY ? next.slice(next.length - MAX_HISTORY) : next;
  });
}, [stats, reqRate]);
// data={trafficHistory}  <-- new reference on each update
```

Note: The rate computation for the first data point (when `prev` is empty) will naturally be 0, matching current behavior.

### Files Changed

| File | Change |
|------|--------|
| `web/ui/src/pages/Stats.tsx` | Replace `useRef` traffic history with `useState` + `useEffect` pattern |

---

## Bug 2: Plugin Stats Always Zero

### Root Cause

`RecordPluginInspected()` is defined in [collector.go:222](internal/stats/collector.go#L222) but **never called** from anywhere in the codebase. The `BuildResponseModifier` callback in [main.go:671-672](cmd/fpsd/main.go#L671-L672) only calls `RecordPluginMatch()`:

```go
modifier := plugin.BuildResponseModifier(results, func(pluginName, rule string, modified bool, removed int) {
    collector.RecordPluginMatch(pluginName, rule, modified, removed)
}, logger)
```

`SnapshotPlugins()` ([collector.go:253-271](internal/stats/collector.go#L253-L271)) iterates over `pluginInspected` as the **primary sync.Map driver**:

```go
func (c *Collector) SnapshotPlugins() []PluginSnapshot {
    var out []PluginSnapshot
    c.pluginInspected.Range(func(key, value any) bool {
        // ... builds snapshot from pluginInspected, then looks up matched/modified
    })
    return out
}
```

Since `RecordPluginInspected` is never called, `pluginInspected` is always empty. `SnapshotPlugins()` returns `[]`. Even though `RecordPluginMatch` IS populating `pluginMatched`, `pluginModified`, and `pluginRules`, those maps are only accessed as secondary lookups inside the `pluginInspected.Range()` loop — which never executes.

In `buildPluginsBlock()` ([probe.go:452-476](internal/probe/probe.go#L452-L476)), the code correctly iterates `pd.Plugins` (from InitResults) and tries to match by name against the snapshot. But with an empty snapshot, no stats are ever populated. All plugin entries show 0/0/0 and empty top_rules.

### Fix

Two changes are needed:

**A. Wire `RecordPluginInspected` into `BuildResponseModifier`:**

Every time a response is dispatched to a plugin for inspection (regardless of whether the filter matches), call `RecordPluginInspected`. This requires adding a second callback to `BuildResponseModifier`, or combining both callbacks into a single `OnPluginEvent` interface.

In [registry.go](internal/plugin/registry.go), inside the response modifier closure, add the inspected recording call before calling the plugin's `Filter()` method:

```go
return func(domain string, req *http.Request, resp *http.Response, body []byte) ([]byte, error) {
    ent, ok := lookup[strings.ToLower(domain)]
    if !ok {
        return body, nil
    }
    onInspect(ent.plugin.Name())  // <-- NEW: record inspection
    newBody, result, err := ent.plugin.Filter(req, resp, body)
    // ... existing match handling ...
}
```

In [main.go](cmd/fpsd/main.go), wire the new callback:

```go
modifier := plugin.BuildResponseModifier(results,
    func(pluginName string) {
        collector.RecordPluginInspected(pluginName)
    },
    func(pluginName, rule string, modified bool, removed int) {
        collector.RecordPluginMatch(pluginName, rule, modified, removed)
    },
    logger,
)
```

**B. Update `BuildResponseModifier` signature:**

Change `OnFilterMatch` to two callbacks (or a struct):

```go
type OnPluginInspect func(pluginName string)
type OnFilterMatch func(pluginName, rule string, modified bool, removed int)

func BuildResponseModifier(
    results []InitResult,
    onInspect OnPluginInspect,
    onMatch OnFilterMatch,
    logger *slog.Logger,
) mitm.ResponseModifier
```

### Files Changed

| File | Change |
|------|--------|
| `internal/plugin/registry.go` | Add `OnPluginInspect` callback type, update `BuildResponseModifier` signature and closure to call `onInspect` before `Filter()` |
| `cmd/fpsd/main.go` | Pass `collector.RecordPluginInspected` callback to updated `BuildResponseModifier` |
| `internal/plugin/plugin_test.go` | Update `BuildResponseModifier` test calls to pass both callbacks |

---

## Acceptance Criteria

### Bug 1: Traffic Graph

- [x] Traffic line graph renders and updates in real time when `chartsVisible` is restored from `localStorage` as `true` on first page load
- [x] No "Collecting data..." state persists beyond the initial 2 data points (~6 seconds)
- [x] Chart still works correctly when toggled on/off manually
- [x] Rolling window still caps at 60 data points
- [x] Existing Go tests pass (`make test`)
- [x] UI builds without errors (`make build-ui`)

### Bug 2: Plugin Stats

- [x] `RecordPluginInspected` is called for every response dispatched to a plugin
- [x] `SnapshotPlugins()` returns non-empty data when plugins have handled traffic
- [x] Dashboard plugin card shows non-zero inspected/matched/modified counts when a plugin is active and processing traffic
- [x] Plugin top_rules table shows rule match counts
- [x] Existing tests pass (`make test`)
- [x] New/updated tests cover the inspected callback wiring
- [x] Lint passes (`make lint`)

---

## Out of Scope

- Plugin stats persistence to SQLite (currently in-memory only; a separate spec if needed)
- Historical plugin stats across restarts
- ResizeObserver integration for LineChart (existing approach is adequate)
- Other chart types (pie charts are not affected by this bug)
