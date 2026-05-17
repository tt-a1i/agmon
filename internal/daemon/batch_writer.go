package daemon

import (
	"log"
	"sync"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

// batchDB is the storage interface BatchWriter requires.
type batchDB interface {
	InsertTokenUsageBatch(events []event.Event) error
}

// BatchWriter coalesces token_usage events into batched SQLite transactions.
// Events are flushed when the buffer reaches flushSize or flushInterval elapses,
// whichever comes first.  Stop() drains the queue and does a final flush.
type BatchWriter struct {
	q             chan event.Event
	flushInterval time.Duration
	flushSize     int
	db            batchDB
	done          chan struct{}
	once          sync.Once
	wg            sync.WaitGroup
}

// NewBatchWriter constructs a BatchWriter.  Call Start() before Enqueue().
func NewBatchWriter(db batchDB, flushInterval time.Duration, flushSize int) *BatchWriter {
	return &BatchWriter{
		q:             make(chan event.Event, 100),
		flushInterval: flushInterval,
		flushSize:     flushSize,
		db:            db,
		done:          make(chan struct{}),
	}
}

// Start launches the background writer goroutine.
func (b *BatchWriter) Start() {
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		b.run()
	}()
}

// Enqueue adds ev to the write queue.  Blocks only if the queue is full;
// returns immediately without enqueuing if Stop() has already been called.
func (b *BatchWriter) Enqueue(ev event.Event) {
	select {
	case b.q <- ev:
	case <-b.done:
	}
}

// Stop signals the writer to exit, waits for the final flush to complete,
// and is safe to call multiple times.
func (b *BatchWriter) Stop() {
	b.once.Do(func() {
		close(b.done)
		b.wg.Wait()
	})
}

func (b *BatchWriter) run() {
	ticker := time.NewTicker(b.flushInterval)
	defer ticker.Stop()

	var buf []event.Event

	flush := func() {
		if len(buf) == 0 {
			return
		}
		if err := b.db.InsertTokenUsageBatch(buf); err != nil {
			log.Printf("BatchWriter flush: %v", err)
		}
		buf = buf[:0]
	}

	for {
		select {
		case ev := <-b.q:
			buf = append(buf, ev)
			if len(buf) >= b.flushSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-b.done:
			// Drain anything still in the channel before the final flush.
			for {
				select {
				case ev := <-b.q:
					buf = append(buf, ev)
				default:
					flush()
					return
				}
			}
		}
	}
}
