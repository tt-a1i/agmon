package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/storage"
)

func TestRunRestoreCreatesPreBackup(t *testing.T) {
	home := t.TempDir()
	current := openHomeDB(t, home)
	seedCompactSession(t, current, time.Now().Add(-time.Minute), "restore-before")
	if err := current.Close(); err != nil {
		t.Fatalf("close current db: %v", err)
	}

	sourcePath := createRestoreSourceDB(t, "restore-after")

	withArgs(t, []string{"tokenmeter", "restore", sourcePath})
	out := captureStdout(t, func() {
		if err := runRestore(); err != nil {
			t.Fatalf("runRestore: %v", err)
		}
	})

	for _, want := range []string{"Pre-restore backup:", "Restored from: " + sourcePath, "New DB size:", "Run 'tm doctor' to verify."} {
		if !strings.Contains(out, want) {
			t.Fatalf("restore output missing %q:\n%s", want, out)
		}
	}
	matches, err := filepath.Glob(filepath.Join(home, ".tokenmeter", "backups", "pre-restore-*.db"))
	if err != nil {
		t.Fatalf("glob pre-restore backups: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("pre-restore backups: got %v, want exactly one", matches)
	}
	assertBackupHasSession(t, matches[0], "restore-before")
	assertBackupHasSession(t, storage.DefaultDBPath(), "restore-after")
}

func TestRunRestoreRejectsInvalidSource(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	invalid := filepath.Join(home, "not-sqlite.db")
	if err := os.WriteFile(invalid, []byte("not a sqlite database"), 0o644); err != nil {
		t.Fatalf("write invalid source: %v", err)
	}

	withArgs(t, []string{"tokenmeter", "restore", invalid})
	err := runRestore()
	if err == nil {
		t.Fatal("expected invalid source error")
	}
	if !strings.Contains(err.Error(), "invalid backup source") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunRestoreDetectsRunningDaemon(t *testing.T) {
	home := t.TempDir()
	current := openHomeDB(t, home)
	seedCompactSession(t, current, time.Now().Add(-time.Minute), "restore-running-before")
	if err := current.Close(); err != nil {
		t.Fatalf("close current db: %v", err)
	}
	base := filepath.Join(home, ".tokenmeter")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "daemon.pid"), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatal(err)
	}
	sourcePath := createRestoreSourceDB(t, "restore-running-after")

	restoreStdin := stdinFromString(t, "n\n")
	defer restoreStdin()

	withArgs(t, []string{"tokenmeter", "restore", sourcePath})
	out := captureStdout(t, func() {
		err := runRestore()
		if err == nil {
			t.Fatal("expected restore to abort when user declines running-daemon prompt")
		}
		if !strings.Contains(err.Error(), "restore aborted") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	if !strings.Contains(out, "daemon is running. Stop daemon first? [y/N]") {
		t.Fatalf("missing running daemon warning:\n%s", out)
	}
	assertBackupHasSession(t, storage.DefaultDBPath(), "restore-running-before")
}

func createRestoreSourceDB(t *testing.T, sessionID string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), sessionID+".db")
	db, err := storage.Open(path)
	if err != nil {
		t.Fatalf("open restore source: %v", err)
	}
	seedCompactSession(t, db, time.Now().Add(-time.Minute), sessionID)
	if err := db.Close(); err != nil {
		t.Fatalf("close restore source: %v", err)
	}
	return path
}
