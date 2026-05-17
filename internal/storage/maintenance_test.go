package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

func TestVacuumReclaimsDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vacuum.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	old := time.Now().UTC().AddDate(0, 0, -30)
	blob := strings.Repeat("x", 32*1024)
	for i := 0; i < 80; i++ {
		sessionID := fmt.Sprintf("vacuum-%03d", i)
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
	before := fileSize(t, path)

	if err := db.Vacuum(); err != nil {
		t.Fatalf("vacuum: %v", err)
	}
	after := fileSize(t, path)
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	if after >= before {
		t.Fatalf("vacuum did not shrink db file: before=%d after=%d", before, after)
	}
}

func TestOptimizeUpdatesStats(t *testing.T) {
	db := testDB(t)
	if err := db.Optimize(); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	stats, err := db.MaintenanceStats()
	if err != nil {
		t.Fatalf("maintenance stats: %v", err)
	}
	if stats.DBSizeBytes <= 0 {
		t.Fatalf("DBSizeBytes = %d, want > 0", stats.DBSizeBytes)
	}
	if stats.IndexCount <= 0 {
		t.Fatalf("IndexCount = %d, want > 0", stats.IndexCount)
	}
	if stats.FragmentationPct < 0 || stats.FragmentationPct > 100 {
		t.Fatalf("FragmentationPct = %f, want 0..100", stats.FragmentationPct)
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Size()
}
