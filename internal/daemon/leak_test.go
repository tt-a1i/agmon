package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/storage"
	"github.com/tt-a1i/tokenmeter/internal/testutil"
)

// openLeakTestDB opens a SQLite DB in a short-lived temp dir.
// Returns db and a cleanup func. Caller should defer cleanup() BEFORE
// defer testutil.LeakCheck(t)() so that db.Close() fires first and
// the database/sql connectionOpener goroutine exits before the snapshot diff.
func openLeakTestDB(t *testing.T) (*storage.DB, func()) {
	t.Helper()
	sockDir, err := os.MkdirTemp("", "tm-lk-")
	if err != nil {
		t.Fatalf("mkdirtemp: %v", err)
	}
	db, err := storage.Open(filepath.Join(sockDir, "test.db"))
	if err != nil {
		_ = os.RemoveAll(sockDir)
		t.Fatalf("open db: %v", err)
	}
	return db, func() {
		_ = db.Close()
		_ = os.RemoveAll(sockDir)
	}
}

func TestDaemonStartStopNoLeak(t *testing.T) {
	// Register db cleanup BEFORE LeakCheck so defer LIFO fires db.Close first.
	db, dbCleanup := openLeakTestDB(t)
	defer dbCleanup()
	defer testutil.LeakCheck(t)()

	sockDir, err := os.MkdirTemp("", "tm-lk-sock-")
	if err != nil {
		t.Fatalf("socket dir: %v", err)
	}
	defer os.RemoveAll(sockDir)

	d := New(db, filepath.Join(sockDir, "d.sock"))
	if err := d.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	// Let background loops (sweepers, budget, webhook retry) initialise.
	time.Sleep(50 * time.Millisecond)
	d.Stop()
}

func TestDaemonSubscribeUnsubscribeNoLeak(t *testing.T) {
	db, dbCleanup := openLeakTestDB(t)
	defer dbCleanup()
	defer testutil.LeakCheck(t)()

	sockDir, err := os.MkdirTemp("", "tm-lk-sock-")
	if err != nil {
		t.Fatalf("socket dir: %v", err)
	}
	defer os.RemoveAll(sockDir)

	d := New(db, filepath.Join(sockDir, "d.sock"))
	if err := d.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	ch := d.Subscribe()
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		for range ch {
		}
	}()

	_ = d.processEvent(event.Event{
		ID:        "sub-session",
		Type:      event.EventSessionStart,
		SessionID: "sub-session",
		Platform:  event.PlatformClaude,
		Timestamp: time.Now(),
	})

	d.Unsubscribe(ch)
	d.Stop()

	// After Unsubscribe+Stop there are no concurrent senders, so it is safe to
	// close ch ourselves to unblock the drain goroutine.
	close(ch)
	select {
	case <-drainDone:
	case <-time.After(2 * time.Second):
		t.Fatal("drain goroutine did not exit after channel close")
	}
}

func TestDaemonProcessEventsNoLeak(t *testing.T) {
	db, dbCleanup := openLeakTestDB(t)
	defer dbCleanup()
	defer testutil.LeakCheck(t)()

	sockDir, err := os.MkdirTemp("", "tm-lk-sock-")
	if err != nil {
		t.Fatalf("socket dir: %v", err)
	}
	defer os.RemoveAll(sockDir)

	d := New(db, filepath.Join(sockDir, "d.sock"))
	if err := d.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	now := time.Now()
	events := []event.Event{
		{ID: "pe-start", Type: event.EventSessionStart, SessionID: "pe-session", Platform: event.PlatformClaude, Timestamp: now},
		{ID: "pe-tool", Type: event.EventToolCallStart, SessionID: "pe-session", AgentID: "a1", Platform: event.PlatformClaude, Timestamp: now, Data: event.EventData{ToolName: "Read"}},
		{ID: "pe-token", Type: event.EventTokenUsage, SessionID: "pe-session", AgentID: "a1", Platform: event.PlatformClaude, Timestamp: now, Data: event.EventData{InputTokens: 100, OutputTokens: 50, Model: "sonnet", CostUSD: 0.01}},
		{ID: "pe-end", Type: event.EventSessionEnd, SessionID: "pe-session", Platform: event.PlatformClaude, Timestamp: now},
	}
	for _, ev := range events {
		if err := d.processEvent(ev); err != nil {
			t.Errorf("processEvent %s: %v", ev.ID, err)
		}
	}

	d.Stop()
}
