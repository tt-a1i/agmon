package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/storage"
)

// TestGracefulShutdownReleasesStart verifies Shutdown unblocks Start within
// the deadline, so callers can react to SIGTERM without leaking the goroutine.
func TestGracefulShutdownReleasesStart(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0") // random port

	done := make(chan error, 1)
	go func() {
		done <- srv.Start()
	}()

	// Give ListenAndServe a moment to register the listener with the Server
	// state machine. Without this, Shutdown completes before there's anything
	// to close.
	time.Sleep(50 * time.Millisecond)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	select {
	case err := <-done:
		// Start swallows http.ErrServerClosed internally.
		if err != nil {
			t.Fatalf("Start returned %v", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("Start did not return within 6 seconds of Shutdown")
	}
}

// TestShutdownBeforeStartIsNoOp verifies calling Shutdown on a never-Start()ed
// Server is safe.
//
// Relies on http.Server.Shutdown returning nil when no listener has been
// registered. Not explicitly documented but stable across Go releases — if
// this test ever fails under a new Go version, audit the Shutdown call site.
func TestShutdownBeforeStartIsNoOp(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown before Start should be no-op, got %v", err)
	}
}

// TestHandleSessionsHonorsLimitQuery verifies the ?limit query parameter
// caps results and that invalid/out-of-range limits fall back gracefully.
func TestHandleSessionsHonorsLimitQuery(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	// Seed 30 visible sessions.
	for i := 0; i < 30; i++ {
		id := "sess-" + string(rune('A'+i))
		if err := db.UpsertSession(id, event.PlatformClaude, now.Add(-time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("upsert %s: %v", id, err)
		}
	}

	srv := NewServer(db, "0")
	mux := http.NewServeMux()
	mux.HandleFunc("/api/sessions", srv.handleSessions)

	cases := []struct {
		name      string
		query     string
		wantCount int
	}{
		{"limit 5", "?limit=5", 5},
		{"limit clamped over 1000", "?limit=999999", 30},
		{"non-numeric falls back to default", "?limit=abc", 30},
		{"missing query uses default", "", 30},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/sessions"+tc.query, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status=%d", w.Code)
			}
			var result []sessionJSON
			if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(result) != tc.wantCount {
				t.Errorf("rows=%d, want=%d", len(result), tc.wantCount)
			}
		})
	}
}

// TestHandleCostsTodayIncludesLocalEarlyMorning regresses P2-15: range=today
// must include tokens written between local-midnight and "now", which are
// still on the previous UTC date for UTC+8 users. The bug was that the
// upper-layer from/to used UTC-midnight; the SQL aggregated by local day,
// and the mismatch silently dropped the early-morning hours.
//
// This test passes regardless of host TZ because we anchor the test row to
// the current local-day boundary directly.
func TestHandleCostsTodayIncludesLocalEarlyMorning(t *testing.T) {
	db := testDB(t)

	now := time.Now()
	localMidnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	// Token at local 02:00 (a few hours into local-today). On UTC+8 hosts
	// this is still 18:00 UTC of the previous calendar day.
	earlyLocal := localMidnight.Add(2 * time.Hour)
	if earlyLocal.After(now) {
		// Test ran less than 2 hours into local-today. Anchor the row to
		// just after local midnight so it still lands in the "today"
		// bucket, but cap at `now` if we're literally at the boundary.
		earlyLocal = localMidnight.Add(time.Second)
		if earlyLocal.After(now) {
			earlyLocal = now
		}
	}

	if err := db.UpsertSession("tz-edge", event.PlatformClaude, earlyLocal); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := db.InsertTokenUsage("a", "tz-edge", 100, 50, 0, 0, "claude-sonnet-4-6", 1.0, earlyLocal, "src-tz"); err != nil {
		t.Fatalf("insert tokens: %v", err)
	}
	if err := db.UpdateSessionTokens("tz-edge"); err != nil {
		t.Fatalf("update tokens: %v", err)
	}

	srv := NewServer(db, "0")
	mux := http.NewServeMux()
	mux.HandleFunc("/api/costs", srv.handleCosts)

	req := httptest.NewRequest("GET", "/api/costs?range=today", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp costResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.TotalCost < 0.99 {
		t.Errorf("local-today token at 02:00 missing from 'today' bucket: cost=%v (TZ=%s)",
			resp.TotalCost, time.Local.String())
	}
}

// TestHandleCostsRangeWeekAndMonth exercises the week/month branches of
// handleCosts which were previously only partly covered.
func TestHandleCostsRangeWeekAndMonth(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	// Seed a token within "this week" so SUM > 0.
	if err := db.UpsertSession("s1", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := db.InsertTokenUsage("a", "s1", 100, 50, 0, 0, "sonnet", 1.0, now, "src-1"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	srv := NewServer(db, "0")
	mux := http.NewServeMux()
	mux.HandleFunc("/api/costs", srv.handleCosts)

	for _, rng := range []string{"week", "month", "today"} {
		t.Run(rng, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/costs?range="+rng, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status=%d", w.Code)
			}
			var resp costResponse
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.Range != rng {
				t.Errorf("range = %q, want %q", resp.Range, rng)
			}
			if resp.TotalCost < 0.99 {
				t.Errorf("expected cost >= 0.99 for range=%s, got %v", rng, resp.TotalCost)
			}
		})
	}
}

// TestHandleStatsFullResponse exercises the full handleStats path including
// active-vs-total counts and weekly cost.
func TestHandleStatsFullResponse(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	for i, status := range []string{"active", "ended", "active"} {
		id := "s-" + string(rune('a'+i))
		if err := db.UpsertSession(id, event.PlatformClaude, now); err != nil {
			t.Fatalf("upsert: %v", err)
		}
		if err := db.InsertTokenUsage("agent", id, 100, 50, 0, 0, "sonnet", 1.0, now, "src-"+id); err != nil {
			t.Fatalf("insert: %v", err)
		}
		if status == "ended" {
			if err := db.EndSession(id, now.Add(time.Hour)); err != nil {
				t.Fatalf("end: %v", err)
			}
		}
	}

	srv := NewServer(db, "0")
	mux := http.NewServeMux()
	mux.HandleFunc("/api/stats", srv.handleStats)

	req := httptest.NewRequest("GET", "/api/stats", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp statsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.TotalSessions != 3 {
		t.Errorf("TotalSessions = %d, want 3", resp.TotalSessions)
	}
	if resp.ActiveCount != 2 {
		t.Errorf("ActiveCount = %d, want 2 (1 ended)", resp.ActiveCount)
	}
}

// TestHandleSessionDetailDistinguishesAmbiguousFromInternalError verifies the
// handler returns 400 for storage.ErrAmbiguousSessionPrefix and does NOT leak
// SQL or table names. (Previously the handler returned 400 with err.Error()
// for any error.)
func TestHandleSessionDetailDistinguishesAmbiguousFromInternalError(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Two sessions sharing a 4-char prefix → ambiguous.
	now := time.Now().UTC()
	if err := db.UpsertSession("abcd1234-aaaa", event.PlatformClaude, now); err != nil {
		t.Fatalf("insert s1: %v", err)
	}
	if err := db.UpsertSession("abcd5678-bbbb", event.PlatformClaude, now); err != nil {
		t.Fatalf("insert s2: %v", err)
	}

	srv := NewServer(db, "0")
	mux := http.NewServeMux()
	mux.HandleFunc("/api/session/", srv.handleSessionDetail)

	req := httptest.NewRequest("GET", "/api/session/abcd", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "ambiguous") {
		t.Errorf("body should mention ambiguous, got: %s", body)
	}
	// Must NOT leak internal SQL keywords/types.
	for _, leak := range []string{"SELECT", "FROM", "WHERE", "JOIN", "sql:", "table"} {
		if strings.Contains(body, leak) {
			t.Errorf("response leaks internal detail %q: %s", leak, body)
		}
	}
	// Sanity: the underlying error must still be the typed sentinel so other
	// callers can rely on errors.Is.
	_, _, dbErr := db.GetSessionByIDPrefix("abcd")
	if !errors.Is(dbErr, storage.ErrAmbiguousSessionPrefix) {
		t.Errorf("expected ErrAmbiguousSessionPrefix, got %v", dbErr)
	}
}
