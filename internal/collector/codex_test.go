package collector

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
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

	events := parseCodexEntryWithContext(entry, "test-session", "gpt-5-codex", "/tmp/project")
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

	events := parseCodexEntryWithContext(entry, "test-session", "gpt-5-codex", "/tmp/project")
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

	events := parseCodexEntryWithContext(entry, "test-session", "gpt-5-codex", "/tmp/project")
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

func TestParseCodexEntry_TokenCountWithoutModelHasNoEstimatedCost(t *testing.T) {
	entry := codexLogEntry{
		Timestamp: "2026-01-14T12:07:16.785Z",
		Type:      "event_msg",
		Payload: json.RawMessage(`{
			"type":"token_count",
			"info":{
				"last_token_usage":{"input_tokens":12983,"output_tokens":20,"total_tokens":13003}
			}
		}`),
	}

	events := parseCodexEntry(entry, "test-session")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Data.Model != "" {
		t.Fatalf("expected empty model, got %q", events[0].Data.Model)
	}
	if events[0].Data.CostUSD != 0 {
		t.Fatalf("expected unpriced event until model is known, got %f", events[0].Data.CostUSD)
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

func TestParseCodexEntry_TurnContextEmitsSessionUpdate(t *testing.T) {
	entry := codexLogEntry{
		Timestamp: "2026-01-14T12:07:10.150Z",
		Type:      "turn_context",
		Payload:   json.RawMessage(`{"cwd":"/tmp/project","model":"gpt-5.5"}`),
	}

	events := parseCodexEntry(entry, "test-session")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != event.EventSessionUpdate {
		t.Fatalf("event type = %q, want %q", events[0].Type, event.EventSessionUpdate)
	}
	if events[0].Data.Model != "gpt-5.5" || events[0].Data.CWD != "/tmp/project" {
		t.Fatalf("unexpected context data: %#v", events[0].Data)
	}
}

func TestCodexTokenSourceIDIncludesSession(t *testing.T) {
	entry := codexLogEntry{
		Timestamp: "2026-01-14T12:07:16.785Z",
		Type:      "event_msg",
		Payload:   json.RawMessage(`{"type":"token_count","info":{"last_token_usage":{"input_tokens":10000,"output_tokens":500,"total_tokens":10500,"cached_input_tokens":8000}}}`),
	}

	a := parseCodexEntryWithContext(entry, "session-a", "gpt-5-codex", "/tmp/a")
	b := parseCodexEntryWithContext(entry, "session-b", "gpt-5-codex", "/tmp/b")
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("expected one token event per session, got %d and %d", len(a), len(b))
	}
	if a[0].ID == b[0].ID {
		t.Fatalf("token event IDs collide across sessions: %q", a[0].ID)
	}
}

func TestCodexWatcher_EmitsIdenticalTokenUsageAtDifferentTimestamps(t *testing.T) {
	dir := t.TempDir()
	sessionID := "dedup111-1111-1111-1111-111111111111"
	path := filepath.Join(dir, "rollout-2026-01-14T20-03-54-"+sessionID+".jsonl")
	writeLinesToFile(t, path,
		`{"timestamp":"2026-01-14T12:07:10.150Z","type":"turn_context","payload":{"cwd":"/tmp/project","model":"gpt-5-codex"}}`,
		`{"timestamp":"2026-01-14T12:07:16.785Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":12879,"output_tokens":57,"total_tokens":12936}}}}`,
		`{"timestamp":"2026-01-14T12:07:19.661Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":12879,"output_tokens":57,"total_tokens":12936}}}}`,
	)

	var emitted []event.Event
	w := NewCodexWatcher(func(ev event.Event) { emitted = append(emitted, ev) })
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat test file: %v", err)
	}
	w.processFile(path, info.Size())

	var tokenEvents []event.Event
	for _, ev := range emitted {
		if ev.Type == event.EventTokenUsage {
			tokenEvents = append(tokenEvents, ev)
		}
	}
	if len(tokenEvents) != 2 {
		t.Fatalf("expected 2 token events with identical usage at different timestamps, got %d", len(tokenEvents))
	}
	if tokenEvents[0].ID == tokenEvents[1].ID {
		t.Fatalf("token event IDs should differ by timestamp, got %q", tokenEvents[0].ID)
	}
}

func TestCodexWatcher_DedupesRepeatedTotalTokenUsageSnapshots(t *testing.T) {
	dir := t.TempDir()
	sessionID := "dedup222-2222-2222-2222-222222222222"
	path := filepath.Join(dir, "rollout-2026-01-14T20-03-54-"+sessionID+".jsonl")
	writeLinesToFile(t, path,
		`{"timestamp":"2026-01-14T12:07:10.150Z","type":"turn_context","payload":{"cwd":"/tmp/project","model":"gpt-5-codex"}}`,
		`{"timestamp":"2026-01-14T12:07:16.785Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":12879,"cached_input_tokens":8000,"output_tokens":57,"total_tokens":12936},"last_token_usage":{"input_tokens":12879,"cached_input_tokens":8000,"output_tokens":57,"total_tokens":12936}}}}`,
		`{"timestamp":"2026-01-14T12:07:19.661Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":12879,"cached_input_tokens":8000,"output_tokens":57,"total_tokens":12936},"last_token_usage":{"input_tokens":12879,"cached_input_tokens":8000,"output_tokens":57,"total_tokens":12936}}}}`,
	)

	var emitted []event.Event
	w := NewCodexWatcher(func(ev event.Event) { emitted = append(emitted, ev) })
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat test file: %v", err)
	}
	w.processFile(path, info.Size())

	var tokenEvents []event.Event
	for _, ev := range emitted {
		if ev.Type == event.EventTokenUsage {
			tokenEvents = append(tokenEvents, ev)
		}
	}
	if len(tokenEvents) != 2 {
		t.Fatalf("watcher should emit both snapshots; storage source_id handles replay dedupe, got %d", len(tokenEvents))
	}
	if tokenEvents[0].ID != tokenEvents[1].ID {
		t.Fatalf("repeated total token snapshots should share source id, got %q and %q", tokenEvents[0].ID, tokenEvents[1].ID)
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
		seen:   make(map[string]int64),
		emitFn: func(ev event.Event) { emitted = append(emitted, ev) },
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

func TestCodexWatcher_TurnContextBackfillsEarlierTokenUsage(t *testing.T) {
	dir := t.TempDir()
	sessionID := "backfill-1111-1111-1111-111111111111"
	path := filepath.Join(dir, "rollout-2026-01-14T20-03-54-"+sessionID+".jsonl")
	writeLinesToFile(t, path,
		`{"timestamp":"2026-01-14T12:07:09.000Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":1000,"output_tokens":100,"total_tokens":1100,"cached_input_tokens":200}}}}`,
		`{"timestamp":"2026-01-14T12:07:10.150Z","type":"turn_context","payload":{"cwd":"/tmp/project","model":"gpt-5-codex"}}`,
	)

	var emitted []event.Event
	w := NewCodexWatcher(func(ev event.Event) { emitted = append(emitted, ev) })
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat test file: %v", err)
	}
	w.processFile(path, info.Size())

	var tokenEvent *event.Event
	for i := range emitted {
		if emitted[i].Type == event.EventTokenUsage {
			tokenEvent = &emitted[i]
			break
		}
	}
	if tokenEvent == nil {
		t.Fatalf("expected token usage event, got %#v", emitted)
	}
	if tokenEvent.Data.Model != "gpt-5-codex" {
		t.Fatalf("token event should be backfilled with model, got %q", tokenEvent.Data.Model)
	}
	if tokenEvent.Data.CWD != "/tmp/project" {
		t.Fatalf("token event should be backfilled with cwd, got %q", tokenEvent.Data.CWD)
	}
	if tokenEvent.Data.CostUSD <= 0 {
		t.Fatalf("token event should be priced after backfill, got %f", tokenEvent.Data.CostUSD)
	}
}

func TestCodexWatcher_CustomToolApplyPatchEmitsFileChanges(t *testing.T) {
	dir := t.TempDir()
	sessionID := "custom11-1111-1111-1111-111111111111"
	path := filepath.Join(dir, "rollout-2026-01-14T20-03-54-"+sessionID+".jsonl")
	patch := "*** Begin Patch\n*** Update File: custom.txt\n-old\n+new\n*** End Patch\n"
	writeLinesToFile(t, path,
		`{"timestamp":"2026-01-14T12:07:10.150Z","type":"turn_context","payload":{"cwd":"/tmp/project","model":"gpt-5-codex"}}`,
		fmt.Sprintf(`{"timestamp":"2026-01-14T12:07:16.415Z","type":"response_item","payload":{"type":"custom_tool_call","status":"completed","call_id":"call_custom_patch","name":"apply_patch","input":%q}}`, patch),
	)

	var emitted []event.Event
	w := NewCodexWatcher(func(ev event.Event) { emitted = append(emitted, ev) })
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat test file: %v", err)
	}
	w.processFile(path, info.Size())

	var sawToolStart, sawToolEnd bool
	var fileChanges []event.Event
	for _, ev := range emitted {
		switch ev.Type {
		case event.EventToolCallStart:
			if ev.ID == "call_custom_patch" && ev.Data.ToolName == "apply_patch" {
				sawToolStart = true
			}
		case event.EventToolCallEnd:
			if ev.ID == "call_custom_patch" && ev.Data.ToolStatus == event.StatusSuccess {
				sawToolEnd = true
			}
		case event.EventFileChange:
			fileChanges = append(fileChanges, ev)
		}
	}
	if !sawToolStart || !sawToolEnd {
		t.Fatalf("custom apply_patch should emit tool start/end, start=%v end=%v events=%#v", sawToolStart, sawToolEnd, emitted)
	}
	if len(fileChanges) != 1 {
		t.Fatalf("expected 1 file change from custom apply_patch, got %d", len(fileChanges))
	}
	if fileChanges[0].Data.FilePath != "custom.txt" || fileChanges[0].Data.ChangeType != event.FileEdit {
		t.Fatalf("unexpected file change: %#v", fileChanges[0].Data)
	}
}

func TestCodexWatcher_CustomToolNonApplyPatchDoesNotEmitFileChanges(t *testing.T) {
	dir := t.TempDir()
	sessionID := "custom22-2222-2222-2222-222222222222"
	path := filepath.Join(dir, "rollout-2026-01-14T20-03-54-"+sessionID+".jsonl")
	input := "*** Begin Patch\n*** Update File: not-a-patch.txt\n-old\n+new\n*** End Patch\n"
	writeLinesToFile(t, path,
		`{"timestamp":"2026-01-14T12:07:10.150Z","type":"turn_context","payload":{"cwd":"/tmp/project","model":"gpt-5-codex"}}`,
		fmt.Sprintf(`{"timestamp":"2026-01-14T12:07:16.415Z","type":"response_item","payload":{"type":"custom_tool_call","status":"completed","call_id":"call_custom_other","name":"shell","input":%q}}`, input),
	)

	var emitted []event.Event
	w := NewCodexWatcher(func(ev event.Event) { emitted = append(emitted, ev) })
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat test file: %v", err)
	}
	w.processFile(path, info.Size())

	for _, ev := range emitted {
		if ev.Type == event.EventFileChange {
			t.Fatalf("non-apply_patch custom tool should not emit file change: %#v", ev)
		}
	}
}

func TestCodexWatcher_FullDiscoverKeepsPendingApplyPatchAcrossScans(t *testing.T) {
	baseDir := filepath.Join(t.TempDir(), "sessions")
	now := time.Now().UTC()
	dir := filepath.Join(baseDir, now.Format("2006"), now.Format("01"), now.Format("02"))
	sessionID := "pending1-1111-1111-1111-111111111111"
	path := filepath.Join(dir, "rollout-"+now.Format("2006-01-02T15-04-05")+"-"+sessionID+".jsonl")

	patch := "*** Begin Patch\n*** Add File: pending.txt\n+hello\n*** End Patch"
	argsJSON, err := json.Marshal(map[string]string{"input": patch})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	writeLinesToFile(t, path,
		`{"timestamp":"2026-01-14T12:07:10.150Z","type":"turn_context","payload":{"cwd":"/tmp/project","model":"gpt-5-codex"}}`,
		fmt.Sprintf(`{"timestamp":"2026-01-14T12:07:16.415Z","type":"response_item","payload":{"type":"function_call","name":"apply_patch","arguments":%q,"call_id":"call_pending"}}`, string(argsJSON)),
	)

	var emitted []event.Event
	w := NewCodexWatcher(func(ev event.Event) { emitted = append(emitted, ev) })
	w.baseDirs = []string{baseDir}
	w.scanLogs()

	if !w.initialDiscovery {
		t.Fatal("expected initial discovery to complete")
	}
	if _, ok := w.pendingFileChanges["call_pending"]; !ok {
		t.Fatal("expected pending apply_patch changes to survive initial discovery")
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open append: %v", err)
	}
	fmt.Fprintln(f, `{"timestamp":"2026-01-14T12:07:16.805Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_pending","output":"ok"}}`)
	if err := f.Close(); err != nil {
		t.Fatalf("close append: %v", err)
	}

	w.scanLogs()
	fileChanges := 0
	for _, ev := range emitted {
		if ev.Type == event.EventFileChange {
			fileChanges++
		}
	}
	if fileChanges != 1 {
		t.Fatalf("expected pending apply_patch output to emit 1 file change, got %d events: %#v", fileChanges, emitted)
	}
	if _, ok := w.pendingFileChanges["call_pending"]; ok {
		t.Fatal("pending apply_patch changes should be cleared after output")
	}
}

func TestCodexWatcher_ScanLogsReusesKnownPathsAndFindsNewRecentFiles(t *testing.T) {
	baseDir := filepath.Join(t.TempDir(), "sessions")
	now := time.Now().UTC()
	dir := filepath.Join(baseDir, now.Format("2006"), now.Format("01"), now.Format("02"))

	session1 := "11111111-1111-1111-1111-111111111111"
	path1 := filepath.Join(dir, "rollout-"+now.Format("2006-01-02T15-04-05")+"-"+session1+".jsonl")
	writeLinesToFile(t, path1,
		fmt.Sprintf(`{"timestamp":"%s","type":"session_meta","payload":{"id":"%s","cwd":"/tmp/a"}}`, now.Format(time.RFC3339), session1),
		fmt.Sprintf(`{"timestamp":"%s","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":100,"output_tokens":10,"total_tokens":110}}}}`, now.Format(time.RFC3339)),
	)

	var emitted []event.Event
	w := NewCodexWatcher(func(ev event.Event) { emitted = append(emitted, ev) })
	w.baseDirs = []string{baseDir}

	w.scanLogs()

	if !w.initialDiscovery {
		t.Fatal("expected initial discovery to complete after first scan")
	}
	if got := len(w.seen); got != 1 {
		t.Fatalf("expected 1 indexed file after first scan, got %d", got)
	}
	if got := len(emitted); got != 2 {
		t.Fatalf("expected 2 emitted events (SessionStart+TokenUsage) after first scan, got %d", got)
	}

	session2 := "22222222-2222-2222-2222-222222222222"
	path2 := filepath.Join(dir, "rollout-"+now.Add(time.Second).Format("2006-01-02T15-04-05")+"-"+session2+".jsonl")
	writeLinesToFile(t, path2,
		fmt.Sprintf(`{"timestamp":"%s","type":"session_meta","payload":{"id":"%s","cwd":"/tmp/b"}}`, now.Add(time.Second).Format(time.RFC3339), session2),
		fmt.Sprintf(`{"timestamp":"%s","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":200,"output_tokens":20,"total_tokens":220}}}}`, now.Add(time.Second).Format(time.RFC3339)),
	)

	w.scanLogs()

	if got := len(w.seen); got != 2 {
		t.Fatalf("expected second scan to index new recent file, got %d", got)
	}
	if got := len(emitted); got != 4 {
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

func TestCodexWatcher_EmptySessionsFiltered(t *testing.T) {
	// JSONL files containing only session_meta (no tokens, no tool calls) should
	// not produce emitted events — they create phantom sessions in the dashboard.
	baseDir := filepath.Join(t.TempDir(), "sessions")
	now := time.Now().UTC()
	dir := filepath.Join(baseDir, now.Format("2006"), now.Format("01"), now.Format("02"))

	session1 := "aaaa1111-1111-1111-1111-111111111111"
	path1 := filepath.Join(dir, "rollout-"+now.Format("2006-01-02T15-04-05")+"-"+session1+".jsonl")
	writeLinesToFile(t, path1,
		fmt.Sprintf(`{"timestamp":"%s","type":"session_meta","payload":{"id":"%s","cwd":"/"}}`, now.Format(time.RFC3339), session1),
	)

	var emitted []event.Event
	w := NewCodexWatcher(func(ev event.Event) { emitted = append(emitted, ev) })
	w.baseDirs = []string{baseDir}

	// First scan (fullDiscover) — empty session should be filtered.
	w.scanLogs()
	if got := len(emitted); got != 0 {
		t.Fatalf("expected 0 emitted events for session_meta-only file, got %d", got)
	}
	// File should still be tracked so we don't re-process it.
	if _, ok := w.seen[path1]; !ok {
		t.Fatal("expected file to be tracked in seen map")
	}
	// SessionStart should be deferred, not lost.
	if _, ok := w.pendingStarts[session1]; !ok {
		t.Fatal("expected deferred SessionStart in pendingStarts")
	}

	// A turn_context alone is metadata, not durable activity.
	f, err := os.OpenFile(path1, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open append: %v", err)
	}
	fmt.Fprintf(f, `{"timestamp":"%s","type":"turn_context","payload":{"cwd":"/","model":"gpt-5-codex"}}`+"\n", now.Add(500*time.Millisecond).Format(time.RFC3339))
	if err := f.Close(); err != nil {
		t.Fatalf("close append: %v", err)
	}
	w.scanLogs()
	if got := len(emitted); got != 0 {
		t.Fatalf("expected 0 emitted events for session_meta+turn_context-only file, got %d", got)
	}

	// Second scan (incremental via scanRecentDirs) — add a new empty file.
	session2 := "bbbb2222-2222-2222-2222-222222222222"
	path2 := filepath.Join(dir, "rollout-"+now.Add(time.Second).Format("2006-01-02T15-04-05")+"-"+session2+".jsonl")
	writeLinesToFile(t, path2,
		fmt.Sprintf(`{"timestamp":"%s","type":"session_meta","payload":{"id":"%s","cwd":"/"}}`, now.Add(time.Second).Format(time.RFC3339), session2),
	)

	w.scanLogs()
	if got := len(emitted); got != 0 {
		t.Fatalf("expected still 0 emitted events after incremental scan of empty file, got %d", got)
	}
}

func TestCodexWatcher_DeferredStartEmittedWhenDataArrives(t *testing.T) {
	// When a file initially has only session_meta, SessionStart is deferred.
	// When real data arrives later, the deferred start is emitted first with
	// the correct original timestamp and CWD.
	baseDir := filepath.Join(t.TempDir(), "sessions")
	now := time.Now().UTC()
	dir := filepath.Join(baseDir, now.Format("2006"), now.Format("01"), now.Format("02"))

	sessionID := "cccc3333-3333-3333-3333-333333333333"
	path := filepath.Join(dir, "rollout-"+now.Format("2006-01-02T15-04-05")+"-"+sessionID+".jsonl")
	startTS := now.Add(-10 * time.Minute).Format(time.RFC3339)
	writeLinesToFile(t, path,
		fmt.Sprintf(`{"timestamp":"%s","type":"session_meta","payload":{"id":"%s","cwd":"/home/user/project"}}`, startTS, sessionID),
	)

	var emitted []event.Event
	w := NewCodexWatcher(func(ev event.Event) { emitted = append(emitted, ev) })
	w.baseDirs = []string{baseDir}

	// First scan: only session_meta → deferred.
	w.scanLogs()
	if len(emitted) != 0 {
		t.Fatalf("expected 0 events, got %d", len(emitted))
	}

	// Append token_count data to the file.
	tokenTS := now.Format(time.RFC3339)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(f, `{"timestamp":"%s","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":500,"output_tokens":50,"total_tokens":550}}}}`+"\n", tokenTS)
	f.Close()

	// Second scan: file grew → incremental processFile.
	w.scanLogs()

	if len(emitted) < 2 {
		t.Fatalf("expected at least 2 events (deferred SessionStart + TokenUsage), got %d", len(emitted))
	}

	// First event should be the deferred SessionStart with original timestamp.
	if emitted[0].Type != event.EventSessionStart {
		t.Errorf("first event should be SessionStart, got %s", emitted[0].Type)
	}
	if emitted[0].Data.CWD != "/home/user/project" {
		t.Errorf("deferred SessionStart should preserve CWD, got %q", emitted[0].Data.CWD)
	}

	// Second event should be TokenUsage.
	found := false
	for _, ev := range emitted[1:] {
		if ev.Type == event.EventTokenUsage {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected TokenUsage event after deferred SessionStart")
	}

	// Pending should be cleared.
	if _, ok := w.pendingStarts[sessionID]; ok {
		t.Error("pendingStart should be cleared after emission")
	}
}

func TestCodexWatcher_CommitsValidJSONAtEOFWithoutTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	sessionID := "dddd4444-4444-4444-4444-444444444444"
	path := filepath.Join(dir, "rollout-2026-01-14T20-03-54-"+sessionID+".jsonl")

	patch := "*** Begin Patch\n*** Add File: eof.txt\n+hello\n*** End Patch"
	argsJSON, err := json.Marshal(map[string]string{"input": patch})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	lines := []string{
		`{"timestamp":"2026-01-14T12:07:10.150Z","type":"turn_context","payload":{"cwd":"/tmp/project","model":"gpt-5-codex"}}`,
		fmt.Sprintf(`{"timestamp":"2026-01-14T12:07:16.415Z","type":"response_item","payload":{"type":"function_call","name":"apply_patch","arguments":%q,"call_id":"call_eof"}}`, string(argsJSON)),
		`{"timestamp":"2026-01-14T12:07:16.805Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_eof","output":"ok"}}`,
	}
	data := []byte(joinLines(lines)) // Intentionally no trailing newline.
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write log file: %v", err)
	}

	var emitted []event.Event
	w := NewCodexWatcher(func(ev event.Event) { emitted = append(emitted, ev) })

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat test file: %v", err)
	}
	w.processFile(path, info.Size())

	if got := w.seen[path]; got != int64(len(data)) {
		t.Fatalf("expected EOF JSON line to be committed, got offset %d want %d", got, len(data))
	}

	fileChanges := 0
	for _, ev := range emitted {
		if ev.Type == event.EventFileChange {
			fileChanges++
		}
	}
	if fileChanges != 1 {
		t.Fatalf("expected 1 file change, got %d events: %#v", fileChanges, emitted)
	}

	w.processFile(path, info.Size())
	afterSecondScan := 0
	for _, ev := range emitted {
		if ev.Type == event.EventFileChange {
			afterSecondScan++
		}
	}
	if afterSecondScan != fileChanges {
		t.Fatalf("expected second scan to emit no additional file changes, got before=%d after=%d", fileChanges, afterSecondScan)
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
