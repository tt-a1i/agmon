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
func EmitEvent(sockPath string, ev event.Event) error {
	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}
	defer conn.Close()

	return json.NewEncoder(conn).Encode(ev)
}

// ClaudeHookEvent represents the actual JSON that Claude Code sends to hook stdin.
// Common fields are present on all hook events.
// Tool-specific fields are present on PreToolUse/PostToolUse.
type ClaudeHookEvent struct {
	// Common fields (all hooks)
	SessionID     string `json:"session_id"`
	HookEventName string `json:"hook_event_name"`
	CWD           string `json:"cwd,omitempty"`

	// Agent fields
	AgentID   string `json:"agent_id,omitempty"`
	AgentType string `json:"agent_type,omitempty"`

	// Tool fields (PreToolUse, PostToolUse, PostToolUseFailure)
	ToolName   string          `json:"tool_name,omitempty"`
	ToolInput  json.RawMessage `json:"tool_input,omitempty"`
	ToolResult string          `json:"tool_result,omitempty"`
	ToolUseID  string          `json:"tool_use_id,omitempty"`

	// Stop/SubagentStop
	Reason               string `json:"reason,omitempty"`
	AgentTranscriptPath  string `json:"agent_transcript_path,omitempty"`
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

// ClaudeHookToEvents converts a Claude Code hook event into one or more unified events.
// For tool calls, it uses tool_use_id from Claude to correlate Pre/Post.
func ClaudeHookToEvents(hook *ClaudeHookEvent) []event.Event {
	now := time.Now()
	base := event.Event{
		SessionID: hook.SessionID,
		AgentID:   hook.AgentID,
		Platform:  event.PlatformClaude,
		Timestamp: now,
	}

	var events []event.Event

	switch hook.HookEventName {
	case "SessionStart":
		ev := base
		ev.ID = fmt.Sprintf("session-start-%s", hook.SessionID)
		ev.Type = event.EventSessionStart
		events = append(events, ev)

	case "SessionEnd", "Stop":
		ev := base
		ev.ID = fmt.Sprintf("session-end-%s", hook.SessionID)
		ev.Type = event.EventSessionEnd
		events = append(events, ev)

	case "PreToolUse":
		ev := base
		ev.ID = hook.ToolUseID
		if ev.ID == "" {
			ev.ID = fmt.Sprintf("tool-%s-%d", hook.ToolName, now.UnixNano())
		}
		ev.Type = event.EventToolCallStart
		ev.Data = event.EventData{
			ToolName:   hook.ToolName,
			ToolParams: truncateBytes(hook.ToolInput, 500),
		}
		events = append(events, ev)

	case "PostToolUse":
		ev := base
		ev.ID = hook.ToolUseID
		if ev.ID == "" {
			ev.ID = fmt.Sprintf("tool-%s-%d", hook.ToolName, now.UnixNano())
		}
		ev.Type = event.EventToolCallEnd
		ev.Data = event.EventData{
			ToolName:   hook.ToolName,
			ToolResult: truncate(hook.ToolResult, 500),
			ToolStatus: event.StatusSuccess,
		}
		// Detect file changes from Edit/Write tool calls
		if hook.ToolName == "Edit" || hook.ToolName == "Write" {
			filePath := extractFilePath(hook.ToolInput)
			if filePath != "" {
				ev.Data.FilePath = filePath
				ev.Data.ChangeType = event.FileEdit
				if hook.ToolName == "Write" {
					ev.Data.ChangeType = event.FileCreate
				}
			}
		}
		events = append(events, ev)

	case "PostToolUseFailure":
		ev := base
		ev.ID = hook.ToolUseID
		if ev.ID == "" {
			ev.ID = fmt.Sprintf("tool-%s-%d", hook.ToolName, now.UnixNano())
		}
		ev.Type = event.EventToolCallEnd
		ev.Data = event.EventData{
			ToolName:   hook.ToolName,
			ToolResult: truncate(hook.ToolResult, 500),
			ToolStatus: event.StatusFail,
		}
		events = append(events, ev)

	case "SubagentStart":
		ev := base
		ev.ID = fmt.Sprintf("agent-start-%s", hook.AgentID)
		ev.Type = event.EventAgentStart
		ev.Data = event.EventData{
			AgentRole: hook.AgentType,
		}
		events = append(events, ev)

	case "SubagentStop":
		ev := base
		ev.ID = fmt.Sprintf("agent-end-%s", hook.AgentID)
		ev.Type = event.EventAgentEnd
		events = append(events, ev)
	}

	return events
}

func extractFilePath(toolInput json.RawMessage) string {
	if len(toolInput) == 0 {
		return ""
	}
	var input struct {
		FilePath string `json:"file_path"`
	}
	if json.Unmarshal(toolInput, &input) == nil && input.FilePath != "" {
		return input.FilePath
	}
	return ""
}

func truncateBytes(b json.RawMessage, maxLen int) string {
	s := string(b)
	return truncate(s, maxLen)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
