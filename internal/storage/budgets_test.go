package storage

import (
	"math"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

func TestBudgetCRUD(t *testing.T) {
	db := testDB(t)

	id, err := db.InsertBudget("All platforms", 100, "")
	if err != nil {
		t.Fatalf("insert budget: %v", err)
	}
	if id == 0 {
		t.Fatal("insert budget returned id 0")
	}

	budgets, err := db.ListBudgets()
	if err != nil {
		t.Fatalf("list budgets: %v", err)
	}
	if len(budgets) != 1 {
		t.Fatalf("budgets len: got %d, want 1", len(budgets))
	}
	if budgets[0].Name != "All platforms" || budgets[0].MonthlyUSD != 100 || budgets[0].Platform != "" {
		t.Fatalf("budget row: got %#v", budgets[0])
	}
	if budgets[0].CreatedAt.IsZero() || budgets[0].UpdatedAt.IsZero() {
		t.Fatalf("budget timestamps should be set: %#v", budgets[0])
	}

	if err := db.UpdateBudget(id, "Claude", 50, string(event.PlatformClaude)); err != nil {
		t.Fatalf("update budget: %v", err)
	}
	budgets, err = db.ListBudgets()
	if err != nil {
		t.Fatalf("list updated budgets: %v", err)
	}
	if budgets[0].Name != "Claude" || budgets[0].MonthlyUSD != 50 || budgets[0].Platform != string(event.PlatformClaude) {
		t.Fatalf("updated budget row: got %#v", budgets[0])
	}

	if err := db.DeleteBudget(id); err != nil {
		t.Fatalf("delete budget: %v", err)
	}
	budgets, err = db.ListBudgets()
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(budgets) != 0 {
		t.Fatalf("budgets after delete: got %d, want 0", len(budgets))
	}
}

func TestGetBudgetUsageWithPlatformFilter(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	thisMonth := now.Add(-time.Hour)
	if thisMonth.Month() != now.Month() {
		thisMonth = now
	}
	lastMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local).Add(-time.Hour)

	if err := db.UpsertSession("claude-current", event.PlatformClaude, thisMonth); err != nil {
		t.Fatalf("upsert claude current: %v", err)
	}
	if err := db.InsertTokenUsage("a1", "claude-current", 100, 50, 0, 0, "sonnet", 30, thisMonth, "claude-current"); err != nil {
		t.Fatalf("insert claude current: %v", err)
	}
	if err := db.UpsertSession("codex-current", event.PlatformCodex, thisMonth); err != nil {
		t.Fatalf("upsert codex current: %v", err)
	}
	if err := db.InsertTokenUsage("a2", "codex-current", 100, 50, 0, 0, "gpt-5", 20, thisMonth, "codex-current"); err != nil {
		t.Fatalf("insert codex current: %v", err)
	}
	if err := db.UpsertSession("claude-old", event.PlatformClaude, lastMonth); err != nil {
		t.Fatalf("upsert claude old: %v", err)
	}
	if err := db.InsertTokenUsage("a3", "claude-old", 100, 50, 0, 0, "sonnet", 99, lastMonth, "claude-old"); err != nil {
		t.Fatalf("insert claude old: %v", err)
	}

	allID, err := db.InsertBudget("All", 100, "")
	if err != nil {
		t.Fatalf("insert all budget: %v", err)
	}
	claudeID, err := db.InsertBudget("Claude", 80, string(event.PlatformClaude))
	if err != nil {
		t.Fatalf("insert claude budget: %v", err)
	}

	used, limit, err := db.GetBudgetUsage(allID)
	if err != nil {
		t.Fatalf("get all usage: %v", err)
	}
	if !within(used, 50) || !within(limit, 100) {
		t.Fatalf("all usage: used=%f limit=%f, want 50/100", used, limit)
	}

	used, limit, err = db.GetBudgetUsage(claudeID)
	if err != nil {
		t.Fatalf("get claude usage: %v", err)
	}
	if !within(used, 30) || !within(limit, 80) {
		t.Fatalf("claude usage: used=%f limit=%f, want 30/80", used, limit)
	}
}

func within(got, want float64) bool {
	return math.Abs(got-want) < 0.000001
}
