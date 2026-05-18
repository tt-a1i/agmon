package testutil_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/tt-a1i/tokenmeter/internal/testutil"
)

func TestFDSnapshotReturnsList(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("FD snapshot not supported on windows")
	}
	// Open a real file so we have at least one non-noisy FD even on Linux,
	// where FDSnapshot's filter strips stdin/stdout/stderr (typically
	// /dev/null and pipes under `go test` on CI). On macOS FDSnapshot
	// reports "fd:N" for stdin/stdout/stderr without filtering, so this
	// canary just adds one more entry there.
	canary := filepath.Join(t.TempDir(), "fd-canary.txt")
	if err := os.WriteFile(canary, []byte("x"), 0o644); err != nil {
		t.Fatalf("write canary: %v", err)
	}
	f, err := os.Open(canary)
	if err != nil {
		t.Fatalf("open canary: %v", err)
	}
	defer f.Close()

	fds, err := testutil.FDSnapshot()
	if err != nil {
		t.Fatalf("FDSnapshot: %v", err)
	}
	if len(fds) < 1 {
		t.Errorf("FDSnapshot returned %d fds, expected at least 1 (the canary):\n%s",
			len(fds), testutil.FormatFDs(fds))
	}
}

func TestFDLeakCheckPassesIfNoChange(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("FD snapshot not supported on windows")
	}
	inner := &testing.T{}
	cleanup := testutil.FDLeakCheck(inner)
	// No new file descriptors opened.
	cleanup()
	if inner.Failed() {
		t.Error("FDLeakCheck incorrectly reported a leak when no FDs were opened")
	}
}

func TestFDLeakCheckDetectsLeak(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("FD snapshot not supported on windows")
	}

	// Write the canary file first so we can open it.
	canary := filepath.Join(t.TempDir(), "canary.txt")
	if err := os.WriteFile(canary, []byte("x"), 0o644); err != nil {
		t.Fatalf("write canary: %v", err)
	}

	inner := &testing.T{}
	cleanup := testutil.FDLeakCheck(inner)

	// Open the file without closing it — intentional leak for this test.
	f, err := os.Open(canary)
	if err != nil {
		t.Fatalf("open canary: %v", err)
	}

	cleanup() // should detect f as a leaked FD

	f.Close() // actually clean up after detection

	if !inner.Failed() {
		t.Error("FDLeakCheck should have reported a leak for an unclosed file but did not")
	}
}

func TestDBConnLeakDetectsLeak(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "connleak.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	inner := &testing.T{}
	cleanup := testutil.DBConnLeakCheck(inner, db)

	// Execute a query without closing Rows — intentional connection leak.
	rows, err := db.Query("SELECT 1")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	_ = rows // intentionally not closed here

	cleanup() // should detect InUse > 0

	rows.Close() // actually clean up

	if !inner.Failed() {
		t.Error("DBConnLeakCheck should have reported a leak for unclosed *sql.Rows but did not")
	}
}

func TestTmpFileLeakDetectsLeak(t *testing.T) {
	prefix := "tmtest-fdleak-"

	inner := &testing.T{}
	cleanup := testutil.TmpFileLeakCheck(inner, prefix)

	// Create a tmp file matching the prefix without removing it.
	f, err := os.CreateTemp(os.TempDir(), prefix)
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	f.Close()
	leaked := f.Name()
	defer os.Remove(leaked) // clean up after this test exits

	cleanup() // should detect the new file

	if !inner.Failed() {
		t.Error("TmpFileLeakCheck should have reported a leak for an undeleted tmp file but did not")
	}
}
