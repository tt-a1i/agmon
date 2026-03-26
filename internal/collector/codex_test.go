package collector

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestCodexWatcher_DedupsRepeatedTokenCount(t *testing.T) {
	var emitted []event.Event
	w := &CodexWatcher{
		lastTokenUsage: make(map[string]string),
		emitFn:         func(ev event.Event) { emitted = append(emitted, ev) },
	}

	// Simulate 3 token_count entries: first two have same values, third is different
	entries := []codexLogEntry{
		{Timestamp: "2026-01-14T12:07:16.785Z", Type: "event_msg",
			Payload: json.RawMessage(`{"type":"token_count","info":{"last_token_usage":{"input_tokens":12879,"output_tokens":57,"total_tokens":12936}}}`)},
		{Timestamp: "2026-01-14T12:07:19.661Z", Type: "event_msg",
			Payload: json.RawMessage(`{"type":"token_count","info":{"last_token_usage":{"input_tokens":12879,"output_tokens":57,"total_tokens":12936}}}`)},
		{Timestamp: "2026-01-14T12:07:21.533Z", Type: "event_msg",
			Payload: json.RawMessage(`{"type":"token_count","info":{"last_token_usage":{"input_tokens":12983,"output_tokens":20,"total_tokens":13003}}}`)},
	}

	sessionID := "test-dedup"
	for _, entry := range entries {
		for _, ev := range parseCodexEntry(entry, sessionID) {
			if ev.Type == event.EventTokenUsage {
				key := fmt.Sprintf("%d:%d", ev.Data.InputTokens, ev.Data.OutputTokens)
				if w.lastTokenUsage[sessionID] == key {
					continue
				}
				w.lastTokenUsage[sessionID] = key
			}
			w.emitFn(ev)
		}
	}

	// Should only emit 2 events (first unique + third unique), not 3
	if len(emitted) != 2 {
		t.Fatalf("expected 2 deduplicated token events, got %d", len(emitted))
	}
	if emitted[0].Data.InputTokens != 12879 {
		t.Errorf("first event input: got %d, want 12879", emitted[0].Data.InputTokens)
	}
	if emitted[1].Data.InputTokens != 12983 {
		t.Errorf("second event input: got %d, want 12983", emitted[1].Data.InputTokens)
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

func TestCodexWatcher_TurnContextAnnotatesTokensAndApplyPatchProducesFileChanges(t *testing.T) {
	dir := t.TempDir()
	sessionID := "d4430cef-110d-42e0-924a-bfceeba0c4e1"
	path := filepath.Join(dir, "rollout-2026-01-14T20-03-54-"+sessionID+".jsonl")

	patch := "*** Begin Patch\n*** Update File: foo.txt\n@@\n-old\n+new\n*** Add File: bar.txt\n+hello\n*** Delete File: old.txt\n*** End Patch"
	argsJSON, err := json.Marshal(map[string]string{"input": patch})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	lines := []string{
		`{"timestamp":"2026-01-14T12:07:10.150Z","type":"turn_context","payload":{"cwd":"/tmp/project","model":"gpt-5-codex","effort":"high","summary":"auto"}}`,
		fmt.Sprintf(`{"timestamp":"2026-01-14T12:07:16.415Z","type":"response_item","payload":{"type":"function_call","name":"apply_patch","arguments":%q,"call_id":"call_patch"}}`, string(argsJSON)),
		`{"timestamp":"2026-01-14T12:07:16.805Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_patch","output":"ok"}}`,
		`{"timestamp":"2026-01-14T12:07:16.905Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":12983,"output_tokens":20,"total_tokens":13003}}}}`,
	}
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%s\n", joinLines(lines))), 0o644); err != nil {
		t.Fatalf("write log file: %v", err)
	}

	var emitted []event.Event
	w := &CodexWatcher{
		seen:           make(map[string]int64),
		lastTokenUsage: make(map[string]string),
		emitFn:         func(ev event.Event) { emitted = append(emitted, ev) },
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat test file: %v", err)
	}
	w.processFile(path, info.Size())

	var tokenEvent *event.Event
	var fileChanges []event.Event
	for i := range emitted {
		ev := emitted[i]
		if ev.Type == event.EventTokenUsage {
			tokenEvent = &emitted[i]
		}
		if ev.Type == event.EventFileChange {
			fileChanges = append(fileChanges, ev)
		}
	}

	if tokenEvent == nil {
		t.Fatalf("expected token usage event, got %#v", emitted)
	}
	if tokenEvent.Data.Model != "gpt-5-codex" {
		t.Fatalf("token event should carry model from turn_context, got %q", tokenEvent.Data.Model)
	}
	if tokenEvent.Data.CWD != "/tmp/project" {
		t.Fatalf("token event should carry cwd from turn_context, got %q", tokenEvent.Data.CWD)
	}
	if len(fileChanges) != 3 {
		t.Fatalf("expected 3 file change events from apply_patch, got %d", len(fileChanges))
	}
}

func TestCodexWatcher_ScanLogsReusesKnownPathsAndFindsNewRecentFiles(t *testing.T) {
	baseDir := filepath.Join(t.TempDir(), "sessions")
	now := time.Now().UTC()
	dir := filepath.Join(baseDir, now.Format("2006"), now.Format("01"), now.Format("02"))

	session1 := "11111111-1111-1111-1111-111111111111"
	path1 := filepath.Join(dir, "rollout-"+now.Format("2006-01-02T15-04-05")+"-"+session1+".jsonl")
	writeLinesToFile(t, path1, fmt.Sprintf(`{"timestamp":"%s","type":"session_meta","payload":{"id":"%s","cwd":"/tmp/a"}}`, now.Format(time.RFC3339), session1))

	var emitted []event.Event
	w := NewCodexWatcher(func(ev event.Event) { emitted = append(emitted, ev) })
	w.baseDir = baseDir

	w.scanLogs()

	if !w.initialDiscovery {
		t.Fatal("expected initial discovery to complete after first scan")
	}
	if got := len(w.seen); got != 1 {
		t.Fatalf("expected 1 indexed file after first scan, got %d", got)
	}
	if got := len(emitted); got != 1 {
		t.Fatalf("expected 1 emitted event after first scan, got %d", got)
	}

	session2 := "22222222-2222-2222-2222-222222222222"
	path2 := filepath.Join(dir, "rollout-"+now.Add(time.Second).Format("2006-01-02T15-04-05")+"-"+session2+".jsonl")
	writeLinesToFile(t, path2, fmt.Sprintf(`{"timestamp":"%s","type":"session_meta","payload":{"id":"%s","cwd":"/tmp/b"}}`, now.Add(time.Second).Format(time.RFC3339), session2))

	w.scanLogs()

	if got := len(w.seen); got != 2 {
		t.Fatalf("expected second scan to index new recent file, got %d", got)
	}
	if got := len(emitted); got != 2 {
		t.Fatalf("expected unchanged file to stay deduped and new file to emit once, got %d events", got)
	}

	w.pathsMu.RLock()
	defer w.pathsMu.RUnlock()
	if got := w.sessionPaths[session1]; got != path1 {
		t.Fatalf("expected session1 path index %q, got %q", path1, got)
	}
	if got := w.sessionPaths[session2]; got != path2 {
		t.Fatalf("expected session2 path index %q, got %q", path2, got)
	}
}

func writeLinesToFile(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	data := joinLines(lines) + "\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func joinLines(lines []string) string {
	out := ""
	for i, line := range lines {
		if i > 0 {
			out += "\n"
		}
		out += line
	}
	return out
}
