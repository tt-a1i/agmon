package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/storage"
)

// decodeInsights pulls the JSON body of a /api/insights response so tests can
// assert against the parsed shape without re-implementing the wire decoder.
func decodeInsights(t *testing.T, body []byte) insightsResponse {
	t.Helper()
	var resp insightsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode insights body: %v\nbody=%s", err, string(body))
	}
	return resp
}

// findInsight returns the first insight with the given kind, or false if missing.
func findInsight(insights []Insight, kind string) (Insight, bool) {
	for _, i := range insights {
		if i.Kind == kind {
			return i, true
		}
	}
	return Insight{}, false
}

// callInsights drives the /api/insights handler against an in-memory test
// server and returns the response code + body.
func callInsights(t *testing.T, srv *Server, query string) (int, []byte) {
	t.Helper()
	url := "/api/insights"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	srv.handleInsights(w, req)
	return w.Code, w.Body.Bytes()
}

func TestHandleInsightsEmptyDB(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")

	code, body := callInsights(t, srv, "")
	if code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", code, string(body))
	}

	resp := decodeInsights(t, body)
	if resp.Range != "week" {
		t.Errorf("range: got %q, want week", resp.Range)
	}
	if resp.Insights == nil {
		t.Fatalf("insights should be empty array, not nil; body=%s", string(body))
	}
	// Must be a JSON array, not null — clients iterate without nil-checking.
	if !strings.Contains(string(body), `"insights":[]`) {
		t.Errorf("expected empty array for insights, got body=%s", string(body))
	}
	if len(resp.Insights) != 0 {
		t.Errorf("expected 0 insights for empty DB, got %d", len(resp.Insights))
	}
}

func TestHandleInsightsBadRange(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")

	code, body := callInsights(t, srv, "range=bogus")
	if code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", code, string(body))
	}
}

func TestHandleInsightsRespectsRangeParam(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")

	// Seed at least one token row so "all" has a non-trivial range anchor.
	now := time.Now().Local()
	if err := db.UpsertSession("s-range", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.InsertTokenUsage("a-range", "s-range", 100, 50, 0, 0, "sonnet", 0.1, now, "src-range"); err != nil {
		t.Fatalf("insert token: %v", err)
	}

	for _, rangeParam := range []string{"week", "month", "all"} {
		t.Run(rangeParam, func(t *testing.T) {
			code, body := callInsights(t, srv, "range="+rangeParam)
			if code != http.StatusOK {
				t.Fatalf("status: got %d, want 200; body=%s", code, string(body))
			}
			resp := decodeInsights(t, body)
			if resp.Range != rangeParam {
				t.Errorf("range echo: got %q, want %q", resp.Range, rangeParam)
			}
			if resp.GeneratedAt == "" {
				t.Errorf("generated_at should be present")
			}
			if _, err := time.Parse(time.RFC3339, resp.GeneratedAt); err != nil {
				t.Errorf("generated_at not RFC3339: %v", err)
			}
		})
	}
}

func TestHandleInsightsPeakDay(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")

	// Seven distinct past calendar days ending today, with the earliest day
	// at 5× the baseline cost. Using range=all anchors the from-bound at the
	// first token date, so this is independent of test-run weekday/timing.
	now := time.Now().Local()
	if err := db.UpsertSession("peak-sess", event.PlatformClaude, now.AddDate(0, 0, -6)); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	for i := 0; i < 7; i++ {
		day := time.Date(now.Year(), now.Month(), now.Day()-(6-i), 12, 0, 0, 0, time.Local)
		cost := 1.0
		if i == 0 {
			cost = 5.0
		}
		src := fmt.Sprintf("src-peak-%d", i)
		if err := db.InsertTokenUsage("a-peak", "peak-sess", 100, 50, 0, 0, "sonnet", cost, day, src); err != nil {
			t.Fatalf("insert token day %d: %v", i, err)
		}
	}

	code, body := callInsights(t, srv, "range=all")
	if code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", code, string(body))
	}
	resp := decodeInsights(t, body)
	ins, ok := findInsight(resp.Insights, "peak_day")
	if !ok {
		t.Fatalf("peak_day insight missing; got insights=%+v", resp.Insights)
	}
	if ins.Value < 4.5 {
		t.Errorf("peak_day value: got %f, want >= 4.5", ins.Value)
	}
	ratio, _ := ins.Metadata["ratio"].(float64)
	if ratio < 1.5 {
		t.Errorf("peak_day ratio: got %f, want >= 1.5", ratio)
	}
}

func TestHandleInsightsTopTool(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")

	now := time.Now().Local()
	if err := db.UpsertSession("tool-sess", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}

	// Edit dominates (10 calls), Read=3, Bash=1. Edit should be top_tool.
	tools := []struct {
		name  string
		count int
	}{
		{"Edit", 10},
		{"Read", 3},
		{"Bash", 1},
	}
	for _, tool := range tools {
		for i := 0; i < tool.count; i++ {
			callID := fmt.Sprintf("%s-%d", tool.name, i)
			if _, err := db.InsertToolCallStart(callID, "a", "tool-sess", tool.name, "{}", now); err != nil {
				t.Fatalf("insert tool %s #%d: %v", tool.name, i, err)
			}
			if err := db.UpdateToolCallEnd(callID, "ok", event.StatusSuccess, 100, now.Add(100*time.Millisecond)); err != nil {
				t.Fatalf("update tool %s #%d: %v", tool.name, i, err)
			}
		}
	}

	code, body := callInsights(t, srv, "range=week")
	if code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", code, string(body))
	}
	resp := decodeInsights(t, body)
	ins, ok := findInsight(resp.Insights, "top_tool")
	if !ok {
		t.Fatalf("top_tool insight missing; got insights=%+v", resp.Insights)
	}
	if got, _ := ins.Metadata["tool"].(string); got != "Edit" {
		t.Errorf("top_tool: got %q, want Edit", got)
	}
	if ins.Value != 10 {
		t.Errorf("top_tool count: got %f, want 10", ins.Value)
	}
	share, _ := ins.Metadata["share"].(float64)
	// 10/14 ≈ 0.71. Allow some slack for rounding.
	if share < 0.6 || share > 0.8 {
		t.Errorf("top_tool share: got %f, want ~0.71", share)
	}
}

func TestHandleInsightsCostAnomaly(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")

	now := time.Now().Local()
	// Six "normal" sessions at $1 each, one outlier at $50. The outlier's
	// z-score crosses the 2.0 threshold used by computeCostAnomalies.
	for i := 0; i < 6; i++ {
		sid := fmt.Sprintf("normal-%d", i)
		if err := db.UpsertSession(sid, event.PlatformClaude, now); err != nil {
			t.Fatalf("upsert normal %d: %v", i, err)
		}
		if err := db.InsertTokenUsage("a", sid, 100, 50, 0, 0, "sonnet", 1.0, now, "src-"+sid); err != nil {
			t.Fatalf("insert normal token %d: %v", i, err)
		}
	}
	if err := db.UpsertSession("outlier-sess", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert outlier: %v", err)
	}
	if err := db.InsertTokenUsage("a", "outlier-sess", 100, 50, 0, 0, "sonnet", 50.0, now, "src-outlier"); err != nil {
		t.Fatalf("insert outlier: %v", err)
	}

	code, body := callInsights(t, srv, "range=week")
	if code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", code, string(body))
	}
	resp := decodeInsights(t, body)
	ins, ok := findInsight(resp.Insights, "cost_anomaly")
	if !ok {
		t.Fatalf("cost_anomaly insight missing; got insights=%+v", resp.Insights)
	}
	if got, _ := ins.Metadata["session_id"].(string); got != "outlier-sess" {
		t.Errorf("cost_anomaly session: got %q, want outlier-sess", got)
	}
	z, _ := ins.Metadata["z_score"].(float64)
	if z < 2 {
		t.Errorf("cost_anomaly z_score: got %f, want > 2", z)
	}
}

func TestHandleInsightsModelMixShift(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")

	// range=month with at least an hour into the month so the prev-period
	// window (from - dur, from) spans a non-empty interval just before the
	// 1st. We anchor prev/cur timestamps at 30 minutes inside their windows
	// to stay clear of boundary rounding.
	now := time.Now().Local()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	if now.Sub(monthStart) < 2*time.Hour {
		t.Skip("test requires today to be >=2h into the month so the prev-period window has space")
	}
	curTime := now.Add(-30 * time.Minute)
	prevTime := monthStart.Add(-30 * time.Minute)

	if err := db.UpsertSession("mix-sess", event.PlatformClaude, prevTime); err != nil {
		t.Fatalf("upsert session: %v", err)
	}

	// Prev period: dominated by sonnet ($9), little haiku ($1).
	if err := db.InsertTokenUsage("a", "mix-sess", 100, 50, 0, 0, "sonnet", 9.0, prevTime, "src-prev-sonnet"); err != nil {
		t.Fatalf("insert prev sonnet: %v", err)
	}
	if err := db.InsertTokenUsage("a", "mix-sess", 100, 50, 0, 0, "haiku", 1.0, prevTime, "src-prev-haiku"); err != nil {
		t.Fatalf("insert prev haiku: %v", err)
	}

	// Cur period: flipped — haiku dominant ($9), sonnet residual ($1).
	if err := db.InsertTokenUsage("a", "mix-sess", 100, 50, 0, 0, "haiku", 9.0, curTime, "src-cur-haiku"); err != nil {
		t.Fatalf("insert cur haiku: %v", err)
	}
	if err := db.InsertTokenUsage("a", "mix-sess", 100, 50, 0, 0, "sonnet", 1.0, curTime, "src-cur-sonnet"); err != nil {
		t.Fatalf("insert cur sonnet: %v", err)
	}

	code, body := callInsights(t, srv, "range=month")
	if code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", code, string(body))
	}
	resp := decodeInsights(t, body)
	ins, ok := findInsight(resp.Insights, "model_mix_shift")
	if !ok {
		t.Fatalf("model_mix_shift insight missing; got insights=%+v", resp.Insights)
	}
	if up, _ := ins.Metadata["model_up"].(string); up != "haiku" {
		t.Errorf("model_up: got %q, want haiku", up)
	}
	if down, _ := ins.Metadata["model_down"].(string); down != "sonnet" {
		t.Errorf("model_down: got %q, want sonnet", down)
	}
}

func TestHandleInsightsMethodNotAllowed(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")

	req := httptest.NewRequest(http.MethodPost, "/api/insights", nil)
	w := httptest.NewRecorder()
	srv.handleInsights(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status: got %d, want 405", w.Code)
	}
	if got := w.Header().Get("Allow"); got != http.MethodGet {
		t.Errorf("Allow header: got %q, want GET", got)
	}
}

func TestBuildPeakDayInsightSkipsWhenFlat(t *testing.T) {
	a := &storage.AnalysisResult{
		Range: "week",
		Cost: storage.CostSummary{
			ActiveDays:     5,
			HighestDay:     "2026-05-14",
			HighestDayCost: 1.0,
			AveragePerDay:  1.0,
		},
	}
	if _, ok := buildPeakDayInsight(a); ok {
		t.Errorf("peak_day should be suppressed when ratio is 1.0")
	}

	// Bumping the highest day to 2× the average should now emit.
	a.Cost.HighestDayCost = 2.0
	ins, ok := buildPeakDayInsight(a)
	if !ok {
		t.Fatalf("peak_day should emit when ratio is 2.0")
	}
	if !strings.Contains(ins.Body, "2.0×") {
		t.Errorf("body should include ratio; got %q", ins.Body)
	}
}

func TestSavedHoursForToolFallback(t *testing.T) {
	// Known tool uses its mapped weight.
	if got := savedHoursForTool("Edit", 60); got != 3.0 {
		t.Errorf("Edit @ 60: got %f, want 3.0", got)
	}
	// Unknown tool uses the default weight (1.5 min × 120 / 60 = 3.0).
	if got := savedHoursForTool("AlienTool", 120); got != 3.0 {
		t.Errorf("AlienTool @ 120: got %f, want 3.0", got)
	}
}
