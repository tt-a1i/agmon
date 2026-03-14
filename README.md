# agmon

Real-time observability for AI coding agents. See what your agents are doing, how much they cost, and where they fail.

```
╭─ agmon ──────────────────────────────────────────╮
│ Active: 3 sessions  Today: $1.24                 │
│                                                  │
│ SESSION          PLATFORM  TOKENS    COST  STATUS│
│ feat/auth        claude    42.1k    $0.83   ●    │
│ fix/nav-bug      claude    12.3k    $0.24   ●    │
│ refactor/api     codex      8.7k    $0.17   ◌    │
╰──────────────────────────────────────────────────╯
```

## Features

- **Token & cost tracking** — per agent, per session, real-time
- **Tool call traces** — who called what, params, result, duration
- **Agent hierarchy** — main agent → subagent tree visualization
- **Success/failure/retry** — spot failing tools instantly
- **Session timeline** — chronological event stream
- **File change tracking** — what got created, edited, deleted

## Supported Platforms

- Claude Code
- Codex

## Install

Requires Go 1.22+.

```bash
git clone https://github.com/tt-a1i/agmon.git
cd agmon
make install
```

## Quick Start

```bash
# Auto-configure Claude Code hooks
agmon setup

# Launch TUI (starts daemon automatically)
agmon
```

## Commands

```
agmon                 Start TUI (auto-starts daemon)
agmon daemon          Start daemon only
agmon status          Quick session summary
agmon cost            Today's cost
agmon setup           Configure Claude Code hooks
agmon version         Show version
```

## TUI Keybindings

| Key | Action |
|-----|--------|
| `Tab` | Switch view |
| `j/k` | Navigate up/down |
| `Enter` | Select / expand details |
| `q` | Quit |

## Architecture

```
Claude Code hooks → Unix socket → agmon daemon → SQLite
                                       ↓
                                   agmon TUI
```

The daemon receives events via Unix socket, stores them in SQLite, and streams to the TUI for real-time rendering.

## License

MIT
