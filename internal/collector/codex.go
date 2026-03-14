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

// CodexWatcher watches Codex log directory for new session logs and parses them.
type CodexWatcher struct {
	logDir string
	emitFn func(event.Event)
	done   chan struct{}
	seen   map[string]int64 // file -> last read offset
}

func NewCodexWatcher(emitFn func(event.Event)) *CodexWatcher {
	home, _ := os.UserHomeDir()
	logDir := filepath.Join(home, ".codex")
	return &CodexWatcher{
		logDir: logDir,
		emitFn: emitFn,
		done:   make(chan struct{}),
		seen:   make(map[string]int64),
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
	entries, err := os.ReadDir(w.logDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		w.processFile(filepath.Join(w.logDir, entry.Name()))
	}
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
		var raw map[string]any
		if err := dec.Decode(&raw); err != nil {
			break
		}
		if ev, ok := parseCodexLogEntry(raw, filepath.Base(path)); ok {
			w.emitFn(ev)
		}
	}

	newOffset, _ := f.Seek(0, 1)
	w.seen[path] = newOffset
}

func parseCodexLogEntry(raw map[string]any, filename string) (event.Event, bool) {
	sessionID := strings.TrimSuffix(filename, ".jsonl")

	ev := event.Event{
		SessionID: sessionID,
		Platform:  event.PlatformCodex,
		Timestamp: time.Now(),
	}

	msgType, _ := raw["type"].(string)
	switch msgType {
	case "function_call":
		name, _ := raw["name"].(string)
		args, _ := raw["arguments"].(string)
		ev.ID = fmt.Sprintf("codex-%s-%d", name, time.Now().UnixNano())
		ev.Type = event.EventToolCallStart
		ev.Data = event.EventData{
			ToolName:   name,
			ToolParams: truncate(args, 500),
		}
		return ev, true

	case "function_call_output":
		output, _ := raw["output"].(string)
		ev.ID = fmt.Sprintf("codex-result-%d", time.Now().UnixNano())
		ev.Type = event.EventToolCallEnd
		ev.Data = event.EventData{
			ToolResult: truncate(output, 500),
			ToolStatus: event.StatusSuccess,
		}
		return ev, true

	case "usage":
		inputTokens, _ := raw["input_tokens"].(float64)
		outputTokens, _ := raw["output_tokens"].(float64)
		model, _ := raw["model"].(string)
		cost := estimateCodexCost(int(inputTokens), int(outputTokens), model)
		ev.ID = fmt.Sprintf("codex-usage-%d", time.Now().UnixNano())
		ev.Type = event.EventTokenUsage
		ev.Data = event.EventData{
			InputTokens:  int(inputTokens),
			OutputTokens: int(outputTokens),
			Model:        model,
			CostUSD:      cost,
		}
		return ev, true
	}

	return ev, false
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
