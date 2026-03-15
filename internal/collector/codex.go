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
	// Codex stores session logs under ~/.codex/sessions/YYYY/MM/DD/*.jsonl
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
	// Walk the sessions directory recursively to find .jsonl files
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

	dec := json.NewDecoder(f)
	for dec.More() {
		var raw codexLogEntry
		if err := dec.Decode(&raw); err != nil {
			break
		}
		for _, ev := range parseCodexEntry(raw) {
			w.emitFn(ev)
		}
	}

	newOffset, _ := f.Seek(0, 1)
	w.seen[path] = newOffset
}

// codexLogEntry matches the actual Codex JSONL format:
// {"timestamp":"...","type":"response_item|session_meta","payload":{...}}
type codexLogEntry struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexSessionMeta struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	CWD       string `json:"cwd"`
	Model     string `json:"model_provider"`
}

type codexResponseItem struct {
	Type    string              `json:"type"`
	Role    string              `json:"role,omitempty"`
	Name    string              `json:"name,omitempty"`
	CallID  string              `json:"call_id,omitempty"`
	Content []codexContentBlock `json:"content,omitempty"`
	Output  string              `json:"output,omitempty"`
	Status  string              `json:"status,omitempty"`
}

type codexContentBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	CallID    string `json:"call_id,omitempty"`
}

func parseCodexEntry(entry codexLogEntry) []event.Event {
	var events []event.Event

	switch entry.Type {
	case "session_meta":
		var meta codexSessionMeta
		if json.Unmarshal(entry.Payload, &meta) != nil {
			return nil
		}
		ev := event.Event{
			ID:        fmt.Sprintf("codex-session-%s", meta.ID),
			Type:      event.EventSessionStart,
			SessionID: meta.ID,
			Platform:  event.PlatformCodex,
			Timestamp: parseTimestamp(entry.Timestamp),
		}
		events = append(events, ev)

	case "response_item":
		var item codexResponseItem
		if json.Unmarshal(entry.Payload, &item) != nil {
			return nil
		}

		ts := parseTimestamp(entry.Timestamp)

		// Extract function calls from content blocks
		for _, block := range item.Content {
			switch block.Type {
			case "function_call":
				ev := event.Event{
					ID:        block.CallID,
					Type:      event.EventToolCallStart,
					SessionID: "", // will be inferred from file context
					Platform:  event.PlatformCodex,
					Timestamp: ts,
					Data: event.EventData{
						ToolName:   block.Name,
						ToolParams: truncate(block.Arguments, 500),
					},
				}
				events = append(events, ev)
			}
		}

		// Function call output
		if item.Type == "function_call_output" {
			ev := event.Event{
				ID:        item.CallID,
				Type:      event.EventToolCallEnd,
				Platform:  event.PlatformCodex,
				Timestamp: ts,
				Data: event.EventData{
					ToolResult: truncate(item.Output, 500),
					ToolStatus: event.StatusSuccess,
				},
			}
			if item.Status == "error" || item.Status == "failed" {
				ev.Data.ToolStatus = event.StatusFail
			}
			events = append(events, ev)
		}
	}

	return events
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
