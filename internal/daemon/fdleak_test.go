package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/testutil"
)

func TestDaemonStartStopNoFDLeak(t *testing.T) {
	db, dbCleanup := openLeakTestDB(t)
	// FD snapshot taken after DB open so DB's FDs are in "initial".
	defer testutil.FDLeakCheck(t)()
	defer dbCleanup()

	sockDir, err := os.MkdirTemp("", "tm-fdlk-sock-")
	if err != nil {
		t.Fatalf("socket dir: %v", err)
	}
	defer os.RemoveAll(sockDir)

	d := New(db, filepath.Join(sockDir, "d.sock"))
	if err := d.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	d.Stop()
	// All socket listeners and lock file FDs must be released by Stop().
}

func TestDaemonHandle100EventsNoFDLeak(t *testing.T) {
	db, dbCleanup := openLeakTestDB(t)
	defer testutil.FDLeakCheck(t)()
	defer dbCleanup()

	sockDir, err := os.MkdirTemp("", "tm-fdlk-100ev-")
	if err != nil {
		t.Fatalf("socket dir: %v", err)
	}
	defer os.RemoveAll(sockDir)

	d := New(db, filepath.Join(sockDir, "d.sock"))
	if err := d.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	now := time.Now()
	sessionID := "fdlk-session"
	agentID := "fdlk-agent"

	_ = d.processEvent(event.Event{
		ID: "fdlk-start", Type: event.EventSessionStart,
		SessionID: sessionID, Platform: event.PlatformClaude, Timestamp: now,
	})
	for i := range 100 {
		id := filepath.Join("fdlk-tool", filepath.Base(filepath.Dir(sockDir)), string(rune('a'+i%26)))
		_ = d.processEvent(event.Event{
			ID: id, Type: event.EventToolCallStart,
			SessionID: sessionID, AgentID: agentID,
			Platform:  event.PlatformClaude,
			Timestamp: now.Add(time.Duration(i) * time.Millisecond),
			Data:      event.EventData{ToolName: "Read"},
		})
		_ = d.processEvent(event.Event{
			ID: id + "-end", Type: event.EventToolCallEnd,
			SessionID: sessionID, AgentID: agentID,
			Platform:  event.PlatformClaude,
			Timestamp: now.Add(time.Duration(i)*time.Millisecond + 1),
			Data:      event.EventData{ToolName: "Read"},
		})
	}
	_ = d.processEvent(event.Event{
		ID: "fdlk-end", Type: event.EventSessionEnd,
		SessionID: sessionID, Platform: event.PlatformClaude, Timestamp: now.Add(time.Second),
	})

	d.Stop()
	// No file descriptor should remain open from event processing.
}
