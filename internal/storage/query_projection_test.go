package storage

import (
	"math"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

func TestGetMonthCostProjectionEarlyMonth(t *testing.T) {
	db := testDB(t)
	now := time.Date(2026, time.January, 2, 12, 0, 0, 0, time.Local)
	if err := db.UpsertSession("projection-early", event.PlatformClaude, now.Add(-time.Hour)); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.InsertTokenUsage("agent-early", "projection-early", 1, 1, 0, 0, "sonnet", 20, now.Add(-time.Hour), "projection-early"); err != nil {
		t.Fatalf("insert usage: %v", err)
	}

	p, err := db.GetMonthCostProjection(now)
	if err != nil {
		t.Fatalf("projection: %v", err)
	}
	if p.UsedSoFar != 20 {
		t.Fatalf("used: got %.2f, want 20", p.UsedSoFar)
	}
	if p.DaysElapsed != 2 || p.DaysInMonth != 31 {
		t.Fatalf("days: got elapsed=%d in_month=%d", p.DaysElapsed, p.DaysInMonth)
	}
	if p.Confidence != "low" {
		t.Fatalf("confidence: got %q, want low", p.Confidence)
	}
	if math.Abs(p.ProjectedTotal-310) > 0.001 {
		t.Fatalf("projected: got %.4f, want 310", p.ProjectedTotal)
	}
}

func TestGetMonthCostProjectionMidMonth(t *testing.T) {
	db := testDB(t)
	now := time.Date(2026, time.April, 15, 12, 0, 0, 0, time.Local)
	if err := db.UpsertSession("projection-mid", event.PlatformClaude, now.AddDate(0, 0, -10)); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := db.InsertTokenUsage("agent-mid", "projection-mid", 1, 1, 0, 0, "sonnet", 150, now.AddDate(0, 0, -10), "projection-mid"); err != nil {
		t.Fatalf("insert usage: %v", err)
	}

	p, err := db.GetMonthCostProjection(now)
	if err != nil {
		t.Fatalf("projection: %v", err)
	}
	if p.Confidence != "high" {
		t.Fatalf("confidence: got %q, want high", p.Confidence)
	}
	if math.Abs(p.ProjectedTotal-300) > 0.001 {
		t.Fatalf("projected: got %.4f, want 300", p.ProjectedTotal)
	}
}

func TestGetMonthCostProjectionEmpty(t *testing.T) {
	db := testDB(t)
	now := time.Date(2026, time.May, 20, 12, 0, 0, 0, time.Local)

	p, err := db.GetMonthCostProjection(now)
	if err != nil {
		t.Fatalf("projection: %v", err)
	}
	if p.UsedSoFar != 0 || p.AvgDailyCost != 0 || p.ProjectedTotal != 0 {
		t.Fatalf("empty projection should be zero, got %#v", p)
	}
	if p.Confidence != "high" {
		t.Fatalf("confidence: got %q, want high", p.Confidence)
	}
}
