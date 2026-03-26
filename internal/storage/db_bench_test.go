package storage

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/tt-a1i/agmon/internal/event"
)

func benchmarkDB(b *testing.B) *DB {
	b.Helper()
	db, err := Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatalf("open db: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })
	return db
}

func BenchmarkInsertTokenUsageIncremental(b *testing.B) {
	db := benchmarkDB(b)
	now := time.Now().UTC()

	if err := db.UpsertSession("bench-session", event.PlatformClaude, now); err != nil {
		b.Fatalf("upsert session: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.InsertTokenUsage(
			"agent-1",
			"bench-session",
			1000+i%50,
			200+i%20,
			10,
			5,
			"sonnet",
			0.01,
			now.Add(time.Duration(i)*time.Second),
			fmt.Sprintf("src-%d", i),
		); err != nil {
			b.Fatalf("insert token usage: %v", err)
		}
	}
}

func BenchmarkUpdateSessionTokensReconcile(b *testing.B) {
	db := benchmarkDB(b)
	now := time.Now().UTC()

	if err := db.UpsertSession("bench-session", event.PlatformClaude, now); err != nil {
		b.Fatalf("upsert session: %v", err)
	}
	for i := 0; i < 5000; i++ {
		if err := db.InsertTokenUsage(
			"agent-1",
			"bench-session",
			1000+i%50,
			200+i%20,
			10,
			5,
			"sonnet",
			0.01,
			now.Add(time.Duration(i)*time.Second),
			fmt.Sprintf("seed-%d", i),
		); err != nil {
			b.Fatalf("seed token usage: %v", err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.UpdateSessionTokens("bench-session"); err != nil {
			b.Fatalf("reconcile session tokens: %v", err)
		}
	}
}
