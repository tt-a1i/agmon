package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tt-a1i/agmon/internal/event"
	"github.com/tt-a1i/agmon/internal/storage"
)

func testDaemon(t *testing.T) (*Daemon, *storage.DB) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db, filepath.Join(dir, "agmon.sock")), db
}

func TestRemoteSubscriberReceivesBroadcast(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	sockPath := filepath.Join(os.TempDir(), fmt.Sprintf("agmon-%d.sock", time.Now().UnixNano()))
	d := New(db, sockPath)
	if err := d.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	defer d.Stop()

	eventCh, closeFn, err := SubscribeRemote(sockPath)
	if err != nil {
		t.Fatalf("subscribe remote: %v", err)
	}
	defer closeFn()

	want := event.Event{
		ID:        "session-start-s1",
		Type:      event.EventSessionStart,
		SessionID: "s1",
		Platform:  event.PlatformClaude,
		Timestamp: time.Now().UTC(),
	}
	d.ProcessExternalEvent(want)

	select {
	case got := <-eventCh:
		if got.ID != want.ID {
			t.Fatalf("event id: got %q want %q", got.ID, want.ID)
		}
		if got.SessionID != want.SessionID {
			t.Fatalf("session id: got %q want %q", got.SessionID, want.SessionID)
		}
		if got.Type != want.Type {
			t.Fatalf("event type: got %q want %q", got.Type, want.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for broadcast")
	}
}

func TestProcessEventEndsHistoricalSessionStart(t *testing.T) {
	d, db := testDaemon(t)
	evTime := time.Now().UTC().Add(-3 * time.Hour)

	err := d.processEvent(event.Event{
		ID:        "session-start-old",
		Type:      event.EventSessionStart,
		SessionID: "old-session",
		Platform:  event.PlatformCodex,
		Timestamp: evTime,
		Data: event.EventData{
			CWD:       "/tmp/project",
			GitBranch: "main",
		},
	})
	if err != nil {
		t.Fatalf("process event: %v", err)
	}

	session, found, err := db.GetSessionByIDPrefix("old-session")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if !found {
		t.Fatal("expected session to be created")
	}
	if session.Status != "ended" {
		t.Fatalf("expected historical session to be ended, got %q", session.Status)
	}
	if session.CWD != "/tmp/project" {
		t.Fatalf("cwd: got %q", session.CWD)
	}
	if session.GitBranch != "main" {
		t.Fatalf("git branch: got %q", session.GitBranch)
	}
	if session.EndTime == nil {
		t.Fatal("expected historical session to have end time")
	}
}

func TestProcessEventSessionEndInterruptsPendingToolCalls(t *testing.T) {
	d, db := testDaemon(t)
	now := time.Now().UTC()

	if err := db.UpsertSession("s1", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.InsertToolCallStart("call-1", "agent-1", "s1", "Edit", "{}", now); err != nil {
		t.Fatalf("insert tool call: %v", err)
	}

	err := d.processEvent(event.Event{
		ID:        "session-end-s1",
		Type:      event.EventSessionEnd,
		SessionID: "s1",
		Platform:  event.PlatformClaude,
		Timestamp: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("process event: %v", err)
	}

	session, found, err := db.GetSessionByIDPrefix("s1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if !found {
		t.Fatal("expected session to exist")
	}
	if session.Status != "ended" {
		t.Fatalf("expected ended session, got %q", session.Status)
	}

	calls, err := db.ListToolCalls("s1", 10)
	if err != nil {
		t.Fatalf("list tool calls: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if calls[0].Status != "interrupted" {
		t.Fatalf("expected interrupted tool call, got %q", calls[0].Status)
	}
}

func TestProcessEventTokenUsageUpdatesSessionTotals(t *testing.T) {
	d, db := testDaemon(t)
	now := time.Now().UTC()

	err := d.processEvent(event.Event{
		ID:        "token-1",
		Type:      event.EventTokenUsage,
		SessionID: "s-token",
		AgentID:   "agent-1",
		Platform:  event.PlatformClaude,
		Timestamp: now,
		Data: event.EventData{
			InputTokens:         100,
			OutputTokens:        25,
			CacheCreationTokens: 10,
			CacheReadTokens:     5,
			Model:               "sonnet",
			CostUSD:             1.25,
			CWD:                 "/tmp/agmon",
			GitBranch:           "feature/test",
		},
	})
	if err != nil {
		t.Fatalf("process event: %v", err)
	}

	session, found, err := db.GetSessionByIDPrefix("s-token")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if !found {
		t.Fatal("expected session to exist")
	}
	if session.TotalInputTokens != 100 || session.TotalOutputTokens != 25 {
		t.Fatalf("unexpected totals: in=%d out=%d", session.TotalInputTokens, session.TotalOutputTokens)
	}
	if session.TotalCacheCreationTokens != 10 || session.TotalCacheReadTokens != 5 {
		t.Fatalf("unexpected cache totals: create=%d read=%d", session.TotalCacheCreationTokens, session.TotalCacheReadTokens)
	}
	if session.TotalCostUSD != 1.25 {
		t.Fatalf("unexpected cost: %f", session.TotalCostUSD)
	}
	if session.CWD != "/tmp/agmon" || session.GitBranch != "feature/test" {
		t.Fatalf("unexpected meta: cwd=%q branch=%q", session.CWD, session.GitBranch)
	}
}

func TestProcessEventToolCallEndInsertsFileChange(t *testing.T) {
	d, db := testDaemon(t)
	now := time.Now().UTC()

	if err := db.UpsertSession("s-file", event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.InsertToolCallStart("call-file", "agent-1", "s-file", "Edit", "{}", now); err != nil {
		t.Fatalf("insert tool call: %v", err)
	}

	err := d.processEvent(event.Event{
		ID:        "call-file",
		Type:      event.EventToolCallEnd,
		SessionID: "s-file",
		AgentID:   "agent-1",
		Platform:  event.PlatformClaude,
		Timestamp: now.Add(2 * time.Second),
		Data: event.EventData{
			ToolResult: "ok",
			ToolStatus: event.StatusSuccess,
			FilePath:   "internal/daemon/daemon.go",
			ChangeType: event.FileEdit,
		},
	})
	if err != nil {
		t.Fatalf("process event: %v", err)
	}

	changes, err := db.ListFileChanges("s-file")
	if err != nil {
		t.Fatalf("list file changes: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("expected 1 file change, got %d", len(changes))
	}
	if changes[0].FilePath != "internal/daemon/daemon.go" {
		t.Fatalf("unexpected file path: %q", changes[0].FilePath)
	}
	if changes[0].ChangeType != string(event.FileEdit) {
		t.Fatalf("unexpected change type: %q", changes[0].ChangeType)
	}
}
