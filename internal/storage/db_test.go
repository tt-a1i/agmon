package storage

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
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

func TestOpenNormalizesLegacySecondPrecisionTimes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	legacy := "2026-01-02T12:00:00Z"
	if _, err := db.db.Exec(`
		INSERT INTO sessions (session_id, platform, start_time, last_event_time, status)
		VALUES ('legacy', 'claude', ?, ?, 'active')
	`, legacy, legacy); err != nil {
		t.Fatalf("insert legacy session: %v", err)
	}
	// Simulate a pre-normalization DB by rewinding the schema version so the
	// next Open triggers the one-shot normalize step.
	if _, err := db.db.Exec("PRAGMA user_version = 0"); err != nil {
		t.Fatalf("reset user_version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	db, err = Open(path)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer db.Close()

	var startTime, lastEvent string
	if err := db.db.QueryRow(`SELECT start_time, last_event_time FROM sessions WHERE session_id = 'legacy'`).Scan(&startTime, &lastEvent); err != nil {
		t.Fatalf("query legacy session: %v", err)
	}
	want := "2026-01-02T12:00:00.000000000Z"
	if startTime != want || lastEvent != want {
		t.Fatalf("legacy times not normalized: start=%q last=%q want=%q", startTime, lastEvent, want)
	}
}

// TestOpenSkipsNormalizeOnUpToDateSchema verifies the schema-version gate so
// daemon restarts on a healthy DB don't re-scan all rows.
func TestOpenSkipsNormalizeOnUpToDateSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skip.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Insert a row whose timestamp is intentionally NOT in the canonical
	// format. If normalize re-ran, this would be rewritten.
	const odd = "2026-01-02T12:00:00Z"
	if _, err := db.db.Exec(`
		INSERT INTO sessions (session_id, platform, start_time, last_event_time, status)
		VALUES ('keep', 'claude', ?, ?, 'active')
	`, odd, odd); err != nil {
		t.Fatalf("insert: %v", err)
	}
	db.Close()

	// Reopen — schema version is already current, so normalize must be skipped.
	db, err = Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db.Close()

	var start string
	if err := db.db.QueryRow(`SELECT start_time FROM sessions WHERE session_id = 'keep'`).Scan(&start); err != nil {
		t.Fatalf("query: %v", err)
	}
	if start != odd {
		t.Errorf("normalize re-ran on up-to-date schema: got %q want %q", start, odd)
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

func TestGetSessionByIDPrefixPrefersExactMatch(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	if err := db.UpsertSession("s1", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert s1: %v", err)
	}
	if err := db.UpsertSession("s10", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert s10: %v", err)
	}

	sess, found, err := db.GetSessionByIDPrefix("s1")
	if err != nil {
		t.Fatalf("exact match should not be ambiguous: %v", err)
	}
	if !found || sess.SessionID != "s1" {
		t.Fatalf("expected exact s1, found=%v session=%q", found, sess.SessionID)
	}
}

func TestGetSessionByIDPrefixEscapesLikeWildcards(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	if err := db.UpsertSession("abc%literal", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert literal percent: %v", err)
	}
	if err := db.UpsertSession("abcdef", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert abcdef: %v", err)
	}

	sess, found, err := db.GetSessionByIDPrefix("abc%")
	if err != nil {
		t.Fatalf("literal percent prefix should not act as wildcard: %v", err)
	}
	if !found || sess.SessionID != "abc%literal" {
		t.Fatalf("expected literal percent session, found=%v session=%q", found, sess.SessionID)
	}
}

func TestToolCallCRUD(t *testing.T) {
	db := testDB(t)
	now := time.Now()

	db.UpsertSession("s1", event.PlatformClaude, now)

	// Insert tool call
	if _, err := db.InsertToolCallStart("tc1", "a1", "s1", "Edit", "src/main.go", now); err != nil {
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
	if _, err := db.InsertToolCallStart("tc-auto", "a1", "s1", "Bash", "ls", now); err != nil {
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

func TestTokenAggregatesDoNotDoubleCountCacheTokens(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	if err := db.UpsertSession("s-cache-agg", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.UpsertAgent("agent-1", "s-cache-agg", "", "main", now); err != nil {
		t.Fatalf("upsert agent: %v", err)
	}
	// input_tokens is already the total input context, with cache token counts
	// stored separately for pricing/cache-rate displays.
	if err := db.InsertTokenUsage("agent-1", "s-cache-agg", 1500, 500, 200, 300, "sonnet", 0.01134, now, "src-cache-agg"); err != nil {
		t.Fatalf("insert token usage: %v", err)
	}

	from := now.Add(-time.Minute)
	to := now.Add(time.Minute)

	models, err := db.GetModelCostBreakdown(from, to)
	if err != nil {
		t.Fatalf("model breakdown: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model row, got %d", len(models))
	}
	if models[0].InputTokens != 1500 {
		t.Fatalf("model input tokens double-counted cache: got %d, want 1500", models[0].InputTokens)
	}

	topSessions, err := db.GetTopSessionsByCost(from, to, 10)
	if err != nil {
		t.Fatalf("top sessions: %v", err)
	}
	if len(topSessions) != 1 {
		t.Fatalf("expected 1 top session row, got %d", len(topSessions))
	}
	if topSessions[0].InputTokens != 1500 {
		t.Fatalf("top session input tokens double-counted cache: got %d, want 1500", topSessions[0].InputTokens)
	}

	agents, err := db.ListAgentStats("s-cache-agg")
	if err != nil {
		t.Fatalf("agent stats: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent row, got %d", len(agents))
	}
	if agents[0].InputTokens != 1500 {
		t.Fatalf("agent input tokens double-counted cache: got %d, want 1500", agents[0].InputTokens)
	}
}

func TestListAgentStatsMapsEmptyTokenAgentToMainAgent(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	if err := db.UpsertSession("s-empty-agent", event.PlatformCodex, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.UpsertAgent("main-agent", "s-empty-agent", "", "main", now); err != nil {
		t.Fatalf("upsert main agent: %v", err)
	}
	if err := db.InsertTokenUsage("", "s-empty-agent", 1200, 300, 0, 0, "gpt-5-codex", 0.02, now, "src-empty-agent"); err != nil {
		t.Fatalf("insert token usage: %v", err)
	}

	agents, err := db.ListAgentStats("s-empty-agent")
	if err != nil {
		t.Fatalf("list agent stats: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent row, got %d", len(agents))
	}
	if agents[0].AgentID != "main-agent" {
		t.Fatalf("expected main-agent row, got %q", agents[0].AgentID)
	}
	if agents[0].InputTokens != 1200 || agents[0].OutputTokens != 300 {
		t.Fatalf("empty agent token totals were not mapped to main agent: in=%d out=%d", agents[0].InputTokens, agents[0].OutputTokens)
	}
}

func TestListAgentStatsSynthesizesMainAgentForCodexSession(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	if err := db.UpsertSession("s-codex-no-agent", event.PlatformCodex, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if _, err := db.InsertToolCallStart("call-empty", "", "s-codex-no-agent", "apply_patch", "{}", now); err != nil {
		t.Fatalf("insert tool call: %v", err)
	}
	if err := db.InsertTokenUsage("", "s-codex-no-agent", 1200, 300, 0, 0, "gpt-5-codex", 0.02, now, "src-codex-no-agent"); err != nil {
		t.Fatalf("insert token usage: %v", err)
	}

	agents, err := db.ListAgentStats("s-codex-no-agent")
	if err != nil {
		t.Fatalf("list agent stats: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("expected synthetic main row, got %d rows", len(agents))
	}
	if agents[0].AgentID != "main" || agents[0].Role != "main" {
		t.Fatalf("unexpected synthetic row identity: %#v", agents[0])
	}
	if agents[0].ToolCallCount != 1 || agents[0].InputTokens != 1200 || agents[0].OutputTokens != 300 {
		t.Fatalf("synthetic row did not aggregate empty-agent activity: %#v", agents[0])
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

func TestListEmptyModelSessionsSkipsAmbiguousKnownModels(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	if err := db.UpsertSession("s-ambiguous-empty", event.PlatformCodex, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.InsertTokenUsage("", "s-ambiguous-empty", 100, 10, 0, 0, "", 0, now, "amb-empty"); err != nil {
		t.Fatalf("insert empty model row: %v", err)
	}
	if err := db.InsertTokenUsage("", "s-ambiguous-empty", 100, 10, 0, 0, "gpt-5", 0.01, now.Add(time.Second), "amb-gpt5"); err != nil {
		t.Fatalf("insert gpt-5 row: %v", err)
	}
	if err := db.InsertTokenUsage("", "s-ambiguous-empty", 100, 10, 0, 0, "gpt-5.5", 0.02, now.Add(2*time.Second), "amb-gpt55"); err != nil {
		t.Fatalf("insert gpt-5.5 row: %v", err)
	}

	sessions, err := db.ListEmptyModelSessions()
	if err != nil {
		t.Fatalf("list empty model sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected one session candidate, got %d", len(sessions))
	}
	if sessions[0].Model != "" {
		t.Fatalf("ambiguous known models should not select one model, got %q", sessions[0].Model)
	}
}

func TestBackfillRecentCodexTokenModelRepairsLastTokenBeforeContext(t *testing.T) {
	db := testDB(t)
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	contextTime := now.Add(2 * time.Second)

	if err := db.UpsertSession("s-recent-model", event.PlatformCodex, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.InsertTokenUsage("", "s-recent-model", 1000, 100, 0, 200, "", 0, now, "codex-tokens-s-recent-model-1"); err != nil {
		t.Fatalf("insert token usage: %v", err)
	}

	updated, err := db.BackfillRecentCodexTokenModel("s-recent-model", "gpt-5.5", contextTime, 5*time.Second, 5.0, 30.0, 0.50)
	if err != nil {
		t.Fatalf("backfill recent codex model: %v", err)
	}
	if updated != 1 {
		t.Fatalf("expected 1 recent token row update, got %d", updated)
	}
	if err := db.UpdateSessionTokens("s-recent-model"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	var model string
	var cost float64
	if err := db.db.QueryRow(`SELECT model, cost_usd FROM token_usage WHERE session_id = ?`, "s-recent-model").Scan(&model, &cost); err != nil {
		t.Fatalf("query token row: %v", err)
	}
	if model != "gpt-5.5" {
		t.Fatalf("expected recent row model to be repaired, got %q", model)
	}
	wantCost := (float64(800)*5.0 + float64(200)*0.50 + float64(100)*30.0) / 1_000_000
	if diff := cost - wantCost; diff < -0.0000001 || diff > 0.0000001 {
		t.Fatalf("cost = %f, want %f", cost, wantCost)
	}
}

func TestBackfillRecentCodexTokenModelSkipsOldRows(t *testing.T) {
	db := testDB(t)
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	contextTime := now.Add(time.Minute)

	if err := db.UpsertSession("s-old-model", event.PlatformCodex, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.InsertTokenUsage("", "s-old-model", 1000, 100, 0, 0, "gpt-5", 0.99, now, "codex-tokens-s-old-model-1"); err != nil {
		t.Fatalf("insert token usage: %v", err)
	}

	updated, err := db.BackfillRecentCodexTokenModel("s-old-model", "gpt-5.5", contextTime, 5*time.Second, 5.0, 30.0, 0.50)
	if err != nil {
		t.Fatalf("backfill recent codex model: %v", err)
	}
	if updated != 0 {
		t.Fatalf("expected old row outside skew window to be skipped, got %d updates", updated)
	}
}

func TestBackfillRecentCodexTokenModelDoesNotRewritePricedOldModel(t *testing.T) {
	db := testDB(t)
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	contextTime := now.Add(2 * time.Second)

	if err := db.UpsertSession("s-priced-old-model", event.PlatformCodex, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.InsertTokenUsage("", "s-priced-old-model", 1000, 100, 0, 0, "gpt-5", 0.00225, now, "codex-tokens-s-priced-old-model-1"); err != nil {
		t.Fatalf("insert token usage: %v", err)
	}

	updated, err := db.BackfillRecentCodexTokenModel("s-priced-old-model", "gpt-5.5", contextTime, 5*time.Second, 5.0, 30.0, 0.50)
	if err != nil {
		t.Fatalf("backfill recent codex model: %v", err)
	}
	if updated != 0 {
		t.Fatalf("expected priced old-model row to be preserved, got %d updates", updated)
	}

	var model string
	if err := db.db.QueryRow(`SELECT model FROM token_usage WHERE session_id = ?`, "s-priced-old-model").Scan(&model); err != nil {
		t.Fatalf("query token row: %v", err)
	}
	if model != "gpt-5" {
		t.Fatalf("priced old-model row was rewritten to %q", model)
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

func TestInsertFileChangeDeduplicatesReplay(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	if err := db.UpsertSession("s1", event.PlatformCodex, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.InsertFileChangeWithSource("s1", "src/main.go", event.FileEdit, now, "event-1"); err != nil {
		t.Fatalf("insert file change: %v", err)
	}
	if err := db.InsertFileChangeWithSource("s1", "src/main.go", event.FileEdit, now, "event-1"); err != nil {
		t.Fatalf("replay file change: %v", err)
	}

	changes, err := db.ListFileChanges("s1")
	if err != nil {
		t.Fatalf("list file changes: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("expected replayed file change to be ignored, got %d rows", len(changes))
	}
}

func TestInsertFileChangeSourceScopedBySession(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	if err := db.UpsertSession("s1", event.PlatformCodex, now); err != nil {
		t.Fatalf("upsert s1: %v", err)
	}
	if err := db.UpsertSession("s2", event.PlatformCodex, now); err != nil {
		t.Fatalf("upsert s2: %v", err)
	}
	if err := db.InsertFileChangeWithSource("s1", "src/main.go", event.FileEdit, now, "call-1:src/main.go"); err != nil {
		t.Fatalf("insert s1 change: %v", err)
	}
	if err := db.InsertFileChangeWithSource("s2", "src/main.go", event.FileEdit, now, "call-1:src/main.go"); err != nil {
		t.Fatalf("insert s2 change: %v", err)
	}

	changes1, err := db.ListFileChanges("s1")
	if err != nil {
		t.Fatalf("list s1 changes: %v", err)
	}
	changes2, err := db.ListFileChanges("s2")
	if err != nil {
		t.Fatalf("list s2 changes: %v", err)
	}
	if len(changes1) != 1 || len(changes2) != 1 {
		t.Fatalf("same source_id in different sessions should be preserved, got s1=%d s2=%d", len(changes1), len(changes2))
	}
}

func TestInsertFileChangeWithoutSourceKeepsSameSecondChanges(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()

	if err := db.UpsertSession("s1", event.PlatformCodex, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.InsertFileChange("s1", "src/main.go", event.FileEdit, now); err != nil {
		t.Fatalf("insert file change: %v", err)
	}
	if err := db.InsertFileChange("s1", "src/main.go", event.FileEdit, now.Add(100*time.Millisecond)); err != nil {
		t.Fatalf("insert second file change: %v", err)
	}

	changes, err := db.ListFileChanges("s1")
	if err != nil {
		t.Fatalf("list file changes: %v", err)
	}
	if len(changes) != 2 {
		t.Fatalf("expected source-less same-second changes to be preserved, got %d rows", len(changes))
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

func TestMarkStaleSessionsEndedUsesLastEventTimeForRetention(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	start := now.AddDate(0, 0, -10)
	lastEvent := now.AddDate(0, 0, -9)

	if err := db.UpsertSession("stale-old", event.PlatformClaude, start); err != nil {
		t.Fatalf("insert old session: %v", err)
	}
	if err := db.UpsertSession("stale-old", event.PlatformClaude, lastEvent); err != nil {
		t.Fatalf("record last event: %v", err)
	}
	if err := db.InsertTokenUsage("a1", "stale-old", 100, 50, 0, 0, "sonnet", 0.1, lastEvent, "src-stale-old"); err != nil {
		t.Fatalf("insert token usage: %v", err)
	}
	if err := db.MarkStaleSessionsEnded(2 * time.Hour); err != nil {
		t.Fatalf("mark stale: %v", err)
	}

	var status string
	var endTime string
	if err := db.db.QueryRow(`SELECT status, end_time FROM sessions WHERE session_id = ?`, "stale-old").Scan(&status, &endTime); err != nil {
		t.Fatalf("query session: %v", err)
	}
	if status != "stale" {
		t.Fatalf("expected stale status, got %q", status)
	}
	if endTime != formatStorageTime(lastEvent) {
		t.Fatalf("stale end_time should be last_event_time, got %q want %q", endTime, formatStorageTime(lastEvent))
	}

	deleted, err := db.CleanOldSessions(7)
	if err != nil {
		t.Fatalf("clean old sessions: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected stale old session to be retention-cleaned, got %d deletions", deleted)
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

func TestEndSessionDoesNotMoveEndTimeBackwards(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	later := now.Add(time.Hour)
	earlier := now.Add(10 * time.Minute)

	if err := db.UpsertSession("s1", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.EndSession("s1", later); err != nil {
		t.Fatalf("end session later: %v", err)
	}
	if err := db.EndSession("s1", earlier); err != nil {
		t.Fatalf("end session earlier: %v", err)
	}

	var endTime string
	if err := db.db.QueryRow(`SELECT end_time FROM sessions WHERE session_id = ?`, "s1").Scan(&endTime); err != nil {
		t.Fatalf("query end_time: %v", err)
	}
	if endTime != formatStorageTime(later) {
		t.Fatalf("end_time moved backwards: got %q want %q", endTime, formatStorageTime(later))
	}
}

func TestEndSessionIgnoresEndBeforeLastEvent(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	laterEvent := now.Add(time.Hour)
	olderEnd := now.Add(10 * time.Minute)

	if err := db.UpsertSession("s1", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.UpsertSession("s1", event.PlatformClaude, laterEvent); err != nil {
		t.Fatalf("record later event: %v", err)
	}
	if err := db.EndSession("s1", olderEnd); err != nil {
		t.Fatalf("older end session: %v", err)
	}

	var status string
	var endTime sql.NullString
	if err := db.db.QueryRow(`SELECT status, end_time FROM sessions WHERE session_id = ?`, "s1").Scan(&status, &endTime); err != nil {
		t.Fatalf("query session: %v", err)
	}
	if status != "active" {
		t.Fatalf("stale older end should not end a session with newer activity, got status %q", status)
	}
	if endTime.Valid {
		t.Fatalf("stale older end should not set end_time, got %q", endTime.String)
	}
}

func TestEndSessionIgnoresSameSecondEndBeforeLastEvent(t *testing.T) {
	db := testDB(t)
	base := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	laterEvent := base.Add(900 * time.Millisecond)
	olderEnd := base.Add(100 * time.Millisecond)

	if err := db.UpsertSession("s-subsecond-end", event.PlatformClaude, base); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.UpsertSession("s-subsecond-end", event.PlatformClaude, laterEvent); err != nil {
		t.Fatalf("record later same-second event: %v", err)
	}
	canEnd, err := db.CanEndSession("s-subsecond-end", olderEnd)
	if err != nil {
		t.Fatalf("can end session: %v", err)
	}
	if canEnd {
		t.Fatal("same-second but older end should not be allowed")
	}
	if err := db.EndSession("s-subsecond-end", olderEnd); err != nil {
		t.Fatalf("end session: %v", err)
	}

	var status string
	var endTime sql.NullString
	if err := db.db.QueryRow(`SELECT status, end_time FROM sessions WHERE session_id = ?`, "s-subsecond-end").Scan(&status, &endTime); err != nil {
		t.Fatalf("query session: %v", err)
	}
	if status != "active" {
		t.Fatalf("same-second older end should not end session, got %q", status)
	}
	if endTime.Valid {
		t.Fatalf("same-second older end should not set end_time, got %q", endTime.String)
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
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")

	path := DefaultDBPath()
	expected := filepath.Join(home, ".tokenmeter", "data", "tokenmeter.db")
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
	// Pick "today" at local-noon so the timestamp falls cleanly within the
	// local-day bucket regardless of the test host's UTC offset.
	now := time.Now()
	todayLocal := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.Local)

	db.UpsertSession("s1", event.PlatformClaude, todayLocal)

	// Insert costs for today and yesterday (both local-noon, well inside their buckets).
	db.InsertTokenUsage("a1", "s1", 1000, 500, 0, 0, "sonnet", 1.50, todayLocal, "src-today")
	db.InsertTokenUsage("a1", "s1", 800, 400, 0, 0, "sonnet", 0.80, todayLocal.AddDate(0, 0, -1), "src-yesterday")

	costs, err := db.GetDailyCosts(7)
	if err != nil {
		t.Fatalf("get daily costs: %v", err)
	}
	if len(costs) != 7 {
		t.Fatalf("expected 7 days, got %d", len(costs))
	}

	// Last entry should be today (local calendar).
	todayEntry := costs[6]
	if todayEntry.Date != todayLocal.Format("2006-01-02") {
		t.Errorf("last day: got %q, want %q", todayEntry.Date, todayLocal.Format("2006-01-02"))
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
	// Use local-noon for consistent bucket placement regardless of host TZ.
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.Local)
	from := today.AddDate(0, 0, -2)
	to := today.AddDate(0, 0, 1)

	db.UpsertSession("s1", event.PlatformClaude, today)
	db.InsertTokenUsage("a1", "s1", 1000, 500, 0, 0, "sonnet", 2.50, today, "src-1")
	db.InsertTokenUsage("a1", "s1", 500, 200, 0, 0, "sonnet", 1.00, today.AddDate(0, 0, -1), "src-2")

	costs, err := db.GetDailyCostsBetween(from, to)
	if err != nil {
		t.Fatalf("get daily costs between: %v", err)
	}
	// Endpoint is inclusive: [today-2, today+1] returns 4 days. Tomorrow has
	// no data so its cost is 0.
	if len(costs) != 4 {
		t.Fatalf("expected 4 days, got %d", len(costs))
	}
	if costs[0].Cost != 0 {
		t.Errorf("day 0 cost (2 days ago): got %f, want 0", costs[0].Cost)
	}
	if costs[1].Cost < 0.99 || costs[1].Cost > 1.01 {
		t.Errorf("day 1 cost (yesterday): got %f, want ~1.00", costs[1].Cost)
	}
	if costs[2].Cost < 2.49 || costs[2].Cost > 2.51 {
		t.Errorf("day 2 cost (today): got %f, want ~2.50", costs[2].Cost)
	}
	if costs[3].Cost != 0 {
		t.Errorf("day 3 cost (tomorrow): got %f, want 0", costs[3].Cost)
	}
}

func TestTimeRangeQueriesNormalizeToUTC(t *testing.T) {
	db := testDB(t)
	loc := time.FixedZone("UTC+1", 3600)
	ts := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)

	if err := db.UpsertSession("s-tz", event.PlatformClaude, ts); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.InsertTokenUsage("a1", "s-tz", 1000, 500, 0, 0, "sonnet", 1.25, ts, "src-tz"); err != nil {
		t.Fatalf("insert token usage: %v", err)
	}

	from := time.Date(2026, 1, 2, 13, 0, 0, 0, loc) // same instant as ts
	to := time.Date(2026, 1, 2, 14, 0, 0, 0, loc)
	cost, err := db.GetCostBetween(from, to)
	if err != nil {
		t.Fatalf("get cost between: %v", err)
	}
	if cost != 1.25 {
		t.Fatalf("non-UTC range should include matching UTC row, got cost %f", cost)
	}

	input, output, err := db.GetTokensSince(&from)
	if err != nil {
		t.Fatalf("get tokens since: %v", err)
	}
	if input != 1000 || output != 500 {
		t.Fatalf("non-UTC since should include row, got input=%d output=%d", input, output)
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

func TestGetFirstTokenDate(t *testing.T) {
	db := testDB(t)

	// Empty table returns zero time without error.
	got, err := db.GetFirstTokenDate()
	if err != nil {
		t.Fatalf("empty table: %v", err)
	}
	if !got.IsZero() {
		t.Errorf("empty table: want zero time, got %v", got)
	}

	// Use local time so the truncated day-start matches GetFirstTokenDate's
	// returned local-day anchor (P2-15).
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.Local)
	older := today.AddDate(0, 0, -5)

	db.UpsertSession("s1", event.PlatformClaude, today)
	db.InsertTokenUsage("a1", "s1", 100, 50, 0, 0, "sonnet", 0.10, today, "src-today")
	db.InsertTokenUsage("a1", "s1", 200, 100, 0, 0, "sonnet", 0.20, older, "src-older")

	got, err = db.GetFirstTokenDate()
	if err != nil {
		t.Fatalf("with data: %v", err)
	}
	want := time.Date(older.Year(), older.Month(), older.Day(), 0, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("first token date: got %v, want %v", got, want)
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
