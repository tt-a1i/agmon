package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/storage"
	"github.com/tt-a1i/tokenmeter/internal/testutil"
)

// openResDB opens a storage.DB in a temp dir and registers cleanup.
func openResDB(t *testing.T) *storage.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "res.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// shortSockPath returns a socket path under /tmp with a short name to stay
// within the 104-character macOS Unix socket limit.
func shortSockPath(name string) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("tm-res-%s-%d.sock", name, time.Now().UnixNano()%100000))
}

// TestDaemonSubscriberPanicDoesNotKillDaemon verifies that a panic inside a
// subscriber goroutine does not crash the daemon or block other subscribers.
//
// The daemon's broadcast is a non-blocking send — it never calls into
// subscriber code. Subscriber goroutines are caller-managed; their panics are
// isolated to the caller's stack. This test confirms that invariant.
func TestDaemonSubscriberPanicDoesNotKillDaemon(t *testing.T) {
	d, _ := testDaemon(t)
	// broadcast/subscribe work without socket listeners; no Start() needed.

	chA := d.Subscribe()
	chB := d.Subscribe()

	// B drains normally, counts events.
	var bCount atomic.Int64
	bStopped := make(chan struct{})
	go func() {
		defer close(bStopped)
		for range chB {
			bCount.Add(1)
		}
	}()

	// A panics on its first event; goroutine exits, channel fills silently.
	var aPanicked atomic.Bool
	aStopped := make(chan struct{})
	go func() {
		defer close(aStopped)
		defer func() {
			if r := recover(); r != nil {
				aPanicked.Store(true)
			}
		}()
		for range chA {
			panic("simulated subscriber panic")
		}
	}()

	const total = 50
	for i := range total {
		d.broadcast(event.Event{
			ID:        fmt.Sprintf("res-%d", i),
			Type:      event.EventToolCallStart,
			SessionID: "res-session",
			Platform:  event.PlatformClaude,
			Timestamp: time.Now(),
		})
		time.Sleep(time.Millisecond)
	}

	testutil.Eventually(t, func() bool {
		return aPanicked.Load() && bCount.Load() >= total
	}, 2*time.Second, 20*time.Millisecond, "wait for A panic and B to receive all events")

	if !aPanicked.Load() {
		t.Error("subscriber A should have panicked — test setup issue")
	}
	if got := bCount.Load(); got < total {
		t.Errorf("subscriber B got %d/%d events; A panic should not affect B", got, total)
	}

	// Daemon is still healthy: subscribe C, broadcast, verify delivery.
	chC := d.Subscribe()
	recv := make(chan string, 1)
	go func() {
		ev := <-chC
		recv <- ev.SessionID
	}()

	d.broadcast(event.Event{
		ID:        "health-check",
		Type:      event.EventSessionStart,
		SessionID: "after-panic",
		Platform:  event.PlatformClaude,
		Timestamp: time.Now(),
	})

	select {
	case sid := <-recv:
		if sid != "after-panic" {
			t.Errorf("expected after-panic session, got %q", sid)
		}
	case <-time.After(time.Second):
		t.Fatal("daemon unresponsive after subscriber A panicked")
	}

	// Cleanup.
	d.Unsubscribe(chA)
	d.Unsubscribe(chB)
	d.Unsubscribe(chC)
	close(chB)
	<-bStopped
	<-aStopped
}

// TestDaemonBroadcastDoesNotPanicOnClosedChannel confirms that the daemon's
// broadcast is resilient to callers that close a subscriber channel without
// first calling Unsubscribe (a contract violation). The trySendEvent helper
// recovers from the resulting "send on closed channel" panic so the daemon
// continues running rather than crashing.
func TestDaemonBroadcastDoesNotPanicOnClosedChannel(t *testing.T) {
	d, _ := testDaemon(t)

	ch := d.Subscribe()
	close(ch) // contract violation — simulates a buggy caller

	panicked := false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		d.broadcast(event.Event{
			Type:      event.EventToolCallStart,
			SessionID: "closed-chan-test",
			Platform:  event.PlatformClaude,
			Timestamp: time.Now(),
		})
	}()

	if panicked {
		t.Error("daemon.broadcast panicked on closed subscriber channel; " +
			"trySendEvent recover() in daemon.go should prevent this crash")
	}

	dropped, _, _ := d.Stats()
	if dropped == 0 {
		t.Error("droppedBroadcasts should be > 0 after a closed-channel drop")
	}

	d.Unsubscribe(ch)
}

// TestDaemonStopWhileSubscriberBlocked verifies that Stop() completes within a
// short deadline even when a subscriber channel is completely full and never
// drained. The non-blocking broadcast (drops when channel full) ensures daemon
// shutdown is never gated on a slow or unresponsive subscriber.
func TestDaemonStopWhileSubscriberBlocked(t *testing.T) {
	db := openResDB(t)
	sockPath := shortSockPath("stop")
	defer func() {
		_ = os.Remove(sockPath)
		_ = os.Remove(subscriberSocketPath(sockPath))
	}()

	d := New(db, sockPath)
	if err := d.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	ch := d.Subscribe()
	_ = ch // intentionally never read — buffer fills, broadcasts drop

	// Saturate the subscriber's buffer.
	for range 300 {
		d.broadcast(event.Event{
			Type:      event.EventTokenUsage,
			SessionID: "blocked",
			Platform:  event.PlatformClaude,
			Timestamp: time.Now(),
		})
	}

	stopped := make(chan struct{})
	go func() {
		d.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
		// Good: Stop returned without waiting for the blocked subscriber.
	case <-time.After(3 * time.Second):
		t.Fatal("Stop() blocked waiting for a subscriber that never drains its channel")
	}
}
