package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/storage"
)

// setupBenchDaemon starts a daemon with a real Unix socket for ingestion benchmarks.
func setupBenchDaemon(b *testing.B) (*Daemon, string) {
	b.Helper()
	dir, err := os.MkdirTemp("", "tm-bench-")
	if err != nil {
		b.Fatalf("tmp dir: %v", err)
	}
	b.Cleanup(func() { os.RemoveAll(dir) })

	db, err := storage.Open(filepath.Join(dir, "bench.db"))
	if err != nil {
		b.Fatalf("open db: %v", err)
	}
	b.Cleanup(func() { db.Close() })

	sockPath := filepath.Join(dir, "d.sock")
	d := New(db, sockPath)
	if err := d.Start(); err != nil {
		b.Fatalf("start daemon: %v", err)
	}
	b.Cleanup(d.Stop)

	return d, sockPath
}

// sendEventToSocket JSON-encodes ev and writes it to the daemon socket.
func sendEventToSocket(conn net.Conn, ev event.Event) error {
	return json.NewEncoder(conn).Encode(ev)
}

// newBenchDaemon creates an unstarted daemon for use in process-level benchmarks.
// Do NOT call d.Stop() — the daemon has no started goroutines to clean up.
func newBenchDaemon(b *testing.B) (*Daemon, *storage.DB) {
	b.Helper()
	dir, err := os.MkdirTemp("", "tm-bench-db-")
	if err != nil {
		b.Fatalf("tmp dir: %v", err)
	}
	b.Cleanup(func() { os.RemoveAll(dir) })
	db, err := storage.Open(filepath.Join(dir, "bench.db"))
	if err != nil {
		b.Fatalf("open db: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })
	sockPath := filepath.Join(dir, "bench.sock")
	return New(db, sockPath), db
}

// BenchmarkIngestionProcessEvent measures the cost of processEvent alone —
// the SQLite write path without socket or JSON decode overhead.
func BenchmarkIngestionProcessEvent(b *testing.B) {
	d, db := newBenchDaemon(b)

	sessionID := "bench-session"
	if err := db.UpsertSession(sessionID, event.PlatformClaude, time.Now()); err != nil {
		b.Fatalf("upsert session: %v", err)
	}

	now := time.Now()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ev := event.Event{
			ID:        fmt.Sprintf("bench-call-%d", i),
			Type:      event.EventToolCallStart,
			SessionID: sessionID,
			AgentID:   "agent-1",
			Platform:  event.PlatformClaude,
			Timestamp: now.Add(time.Duration(i) * time.Microsecond),
			Data:      event.EventData{ToolName: "Read"},
		}
		if err := d.processEvent(ev); err != nil {
			b.Fatalf("processEvent: %v", err)
		}
	}
}

// BenchmarkIngestionViaSocket measures the full end-to-end ingestion latency:
// JSON encode → Unix socket write → daemon decode → processEvent → broadcast.
// Each iteration opens a persistent connection and sends one event, waiting for
// the broadcast to confirm delivery.
func BenchmarkIngestionViaSocket(b *testing.B) {
	_, sockPath := setupBenchDaemon(b)

	// Connect once and reuse the connection across iterations.
	conn, err := dialSocket(sockPath)
	if err != nil {
		b.Fatalf("dial socket: %v", err)
	}
	defer conn.Close()

	sessionID := "bench-sock-session"
	now := time.Now()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ev := event.Event{
			ID:        fmt.Sprintf("sock-call-%d", i),
			Type:      event.EventToolCallStart,
			SessionID: sessionID,
			AgentID:   "agent-1",
			Platform:  event.PlatformClaude,
			Timestamp: now.Add(time.Duration(i) * time.Microsecond),
			Data:      event.EventData{ToolName: "Write"},
		}
		if err := sendEventToSocket(conn, ev); err != nil {
			b.Fatalf("send event: %v", err)
		}
	}
}

// BenchmarkIngestionToolStartEndPair measures the round-trip cost of a
// matched PreToolUse / PostToolUse pair — the most common production pattern.
func BenchmarkIngestionToolStartEndPair(b *testing.B) {
	d, db := newBenchDaemon(b)

	sessionID := "bench-pair-session"
	if err := db.UpsertSession(sessionID, event.PlatformClaude, time.Now()); err != nil {
		b.Fatalf("upsert session: %v", err)
	}

	now := time.Now()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		callID := fmt.Sprintf("pair-call-%d", i)
		ts := now.Add(time.Duration(i*2) * time.Microsecond)
		start := event.Event{
			ID: callID, Type: event.EventToolCallStart,
			SessionID: sessionID, AgentID: "agent-1",
			Platform:  event.PlatformClaude,
			Timestamp: ts,
			Data:      event.EventData{ToolName: "Edit"},
		}
		end := event.Event{
			ID: callID, Type: event.EventToolCallEnd,
			SessionID: sessionID, AgentID: "agent-1",
			Platform:  event.PlatformClaude,
			Timestamp: ts.Add(time.Millisecond),
			Data:      event.EventData{ToolResult: "ok", DurationMs: 1},
		}
		if err := d.processEvent(start); err != nil {
			b.Fatalf("processEvent start: %v", err)
		}
		if err := d.processEvent(end); err != nil {
			b.Fatalf("processEvent end: %v", err)
		}
	}
}
