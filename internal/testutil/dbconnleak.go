package testutil

import (
	"database/sql"
	"testing"
	"time"
)

// DBStatter is satisfied by *sql.DB and any wrapper that exposes Stats().
// storage.DB gains this method via its Stats() accessor.
type DBStatter interface {
	Stats() sql.DBStats
}

// DBConnLeakCheck verifies that no database connections remain in-use after a
// test completes. It waits up to 500 ms for in-flight connections to drain,
// then reports a test failure if any are still held.
//
// Usage: defer testutil.DBConnLeakCheck(t, db)()
//
// The check targets InUse (connections executing a query), not OpenConnections
// (which includes idle connections that are legitimately held by the pool).
// An unclosed *sql.Rows or *sql.Tx will keep InUse > 0 and be detected.
func DBConnLeakCheck(t *testing.T, db DBStatter) func() {
	t.Helper()
	return func() {
		t.Helper()
		deadline := time.Now().Add(500 * time.Millisecond)
		for time.Now().Before(deadline) {
			if db.Stats().InUse <= 0 {
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
		s := db.Stats()
		if s.InUse > 0 {
			t.Errorf("DB connection leak: %d connection(s) still in use (WaitCount=%d)",
				s.InUse, s.WaitCount)
		}
	}
}
