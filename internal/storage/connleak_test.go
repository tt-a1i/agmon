package storage

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/testutil"
)

func openConnLeakDB(t *testing.T) (*DB, func()) {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "connleak.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	return db, func() { _ = db.Close() }
}

// TestStorage1000QueriesNoConnLeak verifies that repeated read queries do not
// accumulate in-use DB connections (i.e. all *sql.Rows are properly closed).
func TestStorage1000QueriesNoConnLeak(t *testing.T) {
	db, cleanup := openConnLeakDB(t)
	defer cleanup()

	// Warm up: establish the initial connection so the baseline InUse is accurate.
	_, _ = db.ListSessions()

	defer testutil.DBConnLeakCheck(t, db)()

	for range 1000 {
		_, _ = db.ListSessions()
	}
}

// TestStorageWriteReadCycleNoConnLeak verifies that a write + read cycle
// (upsert session, token usage, then query) leaves no dangling connections.
func TestStorageWriteReadCycleNoConnLeak(t *testing.T) {
	db, cleanup := openConnLeakDB(t)
	defer cleanup()

	now := time.Now()
	sessionID := "connleak-session"

	// Warm up.
	_, _ = db.ListSessions()
	defer testutil.DBConnLeakCheck(t, db)()

	for i := range 50 {
		_ = db.UpsertSession(sessionID, event.PlatformClaude, now)
		_ = db.UpsertAgent("agent-"+string(rune('a'+i%26)), sessionID, "", "coder", now)
		_ = db.InsertTokenUsage(
			"agent-a", sessionID,
			100, 50, 0, 0,
			"sonnet", 0.001, now,
			"src-"+string(rune('a'+i%26)),
		)
		_, _ = db.ListSessions()
		_, _ = db.ListToolCalls(sessionID, 10)
	}
	_ = db.EndSession(sessionID, now.Add(time.Second))
}
