package web

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"time"

	"github.com/tt-a1i/agmon/internal/collector"
	"github.com/tt-a1i/agmon/internal/event"
	"github.com/tt-a1i/agmon/internal/storage"
)

//go:embed static
var staticFiles embed.FS

// Server serves the agmon web dashboard.
type Server struct {
	db   *storage.DB
	addr string
}

func NewServer(db *storage.DB, port string) *Server {
	return &Server{db: db, addr: "127.0.0.1:" + port}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()

	// Static files
	staticFS, _ := fs.Sub(staticFiles, "static")
	mux.Handle("/", http.FileServer(http.FS(staticFS)))

	// API endpoints
	mux.HandleFunc("/api/sessions", s.handleSessions)
	mux.HandleFunc("/api/costs", s.handleCosts)
	mux.HandleFunc("/api/stats", s.handleStats)
	mux.HandleFunc("/api/session/", s.handleSessionDetail)

	return http.ListenAndServe(s.addr, mux)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

type sessionJSON struct {
	SessionID    string  `json:"session_id"`
	Platform     string  `json:"platform"`
	StartTime    string  `json:"start_time"`
	EndTime      string  `json:"end_time,omitempty"`
	Status       string  `json:"status"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
	GitBranch    string  `json:"git_branch,omitempty"`
	CWD          string  `json:"cwd,omitempty"`
	Model        string  `json:"model,omitempty"`
	Tag          string  `json:"tag,omitempty"`
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.db.ListSessions()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	result := make([]sessionJSON, 0, len(sessions))
	for _, sess := range sessions {
		sj := sessionJSON{
			SessionID:    sess.SessionID,
			Platform:     sess.Platform,
			StartTime:    sess.StartTime.Format(time.RFC3339),
			Status:       sess.Status,
			InputTokens:  sess.TotalInputTokens,
			OutputTokens: sess.TotalOutputTokens,
			CostUSD:      sess.TotalCostUSD,
			GitBranch:    sess.GitBranch,
			CWD:          sess.CWD,
			Model:        sess.Model,
			Tag:          sess.Tag,
		}
		if sess.EndTime != nil {
			sj.EndTime = sess.EndTime.Format(time.RFC3339)
		}
		result = append(result, sj)
	}
	writeJSON(w, result)
}

type costResponse struct {
	Range      string                 `json:"range"`
	TotalCost  float64                `json:"total_cost"`
	PrevCost   float64                `json:"prev_cost"`
	DailyCosts []storage.DailyCost    `json:"daily_costs"`
	Models     []storage.ModelCostRow `json:"models"`
}

func (s *Server) handleCosts(w http.ResponseWriter, r *http.Request) {
	rangeParam := r.URL.Query().Get("range")
	if rangeParam == "" {
		rangeParam = "week"
	}

	now := time.Now().UTC()
	var from time.Time

	switch rangeParam {
	case "today":
		from = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	case "week":
		wd := now.Weekday()
		if wd == 0 {
			wd = 7
		}
		from = time.Date(now.Year(), now.Month(), now.Day()-int(wd-1), 0, 0, 0, 0, time.UTC)
	case "month":
		from = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	case "all":
		firstDate, err := s.db.GetFirstTokenDate()
		if err != nil || firstDate.IsZero() {
			from = time.Now().UTC().AddDate(0, 0, -29)
		} else {
			from = firstDate
		}
	default:
		from = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -6)
	}

	totalCost, _ := s.db.GetCostBetween(from, now)
	dailyCosts, _ := s.db.GetDailyCostsBetween(from, now)
	models, _ := s.db.GetModelCostBreakdown(from, now)

	// Previous period for comparison
	duration := now.Sub(from)
	prevFrom := from.Add(-duration)
	prevCost, _ := s.db.GetCostBetween(prevFrom, from)

	writeJSON(w, costResponse{
		Range:      rangeParam,
		TotalCost:  totalCost,
		PrevCost:   prevCost,
		DailyCosts: dailyCosts,
		Models:     models,
	})
}

type statsResponse struct {
	TotalSessions int                     `json:"total_sessions"`
	ActiveCount   int                     `json:"active_count"`
	TodayCost     float64                 `json:"today_cost"`
	WeekCost      float64                 `json:"week_cost"`
	DailyCosts    []storage.DailyCost     `json:"daily_costs"`
	TopTools      []storage.ToolStatRow   `json:"top_tools"`
	TopSessions   []storage.TopSessionRow `json:"top_sessions"`
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	wd := now.Weekday()
	if wd == 0 {
		wd = 7
	}
	startOfWeek := time.Date(now.Year(), now.Month(), now.Day()-int(wd-1), 0, 0, 0, 0, time.UTC)

	sessions, _ := s.db.ListSessions()
	activeCount := 0
	for _, sess := range sessions {
		if sess.Status == "active" {
			activeCount++
		}
	}

	todayCost, _ := s.db.GetTodayCost()
	weekCost, _ := s.db.GetCostBetween(startOfWeek, now)
	dailyCosts, _ := s.db.GetDailyCosts(7)
	topTools, _ := s.db.AllToolStats(startOfWeek, now)
	topSessions, _ := s.db.GetTopSessionsByCost(startOfWeek, now, 5)

	// Limit tools to top 10
	if len(topTools) > 10 {
		topTools = topTools[:10]
	}

	writeJSON(w, statsResponse{
		TotalSessions: len(sessions),
		ActiveCount:   activeCount,
		TodayCost:     todayCost,
		WeekCost:      weekCost,
		DailyCosts:    dailyCosts,
		TopTools:      topTools,
		TopSessions:   topSessions,
	})
}

// handleSessionDetail serves /api/session/{id} with tools, agents, files for a session.
func (s *Server) handleSessionDetail(w http.ResponseWriter, r *http.Request) {
	// Extract session ID from path: /api/session/{id}
	path := r.URL.Path
	prefix := "/api/session/"
	if len(path) <= len(prefix) {
		http.Error(w, "missing session id", 400)
		return
	}
	idPrefix := path[len(prefix):]

	sess, found, err := s.db.GetSessionByIDPrefix(idPrefix)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if !found {
		http.Error(w, "session not found", 404)
		return
	}

	tools, _ := s.db.ListToolCalls(sess.SessionID, 200)
	agents, _ := s.db.ListAgents(sess.SessionID)
	files, _ := s.db.ListFileChanges(sess.SessionID)
	toolStats, _ := s.db.ListToolStats(sess.SessionID)
	agentStats, _ := s.db.ListAgentStats(sess.SessionID)
	messages := collector.ReadUserMessages(event.Platform(sess.Platform), sess.SessionID, sess.CWD, 100)

	type msgJSON struct {
		Time    string `json:"time"`
		Content string `json:"content"`
	}
	type toolJSON struct {
		CallID     string `json:"call_id"`
		ToolName   string `json:"tool_name"`
		Params     string `json:"params"`
		Result     string `json:"result"`
		StartTime  string `json:"start_time"`
		DurationMs int64  `json:"duration_ms"`
		Status     string `json:"status"`
	}
	type agentJSON struct {
		AgentID  string `json:"agent_id"`
		ParentID string `json:"parent_id,omitempty"`
		Role     string `json:"role"`
		Status   string `json:"status"`
	}
	type fileJSON struct {
		Path       string `json:"path"`
		ChangeType string `json:"change_type"`
		Time       string `json:"time"`
	}
	type toolStatJSON struct {
		Name      string `json:"name"`
		Count     int    `json:"count"`
		AvgMs     int64  `json:"avg_ms"`
		FailCount int    `json:"fail_count"`
	}
	type agentStatJSON struct {
		AgentID   string  `json:"agent_id"`
		ParentID  string  `json:"parent_id,omitempty"`
		Role      string  `json:"role"`
		Status    string  `json:"status"`
		ToolCalls int     `json:"tool_calls"`
		InTokens  int     `json:"input_tokens"`
		OutTokens int     `json:"output_tokens"`
		Cost      float64 `json:"cost_usd"`
	}

	mj := make([]msgJSON, 0, len(messages))
	for _, m := range messages {
		mj = append(mj, msgJSON{Time: m.Timestamp.Format(time.RFC3339), Content: m.Content})
	}

	tj := make([]toolJSON, 0, len(tools))
	for _, t := range tools {
		tj = append(tj, toolJSON{
			CallID: t.CallID, ToolName: t.ToolName,
			Params: t.ParamsSummary, Result: t.ResultSummary,
			StartTime: t.StartTime.Format(time.RFC3339), DurationMs: t.DurationMs, Status: t.Status,
		})
	}
	aj := make([]agentJSON, 0, len(agents))
	for _, a := range agents {
		aj = append(aj, agentJSON{AgentID: a.AgentID, ParentID: a.ParentAgentID, Role: a.Role, Status: a.Status})
	}
	fj := make([]fileJSON, 0, len(files))
	for _, f := range files {
		fj = append(fj, fileJSON{Path: f.FilePath, ChangeType: f.ChangeType, Time: f.Timestamp.Format(time.RFC3339)})
	}
	tsj := make([]toolStatJSON, 0, len(toolStats))
	for _, ts := range toolStats {
		tsj = append(tsj, toolStatJSON{Name: ts.ToolName, Count: ts.Count, AvgMs: ts.AvgMs, FailCount: ts.FailCount})
	}
	asj := make([]agentStatJSON, 0, len(agentStats))
	for _, as := range agentStats {
		asj = append(asj, agentStatJSON{
			AgentID: as.AgentID, ParentID: as.ParentAgentID, Role: as.Role,
			Status: as.Status, ToolCalls: as.ToolCallCount,
			InTokens: as.InputTokens, OutTokens: as.OutputTokens, Cost: as.CostUSD,
		})
	}

	sj := sessionJSON{
		SessionID: sess.SessionID, Platform: sess.Platform,
		StartTime: sess.StartTime.Format(time.RFC3339), Status: sess.Status,
		InputTokens: sess.TotalInputTokens, OutputTokens: sess.TotalOutputTokens,
		CostUSD: sess.TotalCostUSD, GitBranch: sess.GitBranch, CWD: sess.CWD,
		Model: sess.Model, Tag: sess.Tag,
	}
	if sess.EndTime != nil {
		sj.EndTime = sess.EndTime.Format(time.RFC3339)
	}

	writeJSON(w, map[string]any{
		"session":     sj,
		"messages":    mj,
		"tools":       tj,
		"agents":      aj,
		"files":       fj,
		"tool_stats":  tsj,
		"agent_stats": asj,
	})
}
