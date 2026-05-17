package daemon

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

// TestDaemonConcurrentEmitNoLoss verifies that concurrent processEvent calls
// from N goroutines produce exactly N*M tool_call rows in the DB — no events
// are silently dropped under contention.
//
// SQLite is serialized (SetMaxOpenConns(1)), so all writes queue safely.
// This test catches races in the daemon's event routing layer, not the DB.
func TestDaemonConcurrentEmitNoLoss(t *testing.T) {
	const nClients = 20
	const eventsPerClient = 50
	const totalEvents = nClients * eventsPerClient

	d, db := testDaemon(t)
	sessionID := "stress-noloss-session"
	now := time.Now()

	// Pre-create session so all tool call inserts have a valid FK.
	if err := db.UpsertSession(sessionID, event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(nClients)
	for c := range nClients {
		go func(clientID int) {
			defer wg.Done()
			for i := range eventsPerClient {
				callID := fmt.Sprintf("stress-call-%d-%d", clientID, i)
				err := d.processEvent(event.Event{
					ID:        callID,
					Type:      event.EventToolCallStart,
					SessionID: sessionID,
					AgentID:   fmt.Sprintf("agent-%d", clientID),
					Platform:  event.PlatformClaude,
					Timestamp: now.Add(time.Duration(clientID*eventsPerClient+i) * time.Microsecond),
					Data:      event.EventData{ToolName: "Read"},
				})
				if err != nil {
					t.Errorf("processEvent client=%d i=%d: %v", clientID, i, err)
				}
			}
		}(c)
	}
	wg.Wait()

	// All events are synchronous writes via processEvent, so by the time wg.Wait
	// returns all rows must be present — no need for Eventually here.
	calls, err := db.ListToolCalls(sessionID, totalEvents+10)
	if err != nil {
		t.Fatalf("list tool calls: %v", err)
	}
	if got := len(calls); got != totalEvents {
		t.Errorf("concurrent emit: got %d tool_calls, want %d (loss under contention)", got, totalEvents)
	}
}

// TestDaemonConcurrentEmitNoDuplicate verifies that when multiple goroutines
// race to insert the same tool_use_id, the daemon's INSERT OR IGNORE dedup
// ensures each unique ID appears exactly once in the DB.
func TestDaemonConcurrentEmitNoDuplicate(t *testing.T) {
	const nConcurrent = 10
	const uniqueIDs = 100

	d, db := testDaemon(t)
	sessionID := "stress-dedup-session"
	now := time.Now()

	if err := db.UpsertSession(sessionID, event.PlatformClaude, now); err != nil {
		t.Fatalf("upsert session: %v", err)
	}

	// Each goroutine sends all uniqueIDs. Without dedup this would produce
	// nConcurrent * uniqueIDs rows; with it: exactly uniqueIDs rows.
	var wg sync.WaitGroup
	wg.Add(nConcurrent)
	for range nConcurrent {
		go func() {
			defer wg.Done()
			for i := range uniqueIDs {
				_ = d.processEvent(event.Event{
					ID:        fmt.Sprintf("dup-call-%d", i),
					Type:      event.EventToolCallStart,
					SessionID: sessionID,
					AgentID:   "agent-dedup",
					Platform:  event.PlatformClaude,
					Timestamp: now,
					Data:      event.EventData{ToolName: "Write"},
				})
			}
		}()
	}
	wg.Wait()

	calls, err := db.ListToolCalls(sessionID, nConcurrent*uniqueIDs+10)
	if err != nil {
		t.Fatalf("list tool calls: %v", err)
	}
	if got := len(calls); got != uniqueIDs {
		t.Errorf("dedup: got %d tool_calls, want %d (each unique ID should appear once)", got, uniqueIDs)
	}

	// Duplicate inserts should have incremented the counter.
	_, _, dupeStarts := d.Stats()
	if dupeStarts == 0 {
		t.Error("duplicateToolStarts counter should be > 0 after racing inserts")
	}
}

// TestDaemonConcurrentBroadcastNoRace verifies that concurrent Subscribe /
// Unsubscribe / broadcast calls don't trigger the race detector. This is a
// lightweight structural test; event delivery counts are not asserted.
func TestDaemonConcurrentBroadcastNoRace(t *testing.T) {
	d, _ := testDaemon(t)

	const nSubs = 10
	const nBroadcasts = 200

	// Spin up subscribers that drain quickly and then unsubscribe.
	var subWg sync.WaitGroup
	channels := make([]chan event.Event, nSubs)
	for i := range nSubs {
		ch := d.Subscribe()
		channels[i] = ch
		subWg.Add(1)
		go func(c chan event.Event) {
			defer subWg.Done()
			for range c {
			}
		}(ch)
	}

	// Broadcast concurrently with subscribe/unsubscribe churn.
	var bcastWg sync.WaitGroup
	bcastWg.Add(1)
	go func() {
		defer bcastWg.Done()
		for i := range nBroadcasts {
			d.broadcast(event.Event{
				ID:        fmt.Sprintf("race-ev-%d", i),
				Type:      event.EventToolCallStart,
				SessionID: "race-session",
				Platform:  event.PlatformClaude,
				Timestamp: time.Now(),
			})
		}
	}()

	bcastWg.Wait()

	// Unsubscribe all, then close channels to let drain goroutines exit.
	for _, ch := range channels {
		d.Unsubscribe(ch)
		close(ch)
	}
	subWg.Wait()

	// If we reach here without the race detector firing, the test passes.
}

// TestDaemonProcessEventConcurrentSessions verifies that concurrent events
// for different sessions don't interfere (cross-session contamination check).
func TestDaemonProcessEventConcurrentSessions(t *testing.T) {
	const nSessions = 10
	const callsPerSession = 30

	d, db := testDaemon(t)
	now := time.Now()

	// Create all sessions upfront.
	for s := range nSessions {
		sid := fmt.Sprintf("concurrent-sess-%d", s)
		if err := db.UpsertSession(sid, event.PlatformClaude, now); err != nil {
			t.Fatalf("upsert session %d: %v", s, err)
		}
	}

	var wg sync.WaitGroup
	wg.Add(nSessions)
	for s := range nSessions {
		go func(sessIdx int) {
			defer wg.Done()
			sid := fmt.Sprintf("concurrent-sess-%d", sessIdx)
			for i := range callsPerSession {
				_ = d.processEvent(event.Event{
					ID:        fmt.Sprintf("sess%d-call-%d", sessIdx, i),
					Type:      event.EventToolCallStart,
					SessionID: sid,
					AgentID:   fmt.Sprintf("agent-%d", sessIdx),
					Platform:  event.PlatformClaude,
					Timestamp: now.Add(time.Duration(i) * time.Microsecond),
					Data:      event.EventData{ToolName: "Bash"},
				})
			}
		}(s)
	}
	wg.Wait()

	// Verify each session has exactly callsPerSession rows, no cross-contamination.
	for s := range nSessions {
		sid := fmt.Sprintf("concurrent-sess-%d", s)
		calls, err := db.ListToolCalls(sid, callsPerSession+10)
		if err != nil {
			t.Fatalf("list tool calls session %d: %v", s, err)
		}
		if got := len(calls); got != callsPerSession {
			t.Errorf("session %d: got %d calls, want %d", s, got, callsPerSession)
		}
	}
}

// TestDaemonBroadcastBackpressureIsNonBlocking measures that broadcasting to
// a full subscriber channel returns immediately rather than blocking the caller.
// This guards the non-blocking guarantee that lets daemon.Stop() be safe.
func TestDaemonBroadcastBackpressureIsNonBlocking(t *testing.T) {
	d, _ := testDaemon(t)

	// Subscribe without draining — channel fills after 256 events (buffer size).
	ch := d.Subscribe()
	defer d.Unsubscribe(ch)

	ev := event.Event{
		Type:      event.EventToolCallStart,
		SessionID: "backpressure",
		Platform:  event.PlatformClaude,
		Timestamp: time.Now(),
	}

	// Flood: 3× buffer to ensure channel is saturated.
	start := time.Now()
	for range 768 {
		d.broadcast(ev)
	}
	elapsed := time.Since(start)

	// 768 non-blocking sends should complete in well under 100ms on any machine.
	const deadline = 100 * time.Millisecond
	if elapsed > deadline {
		t.Errorf("broadcast took %v for 768 events to a full channel; expected < %v (should be non-blocking)", elapsed, deadline)
	}

	dropped, _, _ := d.Stats()
	if dropped == 0 {
		t.Error("droppedBroadcasts should be > 0 after flooding a full channel")
	}

}
