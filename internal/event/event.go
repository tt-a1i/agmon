package event

import "time"

type Platform string

const (
	PlatformClaude Platform = "claude"
	PlatformCodex  Platform = "codex"
)

type EventType string

const (
	EventToolCallStart EventType = "tool_call_start"
	EventToolCallEnd   EventType = "tool_call_end"
	EventAgentStart    EventType = "agent_start"
	EventAgentEnd      EventType = "agent_end"
	EventTokenUsage    EventType = "token_usage"
	EventFileChange    EventType = "file_change"
	EventSessionStart  EventType = "session_start"
	EventSessionEnd    EventType = "session_end"
)

type ToolCallStatus string

const (
	StatusSuccess ToolCallStatus = "success"
	StatusFail    ToolCallStatus = "fail"
	StatusRetry   ToolCallStatus = "retry"
)

type FileChangeType string

const (
	FileCreate FileChangeType = "create"
	FileEdit   FileChangeType = "edit"
	FileDelete FileChangeType = "delete"
)

// Event is the unified event model for all platforms.
type Event struct {
	ID        string    `json:"id"`
	Type      EventType `json:"type"`
	SessionID string    `json:"session_id"`
	AgentID   string    `json:"agent_id,omitempty"`
	Platform  Platform  `json:"platform"`
	Timestamp time.Time `json:"timestamp"`
	Data      EventData `json:"data"`
}

type EventData struct {
	// Tool call fields
	ToolName   string         `json:"tool_name,omitempty"`
	ToolParams string         `json:"tool_params,omitempty"`
	ToolResult string         `json:"tool_result,omitempty"`
	ToolStatus ToolCallStatus `json:"tool_status,omitempty"`
	DurationMs int64          `json:"duration_ms,omitempty"`

	// Agent fields
	ParentAgentID string `json:"parent_agent_id,omitempty"`
	AgentRole     string `json:"agent_role,omitempty"`

	// Token fields
	InputTokens         int    `json:"input_tokens,omitempty"`
	OutputTokens        int    `json:"output_tokens,omitempty"`
	CacheCreationTokens int    `json:"cache_creation_tokens,omitempty"`
	CacheReadTokens     int    `json:"cache_read_tokens,omitempty"`
	Model               string `json:"model,omitempty"`

	// Cost fields
	CostUSD float64 `json:"cost_usd,omitempty"`

	// File change fields
	FilePath   string         `json:"file_path,omitempty"`
	ChangeType FileChangeType `json:"change_type,omitempty"`

	// Session metadata fields
	CWD       string `json:"cwd,omitempty"`
	GitBranch string `json:"git_branch,omitempty"`
}
