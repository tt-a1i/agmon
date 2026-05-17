package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/storage"
)

func TestRunBackupDefaultPath(t *testing.T) {
	home := t.TempDir()
	db := openHomeDB(t, home)
	seedCompactSession(t, db, time.Now().Add(-time.Minute), "backup-default")
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	withArgs(t, []string{"tokenmeter", "backup"})
	out := captureStdout(t, func() {
		if err := runBackup(); err != nil {
			t.Fatalf("runBackup: %v", err)
		}
	})

	if !strings.Contains(out, "Backup created:") || !strings.Contains(out, "Size:") || !strings.Contains(out, "Original size:") {
		t.Fatalf("backup output missing expected lines:\n%s", out)
	}
	matches, err := filepath.Glob(filepath.Join(home, ".tokenmeter", "backups", "tokenmeter-*.db"))
	if err != nil {
		t.Fatalf("glob backups: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("default backup files: got %v, want exactly one", matches)
	}
	assertBackupHasSession(t, matches[0], "backup-default")
}

func TestRunBackupCustomPath(t *testing.T) {
	home := t.TempDir()
	db := openHomeDB(t, home)
	seedCompactSession(t, db, time.Now().Add(-time.Minute), "backup-custom")
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	dest := filepath.Join(home, "custom-backup.db")

	withArgs(t, []string{"tokenmeter", "backup", dest})
	out := captureStdout(t, func() {
		if err := runBackup(); err != nil {
			t.Fatalf("runBackup custom: %v", err)
		}
	})

	if !strings.Contains(out, "Backup created: "+dest) {
		t.Fatalf("backup output missing custom path:\n%s", out)
	}
	assertBackupHasSession(t, dest, "backup-custom")
}

func TestRunBackupCreatesDirIfMissing(t *testing.T) {
	home := t.TempDir()
	db := openHomeDB(t, home)
	seedCompactSession(t, db, time.Now().Add(-time.Minute), "backup-mkdir")
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	dest := filepath.Join(home, "missing", "nested", "backup.db")

	withArgs(t, []string{"tokenmeter", "backup", dest})
	if err := runBackup(); err != nil {
		t.Fatalf("runBackup mkdir: %v", err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("backup file not created: %v", err)
	}
}

func assertBackupHasSession(t *testing.T, path, sessionID string) {
	t.Helper()
	db, err := storage.Open(path)
	if err != nil {
		t.Fatalf("open backup %s: %v", path, err)
	}
	defer db.Close()
	row, found, err := db.GetSessionByIDPrefix(sessionID)
	if err != nil {
		t.Fatalf("query backup session: %v", err)
	}
	if !found || row.SessionID != sessionID {
		t.Fatalf("backup session found=%v row=%#v want %s", found, row, sessionID)
	}
}
