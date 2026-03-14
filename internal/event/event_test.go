package event

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEventJSON(t *testing.T) {
	ev := Event{
		ID:        "test-123",
		Type:      EventToolCallStart,
		SessionID: "session-abc",
		AgentID:   "agent-1",
		Platform:  PlatformClaude,
		Timestamp: time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC),
		Data: EventData{
			ToolName:   "Edit",
			ToolParams: "src/main.go",
		},
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Event
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.ID != ev.ID {
		t.Errorf("ID: got %q, want %q", decoded.ID, ev.ID)
	}
	if decoded.Type != ev.Type {
		t.Errorf("Type: got %q, want %q", decoded.Type, ev.Type)
	}
	if decoded.Platform != ev.Platform {
		t.Errorf("Platform: got %q, want %q", decoded.Platform, ev.Platform)
	}
	if decoded.Data.ToolName != ev.Data.ToolName {
		t.Errorf("ToolName: got %q, want %q", decoded.Data.ToolName, ev.Data.ToolName)
	}
}

func TestEventTypes(t *testing.T) {
	types := []EventType{
		EventToolCallStart,
		EventToolCallEnd,
		EventAgentStart,
		EventAgentEnd,
		EventTokenUsage,
		EventFileChange,
		EventSessionStart,
		EventSessionEnd,
	}

	for _, typ := range types {
		if typ == "" {
			t.Error("empty event type")
		}
	}
}
