# agmon - AI Agent Observability Tool

## Overview

Real-time observability for AI coding agents. TUI-based monitor that shows what your agents are doing, how much they cost, and where they fail.

**Supported platforms:** Claude Code, Codex (v1)

## Architecture

```
Claude Code hooks ──→ Unix socket ──→ agmon daemon (aggregate/store)
Codex logs ─────────→                        ↓
                                      ~/.agmon/data/agmon.db (SQLite)
                                             ↓
                                      agmon tui (connect to daemon)
```

### Components

1. **agmon emit** — lightweight CLI called by hooks, sends events to daemon via Unix socket
2. **agmon daemon** — receives events, parses logs, aggregates data, stores to SQLite
3. **agmon tui** — connects to daemon, renders real-time TUI

### Communication

- Unix domain socket: `~/.agmon/agmon.sock`
- JSON messages over socket

## Data Collection

### Claude Code

Hooks configured in `~/.claude/settings.json`:
- `PreToolUse` — tool name, params, session ID, timestamp
- `PostToolUse` — result summary, duration, success/fail
- `Notification` — subagent lifecycle events

Background log parsing: scan `~/.claude/projects/` JSONL logs for token usage and cost data.

### Codex

Separate goroutine watches Codex log directory, parses into unified event format.

## Storage

SQLite database at `~/.agmon/data/agmon.db` (using modernc.org/sqlite, pure Go, no CGO).

### Tables

- **sessions** — session_id, platform, start_time, status, total_tokens, total_cost
- **agents** — agent_id, session_id, parent_agent_id, role, status
- **tool_calls** — call_id, agent_id, tool_name, params_summary, result_summary, start_time, duration_ms, status (success/fail/retry)
- **token_usage** — agent_id, input_tokens, output_tokens, model, timestamp
- **file_changes** — session_id, file_path, change_type, timestamp

## TUI Views

4 views, Tab to switch:

### [1] Dashboard (default)
- Active session count, total cost today
- Session list: name, platform, tokens, cost, status

### [2] Agent Tree
- Hierarchical view of main agent → subagents
- Per-agent token usage and status

### [3] Tool Calls
- Real-time tool call stream
- Tool name, target, duration, success/fail
- Enter to expand details (params, result)

### [4] Timeline
- Chronological event stream per session
- Agent lifecycle + tool calls + file changes

### Keybindings
- `Tab` — switch view
- `Enter` — expand details
- `j/k` — navigate
- `q` — quit
- `/` — search/filter

## CLI Commands

```
agmon                    # start TUI (auto-starts daemon)
agmon daemon             # start daemon only
agmon status             # quick summary of active sessions
agmon report [session]   # text report for a session
agmon cost [today|week]  # cost statistics
agmon setup              # auto-configure hooks
agmon uninstall          # clean up hooks and data
```

## V1 Metrics

1. Token consumption (per agent/subagent, input/output)
2. Cost estimation (real-time USD)
3. Tool call traces (who called what, params, result, duration)
4. Agent call tree (main → subagent hierarchy)
5. Success/failure/retry tracking
6. Session timeline
7. File change tracking

## Tech Stack

- **Language:** Go
- **TUI:** bubbletea + lipgloss + bubbles
- **Storage:** SQLite (modernc.org/sqlite)
- **Distribution:** goreleaser + Homebrew tap

## Distribution

- GitHub Releases via goreleaser (darwin/linux × amd64/arm64)
- Homebrew tap: `brew install agmon`
- Single binary, zero dependencies
