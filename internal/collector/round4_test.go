package collector

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/storage"
)

// TestParseClaudeLogTokenEvent table-drives the new shared parser used by
// both the parallel initial scan and the incremental processFile, so we
// don't drift again.
func TestParseClaudeLogTokenEvent(t *testing.T) {
	const sessionID = "11111111-2222-3333-4444-555555555555"

	tests := []struct {
		name        string
		entry       claudeLogEntry
		prevBranch  string
		wantOK      bool
		wantBranch  string
		wantInTok   int
		wantOutTok  int
		wantHasCost bool
	}{
		{
			name:       "non-assistant entry adopts branch but emits nothing",
			entry:      claudeLogEntry{Type: "user", GitBranch: "main"},
			prevBranch: "",
			wantOK:     false,
			wantBranch: "main",
		},
		{
			name: "assistant with valid usage and rfc3339nano timestamp",
			entry: claudeLogEntry{
				Type:      "assistant",
				UUID:      "u1",
				GitBranch: "main",
				Timestamp: "2026-01-14T12:07:10.150Z",
				Message: &claudeLogMsg{
					Model: "claude-sonnet-4-6",
					Usage: &claudeLogUsage{InputTokens: 5, OutputTokens: 7},
				},
			},
			prevBranch:  "",
			wantOK:      true,
			wantBranch:  "main",
			wantInTok:   5,
			wantOutTok:  7,
			wantHasCost: true,
		},
		{
			name: "invalid timestamp drops event, branch still adopted",
			entry: claudeLogEntry{
				Type:      "assistant",
				UUID:      "u2",
				GitBranch: "feature",
				Timestamp: "not a date",
				Message: &claudeLogMsg{
					Model: "claude-sonnet-4-6",
					Usage: &claudeLogUsage{InputTokens: 1, OutputTokens: 1},
				},
			},
			prevBranch: "",
			wantOK:     false,
			wantBranch: "feature",
		},
		{
			name: "prevBranch is sticky — entry without branch doesn't clear it",
			entry: claudeLogEntry{
				Type:      "assistant",
				UUID:      "u3",
				Timestamp: "2026-01-14T12:07:10Z",
				Message: &claudeLogMsg{
					Model: "claude-sonnet-4-6",
					Usage: &claudeLogUsage{InputTokens: 1, OutputTokens: 1},
				},
			},
			prevBranch:  "already-set",
			wantOK:      true,
			wantBranch:  "already-set",
			wantInTok:   1,
			wantOutTok:  1,
			wantHasCost: true,
		},
		{
			name: "input_tokens summed with cache columns for context tracking",
			entry: claudeLogEntry{
				Type:      "assistant",
				UUID:      "u4",
				Timestamp: "2026-01-14T12:07:10Z",
				Message: &claudeLogMsg{
					Model: "claude-sonnet-4-6",
					Usage: &claudeLogUsage{
						InputTokens:              3,
						OutputTokens:             6,
						CacheCreationInputTokens: 100,
						CacheReadInputTokens:     200,
					},
				},
			},
			prevBranch:  "",
			wantOK:      true,
			wantBranch:  "",
			wantInTok:   3 + 100 + 200,
			wantOutTok:  6,
			wantHasCost: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, newBranch, ok := parseClaudeLogTokenEvent(tt.entry, sessionID, tt.prevBranch)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if newBranch != tt.wantBranch {
				t.Errorf("branch = %q, want %q", newBranch, tt.wantBranch)
			}
			if !ok {
				return
			}
			if ev.Data.InputTokens != tt.wantInTok {
				t.Errorf("input_tokens = %d, want %d", ev.Data.InputTokens, tt.wantInTok)
			}
			if ev.Data.OutputTokens != tt.wantOutTok {
				t.Errorf("output_tokens = %d, want %d", ev.Data.OutputTokens, tt.wantOutTok)
			}
			if tt.wantHasCost && ev.Data.CostUSD <= 0 {
				t.Errorf("expected positive CostUSD, got %v", ev.Data.CostUSD)
			}
			if ev.Type != event.EventTokenUsage {
				t.Errorf("event type = %q, want EventTokenUsage", ev.Type)
			}
			if ev.SessionID != sessionID {
				t.Errorf("session ID = %q, want %q", ev.SessionID, sessionID)
			}
		})
	}
}

// TestParseClaudeFileEventsContract covers the exposed offline-tooling API:
// callers (e.g. external scripts) parse a session JSONL into TokenUsage events
// without touching watcher state. The function should:
//  1. Return tokens from assistant entries only
//  2. Carry gitBranch as it first appears
//  3. Skip malformed JSON lines without aborting
//  4. Tolerate trailing newline / missing trailing newline
//  5. Sum cache_creation + cache_read into InputTokens
func TestParseClaudeFileEventsContract(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantCount int
		check     func(*testing.T, []event.Event)
	}{
		{
			name:      "empty file",
			body:      "",
			wantCount: 0,
		},
		{
			name:      "single assistant",
			body:      `{"type":"assistant","sessionId":"s","uuid":"u1","gitBranch":"main","timestamp":"2026-01-14T12:07:10.150Z","message":{"model":"claude-sonnet-4-6","usage":{"input_tokens":10,"output_tokens":5}}}` + "\n",
			wantCount: 1,
			check: func(t *testing.T, evs []event.Event) {
				if evs[0].Data.GitBranch != "main" {
					t.Errorf("gitBranch = %q, want main", evs[0].Data.GitBranch)
				}
				if evs[0].Data.InputTokens != 10 {
					t.Errorf("InputTokens = %d, want 10", evs[0].Data.InputTokens)
				}
			},
		},
		{
			name: "mixed types — only assistant emits",
			body: `{"type":"user","sessionId":"s","uuid":"u0","timestamp":"2026-01-14T12:07:00Z"}` + "\n" +
				`{"type":"system","sessionId":"s","uuid":"u-sys","timestamp":"2026-01-14T12:07:05Z"}` + "\n" +
				`{"type":"assistant","sessionId":"s","uuid":"u1","gitBranch":"feat","timestamp":"2026-01-14T12:07:10Z","message":{"model":"sonnet","usage":{"input_tokens":3,"output_tokens":1}}}` + "\n",
			wantCount: 1,
		},
		{
			name: "malformed line skipped, valid one still parsed",
			body: "not-json-at-all\n" +
				`{"type":"assistant","sessionId":"s","uuid":"u2","gitBranch":"main","timestamp":"2026-01-14T12:07:10Z","message":{"model":"sonnet","usage":{"input_tokens":2,"output_tokens":2}}}` + "\n",
			wantCount: 1,
		},
		{
			name:      "cache tokens summed into InputTokens",
			body:      `{"type":"assistant","sessionId":"s","uuid":"u3","timestamp":"2026-01-14T12:07:10Z","message":{"model":"sonnet","usage":{"input_tokens":3,"output_tokens":1,"cache_creation_input_tokens":100,"cache_read_input_tokens":200}}}` + "\n",
			wantCount: 1,
			check: func(t *testing.T, evs []event.Event) {
				if evs[0].Data.InputTokens != 3+100+200 {
					t.Errorf("InputTokens = %d, want %d", evs[0].Data.InputTokens, 3+100+200)
				}
				if evs[0].Data.CacheReadTokens != 200 {
					t.Errorf("CacheReadTokens = %d, want 200", evs[0].Data.CacheReadTokens)
				}
			},
		},
		{
			name:      "no trailing newline still parsed",
			body:      `{"type":"assistant","sessionId":"s","uuid":"u4","timestamp":"2026-01-14T12:07:10Z","message":{"model":"sonnet","usage":{"input_tokens":5,"output_tokens":5}}}`,
			wantCount: 1,
		},
		{
			name:      "bad timestamp drops event",
			body:      `{"type":"assistant","sessionId":"s","uuid":"u5","timestamp":"not-a-date","message":{"model":"sonnet","usage":{"input_tokens":5,"output_tokens":5}}}` + "\n",
			wantCount: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "session.jsonl")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			got := ParseClaudeFileEvents(path, "s")
			if len(got) != tc.wantCount {
				t.Fatalf("got %d events, want %d: %#v", len(got), tc.wantCount, got)
			}
			if tc.check != nil {
				tc.check(t, got)
			}
		})
	}
}

// TestAddColumnIfMissingViaPragma verifies the PRAGMA-based existence check
// doesn't depend on the SQLite driver's error wording.
func TestAddColumnIfMissingViaPragma(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	// Opening a brand new DB runs migrate(), which exercises addColumnIfMissing
	// against every legacy column. If the new PRAGMA path is broken we'd see
	// `alter table … add …` failures in test output. The fact that Open
	// succeeded (and tests pass) is the regression check; we additionally
	// verify the column tag exists.
	now := time.Now().UTC()
	if err := db.UpsertSession("s1", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := db.SetSessionTag("s1", "hello"); err != nil {
		t.Fatalf("set tag: %v", err)
	}
	// Re-open: addColumnIfMissing must not double-add or error on existing columns.
	db.Close()
	db, err = storage.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	s, found, err := db.GetSessionByIDPrefix("s1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !found || s.Tag != "hello" {
		t.Errorf("tag lost or session missing: found=%v tag=%q", found, s.Tag)
	}
}
