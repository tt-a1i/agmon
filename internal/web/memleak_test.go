package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/testutil"
)

// TestWebHandleSessionsNoMemLeak verifies that the /api/sessions handler does
// not accumulate heap memory across repeated requests. The DB is seeded with
// a small set of sessions so the response is non-trivial.
func TestWebHandleSessionsNoMemLeak(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")

	// Seed a few sessions so JSON serialization exercises real paths.
	now := time.Now()
	for i := range 5 {
		id := "ml-session-" + string(rune('a'+i))
		_ = db.UpsertSession(id, event.PlatformClaude, now.Add(time.Duration(i)*time.Minute))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/sessions", srv.handleSessions)

	testutil.MemLeakCheck(t, func() {
		req := httptest.NewRequest("GET", "/api/sessions", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
	})
}

// TestWebHandleCostsNoMemLeak verifies that the /api/costs handler does not
// leak heap memory across repeated requests.
func TestWebHandleCostsNoMemLeak(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0")

	mux := http.NewServeMux()
	mux.HandleFunc("/api/costs", srv.handleCosts)

	testutil.MemLeakCheck(t, func() {
		req := httptest.NewRequest("GET", "/api/costs", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
	})
}
