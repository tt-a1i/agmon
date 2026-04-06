package storage

import (
	"database/sql"
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

	// A token-less ended session is intentionally hidden from ListSessions.
	// Use GetSessionByIDPrefix for an authoritative status check.
	ended, found, err := db.GetSessionByIDPrefix("s1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if !found {
		t.Fatal("session not found after end")
	}
	if ended.Status != "ended" {
		t.Errorf("status after end: got %q, want %q", ended.Status, "ended")
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

func TestToolCallDurationAutoCalculated(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	db.UpsertSession("s1", event.PlatformClaude, now)

	// Insert tool call start
	if err := db.InsertToolCallStart("tc-auto", "a1", "s1", "Bash", "ls", now); err != nil {
		t.Fatalf("insert tool call: %v", err)
	}

	// End with durationMs=0 (as Claude/Codex hooks actually send) — should auto-calculate
	endTime := now.Add(3500 * time.Millisecond)
	if err := db.UpdateToolCallEnd("tc-auto", "ok", event.StatusSuccess, 0, endTime); err != nil {
		t.Fatalf("update tool call: %v", err)
	}

	calls, err := db.ListToolCalls("s1", 10)
	if err != nil {
		t.Fatalf("list tool calls: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}

	// julianday arithmetic may have slight rounding; accept ±500ms tolerance
	if calls[0].DurationMs < 3000 || calls[0].DurationMs > 4000 {
		t.Errorf("auto-calculated duration: got %d ms, want ~3500", calls[0].DurationMs)
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

func TestInsertTokenUsageUpdatesSessionTotalsIncrementally(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	if err := db.UpsertSession("s1", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.InsertTokenUsage("a1", "s1", 1200, 300, 40, 20, "sonnet", 2.5, now, "src-1"); err != nil {
		t.Fatalf("insert token usage: %v", err)
	}

	sessions, err := db.ListSessions()
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].TotalInputTokens != 1200 || sessions[0].TotalOutputTokens != 300 {
		t.Fatalf("unexpected incremental totals: in=%d out=%d", sessions[0].TotalInputTokens, sessions[0].TotalOutputTokens)
	}
	if sessions[0].TotalCacheCreationTokens != 40 || sessions[0].TotalCacheReadTokens != 20 {
		t.Fatalf("unexpected cache totals: create=%d read=%d", sessions[0].TotalCacheCreationTokens, sessions[0].TotalCacheReadTokens)
	}
	if sessions[0].LatestContextTokens != 1200 {
		t.Fatalf("expected latest context tokens to update incrementally, got %d", sessions[0].LatestContextTokens)
	}
	if sessions[0].Model != "sonnet" {
		t.Fatalf("expected model to update incrementally, got %q", sessions[0].Model)
	}

	if err := db.InsertTokenUsage("a1", "s1", 999, 999, 0, 0, "sonnet", 99, now, "src-1"); err != nil {
		t.Fatalf("duplicate insert token usage: %v", err)
	}
	sessions, err = db.ListSessions()
	if err != nil {
		t.Fatalf("list sessions after dedup: %v", err)
	}
	if sessions[0].TotalInputTokens != 1200 || sessions[0].TotalOutputTokens != 300 {
		t.Fatalf("duplicate insert should not change totals, got in=%d out=%d", sessions[0].TotalInputTokens, sessions[0].TotalOutputTokens)
	}
}

func TestUpdateSessionTokensReconcilesSessionTotals(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	if err := db.UpsertSession("s1", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.InsertTokenUsage("a1", "s1", 500, 100, 0, 0, "", 0, now, "src-1"); err != nil {
		t.Fatalf("insert first token usage: %v", err)
	}
	if err := db.InsertTokenUsage("a1", "s1", 900, 250, 0, 0, "sonnet", 1.2, now.Add(time.Minute), "src-2"); err != nil {
		t.Fatalf("insert second token usage: %v", err)
	}

	if _, err := db.db.Exec(`
		UPDATE sessions SET
			total_input_tokens = 0,
			total_output_tokens = 0,
			total_cost_usd = 0,
			total_cache_read_tokens = 0,
			total_cache_creation_tokens = 0,
			latest_context_tokens = 0,
			latest_token_time = '',
			model = ''
		WHERE session_id = 's1'
	`); err != nil {
		t.Fatalf("corrupt session totals: %v", err)
	}

	if err := db.UpdateSessionTokens("s1"); err != nil {
		t.Fatalf("reconcile session tokens: %v", err)
	}

	sessions, err := db.ListSessions()
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if sessions[0].TotalInputTokens != 1400 || sessions[0].TotalOutputTokens != 350 {
		t.Fatalf("unexpected reconciled totals: in=%d out=%d", sessions[0].TotalInputTokens, sessions[0].TotalOutputTokens)
	}
	if sessions[0].LatestContextTokens != 900 {
		t.Fatalf("expected latest context from newest token row, got %d", sessions[0].LatestContextTokens)
	}
	if sessions[0].Model != "sonnet" {
		t.Fatalf("expected latest non-empty model after reconcile, got %q", sessions[0].Model)
	}
}

func TestBackfillEmptyTokenModelReconcilesSessionTotals(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	if err := db.UpsertSession("s1", event.PlatformCodex, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.InsertTokenUsage("a1", "s1", 800, 120, 0, 0, "", 0, now, "src-1"); err != nil {
		t.Fatalf("insert token usage: %v", err)
	}

	updated, err := db.BackfillEmptyTokenModel("s1", "gpt-5", 1.25, 10.0, 1.25)
	if err != nil {
		t.Fatalf("backfill empty model: %v", err)
	}
	if updated != 1 {
		t.Fatalf("expected 1 backfilled row, got %d", updated)
	}
	if err := db.UpdateSessionTokens("s1"); err != nil {
		t.Fatalf("reconcile after backfill: %v", err)
	}

	sessions, err := db.ListSessions()
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if sessions[0].Model != "gpt-5" {
		t.Fatalf("expected backfilled model to propagate, got %q", sessions[0].Model)
	}
	if sessions[0].TotalCostUSD <= 0 {
		t.Fatalf("expected backfill to recompute cost, got %f", sessions[0].TotalCostUSD)
	}
}

func TestBackfillEmptyTokenModelFullCacheHit(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	if err := db.UpsertSession("s-cache", event.PlatformCodex, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	// input_tokens == cache_read_tokens (full cache hit): regular input should be 0.
	if err := db.InsertTokenUsage("a1", "s-cache", 5000, 200, 0, 5000, "", 0, now, "src-cache"); err != nil {
		t.Fatalf("insert token usage: %v", err)
	}

	// gpt-5.2: input $1.75/M, cache $0.175/M, output $14/M
	updated, err := db.BackfillEmptyTokenModel("s-cache", "gpt-5.2", 1.75, 14.0, 0.175)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if updated != 1 {
		t.Fatalf("expected 1 backfilled row, got %d", updated)
	}
	if err := db.UpdateSessionTokens("s-cache"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	sessions, _ := db.ListSessions()
	var s *SessionRow
	for i := range sessions {
		if sessions[i].SessionID == "s-cache" {
			s = &sessions[i]
			break
		}
	}
	if s == nil {
		t.Fatal("session not found")
	}
	// Expected: 0 * 1.75 + 5000 * 0.175 + 200 * 14.0 = 0 + 875 + 2800 = 3675 / 1M = 0.003675
	wantCost := (float64(5000)*0.175 + float64(200)*14.0) / 1_000_000
	if diff := s.TotalCostUSD - wantCost; diff < -0.0001 || diff > 0.0001 {
		t.Fatalf("cost = %f, want %f (diff %f)", s.TotalCostUSD, wantCost, diff)
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

func TestMarkStaleSessionsEndedUsesRecentActivity(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	if err := db.UpsertSession("s1", event.PlatformClaude, now.Add(-3*time.Hour)); err != nil {
		t.Fatalf("insert old session: %v", err)
	}
	if err := db.UpsertSession("s1", event.PlatformClaude, now.Add(-10*time.Minute)); err != nil {
		t.Fatalf("record recent activity: %v", err)
	}
	if err := db.MarkStaleSessionsEnded(2 * time.Hour); err != nil {
		t.Fatalf("mark stale: %v", err)
	}

	var status string
	var endTime sql.NullString
	if err := db.db.QueryRow(`SELECT status, end_time FROM sessions WHERE session_id = ?`, "s1").Scan(&status, &endTime); err != nil {
		t.Fatalf("query session: %v", err)
	}
	if status != "active" {
		t.Fatalf("recently active session should stay active, got %q", status)
	}
	if endTime.Valid {
		t.Fatalf("recently active session should not have end_time, got %q", endTime.String)
	}
}

func TestUpsertSessionReactivatesStaleSession(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	if err := db.UpsertSession("s1", event.PlatformClaude, now.Add(-3*time.Hour)); err != nil {
		t.Fatalf("insert old session: %v", err)
	}
	if err := db.MarkStaleSessionsEnded(2 * time.Hour); err != nil {
		t.Fatalf("mark stale: %v", err)
	}
	if err := db.UpsertSession("s1", event.PlatformClaude, now); err != nil {
		t.Fatalf("reactivate session: %v", err)
	}

	var status string
	var endTime sql.NullString
	if err := db.db.QueryRow(`SELECT status, end_time FROM sessions WHERE session_id = ?`, "s1").Scan(&status, &endTime); err != nil {
		t.Fatalf("query session: %v", err)
	}
	if status != "active" {
		t.Fatalf("new activity should reactivate session, got %q", status)
	}
	if endTime.Valid {
		t.Fatalf("reactivated session should clear end_time, got %q", endTime.String)
	}
}

func TestPruneEmptyCodexSessions(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	// Phantom Codex session: zero tokens, no tool calls → should be pruned.
	db.UpsertSession("phantom", event.PlatformCodex, now)

	// Real Codex session with tokens → should survive.
	db.UpsertSession("real-tokens", event.PlatformCodex, now)
	db.InsertTokenUsage("", "real-tokens", 100, 10, 0, 0, "gpt-5", 0.01, now, "tu-1")

	// Real Codex session with tool calls but no tokens → should survive.
	db.UpsertSession("real-tools", event.PlatformCodex, now)
	db.InsertToolCallStart("tc-1", "", "real-tools", "shell", "{}", now)

	// Claude session with zero tokens → should NOT be pruned (wrong platform).
	db.UpsertSession("claude-empty", event.PlatformClaude, now)

	n, err := db.PruneEmptyCodexSessions()
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 pruned session, got %d", n)
	}

	// Verify phantom is gone.
	var count int
	db.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE session_id = 'phantom'`).Scan(&count)
	if count != 0 {
		t.Error("phantom session should be deleted")
	}

	// Verify others survive.
	for _, sid := range []string{"real-tokens", "real-tools", "claude-empty"} {
		db.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE session_id = ?`, sid).Scan(&count)
		if count != 1 {
			t.Errorf("session %q should survive, got count=%d", sid, count)
		}
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

func TestSetSessionTag(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	db.UpsertSession("s1", event.PlatformClaude, now)
	db.InsertTokenUsage("a1", "s1", 100, 50, 0, 0, "sonnet", 0.01, now, "src-tag")

	// Set tag
	if err := db.SetSessionTag("s1", "refactoring auth"); err != nil {
		t.Fatalf("set tag: %v", err)
	}

	sessions, _ := db.ListSessions()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].Tag != "refactoring auth" {
		t.Errorf("tag: got %q, want %q", sessions[0].Tag, "refactoring auth")
	}

	// Clear tag
	if err := db.SetSessionTag("s1", ""); err != nil {
		t.Fatalf("clear tag: %v", err)
	}

	s, found, _ := db.GetSessionByIDPrefix("s1")
	if !found {
		t.Fatal("session not found")
	}
	if s.Tag != "" {
		t.Errorf("tag should be empty after clear, got %q", s.Tag)
	}
}

func TestGetDailyCosts(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.UTC)

	db.UpsertSession("s1", event.PlatformClaude, today)

	// Insert costs for today and yesterday
	db.InsertTokenUsage("a1", "s1", 1000, 500, 0, 0, "sonnet", 1.50, today, "src-today")
	db.InsertTokenUsage("a1", "s1", 800, 400, 0, 0, "sonnet", 0.80, today.AddDate(0, 0, -1), "src-yesterday")

	costs, err := db.GetDailyCosts(7)
	if err != nil {
		t.Fatalf("get daily costs: %v", err)
	}
	if len(costs) != 7 {
		t.Fatalf("expected 7 days, got %d", len(costs))
	}

	// Last entry should be today
	todayEntry := costs[6]
	if todayEntry.Date != today.Format("2006-01-02") {
		t.Errorf("last day: got %q, want %q", todayEntry.Date, today.Format("2006-01-02"))
	}
	if todayEntry.Cost < 1.49 || todayEntry.Cost > 1.51 {
		t.Errorf("today cost: got %f, want ~1.50", todayEntry.Cost)
	}

	// Yesterday
	yesterdayEntry := costs[5]
	if yesterdayEntry.Cost < 0.79 || yesterdayEntry.Cost > 0.81 {
		t.Errorf("yesterday cost: got %f, want ~0.80", yesterdayEntry.Cost)
	}

	// Earlier days should be zero
	for i := 0; i < 5; i++ {
		if costs[i].Cost != 0 {
			t.Errorf("day %d cost: got %f, want 0", i, costs[i].Cost)
		}
	}
}

func TestGetDailyCostsEmpty(t *testing.T) {
	db := testDB(t)

	costs, err := db.GetDailyCosts(7)
	if err != nil {
		t.Fatalf("get daily costs: %v", err)
	}
	if len(costs) != 7 {
		t.Fatalf("expected 7 days, got %d", len(costs))
	}
	for _, c := range costs {
		if c.Cost != 0 {
			t.Errorf("expected zero cost for %s, got %f", c.Date, c.Cost)
		}
	}
}

func TestGetModelCostBreakdown(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.UTC)

	db.UpsertSession("s1", event.PlatformClaude, today)
	db.InsertTokenUsage("a1", "s1", 1000, 500, 0, 0, "claude-sonnet-4-6", 1.50, today, "src-1")
	db.InsertTokenUsage("a1", "s1", 2000, 800, 0, 0, "claude-opus-4-6", 3.20, today, "src-2")
	db.InsertTokenUsage("a1", "s1", 500, 200, 0, 0, "claude-sonnet-4-6", 0.80, today, "src-3")

	from := today.Add(-time.Hour)
	to := today.Add(time.Hour)
	models, err := db.GetModelCostBreakdown(from, to)
	if err != nil {
		t.Fatalf("get model cost breakdown: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	// Ordered by cost DESC: opus first
	if models[0].Model != "claude-opus-4-6" {
		t.Errorf("expected opus first, got %q", models[0].Model)
	}
	if models[0].CostUSD < 3.19 || models[0].CostUSD > 3.21 {
		t.Errorf("opus cost: got %f, want ~3.20", models[0].CostUSD)
	}
	// Sonnet: 1.50 + 0.80 = 2.30
	if models[1].CostUSD < 2.29 || models[1].CostUSD > 2.31 {
		t.Errorf("sonnet cost: got %f, want ~2.30", models[1].CostUSD)
	}
}

func TestGetTopSessionsByCost(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.UTC)

	db.UpsertSession("s1", event.PlatformClaude, today)
	db.UpsertSession("s2", event.PlatformClaude, today)
	db.InsertTokenUsage("a1", "s1", 1000, 500, 0, 0, "sonnet", 1.00, today, "src-1")
	db.InsertTokenUsage("a1", "s2", 2000, 800, 0, 0, "sonnet", 5.00, today, "src-2")

	from := today.Add(-time.Hour)
	to := today.Add(time.Hour)
	top, err := db.GetTopSessionsByCost(from, to, 10)
	if err != nil {
		t.Fatalf("get top sessions: %v", err)
	}
	if len(top) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(top))
	}
	// s2 should be first (higher cost)
	if top[0].SessionID != "s2" {
		t.Errorf("expected s2 first, got %q", top[0].SessionID)
	}
	if top[0].CostUSD < 4.99 || top[0].CostUSD > 5.01 {
		t.Errorf("s2 cost: got %f, want ~5.00", top[0].CostUSD)
	}
}

func TestGetDailyCostsBetween(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.UTC)
	from := today.AddDate(0, 0, -2)
	to := today.AddDate(0, 0, 1)

	db.UpsertSession("s1", event.PlatformClaude, today)
	db.InsertTokenUsage("a1", "s1", 1000, 500, 0, 0, "sonnet", 2.50, today, "src-1")
	db.InsertTokenUsage("a1", "s1", 500, 200, 0, 0, "sonnet", 1.00, today.AddDate(0, 0, -1), "src-2")

	costs, err := db.GetDailyCostsBetween(from, to)
	if err != nil {
		t.Fatalf("get daily costs between: %v", err)
	}
	if len(costs) != 3 {
		t.Fatalf("expected 3 days, got %d", len(costs))
	}
	// First day (2 days ago) should be 0
	if costs[0].Cost != 0 {
		t.Errorf("day 0 cost: got %f, want 0", costs[0].Cost)
	}
	// Yesterday should be ~1.00
	if costs[1].Cost < 0.99 || costs[1].Cost > 1.01 {
		t.Errorf("day 1 cost: got %f, want ~1.00", costs[1].Cost)
	}
	// Today should be ~2.50
	if costs[2].Cost < 2.49 || costs[2].Cost > 2.51 {
		t.Errorf("day 2 cost: got %f, want ~2.50", costs[2].Cost)
	}
}

func TestAllToolStats(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.UTC)

	db.UpsertSession("s1", event.PlatformClaude, today)
	db.InsertToolCallStart("tc1", "a1", "s1", "Read", "file.go", today)
	db.UpdateToolCallEnd("tc1", "ok", event.StatusSuccess, 100, today.Add(100*time.Millisecond))
	db.InsertToolCallStart("tc2", "a1", "s1", "Read", "main.go", today)
	db.UpdateToolCallEnd("tc2", "ok", event.StatusSuccess, 200, today.Add(200*time.Millisecond))
	db.InsertToolCallStart("tc3", "a1", "s1", "Edit", "file.go", today)
	db.UpdateToolCallEnd("tc3", "err", event.StatusFail, 50, today.Add(50*time.Millisecond))

	from := today.Add(-time.Hour)
	to := today.Add(time.Hour)
	stats, err := db.AllToolStats(from, to)
	if err != nil {
		t.Fatalf("all tool stats: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(stats))
	}
	// Read should be first (2 calls vs 1)
	if stats[0].ToolName != "Read" || stats[0].Count != 2 {
		t.Errorf("expected Read with 2 calls, got %q with %d", stats[0].ToolName, stats[0].Count)
	}
	if stats[1].ToolName != "Edit" || stats[1].FailCount != 1 {
		t.Errorf("expected Edit with 1 fail, got %q with %d fails", stats[1].ToolName, stats[1].FailCount)
	}
}

func TestListSessionsShowsActiveAndTokenSessions(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	oldSameDay := now.Truncate(time.Hour).Add(-10 * time.Hour)
	recent := now.Add(-30 * time.Minute)

	// Both sessions are "active" (no tokens). ListSessions now shows all active sessions
	// regardless of age — stale cleanup is the daemon's job, not the query's.
	if err := db.UpsertSession("old", event.PlatformClaude, oldSameDay); err != nil {
		t.Fatalf("insert old session: %v", err)
	}
	if err := db.UpsertSession("recent", event.PlatformClaude, recent); err != nil {
		t.Fatalf("insert recent session: %v", err)
	}

	sessions, err := db.ListSessions()
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 active sessions, got %d", len(sessions))
	}

	// Ended session with no tokens should not appear.
	if err := db.EndSession("old", oldSameDay); err != nil {
		t.Fatalf("end old session: %v", err)
	}
	sessions, _ = db.ListSessions()
	for _, s := range sessions {
		if s.SessionID == "old" {
			t.Error("ended zero-token session should not appear in ListSessions")
		}
	}
}
