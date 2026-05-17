package collector

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/testutil"
)

// writeJSONL writes a JSONL file with assistant entries into dir/sessionID.jsonl.
func writeJSONL(t *testing.T, dir, sessionID string, lines int) string {
	t.Helper()
	path := filepath.Join(dir, sessionID+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create jsonl: %v", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for i := range lines {
		_ = enc.Encode(map[string]any{
			"type":      "assistant",
			"sessionId": sessionID,
			"uuid":      fmt.Sprintf("uuid-%d", i),
			"message": map[string]any{
				"model": "claude-sonnet-4-6",
				"usage": map[string]any{
					"input_tokens":  100,
					"output_tokens": 50,
				},
			},
		})
	}
	return path
}

// TestClaudeWatcherNoFDLeak starts a ClaudeLogWatcher, processes several JSONL
// files, then stops it. Verifies that all file handles are released after Stop.
func TestClaudeWatcherNoFDLeak(t *testing.T) {
	projectDir := t.TempDir()
	defer testutil.FDLeakCheck(t)()

	var emitted []event.Event
	w := NewClaudeLogWatcher(func(ev event.Event) { emitted = append(emitted, ev) })
	w.baseDir = projectDir
	w.tickInterval = 30 * time.Millisecond

	// Write JSONL files under a fake project directory (watcher expects subdirs).
	projSubDir := filepath.Join(projectDir, "-tmp-project")
	if err := os.MkdirAll(projSubDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for i := range 5 {
		writeJSONL(t, projSubDir, fmt.Sprintf("session-%d", i), 3)
	}

	w.Start()
	time.Sleep(120 * time.Millisecond) // let watcher scan the files
	w.Stop()
	// All opened JSONL file handles must be released by Stop.
}

// TestCodexWatcherNoFDLeak starts a CodexWatcher over an empty directory and
// stops it. Verifies that no file descriptors are leaked.
func TestCodexWatcherNoFDLeak(t *testing.T) {
	dir := t.TempDir()
	defer testutil.FDLeakCheck(t)()

	w := NewCodexWatcher(noopEmit)
	w.baseDirs = []string{dir}
	w.tickInterval = 30 * time.Millisecond

	w.Start()
	time.Sleep(80 * time.Millisecond)
	w.Stop()
}
