# agmon

Real-time observability for AI coding agents. See what your agents are doing, how many tokens they consume, and where they fail.

```
╭─ agmon ──────────────────────────────────────────╮
│ Active: 3 sessions  Today: 42.1k in / 8.3k out  │
│                                                  │
│ SESSION          PLATFORM       IN      OUT STATUS│
│ feat/auth        claude      32.1k    8.3k   ●   │
│ fix/nav-bug      claude       8.7k    2.1k   ●   │
│ refactor/api     codex        1.3k    0.9k   ◌   │
╰──────────────────────────────────────────────────╯
```

## Features

- **Token tracking** — per agent, per session, input + cache tokens in real time
- **Tool call traces** — who called what, params, result, duration
- **Agent hierarchy** — main agent → subagent tree visualization
- **Success/failure/retry** — spot failing tools instantly
- **Session timeline** — chronological event stream with file changes
- **File change tracking** — what got created, edited, deleted
- **Multi-session** — monitor multiple concurrent agent sessions
- **Zero config** — one command to set up, single binary

## Supported Platforms

- **Claude Code** — via hooks (auto-configured)
- **Codex** — via log file watching

## Install

Requires Go 1.22+.

```bash
git clone https://github.com/tt-a1i/agmon.git
cd agmon
make install
```

## Quick Start

```bash
# 1. Auto-configure Claude Code hooks
agmon setup

# 2. Launch TUI (starts daemon automatically)
agmon
```

That's it. Use Claude Code normally — agmon captures everything in the background.

## Commands

```
agmon                    Start TUI (auto-starts daemon)
agmon daemon             Start daemon only
agmon status             Quick session summary
agmon report [session]   Detailed text report for a session
agmon cost [today|week]  Token usage statistics
agmon clean [days]       Remove sessions older than N days (default: 7)
agmon setup              Configure Claude Code hooks
agmon uninstall          Remove hooks and stop daemon
agmon version            Show version
```

## TUI Views

**Tab** to switch between views:

| View | What it shows |
|------|---------------|
| **Dashboard** | Active sessions, today's token summary, session list |
| **Agent Tree** | Main agent → subagent hierarchy with token counts |
| **Tool Calls** | Real-time tool call stream with duration and status |
| **Timeline** | Chronological events: agent lifecycle, tool calls, file changes |

## Keybindings

| Key | Action |
|-----|--------|
| `Tab` / `Shift+Tab` | Switch view |
| `j` / `k` | Navigate up/down |
| `Enter` | Select session / expand tool call details |
| `[` / `]` | Switch session (any view) |
| `/` | Filter current list |
| `Esc` | Clear filter |
| `q` | Quit |

## Architecture

```
Claude Code hooks ──→ Unix socket ──→ agmon daemon ──→ SQLite
Codex log watcher ──→                      ↓
                                       agmon TUI
```

- **Daemon** receives events via Unix socket, stores to SQLite, broadcasts to subscribers
- **TUI** connects to daemon, polls database, renders real-time views
- **PID lock** prevents multiple daemon instances
- **Claude log watcher** polls `~/.claude/projects/` every 3s for token usage
- **Codex watcher** polls `~/.codex/sessions/` every 3s for new entries

## Data

All data stored in `~/.agmon/`:

```
~/.agmon/
├── data/agmon.db    # SQLite database
├── agmon.sock       # Unix domain socket
└── daemon.pid       # PID lock file
```

## Uninstall

```bash
agmon uninstall        # removes hooks, stops daemon
rm -rf ~/.agmon        # removes all data
```

## License

MIT
