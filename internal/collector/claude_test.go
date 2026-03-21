package collector

import (
	"testing"

	"github.com/tt-a1i/agmon/internal/event"
)

func TestClaudeHookToEvents_PreToolUse(t *testing.T) {
	hook := &ClaudeHookEvent{
		SessionID:     "sess-123",
		HookEventName: "PreToolUse",
		ToolName:      "Edit",
		ToolUseID:     "toolu_abc123",
		ToolInput:     []byte(`{"file_path":"/tmp/test.go","old_string":"foo","new_string":"bar"}`),
	}

	events := ClaudeHookToEvents(hook)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	ev := events[0]
	if ev.Type != event.EventToolCallStart {
		t.Errorf("type: got %q, want %q", ev.Type, event.EventToolCallStart)
	}
	if ev.ID != "toolu_abc123" {
		t.Errorf("ID should be tool_use_id: got %q, want %q", ev.ID, "toolu_abc123")
	}
	if ev.SessionID != "sess-123" {
		t.Errorf("session: got %q", ev.SessionID)
	}
	if ev.Data.ToolName != "Edit" {
		t.Errorf("tool name: got %q", ev.Data.ToolName)
	}
}

func TestClaudeHookToEvents_PostToolUse(t *testing.T) {
	hook := &ClaudeHookEvent{
		SessionID:     "sess-123",
		HookEventName: "PostToolUse",
		ToolName:      "Edit",
		ToolUseID:     "toolu_abc123",
		ToolResult:    "file edited successfully",
	}

	events := ClaudeHookToEvents(hook)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	ev := events[0]
	if ev.Type != event.EventToolCallEnd {
		t.Errorf("type: got %q", ev.Type)
	}
	// Must use same tool_use_id as PreToolUse for correlation
	if ev.ID != "toolu_abc123" {
		t.Errorf("ID must match PreToolUse: got %q, want %q", ev.ID, "toolu_abc123")
	}
	if ev.Data.ToolStatus != event.StatusSuccess {
		t.Errorf("status: got %q", ev.Data.ToolStatus)
	}
}

func TestClaudeHookToEvents_PrePostCorrelation(t *testing.T) {
	toolUseID := "toolu_correlate_test"

	preHook := &ClaudeHookEvent{
		SessionID:     "sess-1",
		HookEventName: "PreToolUse",
		ToolName:      "Bash",
		ToolUseID:     toolUseID,
	}
	postHook := &ClaudeHookEvent{
		SessionID:     "sess-1",
		HookEventName: "PostToolUse",
		ToolName:      "Bash",
		ToolUseID:     toolUseID,
		ToolResult:    "exit 0",
	}

	preEvents := ClaudeHookToEvents(preHook)
	postEvents := ClaudeHookToEvents(postHook)

	if preEvents[0].ID != postEvents[0].ID {
		t.Errorf("Pre/Post IDs must match for correlation: pre=%q post=%q",
			preEvents[0].ID, postEvents[0].ID)
	}
}

func TestClaudeHookToEvents_SessionStart(t *testing.T) {
	hook := &ClaudeHookEvent{
		SessionID:     "sess-new",
		HookEventName: "SessionStart",
	}

	events := ClaudeHookToEvents(hook)
	// SessionStart emits 2 events: EventSessionStart + EventAgentStart (main agent).
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Type != event.EventSessionStart {
		t.Errorf("first event type: got %q, want EventSessionStart", events[0].Type)
	}
	if events[0].SessionID != "sess-new" {
		t.Errorf("session: got %q", events[0].SessionID)
	}
	if events[1].Type != event.EventAgentStart {
		t.Errorf("second event type: got %q, want EventAgentStart", events[1].Type)
	}
	if events[1].Data.AgentRole != "main" {
		t.Errorf("agent role: got %q, want main", events[1].Data.AgentRole)
	}
}

func TestClaudeHookToEvents_SessionEnd(t *testing.T) {
	for _, hookName := range []string{"SessionEnd", "Stop"} {
		hook := &ClaudeHookEvent{
			SessionID:     "sess-done",
			HookEventName: hookName,
		}
		events := ClaudeHookToEvents(hook)
		if len(events) != 1 {
			t.Fatalf("%s: expected 1 event, got %d", hookName, len(events))
		}
		if events[0].Type != event.EventSessionEnd {
			t.Errorf("%s: type: got %q", hookName, events[0].Type)
		}
	}
}

func TestClaudeHookToEvents_SubagentLifecycle(t *testing.T) {
	startHook := &ClaudeHookEvent{
		SessionID:     "sess-1",
		HookEventName: "SubagentStart",
		AgentID:       "agent-sub-1",
		AgentType:     "code-reviewer",
	}
	events := ClaudeHookToEvents(startHook)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != event.EventAgentStart {
		t.Errorf("type: got %q", events[0].Type)
	}
	if events[0].Data.AgentRole != "code-reviewer" {
		t.Errorf("role: got %q", events[0].Data.AgentRole)
	}

	stopHook := &ClaudeHookEvent{
		SessionID:     "sess-1",
		HookEventName: "SubagentStop",
		AgentID:       "agent-sub-1",
	}
	events = ClaudeHookToEvents(stopHook)
	if events[0].Type != event.EventAgentEnd {
		t.Errorf("type: got %q", events[0].Type)
	}
}

func TestClaudeHookToEvents_PostToolUseFailure(t *testing.T) {
	hook := &ClaudeHookEvent{
		SessionID:     "sess-1",
		HookEventName: "PostToolUseFailure",
		ToolName:      "Bash",
		ToolUseID:     "toolu_fail",
		ToolResult:    "command not found",
	}

	events := ClaudeHookToEvents(hook)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Data.ToolStatus != event.StatusFail {
		t.Errorf("status should be fail: got %q", events[0].Data.ToolStatus)
	}
}

func TestClaudeHookToEvents_WriteDetectsFileChange(t *testing.T) {
	hook := &ClaudeHookEvent{
		SessionID:     "sess-1",
		HookEventName: "PostToolUse",
		ToolName:      "Write",
		ToolUseID:     "toolu_write",
		ToolInput:     []byte(`{"file_path":"/tmp/new.go","content":"package main"}`),
		ToolResult:    "file written",
	}

	events := ClaudeHookToEvents(hook)
	if events[0].Data.FilePath != "/tmp/new.go" {
		t.Errorf("file path: got %q", events[0].Data.FilePath)
	}
	if events[0].Data.ChangeType != event.FileCreate {
		t.Errorf("change type: got %q, want %q", events[0].Data.ChangeType, event.FileCreate)
	}
}

func TestClaudeHookToEvents_UnknownEventReturnsEmpty(t *testing.T) {
	hook := &ClaudeHookEvent{
		SessionID:     "sess-1",
		HookEventName: "ConfigChange",
	}
	events := ClaudeHookToEvents(hook)
	if len(events) != 0 {
		t.Errorf("unknown event should return empty, got %d events", len(events))
	}
}
