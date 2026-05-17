package collector

import (
	"os"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/testutil"
)

func noopEmit(_ event.Event) {}

func TestClaudeLogWatcherStartStopNoLeak(t *testing.T) {
	defer testutil.LeakCheck(t)()

	dir := t.TempDir()
	w := NewClaudeLogWatcher(noopEmit)
	w.baseDir = dir
	w.tickInterval = 50 * time.Millisecond // fast interval so we exercise a few ticks

	w.Start()
	time.Sleep(120 * time.Millisecond) // let the poll loop tick a couple of times
	w.Stop()
}

func TestClaudeLogWatcherEmptyDirNoLeak(t *testing.T) {
	defer testutil.LeakCheck(t)()

	w := NewClaudeLogWatcher(noopEmit)
	w.baseDir = t.TempDir() // exists but empty — watcher should handle gracefully
	w.tickInterval = 50 * time.Millisecond

	w.Start()
	time.Sleep(80 * time.Millisecond)
	w.Stop()
}

func TestClaudeLogWatcherMissingDirNoLeak(t *testing.T) {
	defer testutil.LeakCheck(t)()

	w := NewClaudeLogWatcher(noopEmit)
	w.baseDir = t.TempDir() + "/nonexistent" // doesn't exist
	w.tickInterval = 50 * time.Millisecond

	w.Start()
	time.Sleep(80 * time.Millisecond)
	w.Stop()
}

func TestCodexWatcherStartStopNoLeak(t *testing.T) {
	defer testutil.LeakCheck(t)()

	dir := t.TempDir()
	w := NewCodexWatcher(noopEmit)
	w.baseDirs = []string{dir}
	w.tickInterval = 50 * time.Millisecond

	w.Start()
	time.Sleep(120 * time.Millisecond)
	w.Stop()
}

func TestCodexWatcherMissingDirNoLeak(t *testing.T) {
	defer testutil.LeakCheck(t)()

	w := NewCodexWatcher(noopEmit)
	w.baseDirs = []string{os.TempDir() + "/codex-nonexistent-" + t.Name()}
	w.tickInterval = 50 * time.Millisecond

	w.Start()
	time.Sleep(80 * time.Millisecond)
	w.Stop()
}
