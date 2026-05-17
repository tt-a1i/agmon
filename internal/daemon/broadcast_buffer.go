package daemon

import (
	"sync"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

// SSEBuffer coalesces high-frequency events within a sliding time window
// before forwarding them to downstream consumers (e.g. SSE handlers).
//
// For event types that carry a snapshot of session state (token_usage), only
// the most-recent event per session is kept in the window — earlier events for
// the same session carry stale counters that the latest supersedes.
//
// Other event types (tool_call_start, session_start, …) are always forwarded
// individually because every occurrence is semantically distinct.
//
// Usage:
//
//	buf := NewSSEBuffer(50 * time.Millisecond)
//	buf.Start()
//	defer buf.Stop()
//
//	// Feed events (e.g. from a daemon subscriber goroutine):
//	buf.Add(ev)
//
//	// Consume flushed batches:
//	for batch := range buf.Out() {
//	    for _, ev := range batch {
//	        sendSSE(ev)
//	    }
//	}
type SSEBuffer struct {
	window time.Duration
	mu     sync.Mutex
	latest map[string]event.Event // session_id → latest coalesced event
	queue  []event.Event          // non-coalesced events, preserved in order
	out    chan []event.Event
	done   chan struct{}
	once   sync.Once
}

// NewSSEBuffer creates a buffer with the given coalescing window. A 50 ms
// window eliminates burst re-renders while keeping perceived latency low.
func NewSSEBuffer(window time.Duration) *SSEBuffer {
	return &SSEBuffer{
		window: window,
		latest: make(map[string]event.Event),
		out:    make(chan []event.Event, 64),
		done:   make(chan struct{}),
	}
}

// Start launches the flush ticker goroutine.
func (b *SSEBuffer) Start() {
	go b.run()
}

// Stop shuts down the flush goroutine and closes Out().
func (b *SSEBuffer) Stop() {
	b.once.Do(func() {
		close(b.done)
	})
}

// Out returns the channel on which flushed batches are delivered. Closed when
// Stop is called and all pending events have been flushed.
func (b *SSEBuffer) Out() <-chan []event.Event {
	return b.out
}

// Add ingests an event. Coalescing events (token_usage) overwrite the previous
// snapshot for the same session; all other events are appended individually.
func (b *SSEBuffer) Add(ev event.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if isCoalesceable(ev) {
		b.latest[ev.SessionID] = ev
	} else {
		b.queue = append(b.queue, ev)
	}
}

// isCoalesceable returns true for event types where only the latest snapshot
// per session is meaningful. Intermediate events for the same session can be
// dropped within the window without losing information.
func isCoalesceable(ev event.Event) bool {
	return ev.Type == event.EventTokenUsage
}

func (b *SSEBuffer) run() {
	ticker := time.NewTicker(b.window)
	defer ticker.Stop()
	defer close(b.out)
	for {
		select {
		case <-b.done:
			b.flush()
			return
		case <-ticker.C:
			b.flush()
		}
	}
}

func (b *SSEBuffer) flush() {
	b.mu.Lock()
	q := b.queue
	l := b.latest
	b.queue = nil
	b.latest = make(map[string]event.Event)
	b.mu.Unlock()

	if len(q) == 0 && len(l) == 0 {
		return
	}

	batch := make([]event.Event, 0, len(q)+len(l))
	batch = append(batch, q...)
	for _, ev := range l {
		batch = append(batch, ev)
	}

	select {
	case b.out <- batch:
	default:
		// Downstream consumer is backed up — drop the batch rather than
		// blocking the flush goroutine (same non-blocking philosophy as
		// daemon.broadcast).
	}
}
