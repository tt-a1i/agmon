package collector

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/tt-a1i/agmon/internal/event"
)

// EmitEvent sends a single event to the daemon via socket.
func EmitEvent(sockPath string, ev event.Event) error {
	conn, err := dialDaemon(sockPath)
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
	Reason              string `json:"reason,omitempty"`
	AgentTranscriptPath string `json:"agent_transcript_path,omitempty"`

	// SessionStart
	GitBranch string `json:"gitBranch,omitempty"`
}

// ParseClaudeHookStdin reads and parses Claude Code hook input from stdin.
func ParseClaudeHookStdin() (*ClaudeHookEvent, error) {
	return ParseClaudeHook(os.Stdin)
}

// ParseClaudeHook reads and parses Claude Code hook input from the provided reader.
func ParseClaudeHook(r io.Reader) (*ClaudeHookEvent, error) {
	var hookEvent ClaudeHookEvent
	dec := json.NewDecoder(r)
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
		ev.Data = event.EventData{
			CWD:       hook.CWD,
			GitBranch: hook.GitBranch,
		}
		events = append(events, ev)

		// Register the main agent so Agent Tree is never empty.
		agentEv := base
		agentEv.ID = fmt.Sprintf("agent-main-%s", hook.SessionID)
		agentEv.AgentID = fmt.Sprintf("main-%s", hook.SessionID)
		agentEv.Type = event.EventAgentStart
		agentEv.Data = event.EventData{AgentRole: "main"}
		events = append(events, agentEv)

	case "SessionEnd":
		ev := base
		ev.ID = fmt.Sprintf("session-end-%s", hook.SessionID)
		ev.Type = event.EventSessionEnd
		events = append(events, ev)

	case "Stop":
		// Stop = Claude finished one turn, NOT session end.
		// Session is still alive; user can continue the conversation.

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
		// Detect file changes from file-editing tool calls.
		if hook.ToolName == "Edit" || hook.ToolName == "Write" || hook.ToolName == "MultiEdit" || hook.ToolName == "NotebookEdit" {
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
		FilePath     string `json:"file_path"`
		Path         string `json:"path"`
		NotebookPath string `json:"notebook_path"`
	}
	if json.Unmarshal(toolInput, &input) == nil {
		switch {
		case input.FilePath != "":
			return input.FilePath
		case input.Path != "":
			return input.Path
		case input.NotebookPath != "":
			return input.NotebookPath
		}
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
