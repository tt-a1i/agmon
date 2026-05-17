package storage

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

func TestDailyCostCacheStaysConsistentWithTokenUsage(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	base := time.Date(now.Year(), now.Month(), now.Day(), 9, 0, 0, 0, time.Local).AddDate(0, 0, -2)

	if err := db.UpsertSession("s-cache-consistency", event.PlatformClaude, base); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	for i := 0; i < 9; i++ {
		ts := base.Add(time.Duration(i*9) * time.Hour)
		cost := float64(i+1) / 10
		if err := db.InsertTokenUsage("a1", "s-cache-consistency", 100+i, 50+i, 0, 0, "sonnet", cost, ts, "cache-consistency-"+ts.Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("insert token usage %d: %v", i, err)
		}
	}

	tokenCosts := dailyCostsFromTokenUsage(t, db)
	cacheCosts := dailyCostsFromCache(t, db)
	if len(cacheCosts) != len(tokenCosts) {
		t.Fatalf("cache day count = %d, token_usage day count = %d", len(cacheCosts), len(tokenCosts))
	}
	for day, want := range tokenCosts {
		got, ok := cacheCosts[day]
		if !ok {
			t.Fatalf("cache missing day %s", day)
		}
		if math.Abs(got-want) > 1e-9 {
			t.Fatalf("cache[%s] = %.12f, token_usage = %.12f", day, got, want)
		}
	}
}

func TestDailyCostCacheBackfillsOnUpgrade(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "upgrade.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 10, 0, 0, 0, time.Local)
	if err := db.UpsertSession("s-cache-upgrade", event.PlatformClaude, today); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.InsertTokenUsage("a1", "s-cache-upgrade", 100, 50, 0, 0, "sonnet", 1.25, today, "cache-upgrade-1"); err != nil {
		t.Fatalf("insert token usage 1: %v", err)
	}
	if err := db.InsertTokenUsage("a1", "s-cache-upgrade", 100, 50, 0, 0, "sonnet", 2.75, today.AddDate(0, 0, -1), "cache-upgrade-2"); err != nil {
		t.Fatalf("insert token usage 2: %v", err)
	}
	if _, err := db.db.Exec(`DELETE FROM daily_cost_cache`); err != nil {
		t.Fatalf("clear cache: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	db, err = Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db.Close()

	tokenCosts := dailyCostsFromTokenUsage(t, db)
	cacheCosts := dailyCostsFromCache(t, db)
	if len(cacheCosts) != len(tokenCosts) {
		t.Fatalf("cache day count = %d, token_usage day count = %d", len(cacheCosts), len(tokenCosts))
	}
	for day, want := range tokenCosts {
		if got := cacheCosts[day]; math.Abs(got-want) > 1e-9 {
			t.Fatalf("cache[%s] after backfill = %.12f, token_usage = %.12f", day, got, want)
		}
	}
}

func dailyCostsFromTokenUsage(t *testing.T, db *DB) map[string]float64 {
	t.Helper()
	rows, err := db.db.Query(`
		SELECT DATE(timestamp, 'localtime') AS day, SUM(cost_usd)
		FROM token_usage
		GROUP BY day
	`)
	if err != nil {
		t.Fatalf("query token_usage daily costs: %v", err)
	}
	defer rows.Close()
	return scanDailyCostMap(t, rows)
}

func dailyCostsFromCache(t *testing.T, db *DB) map[string]float64 {
	t.Helper()
	rows, err := db.db.Query(`SELECT day, cost_usd FROM daily_cost_cache`)
	if err != nil {
		t.Fatalf("query daily_cost_cache: %v", err)
	}
	defer rows.Close()
	return scanDailyCostMap(t, rows)
}

func scanDailyCostMap(t *testing.T, rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}) map[string]float64 {
	t.Helper()
	result := make(map[string]float64)
	for rows.Next() {
		var day string
		var cost float64
		if err := rows.Scan(&day, &cost); err != nil {
			t.Fatalf("scan daily cost: %v", err)
		}
		result[day] = cost
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("daily cost rows: %v", err)
	}
	return result
}
