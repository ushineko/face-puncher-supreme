# Spec 019: Stats High Watermarks, Resource Monitoring, and UI Text Selection Fix

**Status**: COMPLETE

---

## Background

The stats system currently tracks cumulative counters (total requests, total
bytes in/out) and the UI derives instantaneous rates (req/sec, In/sec) via
client-side delta calculation. There is no record of peak throughput — once a
burst passes, the high watermark is lost.

The proxy also reports no information about its own resource footprint. Operators
have no visibility into memory consumption or socket pressure without reaching
for external tools.

Separately, a UI bug prevents users from selecting and copying text inside
dashboard cards and tables. The entire card `<div>` is marked `draggable`, so
any click-and-drag gesture initiates the drag-and-drop reorder flow instead of
text selection.

---

## Objective

1. Track and expose **peak req/sec** and **peak bytes In/sec** (high watermarks)
   that persist for the lifetime of the process.
2. Add **process resource stats**: open file descriptors (proxy for sockets),
   FD limit, and memory usage (current RSS, peak RSS).
3. Fix dashboard text selection by restricting drag initiation to the drag
   handle element only.

---

## Scope

### In scope

- `internal/stats/collector.go` — watermark tracking
- `internal/probe/probe.go` — resource metrics collection, API response changes
- `web/ui/src/components/StatCard.tsx` — drag handle isolation
- `web/ui/src/components/TopTable.tsx` — drag handle isolation
- `web/ui/src/pages/Stats.tsx` — display new stats fields, new "Resources" card

### Out of scope

- Persisting watermarks or resource stats to SQLite (in-memory only; resets on
  restart — this matches current rate behavior)
- Per-client or per-domain watermark tracking (global only)
- CPU usage (Go's `runtime` package doesn't expose process CPU time; reading
  `/proc/self/stat` is Linux-only and adds complexity for marginal value)
- Historical resource usage trends or charting (can be added later)

---

## Part 1: High Watermarks in Collector

### Design

The collector already tracks `TotalRequests()` and `TotalBytesIn()` as
monotonically increasing atomic counters. Watermarks need a rate computation
inside the collector itself — the backend must own this because the frontend
rate calculation is per-browser-tab and doesn't survive page reloads.

Add a periodic sampler goroutine to the collector that:

1. Runs every 1 second (matches typical rate granularity)
2. Computes `reqRate = (currentRequests - prevRequests) / dt`
3. Computes `bytesInRate = (currentBytesIn - prevBytesIn) / dt`
4. Updates peak atomics via compare-and-swap loop

### New Fields on Collector

```go
// Watermark tracking (atomic, updated by sampler goroutine)
peakReqPerSec   atomic.Int64  // stored as millireqs/sec (x1000) for int precision
peakBytesInSec  atomic.Int64  // bytes/sec

// Sampler lifecycle
samplerStop chan struct{}
samplerDone chan struct{}
```

Using millireqs/sec (multiply rate by 1000, store as int64) avoids float atomics
while preserving one decimal place of precision. The probe endpoint divides by
1000.0 when serializing.

### Sampler Goroutine

```go
func (c *Collector) startSampler() {
    c.samplerStop = make(chan struct{})
    c.samplerDone = make(chan struct{})
    go c.runSampler()
}

func (c *Collector) runSampler() {
    defer close(c.samplerDone)
    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()

    var prevReqs, prevBytes int64
    var prevTime time.Time

    for {
        select {
        case <-c.samplerStop:
            return
        case now := <-ticker.C:
            if prevTime.IsZero() {
                prevReqs = c.TotalRequests()
                prevBytes = c.TotalBytesIn()
                prevTime = now
                continue
            }
            dt := now.Sub(prevTime).Seconds()
            if dt <= 0 {
                continue
            }

            curReqs := c.TotalRequests()
            curBytes := c.TotalBytesIn()

            reqRate := int64(float64(curReqs-prevReqs) / dt * 1000)   // millireqs/sec
            bytesRate := int64(float64(curBytes-prevBytes) / dt)

            // CAS loop for peak update
            for {
                old := c.peakReqPerSec.Load()
                if reqRate <= old || c.peakReqPerSec.CompareAndSwap(old, reqRate) {
                    break
                }
            }
            for {
                old := c.peakBytesInSec.Load()
                if bytesRate <= old || c.peakBytesInSec.CompareAndSwap(old, bytesRate) {
                    break
                }
            }

            prevReqs = curReqs
            prevBytes = curBytes
            prevTime = now
        }
    }
}

func (c *Collector) StopSampler() {
    if c.samplerStop != nil {
        close(c.samplerStop)
        <-c.samplerDone
    }
}

func (c *Collector) PeakReqPerSec() float64  { return float64(c.peakReqPerSec.Load()) / 1000.0 }
func (c *Collector) PeakBytesInSec() int64    { return c.peakBytesInSec.Load() }
```

### Lifecycle

`startSampler()` is called from `NewCollector()`. `StopSampler()` is called
during shutdown (add to `runServers` cleanup or defer in `runProxy`).

---

## Part 2: Process Resource Stats

### Data Source

Go provides `runtime.MemStats` for memory. For file descriptors on Linux,
read `/proc/self/fd` (count entries) and `/proc/self/limits` (parse max open
files). Wrap in a platform-abstracted helper with a stub for non-Linux.

### New File: `internal/probe/resources.go`

```go
type ResourceStats struct {
    MemAllocMB    float64 `json:"mem_alloc_mb"`     // current heap allocation
    MemSysMB      float64 `json:"mem_sys_mb"`       // total memory from OS
    MemHeapInuse  float64 `json:"mem_heap_inuse_mb"` // heap in use
    NumGoroutine  int     `json:"goroutines"`
    OpenFDs       int     `json:"open_fds"`         // -1 if unavailable
    MaxFDs        int     `json:"max_fds"`           // -1 if unavailable
}

func collectResourceStats() ResourceStats { ... }
```

### New File: `internal/probe/resources_linux.go`

```go
//go:build linux

func countOpenFDs() int {
    entries, err := os.ReadDir("/proc/self/fd")
    if err != nil {
        return -1
    }
    return len(entries)
}

func getMaxFDs() int {
    // Parse /proc/self/limits for "Max open files"
    ...
}
```

### New File: `internal/probe/resources_stub.go`

```go
//go:build !linux

func countOpenFDs() int { return -1 }
func getMaxFDs() int    { return -1 }
```

### API Response Changes

Add to `StatsResponse`:

```go
type StatsResponse struct {
    // ... existing fields ...
    Resources ResourceStats     `json:"resources"`
    Watermarks WatermarkStats   `json:"watermarks"`
}

type WatermarkStats struct {
    PeakReqPerSec   float64 `json:"peak_req_per_sec"`
    PeakBytesInSec  int64   `json:"peak_bytes_in_sec"`
}
```

The `resources` and `watermarks` fields are always present in the response.
`open_fds` and `max_fds` are -1 on non-Linux platforms.

---

## Part 3: UI Text Selection Fix

### Problem

Both `StatCard` and `TopTable` set `draggable` on the outermost `<div>`. The
browser's drag-and-drop API intercepts all mousedown+drag gestures on that
element and its children, preventing normal text selection.

### Solution: Handle-Only Dragging

Remove `draggable` from the outer `<div>`. Instead, make only the drag handle
(`⠿`) the drag initiator. The HTML drag-and-drop API allows setting `draggable`
on a child element and using event propagation for the drag data.

However, the drag preview (ghost image) shows only the handle element by
default, not the full card. To get a full-card drag preview while keeping
text selectable in the card body:

1. The drag handle `<span>` gets `draggable="true"`.
2. On `dragstart`, call `e.dataTransfer.setDragImage(cardRef, ...)` to use the
   full card as the ghost image.
3. The card body is no longer `draggable`, so text selection works normally.

### StatCard Changes

```tsx
export default function StatCard({
  title, children, draggable,
  onDragStart, onDragEnd, onDragOver, onDrop,
  ...
}: StatCardProps) {
  const cardRef = useRef<HTMLDivElement>(null);

  return (
    <div
      ref={cardRef}
      className="bg-vsc-surface border border-vsc-border rounded p-4 transition-opacity"
      // draggable removed from here
      onDragOver={onDragOver}
      onDrop={onDrop}
    >
      <div className="flex items-center mb-3 gap-2">
        {draggable && (
          <span
            className="... cursor-grab active:cursor-grabbing select-none ..."
            draggable
            onDragStart={(e) => {
              if (cardRef.current) {
                e.dataTransfer.setDragImage(cardRef.current, 0, 0);
              }
              onDragStart?.(e as any);
            }}
            onDragEnd={onDragEnd}
          >
            ⠿
          </span>
        )}
        ...
      </div>
      {children}
      ...
    </div>
  );
}
```

The same pattern applies to `TopTable`.

### Behavioral Result

- **Drag handle (`⠿`)**: Click-and-drag initiates card reorder. Ghost image
  shows the full card.
- **Card body**: Normal text selection and copy works. Clicking does not
  trigger drag.
- **Drop target**: The `onDragOver` and `onDrop` handlers remain on the outer
  `<div>` so cards can still be dropped onto each other.

---

## Part 4: UI Display

### New "Resources" Card

Add a `resources` card to the stat card grid (after "connections" in default
order):

| Row | Value |
|---|---|
| Goroutines | `{goroutines}` |
| Heap | `{mem_heap_inuse_mb} MB` |
| Memory (OS) | `{mem_sys_mb} MB` |
| Open FDs | `{open_fds} / {max_fds}` (or `N/A` if -1) |

### Watermarks in Traffic Card

Add two rows to the existing "Traffic" card:

| Row | Value |
|---|---|
| Peak Req/sec | `{peak_req_per_sec}` |
| Peak In/sec | `formatBytes({peak_bytes_in_sec})` |

These appear below the existing "Req/sec" and "In/sec" rows respectively,
visually grouped with them.

---

## Test Strategy

### Collector Sampler

- **Unit test**: Start sampler, record known number of requests, wait >1s,
  verify `PeakReqPerSec() > 0`. Stop sampler, verify goroutine exits.
- **Unit test**: Verify watermark only increases (record burst, wait, record
  lower rate, verify peak unchanged).

### Resource Stats

- **Unit test**: `collectResourceStats()` returns non-zero `MemSysMB` and
  `NumGoroutine`. On Linux CI, `OpenFDs > 0`.
- No test for `/proc/self/limits` parsing edge cases — the function returns
  -1 on failure, which is an acceptable degradation.

### Text Selection

- Manual verification: open dashboard, select text in card body, copy to
  clipboard. Verify drag handle still reorders cards.

### Existing Tests

- All 128+ existing tests continue to pass.

---

## Implementation Plan

### Commit 1: Add watermark tracking to stats collector

1. Add atomic fields and sampler goroutine to `collector.go`
2. Add `PeakReqPerSec()` and `PeakBytesInSec()` accessors
3. Wire `startSampler()` into `NewCollector()`, `StopSampler()` into shutdown
4. Add unit tests for watermark behavior

### Commit 2: Add process resource stats

1. Create `internal/probe/resources.go` with `ResourceStats` struct and
   `collectResourceStats()`
2. Create `resources_linux.go` and `resources_stub.go` with FD helpers
3. Add `resources` and `watermarks` to `StatsResponse` in `probe.go`
4. Wire collector watermark accessors into probe builder
5. Add unit test for `collectResourceStats()`

### Commit 3: Fix UI text selection and add new stat displays

1. Refactor `StatCard` and `TopTable` to use handle-only dragging
2. Add `resources` card to `Stats.tsx`
3. Add watermark rows to traffic card
4. Update TypeScript interfaces for new API fields

### Validation (all commits)

- [ ] `make test` — all tests pass
- [ ] `make lint` — 0 warnings
- [ ] `go vet ./...` — clean
- [ ] `make build` — binary builds (includes UI)
- [ ] Service starts, `/fps/stats` returns `resources` and `watermarks` fields
- [ ] Dashboard text is selectable; drag handle still reorders cards

---

## Acceptance Criteria

- [ ] `/fps/stats` response includes `watermarks.peak_req_per_sec` and
      `watermarks.peak_bytes_in_sec`
- [ ] Watermarks only increase during process lifetime (never decrease)
- [ ] `/fps/stats` response includes `resources` with `goroutines`,
      `mem_alloc_mb`, `mem_sys_mb`, `mem_heap_inuse_mb`, `open_fds`, `max_fds`
- [ ] `open_fds` and `max_fds` return real values on Linux, -1 on other
      platforms
- [ ] Dashboard "Traffic" card shows peak req/sec and peak In/sec rows
- [ ] Dashboard has a "Resources" card showing goroutines, heap, memory, FDs
- [ ] Text inside all dashboard cards and tables can be selected and copied
- [ ] Drag handle (`⠿`) still initiates card/table reorder with full-card
      ghost image
- [ ] All quality gates pass (tests, lint, vet)
