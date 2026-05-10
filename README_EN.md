<p align="center">
  <img src="https://img.shields.io/badge/TokenMeter-AI%20Agent%20Usage%20Meter-7C3AED?style=flat-square&logo=data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIyNCIgaGVpZ2h0PSIyNCIgdmlld0JveD0iMCAwIDI0IDI0IiBmaWxsPSJub25lIiBzdHJva2U9IndoaXRlIiBzdHJva2Utd2lkdGg9IjIiPjxwYXRoIGQ9Ik0xMyAyTDMgMTRoOWwtMSA4IDEwLTEyaC05bDEtOHoiLz48L3N2Zz4=&logoColor=white" alt="TokenMeter" height="28">
</p>

<h1 align="center">TokenMeter</h1>

<p align="center">
  <strong>Local usage meter for AI coding agents</strong>
</p>

<p align="center">
  <a href="https://github.com/tt-a1i/tokenmeter/releases"><img src="https://img.shields.io/github/v/release/tt-a1i/tokenmeter?style=flat-square&color=7C3AED&label=version" alt="Version"></a>
  <img src="https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go">
  <a href="https://github.com/tt-a1i/tokenmeter/blob/main/LICENSE"><img src="https://img.shields.io/badge/License-MIT-22c55e?style=flat-square" alt="License"></a>
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
    <img src="docs/architecture.svg" alt="tokenmeter architecture diagram" width="100%">
  </picture>
</p>

## Features

- **Multi-platform** — Claude Code + Codex in one unified view
- **Token tracking** — input, output, cache creation, cache read — per session, per model
- **Cost estimation** — model-aware pricing (Opus / Sonnet / Haiku / GPT-5 / GPT-4.1)
- **7-day cost chart** — vertical bar chart in Stats view showing daily spend at a glance
- **Tool call traces** — name, params, result, duration, success/failure status
- **Conversation messages** — browse user prompts within each session, with `/` search
- **Session tags** — `tokenmeter tag <id> "note"` to label sessions for easy recall
- **Time range stats** — Today / Week / Month / All token & cost aggregation
- **Share recaps** — `tokenmeter share [session]` creates a compact Markdown session recap for sharing or handoff
- **Live updates** — daemon broadcasts events, TUI refreshes in real time
- **Zero config** — `tokenmeter setup` + `tokenmeter`, single binary, no dependencies

## Supported Platforms

| Platform | Integration | How |
|----------|-------------|-----|
| **Claude Code** | Hooks + JSONL log watcher | `tokenmeter setup` auto-injects hooks into `~/.claude/settings.json` |
| **Codex** | JSONL log watcher | Automatic — polls `~/.codex/sessions/` |

## Install

### Quick Install (recommended)

```bash
curl -sL https://raw.githubusercontent.com/tt-a1i/tokenmeter/main/install.sh | sh
```

### Homebrew

Available only when the release pipeline is configured with a Homebrew tap repository and `HOMEBREW_TAP_GITHUB_TOKEN`.
See [docs/release.md](docs/release.md) for release prerequisites.

```bash
brew install tt-a1i/tap/tokenmeter
```

### Go Install

```bash
go install github.com/tt-a1i/tokenmeter/cmd/tokenmeter@latest
```

### From source

```bash
git clone https://github.com/tt-a1i/tokenmeter.git
cd tokenmeter
make install
```

## Quick Start

```bash
# 1. Configure Claude Code hooks
tokenmeter setup

# 2. Launch TUI (auto-starts daemon)
tokenmeter
```

That's it. Use Claude Code or Codex normally — TokenMeter captures everything in the background.

## Commands

| Command | Description |
|---------|-------------|
| `tokenmeter` | Start TUI (auto-starts daemon) |
| `tokenmeter daemon` | Start daemon only |
| `tokenmeter status` | Quick session summary |
| `tokenmeter report [session]` | Detailed text report |
| `tokenmeter share [session]` | Shareable Markdown session recap |
| `tokenmeter cost [today\|week]` | Token usage statistics |
| `tokenmeter clean [days]` | Remove sessions older than N days (default: 7) |
| `tokenmeter tag <id> [text]` | Tag a session with a note (omit text to clear) |
| `tokenmeter setup` | Configure Claude Code hooks |
| `tokenmeter uninstall` | Remove hooks and stop daemon |
| `tokenmeter version` | Show version |

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
| `r` | Copy shareable session recap |
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
> Claude Code hooks ──→ tokenmeter emit ──→ Unix socket ─┐
> Claude JSONL logs ──→ ClaudeLogWatcher ───────────┤
> Codex  JSONL logs ──→ CodexWatcher ───────────────┘
>                                                    ▼
>                                              tokenmeter daemon
>                                                    │
>                                          SQLite (~/.tokenmeter/data/tokenmeter.db)
>                                                    │
>                                  tokenmeter TUI  ◄─────┴─────►  tokenmeter web
> ```

## Data Storage

```
~/.tokenmeter/
├── data/tokenmeter.db    # SQLite database
├── tokenmeter.sock       # Unix domain socket
└── daemon.pid       # PID lock file
```

When upgrading from the old name, TokenMeter continues to read `~/.agmon/` if it exists and `~/.tokenmeter/` has not been created yet, so existing history remains available.

## Uninstall

```bash
tokenmeter uninstall        # remove hooks, stop daemon
rm -rf ~/.tokenmeter        # remove all data
```

## License

[MIT](LICENSE)
