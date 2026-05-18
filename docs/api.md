# TokenMeter Web HTTP API

本文档梳理 `tm web` 守护进程对外暴露的全部 HTTP endpoint。所有路径以 `/api/` 为前缀，唯一例外是 Prometheus 文本端点 `/metrics`。

> 实现位置：`internal/web/server.go` · `internal/web/insights.go` · `internal/web/forecast.go`
> 静态前端：`internal/web/static/index.html`（由 `http.FileServer` 在根路径 `/` 直接 serve）

---

## 总览

### 服务约定

| 项目 | 默认值 / 行为 |
|---|---|
| 监听地址 | `127.0.0.1:8370`（仅 localhost） |
| 端口覆盖 | `tm web --port N` |
| 协议 | HTTP/1.1，无 TLS（本机面板） |
| 响应类型 | JSON（`application/json`），SSE 端点除外 |
| 时间字段 | 通常 RFC3339 字符串；`forecast` 顶层用 UTC time.Time，前端按 ISO 8601 解析 |
| gzip 压缩 | 内容协商自动启用；**SSE (`/api/events`) 与 `/metrics` 强制关闭** |
| CORS | 未启用 — 仅供本机前端使用 |
| 错误信封 | `{"error": "<public message>"}`，内部错误一律 `internal server error`，详细原因记到 stdout log |

### 鉴权

鉴权可选：

* `tm web` 启动时若 `~/.tokenmeter/web-token` 存在且非空，自动启用 Bearer 鉴权。
* `--token <value>` 强制指定 token；`--no-auth` 禁用；`--generate-token` 写入随机 token。
* 未启用时所有 endpoint 直接放行。
* 启用时所有 `/api/*` 与 `/metrics` 都受保护；静态资源（`/index.html`、SVG icon、manifest 等）**永远放行**。
* Token 传递方式（任选其一）：
  * HTTP header：`Authorization: Bearer <token>`
  * Query 参数：`?token=<token>`（仅 SSE / 浏览器直链方便用）

鉴权失败：`401 Unauthorized`，`WWW-Authenticate: Bearer realm="tm"`，body 为 `{"error":"missing or invalid bearer token"}`。

### 通用错误响应

| 状态码 | 含义 |
|---|---|
| 400 | 参数非法（如未知 platform、缺少必填、JSON 解析失败） |
| 401 | 鉴权失败（启用 token 时） |
| 404 | 资源不存在（session 找不到、budget id 不存在） |
| 405 | 方法不允许；同时返回 `Allow` 头声明合法方法 |
| 500 | 内部错误 |
| 503 | 服务不可用（如 SSE 子系统未就绪、健康检查失败） |

### Endpoint 一览

| 分类 | Method | 路径 | 说明 |
|---|---|---|---|
| 健康/元 | GET | `/api/health` | DB + daemon 可达性 + uptime |
| 健康/元 | GET | `/metrics` | Prometheus 文本格式（无 gzip） |
| 数据查询 | GET | `/api/sessions` | session 列表（可按 workspace / platform / limit 过滤） |
| 数据查询 | GET | `/api/session/<id>` | 单 session 全维度详情 |
| 数据查询 | PUT | `/api/session/<id>/tag` | 写入 session 备注 tag |
| 数据查询 | GET | `/api/stats` | dashboard 顶部指标 + 近 7 天 + 热门 tool / session |
| 数据查询 | GET | `/api/costs` | 区间费用 + 日均 + 模型分布 |
| 数据查询 | GET | `/api/projection` | 当月费用线性外推 |
| 数据查询 | GET | `/api/compare?a=&b=` | 两个 session 的 tool / cost / file diff |
| 分析洞察 | GET | `/api/analytics` | 4 张分析卡片：top sessions / tool / model mix / anomaly |
| 分析洞察 | GET | `/api/insights` | 自动生成的 5 类 insight 卡片 |
| 分析洞察 | GET | `/api/forecast` | 周/月预算线性投影 + 趋势 + 同期对比 |
| 搜索/导出 | GET | `/api/search?q=` | 跨会话全文搜索（tool 参数 / 结果 / 文件路径） |
| 搜索/导出 | GET | `/api/export` | CSV / JSON 导出 |
| 搜索/导出 | GET | `/api/export-report` | 自包含 HTML 报告下载 |
| 实时 | GET | `/api/events` | SSE 事件流（token_usage / tool_call / session_start 等） |
| 管理 | GET / POST | `/api/budgets` | 列出 / 新建预算 |
| 管理 | PUT / DELETE | `/api/budgets/<id>` | 修改 / 删除预算 |

---

## 健康检查 / 元信息

### GET `/api/health`

DB + daemon 可达性 + uptime + build 版本。dashboard / 容器 livenessProbe / 自动化脚本可用。

**Query 参数**：无。

**Response (200)**：

```json
{
  "status": "healthy",
  "uptime_seconds": 1234,
  "checks": {
    "db": { "status": "ok", "latency_ms": 0.812 },
    "daemon": { "status": "ok" }
  },
  "version": "v0.8.1"
}
```

| 字段 | 类型 | 说明 |
|---|---|---|
| `status` | string | `healthy` / `unhealthy` |
| `uptime_seconds` | int64 | 自 server 启动经过的秒数 |
| `checks.db.status` | string | `ok` 或 `error: <reason>` |
| `checks.db.latency_ms` | float | SELECT count 探测延迟（毫秒） |
| `checks.daemon.status` | string | `ok` / `unreachable`（仅在已配置 socket 路径时探测） |
| `version` | string | `tm web --version` 同源；CI 注入或 `dev` |

**错误**：
* 503 — 任意 check `!= "ok"`，body 仍为完整 JSON 便于排查。

**示例**：

```bash
curl http://localhost:8370/api/health | jq .
```

**Source**：`internal/web/server.go:handleHealth`

---

### GET `/metrics`

Prometheus 文本曝光格式（无 gzip、`Content-Type: text/plain; version=0.0.4`）。

**Query 参数**：无。

**指标列表**：

| 指标 | 类型 | 说明 |
|---|---|---|
| `tokenmeter_build_info{version=""}` | gauge | 常量 1，version 写在 label 中 |
| `tokenmeter_sessions_total` | gauge | 总 session 数 |
| `tokenmeter_sessions_active` | gauge | 当前 active session |
| `tokenmeter_today_cost_usd` | gauge | 今日累计费用（本地 TZ） |
| `tokenmeter_today_tokens_input` | counter | 今日 input token |
| `tokenmeter_today_tokens_output` | counter | 今日 output token |
| `tokenmeter_daemon_dropped_broadcasts_total` | counter | 慢 subscriber 丢弃的事件 |
| `tokenmeter_daemon_dropped_shutdown_total` | counter | shutdown 期间丢弃的事件 |
| `tokenmeter_daemon_duplicate_tool_starts_total` | counter | 同一 tool_use_id 被 Pre-hook 重复发的次数 |
| `tokenmeter_budget_used_usd{name,platform}` | gauge | 每个 budget 已用 USD |
| `tokenmeter_budget_limit_usd{name,platform}` | gauge | budget limit |
| `tokenmeter_budget_percent{name,platform}` | gauge | 0–100 百分比 |

**错误**：500 — DB 查询失败。

**Source**：`internal/web/server.go:handleMetrics`

---

## 数据查询

### GET `/api/sessions`

返回 session 列表，按开始时间倒序，仅包含已产生 token 用量或仍 active 的会话。

**Query 参数**：

| 名称 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `limit` | int | 200 | cap 1000 |
| `platform` | string | — | `claude` / `codex`；其它值 400 |
| `workspace` | string | — | 按 cwd 精确或前缀（`workspace + "/"`）过滤；POSIX 路径，跨平台一致 |

`workspace` 与 `platform` 可同时传：先按 workspace 过滤后再按 platform 二次过滤。

**Response (200)**：JSON 数组（无 envelope），每个元素：

```json
[
  {
    "session_id": "126b5856-…",
    "platform": "claude",
    "start_time": "2026-05-18T10:23:11Z",
    "end_time": "2026-05-18T11:42:03Z",
    "status": "ended",
    "input_tokens": 18324,
    "output_tokens": 4096,
    "cost_usd": 0.41,
    "git_branch": "main",
    "cwd": "/Users/admin/code/agmon",
    "model": "claude-sonnet-4-6",
    "tag": "release-prep"
  }
]
```

`end_time` / `git_branch` / `cwd` / `model` / `tag` 为可选字段（`omitempty`），未结束 / 未填充时不出现。

**错误**：
* 400 — `invalid platform`
* 500 — DB 错误

**示例**：

```bash
curl 'http://localhost:8370/api/sessions?platform=claude&limit=50'
curl 'http://localhost:8370/api/sessions?workspace=/Users/admin/code/agmon'
```

**Source**：`internal/web/server.go:handleSessions`

---

### GET `/api/session/<id>`

返回单个 session 的全维度详情：metadata + 用户消息 + tool 调用 + agent 树 + file change + tool 聚合统计 + agent 聚合统计 + 按模型拆分的 token / cost。

支持 session ID 前缀匹配（前缀必须唯一）。

**Response (200)**：

```json
{
  "session": {
    "session_id": "126b5856-…", "platform": "claude",
    "start_time": "…", "end_time": "…", "status": "ended",
    "input_tokens": 0, "output_tokens": 0, "cost_usd": 0.0,
    "git_branch": "", "cwd": "", "model": "", "tag": ""
  },
  "messages": [
    { "time": "…", "content": "first user message…" }
  ],
  "tools": [
    {
      "call_id": "tu-abc",
      "tool_name": "Edit",
      "params": "{\"file_path\":\"main.go\"}",
      "result": "ok",
      "start_time": "…",
      "duration_ms": 124,
      "status": "ok"
    }
  ],
  "agents": [
    { "agent_id": "agent-1", "parent_id": "", "role": "main", "status": "ended" }
  ],
  "files": [
    { "path": "main.go", "change_type": "edit", "time": "…" }
  ],
  "tool_stats": [
    { "name": "Edit", "count": 23, "avg_ms": 130, "fail_count": 0 }
  ],
  "agent_stats": [
    {
      "agent_id": "agent-1", "parent_id": "", "role": "main", "status": "ended",
      "tool_calls": 23, "input_tokens": 8000, "output_tokens": 1500, "cost_usd": 0.12
    }
  ],
  "models": [
    { "model": "claude-sonnet-4-6", "input_tokens": 8000, "output_tokens": 1500, "cost_usd": 0.12 }
  ]
}
```

`messages`：来自本地 JSONL 解析（Claude `~/.claude/projects/...`、Codex `~/.codex/sessions/...`），最多 100 条。
`tools`：最多 200 条；超过的从尾部截断。
`models`：按模型分桶，软失败 — 查询出错时打日志并返回空数组，不影响主响应。

**错误**：
* 400 — `missing session id` / `ambiguous session prefix`
* 404 — `session not found`
* 500 — DB 错误

**示例**：

```bash
curl http://localhost:8370/api/session/126b5856
curl http://localhost:8370/api/session/$FULL_ID | jq '.session'
```

**Source**：`internal/web/server.go:handleSessionDetail`

---

### PUT `/api/session/<id>/tag`

写入 session 备注 tag（dashboard "Tag" 卡片调用）。

**Request body**：

```json
{ "tag": "release-prep" }
```

`tag` 自动 `TrimSpace`；传空串等价于清空。

**Response (200)**：

```json
{ "session_id": "126b5856-…", "tag": "release-prep" }
```

**错误**：
* 400 — `invalid json body` / `missing session id` / `ambiguous session prefix`
* 404 — `session not found`
* 405 — 其它 method
* 500 — DB 错误

**Source**：`internal/web/server.go:handleSessionTagUpdate`（由 `handleSessionDetail` 当路径以 `/tag` 结尾时分发）

---

### GET `/api/stats`

dashboard 顶部 metric strip + 7 天 daily 折线 + 本周 top tool / top session。所有时间窗口锚定本地时区。

**Query 参数**：无。

**Response (200)**：

```json
{
  "total_sessions": 421,
  "active_count": 2,
  "today_cost": 1.42,
  "week_cost": 12.05,
  "daily_costs": [
    { "Date": "2026-05-12", "Cost": 1.40 }
  ],
  "top_tools": [
    { "ToolName": "Read", "Count": 248, "AvgMs": 91, "FailCount": 2 }
  ],
  "top_sessions": [
    {
      "SessionID": "…", "Platform": "claude",
      "GitBranch": "main", "CWD": "/Users/…",
      "CostUSD": 4.21, "InputTokens": 102000, "OutputTokens": 13500
    }
  ]
}
```

`daily_costs` 固定 7 个元素；`top_tools` 裁剪到前 10。

**字段大小写说明**：`DailyCost` / `ToolStatRow` / `TopSessionRow` 在 storage 包未带 json tag，默认按 Go 字段名首字母大写序列化（`Date` / `Cost` / `ToolName` / 等）。前端已按该形态消费。

**错误**：500 — DB 错误。

**Source**：`internal/web/server.go:handleStats`

---

### GET `/api/costs`

区间费用 + daily 桶 + 同期 (previous-period) 对比 + 按模型分组。

**Query 参数**：

| 名称 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `range` | string | `week` | `today` / `week` / `month` / `all`；其它值视作"过去 7 天" |

时间桶用本地时区（SQL `DATE(timestamp, 'localtime')`），所以 UTC+8 用户的"今天"不会漏掉凌晨小时。

**Response (200)**：

```json
{
  "range": "week",
  "total_cost": 12.05,
  "prev_cost": 9.87,
  "daily_costs": [
    { "Date": "2026-05-12", "Cost": 1.40 }
  ],
  "models": [
    { "Model": "claude-sonnet-4-6", "InputTokens": 100000, "OutputTokens": 12000, "CostUSD": 8.40 }
  ]
}
```

`prev_cost`：与当前 range 同长度的、紧邻向前推一个区间的费用合计；用于 dashboard "vs prev period" 百分比。

**错误**：500 — DB 错误。

**示例**：

```bash
curl 'http://localhost:8370/api/costs?range=month' | jq '.total_cost'
```

**Source**：`internal/web/server.go:handleCosts`

---

### GET `/api/projection`

当月费用的线性外推（按本月日均推到月末）。

**Response (200)**：

```json
{
  "used_so_far": 12.05,
  "days_elapsed": 18,
  "days_in_month": 31,
  "avg_daily_cost": 0.67,
  "projected_total": 20.77,
  "confidence": "medium"
}
```

`confidence`：`low` / `medium` / `high` — 由月初进度与数据天数决定。该 endpoint 与 `/api/forecast` 功能重叠（forecast 更新、参数更全），新 UI 建议优先用 forecast。

**错误**：405 / 500。

**Source**：`internal/web/server.go:handleProjection` → `storage.GetMonthCostProjection`

---

### GET `/api/compare`

比较两个 session 的 tool 使用 / 费用 / token / 文件改动差异，供 dashboard "Compare" 视图使用。

**Query 参数**：

| 名称 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `a` | string | — | session id 或唯一前缀 |
| `b` | string | — | session id 或唯一前缀 |

**Response (200)**：

```json
{
  "tool_diff": [
    { "name": "Edit", "a_count": 12, "b_count": 8 }
  ],
  "cost_diff": { "a": 0.42, "b": 0.31, "delta": -0.11 },
  "token_diff": {
    "a_input": 18000, "b_input": 13000, "delta_input": -5000,
    "a_output": 4000, "b_output": 3500, "delta_output": -500
  },
  "file_diff": {
    "a_only": ["foo.go"],
    "b_only": ["bar.go"],
    "common": ["main.go"]
  }
}
```

`delta = b - a`，正数表示 b 大于 a。`file_diff.*` 字符串按字典序排序。

**错误**：
* 400 — `missing session id` / `ambiguous session prefix`
* 404 — `session not found`
* 500 — DB 错误

**Source**：`internal/web/server.go:handleCompare`

---

## 分析 / 洞察

### GET `/api/analytics`

4 张分析卡片：top expensive sessions / tool breakdown / model mix / anomaly。可选同期对比。

**Query 参数**：

| 名称 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `range` | string | `7d` | `today` / `week` / `month` / `all`；未知值落入 7 天 |
| `workspace` | string | — | cwd 精确或前缀匹配；过滤 top sessions 与 anomaly |
| `tag` | string | — | session tag 精确匹配（先 fetch 最近 2000 session 建 tagMap） |
| `model` | string | — | 仅过滤 model mix 桶 |
| `compare` | string | — | `prev_period` 时附加同期对比 |

**Response (200)**：

```json
{
  "range": "week",
  "generated_at": "2026-05-18T14:00:00Z",
  "top_expensive_sessions": [
    {
      "id": "126b5856-…", "cost_usd": 4.21,
      "workspace": "/Users/admin/code/agmon", "git_branch": "main",
      "platform": "claude"
    }
  ],
  "tool_breakdown": [
    { "tool": "Edit", "count": 230, "avg_duration_ms": 130, "fail_count": 1 }
  ],
  "model_mix_daily": [
    {
      "date": "week",
      "models": [
        { "model": "claude-sonnet-4-6", "input_tokens": 100000, "output_tokens": 12000, "cost_usd": 8.40 }
      ]
    }
  ],
  "anomalies": [
    {
      "session_id": "…", "reason": "cost > 2σ from mean (z=2.84)",
      "cost_usd": 4.21, "mean": 0.51, "z_score": 2.84
    }
  ],
  "previous_period": {
    "top_expensive_sessions": [],
    "tool_breakdown": [],
    "model_mix_daily": [],
    "anomalies": []
  }
}
```

`tool_breakdown` 不受 `workspace` / `tag` 过滤（注释里指出：AllToolStats 在 range 级聚合，逐 session join 会 N+1，权衡放弃）。
`anomalies`：z-score > 2 的 session，最少 3 条样本才计算，否则返回 null。
`previous_period` 仅在 `compare=prev_period` 时出现，结构是 `analyticsPeriodData`（不含 range / generated_at）。

**错误**：405 / 500。

**Source**：`internal/web/server.go:handleAnalytics` + `buildAnalyticsPeriod` + `computeCostAnomalies`

---

### GET `/api/insights`

自动生成的人话型 insight 卡片。每张 insight 都自描述（title + body），同时附结构化 `value` + `metadata` 供 UI 二次渲染或过滤。

**Query 参数**：

| 名称 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `range` | string | `week` | `week` / `month` / `all`；其它值 400 `invalid range` |

**Response (200)**：

```json
{
  "range": "week",
  "generated_at": "2026-05-18T14:00:00Z",
  "insights": [
    {
      "id": "peak_day",
      "kind": "peak_day",
      "title": "Peak day was Friday",
      "body": "Spent $4.21 on 2026-05-16 — 2.1× your week average",
      "value": 4.21,
      "metadata": { "date": "2026-05-16", "ratio": 2.08 }
    }
  ]
}
```

**Insight kinds**：

| kind | 触发条件 | metadata 字段 |
|---|---|---|
| `peak_day` | range 内活跃天 ≥2 且 highest 日 cost ≥ 1.5× 区间日均 | `date`, `ratio` |
| `top_tool` | 至少有一次 tool 调用 | `tool`, `share`, `saved_hours_est`（按表内系数估算） |
| `model_mix_shift` | 当前/上一周期都有模型记录 且 share 最大涨幅 ≥ 10pp；range=all 时跳过 | `model_up`, `model_down`, `delta_up`, `delta_down` |
| `cost_anomaly` | 至少 3 个 session 且存在 z-score > 2 的异常 | `session_id`, `z_score`, `mean` |
| `rhythm` | 4 个时段 (Mon–Fri 9–13 / 13–18 / 18–23, weekends) 中某档占总活动 ≥ 30% | `window` (`weekday_morning` / `weekday_afternoon` / `weekday_evening` / `weekend`) |

`insights` 字段始终是数组（哪怕一条也命中不上）。

**错误**：
* 400 — `invalid range`
* 405 — 非 GET
* 500 — DB 错误

**Source**：`internal/web/insights.go:handleInsights`

---

### GET `/api/forecast`

周/月预算的线性投影：burn-rate × 剩余天 + 已花。附带置信度、趋势、同期对比。

**Query 参数**：

| 名称 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `period` | string | `month` | `month` / `week`；其它值 400 `invalid period` |

**Response (200)**：

```json
{
  "period": "month",
  "period_start": "2026-05-01T00:00:00Z",
  "period_end": "2026-05-31T23:59:59Z",
  "now": "2026-05-18T14:00:00Z",
  "elapsed_days": 18,
  "remaining_days": 13,
  "spent_to_date": 12.05,
  "burn_rate_per_day": 0.67,
  "burn_rate_window_days": 7,
  "projected_total": 20.77,
  "projected_remaining": 8.72,
  "confidence": "medium",
  "trend": "stable",
  "vs_previous_period": {
    "previous_total": 18.40,
    "projected_change_pct": 12.9,
    "direction": "up"
  }
}
```

| 字段 | 说明 |
|---|---|
| `burn_rate_per_day` | 过去 `burn_rate_window_days`（默认 7）天的日均费用 |
| `confidence` | `low` / `medium` / `high`；取决于 elapsed share 与数据天数 |
| `trend` | `up` / `down` / `stable`；近 3 天均值与 burn rate 偏离 ±15% 时翻转 |
| `vs_previous_period` | 仅在上一周期有费用记录时返回；`direction` 是 `up` / `down` / `flat`（±5pp 阈值） |

`period_start` / `period_end` / `now` 序列化为 RFC3339 UTC（注意：内部计算在 Local TZ 完成，response 再 `.UTC()`）。

**错误**：
* 400 — `invalid period`
* 405 — 非 GET
* 500 — DB 错误

**Source**：`internal/web/forecast.go:handleForecast`

---

## 搜索 / 导出

### GET `/api/search`

跨 session 全文搜索：覆盖 tool 参数 (`tool_param`) / tool 结果 (`tool_result`) / 文件路径 (`file`)。底层走 SQLite FTS5。

**Query 参数**：

| 名称 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `q` | string | — | 必填；rune 长度 ≥ 2 |
| `limit` | int | 50 | cap 200 |

**Response (200)**：JSON 数组（无 envelope）：

```json
[
  {
    "session_id": "…",
    "session_name": "main / agmon",
    "platform": "claude",
    "kind": "tool_param",
    "excerpt": "…<mark>foo</mark>…",
    "timestamp": "2026-05-18T10:23:11Z"
  }
]
```

`kind` 取值：`tool_param` / `tool_result` / `file`。`excerpt` 由 FTS5 的 highlight 函数生成，包含 `<mark>` 标签。

**错误**：
* 400 — `q required` / `query too short` / `invalid limit`
* 405 — 非 GET
* 500 — DB 错误

**示例**：

```bash
curl 'http://localhost:8370/api/search?q=goreleaser&limit=20'
```

**Source**：`internal/web/server.go:handleSearch` → `storage.SearchHits`

---

### GET `/api/export`

下载 session 用量明细，CSV 或 JSON 格式，按区间过滤。

**Query 参数**：

| 名称 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `range` | string | 7d | `today` / `week` / `month` / `all`；其它落入 7 天 |
| `format` | string | `csv` | `csv` / `json`；其它值 400 |

**响应头**：

```
Content-Type: text/csv; charset=utf-8   |   application/json
Content-Disposition: attachment; filename="tm-<range>-<YYYY-MM-DD>.<ext>"
```

**CSV header**：

```
date,session_id,session_name,platform,model,input_tokens,output_tokens,cache_tokens,cost_usd
```

CSV 字段含 `, " \r \n` 时按 RFC 4180 加双引号转义。`session_name` 优先 `git_branch`，否则 `cwd` 的 basename，否则空。

**JSON 格式**：流式数组，每行 1 个对象，字段名同 CSV 列；浮点 `cost_usd` 用 `strconv.AppendFloat(_, 'f', -1, 64)`。

**错误**：
* 400 — `invalid export format`
* 405 — 非 GET
* 500 — 区间解析失败

**示例**：

```bash
curl -o tokens-week.csv 'http://localhost:8370/api/export?range=week'
curl -o tokens-all.json 'http://localhost:8370/api/export?range=all&format=json'
```

**Source**：`internal/web/server.go:handleExport` + `writeExportCSV` + `writeExportJSON`

---

### GET `/api/export-report`

下载自包含 HTML 报告：top sessions / tool stats / model breakdown / anomalies 的可分享单文件版本。

**Query 参数**：

| 名称 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `range` | string | 7d | 同 `/api/export` |

**响应头**：

```
Content-Type: text/html; charset=utf-8
Content-Disposition: attachment; filename="tm-report-<range>-<YYYY-MM-DD>.html"
```

**错误**：
* 405 — 非 GET
* 500 — 区间解析失败

**Source**：`internal/web/server.go:handleExportReport`

---

## 实时

### GET `/api/events` (SSE)

服务器推送实时事件流（token_usage / tool_call / agent / session / file_change）。**禁用 gzip**（行级推送会被压缩窗口卡住），**禁用响应 write deadline**（长连接），并设置 `X-Accel-Buffering: no` 让 nginx 等反向代理不缓冲。

**Query 参数**：无（鉴权 token 可用 `?token=` 传入，EventSource 不支持自定义 header）。

**响应头**：

```
Content-Type: text/event-stream
Cache-Control: no-cache
Connection: keep-alive
X-Accel-Buffering: no
```

**协议**：标准 SSE。

* 心跳：每 30 秒发一行 `: heartbeat\n\n`（comment 行，浏览器忽略）。
* 事件：`data: <JSON>\n\n`，JSON 是 `event.Event`：

```json
{
  "id": "tu-abc",
  "type": "token_usage",
  "session_id": "…",
  "agent_id": "…",
  "platform": "claude",
  "timestamp": "2026-05-18T14:00:00Z",
  "data": { "input_tokens": 3, "output_tokens": 6, "model": "claude-sonnet-4-6", "cost_usd": 0.001 }
}
```

**Event types**：`session_start` / `session_update` / `session_end` / `tool_call_start` / `tool_call_end` / `agent_start` / `agent_end` / `token_usage` / `file_change`。

**Coalescing**：`token_usage` 事件经过 50ms 滑窗 buffer 合并去抖（高频 token 流不会刷爆 client），其它事件直通。stream 结束时 buffer 会 flush。

**错误**：
* 401 — 鉴权失败
* 405 — 非 GET
* 500 — 流式不支持（`http.Flusher` 断言失败，极少见）
* 503 — `event stream unavailable`（daemon socket 未配置或订阅失败）

**示例**：

```bash
curl -N -H 'Authorization: Bearer $TOKEN' http://localhost:8370/api/events
```

```javascript
const es = new EventSource('/api/events?token=' + token)
es.onmessage = (e) => console.log(JSON.parse(e.data))
```

**Source**：`internal/web/server.go:handleEvents` + `daemon.SSEBuffer`

---

## 管理 — 预算

### GET `/api/budgets` · POST `/api/budgets`

列出所有预算 / 新建预算。

**GET Response (200)**：

```json
[
  {
    "id": 1,
    "name": "Claude monthly",
    "monthly_usd": 50,
    "platform": "claude",
    "created_at": "2026-05-01T00:00:00Z",
    "updated_at": "2026-05-01T00:00:00Z",
    "usage": {
      "used": 12.05,
      "limit": 50,
      "percent": 24.1,
      "status": "ok"
    }
  }
]
```

`usage.status`：`ok` (<80%) / `warn` (≥80%) / `over` (≥100%)。

**POST Request body**：

```json
{
  "name": "Claude monthly",
  "monthly_usd": 50,
  "platform": "claude"
}
```

`platform` 留空表示"全部平台合计"。

**POST Response (201)**：返回新建 budget（结构同 GET 单元素）。

**错误**：
* 400 — `invalid request body` / DB 校验失败（如同名）
* 405 — 非 GET/POST；返回 `Allow: GET, POST`
* 500 — DB 错误

**Source**：`internal/web/server.go:handleBudgets` + `listBudgets` + `budgetJSON`

---

### PUT `/api/budgets/<id>` · DELETE `/api/budgets/<id>`

修改 / 删除。`<id>` 是正整数。

**PUT Request body**：同 POST。

**PUT Response (200)**：返回更新后的 budget。

**DELETE Response (204)**：No Content。

**错误**：
* 400 — `invalid budget id` / `invalid request body` / DB 校验失败
* 404 — `budget not found`（仅 PUT；DELETE 当前 unconditional 删除）
* 405 — 非 PUT/DELETE；返回 `Allow: PUT, DELETE`
* 500 — DB 错误

**Source**：`internal/web/server.go:handleBudgetByID`

---

## Roadmap / 备注

* `/api/projection` 与 `/api/forecast` 功能重叠：forecast 更通用（支持 week/month + 趋势 + 同期对比），projection 仅按本月线性外推。新前端建议默认调用 forecast；projection 可视为遗留。
* `/api/analytics` 与 `/api/insights` 同源数据（top sessions / tool stats / model mix），用途不同：analytics 偏数据原始切片，insights 偏自然语言卡片。
* 字段命名混合大小写：早期 storage 包暴露的 `DailyCost` / `ModelCostRow` / `ToolStatRow` / `TopSessionRow` 未带 json tag，序列化为大写字段名（`Date` / `Cost` / `ToolName` 等）；新加的 `Insight` / `Forecast` / `analyticsResponse` 全部带小写 snake_case tag。客户端按字面消费即可，文档章节里给的 JSON 示例已对齐实际响应。
* SSE 不支持 `Authorization` header（浏览器 EventSource API 限制），鉴权强制启用时必须使用 `?token=` query 形式。`subtle.ConstantTimeCompare` 在两种路径下都使用。
