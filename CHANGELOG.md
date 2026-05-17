# Changelog

All notable changes to TokenMeter are tracked here. Versions follow semver.
The "Unreleased" section captures work merged but not yet tagged.

## v0.7.0 — 2026-05-17

This release rolls up a multi-round hardening pass covering security,
correctness, time-zone behavior, observability, and test/benchmark
infrastructure. No new user-facing features; everything is reliability,
safety, and developer ergonomics.

### Security

- **Unix socket now mode `0600`** — `~/.tokenmeter/tokenmeter.sock` and the
  subscriber socket are chmod'd to owner-only after `Listen`. Previously
  the default umask left them at `0644`, which on Linux/macOS lets any
  local user inject fake hook events into the daemon. Same-host attackers
  can no longer poison your usage data without already running as your UID.
  A µs-level TOCTOU window between Listen and Chmod is documented; for
  strict guarantees wrap the daemon start in `syscall.Umask(0o077)`.
- **HTTP server hardening** — `tokenmeter web` now sets
  `ReadHeaderTimeout=5s` / `ReadTimeout=30s` / `WriteTimeout=30s` /
  `IdleTimeout=60s`, and SIGINT/SIGTERM triggers `srv.Shutdown(ctx)` so
  in-flight requests drain before the process exits. Slowloris-style hangs
  are no longer possible.
- **`/api/session/{id}` no longer leaks internal errors** — ambiguous
  prefix matches return a 400 with a user-friendly message; any other DB
  error (SQL syntax, table names, driver internals) becomes a generic
  500 with the detail logged server-side only.

### Correctness — time zones

- **All daily aggregates now bucket by local time** — `DATE(timestamp,
  'localtime')` plus matching local-time `from`/`to` boundaries in
  `web/`, `tui/`, and `cmd/`. Stored timestamps remain UTC; the change is
  purely query-side. For a UTC+8 user, "Today's cost" now matches the
  local calendar instead of starting at UTC midnight (= local 08:00).
- **`GetDailyCostsBetween` is now inclusive of endDay** — the previous
  half-open `[from, to)` semantics dropped today's partial bar when
  callers passed `to = now` mid-day.
- **`GetFirstTokenDate` returns a local-day anchor** — `range=all` no
  longer shows an empty first-day bucket from UTC/local offset.

### Correctness — data integrity

- **`parseTimestamp` returns `(time.Time, bool)`** instead of falling
  back to `time.Now()`. Malformed Codex/Claude log timestamps no longer
  pollute today's cost — affected events are dropped instead.
- **`truncate` is rune-safe** — tool params/results truncated mid-rune no
  longer produce invalid UTF-8 in storage (Chinese, emoji, etc.).
- **`bufio.Scanner` buffer raised to 16MB** in `extractPatchFileChanges`
  — apply_patch bodies with lines >64KB (minified JS, base64) no longer
  silently drop the rest of the patch.
- **Watcher truncation/rotation detected** — when a Claude/Codex JSONL
  file shrinks since the last scan, the watcher resets the byte offset
  to 0 and re-reads. `source_id` UNIQUE indexes prevent double-counting.
- **`codex pendingFileChanges` map now has a 2-hour TTL GC** running
  every ~30s — orphaned `function_call` entries (codex died mid-call)
  no longer leak the map.
- **Schema migrations are gated by `PRAGMA user_version`** — the legacy
  `normalizeTimeColumns` full-table scan no longer runs on every daemon
  restart. Set once, skipped forever.
- **`MarkStaleSessionsEnded(2h)` now runs on a 1-hour ticker** — a
  daemon running for days no longer accumulates `active` zombies.
- **`addColumnIfMissing` uses `PRAGMA table_info`** instead of string-
  matching the driver's error message.

### Performance

- **3 new time indexes** on hot aggregation paths:
  `idx_token_usage_ts`, `idx_tool_calls_start`, `idx_file_changes_ts`.
  First upgrade reopens an old DB will spend a few seconds building
  these; subsequent INSERTs are virtually free to maintain.
- **`/api/sessions?limit=N` query parameter** (capped at 1000). Default
  remains 200; the `/api/stats` `total_sessions` count now uses the
  same filter as `ListSessions` so the dashboard and stats numbers
  agree.
- **11 benchmarks added** as performance regression baselines:
  `broadcast` (~62 ns/op), `processEvent TokenUsage` (~140 µs/op),
  `ParseClaudeFileEvents`, `extractPatchFileChanges`, `truncate`,
  `GetDailyCostsBetween` (~4 ms / 500 rows), `GetCostBetween`,
  `GetModelCostBreakdown`, `ListSessions`, `GetActiveSessionCount`,
  `UpdateSessionTokens`. Future regressions surface immediately.

### Observability

- **Daemon `Stats()` counters** — `dropped_broadcasts`,
  `dropped_shutdown`, `duplicate_tool_starts` atomic counters surfaced
  in the daemon Stop log so operators can spot slow subscribers,
  shutdown-window event loss, or Pre-hook re-emit anomalies.
- **`ProcessExternalEventAsync` uses a two-stage send** — non-blocking
  first try; falls back to blocking with the `done` channel as a
  backstop. The previous random-select made shutdown-window event loss
  more likely than it had to be.
- **Second Ctrl+C force-quits** — `tokenmeter daemon` and `tokenmeter
  web` now install a watchdog goroutine that calls `os.Exit(130)` if a
  user impatiently presses Ctrl+C while graceful shutdown is in
  progress.
- **`emit.log` for `tokenmeter emit` errors** — the hook entry point
  logs to `~/.tokenmeter/emit.log` (10MB self-truncate) instead of
  stderr, so error chatter never pollutes Claude Code's hook
  stdout/stderr parsing.

### Refactors

- **`claude_log.go` extracts `parseClaudeLogTokenEvent`** — single
  source of truth for assistant-message token parsing, shared by both
  the parallel initial scan and the incremental processFile loop.
- **`HTTP Server` built in `NewServer`** — `Start()`/`Shutdown(ctx)`
  pair is now race-free at the cost of moving mux registration out of
  Start.
- **`storage.ErrAmbiguousSessionPrefix` sentinel** — callers use
  `errors.Is` to split user-input errors (400) from system errors (500)
  without parsing error strings.
- **`view_messages.go` chunked rendering** — no longer infinite-loops
  on extremely narrow terminals; preserves user content at the
  `len(remaining) == maxCols` boundary where the previous length-based
  detection mistook a truncated chunk for a complete one.

### Test infrastructure

- **~78 new tests** across all packages — boundary cases, regression
  guards, contract tests, and a TUI keyboard handler harness covering
  17 key events (q / Tab / Shift-Tab / j / k / G / [ / ] / / / Esc /
  Enter / t / p / s / c / r and several window/tick/refresh messages).
- **Coverage gains** (Round 0 → final):
  - cmd/tokenmeter: 33.7% → 53.3%
  - internal/appdir: 87.0% → 95.7%
  - internal/collector: 71.2% → 83.4%
  - internal/daemon: 62.9% → 77.8%
  - internal/report: 80.2% → 88.4%
  - internal/storage: 77.0% → 83.9%
  - internal/tui: 49.2% → 70.9%
  - internal/web: 62.2% → 73.0%

### Upgrade notes

- **First reopen of an existing database** will create three new time
  indexes; expect a few seconds of extra startup time on a database
  with hundreds of thousands of token rows. One-time cost.
- **`tokenmeter web` dashboard "today" / "this week" / "this month"
  values may shift by hours after upgrading** if the host is not in
  UTC — the buckets now align with local calendar days instead of UTC
  midnight. Totals over longer periods are unchanged; only the
  per-bucket numbers move.
- **Existing 0644 socket files from older daemons are removed and
  recreated at 0600** on the next start. No manual action required.
