package storage

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

func TestCheckpointTruncateReducesWalSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoint.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if _, err := db.db.Exec("PRAGMA wal_autocheckpoint=0"); err != nil {
		t.Fatalf("disable autocheckpoint: %v", err)
	}

	seedCheckpointRows(t, db, 1000)
	before := walSize(t, path)
	if before == 0 {
		t.Fatalf("wal size before checkpoint = 0")
	}

	result, err := db.CheckpointTruncate()
	if err != nil {
		t.Fatalf("CheckpointTruncate: %v", err)
	}
	after := walSize(t, path)
	if after >= before {
		t.Fatalf("wal size after checkpoint = %d, want < before %d; result=%#v", after, before, result)
	}
	if !result.Truncated {
		t.Fatalf("checkpoint result = %#v, want truncated", result)
	}
}

func TestCheckpointTruncateIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoint-idempotent.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	seedCheckpointRows(t, db, 25)

	if _, err := db.CheckpointTruncate(); err != nil {
		t.Fatalf("first CheckpointTruncate: %v", err)
	}
	if _, err := db.CheckpointTruncate(); err != nil {
		t.Fatalf("second CheckpointTruncate: %v", err)
	}
}

func TestCheckpointHandlesPureReadDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoint-read.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if _, err := db.GetVisibleSessionCount(); err != nil {
		t.Fatalf("read db: %v", err)
	}
	if _, err := db.CheckpointTruncate(); err != nil {
		t.Fatalf("CheckpointTruncate: %v", err)
	}
}

func seedCheckpointRows(t *testing.T, db *DB, n int) {
	t.Helper()
	now := time.Now()
	if err := db.UpsertSession("checkpoint-session", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	for i := 0; i < n; i++ {
		if err := db.InsertTokenUsage("checkpoint-agent", "checkpoint-session", 100+i, 50, 0, 0, "sonnet", 0.01, now, "checkpoint-token-"+strconv.Itoa(i)); err != nil {
			t.Fatalf("insert token usage %d: %v", i, err)
		}
	}
}

func walSize(t *testing.T, dbPath string) int64 {
	t.Helper()
	info, err := os.Stat(dbPath + "-wal")
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("stat wal: %v", err)
	}
	return info.Size()
}
