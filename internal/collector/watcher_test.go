package collector

import (
	"bytes"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestCodexWatcherScanLogsSkipsMissingDirs(t *testing.T) {
	w := NewCodexWatcher(func(event.Event) {})
	w.baseDirs = []string{filepath.Join(t.TempDir(), "missing")}

	// Missing directories are silently skipped (archived_sessions may not exist).
	w.scanLogs()
	if !w.initialDiscovery {
		t.Fatal("expected initial discovery to complete even with missing dirs")
	}
}

func TestCodexWatcherStopWaitsForInFlightScan(t *testing.T) {
	w := NewCodexWatcher(func(event.Event) {})
	w.tickInterval = time.Millisecond

	started := make(chan struct{})
	release := make(chan struct{})
	stopped := make(chan struct{})
	var startedOnce sync.Once
	w.scanFn = func() {
		startedOnce.Do(func() { close(started) })
		<-release
	}

	w.Start()
	<-started

	go func() {
		w.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
		t.Fatal("Stop returned before scan completed")
	case <-time.After(20 * time.Millisecond):
	}

	close(release)

	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not return after scan completed")
	}
}

func TestClaudeLogWatcherStopWaitsForInFlightScan(t *testing.T) {
	w := NewClaudeLogWatcher(func(event.Event) {})
	w.tickInterval = time.Millisecond

	started := make(chan struct{})
	release := make(chan struct{})
	stopped := make(chan struct{})
	var startedOnce sync.Once
	w.scanFn = func() {
		startedOnce.Do(func() { close(started) })
		<-release
	}

	w.Start()
	<-started

	go func() {
		w.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
		t.Fatal("Stop returned before scan completed")
	case <-time.After(20 * time.Millisecond):
	}

	close(release)

	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not return after scan completed")
	}
}
