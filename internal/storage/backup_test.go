package storage

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

func TestBackupToProducesValidSQLite(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	if err := db.UpsertSession("backup-valid", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.InsertTokenUsage("agent", "backup-valid", 100, 50, 0, 0, "sonnet", 0.25, now, "backup-valid-token"); err != nil {
		t.Fatalf("insert token usage: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "backup.db")
	origSize, backupSize, err := db.BackupTo(dest)
	if err != nil {
		t.Fatalf("BackupTo: %v", err)
	}
	if origSize <= 0 || backupSize <= 0 {
		t.Fatalf("sizes: orig=%d backup=%d, want > 0", origSize, backupSize)
	}

	backup, err := Open(dest)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer backup.Close()
	row, found, err := backup.GetSessionByIDPrefix("backup-valid")
	if err != nil {
		t.Fatalf("query backup session: %v", err)
	}
	if !found || row.SessionID != "backup-valid" {
		t.Fatalf("backup session found=%v row=%#v", found, row)
	}
}

func TestBackupToSmallerThanOriginal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "source.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	old := time.Now().UTC().AddDate(0, 0, -30)
	blob := strings.Repeat("x", 32*1024)
	for i := 0; i < 80; i++ {
		sessionID := fmt.Sprintf("backup-shrink-%03d", i)
		if err := db.UpsertSession(sessionID, event.PlatformClaude, old); err != nil {
			t.Fatalf("upsert session %d: %v", i, err)
		}
		if _, err := db.InsertToolCallStart("call-"+sessionID, "agent", sessionID, "Bash", blob, old); err != nil {
			t.Fatalf("insert tool %d: %v", i, err)
		}
		if err := db.EndSession(sessionID, old.Add(time.Second)); err != nil {
			t.Fatalf("end session %d: %v", i, err)
		}
	}
	if _, err := db.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatalf("checkpoint after seed: %v", err)
	}
	if _, err := db.CleanOldSessions(7); err != nil {
		t.Fatalf("clean old sessions: %v", err)
	}
	if _, err := db.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatalf("checkpoint after clean: %v", err)
	}

	origSize, backupSize, err := db.BackupTo(filepath.Join(dir, "backup.db"))
	if err != nil {
		t.Fatalf("BackupTo: %v", err)
	}
	if backupSize >= origSize {
		t.Fatalf("backup should be smaller after VACUUM INTO: orig=%d backup=%d", origSize, backupSize)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
}
