# agmon

Real-time observability for AI coding agents. See what your agents are doing, how many tokens they consume, and where they fail.

AI 编码 Agent 实时可观测性工具。查看 Agent 的行为、Token 消耗和失败点。

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

---

## Features / 功能

- **Token tracking** — per agent, per session, input + cache tokens in real time
  **Token 追踪** — 按 Agent、按会话实时统计输入及缓存 Token
- **Tool call traces** — who called what, params, result, duration
  **工具调用追踪** — 记录每次调用的参数、结果和耗时
- **Agent hierarchy** — main agent → subagent tree visualization
  **Agent 层级树** — 主 Agent → 子 Agent 树状结构可视化
- **Success/failure/retry** — spot failing tools instantly
  **成功/失败/重试** — 即时发现失败的工具调用
- **Session timeline** — chronological event stream with file changes
  **会话时间线** — 按时间排列的事件流，含文件变更记录
- **Multi-session** — monitor multiple concurrent agent sessions
  **多会话** — 同时监控多个并发 Agent 会话
- **Zero config** — one command to set up, single binary
  **零配置** — 一条命令完成配置，单二进制文件

## Supported Platforms / 支持平台

- **Claude Code** — via hooks (auto-configured) / 通过 hooks 接入（自动配置）
- **Codex** — via log file watching / 通过日志文件轮询

---

## Install / 安装

Requires Go 1.22+ / 需要 Go 1.22+

```bash
git clone https://github.com/tt-a1i/agmon.git
cd agmon
make install
```

## Quick Start / 快速开始

```bash
# 1. Auto-configure Claude Code hooks / 自动配置 Claude Code hooks
agmon setup

# 2. Launch TUI (starts daemon automatically) / 启动 TUI（自动启动后台进程）
agmon
```

That's it. Use Claude Code normally — agmon captures everything in the background.

就这样。正常使用 Claude Code，agmon 在后台自动采集数据。

---

## Commands / 命令

```
agmon                    Start TUI (auto-starts daemon) / 启动 TUI
agmon daemon             Start daemon only / 仅启动后台进程
agmon status             Quick session summary / 快速查看会话摘要
agmon report [session]   Detailed text report / 详细文本报告
agmon cost [today|week]  Token usage statistics / Token 用量统计
agmon clean [days]       Remove sessions older than N days (default: 7) / 清理 N 天前的历史数据
agmon setup              Configure Claude Code hooks / 配置 Claude Code hooks
agmon uninstall          Remove hooks and stop daemon / 卸载 hooks 并停止后台进程
agmon version            Show version / 显示版本
```

## TUI Views / TUI 视图

Press **Tab** to switch / 按 **Tab** 切换视图：

| View / 视图 | What it shows / 内容 |
|-------------|----------------------|
| **Dashboard** | Active sessions, today's token summary / 活跃会话、今日 Token 汇总 |
| **Agent Tree** | Main agent → subagent hierarchy with token counts / Agent 层级树及 Token 统计 |
| **Tool Calls** | Real-time tool call stream with duration and status / 实时工具调用流 |
| **Timeline** | Chronological events: agent lifecycle, tool calls, file changes / 按时间排列的事件流 |

## Keybindings / 快捷键

| Key / 按键 | Action / 操作 |
|------------|---------------|
| `Tab` / `Shift+Tab` | Switch view / 切换视图 |
| `j` / `k` | Navigate up/down / 上下导航 |
| `Enter` | Select session / expand tool call details / 选择会话 / 展开工具调用详情 |
| `[` / `]` | Switch session (any view) / 切换会话（任意视图） |
| `/` | Filter current list / 过滤当前列表 |
| `Esc` | Clear filter / 清除过滤 |
| `q` | Quit / 退出 |

---

## Architecture / 架构

```
Claude Code hooks ──→ Unix socket ──→ agmon daemon ──→ SQLite
Claude log watcher ──→                     ↓
Codex log watcher ──→                  agmon TUI
```

- **Daemon** receives events via Unix socket, stores to SQLite, broadcasts to subscribers
  **后台进程** 通过 Unix socket 接收事件，存入 SQLite，广播给订阅者
- **TUI** connects to daemon, polls database, renders real-time views
  **TUI** 连接后台进程，轮询数据库，渲染实时视图
- **Claude log watcher** polls `~/.claude/projects/` every 3s for token usage
  **Claude 日志监听器** 每 3 秒扫描 `~/.claude/projects/` 获取 Token 用量
- **Codex watcher** polls `~/.codex/sessions/` every 3s for new entries
  **Codex 监听器** 每 3 秒轮询 `~/.codex/sessions/` 获取新日志
- **PID lock** prevents multiple daemon instances / **PID 锁** 防止重复启动后台进程

## Data / 数据存储

All data stored in `~/.agmon/` / 所有数据存储于 `~/.agmon/`：

```
~/.agmon/
├── data/agmon.db    # SQLite database / SQLite 数据库
├── agmon.sock       # Unix domain socket
└── daemon.pid       # PID lock file / PID 锁文件
```

## Uninstall / 卸载

```bash
agmon uninstall        # removes hooks, stops daemon / 移除 hooks，停止后台进程
rm -rf ~/.agmon        # removes all data / 删除所有数据
```

## License

MIT
