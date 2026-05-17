package storage

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

// countRows queries a table directly via the internal *sql.DB (white-box access).
func countRows(t *testing.T, db *DB, table string) int {
	t.Helper()
	var n int
	// table name is not user-supplied in production; only test-local constants used.
	if err := db.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil { //nolint:gosec
		t.Fatalf("countRows(%s): %v", table, err)
	}
	return n
}

// execRaw runs an arbitrary SQL statement against the internal connection (test only).
func execRaw(t *testing.T, db *DB, query string, args ...any) {
	t.Helper()
	if _, err := db.db.Exec(query, args...); err != nil {
		t.Fatalf("execRaw(%q): %v", query, err)
	}
}

// insertUsage is a compact wrapper around InsertTokenUsage for test use.
func insertUsage(t *testing.T, db *DB, sessionID, sourceID string, cost float64) {
	t.Helper()
	ts := time.Now()
	if err := db.InsertTokenUsage("agent-1", sessionID, 100, 50, 0, 0, "claude-sonnet-4-6", cost, ts, sourceID); err != nil {
		t.Fatalf("InsertTokenUsage(%s, %s): %v", sessionID, sourceID, err)
	}
}

// TestTxnRollbackOnSessionsUpdateError verifies that InsertTokenUsage rolls back
// the token_usage INSERT when the subsequent sessions UPDATE is aborted by a trigger.
// This guards the atomic: "insert usage row + update session totals" invariant.
func TestTxnRollbackOnSessionsUpdateError(t *testing.T) {
	db := testDB(t)

	// Create a session so the UPDATE actually matches a row and fires the trigger.
	if err := db.UpsertSession("sess-rollback", event.PlatformClaude, time.Now()); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}

	// Install a trigger that raises ABORT on any UPDATE to sessions.
	// RAISE(ABORT) causes the current statement to fail and the enclosing
	// transaction to be rolled back.
	execRaw(t, db, `
		CREATE TRIGGER txn_test_abort_sessions_update
		BEFORE UPDATE ON sessions
		BEGIN
			SELECT RAISE(ABORT, 'txn_test: intentional abort');
		END
	`)
	t.Cleanup(func() {
		db.db.Exec("DROP TRIGGER IF EXISTS txn_test_abort_sessions_update") //nolint:errcheck
	})

	beforeUsage := countRows(t, db, "token_usage")

	// InsertTokenUsage must fail because the trigger aborts the UPDATE.
	err := db.InsertTokenUsage("agent-1", "sess-rollback", 100, 50, 0, 0, "sonnet", 0.10, time.Now(), "src-rollback-1")
	if err == nil {
		t.Fatal("expected InsertTokenUsage to fail due to trigger, got nil error")
	}

	// The token_usage INSERT was part of the same transaction — it must be rolled back.
	afterUsage := countRows(t, db, "token_usage")
	if afterUsage != beforeUsage {
		t.Errorf("token_usage rows changed: before=%d after=%d — rollback did not fire", beforeUsage, afterUsage)
	}
}

// TestTxnDuplicateSourceIDDeduplication verifies that calling InsertTokenUsage
// twice with the same non-empty sourceID inserts exactly one row (INSERT OR IGNORE).
func TestTxnDuplicateSourceIDDeduplication(t *testing.T) {
	db := testDB(t)
	if err := db.UpsertSession("sess-dedup", event.PlatformClaude, time.Now()); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}

	insertUsage(t, db, "sess-dedup", "stable-src-id", 0.10)
	insertUsage(t, db, "sess-dedup", "stable-src-id", 0.10) // duplicate — should be ignored

	n := countRows(t, db, "token_usage")
	if n != 1 {
		t.Errorf("token_usage rows = %d, want 1 (duplicate should be ignored)", n)
	}

	// Session totals must reflect only the first insertion.
	sess, ok, err := db.GetSessionByIDPrefix("sess-dedup")
	if err != nil || !ok {
		t.Fatalf("GetSession: ok=%v err=%v", ok, err)
	}
	const wantCost = 0.10
	if sess.TotalCostUSD < wantCost-0.001 || sess.TotalCostUSD > wantCost+0.001 {
		t.Errorf("session TotalCostUSD = %.4f, want %.4f (duplicate should not double-count)", sess.TotalCostUSD, wantCost)
	}
}

// TestTxnEmptySourceIDAllowsDuplicates verifies that InsertTokenUsage with sourceID=""
// bypasses the UNIQUE index (partial index excludes empty strings) so both rows insert.
func TestTxnEmptySourceIDAllowsDuplicates(t *testing.T) {
	db := testDB(t)
	if err := db.UpsertSession("sess-nosrcid", event.PlatformClaude, time.Now()); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}

	insertUsage(t, db, "sess-nosrcid", "", 0.05)
	insertUsage(t, db, "sess-nosrcid", "", 0.05)

	n := countRows(t, db, "token_usage")
	if n != 2 {
		t.Errorf("token_usage rows = %d, want 2 (empty source_id should not dedup)", n)
	}
}

// TestTxnConcurrentInsertsAllCommit verifies that N goroutines each calling
// InsertTokenUsage with unique sourceIDs all commit successfully, with no lost updates.
func TestTxnConcurrentInsertsAllCommit(t *testing.T) {
	db := testDB(t)
	const N = 40

	if err := db.UpsertSession("sess-concurrent", event.PlatformClaude, time.Now()); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(N)
	errs := make([]error, N)
	for i := range N {
		go func(i int) {
			defer wg.Done()
			ts := time.Now().Add(time.Duration(i) * time.Microsecond)
			errs[i] = db.InsertTokenUsage(
				"agent-1", "sess-concurrent",
				100+i, 50+i, 0, 0,
				"claude-sonnet-4-6", 0.01,
				ts, fmt.Sprintf("src-concurrent-%d", i),
			)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: InsertTokenUsage error: %v", i, err)
		}
	}

	n := countRows(t, db, "token_usage")
	if n != N {
		t.Errorf("token_usage rows = %d, want %d", n, N)
	}
}

// TestTxnSessionTotalsUpdatedAtomically verifies that after InsertTokenUsage commits,
// session.total_cost_usd and token_usage row are both immediately visible.
func TestTxnSessionTotalsUpdatedAtomically(t *testing.T) {
	db := testDB(t)
	if err := db.UpsertSession("sess-atomic", event.PlatformClaude, time.Now()); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}

	const wantCost = 1.234
	if err := db.InsertTokenUsage("agent-1", "sess-atomic", 1000, 500, 200, 100, "claude-opus-4-7", wantCost, time.Now(), "src-atomic-1"); err != nil {
		t.Fatalf("InsertTokenUsage: %v", err)
	}

	// Both token_usage and sessions must reflect the insert immediately.
	usageRows := countRows(t, db, "token_usage")
	if usageRows != 1 {
		t.Errorf("token_usage rows = %d, want 1", usageRows)
	}

	sess, ok, err := db.GetSessionByIDPrefix("sess-atomic")
	if err != nil || !ok {
		t.Fatalf("GetSession: ok=%v err=%v", ok, err)
	}
	if sess.TotalCostUSD < wantCost-0.001 || sess.TotalCostUSD > wantCost+0.001 {
		t.Errorf("session TotalCostUSD = %.4f, want %.4f", sess.TotalCostUSD, wantCost)
	}
	// sessions.total_input_tokens stores only raw inputTokens (not cache totals).
	if sess.TotalInputTokens != 1000 {
		t.Errorf("session TotalInputTokens = %d, want 1000", sess.TotalInputTokens)
	}
}

// TestTxnCleanOldSessionsCascadesCompletely verifies that CleanOldSessions removes
// the session row, all token_usage rows, tool_calls, and file_changes in one
// atomic transaction, leaving nothing behind for the deleted session.
func TestTxnCleanOldSessionsCascadesCompletely(t *testing.T) {
	db := testDB(t)
	old := time.Now().AddDate(0, 0, -10) // 10 days ago

	if err := db.UpsertSession("sess-old", event.PlatformClaude, old); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}
	if err := db.EndSession("sess-old", old.Add(time.Minute)); err != nil {
		t.Fatalf("EndSession: %v", err)
	}

	// Add token usage, a tool call, and a file change.
	if err := db.InsertTokenUsage("agent-1", "sess-old", 100, 50, 0, 0, "sonnet", 0.10, old, "src-old-1"); err != nil {
		t.Fatalf("InsertTokenUsage: %v", err)
	}
	callID := "call-old-1"
	if _, err := db.InsertToolCallStart(callID, "agent-1", "sess-old", "Read", "file.go", old); err != nil {
		t.Fatalf("InsertToolCallStart: %v", err)
	}
	if err := db.InsertFileChange("sess-old", "/tmp/old.go", event.FileEdit, old); err != nil {
		t.Fatalf("InsertFileChange: %v", err)
	}

	// Also add a recent session that must NOT be deleted.
	if err := db.UpsertSession("sess-recent", event.PlatformClaude, time.Now()); err != nil {
		t.Fatalf("UpsertSession recent: %v", err)
	}
	if err := db.InsertTokenUsage("agent-1", "sess-recent", 200, 100, 0, 0, "sonnet", 0.20, time.Now(), "src-recent-1"); err != nil {
		t.Fatalf("InsertTokenUsage recent: %v", err)
	}

	n, err := db.CleanOldSessions(5) // delete sessions older than 5 days
	if err != nil {
		t.Fatalf("CleanOldSessions: %v", err)
	}
	if n != 1 {
		t.Errorf("CleanOldSessions deleted %d sessions, want 1", n)
	}

	// sess-old must be gone from all tables.
	sessions, _ := db.ListSessions()
	for _, s := range sessions {
		if s.SessionID == "sess-old" {
			t.Error("sess-old still present in sessions after CleanOldSessions")
		}
	}

	// token_usage, tool_calls, file_changes must only have the recent session's rows.
	usageRows := countRows(t, db, "token_usage")
	if usageRows != 1 {
		t.Errorf("token_usage rows = %d, want 1 (only recent session)", usageRows)
	}
	toolRows := countRows(t, db, "tool_calls")
	if toolRows != 0 {
		t.Errorf("tool_calls rows = %d, want 0", toolRows)
	}
	fileRows := countRows(t, db, "file_changes")
	if fileRows != 0 {
		t.Errorf("file_changes rows = %d, want 0", fileRows)
	}
}

// TestTxnCleanOldSessionsRollbackOnError verifies that when CleanOldSessions
// encounters an error mid-transaction, it rolls back completely — leaving the
// session and all its related records intact.
func TestTxnCleanOldSessionsRollbackOnError(t *testing.T) {
	db := testDB(t)
	old := time.Now().AddDate(0, 0, -10)

	if err := db.UpsertSession("sess-abort", event.PlatformClaude, old); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}
	if err := db.EndSession("sess-abort", old.Add(time.Minute)); err != nil {
		t.Fatalf("EndSession: %v", err)
	}
	if err := db.InsertTokenUsage("agent-1", "sess-abort", 100, 50, 0, 0, "sonnet", 0.10, old, "src-abort-1"); err != nil {
		t.Fatalf("InsertTokenUsage: %v", err)
	}
	// Record pre-trigger counts.
	beforeSessions := countRows(t, db, "sessions")
	beforeUsage := countRows(t, db, "token_usage")

	// Install a trigger that fires on DELETE from token_usage and aborts the tx.
	execRaw(t, db, `
		CREATE TRIGGER txn_test_abort_token_delete
		BEFORE DELETE ON token_usage
		BEGIN
			SELECT RAISE(ABORT, 'txn_test: abort on token_usage delete');
		END
	`)
	t.Cleanup(func() {
		db.db.Exec("DROP TRIGGER IF EXISTS txn_test_abort_token_delete") //nolint:errcheck
	})

	_, err := db.CleanOldSessions(5)
	if err == nil {
		t.Fatal("expected CleanOldSessions to fail due to trigger, got nil error")
	}

	// All rows must still be present (rolled back).
	afterSessions := countRows(t, db, "sessions")
	afterUsage := countRows(t, db, "token_usage")

	if afterSessions != beforeSessions {
		t.Errorf("sessions count changed: before=%d after=%d — rollback failed", beforeSessions, afterSessions)
	}
	if afterUsage != beforeUsage {
		t.Errorf("token_usage count changed: before=%d after=%d — rollback failed", beforeUsage, afterUsage)
	}
}

// TestTxnWALReaderSeesPreWriteSnapshot verifies that under WAL mode a reader
// that starts a transaction before a writer commits sees the pre-write snapshot,
// not dirty data from the in-progress write.
//
// SQLite WAL mode guarantees snapshot isolation: readers see a consistent view
// of the database as it was when their transaction began, even if a concurrent
// writer commits new data after that point.
//
// Implementation note: storage.Open uses SetMaxOpenConns(1), so we open two
// separate *DB handles pointing to the same file — readerDB for the snapshot
// transaction and writerDB for the commit. WAL mode allows concurrent readers
// and one writer across different connections.
func TestTxnWALReaderSeesPreWriteSnapshot(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "wal_snapshot.db")

	// writerDB performs writes via the normal storage API.
	writerDB, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open writerDB: %v", err)
	}
	t.Cleanup(func() { _ = writerDB.Close() })

	if err := writerDB.UpsertSession("sess-wal", event.PlatformClaude, time.Now()); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}

	// readerDB is a separate connection for the snapshot read transaction.
	readerDB, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open readerDB: %v", err)
	}
	t.Cleanup(func() { _ = readerDB.Close() })

	// Reader begins a transaction — this fixes its snapshot point in WAL mode.
	readTx, err := readerDB.db.Begin()
	if err != nil {
		t.Fatalf("begin read tx: %v", err)
	}
	defer func() { _ = readTx.Rollback() }()

	var countBefore int
	if err := readTx.QueryRow("SELECT COUNT(*) FROM token_usage").Scan(&countBefore); err != nil {
		t.Fatalf("count before: %v", err)
	}

	// Writer commits a new row into the file.
	if err := writerDB.InsertTokenUsage("agent-1", "sess-wal", 100, 50, 0, 0, "sonnet", 0.01, time.Now(), "src-wal-1"); err != nil {
		t.Fatalf("InsertTokenUsage (writer): %v", err)
	}

	// The reader must still see the pre-write count within its open snapshot.
	var countDuring int
	if err := readTx.QueryRow("SELECT COUNT(*) FROM token_usage").Scan(&countDuring); err != nil {
		t.Fatalf("count during: %v", err)
	}
	if countDuring != countBefore {
		t.Errorf("reader saw write within its snapshot: before=%d during=%d (expected WAL snapshot isolation)", countBefore, countDuring)
	}

	// After the reader's transaction closes, a fresh read reflects the committed write.
	if err := readTx.Rollback(); err != nil {
		t.Fatalf("rollback read tx: %v", err)
	}
	countAfter := countRows(t, readerDB, "token_usage")
	if countAfter != countBefore+1 {
		t.Errorf("after reader closed: count=%d, want %d", countAfter, countBefore+1)
	}
}
