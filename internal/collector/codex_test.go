package collector

import (
	"encoding/json"
	"testing"

	"github.com/tt-a1i/agmon/internal/event"
)

func TestParseCodexEntry_SessionMeta(t *testing.T) {
	entry := codexLogEntry{
		Timestamp: "2026-01-14T12:07:10.150Z",
		Type:      "session_meta",
		Payload:   json.RawMessage(`{"id":"d4430cef-110d-42e0-924a-bfceeba0c4e1","timestamp":"2026-01-14T12:07:10.150Z","cwd":"/tmp"}`),
	}

	events := parseCodexEntry(entry, "fallback-session")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != event.EventSessionStart {
		t.Errorf("type: got %q", events[0].Type)
	}
	if events[0].SessionID != "d4430cef-110d-42e0-924a-bfceeba0c4e1" {
		t.Errorf("session ID should come from meta.id: got %q", events[0].SessionID)
	}
}

func TestParseCodexEntry_FunctionCall(t *testing.T) {
	// Actual Codex format: payload.type == "function_call" with name/arguments/call_id at payload root
	entry := codexLogEntry{
		Timestamp: "2026-01-14T12:07:16.415Z",
		Type:      "response_item",
		Payload:   json.RawMessage(`{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"pnpm -v\"}","call_id":"call_OTjFN4sOjWalj9tGeMFkp5CU"}`),
	}

	events := parseCodexEntry(entry, "test-session")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	ev := events[0]
	if ev.Type != event.EventToolCallStart {
		t.Errorf("type: got %q", ev.Type)
	}
	if ev.ID != "call_OTjFN4sOjWalj9tGeMFkp5CU" {
		t.Errorf("ID should be call_id: got %q", ev.ID)
	}
	if ev.SessionID != "test-session" {
		t.Errorf("session: got %q", ev.SessionID)
	}
	if ev.Data.ToolName != "exec_command" {
		t.Errorf("tool name: got %q", ev.Data.ToolName)
	}
}

func TestParseCodexEntry_FunctionCallOutput(t *testing.T) {
	entry := codexLogEntry{
		Timestamp: "2026-01-14T12:07:16.805Z",
		Type:      "response_item",
		Payload:   json.RawMessage(`{"type":"function_call_output","call_id":"call_OTjFN4sOjWalj9tGeMFkp5CU","output":"Process exited with code 0\npnpm 9.0.1"}`),
	}

	events := parseCodexEntry(entry, "test-session")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	ev := events[0]
	if ev.Type != event.EventToolCallEnd {
		t.Errorf("type: got %q", ev.Type)
	}
	// Must use same call_id as function_call for correlation
	if ev.ID != "call_OTjFN4sOjWalj9tGeMFkp5CU" {
		t.Errorf("ID must match function_call: got %q", ev.ID)
	}
	if ev.SessionID != "test-session" {
		t.Errorf("session: got %q", ev.SessionID)
	}
	if ev.Data.ToolStatus != event.StatusSuccess {
		t.Errorf("status: got %q", ev.Data.ToolStatus)
	}
}

func TestParseCodexEntry_FunctionCallCorrelation(t *testing.T) {
	callID := "call_test_correlate"
	sessionID := "session-abc"

	startEntry := codexLogEntry{
		Timestamp: "2026-01-14T12:07:16.415Z",
		Type:      "response_item",
		Payload:   json.RawMessage(`{"type":"function_call","name":"exec_command","arguments":"{}","call_id":"` + callID + `"}`),
	}
	endEntry := codexLogEntry{
		Timestamp: "2026-01-14T12:07:16.805Z",
		Type:      "response_item",
		Payload:   json.RawMessage(`{"type":"function_call_output","call_id":"` + callID + `","output":"ok"}`),
	}

	startEvents := parseCodexEntry(startEntry, sessionID)
	endEvents := parseCodexEntry(endEntry, sessionID)

	if startEvents[0].ID != endEvents[0].ID {
		t.Errorf("start/end IDs must match: start=%q end=%q", startEvents[0].ID, endEvents[0].ID)
	}
	if startEvents[0].SessionID != sessionID || endEvents[0].SessionID != sessionID {
		t.Error("both events must have session ID from file context")
	}
}

func TestParseCodexEntry_TokenCount(t *testing.T) {
	// Actual Codex token_count format from event_msg
	entry := codexLogEntry{
		Timestamp: "2026-01-14T12:07:16.785Z",
		Type:      "event_msg",
		Payload: json.RawMessage(`{
			"type":"token_count",
			"info":{
				"last_token_usage":{"input_tokens":12983,"output_tokens":20,"total_tokens":13003},
				"model_context_window":258400
			}
		}`),
	}

	events := parseCodexEntry(entry, "test-session")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	ev := events[0]
	if ev.Type != event.EventTokenUsage {
		t.Errorf("type: got %q", ev.Type)
	}
	if ev.SessionID != "test-session" {
		t.Errorf("session: got %q", ev.SessionID)
	}
	if ev.Data.InputTokens != 12983 {
		t.Errorf("input tokens: got %d, want 12983", ev.Data.InputTokens)
	}
	if ev.Data.OutputTokens != 20 {
		t.Errorf("output tokens: got %d, want 20", ev.Data.OutputTokens)
	}
	if ev.Data.CostUSD <= 0 {
		t.Errorf("cost should be > 0, got %f", ev.Data.CostUSD)
	}
}

func TestParseCodexEntry_TokenCountNullInfo(t *testing.T) {
	// First token_count often has null info
	entry := codexLogEntry{
		Timestamp: "2026-01-14T12:07:13.103Z",
		Type:      "event_msg",
		Payload:   json.RawMessage(`{"type":"token_count","info":null}`),
	}

	events := parseCodexEntry(entry, "test-session")
	if len(events) != 0 {
		t.Errorf("null info should produce no events, got %d", len(events))
	}
}

func TestParseCodexEntry_UnknownType(t *testing.T) {
	entry := codexLogEntry{
		Timestamp: "2026-01-14T12:07:10.150Z",
		Type:      "unknown_type",
		Payload:   json.RawMessage(`{}`),
	}

	events := parseCodexEntry(entry, "test-session")
	if len(events) != 0 {
		t.Errorf("unknown type should return nil, got %d", len(events))
	}
}

func TestExtractSessionID(t *testing.T) {
	tests := []struct {
		filename string
		want     string
	}{
		{
			"rollout-2026-01-14T20-03-54-d4430cef-110d-42e0-924a-bfceeba0c4e1.jsonl",
			"d4430cef-110d-42e0-924a-bfceeba0c4e1",
		},
		{
			"short.jsonl",
			"short",
		},
	}

	for _, tt := range tests {
		got := extractSessionID(tt.filename)
		if got != tt.want {
			t.Errorf("extractSessionID(%q) = %q, want %q", tt.filename, got, tt.want)
		}
	}
}
