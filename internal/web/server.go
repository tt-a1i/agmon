package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/collector"
	"github.com/tt-a1i/tokenmeter/internal/daemon"
	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/storage"
)

//go:embed static
var staticFiles embed.FS

// Server serves the tokenmeter web dashboard.
type Server struct {
	db              *storage.DB
	addr            string
	eventSockPath   string
	eventHeartbeat  time.Duration
	subscribeRemote func(string) (<-chan event.Event, func(), error)
	srv             *http.Server // built in NewServer so Shutdown is race-free vs Start
}

type ServerOption func(*Server)

func WithEventSocketPath(sockPath string) ServerOption {
	return func(s *Server) {
		s.eventSockPath = sockPath
	}
}

func NewServer(db *storage.DB, port string, opts ...ServerOption) *Server {
	s := &Server{
		db:              db,
		addr:            "127.0.0.1:" + port,
		eventHeartbeat:  30 * time.Second,
		subscribeRemote: daemon.SubscribeRemote,
	}
	for _, opt := range opts {
		opt(s)
	}

	mux := http.NewServeMux()
	staticFS, _ := fs.Sub(staticFiles, "static")
	mux.Handle("/", http.FileServer(http.FS(staticFS)))
	mux.HandleFunc("/api/sessions", s.handleSessions)
	mux.HandleFunc("/api/costs", s.handleCosts)
	mux.HandleFunc("/api/stats", s.handleStats)
	mux.HandleFunc("/api/events", s.handleEvents)
	mux.HandleFunc("/api/session/", s.handleSessionDetail)

	s.srv = &http.Server{
		Addr:              s.addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return s
}

// Start binds and runs the HTTP server. It blocks until the server stops,
// returning nil on graceful shutdown and the underlying error otherwise.
// Pair with Shutdown(ctx) for graceful termination — see runWeb in cmd/.
// Read/Write timeouts cap slow clients; Idle timeout limits keep-alive hogs.
func (s *Server) Start() error {
	if err := s.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown gracefully stops a Start()ed server, waiting up to ctx's deadline
// for in-flight requests to complete. Safe to call before Start: it issues
// Shutdown against a never-listening server, which returns nil immediately.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeAPIError(w http.ResponseWriter, status int, publicMessage string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": publicMessage})
}

func writeInternalError(w http.ResponseWriter, err error) {
	log.Printf("web api error: %v", err)
	writeAPIError(w, http.StatusInternalServerError, "internal server error")
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.eventSockPath == "" || s.subscribeRemote == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "event stream unavailable")
		return
	}

	eventCh, closeFn, err := s.subscribeRemote(s.eventSockPath)
	if err != nil {
		log.Printf("web events subscribe: %v", err)
		writeAPIError(w, http.StatusServiceUnavailable, "event stream unavailable")
		return
	}
	defer closeFn()

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	// This endpoint is intentionally long-lived; disable the server write
	// deadline inherited from the dashboard's normal request timeout.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})

	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")
	header.Set("X-Accel-Buffering", "no")

	sendHeartbeat := func() bool {
		if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	sendEvent := func(ev event.Event) bool {
		payload, err := json.Marshal(ev)
		if err != nil {
			log.Printf("web events marshal: %v", err)
			return true
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	if !sendHeartbeat() {
		return
	}

	ticker := time.NewTicker(s.eventHeartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-eventCh:
			if !ok {
				return
			}
			if !sendEvent(ev) {
				return
			}
		case <-ticker.C:
			if !sendHeartbeat() {
				return
			}
		}
	}
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
	// Optional ?limit=N — capped at 1000 to keep a single response bounded.
	limit := storage.DefaultSessionListLimit
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 {
			if n > 1000 {
				n = 1000
			}
			limit = n
		}
	}
	sessions, err := s.db.ListSessionsLimit(limit)
	if err != nil {
		writeInternalError(w, err)
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

	// Use local time for bucket boundaries — SQL aggregates with
	// DATE(timestamp, 'localtime'), so the from/to range must match the
	// local calendar day or UTC+8 users miss their local-today early hours.
	now := time.Now()
	var from time.Time

	switch rangeParam {
	case "today":
		from = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	case "week":
		wd := now.Weekday()
		if wd == 0 {
			wd = 7
		}
		from = time.Date(now.Year(), now.Month(), now.Day()-int(wd-1), 0, 0, 0, 0, time.Local)
	case "month":
		from = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	case "all":
		firstDate, err := s.db.GetFirstTokenDate()
		if err != nil {
			writeInternalError(w, err)
			return
		}
		if firstDate.IsZero() {
			from = now.AddDate(0, 0, -29)
		} else {
			from = firstDate
		}
	default:
		from = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local).AddDate(0, 0, -6)
	}

	totalCost, err := s.db.GetCostBetween(from, now)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	dailyCosts, err := s.db.GetDailyCostsBetween(from, now)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	models, err := s.db.GetModelCostBreakdown(from, now)
	if err != nil {
		writeInternalError(w, err)
		return
	}

	// Previous period for comparison
	duration := now.Sub(from)
	prevFrom := from.Add(-duration)
	prevCost, err := s.db.GetCostBetween(prevFrom, from)
	if err != nil {
		writeInternalError(w, err)
		return
	}

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
	// Local time so the week boundary aligns with the user's calendar — see
	// handleCosts for the same rationale.
	now := time.Now()
	wd := now.Weekday()
	if wd == 0 {
		wd = 7
	}
	startOfWeek := time.Date(now.Year(), now.Month(), now.Day()-int(wd-1), 0, 0, 0, 0, time.Local)

	// Active session count is a single COUNT(*) — no need to materialize all
	// sessions just to filter and count them in Go.
	activeCount, err := s.db.GetActiveSessionCount()
	if err != nil {
		writeInternalError(w, err)
		return
	}
	totalSessions, err := s.db.GetVisibleSessionCount()
	if err != nil {
		writeInternalError(w, err)
		return
	}

	todayCost, err := s.db.GetTodayCost()
	if err != nil {
		writeInternalError(w, err)
		return
	}
	weekCost, err := s.db.GetCostBetween(startOfWeek, now)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	dailyCosts, err := s.db.GetDailyCosts(7)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	topTools, err := s.db.AllToolStats(startOfWeek, now)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	topSessions, err := s.db.GetTopSessionsByCost(startOfWeek, now, 5)
	if err != nil {
		writeInternalError(w, err)
		return
	}

	// Limit tools to top 10
	if len(topTools) > 10 {
		topTools = topTools[:10]
	}

	writeJSON(w, statsResponse{
		TotalSessions: totalSessions,
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
		writeAPIError(w, http.StatusBadRequest, "missing session id")
		return
	}
	idPrefix := path[len(prefix):]

	sess, found, err := s.db.GetSessionByIDPrefix(idPrefix)
	if err != nil {
		if errors.Is(err, storage.ErrAmbiguousSessionPrefix) {
			// User-input error: safe to surface the message (no SQL/table internals).
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeInternalError(w, err)
		return
	}
	if !found {
		writeAPIError(w, http.StatusNotFound, "session not found")
		return
	}

	tools, err := s.db.ListToolCalls(sess.SessionID, 200)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	agents, err := s.db.ListAgents(sess.SessionID)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	files, err := s.db.ListFileChanges(sess.SessionID)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	toolStats, err := s.db.ListToolStats(sess.SessionID)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	agentStats, err := s.db.ListAgentStats(sess.SessionID)
	if err != nil {
		writeInternalError(w, err)
		return
	}
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
