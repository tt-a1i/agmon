package storage

import (
	"fmt"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

// seedBench creates a benchmark DB with N token_usage rows across M sessions,
// spread over the last 30 days for realistic daily-aggregation benchmarking.
func seedBench(b *testing.B, sessions, rowsPerSession int) (*DB, time.Time, time.Time) {
	b.Helper()
	db := benchmarkDB(b)
	now := time.Now().UTC()
	start := now.AddDate(0, 0, -30)

	for s := 0; s < sessions; s++ {
		sid := fmt.Sprintf("sess-%04d", s)
		if err := db.UpsertSession(sid, event.PlatformClaude, start); err != nil {
			b.Fatalf("upsert: %v", err)
		}
		for i := 0; i < rowsPerSession; i++ {
			ts := start.Add(time.Duration(i*30) * time.Minute)
			src := fmt.Sprintf("bsrc-%d-%d", s, i)
			if err := db.InsertTokenUsage("agent", sid, 100+i, 50+i, 0, 0, "claude-sonnet-4-6", 0.01, ts, src); err != nil {
				b.Fatalf("insert: %v", err)
			}
		}
	}
	return db, start, now
}

func BenchmarkGetDailyCostsBetween(b *testing.B) {
	// 10 sessions × 50 rows = 500 rows over 30 days — realistic dashboard load.
	db, from, to := seedBench(b, 10, 50)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := db.GetDailyCostsBetween(from, to); err != nil {
			b.Fatalf("query: %v", err)
		}
	}
}

func BenchmarkGetCostBetween(b *testing.B) {
	db, from, to := seedBench(b, 10, 50)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := db.GetCostBetween(from, to); err != nil {
			b.Fatalf("query: %v", err)
		}
	}
}

func BenchmarkGetModelCostBreakdown(b *testing.B) {
	db, from, to := seedBench(b, 10, 50)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := db.GetModelCostBreakdown(from, to); err != nil {
			b.Fatalf("query: %v", err)
		}
	}
}

func BenchmarkListSessions(b *testing.B) {
	// 250 visible sessions — exercises ORDER BY + LIMIT 200 hot path.
	db, _, _ := seedBench(b, 250, 1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := db.ListSessions(); err != nil {
			b.Fatalf("list: %v", err)
		}
	}
}

func BenchmarkGetActiveSessionCount(b *testing.B) {
	db, _, _ := seedBench(b, 250, 1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := db.GetActiveSessionCount(); err != nil {
			b.Fatalf("count: %v", err)
		}
	}
}
