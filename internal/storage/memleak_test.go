package storage

import (
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/testutil"
)

func openMemLeakDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "memleak.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestStorageListSessionsNoMemLeak verifies that repeated ListSessions calls
// do not accumulate heap memory (e.g. via internal row-buffer growth or
// unreleased query state).
func TestStorageListSessionsNoMemLeak(t *testing.T) {
	db := openMemLeakDB(t)

	// Seed a batch of sessions so the query returns real rows.
	now := time.Now()
	for i := range 20 {
		_ = db.UpsertSession(fmt.Sprintf("ml-sess-%d", i), event.PlatformClaude, now)
	}

	// Warm up to establish baseline after initial DB state.
	_, _ = db.ListSessions()

	testutil.MemLeakCheck(t, func() {
		_, _ = db.ListSessions()
	})
}

// TestStorageInsertTokenUsageNoMemLeak verifies that repeated InsertTokenUsage
// calls (including deduplication via source_id) do not grow the heap.
func TestStorageInsertTokenUsageNoMemLeak(t *testing.T) {
	db := openMemLeakDB(t)

	now := time.Now()
	sessionID := "ml-insert-sess"
	_ = db.UpsertSession(sessionID, event.PlatformClaude, now)

	var seq atomic.Int64

	testutil.MemLeakCheck(t, func() {
		i := seq.Add(1)
		// Each call uses a unique source_id so the INSERT is not a no-op.
		_ = db.InsertTokenUsage(
			"agent-a", sessionID,
			100, 50, 0, 0,
			"sonnet", 0.001,
			now.Add(time.Duration(i)*time.Millisecond),
			fmt.Sprintf("src-%d", i),
		)
	}, testutil.MemLeakOpts{Rounds: 100})
}
