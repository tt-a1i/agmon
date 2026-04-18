<p align="center">
  <img src="https://img.shields.io/badge/agmon-AI%20Agent%20Observability-7C3AED?style=flat-square&logo=data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIyNCIgaGVpZ2h0PSIyNCIgdmlld0JveD0iMCAwIDI0IDI0IiBmaWxsPSJub25lIiBzdHJva2U9IndoaXRlIiBzdHJva2Utd2lkdGg9IjIiPjxwYXRoIGQ9Ik0xMyAyTDMgMTRoOWwtMSA4IDEwLTEyaC05bDEtOHoiLz48L3N2Zz4=&logoColor=white" alt="agmon" height="28">
</p>

<h1 align="center">agmon</h1>

<p align="center">
  <strong>Real-time observability for AI coding agents</strong>
</p>

<p align="center">
  <a href="https://github.com/tt-a1i/agmon/releases"><img src="https://img.shields.io/github/v/release/tt-a1i/agmon?style=flat-square&color=7C3AED&label=version" alt="Version"></a>
  <img src="https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go">
  <a href="https://github.com/tt-a1i/agmon/blob/main/LICENSE"><img src="https://img.shields.io/badge/License-MIT-22c55e?style=flat-square" alt="License"></a>
  <img src="https://img.shields.io/badge/Platform-macOS%20%7C%20Linux%20%7C%20Windows-6B7280?style=flat-square" alt="Platform">
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

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/architecture.svg">
    <source media="(prefers-color-scheme: light)" srcset="docs/architecture-light.svg">
    <img src="docs/architecture.svg" alt="agmon architecture diagram" width="100%">
  </picture>
</p>

## Features

- **Multi-platform** — Claude Code + Codex in one unified view
- **Token tracking** — input, output, cache creation, cache read — per session, per model
- **Cost estimation** — model-aware pricing (Opus / Sonnet / Haiku / GPT-5 / GPT-4.1)
- **7-day cost chart** — vertical bar chart in Stats view showing daily spend at a glance
- **Tool call traces** — name, params, result, duration, success/failure status
- **Conversation messages** — browse user prompts within each session, with `/` search
- **Session tags** — `agmon tag <id> "note"` to label sessions for easy recall
- **Time range stats** — Today / Week / Month / All token & cost aggregation
- **Live updates** — daemon broadcasts events, TUI refreshes in real time
- **Zero config** — `agmon setup` + `agmon`, single binary, no dependencies

## Supported Platforms

| Platform | Integration | How |
|----------|-------------|-----|
| **Claude Code** | Hooks + JSONL log watcher | `agmon setup` auto-injects hooks into `~/.claude/settings.json` |
| **Codex** | JSONL log watcher | Automatic — polls `~/.codex/sessions/` |

## Install

### Quick Install (recommended)

```bash
curl -sL https://raw.githubusercontent.com/tt-a1i/agmon/main/install.sh | sh
```

### Homebrew

Available only when the release pipeline is configured with a Homebrew tap repository and `HOMEBREW_TAP_GITHUB_TOKEN`.
See [docs/release.md](docs/release.md) for release prerequisites.

```bash
brew install tt-a1i/tap/agmon
```

### Go Install

```bash
go install github.com/tt-a1i/agmon/cmd/agmon@latest
```

### From source

```bash
git clone https://github.com/tt-a1i/agmon.git
cd agmon
make install
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
| `agmon tag <id> [text]` | Tag a session with a note (omit text to clear) |
| `agmon setup` | Configure Claude Code hooks |
| `agmon uninstall` | Remove hooks and stop daemon |
| `agmon version` | Show version |

## TUI Views

Press **Tab** to switch between views:

| View | Content |
|------|---------|
| **Dashboard** | Session list with cost, context usage, status, tags; summary bar with time range toggle (`t` key) |
| **Messages** | User conversation messages from Claude / Codex JSONL logs, with `/` search |
| **Tool Calls** | Real-time tool call stream with duration and expand/collapse details |
| **Stats** | 7-day cost bar chart, tool usage stats, agent breakdown, file change summary |

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
| `p` | Cycle platform filter (All / Claude / Codex) |
| `s` | Cycle sort order (Recent / Cost) |
| `c` | Copy session resume command |
| `Esc` | Clear filter |
| `q` | Quit |

## Architecture

The diagram at the top shows the full data flow. Component cheat sheet:

- **Daemon** — receives Claude hook events over a Unix socket, persists them to SQLite, and broadcasts live events to TUI / Web
- **Claude hooks** — 8 events: `PreToolUse`, `PostToolUse`, `SessionStart`, `SessionEnd`, etc.
- **Log watchers** — Claude watcher scans JSONL under `~/.claude/projects/` for tokens; Codex watcher polls `~/.codex/sessions/` with in-memory deduplication
- **TUI** — bubbletea with four views (Dashboard / Messages / Tool Calls / Timeline), subscribes to the daemon's live event stream
- **Web** — standalone HTTP server + embedded SPA, reads SQLite, serves REST API and cost reports

> Interactive diagram (theme toggle + PNG/SVG export): [`docs/architecture.html`](docs/architecture.html)
>
> ASCII sketch:
>
> ```
> Claude Code hooks ──→ agmon emit ──→ Unix socket ─┐
> Claude JSONL logs ──→ ClaudeLogWatcher ───────────┤
> Codex  JSONL logs ──→ CodexWatcher ───────────────┘
>                                                    ▼
>                                              agmon daemon
>                                                    │
>                                          SQLite (~/.agmon/data/agmon.db)
>                                                    │
>                                  agmon TUI  ◄─────┴─────►  agmon web
> ```

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
