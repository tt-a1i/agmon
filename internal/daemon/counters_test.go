package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/storage"
)

// TestSlowSubscriberDropIsCounted verifies droppedBroadcasts increments when a
// subscriber's buffer is full so we can surface slow-consumer pressure later.
func TestSlowSubscriberDropIsCounted(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	d := New(db, filepath.Join(dir, "drop.sock"))
	// We do NOT call Start — we exercise broadcast directly to keep the test
	// hermetic.

	// Subscribe + don't drain → channel fills at 256 and subsequent sends drop.
	_ = d.Subscribe()

	ev := event.Event{
		ID:        "x",
		Type:      event.EventTokenUsage,
		SessionID: "s1",
		Platform:  event.PlatformClaude,
		Timestamp: time.Now().UTC(),
	}
	for i := 0; i < 300; i++ { // 300 > buffer (256)
		d.broadcast(ev)
	}

	got, _, _ := d.Stats()
	if got == 0 {
		t.Fatalf("expected at least one dropped broadcast, got 0")
	}
	if got < 40 {
		t.Errorf("expected most overflow events dropped (~44), got %d", got)
	}
}

// TestDuplicateToolStartIsCounted verifies the daemon increments
// duplicateToolStarts when a Pre-emit re-fires for the same call_id. Used to
// measure how often this happens before deciding whether to switch to
// ON CONFLICT DO UPDATE semantics.
func TestDuplicateToolStartIsCounted(t *testing.T) {
	d, _ := testDaemon(t)
	now := time.Now().UTC()

	first := event.Event{
		ID:        "tc-dup",
		Type:      event.EventToolCallStart,
		SessionID: "s1",
		AgentID:   "a1",
		Platform:  event.PlatformClaude,
		Timestamp: now,
		Data:      event.EventData{ToolName: "Edit", ToolParams: "v1"},
	}
	if err := d.processEvent(first); err != nil {
		t.Fatalf("first emit: %v", err)
	}

	// Same call_id re-emitted — must be counted but not error out.
	dup := first
	dup.Data.ToolParams = "v2"
	if err := d.processEvent(dup); err != nil {
		t.Fatalf("duplicate emit: %v", err)
	}

	_, _, dups := d.Stats()
	if dups != 1 {
		t.Errorf("duplicateToolStarts = %d, want 1", dups)
	}
}

// TestDaemonStopWaitsForStaleSweep verifies Stop blocks until the
// staleSweepLoop goroutine returns (bgWG.Wait) so the loop never outlives the
// db handle. A regression here would let DB close before sweep completes,
// surfacing as a spurious "use of closed database" log on shutdown.
func TestDaemonStopWaitsForStaleSweep(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "sweep.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	d := New(db, filepath.Join(os.TempDir(),
		"tokenmeter-sweep-"+time.Now().Format("150405.999")+".sock"))
	if err := d.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	done := make(chan struct{})
	go func() {
		d.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop blocked on staleSweepLoop or other bg goroutine")
	}
}

// TestRemoteSubscriberCleanupOnSlowConsumer verifies that when a subscriber
// stalls past the 200ms write deadline, the broadcast path detaches them
// (calls removeRemoteSub) so the remoteSubs map doesn't leak entries.
// Race detector should catch any unsynchronized map access between
// broadcast (reads under RLock then writes outside) and addRemoteSub.
func TestRemoteSubscriberCleanupOnSlowConsumer(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "rs.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	sockPath := filepath.Join(os.TempDir(),
		"tokenmeter-rs-"+time.Now().Format("150405.999")+".sock")
	t.Cleanup(func() {
		_ = os.Remove(sockPath)
		_ = os.Remove(subscriberSocketPath(sockPath))
	})

	d := New(db, sockPath)
	if err := d.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer d.Stop()

	// Connect a remote subscriber but DON'T read; the writeRemoteEvent
	// deadline (200ms) will expire and trigger removeRemoteSub.
	subSockPath := subscriberSocketPath(sockPath)
	conn, err := dialSocket(subSockPath)
	if err != nil {
		t.Fatalf("dial subscriber: %v", err)
	}
	defer conn.Close()

	// Wait for the daemon to register the connection.
	waitForRemoteSubscribers(t, d, 1)

	// Spam events so the writer fills the socket buffer and hits the
	// 200ms write deadline.
	//
	// The payload is intentionally large because unix-socket sndbuf
	// defaults differ by platform: macOS is ~8 KB and a handful of
	// ~150-byte events overflow it, but Linux's default is ~208 KB
	// (sysctl net.core.wmem_default). Without padding, 200 events
	// fit comfortably in the Linux buffer, every write returns before
	// its 200ms deadline, removeRemoteSub never fires, and the test
	// fails with "slow remote subscriber not cleaned up" on Linux CI.
	// 200 events × ~4.5 KB = ~900 KB guarantees overflow on both.
	ev := event.Event{
		ID:        "spam",
		Type:      event.EventTokenUsage,
		SessionID: "s",
		Platform:  event.PlatformClaude,
		Timestamp: time.Now().UTC(),
		Data:      event.EventData{ToolParams: strings.Repeat("x", 4096)},
	}
	for i := 0; i < 200; i++ {
		d.broadcast(ev)
	}

	// After enough broadcasts, the slow subscriber should be detached.
	// Poll with a short timeout — race detector catches bad sync regardless.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		d.mu.RLock()
		n := len(d.remoteSubs)
		d.mu.RUnlock()
		if n == 0 {
			return // success
		}
		time.Sleep(50 * time.Millisecond)
	}
	d.mu.RLock()
	n := len(d.remoteSubs)
	d.mu.RUnlock()
	if n != 0 {
		t.Errorf("slow remote subscriber not cleaned up; remoteSubs len=%d", n)
	}
}

// TestStartRefusesIfSocketAlreadyLive verifies Start returns os.ErrExist when
// another daemon is listening on the same socket, and that listener / DB
// resources of the failed start are cleaned up.
func TestStartRefusesIfSocketAlreadyLive(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(os.TempDir(),
		"tokenmeter-start-err-"+time.Now().Format("150405.999")+".sock")
	t.Cleanup(func() {
		_ = os.Remove(sockPath)
		_ = os.Remove(subscriberSocketPath(sockPath))
	})

	db1, err := storage.Open(filepath.Join(dir, "first.db"))
	if err != nil {
		t.Fatalf("open db1: %v", err)
	}
	t.Cleanup(func() { _ = db1.Close() })

	d1 := New(db1, sockPath)
	if err := d1.Start(); err != nil {
		t.Fatalf("first start: %v", err)
	}
	t.Cleanup(d1.Stop)

	// Second daemon attempt on the same socket should fail without leaking
	// the lock or breaking the live first daemon.
	db2, err := storage.Open(filepath.Join(dir, "second.db"))
	if err != nil {
		t.Fatalf("open db2: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close() })

	d2 := New(db2, sockPath)
	err = d2.Start()
	if err == nil {
		d2.Stop()
		t.Fatal("expected second Start to fail with live socket")
	}
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected ErrExist, got %v", err)
	}

	// First daemon must still be reachable — confirming the second Start's
	// failure didn't tear down the existing listener.
	conn, err := dialSocket(sockPath)
	if err != nil {
		t.Fatalf("first daemon socket should remain live: %v", err)
	}
	conn.Close()
}

// TestBatchConsumerDrainsPendingOnShutdown verifies events still in batchCh
// when Stop is called get processed before the consumer exits. Otherwise
// final-tick watcher emits would be silently lost.
func TestBatchConsumerDrainsPendingOnShutdown(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "drain.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	d := New(db, filepath.Join(os.TempDir(),
		"tokenmeter-drain-"+time.Now().Format("150405.999")+".sock"))
	if err := d.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Queue a few events via the async path, then immediately Stop.
	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		d.ProcessExternalEventAsync(event.Event{
			ID:        "ses-" + time.Now().Format("150405.999"),
			Type:      event.EventSessionStart,
			SessionID: "drain-sess",
			Platform:  event.PlatformClaude,
			Timestamp: now,
		})
	}
	d.Stop()

	// After Stop returns, the drain path must have processed enough that
	// the session row exists in the DB.
	s, found, err := db.GetSessionByIDPrefix("drain-sess")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if !found || s.SessionID != "drain-sess" {
		t.Errorf("expected drain-sess to be persisted before Stop returns, found=%v", found)
	}
}

// TestSubscribeUnsubscribeRoundtrip exercises the local subscriber lifecycle:
// Subscribe returns a channel that receives broadcasts; Unsubscribe removes
// it from the broadcast set so subsequent events don't enqueue.
func TestSubscribeUnsubscribeRoundtrip(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "sub.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	d := New(db, filepath.Join(dir, "sub.sock"))
	ch := d.Subscribe()

	ev := event.Event{
		ID:        "u1",
		Type:      event.EventSessionStart,
		SessionID: "sess",
		Platform:  event.PlatformClaude,
		Timestamp: time.Now().UTC(),
	}
	d.broadcast(ev)
	select {
	case got := <-ch:
		if got.ID != "u1" {
			t.Errorf("got %q, want %q", got.ID, "u1")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("subscriber didn't receive broadcast within 500ms")
	}

	// After Unsubscribe, broadcasts must not enqueue to ch.
	d.Unsubscribe(ch)
	d.broadcast(ev)
	select {
	case got := <-ch:
		t.Errorf("subscriber should be detached, but got %v", got)
	case <-time.After(50 * time.Millisecond):
		// good
	}
}

// TestUnsubscribeUnknownChannelIsNoOp ensures calling Unsubscribe on a
// channel that was never registered (or already removed) doesn't panic.
func TestUnsubscribeUnknownChannelIsNoOp(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "unsub.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	d := New(db, filepath.Join(dir, "unsub.sock"))
	stranger := make(chan event.Event)
	d.Unsubscribe(stranger) // must not panic or hang
}

// TestRepairEmptyTokenModelsBackfillsCodexRows verifies the daemon startup
// repair path: codex token_usage rows with empty model get back-filled from
// the session's known model.
func TestRepairEmptyTokenModelsBackfillsCodexRows(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "rep.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	now := time.Now().UTC()
	if err := db.UpsertSession("s-codex", event.PlatformCodex, now); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Insert a row with a known model so the session anchors to "gpt-5".
	if err := db.InsertTokenUsage("a", "s-codex", 100, 50, 0, 0, "gpt-5", 0.1, now, "codex-src-known"); err != nil {
		t.Fatalf("insert known: %v", err)
	}
	// Insert a second row with EMPTY model — this is what repairEmptyTokenModels targets.
	if err := db.InsertTokenUsage("a", "s-codex", 200, 100, 0, 0, "", 0, now.Add(time.Second), "codex-src-empty"); err != nil {
		t.Fatalf("insert empty: %v", err)
	}

	// Daemon doesn't need full Start — exercise the repair helper directly.
	d := New(db, filepath.Join(dir, "rep.sock"))
	d.repairEmptyTokenModels()

	// Verify the empty-model row's model was repaired. Use GetSessionByIDPrefix
	// indirectly: ListEmptyModelSessions should now report 0 sessions because
	// every codex row has a model.
	remaining, err := db.ListEmptyModelSessions()
	if err != nil {
		t.Fatalf("list empty after repair: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("repair didn't clear empty-model sessions: %+v", remaining)
	}
}

func TestShouldRepairRecentCodexTokenModel(t *testing.T) {
	// Zero time → never repair.
	if shouldRepairRecentCodexTokenModel(time.Time{}) {
		t.Error("zero time should return false")
	}
	// Recent (within 2h) → repair.
	recent := time.Now().Add(-30 * time.Minute)
	if !shouldRepairRecentCodexTokenModel(recent) {
		t.Error("30min-old context should be repaired")
	}
	// Stale (>2h) → skip.
	old := time.Now().Add(-3 * time.Hour)
	if shouldRepairRecentCodexTokenModel(old) {
		t.Error("3h-old context should NOT be repaired")
	}
}

func TestDefaultSocketPathProducesPath(t *testing.T) {
	got := DefaultSocketPath()
	if got == "" {
		t.Error("DefaultSocketPath should return non-empty path")
	}
}

// TestStatsZeroBeforeAnyDrop verifies the counters start at 0.
func TestStatsZeroBeforeAnyDrop(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	d := New(db, filepath.Join(dir, "stats.sock"))
	got1, got2, got3 := d.Stats()
	if got1 != 0 || got2 != 0 || got3 != 0 {
		t.Errorf("Stats on fresh daemon = (%d, %d, %d), want (0, 0, 0)", got1, got2, got3)
	}
}
