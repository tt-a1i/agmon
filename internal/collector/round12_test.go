package collector

import (
	"strings"
	"testing"
)

// TestParseClaudeHookStdinDecodesReader covers the public io.Reader entry
// point used by `tokenmeter emit`. (ParseClaudeHook is already tested via
// runEmitWithReader; this targets the stdin variant directly.)
func TestParseClaudeHookStdinDecodesReader(t *testing.T) {
	r := strings.NewReader(`{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"Read","tool_use_id":"tu-1"}`)
	hook, err := ParseClaudeHook(r)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if hook.SessionID != "s1" || hook.HookEventName != "PreToolUse" || hook.ToolName != "Read" || hook.ToolUseID != "tu-1" {
		t.Errorf("decoded fields wrong: %+v", hook)
	}
}

func TestParseClaudeHookRejectsBadJSON(t *testing.T) {
	r := strings.NewReader(`{not valid json`)
	if _, err := ParseClaudeHook(r); err == nil {
		t.Error("expected error on bad JSON")
	}
}

// TestRegisterCodexWatcherWiresPathResolver verifies the package-level
// resolver returns the watcher's indexed path after registration.
func TestRegisterCodexWatcherWiresPathResolver(t *testing.T) {
	// Save and restore the package-global resolver.
	prev := codexPathResolver
	t.Cleanup(func() { codexPathResolver = prev })

	w := NewCodexWatcher(nil)
	w.ensureState()
	w.sessionPaths["sess-x"] = "/var/folders/x/sess-x.jsonl"
	RegisterCodexWatcher(w)

	if codexPathResolver == nil {
		t.Fatal("resolver should be set after registration")
	}
	got := codexPathResolver("sess-x")
	if got != "/var/folders/x/sess-x.jsonl" {
		t.Errorf("resolver lookup = %q, want indexed path", got)
	}

	// Unknown sessionID returns empty string (not panic).
	if codexPathResolver("never-seen") != "" {
		t.Errorf("unknown sessionID should yield empty string")
	}
}

// TestCodexPayloadCallIDFallback covers the synthetic-ID branch when the
// payload lacks a CallID. It must build a stable ID from session+name+ts.
func TestCodexPayloadCallIDFallback(t *testing.T) {
	entry := codexLogEntry{Timestamp: "2026-01-14T12:07:10.150Z"}
	payload := codexResponsePayload{Name: "apply_patch"}

	got := codexPayloadCallID(payload, entry, "sess-1")
	if got == "" {
		t.Fatal("synthetic call_id should not be empty")
	}
	if !strings.HasPrefix(got, "codex-custom-sess-1-apply_patch-") {
		t.Errorf("synthetic id prefix wrong: %q", got)
	}

	// CallID present → return as-is.
	payload2 := codexResponsePayload{CallID: "real-id", Name: "apply_patch"}
	if got := codexPayloadCallID(payload2, entry, "sess-1"); got != "real-id" {
		t.Errorf("present CallID should pass through, got %q", got)
	}

	// Bad timestamp → fallback uses time.Now (just verify non-empty + prefix).
	badEntry := codexLogEntry{Timestamp: "not-a-date"}
	got2 := codexPayloadCallID(payload, badEntry, "sess-2")
	if !strings.HasPrefix(got2, "codex-custom-sess-2-apply_patch-") {
		t.Errorf("bad-ts fallback id wrong: %q", got2)
	}
}
