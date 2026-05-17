package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRotateBackupsKeepsLatestN(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 6; i++ {
		path := filepath.Join(dir, "auto-"+base.Add(time.Duration(i)*time.Minute).Format("20060102-150405")+".db")
		writeBackupRotationFile(t, path, base.Add(time.Duration(i)*time.Minute))
	}

	if err := rotateBackups(dir, "auto-", 4); err != nil {
		t.Fatalf("rotateBackups: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "auto-*.db"))
	if err != nil {
		t.Fatalf("glob backups: %v", err)
	}
	if len(matches) != 4 {
		t.Fatalf("kept backups=%d, want 4: %v", len(matches), matches)
	}
	for i := 0; i < 2; i++ {
		path := filepath.Join(dir, "auto-"+base.Add(time.Duration(i)*time.Minute).Format("20060102-150405")+".db")
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("old backup still exists: %s err=%v", path, err)
		}
	}
}

func TestRotateBackupsIgnoresOtherFiles(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-time.Hour)
	for i := 0; i < 5; i++ {
		path := filepath.Join(dir, "auto-"+old.Add(time.Duration(i)*time.Minute).Format("20060102-150405")+".db")
		writeBackupRotationFile(t, path, old.Add(time.Duration(i)*time.Minute))
	}
	manual := filepath.Join(dir, "manual-backup.db")
	writeBackupRotationFile(t, manual, old.Add(-time.Hour))

	if err := rotateBackups(dir, "auto-", 4); err != nil {
		t.Fatalf("rotateBackups: %v", err)
	}
	if _, err := os.Stat(manual); err != nil {
		t.Fatalf("manual backup should not be rotated: %v", err)
	}
}

func writeBackupRotationFile(t *testing.T, path string, modTime time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte("backup"), 0o644); err != nil {
		t.Fatalf("write backup %s: %v", path, err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("chtimes backup %s: %v", path, err)
	}
}
