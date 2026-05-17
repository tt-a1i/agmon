package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/storage"
)

func BenchmarkHandleSearch(b *testing.B) {
	db := benchWebDB(b)
	seedSearchEndpointBench(b, db, 1000)
	srv := NewServer(db, "0")
	req := httptest.NewRequest(http.MethodGet, "/api/search?q=Edit", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		srv.handleSearch(w, req)
		if w.Code != http.StatusOK {
			b.Fatalf("status: got %d, want 200. body: %s", w.Code, w.Body.String())
		}
	}
}

func BenchmarkHandleExportCSV(b *testing.B) {
	db := benchWebDB(b)
	seedExportEndpointBench(b, db, 1000)
	srv := NewServer(db, "0")
	req := httptest.NewRequest(http.MethodGet, "/api/export?range=week&format=csv", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		srv.handleExport(w, req)
		if w.Code != http.StatusOK {
			b.Fatalf("status: got %d, want 200. body: %s", w.Code, w.Body.String())
		}
	}
}

func BenchmarkHandleExportJSON(b *testing.B) {
	db := benchWebDB(b)
	seedExportEndpointBench(b, db, 1000)
	srv := NewServer(db, "0")
	req := httptest.NewRequest(http.MethodGet, "/api/export?range=week&format=json", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		srv.handleExport(w, req)
		if w.Code != http.StatusOK {
			b.Fatalf("status: got %d, want 200. body: %s", w.Code, w.Body.String())
		}
	}
}

func BenchmarkHandleCompare(b *testing.B) {
	db := benchWebDB(b)
	seedCompareEndpointBench(b, db, 100)
	srv := NewServer(db, "0")
	req := httptest.NewRequest(http.MethodGet, "/api/compare?a=bench-compare-a&b=bench-compare-b", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		srv.handleCompare(w, req)
		if w.Code != http.StatusOK {
			b.Fatalf("status: got %d, want 200. body: %s", w.Code, w.Body.String())
		}
	}
}

func BenchmarkHandleBudgetsGET(b *testing.B) {
	db := benchWebDB(b)
	seedBudgetEndpointBench(b, db, 10)
	srv := NewServer(db, "0")
	req := httptest.NewRequest(http.MethodGet, "/api/budgets", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		srv.handleBudgets(w, req)
		if w.Code != http.StatusOK {
			b.Fatalf("status: got %d, want 200. body: %s", w.Code, w.Body.String())
		}
	}
}

func BenchmarkHandleMetrics(b *testing.B) {
	db := benchWebDB(b)
	seedMetricsEndpointBench(b, db)
	srv := NewServer(db, "0",
		WithBuildVersion("bench"),
		WithMetricsProvider(benchMetricsProvider{
			droppedBcast: 11,
			droppedShut:  7,
			dupTool:      3,
			budgets:      benchBudgetMetrics(5),
		}),
	)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		srv.handleMetrics(w, req)
		if w.Code != http.StatusOK {
			b.Fatalf("status: got %d, want 200. body: %s", w.Code, w.Body.String())
		}
	}
}

type benchMetricsProvider struct {
	droppedBcast int64
	droppedShut  int64
	dupTool      int64
	budgets      []BudgetMetric
}

func (m benchMetricsProvider) DaemonStats() (int64, int64, int64) {
	return m.droppedBcast, m.droppedShut, m.dupTool
}

func (m benchMetricsProvider) BudgetUsageAll() ([]BudgetMetric, error) {
	return m.budgets, nil
}

func benchWebDB(b *testing.B) *storage.DB {
	b.Helper()
	db, err := storage.Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatalf("open db: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })
	return db
}

func seedSearchEndpointBench(b *testing.B, db *storage.DB, rows int) {
	b.Helper()
	now := time.Now().UTC()
	if err := db.UpsertSession("bench-search", event.PlatformClaude, now); err != nil {
		b.Fatalf("upsert search session: %v", err)
	}
	for i := 0; i < rows; i++ {
		params := fmt.Sprintf("Edit file %04d", i)
		if _, err := db.InsertToolCallStart(fmt.Sprintf("bench-search-call-%04d", i), "bench-agent", "bench-search", "Edit", params, now.Add(time.Duration(i)*time.Millisecond)); err != nil {
			b.Fatalf("insert search tool call %d: %v", i, err)
		}
	}
}

func seedExportEndpointBench(b *testing.B, db *storage.DB, rows int) {
	b.Helper()
	now := time.Now().UTC()
	for s := 0; s < 10; s++ {
		sessionID := fmt.Sprintf("bench-export-%02d", s)
		if err := db.UpsertSession(sessionID, event.PlatformClaude, now); err != nil {
			b.Fatalf("upsert export session %d: %v", s, err)
		}
		if err := db.UpdateSessionMeta(sessionID, fmt.Sprintf("/tmp/export-%02d", s), fmt.Sprintf("bench/export-%02d", s)); err != nil {
			b.Fatalf("update export meta %d: %v", s, err)
		}
	}
	for i := 0; i < rows; i++ {
		sessionID := fmt.Sprintf("bench-export-%02d", i%10)
		ts := now.Add(-time.Duration(i) * time.Second)
		if err := db.InsertTokenUsage("bench-agent", sessionID, 100+i%50, 25+i%20, 2, 3, "bench-model", 0.01, ts, fmt.Sprintf("bench-export-src-%04d", i)); err != nil {
			b.Fatalf("insert export usage %d: %v", i, err)
		}
	}
}

func seedCompareEndpointBench(b *testing.B, db *storage.DB, rowsPerSession int) {
	b.Helper()
	now := time.Now().UTC()
	for _, sessionID := range []string{"bench-compare-a", "bench-compare-b"} {
		if err := db.UpsertSession(sessionID, event.PlatformClaude, now); err != nil {
			b.Fatalf("upsert compare session %s: %v", sessionID, err)
		}
	}
	if err := db.InsertTokenUsage("bench-agent-a", "bench-compare-a", 5000, 1200, 0, 0, "bench-model", 1.25, now, "bench-compare-token-a"); err != nil {
		b.Fatalf("insert compare token a: %v", err)
	}
	if err := db.InsertTokenUsage("bench-agent-b", "bench-compare-b", 6200, 900, 0, 0, "bench-model", 1.45, now, "bench-compare-token-b"); err != nil {
		b.Fatalf("insert compare token b: %v", err)
	}

	toolNames := []string{"Read", "Edit", "Bash", "Grep"}
	for i := 0; i < rowsPerSession; i++ {
		for _, sessionID := range []string{"bench-compare-a", "bench-compare-b"} {
			callID := fmt.Sprintf("%s-call-%03d", sessionID, i)
			tool := toolNames[i%len(toolNames)]
			start := now.Add(time.Duration(i) * time.Millisecond)
			if _, err := db.InsertToolCallStart(callID, "bench-agent", sessionID, tool, fmt.Sprintf("%s params %03d", tool, i), start); err != nil {
				b.Fatalf("insert compare call %s: %v", callID, err)
			}
			if err := db.UpdateToolCallEnd(callID, "ok", event.StatusSuccess, int64(10+i%50), start.Add(time.Millisecond)); err != nil {
				b.Fatalf("end compare call %s: %v", callID, err)
			}
		}
	}
	for i := 0; i < 25; i++ {
		if err := db.InsertFileChange("bench-compare-a", fmt.Sprintf("/tmp/common-%02d.go", i), event.FileEdit, now); err != nil {
			b.Fatalf("insert common a file %d: %v", i, err)
		}
		if err := db.InsertFileChange("bench-compare-b", fmt.Sprintf("/tmp/common-%02d.go", i), event.FileEdit, now); err != nil {
			b.Fatalf("insert common b file %d: %v", i, err)
		}
		if err := db.InsertFileChange("bench-compare-a", fmt.Sprintf("/tmp/a-only-%02d.go", i), event.FileEdit, now); err != nil {
			b.Fatalf("insert a-only file %d: %v", i, err)
		}
		if err := db.InsertFileChange("bench-compare-b", fmt.Sprintf("/tmp/b-only-%02d.go", i), event.FileCreate, now); err != nil {
			b.Fatalf("insert b-only file %d: %v", i, err)
		}
	}
}

func seedBudgetEndpointBench(b *testing.B, db *storage.DB, count int) {
	b.Helper()
	now := time.Now().UTC().Add(-time.Minute)
	for _, platform := range []event.Platform{event.PlatformClaude, event.PlatformCodex} {
		sessionID := "bench-budget-" + string(platform)
		if err := db.UpsertSession(sessionID, platform, now); err != nil {
			b.Fatalf("upsert budget session %s: %v", platform, err)
		}
		if err := db.InsertTokenUsage("bench-agent", sessionID, 1000, 300, 0, 0, "bench-model", 2.5, now, "bench-budget-token-"+string(platform)); err != nil {
			b.Fatalf("insert budget usage %s: %v", platform, err)
		}
	}
	for i := 0; i < count; i++ {
		platform := ""
		if i%3 == 1 {
			platform = string(event.PlatformClaude)
		} else if i%3 == 2 {
			platform = string(event.PlatformCodex)
		}
		if _, err := db.InsertBudget(fmt.Sprintf("Bench Budget %02d", i), 100+float64(i), platform); err != nil {
			b.Fatalf("insert budget %d: %v", i, err)
		}
	}
}

func seedMetricsEndpointBench(b *testing.B, db *storage.DB) {
	b.Helper()
	now := time.Now().UTC().Add(-time.Minute)
	if err := db.UpsertSession("bench-metrics", event.PlatformClaude, now); err != nil {
		b.Fatalf("upsert metrics session: %v", err)
	}
	if err := db.InsertTokenUsage("bench-agent", "bench-metrics", 1200, 450, 0, 0, "bench-model", 0.75, now, "bench-metrics-token"); err != nil {
		b.Fatalf("insert metrics usage: %v", err)
	}
}

func benchBudgetMetrics(count int) []BudgetMetric {
	budgets := make([]BudgetMetric, 0, count)
	for i := 0; i < count; i++ {
		limit := 100 + float64(i)
		used := float64(i+1) * 3.5
		budgets = append(budgets, BudgetMetric{
			Name:     fmt.Sprintf("Bench Metric Budget %02d", i),
			Platform: string(event.PlatformClaude),
			UsedUSD:  used,
			LimitUSD: limit,
			Percent:  used / limit * 100,
		})
	}
	return budgets
}
