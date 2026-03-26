# Quality Hardening Design

## Goal

Repair the project's weakest quality boundaries without destabilizing shipped behavior:

- raise coverage around the TUI, daemon event processing, and CLI commands
- stop silent failure paths from hiding lost events
- reduce the most obvious scaling risks in the TUI and Codex watcher
- bring project docs and release automation back in sync with the code

## Scope

This design covers four coordinated tracks:

1. Test and observability hardening
2. TUI structural cleanup and render-path optimization
3. Data-path and watcher performance improvements
4. Release and platform automation

The intent is to deliver each track in a verifiable batch, not as one large refactor.

## Constraints

- Preserve current user-facing behavior unless the change is explicitly corrective.
- Prefer test-first changes for all behavior updates.
- Keep daemon-only logging and TUI-mode logging behavior distinct.
- Avoid speculative new features; focus on safety, diagnosability, and maintainability.

## Architecture Direction

### 1. Quality Guardrails First

The first batch should add regression coverage before touching high-churn files further.

- Add direct tests for `daemon.processEvent()` covering session lifecycle, tool events, token updates, and file changes.
- Add CLI command tests for `setup`, `uninstall`, `report`, `cost`, and `clean` using temp HOME directories and redirected stdio.
- Add model-level TUI tests around refresh logic, tab switching, filtering, and message loading behavior.
- Replace silent drops with diagnosable handling in watcher scan failures and emit parsing failures.

This batch creates the safety net required for the later structural changes.

### 2. TUI Decomposition

`internal/tui/model.go` currently mixes model state, update logic, rendering helpers, tab rendering, formatting, and side effects in one file. The design is to split by responsibility while keeping the same package API:

- `model.go`: core state, constructor, shared helpers
- `update.go`: Bubble Tea update logic and refresh pipeline
- `view_dashboard.go`
- `view_messages.go`
- `view_tool_calls.go`
- `view_timeline.go`
- `view_shared.go` for formatting helpers

This is not a design-system rewrite. It is a package-internal split to make test seams and future edits tractable.

### 3. TUI Render-Path Optimization

The current view path recomputes filtered slices repeatedly. The design is to cache derived slices on data/filter changes:

- cache filtered sessions
- cache filtered tool calls
- cache filtered timeline entries
- invalidate caches only when backing data or filter text changes

`expandedCalls` should also be bounded to active data. The simplest design is to prune expansion state during refresh and tab/session changes so stale keys cannot accumulate indefinitely.

### 4. Watcher and Aggregation Performance

The Codex watcher still performs full recursive scans on every poll. The next improvement is to maintain a discovered-path set and only re-walk when needed, while preserving the existing offset-based incremental reading.

For `UpdateSessionTokens()`, the current full-session aggregation is acceptable for correctness but scales poorly. The design direction is to shift toward incremental session totals updated from the newly inserted token row, while keeping a fallback reconciliation path for repair and migration scenarios.

### 5. Error Handling Policy

Silent failure paths should become observable without spamming the TUI:

- watcher directory read failures should log contextual warnings
- malformed event inputs should return structured errors where safe
- non-fatal background failures should go to the log file / daemon logs

The rule is: background failures may be non-fatal, but they should not be invisible.

### 6. Documentation and Release Alignment

`CLAUDE.md` must match the current TUI tab model. CI should expand beyond Ubuntu to include Windows. Release docs should explicitly describe tagging and Homebrew status so contributors do not infer support that does not exist yet.

## Testing Strategy

- Unit tests for daemon event processing and storage-facing side effects
- Command tests for CLI commands with temp filesystem fixtures
- TUI model tests that call `Update`, `refresh`, and tab render helpers without requiring terminal snapshots
- Targeted regression tests for watcher scan/index logic
- Existing full-suite verification: `go test ./...`, `go vet ./...`, `go test -race ./...`

## Rollout Plan

1. Add tests and observability fixes first
2. Split TUI into focused files without behavior changes
3. Add TUI caching and state pruning with regression coverage
4. Improve watcher and aggregation performance with benchmarks/tests
5. Update docs, CI, and release wiring

## Risks

- TUI file splitting can accidentally change key handling or rendering if mixed with behavioral edits
- CLI tests must carefully isolate `HOME`, `os.Args`, and stdio to avoid flaky global-state interactions
- incremental token aggregation can drift if repair/backfill paths are not included in the model

## Success Criteria

- non-zero coverage for `internal/tui` and meaningful new coverage for `cmd/agmon` and `internal/daemon`
- no silent watcher directory failures in key scan paths
- `CLAUDE.md` reflects current tab names and behavior
- CI runs on both Linux and Windows
- release path is documented and executable from the repository state
