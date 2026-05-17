package storage

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

// legacySchema is a "pre-addColumnIfMissing" snapshot of the 5 core tables.
// It includes all columns that existed in the original CREATE TABLE statements
// (end_time, status, last_event_time, etc.) but omits the columns subsequently
// added by addColumnIfMissing (cwd, git_branch, tag, model, source_id, …).
// normalizeTimeColumns() requires end_time and last_event_time to be present,
// so those are included here.
const legacySchema = `
	CREATE TABLE sessions (
		session_id          TEXT PRIMARY KEY,
		platform            TEXT NOT NULL,
		start_time          TEXT NOT NULL,
		last_event_time     TEXT NOT NULL DEFAULT '',
		end_time            TEXT,
		status              TEXT NOT NULL DEFAULT 'active',
		total_input_tokens  INTEGER NOT NULL DEFAULT 0,
		total_output_tokens INTEGER NOT NULL DEFAULT 0,
		total_cost_usd      REAL NOT NULL DEFAULT 0
	);
	CREATE TABLE agents (
		agent_id        TEXT PRIMARY KEY,
		session_id      TEXT NOT NULL,
		parent_agent_id TEXT,
		role            TEXT,
		status          TEXT NOT NULL DEFAULT 'active',
		start_time      TEXT NOT NULL,
		end_time        TEXT
	);
	CREATE TABLE tool_calls (
		call_id        TEXT PRIMARY KEY,
		agent_id       TEXT NOT NULL,
		session_id     TEXT NOT NULL,
		tool_name      TEXT NOT NULL,
		params_summary TEXT,
		result_summary TEXT,
		start_time     TEXT NOT NULL,
		end_time       TEXT,
		duration_ms    INTEGER,
		status         TEXT NOT NULL DEFAULT 'pending'
	);
	CREATE TABLE token_usage (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		agent_id      TEXT NOT NULL,
		session_id    TEXT NOT NULL,
		input_tokens  INTEGER NOT NULL DEFAULT 0,
		output_tokens INTEGER NOT NULL DEFAULT 0,
		model         TEXT,
		cost_usd      REAL NOT NULL DEFAULT 0,
		timestamp     TEXT NOT NULL
	);
	CREATE TABLE file_changes (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id  TEXT NOT NULL,
		file_path   TEXT NOT NULL,
		change_type TEXT NOT NULL,
		timestamp   TEXT NOT NULL
	);
`

// tableExists reports whether a table is present in sqlite_master.
func tableExists(t *testing.T, rawDB *sql.DB, name string) bool {
	t.Helper()
	var count int
	err := rawDB.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name,
	).Scan(&count)
	if err != nil {
		t.Fatalf("tableExists(%s): %v", name, err)
	}
	return count > 0
}

// columnExistsRaw reports whether a column exists in a table via a raw *sql.DB.
func columnExistsRaw(t *testing.T, rawDB *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := rawDB.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("PRAGMA table_info(%s): %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan table_info: %v", err)
		}
		if name == column {
			return true
		}
	}
	return false
}

// indexExists reports whether a named index exists in sqlite_master.
func indexExists(t *testing.T, rawDB *sql.DB, name string) bool {
	t.Helper()
	var count int
	err := rawDB.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, name,
	).Scan(&count)
	if err != nil {
		t.Fatalf("indexExists(%s): %v", name, err)
	}
	return count > 0
}

// dumpSchema returns the sorted sql DDL strings from sqlite_master (for idempotency comparison).
func dumpSchema(t *testing.T, rawDB *sql.DB) string {
	t.Helper()
	rows, err := rawDB.Query(
		`SELECT COALESCE(sql,'') FROM sqlite_master WHERE sql IS NOT NULL ORDER BY type, name`,
	)
	if err != nil {
		t.Fatalf("dump schema: %v", err)
	}
	defer rows.Close()
	var result string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan schema: %v", err)
		}
		result += s + "\n"
	}
	return result
}

// openRaw opens the SQLite file directly without running migrate().
func openRaw(t *testing.T, path string) *sql.DB {
	t.Helper()
	rawDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("openRaw(%s): %v", path, err)
	}
	t.Cleanup(func() { rawDB.Close() })
	return rawDB
}

// TestMigrateCreatesAllTables verifies that a fresh storage.Open creates all
// required tables.
func TestMigrateCreatesAllTables(t *testing.T) {
	db := testDB(t)

	rawDB := openRaw(t, db.path)

	requiredTables := []string{
		"sessions", "agents", "tool_calls", "token_usage", "file_changes",
	}
	for _, tbl := range requiredTables {
		if !tableExists(t, rawDB, tbl) {
			t.Errorf("table %q not created by migrate()", tbl)
		}
	}
}

// TestMigrateIsIdempotent verifies that opening the same database path four
// times in a row neither errors nor corrupts the schema.
func TestMigrateIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "idempotent.db")

	// First open — creates schema.
	db1, err := Open(dbPath)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}

	// Capture schema after first open.
	raw1 := openRaw(t, dbPath)
	schemaBefore := dumpSchema(t, raw1)
	raw1.Close()

	// Reopen three more times.
	for i := range 3 {
		db, err := Open(dbPath)
		if err != nil {
			t.Fatalf("open #%d: %v", i+2, err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("close #%d: %v", i+2, err)
		}
	}

	// Schema must be identical after repeated opens.
	raw2 := openRaw(t, dbPath)
	schemaAfter := dumpSchema(t, raw2)
	raw2.Close()

	if schemaBefore != schemaAfter {
		t.Errorf("schema changed across multiple opens:\nbefore:\n%s\nafter:\n%s", schemaBefore, schemaAfter)
	}
}

// TestMigrateAddsMissingColumnsToOldDB is the key upgrade test.
// It creates a minimal "legacy" sessions table (only session_id, platform, start_time),
// inserts a row, then runs storage.Open to trigger migrate(). It verifies:
//  1. New columns are added (cwd, git_branch, tag, model, latest_context_tokens, …)
//  2. The original data row is preserved
//  3. New columns carry correct default values for existing rows
func TestMigrateAddsMissingColumnsToOldDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy.db")

	// ── Step 1: Create a "legacy" database ────────────────────────────────────
	// Uses legacySchema: all original CREATE TABLE columns present, but
	// addColumnIfMissing columns (cwd, git_branch, tag, model, source_id, …) absent.
	legacyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}

	if _, err := legacyDB.Exec(legacySchema); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}

	// Insert an old-format session row.
	if _, err := legacyDB.Exec(
		`INSERT INTO sessions (session_id, platform, start_time) VALUES ('legacy-sess-1', 'claude', '2024-01-01T00:00:00.000000000Z')`,
	); err != nil {
		t.Fatalf("insert legacy session: %v", err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	// ── Step 2: storage.Open — triggers migrate() with addColumnIfMissing ──────
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("storage.Open on legacy db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// ── Step 3: Verify new columns were added ───────────────────────────────────
	rawDB := openRaw(t, dbPath)
	newCols := []string{"cwd", "git_branch", "tag", "model", "latest_context_tokens", "total_cache_read_tokens", "total_cache_creation_tokens"}
	for _, col := range newCols {
		if !columnExistsRaw(t, rawDB, "sessions", col) {
			t.Errorf("column sessions.%s not added by migrate()", col)
		}
	}
	tokenUsageNewCols := []string{"source_id", "cache_creation_tokens", "cache_read_tokens"}
	for _, col := range tokenUsageNewCols {
		if !columnExistsRaw(t, rawDB, "token_usage", col) {
			t.Errorf("column token_usage.%s not added by migrate()", col)
		}
	}
	if !columnExistsRaw(t, rawDB, "file_changes", "source_id") {
		t.Error("column file_changes.source_id not added by migrate()")
	}

	// ── Step 4: Verify the original data row is preserved ──────────────────────
	sessions, err := db.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions after migration: %v", err)
	}
	found := false
	for _, s := range sessions {
		if s.SessionID == "legacy-sess-1" {
			found = true
			break
		}
	}
	if !found {
		t.Error("legacy row 'legacy-sess-1' was lost during migration")
	}

	// ── Step 5: Verify new columns have correct defaults on the legacy row ──────
	var cwd, gitBranch, tag, model string
	var latestCtx, cacheRead, cacheCreate int
	err = rawDB.QueryRow(
		`SELECT COALESCE(cwd,''), COALESCE(git_branch,''), COALESCE(tag,''), COALESCE(model,''),
		        COALESCE(latest_context_tokens,0), COALESCE(total_cache_read_tokens,0), COALESCE(total_cache_creation_tokens,0)
		   FROM sessions WHERE session_id = 'legacy-sess-1'`,
	).Scan(&cwd, &gitBranch, &tag, &model, &latestCtx, &cacheRead, &cacheCreate)
	if err != nil {
		t.Fatalf("read defaults for legacy row: %v", err)
	}
	if cwd != "" {
		t.Errorf("default cwd = %q, want ''", cwd)
	}
	if gitBranch != "" {
		t.Errorf("default git_branch = %q, want ''", gitBranch)
	}
	if tag != "" {
		t.Errorf("default tag = %q, want ''", tag)
	}
	if model != "" {
		t.Errorf("default model = %q, want ''", model)
	}
	if latestCtx != 0 {
		t.Errorf("default latest_context_tokens = %d, want 0", latestCtx)
	}
}

// TestMigratePreservesDataOnReopens inserts 5 sessions, closes, reopens, and
// verifies all 5 rows survive.
func TestMigratePreservesDataOnReopens(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "reopen.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	ts := time.Now()
	for i := range 5 {
		sid := fmt.Sprintf("reopen-sess-%d", i)
		if err := db.UpsertSession(sid, event.PlatformClaude, ts.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("upsert %s: %v", sid, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopen — triggers migrate() again.
	db2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { db2.Close() })

	sessions, err := db2.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions after reopen: %v", err)
	}
	if len(sessions) != 5 {
		t.Errorf("sessions after reopen = %d, want 5", len(sessions))
	}
}

// TestMigrateAddColumnIfMissingSkipsExisting verifies that running migrate()
// on an already-migrated database leaves the schema byte-for-byte identical
// (the addColumnIfMissing "skip" path fires for every column).
func TestMigrateAddColumnIfMissingSkipsExisting(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "skip.db")

	db1, err := Open(dbPath)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}

	rawBefore := openRaw(t, dbPath)
	schemaBefore := dumpSchema(t, rawBefore)
	rawBefore.Close()

	// Second open — all columns already exist, addColumnIfMissing must skip all.
	db2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	if err := db2.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}

	rawAfter := openRaw(t, dbPath)
	schemaAfter := dumpSchema(t, rawAfter)
	rawAfter.Close()

	if schemaBefore != schemaAfter {
		t.Errorf("schema changed on second open (addColumnIfMissing did not skip):\nbefore:\n%s\nafter:\n%s",
			schemaBefore, schemaAfter)
	}
}

// TestMigrateCreatesAllExpectedIndexes verifies that every index declared in
// migrate() is present after a fresh Open.
func TestMigrateCreatesAllExpectedIndexes(t *testing.T) {
	db := testDB(t)
	rawDB := openRaw(t, db.path)

	expectedIndexes := []string{
		"idx_agents_session",
		"idx_tool_calls_session",
		"idx_tool_calls_agent",
		"idx_tool_calls_start",
		"idx_token_usage_session",
		"idx_token_usage_ts",
		"idx_daily_cost_day",
		"idx_file_changes_session",
		"idx_file_changes_ts",
		"idx_budgets_platform",
		"idx_token_usage_source",
		"idx_file_changes_source",
	}
	for _, idx := range expectedIndexes {
		if !indexExists(t, rawDB, idx) {
			t.Errorf("index %q not found after migrate()", idx)
		}
	}
}

// TestMigrateColumnDefaultsApplied verifies that a row inserted with only the
// primary key populated reads back correct default values for all new columns.
func TestMigrateColumnDefaultsApplied(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "defaults.db")

	// Create a legacy schema without the addColumnIfMissing columns.
	legacyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy: %v", err)
	}
	if _, err := legacyDB.Exec(legacySchema); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close legacy: %v", err)
	}

	// Trigger migration (adds all the new columns with defaults).
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Insert a bare row via raw SQL — relies on column defaults.
	rawDB := openRaw(t, dbPath)
	if _, err := rawDB.Exec(
		`INSERT INTO sessions (session_id, platform, start_time) VALUES ('defaults-sess', 'claude', '2026-01-01T00:00:00.000000000Z')`,
	); err != nil {
		t.Fatalf("insert bare row: %v", err)
	}

	// Read back and verify all added columns carry their declared defaults.
	type defaults struct {
		cwd                      string
		gitBranch                string
		tag                      string
		model                    string
		latestContextTokens      int
		latestTokenTime          string
		totalCacheReadTokens     int
		totalCacheCreationTokens int
	}
	var d defaults
	err = rawDB.QueryRow(`
		SELECT cwd, git_branch, tag, model,
		       latest_context_tokens, latest_token_time,
		       total_cache_read_tokens, total_cache_creation_tokens
		  FROM sessions WHERE session_id = 'defaults-sess'
	`).Scan(&d.cwd, &d.gitBranch, &d.tag, &d.model,
		&d.latestContextTokens, &d.latestTokenTime,
		&d.totalCacheReadTokens, &d.totalCacheCreationTokens)
	if err != nil {
		t.Fatalf("read defaults row: %v", err)
	}

	if d.cwd != "" {
		t.Errorf("cwd default = %q, want ''", d.cwd)
	}
	if d.gitBranch != "" {
		t.Errorf("git_branch default = %q, want ''", d.gitBranch)
	}
	if d.tag != "" {
		t.Errorf("tag default = %q, want ''", d.tag)
	}
	if d.model != "" {
		t.Errorf("model default = %q, want ''", d.model)
	}
	if d.latestContextTokens != 0 {
		t.Errorf("latest_context_tokens default = %d, want 0", d.latestContextTokens)
	}
	if d.latestTokenTime != "" {
		t.Errorf("latest_token_time default = %q, want ''", d.latestTokenTime)
	}
	if d.totalCacheReadTokens != 0 {
		t.Errorf("total_cache_read_tokens default = %d, want 0", d.totalCacheReadTokens)
	}
	if d.totalCacheCreationTokens != 0 {
		t.Errorf("total_cache_creation_tokens default = %d, want 0", d.totalCacheCreationTokens)
	}
}

// TestMigrateUserVersionTracking verifies that:
//  1. A freshly opened database has user_version = schemaVersion (1).
//  2. Reopening does not bump the version beyond schemaVersion.
//  3. A database whose user_version is 0 gets updated to schemaVersion on first open.
func TestMigrateUserVersionTracking(t *testing.T) {
	const schemaVersion = 1

	// ── Case 1: fresh DB ────────────────────────────────────────────────────────
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "version.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	rawDB := openRaw(t, dbPath)
	var ver int
	if err := rawDB.QueryRow("PRAGMA user_version").Scan(&ver); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if ver != schemaVersion {
		t.Errorf("fresh db user_version = %d, want %d", ver, schemaVersion)
	}
	rawDB.Close()

	// ── Case 2: reopen must not downgrade or exceed schemaVersion ────────────────
	db2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if err := db2.Close(); err != nil {
		t.Fatalf("reopen close: %v", err)
	}

	rawDB2 := openRaw(t, dbPath)
	var ver2 int
	if err := rawDB2.QueryRow("PRAGMA user_version").Scan(&ver2); err != nil {
		t.Fatalf("read user_version after reopen: %v", err)
	}
	if ver2 != schemaVersion {
		t.Errorf("after reopen user_version = %d, want %d", ver2, schemaVersion)
	}
	rawDB2.Close()

	// ── Case 3: database with user_version=0 gets upgraded ──────────────────────
	dir3 := t.TempDir()
	dbPath3 := filepath.Join(dir3, "version0.db")

	// Create a minimal schema with user_version intentionally left at 0.
	legacyDB, err := sql.Open("sqlite", dbPath3)
	if err != nil {
		t.Fatalf("open v0 db: %v", err)
	}
	if _, err := legacyDB.Exec(legacySchema); err != nil {
		t.Fatalf("create v0 schema: %v", err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close v0 db: %v", err)
	}

	db3, err := Open(dbPath3)
	if err != nil {
		t.Fatalf("Open v0 db: %v", err)
	}
	if err := db3.Close(); err != nil {
		t.Fatalf("Close v0 db: %v", err)
	}

	rawDB3 := openRaw(t, dbPath3)
	var ver3 int
	if err := rawDB3.QueryRow("PRAGMA user_version").Scan(&ver3); err != nil {
		t.Fatalf("read user_version v0: %v", err)
	}
	rawDB3.Close()
	if ver3 != schemaVersion {
		t.Errorf("v0 db after upgrade user_version = %d, want %d", ver3, schemaVersion)
	}
}
