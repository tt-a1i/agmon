package storage

import (
	"path/filepath"
	"time"
)

type AnalysisResult struct {
	Range      string          `json:"range"`
	From       time.Time       `json:"from"`
	To         time.Time       `json:"to"`
	Cost       CostSummary     `json:"cost"`
	Sessions   SessionSummary  `json:"sessions"`
	Models     []ModelStat     `json:"models"`
	Tools      []ToolStat      `json:"tools"`
	FilesByExt map[string]int  `json:"files_by_ext"`
	TopFiles   []FileEditCount `json:"top_files"`
	Heatmap    [7][24]int      `json:"heatmap"`
}

type CostSummary struct {
	Total          float64 `json:"total"`
	AveragePerDay  float64 `json:"average_per_day"`
	HighestDay     string  `json:"highest_day"`
	HighestDayCost float64 `json:"highest_day_cost"`
	ActiveDays     int     `json:"active_days"`
	Days           int     `json:"days"`
}

type SessionSummary struct {
	Total         int            `json:"total"`
	Active        int            `json:"active"`
	ByPlatform    map[string]int `json:"by_platform"`
	AveragePerDay float64        `json:"average_per_day"`
	AverageCost   float64        `json:"average_cost"`
	MostExpensive *SessionCost   `json:"most_expensive,omitempty"`
}

type SessionCost struct {
	SessionID string  `json:"session_id"`
	Name      string  `json:"name"`
	Platform  string  `json:"platform"`
	CostUSD   float64 `json:"cost_usd"`
}

type ModelStat struct {
	Model   string  `json:"model"`
	CostUSD float64 `json:"cost_usd"`
	Percent float64 `json:"percent"`
}

type ToolStat struct {
	Name        string  `json:"name"`
	Count       int     `json:"count"`
	AvgMs       int64   `json:"avg_ms"`
	FailCount   int     `json:"fail_count"`
	FailPercent float64 `json:"fail_percent"`
}

type FileEditCount struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}

func (s *DB) Analyze(from, to time.Time) (*AnalysisResult, error) {
	result := &AnalysisResult{
		Range:      analysisRangeLabel(from, to),
		From:       from,
		To:         to,
		FilesByExt: make(map[string]int),
	}
	days := analysisDays(from, to)
	result.Cost.Days = days
	result.Sessions.ByPlatform = make(map[string]int)

	totalCost, err := s.GetCostBetween(from, to)
	if err != nil {
		return nil, err
	}
	result.Cost.Total = totalCost
	if days > 0 {
		result.Cost.AveragePerDay = totalCost / float64(days)
	}

	dailyCosts, err := s.GetDailyCostsBetween(from, to)
	if err != nil {
		return nil, err
	}
	for _, day := range dailyCosts {
		if day.Cost > 0 {
			result.Cost.ActiveDays++
		}
		if day.Cost > result.Cost.HighestDayCost {
			result.Cost.HighestDayCost = day.Cost
			result.Cost.HighestDay = day.Date
		}
	}

	if err := s.fillAnalysisSessions(result, from, to, days, totalCost); err != nil {
		return nil, err
	}
	if err := s.fillAnalysisModels(result, from, to, totalCost); err != nil {
		return nil, err
	}
	if err := s.fillAnalysisTools(result, from, to); err != nil {
		return nil, err
	}
	if err := s.fillAnalysisFiles(result, from, to); err != nil {
		return nil, err
	}
	if err := s.fillAnalysisHeatmap(result, from, to); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *DB) fillAnalysisSessions(result *AnalysisResult, from, to time.Time, days int, totalCost float64) error {
	rows, err := s.db.Query(`
		SELECT session_id, platform, status
		FROM sessions
		WHERE start_time >= ? AND start_time < ?
	`, formatQueryTime(from), formatQueryTime(to))
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var sessionID, platform, status string
		if err := rows.Scan(&sessionID, &platform, &status); err != nil {
			return err
		}
		result.Sessions.Total++
		if status == "active" {
			result.Sessions.Active++
		}
		result.Sessions.ByPlatform[platform]++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if days > 0 {
		result.Sessions.AveragePerDay = float64(result.Sessions.Total) / float64(days)
	}
	if result.Sessions.Total > 0 {
		result.Sessions.AverageCost = totalCost / float64(result.Sessions.Total)
	}

	top, err := s.GetTopSessionsByCost(from, to, 1)
	if err != nil {
		return err
	}
	if len(top) > 0 {
		result.Sessions.MostExpensive = &SessionCost{
			SessionID: top[0].SessionID,
			Name:      analysisSessionName(top[0]),
			Platform:  top[0].Platform,
			CostUSD:   top[0].CostUSD,
		}
	}
	return nil
}

func (s *DB) fillAnalysisModels(result *AnalysisResult, from, to time.Time, totalCost float64) error {
	models, err := s.GetModelCostBreakdown(from, to)
	if err != nil {
		return err
	}
	result.Models = make([]ModelStat, 0, len(models))
	for _, model := range models {
		stat := ModelStat{Model: model.Model, CostUSD: model.CostUSD}
		if totalCost > 0 {
			stat.Percent = model.CostUSD / totalCost * 100
		}
		result.Models = append(result.Models, stat)
	}
	return nil
}

func (s *DB) fillAnalysisTools(result *AnalysisResult, from, to time.Time) error {
	stats, err := s.AllToolStats(from, to)
	if err != nil {
		return err
	}
	result.Tools = make([]ToolStat, 0, len(stats))
	for _, stat := range stats {
		tool := ToolStat{
			Name:      stat.ToolName,
			Count:     stat.Count,
			AvgMs:     stat.AvgMs,
			FailCount: stat.FailCount,
		}
		if stat.Count > 0 {
			tool.FailPercent = float64(stat.FailCount) / float64(stat.Count) * 100
		}
		result.Tools = append(result.Tools, tool)
	}
	return nil
}

func (s *DB) fillAnalysisFiles(result *AnalysisResult, from, to time.Time) error {
	rows, err := s.db.Query(`
		SELECT file_path, COUNT(*) AS cnt
		FROM file_changes
		WHERE timestamp >= ? AND timestamp < ?
		GROUP BY file_path
		ORDER BY cnt DESC, file_path ASC
	`, formatQueryTime(from), formatQueryTime(to))
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var path string
		var count int
		if err := rows.Scan(&path, &count); err != nil {
			return err
		}
		ext := filepath.Ext(path)
		if ext == "" {
			ext = "other"
		}
		result.FilesByExt[ext]++
		result.TopFiles = append(result.TopFiles, FileEditCount{Path: path, Count: count})
	}
	return rows.Err()
}

func (s *DB) fillAnalysisHeatmap(result *AnalysisResult, from, to time.Time) error {
	rows, err := s.db.Query(`
		SELECT CAST(strftime('%w', datetime(timestamp, '+8 hours')) AS INTEGER) AS weekday,
		       CAST(strftime('%H', datetime(timestamp, '+8 hours')) AS INTEGER) AS hour,
		       COUNT(*) AS cnt
		FROM (
			SELECT timestamp FROM token_usage WHERE timestamp >= ? AND timestamp < ?
			UNION ALL
			SELECT start_time AS timestamp FROM tool_calls WHERE start_time >= ? AND start_time < ?
			UNION ALL
			SELECT timestamp FROM file_changes WHERE timestamp >= ? AND timestamp < ?
		)
		GROUP BY weekday, hour
	`, formatQueryTime(from), formatQueryTime(to), formatQueryTime(from), formatQueryTime(to), formatQueryTime(from), formatQueryTime(to))
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var sqliteWeekday, hour, count int
		if err := rows.Scan(&sqliteWeekday, &hour, &count); err != nil {
			return err
		}
		if hour < 0 || hour > 23 {
			continue
		}
		weekday := (sqliteWeekday + 6) % 7
		if weekday >= 0 && weekday < 7 {
			result.Heatmap[weekday][hour] = count
		}
	}
	return rows.Err()
}

func analysisDays(from, to time.Time) int {
	if !to.After(from) {
		return 0
	}
	hours := to.Sub(from).Hours()
	days := int(hours / 24)
	if hours > float64(days*24) {
		days++
	}
	if days < 1 {
		return 1
	}
	return days
}

func analysisRangeLabel(from, to time.Time) string {
	return from.Format("2006-01-02") + " to " + to.Format("2006-01-02")
}

func analysisSessionName(row TopSessionRow) string {
	if row.CWD != "" && row.GitBranch != "" {
		base := filepath.Base(row.CWD)
		if base != "." && base != string(filepath.Separator) {
			return base + "/" + row.GitBranch
		}
		return row.CWD + "/" + row.GitBranch
	}
	if row.GitBranch != "" {
		return row.GitBranch
	}
	if row.CWD != "" {
		base := filepath.Base(row.CWD)
		if base != "." && base != string(filepath.Separator) {
			return base
		}
		return row.CWD
	}
	if len(row.SessionID) > 8 {
		return row.SessionID[:8]
	}
	return row.SessionID
}
