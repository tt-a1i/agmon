package storage

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

// TestListSessionsLimit verifies the parameterized cap returns up to limit
// rows in start_time desc order, and that limit <= 0 falls back to the
// default cap.
func TestListSessionsLimit(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "limit.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	base := time.Now().UTC().Add(-time.Hour)
	const seeded = 250
	for i := 0; i < seeded; i++ {
		id := fmt.Sprintf("s-%04d", i)
		startTime := base.Add(time.Duration(i) * time.Minute)
		if err := db.UpsertSession(id, event.PlatformClaude, startTime); err != nil {
			t.Fatalf("upsert %s: %v", id, err)
		}
		// Give each row tokens so the "visible" filter matches all 250.
		if err := db.InsertTokenUsage("a", id, 10, 5, 0, 0, "sonnet", 0.01, startTime, fmt.Sprintf("src-%d", i)); err != nil {
			t.Fatalf("insert tokens: %v", err)
		}
	}

	// Default ListSessions caps at 200.
	got, err := db.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(got) != DefaultSessionListLimit {
		t.Errorf("ListSessions default = %d, want %d", len(got), DefaultSessionListLimit)
	}

	// Explicit large limit pulls them all.
	got, err = db.ListSessionsLimit(seeded)
	if err != nil {
		t.Fatalf("ListSessionsLimit(%d): %v", seeded, err)
	}
	if len(got) != seeded {
		t.Errorf("ListSessionsLimit(%d) = %d, want %d", seeded, len(got), seeded)
	}

	// limit <= 0 → falls back to default.
	got, err = db.ListSessionsLimit(0)
	if err != nil {
		t.Fatalf("ListSessionsLimit(0): %v", err)
	}
	if len(got) != DefaultSessionListLimit {
		t.Errorf("ListSessionsLimit(0) = %d, want default %d", len(got), DefaultSessionListLimit)
	}

	// Order: latest first.
	if got[0].SessionID != "s-0249" {
		t.Errorf("first row should be newest, got %q", got[0].SessionID)
	}

	// Visible count matches seeded (no LIMIT applied here).
	count, err := db.GetVisibleSessionCount()
	if err != nil {
		t.Fatalf("GetVisibleSessionCount: %v", err)
	}
	if count != seeded {
		t.Errorf("GetVisibleSessionCount = %d, want %d", count, seeded)
	}
}
