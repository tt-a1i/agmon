package web

import (
	"context"
	"embed"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
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
	buildVersion    string
	eventSockPath   string
	eventHeartbeat  time.Duration
	metricsProvider MetricsProvider
	subscribeRemote func(string) (<-chan event.Event, func(), error)
	srv             *http.Server // built in NewServer so Shutdown is race-free vs Start
}

type ServerOption func(*Server)

type MetricsProvider interface {
	DaemonStats() (droppedBroadcasts, droppedShutdownEvts, duplicateToolStarts int64)
	BudgetUsageAll() ([]BudgetMetric, error)
}

type BudgetMetric struct {
	Name     string
	Platform string
	UsedUSD  float64
	LimitUSD float64
	Percent  float64
}

func WithEventSocketPath(sockPath string) ServerOption {
	return func(s *Server) {
		s.eventSockPath = sockPath
	}
}

func WithMetricsProvider(p MetricsProvider) ServerOption {
	return func(s *Server) {
		s.metricsProvider = p
	}
}

func WithBuildVersion(version string) ServerOption {
	return func(s *Server) {
		s.buildVersion = version
	}
}

func NewServer(db *storage.DB, port string, opts ...ServerOption) *Server {
	s := &Server{
		db:              db,
		addr:            "127.0.0.1:" + port,
		buildVersion:    "dev",
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
	mux.HandleFunc("/api/export", s.handleExport)
	mux.HandleFunc("/api/compare", s.handleCompare)
	mux.HandleFunc("/api/search", s.handleSearch)
	mux.HandleFunc("/api/budgets", s.handleBudgets)
	mux.HandleFunc("/api/budgets/", s.handleBudgetByID)
	mux.HandleFunc("/api/session/", s.handleSessionDetail)
	mux.HandleFunc("/metrics", s.handleMetrics)

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
	platform := r.URL.Query().Get("platform")
	var sessions []storage.SessionRow
	var err error
	if platform != "" {
		if platform != string(event.PlatformClaude) && platform != string(event.PlatformCodex) {
			writeAPIError(w, http.StatusBadRequest, "invalid platform")
			return
		}
		sessions, err = s.db.ListSessionsByPlatform(platform, limit)
	} else {
		sessions, err = s.db.ListSessionsLimit(limit)
	}
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

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "csv"
	}
	if format != "csv" && format != "json" {
		writeAPIError(w, http.StatusBadRequest, "invalid export format")
		return
	}

	rangeLabel, from, to, err := s.exportRange(r.URL.Query().Get("range"))
	if err != nil {
		writeInternalError(w, err)
		return
	}

	filename := fmt.Sprintf("tokenmeter-%s-%s.%s", rangeLabel, to.Format("2006-01-02"), format)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	if format == "json" {
		w.Header().Set("Content-Type", "application/json")
		s.writeExportJSON(w, from, to)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	s.writeExportCSV(w, from, to)
}

func (s *Server) exportRange(rangeParam string) (string, time.Time, time.Time, error) {
	now := time.Now()
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)

	switch rangeParam {
	case "today":
		return "today", startOfToday, now, nil
	case "week":
		wd := now.Weekday()
		if wd == 0 {
			wd = 7
		}
		return "week", time.Date(now.Year(), now.Month(), now.Day()-int(wd-1), 0, 0, 0, 0, time.Local), now, nil
	case "month":
		return "month", time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local), now, nil
	case "all":
		firstDate, err := s.db.GetFirstTokenDate()
		if err != nil {
			return "", time.Time{}, time.Time{}, err
		}
		if firstDate.IsZero() {
			firstDate = startOfToday.AddDate(0, 0, -29)
		}
		return "all", firstDate, now, nil
	default:
		return "7d", startOfToday.AddDate(0, 0, -6), now, nil
	}
}

func (s *Server) writeExportCSV(w http.ResponseWriter, from, to time.Time) {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"date", "session_id", "session_name", "platform", "model", "input_tokens", "output_tokens", "cache_tokens", "cost_usd"}); err != nil {
		log.Printf("web export csv header: %v", err)
		return
	}
	err := s.db.ForEachSessionExportRow(from, to, func(row storage.SessionExportRow) error {
		return cw.Write([]string{
			row.Date,
			row.SessionID,
			row.SessionName,
			row.Platform,
			row.Model,
			strconv.Itoa(row.InputTokens),
			strconv.Itoa(row.OutputTokens),
			strconv.Itoa(row.CacheTokens),
			fmt.Sprintf("%.6f", row.CostUSD),
		})
	})
	cw.Flush()
	if flushErr := cw.Error(); flushErr != nil {
		log.Printf("web export csv flush: %v", flushErr)
	}
	if err != nil {
		log.Printf("web export csv rows: %v", err)
	}
}

func (s *Server) writeExportJSON(w http.ResponseWriter, from, to time.Time) {
	if _, err := fmt.Fprint(w, "["); err != nil {
		return
	}
	first := true
	err := s.db.ForEachSessionExportRow(from, to, func(row storage.SessionExportRow) error {
		payload, err := json.Marshal(row)
		if err != nil {
			return err
		}
		if !first {
			if _, err := fmt.Fprint(w, ","); err != nil {
				return err
			}
		}
		first = false
		_, err = w.Write(payload)
		return err
	})
	if _, closeErr := fmt.Fprint(w, "]"); closeErr != nil {
		log.Printf("web export json close: %v", closeErr)
	}
	if err != nil {
		log.Printf("web export json rows: %v", err)
	}
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

// handleMetrics emits Prometheus text exposition metrics. Metric names use the
// tokenmeter_ prefix, counters end in _total, cost values are USD, token values
// are raw token counts, and budget_percent is a 0-100 percentage.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	totalSessions, err := s.db.GetVisibleSessionCount()
	if err != nil {
		writeInternalError(w, err)
		return
	}
	activeSessions, err := s.db.GetActiveSessionCount()
	if err != nil {
		writeInternalError(w, err)
		return
	}
	todayCost, err := s.db.GetTodayCost()
	if err != nil {
		writeInternalError(w, err)
		return
	}
	todayInput, todayOutput, err := s.db.GetTodayTokens()
	if err != nil {
		writeInternalError(w, err)
		return
	}
	budgets, err := s.metricsBudgets()
	if err != nil {
		writeInternalError(w, err)
		return
	}

	var droppedBroadcasts, droppedShutdown, duplicateToolStarts int64
	if s.metricsProvider != nil {
		droppedBroadcasts, droppedShutdown, duplicateToolStarts = s.metricsProvider.DaemonStats()
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintln(w, "# HELP tokenmeter_build_info Build version")
	fmt.Fprintln(w, "# TYPE tokenmeter_build_info gauge")
	fmt.Fprintf(w, "tokenmeter_build_info{version=\"%s\"} 1\n\n", prometheusLabelValue(s.buildVersion))

	writePromGauge(w, "tokenmeter_sessions_total", "Total sessions in storage", float64(totalSessions))
	writePromGauge(w, "tokenmeter_sessions_active", "Active sessions", float64(activeSessions))
	writePromGauge(w, "tokenmeter_today_cost_usd", "Total cost today (local TZ bucket)", todayCost)
	writePromCounter(w, "tokenmeter_today_tokens_input", "Total input tokens today", float64(todayInput))
	writePromCounter(w, "tokenmeter_today_tokens_output", "Total output tokens today", float64(todayOutput))
	writePromCounter(w, "tokenmeter_daemon_dropped_broadcasts_total", "Slow subscriber drops", float64(droppedBroadcasts))
	writePromCounter(w, "tokenmeter_daemon_dropped_shutdown_total", "Events dropped during shutdown", float64(droppedShutdown))
	writePromCounter(w, "tokenmeter_daemon_duplicate_tool_starts_total", "Duplicate Pre-hook emits", float64(duplicateToolStarts))

	fmt.Fprintln(w, "# HELP tokenmeter_budget_used_usd Budget usage")
	fmt.Fprintln(w, "# TYPE tokenmeter_budget_used_usd gauge")
	for _, budget := range budgets {
		fmt.Fprintf(w, "tokenmeter_budget_used_usd{name=\"%s\",platform=\"%s\"} %g\n",
			prometheusLabelValue(budget.Name), prometheusLabelValue(budget.Platform), budget.UsedUSD)
	}
	fmt.Fprintln(w, "# HELP tokenmeter_budget_limit_usd Budget limit")
	fmt.Fprintln(w, "# TYPE tokenmeter_budget_limit_usd gauge")
	for _, budget := range budgets {
		fmt.Fprintf(w, "tokenmeter_budget_limit_usd{name=\"%s\",platform=\"%s\"} %g\n",
			prometheusLabelValue(budget.Name), prometheusLabelValue(budget.Platform), budget.LimitUSD)
	}
	fmt.Fprintln(w, "# HELP tokenmeter_budget_percent Budget usage percent")
	fmt.Fprintln(w, "# TYPE tokenmeter_budget_percent gauge")
	for _, budget := range budgets {
		fmt.Fprintf(w, "tokenmeter_budget_percent{name=\"%s\",platform=\"%s\"} %g\n",
			prometheusLabelValue(budget.Name), prometheusLabelValue(budget.Platform), budget.Percent)
	}
}

func writePromGauge(w http.ResponseWriter, name, help string, value float64) {
	fmt.Fprintf(w, "# HELP %s %s\n", name, help)
	fmt.Fprintf(w, "# TYPE %s gauge\n", name)
	fmt.Fprintf(w, "%s %g\n\n", name, value)
}

func writePromCounter(w http.ResponseWriter, name, help string, value float64) {
	fmt.Fprintf(w, "# HELP %s %s\n", name, help)
	fmt.Fprintf(w, "# TYPE %s counter\n", name)
	fmt.Fprintf(w, "%s %g\n\n", name, value)
}

func prometheusLabelValue(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return value
}

func (s *Server) metricsBudgets() ([]BudgetMetric, error) {
	if s.metricsProvider != nil {
		return s.metricsProvider.BudgetUsageAll()
	}

	budgets, err := s.db.ListBudgets()
	if err != nil {
		return nil, err
	}
	result := make([]BudgetMetric, 0, len(budgets))
	for _, budget := range budgets {
		used, limit, err := s.db.GetBudgetUsage(budget.ID)
		if err != nil {
			return nil, err
		}
		percent := 0.0
		if limit > 0 {
			percent = used / limit * 100
		}
		result = append(result, BudgetMetric{
			Name:     budget.Name,
			Platform: budget.Platform,
			UsedUSD:  used,
			LimitUSD: limit,
			Percent:  percent,
		})
	}
	return result, nil
}

type budgetRequest struct {
	Name       string  `json:"name"`
	MonthlyUSD float64 `json:"monthly_usd"`
	Platform   string  `json:"platform"`
}

type budgetUsageJSON struct {
	Used    float64 `json:"used"`
	Limit   float64 `json:"limit"`
	Percent float64 `json:"percent"`
	Status  string  `json:"status"`
}

type budgetJSON struct {
	ID         int64           `json:"id"`
	Name       string          `json:"name"`
	MonthlyUSD float64         `json:"monthly_usd"`
	Platform   string          `json:"platform"`
	CreatedAt  string          `json:"created_at"`
	UpdatedAt  string          `json:"updated_at"`
	Usage      budgetUsageJSON `json:"usage"`
}

func (s *Server) handleBudgets(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listBudgets(w)
	case http.MethodPost:
		var req budgetRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		id, err := s.db.InsertBudget(req.Name, req.MonthlyUSD, req.Platform)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		budget, ok, err := s.findBudget(id)
		if err != nil {
			writeInternalError(w, err)
			return
		}
		if !ok {
			writeInternalError(w, fmt.Errorf("created budget %d not found", id))
			return
		}
		s.writeBudgetWithStatus(w, budget, http.StatusCreated)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleBudgetByID(w http.ResponseWriter, r *http.Request) {
	id, ok := parseBudgetID(r.URL.Path)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, "invalid budget id")
		return
	}

	switch r.Method {
	case http.MethodPut:
		var req budgetRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if err := s.db.UpdateBudget(id, req.Name, req.MonthlyUSD, req.Platform); err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		budget, ok, err := s.findBudget(id)
		if err != nil {
			writeInternalError(w, err)
			return
		}
		if !ok {
			writeAPIError(w, http.StatusNotFound, "budget not found")
			return
		}
		s.writeBudgetWithStatus(w, budget, http.StatusOK)
	case http.MethodDelete:
		if err := s.db.DeleteBudget(id); err != nil {
			writeInternalError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "PUT, DELETE")
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func parseBudgetID(path string) (int64, bool) {
	raw := strings.TrimPrefix(path, "/api/budgets/")
	if raw == "" || strings.Contains(raw, "/") {
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	return id, err == nil && id > 0
}

func (s *Server) listBudgets(w http.ResponseWriter) {
	budgets, err := s.db.ListBudgets()
	if err != nil {
		writeInternalError(w, err)
		return
	}
	result := make([]budgetJSON, 0, len(budgets))
	for _, budget := range budgets {
		row, err := s.budgetJSON(budget)
		if err != nil {
			writeInternalError(w, err)
			return
		}
		result = append(result, row)
	}
	writeJSON(w, result)
}

func (s *Server) findBudget(id int64) (storage.BudgetRow, bool, error) {
	budgets, err := s.db.ListBudgets()
	if err != nil {
		return storage.BudgetRow{}, false, err
	}
	for _, budget := range budgets {
		if budget.ID == id {
			return budget, true, nil
		}
	}
	return storage.BudgetRow{}, false, nil
}

func (s *Server) writeBudget(w http.ResponseWriter, budget storage.BudgetRow) {
	s.writeBudgetWithStatus(w, budget, http.StatusOK)
}

func (s *Server) writeBudgetWithStatus(w http.ResponseWriter, budget storage.BudgetRow, status int) {
	row, err := s.budgetJSON(budget)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(row)
}

func (s *Server) budgetJSON(budget storage.BudgetRow) (budgetJSON, error) {
	used, limit, err := s.db.GetBudgetUsage(budget.ID)
	if err != nil {
		return budgetJSON{}, err
	}
	percent := 0.0
	if limit > 0 {
		percent = used / limit * 100
	}
	status := "ok"
	if percent >= 100 {
		status = "over"
	} else if percent >= 80 {
		status = "warn"
	}
	return budgetJSON{
		ID:         budget.ID,
		Name:       budget.Name,
		MonthlyUSD: budget.MonthlyUSD,
		Platform:   budget.Platform,
		CreatedAt:  budget.CreatedAt.Format(time.RFC3339),
		UpdatedAt:  budget.UpdatedAt.Format(time.RFC3339),
		Usage: budgetUsageJSON{
			Used:    used,
			Limit:   limit,
			Percent: percent,
			Status:  status,
		},
	}, nil
}

type compareToolDiff struct {
	Name   string `json:"name"`
	ACount int    `json:"a_count"`
	BCount int    `json:"b_count"`
}

type compareCostDiff struct {
	A     float64 `json:"a"`
	B     float64 `json:"b"`
	Delta float64 `json:"delta"`
}

type compareTokenDiff struct {
	AInput      int `json:"a_input"`
	BInput      int `json:"b_input"`
	DeltaInput  int `json:"delta_input"`
	AOutput     int `json:"a_output"`
	BOutput     int `json:"b_output"`
	DeltaOutput int `json:"delta_output"`
}

type compareFileDiff struct {
	AOnly  []string `json:"a_only"`
	BOnly  []string `json:"b_only"`
	Common []string `json:"common"`
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeAPIError(w, http.StatusBadRequest, "q required")
		return
	}
	if len([]rune(query)) < 2 {
		writeAPIError(w, http.StatusBadRequest, "query too short")
		return
	}

	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeAPIError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		if n > 200 {
			n = 200
		}
		limit = n
	}

	hits, err := s.db.SearchHits(query, limit)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, hits)
}

func (s *Server) handleCompare(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	aPrefix := r.URL.Query().Get("a")
	bPrefix := r.URL.Query().Get("b")
	if aPrefix == "" || bPrefix == "" {
		writeAPIError(w, http.StatusBadRequest, "missing session id")
		return
	}

	a, ok, err := s.db.GetSessionByIDPrefix(aPrefix)
	if err != nil {
		if errors.Is(err, storage.ErrAmbiguousSessionPrefix) {
			writeAPIError(w, http.StatusBadRequest, err.Error())
		} else {
			writeInternalError(w, err)
		}
		return
	}
	if !ok {
		writeAPIError(w, http.StatusNotFound, "session not found")
		return
	}
	b, ok, err := s.db.GetSessionByIDPrefix(bPrefix)
	if err != nil {
		if errors.Is(err, storage.ErrAmbiguousSessionPrefix) {
			writeAPIError(w, http.StatusBadRequest, err.Error())
		} else {
			writeInternalError(w, err)
		}
		return
	}
	if !ok {
		writeAPIError(w, http.StatusNotFound, "session not found")
		return
	}

	toolDiff, err := s.compareTools(a.SessionID, b.SessionID)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	fileDiff, err := s.compareFiles(a.SessionID, b.SessionID)
	if err != nil {
		writeInternalError(w, err)
		return
	}

	writeJSON(w, map[string]any{
		"tool_diff": toolDiff,
		"cost_diff": compareCostDiff{
			A:     a.TotalCostUSD,
			B:     b.TotalCostUSD,
			Delta: b.TotalCostUSD - a.TotalCostUSD,
		},
		"token_diff": compareTokenDiff{
			AInput:      a.TotalInputTokens,
			BInput:      b.TotalInputTokens,
			DeltaInput:  b.TotalInputTokens - a.TotalInputTokens,
			AOutput:     a.TotalOutputTokens,
			BOutput:     b.TotalOutputTokens,
			DeltaOutput: b.TotalOutputTokens - a.TotalOutputTokens,
		},
		"file_diff": fileDiff,
	})
}

func (s *Server) compareTools(aID, bID string) ([]compareToolDiff, error) {
	aStats, err := s.db.ListToolStats(aID)
	if err != nil {
		return nil, err
	}
	bStats, err := s.db.ListToolStats(bID)
	if err != nil {
		return nil, err
	}

	counts := make(map[string]*compareToolDiff)
	for _, stat := range aStats {
		diff := counts[stat.ToolName]
		if diff == nil {
			diff = &compareToolDiff{Name: stat.ToolName}
			counts[stat.ToolName] = diff
		}
		diff.ACount = stat.Count
	}
	for _, stat := range bStats {
		diff := counts[stat.ToolName]
		if diff == nil {
			diff = &compareToolDiff{Name: stat.ToolName}
			counts[stat.ToolName] = diff
		}
		diff.BCount = stat.Count
	}

	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)

	result := make([]compareToolDiff, 0, len(names))
	for _, name := range names {
		result = append(result, *counts[name])
	}
	return result, nil
}

func (s *Server) compareFiles(aID, bID string) (compareFileDiff, error) {
	aFiles, err := s.db.ListFileChanges(aID)
	if err != nil {
		return compareFileDiff{}, err
	}
	bFiles, err := s.db.ListFileChanges(bID)
	if err != nil {
		return compareFileDiff{}, err
	}

	aSet := make(map[string]struct{})
	bSet := make(map[string]struct{})
	for _, f := range aFiles {
		aSet[f.FilePath] = struct{}{}
	}
	for _, f := range bFiles {
		bSet[f.FilePath] = struct{}{}
	}

	var diff compareFileDiff
	for path := range aSet {
		if _, ok := bSet[path]; ok {
			diff.Common = append(diff.Common, path)
		} else {
			diff.AOnly = append(diff.AOnly, path)
		}
	}
	for path := range bSet {
		if _, ok := aSet[path]; !ok {
			diff.BOnly = append(diff.BOnly, path)
		}
	}
	sort.Strings(diff.AOnly)
	sort.Strings(diff.BOnly)
	sort.Strings(diff.Common)
	return diff, nil
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

	// Per-model breakdown for this session (used by the detail view's
	// "By model" card). Soft-fail: an error here shouldn't block the rest
	// of the detail payload, the chart is purely additive.
	models, err := s.db.GetSessionModelBreakdown(sess.SessionID)
	if err != nil {
		log.Printf("session model breakdown: %v", err)
		models = nil
	}
	mb := make([]map[string]any, 0, len(models))
	for _, m := range models {
		mb = append(mb, map[string]any{
			"model":         m.Model,
			"input_tokens":  m.InputTokens,
			"output_tokens": m.OutputTokens,
			"cost_usd":      m.CostUSD,
		})
	}

	writeJSON(w, map[string]any{
		"session":     sj,
		"messages":    mj,
		"tools":       tj,
		"agents":      aj,
		"files":       fj,
		"tool_stats":  tsj,
		"agent_stats": asj,
		"models":      mb,
	})
}
