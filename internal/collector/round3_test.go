package collector

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

// TestCodexPendingGCDropsOrphans verifies the watcher prunes pending
// function_call entries whose function_call_output never arrived (e.g.
// codex crashed mid-tool-call). 2h TTL aligns with MarkStaleSessionsEnded.
func TestCodexPendingGCDropsOrphans(t *testing.T) {
	w := NewCodexWatcher(func(event.Event) {})

	// One entry well past TTL, one fresh.
	w.pendingFileChanges["old-call"] = codexPendingChange{
		changes:    []codexFileChange{{Path: "a", ChangeType: event.FileEdit}},
		insertedAt: time.Now().Add(-3 * time.Hour),
	}
	w.pendingFileChanges["fresh-call"] = codexPendingChange{
		changes:    []codexFileChange{{Path: "b", ChangeType: event.FileEdit}},
		insertedAt: time.Now(),
	}

	out := captureCollectorLogs(t, w.gcPending)

	if _, ok := w.pendingFileChanges["old-call"]; ok {
		t.Errorf("expected old-call entry to be GC'd")
	}
	if _, ok := w.pendingFileChanges["fresh-call"]; !ok {
		t.Errorf("fresh entry should remain")
	}
	if !strings.Contains(out, "gc dropped 1") {
		t.Errorf("expected GC log message, got: %s", out)
	}
}

// TestCodexGCThrottling verifies gcPending only runs every gcInterval ticks,
// not on every scan.
func TestCodexGCThrottling(t *testing.T) {
	w := NewCodexWatcher(func(event.Event) {})
	w.baseDirs = []string{filepath.Join(t.TempDir(), "missing")}
	w.gcInterval = 5

	w.pendingFileChanges["old"] = codexPendingChange{
		changes:    []codexFileChange{{Path: "x", ChangeType: event.FileEdit}},
		insertedAt: time.Now().Add(-3 * time.Hour),
	}

	// First scanLogs enters initialDiscovery and returns early — scanCount
	// doesn't increment. We need 1 + gcInterval calls to actually fire.
	w.scanLogs() // initial discovery, scanCount stays 0
	for i := 0; i < 4; i++ {
		w.scanLogs()
	}
	if _, ok := w.pendingFileChanges["old"]; !ok {
		t.Errorf("GC ran too early at scanCount=%d", w.scanCount)
	}

	w.scanLogs() // scanCount=5, GC fires
	if _, ok := w.pendingFileChanges["old"]; ok {
		t.Errorf("GC should have fired at scanCount=5, entry still present (count=%d)", w.scanCount)
	}
}

// TestClaudeLogWatcherTruncationResetsOffset verifies a shrunk file is
// re-read from offset 0 instead of being silently skipped.
func TestClaudeLogWatcherTruncationResetsOffset(t *testing.T) {
	dir := t.TempDir()
	projDir := filepath.Join(dir, "test-project")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sessionID := "11111111-2222-3333-4444-555555555555"
	path := filepath.Join(projDir, sessionID+".jsonl")

	// Initial content: one assistant row with valid token usage.
	initial := fmt.Sprintf(`{"type":"assistant","sessionId":"%s","uuid":"u1","gitBranch":"main","timestamp":"2026-01-01T12:00:00.000Z","message":{"model":"claude-sonnet-4-6","usage":{"input_tokens":10,"output_tokens":20}}}`, sessionID) + "\n"
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	var events []event.Event
	w := NewClaudeLogWatcher(func(ev event.Event) {
		events = append(events, ev)
	})
	w.baseDir = dir

	w.scanLogs() // first pass — picks up u1
	firstCount := len(events)
	if firstCount == 0 {
		t.Fatalf("expected at least 1 event from first scan, got 0")
	}

	// Simulate truncate + rewrite: replace file content with a brand new row.
	truncated := fmt.Sprintf(`{"type":"assistant","sessionId":"%s","uuid":"u2","gitBranch":"main","timestamp":"2026-01-01T12:05:00.000Z","message":{"model":"claude-sonnet-4-6","usage":{"input_tokens":5,"output_tokens":7}}}`, sessionID) + "\n"
	if err := os.WriteFile(path, []byte(truncated), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	w.scanLogs() // second pass — must detect shrink, reset offset, re-emit
	// Watcher re-emits u1 after truncate (it doesn't know daemon stored it).
	// Daemon-level source_id dedup keeps the DB row count correct — covered
	// by TestProcessEventClaudeWatcherToDB in daemon_test.go.
	if len(events) <= firstCount {
		t.Fatalf("watcher silently skipped truncated file; events before=%d after=%d", firstCount, len(events))
	}
	// The second-scan emission must reference uuid u2 (the new row).
	gotU2 := false
	for _, ev := range events[firstCount:] {
		if strings.Contains(ev.ID, "u2") {
			gotU2 = true
			break
		}
	}
	if !gotU2 {
		t.Errorf("post-truncate events should include uuid u2, got: %v", events[firstCount:])
	}
}
