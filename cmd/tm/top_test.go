package main

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/storage"
)

func TestRunTopOnceProducesSnapshot(t *testing.T) {
	home := t.TempDir()
	db := openHomeDB(t, home)
	seedTopSnapshot(t, db)
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	var out bytes.Buffer
	if err := runTopWithDeps([]string{"--once"}, &out, nil); err != nil {
		t.Fatalf("runTop once: %v", err)
	}
	text := out.String()
	for _, want := range []string{"TokenMeter @", "Today", "This month", "Top sessions by cost (24h):", "Top tools today:", "Models:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("top snapshot missing %q:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "agmon/main") || !strings.Contains(text, "Edit") || !strings.Contains(text, "claude-sonnet-4-6") {
		t.Fatalf("top snapshot missing seeded data:\n%s", text)
	}
}

func TestRunTopRespectsInterval(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("100ms interval timing is too tight for the Windows CI runner; the loop only fires once before the 260ms stop signal")
	}
	home := t.TempDir()
	db := openHomeDB(t, home)
	seedTopSnapshot(t, db)
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	stop := make(chan struct{})
	var out bytes.Buffer
	errCh := make(chan error, 1)
	go func() {
		errCh <- runTopWithDeps([]string{"--interval", "100ms", "--no-clear"}, &out, stop)
	}()
	time.Sleep(260 * time.Millisecond)
	close(stop)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("runTop interval: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runTop interval did not stop")
	}
	if frames := strings.Count(out.String(), "TokenMeter @"); frames < 2 {
		t.Fatalf("frames=%d, want >=2:\n%s", frames, out.String())
	}
}

func TestRunTopNoClear(t *testing.T) {
	home := t.TempDir()
	db := openHomeDB(t, home)
	seedTopSnapshot(t, db)
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	var out bytes.Buffer
	if err := runTopWithDeps([]string{"--once", "--no-clear"}, &out, nil); err != nil {
		t.Fatalf("runTop no-clear: %v", err)
	}
	text := out.String()
	if strings.Contains(text, "\033[2J") || strings.Contains(text, "\033[H") {
		t.Fatalf("no-clear output contains clear codes:\n%q", text)
	}
	if !strings.Contains(text, "===") {
		t.Fatalf("no-clear output missing frame separator:\n%s", text)
	}
}

func TestRunTopWithEmptyDB(t *testing.T) {
	home := t.TempDir()
	db := openHomeDB(t, home)
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	var out bytes.Buffer
	if err := runTopWithDeps([]string{"--once"}, &out, nil); err != nil {
		t.Fatalf("runTop empty: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "TokenMeter @") || !strings.Contains(text, "no data") {
		t.Fatalf("empty top output missing friendly no data:\n%s", text)
	}
}

func seedTopSnapshot(t *testing.T, db *storage.DB) {
	t.Helper()
	now := time.Now()
	// Anchor both seeds within the last few minutes so the test passes
	// regardless of when the wall clock fires (previously now-3h / now-1h
	// would slip into "yesterday" if invoked shortly after local midnight,
	// breaking the "Top tools today" and "Models" sections).
	seedTopSession(t, db, "top-claude", event.PlatformClaude, "/work/agmon", "main", "claude-sonnet-4-6", 12.34, now.Add(-2*time.Minute))
	seedTopSession(t, db, "top-codex", event.PlatformCodex, "/work/web", "feature", "gpt-5.5", 8.90, now.Add(-time.Minute))
}

func seedTopSession(t *testing.T, db *storage.DB, sessionID string, platform event.Platform, cwd, branch, model string, cost float64, ts time.Time) {
	t.Helper()
	if err := db.UpsertSession(sessionID, platform, ts); err != nil {
		t.Fatalf("upsert top session: %v", err)
	}
	if err := db.UpdateSessionMeta(sessionID, cwd, branch); err != nil {
		t.Fatalf("update top session meta: %v", err)
	}
	if err := db.InsertTokenUsage("agent-"+sessionID, sessionID, 1000, 500, 0, 0, model, cost, ts, "token-"+sessionID); err != nil {
		t.Fatalf("insert top token usage: %v", err)
	}
	for i := 0; i < 3; i++ {
		callID := sessionID + "-call-" + string(rune('a'+i))
		toolName := "Edit"
		if i == 2 {
			toolName = "Read"
		}
		start := ts.Add(time.Duration(i) * time.Minute)
		if _, err := db.InsertToolCallStart(callID, "agent-"+sessionID, sessionID, toolName, "{}", start); err != nil {
			t.Fatalf("insert top tool start: %v", err)
		}
		status := event.StatusSuccess
		if sessionID == "top-codex" && i == 0 {
			status = event.StatusFail
		}
		if err := db.UpdateToolCallEnd(callID, "ok", status, int64(100+i*50), start.Add(time.Second)); err != nil {
			t.Fatalf("insert top tool end: %v", err)
		}
	}
}
