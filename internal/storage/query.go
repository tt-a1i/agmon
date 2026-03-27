package storage

import (
	"database/sql"
	"fmt"
	"time"
)

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
	ToolName string
	Count    int
	AvgMs    int64
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
	t, _ := time.Parse(time.RFC3339, s)
	return t.Local()
}

func parseTimePtr(s *string) *time.Time {
	if s == nil {
		return nil
	}
	t := parseTime(*s)
	return &t
}

func (s *DB) ListSessions() ([]SessionRow, error) {
	rows, err := s.db.Query(`
		SELECT session_id, platform, start_time, end_time, status,
		       total_input_tokens, total_output_tokens, total_cost_usd,
		       cwd, git_branch, latest_context_tokens, model,
		       total_cache_read_tokens, total_cache_creation_tokens
		FROM sessions
		WHERE status = 'active'
		   OR total_input_tokens > 0 OR total_output_tokens > 0
		   OR total_cache_read_tokens > 0 OR total_cache_creation_tokens > 0
		ORDER BY start_time DESC LIMIT 200
	`)
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
			&r.TotalCacheReadTokens, &r.TotalCacheCreationTokens); err != nil {
			return nil, err
		}
		r.StartTime = parseTime(startStr)
		r.EndTime = parseTimePtr(endStr)
		result = append(result, r)
	}
	return result, rows.Err()
}

// GetSessionByIDPrefix looks up a session by exact ID or unique prefix, searching
// all sessions (not just the filtered list returned by ListSessions).
// Returns an error if the prefix matches more than one session.
func (s *DB) GetSessionByIDPrefix(prefix string) (SessionRow, bool, error) {
	// Check for ambiguity before returning a result.
	var count int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM sessions WHERE session_id LIKE ? || '%'`, prefix,
	).Scan(&count); err != nil {
		return SessionRow{}, false, err
	}
	if count > 1 {
		return SessionRow{}, false, fmt.Errorf("ambiguous prefix %q matches %d sessions; use more characters", prefix, count)
	}

	row := s.db.QueryRow(`
		SELECT session_id, platform, start_time, end_time, status,
		       total_input_tokens, total_output_tokens, total_cost_usd,
		       cwd, git_branch, latest_context_tokens, model,
		       total_cache_read_tokens, total_cache_creation_tokens
		FROM sessions WHERE session_id LIKE ? || '%' LIMIT 1
	`, prefix)
	var r SessionRow
	var startStr string
	var endStr *string
	err := row.Scan(&r.SessionID, &r.Platform, &startStr, &endStr,
		&r.Status, &r.TotalInputTokens, &r.TotalOutputTokens, &r.TotalCostUSD,
		&r.CWD, &r.GitBranch, &r.LatestContextTokens, &r.Model,
		&r.TotalCacheReadTokens, &r.TotalCacheCreationTokens)
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
	rows, err := s.db.Query(`
		SELECT a.agent_id, COALESCE(a.parent_agent_id, ''), COALESCE(a.role, ''),
		       a.status,
		       COALESCE(tc.cnt, 0),
		       COALESCE(t.in_tok, 0), COALESCE(t.out_tok, 0), COALESCE(t.cost, 0)
		FROM agents a
		LEFT JOIN (
			SELECT agent_id, COUNT(*) as cnt
			FROM tool_calls WHERE session_id = ? GROUP BY agent_id
		) tc ON a.agent_id = tc.agent_id
		LEFT JOIN (
			SELECT agent_id,
			       SUM(input_tokens + cache_creation_tokens + cache_read_tokens) as in_tok,
			       SUM(output_tokens) as out_tok,
			       SUM(cost_usd) as cost
			FROM token_usage WHERE session_id = ? AND agent_id != '' GROUP BY agent_id
		) t ON a.agent_id = t.agent_id
		WHERE a.session_id = ?
		ORDER BY a.start_time ASC
	`, sessionID, sessionID, sessionID)
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
	return result, rows.Err()
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
		`, since.Format(time.RFC3339)).Scan(&input, &output)
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
		`, since.Format(time.RFC3339)).Scan(&cost)
	}
	return cost, err
}

func (s *DB) GetTodayCost() (float64, error) {
	t := startOfToday()
	return s.GetCostSince(&t)
}

func startOfToday() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}
