package storage

import (
	"math"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

func TestGetCostBetweenUsesDailyCostCacheForFullLocalDays(t *testing.T) {
	db := testDB(t)
	base := localDayStart(time.Now()).AddDate(0, 0, -2)
	day := base.Format("2006-01-02")

	if err := db.UpsertSession("s-cost-cache-full", event.PlatformClaude, base); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.InsertTokenUsage("a1", "s-cost-cache-full", 100, 50, 0, 0, "sonnet", 1.25, base.Add(10*time.Hour), "cost-cache-full-1"); err != nil {
		t.Fatalf("insert usage 1: %v", err)
	}
	if err := db.InsertTokenUsage("a1", "s-cost-cache-full", 100, 50, 0, 0, "sonnet", 2.25, base.AddDate(0, 0, 1).Add(10*time.Hour), "cost-cache-full-2"); err != nil {
		t.Fatalf("insert usage 2: %v", err)
	}

	// Force a recognizable cache value. The cache is maintained by writes in
	// production; this assertion locks the full-local-day fast path to the
	// cache instead of allowing a regression back to a token_usage scan.
	if _, err := db.db.Exec(`UPDATE daily_cost_cache SET cost_usd = cost_usd + 10 WHERE day = ?`, day); err != nil {
		t.Fatalf("adjust cache sentinel: %v", err)
	}

	got, err := db.GetCostBetween(base, base.AddDate(0, 0, 2))
	if err != nil {
		t.Fatalf("get cost between: %v", err)
	}
	want := 13.5
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("cost = %.12f, want %.12f from daily_cost_cache", got, want)
	}
}

func TestGetCostBetweenFastMatchesDirectTokenUsage(t *testing.T) {
	db := testDB(t)
	base := localDayStart(time.Now()).AddDate(0, 0, -4)
	if err := db.UpsertSession("s-cost-cache-match", event.PlatformClaude, base); err != nil {
		t.Fatalf("upsert session: %v", err)
	}

	events := []struct {
		at   time.Time
		cost float64
		src  string
	}{
		{base.Add(2 * time.Hour), 0.50, "match-1"},
		{base.Add(20 * time.Hour), 0.75, "match-2"},
		{base.AddDate(0, 0, 1).Add(12 * time.Hour), 1.25, "match-3"},
		{base.AddDate(0, 0, 2).Add(3 * time.Hour), 2.50, "match-4"},
		{base.AddDate(0, 0, 3).Add(8 * time.Hour), 4.00, "match-5"},
	}
	for _, ev := range events {
		if err := db.InsertTokenUsage("a1", "s-cost-cache-match", 100, 50, 0, 0, "sonnet", ev.cost, ev.at, ev.src); err != nil {
			t.Fatalf("insert usage %s: %v", ev.src, err)
		}
	}

	cases := []struct {
		name string
		from time.Time
		to   time.Time
	}{
		{name: "whole_days", from: base, to: base.AddDate(0, 0, 4)},
		{name: "partial_boundaries", from: base.Add(6 * time.Hour), to: base.AddDate(0, 0, 2).Add(18 * time.Hour)},
		{name: "same_day_partial", from: base.AddDate(0, 0, 1).Add(time.Hour), to: base.AddDate(0, 0, 1).Add(23 * time.Hour)},
		{name: "empty_reversed", from: base.AddDate(0, 0, 3), to: base.AddDate(0, 0, 2)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := directTokenUsageCostBetween(t, db, tc.from, tc.to)
			got, err := db.GetCostBetween(tc.from, tc.to)
			if err != nil {
				t.Fatalf("get cost between: %v", err)
			}
			if math.Abs(got-want) > 1e-9 {
				t.Fatalf("cost = %.12f, direct token_usage = %.12f", got, want)
			}
		})
	}
}

func TestGetCostBetweenPartialDayBoundary(t *testing.T) {
	db := testDB(t)
	base := localDayStart(time.Now()).AddDate(0, 0, -3)
	if err := db.UpsertSession("s-cost-cache-boundary", event.PlatformClaude, base); err != nil {
		t.Fatalf("upsert session: %v", err)
	}

	rows := []struct {
		at   time.Time
		cost float64
		src  string
	}{
		{base.Add(2 * time.Hour), 1, "boundary-before-from"},
		{base.Add(8 * time.Hour), 2, "boundary-after-from"},
		{base.AddDate(0, 0, 1).Add(12 * time.Hour), 3, "boundary-middle"},
		{base.AddDate(0, 0, 2).Add(4 * time.Hour), 4, "boundary-before-to"},
		{base.AddDate(0, 0, 2).Add(20 * time.Hour), 5, "boundary-after-to"},
	}
	for _, row := range rows {
		if err := db.InsertTokenUsage("a1", "s-cost-cache-boundary", 100, 50, 0, 0, "sonnet", row.cost, row.at, row.src); err != nil {
			t.Fatalf("insert usage %s: %v", row.src, err)
		}
	}

	from := base.Add(6 * time.Hour)
	to := base.AddDate(0, 0, 2).Add(6 * time.Hour)
	got, err := db.GetCostBetween(from, to)
	if err != nil {
		t.Fatalf("get cost between: %v", err)
	}
	if got != 9 {
		t.Fatalf("cost = %.2f, want 9.00", got)
	}
}

func directTokenUsageCostBetween(t *testing.T, db *DB, from, to time.Time) float64 {
	t.Helper()
	var cost float64
	if err := db.db.QueryRow(`
		SELECT COALESCE(SUM(cost_usd), 0)
		FROM token_usage WHERE timestamp >= ? AND timestamp < ?
	`, formatQueryTime(from), formatQueryTime(to)).Scan(&cost); err != nil {
		t.Fatalf("direct token_usage cost: %v", err)
	}
	return cost
}
