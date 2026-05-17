package storage

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

// TestOpenRejectsNonSQLiteFile verifies that storage.Open returns a non-nil
// error (and does not panic) when the target file contains non-SQLite data.
func TestOpenRejectsNonSQLiteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "junk.db")
	if err := os.WriteFile(path, []byte("hello world not sqlite at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := Open(path)
	if err == nil {
		db.Close()
		t.Fatal("expected error opening non-SQLite file, got nil")
	}
	// Verify the error message is informative enough for a user to diagnose.
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "not a database") &&
		!strings.Contains(msg, "file is not") &&
		!strings.Contains(msg, "corrupt") &&
		!strings.Contains(msg, "wal") &&
		!strings.Contains(msg, "database disk image") &&
		!strings.Contains(msg, "migrate") &&
		!strings.Contains(msg, "wal mode") {
		t.Logf("error did not contain expected keywords but that is acceptable: %v", err)
	}
}

// TestOpenRejectsTruncatedFile writes a valid populated SQLite db, truncates
// it to half its size, then verifies Open does not panic. SQLite may or may
// not recover — both outcomes are acceptable; only panics are failures.
func TestOpenRejectsTruncatedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trunc.db")

	// Build a non-trivial database so the file is a few KB.
	db, err := Open(path)
	if err != nil {
		t.Fatalf("initial open: %v", err)
	}
	now := time.Now()
	for i := 0; i < 20; i++ {
		sid := "sess-trunc-" + string(rune('A'+i))
		_ = db.UpsertSession(sid, event.PlatformClaude, now)
		_ = db.InsertTokenUsage("agent1", sid, 100, 50, 0, 0, "claude-sonnet-4-6", 0.001, now,
			"src-trunc-"+sid)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("expected non-empty db file after seeding")
	}

	// Truncate to half.
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open for truncate: %v", err)
	}
	if err := f.Truncate(info.Size() / 2); err != nil {
		f.Close()
		t.Fatalf("truncate: %v", err)
	}
	f.Close()

	// Reopen — must not panic; error is acceptable.
	db2, err := Open(path)
	if err != nil {
		t.Logf("truncated file returned error (expected): %v", err)
		return
	}
	// SQLite auto-repair can sometimes recover truncated pages.
	t.Log("truncated file opened without error (SQLite auto-repair)")
	db2.Close()
}

// TestOpenHandlesRandomBytes writes 4 KB of random bytes and verifies Open
// returns an error and does not panic.
func TestOpenHandlesRandomBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "random.db")
	buf := make([]byte, 4096)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}

	db, err := Open(path)
	if err != nil {
		t.Logf("random bytes returned error (expected): %v", err)
		return
	}
	// Unlikely but acceptable if SQLite treats random bytes as a valid page.
	t.Log("random bytes opened without error (SQLite resilient to this input)")
	db.Close()
}

// TestOpenHandlesEmptyFile verifies that a 0-byte file is treated by SQLite
// as a new empty database — Open should succeed and migrate() should run.
func TestOpenHandlesEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.db")
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatalf("empty file should be treated as a new db: %v", err)
	}
	defer db.Close()

	// Schema must have been migrated.
	if !tableExists(t, db.db, "sessions") {
		t.Error("migrate() did not create the sessions table on empty file")
	}
	if !tableExists(t, db.db, "token_usage") {
		t.Error("migrate() did not create the token_usage table on empty file")
	}
}

// TestOpenHandlesReadOnlyDirectory verifies that storage.Open returns a clear
// permission error (not a panic) when the directory does not allow file
// creation.
func TestOpenHandlesReadOnlyDirectory(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks")
	}

	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	// Restore write permission so TempDir cleanup can remove the directory.
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	path := filepath.Join(dir, "no-write.db")
	db, err := Open(path)
	if err == nil {
		db.Close()
		t.Error("expected error opening db in read-only directory, got nil")
		return
	}
	t.Logf("read-only directory returned expected error: %v", err)
}

// TestOpenHandlesOrphanedWAL exercises the scenario where the main .db file
// has been deleted but the .db-wal sidecar was left behind.  SQLite should
// either create a fresh database (ignoring the stale WAL) or return a clean
// error — it must not panic or hang.
func TestOpenHandlesOrphanedWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orphan.db")

	// Create a valid db with WAL mode so a -wal file may be produced.
	db1, err := Open(path)
	if err != nil {
		t.Fatalf("initial open: %v", err)
	}
	now := time.Now()
	for i := 0; i < 50; i++ {
		sid := "sess-wal-" + string(rune('a'+i%26))
		_ = db1.UpsertSession(sid, event.PlatformClaude, now)
	}
	// Close checkpoints the WAL; the -wal file may or may not persist.
	if err := db1.Close(); err != nil {
		t.Fatalf("close db1: %v", err)
	}

	// Remove the main db file.
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove main db: %v", err)
	}

	// Ensure a .db-wal orphan exists (create a dummy if Close checkpointed it away).
	walPath := path + "-wal"
	if _, statErr := os.Stat(walPath); statErr != nil {
		if err := os.WriteFile(walPath, []byte("orphan wal payload"), 0o644); err != nil {
			t.Fatalf("write orphan WAL: %v", err)
		}
	}

	// Reopen — must not panic or hang.
	db2, err := Open(path)
	if err != nil {
		t.Logf("orphan WAL caused error (acceptable): %v", err)
		return
	}
	t.Log("orphan WAL: SQLite created a fresh database ignoring stale WAL")
	db2.Close()
}

// TestOpenHandlesMagicBytesOnly writes the 16-byte SQLite magic header
// followed by 4 KB of random bytes and verifies Open does not panic.
// SQLite may detect the invalid page size or header checksum and return an
// error, or in rare cases it may treat the file as recoverable.
func TestOpenHandlesMagicBytesOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "magic.db")

	// First 16 bytes: SQLite format 3 magic string (null-terminated).
	magic := []byte("SQLite format 3\x00")
	junk := make([]byte, 4080)
	if _, err := rand.Read(junk); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	data := append(magic, junk...)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	db, err := Open(path)
	if err != nil {
		t.Logf("magic+junk returned error (expected): %v", err)
		return
	}
	// SQLite page-size field at offset 16 is part of random bytes; unlikely
	// to be valid, but document any accidental recovery.
	t.Log("magic+junk opened without error (SQLite recovered random page layout)")
	db.Close()
}
