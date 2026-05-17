package daemon

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

// mockBatchDB records InsertTokenUsageBatch calls for test assertions.
type mockBatchDB struct {
	mu    sync.Mutex
	calls [][]event.Event
	err   error
}

func (m *mockBatchDB) InsertTokenUsageBatch(events []event.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]event.Event, len(events))
	copy(cp, events)
	m.calls = append(m.calls, cp)
	return m.err
}

func (m *mockBatchDB) totalEvents() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, batch := range m.calls {
		n += len(batch)
	}
	return n
}

func (m *mockBatchDB) batchCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

func makeTokenEvent(id string) event.Event {
	return event.Event{
		ID: id, Type: event.EventTokenUsage,
		SessionID: "s1", Platform: event.PlatformClaude,
		Timestamp: time.Now(),
		Data:      event.EventData{InputTokens: 10, OutputTokens: 5},
	}
}

// TestBatchWriterFlushesBySize verifies that accumulating flushSize events
// triggers a flush before the ticker fires.
func TestBatchWriterFlushesBySize(t *testing.T) {
	mock := &mockBatchDB{}
	bw := NewBatchWriter(mock, 10*time.Second, 5) // long ticker, flush at 5 events
	bw.Start()
	defer bw.Stop()

	for i := range 5 {
		bw.Enqueue(makeTokenEvent(fmt.Sprintf("e%d", i)))
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if mock.totalEvents() == 5 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if got := mock.totalEvents(); got != 5 {
		t.Errorf("expected 5 events flushed by size, got %d", got)
	}
	if got := mock.batchCount(); got != 1 {
		t.Errorf("expected 1 batch, got %d", got)
	}
}

// TestBatchWriterFlushesByTimer verifies that events below flushSize are
// still flushed when the ticker fires.
func TestBatchWriterFlushesByTimer(t *testing.T) {
	mock := &mockBatchDB{}
	bw := NewBatchWriter(mock, 30*time.Millisecond, 100) // small window, large size threshold
	bw.Start()
	defer bw.Stop()

	for i := range 3 {
		bw.Enqueue(makeTokenEvent(fmt.Sprintf("t%d", i)))
	}

	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		if mock.totalEvents() == 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if got := mock.totalEvents(); got != 3 {
		t.Errorf("expected 3 events flushed by timer, got %d", got)
	}
}

// TestBatchWriterStopFlushesRemaining verifies that Stop() drains the queue
// and writes all buffered events even if the ticker has not fired.
func TestBatchWriterStopFlushesRemaining(t *testing.T) {
	mock := &mockBatchDB{}
	bw := NewBatchWriter(mock, 10*time.Second, 1000) // long ticker, huge size threshold
	bw.Start()

	for i := range 7 {
		bw.Enqueue(makeTokenEvent(fmt.Sprintf("r%d", i)))
	}

	bw.Stop() // must drain and flush synchronously

	if got := mock.totalEvents(); got != 7 {
		t.Errorf("expected 7 events after Stop, got %d", got)
	}
}

// TestBatchWriterEmptyBatchNoOp verifies that Stop() on an empty writer does
// not call InsertTokenUsageBatch.
func TestBatchWriterEmptyBatchNoOp(t *testing.T) {
	mock := &mockBatchDB{}
	bw := NewBatchWriter(mock, 10*time.Second, 50)
	bw.Start()
	bw.Stop()

	if got := mock.batchCount(); got != 0 {
		t.Errorf("expected 0 batch calls on empty writer, got %d", got)
	}
}

// TestBatchWriterConcurrentEnqueue verifies that concurrent Enqueue calls
// from multiple goroutines do not cause races and all events are delivered.
func TestBatchWriterConcurrentEnqueue(t *testing.T) {
	mock := &mockBatchDB{}
	bw := NewBatchWriter(mock, 10*time.Millisecond, 50)
	bw.Start()

	const goroutines = 10
	const perGoroutine = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := range goroutines {
		go func(g int) {
			defer wg.Done()
			for i := range perGoroutine {
				bw.Enqueue(makeTokenEvent(fmt.Sprintf("c%d-%d", g, i)))
			}
		}(g)
	}
	wg.Wait()
	bw.Stop()

	if got := mock.totalEvents(); got != goroutines*perGoroutine {
		t.Errorf("expected %d events after concurrent enqueue, got %d", goroutines*perGoroutine, got)
	}
}

// TestBatchWriterMultipleFlushGroups verifies that events beyond flushSize
// are split into multiple batch calls correctly.
func TestBatchWriterMultipleFlushGroups(t *testing.T) {
	mock := &mockBatchDB{}
	bw := NewBatchWriter(mock, 10*time.Second, 3) // flush every 3 events
	bw.Start()

	for i := range 9 { // 9 events → 3 flushes of 3 each
		bw.Enqueue(makeTokenEvent(fmt.Sprintf("g%d", i)))
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if mock.totalEvents() == 9 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	bw.Stop()

	if got := mock.totalEvents(); got != 9 {
		t.Errorf("expected 9 events total, got %d", got)
	}
	if got := mock.batchCount(); got < 3 {
		t.Errorf("expected at least 3 batch calls for 9 events (size=3), got %d", got)
	}
}
