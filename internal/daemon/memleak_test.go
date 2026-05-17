package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/testutil"
)

// startMemLeakDaemon creates a daemon using a short /tmp-based socket path
// to stay within the 104-char macOS Unix socket limit.
func startMemLeakDaemon(t *testing.T) *Daemon {
	t.Helper()
	db, dbCleanup := openLeakTestDB(t)
	t.Cleanup(dbCleanup)

	sockDir, err := os.MkdirTemp("", "tm-ml-")
	if err != nil {
		t.Fatalf("socket dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })

	d := New(db, filepath.Join(sockDir, "d.sock"))
	if err := d.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	return d
}

// TestDaemonBroadcastNoMemLeak verifies that repeatedly broadcasting an event
// to a subscribed channel does not accumulate retained heap memory.
func TestDaemonBroadcastNoMemLeak(t *testing.T) {
	d := startMemLeakDaemon(t)
	defer d.Stop()

	ch := d.Subscribe()
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		for range ch {
		}
	}()
	defer func() {
		d.Unsubscribe(ch)
		close(ch)
		<-drainDone
	}()

	now := time.Now()
	ev := event.Event{
		ID:        "bcast-memlk",
		Type:      event.EventToolCallStart,
		SessionID: "bcast-session",
		AgentID:   "agent-1",
		Platform:  event.PlatformClaude,
		Timestamp: now,
		Data:      event.EventData{ToolName: "Read"},
	}

	testutil.MemLeakCheck(t, func() {
		d.broadcast(ev)
	}, testutil.MemLeakOpts{Rounds: 500})
}

// TestDaemonHandleEventNoMemLeak verifies that processEvent does not
// accumulate retained heap across repeated event-processing cycles.
func TestDaemonHandleEventNoMemLeak(t *testing.T) {
	d := startMemLeakDaemon(t)
	defer d.Stop()

	now := time.Now()
	sessionID := "memlk-session"

	_ = d.processEvent(event.Event{
		ID: "memlk-start", Type: event.EventSessionStart,
		SessionID: sessionID, Platform: event.PlatformClaude, Timestamp: now,
	})

	var seq int
	testutil.MemLeakCheck(t, func() {
		seq++
		id := "memlk-tool-" + string(rune('a'+seq%26))
		_ = d.processEvent(event.Event{
			ID: id, Type: event.EventToolCallStart,
			SessionID: sessionID, AgentID: "agent-1",
			Platform:  event.PlatformClaude,
			Timestamp: now.Add(time.Duration(seq) * time.Millisecond),
			Data:      event.EventData{ToolName: "Write"},
		})
		_ = d.processEvent(event.Event{
			ID: id + "-end", Type: event.EventToolCallEnd,
			SessionID: sessionID, AgentID: "agent-1",
			Platform:  event.PlatformClaude,
			Timestamp: now.Add(time.Duration(seq)*time.Millisecond + 1),
		})
	}, testutil.MemLeakOpts{Rounds: 100})
}
