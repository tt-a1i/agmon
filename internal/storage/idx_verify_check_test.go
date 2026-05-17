package storage

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// Verify the migrate() call creates the expected indexes (sanity check for P0-2 fix).
func TestMigrateCreatesTimeIndexes(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "idxcheck.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.Close()

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer raw.Close()

	want := map[string]string{
		"idx_token_usage_ts":   "token_usage",
		"idx_tool_calls_start": "tool_calls",
		"idx_file_changes_ts":  "file_changes",
	}
	got := map[string]string{}
	rows, _ := raw.Query("SELECT name, tbl_name FROM sqlite_master WHERE type='index' AND name NOT LIKE 'sqlite_%'")
	defer rows.Close()
	for rows.Next() {
		var name, tbl string
		rows.Scan(&name, &tbl)
		got[name] = tbl
	}
	for name, tbl := range want {
		if got[name] != tbl {
			t.Errorf("missing index %s on %s (got=%v)", name, tbl, got[name])
		}
	}
}
