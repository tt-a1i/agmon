package collector

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/tt-a1i/agmon/internal/event"
	"github.com/tt-a1i/agmon/internal/storage"
)

// newTestDB opens a fresh SQLite DB in a temp directory. Returns a DB that
// will be closed automatically when the test ends.
func newTestDB(t *testing.T) *storage.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// applyEventsToDB mirrors what the daemon does with TokenUsage events:
// UpsertSession + InsertTokenUsage. Returns the number of events inserted.
func applyEventsToDB(t *testing.T, db *storage.DB, platform event.Platform, events []event.Event) {
	t.Helper()
	for _, ev := range events {
		if ev.Type != event.EventTokenUsage {
			continue
		}
		if err := db.UpsertSession(ev.SessionID, platform, ev.Timestamp); err != nil {
			t.Fatalf("UpsertSession: %v", err)
		}
		err := db.InsertTokenUsage(
			ev.AgentID, ev.SessionID,
			ev.Data.InputTokens, ev.Data.OutputTokens,
			ev.Data.CacheCreationTokens, ev.Data.CacheReadTokens,
			ev.Data.Model, ev.Data.CostUSD, ev.Timestamp, ev.ID,
		)
		if err != nil {
			t.Fatalf("InsertTokenUsage: %v", err)
		}
	}
}

// sessionCost pulls total_cost_usd for a specific session_id.
func sessionCost(t *testing.T, db *storage.DB, sessionID string) float64 {
	t.Helper()
	sess, found, err := db.GetSessionByIDPrefix(sessionID)
	if err != nil {
		t.Fatalf("GetSessionByIDPrefix: %v", err)
	}
	if !found {
		t.Fatalf("session %q not found", sessionID)
	}
	return sess.TotalCostUSD
}

// TestClaudeEndToEndCost walks a JSONL line through parseClaudeFileCollect
// and InsertTokenUsage, then asserts session.total_cost_usd matches the
// hand-calculated value. A second pass (simulating daemon restart) must
// leave the total unchanged because UUID-based dedup holds.
func TestClaudeEndToEndCost(t *testing.T) {
	const (
		sessionID    = "sess-e2e-1"
		msgUUID      = "msg-uuid-1"
		model        = "claude-sonnet-4-6"
		inputTokens  = 1000
		outputTokens = 500
		cacheCreate  = 200
		cacheRead    = 300
	)

	// Hand-calculated expected cost using Sonnet-4 rates:
	// (1000*3.0 + 500*15.0 + 200*3.75 + 300*0.30) / 1e6
	// = (3000 + 7500 + 750 + 90) / 1e6
	// = 11340 / 1e6 = 0.011340
	const expectedCost = 0.011340

	// Construct a single-line Claude JSONL file.
	jsonl := map[string]any{
		"type":      "assistant",
		"sessionId": sessionID,
		"uuid":      msgUUID,
		"cwd":       "/test/cwd",
		"gitBranch": "main",
		"timestamp": "2026-04-16T08:00:00.000Z",
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
	dir := t.TempDir()
	path := filepath.Join(dir, sessionID+".jsonl")
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	// First pass: parse + insert.
	db := newTestDB(t)
	result := processClaudeFileCollect(path, sessionID, 0, "", nil)
	if len(result.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(result.events))
	}
	applyEventsToDB(t, db, event.PlatformClaude, result.events)

	got := sessionCost(t, db, sessionID)
	if math.Abs(got-expectedCost) > 1e-6 {
		t.Fatalf("first pass cost = %f, want %f", got, expectedCost)
	}

	// Second pass: simulate daemon restart by re-parsing the same file from
	// offset 0. UUID-based source_id dedup should prevent double-counting.
	result2 := processClaudeFileCollect(path, sessionID, 0, "", nil)
	applyEventsToDB(t, db, event.PlatformClaude, result2.events)

	got2 := sessionCost(t, db, sessionID)
	if math.Abs(got2-expectedCost) > 1e-6 {
		t.Fatalf("after restart cost = %f, want %f (UUID dedup failed)", got2, expectedCost)
	}
}

// TestCodexEndToEndCost walks a Codex token_count event through
// parseCodexEntryWithContext and InsertTokenUsage, asserts the cost, then
// re-emits the same event (simulating restart) and verifies the total does
// NOT double because the source_id — derived from the event's JSONL
// timestamp — is stable across runs.
func TestCodexEndToEndCost(t *testing.T) {
	const (
		sessionID    = "sess-codex-e2e-1"
		model        = "gpt-5.4"
		inputTokens  = 500
		outputTokens = 100
		cachedInput  = 200
	)

	// Hand-calculated cost for gpt-5.4 ($2.50 in / $15 out / $0.25 cache):
	// regularInput = 500 - 200 = 300
	// (300*2.50 + 200*0.25 + 100*15) / 1e6
	// = (750 + 50 + 1500) / 1e6
	// = 2300 / 1e6 = 0.002300
	const expectedCost = 0.002300

	// Build a codexLogEntry with an event_msg/token_count payload.
	payload := map[string]any{
		"type": "token_count",
		"info": map[string]any{
			"last_token_usage": map[string]any{
				"input_tokens":        inputTokens,
				"output_tokens":       outputTokens,
				"total_tokens":        inputTokens + outputTokens,
				"cached_input_tokens": cachedInput,
			},
		},
	}
	payloadBytes, _ := json.Marshal(payload)
	entry := codexLogEntry{
		Timestamp: "2026-04-16T08:00:00.000000000Z",
		Type:      "event_msg",
		Payload:   payloadBytes,
	}

	// First pass.
	db := newTestDB(t)
	events := parseCodexEntryWithContext(entry, sessionID, model, "/test/cwd")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	applyEventsToDB(t, db, event.PlatformCodex, events)

	got := sessionCost(t, db, sessionID)
	if math.Abs(got-expectedCost) > 1e-6 {
		t.Fatalf("first pass cost = %f, want %f", got, expectedCost)
	}

	// Second pass: same entry re-parsed (simulating daemon restart reading
	// the same JSONL line again). Event.ID is derived from the entry
	// timestamp, so the source_id is identical → unique index rejects it.
	events2 := parseCodexEntryWithContext(entry, sessionID, model, "/test/cwd")
	applyEventsToDB(t, db, event.PlatformCodex, events2)

	got2 := sessionCost(t, db, sessionID)
	if math.Abs(got2-expectedCost) > 1e-6 {
		t.Fatalf("after restart cost = %f, want %f (source_id dedup failed)", got2, expectedCost)
	}
}

// TestCodexEndToEndCostVariesByModel ensures that switching model between
// otherwise-identical events produces distinct costs — proves the pricing
// table wiring all the way through to the DB.
func TestCodexEndToEndCostVariesByModel(t *testing.T) {
	mkEntry := func(ts string, in, out, cached int) codexLogEntry {
		payload, _ := json.Marshal(map[string]any{
			"type": "token_count",
			"info": map[string]any{
				"last_token_usage": map[string]any{
					"input_tokens":        in,
					"output_tokens":       out,
					"total_tokens":        in + out,
					"cached_input_tokens": cached,
				},
			},
		})
		return codexLogEntry{Timestamp: ts, Type: "event_msg", Payload: payload}
	}

	cases := []struct {
		name, model, ts, session string
		in, out, cached          int
		wantCost                 float64
	}{
		{
			// gpt-5-nano: (900*0.05 + 100*0.005 + 50*0.40) / 1e6
			// = (45 + 0.5 + 20) / 1e6 = 0.0000655
			name: "gpt-5-nano", model: "gpt-5-nano",
			ts:      "2026-04-16T09:00:00.000000001Z",
			session: "sess-nano", in: 1000, out: 50, cached: 100,
			wantCost: 0.0000655,
		},
		{
			// gpt-5.4: (800*2.50 + 200*0.25 + 50*15) / 1e6
			// = (2000 + 50 + 750) / 1e6 = 0.00280
			name: "gpt-5.4", model: "gpt-5.4",
			ts:      "2026-04-16T09:00:00.000000002Z",
			session: "sess-5-4", in: 1000, out: 50, cached: 200,
			wantCost: 0.00280,
		},
	}

	db := newTestDB(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := mkEntry(tc.ts, tc.in, tc.out, tc.cached)
			events := parseCodexEntryWithContext(entry, tc.session, tc.model, "/test")
			if len(events) != 1 {
				t.Fatalf("expected 1 event, got %d", len(events))
			}
			applyEventsToDB(t, db, event.PlatformCodex, events)
			got := sessionCost(t, db, tc.session)
			if math.Abs(got-tc.wantCost) > 1e-7 {
				t.Errorf("%s cost = %f, want %f", tc.model, got, tc.wantCost)
			}
		})
	}
}
