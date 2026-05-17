package storage

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

// TestPreparedStatementReused verifies that calling ListSessions twice shares
// the same underlying *sql.Stmt (i.e. the cache deduplicates preparation).
func TestPreparedStatementReused(t *testing.T) {
	db := testDB(t)

	// Warm the cache.
	_, err := db.ListSessions()
	if err != nil {
		t.Fatalf("first ListSessions: %v", err)
	}

	db.cache.mu.RLock()
	countAfterFirst := len(db.cache.stmts)
	db.cache.mu.RUnlock()

	// Second call must not grow the cache.
	_, err = db.ListSessions()
	if err != nil {
		t.Fatalf("second ListSessions: %v", err)
	}

	db.cache.mu.RLock()
	countAfterSecond := len(db.cache.stmts)
	db.cache.mu.RUnlock()

	if countAfterFirst == 0 {
		t.Error("expected at least one cached statement after ListSessions")
	}
	if countAfterSecond != countAfterFirst {
		t.Errorf("cache grew from %d to %d stmts on second call; statement was not reused",
			countAfterFirst, countAfterSecond)
	}
}

// TestPreparedStatementClosedOnDBClose verifies that Close() drains the stmt
// cache. Accessing cache.stmts after Close must return nil (cleared map).
func TestPreparedStatementClosedOnDBClose(t *testing.T) {
	dir := t.TempDir()

	// Open a separate DB (not the one registered via t.Cleanup) so we can
	// call Close ourselves and inspect state afterwards.
	import_path := dir + "/prep_close.db"
	db, err := Open(import_path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	// Warm the cache.
	if _, err := db.ListSessions(); err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if _, err := db.GetCostBetween(time.Now().Add(-time.Hour), time.Now()); err != nil {
		t.Fatalf("GetCostBetween: %v", err)
	}

	db.cache.mu.RLock()
	countBefore := len(db.cache.stmts)
	db.cache.mu.RUnlock()

	if countBefore == 0 {
		t.Fatal("expected cached stmts before Close")
	}

	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// After Close, the map must be nil (cleared by Close).
	db.cache.mu.RLock()
	stmtsAfter := db.cache.stmts
	db.cache.mu.RUnlock()

	if stmtsAfter != nil {
		t.Errorf("cache.stmts should be nil after Close, got map with %d entries", len(stmtsAfter))
	}
}

// TestPreparedStatementConcurrentSafe runs 50 goroutines concurrently each
// calling ListSessions and GetCostBetween; -race must not fire.
func TestPreparedStatementConcurrentSafe(t *testing.T) {
	db := testDB(t)

	// Seed a few sessions so queries return real rows.
	now := time.Now()
	for i := range 10 {
		if err := db.UpsertSession(fmt.Sprintf("prep-sess-%d", i), event.PlatformClaude, now); err != nil {
			t.Fatalf("upsert session: %v", err)
		}
	}

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			if _, err := db.ListSessions(); err != nil {
				t.Errorf("ListSessions: %v", err)
			}
			if _, err := db.GetCostBetween(now.Add(-time.Hour), now.Add(time.Hour)); err != nil {
				t.Errorf("GetCostBetween: %v", err)
			}
		}()
	}
	wg.Wait()
}
