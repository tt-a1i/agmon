package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/storage"
)

// BenchmarkBroadcast measures the per-event broadcast overhead with one
// local subscriber attached. Regressions here would slow the hot path used
// by every emitted hook event.
func BenchmarkBroadcast(b *testing.B) {
	dir := b.TempDir()
	db, err := storage.Open(filepath.Join(dir, "b.db"))
	if err != nil {
		b.Fatalf("open db: %v", err)
	}
	b.Cleanup(func() { db.Close() })

	d := New(db, filepath.Join(dir, "bench.sock"))
	ch := d.Subscribe()
	// Drain in a goroutine so the buffer doesn't fill.
	done := make(chan struct{})
	go func() {
		for range ch {
			// drop
		}
		close(done)
	}()

	ev := event.Event{
		ID:        "bench",
		Type:      event.EventTokenUsage,
		SessionID: "s1",
		Platform:  event.PlatformClaude,
		Timestamp: time.Now().UTC(),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d.broadcast(ev)
	}
	b.StopTimer()
	d.Unsubscribe(ch)
}

// BenchmarkProcessEventTokenUsage measures end-to-end overhead of the
// daemon's main hot path: TokenUsage event → InsertTokenUsage → session
// totals incremental update.
func BenchmarkProcessEventTokenUsage(b *testing.B) {
	dir := b.TempDir()
	db, err := storage.Open(filepath.Join(dir, "b.db"))
	if err != nil {
		b.Fatalf("open db: %v", err)
	}
	b.Cleanup(func() { db.Close() })

	d := New(db, filepath.Join(dir, "bench.sock"))
	now := time.Now().UTC()
	if err := db.UpsertSession("bench-s", event.PlatformClaude, now); err != nil {
		b.Fatalf("upsert: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ev := event.Event{
			ID:        "tok-" + time.Now().Format("150405.999999999"),
			Type:      event.EventTokenUsage,
			SessionID: "bench-s",
			AgentID:   "agent",
			Platform:  event.PlatformClaude,
			Timestamp: now.Add(time.Duration(i) * time.Microsecond),
			Data: event.EventData{
				InputTokens:  100,
				OutputTokens: 50,
				Model:        "claude-sonnet-4-6",
				CostUSD:      0.01,
			},
		}
		// Use a unique source_id so dedup doesn't skip the insert.
		ev.ID = ev.ID + "-" + string(rune('a'+(i%26)))
		_ = d.processEvent(ev)
	}
}
