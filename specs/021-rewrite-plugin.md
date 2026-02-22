# Spec 021: Content Rewrite Plugin

**Status**: PENDING
**Created**: 2026-02-22
**Depends on**: Spec 007 (content filter plugin architecture), Spec 009 (web dashboard)

---

## Problem Statement

fpsd currently supports one content filter plugin (reddit-promotions) with hardcoded
filtering logic and static configuration via `fpsd.yml`. There is no general-purpose
mechanism for users to define custom content rewrite rules through the web UI without
writing Go code and rebuilding the binary.

Users want to:
- Replace specific words or patterns in proxied HTML/JSON/text responses
- Scope rewrites to specific domains and URL paths
- Add, edit, and remove rules without restarting the proxy
- Use regex patterns for flexible matching
- Chain the rewrite plugin with existing plugins (e.g., reddit-promotions removes ads
  first, then rewrite rules apply to the remaining content)

Additionally, the Config tab in the dashboard currently shows only a raw JSON dump of
the proxy configuration. It needs structured UI for managing rewrite rules.

---

## Approach

### Architecture Overview

```
                   ┌────────────────────────────┐
                   │   MITM Response Modifier    │
                   │                              │
                   │  domain: www.reddit.com      │
                   │  ┌──────────────────────┐   │
request/response──>│  │ reddit-promotions     │   │──> modified body
                   │  │ (priority: 100)       │   │
                   │  └──────────┬───────────┘   │
                   │             │ output         │
                   │  ┌──────────▼───────────┐   │
                   │  │ rewrite              │   │
                   │  │ (priority: 900)       │   │
                   │  └──────────────────────┘   │
                   └────────────────────────────┘
```

Two independent changes:
1. **Plugin chaining** — Allow multiple plugins to process the same domain in
   priority order (lower number = higher priority = runs first)
2. **Rewrite plugin** — A new `ContentFilter` plugin with API-managed rules,
   regex support, and web UI configuration

### Rule Storage

Rules are persisted in a SQLite database at `<data_dir>/rewrite.db`, consistent with
the existing blocklist and stats databases. SQLite provides proper concurrent access
via WAL mode, atomic transactions, and efficient per-row CRUD without needing to
rewrite the entire dataset on every change. This also future-proofs for features like
rule ordering, domain-scoped queries, and audit history.

### Hot Reconfiguration

When rules are created, updated, or deleted via the API:
1. The API handler writes the change to `rewrite.db` (single SQL transaction)
2. The handler calls a reload callback on the rewrite plugin
3. The plugin queries the DB for all enabled rules, compiles regex patterns, and
   swaps the in-memory compiled rule set under a write lock
4. In-flight requests that already read the old rules under a read lock complete
   unaffected
5. No proxy restart, no MITM reconnection, no config file reload required

### Domain Constraint

The rewrite plugin can only target domains that are already in `mitm.domains`. Adding
a new domain to rewrite rules that is not MITM-intercepted requires adding it to
`mitm.domains` in `fpsd.yml` and restarting the proxy (MITM certificate setup is a
startup-time operation).

### Proxy Restart from UI

Some configuration changes (MITM domains, listen address, plugin enable/disable)
require a full proxy restart. Rather than requiring SSH access or terminal commands,
the dashboard provides a "Restart Proxy" button on the General config tab.

**Mechanism**: The backend executes `systemctl --user restart fpsd` via `os/exec`.
systemd handles the full stop/start lifecycle — the current process receives SIGTERM,
shuts down gracefully, and systemd starts a fresh instance. The dashboard WebSocket
connection drops during restart; the existing `ReconnectBanner` component handles
this automatically. After reconnection, the user must log in again (sessions are
in-memory and do not survive restarts).

**systemd detection**: The proxy checks for the `INVOCATION_ID` environment variable
(set by systemd for all service processes). If absent, the restart endpoint returns
an error explaining that restart is only available when running as a systemd service.

**Confirmation dialog**: Before sending the restart request, the UI shows a modal
dialog warning the user that:
1. The proxy will restart and all active connections will be dropped
2. They will be disconnected from the dashboard
3. They will need to log in again after the proxy comes back up

The dialog has "Restart" and "Cancel" buttons. Only clicking "Restart" sends the
request.

---

## Scope

### In Scope

- Rewrite plugin implementing `ContentFilter` with API-managed rules
- Plugin chaining: multiple plugins per domain with priority ordering
- SQLite-backed rule persistence (consistent with blocklist and stats DBs)
- Hot reconfiguration via API (no restart)
- Regex and literal string pattern matching
- Per-rule domain and URL path scoping
- Stats tracking (inspected, matched, modified, per-rule counts)
- Web UI: Config tab rework with sub-tabs for general config and rewrite rules
- REST API endpoints for rule CRUD
- Rule testing endpoint (dry-run a pattern against sample text)
- Proxy restart from dashboard UI (systemd-managed instances only)

### Out of Scope

- Rewrite rules for non-MITM'd traffic (transparent pass-through)
- Web UI for editing `fpsd.yml` directly (config tab still shows read-only JSON)
- Request modification (only response bodies are rewritten)
- Proxy restart via UI when not running under systemd
- Binary content rewriting (only text-based Content-Types)
- Rule import/export UI
- Conditional rewrites based on response headers or status codes
- Websocket-based live rule push (rules are fetched via REST)

---

## Design

### 1. Plugin Chaining (Registry Changes)

**File**: `internal/plugin/registry.go`

**Current behavior**: `InitPlugins` rejects two plugins claiming the same domain
(`domainOwner` conflict check). `BuildResponseModifier` maps each domain to a single
plugin.

**New behavior**:

Add a `Priority` field to `PluginConfig`:

```go
type PluginConfig struct {
    Enabled     bool
    Mode        string
    Placeholder string
    Domains     []string
    Options     map[string]any
    Priority    int  // lower = runs first; default 100
}
```

Corresponding YAML field in `PluginConf`:

```go
type PluginConf struct {
    // ... existing fields ...
    Priority int `yaml:"priority"` // default: 100
}
```

Changes to `InitPlugins`:
- Remove the domain exclusivity check (`domainOwner` error for conflicts)
- Track domain → []pluginName for logging/debugging
- Validate that no two plugins have the same priority on the same domain

Changes to `BuildResponseModifier`:
- `lookup` becomes `map[string][]entry` (domain → ordered list of plugins)
- Entries are sorted by priority (ascending) at build time
- At runtime, plugins execute in order; each receives the output of the previous

```go
return func(domain string, req *http.Request, resp *http.Response, body []byte) ([]byte, error) {
    entries, ok := lookup[strings.ToLower(domain)]
    if !ok {
        return body, nil
    }

    current := body
    for _, e := range entries {
        if onInspect != nil {
            onInspect(e.plugin.Name())
        }

        modified, result, err := e.plugin.Filter(req, resp, current)
        if err != nil {
            return nil, fmt.Errorf("plugin %s: %w", e.plugin.Name(), err)
        }

        if result.Matched && onMatch != nil {
            onMatch(e.plugin.Name(), result.Rule, result.Modified, result.Removed)
        }

        // ... logging (same as current) ...

        current = modified
    }
    return current, nil
}
```

**Backward compatibility**: Existing single-plugin-per-domain setups continue working
unchanged. The reddit-promotions plugin gets a default priority of 100.

### 2. Rewrite Plugin

**File**: `internal/plugin/rewrite.go`

#### Registration

```go
func init() {
    Registry["rewrite"] = func() ContentFilter {
        return &rewriteFilter{
            name:    "rewrite",
            version: "0.1.0",
        }
    }
}
```

The rewrite plugin has no built-in domains. Its effective domain list comes from:
1. Domains specified in `fpsd.yml` under `plugins.rewrite.domains` (if provided)
2. If no domains are configured in YAML, defaults to all `mitm.domains` (the plugin
   only fires for domains that have at least one matching rule anyway)

#### Rule Structure

```go
type RewriteRule struct {
    ID          string   `json:"id"`           // UUID, assigned on creation
    Name        string   `json:"name"`         // human-readable label
    Pattern     string   `json:"pattern"`      // literal string or regex
    Replacement string   `json:"replacement"`  // replacement text ($1, $2 for captures)
    IsRegex     bool     `json:"is_regex"`     // false = literal string match
    Domains     []string `json:"domains"`      // domains this rule applies to (empty = all)
    URLPatterns []string `json:"url_patterns"` // URL path globs (empty = all paths)
    Enabled     bool     `json:"enabled"`      // can be toggled without deletion
    CreatedAt   string   `json:"created_at"`   // RFC 3339
    UpdatedAt   string   `json:"updated_at"`   // RFC 3339
}
```

**Pattern semantics**:
- `is_regex: false` → `strings.ReplaceAll(body, pattern, replacement)`
- `is_regex: true` → `regexp.ReplaceAll` using Go RE2 syntax
- Regex patterns are pre-compiled when rules are loaded; invalid patterns are
  rejected at creation time with a descriptive error

**Replacement semantics**:
- Literal mode: replacement is used as-is
- Regex mode: `$1`, `$2`, etc. expand to captured groups (Go `regexp.Expand` behavior)

**URL pattern semantics**:
- Glob-style matching using `path.Match` rules
- `*` matches any non-slash sequence, `**` is not supported
- Examples: `/r/*`, `/api/v1/*`, `/`
- Empty list means the rule applies to all URL paths

**Domain semantics**:
- Exact match (case-insensitive)
- Empty list means the rule applies to all domains the plugin is registered for

#### Filter Logic

```go
func (f *rewriteFilter) Filter(req *http.Request, resp *http.Response, body []byte) ([]byte, FilterResult, error) {
    f.mu.RLock()
    rules := f.compiledRules  // snapshot under read lock
    f.mu.RUnlock()

    domain := strings.ToLower(req.Host)
    urlPath := req.URL.Path

    current := body
    totalRemoved := 0
    firstRule := ""
    matched := false

    for _, rule := range rules {
        if !rule.Enabled {
            continue
        }
        if !rule.matchesDomain(domain) || !rule.matchesURL(urlPath) {
            continue
        }

        var replaced []byte
        var count int

        if rule.IsRegex {
            replaced, count = rule.regexReplace(current)
        } else {
            replaced, count = rule.literalReplace(current)
        }

        if count > 0 {
            matched = true
            totalRemoved += count
            if firstRule == "" {
                firstRule = rule.Name
            }
            current = replaced
        }
    }

    return current, FilterResult{
        Matched:  matched,
        Modified: !bytes.Equal(current, body),
        Rule:     firstRule,
        Removed:  totalRemoved,
    }, nil
}
```

**Notes**:
- `Removed` counts the total number of replacements across all matching rules
- `Rule` reports the name of the first rule that matched (consistent with reddit
  plugin which reports the first matching rule)
- Per-rule hit counts are tracked separately via `onMatch` callbacks, using the
  rule name as the stats key
- If multiple rules match, `onMatch` is called once per rule (each rule gets its
  own stats line)

#### Stats Integration

The rewrite plugin reports stats identically to the reddit plugin:
- `RecordPluginInspected("rewrite")` on every response dispatch
- `RecordPluginMatch("rewrite", ruleName, modified, count)` for each matching rule
- Stats appear in `/fps/stats` under `plugins.filters[]` and in the dashboard

**Difference from reddit plugin**: Since multiple rules can match a single response,
`onMatch` is called once per matching rule (not once per response). This requires a
small change: the rewrite plugin calls the match callback directly rather than relying
on `BuildResponseModifier` to call it once. The modifier's post-Filter callback is
still called for the aggregate result, but per-rule tracking happens inside Filter().

**Approach**: Add an optional `OnRuleMatch` callback to `PluginConfig.Options` that
the rewrite plugin reads during `Init()`. This avoids changing the `ContentFilter`
interface.

Alternatively (simpler): the rewrite plugin uses a combined rule name in `FilterResult.Rule`
formatted as `"rule1,rule2,rule3"` and the modifier calls `onMatch` once with all rules.
The stats collector already splits on `:` for the `pluginName:rule` key, so each rule
gets its own counter.

**Chosen approach**: The rewrite plugin returns `FilterResult` with `Rule` set to
each individual matching rule name. Since `BuildResponseModifier` calls `onMatch` once
with the result, we need a mechanism for multi-rule reporting. Add a new optional field
to `FilterResult`:

```go
type FilterResult struct {
    Matched  bool
    Modified bool
    Rule     string           // first matching rule (backward compat)
    Removed  int
    Rules    []RuleMatch      // all matching rules (nil for single-rule plugins)
}

type RuleMatch struct {
    Rule    string
    Count   int
    Modified bool
}
```

When `Rules` is non-nil, `BuildResponseModifier` calls `onMatch` for each entry
instead of once for the aggregate. Existing plugins that leave `Rules` nil continue
working unchanged.

### 3. Rule Persistence

**File**: `internal/plugin/rewrite_store.go`

```go
type RewriteStore struct {
    db *sqlite.Conn  // zombiezen.com/go/sqlite
}

func OpenRewriteStore(dataDir string) (*RewriteStore, error)
func (s *RewriteStore) Close() error
func (s *RewriteStore) List() ([]RewriteRule, error)
func (s *RewriteStore) Get(id string) (RewriteRule, error)
func (s *RewriteStore) Add(rule RewriteRule) (RewriteRule, error)
func (s *RewriteStore) Update(id string, rule RewriteRule) (RewriteRule, error)
func (s *RewriteStore) Delete(id string) error
func (s *RewriteStore) Toggle(id string) (RewriteRule, error)
```

**Database**: `<data_dir>/rewrite.db`

Uses `zombiezen.com/go/sqlite` (pure Go, no CGO) — same binding as blocklist and
stats DBs.

**Schema** (created via `CREATE TABLE IF NOT EXISTS` on open):

```sql
CREATE TABLE IF NOT EXISTS rewrite_rules (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    pattern     TEXT NOT NULL,
    replacement TEXT NOT NULL DEFAULT '',
    is_regex    INTEGER NOT NULL DEFAULT 0,
    domains     TEXT NOT NULL DEFAULT '[]',    -- JSON array of strings
    url_patterns TEXT NOT NULL DEFAULT '[]',   -- JSON array of strings
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT NOT NULL,                 -- RFC 3339
    updated_at  TEXT NOT NULL                  -- RFC 3339
);
```

**Domain and URL pattern columns**: Stored as JSON-encoded string arrays. Decoded on
read. This avoids a separate join table for what is always a small list per rule.

**WAL mode**: Enabled on open (`PRAGMA journal_mode=WAL`) for concurrent read access
during writes. Consistent with how the stats DB is configured.

**Missing database**: If the file does not exist at startup, `OpenRewriteStore`
creates it with the schema above. The plugin starts with an empty rule set.

### 4. REST API Endpoints

All endpoints require authentication (same middleware as existing `/fps/api/` routes).

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/fps/api/rewrite/rules` | List all rules |
| `POST` | `/fps/api/rewrite/rules` | Create a new rule |
| `GET` | `/fps/api/rewrite/rules/{id}` | Get a single rule |
| `PUT` | `/fps/api/rewrite/rules/{id}` | Update a rule |
| `DELETE` | `/fps/api/rewrite/rules/{id}` | Delete a rule |
| `PATCH` | `/fps/api/rewrite/rules/{id}/toggle` | Toggle enabled/disabled |
| `POST` | `/fps/api/rewrite/test` | Test a pattern against sample text |
| `POST` | `/fps/api/restart` | Restart the proxy via systemd |

#### Request/Response Formats

**POST /fps/api/rewrite/rules** (create):
```json
// Request
{
  "name": "Remove newsletter popups",
  "pattern": "<div class=\"newsletter-popup\">.*?</div>",
  "replacement": "",
  "is_regex": true,
  "domains": ["example.com"],
  "url_patterns": ["/blog/*"],
  "enabled": true
}

// Response: 201 Created
{
  "id": "a1b2c3d4-...",
  "name": "Remove newsletter popups",
  "pattern": "<div class=\"newsletter-popup\">.*?</div>",
  "replacement": "",
  "is_regex": true,
  "domains": ["example.com"],
  "url_patterns": ["/blog/*"],
  "enabled": true,
  "created_at": "2026-02-22T10:00:00Z",
  "updated_at": "2026-02-22T10:00:00Z"
}
```

**POST /fps/api/rewrite/test** (dry-run):
```json
// Request
{
  "pattern": "\\bfoo\\b",
  "replacement": "bar",
  "is_regex": true,
  "sample": "The foo jumped over the foo fence."
}

// Response: 200 OK
{
  "result": "The bar jumped over the bar fence.",
  "match_count": 2,
  "valid": true,
  "error": ""
}
```

**POST /fps/api/restart** (proxy restart):
```json
// Response: 200 OK (sent before the process exits)
{
  "status": "restarting",
  "message": "Proxy is restarting via systemd. You will need to log in again."
}

// Response: 503 Service Unavailable (not running under systemd)
{
  "status": "error",
  "message": "Restart is only available when running as a systemd service."
}
```

The handler checks for the `INVOCATION_ID` environment variable to confirm systemd
management. If present, it sends the 200 response, then asynchronously executes
`systemctl --user restart fpsd` after a short delay (100ms) to allow the HTTP
response to flush to the client. The `exec.Command` call uses `Start()` (not `Run()`)
so the restart proceeds even as the current process begins shutting down.

#### Validation

- `name`: required, non-empty, max 200 characters
- `pattern`: required, non-empty; if `is_regex`, must compile with `regexp.Compile`
- `replacement`: allowed to be empty (deletion)
- `domains`: each must be a valid domain name; empty list is allowed (matches all)
- `url_patterns`: each must be a valid `path.Match` pattern; empty list is allowed
- Invalid regex returns 400 with the compilation error message

#### Hot-Reload Flow

After any successful write (create, update, delete, toggle):
1. The store method commits the change to SQLite (single transaction)
2. The API handler calls `rewritePlugin.ReloadRules()`
3. `ReloadRules()` queries the DB for all enabled rules, compiles regex patterns,
   and swaps the in-memory compiled rule set under a write lock
4. Response returned to the client includes the updated rule

### 5. Web UI: Config Tab Rework

**File**: `web/ui/src/pages/Config.tsx` (modified)

The Config page gets a sub-tab layout:

```
┌───────────────────────────────────────────────────┐
│  [General]  [Rewrite Rules]                       │
├───────────────────────────────────────────────────┤
│                                                   │
│  (tab content)                                    │
│                                                   │
└───────────────────────────────────────────────────┘
```

#### General Tab (existing behavior + restart)

- Raw JSON config display (unchanged)
- Reload Config button (unchanged)
- Reload result notification (unchanged)
- **New**: "Restart Proxy" button (shown only when proxy reports systemd management)
- **New**: Restart confirmation dialog

**Restart confirmation dialog**:

```
┌─────────────────────────────────────────────────────┐
│  Restart Proxy?                                     │
│                                                     │
│  The proxy will restart and all active connections   │
│  will be dropped. You will be disconnected from     │
│  the dashboard and will need to log in again.       │
│                                                     │
│                          [Cancel]  [Restart]         │
└─────────────────────────────────────────────────────┘
```

- "Restart" button sends `POST /fps/api/restart`
- After sending, the dialog shows "Restarting..." with a spinner
- The WebSocket will disconnect; the `ReconnectBanner` takes over
- Once reconnected, the app redirects to the login page

**systemd detection for UI**: The heartbeat response (already broadcast every 5s)
gains a new boolean field `systemd_managed`. The General tab reads this to decide
whether to show the "Restart Proxy" button. When false, the button is hidden (no
point showing a button that would return a 503).

#### Rewrite Rules Tab

**New files**:
- `web/ui/src/components/RewriteRuleList.tsx` — Rule table with actions
- `web/ui/src/components/RewriteRuleForm.tsx` — Create/edit form
- `web/ui/src/components/RewriteRuleTest.tsx` — Pattern test panel

**Rule list view**:

```
┌──────────────────────────────────────────────────────────────┐
│  Rewrite Rules                              [+ Add Rule]     │
├──────────────────────────────────────────────────────────────┤
│  Name            Pattern         Domains     Enabled  Actions│
│  ─────────────── ─────────────── ─────────── ──────── ───────│
│  Remove popups   <div class=...  example.com   ●      ✎ ✕   │
│  Fix typo        teh → the       (all)         ○      ✎ ✕   │
│  Strip tracking  \?utm_[a-z_]..  (all)         ●      ✎ ✕   │
└──────────────────────────────────────────────────────────────┘
```

- Enabled column: clickable toggle (calls PATCH .../toggle)
- Edit button: opens inline form below the row (or replaces the row)
- Delete button: confirmation prompt, then DELETE
- Pattern column: truncated with tooltip on hover, regex patterns shown with a
  small `/re/` badge
- Domains column: shows domain list, or "(all)" if empty

**Rule form** (add/edit):

```
┌──────────────────────────────────────────────────────────────┐
│  Name:         [________________________]                    │
│  Pattern:      [________________________]  [ ] Regex         │
│  Replacement:  [________________________]                    │
│  Domains:      [________________________] (comma-separated)  │
│  URL Patterns: [________________________] (comma-separated)  │
│                                                              │
│  [Test Pattern]  [Save]  [Cancel]                            │
└──────────────────────────────────────────────────────────────┘
```

- Regex checkbox toggles a visual indicator on the pattern field
- Domains field shows placeholder text: "Leave empty for all MITM domains"
- "Test Pattern" button opens the test panel

**Test panel** (expandable below form):

```
┌──────────────────────────────────────────────────────────────┐
│  Sample Text:                                                │
│  ┌──────────────────────────────────────────────────────────┐│
│  │ Enter sample text to test the pattern against...         ││
│  └──────────────────────────────────────────────────────────┘│
│                                              [Run Test]      │
│  Result: "The bar jumped over the bar fence." (2 matches)    │
└──────────────────────────────────────────────────────────────┘
```

**New API functions** in `web/ui/src/api.ts`:

```typescript
export async function fetchRewriteRules(): Promise<RewriteRule[]>
export async function createRewriteRule(rule: NewRule): Promise<RewriteRule>
export async function updateRewriteRule(id: string, rule: NewRule): Promise<RewriteRule>
export async function deleteRewriteRule(id: string): Promise<void>
export async function toggleRewriteRule(id: string): Promise<RewriteRule>
export async function testRewritePattern(req: TestRequest): Promise<TestResult>
export async function restartProxy(): Promise<{ status: string; message: string }>
```

### 6. Configuration

**fpsd.yml** addition:

```yaml
plugins:
  reddit-promotions:
    enabled: true
    mode: "filter"
    placeholder: "visible"
    # priority defaults to 100
    domains:
      - www.reddit.com
    options:
      log_matches: true

  rewrite:
    enabled: true
    mode: "filter"
    placeholder: "none"      # rewrites don't need placeholder markers
    priority: 900            # always runs after other plugins
    # domains: omitted = all mitm.domains
    options:
      log_matches: true
```

**Priority semantics**:
- Lower number = higher priority = runs first
- Default: 100 (for existing and new plugins)
- Rewrite plugin default: 900 (runs last)
- Valid range: 1-999
- Two plugins cannot share the same priority on the same domain (error at startup)

**When rewrite is the only plugin on a domain**: Chaining has no effect, plugin runs
alone as before.

**When rewrite shares a domain with another plugin**: Both plugins run in priority
order. Example with `www.reddit.com`:
1. `reddit-promotions` (priority 100) removes ads, returns modified body
2. `rewrite` (priority 900) applies user-defined rules to the already-modified body

---

## Implementation Plan

### Commit 1: Plugin Chaining Support

Modify `internal/plugin/registry.go`:
- Add `Priority` field to `PluginConfig`
- Remove domain exclusivity check from `InitPlugins`
- Validate no duplicate priorities per domain
- Change `BuildResponseModifier` to use `map[string][]entry` with sorted entries
- Execute plugins in chain, passing output of each to the next
- Support `FilterResult.Rules` for multi-rule match reporting

Modify `internal/config/config.go`:
- Add `Priority int` to `PluginConf`
- Default to 100 during validation

Update tests in `internal/plugin/plugin_test.go` for chaining scenarios.

### Commit 2: Rewrite Plugin Core

New file `internal/plugin/rewrite.go`:
- `rewriteFilter` struct implementing `ContentFilter`
- `RewriteRule` and `compiledRule` types
- `Filter()` with literal and regex replacement
- Domain and URL pattern matching
- Thread-safe rule access via `sync.RWMutex` (compiled rule cache)
- `ReloadRules()` method: queries DB, compiles regexes, swaps cache

New file `internal/plugin/rewrite_store.go`:
- `RewriteStore` with SQLite persistence (zombiezen, WAL mode)
- Schema creation on open (`CREATE TABLE IF NOT EXISTS`)
- CRUD operations via SQL transactions
- JSON-encoded arrays for domains and URL patterns columns
- UUID generation for rule IDs
- `Close()` for clean shutdown

New file `internal/plugin/rewrite_test.go`:
- Literal string replacement
- Regex replacement with capture groups
- Domain and URL scoping
- Multiple rules on same response
- Empty rule set (pass-through)
- Invalid regex rejection
- Hot-reload under concurrent access
- Store CRUD with in-memory SQLite

### Commit 3: REST API Endpoints + Proxy Restart

Modify `web/handlers.go`:
- Add handlers for rewrite rule CRUD + test endpoint
- Add `handleRestart` handler: check `INVOCATION_ID`, exec `systemctl --user restart fpsd`
- Wire up `RewriteStore` and `rewriteFilter.ReloadRules` callback

Modify `web/server.go`:
- Register new routes under `/fps/api/rewrite/`
- Register `POST /fps/api/restart`

Modify `internal/probe/probe.go`:
- Add `SystemdManaged bool` field to `HeartbeatResponse`
- Set from `os.Getenv("INVOCATION_ID") != ""`

Modify `cmd/fpsd/main.go`:
- Create `RewriteStore` during init
- Pass store reference to dashboard server
- Wire reload callback

### Commit 4: Web UI — Config Tab Rework

Modify `web/ui/src/pages/Config.tsx`:
- Add sub-tab navigation (General | Rewrite Rules)
- Extract existing config view into General tab
- Add "Restart Proxy" button (conditional on `systemd_managed` heartbeat field)
- Add restart confirmation dialog component

New file `web/ui/src/components/RewriteRuleList.tsx`:
- Rule table with toggle, edit, delete actions

New file `web/ui/src/components/RewriteRuleForm.tsx`:
- Create/edit form with validation

New file `web/ui/src/components/RewriteRuleTest.tsx`:
- Pattern test panel

Modify `web/ui/src/api.ts`:
- Add rewrite rules API functions

### Commit 5: Integration and Config

Update `fpsd.yml` with rewrite plugin configuration.
End-to-end testing: create rules via UI, verify content rewriting on live traffic.

---

## Test Strategy

### Unit Tests (Go)

| Test | What It Verifies |
|------|------------------|
| Literal replacement | `strings.ReplaceAll` behavior, count accuracy |
| Regex replacement | RE2 pattern matching, capture group expansion |
| Invalid regex rejection | `regexp.Compile` error surfaced at rule creation |
| Domain scoping | Rule only fires for configured domains |
| URL pattern scoping | Rule only fires for matching URL paths |
| Empty rule set | Body passes through unmodified, no match reported |
| Multiple rules | Rules apply in order, each sees previous output |
| Disabled rules | Skipped during filtering |
| Hot-reload | Rules updated under RWMutex; concurrent reads safe |
| Store CRUD | SQLite read/write, transactions, missing DB creation |
| Store validation | Required fields, pattern compilation, domain format |
| Plugin chaining | Two plugins on same domain execute in priority order |
| Priority validation | Duplicate priority on same domain rejected |
| FilterResult.Rules | Multi-rule match reported per-rule to stats |
| Restart systemd check | Returns 503 when `INVOCATION_ID` is unset |
| Restart happy path | Execs `systemctl --user restart fpsd` when under systemd |

### Integration Tests

| Test | What It Verifies |
|------|------------------|
| API CRUD | Create, read, update, delete rules via HTTP |
| API validation | 400 on invalid regex, missing name, etc. |
| API toggle | PATCH endpoint toggles enabled state |
| API test | Test endpoint returns match count and result |
| Hot-reload via API | Creating a rule via API immediately takes effect |
| Stats reporting | `/fps/stats` shows rewrite plugin with per-rule counts |
| Chain execution | Reddit plugin + rewrite plugin on same domain |
| Restart API (no systemd) | `POST /fps/api/restart` returns 503 without `INVOCATION_ID` |
| Restart API (auth) | Restart endpoint requires authentication |

### Manual Verification

- Create a rewrite rule via the dashboard Config tab
- Browse a MITM'd site through the proxy
- Verify content is rewritten in the browser
- Verify stats appear in the dashboard Stats tab
- Toggle rule off, verify rewriting stops
- Delete rule, verify pass-through
- Click "Restart Proxy" on General tab, confirm dialog appears with warning
- Confirm restart, verify proxy restarts and dashboard reconnects to login page

---

## Acceptance Criteria

- [ ] `plugins.rewrite` configuration accepted in `fpsd.yml` with `priority` field
- [ ] `Priority` field added to `PluginConf`/`PluginConfig` (default: 100, rewrite default: 900)
- [ ] `BuildResponseModifier` executes multiple plugins per domain in priority order
- [ ] Duplicate priority on the same domain produces a startup error
- [ ] Rewrite plugin registered in `Registry` as `"rewrite"`
- [ ] Literal string replacement works (case-sensitive `strings.ReplaceAll`)
- [ ] Regex replacement works (Go RE2 syntax, `$1`/`$2` capture groups)
- [ ] Invalid regex rejected at rule creation with descriptive error message
- [ ] Rules scoped by domain (empty = all MITM domains)
- [ ] Rules scoped by URL path pattern (glob matching, empty = all paths)
- [ ] Rules can be enabled/disabled without deletion
- [ ] Rules persisted to `<data_dir>/rewrite.db` (SQLite, WAL mode, zombiezen binding)
- [ ] Missing database at startup is not an error (created with schema, empty rule set)
- [ ] `GET /fps/api/rewrite/rules` returns all rules
- [ ] `POST /fps/api/rewrite/rules` creates a rule and hot-reloads the plugin
- [ ] `PUT /fps/api/rewrite/rules/{id}` updates a rule and hot-reloads
- [ ] `DELETE /fps/api/rewrite/rules/{id}` deletes a rule and hot-reloads
- [ ] `PATCH /fps/api/rewrite/rules/{id}/toggle` toggles enabled and hot-reloads
- [ ] `POST /fps/api/rewrite/test` returns match result without persisting
- [ ] All API endpoints require authentication
- [ ] API returns 400 with message for validation errors
- [ ] API returns 404 for unknown rule IDs
- [ ] Stats: `responses_inspected`, `responses_matched`, `responses_modified` tracked
- [ ] Stats: per-rule hit counts appear in `/fps/stats` under `top_rules`
- [ ] Stats: rewrite plugin appears in dashboard Stats tab plugin section
- [ ] Config tab: sub-tab navigation between General and Rewrite Rules
- [ ] Config tab General: existing behavior preserved (JSON view, reload button)
- [ ] Config tab Rewrite Rules: rule list with enable toggle, edit, delete
- [ ] Config tab Rewrite Rules: add/edit form with pattern, replacement, regex toggle, domains, URL patterns
- [ ] Config tab Rewrite Rules: test panel for dry-running patterns
- [ ] Plugin chaining: reddit-promotions (priority 100) + rewrite (priority 900) on `www.reddit.com` both execute in order
- [ ] Hot-reload: creating a rule via API immediately affects subsequent responses (no restart)
- [ ] Thread safety: concurrent requests during rule reload do not panic or corrupt data
- [ ] `POST /fps/api/restart` executes `systemctl --user restart fpsd` when `INVOCATION_ID` is set
- [ ] `POST /fps/api/restart` returns 503 with descriptive message when not running under systemd
- [ ] `POST /fps/api/restart` requires authentication
- [ ] Heartbeat response includes `systemd_managed` boolean field
- [ ] General tab shows "Restart Proxy" button only when `systemd_managed` is true
- [ ] Clicking "Restart Proxy" shows confirmation dialog warning about disconnection and re-login
- [ ] Confirmation dialog "Restart" button triggers the restart; "Cancel" dismisses without action
- [ ] After restart, ReconnectBanner appears and app redirects to login on reconnect
- [ ] Proxy builds, 0 lint issues, all tests pass

---

## Non-Goals

- Case-insensitive literal matching (users can use regex `(?i)pattern` instead)
- Regex replacement with lookahead/lookbehind (Go RE2 does not support these)
- Request body or header rewriting
- Content-Type-specific rule scoping (rules apply to all text-based responses)
- Rule ordering within the rewrite plugin (rules execute in creation order; reorder is a future feature)
- Importing/exporting rules as files via the UI
- WebSocket push of rule changes to other dashboard sessions
- Undo/redo for rule edits
