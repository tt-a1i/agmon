<p align="center">
  <img src="https://img.shields.io/badge/agmon-AI%20Agent%20%E5%8F%AF%E8%A7%82%E6%B5%8B%E6%80%A7-7C3AED?style=flat-square&logo=data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIyNCIgaGVpZ2h0PSIyNCIgdmlld0JveD0iMCAwIDI0IDI0IiBmaWxsPSJub25lIiBzdHJva2U9IndoaXRlIiBzdHJva2Utd2lkdGg9IjIiPjxwYXRoIGQ9Ik0xMyAyTDMgMTRoOWwtMSA4IDEwLTEyaC05bDEtOHoiLz48L3N2Zz4=&logoColor=white" alt="agmon" height="28">
</p>

<h1 align="center">agmon</h1>

<p align="center">
  <strong>AI 编码 Agent 实时可观测性工具</strong>
</p>

<p align="center">
  <a href="https://github.com/tt-a1i/agmon/releases"><img src="https://img.shields.io/github/v/release/tt-a1i/agmon?style=flat-square&color=7C3AED&label=version" alt="版本"></a>
  <img src="https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go">
  <a href="https://github.com/tt-a1i/agmon/blob/main/LICENSE"><img src="https://img.shields.io/badge/License-MIT-22c55e?style=flat-square" alt="许可证"></a>
  <img src="https://img.shields.io/badge/%E5%B9%B3%E5%8F%B0-macOS%20%7C%20Linux%20%7C%20Windows-6B7280?style=flat-square" alt="平台">
  <img src="https://img.shields.io/badge/Claude%20Code-%E5%B7%B2%E6%94%AF%E6%8C%81-F59E0B?style=flat-square" alt="Claude Code">
  <img src="https://img.shields.io/badge/Codex-%E5%B7%B2%E6%94%AF%E6%8C%81-22C55E?style=flat-square" alt="Codex">
</p>

<p align="center">
  <a href="./README_EN.md">English</a>
</p>

---

> 在一个终端面板中监控 Claude Code 和 Codex 的 Token 消耗、费用、工具调用，消息列表，支持tui和web面板。
<p align="center">
  <img width="711" alt="image" src="https://github.com/user-attachments/assets/b1dc6609-868e-4c24-bfc0-73baa9c81432" />
</p>

<p align="center">
  <img width="711" alt="工具调用" src="https://github.com/user-attachments/assets/1fcc7162-0af7-49c3-93f4-2e947c531549" />
</p>

<p align="center">
<img width="711" alt="image" src="https://github.com/user-attachments/assets/90389a23-352d-4159-ad78-aed5a0b1a54a" />
</p>


<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/architecture.svg">
    <source media="(prefers-color-scheme: light)" srcset="docs/architecture-light.svg">
    <img src="docs/architecture.svg" alt="agmon 架构图" width="100%">
  </picture>
</p>

## 功能

- **多平台** — Claude Code + Codex 统一视图
- **Token 追踪** — 输入、输出、缓存创建、缓存读取 — 按会话、按模型细分
- **费用估算** — 模型感知定价（Opus / Sonnet / Haiku / GPT-5 / GPT-4.1）
- **7 天费用趋势** — Stats 视图内置垂直柱状图，一眼看清每日花费走势
- **工具调用追踪** — 名称、参数、结果、耗时、状态
- **对话消息** — 浏览每个会话中的用户提示词，支持 `/` 搜索过滤
- **会话标签** — `agmon tag <id> "备注"` 给会话打标签，方便回忆
- **时间范围统计** — 今日 / 本周 / 本月 / 全部 Token 与费用聚合
- **费用报告** — `agmon report --weekly/--monthly` 生成 Markdown 费用报告（按模型、按会话细分）
- **Web Dashboard** — `agmon web` 启动本地 Web 面板，支持深色/浅色模式、面积图、会话详情、对话回顾
- **实时更新** — daemon 广播事件，TUI 实时刷新
- **零配置** — `agmon setup` + `agmon`，单二进制文件，无依赖

## 支持平台

| 平台 | 接入方式 | 说明 |
|------|---------|------|
| **Claude Code** | Hooks + JSONL 日志监听 | `agmon setup` 自动注入 hooks 到 `~/.claude/settings.json` |
| **Codex** | JSONL 日志监听 | 自动轮询 `~/.codex/sessions/` |

## 安装

### 一键安装（推荐）

```bash
curl -sL https://raw.githubusercontent.com/tt-a1i/agmon/main/install.sh | sh
```

### Homebrew

仅当 release 流水线配置了 Homebrew tap 仓库和 `HOMEBREW_TAP_GITHUB_TOKEN` 时可用。
发布细节见 [docs/release.md](docs/release.md)。

```bash
brew install tt-a1i/tap/agmon
```

### Go Install

```bash
go install github.com/tt-a1i/agmon/cmd/agmon@latest
```

### 从源码构建

```bash
git clone https://github.com/tt-a1i/agmon.git
cd agmon
make install
```

## 快速开始

```bash
# 1. 配置 Claude Code hooks
agmon setup

# 2. 启动 TUI（自动启动 daemon）
agmon
```

就这样。正常使用 Claude Code 或 Codex，agmon 在后台自动采集所有数据。

## 命令

| 命令 | 说明 |
|------|------|
| `agmon` | 启动 TUI（自动启动 daemon） |
| `agmon daemon` | 仅启动 daemon |
| `agmon status` | 快速查看会话摘要 |
| `agmon report [session]` | 详细文本报告 |
| `agmon report --weekly` | 本周 Markdown 费用报告 |
| `agmon report --monthly` | 本月 Markdown 费用报告 |
| `agmon cost [today\|week]` | Token 用量统计 |
| `agmon web [--port N]` | 启动 Web Dashboard（默认端口 8370） |
| `agmon clean [days]` | 清理 N 天前的历史数据（默认 7 天） |
| `agmon tag <id> [text]` | 给会话打标签（省略 text 则清除） |
| `agmon setup` | 配置 Claude Code hooks |
| `agmon uninstall` | 卸载 hooks 并停止 daemon |
| `agmon version` | 显示版本 |

## TUI 视图

按 **Tab** 切换视图：

| 视图 | 内容 |
|------|------|
| **Dashboard** | 会话列表（费用、上下文占用、状态、标签）；汇总栏支持 `t` 键切换时间范围 |
| **Messages** | 从 Claude / Codex JSONL 日志中提取的用户对话消息，支持 `/` 搜索 |
| **Tool Calls** | 实时工具调用流，支持展开/折叠查看详情 |
| **Stats** | 7 天费用柱状图、工具调用统计、Agent 分布、文件变更汇总 |

## 快捷键

| 按键 | 操作 |
|------|------|
| `Tab` / `Shift+Tab` | 切换视图 |
| `j` / `k` | 上 / 下导航 |
| `G` | 跳到底部 |
| `Enter` | 选择会话 / 展开详情 |
| `[` / `]` | 切换会话（任意视图） |
| `/` | 过滤当前列表 |
| `t` | 切换时间范围（今日 → 本周 → 本月 → 全部） |
| `p` | 切换平台过滤（全部 / Claude / Codex） |
| `s` | 切换排序（最近 / 费用） |
| `c` | 复制会话恢复命令 |
| `Esc` | 清除过滤 |
| `q` | 退出 |

## Web Dashboard

```bash
agmon web              # 打开 http://localhost:8370
agmon web --port 9000  # 自定义端口
```

浏览器面板功能：

- 费用面积图（Canvas 绘制，hover 显示详情，点击按天筛选会话）
- 模型费用占比 + 工具调用排行
- 会话列表（搜索、排序、费用色标）
- 会话详情（对话消息、工具时间线、文件变更、Agent 层级）
- 深色/浅色模式切换（自动检测系统偏好）
- 键盘导航（`j`/`k` 选择、`Enter` 打开、`/` 搜索、`←`/`→` 切换、`Esc` 返回、`?` 帮助）

## 架构

顶部的架构图展示了完整数据流。下面是组件职责速查：

- **Daemon** — 通过 Unix socket 接收 Claude hook 事件，存入 SQLite，实时广播给 TUI / Web
- **Claude hooks** — `PreToolUse` / `PostToolUse` / `SessionStart` / `SessionEnd` 等 8 个事件
- **日志监听器** — Claude watcher 扫描 `~/.claude/projects/` 的 JSONL 提取 token；Codex watcher 轮询 `~/.codex/sessions/`，内存去重
- **TUI** — bubbletea 四视图（Dashboard / Messages / Tool Calls / Timeline），订阅 daemon 实时事件
- **Web** — 独立 HTTP 服务 + 嵌入式 SPA，读取 SQLite，提供 REST API 与费用报表

> 交互版架构图（主题切换 + PNG/SVG 导出）：[`docs/architecture.html`](docs/architecture.html)
>
> ASCII 版速写：
>
> ```
> Claude Code hooks ──→ agmon emit ──→ Unix socket ─┐
> Claude JSONL 日志 ──→ ClaudeLogWatcher ───────────┤
> Codex  JSONL 日志 ──→ CodexWatcher ───────────────┘
>                                                    ▼
>                                              agmon daemon
>                                                    │
>                                          SQLite (~/.agmon/data/agmon.db)
>                                                    │
>                                  agmon TUI  ◄─────┴─────►  agmon web
> ```

## 数据存储

```
~/.agmon/
├── data/agmon.db    # SQLite 数据库
├── agmon.sock       # Unix domain socket
└── daemon.pid       # PID 锁文件
```

## 卸载

```bash
agmon uninstall        # 移除 hooks，停止 daemon
rm -rf ~/.agmon        # 删除所有数据
```

## 许可证

[MIT](LICENSE)
