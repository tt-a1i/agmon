package main

import (
	"strings"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

func TestRunCheckpoint(t *testing.T) {
	home := t.TempDir()
	db := openHomeDB(t, home)
	now := time.Now()
	if err := db.UpsertSession("checkpoint-cli", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.InsertTokenUsage("agent", "checkpoint-cli", 100, 50, 0, 0, "sonnet", 0.25, now, "checkpoint-cli-token"); err != nil {
		t.Fatalf("insert token usage: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	withArgs(t, []string{"tokenmeter", "checkpoint"})
	out := captureStdout(t, func() {
		if err := runCheckpoint(); err != nil {
			t.Fatalf("runCheckpoint: %v", err)
		}
	})

	for _, want := range []string{"WAL checkpoint:", "Before:", "Running TRUNCATE", "After:", "Reclaimed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("checkpoint output missing %q:\n%s", want, out)
		}
	}
}
