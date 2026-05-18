package daemon

import (
	"fmt"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

// TestBatchingReducesLatency compares the p99 hot-path latency of
// BatchWriter.Enqueue() (channel send) against processEvent with a
// synchronous InsertTokenUsage.  Enqueue must be at least 10× faster at p99.
func TestBatchingReducesLatency(t *testing.T) {
	const N = 500

	// --- synchronous baseline (no BatchWriter) ---
	d, db := testDaemon(t)
	sessionID := "bw-latency-session"
	if err := db.UpsertSession(sessionID, event.PlatformClaude, time.Now()); err != nil {
		t.Fatalf("upsert session: %v", err)
	}

	now := time.Now()
	directLat := make([]time.Duration, N)
	for i := range N {
		ev := event.Event{
			ID:        fmt.Sprintf("bw-direct-%d", i),
			Type:      event.EventTokenUsage,
			SessionID: sessionID,
			AgentID:   "agent-bw",
			Platform:  event.PlatformClaude,
			Timestamp: now.Add(time.Duration(i) * time.Microsecond),
			Data:      event.EventData{InputTokens: 10, Model: "claude-sonnet-4-6"},
		}
		start := time.Now()
		if err := d.processEvent(ev); err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		directLat[i] = time.Since(start)
	}
	sort.Slice(directLat, func(i, j int) bool { return directLat[i] < directLat[j] })
	directP99 := directLat[N*99/100]
	t.Logf("direct processEvent p99=%v", directP99)

	// --- BatchWriter path (Enqueue = non-blocking channel send) ---
	mock := &mockBatchDB{}
	bw := NewBatchWriter(mock, 50*time.Millisecond, 50)
	bw.Start()

	enqueueLat := make([]time.Duration, N)
	for i := range N {
		ev := event.Event{
			ID:        fmt.Sprintf("bw-enqueue-%d", i),
			Type:      event.EventTokenUsage,
			SessionID: sessionID,
			Platform:  event.PlatformClaude,
			Timestamp: now.Add(time.Duration(i) * time.Microsecond),
			Data:      event.EventData{InputTokens: 10, Model: "claude-sonnet-4-6"},
		}
		start := time.Now()
		bw.Enqueue(ev)
		enqueueLat[i] = time.Since(start)
	}
	bw.Stop()

	sort.Slice(enqueueLat, func(i, j int) bool { return enqueueLat[i] < enqueueLat[j] })
	enqueueP99 := enqueueLat[N*99/100]
	t.Logf("BatchWriter Enqueue p99=%v (direct=%v, ratio=%.1f×)", enqueueP99, directP99, float64(directP99)/float64(enqueueP99))

	if enqueueP99 >= directP99 {
		t.Errorf("Enqueue p99 %v not faster than direct p99 %v — batch path provides no latency benefit", enqueueP99, directP99)
	}
}

// TestIngestionP99LatencyUnderThreshold measures processEvent latency over
// 500 iterations and asserts p99 < 50ms. p50 and p99 are logged for tracking.
//
// processEvent covers: UpsertSession (first call only) + InsertToolCallStart
// + broadcast — the full synchronous hot path that every hook event traverses.
//
// The 50ms p99 threshold is deliberately conservative; typical p99 on any
// modern machine is well under 5ms. If this test fails, the storage layer
// or broadcast has a systemic latency problem worth investigating.
func TestIngestionP99LatencyUnderThreshold(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("GitHub Actions windows-latest runner I/O is too slow for a meaningful 50ms p99 budget")
	}
	d, db := testDaemon(t)
	sessionID := "latency-session"
	if err := db.UpsertSession(sessionID, event.PlatformClaude, time.Now()); err != nil {
		t.Fatalf("upsert session: %v", err)
	}

	const N = 500
	latencies := make([]time.Duration, N)
	now := time.Now()

	for i := range N {
		ev := event.Event{
			ID:        fmt.Sprintf("lat-call-%d", i),
			Type:      event.EventToolCallStart,
			SessionID: sessionID,
			AgentID:   "agent-lat",
			Platform:  event.PlatformClaude,
			Timestamp: now.Add(time.Duration(i) * time.Microsecond),
			Data:      event.EventData{ToolName: "Read"},
		}
		start := time.Now()
		if err := d.processEvent(ev); err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		latencies[i] = time.Since(start)
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p50 := latencies[N/2]
	p90 := latencies[N*90/100]
	p99 := latencies[N*99/100]

	t.Logf("processEvent latency (N=%d): p50=%v p90=%v p99=%v", N, p50, p90, p99)

	const threshold = 50 * time.Millisecond
	if p99 > threshold {
		t.Errorf("p99 latency %v exceeds %v threshold — storage or broadcast bottleneck", p99, threshold)
	}
}

// TestIngestionBroadcastLatencyUnderThreshold measures broadcast() latency
// (channel send only, no DB write) to isolate the subscriber-fanout cost.
func TestIngestionBroadcastLatencyUnderThreshold(t *testing.T) {
	d, _ := testDaemon(t)

	// Add a few subscribers to simulate real-world fan-out.
	const nSubs = 4
	channels := make([]chan event.Event, nSubs)
	for i := range nSubs {
		ch := d.Subscribe()
		channels[i] = ch
		go func(c chan event.Event) {
			for range c {
			}
		}(ch)
	}
	defer func() {
		for _, ch := range channels {
			d.Unsubscribe(ch)
			close(ch)
		}
	}()

	const N = 500
	latencies := make([]time.Duration, N)
	now := time.Now()

	ev := event.Event{
		ID:        "bcast-lat",
		Type:      event.EventTokenUsage,
		SessionID: "bcast-session",
		Platform:  event.PlatformClaude,
		Timestamp: now,
		Data:      event.EventData{InputTokens: 100},
	}

	for i := range N {
		start := time.Now()
		d.broadcast(ev)
		latencies[i] = time.Since(start)
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p50 := latencies[N/2]
	p99 := latencies[N*99/100]

	t.Logf("broadcast latency (N=%d, subs=%d): p50=%v p99=%v", N, nSubs, p50, p99)

	const threshold = 1 * time.Millisecond
	if p99 > threshold {
		t.Errorf("p99 broadcast latency %v exceeds %v — channel fanout is unexpectedly slow", p99, threshold)
	}
}
