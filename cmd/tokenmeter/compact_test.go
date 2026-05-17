package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/storage"
)

func TestRunCompactNoFlag(t *testing.T) {
	home := t.TempDir()
	db := openHomeDB(t, home)
	seedCompactSession(t, db, time.Now().Add(-time.Minute), "compact-default")
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	withArgs(t, []string{"tokenmeter", "compact"})
	out := captureStdout(t, func() {
		if err := runCompact(); err != nil {
			t.Fatalf("runCompact: %v", err)
		}
	})

	for _, want := range []string{"TokenMeter compact:", "Before:", "Running ANALYZE", "After:", "Fragmentation:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("compact output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Running VACUUM") {
		t.Fatalf("default compact should not run vacuum:\n%s", out)
	}
}

func TestRunCompactFullFlag(t *testing.T) {
	home := t.TempDir()
	db := openHomeDB(t, home)
	seedCompactSession(t, db, time.Now().Add(-time.Minute), "compact-full")
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	withArgs(t, []string{"tokenmeter", "compact", "--full"})
	out := captureStdout(t, func() {
		if err := runCompact(); err != nil {
			t.Fatalf("runCompact --full: %v", err)
		}
	})

	for _, want := range []string{"TokenMeter compact:", "Running VACUUM", "After:", "Saved:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("compact --full output missing %q:\n%s", want, out)
		}
	}
}

func TestRunCompactDetectsRunningDaemon(t *testing.T) {
	home := t.TempDir()
	db := openHomeDB(t, home)
	seedCompactSession(t, db, time.Now().Add(-time.Minute), "compact-running")
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	base := filepath.Join(home, ".tokenmeter")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "daemon.pid"), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatal(err)
	}

	restoreStdin := stdinFromString(t, "n\n")
	defer restoreStdin()

	withArgs(t, []string{"tokenmeter", "compact", "--full"})
	out := captureStdout(t, func() {
		err := runCompact()
		if err == nil {
			t.Fatal("expected compact to abort when user declines running-daemon prompt")
		}
		if !strings.Contains(err.Error(), "compact aborted") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	if !strings.Contains(out, "daemon is running; VACUUM will block writes. Continue? [y/N]") {
		t.Fatalf("missing running daemon warning:\n%s", out)
	}
}

func seedCompactSession(t *testing.T, db *storage.DB, ts time.Time, sessionID string) {
	t.Helper()
	if err := db.UpsertSession(sessionID, event.PlatformClaude, ts); err != nil {
		t.Fatalf("upsert compact session: %v", err)
	}
	if err := db.InsertTokenUsage("compact-agent", sessionID, 1000, 250, 0, 0, "sonnet", 0.42, ts, "token-"+sessionID); err != nil {
		t.Fatalf("insert compact usage: %v", err)
	}
}

func stdinFromString(t *testing.T, input string) func() {
	t.Helper()
	prev := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdin: %v", err)
	}
	if _, err := w.WriteString(input); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close stdin writer: %v", err)
	}
	os.Stdin = r
	return func() {
		os.Stdin = prev
		_ = r.Close()
	}
}
