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
