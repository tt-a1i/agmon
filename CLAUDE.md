# agmon — 开发指南

## 项目概述

agmon 是一个 AI 编码 Agent 实时可观测性工具，监控 Claude Code 和 Codex 的 Token 消耗、费用、工具调用、文件变更等。

**技术栈：** Go 1.24 · Bubbletea TUI · SQLite (modernc, 纯 Go) · Unix socket

---

## 架构

```
Claude Code hooks ──→ agmon emit ──→ Unix socket (~/.agmon/agmon.sock)
                                           │
Codex JSONL 日志 ──→ CodexWatcher ─────────┘
                                           │
                                     agmon daemon
                                           │
                                    SQLite (~/.agmon/data/agmon.db)
                                           │
                                     agmon TUI (bubbletea)
```

### 组件职责

| 组件 | 文件 | 职责 |
|------|------|------|
| `agmon emit` | `cmd/agmon/main.go:runEmit` | Claude hook 的轻量接收端，读 stdin → 发 Unix socket |
| daemon | `internal/daemon/daemon.go` | 接收事件、存 SQLite、广播给订阅者 |
| collector/claude | `internal/collector/claude.go` | Claude hook 事件解析 → 统一 Event |
| collector/codex | `internal/collector/codex.go` | 轮询 Codex JSONL 日志 → 统一 Event |
| collector/cost | `internal/collector/cost.go` / `pricing.go` | Claude / Codex 费用估算 |
| storage | `internal/storage/` | SQLite schema、读写、查询 |
| tui | `internal/tui/*.go` | Bubbletea TUI，4 个 tab |
| event | `internal/event/event.go` | 统一事件类型定义 |

---

## 构建与运行

```bash
make build          # 编译到 ./agmon
make install        # 编译并复制到 $GOPATH/bin
go test ./...       # 运行所有测试
go vet ./...        # 静态检查

agmon setup         # 向 ~/.claude/settings.json 注入 hooks
agmon               # 启动 TUI（自动启动 daemon）
agmon daemon        # 仅启动 daemon
```

**本地调试 Claude hooks：**
```bash
echo '{"hook_event_name":"PreToolUse","session_id":"test-123","tool_name":"Read","tool_use_id":"tu-abc"}' | agmon emit
```

---

## 数据库 Schema

5 张表，均在 `internal/storage/db.go:migrate()` 中用 `CREATE TABLE IF NOT EXISTS` 创建：

- `sessions` — 会话（session_id PK, platform, start_time, status, total_input_tokens, total_output_tokens, total_cost_usd）
- `agents` — Agent 实例（agent_id PK, session_id FK, parent_agent_id, role, status）
- `tool_calls` — 工具调用（call_id PK, agent_id, session_id FK, tool_name, params_summary, result_summary, duration_ms, status）
- `token_usage` — Token 记录（id AUTOINCREMENT, agent_id, session_id FK, input_tokens, output_tokens, model, cost_usd）
- `file_changes` — 文件变更（id AUTOINCREMENT, session_id FK, file_path, change_type）

**Schema 变更规则：** `migrate()` 以 `CREATE TABLE IF NOT EXISTS` / `CREATE INDEX IF NOT EXISTS` 为主，并通过 `addColumnIfMissing()` 做幂等 `ALTER TABLE ADD COLUMN`。允许新增带默认值的兼容列；不允许删除或重命名已有列。

---

## Claude Code Hook 集成

Claude Code 向每个 hook 的 stdin 发送 JSON。已注册的 hooks：

```
SessionStart, SessionEnd, Stop,
PreToolUse, PostToolUse, PostToolUseFailure,
SubagentStart, SubagentStop
```

### 关键字段

| Hook | 关键字段 |
|------|---------|
| SessionStart | `session_id`, `cwd` |
| PreToolUse | `session_id`, `agent_id`, `tool_name`, `tool_input` (JSON), `tool_use_id` |
| PostToolUse | `session_id`, `agent_id`, `tool_name`, `tool_result`, `tool_use_id` |
| PostToolUseFailure | 同上 |
| Stop | `session_id`, `agent_id`, `reason`, `agent_transcript_path` |
| SubagentStart | `session_id`, `agent_id`, `agent_type` |
| SubagentStop | `session_id`, `agent_id` |

**注意：** Claude hook 事件本身**不携带 token 数量**。Token 数据需通过 `agent_transcript_path`（Stop hook）或扫描 `~/.claude/projects/*/` 下的 JSONL 文件获取。

### Claude JSONL 日志格式

路径：`~/.claude/projects/<cwd-encoded>/<session-id>.jsonl`

**cwd 编码规则：** 将 cwd 中每个 `/` 替换为 `-`（含首字符）。
例：`/Users/admin/code/agmon` → `-Users-admin-code-agmon`

每行一个 JSON 对象。顶层字段示例：

```json
{
  "type": "assistant",
  "sessionId": "126b5856-...",
  "uuid": "d832a967-...",
  "parentUuid": "deef5b7b-...",
  "cwd": "/Users/admin/code/agmon",
  "gitBranch": "main",
  "message": {
    "model": "claude-sonnet-4-6",
    "usage": {
      "input_tokens": 3,
      "output_tokens": 6,
      "cache_creation_input_tokens": 15104,
      "cache_read_input_tokens": 8909
    }
  }
}
```

关注 `type == "assistant"` 的行，从 `message.usage` 取 token 数量。

**只需统计 token 数量，不做费用估算。** 展示字段：
- `input_tokens` + `cache_creation_input_tokens` + `cache_read_input_tokens` → 输入 token 合计
- `output_tokens` → 输出 token 合计

`gitBranch` 字段也出现在顶层（包括非 assistant 类型的行），可用于 session 的人类可读展示名。

---

## Codex 日志格式

`~/.codex/sessions/YYYY/MM/DD/*.jsonl`，每行：`{"timestamp":"...","type":"session_meta|response_item|event_msg","payload":{...}}`

关键 payload 类型：
- `session_meta` → session 开始
- `response_item` → `function_call`（工具调用开始）/ `function_call_output`（工具调用结束）
- `event_msg` with `type:"token_count"` → token 统计

---

## 统一事件模型

所有数据源最终转换成 `internal/event/Event`：

```go
type Event struct {
    ID        string    // tool_use_id (Claude) 或 call_id (Codex)，用于 Pre/Post 关联
    Type      EventType // EventToolCallStart|End|AgentStart|End|TokenUsage|FileChange|SessionStart|End
    SessionID string
    AgentID   string
    Platform  Platform  // "claude" | "codex"
    Timestamp time.Time
    Data      EventData
}
```

Pre/Post 工具调用通过相同的 `ID`（`tool_use_id`）在 daemon 里关联，计算 duration。

---

## TUI 结构

4 个 tab（`internal/tui/view*.go` + `model.go`）：

| Tab | 常量 | 数据来源 |
|-----|------|---------|
| Dashboard | `tabDashboard` | `db.ListSessions()` |
| Messages | `tabMessages` | `collector.ReadUserMessages(platform, sessionID, cwd, 200)` |
| Tool Calls | `tabToolCalls` | `db.ListToolCalls(selectedSession, 500)` |
| Timeline | `tabTimeline` | agents + toolCalls + fileChanges 合并排序 |

TUI 每 2 秒轮询（`tickCmd`），也会在 daemon 广播事件时立即刷新（`listenEvents`）。
过滤列表在数据变化或 filter 变化时预计算，不在每次渲染时重算；`expandedCalls` 会在 refresh 时按当前 session 数据修剪。

**选中状态：**
- `selectedSession` — 当前查看的 session 索引（在 sessions slice 中）
- `selectedRow` — 当前高亮行索引（在当前 tab 的列表中）

---

## 已知问题与待实施清单

### 已完成

- [x] Claude token 永远为 0 → ClaudeLogWatcher 扫描 JSONL，INSERT OR IGNORE 防重复
- [x] TUI j 键越界 → bounds check + refresh 时 clamp
- [x] Enter 展开工具调用详情 → expandedCalls map，by call_id
- [x] Session 显示原始 UUID → gitBranch > filepath.Base(cwd) > UUID
- [x] 无法在非 Dashboard tab 切换 session → [/] 键
- [x] TUI 错误不显示 → footer 区域展示 m.err
- [x] pending 工具调用永不清理 → SessionEnd 时 MarkPendingToolCallsInterrupted
- [x] 僵尸 Session → daemon 启动时 `MarkStaleSessionsEnded(2h)` 清理长时间未结束会话
- [x] Token 重复计数（重启后）→ source_id 唯一索引 + INSERT OR IGNORE
- [x] 会话列表展示成本与上下文占用 → Dashboard 显示 `COST` / `CTX` 列与底部 session 预览
- [x] session 列表无滚动 → viewOffset + adjustScroll，所有 tab 均支持
- [x] Timeline 排序 O(n²) → sort.Slice
- [x] Makefile 增加 test / lint target

### 待完成（仅发布相关）

- [ ] **发布 v0.2.0** — goreleaser 配置就绪，打 tag 即可触发 CI release
- [ ] **Homebrew tap 仓库** — 需要外部仓库和 token，仓库内已补充 release 文档与 CI 降级逻辑

---

## 测试覆盖现状

| Package | 有测试 | 说明 |
|---------|--------|------|
| `internal/collector` | ✅ | claude_test.go, codex_test.go, cost_test.go |
| `internal/event` | ✅ | event_test.go |
| `internal/storage` | ✅ | db_test.go |
| `internal/daemon` | ✅ | daemon_test.go |
| `internal/tui` | ✅ | model_test.go |
| `cmd/agmon` | ✅ | cli_test.go |

新增功能必须在对应 package 的 `_test.go` 文件中覆盖。

---

## 代码约定

- 错误处理：底层函数返回 `error`；daemon 和 TUI 层用 `log.Printf` 记录非致命错误
- 不用 `panic`，不用 `log.Fatal`（除 main 函数初始化阶段）
- SQLite 查询：时间存为 `TEXT`（RFC3339 格式），读取时用 `parseTime()`
- 事件 ID：Claude 用 `tool_use_id`（由 Claude Code 提供，保证唯一）；Codex 用 `call_id`
- Token dedup：Codex watcher 在 `lastTokenUsage` map 中去重，避免同一状态重复计费

## Squad Collaboration

This project uses squad for multi-agent collaboration. Run `squad help` for all commands and usage guide.

