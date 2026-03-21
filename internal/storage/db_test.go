package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tt-a1i/agmon/internal/event"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenAndMigrate(t *testing.T) {
	db := testDB(t)
	if db == nil {
		t.Fatal("db is nil")
	}
}

func TestSessionCRUD(t *testing.T) {
	db := testDB(t)
	now := time.Now()

	// Create session
	if err := db.UpsertSession("s1", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}

	// List sessions
	sessions, err := db.ListSessions()
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].SessionID != "s1" {
		t.Errorf("session ID: got %q, want %q", sessions[0].SessionID, "s1")
	}
	if sessions[0].Status != "active" {
		t.Errorf("status: got %q, want %q", sessions[0].Status, "active")
	}

	// End session
	if err := db.EndSession("s1", now.Add(time.Hour)); err != nil {
		t.Fatalf("end session: %v", err)
	}

	sessions, _ = db.ListSessions()
	if sessions[0].Status != "ended" {
		t.Errorf("status after end: got %q, want %q", sessions[0].Status, "ended")
	}

	// Active count
	count, _ := db.GetActiveSessionCount()
	if count != 0 {
		t.Errorf("active count: got %d, want 0", count)
	}
}

func TestToolCallCRUD(t *testing.T) {
	db := testDB(t)
	now := time.Now()

	db.UpsertSession("s1", event.PlatformClaude, now)

	// Insert tool call
	if err := db.InsertToolCallStart("tc1", "a1", "s1", "Edit", "src/main.go", now); err != nil {
		t.Fatalf("insert tool call: %v", err)
	}

	// Update tool call
	if err := db.UpdateToolCallEnd("tc1", "ok", event.StatusSuccess, 1200, now.Add(time.Second)); err != nil {
		t.Fatalf("update tool call: %v", err)
	}

	// List
	calls, err := db.ListToolCalls("s1", 10)
	if err != nil {
		t.Fatalf("list tool calls: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if calls[0].ToolName != "Edit" {
		t.Errorf("tool name: got %q, want %q", calls[0].ToolName, "Edit")
	}
	if calls[0].DurationMs != 1200 {
		t.Errorf("duration: got %d, want 1200", calls[0].DurationMs)
	}
	if calls[0].Status != "success" {
		t.Errorf("status: got %q, want %q", calls[0].Status, "success")
	}
}

func TestTokenUsage(t *testing.T) {
	db := testDB(t)
	now := time.Now()

	db.UpsertSession("s1", event.PlatformClaude, now)

	if err := db.InsertTokenUsage("a1", "s1", 1000, 500, 0, 0, "sonnet", 0, now, "test-src-1"); err != nil {
		t.Fatalf("insert token usage: %v", err)
	}

	if err := db.UpdateSessionTokens("s1"); err != nil {
		t.Fatalf("update session tokens: %v", err)
	}

	sessions, _ := db.ListSessions()
	if sessions[0].TotalInputTokens != 1000 {
		t.Errorf("input tokens: got %d, want 1000", sessions[0].TotalInputTokens)
	}
	if sessions[0].TotalOutputTokens != 500 {
		t.Errorf("output tokens: got %d, want 500", sessions[0].TotalOutputTokens)
	}

	in, out, err := db.GetTodayTokens()
	if err != nil {
		t.Fatalf("get today tokens: %v", err)
	}
	if in != 1000 {
		t.Errorf("today input: got %d, want 1000", in)
	}
	if out != 500 {
		t.Errorf("today output: got %d, want 500", out)
	}

	// Dedup: inserting same source_id again should be a no-op.
	db.InsertTokenUsage("a1", "s1", 999, 999, 0, 0, "sonnet", 0, now, "test-src-1")
	db.UpdateSessionTokens("s1")
	sessions, _ = db.ListSessions()
	if sessions[0].TotalInputTokens != 1000 {
		t.Errorf("dedup failed: input tokens changed to %d", sessions[0].TotalInputTokens)
	}
}

func TestAgentHierarchy(t *testing.T) {
	db := testDB(t)
	now := time.Now()

	db.UpsertSession("s1", event.PlatformClaude, now)
	db.UpsertAgent("main", "s1", "", "main-agent", now)
	db.UpsertAgent("sub1", "s1", "main", "reviewer", now.Add(time.Second))

	agents, err := db.ListAgents("s1")
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}
	if agents[1].ParentAgentID != "main" {
		t.Errorf("parent: got %q, want %q", agents[1].ParentAgentID, "main")
	}
}

func TestFileChanges(t *testing.T) {
	db := testDB(t)
	now := time.Now()

	db.UpsertSession("s1", event.PlatformClaude, now)

	db.InsertFileChange("s1", "src/main.go", event.FileCreate, now)
	db.InsertFileChange("s1", "src/util.go", event.FileEdit, now.Add(time.Second))

	changes, err := db.ListFileChanges("s1")
	if err != nil {
		t.Fatalf("list file changes: %v", err)
	}
	if len(changes) != 2 {
		t.Fatalf("expected 2 changes, got %d", len(changes))
	}
	if changes[0].ChangeType != "create" {
		t.Errorf("change type: got %q, want %q", changes[0].ChangeType, "create")
	}
}

func TestCleanOldSessions(t *testing.T) {
	db := testDB(t)
	now := time.Now()

	// Old ended session (10 days ago) — should be deleted.
	db.UpsertSession("old-ended", event.PlatformClaude, now.AddDate(0, 0, -10))
	db.InsertTokenUsage("a1", "old-ended", 100, 50, 0, 0, "sonnet", 0.01, now.AddDate(0, 0, -10), "src-old")
	db.UpdateSessionTokens("old-ended")
	db.EndSession("old-ended", now.AddDate(0, 0, -10))

	// Recent ended session (2 days ago) — should survive.
	db.UpsertSession("recent-ended", event.PlatformClaude, now.AddDate(0, 0, -2))
	db.InsertTokenUsage("a1", "recent-ended", 200, 100, 0, 0, "sonnet", 0.02, now.AddDate(0, 0, -2), "src-recent")
	db.UpdateSessionTokens("recent-ended")
	db.EndSession("recent-ended", now.AddDate(0, 0, -2))

	// Old but still active session — must NOT be deleted regardless of age.
	db.UpsertSession("old-active", event.PlatformClaude, now.AddDate(0, 0, -10))
	db.InsertTokenUsage("a1", "old-active", 300, 150, 0, 0, "sonnet", 0.03, now.AddDate(0, 0, -10), "src-active")
	db.UpdateSessionTokens("old-active")

	n, err := db.CleanOldSessions(7)
	if err != nil {
		t.Fatalf("clean: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 session deleted, got %d", n)
	}

	sessions, _ := db.ListSessions()
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions remaining, got %d", len(sessions))
	}
	for _, s := range sessions {
		if s.SessionID == "old-ended" {
			t.Errorf("old-ended session should have been deleted")
		}
	}
}

func TestDefaultDBPath(t *testing.T) {
	path := DefaultDBPath()
	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".agmon", "data", "agmon.db")
	if path != expected {
		t.Errorf("default path: got %q, want %q", path, expected)
	}
}
