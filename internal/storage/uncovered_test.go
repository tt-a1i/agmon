package storage

import (
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

func TestMarkPendingToolCallsInterruptedFlipsOnlyPending(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	if err := db.UpsertSession("s1", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Three tool calls: one pending, one already ended success, one ended fail.
	if _, err := db.InsertToolCallStart("tc-pending", "a", "s1", "Edit", "{}", now); err != nil {
		t.Fatalf("pending: %v", err)
	}
	if _, err := db.InsertToolCallStart("tc-done", "a", "s1", "Read", "{}", now); err != nil {
		t.Fatalf("done: %v", err)
	}
	if err := db.UpdateToolCallEnd("tc-done", "ok", event.StatusSuccess, 100, now.Add(time.Second)); err != nil {
		t.Fatalf("end done: %v", err)
	}
	if _, err := db.InsertToolCallStart("tc-fail", "a", "s1", "Bash", "{}", now); err != nil {
		t.Fatalf("fail: %v", err)
	}
	if err := db.UpdateToolCallEnd("tc-fail", "boom", event.StatusFail, 50, now.Add(time.Second)); err != nil {
		t.Fatalf("end fail: %v", err)
	}

	if err := db.MarkPendingToolCallsInterrupted("s1"); err != nil {
		t.Fatalf("mark interrupted: %v", err)
	}

	calls, err := db.ListToolCalls("s1", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	statuses := map[string]string{}
	for _, c := range calls {
		statuses[c.CallID] = c.Status
	}
	if statuses["tc-pending"] != "interrupted" {
		t.Errorf("tc-pending status = %q, want interrupted", statuses["tc-pending"])
	}
	if statuses["tc-done"] != "success" {
		t.Errorf("tc-done should stay success, got %q", statuses["tc-done"])
	}
	if statuses["tc-fail"] != "fail" {
		t.Errorf("tc-fail should stay fail, got %q", statuses["tc-fail"])
	}
}

func TestEndAgentSetsStatusAndEndTime(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	if err := db.UpsertSession("s1", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := db.UpsertAgent("a1", "s1", "", "explorer", now); err != nil {
		t.Fatalf("upsert agent: %v", err)
	}
	if err := db.EndAgent("a1", now.Add(5*time.Second)); err != nil {
		t.Fatalf("end agent: %v", err)
	}

	agents, err := db.ListAgents("s1")
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	if len(agents) != 1 || agents[0].Status != "ended" {
		t.Errorf("agent not ended: %v", agents)
	}
}

func TestRepairSyntheticModelsBackfillsFromTokenUsage(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	if err := db.UpsertSession("s1", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Insert a token row with a real model name.
	if err := db.InsertTokenUsage("a", "s1", 100, 50, 0, 0, "claude-sonnet-4-6", 0.1, now, "src-real"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// Manually corrupt the session model to "<synthetic>".
	if _, err := db.db.Exec(`UPDATE sessions SET model = '<synthetic>' WHERE session_id = 's1'`); err != nil {
		t.Fatalf("corrupt: %v", err)
	}

	n, err := db.RepairSyntheticModels()
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if n != 1 {
		t.Errorf("repaired = %d, want 1", n)
	}

	sess, found, err := db.GetSessionByIDPrefix("s1")
	if err != nil || !found {
		t.Fatalf("get session: found=%v err=%v", found, err)
	}
	if sess.Model != "claude-sonnet-4-6" {
		t.Errorf("model after repair = %q, want claude-sonnet-4-6", sess.Model)
	}
}

func TestListToolStatsAggregatesPerTool(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	if err := db.UpsertSession("s1", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// 2× Edit (1 success, 1 fail), 1× Read.
	for i, c := range []struct {
		id, name, status string
	}{
		{"e1", "Edit", string(event.StatusSuccess)},
		{"e2", "Edit", string(event.StatusFail)},
		{"r1", "Read", string(event.StatusSuccess)},
	} {
		t.Run(c.id, func(t *testing.T) {
			startT := now.Add(time.Duration(i) * time.Second)
			if _, err := db.InsertToolCallStart(c.id, "a", "s1", c.name, "{}", startT); err != nil {
				t.Fatalf("start: %v", err)
			}
			if err := db.UpdateToolCallEnd(c.id, "out", event.ToolCallStatus(c.status), int64(100*(i+1)), startT.Add(time.Second)); err != nil {
				t.Fatalf("end: %v", err)
			}
		})
	}

	stats, err := db.ListToolStats("s1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	byName := map[string]ToolStatRow{}
	for _, s := range stats {
		byName[s.ToolName] = s
	}
	if byName["Edit"].Count != 2 {
		t.Errorf("Edit count = %d, want 2", byName["Edit"].Count)
	}
	if byName["Edit"].FailCount != 1 {
		t.Errorf("Edit fail = %d, want 1", byName["Edit"].FailCount)
	}
	if byName["Read"].Count != 1 {
		t.Errorf("Read count = %d, want 1", byName["Read"].Count)
	}
}

func TestGetCostAndTokenSinceWindow(t *testing.T) {
	db := testDB(t)
	base := time.Now().UTC().Add(-3 * 24 * time.Hour) // 3 days ago
	if err := db.UpsertSession("s1", event.PlatformClaude, base); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Older row (5 days ago) — should be excluded by "since 3 days ago"
	old := base.Add(-2 * 24 * time.Hour)
	if err := db.InsertTokenUsage("a", "s1", 100, 50, 0, 0, "sonnet", 1.0, old, "src-old"); err != nil {
		t.Fatalf("insert old: %v", err)
	}
	// Newer row (1 day ago) — included
	recent := base.Add(2 * 24 * time.Hour)
	if err := db.InsertTokenUsage("a", "s1", 200, 100, 0, 0, "sonnet", 2.0, recent, "src-new"); err != nil {
		t.Fatalf("insert new: %v", err)
	}

	since := base // exactly the boundary
	cost, err := db.GetCostSince(&since)
	if err != nil {
		t.Fatalf("cost since: %v", err)
	}
	if cost < 1.99 || cost > 2.01 {
		t.Errorf("cost since = %v, want ~2.00 (only newer row)", cost)
	}

	in, out, err := db.GetTokensSince(&since)
	if err != nil {
		t.Fatalf("tokens since: %v", err)
	}
	if in != 200 || out != 100 {
		t.Errorf("tokens since = (%d, %d), want (200, 100)", in, out)
	}

	// nil since → all-time
	totalCost, _ := db.GetCostSince(nil)
	if totalCost < 2.99 || totalCost > 3.01 {
		t.Errorf("all-time cost = %v, want ~3.00", totalCost)
	}
}

func TestGetTodayCostBucketsByLocalDay(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	// Tokens at "now" should appear in "today".
	if err := db.UpsertSession("s1", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := db.InsertTokenUsage("a", "s1", 100, 50, 0, 0, "sonnet", 1.5, now, "src-now"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	cost, err := db.GetTodayCost()
	if err != nil {
		t.Fatalf("today cost: %v", err)
	}
	if cost < 1.49 || cost > 1.51 {
		t.Errorf("today cost = %v, want ~1.50", cost)
	}
}

func TestParseStorageTimeAcceptsBothLayouts(t *testing.T) {
	if got, ok := parseStorageTime(""); ok || !got.IsZero() {
		t.Errorf("empty string: ok=%v t=%v", ok, got)
	}
	if got, ok := parseStorageTime("not a time"); ok || !got.IsZero() {
		t.Errorf("garbage: ok=%v t=%v", ok, got)
	}
	if got, ok := parseStorageTime("2026-01-02T12:00:00Z"); !ok {
		t.Errorf("RFC3339 should parse: ok=%v t=%v", ok, got)
	}
	if got, ok := parseStorageTime("2026-01-02T12:00:00.123456789Z"); !ok {
		t.Errorf("RFC3339Nano should parse: ok=%v t=%v", ok, got)
	}
	// Returned time should be in UTC.
	if got, _ := parseStorageTime("2026-01-02T12:00:00+08:00"); got.Location() != time.UTC {
		t.Errorf("parseStorageTime should normalize to UTC, got %v", got.Location())
	}
}

func TestNormalizeStorageTimeIdempotent(t *testing.T) {
	// Already-normalized string should be unchanged or equivalent.
	canonical := "2026-01-02T12:00:00.000000000Z"
	out := normalizeStorageTime(canonical)
	if out != canonical {
		t.Errorf("canonical input should round-trip, got %q", out)
	}

	// Old-format input should be upgraded.
	out = normalizeStorageTime("2026-01-02T12:00:00Z")
	if out != canonical {
		t.Errorf("RFC3339 should normalize to canonical, got %q", out)
	}

	// Garbage stays unchanged.
	garbage := "not-a-date"
	if got := normalizeStorageTime(garbage); got != garbage {
		t.Errorf("invalid input should pass through, got %q", got)
	}
}

// TestBackfillRecentCodexTokenModel covers the edge case where Codex emits
// token_count before turn_context (which carries the model name). The
// backfill should update only the latest row within maxSkew of contextTime
// that has no model / zero cost — never touching older or already-priced rows.
func TestBackfillRecentCodexTokenModel(t *testing.T) {
	t.Run("maxSkew=0 is a no-op", func(t *testing.T) {
		db := testDB(t)
		now := time.Now().UTC()
		db.UpsertSession("s1", event.PlatformCodex, now)
		// codex-tokens- source_id so the SQL filter matches.
		db.InsertTokenUsage("a", "s1", 100, 50, 0, 0, "", 0, now, "codex-tokens-s1-1")

		n, err := db.BackfillRecentCodexTokenModel("s1", "gpt-5", now, 0, 1.25, 10.0, 0.125)
		if err != nil {
			t.Fatalf("backfill: %v", err)
		}
		if n != 0 {
			t.Errorf("maxSkew=0 should be no-op, n=%d", n)
		}
	})

	t.Run("empty model is a no-op", func(t *testing.T) {
		db := testDB(t)
		now := time.Now().UTC()
		db.UpsertSession("s1", event.PlatformCodex, now)
		db.InsertTokenUsage("a", "s1", 100, 50, 0, 0, "", 0, now, "codex-tokens-s1-1")

		n, _ := db.BackfillRecentCodexTokenModel("s1", "", now, 5*time.Second, 1.25, 10.0, 0.125)
		if n != 0 {
			t.Errorf("empty model should skip, n=%d", n)
		}
	})

	t.Run("updates row within skew window", func(t *testing.T) {
		db := testDB(t)
		now := time.Now().UTC()
		db.UpsertSession("s1", event.PlatformCodex, now)
		// Token emitted 2s before context — within 5s skew.
		emitTime := now.Add(-2 * time.Second)
		db.InsertTokenUsage("a", "s1", 100, 50, 0, 0, "", 0, emitTime, "codex-tokens-s1-2")

		n, err := db.BackfillRecentCodexTokenModel("s1", "gpt-5", now, 5*time.Second, 1.25, 10.0, 0.125)
		if err != nil {
			t.Fatalf("backfill: %v", err)
		}
		if n != 1 {
			t.Errorf("expected 1 row updated, got %d", n)
		}
	})

	t.Run("row outside window is untouched", func(t *testing.T) {
		db := testDB(t)
		now := time.Now().UTC()
		db.UpsertSession("s1", event.PlatformCodex, now)
		// 10s before context, beyond 5s skew.
		old := now.Add(-10 * time.Second)
		db.InsertTokenUsage("a", "s1", 100, 50, 0, 0, "", 0, old, "codex-tokens-s1-3")

		n, _ := db.BackfillRecentCodexTokenModel("s1", "gpt-5", now, 5*time.Second, 1.25, 10.0, 0.125)
		if n != 0 {
			t.Errorf("row outside window should be ignored, n=%d", n)
		}
	})

	t.Run("only most recent within window is updated", func(t *testing.T) {
		db := testDB(t)
		now := time.Now().UTC()
		db.UpsertSession("s1", event.PlatformCodex, now)
		// Two unpriced rows both within skew.
		db.InsertTokenUsage("a", "s1", 100, 50, 0, 0, "", 0, now.Add(-3*time.Second), "codex-tokens-s1-A")
		db.InsertTokenUsage("a", "s1", 200, 100, 0, 0, "", 0, now.Add(-1*time.Second), "codex-tokens-s1-B")

		n, _ := db.BackfillRecentCodexTokenModel("s1", "gpt-5", now, 5*time.Second, 1.25, 10.0, 0.125)
		if n != 1 {
			t.Errorf("should update only 1 (most recent), got %d", n)
		}
	})

	t.Run("already-priced row is not touched", func(t *testing.T) {
		db := testDB(t)
		now := time.Now().UTC()
		db.UpsertSession("s1", event.PlatformCodex, now)
		// Already has cost > 0 AND model set, should be skipped per
		// "AND (model = '' OR cost_usd = 0)" filter.
		db.InsertTokenUsage("a", "s1", 100, 50, 0, 0, "gpt-4", 1.5, now.Add(-1*time.Second), "codex-tokens-s1-C")

		n, _ := db.BackfillRecentCodexTokenModel("s1", "gpt-5", now, 5*time.Second, 1.25, 10.0, 0.125)
		if n != 0 {
			t.Errorf("priced row should be skipped, got %d", n)
		}
	})
}

func TestGetAgentTokenSummary(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	if err := db.UpsertSession("s1", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Two token rows for the same agent.
	for i, c := range []struct {
		in, out int
		cost    float64
	}{
		{100, 50, 0.5},
		{200, 100, 1.0},
	} {
		if err := db.InsertTokenUsage("agent-x", "s1", c.in, c.out, 0, 0, "sonnet", c.cost,
			now.Add(time.Duration(i)*time.Second),
			"src-"+time.Now().Format("150405.999")+"-"+string(rune('a'+i))); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	in, out, cost, err := db.GetAgentTokenSummary("agent-x")
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if in != 300 || out != 150 {
		t.Errorf("summary tokens = (%d, %d), want (300, 150)", in, out)
	}
	if cost < 1.49 || cost > 1.51 {
		t.Errorf("summary cost = %v, want ~1.50", cost)
	}

	// Unknown agent → zero result, no error.
	in, out, cost, err = db.GetAgentTokenSummary("never-existed")
	if err != nil {
		t.Errorf("unknown agent: %v", err)
	}
	if in != 0 || out != 0 || cost != 0 {
		t.Errorf("unknown agent summary = (%d, %d, %v), want zeros", in, out, cost)
	}
}
