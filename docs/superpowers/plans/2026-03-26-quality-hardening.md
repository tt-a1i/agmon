# Quality Hardening Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Raise coverage, reduce silent failures, split and optimize the TUI, improve watcher/data-path scalability, and complete release/platform hardening.

**Architecture:** Work in four verified batches. Batch 1 adds test and logging guardrails. Batch 2 splits the TUI into focused files and preserves behavior. Batch 3 optimizes derived-state and watcher/data aggregation paths. Batch 4 updates docs, CI, and release support.

**Tech Stack:** Go 1.24, Bubble Tea, SQLite via `modernc.org/sqlite`, GitHub Actions, Go test/vet/race.

---

## Chunk 1: Test And Observability Guardrails

### Task 1: Daemon `processEvent()` Coverage

**Files:**
- Modify: `internal/daemon/daemon_test.go`
- Reference: `internal/daemon/daemon.go`
- Reference: `internal/storage/db.go`

- [ ] **Step 1: Write failing tests for `processEvent()` session lifecycle**

Add tests covering:
- historical non-`SessionEnd` events becoming ended/stale instead of lingering active
- `EventSessionEnd` marking pending tools interrupted
- `EventTokenUsage` updating session totals
- tool end events inserting file changes when `FilePath` is present

- [ ] **Step 2: Run the daemon test subset and verify the new tests fail**

Run: `go test ./internal/daemon -run TestProcessEvent`

- [ ] **Step 3: Implement the minimal daemon changes required by the tests**

Keep the implementation focused on correctness and logging, not refactoring.

- [ ] **Step 4: Re-run the daemon test subset until green**

Run: `go test ./internal/daemon -run TestProcessEvent`

- [ ] **Step 5: Run full daemon verification**

Run: `go test ./internal/daemon && go test -race ./internal/daemon`

### Task 2: CLI Command Coverage

**Files:**
- Create: `cmd/agmon/cli_test.go`
- Modify: `cmd/agmon/main.go`

- [ ] **Step 1: Write failing tests for `setup`, `uninstall`, `report`, `cost`, and `clean`**

Use temp HOME directories and helper functions to isolate:
- `~/.claude/settings.json`
- `~/.agmon` data
- `os.Args`
- stdout/stderr capture

- [ ] **Step 2: Run the CLI test subset and verify it fails**

Run: `go test ./cmd/agmon -run 'TestRun(Setup|Uninstall|Report|Cost|Clean)'`

- [ ] **Step 3: Make the smallest code changes needed for testability**

Preferred techniques:
- extract side-effect helpers
- inject paths via helper functions
- avoid changing user-facing output unless the test proves it is wrong

- [ ] **Step 4: Re-run the CLI test subset**

Run: `go test ./cmd/agmon -run 'TestRun(Setup|Uninstall|Report|Cost|Clean)'`

- [ ] **Step 5: Run full command package verification**

Run: `go test ./cmd/agmon && go test -race ./cmd/agmon`

### Task 3: Silent Failure Surfacing

**Files:**
- Modify: `internal/collector/codex.go`
- Modify: `internal/collector/claude_log.go`
- Modify: `cmd/agmon/main.go`

- [ ] **Step 1: Write failing tests for non-fatal scan/parse error observability**

Cover:
- Codex watcher scan path errors
- Claude watcher directory read failures
- `runEmit()` parse failure / emit failure expectations

- [ ] **Step 2: Run the relevant package tests and verify failure**

Run: `go test ./internal/collector ./cmd/agmon -run 'Test(Claude|Codex|RunEmit)'`

- [ ] **Step 3: Add contextual non-fatal logging without polluting TUI**

Route background diagnostics through existing logging, not stdout UI rendering.

- [ ] **Step 4: Re-run targeted tests**

Run: `go test ./internal/collector ./cmd/agmon -run 'Test(Claude|Codex|RunEmit)'`

## Chunk 2: TUI Structural Cleanup

### Task 4: Split `internal/tui/model.go`

**Files:**
- Modify: `internal/tui/model.go`
- Create: `internal/tui/update.go`
- Create: `internal/tui/view_dashboard.go`
- Create: `internal/tui/view_messages.go`
- Create: `internal/tui/view_tool_calls.go`
- Create: `internal/tui/view_timeline.go`
- Create: `internal/tui/view_shared.go`

- [ ] **Step 1: Add TUI model tests before splitting**

Create tests for:
- refresh behavior
- dashboard enter-to-messages transition
- filter behavior
- session switching

- [ ] **Step 2: Run the TUI test subset and verify failure**

Run: `go test ./internal/tui -run 'Test(Model|Refresh|Filter|Session)'`

- [ ] **Step 3: Split files without changing behavior**

Move code by responsibility only. Keep package names, exported API, and semantics unchanged.

- [ ] **Step 4: Re-run the TUI subset**

Run: `go test ./internal/tui -run 'Test(Model|Refresh|Filter|Session)'`

- [ ] **Step 5: Run full TUI package tests**

Run: `go test ./internal/tui`

### Task 5: TUI Derived-State Caching And Expansion Pruning

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/update.go`
- Modify: `internal/tui/view_shared.go`
- Modify: `internal/tui/*_test.go`

- [ ] **Step 1: Write failing tests for filtered cache invalidation and expansion pruning**

Cover:
- filter text change invalidates cached filtered slices
- refresh/session change prunes stale `expandedCalls`

- [ ] **Step 2: Run the TUI cache-related tests and verify failure**

Run: `go test ./internal/tui -run 'Test(FilterCache|ExpandedCalls)'`

- [ ] **Step 3: Implement minimal caching and pruning**

Only cache derived data. Do not cache rendered strings.

- [ ] **Step 4: Re-run TUI cache-related tests**

Run: `go test ./internal/tui -run 'Test(FilterCache|ExpandedCalls)'`

## Chunk 3: Watcher And Aggregation Performance

### Task 6: Codex Watcher Path-Set Optimization

**Files:**
- Modify: `internal/collector/codex.go`
- Modify: `internal/collector/codex_test.go`
- Modify: `internal/collector/messages.go`

- [ ] **Step 1: Write failing tests for known-path reuse**

Target behavior:
- after initial discovery, watcher avoids unnecessary full traversal for known files
- message lookup prefers indexed path resolution

- [ ] **Step 2: Run targeted collector tests and verify failure**

Run: `go test ./internal/collector -run 'TestCodexWatcher|TestReadUserMessages'`

- [ ] **Step 3: Implement discovered-path tracking**

Keep correctness over maximal cleverness. Preserve recursive fallback behavior.

- [ ] **Step 4: Re-run targeted collector tests**

Run: `go test ./internal/collector -run 'TestCodexWatcher|TestReadUserMessages'`

### Task 7: Incremental Session Token Aggregation

**Files:**
- Modify: `internal/storage/db.go`
- Modify: `internal/storage/db_test.go`
- Optional: create `internal/storage/db_bench_test.go`

- [ ] **Step 1: Write failing tests for incremental total updates**

Cover:
- token insert updates totals without full-table recompute
- backfill/repair paths still reconcile correctly

- [ ] **Step 2: Run storage tests and verify failure**

Run: `go test ./internal/storage -run 'Test(TokenUsage|Backfill|SessionTotals)'`

- [ ] **Step 3: Implement incremental update helpers**

Keep or retain a fallback reconciliation function for repair operations.

- [ ] **Step 4: Re-run storage tests**

Run: `go test ./internal/storage -run 'Test(TokenUsage|Backfill|SessionTotals)'`

- [ ] **Step 5: Add or run benchmarks**

Run: `go test ./internal/storage -bench .`

## Chunk 4: Documentation, CI, And Release

### Task 8: Documentation Alignment

**Files:**
- Modify: `CLAUDE.md`
- Modify: `README.md`
- Modify: `README_EN.md`

- [ ] **Step 1: Update stale tab and workflow references**

Replace `tabAgentTree` and any outdated UI descriptions with current behavior.

- [ ] **Step 2: Verify docs against code**

Cross-check with `internal/tui/model.go` and CLI commands in `cmd/agmon/main.go`.

### Task 9: Windows CI And Release Path

**Files:**
- Modify: `.github/workflows/ci.yml`
- Modify: `.goreleaser.yml`
- Optional: create `docs/release.md`

- [ ] **Step 1: Add Windows test/build coverage to CI**

Run Go tests and build on Windows in addition to Linux.

- [ ] **Step 2: Document release expectations**

Document:
- version tagging flow
- current Homebrew tap status
- what is and is not automated

- [ ] **Step 3: Verify workflow syntax and local build assumptions**

Run: `go test ./... && go vet ./... && go test -race ./...`

### Task 10: Pricing Data Maintainability

**Files:**
- Modify: `internal/collector/cost.go`
- Modify: `internal/collector/codex.go`
- Create: `internal/collector/pricing.go`
- Modify: `internal/collector/*_test.go`

- [ ] **Step 1: Write failing tests around pricing table lookup**

Cover:
- known model families
- default fallback pricing

- [ ] **Step 2: Run collector pricing tests and verify failure**

Run: `go test ./internal/collector -run 'Test.*Pricing|Test.*Cost'`

- [ ] **Step 3: Extract pricing tables into dedicated data structures**

This is maintainability-oriented, not a dynamic remote update system.

- [ ] **Step 4: Re-run collector pricing tests**

Run: `go test ./internal/collector -run 'Test.*Pricing|Test.*Cost'`

## Final Verification

- [ ] Run: `gofmt -l .`
- [ ] Run: `go test ./...`
- [ ] Run: `go vet ./...`
- [ ] Run: `go test -race ./...`
- [ ] Review `git diff --stat`
- [ ] Commit in logical batches, not one monolith

Plan complete and saved to `docs/superpowers/plans/2026-03-26-quality-hardening.md`. Ready to execute.
