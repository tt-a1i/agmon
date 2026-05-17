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
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

// CodexWatcher watches Codex session log directories for new logs and parses them.
type CodexWatcher struct {
	baseDirs           []string // directories to scan (sessions + archived_sessions)
	emitFn             func(event.Event)
	done               chan struct{}
	stopOnce           sync.Once
	loopWG             sync.WaitGroup
	seen               map[string]int64 // file path -> last read byte offset
	pathsMu            sync.RWMutex
	sessionPaths       map[string]string // sessionID -> file path; protected by pathsMu
	sessionModels      map[string]string
	sessionCWDs        map[string]string
	pendingFileChanges map[string]codexPendingChange
	pendingStarts      map[string]event.Event // deferred SessionStart events for empty sessions
	initialDiscovery   bool
	tickInterval       time.Duration
	pendingTTL         time.Duration // GC pending entries older than this (default 2h)
	gcInterval         int           // run gcPending every Nth scanLogs call
	scanCount          int           // monotonic counter for GC throttling
	scanFn             func()
}

// codexPendingChange tracks file changes parsed from a function_call until
// the matching function_call_output is observed. insertedAt drives GC so an
// orphaned entry (codex died mid-tool-call) doesn't leak forever.
type codexPendingChange struct {
	changes    []codexFileChange
	insertedAt time.Time
}

func NewCodexWatcher(emitFn func(event.Event)) *CodexWatcher {
	home, _ := os.UserHomeDir()
	codexDir := filepath.Join(home, ".codex")
	return &CodexWatcher{
		baseDirs: []string{
			filepath.Join(codexDir, "sessions"),
			filepath.Join(codexDir, "archived_sessions"),
		},
		emitFn:             emitFn,
		done:               make(chan struct{}),
		seen:               make(map[string]int64),
		sessionModels:      make(map[string]string),
		sessionCWDs:        make(map[string]string),
		pendingFileChanges: make(map[string]codexPendingChange),
		tickInterval:       3 * time.Second,
		// 2h aligned with MarkStaleSessionsEnded — a tool call still pending
		// after that is almost certainly orphaned (codex crashed mid-call).
		pendingTTL: 2 * time.Hour,
		// 3s ticks × 10 = 30s GC cadence; map iteration is cheap but no
		// reason to do it 20× per minute.
		gcInterval: 10,
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
	w.loopWG.Add(1)
	go func() {
		defer w.loopWG.Done()
		w.pollLoop()
	}()
}

func (w *CodexWatcher) Stop() {
	w.stopOnce.Do(func() { close(w.done) })
	w.loopWG.Wait()
}

func (w *CodexWatcher) stopped() bool {
	select {
	case <-w.done:
		return true
	default:
		return false
	}
}

func (w *CodexWatcher) pollLoop() {
	interval := w.tickInterval
	if interval <= 0 {
		interval = 3 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-w.done:
			return
		case <-ticker.C:
			if w.scanFn != nil {
				w.scanFn()
				continue
			}
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
	w.scanCount++
	if w.gcInterval > 0 && w.scanCount%w.gcInterval == 0 {
		w.gcPending()
	}
}

// gcPending drops orphaned pending entries whose insertedAt is older than
// pendingTTL. These accumulate when a codex tool call starts (function_call
// observed) but the matching function_call_output never arrives because the
// codex process died, the JSONL was truncated, or the tool ran past TTL.
func (w *CodexWatcher) gcPending() {
	if w.pendingTTL <= 0 {
		return
	}
	cutoff := time.Now().Add(-w.pendingTTL)
	dropped := 0
	for callID, p := range w.pendingFileChanges {
		if p.insertedAt.Before(cutoff) {
			delete(w.pendingFileChanges, callID)
			dropped++
		}
	}
	if dropped > 0 {
		log.Printf("codex watcher: gc dropped %d orphaned pending file-change entries (TTL %s)", dropped, w.pendingTTL)
	}
}

type codexFileJob struct {
	path string
	size int64
}

type codexFileResult struct {
	path         string
	offset       int64
	sessionID    string
	model        string
	cwd          string
	events       []event.Event
	pendingCalls map[string]codexPendingChange
	pendingStart *event.Event // deferred SessionStart when file has no substantive data
}

func (w *CodexWatcher) fullDiscover() bool {
	// Collect file jobs from all base directories.
	var jobs []codexFileJob
	for _, baseDir := range w.baseDirs {
		if w.stopped() {
			return true
		}
		if _, err := os.Stat(baseDir); err != nil {
			continue // directory may not exist (e.g. no archived sessions)
		}
		if err := filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
			if w.stopped() {
				return filepath.SkipAll
			}
			if err != nil {
				log.Printf("codex watcher walk %s: %v", path, err)
				return nil
			}
			if info.IsDir() || !strings.HasSuffix(info.Name(), ".jsonl") {
				return nil
			}
			if offset, seen := w.seen[path]; seen && info.Size() <= offset {
				return nil
			}
			jobs = append(jobs, codexFileJob{path: path, size: info.Size()})
			return nil
		}); err != nil {
			log.Printf("codex watcher walk root %s: %v", baseDir, err)
		}
	}

	if len(jobs) == 0 {
		return true
	}

	// Fan out to workers.
	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8
	}
	if workers > len(jobs) {
		workers = len(jobs)
	}

	jobCh := make(chan codexFileJob, len(jobs))
	for _, j := range jobs {
		jobCh <- j
	}
	close(jobCh)

	resultCh := make(chan codexFileResult, len(jobs))
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for job := range jobCh {
				if w.stopped() {
					return
				}
				sessionID := extractSessionID(filepath.Base(job.path))
				r := processCodexFileCollect(
					job.path, job.size, w.seen[job.path],
					w.sessionModels[sessionID], w.sessionCWDs[sessionID],
					w.stopped,
				)
				resultCh <- r
			}
		}()
	}
	wg.Wait()
	close(resultCh)

	// Merge results and emit events on the main goroutine.
	for r := range resultCh {
		w.seen[r.path] = r.offset
		if r.sessionID != "" {
			w.pathsMu.Lock()
			w.sessionPaths[r.sessionID] = r.path
			w.pathsMu.Unlock()
			if r.model != "" {
				w.sessionModels[r.sessionID] = r.model
			}
			if r.cwd != "" {
				w.sessionCWDs[r.sessionID] = r.cwd
			}
			if len(r.pendingCalls) > 0 {
				for callID, changes := range r.pendingCalls {
					w.pendingFileChanges[callID] = changes
				}
			}
		}
		if r.pendingStart != nil && len(r.events) == 0 {
			w.pendingStarts[r.sessionID] = *r.pendingStart
		}
		if len(r.events) > 0 && !w.stopped() {
			// Prepend deferred SessionStart if one was saved for this session.
			if pending, ok := w.pendingStarts[r.sessionID]; ok {
				w.emitFn(pending)
				delete(w.pendingStarts, r.sessionID)
			}
			for _, ev := range r.events {
				w.emitFn(ev)
			}
		}
	}
	return true
}

// processCodexFileCollect parses a Codex JSONL file without touching watcher state.
// Returns collected events and updated per-session metadata.
func processCodexFileCollect(path string, size, startOffset int64, prevModel, prevCWD string, cancelled func() bool) codexFileResult {
	result := codexFileResult{path: path, offset: startOffset}

	f, err := os.Open(path)
	if err != nil {
		return result
	}
	defer f.Close()

	if startOffset > 0 {
		f.Seek(startOffset, 0)
	}

	sessionID := extractSessionID(filepath.Base(path))
	result.sessionID = sessionID
	result.model = prevModel
	result.cwd = prevCWD

	reader := bufio.NewReaderSize(f, 1024*1024)
	committedOffset := startOffset
	pendingFileChanges := make(map[string]codexPendingChange)
	linesRead := 0

	for {
		if linesRead%100 == 0 && cancelled != nil && cancelled() {
			break
		}
		linesRead++

		lineBytes, err := reader.ReadBytes('\n')

		if len(lineBytes) > 0 {
			line := bytes.TrimRight(lineBytes, "\r\n")
			parsedLine := false

			if len(line) > 0 {
				var raw codexLogEntry
				if json.Unmarshal(line, &raw) == nil {
					parsedLine = true
					if raw.Type == "turn_context" {
						var ctx codexTurnContext
						if json.Unmarshal(raw.Payload, &ctx) == nil {
							if ctx.Model != "" {
								result.model = ctx.Model
							}
							if ctx.CWD != "" {
								result.cwd = ctx.CWD
							}
							annotateCodexTokenEvents(result.events, result.model, result.cwd)
						}
					}

					payload, ok := decodeCodexResponsePayload(raw)
					if ok && payload.Type == "function_call" {
						pendingFileChanges[payload.CallID] = codexPendingChange{
							changes:    extractCodexFileChanges(payload.Name, payload.Arguments),
							insertedAt: time.Now(),
						}
					}

					for _, ev := range parseCodexEntryWithContext(raw, sessionID, result.model, result.cwd) {
						result.events = append(result.events, ev)
					}

					ts, tsOk := parseTimestamp(raw.Timestamp)
					if ok && payload.Type == "function_call_output" {
						if tsOk && payload.Status != "error" && payload.Status != "failed" {
							for _, change := range pendingFileChanges[payload.CallID].changes {
								result.events = append(result.events, event.Event{
									ID:        fmt.Sprintf("%s:%s", payload.CallID, change.Path),
									Type:      event.EventFileChange,
									SessionID: sessionID,
									Platform:  event.PlatformCodex,
									Timestamp: ts,
									Data: event.EventData{
										FilePath:   change.Path,
										ChangeType: change.ChangeType,
									},
								})
							}
						}
						delete(pendingFileChanges, payload.CallID)
					}
					if tsOk && ok && payload.Type == "custom_tool_call" && payload.Name == "apply_patch" && payload.Status != "error" && payload.Status != "failed" {
						for _, change := range extractPatchFileChanges(payload.Input) {
							result.events = append(result.events, event.Event{
								ID:        fmt.Sprintf("%s:%s", codexPayloadCallID(payload, raw, sessionID), change.Path),
								Type:      event.EventFileChange,
								SessionID: sessionID,
								Platform:  event.PlatformCodex,
								Timestamp: ts,
								Data: event.EventData{
									FilePath:   change.Path,
									ChangeType: change.ChangeType,
								},
							})
						}
					}
				}
			}

			if err == nil || parsedLine {
				committedOffset += int64(len(lineBytes))
			}
		}

		if err != nil {
			break
		}
	}

	result.offset = committedOffset
	result.pendingCalls = pendingFileChanges
	// Defer SessionStart for files with no substantive data (empty/idle sessions).
	// The deferred start will be emitted later if real data arrives.
	if !hasSubstantiveEvents(result.events) {
		for _, ev := range result.events {
			if ev.Type == event.EventSessionStart {
				evCopy := ev
				result.pendingStart = &evCopy
				break
			}
		}
		result.events = nil
	}
	return result
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
		if w.stopped() {
			return
		}
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
		// Only short-circuit when the file is unchanged. Shrunk files (size <
		// offset = truncation/rotation) must reach processFile so it can reset
		// the offset to 0 and re-scan.
		if info.Size() == w.seen[path] {
			continue
		}
		w.processFile(path, info.Size())
	}
}

func (w *CodexWatcher) scanRecentDirs() {
	var recentDirs []string
	for _, baseDir := range w.baseDirs {
		recentDirs = append(recentDirs, recentCodexSessionDirs(baseDir, time.Now())...)
	}
	for _, dir := range recentDirs {
		if w.stopped() {
			return
		}
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
			// Same as scanKnownFiles: only `==` short-circuits; shrunk files
			// must reach processFile for truncation detection.
			if offset, seen := w.seen[path]; seen && info.Size() == offset {
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
	// Detect truncation/rotation: file shrank since last scan. Reset to read
	// from the start; dedup via source_id keeps already-stored rows from
	// double-counting. Mirror w.seen so a subsequent same-size scan can
	// short-circuit cleanly (parity with claude_log.go).
	if offset > 0 && size < offset {
		log.Printf("codex watcher: file %s shrank (%d → %d), restarting from offset 0", path, offset, size)
		offset = 0
		w.seen[path] = 0
	}
	if size == offset {
		return // no new bytes
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
	var bufferedEvents []event.Event
	linesRead := 0

	for {
		if linesRead%100 == 0 && w.stopped() {
			break
		}
		linesRead++

		lineBytes, err := reader.ReadBytes('\n')

		if len(lineBytes) > 0 {
			line := bytes.TrimRight(lineBytes, "\r\n")
			parsedLine := false

			if len(line) > 0 {
				var raw codexLogEntry
				if json.Unmarshal(line, &raw) == nil {
					parsedLine = true
					if raw.Type == "turn_context" {
						w.recordTurnContext(sessionID, raw.Payload)
						annotateCodexTokenEvents(bufferedEvents, w.sessionModels[sessionID], w.sessionCWDs[sessionID])
					}

					payload, ok := decodeCodexResponsePayload(raw)
					if ok && payload.Type == "function_call" {
						w.pendingFileChanges[payload.CallID] = codexPendingChange{
							changes:    extractCodexFileChanges(payload.Name, payload.Arguments),
							insertedAt: time.Now(),
						}
					}

					for _, ev := range parseCodexEntryWithContext(raw, sessionID, w.sessionModels[sessionID], w.sessionCWDs[sessionID]) {
						bufferedEvents = append(bufferedEvents, ev)
					}

					ts, tsOk := parseTimestamp(raw.Timestamp)
					if ok && payload.Type == "function_call_output" {
						if tsOk && payload.Status != "error" && payload.Status != "failed" {
							for _, change := range w.pendingFileChanges[payload.CallID].changes {
								bufferedEvents = append(bufferedEvents, event.Event{
									ID:        fmt.Sprintf("%s:%s", payload.CallID, change.Path),
									Type:      event.EventFileChange,
									SessionID: sessionID,
									Platform:  event.PlatformCodex,
									Timestamp: ts,
									Data: event.EventData{
										FilePath:   change.Path,
										ChangeType: change.ChangeType,
									},
								})
							}
						}
						delete(w.pendingFileChanges, payload.CallID)
					}
					if tsOk && ok && payload.Type == "custom_tool_call" && payload.Name == "apply_patch" && payload.Status != "error" && payload.Status != "failed" {
						for _, change := range extractPatchFileChanges(payload.Input) {
							bufferedEvents = append(bufferedEvents, event.Event{
								ID:        fmt.Sprintf("%s:%s", codexPayloadCallID(payload, raw, sessionID), change.Path),
								Type:      event.EventFileChange,
								SessionID: sessionID,
								Platform:  event.PlatformCodex,
								Timestamp: ts,
								Data: event.EventData{
									FilePath:   change.Path,
									ChangeType: change.ChangeType,
								},
							})
						}
					}
				}
			}

			// Only advance offset for complete lines (err == nil means \n was found).
			// A valid JSON object at EOF is also complete even if the writer has
			// not appended a trailing newline yet. Invalid EOF fragments stay
			// uncommitted so they can be completed on a later scan.
			if err == nil || parsedLine {
				committedOffset += int64(len(lineBytes))
			}
		}

		if err != nil {
			break
		}
	}

	w.seen[path] = committedOffset

	// Emit events only if the session has substantive data.
	// Defer SessionStart for empty files; prepend deferred start when data arrives.
	if hasSubstantiveEvents(bufferedEvents) {
		if pending, ok := w.pendingStarts[sessionID]; ok {
			w.emitFn(pending)
			delete(w.pendingStarts, sessionID)
		}
		for _, ev := range bufferedEvents {
			w.emitFn(ev)
		}
	} else {
		for _, ev := range bufferedEvents {
			if ev.Type == event.EventSessionStart {
				w.pendingStarts[sessionID] = ev
				break
			}
		}
	}
}

// hasSubstantiveEvents returns true if events contain durable user/session activity.
func hasSubstantiveEvents(events []event.Event) bool {
	for _, ev := range events {
		switch ev.Type {
		case event.EventSessionStart, event.EventSessionUpdate:
			continue
		default:
			return true
		}
	}
	return false
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
	Input     string `json:"input,omitempty"`
	Output    string `json:"output,omitempty"`
	Status    string `json:"status,omitempty"`
}

// event_msg payload for token_count
type codexEventMsg struct {
	Type string          `json:"type"`
	Info *codexTokenInfo `json:"info,omitempty"`
}

type codexTokenInfo struct {
	LastTokenUsage  codexTokenUsage  `json:"last_token_usage"`
	TotalTokenUsage *codexTokenUsage `json:"total_token_usage,omitempty"`
}

type codexTokenUsage struct {
	InputTokens       int `json:"input_tokens"`
	OutputTokens      int `json:"output_tokens"`
	TotalTokens       int `json:"total_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
}

func parseCodexEntry(entry codexLogEntry, sessionID string) []event.Event {
	return parseCodexEntryWithContext(entry, sessionID, "", "")
}

func parseCodexEntryWithContext(entry codexLogEntry, sessionID, model, cwd string) []event.Event {
	// Every output below uses ts as Event.Timestamp (drives daemon dedup and
	// aggregation) or as part of a synthetic ID. Substituting time.Now() on
	// parse failure would let stale or malformed log lines land in today's
	// stats; dropping the entry is the safer universal choice.
	ts, ok := parseTimestamp(entry.Timestamp)
	if !ok {
		return nil
	}

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
		case "custom_tool_call":
			callID := codexPayloadCallID(payload, entry, sessionID)
			status := event.StatusSuccess
			if payload.Status == "error" || payload.Status == "failed" {
				status = event.StatusFail
			}
			return []event.Event{
				{
					ID:        callID,
					Type:      event.EventToolCallStart,
					SessionID: sessionID,
					Platform:  event.PlatformCodex,
					Timestamp: ts,
					Data: event.EventData{
						ToolName:   payload.Name,
						ToolParams: truncate(payload.Input, 500),
					},
				},
				{
					ID:        callID,
					Type:      event.EventToolCallEnd,
					SessionID: sessionID,
					Platform:  event.PlatformCodex,
					Timestamp: ts,
					Data: event.EventData{
						ToolResult: truncate(payload.Status, 500),
						ToolStatus: status,
					},
				},
			}
		}

	case "turn_context":
		var ctx codexTurnContext
		if json.Unmarshal(entry.Payload, &ctx) != nil {
			return nil
		}
		if ctx.Model == "" {
			ctx.Model = model
		}
		if ctx.CWD == "" {
			ctx.CWD = cwd
		}
		return []event.Event{{
			ID:        fmt.Sprintf("codex-context-%s-%d", sessionID, ts.UnixNano()),
			Type:      event.EventSessionUpdate,
			SessionID: sessionID,
			Platform:  event.PlatformCodex,
			Timestamp: ts,
			Data: event.EventData{
				Model: ctx.Model,
				CWD:   ctx.CWD,
			},
		}}

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
			sourceID := fmt.Sprintf("codex-tokens-%s-%d-%d-%d-%d", sessionID, ts.UnixNano(), usage.InputTokens, usage.OutputTokens, usage.CachedInputTokens)
			if msg.Info.TotalTokenUsage != nil && msg.Info.TotalTokenUsage.TotalTokens != 0 {
				total := msg.Info.TotalTokenUsage
				sourceID = fmt.Sprintf("codex-tokens-%s-total-%d-%d-%d-%d", sessionID, total.InputTokens, total.OutputTokens, total.CachedInputTokens, total.TotalTokens)
			}
			cost := 0.0
			if model != "" {
				cost = estimateCodexCost(usage.InputTokens, usage.OutputTokens, usage.CachedInputTokens, model)
			}
			return []event.Event{{
				ID:        sourceID,
				Type:      event.EventTokenUsage,
				SessionID: sessionID,
				Platform:  event.PlatformCodex,
				Timestamp: ts,
				Data: event.EventData{
					InputTokens:     usage.InputTokens,
					OutputTokens:    usage.OutputTokens,
					CacheReadTokens: usage.CachedInputTokens,
					Model:           model,
					CWD:             cwd,
					CostUSD:         cost,
				},
			}}
		}
	}

	return nil
}

// parseTimestamp parses RFC3339(Nano) and returns (zero, false) on failure.
// Callers must decide whether to skip the entry or substitute a sentinel —
// silently falling back to time.Now() would let malformed historical logs
// pollute today's cost/dedup buckets.
func parseTimestamp(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339, s)
		if err != nil {
			return time.Time{}, false
		}
	}
	return t, true
}

// CodexPricing returns per-million-token pricing for a Codex model.
// cacheReadPricePerM falls back to inputPricePerM when the model has no cache discount.
func CodexPricing(model string) (inputPricePerM, outputPricePerM, cacheReadPricePerM float64) {
	pricing := codexPricing(model)
	cacheP := pricing.cacheReadPerMill
	if cacheP == 0 {
		cacheP = pricing.inputPerMillion
	}
	return pricing.inputPerMillion, pricing.outputPerMillion, cacheP
}

func estimateCodexCost(inputTokens, outputTokens, cachedInputTokens int, model string) float64 {
	inP, outP, cacheP := CodexPricing(model)
	regularInput := inputTokens - cachedInputTokens
	if regularInput < 0 {
		regularInput = 0
	}
	return (float64(regularInput)*inP + float64(cachedInputTokens)*cacheP + float64(outputTokens)*outP) / 1_000_000
}

func annotateCodexTokenEvents(events []event.Event, model, cwd string) {
	for i := range events {
		if events[i].Type != event.EventTokenUsage {
			continue
		}
		if model != "" && events[i].Data.Model == "" {
			events[i].Data.Model = model
			events[i].Data.CostUSD = estimateCodexCost(
				events[i].Data.InputTokens,
				events[i].Data.OutputTokens,
				events[i].Data.CacheReadTokens,
				model,
			)
		}
		if cwd != "" && events[i].Data.CWD == "" {
			events[i].Data.CWD = cwd
		}
	}
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
	if w.sessionModels == nil {
		w.sessionModels = make(map[string]string)
	}
	if w.sessionCWDs == nil {
		w.sessionCWDs = make(map[string]string)
	}
	if w.pendingFileChanges == nil {
		w.pendingFileChanges = make(map[string]codexPendingChange)
	}
	if w.pendingStarts == nil {
		w.pendingStarts = make(map[string]event.Event)
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

func codexPayloadCallID(payload codexResponsePayload, entry codexLogEntry, sessionID string) string {
	if payload.CallID != "" {
		return payload.CallID
	}
	ts, ok := parseTimestamp(entry.Timestamp)
	if !ok {
		ts = time.Now()
	}
	return fmt.Sprintf("codex-custom-%s-%s-%d", sessionID, payload.Name, ts.UnixNano())
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
	// Default bufio.Scanner max token size is 64KB. apply_patch bodies can
	// contain long minified/base64 lines that exceed it; raise the cap so
	// trailing "*** Update File:" headers are not silently skipped.
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
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
	if err := scanner.Err(); err != nil {
		log.Printf("extractPatchFileChanges scan: %v", err)
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
