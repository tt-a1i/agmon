package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrAmbiguousSessionPrefix is returned by GetSessionByIDPrefix when the prefix matches
// more than one session. Callers (e.g. HTTP handlers) can use errors.Is to
// distinguish user-input errors from system errors without parsing strings.
var ErrAmbiguousSessionPrefix = errors.New("ambiguous session prefix")

type SessionRow struct {
	SessionID                string
	Platform                 string
	StartTime                time.Time
	EndTime                  *time.Time
	Status                   string
	TotalInputTokens         int
	TotalOutputTokens        int
	TotalCostUSD             float64
	CWD                      string
	GitBranch                string
	LatestContextTokens      int
	Model                    string
	TotalCacheReadTokens     int
	TotalCacheCreationTokens int
	Tag                      string
}

type SessionExportRow struct {
	Date         string  `json:"date"`
	SessionID    string  `json:"session_id"`
	SessionName  string  `json:"session_name"`
	Platform     string  `json:"platform"`
	Model        string  `json:"model"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CacheTokens  int     `json:"cache_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

type AgentRow struct {
	AgentID       string
	SessionID     string
	ParentAgentID string
	Role          string
	Status        string
	StartTime     time.Time
	EndTime       *time.Time
}

type ToolCallRow struct {
	CallID        string
	AgentID       string
	SessionID     string
	ToolName      string
	ParamsSummary string
	ResultSummary string
	StartTime     time.Time
	EndTime       *time.Time
	DurationMs    int64
	Status        string
}

type FileChangeRow struct {
	ID         int
	SessionID  string
	FilePath   string
	ChangeType string
	Timestamp  time.Time
}

type ToolStatRow struct {
	ToolName  string
	Count     int
	AvgMs     int64
	FailCount int
}

type AgentStatRow struct {
	AgentID       string
	ParentAgentID string
	Role          string
	Status        string
	ToolCallCount int
	InputTokens   int
	OutputTokens  int
	CostUSD       float64
}

func parseTime(s string) time.Time {
	t, ok := parseStorageTime(s)
	if !ok {
		return time.Time{}
	}
	return t.Local()
}

func parseTimePtr(s *string) *time.Time {
	if s == nil {
		return nil
	}
	t := parseTime(*s)
	return &t
}

func formatQueryTime(t time.Time) string {
	return formatStorageTime(t)
}

// DefaultSessionListLimit caps the row count for ListSessions(). Use
// ListSessionsLimit when a different cap is needed (e.g. the web dashboard
// asking for more than the default).
const DefaultSessionListLimit = 200

func (s *DB) ListSessions() ([]SessionRow, error) {
	return s.ListSessionsLimit(DefaultSessionListLimit)
}

// ListSessionsLimit returns up to limit "visible" sessions ordered by start
// time desc. A session is visible when it's active or has accumulated any
// tokens. Pass <= 0 to use DefaultSessionListLimit.
func (s *DB) ListSessionsLimit(limit int) ([]SessionRow, error) {
	if limit <= 0 {
		limit = DefaultSessionListLimit
	}
	rows, err := s.db.Query(`
		SELECT session_id, platform, start_time, end_time, status,
		       total_input_tokens, total_output_tokens, total_cost_usd,
		       cwd, git_branch, latest_context_tokens, model,
		       total_cache_read_tokens, total_cache_creation_tokens, tag
		FROM sessions
		WHERE status = 'active'
		   OR total_input_tokens > 0 OR total_output_tokens > 0
		   OR total_cache_read_tokens > 0 OR total_cache_creation_tokens > 0
		ORDER BY start_time DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []SessionRow
	for rows.Next() {
		var r SessionRow
		var startStr string
		var endStr *string
		if err := rows.Scan(&r.SessionID, &r.Platform, &startStr, &endStr,
			&r.Status, &r.TotalInputTokens, &r.TotalOutputTokens, &r.TotalCostUSD,
			&r.CWD, &r.GitBranch, &r.LatestContextTokens, &r.Model,
			&r.TotalCacheReadTokens, &r.TotalCacheCreationTokens, &r.Tag); err != nil {
			return nil, err
		}
		r.StartTime = parseTime(startStr)
		r.EndTime = parseTimePtr(endStr)
		result = append(result, r)
	}
	return result, rows.Err()
}

// ListSessionsByPlatform returns visible sessions for a single platform.
func (s *DB) ListSessionsByPlatform(platform string, limit int) ([]SessionRow, error) {
	if limit <= 0 {
		limit = DefaultSessionListLimit
	}
	rows, err := s.db.Query(`
		SELECT session_id, platform, start_time, end_time, status,
		       total_input_tokens, total_output_tokens, total_cost_usd,
		       cwd, git_branch, latest_context_tokens, model,
		       total_cache_read_tokens, total_cache_creation_tokens, tag
		FROM sessions
		WHERE platform = ?
		  AND (
		       status = 'active'
		    OR total_input_tokens > 0 OR total_output_tokens > 0
		    OR total_cache_read_tokens > 0 OR total_cache_creation_tokens > 0
		  )
		ORDER BY start_time DESC LIMIT ?
	`, platform, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []SessionRow
	for rows.Next() {
		var r SessionRow
		var startStr string
		var endStr *string
		if err := rows.Scan(&r.SessionID, &r.Platform, &startStr, &endStr,
			&r.Status, &r.TotalInputTokens, &r.TotalOutputTokens, &r.TotalCostUSD,
			&r.CWD, &r.GitBranch, &r.LatestContextTokens, &r.Model,
			&r.TotalCacheReadTokens, &r.TotalCacheCreationTokens, &r.Tag); err != nil {
			return nil, err
		}
		r.StartTime = parseTime(startStr)
		r.EndTime = parseTimePtr(endStr)
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *DB) ForEachSessionExportRow(from, to time.Time, fn func(SessionExportRow) error) error {
	rows, err := s.db.Query(`
		SELECT DATE(t.timestamp, 'localtime') as day,
		       t.session_id,
		       COALESCE(s.git_branch, ''),
		       COALESCE(s.cwd, ''),
		       s.platform,
		       COALESCE(NULLIF(t.model, ''), NULLIF(s.model, ''), '') as model,
		       t.input_tokens,
		       t.output_tokens,
		       t.cache_creation_tokens + t.cache_read_tokens as cache_tokens,
		       t.cost_usd
		FROM token_usage t
		JOIN sessions s ON t.session_id = s.session_id
		WHERE t.timestamp >= ? AND t.timestamp < ?
		ORDER BY t.timestamp ASC, t.id ASC
	`, formatQueryTime(from), formatQueryTime(to))
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var row SessionExportRow
		var gitBranch, cwd string
		if err := rows.Scan(&row.Date, &row.SessionID, &gitBranch, &cwd,
			&row.Platform, &row.Model, &row.InputTokens, &row.OutputTokens,
			&row.CacheTokens, &row.CostUSD); err != nil {
			return err
		}
		row.SessionName = exportSessionName(row.SessionID, gitBranch, cwd)
		if err := fn(row); err != nil {
			return err
		}
	}
	return rows.Err()
}

func exportSessionName(sessionID, gitBranch, cwd string) string {
	if gitBranch != "" {
		return gitBranch
	}
	if cwd != "" {
		return cwd
	}
	if len(sessionID) > 8 {
		return sessionID[:8]
	}
	return sessionID
}

// GetSessionByIDPrefix looks up a session by exact ID or unique prefix, searching
// all sessions (not just the filtered list returned by ListSessions).
// Returns an error if the prefix matches more than one session.
func (s *DB) GetSessionByIDPrefix(prefix string) (SessionRow, bool, error) {
	exact, found, err := s.getSessionByExactID(prefix)
	if err != nil || found {
		return exact, found, err
	}

	likePattern := escapeLikePattern(prefix) + "%"
	// Check for ambiguity before returning a result.
	var count int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM sessions WHERE session_id LIKE ? ESCAPE '\'`, likePattern,
	).Scan(&count); err != nil {
		return SessionRow{}, false, err
	}
	if count > 1 {
		return SessionRow{}, false, fmt.Errorf("%w: %q matches %d sessions; use more characters", ErrAmbiguousSessionPrefix, prefix, count)
	}

	row := s.db.QueryRow(`
		SELECT session_id, platform, start_time, end_time, status,
		       total_input_tokens, total_output_tokens, total_cost_usd,
		       cwd, git_branch, latest_context_tokens, model,
		       total_cache_read_tokens, total_cache_creation_tokens, tag
		FROM sessions WHERE session_id LIKE ? ESCAPE '\' LIMIT 1
	`, likePattern)
	return scanSessionRow(row)
}

func (s *DB) getSessionByExactID(sessionID string) (SessionRow, bool, error) {
	row := s.db.QueryRow(`
		SELECT session_id, platform, start_time, end_time, status,
		       total_input_tokens, total_output_tokens, total_cost_usd,
		       cwd, git_branch, latest_context_tokens, model,
		       total_cache_read_tokens, total_cache_creation_tokens, tag
		FROM sessions WHERE session_id = ?
	`, sessionID)
	return scanSessionRow(row)
}

func escapeLikePattern(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

func scanSessionRow(row *sql.Row) (SessionRow, bool, error) {
	var r SessionRow
	var startStr string
	var endStr *string
	err := row.Scan(&r.SessionID, &r.Platform, &startStr, &endStr,
		&r.Status, &r.TotalInputTokens, &r.TotalOutputTokens, &r.TotalCostUSD,
		&r.CWD, &r.GitBranch, &r.LatestContextTokens, &r.Model,
		&r.TotalCacheReadTokens, &r.TotalCacheCreationTokens, &r.Tag)
	if err == sql.ErrNoRows {
		return SessionRow{}, false, nil
	}
	if err != nil {
		return SessionRow{}, false, err
	}
	r.StartTime = parseTime(startStr)
	r.EndTime = parseTimePtr(endStr)
	return r, true, nil
}

func (s *DB) ListAgents(sessionID string) ([]AgentRow, error) {
	rows, err := s.db.Query(`
		SELECT agent_id, session_id, parent_agent_id, role, status, start_time, end_time
		FROM agents WHERE session_id = ? ORDER BY start_time ASC
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []AgentRow
	for rows.Next() {
		var r AgentRow
		var startStr string
		var endStr *string
		var parentID *string
		if err := rows.Scan(&r.AgentID, &r.SessionID, &parentID, &r.Role,
			&r.Status, &startStr, &endStr); err != nil {
			return nil, err
		}
		r.StartTime = parseTime(startStr)
		r.EndTime = parseTimePtr(endStr)
		if parentID != nil {
			r.ParentAgentID = *parentID
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *DB) ListToolCalls(sessionID string, limit int) ([]ToolCallRow, error) {
	rows, err := s.db.Query(`
		SELECT call_id, agent_id, session_id, tool_name,
		       COALESCE(params_summary, ''), COALESCE(result_summary, ''),
		       start_time, end_time, COALESCE(duration_ms, 0), status
		FROM tool_calls WHERE session_id = ? ORDER BY start_time DESC LIMIT ?
	`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ToolCallRow
	for rows.Next() {
		var r ToolCallRow
		var startStr string
		var endStr *string
		if err := rows.Scan(&r.CallID, &r.AgentID, &r.SessionID, &r.ToolName,
			&r.ParamsSummary, &r.ResultSummary, &startStr, &endStr,
			&r.DurationMs, &r.Status); err != nil {
			return nil, err
		}
		r.StartTime = parseTime(startStr)
		r.EndTime = parseTimePtr(endStr)
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *DB) ListFileChanges(sessionID string) ([]FileChangeRow, error) {
	rows, err := s.db.Query(`
		SELECT id, session_id, file_path, change_type, timestamp
		FROM file_changes WHERE session_id = ? ORDER BY timestamp ASC
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []FileChangeRow
	for rows.Next() {
		var r FileChangeRow
		var tsStr string
		if err := rows.Scan(&r.ID, &r.SessionID, &r.FilePath, &r.ChangeType, &tsStr); err != nil {
			return nil, err
		}
		r.Timestamp = parseTime(tsStr)
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *DB) ListToolStats(sessionID string) ([]ToolStatRow, error) {
	rows, err := s.db.Query(`
		SELECT tool_name,
		       COUNT(*) as cnt,
		       CAST(COALESCE(AVG(CASE WHEN duration_ms > 0 THEN duration_ms END), 0) AS INTEGER) as avg_ms,
		       SUM(CASE WHEN status IN ('fail','error') THEN 1 ELSE 0 END) as fail_cnt
		FROM tool_calls WHERE session_id = ?
		GROUP BY tool_name ORDER BY cnt DESC
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ToolStatRow
	for rows.Next() {
		var r ToolStatRow
		if err := rows.Scan(&r.ToolName, &r.Count, &r.AvgMs, &r.FailCount); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *DB) ListAgentStats(sessionID string) ([]AgentStatRow, error) {
	// Map empty agent_id tool calls to the main agent (first agent without a parent).
	rows, err := s.db.Query(`
		WITH main_agent AS (
			SELECT agent_id FROM agents
			WHERE session_id = ? AND (parent_agent_id IS NULL OR parent_agent_id = '')
			ORDER BY start_time ASC LIMIT 1
		),
		tool_counts AS (
			SELECT CASE WHEN tc.agent_id = '' THEN COALESCE((SELECT agent_id FROM main_agent), '') ELSE tc.agent_id END as agent_id,
			       COUNT(*) as cnt
			FROM tool_calls tc WHERE tc.session_id = ?
			GROUP BY 1
		),
		token_totals AS (
			SELECT CASE WHEN tu.agent_id = '' THEN COALESCE((SELECT agent_id FROM main_agent), '') ELSE tu.agent_id END as agent_id,
			       SUM(input_tokens) as in_tok,
			       SUM(output_tokens) as out_tok,
			       SUM(cost_usd) as cost
			FROM token_usage tu WHERE tu.session_id = ?
			GROUP BY 1
		)
		SELECT a.agent_id, COALESCE(a.parent_agent_id, ''), COALESCE(a.role, ''),
		       a.status,
		       COALESCE(tc.cnt, 0),
		       COALESCE(t.in_tok, 0), COALESCE(t.out_tok, 0), COALESCE(t.cost, 0)
		FROM agents a
		LEFT JOIN tool_counts tc ON a.agent_id = tc.agent_id
		LEFT JOIN token_totals t ON a.agent_id = t.agent_id
		WHERE a.session_id = ?
		ORDER BY a.start_time ASC
	`, sessionID, sessionID, sessionID, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []AgentStatRow
	for rows.Next() {
		var r AgentStatRow
		if err := rows.Scan(&r.AgentID, &r.ParentAgentID, &r.Role, &r.Status,
			&r.ToolCallCount, &r.InputTokens, &r.OutputTokens, &r.CostUSD); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return s.syntheticMainAgentStats(sessionID)
	}
	return result, nil
}

func (s *DB) syntheticMainAgentStats(sessionID string) ([]AgentStatRow, error) {
	var r AgentStatRow
	err := s.db.QueryRow(`
		SELECT
			COALESCE((SELECT COUNT(*) FROM tool_calls WHERE session_id = ? AND agent_id = ''), 0),
			COALESCE((SELECT SUM(input_tokens) FROM token_usage WHERE session_id = ? AND agent_id = ''), 0),
			COALESCE((SELECT SUM(output_tokens) FROM token_usage WHERE session_id = ? AND agent_id = ''), 0),
			COALESCE((SELECT SUM(cost_usd) FROM token_usage WHERE session_id = ? AND agent_id = ''), 0)
	`, sessionID, sessionID, sessionID, sessionID).Scan(&r.ToolCallCount, &r.InputTokens, &r.OutputTokens, &r.CostUSD)
	if err != nil {
		return nil, err
	}
	if r.ToolCallCount == 0 && r.InputTokens == 0 && r.OutputTokens == 0 && r.CostUSD == 0 {
		return nil, nil
	}
	r.AgentID = "main"
	r.Role = "main"
	r.Status = "active"
	return []AgentStatRow{r}, nil
}

// SetSessionTag sets or clears the tag on a session.
func (s *DB) SetSessionTag(sessionID, tag string) error {
	_, err := s.db.Exec(`UPDATE sessions SET tag = ? WHERE session_id = ?`, tag, sessionID)
	return err
}

// GetTokensSince returns total input and output tokens since the given time.
// If since is nil, returns all-time totals.
func (s *DB) GetTokensSince(since *time.Time) (input, output int, err error) {
	if since == nil {
		err = s.db.QueryRow(`
			SELECT COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0)
			FROM token_usage
		`).Scan(&input, &output)
	} else {
		err = s.db.QueryRow(`
			SELECT COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0)
			FROM token_usage WHERE timestamp >= ?
		`, formatQueryTime(*since)).Scan(&input, &output)
	}
	return
}

func (s *DB) GetTodayTokens() (int, int, error) {
	t := startOfToday()
	return s.GetTokensSince(&t)
}

func (s *DB) GetActiveSessionCount() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE status = 'active'`).Scan(&count)
	return count, err
}

// GetVisibleSessionCount counts the sessions that ListSessions would return
// (ignoring its LIMIT), so /api/stats "total sessions" stays consistent with
// what the dashboard list actually shows. Filter must match ListSessions.
func (s *DB) GetVisibleSessionCount() (int, error) {
	var count int
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM sessions
		WHERE status = 'active'
		   OR total_input_tokens > 0 OR total_output_tokens > 0
		   OR total_cache_read_tokens > 0 OR total_cache_creation_tokens > 0
	`).Scan(&count)
	return count, err
}

func (s *DB) GetAgentTokenSummary(agentID string) (inputTokens, outputTokens int, costUSD float64, err error) {
	err = s.db.QueryRow(`
		SELECT COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0), COALESCE(SUM(cost_usd), 0)
		FROM token_usage WHERE agent_id = ?
	`, agentID).Scan(&inputTokens, &outputTokens, &costUSD)
	return
}

// GetCostSince returns total cost since the given time.
// If since is nil, returns all-time cost.
func (s *DB) GetCostSince(since *time.Time) (float64, error) {
	var cost float64
	var err error
	if since == nil {
		err = s.db.QueryRow(`
			SELECT COALESCE(SUM(cost_usd), 0) FROM token_usage
		`).Scan(&cost)
	} else {
		err = s.db.QueryRow(`
			SELECT COALESCE(SUM(cost_usd), 0)
			FROM token_usage WHERE timestamp >= ?
		`, formatQueryTime(*since)).Scan(&cost)
	}
	return cost, err
}

// GetCostBetween returns total cost between two times.
func (s *DB) GetCostBetween(from, to time.Time) (float64, error) {
	var cost float64
	err := s.db.QueryRow(`
		SELECT COALESCE(SUM(cost_usd), 0)
		FROM token_usage WHERE timestamp >= ? AND timestamp < ?
	`, formatQueryTime(from), formatQueryTime(to)).Scan(&cost)
	return cost, err
}

func (s *DB) GetTodayCost() (float64, error) {
	t := startOfToday()
	return s.GetCostSince(&t)
}

// startOfToday returns the start of "today" in the daemon host's local
// time zone. Returned in time.Local so AddDate and Format produce calendar
// dates the user expects; callers that compare against stored timestamps
// already pass them through formatQueryTime which UTC-normalizes.
func startOfToday() time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
}

// ModelCostRow holds cost data for a single model.
type ModelCostRow struct {
	Model        string
	InputTokens  int
	OutputTokens int
	CostUSD      float64
}

// GetModelCostBreakdown returns per-model cost aggregation in a time range.
func (s *DB) GetModelCostBreakdown(from, to time.Time) ([]ModelCostRow, error) {
	rows, err := s.db.Query(`
		SELECT COALESCE(NULLIF(model, ''), 'unknown') as m,
		       SUM(input_tokens),
		       SUM(output_tokens),
		       SUM(cost_usd)
		FROM token_usage
		WHERE timestamp >= ? AND timestamp < ?
		GROUP BY m ORDER BY SUM(cost_usd) DESC
	`, formatQueryTime(from), formatQueryTime(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ModelCostRow
	for rows.Next() {
		var r ModelCostRow
		if err := rows.Scan(&r.Model, &r.InputTokens, &r.OutputTokens, &r.CostUSD); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// TopSessionRow holds session cost data for ranking.
type TopSessionRow struct {
	SessionID    string
	Platform     string
	GitBranch    string
	CWD          string
	CostUSD      float64
	InputTokens  int
	OutputTokens int
}

// GetTopSessionsByCost returns the top sessions by cost in a time range.
func (s *DB) GetTopSessionsByCost(from, to time.Time, limit int) ([]TopSessionRow, error) {
	rows, err := s.db.Query(`
		SELECT t.session_id, s.platform, s.git_branch, s.cwd,
		       SUM(t.cost_usd), SUM(t.input_tokens), SUM(t.output_tokens)
		FROM token_usage t
		JOIN sessions s ON t.session_id = s.session_id
		WHERE t.timestamp >= ? AND t.timestamp < ?
		GROUP BY t.session_id
		ORDER BY SUM(t.cost_usd) DESC
		LIMIT ?
	`, formatQueryTime(from), formatQueryTime(to), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []TopSessionRow
	for rows.Next() {
		var r TopSessionRow
		if err := rows.Scan(&r.SessionID, &r.Platform, &r.GitBranch, &r.CWD,
			&r.CostUSD, &r.InputTokens, &r.OutputTokens); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// GetDailyCostsBetween returns per-day cost totals between from and to.
// Results are ordered oldest-first, with zero-filled gaps.
//
// Bucketing is in the daemon-host local time zone so a UTC+8 user's
// "Today" matches calendar-local midnight (not UTC midnight). The stored
// timestamps remain UTC strings; SQL converts at query time via SQLite's
// 'localtime' modifier.
func (s *DB) GetDailyCostsBetween(from, to time.Time) ([]DailyCost, error) {
	rows, err := s.db.Query(`
		SELECT DATE(timestamp, 'localtime') as day, SUM(cost_usd) as cost
		FROM token_usage
		WHERE timestamp >= ? AND timestamp < ?
		GROUP BY day ORDER BY day ASC
	`, formatQueryTime(from.UTC()), formatQueryTime(to.UTC()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	costMap := make(map[string]float64)
	for rows.Next() {
		var day string
		var cost float64
		if err := rows.Scan(&day, &cost); err != nil {
			return nil, err
		}
		costMap[day] = cost
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Zero-fill in local time so the iteration keys align with the SQL
	// 'localtime' DATE() output.
	fromLocal := from.In(time.Local)
	toLocal := to.In(time.Local)
	startDay := time.Date(fromLocal.Year(), fromLocal.Month(), fromLocal.Day(), 0, 0, 0, 0, time.Local)
	endDay := time.Date(toLocal.Year(), toLocal.Month(), toLocal.Day(), 0, 0, 0, 0, time.Local)

	// Iterate inclusive of endDay so the partial current day (where `to` is
	// "now" before midnight) still appears in the chart with whatever cost
	// has accumulated so far. Otherwise users see an empty bar for today
	// when their query bounds are [past, now] within a single local day.
	var result []DailyCost
	for d := startDay; !d.After(endDay); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		result = append(result, DailyCost{Date: key, Cost: costMap[key]})
	}
	return result, nil
}

// AllToolStats returns aggregated tool stats across all sessions in a time range.
func (s *DB) AllToolStats(from, to time.Time) ([]ToolStatRow, error) {
	rows, err := s.db.Query(`
		SELECT tool_name,
		       COUNT(*) as cnt,
		       CAST(COALESCE(AVG(CASE WHEN duration_ms > 0 THEN duration_ms END), 0) AS INTEGER) as avg_ms,
		       SUM(CASE WHEN status IN ('fail','error') THEN 1 ELSE 0 END) as fail_cnt
		FROM tool_calls
		WHERE start_time >= ? AND start_time < ?
		GROUP BY tool_name ORDER BY cnt DESC
	`, formatQueryTime(from), formatQueryTime(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ToolStatRow
	for rows.Next() {
		var r ToolStatRow
		if err := rows.Scan(&r.ToolName, &r.Count, &r.AvgMs, &r.FailCount); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// DailyCost holds cost data for a single day.
type DailyCost struct {
	Date string // YYYY-MM-DD
	Cost float64
}

// GetFirstTokenDate returns the earliest date in token_usage, truncated to
// the local calendar day. Used by Web "all-time" range to anchor the chart
// at the user's first calendar day, matching the local-bucketed aggregations.
// Returns zero time if the table is empty.
func (s *DB) GetFirstTokenDate() (time.Time, error) {
	var ts *string
	if err := s.db.QueryRow("SELECT MIN(timestamp) FROM token_usage").Scan(&ts); err != nil {
		return time.Time{}, err
	}
	if ts == nil || *ts == "" {
		return time.Time{}, nil
	}
	t := parseTime(*ts).In(time.Local)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local), nil
}

// GetDailyCosts returns per-day cost totals for the last N days (including today).
// Results are ordered oldest-first. Buckets use the daemon host's local time.
func (s *DB) GetDailyCosts(days int) ([]DailyCost, error) {
	since := startOfToday().AddDate(0, 0, -(days - 1))
	rows, err := s.db.Query(`
		SELECT DATE(timestamp, 'localtime') as day, SUM(cost_usd) as cost
		FROM token_usage
		WHERE timestamp >= ?
		GROUP BY day ORDER BY day ASC
	`, formatQueryTime(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	costMap := make(map[string]float64)
	for rows.Next() {
		var day string
		var cost float64
		if err := rows.Scan(&day, &cost); err != nil {
			return nil, err
		}
		costMap[day] = cost
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Fill in all days (including zeros) so the sparkline has no gaps.
	result := make([]DailyCost, days)
	for i := 0; i < days; i++ {
		d := since.AddDate(0, 0, i)
		key := d.Format("2006-01-02")
		result[i] = DailyCost{Date: key, Cost: costMap[key]}
	}
	return result, nil
}
