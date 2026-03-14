package collector

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/tt-a1i/agmon/internal/event"
)

// EmitEvent sends a single event to the daemon via Unix socket.
// Used by the `agmon emit` CLI command called from Claude Code hooks.
func EmitEvent(sockPath string, ev event.Event) error {
	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}
	defer conn.Close()

	return json.NewEncoder(conn).Encode(ev)
}

// ClaudeHookEvent represents the JSON that Claude Code passes to hooks.
type ClaudeHookEvent struct {
	Type      string `json:"type"`       // PreToolUse, PostToolUse, Notification
	SessionID string `json:"session_id"`
	ToolName  string `json:"tool_name,omitempty"`
	ToolInput string `json:"tool_input,omitempty"`
	ToolOutput string `json:"tool_output,omitempty"`
}

// ParseClaudeHookStdin reads and parses Claude Code hook input from stdin.
func ParseClaudeHookStdin() (*ClaudeHookEvent, error) {
	var hookEvent ClaudeHookEvent
	dec := json.NewDecoder(os.Stdin)
	if err := dec.Decode(&hookEvent); err != nil {
		return nil, fmt.Errorf("decode hook stdin: %w", err)
	}
	return &hookEvent, nil
}

// ClaudeHookToEvent converts a Claude Code hook event into our unified event format.
func ClaudeHookToEvent(hook *ClaudeHookEvent, callID string) event.Event {
	now := time.Now()
	ev := event.Event{
		ID:        callID,
		SessionID: hook.SessionID,
		Platform:  event.PlatformClaude,
		Timestamp: now,
	}

	switch hook.Type {
	case "PreToolUse":
		ev.Type = event.EventToolCallStart
		ev.Data = event.EventData{
			ToolName:   hook.ToolName,
			ToolParams: truncate(hook.ToolInput, 500),
		}
	case "PostToolUse":
		ev.Type = event.EventToolCallEnd
		ev.Data = event.EventData{
			ToolName:   hook.ToolName,
			ToolResult: truncate(hook.ToolOutput, 500),
			ToolStatus: event.StatusSuccess,
		}
	}

	return ev
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
