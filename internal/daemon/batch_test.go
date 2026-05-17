package daemon

import (
	"fmt"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/testutil"
)

// makeTokEv builds a token_usage event for a given session.
func makeTokEv(sessionID string, seq int) event.Event {
	return event.Event{
		ID:        fmt.Sprintf("tok-%s-%d", sessionID, seq),
		Type:      event.EventTokenUsage,
		SessionID: sessionID,
		Platform:  event.PlatformClaude,
		Timestamp: time.Now(),
		Data:      event.EventData{InputTokens: seq * 10, OutputTokens: seq},
	}
}

// TestSSEBufferCoalescesBurstTokenUsage verifies that 100 token_usage events
// for the same session are coalesced to a single event per session within
// the flush window — at least 70% reduction in output event count.
func TestSSEBufferCoalescesBurstTokenUsage(t *testing.T) {
	buf := NewSSEBuffer(50 * time.Millisecond)
	buf.Start()
	defer buf.Stop()

	const nEvents = 100
	const session = "burst-session"

	for i := range nEvents {
		buf.Add(makeTokEv(session, i+1))
	}

	// Collect all output events within 200ms (2 flush windows + margin).
	var received []event.Event
	deadline := time.After(200 * time.Millisecond)
collect:
	for {
		select {
		case batch, ok := <-buf.Out():
			if !ok {
				break collect
			}
			received = append(received, batch...)
		case <-deadline:
			break collect
		}
	}

	// 100 token_usage events for one session → at most a handful of flush
	// windows, so output count should be much less than 100.
	if got := len(received); got >= 30 {
		t.Errorf("coalescing ineffective: got %d output events for %d inputs; expected < 30", got, nEvents)
	}
	if len(received) == 0 {
		t.Error("no events flushed; expected at least 1 coalesced token_usage")
	}

	// The last flushed token_usage must carry the latest InputTokens value.
	for _, ev := range received {
		if ev.Type == event.EventTokenUsage && ev.SessionID == session {
			if ev.Data.InputTokens == 0 {
				t.Error("coalesced token_usage has zero InputTokens; latest snapshot not preserved")
			}
		}
	}
}

// TestSSEBufferPreservesNonCoalesceableEvents verifies that tool_call_start
// events are never dropped, even when mixed with burst token_usage events.
func TestSSEBufferPreservesNonCoalesceableEvents(t *testing.T) {
	buf := NewSSEBuffer(30 * time.Millisecond)
	buf.Start()
	defer buf.Stop()

	const nToolCalls = 10
	for i := range nToolCalls {
		buf.Add(event.Event{
			ID:        fmt.Sprintf("call-%d", i),
			Type:      event.EventToolCallStart,
			SessionID: "tc-session",
			Platform:  event.PlatformClaude,
			Timestamp: time.Now(),
			Data:      event.EventData{ToolName: "Read"},
		})
		// Interleave token_usage bursts between tool calls.
		for range 5 {
			buf.Add(makeTokEv("tc-session", i))
		}
	}

	var received []event.Event
	deadline := time.After(200 * time.Millisecond)
collect:
	for {
		select {
		case batch, ok := <-buf.Out():
			if !ok {
				break collect
			}
			received = append(received, batch...)
		case <-deadline:
			break collect
		}
	}

	var toolCalls int
	for _, ev := range received {
		if ev.Type == event.EventToolCallStart {
			toolCalls++
		}
	}
	if toolCalls != nToolCalls {
		t.Errorf("expected %d tool_call_start events, got %d (some were incorrectly coalesced)", nToolCalls, toolCalls)
	}
}

// TestSSEBufferMultiSessionCoalescing verifies that coalescing is per-session:
// the last snapshot for each session is independently preserved.
func TestSSEBufferMultiSessionCoalescing(t *testing.T) {
	buf := NewSSEBuffer(40 * time.Millisecond)
	buf.Start()
	defer buf.Stop()

	const nSessions = 5
	const burstPerSession = 20

	for s := range nSessions {
		sid := fmt.Sprintf("sess-%d", s)
		for i := range burstPerSession {
			buf.Add(makeTokEv(sid, i+1))
		}
	}

	var received []event.Event
	deadline := time.After(200 * time.Millisecond)
collect:
	for {
		select {
		case batch, ok := <-buf.Out():
			if !ok {
				break collect
			}
			received = append(received, batch...)
		case <-deadline:
			break collect
		}
	}

	// Count unique sessions in output.
	sessionSeen := make(map[string]int)
	for _, ev := range received {
		if ev.Type == event.EventTokenUsage {
			sessionSeen[ev.SessionID]++
		}
	}
	if got := len(sessionSeen); got != nSessions {
		t.Errorf("expected %d sessions in output, got %d", nSessions, got)
	}
	// Each session should appear only once (coalesced to latest).
	for sid, count := range sessionSeen {
		if count > 1 {
			t.Errorf("session %s appeared %d times; should be coalesced to 1", sid, count)
		}
	}
}

// TestSSEBufferStopFlushesRemaining verifies that Stop() triggers a final flush
// so no events are silently lost.
func TestSSEBufferStopFlushesRemaining(t *testing.T) {
	buf := NewSSEBuffer(10 * time.Second) // Very long window — relies on Stop to flush.
	buf.Start()

	buf.Add(makeTokEv("stop-session", 42))
	buf.Add(event.Event{
		ID: "final-call", Type: event.EventToolCallStart,
		SessionID: "stop-session", Platform: event.PlatformClaude,
		Timestamp: time.Now(),
	})

	buf.Stop()

	// Drain Out() with a short timeout.
	var received []event.Event
	testutil.Eventually(t, func() bool {
		for {
			select {
			case batch, ok := <-buf.Out():
				if !ok {
					return len(received) >= 2
				}
				received = append(received, batch...)
			default:
				return len(received) >= 2
			}
		}
	}, 500*time.Millisecond, 10*time.Millisecond, "final flush after Stop")

	if len(received) < 2 {
		t.Errorf("expected at least 2 events after Stop flush, got %d", len(received))
	}
}
