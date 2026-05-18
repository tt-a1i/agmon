package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

func callForecast(t *testing.T, srv *Server, query string) (int, []byte) {
	t.Helper()
	url := "/api/forecast"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	srv.handleForecast(w, req)
	return w.Code, w.Body.Bytes()
}

func decodeForecast(t *testing.T, body []byte) Forecast {
	t.Helper()
	var f Forecast
	if err := json.Unmarshal(body, &f); err != nil {
		t.Fatalf("decode forecast: %v\nbody=%s", err, string(body))
	}
	return f
}

// seedDailyCosts inserts one token-usage row per day for `days` consecutive
// days ending today, each at noon local. Cost per day is given by costFn(i)
// where i is the day offset (0 = `days-1` days ago, days-1 = today).
func seedDailyCosts(t *testing.T, srv *Server, sessionID string, days int, costFn func(int) float64) {
	t.Helper()
	now := time.Now().Local()
	earliest := now.AddDate(0, 0, -(days - 1))
	if err := srv.db.UpsertSession(sessionID, event.PlatformClaude, earliest); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	for i := 0; i < days; i++ {
		ts := time.Date(now.Year(), now.Month(), now.Day()-(days-1-i), 12, 0, 0, 0, time.Local)
		cost := costFn(i)
		src := fmt.Sprintf("%s-%d", sessionID, i)
		if err := srv.db.InsertTokenUsage("a", sessionID, 100, 50, 0, 0, "sonnet", cost, ts, src); err != nil {
			t.Fatalf("insert token day %d: %v", i, err)
		}
	}
}

func TestHandleForecastMonth(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")

	// 14 days of $2/day. Burn rate should land near $2/day, projection
	// should equal spent + burn * remaining.
	seedDailyCosts(t, srv, "m-sess", 14, func(int) float64 { return 2.0 })

	code, body := callForecast(t, srv, "period=month")
	if code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", code, string(body))
	}
	f := decodeForecast(t, body)

	if f.Period != "month" {
		t.Errorf("period: got %q, want month", f.Period)
	}
	if f.BurnRateWindowDays != 7 {
		t.Errorf("burn window: got %d, want 7", f.BurnRateWindowDays)
	}
	// Burn rate is last-7 average; with constant $2/day data extending into
	// the window we expect exactly 2.0 (or 0 if all 7 burn-window days fall
	// before period start, but with 14 seeded days that can't happen).
	if f.BurnRatePerDay < 1.5 || f.BurnRatePerDay > 2.5 {
		t.Errorf("burn rate: got %f, want ~2.0", f.BurnRatePerDay)
	}
	// Projection identity: projected_total == spent_to_date + burn * remaining.
	wantProjected := f.SpentToDate + f.BurnRatePerDay*float64(f.RemainingDays)
	if diff := f.ProjectedTotal - wantProjected; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("projected_total: got %f, want %f (spent + burn*remaining)", f.ProjectedTotal, wantProjected)
	}
	if diff := f.ProjectedRemaining - (f.ProjectedTotal - f.SpentToDate); diff > 1e-9 || diff < -1e-9 {
		t.Errorf("projected_remaining: got %f, want %f", f.ProjectedRemaining, f.ProjectedTotal-f.SpentToDate)
	}
}

func TestHandleForecastWeek(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")

	// Single day of data — just need to hit the endpoint cleanly.
	seedDailyCosts(t, srv, "w-sess", 1, func(int) float64 { return 1.0 })

	code, body := callForecast(t, srv, "period=week")
	if code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", code, string(body))
	}
	f := decodeForecast(t, body)

	if f.Period != "week" {
		t.Errorf("period: got %q, want week", f.Period)
	}
	// period_end should be the last second of Sunday (week ends Sun 23:59:59).
	endLocal := f.PeriodEnd.In(time.Local)
	if endLocal.Weekday() != time.Sunday {
		t.Errorf("period_end weekday: got %s, want Sunday (in %s)", endLocal.Weekday(), time.Local)
	}
	if endLocal.Hour() != 23 || endLocal.Minute() != 59 || endLocal.Second() != 59 {
		t.Errorf("period_end time-of-day: got %02d:%02d:%02d, want 23:59:59",
			endLocal.Hour(), endLocal.Minute(), endLocal.Second())
	}
	// period_start should be the same week's Monday at 00:00.
	startLocal := f.PeriodStart.In(time.Local)
	if startLocal.Weekday() != time.Monday {
		t.Errorf("period_start weekday: got %s, want Monday", startLocal.Weekday())
	}
}

func TestHandleForecastEmptyDB(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")

	code, body := callForecast(t, srv, "period=month")
	if code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", code, string(body))
	}
	f := decodeForecast(t, body)

	if f.SpentToDate != 0 {
		t.Errorf("spent_to_date: got %f, want 0", f.SpentToDate)
	}
	if f.BurnRatePerDay != 0 {
		t.Errorf("burn_rate: got %f, want 0", f.BurnRatePerDay)
	}
	if f.ProjectedTotal != 0 {
		t.Errorf("projected_total: got %f, want 0", f.ProjectedTotal)
	}
	if f.Confidence != "low" {
		t.Errorf("confidence: got %q, want low", f.Confidence)
	}
	if f.VsPreviousPeriod != nil {
		t.Errorf("vs_previous_period: got %+v, want nil", f.VsPreviousPeriod)
	}
}

func TestHandleForecastBadPeriod(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")

	code, body := callForecast(t, srv, "period=year")
	if code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", code, string(body))
	}
}

func TestHandleForecastMethodNotAllowed(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")

	req := httptest.NewRequest(http.MethodPost, "/api/forecast", nil)
	w := httptest.NewRecorder()
	srv.handleForecast(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status: got %d, want 405", w.Code)
	}
	if got := w.Header().Get("Allow"); got != http.MethodGet {
		t.Errorf("Allow header: got %q, want GET", got)
	}
}

func TestHandleForecastVsPreviousPeriod(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")

	now := time.Now().Local()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	// Drop a single record in the previous calendar month. Even one row
	// triggers the comparison block.
	prevMonth := monthStart.AddDate(0, 0, -3)
	if err := db.UpsertSession("prev-sess", event.PlatformClaude, prevMonth); err != nil {
		t.Fatalf("upsert prev: %v", err)
	}
	if err := db.InsertTokenUsage("a", "prev-sess", 100, 50, 0, 0, "sonnet", 10.0, prevMonth, "src-prev"); err != nil {
		t.Fatalf("insert prev token: %v", err)
	}
	// Some current-month data so projected_total > 0.
	if err := db.UpsertSession("cur-sess", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert cur: %v", err)
	}
	if err := db.InsertTokenUsage("a", "cur-sess", 100, 50, 0, 0, "sonnet", 5.0, now.Add(-time.Hour), "src-cur"); err != nil {
		t.Fatalf("insert cur token: %v", err)
	}

	code, body := callForecast(t, srv, "period=month")
	if code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", code, string(body))
	}
	f := decodeForecast(t, body)
	if f.VsPreviousPeriod == nil {
		t.Fatalf("vs_previous_period should be present; got nil")
	}
	if f.VsPreviousPeriod.PreviousTotal != 10.0 {
		t.Errorf("previous_total: got %f, want 10.0", f.VsPreviousPeriod.PreviousTotal)
	}
	if f.VsPreviousPeriod.Direction != "up" && f.VsPreviousPeriod.Direction != "down" && f.VsPreviousPeriod.Direction != "flat" {
		t.Errorf("direction must be one of up/down/flat, got %q", f.VsPreviousPeriod.Direction)
	}
}

func TestComputeConfidenceMatrix(t *testing.T) {
	// Direct unit table covering the three branches.
	cases := []struct {
		name        string
		elapsedDays int
		totalDays   int
		dataDays    int
		want        string
	}{
		{"empty mid-month", 15, 30, 0, "low"},
		{"early in period", 3, 30, 3, "low"},
		{"mid-period fewer than 7 data", 10, 30, 5, "low"},
		{"quarter-elapsed enough data", 8, 30, 8, "medium"},
		{"half-elapsed but data short", 15, 30, 10, "medium"},
		{"half elapsed and ample data", 15, 30, 14, "high"},
		{"late period plenty of data", 25, 30, 25, "high"},
		{"zero total days", 0, 0, 0, "low"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := computeConfidence(c.elapsedDays, c.totalDays, c.dataDays)
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestHandleForecastConfidenceHigh(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")

	now := time.Now().Local()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	monthDays := monthStart.AddDate(0, 1, -1).Day()
	elapsed := daysElapsed(monthStart, now)
	if elapsed < 14 || float64(elapsed)/float64(monthDays) < 0.5 {
		t.Skipf("test requires today to be at least 14 days into the month and >= 50%% elapsed; got elapsed=%d/%d", elapsed, monthDays)
	}

	// 18 distinct days of data ending today, all inside the current month.
	if err := db.UpsertSession("hi-sess", event.PlatformClaude, monthStart); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	for i := 0; i < 18; i++ {
		day := time.Date(now.Year(), now.Month(), now.Day()-(17-i), 12, 0, 0, 0, time.Local)
		if day.Before(monthStart) {
			t.Skipf("test requires 18 distinct in-month days; day -%d lands in previous month", 17-i)
		}
		src := fmt.Sprintf("hi-src-%d", i)
		if err := db.InsertTokenUsage("a", "hi-sess", 100, 50, 0, 0, "sonnet", 1.0, day, src); err != nil {
			t.Fatalf("insert day %d: %v", i, err)
		}
	}

	code, body := callForecast(t, srv, "period=month")
	if code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", code, string(body))
	}
	f := decodeForecast(t, body)
	if f.Confidence != "high" {
		t.Errorf("confidence: got %q, want high (elapsed=%d data=%d)", f.Confidence, f.ElapsedDays, 18)
	}
}

func TestHandleForecastConfidenceLow(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")

	// Three days of data — fewer than 7 → confidence=low regardless of
	// where in the period we are.
	seedDailyCosts(t, srv, "lo-sess", 3, func(int) float64 { return 1.0 })

	code, body := callForecast(t, srv, "period=month")
	if code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", code, string(body))
	}
	f := decodeForecast(t, body)
	if f.Confidence != "low" {
		t.Errorf("confidence: got %q, want low", f.Confidence)
	}
}

func TestHandleForecastTrendUp(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")

	// 7 days: first 4 at $1, last 3 at $10. Burn rate ≈ $4.86, recent 3 = $10.
	// Recent/burn = 2.06 → > 1.15 → trend=up.
	seedDailyCosts(t, srv, "up-sess", 7, func(i int) float64 {
		if i < 4 {
			return 1.0
		}
		return 10.0
	})

	code, body := callForecast(t, srv, "period=month")
	if code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", code, string(body))
	}
	f := decodeForecast(t, body)
	if f.Trend != "up" {
		t.Errorf("trend: got %q, want up (burn=%.2f)", f.Trend, f.BurnRatePerDay)
	}
}

func TestHandleForecastTrendDown(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")

	// Inverse: first 4 at $10, last 3 at $1. Recent 3 well below burn rate.
	seedDailyCosts(t, srv, "dn-sess", 7, func(i int) float64 {
		if i < 4 {
			return 10.0
		}
		return 1.0
	})

	code, body := callForecast(t, srv, "period=month")
	if code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", code, string(body))
	}
	f := decodeForecast(t, body)
	if f.Trend != "down" {
		t.Errorf("trend: got %q, want down (burn=%.2f)", f.Trend, f.BurnRatePerDay)
	}
}

func TestPeriodBoundsMonthAndWeek(t *testing.T) {
	ref := time.Date(2026, time.May, 15, 14, 30, 0, 0, time.Local)
	mStart, mEnd := periodBounds("month", ref)
	if mStart.Year() != 2026 || mStart.Month() != time.May || mStart.Day() != 1 {
		t.Errorf("month start: got %v, want 2026-05-01", mStart)
	}
	if mEnd.Day() != 31 || mEnd.Hour() != 23 || mEnd.Minute() != 59 {
		t.Errorf("month end: got %v, want 2026-05-31 23:59:59", mEnd)
	}

	wStart, wEnd := periodBounds("week", ref) // 2026-05-15 is a Friday
	if wStart.Weekday() != time.Monday {
		t.Errorf("week start weekday: got %s, want Monday", wStart.Weekday())
	}
	if wEnd.Weekday() != time.Sunday {
		t.Errorf("week end weekday: got %s, want Sunday", wEnd.Weekday())
	}
	if wEnd.Sub(wStart) < 6*24*time.Hour {
		t.Errorf("week duration: got %v, want >= 6 days", wEnd.Sub(wStart))
	}
}
