package collector

import (
	"bytes"
	"log"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tt-a1i/agmon/internal/event"
)

func captureCollectorLogs(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prevWriter := log.Writer()
	prevFlags := log.Flags()
	prevPrefix := log.Prefix()
	log.SetOutput(&buf)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(prevWriter)
		log.SetFlags(prevFlags)
		log.SetPrefix(prevPrefix)
	})

	fn()
	return buf.String()
}

func TestClaudeLogWatcherScanLogsReportsBaseDirReadError(t *testing.T) {
	w := NewClaudeLogWatcher(func(event.Event) {})
	w.baseDir = filepath.Join(t.TempDir(), "missing")

	out := captureCollectorLogs(t, w.scanLogs)
	if !strings.Contains(out, "claude watcher read base dir") {
		t.Fatalf("expected watcher to log base dir read failure, got %q", out)
	}
}

func TestCodexWatcherScanLogsReportsWalkError(t *testing.T) {
	w := NewCodexWatcher(func(event.Event) {})
	w.baseDir = filepath.Join(t.TempDir(), "missing")

	out := captureCollectorLogs(t, w.scanLogs)
	if !strings.Contains(out, "codex watcher walk") {
		t.Fatalf("expected watcher to log walk failure, got %q", out)
	}
}
