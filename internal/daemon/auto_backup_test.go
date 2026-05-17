package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

func TestRunAutoBackupSkipIfRecent(t *testing.T) {
	base := setWebhookTestHome(t)
	dir := filepath.Join(base, "backups")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	recent := filepath.Join(dir, "auto-recent.db")
	writeBackupRotationFile(t, recent, time.Now().Add(-time.Hour))

	d := New(webhookTestDB(t), filepath.Join(t.TempDir(), "daemon.sock"))
	if err := d.runAutoBackup(); err != nil {
		t.Fatalf("runAutoBackup: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "auto-*.db"))
	if err != nil {
		t.Fatalf("glob auto backups: %v", err)
	}
	if len(matches) != 1 || matches[0] != recent {
		t.Fatalf("recent backup should skip creation, got %v", matches)
	}
}

func TestRunAutoBackupCreatesAndRotates(t *testing.T) {
	base := setWebhookTestHome(t)
	dir := filepath.Join(base, "backups")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	for i := 0; i < 5; i++ {
		path := filepath.Join(dir, "auto-old-"+string(rune('a'+i))+".db")
		writeBackupRotationFile(t, path, old.Add(time.Duration(i)*time.Minute))
	}

	db := webhookTestDB(t)
	now := time.Now().Add(-time.Minute)
	if err := db.UpsertSession("auto-backup-session", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.InsertTokenUsage("agent", "auto-backup-session", 10, 5, 0, 0, "sonnet", 0.12, now, "auto-backup-token"); err != nil {
		t.Fatalf("insert usage: %v", err)
	}
	d := New(db, filepath.Join(t.TempDir(), "daemon.sock"))

	if err := d.runAutoBackup(); err != nil {
		t.Fatalf("runAutoBackup: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "auto-*.db"))
	if err != nil {
		t.Fatalf("glob auto backups: %v", err)
	}
	if len(matches) != 4 {
		t.Fatalf("auto backups after rotate=%d, want 4: %v", len(matches), matches)
	}
	foundNew := false
	for _, path := range matches {
		if strings.HasPrefix(filepath.Base(path), "auto-20") {
			foundNew = true
			backup, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat new backup: %v", err)
			}
			if backup.Size() == 0 {
				t.Fatalf("new backup is empty: %s", path)
			}
		}
	}
	if !foundNew {
		t.Fatalf("new timestamped auto backup not found: %v", matches)
	}
}
