package daemon

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

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
