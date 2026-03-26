package collector

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tt-a1i/agmon/internal/event"
)

// CodexWatcher watches Codex session log directory for new logs and parses them.
type CodexWatcher struct {
	baseDir            string
	emitFn             func(event.Event)
	done               chan struct{}
	stopOnce           sync.Once
	seen               map[string]int64 // file path -> last read byte offset
	pathsMu            sync.RWMutex
	sessionPaths       map[string]string // sessionID -> file path; protected by pathsMu
	lastTokenUsage     map[string]string // sessionID -> "input:output" dedup key
	sessionModels      map[string]string
	sessionCWDs        map[string]string
	pendingFileChanges map[string][]codexFileChange
	initialDiscovery   bool
}

func NewCodexWatcher(emitFn func(event.Event)) *CodexWatcher {
	home, _ := os.UserHomeDir()
	baseDir := filepath.Join(home, ".codex", "sessions")
	return &CodexWatcher{
		baseDir:            baseDir,
		emitFn:             emitFn,
		done:               make(chan struct{}),
		seen:               make(map[string]int64),
		lastTokenUsage:     make(map[string]string),
		sessionModels:      make(map[string]string),
		sessionCWDs:        make(map[string]string),
		pendingFileChanges: make(map[string][]codexFileChange),
	}
}

// codexPathResolver is set by RegisterCodexWatcher so the Messages view can look
// up Codex log paths from the watcher's in-memory index instead of walking the FS.
var codexPathResolver func(sessionID string) string

// RegisterCodexWatcher wires the watcher into the package-level path resolver.
// Call this once after creating the watcher, before the first Messages lookup.
func RegisterCodexWatcher(w *CodexWatcher) {
	codexPathResolver = func(sid string) string {
		w.pathsMu.RLock()
		p := w.sessionPaths[sid]
		w.pathsMu.RUnlock()
		return p
	}
}

func (w *CodexWatcher) Start() {
	go w.pollLoop()
}

func (w *CodexWatcher) Stop() {
	w.stopOnce.Do(func() { close(w.done) })
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
	w.ensureState()

	if !w.initialDiscovery {
		if !w.fullDiscover() {
			return
		}
		w.initialDiscovery = true
		return
	}

	w.scanKnownFiles()
	w.scanRecentDirs()
}

func (w *CodexWatcher) fullDiscover() bool {
	if _, err := os.Stat(w.baseDir); err != nil {
		log.Printf("codex watcher walk %s: %v", w.baseDir, err)
		return false
	}

	if err := filepath.Walk(w.baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Printf("codex watcher walk %s: %v", path, err)
			return nil
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".jsonl") {
			return nil
		}
		// Skip files where we've already read all bytes (avoids open+stat per file).
		if offset, seen := w.seen[path]; seen && info.Size() <= offset {
			return nil
		}
		w.processFile(path, info.Size())
		return nil
	}); err != nil {
		log.Printf("codex watcher walk root %s: %v", w.baseDir, err)
		return false
	}
	return true
}

func (w *CodexWatcher) scanKnownFiles() {
	if len(w.seen) == 0 {
		return
	}

	paths := make([]string, 0, len(w.seen))
	for path := range w.seen {
		paths = append(paths, path)
	}

	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				log.Printf("codex watcher stat %s: %v", path, err)
			}
			delete(w.seen, path)
			w.removeSessionPath(path)
			continue
		}
		if info.IsDir() {
			delete(w.seen, path)
			w.removeSessionPath(path)
			continue
		}
		if info.Size() <= w.seen[path] {
			continue
		}
		w.processFile(path, info.Size())
	}
}

func (w *CodexWatcher) scanRecentDirs() {
	for _, dir := range recentCodexSessionDirs(w.baseDir, time.Now()) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			log.Printf("codex watcher read recent dir %s: %v", dir, err)
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				log.Printf("codex watcher stat %s: %v", filepath.Join(dir, entry.Name()), err)
				continue
			}
			path := filepath.Join(dir, entry.Name())
			if offset, seen := w.seen[path]; seen && info.Size() <= offset {
				continue
			}
			w.processFile(path, info.Size())
		}
	}
}

func recentCodexSessionDirs(baseDir string, now time.Time) []string {
	dirs := make([]string, 0, 3)
	seen := make(map[string]struct{}, 3)
	for i := 0; i < 3; i++ {
		day := now.AddDate(0, 0, -i).UTC()
		dir := filepath.Join(baseDir, day.Format("2006"), day.Format("01"), day.Format("02"))
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		dirs = append(dirs, dir)
	}
	return dirs
}

func (w *CodexWatcher) removeSessionPath(path string) {
	w.pathsMu.Lock()
	defer w.pathsMu.Unlock()
	for sessionID, indexedPath := range w.sessionPaths {
		if indexedPath == path {
			delete(w.sessionPaths, sessionID)
		}
	}
}

func (w *CodexWatcher) processFile(path string, size int64) {
	w.ensureState()

	offset := w.seen[path]
	if offset > 0 && size <= offset {
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
	w.pathsMu.Lock()
	w.sessionPaths[sessionID] = path // index for O(1) lookup in Messages view
	w.pathsMu.Unlock()

	reader := bufio.NewReaderSize(f, 1024*1024)
	committedOffset := offset

	for {
		lineBytes, err := reader.ReadBytes('\n')

		if len(lineBytes) > 0 {
			line := bytes.TrimRight(lineBytes, "\r\n")

			if len(line) > 0 {
				var raw codexLogEntry
				if json.Unmarshal(line, &raw) == nil {
					if raw.Type == "turn_context" {
						w.recordTurnContext(sessionID, raw.Payload)
					}

					payload, ok := decodeCodexResponsePayload(raw)
					if ok && payload.Type == "function_call" {
						w.pendingFileChanges[payload.CallID] = extractCodexFileChanges(payload.Name, payload.Arguments)
					}

					for _, ev := range parseCodexEntryWithContext(raw, sessionID, w.sessionModels[sessionID], w.sessionCWDs[sessionID]) {
						// Dedup repeated token_count events with same last_token_usage
						if ev.Type == event.EventTokenUsage {
							key := fmt.Sprintf("%d:%d", ev.Data.InputTokens, ev.Data.OutputTokens)
							if w.lastTokenUsage[sessionID] == key {
								continue
							}
							w.lastTokenUsage[sessionID] = key
						}
						w.emitFn(ev)
					}

					if ok && payload.Type == "function_call_output" {
						if payload.Status != "error" && payload.Status != "failed" {
							for _, change := range w.pendingFileChanges[payload.CallID] {
								w.emitFn(event.Event{
									ID:        fmt.Sprintf("%s:%s", payload.CallID, change.Path),
									Type:      event.EventFileChange,
									SessionID: sessionID,
									Platform:  event.PlatformCodex,
									Timestamp: parseTimestamp(raw.Timestamp),
									Data: event.EventData{
										FilePath:   change.Path,
										ChangeType: change.ChangeType,
									},
								})
							}
						}
						delete(w.pendingFileChanges, payload.CallID)
					}
				}
			}

			// Only advance offset for complete lines (err == nil means \n was found).
			if err == nil {
				committedOffset += int64(len(lineBytes))
			}
		}

		if err != nil {
			break
		}
	}

	w.seen[path] = committedOffset
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
	ID  string `json:"id"`
	CWD string `json:"cwd"`
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
	Type string          `json:"type"`
	Info *codexTokenInfo `json:"info,omitempty"`
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
	return parseCodexEntryWithContext(entry, sessionID, "", "")
}

func parseCodexEntryWithContext(entry codexLogEntry, sessionID, model, cwd string) []event.Event {
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
			Data: event.EventData{
				CWD: meta.CWD,
			},
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
			cost := estimateCodexCost(usage.InputTokens, usage.OutputTokens, model)
			return []event.Event{{
				ID:        fmt.Sprintf("codex-tokens-%d", ts.UnixNano()),
				Type:      event.EventTokenUsage,
				SessionID: sessionID,
				Platform:  event.PlatformCodex,
				Timestamp: ts,
				Data: event.EventData{
					InputTokens:  usage.InputTokens,
					OutputTokens: usage.OutputTokens,
					Model:        model,
					CWD:          cwd,
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

// CodexPricing returns per-million-token pricing for a Codex model.
func CodexPricing(model string) (inputPricePerM, outputPricePerM float64) {
	pricing := codexPricing(model)
	return pricing.inputPerMillion, pricing.outputPerMillion
}

func estimateCodexCost(inputTokens, outputTokens int, model string) float64 {
	inP, outP := CodexPricing(model)
	return (float64(inputTokens)*inP + float64(outputTokens)*outP) / 1_000_000
}

type codexTurnContext struct {
	CWD   string `json:"cwd"`
	Model string `json:"model"`
}

type codexFileChange struct {
	Path       string
	ChangeType event.FileChangeType
}

func (w *CodexWatcher) ensureState() {
	if w.seen == nil {
		w.seen = make(map[string]int64)
	}
	if w.sessionPaths == nil {
		w.pathsMu.Lock()
		w.sessionPaths = make(map[string]string)
		w.pathsMu.Unlock()
	}
	if w.lastTokenUsage == nil {
		w.lastTokenUsage = make(map[string]string)
	}
	if w.sessionModels == nil {
		w.sessionModels = make(map[string]string)
	}
	if w.sessionCWDs == nil {
		w.sessionCWDs = make(map[string]string)
	}
	if w.pendingFileChanges == nil {
		w.pendingFileChanges = make(map[string][]codexFileChange)
	}
}

func (w *CodexWatcher) recordTurnContext(sessionID string, payload json.RawMessage) {
	var ctx codexTurnContext
	if json.Unmarshal(payload, &ctx) != nil {
		return
	}
	if ctx.Model != "" {
		w.sessionModels[sessionID] = ctx.Model
	}
	if ctx.CWD != "" {
		w.sessionCWDs[sessionID] = ctx.CWD
	}
}

func decodeCodexResponsePayload(entry codexLogEntry) (codexResponsePayload, bool) {
	if entry.Type != "response_item" {
		return codexResponsePayload{}, false
	}
	var payload codexResponsePayload
	if json.Unmarshal(entry.Payload, &payload) != nil {
		return codexResponsePayload{}, false
	}
	return payload, true
}

func extractCodexFileChanges(toolName, arguments string) []codexFileChange {
	switch toolName {
	case "apply_patch":
		var input struct {
			Input string `json:"input"`
			Patch string `json:"patch"`
		}
		if json.Unmarshal([]byte(arguments), &input) != nil {
			return nil
		}
		patch := input.Input
		if patch == "" {
			patch = input.Patch
		}
		return extractPatchFileChanges(patch)
	default:
		return nil
	}
}

func extractPatchFileChanges(patch string) []codexFileChange {
	if patch == "" {
		return nil
	}

	var changes []codexFileChange
	seen := make(map[string]event.FileChangeType)
	scanner := bufio.NewScanner(strings.NewReader(patch))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "*** Update File: "):
			recordPatchFileChange(seen, &changes, strings.TrimSpace(strings.TrimPrefix(line, "*** Update File: ")), event.FileEdit)
		case strings.HasPrefix(line, "*** Add File: "):
			recordPatchFileChange(seen, &changes, strings.TrimSpace(strings.TrimPrefix(line, "*** Add File: ")), event.FileCreate)
		case strings.HasPrefix(line, "*** Delete File: "):
			recordPatchFileChange(seen, &changes, strings.TrimSpace(strings.TrimPrefix(line, "*** Delete File: ")), event.FileDelete)
		}
	}
	return changes
}

func recordPatchFileChange(seen map[string]event.FileChangeType, changes *[]codexFileChange, path string, changeType event.FileChangeType) {
	if path == "" {
		return
	}
	if existing, ok := seen[path]; ok && existing == changeType {
		return
	}
	seen[path] = changeType
	*changes = append(*changes, codexFileChange{Path: path, ChangeType: changeType})
}
