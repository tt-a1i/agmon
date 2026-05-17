package collector

import (
	"encoding/json"
	"strings"
	"testing"
)

// FuzzParseClaudeJSONL fuzzes single-line Claude JSONL parsing.
// The parser must not panic on any input.
func FuzzParseClaudeJSONL(f *testing.F) {
	// Valid assistant entry with token usage
	f.Add(`{"type":"assistant","sessionId":"s1","uuid":"u1","gitBranch":"main","cwd":"/code","timestamp":"2026-01-15T14:30:00Z","message":{"model":"claude-sonnet-4-6","usage":{"input_tokens":100,"output_tokens":50,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`)
	// Valid assistant with cache tokens
	f.Add(`{"type":"assistant","sessionId":"s2","uuid":"u2","timestamp":"2026-01-15T14:30:00.123456789Z","message":{"model":"claude-opus-4-7","usage":{"input_tokens":1000,"output_tokens":200,"cache_creation_input_tokens":5000,"cache_read_input_tokens":8000}}}`)
	// Non-assistant type (should be skipped)
	f.Add(`{"type":"user","sessionId":"s3","uuid":"u3","timestamp":"2026-01-15T14:30:00Z","message":{"model":"","usage":null}}`)
	// Missing usage field
	f.Add(`{"type":"assistant","sessionId":"s4","uuid":"u4","timestamp":"2026-01-15T14:30:00Z","message":{"model":"claude-haiku-4-5"}}`)
	// Invalid timestamp
	f.Add(`{"type":"assistant","sessionId":"s5","uuid":"u5","timestamp":"not-a-time","message":{"model":"claude-sonnet-4-6","usage":{"input_tokens":1,"output_tokens":1}}}`)
	// Empty JSON object
	f.Add(`{}`)
	// Only gitBranch (no message)
	f.Add(`{"type":"summary","gitBranch":"feat/xyz","sessionId":"s6","uuid":"u6","timestamp":"2026-01-15T14:30:00Z"}`)
	// Large token counts
	f.Add(`{"type":"assistant","sessionId":"s7","uuid":"u7","timestamp":"2026-01-15T14:30:00Z","message":{"model":"claude-opus-4-7","usage":{"input_tokens":2000000,"output_tokens":500000,"cache_creation_input_tokens":9999999,"cache_read_input_tokens":9999999}}}`)

	f.Fuzz(func(t *testing.T, data string) {
		if len(data) > 64*1024 {
			t.Skip()
		}
		var entry claudeLogEntry
		if err := json.Unmarshal([]byte(data), &entry); err != nil {
			return // invalid JSON — parser never sees it
		}
		// Must not panic
		parseClaudeLogTokenEvent(entry, "fuzz-session", "main")
	})
}

// FuzzParseCodexJSONL fuzzes single-line Codex JSONL parsing.
// All valid entry types (session_meta, response_item, event_msg, turn_context) are seeded.
func FuzzParseCodexJSONL(f *testing.F) {
	// session_meta
	f.Add(`{"timestamp":"2026-01-15T14:30:00Z","type":"session_meta","payload":{"id":"codex-sess-1","cwd":"/code/api"}}`)
	// response_item function_call
	f.Add(`{"timestamp":"2026-01-15T14:30:00Z","type":"response_item","payload":{"type":"function_call","name":"Read","arguments":"{\"file_path\":\"/etc/hosts\"}","call_id":"call-abc123"}}`)
	// response_item function_call_output success
	f.Add(`{"timestamp":"2026-01-15T14:30:01Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call-abc123","output":"file contents here","status":"success"}}`)
	// response_item function_call_output error
	f.Add(`{"timestamp":"2026-01-15T14:30:02Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call-abc124","output":"permission denied","status":"error"}}`)
	// event_msg token_count
	f.Add(`{"timestamp":"2026-01-15T14:30:03Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":500,"output_tokens":120,"total_tokens":620,"cached_input_tokens":0}}}}`)
	// event_msg token_count with total_token_usage
	f.Add(`{"timestamp":"2026-01-15T14:30:04Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":100,"output_tokens":50,"total_tokens":150,"cached_input_tokens":10},"total_token_usage":{"input_tokens":600,"output_tokens":170,"total_tokens":770,"cached_input_tokens":10}}}}`)
	// turn_context
	f.Add(`{"timestamp":"2026-01-15T14:30:05Z","type":"turn_context","payload":{"model":"gpt-4o","cwd":"/code"}}`)
	// custom_tool_call
	f.Add(`{"timestamp":"2026-01-15T14:30:06Z","type":"response_item","payload":{"type":"custom_tool_call","name":"shell","input":"ls -la","call_id":"call-xyz","status":"success"}}`)
	// invalid timestamp
	f.Add(`{"timestamp":"bad","type":"session_meta","payload":{"id":"x","cwd":"/tmp"}}`)
	// empty payload
	f.Add(`{"timestamp":"2026-01-15T14:30:00Z","type":"response_item","payload":{}}`)

	f.Fuzz(func(t *testing.T, data string) {
		if len(data) > 64*1024 {
			t.Skip()
		}
		var entry codexLogEntry
		if err := json.Unmarshal([]byte(data), &entry); err != nil {
			return
		}
		// Must not panic
		parseCodexEntryWithContext(entry, "fuzz-codex-session", "gpt-4o", "/tmp")
	})
}

// FuzzParseClaudeHookEvent fuzzes Claude Code hook stdin JSON parsing.
// Covers all hook types that Claude Code can emit.
func FuzzParseClaudeHookEvent(f *testing.F) {
	// SessionStart
	f.Add(`{"hook_event_name":"SessionStart","session_id":"sess-001","cwd":"/code/tokenmeter","gitBranch":"main"}`)
	// SessionEnd
	f.Add(`{"hook_event_name":"SessionEnd","session_id":"sess-001"}`)
	// PreToolUse
	f.Add(`{"hook_event_name":"PreToolUse","session_id":"sess-002","agent_id":"agent-1","tool_name":"Read","tool_input":{"file_path":"/etc/passwd"},"tool_use_id":"tu-abc"}`)
	// PostToolUse
	f.Add(`{"hook_event_name":"PostToolUse","session_id":"sess-002","agent_id":"agent-1","tool_name":"Read","tool_result":"file content","tool_use_id":"tu-abc"}`)
	// PostToolUseFailure
	f.Add(`{"hook_event_name":"PostToolUseFailure","session_id":"sess-002","agent_id":"agent-1","tool_name":"Bash","tool_result":"command not found","tool_use_id":"tu-xyz"}`)
	// Stop with transcript path
	f.Add(`{"hook_event_name":"Stop","session_id":"sess-003","agent_id":"agent-1","reason":"completed","agent_transcript_path":"/home/user/.claude/projects/-code/sess-003.jsonl"}`)
	// SubagentStart
	f.Add(`{"hook_event_name":"SubagentStart","session_id":"sess-004","agent_id":"sub-agent-1","agent_type":"subagent"}`)
	// SubagentStop
	f.Add(`{"hook_event_name":"SubagentStop","session_id":"sess-004","agent_id":"sub-agent-1"}`)
	// Unknown hook type (should not panic)
	f.Add(`{"hook_event_name":"UnknownHook","session_id":"sess-005"}`)
	// Minimal valid JSON
	f.Add(`{"session_id":"s"}`)
	// tool_input as raw JSON array
	f.Add(`{"hook_event_name":"PreToolUse","session_id":"s","tool_name":"Bash","tool_input":["cmd","arg1"],"tool_use_id":"tu-1"}`)

	f.Fuzz(func(t *testing.T, data string) {
		if len(data) > 64*1024 {
			t.Skip()
		}
		hook, err := ParseClaudeHook(strings.NewReader(data))
		if err != nil {
			return // invalid JSON — conversion never attempted
		}
		// Must not panic
		ClaudeHookToEvents(hook)
	})
}

// FuzzEstimateClaudeCost fuzzes the Claude cost estimation function with
// arbitrary token counts and model strings.
func FuzzEstimateClaudeCost(f *testing.F) {
	f.Add(100, 50, 0, 0, "claude-sonnet-4-6")
	f.Add(1000, 200, 5000, 8000, "claude-opus-4-7")
	f.Add(0, 0, 0, 0, "claude-haiku-4-5")
	f.Add(2000000, 500000, 0, 0, "claude-sonnet-4-6-20251001")
	f.Add(1, 1, 1, 1, "unknown-model")
	f.Add(0, 0, 0, 0, "")

	f.Fuzz(func(t *testing.T, inputTokens, outputTokens, cacheCreate, cacheRead int, model string) {
		if len(model) > 256 {
			t.Skip()
		}
		// Negative token counts are not a valid domain input; skip them.
		if inputTokens < 0 || outputTokens < 0 || cacheCreate < 0 || cacheRead < 0 {
			t.Skip()
		}
		// Must not panic; result must be non-negative for valid inputs.
		cost := EstimateClaudeCost(inputTokens, outputTokens, cacheCreate, cacheRead, model)
		if cost < 0 {
			t.Errorf("EstimateClaudeCost returned negative cost %f", cost)
		}
	})
}

// FuzzParseTimestamp fuzzes the timestamp parser with various formats and garbage input.
func FuzzParseTimestamp(f *testing.F) {
	f.Add("2026-01-15T14:30:00Z")
	f.Add("2026-01-15T14:30:00.123456789Z")
	f.Add("2026-01-15T14:30:00+08:00")
	f.Add("2006-01-02T15:04:05Z07:00")
	f.Add("")
	f.Add("not-a-timestamp")
	f.Add("2026-13-99T99:99:99Z")
	f.Add("0001-01-01T00:00:00Z")
	f.Add("9999-12-31T23:59:59.999999999Z")

	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 64 {
			t.Skip()
		}
		// Must not panic
		parseTimestamp(s)
	})
}
