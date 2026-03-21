package storage

import (
	"time"
)

type SessionRow struct {
	SessionID            string
	Platform             string
	StartTime            time.Time
	EndTime              *time.Time
	Status               string
	TotalInputTokens     int
	TotalOutputTokens    int
	TotalCostUSD         float64
	CWD                  string
	GitBranch            string
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
		WHERE total_input_tokens > 0 OR total_output_tokens > 0
		   OR start_time > datetime('now', '-1 hour')
		ORDER BY start_time DESC LIMIT 50
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

func (s *DB) GetTodayTokens() (input, output int, err error) {
	today := time.Now().UTC().Format("2006-01-02")
	err = s.db.QueryRow(`
		SELECT COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0)
		FROM token_usage WHERE timestamp >= ?
	`, today+"T00:00:00Z").Scan(&input, &output)
	return
}

func (s *DB) GetWeekTokens() (input, output int, err error) {
	weekAgo := time.Now().UTC().AddDate(0, 0, -7).Format("2006-01-02")
	err = s.db.QueryRow(`
		SELECT COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0)
		FROM token_usage WHERE timestamp >= ?
	`, weekAgo+"T00:00:00Z").Scan(&input, &output)
	return
}

func (s *DB) GetMonthTokens() (input, output int, err error) {
	monthAgo := time.Now().UTC().AddDate(0, -1, 0).Format("2006-01-02")
	err = s.db.QueryRow(`
		SELECT COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0)
		FROM token_usage WHERE timestamp >= ?
	`, monthAgo+"T00:00:00Z").Scan(&input, &output)
	return
}

func (s *DB) GetAllTokens() (input, output int, err error) {
	err = s.db.QueryRow(`
		SELECT COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0)
		FROM token_usage
	`).Scan(&input, &output)
	return
}

func (s *DB) GetActiveSessionCount() (int, error) {
	cutoff := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	var count int
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM sessions
		WHERE status = 'active' AND start_time > ?
	`, cutoff).Scan(&count)
	return count, err
}

func (s *DB) GetAgentTokenSummary(agentID string) (inputTokens, outputTokens int, costUSD float64, err error) {
	err = s.db.QueryRow(`
		SELECT COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0), COALESCE(SUM(cost_usd), 0)
		FROM token_usage WHERE agent_id = ?
	`, agentID).Scan(&inputTokens, &outputTokens, &costUSD)
	return
}

func (s *DB) GetTodayCost() (float64, error) {
	today := time.Now().UTC().Format("2006-01-02")
	var cost float64
	err := s.db.QueryRow(`
		SELECT COALESCE(SUM(cost_usd), 0)
		FROM token_usage WHERE timestamp >= ?
	`, today+"T00:00:00Z").Scan(&cost)
	return cost, err
}

func (s *DB) GetWeekCost() (float64, error) {
	weekAgo := time.Now().UTC().AddDate(0, 0, -7).Format("2006-01-02")
	var cost float64
	err := s.db.QueryRow(`
		SELECT COALESCE(SUM(cost_usd), 0)
		FROM token_usage WHERE timestamp >= ?
	`, weekAgo+"T00:00:00Z").Scan(&cost)
	return cost, err
}

func (s *DB) GetMonthCost() (float64, error) {
	monthAgo := time.Now().UTC().AddDate(0, -1, 0).Format("2006-01-02")
	var cost float64
	err := s.db.QueryRow(`
		SELECT COALESCE(SUM(cost_usd), 0)
		FROM token_usage WHERE timestamp >= ?
	`, monthAgo+"T00:00:00Z").Scan(&cost)
	return cost, err
}

func (s *DB) GetAllCost() (float64, error) {
	var cost float64
	err := s.db.QueryRow(`
		SELECT COALESCE(SUM(cost_usd), 0)
		FROM token_usage
	`).Scan(&cost)
	return cost, err
}

