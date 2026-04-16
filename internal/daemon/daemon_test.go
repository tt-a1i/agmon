package daemon

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tt-a1i/agmon/internal/collector"
	"github.com/tt-a1i/agmon/internal/event"
	"github.com/tt-a1i/agmon/internal/storage"
)

func testDaemon(t *testing.T) (*Daemon, *storage.DB) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db, filepath.Join(dir, "agmon.sock")), db
}

func TestRemoteSubscriberReceivesBroadcast(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	sockPath := filepath.Join(os.TempDir(), fmt.Sprintf("agmon-%d.sock", time.Now().UnixNano()))
	d := New(db, sockPath)
	if err := d.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	defer d.Stop()

	eventCh, closeFn, err := SubscribeRemote(sockPath)
	if err != nil {
		t.Fatalf("subscribe remote: %v", err)
	}
	defer closeFn()

	want := event.Event{
		ID:        "session-start-s1",
		Type:      event.EventSessionStart,
		SessionID: "s1",
		Platform:  event.PlatformClaude,
		Timestamp: time.Now().UTC(),
	}
	d.ProcessExternalEvent(want)

	select {
	case got := <-eventCh:
		if got.ID != want.ID {
			t.Fatalf("event id: got %q want %q", got.ID, want.ID)
		}
		if got.SessionID != want.SessionID {
			t.Fatalf("session id: got %q want %q", got.SessionID, want.SessionID)
		}
		if got.Type != want.Type {
			t.Fatalf("event type: got %q want %q", got.Type, want.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for broadcast")
	}
}

func TestProcessEventEndsHistoricalSessionStart(t *testing.T) {
	d, db := testDaemon(t)
	evTime := time.Now().UTC().Add(-3 * time.Hour)

	err := d.processEvent(event.Event{
		ID:        "session-start-old",
		Type:      event.EventSessionStart,
		SessionID: "old-session",
		Platform:  event.PlatformCodex,
		Timestamp: evTime,
		Data: event.EventData{
			CWD:       "/tmp/project",
			GitBranch: "main",
		},
	})
	if err != nil {
		t.Fatalf("process event: %v", err)
	}

	session, found, err := db.GetSessionByIDPrefix("old-session")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if !found {
		t.Fatal("expected session to be created")
	}
	if session.Status != "ended" {
		t.Fatalf("expected historical session to be ended, got %q", session.Status)
	}
	if session.CWD != "/tmp/project" {
		t.Fatalf("cwd: got %q", session.CWD)
	}
	if session.GitBranch != "main" {
		t.Fatalf("git branch: got %q", session.GitBranch)
	}
	if session.EndTime == nil {
		t.Fatal("expected historical session to have end time")
	}
}

func TestProcessEventSessionEndInterruptsPendingToolCalls(t *testing.T) {
	d, db := testDaemon(t)
	now := time.Now().UTC()

	if err := db.UpsertSession("s1", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.InsertToolCallStart("call-1", "agent-1", "s1", "Edit", "{}", now); err != nil {
		t.Fatalf("insert tool call: %v", err)
	}

	err := d.processEvent(event.Event{
		ID:        "session-end-s1",
		Type:      event.EventSessionEnd,
		SessionID: "s1",
		Platform:  event.PlatformClaude,
		Timestamp: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("process event: %v", err)
	}

	session, found, err := db.GetSessionByIDPrefix("s1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if !found {
		t.Fatal("expected session to exist")
	}
	if session.Status != "ended" {
		t.Fatalf("expected ended session, got %q", session.Status)
	}

	calls, err := db.ListToolCalls("s1", 10)
	if err != nil {
		t.Fatalf("list tool calls: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if calls[0].Status != "interrupted" {
		t.Fatalf("expected interrupted tool call, got %q", calls[0].Status)
	}
}

func TestProcessEventTokenUsageUpdatesSessionTotals(t *testing.T) {
	d, db := testDaemon(t)
	now := time.Now().UTC()

	err := d.processEvent(event.Event{
		ID:        "token-1",
		Type:      event.EventTokenUsage,
		SessionID: "s-token",
		AgentID:   "agent-1",
		Platform:  event.PlatformClaude,
		Timestamp: now,
		Data: event.EventData{
			InputTokens:         100,
			OutputTokens:        25,
			CacheCreationTokens: 10,
			CacheReadTokens:     5,
			Model:               "sonnet",
			CostUSD:             1.25,
			CWD:                 "/tmp/agmon",
			GitBranch:           "feature/test",
		},
	})
	if err != nil {
		t.Fatalf("process event: %v", err)
	}

	session, found, err := db.GetSessionByIDPrefix("s-token")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if !found {
		t.Fatal("expected session to exist")
	}
	if session.TotalInputTokens != 100 || session.TotalOutputTokens != 25 {
		t.Fatalf("unexpected totals: in=%d out=%d", session.TotalInputTokens, session.TotalOutputTokens)
	}
	if session.TotalCacheCreationTokens != 10 || session.TotalCacheReadTokens != 5 {
		t.Fatalf("unexpected cache totals: create=%d read=%d", session.TotalCacheCreationTokens, session.TotalCacheReadTokens)
	}
	if session.TotalCostUSD != 1.25 {
		t.Fatalf("unexpected cost: %f", session.TotalCostUSD)
	}
	if session.CWD != "/tmp/agmon" || session.GitBranch != "feature/test" {
		t.Fatalf("unexpected meta: cwd=%q branch=%q", session.CWD, session.GitBranch)
	}
}

func TestProcessEventToolCallEndInsertsFileChange(t *testing.T) {
	d, db := testDaemon(t)
	now := time.Now().UTC()

	if err := db.UpsertSession("s-file", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.InsertToolCallStart("call-file", "agent-1", "s-file", "Edit", "{}", now); err != nil {
		t.Fatalf("insert tool call: %v", err)
	}

	err := d.processEvent(event.Event{
		ID:        "call-file",
		Type:      event.EventToolCallEnd,
		SessionID: "s-file",
		AgentID:   "agent-1",
		Platform:  event.PlatformClaude,
		Timestamp: now.Add(2 * time.Second),
		Data: event.EventData{
			ToolResult: "ok",
			ToolStatus: event.StatusSuccess,
			FilePath:   "internal/daemon/daemon.go",
			ChangeType: event.FileEdit,
		},
	})
	if err != nil {
		t.Fatalf("process event: %v", err)
	}

	changes, err := db.ListFileChanges("s-file")
	if err != nil {
		t.Fatalf("list file changes: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("expected 1 file change, got %d", len(changes))
	}
	if changes[0].FilePath != "internal/daemon/daemon.go" {
		t.Fatalf("unexpected file path: %q", changes[0].FilePath)
	}
	if changes[0].ChangeType != string(event.FileEdit) {
		t.Fatalf("unexpected change type: %q", changes[0].ChangeType)
	}
}

// TestProcessEventClaudeWatcherToDB exercises the full Claude watcher → daemon
// → DB path: parse a real JSONL file via collector, feed each emitted event
// through d.processEvent, assert session totals match hand-calculated cost
// AND that FillSessionMeta populated cwd/git_branch. A second pass simulates
// daemon restart and must not double-count (UUID-based source_id dedup).
func TestProcessEventClaudeWatcherToDB(t *testing.T) {
	d, db := testDaemon(t)

	const (
		sessionID    = "sess-daemon-claude-1"
		msgUUID      = "uuid-daemon-1"
		model        = "claude-sonnet-4-6"
		inputTokens  = 1000
		outputTokens = 500
		cacheCreate  = 200
		cacheRead    = 300
		expectedCost = 0.011340 // (1000*3 + 500*15 + 200*3.75 + 300*0.30) / 1e6
	)

	jsonl := map[string]any{
		"type":      "assistant",
		"sessionId": sessionID,
		"uuid":      msgUUID,
		"cwd":       "/tmp/agmon/daemon-test",
		"gitBranch": "feature/daemon-e2e",
		"timestamp": "2026-04-16T10:00:00.000Z",
		"message": map[string]any{
			"model": model,
			"usage": map[string]any{
				"input_tokens":                inputTokens,
				"output_tokens":               outputTokens,
				"cache_creation_input_tokens": cacheCreate,
				"cache_read_input_tokens":     cacheRead,
			},
		},
	}
	body, _ := json.Marshal(jsonl)
	path := filepath.Join(t.TempDir(), sessionID+".jsonl")
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	// First pass: parse + process through the daemon.
	events := collector.ParseClaudeFileEvents(path, sessionID)
	if len(events) != 1 {
		t.Fatalf("expected 1 event from parser, got %d", len(events))
	}
	for _, ev := range events {
		if err := d.processEvent(ev); err != nil {
			t.Fatalf("processEvent: %v", err)
		}
	}

	sess, found, err := db.GetSessionByIDPrefix(sessionID)
	if err != nil || !found {
		t.Fatalf("session lookup: err=%v found=%v", err, found)
	}
	if math.Abs(sess.TotalCostUSD-expectedCost) > 1e-6 {
		t.Fatalf("total_cost_usd = %f, want %f", sess.TotalCostUSD, expectedCost)
	}
	if sess.CWD != "/tmp/agmon/daemon-test" {
		t.Errorf("cwd = %q, want /tmp/agmon/daemon-test (FillSessionMeta not wired)", sess.CWD)
	}
	if sess.GitBranch != "feature/daemon-e2e" {
		t.Errorf("git_branch = %q, want feature/daemon-e2e (FillSessionMeta not wired)", sess.GitBranch)
	}

	// Second pass: replay same file (simulating daemon restart). UUID-based
	// source_id dedup in InsertTokenUsage must prevent double counting.
	replay := collector.ParseClaudeFileEvents(path, sessionID)
	for _, ev := range replay {
		if err := d.processEvent(ev); err != nil {
			t.Fatalf("processEvent replay: %v", err)
		}
	}
	sess2, _, _ := db.GetSessionByIDPrefix(sessionID)
	if math.Abs(sess2.TotalCostUSD-expectedCost) > 1e-6 {
		t.Fatalf("after replay total_cost_usd = %f, want %f (dedup failed)", sess2.TotalCostUSD, expectedCost)
	}
}

// TestProcessEventCodexEmptyModelBackfill targets the branch at
// daemon.go:271 — Codex token_count events can arrive before turn_context
// sets the model. Earlier rows have model="" until a later event with a
// known model triggers BackfillEmptyTokenModel + UpdateSessionTokens.
func TestProcessEventCodexEmptyModelBackfill(t *testing.T) {
	d, db := testDaemon(t)
	now := time.Now().UTC()

	const sessionID = "sess-daemon-codex-backfill"

	// Event 1: token_count arrives before turn_context — model is empty,
	// cost is 0. Row goes into token_usage with model=''.
	event1 := event.Event{
		ID:        "codex-tokens-1",
		Type:      event.EventTokenUsage,
		SessionID: sessionID,
		Platform:  event.PlatformCodex,
		Timestamp: now,
		Data: event.EventData{
			InputTokens:     300,
			OutputTokens:    50,
			CacheReadTokens: 100,
			Model:           "",
			CostUSD:         0,
		},
	}
	if err := d.processEvent(event1); err != nil {
		t.Fatalf("processEvent #1: %v", err)
	}

	// Before the backfill event arrives, session cost must be 0.
	sess0, _, _ := db.GetSessionByIDPrefix(sessionID)
	if sess0.TotalCostUSD != 0 {
		t.Fatalf("pre-backfill total_cost_usd = %f, want 0", sess0.TotalCostUSD)
	}

	// Event 2: same session, now with model="gpt-5.4" and a pre-computed
	// cost. Daemon should (a) insert this row, (b) detect empty-model rows
	// in this session and backfill them with gpt-5.4 rates, (c) re-sum
	// session totals.
	//
	// gpt-5.4 rates: input=$2.50/M, output=$15/M, cache_read=$0.25/M.
	//   Row 1 backfilled cost: ((300-100)*2.50 + 100*0.25 + 50*15) / 1e6
	//                        = (500 + 25 + 750) / 1e6 = 0.001275
	//   Row 2 cost:           ((500-200)*2.50 + 200*0.25 + 100*15) / 1e6
	//                        = (750 + 50 + 1500) / 1e6 = 0.002300
	//   Session total:        0.001275 + 0.002300 = 0.003575
	event2 := event.Event{
		ID:        "codex-tokens-2",
		Type:      event.EventTokenUsage,
		SessionID: sessionID,
		Platform:  event.PlatformCodex,
		Timestamp: now.Add(5 * time.Second),
		Data: event.EventData{
			InputTokens:     500,
			OutputTokens:    100,
			CacheReadTokens: 200,
			Model:           "gpt-5.4",
			CostUSD:         0.002300,
		},
	}
	if err := d.processEvent(event2); err != nil {
		t.Fatalf("processEvent #2: %v", err)
	}

	sess, _, _ := db.GetSessionByIDPrefix(sessionID)
	const wantCost = 0.003575
	if math.Abs(sess.TotalCostUSD-wantCost) > 1e-6 {
		t.Fatalf("post-backfill total_cost_usd = %f, want %f", sess.TotalCostUSD, wantCost)
	}
	if sess.TotalInputTokens != 800 {
		t.Errorf("total_input_tokens = %d, want 800", sess.TotalInputTokens)
	}
	if sess.TotalOutputTokens != 150 {
		t.Errorf("total_output_tokens = %d, want 150", sess.TotalOutputTokens)
	}
}
