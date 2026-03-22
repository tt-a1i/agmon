<p align="center">
  <img src="https://img.shields.io/badge/agmon-AI%20Agent%20Observability-7C3AED?style=flat-square&logo=data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIyNCIgaGVpZ2h0PSIyNCIgdmlld0JveD0iMCAwIDI0IDI0IiBmaWxsPSJub25lIiBzdHJva2U9IndoaXRlIiBzdHJva2Utd2lkdGg9IjIiPjxwYXRoIGQ9Ik0xMyAyTDMgMTRoOWwtMSA4IDEwLTEyaC05bDEtOHoiLz48L3N2Zz4=&logoColor=white" alt="agmon" height="28">
</p>

<h1 align="center">agmon</h1>

<p align="center">
  <strong>Real-time observability for AI coding agents</strong>
</p>

<p align="center">
  <a href="https://github.com/tt-a1i/agmon/releases"><img src="https://img.shields.io/badge/version-0.2.0-7C3AED?style=flat-square" alt="Version"></a>
  <img src="https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go">
  <a href="https://github.com/tt-a1i/agmon/blob/main/LICENSE"><img src="https://img.shields.io/badge/License-MIT-22c55e?style=flat-square" alt="License"></a>
  <img src="https://img.shields.io/badge/Platform-macOS%20%7C%20Linux-6B7280?style=flat-square" alt="Platform">
  <img src="https://img.shields.io/badge/Claude%20Code-supported-F59E0B?style=flat-square" alt="Claude Code">
  <img src="https://img.shields.io/badge/Codex-supported-22C55E?style=flat-square" alt="Codex">
</p>

<p align="center">
  <a href="./README.md">中文文档</a>
</p>

---

> Monitor token consumption, costs, tool calls, and file changes across your Claude Code and Codex sessions — all in a single terminal dashboard.

<p align="center">
  <img width="732" alt="Dashboard" src="https://github.com/user-attachments/assets/06664199-5860-484c-818c-0b3257313dde" />
</p>

<p align="center">
  <img width="711" alt="Tool Calls" src="https://github.com/user-attachments/assets/32d70f5b-e6ab-48be-98c0-12209ddcd621" />
</p>


## Features

- **Multi-platform** — Claude Code + Codex in one unified view
- **Token tracking** — input, output, cache creation, cache read — per session, per model
- **Cost estimation** — model-aware pricing (Opus / Sonnet / Haiku / GPT-4)
- **Tool call traces** — name, params, result, duration, success/failure status
- **Session timeline** — chronological event stream with file changes
- **Conversation messages** — browse user prompts within each session
- **Time range stats** — Today / Week / Month / All token & cost aggregation
- **Live updates** — daemon broadcasts events, TUI refreshes in real time
- **Zero config** — `agmon setup` + `agmon`, single binary, no dependencies

## Supported Platforms

| Platform | Integration | How |
|----------|-------------|-----|
| **Claude Code** | Hooks + JSONL log watcher | `agmon setup` auto-injects hooks into `~/.claude/settings.json` |
| **Codex** | JSONL log watcher | Automatic — polls `~/.codex/sessions/` |

## Install

### From source

```bash
git clone https://github.com/tt-a1i/agmon.git
cd agmon
make install
```

### Homebrew (coming soon)

```bash
brew install tt-a1i/tap/agmon
```

## Quick Start

```bash
# 1. Configure Claude Code hooks
agmon setup

# 2. Launch TUI (auto-starts daemon)
agmon
```

That's it. Use Claude Code or Codex normally — agmon captures everything in the background.

## Commands

| Command | Description |
|---------|-------------|
| `agmon` | Start TUI (auto-starts daemon) |
| `agmon daemon` | Start daemon only |
| `agmon status` | Quick session summary |
| `agmon report [session]` | Detailed text report |
| `agmon cost [today\|week]` | Token usage statistics |
| `agmon clean [days]` | Remove sessions older than N days (default: 7) |
| `agmon setup` | Configure Claude Code hooks |
| `agmon uninstall` | Remove hooks and stop daemon |
| `agmon version` | Show version |

## TUI Views

Press **Tab** to switch between views:

| View | Content |
|------|---------|
| **Dashboard** | Session list with cost, context usage, status; summary bar with time range toggle (`t` key) |
| **Messages** | User conversation messages from Claude JSONL logs |
| **Tool Calls** | Real-time tool call stream with duration and expand/collapse details |
| **Timeline** | Chronological events: agent lifecycle, tool calls, file changes |

## Keybindings

| Key | Action |
|-----|--------|
| `Tab` / `Shift+Tab` | Switch view |
| `j` / `k` | Navigate up / down |
| `G` | Jump to bottom |
| `Enter` | Select session / expand details |
| `[` / `]` | Switch session (any view) |
| `/` | Filter current list |
| `t` | Cycle time range (Today → Week → Month → All) |
| `c` | Copy session resume command |
| `Esc` | Clear filter |
| `q` | Quit |

## Architecture

```
Claude Code hooks ──→ agmon emit ──→ Unix socket
                                         │
Claude JSONL logs ──→ ClaudeLogWatcher ──→│
                                         │
Codex JSONL logs  ──→ CodexWatcher ──────→│
                                         ▼
                                    agmon daemon
                                         │
                                    SQLite (~/.agmon/data/agmon.db)
                                         │
                                    agmon TUI (Bubbletea)
```

- **Daemon** — receives events via Unix socket, stores to SQLite, broadcasts to TUI
- **Claude hooks** — `PreToolUse`, `PostToolUse`, `SessionStart`, `SessionEnd`, etc.
- **Log watchers** — poll JSONL files for token usage data (every 3s)
- **TUI** — connects to daemon, renders 4 views with live refresh

## Data Storage

```
~/.agmon/
├── data/agmon.db    # SQLite database
├── agmon.sock       # Unix domain socket
└── daemon.pid       # PID lock file
```

## Uninstall

```bash
agmon uninstall        # remove hooks, stop daemon
rm -rf ~/.agmon        # remove all data
```

## License

[MIT](LICENSE)
