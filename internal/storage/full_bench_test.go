package storage

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

type fullBenchSize struct {
	name           string
	sessions       int
	rowsPerSession int
}

var fullBenchSizes = []fullBenchSize{
	{name: "small", sessions: 10, rowsPerSession: 50},
	{name: "medium", sessions: 100, rowsPerSession: 100},
}

func BenchmarkGetSessionByIDPrefix(b *testing.B) {
	sizes := []struct {
		name     string
		sessions int
	}{
		{name: "small", sessions: 100},
		{name: "medium", sessions: 1000},
	}
	for _, sz := range sizes {
		b.Run(sz.name, func(b *testing.B) {
			db := benchmarkDB(b)
			now := time.Now().UTC()
			for i := 0; i < sz.sessions; i++ {
				sid := fmt.Sprintf("prefix-sess-%04d-deadbeef", i)
				if err := db.UpsertSession(sid, event.PlatformClaude, now); err != nil {
					b.Fatalf("upsert session: %v", err)
				}
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, _, err := db.GetSessionByIDPrefix("prefix-sess-0000"); err != nil {
					b.Fatalf("lookup prefix: %v", err)
				}
			}
		})
	}
}

func BenchmarkListAgents(b *testing.B) {
	for _, sz := range fullBenchSizes {
		b.Run(sz.name, func(b *testing.B) {
			db, targetSession, _, _ := seedFullBench(b, sz)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := db.ListAgents(targetSession); err != nil {
					b.Fatalf("list agents: %v", err)
				}
			}
		})
	}
}

func BenchmarkListToolCalls(b *testing.B) {
	for _, sz := range fullBenchSizes {
		b.Run(sz.name, func(b *testing.B) {
			db, targetSession, _, _ := seedFullBench(b, sz)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := db.ListToolCalls(targetSession, 200); err != nil {
					b.Fatalf("list tool calls: %v", err)
				}
			}
		})
	}
}

func BenchmarkListFileChanges(b *testing.B) {
	for _, sz := range fullBenchSizes {
		b.Run(sz.name, func(b *testing.B) {
			db, targetSession, _, _ := seedFullBench(b, sz)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := db.ListFileChanges(targetSession); err != nil {
					b.Fatalf("list file changes: %v", err)
				}
			}
		})
	}
}

func BenchmarkGetTokensSince(b *testing.B) {
	for _, sz := range fullBenchSizes {
		b.Run(sz.name, func(b *testing.B) {
			db, since, _ := seedCurrentBench(b, sz)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, _, err := db.GetTokensSince(&since); err != nil {
					b.Fatalf("get tokens since: %v", err)
				}
			}
		})
	}
}

func BenchmarkGetTodayCost(b *testing.B) {
	for _, sz := range fullBenchSizes {
		b.Run(sz.name, func(b *testing.B) {
			db, _, _ := seedCurrentBench(b, sz)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := db.GetTodayCost(); err != nil {
					b.Fatalf("get today cost: %v", err)
				}
			}
		})
	}
}

func BenchmarkGetMonthCostProjection(b *testing.B) {
	for _, sz := range fullBenchSizes {
		b.Run(sz.name, func(b *testing.B) {
			db, _, now := seedCurrentBench(b, sz)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := db.GetMonthCostProjection(now); err != nil {
					b.Fatalf("get projection: %v", err)
				}
			}
		})
	}
}

func BenchmarkGetSessionModelBreakdown(b *testing.B) {
	for _, sz := range fullBenchSizes {
		b.Run(sz.name, func(b *testing.B) {
			db, targetSession, _, _ := seedFullBench(b, sz)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := db.GetSessionModelBreakdown(targetSession); err != nil {
					b.Fatalf("get session model breakdown: %v", err)
				}
			}
		})
	}
}

func BenchmarkAnalyze(b *testing.B) {
	for _, sz := range fullBenchSizes {
		b.Run(sz.name, func(b *testing.B) {
			db, _, from, to := seedFullBench(b, sz)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := db.Analyze(from, to); err != nil {
					b.Fatalf("analyze: %v", err)
				}
			}
		})
	}
}

func BenchmarkListBudgets(b *testing.B) {
	for _, sz := range fullBenchSizes {
		b.Run(sz.name, func(b *testing.B) {
			db, _ := seedBudgetBench(b, sz)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := db.ListBudgets(); err != nil {
					b.Fatalf("list budgets: %v", err)
				}
			}
		})
	}
}

func BenchmarkGetBudgetUsage(b *testing.B) {
	for _, sz := range fullBenchSizes {
		b.Run(sz.name, func(b *testing.B) {
			db, budgetID := seedBudgetBench(b, sz)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, _, err := db.GetBudgetUsage(budgetID); err != nil {
					b.Fatalf("get budget usage: %v", err)
				}
			}
		})
	}
}

func BenchmarkBackupTo(b *testing.B) {
	for _, sz := range fullBenchSizes {
		b.Run(sz.name, func(b *testing.B) {
			db, _, _ := seedCurrentBench(b, sz)
			destDir := b.TempDir()

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				dest := filepath.Join(destDir, fmt.Sprintf("backup-%06d.db", i))
				if _, _, err := db.BackupTo(dest); err != nil {
					b.Fatalf("backup to %s: %v", dest, err)
				}
			}
		})
	}
}

func BenchmarkInsertToolCallStart(b *testing.B) {
	for _, sz := range fullBenchSizes {
		b.Run(sz.name, func(b *testing.B) {
			db, _, _ := seedBench(b, sz.sessions, 1)
			now := time.Now().UTC()

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				callID := fmt.Sprintf("bench-insert-call-%s-%d", sz.name, i)
				if _, err := db.InsertToolCallStart(callID, "agent", "sess-0000", "Edit", "Edit file.go", now.Add(time.Duration(i)*time.Millisecond)); err != nil {
					b.Fatalf("insert tool call start: %v", err)
				}
			}
		})
	}
}

func BenchmarkUpdateToolCallEnd(b *testing.B) {
	for _, sz := range fullBenchSizes {
		b.Run(sz.name, func(b *testing.B) {
			db, _, _ := seedBench(b, sz.sessions, 1)
			now := time.Now().UTC()
			callCount := sz.rowsPerSession * 20
			for i := 0; i < callCount; i++ {
				callID := fmt.Sprintf("bench-update-call-%s-%d", sz.name, i)
				if _, err := db.InsertToolCallStart(callID, "agent", "sess-0000", "Bash", "echo before", now.Add(time.Duration(i)*time.Millisecond)); err != nil {
					b.Fatalf("seed tool call start: %v", err)
				}
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				callID := fmt.Sprintf("bench-update-call-%s-%d", sz.name, i%callCount)
				if err := db.UpdateToolCallEnd(callID, "tool result output", event.StatusSuccess, 42, now.Add(time.Duration(i)*time.Millisecond)); err != nil {
					b.Fatalf("update tool call end: %v", err)
				}
			}
		})
	}
}

func BenchmarkInsertFileChangeWithSource(b *testing.B) {
	for _, sz := range fullBenchSizes {
		b.Run(sz.name, func(b *testing.B) {
			db, _, _ := seedBench(b, sz.sessions, 1)
			now := time.Now().UTC()

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				path := fmt.Sprintf("src/bench-file-%s-%06d.go", sz.name, i)
				source := fmt.Sprintf("file-source-%s-%06d", sz.name, i)
				if err := db.InsertFileChangeWithSource("sess-0000", path, event.FileEdit, now.Add(time.Duration(i)*time.Millisecond), source); err != nil {
					b.Fatalf("insert file change: %v", err)
				}
			}
		})
	}
}

func seedFullBench(b *testing.B, sz fullBenchSize) (*DB, string, time.Time, time.Time) {
	b.Helper()
	db, from, to := seedBench(b, sz.sessions, sz.rowsPerSession)
	targetSession := "sess-0000"
	detailRows := maxBenchRows(250, sz.rowsPerSession*3)

	for i := 0; i < 8; i++ {
		agentID := fmt.Sprintf("detail-agent-%02d", i)
		parentID := ""
		if i > 0 {
			parentID = "detail-agent-00"
		}
		if err := db.UpsertAgent(agentID, targetSession, parentID, "worker", from.Add(time.Duration(i)*time.Second)); err != nil {
			b.Fatalf("seed agent: %v", err)
		}
	}

	for i := 0; i < detailRows; i++ {
		callID := fmt.Sprintf("detail-call-%04d", i)
		agentID := fmt.Sprintf("detail-agent-%02d", i%8)
		toolName := []string{"Read", "Edit", "Bash", "Grep"}[i%4]
		ts := from.Add(time.Duration(i) * time.Second)
		if _, err := db.InsertToolCallStart(callID, agentID, targetSession, toolName, fmt.Sprintf("%s params %d", toolName, i), ts); err != nil {
			b.Fatalf("seed tool call start: %v", err)
		}
		if err := db.UpdateToolCallEnd(callID, "ok", event.StatusSuccess, int64(10+i%50), ts.Add(100*time.Millisecond)); err != nil {
			b.Fatalf("seed tool call end: %v", err)
		}
		if err := db.InsertFileChangeWithSource(targetSession, fmt.Sprintf("src/file_%04d.go", i), event.FileEdit, ts, "detail-file-"+callID); err != nil {
			b.Fatalf("seed file change: %v", err)
		}
	}

	models := []string{"claude-haiku-4-5", "claude-opus-4-7"}
	for i, model := range models {
		if err := db.InsertTokenUsage("agent", targetSession, 1000+i, 500+i, 0, 0, model, 0.25+float64(i)*0.10, from.Add(time.Duration(i)*time.Minute), fmt.Sprintf("detail-model-%d", i)); err != nil {
			b.Fatalf("seed model usage: %v", err)
		}
	}
	return db, targetSession, from, to
}

func seedCurrentBench(b *testing.B, sz fullBenchSize) (*DB, time.Time, time.Time) {
	b.Helper()
	db := benchmarkDB(b)
	now := time.Now().UTC()
	start := now.Add(-time.Duration(sz.rowsPerSession) * time.Minute)
	for s := 0; s < sz.sessions; s++ {
		sid := fmt.Sprintf("current-sess-%04d", s)
		if err := db.UpsertSession(sid, event.PlatformClaude, start); err != nil {
			b.Fatalf("upsert current session: %v", err)
		}
		for i := 0; i < sz.rowsPerSession; i++ {
			ts := start.Add(time.Duration(i) * time.Minute)
			src := fmt.Sprintf("current-src-%d-%d", s, i)
			model := []string{"claude-sonnet-4-6", "claude-haiku-4-5", "claude-opus-4-7"}[i%3]
			if err := db.InsertTokenUsage("agent", sid, 100+i, 50+i, 0, 0, model, 0.01, ts, src); err != nil {
				b.Fatalf("insert current usage: %v", err)
			}
		}
	}
	return db, start, now
}

func seedBudgetBench(b *testing.B, sz fullBenchSize) (*DB, int64) {
	b.Helper()
	db, _, _ := seedCurrentBench(b, sz)
	budgetCount := maxBenchRows(5, sz.sessions/2)
	var firstID int64
	for i := 0; i < budgetCount; i++ {
		platform := ""
		if i%3 == 1 {
			platform = string(event.PlatformClaude)
		} else if i%3 == 2 {
			platform = string(event.PlatformCodex)
		}
		id, err := db.InsertBudget(fmt.Sprintf("Bench Budget %02d", i), 100+float64(i), platform)
		if err != nil {
			b.Fatalf("insert budget: %v", err)
		}
		if i == 0 {
			firstID = id
		}
	}
	return db, firstID
}

func maxBenchRows(a, b int) int {
	if a > b {
		return a
	}
	return b
}
