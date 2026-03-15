package collector

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tt-a1i/agmon/internal/event"
)

// CodexWatcher watches Codex session log directory for new logs and parses them.
type CodexWatcher struct {
	baseDir string
	emitFn  func(event.Event)
	done    chan struct{}
	seen    map[string]int64 // file -> last read offset
}

func NewCodexWatcher(emitFn func(event.Event)) *CodexWatcher {
	home, _ := os.UserHomeDir()
	baseDir := filepath.Join(home, ".codex", "sessions")
	return &CodexWatcher{
		baseDir: baseDir,
		emitFn:  emitFn,
		done:    make(chan struct{}),
		seen:    make(map[string]int64),
	}
}

func (w *CodexWatcher) Start() {
	go w.pollLoop()
}

func (w *CodexWatcher) Stop() {
	close(w.done)
}

func (w *CodexWatcher) pollLoop() {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-w.done:
			return
		case <-ticker.C:
			w.scanLogs()
		}
	}
}

func (w *CodexWatcher) scanLogs() {
	filepath.Walk(w.baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".jsonl") {
			return nil
		}
		w.processFile(path)
		return nil
	})
}

func (w *CodexWatcher) processFile(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}

	offset, exists := w.seen[path]
	if exists && info.Size() <= offset {
		return
	}

	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	if offset > 0 {
		f.Seek(offset, 0)
	}

	// Extract session ID from filename: rollout-...-<uuid>.jsonl
	sessionID := extractSessionID(filepath.Base(path))

	dec := json.NewDecoder(f)
	for dec.More() {
		var raw codexLogEntry
		if err := dec.Decode(&raw); err != nil {
			break
		}
		for _, ev := range parseCodexEntry(raw, sessionID) {
			w.emitFn(ev)
		}
	}

	newOffset, _ := f.Seek(0, 1)
	w.seen[path] = newOffset
}

// extractSessionID pulls the UUID from a filename like:
// rollout-2026-01-14T20-03-54-d4430cef-110d-42e0-924a-bfceeba0c4e1.jsonl
func extractSessionID(filename string) string {
	name := strings.TrimSuffix(filename, ".jsonl")
	// The UUID is the last 36 chars (8-4-4-4-12)
	if len(name) >= 36 {
		candidate := name[len(name)-36:]
		// Quick check it looks like a UUID
		if len(candidate) == 36 && candidate[8] == '-' {
			return candidate
		}
	}
	return name
}

// --- Codex log entry types matching actual JSONL format ---

// Top-level entry: {"timestamp":"...","type":"session_meta|response_item|event_msg","payload":{...}}
type codexLogEntry struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

// session_meta payload
type codexSessionMeta struct {
	ID string `json:"id"`
}

// response_item payload — covers function_call, function_call_output, and message types.
// For function_call: name, arguments, call_id are at payload root.
// For function_call_output: call_id, output are at payload root.
type codexResponsePayload struct {
	Type      string `json:"type"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Output    string `json:"output,omitempty"`
	Status    string `json:"status,omitempty"`
}

// event_msg payload for token_count
type codexEventMsg struct {
	Type string             `json:"type"`
	Info *codexTokenInfo    `json:"info,omitempty"`
}

type codexTokenInfo struct {
	LastTokenUsage codexTokenUsage `json:"last_token_usage"`
}

type codexTokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

func parseCodexEntry(entry codexLogEntry, sessionID string) []event.Event {
	ts := parseTimestamp(entry.Timestamp)

	switch entry.Type {
	case "session_meta":
		var meta codexSessionMeta
		if json.Unmarshal(entry.Payload, &meta) != nil {
			return nil
		}
		sid := meta.ID
		if sid == "" {
			sid = sessionID
		}
		return []event.Event{{
			ID:        fmt.Sprintf("codex-session-%s", sid),
			Type:      event.EventSessionStart,
			SessionID: sid,
			Platform:  event.PlatformCodex,
			Timestamp: ts,
		}}

	case "response_item":
		var payload codexResponsePayload
		if json.Unmarshal(entry.Payload, &payload) != nil {
			return nil
		}

		switch payload.Type {
		case "function_call":
			// name, arguments, call_id are at payload root
			return []event.Event{{
				ID:        payload.CallID,
				Type:      event.EventToolCallStart,
				SessionID: sessionID,
				Platform:  event.PlatformCodex,
				Timestamp: ts,
				Data: event.EventData{
					ToolName:   payload.Name,
					ToolParams: truncate(payload.Arguments, 500),
				},
			}}

		case "function_call_output":
			status := event.StatusSuccess
			if payload.Status == "error" || payload.Status == "failed" {
				status = event.StatusFail
			}
			return []event.Event{{
				ID:        payload.CallID,
				Type:      event.EventToolCallEnd,
				SessionID: sessionID,
				Platform:  event.PlatformCodex,
				Timestamp: ts,
				Data: event.EventData{
					ToolResult: truncate(payload.Output, 500),
					ToolStatus: status,
				},
			}}
		}

	case "event_msg":
		var msg codexEventMsg
		if json.Unmarshal(entry.Payload, &msg) != nil {
			return nil
		}

		if msg.Type == "token_count" && msg.Info != nil {
			usage := msg.Info.LastTokenUsage
			if usage.TotalTokens == 0 {
				return nil
			}
			cost := estimateCodexCost(usage.InputTokens, usage.OutputTokens, "")
			return []event.Event{{
				ID:        fmt.Sprintf("codex-tokens-%d", ts.UnixNano()),
				Type:      event.EventTokenUsage,
				SessionID: sessionID,
				Platform:  event.PlatformCodex,
				Timestamp: ts,
				Data: event.EventData{
					InputTokens:  usage.InputTokens,
					OutputTokens: usage.OutputTokens,
					CostUSD:      cost,
				},
			}}
		}
	}

	return nil
}

func parseTimestamp(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339, s)
		if err != nil {
			return time.Now()
		}
	}
	return t
}

func estimateCodexCost(inputTokens, outputTokens int, model string) float64 {
	inputPricePerM := 2.0
	outputPricePerM := 8.0

	if strings.Contains(model, "gpt-4") {
		inputPricePerM = 2.5
		outputPricePerM = 10.0
	}

	return (float64(inputTokens)*inputPricePerM + float64(outputTokens)*outputPricePerM) / 1_000_000
}
